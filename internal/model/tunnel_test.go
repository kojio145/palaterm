package model

import (
	"strings"
	"testing"
)

func TestValidateTunnelMode(t *testing.T) {
	hop := func(m BastionMethod) Bastion { return Bastion{Host: "192.0.2.1", Method: m, Username: "ops"} }
	cases := []struct {
		name string
		d    Device
		want string // "" = valid
	}{
		{"tunnel via ssh hops", Device{Name: "a", Host: "10.0.0.1", Conn: ConnSSH, BastionMode: BastionViaTunnel,
			Bastions: []Bastion{hop(BastionSSH), hop(BastionSSH)}}, ""},
		{"tunnel to telnet device", Device{Name: "a", Host: "10.0.0.1", Conn: ConnTelnet, BastionMode: BastionViaTunnel,
			Bastions: []Bastion{hop(BastionSSH)}}, ""},
		{"tunnel through telnet hop", Device{Name: "a", Host: "10.0.0.1", Conn: ConnSSH, BastionMode: BastionViaTunnel,
			Bastions: []Bastion{hop(BastionSSH), hop(BastionTelnet)}}, "すべて SSH"},
		{"tunnel to serial", Device{Name: "a", Conn: ConnSerial, BastionMode: BastionViaTunnel,
			Bastions: []Bastion{hop(BastionSSH)}}, "シリアル"},
		{"unknown mode", Device{Name: "a", Host: "10.0.0.1", Conn: ConnSSH, BastionMode: "warp"}, "通り方"},
		// Nothing is typed on a tunnel's hops, so a user name a shell would
		// choke on is fine there (and still refused when typed).
		{"tunnel user with space", Device{Name: "a", Host: "10.0.0.1", Conn: ConnSSH, Username: "a b", BastionMode: BastionViaTunnel,
			Bastions: []Bastion{hop(BastionSSH)}}, ""},
		{"shell user with space", Device{Name: "a", Host: "10.0.0.1", Conn: ConnSSH, Username: "a b",
			Bastions: []Bastion{hop(BastionSSH)}}, "ユーザー名"},
	}
	for _, c := range cases {
		err := c.d.Validate()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: want error with %q, got %v", c.name, c.want, err)
		}
	}
}

// Host keys behind a tunnel are pinned under the chain; through typed jumps
// (and directly) under the address alone, as before.
func TestHostKeyNames(t *testing.T) {
	d := Device{Host: "10.0.0.5", Conn: ConnSSH, Bastions: []Bastion{
		{Host: "192.0.2.1", Method: BastionSSH}, {Host: "10.0.0.2", Method: BastionSSH, Port: 2222}}}
	if got := strings.Join(d.HostKeyNames(), ","); got != "192.0.2.1:22,10.0.0.2:2222,10.0.0.5:22" {
		t.Errorf("shell: %s", got)
	}
	d.BastionMode = BastionViaTunnel
	if got := strings.Join(d.HostKeyNames(), ","); got != "192.0.2.1:22,192.0.2.1:22>10.0.0.2:2222,192.0.2.1:22>10.0.0.2:2222>10.0.0.5:22" {
		t.Errorf("tunnel: %s", got)
	}
	d.Bastions = nil
	if !strings.EqualFold(strings.Join(d.HostKeyNames(), ","), "10.0.0.5:22") || d.Tunneled() {
		t.Errorf("direct: %v tunneled=%v", d.HostKeyNames(), d.Tunneled())
	}
}
