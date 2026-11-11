package session

import (
	"bytes"
	"io"
	"testing"

	"golang.org/x/text/encoding/japanese"
)

// pipeSession is a Session whose Read serves canned device output in the
// chunks given, and whose Write collects what the device would receive.
type pipeSession struct {
	chunks [][]byte
	wrote  bytes.Buffer
	closed bool
}

func (p *pipeSession) Read(b []byte) (int, error) {
	if len(p.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(b, p.chunks[0])
	if n < len(p.chunks[0]) {
		p.chunks[0] = p.chunks[0][n:]
	} else {
		p.chunks = p.chunks[1:]
	}
	return n, nil
}
func (p *pipeSession) Write(b []byte) (int, error) { return p.wrote.Write(b) }
func (p *pipeSession) Close() error                { p.closed = true; return nil }

func sjis(t *testing.T, s string) []byte {
	t.Helper()
	b, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A Shift_JIS device's output arrives as UTF-8, even when a two-byte
// character is split across two reads, and escape sequences pass through.
func TestTranscodeReadDecodesSplitSJIS(t *testing.T) {
	raw := sjis(t, "lab-rt1# \x1b[32m状態: 正常\x1b[0m\r\n")
	// Cut inside 状 (two bytes).
	cut := bytes.Index(raw, sjis(t, "状")) + 1
	p := &pipeSession{chunks: [][]byte{raw[:cut], raw[cut:]}}
	s := Transcode(p, "sjis")
	got, err := io.ReadAll(s)
	if err != nil {
		t.Fatal(err)
	}
	if want := "lab-rt1# \x1b[32m状態: 正常\x1b[0m\r\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// What is typed in UTF-8 reaches the device in Shift_JIS; a character cut
// off at the end of one Write is completed by the next; one Shift_JIS has
// no form for goes out as "?" instead of failing the write.
func TestTranscodeWriteEncodes(t *testing.T) {
	p := &pipeSession{}
	s := Transcode(p, "sjis")
	msg := []byte("description 東京本社\r")
	cut := bytes.Index(msg, []byte("京")) + 1 // inside 京 (three bytes)
	for _, part := range [][]byte{msg[:cut], msg[cut:]} {
		if n, err := s.Write(part); err != nil || n != len(part) {
			t.Fatalf("Write(%q) = %d, %v", part, n, err)
		}
	}
	if want := sjis(t, "description 東京本社\r"); !bytes.Equal(p.wrote.Bytes(), want) {
		t.Errorf("device got % x, want % x", p.wrote.Bytes(), want)
	}
	p.wrote.Reset()
	if _, err := s.Write([]byte("! 😀\r")); err != nil {
		t.Fatal(err)
	}
	if want := []byte("! ?\r"); !bytes.Equal(p.wrote.Bytes(), want) {
		t.Errorf("unsupported rune: got %q, want %q", p.wrote.Bytes(), want)
	}
}

// A UTF-8 device (the default) is the session itself: no wrapper, no
// change to the path every device took before.
func TestTranscodeUTF8IsIdentity(t *testing.T) {
	p := &pipeSession{}
	for _, enc := range []string{"", "utf-8", "unknown"} {
		if s := Transcode(p, enc); s != Session(p) {
			t.Errorf("Transcode(%q) wrapped the session", enc)
		}
	}
}

type fakeSerial struct{ pipeSession }

func (fakeSerial) SendBreak() error   { return nil }
func (fakeSerial) Describe() string   { return "COM9 @ 9600" }
func (fakeSerial) LineEnding() string { return "\r" }

type fakeSSH struct{ pipeSession }

func (fakeSSH) Resize(cols, rows int) error { return nil }
func (fakeSSH) LineEnding() string          { return "\r" }

// The wrapper has exactly the optional interfaces the transport has: the
// serial console still breaks and describes itself, SSH still resizes,
// Telnet gains nothing.
func TestTranscodeKeepsOptionalInterfaces(t *testing.T) {
	kinds := func(s Session) [4]bool {
		_, a := s.(Resizer)
		_, b := s.(Breaker)
		_, c := s.(Describer)
		_, d := s.(LineEnder)
		return [4]bool{a, b, c, d}
	}
	for _, inner := range []Session{&fakeSerial{}, &fakeSSH{}, &pipeSession{}} {
		w := Transcode(inner, "sjis")
		if w == inner {
			t.Fatalf("%T: not wrapped", inner)
		}
		if kinds(w) != kinds(inner) {
			t.Errorf("%T: wrapper interfaces %v, want %v", inner, kinds(w), kinds(inner))
		}
	}
	w := Transcode(&fakeSerial{}, "sjis")
	if w.(Describer).Describe() != "COM9 @ 9600" || w.(LineEnder).LineEnding() != "\r" {
		t.Error("forwarded Describe/LineEnding differ from the transport's")
	}
}
