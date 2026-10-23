package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/kojio145/palaterm/internal/guard"
	"github.com/kojio145/palaterm/internal/model"
)

// コマンドセットの「表示」: one command set, read-only, in a window of its
// own ("PalaTerm.exe --cmdset"), so it can sit beside a terminal while the
// commands are typed by hand. The child cannot open the vault, so the main
// window hands it the set — as JSON lines on its stdin, never in its
// arguments or a file (a command may carry a password) — and sends the set
// again whenever it is saved (renamed: the new name; deleted: a notice).
// One window per set: opening it again brings that window to the front.
// The windows close with the vault (ロック, exit), as the GUI windows do:
// closing the pipe is the signal.

// cmdsetLine is one command as the window shows it.
type cmdsetLine struct {
	Text      string `json:"text"`
	Wait      string `json:"wait,omitempty"`
	PauseSec  int    `json:"pauseSec"`
	SerialSec int    `json:"serialSec"`
	// Danger is guard.Check's reason (Japanese, translated by the page) when
	// the command changes the device; "" otherwise.
	Danger string `json:"danger,omitempty"`
}

// cmdsetDoc is a set as the window shows it.
type cmdsetDoc struct {
	Name     string       `json:"name"`
	PauseOff bool         `json:"pauseOff,omitempty"`
	Commands []cmdsetLine `json:"commands"`
}

// cmdsetMsg is one line on a set window's stdin.
type cmdsetMsg struct {
	// Kind: "show" (Doc is the set, Title the window's), "deleted" (the set
	// is gone; the window stays with what it showed) or "focus" (come to
	// the front: the set was opened again).
	Kind  string     `json:"kind"`
	Title string     `json:"title,omitempty"`
	Doc   *cmdsetDoc `json:"doc,omitempty"`
	// Parent is the main window's rectangle ("l,t,r,b") on the first show
	// sent to a spare window, which was started hidden before the click and
	// is placed over the main window as it is now.
	Parent string `json:"parent,omitempty"`
}

// cmdsetDocOf is what a window shows for cs.
func cmdsetDocOf(cs model.CommandSet) cmdsetDoc {
	d := cmdsetDoc{Name: cs.Name, PauseOff: cs.PauseOff, Commands: make([]cmdsetLine, len(cs.Commands))}
	texts := make([]string, len(cs.Commands))
	for i, c := range cs.Commands {
		texts[i] = c.Text
		d.Commands[i] = cmdsetLine{Text: c.Text, Wait: c.Wait, PauseSec: c.PauseSec, SerialSec: c.SerialSec}
	}
	for _, h := range guard.Check(texts) {
		d.Commands[h.Index].Danger = h.Reason
	}
	return d
}

func cmdsetTitle(name string) string {
	return "PalaTerm - " + winWord("コマンドセット", "Command set") + ": " + name
}

// cmdsetShowLine is the "show" line for cs.
func cmdsetShowLine(cs model.CommandSet) []byte {
	d := cmdsetDocOf(cs)
	b, _ := json.Marshal(cmdsetMsg{Kind: "show", Title: cmdsetTitle(cs.Name), Doc: &d})
	return append(b, '\n')
}

func cmdsetKindLine(kind string) []byte {
	b, _ := json.Marshal(cmdsetMsg{Kind: kind})
	return append(b, '\n')
}

// ---- main-window side ----

// cmdsetWinRef is an open set window, known by the name of its set.
type cmdsetWinRef struct {
	in   io.WriteCloser
	pid  int
	name string
	last []byte // the last line sent (nothing is resent unchanged)
}

// cmdsetSpareRef is a set window started hidden ahead of the click: a new
// window costs a process and a WebView2 start (a second or two), so one is
// kept ready while the Command sets tab is open, and View only hands it the
// set and shows it.
type cmdsetSpareRef struct {
	in   io.WriteCloser
	pid  int
	done <-chan struct{}
}

var (
	cmdsetMu    sync.Mutex
	cmdsetWins  = map[string]*cmdsetWinRef{}
	cmdsetSpare *cmdsetSpareRef
	// cmdsetPrewarmOn turns the spare window on (off in tests that count the
	// windows started).
	cmdsetPrewarmOn = true
	// cmdsetRespareAfter is how long after View the next spare starts: once
	// the window just shown holds the foreground, so the spare hands it
	// back to that window (keepForegroundWhileHidden). 0 starts it at once
	// (tests).
	cmdsetRespareAfter = 1500 * time.Millisecond
	// startCmdsetProc starts a set window (spare: hidden, waiting for its
	// set) and returns its stdin and pid, and a channel closed when it has
	// exited (replaced in tests).
	startCmdsetProc = spawnCmdsetProc
)

func spawnCmdsetProc(spare bool) (io.WriteCloser, int, <-chan struct{}, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, 0, nil, err
	}
	args := []string{"--cmdset"}
	if spare {
		args = append(args, "--spare")
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = childEnv()
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, 0, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, 0, nil, err
	}
	tieToMainWindow(cmd.Process.Pid)
	trackChild(cmd.Process)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	return in, cmd.Process.Pid, done, nil
}

