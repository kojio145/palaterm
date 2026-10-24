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
	"sync/atomic"
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

// cmdsetWithParent is a "show" line with the main window's rectangle added
// (the first line to a spare window; added when it is written, see
// cmdsetOut).
func cmdsetWithParent(line []byte, parent string) []byte {
	var m cmdsetMsg
	if json.Unmarshal(line, &m) != nil {
		return line
	}
	m.Parent = parent
	b, _ := json.Marshal(m)
	return append(b, '\n')
}

func cmdsetKindLine(kind string) []byte {
	b, _ := json.Marshal(cmdsetMsg{Kind: kind})
	return append(b, '\n')
}

// ---- main-window side ----

// cmdsetWinRef is an open set window, known by the name of its set. Its
// lines are written by a goroutine of its own (writer): a pipe write blocks
// while the child is still starting its WebView2 (or has hung), and a save
// in the main window must not wait on that — persist() only queues the line.
// Until the process is started (in == nil) the entry marks the name as being
// opened, so a second click in that moment opens nothing more.
type cmdsetWinRef struct {
	in   io.WriteCloser
	pid  int
	name string
	last []byte // the last line queued (nothing is resent unchanged)

	qmu  sync.Mutex
	q    []cmdsetOut
	kick chan struct{}
	stop chan struct{}
	once sync.Once
	dead atomic.Bool // the pipe broke: the window is gone or going
}

// cmdsetOut is a line waiting to be written to a window. first marks the
// window's first line, a set (a window started with no set ends; a spare
// shows on its first set); parent is then the main window's rectangle for
// a spare (it places itself over it), "" otherwise, added to the line as it
// is written so it survives the set being replaced in the queue.
type cmdsetOut struct {
	kind   string
	line   []byte
	first  bool
	parent string
}

func newCmdsetWinRef(name string) *cmdsetWinRef {
	return &cmdsetWinRef{name: name, kick: make(chan struct{}, 1), stop: make(chan struct{})}
}

// send queues one line. A newer set (or a delete) replaces one still waiting
// — only the latest content matters — so a child that does not read keeps at
// most one set and the focus requests in memory. The first line is the
// exception: a newer set takes its place (and a spare's parent), and a
// delete queues behind it, or the window would end, or never show.
func (w *cmdsetWinRef) send(kind string, line []byte) { w.queue(cmdsetOut{kind: kind, line: line}) }

// sendFirst queues the window's first line: its set, with the main window's
// rectangle when it is a spare.
func (w *cmdsetWinRef) sendFirst(line []byte, parent string) {
	w.queue(cmdsetOut{kind: "show", line: line, first: true, parent: parent})
}

func (w *cmdsetWinRef) queue(out cmdsetOut) {
	w.qmu.Lock()
	if out.kind != "focus" {
		kept := w.q[:0]
		for _, o := range w.q {
			switch {
			case o.kind == "focus":
				kept = append(kept, o)
			case o.first && out.kind == "show":
				out.first, out.parent = true, o.parent
			case o.first:
				kept = append(kept, o)
			}
		}
		w.q = kept
	}
	w.q = append(w.q, out)
	w.qmu.Unlock()
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// writer writes the queued lines in order until the pipe breaks or the
// window is closed from this side.
func (w *cmdsetWinRef) writer() {
	for {
		select {
		case <-w.stop:
			return
		case <-w.kick:
		}
		for {
			w.qmu.Lock()
			if len(w.q) == 0 {
				w.qmu.Unlock()
				break
			}
			o := w.q[0]
			w.q = w.q[1:]
			w.qmu.Unlock()
			line := o.line
			if o.parent != "" {
				line = cmdsetWithParent(line, o.parent)
			}
			if _, err := w.in.Write(line); err != nil {
				w.dead.Store(true)
				_ = w.in.Close()
				return
			}
		}
	}
}

// close ends the window from this side: the pipe closes (the child quits on
// it) and the writer stops.
func (w *cmdsetWinRef) close() {
	w.once.Do(func() {
		w.dead.Store(true)
		close(w.stop)
		if w.in != nil {
			_ = w.in.Close()
		}
	})
}

// cmdsetSpareRef is a set window started hidden ahead of the click: a new
// window costs a process and a WebView2 start (a second or two), so one is
// kept ready while the Command sets tab is shown, and View only hands it the
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
	// cmdsetSpareWanted: the Command sets tab is shown, so a spare is kept
	// (Prewarm sets it, DropSpare clears it; the respare after View checks
	// it, so leaving the tab leaves no idle process behind).
	cmdsetSpareWanted bool
	// cmdsetSpareStarting: a spare's process is being started (outside the
	// lock, see respareCommandSetWindow), so no second one starts meanwhile.
	cmdsetSpareStarting bool
	// cmdsetCloseGen counts closeCmdsetWindows calls: a spare whose start
	// straddled one (ロック while it started) is closed, not kept.
	cmdsetCloseGen int
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

// commandSetCopyLocked is a copy of the set called name, nil when there is
// none. Under a.mu.
func (a *App) commandSetCopyLocked(name string) *model.CommandSet {
	if a.inv == nil {
		return nil
	}
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == name {
			c := a.inv.CommandSets[i]
			c.Commands = append([]model.Command(nil), c.Commands...)
			return &c
		}
	}
	return nil
}

