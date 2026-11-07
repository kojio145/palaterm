package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// line is the line the window opens at (a grep hit), 0 = the end.
	line int
	cc   childCtx // for the parent's CLOSE / LINE (watchParent)

	// What Load read so far (see read): the file's bytes, their cleaned
	// text, and the size and time the file had, so the follow timer reads
	// only what was added.
	mu     sync.Mutex
	raw    []byte
	text   string
	mod    time.Time
	loaded bool
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
	// Line: the line to show and select first (a grep hit), 0 = the end.
	Line int `json:"line,omitempty"`
}

func NewViewer(path, title string) *Viewer {
	line, _ := strconv.Atoi(os.Getenv(viewLineEnv))
	return &Viewer{path: path, title: title, live: os.Getenv("PALATERM_VIEW_LIVE") == "1", line: line}
}

// openInExplorer shows a file selected in Explorer (or opens a folder).
func openInExplorer(path string) error {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return startExplorer(path)
	}
	return startExplorer("/select,", path)
}

// windowEnv is childEnv for a window opened by a window that may itself be
// a child: a terminal window's own place (PALATERM_TILE, PALATERM_CASCADE
// — its grid cell, its step in the cascade) and its 同時入力 (PALATERM_BCAST)
// are left out, or its paste window would open up to 224 px off its centre
// and its log viewer in its grid cell.
func windowEnv(extra ...string) []string {
	all := childEnv(extra...)
	env := all[:0]
	for _, kv := range all {
		if strings.HasPrefix(kv, tileEnv+"=") || strings.HasPrefix(kv, cascadeEnv+"=") || strings.HasPrefix(kv, bcastEnv+"=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// viewerKids are the log, diff, grep and screen windows this process opened
// and still running (spawnWindow): closed with the vault (Lock, ResetVault),
// as the GUI and set windows are — they show device configurations.
var (
	viewerKidsMu sync.Mutex
	viewerKids   = map[*viewerKid]struct{}{}
)

// A window started by spawnWindow has its stdin as a pipe its parent holds
// (kidPipeEnv says so; without it a window never reads stdin — a console's,
// or none). The parent writes CLOSE when it closes its windows (the vault
// locked): the window closes the windows it opened itself first, then ends
// — a kill left those behind (a log opened from the grep window stayed
// after the lock). A log viewer also takes LINE <n>: show line n (0: just
// come to the front), which is how the grep window reuses an open viewer
// for the same file. EOF alone changes nothing: a parent that ended the
// ordinary way (the grep window closed by hand) leaves its windows as
// before.
const (
	kidPipeEnv   = "PALATERM_PARENT_PIPE"
	kidLineClose = "CLOSE"
	kidLineGoto  = "LINE"
	// kidCloseWait is how long a window asked to close has before it is
	// ended anyway.
	kidCloseWait = 700 * time.Millisecond
)

// viewerKid is one window this process opened (spawnKid).
type viewerKid struct {
	proc *os.Process
	in   io.WriteCloser
	inMu sync.Mutex
	done chan struct{} // closed once the process has ended
}

// alive reports the window still running.
func (k *viewerKid) alive() bool {
	select {
	case <-k.done:
		return false
	default:
		return true
	}
}

// send writes one line of the protocol above to the window.
func (k *viewerKid) send(line string) error {
	k.inMu.Lock()
	defer k.inMu.Unlock()
	if !k.alive() {
		return errors.New("window closed")
	}
	_, err := io.WriteString(k.in, line+"\n")
	return err
}

// spawnWindow starts another PalaTerm window process detached from this one.
func spawnWindow(env []string, args ...string) error {
	_, err := spawnKid(env, args...)
	return err
}

// spawnKid is spawnWindow with the window handed back, for a parent that
// talks to it (the grep window's LINE).
func spawnKid(env []string, args ...string) (*viewerKid, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = windowEnv(append(append([]string(nil), env...), kidPipeEnv+"=1")...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	tieToMainWindow(cmd.Process.Pid)
	trackChild(cmd.Process)
	k := &viewerKid{proc: cmd.Process, in: stdin, done: make(chan struct{})}
	viewerKidsMu.Lock()
	viewerKids[k] = struct{}{}
	viewerKidsMu.Unlock()
	go func() {
		_ = cmd.Wait()
		close(k.done)
		viewerKidsMu.Lock()
		delete(viewerKids, k)
		viewerKidsMu.Unlock()
	}()
	return k, nil
}

// closeViewerWindows ends the log, diff, grep and screen windows still open
// (the vault locked or deleted): each is asked to close (CLOSE, so it closes
// the windows it opened itself), and one still running after kidCloseWait
// is ended anyway. Returns at once.
func closeViewerWindows() {
	viewerKidsMu.Lock()
	kids := make([]*viewerKid, 0, len(viewerKids))
	for k := range viewerKids {
		kids = append(kids, k)
		delete(viewerKids, k)
	}
	viewerKidsMu.Unlock()
	askKidsClose(kids)
	go killKidsAfter(kids, kidCloseWait)
}

// askKidsClose writes CLOSE to each window.
func askKidsClose(kids []*viewerKid) {
	for _, k := range kids {
		_ = k.send(kidLineClose)
	}
}

// killKidsAfter ends the windows of kids still running after wait.
func killKidsAfter(kids []*viewerKid, wait time.Duration) {
	time.Sleep(wait)
	for _, k := range kids {
		if k.alive() && k.proc != nil {
			_ = k.proc.Kill()
		}
	}
}

// openViewerWindows is how many such windows are still running (tests).
func openViewerWindows() int {
	viewerKidsMu.Lock()
	defer viewerKidsMu.Unlock()
	return len(viewerKids)
}

// parseKidLine reads one line of the parent's protocol: the command and
// its number (LINE 12 → "LINE", 12; CLOSE → "CLOSE", 0). Anything else is
// "" — ignored.
func parseKidLine(s string) (string, int) {
	cmd, arg, _ := strings.Cut(strings.TrimSpace(s), " ")
	switch cmd {
	case kidLineClose:
		return cmd, 0
	case kidLineGoto:
		n, err := strconv.Atoi(strings.TrimSpace(arg))
		if err != nil || n < 0 {
			return "", 0
		}
		return cmd, n
	}
	return "", 0
}

// childCtx is a child window's context once startup has it, read from the
// parent-pipe goroutine (watchParent), which may need it before or after.
type childCtx struct {
	mu  sync.Mutex
	ctx context.Context
}

func (c *childCtx) set(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
}

func (c *childCtx) get() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctx
}

// quit ends the window: through Wails once it is up, else the process.
func (c *childCtx) quit() {
	if ctx := c.get(); ctx != nil {
		runtime.Quit(ctx)
		return
	}
	os.Exit(0)
}

// watchParent reads the parent's lines from stdin when kidPipeEnv says the
// parent holds the pipe: CLOSE closes this window's own windows and ends it
// (quit); LINE n goes to onLine (nil: ignored). Returns at EOF, leaving the
// window as it is.
func watchParent(quit func(), onLine func(n int)) {
	if os.Getenv(kidPipeEnv) != "1" {
		return
	}
	go func() {
		defer recoverGo("parent pipe")
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			switch cmd, n := parseKidLine(sc.Text()); cmd {
			case kidLineClose:
				closeViewerWindows()
				quit()
				return
			case kidLineGoto:
				if onLine != nil {
					onLine(n)
				}
			}
		}
	}()
}

func (v *Viewer) startup(ctx context.Context) {
	v.ctx = ctx
	v.cc.set(ctx)
	viewerWin.fit(ctx)
	placeNearParent(ctx, viewerWin)
}

// showLine is LINE n from the parent (the grep window, a hit in a file this
// window already shows): the page selects and scrolls to line n (0: nothing
// to show), and the window comes to the front.
func (v *Viewer) showLine(n int) {
	if ctx := v.cc.get(); ctx != nil && n > 0 {
		runtime.EventsEmit(ctx, "viewer:line", n)
	}
	go focusOwnWindow()
}

// Changed says whether the file differs from what Load last read (its size
// or time). The page's follow timer asks this every two seconds and loads
// the text only then: the whole text used to cross to the page each time,
// to be compared there.
func (v *Viewer) Changed() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.loaded {
		return true
	}
	st, err := os.Stat(v.path)
	if err != nil {
		return true
	}
	return st.Size() != int64(len(v.raw)) || !st.ModTime().Equal(v.mod)
}

func (v *Viewer) domReady(ctx context.Context) { go focusOwnWindow() }

// Load reads the file (also used by the page's reload / follow timer, so a
// log that is still being written — an interactive session — stays current).
func (v *Viewer) Load() viewerDoc {
	d := viewerDoc{Path: v.path, Name: filepath.Base(v.path)}
	text, err := v.read()
	if err != nil {
		d.Err = err.Error()
		return d
	}
	d.Text = text
	d.Live = v.live
	d.Line = v.line
	return d
}

// read is the file's text as it was on screen: an interactive capture keeps
// the device's line editing ("\b \b" from tab completion) and colour
// escapes, which read as garbage in a text view (history.CleanTranscript;
// the file itself is left as captured). The follow timer calls it every
// two seconds: a file that has not changed costs a Stat, one that grew is
// read from where the last read ended, and one that shrank or was rewritten
// (replaced) is read from the start again.
func (v *Viewer) read() (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	f, err := os.Open(v.path)
	if err != nil {
		v.loaded = false // whatever comes back is read whole
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		v.loaded = false
		return "", err
	}
	size, have := st.Size(), int64(len(v.raw))
	if v.loaded && size == have && st.ModTime().Equal(v.mod) {
		return v.text, nil
	}
	// Appended to, or another file in its place? The end of what was read
	// must still be there (a replacement larger than the old file would
	// otherwise show as the old text plus its tail).
	if v.loaded && size > have && have > 0 {
		k := min(have, int64(4096))
		tail := make([]byte, k)
		if n, err := f.ReadAt(tail, have-k); err != nil || !bytes.Equal(tail[:n], v.raw[have-k:]) {
			v.loaded = false
		}
	}
	if !v.loaded || size <= have {
		v.raw, have = v.raw[:0], 0
	}
	buf := make([]byte, size-have)
	n, err := f.ReadAt(buf, have)
	if err != nil && err != io.EOF {
		return "", err
	}
	v.raw = append(v.raw, buf[:n]...)
	v.text = history.CleanTranscript(string(v.raw))
	v.mod, v.loaded = st.ModTime(), true
	return v.text, nil
}

// OpenFolder shows the file in Explorer.
func (v *Viewer) OpenFolder() error { return openInExplorer(v.path) }

// Close ends the viewer (Esc).
func (v *Viewer) Close() { runtime.Quit(v.ctx) }
