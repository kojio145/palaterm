package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/kojio145/palaterm/internal/guard"
	"github.com/kojio145/palaterm/internal/model"
)

// fakeCmdsetPipe stands in for a set window's stdin.
type fakeCmdsetPipe struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
}

func (f *fakeCmdsetPipe) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	return f.buf.Write(p)
}

func (f *fakeCmdsetPipe) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// msgs decodes every line written so far.
func (f *fakeCmdsetPipe) msgs(t *testing.T) []cmdsetMsg {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cmdsetMsg
	for _, l := range strings.Split(strings.TrimSpace(f.buf.String()), "\n") {
		if l == "" {
			continue
		}
		var m cmdsetMsg
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad line %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func (f *fakeCmdsetPipe) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// withFakeCmdsetProcs replaces the window process with fakes for one test.
func withFakeCmdsetProcs(t *testing.T) *[]*fakeCmdsetPipe {
	t.Helper()
	var started []*fakeCmdsetPipe
	old := startCmdsetProc
	startCmdsetProc = func() (io.WriteCloser, int, <-chan struct{}, error) {
		p := &fakeCmdsetPipe{}
		started = append(started, p)
		return p, 0, make(chan struct{}), nil
	}
	t.Cleanup(func() {
		startCmdsetProc = old
		closeCmdsetWindows()
	})
	return &started
}

func TestCmdsetDocOfMarksChanges(t *testing.T) {
	cs := model.CommandSet{Name: "lab-vlan-add", Commands: []model.Command{
		{Text: "show clock", PauseSec: 1, SerialSec: 2},
		{Text: "configure terminal", Wait: "(config)#", PauseSec: 1, SerialSec: 1},
		{Text: "vlan 10", PauseSec: 0, SerialSec: 0},
		{Text: "write memory", PauseSec: 3, SerialSec: 3},
	}}
	d := cmdsetDocOf(cs)
	if d.Name != "lab-vlan-add" || len(d.Commands) != 4 {
		t.Fatalf("doc = %+v", d)
	}
	want := []string{"", guard.ReasonConfigMode, guard.ReasonInterface, guard.ReasonWrite}
	for i, w := range want {
		if d.Commands[i].Danger != w {
			t.Errorf("line %d danger = %q, want %q", i+1, d.Commands[i].Danger, w)
		}
	}
	if d.Commands[1].Wait != "(config)#" || d.Commands[0].SerialSec != 2 || d.Commands[3].PauseSec != 3 {
		t.Fatalf("wait / pauses not kept: %+v", d.Commands)
	}
}

func TestCmdsetWindowLifecycle(t *testing.T) {
	started := withFakeCmdsetProcs(t)
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.OpenCommandSetWindow("health-check"); err == nil {
		t.Fatal("a set that does not exist must not open")
	}
	mustSave := func(cs model.CommandSet) {
		t.Helper()
		if err := a.SaveCommandSet(cs); err != nil {
			t.Fatal(err)
		}
	}
	mustSave(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show clock", PauseSec: 1, SerialSec: 1}}})
	mustSave(model.CommandSet{Name: "config-backup", Commands: []model.Command{{Text: "show running-config", PauseSec: 1, SerialSec: 1}}})

	// Opened: the set goes on stdin (never in the arguments).
	if err := a.OpenCommandSetWindow("health-check"); err != nil {
		t.Fatal(err)
	}
	if len(*started) != 1 {
		t.Fatalf("started %d windows, want 1", len(*started))
	}
	w1 := (*started)[0]
	m := w1.msgs(t)
	if len(m) != 1 || m[0].Kind != "show" || m[0].Doc == nil || m[0].Doc.Name != "health-check" ||
		!strings.Contains(m[0].Title, "health-check") || m[0].Doc.Commands[0].Text != "show clock" {
		t.Fatalf("first line = %+v", m)
	}

	// The same set again: no second window, the first comes to the front.
	if err := a.OpenCommandSetWindow("health-check"); err != nil {
		t.Fatal(err)
	}
	if len(*started) != 1 {
		t.Fatalf("a second window was started for the same set")
	}
	if m = w1.msgs(t); m[len(m)-1].Kind != "focus" {
		t.Fatalf("no focus request: %+v", m)
	}

	// Another set: a window of its own.
	if err := a.OpenCommandSetWindow("config-backup"); err != nil {
		t.Fatal(err)
	}
	if len(*started) != 2 {
		t.Fatalf("started %d windows, want 2", len(*started))
	}
	w2 := (*started)[1]

	// Saving the set again updates its window, and only its window.
	n1, n2 := len(w1.msgs(t)), len(w2.msgs(t))
	mustSave(model.CommandSet{Name: "health-check", Commands: []model.Command{
		{Text: "show clock", PauseSec: 1, SerialSec: 1}, {Text: "configure terminal", PauseSec: 1, SerialSec: 1}}})
	m = w1.msgs(t)
	if len(m) != n1+1 || m[n1].Kind != "show" || len(m[n1].Doc.Commands) != 2 || m[n1].Doc.Commands[1].Danger != guard.ReasonConfigMode {
		t.Fatalf("update not sent: %+v", m[n1:])
	}
	if len(w2.msgs(t)) != n2 {
		t.Fatalf("the other window got a line it did not need")
	}
	// Saved unchanged: nothing resent.
	mustSave(model.CommandSet{Name: "health-check", Commands: []model.Command{
		{Text: "show clock", PauseSec: 1, SerialSec: 1}, {Text: "configure terminal", PauseSec: 1, SerialSec: 1}}})
	if len(w1.msgs(t)) != n1+1 {
		t.Fatalf("an unchanged set was sent again")
	}

	// Renamed: the window follows the new name (title and set).
	if err := a.RenameCommandSet("health-check", "health-check-2"); err != nil {
		t.Fatal(err)
	}
	m = w1.msgs(t)
	last := m[len(m)-1]
	if last.Kind != "show" || last.Doc.Name != "health-check-2" || !strings.Contains(last.Title, "health-check-2") {
		t.Fatalf("rename not followed: %+v", last)
	}
	// …and opening it under the new name finds the same window.
	if err := a.OpenCommandSetWindow("health-check-2"); err != nil {
		t.Fatal(err)
	}
	if len(*started) != 2 {
		t.Fatalf("the renamed set opened a new window")
	}

	// Deleted: the window is told, and stays.
	if err := a.DeleteCommandSet("config-backup"); err != nil {
		t.Fatal(err)
	}
	m = w2.msgs(t)
	if m[len(m)-1].Kind != "deleted" || w2.isClosed() {
		t.Fatalf("delete not told: %+v closed=%v", m[len(m)-1], w2.isClosed())
	}

	// Locked: every window's pipe is closed (the window closes itself).
	a.Lock()
	if !w1.isClosed() || !w2.isClosed() {
		t.Fatalf("lock left a set window open")
	}
	if err := a.OpenCommandSetWindow("health-check-2"); err == nil {
		t.Fatal("opened while locked")
	}
}

func TestCmdsetWinApply(t *testing.T) {
	c := &CmdsetWin{}
	d := cmdsetDoc{Name: "lab-vlan-add", Commands: []cmdsetLine{{Text: "show vlan"}}}
	if r, f := c.apply(cmdsetMsg{Kind: "show", Title: "T1", Doc: &d}); !r || f {
		t.Fatal("show must redraw")
	}
	if st := c.Load(); st.Doc.Name != "lab-vlan-add" || st.Deleted || c.title != "T1" {
		t.Fatalf("state = %+v", st)
	}
	if r, _ := c.apply(cmdsetMsg{Kind: "deleted"}); !r || !c.Load().Deleted {
		t.Fatal("deleted not kept")
	}
	// What it showed stays readable after the delete.
	if c.Load().Doc.Commands[0].Text != "show vlan" {
		t.Fatal("the commands went with the delete")
	}
	if _, f := c.apply(cmdsetMsg{Kind: "focus"}); !f {
		t.Fatal("focus not asked")
	}
	if r, f := c.apply(cmdsetMsg{Kind: "show"}); r || f {
		t.Fatal("a show without a set must be ignored")
	}
}
