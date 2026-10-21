package listimport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// small is a plain table: 資産番号, 機器名称, IP.
func small(rows ...[]string) *sheet.Sheet {
	s := &sheet.Sheet{Name: "s", Rows: [][]string{{"資産番号", "機器名称", "IP", "状態"}}}
	s.Rows = append(s.Rows, rows...)
	return s
}

func smallRule() *model.ImportRule {
	return &model.ImportRule{
		Name: "small", HeaderRow: 1,
		Fields: []model.ImportField{
			{Key: "name", Source: "column", Column: "機器名称"},
			{Key: "host", Source: "column", Column: "IP"},
		},
		Exclude: []model.ImportCond{{Target: "column", Key: "状態", Op: "contains", Value: "撤去"}},
		Match:   model.ImportMatch{IgnoreCase: true, Width: true, Space: true, KeyColumn: "資産番号"},
	}
}

func labDev(id, name, host string) model.Device {
	return model.Device{ID: id, Name: name, Group: "拠点A", Host: host, Conn: "ssh", OSType: "generic", Enabled: true}
}

// A row failing on a name keeps its device's name taken: another row may
// not take it in turn (two devices of one name in a group).
func TestNameClashChain(t *testing.T) {
	inv := lab()
	inv.Devices = []model.Device{labDev("x1", "x", "192.0.2.1"), labDev("y1", "y", "192.0.2.2")}
	inv.ImportRecords = map[string]model.ImportRecord{"x1": {Key: "N1", KeyBy: "資産番号", Base: map[string]string{"name": "x", "host": "192.0.2.1"}}}
	s := small([]string{"N1", "y", "192.0.2.1", ""}, []string{"N2", "x", "192.0.2.9", ""})
	p := plan(t, inv, s, smallRule(), nil)
	if r := rowOf(p, 2); r.Status != "err" {
		t.Fatalf("renaming x to y (kept by another device) should fail: %+v", r)
	}
	if r := rowOf(p, 3); r.Status != "err" {
		t.Fatalf("a new x while the device x keeps its name should fail: %+v", r)
	}
	Apply(inv, p, "拠点A", nil)
	seen := map[string]int{}
	for _, d := range inv.Devices {
		seen[d.Name]++
		if seen[d.Name] > 1 {
			t.Errorf("two devices named %q", d.Name)
		}
	}
}

// The import renames a device: Apply says so, for the schedules.
func TestRenamesReported(t *testing.T) {
	inv := lab()
	r := smallRule()
	Apply(inv, plan(t, inv, small([]string{"N1", "sw1", "192.0.2.1", ""}), r, nil), "拠点A", nil)
	res := Apply(inv, plan(t, inv, small([]string{"N1", "sw1-new", "192.0.2.1", ""}), r, nil), "拠点A", nil)
	if res.Renames["sw1"] != "sw1-new" || res.Updated != 1 {
		t.Errorf("renames %v, result %+v", res.Renames, res)
	}
}

// A device the file still lists is not offered for deletion, whatever
// happens to its row.
func TestMissingOnlyWhatTheFileLacks(t *testing.T) {
	inv := lab()
	r := smallRule()
	Apply(inv, plan(t, inv, small(
		[]string{"N1", "sw1", "192.0.2.1", ""},
		[]string{"N2", "sw2", "192.0.2.2", ""},
		[]string{"N3", "sw3", "192.0.2.3", ""},
		[]string{"N4", "sw4", "192.0.2.4", ""},
	), r, nil), "拠点A", nil)
	inv.Devices = append(inv.Devices, labDev("h1", "SW5", "192.0.2.51"), labDev("h2", "sw5", "192.0.2.52"))
	s := small(
		[]string{"N1", "sw1", "192.0.2.1", "撤去予定"}, // excluded
		[]string{"N2", "sw2", "192.0.2.2", ""},     // set to 取り込まない
		[]string{"N3", "sw3", "192.0.2.3", ""},
		[]string{"", "Sw5", "192.0.2.50", ""}, // two devices fit
	)
	p := plan(t, inv, s, r, map[int]Decision{3: {Link: "skip"}})
	if r5 := rowOf(p, 5); len(r5.Candidates) != 2 {
		t.Fatalf("ambiguous row: %+v", r5)
	}
	var names []string
	for _, m := range p.Missing {
		names = append(names, m.Name)
	}
	if len(names) != 1 || names[0] != "sw4" {
		t.Errorf("missing = %v, want only sw4", names)
	}
}

