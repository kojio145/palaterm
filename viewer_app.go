package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kojio145/palaterm/internal/history"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Viewer is the backend of the log-viewer window, a separate process
// ("PalaTerm.exe --view <file>") so a log opens beside the main window or
// the terminal instead of as a dialog on top of it, like the paste
// confirmation. It only reads the one file it was started with — the parent
// checks that file lies under the log folder before starting it. No vault,
// no password.
type Viewer struct {
	ctx   context.Context
	path  string
	title string
	live  bool
}

// viewerDoc is what the page shows.
type viewerDoc struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Text string `json:"text"`
	Err  string `json:"err,omitempty"`
	// Live says the file is still being written (opened from a running
	// interactive session), so the page starts in follow mode.
	Live bool `json:"live,omitempty"`
}

func NewViewer(path, title string) *Viewer {
	return &Viewer{path: path, title: title, live: os.Getenv("PALATERM_VIEW_LIVE") == "1"}
}

// openInExplorer shows a file selected in Explorer (or opens a folder).
func openInExplorer(path string) error {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return exec.Command("explorer", path).Start()
	}
	return exec.Command("explorer", "/select,", path).Start()
}

// spawnWindow starts another PalaTerm window process detached from this one.
func spawnWindow(env []string, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (v *Viewer) startup(ctx context.Context) { v.ctx = ctx; viewerWin.fit(ctx) }

func (v *Viewer) domReady(ctx context.Context) { go focusOwnWindow() }

// Load reads the file (also used by the page's reload / follow timer, so a
// log that is still being written — an interactive session — stays current).
func (v *Viewer) Load() viewerDoc {
	d := viewerDoc{Path: v.path, Name: filepath.Base(v.path)}
	b, err := os.ReadFile(v.path)
	if err != nil {
		d.Err = err.Error()
		return d
	}
	// Shown as it was on screen: an interactive capture keeps the device's
	// line editing ("\b \b" from tab completion) and colour escapes, which
	// read as garbage in a text view. The file itself is left as captured.
	d.Text = history.CleanTranscript(string(b))
	d.Live = v.live
	return d
}

// OpenFolder shows the file in Explorer.
func (v *Viewer) OpenFolder() error { return openInExplorer(v.path) }

// Close ends the viewer (Esc).
func (v *Viewer) Close() { runtime.Quit(v.ctx) }
