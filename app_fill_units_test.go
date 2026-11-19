package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// unitsBook is a list with a redundant pair per line (予備ホスト名 is the
// backup, 予備NH its next hop), continuing lines, a line without a backup
// and lines marked 撤去.
func unitsBook(t *testing.T) *sheet.Book {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pair.csv")
	csv := "拠点,ホスト名,予備ホスト名,宛先,NH,予備NH,状態\r\n" +
		"A,lab-rt1,lab-rt2,198.51.100.0,192.0.2.1,192.0.2.2,\r\n" +
		",,,203.0.113.0,192.0.2.1,192.0.2.2,\r\n" +
		"B,lab-rt3,,198.51.100.0,192.0.2.9,,\r\n" +
		",,,203.0.113.0,192.0.2.9,,\r\n" +
		"C,lab-rt4,lab-rt5,198.51.100.0,192.0.2.11,192.0.2.12,撤去\r\n" +
		",,,203.0.113.0,192.0.2.11,192.0.2.12,\r\n" +
		"D,lab-rt6,,198.51.100.0,192.0.2.13,,\r\n" +
		",,,203.0.113.0,192.0.2.13,,撤去\r\n" +
		",,,192.0.2.0,192.0.2.13,,\r\n"
	if err := os.WriteFile(p, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := sheet.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func unitsRule() model.FillRule {
	col := func(c string) model.ImportField { return model.ImportField{Source: "column", Column: c} }
	return model.FillRule{ID: "r2", Name: "冗長", HeaderRow: 1, Key: col("ホスト名"),
		Items: []model.FillItem{{Name: "宛先", ImportField: col("宛先")}, {Name: "NH", ImportField: col("NH")}},
		Units: []model.FillUnit{{Label: "バックアップ", Key: col("予備ホスト名"), Items: []model.FillItem{{Name: "NH", ImportField: col("予備NH")}}}},
		Exclude: []model.ImportCond{{Target: "column", Key: "状態", Op: "equals", Value: "撤去"}},
	}
}

func fillDests(rows []map[string]string, k string) string {
	var s []string
	for _, r := range rows {
		s = append(s, r[k])
	}
	return strings.Join(s, ",")
}

func TestReadFillUnits(t *testing.T) {
	r := unitsRule()
	if err := model.CheckFillRule(&r); err != nil {
		t.Fatal(err)
	}
	rd, err := readFill(unitsBook(t), &r)
	if err != nil {
		t.Fatal(err)
	}
	f := rd.Fill
	var names []string
	for _, d := range f.Devices {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "lab-rt1,lab-rt2,lab-rt3,lab-rt6" {
		t.Fatalf("devices %v (the 撤去 pair is left out)", names)
	}
	if got := fillDests(f.RowsFor("lab-rt1"), "NH"); got != "192.0.2.1,192.0.2.1" {
		t.Fatalf("lab-rt1 NH %s", got)
	}
	// The backup reads its own NH column and the first device's 宛先, and
	// a continuing line continues it too.
	rows := f.RowsFor("lab-rt2")
	if fillDests(rows, "NH") != "192.0.2.2,192.0.2.2" || fillDests(rows, "宛先") != "198.51.100.0,203.0.113.0" {
		t.Fatalf("lab-rt2 rows %+v", rows)
	}
	// A line without a backup ends the backup above: lab-rt3's second line
	// is not lab-rt2's.
	if len(f.RowsFor("lab-rt3")) != 2 {
		t.Fatalf("lab-rt3 rows %+v", f.RowsFor("lab-rt3"))
	}
	// An excluded continuing line leaves out itself only.
	if got := fillDests(f.RowsFor("lab-rt6"), "宛先"); got != "198.51.100.0,192.0.2.0" {
		t.Fatalf("lab-rt6 宛先 %s", got)
	}
	if rd.Excluded != 3 || rd.Rows != 8 {
		t.Fatalf("excluded %d rows %d", rd.Excluded, rd.Rows)
	}
	var unit2 bool
	for _, l := range rd.lines {
		unit2 = unit2 || (l.Unit == 2 && l.Device == "lab-rt2")
	}
	if !unit2 {
		t.Fatalf("the editor's rows name the unit: %+v", rd.lines)
	}
}

func TestCheckFillRuleUnitsAndConds(t *testing.T) {
	r := unitsRule()
	r.Units[0].Key = model.ImportField{}
	if err := model.CheckFillRule(&r); err == nil || !strings.Contains(err.Error(), "2 台目の機器名") {
		t.Fatalf("a unit needs its device column: %v", err)
	}
	r = unitsRule()
	r.Units[0].Items[0].Name = "VLAN"
	if err := model.CheckFillRule(&r); err == nil || !strings.Contains(err.Error(), "1 台目にありません") {
		t.Fatalf("a unit's item must be the first device's: %v", err)
	}
	r = unitsRule()
	r.Exclude = append(r.Exclude, model.ImportCond{Target: "field", Key: model.FillItemKey("旧名"), Op: "empty"})
	if err := model.CheckFillRule(&r); err == nil || !strings.Contains(err.Error(), "旧名") {
		t.Fatalf("a condition on a gone variable is refused: %v", err)
	}
	r = unitsRule()
	r.Items[1].Source, r.Items[1].Column, r.Items[1].Template = "template", "", ""
	if err := model.CheckFillRule(&r); err == nil || !strings.Contains(err.Error(), "変数「NH」の組み立て") {
		t.Fatalf("an empty template is refused in the variable's words: %v", err)
	}
	r = unitsRule()
	r.Units = append(r.Units, r.Units[0], r.Units[0], r.Units[0])
	if err := model.CheckFillRule(&r); err == nil {
		t.Fatal("five devices on a line are refused")
	}
}

func TestMatchFillNames(t *testing.T) {
	a := &App{inv: &model.Inventory{Devices: []model.Device{{Name: "lab-rt1"}, {Name: "lab-rt2"}, {Name: "ＬＡＢ-ＲＴ９"}}}}
	f := &model.SetFill{Devices: []model.FillDevice{
		{Name: "LAB_RT1.lab.example", Rows: []map[string]string{{"NH": "192.0.2.1"}}},
		{Name: "lab-rt1", Rows: []map[string]string{{"NH": "192.0.2.3"}}},
		{Name: "lab_rt7", Rows: []map[string]string{{"NH": "192.0.2.7"}}},
	}}
	g := f.Clone()
	a.matchFillNames(g, model.ImportMatch{})
	if len(g.Devices) != 3 || g.RowsFor("lab-rt1") == nil || len(g.RowsFor("lab-rt1")) != 1 {
		t.Fatalf("no match setting: names as they are %+v", g.Devices)
	}
	a.matchFillNames(f, model.ImportMatch{Domain: true, Dash: true, Width: true})
	if len(f.Devices) != 2 || fillDests(f.RowsFor("lab-rt1"), "NH") != "192.0.2.1,192.0.2.3" {
		t.Fatalf("names coming to lab-rt1 are joined in order: %+v", f.Devices)
	}
	if f.Devices[1].Name != "lab_rt7" {
		t.Fatalf("a name matching no device is kept: %+v", f.Devices)
	}
}
