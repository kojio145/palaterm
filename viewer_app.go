package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
}

func NewViewer(path, title string) *Viewer {
	return &Viewer{path: path, title: title, live: os.Getenv("PALATERM_VIEW_LIVE") == "1"}
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

// spawnWindow starts another PalaTerm window process detached from this one.
func spawnWindow(env []string, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = windowEnv(env...)
	if err := cmd.Start(); err != nil {
		return err
	}
	tieToMainWindow(cmd.Process.Pid)
	trackChild(cmd.Process)
	go func() { _ = cmd.Wait() }()
	return nil
}

func (v *Viewer) startup(ctx context.Context) {
	v.ctx = ctx
	viewerWin.fit(ctx)
	placeNearParent(ctx, viewerWin)
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