// OpenCommandSetWindow shows the set in its read-only window: a new one, or
// the one already open for it brought to the front.
func (a *App) OpenCommandSetWindow(name string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	var cs *model.CommandSet
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == name {
			c := a.inv.CommandSets[i]
			c.Commands = append([]model.Command(nil), c.Commands...)
			cs = &c
			break
		}
	}
	a.mu.Unlock()
	if cs == nil {
		return fmt.Errorf("command set %q not found", name)
	}
	line := cmdsetShowLine(*cs)
	cmdsetMu.Lock()
	if w := cmdsetWins[name]; w != nil {
		var err error
		if !bytes.Equal(w.last, line) {
			_, err = w.in.Write(line)
			w.last = line
		}
		if err == nil {
			_, err = w.in.Write(cmdsetKindLine("focus"))
		}
		if err == nil {
			pid := w.pid
			cmdsetMu.Unlock()
			bringProcessToFront(pid)
			return nil
		}
		// Closed by hand a moment ago: open a new one.
		delete(cmdsetWins, name)
	}
	// The spare window when there is one (it shows at once), a new one
	// otherwise; either way the next spare starts behind it.
	var in io.WriteCloser
	var pid int
	var done <-chan struct{}
	first := line
	if sp := cmdsetSpare; sp != nil {
		cmdsetSpare = nil
		in, pid, done = sp.in, sp.pid, sp.done
		first = cmdsetSpareShowLine(*cs, parentRectString())
	}
	cmdsetMu.Unlock()
	defer func() {
		if cmdsetRespareAfter == 0 {
			a.PrewarmCommandSetWindow()
			return
		}
		time.AfterFunc(cmdsetRespareAfter, a.PrewarmCommandSetWindow)
	}()
	if in == nil {
		var err error
		if in, pid, done, err = startCmdsetProc(false); err != nil {
			return err
		}
	}
	if _, err := in.Write(first); err != nil {
		_ = in.Close()
		return err
	}
	w := &cmdsetWinRef{in: in, pid: pid, name: name, last: line}
	cmdsetMu.Lock()
	cmdsetWins[name] = w
	cmdsetMu.Unlock()
	go func() {
		<-done
		cmdsetMu.Lock()
		if cmdsetWins[w.name] == w {
			delete(cmdsetWins, w.name)
		}
		cmdsetMu.Unlock()
	}()
	return nil
}

// PrewarmCommandSetWindow starts the spare set window if there is none (the
// Command sets tab calls it when it is shown; View starts the next one).
// Nothing while the vault is locked.
func (a *App) PrewarmCommandSetWindow() {
	if !cmdsetPrewarmOn {
		return
	}
	a.mu.Lock()
	open := a.inv != nil
	a.mu.Unlock()
	if !open {
		return
	}
	cmdsetMu.Lock()
	defer cmdsetMu.Unlock()
	if cmdsetSpare != nil {
		return
	}
	in, pid, done, err := startCmdsetProc(true)
	if err != nil {
		return
	}
	sp := &cmdsetSpareRef{in: in, pid: pid, done: done}
	cmdsetSpare = sp
	go func() {
		<-done
		cmdsetMu.Lock()
		if cmdsetSpare == sp {
			cmdsetSpare = nil
		}
		cmdsetMu.Unlock()
	}()
}

// cmdsetSpareShowLine is the first line to a spare window: the set, and
// where the main window is now.
func cmdsetSpareShowLine(cs model.CommandSet, parent string) []byte {
	doc := cmdsetDocOf(cs)
	b, _ := json.Marshal(cmdsetMsg{Kind: "show", Title: cmdsetTitle(cs.Name), Doc: &doc, Parent: parent})
	return append(b, '\n')
}

// renameCmdsetWindow moves the window of a renamed set to its new name (the
// next sync sends the set, and so the new title). A window still on the
// new name — its set was deleted, or it would not be free — is closed.
func renameCmdsetWindow(oldName, newName string) {
	cmdsetMu.Lock()
	defer cmdsetMu.Unlock()
	w := cmdsetWins[oldName]
	if w == nil || oldName == newName {
		return
	}
	if o := cmdsetWins[newName]; o != nil {
		_ = o.in.Close()
	}
	delete(cmdsetWins, oldName)
	w.name = newName
	cmdsetWins[newName] = w
}

// syncCmdsetWindows brings every open set window up to date with sets (the
// vault after a save): the set again when it changed, a notice when it is
// gone. Called with nothing held.
func syncCmdsetWindows(sets []model.CommandSet) {
	cmdsetMu.Lock()
	defer cmdsetMu.Unlock()
	if len(cmdsetWins) == 0 {
		return
	}
	byName := make(map[string]*model.CommandSet, len(sets))
	for i := range sets {
		byName[sets[i].Name] = &sets[i]
	}
	for name, w := range cmdsetWins {
		var line []byte
		if cs := byName[name]; cs != nil {
			line = cmdsetShowLine(*cs)
		} else {
			line = cmdsetKindLine("deleted")
		}
		if bytes.Equal(w.last, line) {
			continue
		}
		_, _ = w.in.Write(line)
		w.last = line
	}
}

