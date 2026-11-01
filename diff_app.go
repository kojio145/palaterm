package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/kojio145/palaterm/internal/history"
)

// DiffWin is the backend of the diff window ("PalaTerm.exe --diff <A> <B>
// [label]"): two logs side by side, WinMerge style, in its own OS window.
// Like the viewer it only reads the two files it was started with (the
// parent checks both lie under the log folder) and needs no vault.
type DiffWin struct {
	ctx   context.Context
	a, b  string
	label string
	title string
}

// diffDoc is what the page renders.
type diffDoc struct {
	PathA  string             `json:"pathA"`
	PathB  string             `json:"pathB"`
	NameA  string             `json:"nameA"`
	NameB  string             `json:"nameB"`
	Label  string             `json:"label"`
	Result history.DiffResult `json:"result"`
	Err    string             `json:"err,omitempty"`
	// Writing: a log still being written (a device still running): what
	// is compared is what was there when the window read it.
	Writing bool `json:"writing,omitempty"`
}

func NewDiffWin(a, b, label, title string) *DiffWin {
	return &DiffWin{a: a, b: b, label: label, title: title}
}

func (d *DiffWin) startup(ctx context.Context) {
	d.ctx = ctx
	diffWin.fit(ctx)
	placeNearParent(ctx, diffWin)
}

func (d *DiffWin) domReady(ctx context.Context) { go focusOwnWindow() }

// Load compares the two files (re-run when an option changes).
func (d *DiffWin) Load(opts history.DiffOpts) diffDoc {
	doc := diffDoc{PathA: d.a, PathB: d.b, NameA: filepath.Base(d.a), NameB: filepath.Base(d.b), Label: d.label}
	ba, wa, err := readWatching(d.a)
	if err != nil {
		doc.Err = err.Error()
		return doc
	}
	bb, wb, err := readWatching(d.b)
	if err != nil {
		doc.Err = err.Error()
		return doc
	}
	doc.Writing = wa || wb
	doc.Result = history.DiffWith(string(ba), string(bb), opts)
	return doc
}

// readWatching reads a file and says whether it is still being written:
// its size changed while it was read, or it was last written within the
// second.
func readWatching(path string) ([]byte, bool, error) {
	before, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	after, err := os.Stat(path)
	if err != nil {
		return data, false, nil
	}
	writing := after.Size() != before.Size() || time.Since(after.ModTime()) < time.Second
	return data, writing, nil
}

// Commands makes the rollback (B → A) or forward (A → B) commands for the
// two logs (history.GenerateCommands); the window shows them for copying.
func (d *DiffWin) Commands(direction string) history.CmdGen {
	ba, err := os.ReadFile(d.a)
	if err != nil {
		return history.CmdGen{Warnings: []string{err.Error()}, Empty: true}
	}
	bb, err := os.ReadFile(d.b)
	if err != nil {
		return history.CmdGen{Warnings: []string{err.Error()}, Empty: true}
	}
	if direction != "forward" {
		direction = "rollback"
	}
	g := history.GenerateCommands(string(ba), string(bb), direction, "")
	if g.Lines == nil {
		g.Lines = []history.CmdLine{}
	}
	return g
}

// OpenLog opens side "a" or "b" in a viewer window.
func (d *DiffWin) OpenLog(side string) error {
	p := d.a
	if side == "b" {
		p = d.b
	}
	return spawnWindow(nil, "--view", p)
}

// Close ends the window (Esc).
func (d *DiffWin) Close() { runtime.Quit(d.ctx) }
