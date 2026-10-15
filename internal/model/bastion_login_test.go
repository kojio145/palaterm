package model

import "testing"

func loginInventory() *Inventory {
	return &Inventory{
		Devices: []Device{{Name: "fw1", Host: "192.0.2.10", Conn: ConnSSH, Username: "admin", Password: "pw",
			Bastions: []Bastion{
				{Host: "JUMP1.example", Method: BastionSSH},
				{Host: "192.0.2.20", Method: BastionSSH, Username: "hop2", Password: "hp"},
			}}},
		MyBastionLogins: []BastionLogin{
			{Host: "jump1.example", Username: "ops", AuthMethod: AuthPublicKey, KeyFile: `C:\keys\id_ed25519`, KeyPassphrase: "kp"},
			{Host: "192.0.2.20", Username: "mine", Password: "x"},
		},
	}
}

func TestResolveFillsEmptyHopsFromOwnLogins(t *testing.T) {
	inv := loginInventory()
	d := inv.ResolveCredentials(inv.Devices[0])
	b := d.Bastions[0]
	if b.Username != "ops" || b.AuthMethod != AuthPublicKey || b.KeyFile != `C:\keys\id_ed25519` || b.KeyPassphrase != "kp" {
		t.Errorf("hop 1 = %+v, want the own login (host matched case-insensitively)", b)
	}
	if d.Bastions[1].Username != "hop2" || d.Bastions[1].Password != "hp" {
		t.Errorf("hop 2 = %+v: a hop with its own username must keep it", d.Bastions[1])
	}
	if d.Username != "admin" || d.Password != "pw" {
		t.Errorf("device login changed: %+v", d)
	}
	if inv.Devices[0].Bastions[0].Username != "" {
		t.Error("the vault's device was modified (chain not copied)")
	}
}

func TestResolveMatchesPort(t *testing.T) {
	inv := loginInventory()
	inv.Devices[0].Bastions[0].Port = 2222
	if d := inv.ResolveCredentials(inv.Devices[0]); d.Bastions[0].Username != "" {
		t.Errorf("login for :22 applied to :2222: %+v", d.Bastions[0])
	}
	inv.MyBastionLogins[0].Port = 2222
	if d := inv.ResolveCredentials(inv.Devices[0]); d.Bastions[0].Username != "ops" {
		t.Errorf("login for :2222 not applied: %+v", d.Bastions[0])
	}
}

func TestResolveSkipsTelnetHops(t *testing.T) {
	inv := loginInventory()
	inv.Devices[0].Bastions[0].Method = BastionTelnet
	if d := inv.ResolveCredentials(inv.Devices[0]); d.Bastions[0].Username != "" {
		t.Errorf("own SSH login applied to a Telnet hop: %+v", d.Bastions[0])
	}
}

func TestResolveLegacySingleBastion(t *testing.T) {
	inv := loginInventory()
	inv.Devices[0].Bastions = nil
	inv.Devices[0].Bastion = &Bastion{Host: "jump1.example", Method: BastionSSH}
	d := inv.ResolveCredentials(inv.Devices[0])
	if len(d.Bastions) != 1 || d.Bastions[0].Username != "ops" {
		t.Errorf("legacy bastion not filled: %+v", d.Bastions)
	}
}

func TestWithoutBastions(t *testing.T) {
	d := Device{Name: "fw1", Host: "192.0.2.10", Conn: ConnSSH, BastionMode: BastionViaTunnel,
		Bastions: []Bastion{{Host: "jump1.example", Method: BastionSSH}},
		Bastion:  &Bastion{Host: "old", Method: BastionSSH}}
	got := d.WithoutBastions()
	if len(got.ActiveBastions()) != 0 || got.Tunneled() || got.BastionMode != BastionViaShell {
		t.Errorf("still has jump hosts: %+v", got)
	}
	if len(d.Bastions) != 1 {
		t.Error("original changed")
	}
}

func TestStripBastionCredentials(t *testing.T) {
	inv := loginInventory()
	d := inv.Devices[0]
	d.Bastions[0].Username, d.Bastions[0].KeyFile, d.Bastions[0].KeyPassphrase, d.Bastions[0].AuthMethod = "ops", "k", "kp", AuthPublicKey
	d.Bastions[0].JumpCommand, d.Bastions[0].LegacyAlgos = "ssh -l {user} {host}", true
	d.StripBastionCredentials()
	for i, b := range d.Bastions {
		if b.Username != "" || b.Password != "" || b.KeyFile != "" || b.KeyPassphrase != "" || b.AuthMethod != "" {
			t.Errorf("hop %d still has a login: %+v", i+1, b)
		}
	}
	if d.Bastions[0].Host != "JUMP1.example" || d.Bastions[0].JumpCommand == "" || !d.Bastions[0].LegacyAlgos {
		t.Errorf("non-secret hop fields lost: %+v", d.Bastions[0])
	}
	if d.Username != "admin" || d.Password != "pw" {
		t.Errorf("device login must stay: %+v", d)
	}
	if inv.Devices[0].Bastions[0].Username != "ops" {
		t.Error("the inventory's chain was modified (not copied)")
	}
}

func TestValidateBastionLogins(t *testing.T) {
	ok := []BastionLogin{{Host: "jump1.example", Username: "ops"}, {Host: "jump1.example", Port: 2222, Username: "ops"}}
	if err := ValidateBastionLogins(ok); err != nil {
		t.Errorf("valid: %v", err)
	}
	bad := map[string][]BastionLogin{
		"duplicate":  {{Host: "jump1.example", Username: "a"}, {Host: "JUMP1.example", Port: 22, Username: "b"}},
		"no user":    {{Host: "jump1.example"}},
		"shell user": {{Host: "jump1.example", Username: "a;reboot"}},
		"bad host":   {{Host: "a b", Username: "a"}},
		"bad port":   {{Host: "jump1.example", Port: 70000, Username: "a"}},
		"unc key":    {{Host: "jump1.example", Username: "a", AuthMethod: AuthPublicKey, KeyFile: `//srv/share/key`}},
	}
	for name, ls := range bad {
		if err := ValidateBastionLogins(ls); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
