package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/guard"
	"github.com/kojio145/palaterm/internal/model"
)

// fakeCmdsetPipe stands in for a set window's stdin. Lines reach it from the
// window's writer goroutine, so tests wait for them (awaitMsgs).
type fakeCmdsetPipe struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
	// block, when set, holds every Write until it is closed: a child that
	// does not read its pipe.
	block chan struct{}
}

func (f *fakeCmdsetPipe) Write(p []byte) (int, error) {
	f.mu.Lock()
	block := f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
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

// awaitMsgs waits until n lines have been written and returns them.
func (f *fakeCmdsetPipe) awaitMsgs(t *testing.T, n int) []cmdsetMsg {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		m := f.msgs(t)
		if len(m) >= n || time.Now().After(deadline) {
			if len(m) < n {
				t.Fatalf("want %d lines, got %d: %+v", n, len(m), m)
			}
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settled is msgs after a moment with nothing more arriving (for "nothing
// was sent" checks).
func (f *fakeCmdsetPipe) settled(t *testing.T) []cmdsetMsg {
	t.Helper()
	time.Sleep(40 * time.Millisecond)
	return f.msgs(t)
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
	old, oldPrewarm := startCmdsetProc, cmdsetPrewarmOn
	cmdsetPrewarmOn = false
	startCmdsetProc = func(spare bool) (io.WriteCloser, int, <-chan struct{}, error) {
		if spare {
			t.Error("a spare window was started with the spare turned off")
		}
		p := &fakeCmdsetPipe{}
		started = append(started, p)
		return p, 0, make(chan struct{}), nil
	}
	t.Cleanup(func() {
		startCmdsetProc, cmdsetPrewarmOn = old, oldPrewarm
		closeCmdsetWindows()
	})
	return &started
}

// The spare window: started hidden ahead of the click (only while the vault
// is open and the Command sets tab is shown), handed the set — with where
// the main window is — by View, which starts the next spare; dropped when
// the tab is left; closed with the windows on ロック.
func TestCmdsetSpareWindow(t *testing.T) {
	type proc struct {
		p     *fakeCmdsetPipe
		spare bool
	}
	var procs []proc
	old, oldPrewarm, oldAfter := startCmdsetProc, cmdsetPrewarmOn, cmdsetRespareAfter
	cmdsetPrewarmOn, cmdsetRespareAfter = true, 0
	startCmdsetProc = func(spare bool) (io.WriteCloser, int, <-chan struct{}, error) {
		p := &fakeCmdsetPipe{}
		procs = append(procs, proc{p, spare})
		return p, 0, make(chan struct{}), nil
	}
	t.Cleanup(func() {
		startCmdsetProc, cmdsetPrewarmOn, cmdsetRespareAfter = old, oldPrewarm, oldAfter
		closeCmdsetWindows()
		cmdsetSpareWanted = false
	})
	a := newTestApp(t)
	a.PrewarmCommandSetWindow()
	if len(procs) != 0 {
		t.Fatal("a spare window was started while the vault is locked")
	}
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show clock"}}}); err != nil {
		t.Fatal(err)
	}
	a.PrewarmCommandSetWindow()
	a.PrewarmCommandSetWindow()
	if len(procs) != 1 || !procs[0].spare {
		t.Fatalf("want one spare after two calls, got %+v", procs)
	}
	if m := procs[0].p.settled(t); len(m) != 0 {
		t.Fatalf("the spare got lines before View: %+v", m)
	}
	if err := a.OpenCommandSetWindow("health-check"); err != nil {
		t.Fatal(err)
	}
	// View used the spare (no visible window started) and started the next.
	if len(procs) != 2 || !procs[1].spare {
		t.Fatalf("View should use the spare and start the next one: %+v", procs)
	}
	m := procs[0].p.awaitMsgs(t, 1)
	if len(m) != 1 || m[0].Kind != "show" || m[0].Doc == nil || m[0].Doc.Name != "health-check" || !strings.Contains(m[0].Title, "health-check") {
		t.Fatalf("the spare's first line = %+v", m)
	}
	// Same set again: the window that was the spare comes to the front.
	if err := a.OpenCommandSetWindow("health-check"); err != nil {
		t.Fatal(err)
	}
	if len(procs) != 2 {
		t.Fatalf("the same set started another window: %d", len(procs))
	}
	if m := procs[0].p.awaitMsgs(t, 2); m[len(m)-1].Kind != "focus" {
		t.Fatalf("want focus, got %+v", m)
	}
	// Leaving the tab drops the spare; no new one starts after a View (the
	// respare) until the tab asks again.
	a.DropSpareCommandSetWindow()
	if !procs[1].p.isClosed() {
		t.Fatal("leaving the tab must close the spare")
	}
	if err := a.OpenCommandSetWindow("health-check"); err != nil {
		t.Fatal(err)
	}
	if len(procs) != 2 {
		t.Fatalf("a spare was started after the tab was left: %+v", procs)
	}
	a.PrewarmCommandSetWindow()
	if len(procs) != 3 || !procs[2].spare {
		t.Fatalf("showing the tab again must start a spare: %+v", procs)
	}
	a.Lock()
	if !procs[0].p.isClosed() || !procs[2].p.isClosed() {
		t.Fatal("ロック must close the set window and the spare")
	}
}

// Two clicks on the same set a moment apart (a double click on the preview)
// open one window, even while the first is still starting its process.
func TestCmdsetWindowOpenedTwiceAtOnce(t *testing.T) {
	var mu sync.Mutex
	var started []*fakeCmdsetPipe
	release := make(chan struct{})
	old, oldPrewarm := startCmdsetProc, cmdsetPrewarmOn
	cmdsetPrewarmOn = false
	startCmdsetProc = func(spare bool) (io.WriteCloser, int, <-chan struct{}, error) {
		<-release // a slow process start
		p := &fakeCmdsetPipe{}
		mu.Lock()
		started = append(started, p)
		mu.Unlock()
		return p, 0, make(chan struct{}), nil
	}
	t.Cleanup(func() {
		startCmdsetProc, cmdsetPrewarmOn = old, oldPrewarm
		closeCmdsetWindows()
	})
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show clock"}}}); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- a.OpenCommandSetWindow("health-check") }()
	}
	time.Sleep(50 * time.Millisecond) // both clicks in before the process is up
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	n := len(started)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("started %d windows for one set, want 1", n)
	}
	m := started[0].awaitMsgs(t, 1)
	if m[0].Kind != "show" || m[0].Doc == nil || m[0].Doc.Name != "health-check" {
		t.Fatalf("first line = %+v", m)
	}
	a.Lock()
	if !started[0].isClosed() {
		t.Fatal("ロック left the window open")
	}
}

// A window that does not read its pipe (its WebView2 still starting, or
// hung) must not hold up a save in the main window: lines are queued, only
// the newest set is kept for it, and ロック still closes it.
func TestCmdsetWindowSlowChildDoesNotBlockSave(t *testing.T) {
	started := withFakeCmdsetProcs(t)
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show clock"}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.OpenCommandSetWindow("health-check"); err != nil {
		t.Fatal(err)
	}
	w := (*started)[0]
	w.awaitMsgs(t, 1)
	block := make(chan struct{})
	w.mu.Lock()
	w.block = block
	w.mu.Unlock()
	// Three saves while the child reads nothing: each must return at once.
	for i, text := range []string{"show version", "show inventory", "show interfaces"} {
		done := make(chan error, 1)
		go func() {
			done <- a.SaveCommandSet(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: text}}})
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("save %d blocked on a window that does not read", i+1)
		}
	}
	if err := a.OpenCommandSetWindow("health-check"); err != nil {
		t.Fatal(err)
	}
	close(block)
	// The child catches up: the line that was already in the pipe write, then
	// the newest set (the one between was replaced in the queue) and the focus.
	time.Sleep(60 * time.Millisecond)
	m := w.msgs(t)
	var shows []string
	for _, x := range m[1:] {
		if x.Kind == "show" {
			shows = append(shows, x.Doc.Commands[0].Text)
		}
	}
	if len(shows) == 0 || len(shows) > 2 || shows[len(shows)-1] != "show interfaces" || strings.Join(shows, " ") == "show version show inventory show interfaces" {
		t.Fatalf("want the newest set last and the one between dropped, got shows %v (all %+v)", shows, m)
	}
	if m[len(m)-1].Kind != "focus" && m[len(m)-2].Kind != "focus" {
		t.Fatalf("focus not sent: %+v", m)
	}
	a.Lock()
	if !w.isClosed() {
		t.Fatal("ロック left the window open")
	}
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
	m := w1.awaitMsgs(t, 1)
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
	if m = w1.awaitMsgs(t, 2); m[len(m)-1].Kind != "focus" {
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
	w2.awaitMsgs(t, 1)

	// Saving the set again updates its window, and only its window.
	n1, n2 := len(w1.msgs(t)), len(w2.msgs(t))
	mustSave(model.CommandSet{Name: "health-check", Commands: []model.Command{
		{Text: "show clock", PauseSec: 1, SerialSec: 1}, {Text: "configure terminal", PauseSec: 1, SerialSec: 1}}})
	m = w1.awaitMsgs(t, n1+1)
	if len(m) != n1+1 || m[n1].Kind != "show" || len(m[n1].Doc.Commands) != 2 || m[n1].Doc.Commands[1].Danger != guard.ReasonConfigMode {
		t.Fatalf("update not sent: %+v", m[n1:])
	}
	if len(w2.settled(t)) != n2 {
		t.Fatalf("the other window got a line it did not need")
	}
	// Saved unchanged: nothing resent.
	mustSave(model.CommandSet{Name: "health-check", Commands: []model.Command{
		{Text: "show clock", PauseSec: 1, SerialSec: 1}, {Text: "configure terminal", PauseSec: 1, SerialSec: 1}}})
	if len(w1.settled(t)) != n1+1 {
		t.Fatalf("an unchanged set was sent again")
	}

	// Renamed: the window follows the new name (title and set).
	if err := a.RenameCommandSet("health-check", "health-check-2"); err != nil {
		t.Fatal(err)
	}
	m = w1.awaitMsgs(t, n1+2)
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
	m = w2.awaitMsgs(t, n2+1)
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

// withSlowCmdsetProcs is withFakeCmdsetProcs with a process start that waits
// for release: what happens in the main window while a window starts.
func withSlowCmdsetProcs(t *testing.T) (started func() []*fakeCmdsetPipe, release chan struct{}) {
	t.Helper()
	var mu sync.Mutex
	var procs []*fakeCmdsetPipe
	release = make(chan struct{})
	old, oldPrewarm := startCmdsetProc, cmdsetPrewarmOn
	cmdsetPrewarmOn = false
	startCmdsetProc = func(spare bool) (io.WriteCloser, int, <-chan struct{}, error) {
		<-release
		p := &fakeCmdsetPipe{}
		mu.Lock()
		procs = append(procs, p)
		mu.Unlock()
		return p, 0, make(chan struct{}), nil
	}
	t.Cleanup(func() {
		startCmdsetProc, cmdsetPrewarmOn = old, oldPrewarm
		closeCmdsetWindows()
	})
	return func() []*fakeCmdsetPipe {
		mu.Lock()
		defer mu.Unlock()
		return append([]*fakeCmdsetPipe(nil), procs...)
	}, release
}

// The set is saved and renamed while its window's process starts (a save
// passes a window with no pipe by): the window opens with the set as it is
// now, under the new name, and stays that name's window. The old check by
// the click's name left a pipeless entry under the new name for good, and
// View on that set opened nothing from then on.
func TestCmdsetWindowRenamedWhileStarting(t *testing.T) {
	started, release := withSlowCmdsetProcs(t)
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show clock"}}}); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 1)
	go func() { errs <- a.OpenCommandSetWindow("health-check") }()
	time.Sleep(50 * time.Millisecond) // the click is in; the process is starting
	if err := a.SaveCommandSet(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show clock"}, {Text: "show version"}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.RenameCommandSet("health-check", "health-check-2"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if n := len(started()); n != 1 {
		t.Fatalf("started %d windows, want 1", n)
	}
	w := started()[0]
	m := w.awaitMsgs(t, 1)
	if m[0].Kind != "show" || m[0].Doc == nil || m[0].Doc.Name != "health-check-2" || len(m[0].Doc.Commands) != 2 || !strings.Contains(m[0].Title, "health-check-2") {
		t.Fatalf("first line must be the set as saved and renamed meanwhile: %+v", m[0])
	}
	cmdsetMu.Lock()
	_, stale := cmdsetWins["health-check"]
	own := cmdsetWins["health-check-2"]
	cmdsetMu.Unlock()
	if stale || own == nil || own.in == nil {
		t.Fatalf("map after the rename: stale=%v own=%+v", stale, own)
	}
	// View under the new name: the same window, to the front.
	if err := a.OpenCommandSetWindow("health-check-2"); err != nil {
		t.Fatal(err)
	}
	if n := len(started()); n != 1 {
		t.Fatalf("View under the new name started another window (%d)", n)
	}
	if m = w.awaitMsgs(t, 2); m[1].Kind != "focus" {
		t.Fatalf("want focus, got %+v", m[1])
	}
	// …and a later save still reaches it.
	if err := a.SaveCommandSet(model.CommandSet{Name: "health-check-2", Commands: []model.Command{{Text: "show clock"}}}); err != nil {
		t.Fatal(err)
	}
	if m = w.awaitMsgs(t, 3); m[2].Kind != "show" || len(m[2].Doc.Commands) != 1 {
		t.Fatalf("save after the rename not sent: %+v", m[2])
	}
	a.Lock()
	if !w.isClosed() {
		t.Fatal("ロック left the window open")
	}
}

// The set is deleted while its window's process starts: the window opens
// with what was clicked (its first line must be a set) and is told, as an
// open window is.
func TestCmdsetWindowDeletedWhileStarting(t *testing.T) {
	started, release := withSlowCmdsetProcs(t)
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show clock"}}}); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 1)
	go func() { errs <- a.OpenCommandSetWindow("health-check") }()
	time.Sleep(50 * time.Millisecond)
	if err := a.DeleteCommandSet("health-check"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	m := started()[0].awaitMsgs(t, 2)
	if m[0].Kind != "show" || m[0].Doc == nil || m[0].Doc.Name != "health-check" || m[1].Kind != "deleted" {
		t.Fatalf("want the set then the notice, got %+v", m)
	}
}

// The spare's process has exited (crashed) and its watcher has not cleared
// it yet: View starts a window of its own instead of handing the set to a
// process that is gone and showing nothing.
func TestCmdsetSpareExitedBeforeView(t *testing.T) {
	started := withFakeCmdsetProcs(t)
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show clock"}}}); err != nil {
		t.Fatal(err)
	}
	gone := &fakeCmdsetPipe{}
	done := make(chan struct{})
	close(done)
	cmdsetMu.Lock()
	cmdsetSpare = &cmdsetSpareRef{in: gone, done: done}
	cmdsetMu.Unlock()
	if err := a.OpenCommandSetWindow("health-check"); err != nil {
		t.Fatal(err)
	}
	if len(*started) != 1 {
		t.Fatalf("View must start a window of its own when the spare has exited (started %d)", len(*started))
	}
	if !gone.isClosed() {
		t.Fatal("the exited spare's pipe was not closed")
	}
	if m := (*started)[0].awaitMsgs(t, 1); m[0].Kind != "show" || m[0].Doc.Name != "health-check" {
		t.Fatalf("first line = %+v", m)
	}
	cmdsetMu.Lock()
	sp := cmdsetSpare
	cmdsetMu.Unlock()
	if sp != nil {
		t.Fatal("the exited spare is still held")
	}
}

// The spare's process starts outside the lock: a save in the main window
// (which reaches the set windows under it) returns at once meanwhile, no
// second spare starts, and a spare whose tab was left while it started is
// closed rather than kept.
func TestCmdsetRespareStartsOutsideLock(t *testing.T) {
	var mu sync.Mutex
	var procs []*fakeCmdsetPipe
	release := make(chan struct{})
	starting := make(chan struct{}, 2)
	old, oldPrewarm, oldAfter := startCmdsetProc, cmdsetPrewarmOn, cmdsetRespareAfter
	cmdsetPrewarmOn, cmdsetRespareAfter = true, 0
	startCmdsetProc = func(spare bool) (io.WriteCloser, int, <-chan struct{}, error) {
		if spare {
			starting <- struct{}{}
			<-release
		}
		p := &fakeCmdsetPipe{}
		mu.Lock()
		procs = append(procs, p)
		mu.Unlock()
		return p, 0, make(chan struct{}), nil
	}
	t.Cleanup(func() {
		startCmdsetProc, cmdsetPrewarmOn, cmdsetRespareAfter = old, oldPrewarm, oldAfter
		closeCmdsetWindows()
		cmdsetSpareWanted = false
	})
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(procs) }
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show clock"}}}); err != nil {
		t.Fatal(err)
	}
	go a.PrewarmCommandSetWindow()
	select {
	case <-starting:
	case <-time.After(2 * time.Second):
		t.Fatal("no spare started")
	}
	saved := make(chan error, 1)
	go func() {
		saved <- a.SaveCommandSet(model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show version"}}})
	}()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a save waited on the spare's process start")
	}
	a.PrewarmCommandSetWindow() // a second call while one starts: nothing more
	if len(starting) != 0 {
		t.Fatal("a second spare was started while the first was starting")
	}
	// The tab is left before the process is up: the spare is dropped.
	a.DropSpareCommandSetWindow()
	close(release)
	if !waitFor(func() bool { return count() == 1 && procs[0].isClosed() }) {
		t.Fatalf("a spare started after the tab was left must be closed (started %d)", count())
	}
	cmdsetMu.Lock()
	sp, startingNow := cmdsetSpare, cmdsetSpareStarting
	cmdsetMu.Unlock()
	if sp != nil || startingNow {
		t.Fatalf("spare kept: %v starting: %v", sp != nil, startingNow)
	}
	// The tab is shown again: one spare, kept.
	a.PrewarmCommandSetWindow()
	if count() != 2 || procs[1].isClosed() {
		t.Fatalf("showing the tab again must start one spare: %d", count())
	}
}

