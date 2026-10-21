package listimport

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

func TestNormalizeChoice(t *testing.T) {
	for _, c := range []struct{ key, in, want string }{
		{"conn", "SSH", "ssh"}, {"conn", "SSH2", "ssh"}, {"conn", "ＳＳＨ", "ssh"}, {"conn", "SSHv2", "ssh"},
		{"conn", "TELNET", "telnet"}, {"conn", "Telnet(23)", "telnet"}, {"conn", "SSH/TELNET", "ssh"},
		{"conn", "CONSOLE", "serial"}, {"conn", "コンソール", "serial"}, {"conn", "COM3", "serial"}, {"conn", "シリアル", "serial"},
		{"conn", "", ""}, {"conn", "RDP", "RDP"},
		{"bastion1Method", "TELNET", "telnet"},
		{"authMethod", "PASSWORD", "password"}, {"authMethod", "公開鍵認証", "publickey"},
		{"enabled", "YES", "true"}, {"enabled", "有", "true"}, {"enabled", "無効", "false"}, {"enabled", "Ｎｏ", "false"},
		{"guiScheme", "HTTPS", "https"}, {"guiScheme", "HTTP", "http"},
		{"port", "２２", "22"}, {"port", " 22 ", "22"},
		{"host", "ＣＯＲＥ", "ＣＯＲＥ"}, // free text: as it is
	} {
		if got := normalizeChoice(c.key, c.in); got != c.want {
			t.Errorf("normalizeChoice(%s, %q) = %q, want %q", c.key, c.in, got, c.want)
		}
	}
}

// pairSheet lists redundant pairs: primary and backup in their own
// columns, the backup's address also given as "primary/backup" in one cell.
func pairSheet() *sheet.Sheet {
	return &sheet.Sheet{Rows: [][]string{
		{"資産番号", "ホスト名(主)", "ホスト名(副)", "IP(主/副)", "接続", "拠点"},
		{"N-1", "fw01a", "fw01b", "192.0.2.1/192.0.2.2", "SSH2", "本社"},
		{"N-2", "core01", "", "192.0.2.11", "TELNET", "本社"}, // no backup
		{"N-3", "", "edge01b", "/192.0.2.22", "SSH", "支社"},  // backup only
	}}
}

func pairRule() *model.ImportRule {
	return &model.ImportRule{
		Name: "pairs", HeaderRow: 1,
		Fields: []model.ImportField{
			{Key: "name", Source: "column", Column: "ホスト名(主)"},
			{Key: "host", Source: "column", Column: "IP(主/副)", Steps: []model.ImportStep{{Type: "split", Sep: "/", Pos: "first", Keep: "left"}}},
			{Key: "conn", Source: "column", Column: "接続"},
			{Key: "site", Source: "column", Column: "拠点"},
		},
		Units: []model.ImportUnit{{Label: "バックアップ", Fields: []model.ImportField{
			{Key: "name", Source: "column", Column: "ホスト名(副)"},
			{Key: "host", Source: "column", Column: "IP(主/副)", Steps: []model.ImportStep{{Type: "split", Sep: "/", Pos: "first", Keep: "right", Miss: "empty"}}},
		}}},
		Match: model.ImportMatch{IgnoreCase: true, Width: true, Space: true, KeyColumn: "資産番号"},
	}
}

