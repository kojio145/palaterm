package session

import (
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// Transcode wraps sess so that what the device sends in enc arrives as
// UTF-8 and what is written in UTF-8 reaches the device in enc. enc is a
// model.Device.Encoding value: "" or "utf-8" returns sess itself — a UTF-8
// device takes the path it always took — and "sjis" converts Shift_JIS
// (CP932, the Windows superset) both ways. An unknown value is treated as
// UTF-8 rather than refused: the device list is validated on save, and a
// stale value must not keep a window from opening.
//
// Everything above the session — the expecter's prompt matching, the
// terminal window, the saved log, the comparison — then works in UTF-8 and
// needs no knowledge of the device's character set. The wrapper forwards
// the optional interfaces (Resizer, Breaker, Describer, LineEnder) exactly
// when the wrapped session has them.
func Transcode(sess Session, enc string) Session {
	var cm encoding.Encoding
	switch enc {
	case "sjis":
		cm = japanese.ShiftJIS
	default:
		return sess
	}
	t := &transcoded{
		Session: sess,
		r:       transform.NewReader(sess, cm.NewDecoder()),
		cm:      cm,
		enc:     cm.NewEncoder(),
	}
	return wrapOptional(t, sess)
}

// transcoded is a Session whose Read decodes and whose Write encodes.
type transcoded struct {
	Session
	r   io.Reader
	cm  encoding.Encoding
	enc *encoding.Encoder
	// tail holds the bytes of a UTF-8 sequence cut off at the end of a
	// Write: the next Write completes it. Keys come one at a time, but a
	// multi-byte character can arrive in two chunks from a relayed paste.
	tail []byte
}

func (t *transcoded) Read(p []byte) (int, error) { return t.r.Read(p) }

func (t *transcoded) Write(p []byte) (int, error) {
	n := len(p)
	b := p
	if len(t.tail) > 0 {
		b = append(t.tail, p...)
		t.tail = nil
	}
	// Keep an incomplete trailing sequence for the next Write.
	if cut := incompleteTail(b); cut > 0 {
		t.tail = append([]byte(nil), b[len(b)-cut:]...)
		b = b[:len(b)-cut]
	}
	if len(b) == 0 {
		return n, nil
	}
	out, err := t.enc.Bytes(b)
	if err != nil {
		// A rune the character set has no form for (an emoji in a
		// description), or a byte that is not UTF-8: it goes out as "?".
		// Not the encoder's own replacement, 0x1A — a device reads that
		// as Ctrl+Z, which on an IOS ends configuration mode.
		out, err = t.enc.Bytes(replaceUnencodable(t.cm, b))
		if err != nil {
			return 0, err
		}
	}
	if _, err := t.Session.Write(out); err != nil {
		return 0, err
	}
	return n, nil
}

// replaceUnencodable returns b with every rune cm cannot encode, and every
// byte that is not UTF-8, replaced by "?". ASCII is never touched.
func replaceUnencodable(cm encoding.Encoding, b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		switch {
		case r < utf8.RuneSelf && !(r == utf8.RuneError && n == 1):
			out = append(out, b[i])
		case r == utf8.RuneError && n == 1:
			out = append(out, '?')
		default:
			if _, err := cm.NewEncoder().Bytes(b[i : i+n]); err != nil {
				out = append(out, '?')
			} else {
				out = append(out, b[i:i+n]...)
			}
		}
		i += n
	}
	return out
}

// incompleteTail is the length of a UTF-8 sequence that starts within the
// last three bytes of b and does not end in b; 0 when b ends cleanly (or
// ends in bytes that are not UTF-8 at all, which go out as they are).
func incompleteTail(b []byte) int {
	for i := 1; i <= 3 && i <= len(b); i++ {
		c := b[len(b)-i]
		if c&0xC0 == 0x80 { // continuation byte: keep looking for the lead
			continue
		}
		if c&0x80 == 0 { // ASCII: the tail is complete
			return 0
		}
		// A lead byte: complete when the sequence it starts fits in i bytes.
		if utf8.FullRune(b[len(b)-i:]) {
			return 0
		}
		return i
	}
	return 0
}

// The optional interfaces are forwarded only when the wrapped session has
// them, so a caller's type assertion keeps telling the truth (a serial
// console is still not a Resizer; an SSH session still cannot Break).

type tResize struct{ *transcoded }

func (s tResize) Resize(cols, rows int) error { return s.Session.(Resizer).Resize(cols, rows) }

type tBreak struct{ *transcoded }

func (s tBreak) SendBreak() error { return s.Session.(Breaker).SendBreak() }

type tDescribe struct{ *transcoded }

func (s tDescribe) Describe() string { return s.Session.(Describer).Describe() }

type tLineEnd struct{ *transcoded }

func (s tLineEnd) LineEnding() string { return s.Session.(LineEnder).LineEnding() }

type tDescribeLineEnd struct{ *transcoded }

func (s tDescribeLineEnd) Describe() string   { return s.Session.(Describer).Describe() }
func (s tDescribeLineEnd) LineEnding() string { return s.Session.(LineEnder).LineEnding() }

type tBreakDescribeLineEnd struct{ *transcoded }

func (s tBreakDescribeLineEnd) SendBreak() error   { return s.Session.(Breaker).SendBreak() }
func (s tBreakDescribeLineEnd) Describe() string   { return s.Session.(Describer).Describe() }
func (s tBreakDescribeLineEnd) LineEnding() string { return s.Session.(LineEnder).LineEnding() }

type tResizeLineEnd struct{ *transcoded }

func (s tResizeLineEnd) Resize(cols, rows int) error { return s.Session.(Resizer).Resize(cols, rows) }
func (s tResizeLineEnd) LineEnding() string          { return s.Session.(LineEnder).LineEnding() }

// wrapOptional picks the wrapper type with exactly inner's optional
// interfaces. The transports today: SSH = Resizer (+ LineEnder), serial =
// Breaker + Describer + LineEnder, Telnet = none or LineEnder; the single
// combinations cover a future transport that has only one.
func wrapOptional(t *transcoded, inner Session) Session {
	_, resize := inner.(Resizer)
	_, brk := inner.(Breaker)
	_, desc := inner.(Describer)
	_, le := inner.(LineEnder)
	switch {
	case brk && desc && le:
		return tBreakDescribeLineEnd{t}
	case desc && le:
		return tDescribeLineEnd{t}
	case resize && le:
		return tResizeLineEnd{t}
	case resize:
		return tResize{t}
	case brk:
		return tBreak{t}
	case desc:
		return tDescribe{t}
	case le:
		return tLineEnd{t}
	}
	return t
}
