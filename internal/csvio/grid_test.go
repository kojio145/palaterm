package csvio

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A device read back from its own row is the same device.
func TestGridCellsRoundTrip(t *testing.T) {
	d := model.Device{Name: "rt1", Group: "lab", Site: "本社", Host: "192.0.2.1", Conn: model.ConnSSH, Port: 2222,
		OSType: "cisco-ios", CommandSet: "bk", Enabled: true, UseGroupCreds: true,
		Username: "ops", Password: " pw ", EnablePassword: "en", AuthMethod: model.AuthPublicKey, KeyFile: `C:\k\id`, KeyPassphrase: "pp",
		BastionMode: model.BastionViaTunnel,
		Bastions: []model.Bastion{{Host: "192.0.2.10", Method: model.BastionSSH, Port: 22, Username: "j", Password: "jp", AuthMethod: model.AuthPassword, LegacyAlgos: true},
			{Host: "10.0.0.2", Method: model.BastionSSH, Username: "j2", JumpCommand: "ssh -l {user} {host}"}}}
	c := DeviceCells(d)
	back, errs := CellsDevice(c)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if !model.SameCells(DeviceCells(back), c) {
		t.Fatalf("round trip changed the row:\n%v\n%v", c, DeviceCells(back))
	}
	if back.Password != " pw " || back.Bastions[1].JumpCommand != "ssh -l {user} {host}" || !back.Bastions[0].LegacyAlgos {
		t.Errorf("device %+v", back)
	}
}

// Japanese words are read like the CSV's; anything else names the cell.
func TestGridCellsWordsAndErrors(t *testing.T) {
	d, errs := CellsDevice(map[string]string{"name": "a", "host": "192.0.2.1", "conn": "シリアル", "enabled": "いいえ",
		"authMethod": "公開鍵", "bastionMode": "SSHトンネル", "bastion1Host": "192.0.2.5", "bastion1Method": "SSH"})
	if len(errs) != 0 || d.Conn != model.ConnSerial || d.Enabled || d.AuthMethod != model.AuthPublicKey || d.BastionMode != model.BastionViaTunnel {
		t.Fatalf("%+v %v", d, errs)
	}
	_, errs = CellsDevice(map[string]string{"name": "a", "port": "x", "conn": "ftp", "enabled": "maybe",
		"authMethod": "otp", "bastionMode": "warp", "bastion2Username": "j", "bastion1Host": "h", "bastion1Method": "serial"})
	want := map[string]bool{"port": true, "conn": true, "enabled": true, "authMethod": true, "bastionMode": true, "bastion2Host": true, "bastion1Method": true}
	got := map[string]bool{}
	for _, e := range errs {
		got[e.Col] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("no error on %s (got %v)", k, errs)
		}
	}
	// An untouched hop (defaults only) is no hop.
	d, errs = CellsDevice(map[string]string{"name": "a", "host": "192.0.2.1", "bastion1Method": "ssh", "bastion1AuthMethod": "password", "bastion1LegacyAlgos": "false"})
	if len(errs) != 0 || len(d.Bastions) != 0 {
		t.Errorf("%+v %v", d.Bastions, errs)
	}
}

func TestErrorColumn(t *testing.T) {
	for msg, col := range map[string]string{
		"IPアドレスの形式が不正です: \"x\"":                       "host",
		"踏み台2段目のホスト形式が不正です: \"x\"":                    "bastion2Host",
		"SSH トンネルで通る踏み台はすべて SSH にしてください（3段目が telnet）": "bastion3Method",
		"名前は必須です":                     "name",
		"ポート番号が範囲外です: 70000（0〜65535）": "port",
		"シリアル接続の機器は踏み台を経由できません":       "conn",
	} {
		if got := ErrorColumn(msg); got != col {
			t.Errorf("%s → %s, want %s", msg, got, col)
		}
	}
}

// A draft saved by v1.7.5 or before has the GUI scheme https as "" in its
// Base; brought to the current spelling it must not read as a change.
func TestUpgradeBaseOldScheme(t *testing.T) {
	d := model.Device{Name: "rt1", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios"}
	now := DeviceCells(d)
	old := map[string]string{}
	for k, v := range now {
		old[k] = v
	}
	old["guiScheme"] = ""
	if model.SameCells(now, old) {
		t.Fatal("test premise: the old spelling must differ from the current one")
	}
	if !model.SameCells(now, UpgradeBase(old)) {
		t.Errorf("old draft base still reads as changed: %v vs %v", UpgradeBase(old)["guiScheme"], now["guiScheme"])
	}
	if UpgradeBase(nil) != nil {
		t.Error("nil base must stay nil")
	}
	http := map[string]string{"guiScheme": "http"}
	if UpgradeBase(http)["guiScheme"] != "http" {
		t.Error("http must stay http")
	}
}
