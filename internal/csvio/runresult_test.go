package csvio

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// The failure list carries the device, how it was reached and the error, in
// a file Excel opens as UTF-8 — and never a password.
func TestExportRunResults(t *testing.T) {
	rows := []RunResult{
		{Device: model.Device{Name: "core-sw1", Group: "lab", Site: "HQ", Role: "L3",
			Host: "192.0.2.10", Conn: model.ConnSSH, Port: 22, OSType: "nec-qx",
			Username: "ops", Password: "secret-pw", EnablePassword: "secret-en",
			Bastions: []model.Bastion{{Host: "192.0.2.201", Method: model.BastionSSH, Password: "secret-jump"}, {Host: "192.0.2.202", Method: model.BastionSSH}}},
			Status: "失敗", Message: "接続できません: i/o timeout"},
		{Device: model.Device{Name: "con-1", Conn: model.ConnSerial, SerialPort: "COM3", Baud: 9600},
			Status: "失敗", Message: "=cmd"},
	}
	text, err := ExportRunResults(rows, "ja")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, BOM) {
		t.Error("no BOM: Excel would read the file as Shift_JIS")
	}
	for _, sec := range []string{"secret-pw", "secret-en", "secret-jump", "ops"} {
		if strings.Contains(text, sec) {
			t.Errorf("credential %q in the failure list", sec)
		}
	}
	recs, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, BOM))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("rows = %d, want header + 2", len(recs))
	}
	want := []string{"core-sw1", "lab", "HQ", "L3", "192.0.2.10", "SSH", "22", "", "nec-qx",
		"192.0.2.201 → 192.0.2.202", "失敗", "接続できません: i/o timeout"}
	if strings.Join(recs[1], "|") != strings.Join(want, "|") {
		t.Errorf("row = %q\nwant  %q", recs[1], want)
	}
	if recs[0][0] != "機器名" || recs[0][len(recs[0])-1] != "メッセージ" {
		t.Errorf("header = %q", recs[0])
	}
	// Serial: no port number, the COM port instead; the formula is defused.
	if recs[2][5] != "シリアル" || recs[2][6] != "" || recs[2][7] != "COM3" || recs[2][11] != "'=cmd" {
		t.Errorf("serial row = %q", recs[2])
	}
	en, _ := ExportRunResults(rows[:1], "en")
	if !strings.Contains(en, "name,group,site") || !strings.Contains(en, ",ssh,") {
		t.Errorf("English export: %q", en)
	}
}
