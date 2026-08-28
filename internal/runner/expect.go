package runner

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kojio145/palaterm/internal/session"
)

// ErrExpectTimeout means the pattern was not seen before the deadline.
var ErrExpectTimeout = errors.New("expect timeout")

// expecter reads a session in the background, mirrors everything into a
// transcript, and lets callers wait for regexps against the live output.
type expecter struct {
	sess session.Session

	mu         sync.Mutex
	buf        strings.Builder // accumulated output since last match
	transcript strings.Builder // full session log
	closed     bool
	readErr    error

	rawSink func([]byte) // when set, incoming bytes are forwarded here (interactive mode)
	onClose func()       // called once when the read loop ends (session closed)

	newData chan struct{}
}

func newExpecter(sess session.Session) *expecter {
	e := &expecter{sess: sess, newData: make(chan struct{}, 1)}
	go e.readLoop()
	return e
}

func (e *expecter) readLoop() {
	tmp := make([]byte, 4096)
	for {
		n, err := e.sess.Read(tmp)
		if n > 0 {
			e.mu.Lock()
			sink := e.rawSink
			if sink == nil {
				e.buf.Write(tmp[:n])
				e.transcript.Write(tmp[:n])
			}
			e.mu.Unlock()
			if sink != nil {
				// Interactive mode: forward bytes straight to the UI terminal.
				chunk := make([]byte, n)
				copy(chunk, tmp[:n])
				sink(chunk)
			} else {
				select {
				case e.newData <- struct{}{}:
				default:
				}
			}
		}
		if err != nil {
			e.mu.Lock()
			e.closed = true
			e.readErr = err
			oc := e.onClose
			e.mu.Unlock()
			select {
			case e.newData <- struct{}{}:
			default:
			}
			if oc != nil {
				oc()
			}
			return
		}
	}
}

// Expect waits until pattern matches the accumulated output or the timeout
// elapses. On match, the buffer is consumed up to and including the match so
// the next Expect starts fresh.
func (e *expecter) Expect(ctx context.Context, pattern string, timeout time.Duration) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		e.mu.Lock()
		cur := e.buf.String()
		if loc := re.FindStringIndex(cur); loc != nil {
			remaining := cur[loc[1]:]
			e.buf.Reset()
			e.buf.WriteString(remaining)
			e.mu.Unlock()
			return nil
		}
		closed := e.closed
		e.mu.Unlock()

		if closed {
			return ErrExpectTimeout
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return ErrExpectTimeout
		case <-e.newData:
		}
	}
}

// Drain discards any pending unmatched output, so the next Expect only sees
// data that arrives afterward. The full transcript is unaffected. This clears
// stale prompts left by prior commands before a new command is sent.
func (e *expecter) Drain() {
	e.mu.Lock()
	e.buf.Reset()
	e.mu.Unlock()
}

// Send writes s followed by CRLF.
func (e *expecter) Send(s string) error {
	_, err := e.sess.Write([]byte(s + "\r\n"))
	return err
}

// SendRaw writes bytes verbatim (for control characters).
func (e *expecter) SendRaw(s string) error {
	_, err := e.sess.Write([]byte(s))
	return err
}

// StartRaw switches the expecter into interactive pass-through mode: the login
// transcript captured so far is flushed to sink (so the terminal opens showing
// the login banner and current prompt), and every subsequent byte is forwarded
// to sink instead of being buffered for Expect.
func (e *expecter) StartRaw(sink func([]byte)) {
	e.mu.Lock()
	pending := e.transcript.String()
	e.buf.Reset()
	e.rawSink = sink
	e.mu.Unlock()
	if pending != "" {
		sink([]byte(pending))
	}
}

// Transcript returns the full captured output so far.
func (e *expecter) Transcript() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.transcript.String()
}

func (e *expecter) Close() error {
	return e.sess.Close()
}

// Resize forwards a terminal resize to the underlying session if it supports
// one (SSH); otherwise it is a no-op.
func (e *expecter) Resize(cols, rows int) error {
	if r, ok := e.sess.(session.Resizer); ok {
		return r.Resize(cols, rows)
	}
	return nil
}
