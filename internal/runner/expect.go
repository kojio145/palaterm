package runner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kojio145/palaterm/internal/session"
)

// ErrExpectTimeout means the pattern was not seen before the deadline.
var ErrExpectTimeout = errors.New("expect timeout")

// ErrClosed means the session ended before the pattern was seen: nothing
// more is coming, so waiting out the timeout would only spin — and a
// timeout's advice (タイムアウトを延ばす) would send the user the wrong way.
var ErrClosed = errors.New("機器との接続が切れました")

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
	transcript strings.Builder // session log (only its tail once keepTail is set)
	closed     bool

	// keepTail, when set, bounds the transcript to about its last keepTail
	// bytes: a batch device whose live log keeps the full output need not
	// also hold hours of "show tech" in memory. dropped counts the bytes cut
	// from the front, so Mark/Since offsets stay absolute; head keeps the
	// first headKeep bytes after the latest Mark (a command's echo and the
	// device's first answer, which rejectedLine reads).
	keepTail int
	dropped  int
	headMark int
	head     strings.Builder
	readErr    error

	rawSink func([]byte) // when set, incoming bytes are forwarded here (interactive mode)
	tee     func([]byte) // when set, also gets every byte the transcript gets (a batch device's live log)
	onClose func()       // called once when the read loop ends (session closed)

	lastData time.Time // when the last byte arrived (creation time until then)

	// eol ends a line sent to this transport. CRLF for Telnet; CR for SSH and
	// console, which reads CR and LF as two separate Enters.
	eol string

	newData chan struct{}
}

func newExpecter(sess session.Session) *expecter {
	return newExpecterTee(sess, nil)
}

// newExpecterTee is newExpecter with tee set before the first read, so the
// copy misses nothing. tee is called from the read loop, in order.
func newExpecterTee(sess session.Session, tee func([]byte)) *expecter {
	e := &expecter{sess: sess, newData: make(chan struct{}, 1), eol: "\r\n", lastData: time.Now(), tee: tee}
	if le, ok := sess.(session.LineEnder); ok {
		e.eol = le.LineEnding()
	}
	go e.readLoop()
	return e
}

func (e *expecter) readLoop() {
	// A panic here (in a tee or a sink) would take the whole program down,
	// every other device and terminal with it: it ends this session instead,
	// which the waits then report as a closed connection.
	defer func() {
		if p := recover(); p != nil {
			e.mu.Lock()
			e.closed = true
			e.readErr = fmt.Errorf("内部エラー: %v", p)
			e.mu.Unlock()
			select {
			case e.newData <- struct{}{}:
			default:
			}
			_ = e.sess.Close()
		}
	}()
	tmp := make([]byte, 4096)
	for {
		n, err := e.sess.Read(tmp)
		if n > 0 {
			e.mu.Lock()
			e.lastData = time.Now()
			sink := e.rawSink
			if sink == nil {
				e.buf.Write(tmp[:n])
				e.record(tmp[:n])
			}
			e.mu.Unlock()
			if sink == nil && e.tee != nil {
				e.tee(tmp[:n])
			}
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
			return ErrClosed
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

// Pending returns the output not yet consumed by an Expect (or a Drain).
func (e *expecter) Pending() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buf.String()
}

// Closed reports whether the session has ended (nothing more will arrive).
func (e *expecter) Closed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closed
}

// headKeep is how much of a command's output Since still has after the
// transcript's front was cut: the echo and the first lines of the answer.
const headKeep = 4 << 10

// record appends b to the transcript (caller holds mu), cutting it back to
// its tail when keepTail is set. The cut waits until the transcript is twice
// keepTail, so the copy happens once per keepTail bytes, not on every read.
func (e *expecter) record(b []byte) {
	if e.keepTail > 0 {
		if room := headKeep - e.head.Len(); room > 0 {
			if len(b) < room {
				room = len(b)
			}
			e.head.Write(b[:room])
		}
	}
	e.transcript.Write(b)
	if e.keepTail <= 0 || e.transcript.Len() <= 2*e.keepTail {
		return
	}
	s := e.transcript.String()
	cut := len(s) - e.keepTail
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	e.transcript.Reset()
	e.transcript.WriteString(s[cut:])
	e.dropped += cut
}

// KeepTail bounds the transcript to about its last n bytes from now on (see
// keepTail). Mark and Since keep working across the cuts.
func (e *expecter) KeepTail(n int) {
	e.mu.Lock()
	e.keepTail = n
	e.mu.Unlock()
}

// Mark is the transcript's current end, for Since: what arrives from here on
// is, say, one command's output.
func (e *expecter) Mark() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.headMark = e.dropped + e.transcript.Len()
	e.head.Reset()
	return e.headMark
}

// Since returns the output that arrived after mark. When the transcript's
// front has been cut since (KeepTail), it is the start of that output (its
// first headKeep bytes) and the kept tail, joined by a line holding "…".
func (e *expecter) Since(mark int) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.transcript.String()
	if off := mark - e.dropped; off >= 0 {
		if off > len(s) {
			return ""
		}
		return s[off:]
	}
	if mark == e.headMark && e.head.Len() > 0 {
		return e.head.String() + "\n…\n" + s
	}
	return s
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

// StartRawShown is StartRaw for a session whose output so far is already on
// screen (streamed by a tee while the login ran): that part goes to logged
// only, everything after it to sink. Taken and switched in one step, so no
// byte is shown twice or lost in between.
func (e *expecter) StartRawShown(sink, logged func([]byte)) {
	e.mu.Lock()
	pending := e.transcript.String()
	e.dropped += len(pending)
	e.transcript.Reset()
	e.buf.Reset()
	e.rawSink = sink
	e.mu.Unlock()
	if pending != "" && logged != nil {
		logged([]byte(pending))
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
	e.dropped += len(s)
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

// Break sends a serial break when the transport is a serial line.
func (e *expecter) Break() error {
	if b, ok := e.sess.(session.Breaker); ok {
		return b.SendBreak()
	}
	return fmt.Errorf("ブレーク信号はシリアル接続でのみ送れます")
}

// Resize forwards a terminal resize to the underlying session if it supports
// one (SSH); otherwise it is a no-op.
func (e *expecter) Resize(cols, rows int) error {
	if r, ok := e.sess.(session.Resizer); ok {
		return r.Resize(cols, rows)
	}
	return nil
}
