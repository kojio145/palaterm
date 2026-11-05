package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/kojio145/palaterm/internal/history"
)

// GrepWin is the backend of the grep window ("PalaTerm.exe --grep <root>
// [dir]"): one search over every log file under the log folder (or one run
// folder of it), the hits listed by file the way an editor's grep does, in
// its own OS window so the run tab and the terminals stay usable beside it.
// Like the viewer it reads only files under the root it was started with
// (the parent passes its log folder) and needs no vault.
type GrepWin struct {
	ctx   context.Context
	root  string
	dir   string
	title string
}

// grepInit is what the page starts from.
type grepInit struct {
	Root string `json:"root"`
	// Dir is the run folder the window was opened for ("" = the whole log
	// folder); DirName its folder name.
	Dir     string       `json:"dir,omitempty"`
	DirName string       `json:"dirName,omitempty"`
	Folders []grepFolder `json:"folders"`
}

// grepFolder is one run folder under the root (the 範囲 choices).
type grepFolder struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
}

func NewGrepWin(root, dir, title string) *GrepWin {
	return &GrepWin{root: root, dir: dir, title: title}
}

func (g *GrepWin) startup(ctx context.Context) {
	g.ctx = ctx
	grepWin.fit(ctx)
	placeNearParent(ctx, grepWin)
}

func (g *GrepWin) domReady(ctx context.Context) { go focusOwnWindow() }

// Load is the window's starting point: the root, the folder it was opened
// for, and the run folders under the root (newest first by name).
func (g *GrepWin) Load() grepInit {
	init := grepInit{Root: g.root, Dir: g.dir, Folders: []grepFolder{}}
	if g.dir != "" {
		init.DirName = filepath.Base(g.dir)
	}
	entries, err := os.ReadDir(g.root)
	if err != nil {
		return init
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		init.Folders = append(init.Folders, grepFolder{Dir: filepath.Join(g.root, e.Name()), Name: e.Name()})
	}
	sort.Slice(init.Folders, func(i, j int) bool { return init.Folders[i].Name > init.Folders[j].Name })
	return init
}

// grepTimeout bounds one search (a log folder of years on a slow share).
const grepTimeout = 120 * time.Second

// Search runs one search. The root is always this window's; a folder must
// lie under it.
func (g *GrepWin) Search(req history.GrepRequest) (*history.GrepResult, error) {
	req.Root = g.root
	if req.Dir != "" {
		if err := underRoot(g.root, req.Dir, "ログフォルダの外は検索できません"); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), grepTimeout)
	defer cancel()
	return history.Grep(ctx, req)
}

// OpenLog opens a hit's file in a log viewer window, at that line.
func (g *GrepWin) OpenLog(path string, line int) error {
	if err := underRoot(g.root, path, "ログフォルダの外は開けません"); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	var env []string
	if line > 0 {
		env = []string{viewLineEnv + "=" + strconv.Itoa(line)}
	}
	return spawnWindow(env, "--view", path)
}

// OpenFolder shows a file in Explorer (or the folder searched).
func (g *GrepWin) OpenFolder(path string) error {
	if path == "" {
		path = g.root
		if g.dir != "" {
			path = g.dir
		}
	}
	if err := underRoot(g.root, path, "ログフォルダの外は開けません"); err != nil {
		return err
	}
	return openInExplorer(path)
}

// SaveText writes the result's text to export\grep\grep_<date>_<time>.txt
// beside the exe and returns the path.
func (g *GrepWin) SaveText(text string) (string, error) {
	dir := filepath.Join(exeDir(), "export", "grep")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "grep_"+time.Now().Format("20060102_150405")+".txt")
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(text, "\n", "\r\n")), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// Close ends the window (Esc).
func (g *GrepWin) Close() { runtime.Quit(g.ctx) }

// viewLineEnv carries the line a viewer window opens at (OpenLog).
const viewLineEnv = "PALATERM_VIEW_LINE"

// runGrepWindow is the "--grep" process.
func runGrepWindow(root, dir string) {
	where := filepath.Base(root)
	if dir != "" {
		where = filepath.Base(dir)
	}
	title := fmt.Sprintf("PalaTerm - %s - %s", winWord("ログの grep", "Grep logs"), where)
	g := NewGrepWin(root, dir, title)
	runChildWindow(childWindow{title: title, size: grepWin, bg: panelBG, bind: g, startup: g.startup, domReady: g.domReady})
}
