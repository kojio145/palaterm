package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// streamLog writes an interactive session's transcript to its log file as the
// bytes arrive, the way Tera Term does, while still masking the device's
// passwords the way the batch logs do.
//
// Masking is the one thing that keeps this from being a plain append: a
// secret (a password echoed back, or sitting in a "show running-config") can
// arrive split across two reads, and masking each read on its own would write
// both halves in clear. So the last hold bytes of what has arrived — one
// short of the longest secret — stay in memory, and if a complete secret
// straddles that boundary the boundary moves back to its start. Everything
// before the boundary can then be masked and appended knowing no secret
// crosses it. The held tail is written once the device has been quiet for
// idleFlush (nothing is half-way through arriving by then), on an explicit
// Flush, and on Close — so the file is never more than a second behind.
type streamLog struct {
	mu      sync.Mutex
	path    string
	secrets [][]byte
	hold    int
	buf     []byte // received, not yet written
	f       *os.File
	timer   *time.Timer
	written int
	err     error
	paused  bool
}

// idleFlush is how long the device must be quiet before the held tail is
// written out too.
const idleFlush = time.Second

// newStreamLog prepares a log at path (the file is created on the first
// byte, so a session that produced nothing leaves no empty file). Secrets
// shorter than 4 bytes are not masked, as in RedactSecrets.
func newStreamLog(path string, secrets []string) *streamLog {
	l := &streamLog{path: path}
	for _, s := range secrets {
		if len(s) >= 4 {
			l.secrets = append(l.secrets, []byte(s))
			if len(s)-1 > l.hold {
				l.hold = len(s) - 1
			}
		}
	}
	return l
}

// SetPaused stops (true) or resumes (false) recording. Pausing first writes
// out everything received so far, so the file is complete up to the moment
// of the click; output that arrives while paused is not recorded. A marker
// line notes each stop and start so a gap in the file is not mistaken for
// the device having said nothing.
func (l *streamLog) SetPaused(paused bool) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if paused == l.paused {
		return l.pathLocked(), l.err
	}
	l.flushLocked(true)
	l.paused = paused
	if l.written > 0 {
		mark := "\r\n[PalaTerm: log resumed %s]\r\n"
		if paused {
			mark = "\r\n[PalaTerm: log paused %s]\r\n"
		}
		l.buf = append(l.buf, []byte(fmt.Sprintf(mark, time.Now().Format("15:04:05")))...)
		l.flushLocked(true)
	}
	return l.pathLocked(), l.err
}

// Paused reports whether recording is stopped.
func (l *streamLog) Paused() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.paused
}

// Write takes the next chunk of transcript.
func (l *streamLog) Write(b []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.paused {
		return
	}
	l.buf = append(l.buf, b...)
	l.flushLocked(false)
	if l.timer == nil {
		l.timer = time.AfterFunc(idleFlush, func() { l.Flush() })
	} else {
		l.timer.Reset(idleFlush)
	}
}

// Flush writes everything received so far, held tail included, and returns
// the file path ("" while nothing has been written).
func (l *streamLog) Flush() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushLocked(true)
	return l.pathLocked(), l.err
}

// Close flushes and closes the file.
func (l *streamLog) Close() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.timer != nil {
		l.timer.Stop()
	}
	l.flushLocked(true)
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	return l.pathLocked(), l.err
}

func (l *streamLog) pathLocked() string {
	if l.written == 0 {
		return ""
	}
	return l.path
}

// cut returns how many bytes of buf can be written now: all of them when all
// is set, otherwise everything but the held tail, pulled back to the start of
// any secret occurrence that would otherwise be split by the boundary.
func (l *streamLog) cut(all bool) int {
	cut := len(l.buf)
	if !all {
		cut -= l.hold
	}
	if cut <= 0 {
		return 0
	}
	for changed := true; changed; {
		changed = false
		for _, s := range l.secrets {
			for from := 0; from < cut; {
				i := bytes.Index(l.buf[from:], s)
				if i < 0 {
					break
				}
				p := from + i
				if p < cut && p+len(s) > cut {
					cut = p
					changed = true
				}
				from = p + 1
			}
		}
	}
	return cut
}

func (l *streamLog) flushLocked(all bool) {
	n := l.cut(all)
	if n <= 0 || l.err != nil {
		return
	}
	out := l.buf[:n]
	for _, s := range l.secrets {
		out = bytes.ReplaceAll(out, s, []byte("****"))
	}
	if l.f == nil {
		if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
			l.err = err
			return
		}
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			l.err = err
			return
		}
		l.f = f
	}
	if _, err := l.f.Write(out); err != nil {
		l.err = err
		return
	}
	l.written += n
	l.buf = append(l.buf[:0], l.buf[n:]...)
}
