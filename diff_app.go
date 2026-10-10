package main

import (
	"context"
	"os"
	"path/filepath"

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
}

func NewDiffWin(a, b, label, title string) *DiffWin {
	return &DiffWin{a: a, b: b, label: label, title: title}
}

func (d *DiffWin) startup(ctx context.Context) { d.ctx = ctx }

func (d *DiffWin) domReady(ctx context.Context) { go focusOwnWindow() }

// Load compares the two files (re-run when the noise option changes).
func (d *DiffWin) Load(ignoreNoise bool) diffDoc {
	doc := diffDoc{PathA: d.a, PathB: d.b, NameA: filepath.Base(d.a), NameB: filepath.Base(d.b), Label: d.label}
	ba, err := os.ReadFile(d.a)
	if err != nil {
		doc.Err = err.Error()
		return doc
	}
	bb, err := os.ReadFile(d.b)
	if err != nil {
		doc.Err = err.Error()
		return doc
	}
	doc.Result = history.Diff(string(ba), string(bb), ignoreNoise)
	if doc.Result.Lines == nil {
		doc.Result.Lines = []history.DiffLine{}
	}
	return doc
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