// cmdsetSetsLocked is a copy of the vault's sets for syncCmdsetWindows, or
// nil when no set window is open (the common case costs nothing). a.mu held.
func (a *App) cmdsetSetsLocked() []model.CommandSet {
	cmdsetMu.Lock()
	n := len(cmdsetWins)
	cmdsetMu.Unlock()
	if n == 0 || a.inv == nil {
		return nil
	}
	out := make([]model.CommandSet, len(a.inv.CommandSets))
	for i, cs := range a.inv.CommandSets {
		cs.Commands = append([]model.Command(nil), cs.Commands...)
		out[i] = cs
	}
	return out
}

// closeCmdsetWindows closes every set window and the spare (ロック, exit,
// 全データ削除).
func closeCmdsetWindows() {
	cmdsetMu.Lock()
	defer cmdsetMu.Unlock()
	if cmdsetSpare != nil {
		_ = cmdsetSpare.in.Close()
		cmdsetSpare = nil
	}
	for name, w := range cmdsetWins {
		_ = w.in.Close()
		delete(cmdsetWins, name)
	}
}

// ---- the window's own process ----

// CmdsetWin is the backend of a command-set window. It holds only what the
// main window sent; it has no vault and changes nothing.
type CmdsetWin struct {
	ctx context.Context

	mu      sync.Mutex
	doc     cmdsetDoc
	title   string
	deleted bool
	// hidden: a spare window not shown yet (its first set shows it).
	hidden bool
}

// cmdsetState is what the page shows.
type cmdsetState struct {
	Doc     cmdsetDoc `json:"doc"`
	Deleted bool      `json:"deleted"`
}

// apply takes one line from the main window; it reports whether the page
// must redraw and whether the window must come to the front.
func (c *CmdsetWin) apply(m cmdsetMsg) (redraw, focus bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch m.Kind {
	case "show":
		if m.Doc == nil {
			return false, false
		}
		c.doc, c.deleted = *m.Doc, false
		if m.Title != "" {
			c.title = m.Title
		}
		return true, false
	case "deleted":
		c.deleted = true
		return true, false
	case "focus":
		return false, true
	}
	return false, false
}

func (c *CmdsetWin) startup(ctx context.Context) {
	c.ctx = ctx
	c.mu.Lock()
	hidden := c.hidden
	c.mu.Unlock()
	// A spare is sized and placed when it is shown: sizing a hidden frame
	// through the runtime made it the (invisible) foreground window, and the
	// keyboard went to it.
	if !hidden {
		cmdsetWinSize.fit(ctx)
		placeNearParent(ctx, cmdsetWinSize)
	}
}

func (c *CmdsetWin) domReady(ctx context.Context) {
	c.mu.Lock()
	hidden := c.hidden
	c.mu.Unlock()
	if !hidden {
		go focusOwnWindow()
	}
}

// Load is the set as last sent.
func (c *CmdsetWin) Load() cmdsetState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cmdsetState{Doc: c.doc, Deleted: c.deleted}
}

// SetOnTop keeps the window above the others (常に手前に表示).
func (c *CmdsetWin) SetOnTop(on bool) {
	if c.ctx != nil {
		runtime.WindowSetAlwaysOnTop(c.ctx, on)
	}
}

// Close ends the window (Esc).
func (c *CmdsetWin) Close() { runtime.Quit(c.ctx) }

// follow reads the main window's lines until the pipe closes (ロック, exit),
// then ends the window.
func (c *CmdsetWin) follow(in *bufio.Reader, ready <-chan struct{}) {
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			var m cmdsetMsg
			if json.Unmarshal(line, &m) == nil {
				redraw, focus := c.apply(m)
				<-ready
				// A spare's first set: placed over the main window as it is
				// now, then shown with the set already drawn.
				c.mu.Lock()
				first := c.hidden && m.Kind == "show" && m.Doc != nil
				if first {
					c.hidden = false
				}
				c.mu.Unlock()
				if first {
					cmdsetWinSize.fit(c.ctx)
					placeHiddenOver(c.ctx, cmdsetWinSize, m.Parent)
					focus = true
				}
				if redraw {
					c.mu.Lock()
					st, title := cmdsetState{Doc: c.doc, Deleted: c.deleted}, c.title
					c.mu.Unlock()
					runtime.WindowSetTitle(c.ctx, title)
					runtime.EventsEmit(c.ctx, "cmdset:update", st)
				}
				if focus {
					runtime.WindowUnminimise(c.ctx)
					runtime.WindowShow(c.ctx)
					go focusOwnWindow()
				}
			}
		}
		if err != nil {
			<-ready
			runtime.Quit(c.ctx)
			return
		}
	}
}
