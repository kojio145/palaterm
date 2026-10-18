package runner

import (
	"errors"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// A refused enable password leaves the device at "Password:" after the
// profile sent {enable}: named as such, not as a prompt timeout.
func TestEnableRefusedHint(t *testing.T) {
	prof := profile.Profile{Login: []profile.Step{
		{Expect: "sername:", Send: "{user}"}, {Expect: "assword:", Send: "{password}"},
		{Expect: ">", Send: "enable"}, {Expect: "Password:", Send: "{enable}"},
	}}
	if h := enableRefusedHint(prof, "Router>enable\r\nPassword: \r\nPassword: "); h == "" || !strings.Contains(h, "enable") {
		t.Errorf("no hint at a second Password: prompt: %q", h)
	}
	if h := enableRefusedHint(prof, "Router#"); h != "" {
		t.Errorf("hint at the operational prompt: %q", h)
	}
	noEnable := profile.Profile{Login: []profile.Step{{Expect: "assword:", Send: "{password}"}}}
	if h := enableRefusedHint(noEnable, "Password: "); h != "" {
		t.Errorf("hint for a profile that never sends {enable}: %q", h)
	}
}

// A failed dial says in plain words what to check, with the raw error kept.
func TestDialHint(t *testing.T) {
	dev := &model.Device{Name: "sw1", Host: "192.0.2.11", Conn: model.ConnSSH, Port: 22}
	cases := []struct{ err, want string }{
		{"ssh dial 192.0.2.11:22: dial tcp 192.0.2.11:22: i/o timeout", "応答がありません"},
		{"telnet dial 192.0.2.11:23: dial tcp 192.0.2.11:23: connectex: No connection could be made because the target machine actively refused it.", "拒否されました"},
		{"ssh dial sw1.example:22: dial tcp: lookup sw1.example: no such host", "ホスト名を解決できません"},
		{"dial tcp 10.0.0.1:22: connectex: A socket operation was attempted to an unreachable network.", "経路がありません"},
	}
	for _, c := range cases {
		h := dialHint(errors.New(c.err), dev, nil)
		if !strings.Contains(h, c.want) || !strings.Contains(h, "192.0.2.11") && !strings.Contains(c.err, "lookup") {
			t.Errorf("dialHint(%q) = %q", c.err, h)
		}
	}
	if h := dialHint(errors.New("ssh: handshake failed: ssh: unable to authenticate"), dev, nil); h != "" {
		t.Errorf("hint for an auth failure: %q", h)
	}
	serial := &model.Device{Name: "c1", Conn: model.ConnSerial}
	if h := dialHint(errors.New("open serial COM3 @ 9600: i/o timeout"), serial, nil); h != "" {
		t.Errorf("hint for a serial port: %q", h)
	}
	hops := []model.Bastion{{Host: "jump1", Method: model.BastionSSH}}
	if h := dialHint(errors.New("dial tcp: i/o timeout"), dev, hops); !strings.Contains(h, "踏み台1段目（jump1）") {
		t.Errorf("hint through a jump host names the hop: %q", h)
	}
}
