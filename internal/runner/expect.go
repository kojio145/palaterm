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

// Expecter is the handle callers outside this package hold on a session
// returned by Connect or Open: something to pass back into StartInteractive
// or Close, nothing more.
type Expecter = expecter

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

	lastData time.Time // when the last byte arrived (creation time until then)

	// eol ends a line sent to this transport. CRLF for Telnet; CR for SSH and
	// console, which reads CR and LF as two separate Enters.
	eol string

	newData chan struct{}
}

func newExpecter(sess session.Session) *expecter {
	e := &expecter{sess: sess, newData: make(chan struct{}, 1), eol: "\r\n", lastData: time.Now()}
	if le, ok := sess.(session.LineEnder); ok {
		e.eol = le.LineEnding()
	}
	go e.readLoop()
	return e
}

func (e *expecter) readLoop() {
	tmp := make([]byte, 4096)
	for {
		n, err := e.sess.Read(tmp)
		if n > 0 {
			e.mu.Lock()
			e.lastData = time.Now()
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

// Seen reports whether pattern is currently present in the unconsumed output,
// without consuming it. It lets a caller notice that the device is already at
// its operational prompt while it is still waiting for some other pattern.
func (e *expecter) Seen(pattern string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return re.MatchString(e.buf.String())
}

// Drain discards any pending unmatched output, so the next Expect only sees
// data that arrives afterward. The full transcript is unaffected. This clears
// stale prompts left by prior commands before a new command is sent.
func (e *expecter) Drain() {
	e.mu.Lock()
	e.buf.Reset()
	e.mu.Unlock()
}

// Idle reports how long the far end has been silent.
func (e *expecter) Idle() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return time.Since(e.lastData)
}

// Send writes s followed by this transport's line ending.
func (e *expecter) Send(s string) error {
	_, err := e.sess.Write([]byte(s + e.eol))
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

// TakeTranscript returns the captured output so far and forgets it, in one
// step, so nothing arriving in between is lost. The interactive terminal
// uses it when a failed automatic login is handed over to the user: the
// transcript was already shown on screen with the error, so StartRaw must
// not replay it there — but it still belongs in the session log, which the
// caller writes from the returned string.
func (e *expecter) TakeTranscript() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.transcript.String()
	e.transcript.Reset()
	return s
}

func (e *expecter) Close() error {
	return e.sess.Close()
}

// Transport names the open transport when it can describe itself (a serial
// session: "COM3 @ 9600"); "" otherwise.
func (e *expecter) Transport() string {
	if d, ok := e.sess.(session.Describer); ok {
		return d.Describe()
	}
	return ""
}

// Resize forwards a terminal resize to the underlying session if it supports
// one (SSH); otherwise it is a no-op.
func (e *expecter) Resize(cols, rows int) error {
	if r, ok := e.sess.(session.Resizer); ok {
		return r.Resize(cols, rows)
	}
	return nil
}