// dropCmdsetWinLocked takes w out of the map, under whichever name it holds
// now (a rename moves it). Under cmdsetMu.
func dropCmdsetWinLocked(w *cmdsetWinRef) {
	if cmdsetWins[w.name] == w {
		delete(cmdsetWins, w.name)
	}
}

// OpenCommandSetWindow shows the set in its read-only window: a new one, or
// the one already open for it brought to the front.
func (a *App) OpenCommandSetWindow(name string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	cs := a.commandSetCopyLocked(name)
	a.mu.Unlock()
	if cs == nil {
		return fmt.Errorf("command set %q not found", name)
	}
	line := cmdsetShowLine(*cs)
	cmdsetMu.Lock()
	if w := cmdsetWins[name]; w != nil && !w.dead.Load() {
		if w.in == nil {
			// Being opened by a click a moment ago (a double click on the
			// preview): that window shows this set and comes to the front.
			cmdsetMu.Unlock()
			return nil
		}
		if !bytes.Equal(w.last, line) {
			w.send("show", line)
			w.last = line
		}
		w.send("focus", cmdsetKindLine("focus"))
		pid := w.pid
		cmdsetMu.Unlock()
		bringProcessToFront(pid)
		return nil
	}
	// The name is taken while the window starts (a second click in that
	// moment, above, opens nothing). The spare window when there is one (it
	// shows at once), a new one otherwise; either way the next spare starts
	// behind it.
	w := newCmdsetWinRef(name)
	cmdsetWins[name] = w
	var in io.WriteCloser
	var pid int
	var done <-chan struct{}
	parent := ""
	if sp := cmdsetSpare; sp != nil {
		cmdsetSpare = nil
		select {
		case <-sp.done:
			// It has exited (crashed) and its watcher has not cleared it
			// yet: a window of its own instead of a click that shows nothing.
			_ = sp.in.Close()
		default:
			in, pid, done = sp.in, sp.pid, sp.done
			parent = parentRectString()
		}
	}
	cmdsetMu.Unlock()
	defer func() {
		if cmdsetRespareAfter == 0 {
			a.respareCommandSetWindow()
			return
		}
		time.AfterFunc(cmdsetRespareAfter, a.respareCommandSetWindow)
	}()
	var err error
	if in == nil {
		in, pid, done, err = startCmdsetProc(false)
	}
	// The window gets the set as it is now, under its name as it is now: a
	// save or a rename while the process started passed it by (sync skips a
	// window with no pipe yet), and the click's copy would stand until the
	// next save. a.mu before cmdsetMu, as persist takes them.
	a.mu.Lock()
	cmdsetMu.Lock()
	if err != nil {
		dropCmdsetWinLocked(w)
		cmdsetMu.Unlock()
		a.mu.Unlock()
		return err
	}
	if cmdsetWins[w.name] != w {
		// Closed (ロック) while the process was starting.
		cmdsetMu.Unlock()
		a.mu.Unlock()
		_ = in.Close()
		return nil
	}
	first, deleted := line, false
	if cur := a.commandSetCopyLocked(w.name); cur != nil {
		first = cmdsetShowLine(*cur)
	} else {
		// Deleted while starting: the window opens with what was clicked
		// (its first line must be a set) and is told, as an open one is.
		deleted = true
	}
	w.in, w.pid, w.last = in, pid, first
	go w.writer()
	w.sendFirst(first, parent)
	if deleted {
		w.last = cmdsetKindLine("deleted")
		w.send("deleted", w.last)
	}
	cmdsetMu.Unlock()
	a.mu.Unlock()
	go func() {
		<-done
		cmdsetMu.Lock()
		dropCmdsetWinLocked(w)
		cmdsetMu.Unlock()
		w.close()
	}()
	return nil
}

