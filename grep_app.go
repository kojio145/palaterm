package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	cc    childCtx // for the parent's CLOSE (watchParent)
	// kids are the log windows opened from here, by file (kidKey): a hit in
	// a file whose window is still open goes to that window (LINE) instead
	// of opening another.
	kidsMu sync.Mutex
	kids   map[string]*viewerKid
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
	g.cc.set(ctx)
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

// OpenLog opens a hit's file in a log viewer window, at that line. A window
// of the same file still open shows the line instead (and comes to the
// front): every click used to open one more window.
func (g *GrepWin) OpenLog(path string, line int) error {
	if err := underRoot(g.root, path, "ログフォルダの外は開けません"); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	key := kidKey(path)
	if k := g.kidFor(key); k != nil && k.send(kidLineGoto+" "+strconv.Itoa(line)) == nil {
		return nil
	}
	var env []string
	if line > 0 {
		env = []string{viewLineEnv + "=" + strconv.Itoa(line)}
	}
	k, err := spawnKid(env, "--view", path)
	if err != nil {
		return err
	}
	g.kidsMu.Lock()
	if g.kids == nil {
		g.kids = map[string]*viewerKid{}
	}
	g.kids[key] = k
	g.kidsMu.Unlock()
	return nil
}

// kidFor is the log window open for key, nil when there is none (or it has
// ended, which forgets it).
func (g *GrepWin) kidFor(key string) *viewerKid {
	g.kidsMu.Lock()
	defer g.kidsMu.Unlock()
	k := g.kids[key]
	if k == nil {
		return nil
	}
	if !k.alive() {
		delete(g.kids, key)
		return nil
	}
	return k
}

// kidKey is a file's key among the open log windows: the path as Windows
// compares it (case does not matter).
func kidKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
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
		return "", saveTextErr(dir, err)
	}
	p := filepath.Join(dir, "grep_"+time.Now().Format("20060102_150405")+".txt")
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(text, "\n", "\r\n")), 0o644); err != nil {
		return "", saveTextErr(dir, err)
	}
	return p, nil
}

// saveTextErr says the folder the result could not be written to and what
// to do (the folder beside the exe may be read-only: Program Files, a
// share): the bare OS error gave no way on.
func saveTextErr(dir string, err error) error {
	return fmt.Errorf("%s に書けません（%v）。書き込める場所に PalaTerm を置くか、「結果をコピー」で貼り付けてください", dir, err)
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
	watchParent(g.cc.quit, nil)
	runChildWindow(childWindow{title: title, size: grepWin, bg: panelBG, bind: g, startup: g.startup, domReady: g.domReady})
}
