package model

import "testing"

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