// PrewarmCommandSetWindow starts the spare set window if there is none, and
// keeps one while the Command sets tab is shown (the tab calls it when it is
// shown; View starts the next one). Nothing while the vault is locked.
func (a *App) PrewarmCommandSetWindow() {
	cmdsetMu.Lock()
	cmdsetSpareWanted = true
	cmdsetMu.Unlock()
	a.respareCommandSetWindow()
}

// DropSpareCommandSetWindow closes the spare and stops keeping one (the
// Command sets tab was left): an idle process is not kept for hours for a
// click that may not come.
func (a *App) DropSpareCommandSetWindow() {
	cmdsetMu.Lock()
	defer cmdsetMu.Unlock()
	cmdsetSpareWanted = false
	if cmdsetSpare != nil {
		_ = cmdsetSpare.in.Close()
		cmdsetSpare = nil
	}
}

// respareCommandSetWindow starts the spare when one is wanted and there is
// none.
func (a *App) respareCommandSetWindow() {
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
	if cmdsetSpare != nil || cmdsetSpareStarting || !cmdsetSpareWanted {
		cmdsetMu.Unlock()
		return
	}
	// Reserved under the lock, started outside it: a process start takes a
	// second or two, and a save (syncCmdsetWindowsLocked, under a.mu) must
	// not wait on it.
	cmdsetSpareStarting = true
	gen := cmdsetCloseGen
	cmdsetMu.Unlock()
	in, pid, done, err := startCmdsetProc(true)
	cmdsetMu.Lock()
	defer cmdsetMu.Unlock()
	cmdsetSpareStarting = false
	if err != nil {
		return
	}
	if !cmdsetSpareWanted || cmdsetCloseGen != gen {
		// The tab was left, or the windows were closed (ロック), meanwhile.
		_ = in.Close()
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
		o.close()
	}
	delete(cmdsetWins, oldName)
	w.name = newName
	cmdsetWins[newName] = w
}

// syncCmdsetWindowsLocked brings every open set window up to date with the
// vault just saved: the set again when it changed, a notice when it is gone.
// Called under a.mu, right after the save, so two saves cannot reach the
// windows in the wrong order; it only queues the lines (see cmdsetWinRef).
func (a *App) syncCmdsetWindowsLocked() {
	cmdsetMu.Lock()
	defer cmdsetMu.Unlock()
	if len(cmdsetWins) == 0 || a.inv == nil {
		return
	}
	byName := make(map[string]*model.CommandSet, len(a.inv.CommandSets))
	for i := range a.inv.CommandSets {
		byName[a.inv.CommandSets[i].Name] = &a.inv.CommandSets[i]
	}
	for name, w := range cmdsetWins {
		if w.in == nil {
			continue // still opening: it gets the set as it is now
		}
		kind, line := "show", []byte(nil)
		if cs := byName[name]; cs != nil {
			line = cmdsetShowLine(*cs)
		} else {
			kind, line = "deleted", cmdsetKindLine("deleted")
		}
		if bytes.Equal(w.last, line) {
			continue
		}
		w.send(kind, line)
		w.last = line
	}
}

// closeCmdsetWindows closes every set window and the spare (ロック, exit,
// 全データ削除). A window still starting is dropped from the map; its opener
// closes the pipe when the process is there.
func closeCmdsetWindows() {
	cmdsetMu.Lock()
	defer cmdsetMu.Unlock()
	cmdsetCloseGen++
	if cmdsetSpare != nil {
		_ = cmdsetSpare.in.Close()
		cmdsetSpare = nil
	}
	for name, w := range cmdsetWins {
		w.close()
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

// Load is the set as last sent. The page calls it when it starts and again
// once it listens for updates: a spare's first set may have arrived in
// between, and an event emitted before the page listened is lost.
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
