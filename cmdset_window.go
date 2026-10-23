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

var (
	cmdsetMu   sync.Mutex
	cmdsetWins = map[string]*cmdsetWinRef{}
	// startCmdsetProc starts a set window and returns its stdin and pid,
	// and a channel closed when it has exited (replaced in tests).
	startCmdsetProc = spawnCmdsetProc
)

func spawnCmdsetProc() (io.WriteCloser, int, <-chan struct{}, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, 0, nil, err
	}
	cmd := exec.Command(exe, "--cmdset")
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
	cmdsetMu.Unlock()
	in, pid, done, err := startCmdsetProc()
	if err != nil {
		return err
	}
	if _, err := in.Write(line); err != nil {
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

// closeCmdsetWindows closes every set window (ロック, exit, 全データ削除).
func closeCmdsetWindows() {
	cmdsetMu.Lock()
	defer cmdsetMu.Unlock()
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
	cmdsetWinSize.fit(ctx)
	placeNearParent(ctx, cmdsetWinSize)
}

func (c *CmdsetWin) domReady(ctx context.Context) { go focusOwnWindow() }

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
