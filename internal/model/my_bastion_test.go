package model

import (
	"strings"
	"testing"
)

func myBastionInventory() *Inventory {
	return &Inventory{
		Devices: []Device{{Name: "fw1", Host: "192.0.2.10", Conn: ConnSSH, Username: "admin", Password: "pw", UseMyBastion: true,
			Bastions: []Bastion{{Host: "192.0.2.20", Method: BastionSSH, Username: "hop2", Password: "hp"}}}},
		MyBastions: []Bastion{{Host: "jump1.example", Method: BastionSSH, Username: "ops", AuthMethod: AuthPublicKey,
			KeyFile: `C:\keys\id_ed25519`, KeyPassphrase: "kp", LegacyAlgos: true}},
	}
}

func TestResolvePutsMyBastionFirst(t *testing.T) {
	inv := myBastionInventory()
	d := inv.ResolveCredentials(inv.Devices[0])
	hops := d.ActiveBastions()
	if len(hops) != 2 {
		t.Fatalf("hops = %+v, want own jump host + the device's hop", hops)
	}
	b := hops[0]
	if !b.Mine || b.Host != "jump1.example" || b.Username != "ops" || b.KeyFile != `C:\keys\id_ed25519` || b.KeyPassphrase != "kp" || !b.LegacyAlgos {
		t.Errorf("hop 1 = %+v, want the own jump host", b)
	}
	if hops[1].Host != "192.0.2.20" || hops[1].Username != "hop2" {
		t.Errorf("hop 2 = %+v", hops[1])
	}
	if d.UseMyBastion {
		t.Error("resolved copy still asks for the own jump host (would be added twice)")
	}
	if again := inv.ResolveCredentials(d); len(again.ActiveBastions()) != 2 {
		t.Errorf("resolving twice added it again: %d hops", len(again.ActiveBastions()))
	}
	if len(inv.Devices[0].Bastions) != 1 || !inv.Devices[0].UseMyBastion {
		t.Error("the vault's device was modified")
	}
}

func TestResolveWithoutMyBastionNeverGoesDirect(t *testing.T) {
	inv := myBastionInventory()
	inv.MyBastions = nil
	inv.Devices[0].Bastions = nil
	d := inv.ResolveCredentials(inv.Devices[0])
	hops := d.ActiveBastions()
	if len(hops) != 1 || !hops[0].Mine || hops[0].Host != "" {
		t.Errorf("hops = %+v, want a placeholder own hop (the dial refuses it)", hops)
	}
}

func TestResolveLeavesOtherDevicesAlone(t *testing.T) {
	inv := myBastionInventory()
	inv.Devices[0].UseMyBastion = false
	if d := inv.ResolveCredentials(inv.Devices[0]); len(d.ActiveBastions()) != 1 || d.ActiveBastions()[0].Mine {
		t.Errorf("own jump host added to a device not asking for it: %+v", d.ActiveBastions())
	}
}

func TestUsesBastionAndTunnel(t *testing.T) {
	d := Device{Name: "fw1", Host: "192.0.2.10", Conn: ConnSSH, UseMyBastion: true, BastionMode: BastionViaTunnel}
	if !d.UsesBastion() || !d.Tunneled() {
		t.Errorf("UsesBastion=%v Tunneled=%v, want both", d.UsesBastion(), d.Tunneled())
	}
	if err := d.Validate(); err != nil {
		t.Errorf("valid: %v", err)
	}
	d.Conn = ConnSerial
	if err := d.Validate(); err == nil {
		t.Error("serial device through the own jump host accepted")
	}
	d = Device{Name: "fw1", Host: "192.0.2.10", Conn: ConnSSH, UseMyBastion: true, Username: "a;reboot"}
	if err := d.Validate(); err == nil {
		t.Error("shell syntax in a username typed on the own jump host accepted")
	}
}

func TestWithoutBastionsDropsMyBastion(t *testing.T) {
	d := Device{Name: "fw1", Host: "192.0.2.10", Conn: ConnSSH, UseMyBastion: true, BastionMode: BastionViaTunnel,
		Bastions: []Bastion{{Host: "jump2.example", Method: BastionSSH}}}
	got := d.WithoutBastions()
	if got.UsesBastion() || got.Tunneled() {
		t.Errorf("still has jump hosts: %+v", got)
	}
	if !d.UseMyBastion || len(d.Bastions) != 1 {
		t.Error("original changed")
	}
}