func TestPairsFromOneRow(t *testing.T) {
	if err := ValidateRule(pairRule()); err != nil {
		t.Fatal(err)
	}
	c := convert(t, pairSheet(), pairRule())
	type dev struct{ name, host, conn, site, label string }
	var got []dev
	for _, r := range c.Rows {
		if r.Excluded != "" {
			got = append(got, dev{name: "(excluded:" + r.Excluded + ")"})
			continue
		}
		got = append(got, dev{r.Values["name"], r.Values["host"], r.Values["conn"], r.Values["site"], r.Label})
	}
	want := []dev{
		{"fw01a", "192.0.2.1", "ssh", "本社", ""},
		{"fw01b", "192.0.2.2", "ssh", "本社", "バックアップ"}, // conn and site as the first device's
		{"core01", "192.0.2.11", "telnet", "本社", ""},
		{name: "(excluded:機器名が空です)"},
		{"edge01b", "192.0.2.22", "ssh", "支社", "バックアップ"},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if c.Rows[1].ID == c.Rows[0].ID || c.Rows[1].Line != 2 || c.Rows[1].Unit != 2 {
		t.Errorf("backup row ids: %+v", c.Rows[1])
	}

	// Imported, then imported again: both of the pair are found again (the
	// asset number is shared, the unit tells them apart).
	inv := lab()
	p := BuildPlan(inv, c, pairRule(), Options{Group: "拠点A"})
	if p.Counts["add"] != 4 {
		t.Fatalf("counts = %v", p.Counts)
	}
	Apply(inv, p, "拠点A", nil)
	if rec := inv.ImportRecords[dev2(inv, "fw01b").ID]; rec.Key != "N-1#2" {
		t.Errorf("backup key = %+v", rec)
	}
	s := pairSheet()
	s.Rows[1][2] = "fw01-backup" // the backup renamed at the source
	p = BuildPlan(inv, convert(t, s, pairRule()), pairRule(), Options{Group: "拠点A"})
	var r2 *PlanRow
	for i := range p.Rows {
		if p.Rows[i].Line == 2 && p.Rows[i].Unit == 2 {
			r2 = &p.Rows[i]
		}
	}
	if r2 == nil || r2.MatchBy != "key" || r2.Status != "upd" || r2.Label != "バックアップ" {
		t.Errorf("backup re-import: %+v", r2)
	}
	if p.Counts["same"] != 3 {
		t.Errorf("counts = %v", p.Counts)
	}
	// A choice for the backup goes by its id, not its line.
	p = BuildPlan(inv, convert(t, s, pairRule()), pairRule(), Options{Group: "拠点A", Decisions: map[int]Decision{UnitID(2, 2): {Link: "skip"}}})
	for _, r := range p.Rows {
		if r.Line == 2 && r.Unit == 1 && r.Status != "same" {
			t.Errorf("the primary followed the backup's choice: %+v", r)
		}
		if r.Line == 2 && r.Unit == 2 && r.Status != "skip" {
			t.Errorf("backup choice ignored: %+v", r)
		}
	}
}

func dev2(inv *model.Inventory, name string) *model.Device {
	for i := range inv.Devices {
		if inv.Devices[i].Name == name {
			return &inv.Devices[i]
		}
	}
	return &model.Device{}
}

// Two devices of one line with the same name: the message names the row
// and its unit.
func TestPairNameClashLabel(t *testing.T) {
	s := pairSheet()
	s.Rows[1][2] = "fw01a"
	p := BuildPlan(lab(), convert(t, s, pairRule()), pairRule(), Options{Group: "拠点A"})
	for _, r := range p.Rows {
		if r.Line == 2 && r.Unit == 2 {
			if r.Status != "err" || !strings.Contains(strings.Join(r.Errors, ""), "「fw01a」が 2 行目と重なっています") {
				t.Errorf("clash: %+v", r)
			}
		}
	}
	if got := RowLabel(12, 2, "バックアップ"); got != "12（バックアップ）" {
		t.Error(got)
	}
	if got := RowLabel(12, 3, ""); got != "12-3" {
		t.Error(got)
	}
}

func TestUnitValidationAndExport(t *testing.T) {
	r := pairRule()
	r.Units[0].Fields = r.Units[0].Fields[1:] // no name of its own
	if err := ValidateRule(r); err == nil || !strings.Contains(err.Error(), "2 台目の機器名") {
		t.Errorf("unit without a name: %v", err)
	}
	r = pairRule()
	r.Units = append(r.Units, r.Units[0], r.Units[0], r.Units[0])
	if err := ValidateRule(r); err == nil {
		t.Error("five devices a row accepted")
	}
	r = pairRule()
	r.Units[0].Fields = append(r.Units[0].Fields,
		model.ImportField{Key: "password", Source: "template", Template: "{拠点}-Pw9-backup"},
		model.ImportField{Key: "enablePassword", Source: "column", Column: "拠点", Steps: []model.ImportStep{{Type: "replace", From: "本社", To: "Pw9-en"}}})
	b, err := ExportRule(*r, "t")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Pw9") {
		t.Errorf("a unit's password text exported:\n%s", b)
	}
	got, err := ParseRule(b)
	if err != nil || len(got.Units) != 1 || got.Units[0].Label != "バックアップ" {
		t.Errorf("round trip: %+v %v", got.Units, err)
	}
}