// Key column values are matched without the name-only folding: with the
// domain cut on, NW.0012 and NW.0013 still differ.
func TestKeyColumnNotCutAtDot(t *testing.T) {
	inv := lab()
	r := smallRule()
	r.Match.Domain = true
	s := small([]string{"NW.0012", "sw1", "192.0.2.1", ""}, []string{"NW.0013", "sw2", "192.0.2.2", ""})
	Apply(inv, plan(t, inv, s, r, nil), "拠点A", nil)
	s.Rows[1][1], s.Rows[2][1] = "sw1-a", "sw2-a" // both renamed at the source
	p := plan(t, inv, s, r, nil)
	for _, line := range []int{2, 3} {
		if row := rowOf(p, line); row.MatchBy != "key" || len(row.Candidates) != 0 {
			t.Errorf("row %d: %+v", line, row)
		}
	}
}

// A row asked about with every value kept changes nothing: not counted as
// an update.
func TestKeptAskIsNoUpdate(t *testing.T) {
	inv := lab()
	inv.Devices = []model.Device{labDev("a1", "sw1", "192.0.2.99")} // made by hand: no record
	p := plan(t, inv, small([]string{"N1", "sw1", "192.0.2.1", ""}), smallRule(), nil)
	row := rowOf(p, 2)
	if row.Status != "ask" || row.Changes {
		t.Fatalf("row = %+v", row)
	}
	res := Apply(inv, p, "拠点A", nil)
	if res.Updated != 0 || res.Same != 1 || inv.Devices[0].Host != "192.0.2.99" {
		t.Errorf("result %+v host %s", res, inv.Devices[0].Host)
	}
	// Recorded all the same: next time the address counts as edited here.
	if inv.ImportRecords["a1"].Base["host"] != "192.0.2.1" {
		t.Errorf("record = %+v", inv.ImportRecords["a1"])
	}
}

// No text a password could be written in leaves with an exported rule.
func TestExportLeavesSecretsOut(t *testing.T) {
	r := model.ImportRule{
		Name: "x", HeaderRow: 1, SampleName: "社内_機器台帳.xls",
		Fields: []model.ImportField{
			{Key: "name", Source: "column", Column: "機器名称"},
			{Key: "password", Source: "column", Column: "拠点", Steps: []model.ImportStep{
				{Type: "trim"},
				{Type: "map", Rows: []model.ImportMapRow{{Match: "equals", From: "TKY", To: "Pw9-tky"}}},
				{Type: "replace", From: "x", To: "Pw9-rep"},
				{Type: "regex", Pattern: ".*", To: "Pw9-re"},
			}},
			{Key: "enablePassword", Source: "template", Template: "{拠点}/Pw9-tpl"},
			{Key: "keyPassphrase", Source: "column", Column: "ログイン", Default: "Pw9-def", Steps: []model.ImportStep{{Type: "split", Sep: "/", Keep: "right"}}},
			{Key: "osType", Source: "column", Column: "機種", Steps: []model.ImportStep{{Type: "map", Rows: []model.ImportMapRow{{From: "Catalyst", To: "cisco-ios"}}}}},
		},
		Exclude: []model.ImportCond{
			{Target: "field", Key: "password", Op: "equals", Value: "Pw9-cond"},
			{Target: "field", Key: "password", Op: "empty"},
		},
	}
	b, err := ExportRule(r, "t")
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, s := range []string{"Pw9-", "社内_機器台帳"} {
		if strings.Contains(out, s) {
			t.Errorf("%q exported:\n%s", s, out)
		}
	}
	var f RuleFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	got := map[string]model.ImportField{}
	for _, fl := range f.Rule.Fields {
		got[fl.Key] = fl
	}
	if pw := got["password"]; pw.Column != "拠点" || len(pw.Steps) != 1 || pw.Steps[0].Type != "trim" {
		t.Errorf("password field = %+v", pw)
	}
	if _, ok := got["enablePassword"]; ok {
		t.Error("a template-only password field should go")
	}
	if kp := got["keyPassphrase"]; len(kp.Steps) != 1 || kp.Steps[0].Type != "split" {
		t.Errorf("the split that cuts a password out of its cell should stay: %+v", kp)
	}
	if os := got["osType"]; len(os.Steps) != 1 {
		t.Errorf("other fields keep their tables: %+v", os)
	}
	if len(f.Rule.Exclude) != 1 || f.Rule.Exclude[0].Op != "empty" {
		t.Errorf("exclusions = %+v", f.Rule.Exclude)
	}
	if _, err := ParseRule(b); err != nil {
		t.Errorf("the exported rule does not load: %v", err)
	}
}