func TestStripBastionCredentials(t *testing.T) {
	inv := myBastionInventory()
	d := inv.Devices[0]
	d.Bastions[0].KeyFile, d.Bastions[0].KeyPassphrase, d.Bastions[0].AuthMethod = "k", "kp", AuthPublicKey
	d.StripBastionCredentials()
	b := d.Bastions[0]
	if b.Username != "" || b.Password != "" || b.KeyFile != "" || b.KeyPassphrase != "" || b.AuthMethod != "" {
		t.Errorf("hop still has a login: %+v", b)
	}
	if b.Host != "192.0.2.20" || d.Username != "admin" || !d.UseMyBastion {
		t.Errorf("non-secret fields lost: %+v", d)
	}
	if inv.Devices[0].Bastions[0].Username != "hop2" {
		t.Error("the inventory's chain was modified (not copied)")
	}
}

func TestValidateMyBastions(t *testing.T) {
	ok := []Bastion{{Host: "jump1.example", Method: BastionSSH, Username: "ops"}, {Host: "192.0.2.30", Method: BastionTelnet}}
	if err := ValidateMyBastions(ok); err != nil {
		t.Errorf("valid: %v", err)
	}
	bad := map[string][]Bastion{
		"no user on hop 1": {{Host: "jump1.example", Method: BastionSSH}},
		"bad host":         {{Host: "a b", Method: BastionSSH, Username: "ops"}},
		"bad port":         {{Host: "jump1.example", Method: BastionSSH, Port: 70000, Username: "ops"}},
		"bad method":       {{Host: "jump1.example", Username: "ops"}},
		"shell user":       {{Host: "jump1.example", Method: BastionSSH, Username: "ops"}, {Host: "jump2.example", Method: BastionSSH, Username: "a;b"}},
		"unc key":          {{Host: "jump1.example", Method: BastionSSH, Username: "ops", AuthMethod: AuthPublicKey, KeyFile: `//srv/share/key`}},
		"six hops":         make([]Bastion, 6),
	}
	for name, c := range bad {
		if err := ValidateMyBastions(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A chain of own jump hosts goes in front, in order, and the device can
// still add its own five after it.
func TestResolveMyBastionChain(t *testing.T) {
	inv := myBastionInventory()
	inv.MyBastions = append(inv.MyBastions, Bastion{Host: "jump2.example", Method: BastionSSH, Username: "ops2"})
	own := make([]Bastion, MaxBastions)
	for i := range own {
		own[i] = Bastion{Host: "192.0.2." + string(rune('1'+i)), Method: BastionSSH, Username: "u"}
	}
	inv.Devices[0].Bastions = own
	if err := inv.Devices[0].Validate(); err != nil {
		t.Fatalf("five own hops with the own chain: %v", err)
	}
	r := inv.ResolveCredentials(inv.Devices[0])
	hops := r.ActiveBastions()
	if len(hops) != 2+MaxBastions || hops[0].Host != "jump1.example" || hops[1].Host != "jump2.example" || !hops[1].Mine || hops[2].Mine {
		t.Errorf("hops = %d: %+v", len(hops), hops)
	}
}

// v1.7.3 kept per-host logins and filled any hop left without one; those
// become the single own jump host, and the hops it filled become the flag.
func TestMigrateMyBastion(t *testing.T) {
	inv := &Inventory{
		Devices: []Device{
			{Name: "a", Bastions: []Bastion{{Host: "JUMP1.example", Method: BastionSSH}, {Host: "192.0.2.30", Method: BastionSSH, Username: "r"}}},
			{Name: "b", Bastions: []Bastion{{Host: "jump1.example", Method: BastionSSH, Username: "own"}}},
			{Name: "c", Bastions: []Bastion{{Host: "jump1.example", Port: 2222, Method: BastionSSH}}},
			{Name: "d"},
		},
		MyBastionLogins: []BastionLogin{{Host: "jump1.example", Username: "ops", AuthMethod: AuthPublicKey, KeyFile: `C:\k`, KeyPassphrase: "kp"}},
	}
	if !inv.MigrateMyBastion() {
		t.Fatal("nothing migrated")
	}
	if len(inv.MyBastions) != 1 || inv.MyBastions[0].Host != "jump1.example" || inv.MyBastions[0].Username != "ops" || inv.MyBastions[0].KeyFile != `C:\k` {
		t.Fatalf("MyBastions = %+v", inv.MyBastions)
	}
	a := inv.Devices[0]
	if !a.UseMyBastion || len(a.Bastions) != 1 || a.Bastions[0].Host != "192.0.2.30" {
		t.Errorf("a = %+v, want the flag and its second hop kept", a)
	}
	if inv.Devices[1].UseMyBastion || len(inv.Devices[1].Bastions) != 1 {
		t.Error("b had its own login: must stay as it was")
	}
	if inv.Devices[2].UseMyBastion {
		t.Error("c is on another port: must stay as it was")
	}
	if inv.Devices[3].UseMyBastion {
		t.Error("d has no jump host")
	}
	if inv.MyBastionLogins != nil || inv.MigrateMyBastion() {
		t.Error("migrated twice")
	}
}

// Every v1.7.3 login survives the move: the first becomes the own jump host
// (dropping only first hops that lose nothing), the others are written into
// the hops they filled.
func TestMigrateMyBastionKeepsEveryLogin(t *testing.T) {
	inv := &Inventory{
		Devices: []Device{
			{Name: "a", Bastions: []Bastion{{Host: "jump1.example", Method: BastionSSH}, {Host: "jump2.example", Method: BastionSSH}}},
			{Name: "b", Bastions: []Bastion{{Host: "jump1.example", Method: BastionSSH, JumpCommand: "ssh -l {user} {host}"}}},
		},
		MyBastionLogins: []BastionLogin{{Host: "jump1.example", Username: "ops"}, {Host: "jump2.example", Username: "ops2", Password: "p2"}},
	}
	inv.MigrateMyBastion()
	a := inv.Devices[0]
	if !a.UseMyBastion || len(a.Bastions) != 1 || a.Bastions[0].Username != "ops2" || a.Bastions[0].Password != "p2" {
		t.Errorf("a = %+v, want own jump host + hop 2 with the second login", a)
	}
	b := inv.Devices[1]
	if b.UseMyBastion || len(b.Bastions) != 1 || b.Bastions[0].Username != "ops" || b.Bastions[0].JumpCommand == "" {
		t.Errorf("b = %+v, want its hop (jump command kept) filled in place", b)
	}
}

// A device tunnelling through the own jump hosts needs them all SSH: said
// when the device is saved (ValidateIn), not on every connection.
func TestValidateInRefusesTunnelThroughTelnetOwnHop(t *testing.T) {
	inv := &Inventory{MyBastions: []Bastion{{Host: "jump1.example", Method: BastionTelnet}}}
	d := Device{Name: "fw1", Host: "192.0.2.10", Conn: ConnSSH, UseMyBastion: true, BastionMode: BastionViaTunnel}
	if err := inv.ValidateIn(&d); err == nil || !strings.Contains(err.Error(), "Telnet") {
		t.Errorf("tunnel through a Telnet own hop accepted: %v", err)
	}
	d.BastionMode = BastionViaShell
	if err := inv.ValidateIn(&d); err != nil {
		t.Errorf("typing on the hop is fine: %v", err)
	}
	d.BastionMode, d.CLIOff, d.GUIOn = BastionViaTunnel, true, true
	if err := inv.ValidateIn(&d); err != nil {
		t.Errorf("a GUI-only device never tunnels: %v", err)
	}
}

// A GUI is opened at the device's address: a device using one needs it.
func TestValidateGUINeedsHost(t *testing.T) {
	d := Device{Name: "gui1", Conn: ConnSerial, SerialPort: "COM3", GUIOn: true}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "IPアドレス") {
		t.Errorf("GUI without an address accepted: %v", err)
	}
	d.Host = "192.0.2.5"
	if err := d.Validate(); err != nil {
		t.Errorf("serial CLI + GUI at an address: %v", err)
	}
	d.GUIOn = false
	d.Host = ""
	if err := d.Validate(); err != nil {
		t.Errorf("a serial device without a GUI needs no address: %v", err)
	}
}