// The spare's first set carries where the main window is (the window places
// itself over it when shown). A save before the writer wrote that line
// replaces the set but keeps the place; a delete queues behind it (the set
// still shows the window); past the first line the older set is dropped.
func TestCmdsetSendKeepsSpareParent(t *testing.T) {
	cs1 := model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show clock"}}}
	cs2 := model.CommandSet{Name: "health-check", Commands: []model.Command{{Text: "show version"}}}
	w := newCmdsetWinRef("health-check")
	w.sendFirst(cmdsetShowLine(cs1), "10,20,810,620")
	w.send("show", cmdsetShowLine(cs2))
	w.send("deleted", cmdsetKindLine("deleted"))
	p := &fakeCmdsetPipe{}
	w.in = p
	go w.writer()
	defer w.close()
	m := p.awaitMsgs(t, 2)
	if m[0].Kind != "show" || m[0].Parent != "10,20,810,620" || m[0].Doc == nil || m[0].Doc.Commands[0].Text != "show version" {
		t.Fatalf("the spare's first line must carry the place and the newest set: %+v", m[0])
	}
	if m[1].Kind != "deleted" {
		t.Fatalf("the delete must follow the set: %+v", m[1])
	}
	if m = p.settled(t); len(m) != 2 {
		t.Fatalf("lines written: %d, want 2", len(m))
	}

	w2 := newCmdsetWinRef("health-check")
	w2.send("show", cmdsetShowLine(cs1))
	w2.send("show", cmdsetShowLine(cs2))
	w2.send("focus", cmdsetKindLine("focus"))
	p2 := &fakeCmdsetPipe{}
	w2.in = p2
	go w2.writer()
	defer w2.close()
	if m = p2.awaitMsgs(t, 2); m[0].Parent != "" || m[0].Doc.Commands[0].Text != "show version" || m[1].Kind != "focus" {
		t.Fatalf("an ordinary window keeps only the newest set and the focus: %+v", m)
	}
}
