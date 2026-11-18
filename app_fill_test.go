package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

func fillRule() model.FillRule {
	col := func(c string) model.ImportField { return model.ImportField{Source: "column", Column: c} }
	return model.FillRule{ID: "r1", Name: "経路", HeaderRow: 1, Key: col("ホスト名"), Items: []model.FillItem{
		{Name: "宛先", ImportField: col("宛先")},
		{Name: "NH", ImportField: col("ネクストホップ")},
		{Name: "説明", ImportField: col("説明"), Empty: model.FillEmptySkip},
	}}
}

// fillBook is a list as a CSV gives it: lab-sw1 on two lines (the second
// with its name cell empty), lab-sw2 once, a blank line.
func fillBook(t *testing.T) *sheet.Book {
	t.Helper()
	p := filepath.Join(t.TempDir(), "route.csv")
	csv := "ホスト名,宛先,ネクストホップ,説明\r\n" +
		"lab-sw1,198.51.100.0,192.0.2.1,lab-a\r\n" +
		",203.0.113.0,192.0.2.1,\r\n" +
		",,,\r\n" +
		"LAB-SW2,198.51.100.0,192.0.2.5,\r\n"
	if err := os.WriteFile(p, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := sheet.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReadFill(t *testing.T) {
	r := fillRule()
	rd, err := readFill(fillBook(t), &r)
	if err != nil {
		t.Fatal(err)
	}
	f := rd.Fill
	if len(f.Devices) != 2 || rd.Rows != 3 {
		t.Fatalf("devices %+v rows %d", f.Devices, rd.Rows)
	}
	if rows := f.RowsFor("lab-sw1"); len(rows) != 2 || rows[1]["宛先"] != "203.0.113.0" {
		t.Fatalf("an empty name cell continues the device above: %+v", rows)
	}
	if f.RowsFor("lab-sw2") == nil {
		t.Fatal("case must not count")
	}
	if strings.Join(f.Skip, ",") != "説明" || strings.Join(f.Items, ",") != "宛先,NH,説明" {
		t.Fatalf("items %v skip %v", f.Items, f.Skip)
	}
}

func fillInventory(t *testing.T) *model.Inventory {
	r := fillRule()
	rd, err := readFill(fillBook(t), &r)
	if err != nil {
		t.Fatal(err)
	}
	return &model.Inventory{
		DeviceGroups: []model.DeviceGroup{{Name: "lab"}},
		CommandSets: []model.CommandSet{
			{Name: "route", Fill: rd.Fill, Commands: []model.Command{{Text: "configure terminal"}, {Text: "{{#each}}"},
				{Text: "ip route {{宛先}} 255.255.255.0 {{NH}}"}, {Text: "{{/each}}"}, {Text: "description {{説明}}"}, {Text: "end"}}},
			{Name: "show", Commands: []model.Command{{Text: "show clock"}}},
			{Name: "nolist", Commands: []model.Command{{Text: "ip route {{宛先}}"}}},
		},
		Devices: []model.Device{
			{Name: "lab-sw1", Host: "192.0.2.11", Group: "lab", CommandSet: "route", Enabled: true},
			{Name: "lab-sw2", Host: "192.0.2.12", Group: "lab", CommandSet: "route", Enabled: true},
			{Name: "lab-sw3", Host: "192.0.2.13", Group: "lab", CommandSet: "route", Enabled: true},
			{Name: "lab-sw4", Host: "192.0.2.14", Group: "lab", CommandSet: "show", Enabled: true},
		},
	}
}

func cmdTexts(cs *model.CommandSet) string {
	var s []string
	for _, c := range cs.Commands {
		s = append(s, c.Text)
	}
	return strings.Join(s, "|")
}

func TestFillSetsFromLinkedList(t *testing.T) {
	inv := fillInventory(t)
	req := RunRequest{Group: "lab", Names: []string{"lab-sw1", "lab-sw2", "lab-sw4"}}
	snap, err := snapshotOf(inv, req)
	if err != nil {
		t.Fatal(err)
	}
	sets, err := fillSets(snap, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 2 {
		t.Fatalf("sets %v", sets)
	}
	if got := cmdTexts(sets["lab-sw1"]); got != "configure terminal|ip route 198.51.100.0 255.255.255.0 192.0.2.1|ip route 203.0.113.0 255.255.255.0 192.0.2.1|description lab-a|end" {
		t.Fatalf("lab-sw1: %s", got)
	}
	// 説明 is empty for lab-sw2: the rule says leave the line out.
	if got := cmdTexts(sets["lab-sw2"]); got != "configure terminal|ip route 198.51.100.0 255.255.255.0 192.0.2.5|end" {
		t.Fatalf("lab-sw2: %s", got)
	}
	if inv.CommandSets[0].Commands[2].Text != "ip route {{宛先}} 255.255.255.0 {{NH}}" {
		t.Fatal("the saved set changed")
	}
	// lab-sw3 is not in the list: the run refuses it, the others still go.
	req.Names = []string{"lab-sw1", "lab-sw3"}
	snap, _ = snapshotOf(inv, req)
	sets, err = fillSets(snap, req)
	if err == nil || !strings.Contains(err.Error(), "lab-sw3") || sets["lab-sw1"] == nil {
		t.Fatalf("err = %v, sets %v", err, sets)
	}
	// A set with {{…}} and no list linked cannot run either.
	req = RunRequest{Group: "lab", Names: []string{"lab-sw4"}, CommandSet: "nolist"}
	snap, _ = snapshotOf(inv, req)
	if _, err := fillSets(snap, req); err == nil {
		t.Fatal("a set without its list must be refused")
	}
	// A dry run sends no commands and needs no values.
	req.DryRun = true
	if _, err := fillSets(snap, req); err != nil {
		t.Fatal(err)
	}
}

func TestFillPreviewDevice(t *testing.T) {
	inv := fillInventory(t)
	fd := fillDevice(setNamed(inv, "route"), "lab-sw1")
	if !fd.Fill || fd.Error != "" || fd.Rows != 2 || len(fd.Commands) != 5 || fd.Values[0] != "宛先=198.51.100.0" {
		t.Fatalf("%+v", fd)
	}
	if fd := fillDevice(setNamed(inv, "route"), "lab-sw3"); fd.Error == "" {
		t.Fatal("a device the list lacks must say so")
	}
	if fd := fillDevice(setNamed(inv, "show"), "lab-sw4"); fd.Fill {
		t.Fatal("a set without {{…}} fills in nothing")
	}
}

func TestFillSetRefusedForSchedule(t *testing.T) {
	inv := fillInventory(t)
	snap, _ := snapshotOf(inv, RunRequest{Group: "lab"})
	if err := fillSetsErr(snap); err == nil || !strings.Contains(err.Error(), "「route」") {
		t.Fatalf("err = %v", err)
	}
	snap, _ = snapshotOf(inv, RunRequest{Group: "lab", CommandSet: "show"})
	if err := fillSetsErr(snap); err != nil {
		t.Fatal(err)
	}
}

// A save of the commands keeps the linked list; a copy gets its own.
func TestFillKeptOnSaveAndCopy(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	inv := fillInventory(t)
	a.mu.Lock()
	a.inv.CommandSets = inv.CommandSets
	a.mu.Unlock()
	if err := a.SaveCommandSet(model.CommandSet{Name: "route", Commands: []model.Command{{Text: "ip route {{宛先}} 255.255.255.0 {{NH}}"}}}); err != nil {
		t.Fatal(err)
	}
	if setNamed(a.inv, "route").Fill == nil {
		t.Fatal("the save dropped the linked list")
	}
	n, err := a.CopyCommandSet("route")
	if err != nil {
		t.Fatal(err)
	}
	cp := setNamed(a.inv, n)
	if cp.Fill == nil || cp.Fill == setNamed(a.inv, "route").Fill {
		t.Fatal("the copy must have a list of its own")
	}
	if err := a.UnlinkFillList("route"); err != nil || setNamed(a.inv, "route").Fill != nil {
		t.Fatalf("unlink: %v", err)
	}
}

func TestFillRuleSaveExportImport(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	r := fillRule()
	r.ID = ""
	id, err := a.SaveFillRule(r)
	if err != nil || id == "" {
		t.Fatalf("save: %v", err)
	}
	if _, err := a.SaveFillRule(r); err == nil {
		t.Fatal("a second rule of the same name must be refused")
	}
	bad := fillRule()
	bad.ID, bad.Name, bad.Items = "", "x", nil
	if _, err := a.SaveFillRule(bad); err == nil {
		t.Fatal("a rule without items must be refused")
	}
	got, err := parseFillRule([]byte(`{"format":"palaterm-fill-rule","version":1,"rule":{"name":"y","headerRow":1,"key":{"source":"column","column":"ホスト名"},"items":[{"name":"NH","source":"column","column":"NH"}]}}`))
	if err != nil || got.Items[0].Column != "NH" {
		t.Fatalf("parse: %v %+v", err, got)
	}
	if _, err := parseFillRule([]byte(`{"format":"palaterm-import-rule"}`)); err == nil {
		t.Fatal("another file must be refused")
	}
}
