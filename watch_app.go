package main

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Watcher is the backend of the screen window ("PalaTerm.exe --watch
// <file>"): a batch device's log replayed into a read-only terminal as it
// grows, the way a Tera Term macro's window shows what the macro is doing —
// so a device that has gone wrong is seen at once rather than at the end of
// the batch. It reads the one file the parent checked lies under the log
// folder; the log is masked as it is written (logstore.Stream), so the
// screen shows nothing the log would not. No vault, no password.
type Watcher struct {
	ctx   context.Context
	path  string
	title string
}

// watchChunk is the next part of the file from the offset asked for.
type watchChunk struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Data string `json:"data"` // base64: the bytes as the device sent them
	Off  int64  `json:"off"`  // offset after Data
	// ModUnixMs is when the file last grew (the page says how long the
	// device has been quiet).
	ModUnixMs int64  `json:"modUnixMs"`
	Err       string `json:"err,omitempty"`
}

// watchChunkMax bounds one read: a long log opened late catches up in a few
// calls instead of one huge event.
const watchChunkMax = 512 << 10

func NewWatcher(path, title string) *Watcher { return &Watcher{path: path, title: title} }

func (w *Watcher) startup(ctx context.Context) {
	w.ctx = ctx
	termWin.fit(ctx)
	placeNearParent(ctx, termWin)
}

func (w *Watcher) domReady(ctx context.Context) { go focusOwnWindow() }

// Read returns what the file holds from off on (at most watchChunkMax).
func (w *Watcher) Read(off int64) watchChunk {
	c := watchChunk{Name: filepath.Base(w.path), Path: w.path, Off: off}
	f, err := os.Open(w.path)
	if err != nil {
		c.Err = err.Error()
		return c
	}
	defer f.Close()
	// The buffer is what is there to read (polled every moment, mostly
	// nothing), the chunk at most.
	size := int64(watchChunkMax)
	if st, err := f.Stat(); err == nil {
		c.ModUnixMs = st.ModTime().UnixMilli()
		if st.Size() < off {
			// Not this file any more (replaced): start over.
			off = 0
		}
		size = min(size, st.Size()-off)
	}
	if size <= 0 {
		return c
	}
	buf := make([]byte, size)
	n, err := f.ReadAt(buf, off)
	if err != nil && err != io.EOF {
		c.Err = err.Error()
		return c
	}
	c.Data = base64.StdEncoding.EncodeToString(buf[:n])
	c.Off = off + int64(n)
	return c
}

// OpenLog opens the same file in the text log viewer (find, copy all).
func (w *Watcher) OpenLog() error { return spawnWindow(nil, "--view", w.path) }

// Close ends the window (Esc).
func (w *Watcher) Close() { runtime.Quit(w.ctx) }
