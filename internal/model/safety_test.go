package model

import "testing"

// Values typed into a jump host's shell, and key paths that would send the
// user's Windows credentials to a remote server, are refused.
func TestValidateShellAndUNC(t *testing.T) {
	base := func() Device {
		return Device{Name: "sw1", Host: "192.0.2.1", Conn: ConnSSH, OSType: "cisco-ios"}
	}
	hop := Bastion{Host: "192.0.2.9", Method: BastionSSH, Username: "jump"}
	for _, tc := range []struct {
		name string
		mod  func(d *Device)
		bad  bool
	}{
		{"plain", func(d *Device) {}, false},
		{"odd user without a jump host is the device's business", func(d *Device) { d.Username = "a b" }, false},
		{"user behind a jump host", func(d *Device) { d.Bastions = []Bastion{hop}; d.Username = "admin" }, false},
		{"dotted user behind a jump host", func(d *Device) { d.Bastions = []Bastion{hop}; d.Username = "ops.user-1@corp" }, false},
		{"semicolon behind a jump host", func(d *Device) { d.Bastions = []Bastion{hop}; d.Username = "x;reboot" }, true},
		{"command substitution", func(d *Device) { d.Bastions = []Bastion{hop}; d.Username = "$(id)" }, true},
		{"space", func(d *Device) { d.Bastions = []Bastion{hop}; d.Username = "a b" }, true},
		{"second hop user", func(d *Device) { h2 := hop; h2.Username = "u|sh"; d.Bastions = []Bastion{hop, h2} }, true},
		{"first hop user is not typed in a shell", func(d *Device) { h := hop; h.Username = "a b"; d.Bastions = []Bastion{h} }, false},
		{"UNC key", func(d *Device) { d.KeyFile = `\\evil\share\id_rsa` }, true},
		{"slash UNC key", func(d *Device) { d.KeyFile = "//evil/share/id_rsa" }, true},
		{"local key", func(d *Device) { d.KeyFile = `C:\Users\me\.ssh\id_rsa` }, false},
		{"mixed-separator UNC key", func(d *Device) { d.KeyFile = `\/evil/share/k` }, true},
		{"mixed-separator UNC key 2", func(d *Device) { d.KeyFile = `/\evil\share\k` }, true},
		{"NT path key", func(d *Device) { d.KeyFile = `\??\UNC\evil\share\k` }, true},
		{"file URL key", func(d *Device) { d.KeyFile = "file://evil/share/k" }, true},
		{"UNC key on a hop", func(d *Device) { h := hop; h.KeyFile = `\\evil\k`; d.Bastions = []Bastion{h} }, true},
	} {
		d := base()
		tc.mod(&d)
		if err := d.Validate(); (err != nil) != tc.bad {
			t.Errorf("%s: err = %v, want bad=%v", tc.name, err, tc.bad)
		}
	}
}
