package model

import (
	"strings"
	"testing"
	"time"
)

// The pause between pasted lines: serial waits the default unless set,
// -1 turns it off; the network waits only when asked; both are capped.
func TestPasteLineDelay(t *testing.T) {
	for _, c := range []struct {
		ser, net int
		conn     ConnMethod
		want     time.Duration
	}{
		{0, 0, ConnSerial, DefaultPasteLineDelayMs * time.Millisecond},
		{250, 0, ConnSerial, 250 * time.Millisecond},
		{PasteLineDelayNone, 0, ConnSerial, 0},
		{0, 0, ConnSSH, 0},
		{0, 0, ConnTelnet, 0},
		{0, 300, ConnSSH, 300 * time.Millisecond},
		{0, 300, ConnSerial, DefaultPasteLineDelayMs * time.Millisecond},
		{99999, 99999, ConnSSH, MaxPasteLineDelayMs * time.Millisecond},
	} {
		s := Settings{PasteLineDelayMs: c.ser, PasteLineDelayNetMs: c.net}
		if got := s.PasteLineDelay(c.conn); got != c.want {
			t.Errorf("ser=%d net=%d %s: %v, want %v", c.ser, c.net, c.conn, got, c.want)
		}
	}
	for _, s := range []Settings{{PasteLineDelayMs: -2}, {PasteLineDelayMs: 5001}, {PasteLineDelayNetMs: -1}, {PasteLineDelayNetMs: 5001}} {
		if s.ValidatePasteLineDelay() == nil {
			t.Errorf("%+v accepted", s)
		}
	}
	if err := (Settings{PasteLineDelayMs: -1, PasteLineDelayNetMs: 5000}).ValidatePasteLineDelay(); err != nil {
		t.Error(err)
	}
}

// The login wait is the command timeout (its default included) until set.
func TestLoginWait(t *testing.T) {
	for _, c := range []struct {
		login, cmd int
		want       time.Duration
	}{
		{0, 0, 30 * time.Second},
		{0, 45, 45 * time.Second},
		{10, 45, 10 * time.Second},
		{120, 45, 120 * time.Second},
	} {
		if got := (Settings{LoginTimeout: c.login, CommandTimeout: c.cmd}).LoginWait(); got != c.want {
			t.Errorf("login=%d cmd=%d: %v, want %v", c.login, c.cmd, got, c.want)
		}
	}
}

// The character set is read in every spelling a CSV or a cell may use,
// stored as "" / "sjis", and an unknown word is refused by Validate.
func TestDeviceEncoding(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "UTF-8": "", "utf8": "", "Shift_JIS": "sjis", "shift-jis": "sjis", "SJIS": "sjis",
		"CP932": "sjis", "Windows-31J": "sjis", " sjis ": "sjis",
	} {
		got, ok := NormalizeEncoding(in)
		if !ok || got != want {
			t.Errorf("NormalizeEncoding(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := NormalizeEncoding("euc-jp"); ok {
		t.Error("euc-jp accepted")
	}
	d := Device{Name: "lab-rt1", Host: "192.0.2.1", Conn: ConnSSH, OSType: "generic", Encoding: "Shift_JIS"}
	if err := d.Validate(); err != nil || d.Encoding != EncodingSJIS {
		t.Errorf("Validate: %v, encoding %q", err, d.Encoding)
	}
	d.Encoding = "euc-jp"
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "文字コードが不正です") {
		t.Errorf("euc-jp: %v", err)
	}
}
