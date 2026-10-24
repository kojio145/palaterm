package logstore

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Stream writes a session's transcript to its log file as the bytes arrive,
// the way Tera Term does, while still masking the device's passwords: the
// interactive window's log, and each device's log in a batch (so a batch cut
// short by quitting, a power cut or a sleep still leaves what it had read).
//
// Masking is the one thing that keeps this from being a plain append: a
// secret (a password echoed back, or sitting in a "show running-config") can
// arrive split across two reads, and masking each read on its own would write
// both halves in clear. So the last hold bytes of what has arrived — one
// short of the longest secret — stay in memory, and if a complete secret
// straddles that boundary the boundary moves back to its start. Everything
// before the boundary can then be masked and appended knowing no secret
// crosses it. Once the device has been quiet for idleFlush, and on an
// explicit Flush (the log viewer opening), the held tail is written too —
// all but a tail that reads as the start of a secret, which stays back until
// the rest of it (or anything else) arrives: a password that has half
// arrived must not go to the file in clear with its other half appended
// later. Close and a pause write everything. So the file is never more than
// a second behind, except for those few bytes.
type Stream struct {
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
	closed  bool
}

// idleFlush is how long the device must be quiet before the held tail is
// written out too.
const idleFlush = time.Second

// NewStream prepares a log at path (the file is created on the first
// byte, so a session that produced nothing leaves no empty file). Secrets
// shorter than 4 bytes are not masked, as in RedactSecrets.
func NewStream(path string, secrets []string) *Stream {
	l := &Stream{path: path}
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
func (l *Stream) SetPaused(paused bool) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// After Close there is nothing to pause: a marker would reopen the file
	// and leak the handle.
	if paused == l.paused || l.closed {
		return l.pathLocked(), l.err
	}
	l.flushLocked(0)
	l.paused = paused
	if l.written > 0 {
		mark := "\r\n[PalaTerm: log resumed %s]\r\n"
		if paused {
			mark = "\r\n[PalaTerm: log paused %s]\r\n"
		}
		l.buf = append(l.buf, []byte(fmt.Sprintf(mark, time.Now().Format("15:04:05")))...)
		l.flushLocked(0)
	}
	return l.pathLocked(), l.err
}

// Paused reports whether recording is stopped.
func (l *Stream) Paused() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.paused
}

// Write takes the next chunk of transcript.
func (l *Stream) Write(b []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Bytes the session's reader delivers after Close (the last of the
	// logout) must not reopen the file. Once a write has failed the rest of
	// the session is not kept either (see flushLocked).
	if l.paused || l.closed || l.err != nil {
		return
	}
	l.buf = append(l.buf, b...)
	l.flushLocked(l.hold)
	if l.timer == nil {
		l.timer = time.AfterFunc(idleFlush, func() { l.Flush() })
	} else {
		l.timer.Reset(idleFlush)
	}
}

// Flush writes everything received so far, held tail included — except a
// tail that could be the start of a secret still arriving — and returns the
// file path ("" while nothing has been written).
func (l *Stream) Flush() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushLocked(l.secretStart())
	return l.pathLocked(), l.err
}

// Close flushes and closes the file.
func (l *Stream) Close() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.timer != nil {
		l.timer.Stop()
	}
	l.flushLocked(0)
	l.closed = true
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	return l.pathLocked(), l.err
}

func (l *Stream) pathLocked() string {
	if l.written == 0 {
		return ""
	}
	return l.path
}

// secretStart is how many bytes at the end of buf read as the beginning of
// a secret (a proper prefix of one): the longest such tail, 0 when the tail
// is nothing a secret starts with. Written out now, those bytes would stand
// in the file in clear while the rest of the secret is still on its way.
func (l *Stream) secretStart() int {
	keep := 0
	for _, s := range l.secrets {
		for k := len(s) - 1; k > keep; k-- {
			if k <= len(l.buf) && bytes.Equal(l.buf[len(l.buf)-k:], s[:k]) {
				keep = k
				break
			}
		}
	}
	return keep
}

// cut returns how many bytes of buf can be written now: everything but the
// last keep bytes, pulled back to the start of any secret occurrence that
// would otherwise be split by the boundary.
func (l *Stream) cut(keep int) int {
	cut := len(l.buf) - keep
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

// flushLocked writes buf up to the cut (see cut) for keep. After a failed
// write nothing more is kept: the file cannot take it, and holding the rest
// of the session in memory only made every later cut rescan it.
func (l *Stream) flushLocked(keep int) {
	if l.err != nil {
		l.buf = nil
		return
	}
	n := l.cut(keep)
	if n <= 0 {
		return
	}
	out := l.buf[:n]
	for _, s := range l.secrets {
		out = bytes.ReplaceAll(out, s, []byte("****"))
	}
	if l.f == nil {
		if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
			l.fail(err)
			return
		}
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			l.fail(err)
			return
		}
		l.f = f
	}
	if _, err := l.f.Write(out); err != nil {
		l.fail(err)
		return
	}
	l.written += n
	l.buf = append(l.buf[:0], l.buf[n:]...)
}

// fail records the first error and drops what was waiting to be written.
func (l *Stream) fail(err error) {
	l.err = err
	l.buf = nil
}
