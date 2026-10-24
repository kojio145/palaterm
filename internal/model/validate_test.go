package model

import (
	"strings"
	"testing"
)

func TestDeviceValidate(t *testing.T) {
	base := func() Device {
		return Device{Name: "sw1", Host: "10.0.0.1", Conn: ConnSSH}
	}
	cases := []struct {
		name    string
		mutate  func(*Device)
		wantErr bool
	}{
		{"valid ipv4", func(d *Device) {}, false},
		{"valid hostname", func(d *Device) { d.Host = "core-sw1.lab.example.com" }, false},
		{"valid ipv6", func(d *Device) { d.Host = "2001:db8::1" }, false},
		{"empty name", func(d *Device) { d.Name = "" }, true},
		{"empty host ssh", func(d *Device) { d.Host = "" }, true},
		{"bad ipv4 too big", func(d *Device) { d.Host = "192.168.1111.1" }, true},
		{"bad ipv4 octet", func(d *Device) { d.Host = "10.0.0.999" }, true},
		{"bad ipv4 parts", func(d *Device) { d.Host = "1.2.3.4.5" }, true},
		{"port out of range", func(d *Device) { d.Port = 70000 }, true},
		{"port valid", func(d *Device) { d.Port = 2222 }, false},
		{"serial needs no host", func(d *Device) { d.Conn = ConnSerial; d.Host = "" }, false},
		{"bad conn", func(d *Device) { d.Conn = "carrierpigeon" }, true},
		{"host with spaces", func(d *Device) { d.Host = "10.0.0.1 x" }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := base()
			c.mutate(&d)
			err := d.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate()=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestBastionValidation(t *testing.T) {
	d := Device{Name: "x", Host: "10.0.0.1", Conn: ConnSSH,
		Bastions: []Bastion{{Method: BastionSSH, Host: "192.168.1111.1"}}}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for malformed bastion host")
	}
}

// Addresses are saved trimmed: " 192.0.2.10" passed the checks and then
// failed the name lookup on connecting.
func TestValidateTrimsHosts(t *testing.T) {
	d := Device{Name: "sw1", Host: " 192.0.2.10\t", Conn: ConnSSH,
		Bastions: []Bastion{{Method: BastionSSH, Host: " jump1.lab.example ", Username: "ops"}}}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if d.Host != "192.0.2.10" {
		t.Errorf("Host = %q, want trimmed", d.Host)
	}
	if d.Bastions[0].Host != "jump1.lab.example" {
		t.Errorf("bastion Host = %q, want trimmed", d.Bastions[0].Host)
	}
	d = Device{Name: "sw1", Host: "   ", Conn: ConnSSH}
	if err := d.Validate(); err == nil {
		t.Error("a blank-only host accepted")
	}
}

// A console-cable device goes nowhere through a jump host, whichever way
// the hops are crossed: typed on the hop's shell (the default), tunneled,
// or the user's own jump host. Only a GUI-only device keeps its hidden,
// unchecked hop fields.
func TestSerialRefusesAnyBastion(t *testing.T) {
	hop := func(m BastionMethod) Bastion { return Bastion{Host: "192.0.2.1", Method: m, Username: "ops"} }
	cases := []struct {
		name string
		d    Device
		want bool // refused
	}{
		{"serial alone", Device{Name: "con1", Conn: ConnSerial, SerialPort: "COM3"}, false},
		{"serial behind ssh hop typed", Device{Name: "con1", Conn: ConnSerial, Bastions: []Bastion{hop(BastionSSH)}}, true},
		{"serial behind telnet hop typed", Device{Name: "con1", Conn: ConnSerial, Bastions: []Bastion{hop(BastionTelnet)}}, true},
		{"serial behind tunnel", Device{Name: "con1", Conn: ConnSerial, BastionMode: BastionViaTunnel, Bastions: []Bastion{hop(BastionSSH)}}, true},
		{"serial through own jump host", Device{Name: "con1", Conn: ConnSerial, UseMyBastion: true}, true},
		{"serial legacy single bastion", Device{Name: "con1", Conn: ConnSerial, Bastion: &Bastion{Host: "192.0.2.1", Method: BastionSSH}}, true},
		{"gui-only serial keeps hidden hop", Device{Name: "con1", Conn: ConnSerial, Host: "192.0.2.5", CLIOff: true, GUIOn: true, Bastions: []Bastion{hop(BastionSSH)}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := c.d
			err := d.Validate()
			if c.want && (err == nil || !strings.Contains(err.Error(), "シリアル接続の機器は踏み台を経由できません")) {
				t.Fatalf("want refusal, got %v", err)
			}
			if !c.want && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBastionEffectivePort(t *testing.T) {
	cases := []struct {
		b    Bastion
		want int
	}{
		{Bastion{Method: BastionSSH}, 22},
		{Bastion{Method: BastionTelnet}, 23},
		{Bastion{Method: BastionSSH, Port: 2222}, 2222},
		{Bastion{Method: BastionTelnet, Port: 2323}, 2323},
		{Bastion{}, 22}, // the own jump host placeholder, SSH by default
	}
	for _, c := range cases {
		if got := c.b.EffectivePort(); got != c.want {
			t.Errorf("%+v: EffectivePort() = %d, want %d", c.b, got, c.want)
		}
	}
}

// "The device was replaced" drops the device's own pin and nothing else: a
// jump host's key changing is not a replaced device, and is refused by name
// so the user learns which hop it was.
func TestReplacedHostKeyNames(t *testing.T) {
	d := Device{Name: "sw1", Host: "192.0.2.10", Conn: ConnSSH, BastionMode: BastionViaTunnel, Bastions: []Bastion{
		{Host: "192.0.2.1", Method: BastionSSH, Mine: true},
		{Host: "192.0.2.2", Method: BastionSSH, Port: 2222},
	}}
	own := "192.0.2.1:22>192.0.2.2:2222>192.0.2.10:22"
	if got := d.OwnHostKeyName(); got != own {
		t.Fatalf("OwnHostKeyName() = %q", got)
	}
	for _, mismatched := range []string{"", own, "203.0.113.9:22"} {
		names, err := d.ReplacedHostKeyNames(mismatched)
		if err != nil || strings.Join(names, ",") != own {
			t.Errorf("mismatched %q: names %v, err %v; want just the own pin", mismatched, names, err)
		}
	}
	names, err := d.ReplacedHostKeyNames("192.0.2.1:22")
	if err == nil || names != nil || !strings.Contains(err.Error(), "自分の踏み台（192.0.2.1:22）") {
		t.Errorf("own jump host mismatch: names %v, err %v", names, err)
	}
	names, err = d.ReplacedHostKeyNames("192.0.2.1:22>192.0.2.2:2222")
	if err == nil || names != nil || !strings.Contains(err.Error(), "踏み台2段目（192.0.2.1:22>192.0.2.2:2222）") {
		t.Errorf("device hop mismatch: names %v, err %v", names, err)
	}
	// Typed hops are pinned under their bare address; a Telnet hop takes 23.
	d.BastionMode = BastionViaShell
	d.Bastions[1].Method, d.Bastions[1].Port = BastionTelnet, 0
	if got := strings.Join(d.HostKeyNames(), ","); got != "192.0.2.1:22,192.0.2.2:23,192.0.2.10:22" {
		t.Errorf("shell names = %s", got)
	}
	if names, err := d.ReplacedHostKeyNames("192.0.2.2:23"); err == nil || names != nil {
		t.Errorf("typed hop mismatch: names %v, err %v", names, err)
	}
}

// The per-field checks read the same for the first device and for a unit,
// apart from the unit's prefix and its need for a source.
func TestImportRuleFieldChecks(t *testing.T) {
	rule := func(f ImportField, units ...ImportUnit) *ImportRule {
		return &ImportRule{Name: "r", HeaderRow: 1,
			Fields: []ImportField{{Key: "name", Source: "column", Column: "Name"}, f}, Units: units}
	}
	cases := []struct {
		name string
		r    *ImportRule
		want string
	}{
		{"default only is fine", rule(ImportField{Key: "osType", Default: "cisco-ios"}), ""},
		{"bad source kind", rule(ImportField{Key: "host", Source: "magic"}), "項目「host」の元の値の種類が不正です"},
		{"column without column", rule(ImportField{Key: "host", Source: "column"}), "項目「host」の元の列を選んでください"},
		{"template without text", rule(ImportField{Key: "host", Source: "template"}), "項目「host」の組み立てを入力してください"},
		{"unit needs a source", rule(ImportField{Key: "host", Source: "column", Column: "IP"},
			ImportUnit{Fields: []ImportField{{Key: "name", Default: "x"}}}), "2 台目の項目「name」の元の値を選んでください"},
		{"unit column without column", rule(ImportField{Key: "host", Source: "column", Column: "IP"},
			ImportUnit{Fields: []ImportField{{Key: "name", Source: "column"}}}), "2 台目の項目「name」の元の列を選んでください"},
		{"unit ok", rule(ImportField{Key: "host", Source: "column", Column: "IP"},
			ImportUnit{Fields: []ImportField{{Key: "name", Source: "template", Template: "{Name}-2"}}}), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.r.Validate()
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("unexpected error %v", err)
			case c.want != "" && (err == nil || err.Error() != c.want):
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}

func TestResolveCredentials(t *testing.T) {
	inv := &Inventory{
		DeviceGroups: []DeviceGroup{{Name: "A社", Username: "gu", Password: "gp", EnablePassword: "ge"}},
		Devices: []Device{
			{Name: "shared", Group: "A社", Username: "du", Password: "dp", UseGroupCreds: true},
			{Name: "own", Group: "A社", Username: "du", Password: "dp"},
			{Name: "orphan", Group: "Z社", Username: "du", Password: "dp", UseGroupCreds: true},
		},
	}
	if r := inv.ResolveCredentials(inv.Devices[0]); r.Username != "gu" || r.Password != "gp" || r.EnablePassword != "ge" {
		t.Errorf("group creds not applied: %+v", r)
	}
	if r := inv.ResolveCredentials(inv.Devices[1]); r.Username != "du" || r.Password != "dp" {
		t.Errorf("own creds replaced: %+v", r)
	}
	if r := inv.ResolveCredentials(inv.Devices[2]); r.Username != "du" {
		t.Errorf("missing group must keep own creds: %+v", r)
	}
	if inv.Devices[0].Username != "du" {
		t.Errorf("ResolveCredentials must not mutate the inventory")
	}
}

func TestAutoLockDefaults(t *testing.T) {
	if (Settings{}).EffectiveAutoLockMin() != DefaultAutoLockMin {
		t.Errorf("missing setting must default to %d", DefaultAutoLockMin)
	}
	if (Settings{AutoLockMin: 5}).EffectiveAutoLockMin() != 5 {
		t.Errorf("explicit minutes ignored")
	}
	if (Settings{AutoLockMin: 5, AutoLockOff: true}).EffectiveAutoLockMin() != 0 {
		t.Errorf("off must give 0")
	}
}
