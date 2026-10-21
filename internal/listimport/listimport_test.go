package listimport

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

func step(t string, kv ...string) model.ImportStep {
	s := model.ImportStep{Type: t}
	for i := 0; i+1 < len(kv); i += 2 {
		switch kv[i] {
		case "sep":
			s.Sep = kv[i+1]
		case "pos":
			s.Pos = kv[i+1]
		case "keep":
			s.Keep = kv[i+1]
		case "from":
			s.From = kv[i+1]
		case "to":
			s.To = kv[i+1]
		case "pattern":
			s.Pattern = kv[i+1]
		case "miss":
			s.Miss = kv[i+1]
		}
	}
	return s
}

func TestSteps(t *testing.T) {
	cases := []struct {
		in    string
		steps []model.ImportStep
		want  string
	}{
		{"admin/x9/Lp2q", []model.ImportStep{step("split", "sep", "/", "pos", "first", "keep", "left")}, "admin"},
		{"admin/x9/Lp2q", []model.ImportStep{step("split", "sep", "/", "pos", "first", "keep", "right")}, "x9/Lp2q"},
		{"192.0.2.13/24", []model.ImportStep{step("split", "sep", "/", "pos", "last", "keep", "left")}, "192.0.2.13"},
		{"本社/core-sw03", []model.ImportStep{step("split", "sep", "/", "keep", "right")}, "core-sw03"},
		// No separator: the value as it is, or empty when asked.
		{"admin", []model.ImportStep{step("split", "sep", "/", "pos", "first", "keep", "right")}, "admin"},
		{"admin", []model.ImportStep{step("split", "sep", "/", "keep", "right", "miss", "empty")}, ""},
		// A separator of several characters.
		{"ops :: secret", []model.ImportStep{step("split", "sep", " :: ", "keep", "right")}, "secret"},
		{" core-sw01 ", []model.ImportStep{step("trim")}, "core-sw01"},
		{"ＣＯＲＥ－ＳＷ１", []model.ImportStep{step("width"), step("lower")}, "core-sw1"},
		{"core_sw01", []model.ImportStep{step("replace", "from", "_", "to", "-")}, "core-sw01"},
		{"core-sw01 (本番)", []model.ImportStep{step("regex", "pattern", `\s*\(.*\)$`)}, "core-sw01"},
		{"Catalyst 9300", []model.ImportStep{{Type: "map", Miss: "empty", Rows: []model.ImportMapRow{{Match: "contains", From: "catalyst", To: "cisco-ios"}}}}, "cisco-ios"},
		{"SRX300", []model.ImportStep{{Type: "map", Miss: "empty", Rows: []model.ImportMapRow{{Match: "contains", From: "catalyst", To: "cisco-ios"}}}}, ""},
		{"SRX300", []model.ImportStep{{Type: "map", Miss: "keep", Rows: []model.ImportMapRow{{Match: "equals", From: "SRX", To: "x"}}}}, "SRX300"},
		{"SSH2", []model.ImportStep{{Type: "map", Rows: []model.ImportMapRow{{Match: "equals", From: "ssh2", To: "ssh"}}}}, "ssh"},
	}
	for _, c := range cases {
		if got := ApplySteps(c.in, c.steps); got != c.want {
			t.Errorf("ApplySteps(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	tr := TraceSteps(" admin/x ", []model.ImportStep{step("trim"), step("split", "sep", "/", "keep", "left")})
	if len(tr) != 2 || tr[0] != "admin/x" || tr[1] != "admin" {
		t.Errorf("TraceSteps = %q", tr)
	}
}

// fixture is a sheet as another system writes it: a title above the
// table, two empty columns on the left, the header on row 4, a merged site
// cell, a retired device and a row without a name.
func fixture() *sheet.Sheet {
	return &sheet.Sheet{
		Name: "機器一覧",
		Rows: [][]string{
			{"", "", "ネットワーク機器台帳"},
			{"", "", "出力日: 2026/10/01"},
			{},
			{"", "", "拠点コード", "設置場所", "機器名称", "管理\nIP", "機種", "ログイン情報", "状態"},
			{"", "", "TKY", "本社3F", "本社/core-sw01", "192.0.2.11/24", "Catalyst 9300", "admin/x9/Lp2q", "稼働"},
			{"", "", "TKY", "", "本社/core-sw02", "192.0.2.12/24", "Catalyst 9300", "admin/x9/Lp2q", "稼働"},
			{"", "", "TKY", "本社2F", "本社/acc-sw21", "192.0.2.21/24", "FortiGate 60F", "ops", "稼働"},
			{"", "", "TKY", "本社1F", "本社/old-sw01", "192.0.2.60/24", "Catalyst 2960", "admin/x", "撤去済"},
			{"", "", "TKY", "本社1F", "", "192.0.2.99/24", "", "", "予備"},
			{},
		},
		// 設置場所 本社3F covers rows 5–6.
		Merges: []sheet.Range{{R1: 4, C1: 3, R2: 5, C2: 3}},
	}
}

func fixtureRule() *model.ImportRule {
	return &model.ImportRule{
		Name: "資産管理", HeaderRow: 4, DataRow: 5,
		Fields: []model.ImportField{
			{Key: "name", Source: "column", Column: "機器名称", Steps: []model.ImportStep{step("split", "sep", "/", "keep", "right")}},
			{Key: "host", Source: "column", Column: "管理 IP", Steps: []model.ImportStep{step("split", "sep", "/", "pos", "last", "keep", "left")}},
			{Key: "osType", Source: "column", Column: "機種", Default: "generic", Steps: []model.ImportStep{{Type: "map", Miss: "empty", Rows: []model.ImportMapRow{{Match: "contains", From: "catalyst", To: "cisco-ios"}}}}},
			{Key: "username", Source: "column", Column: "ログイン情報", Steps: []model.ImportStep{step("split", "sep", "/", "keep", "left")}},
			{Key: "password", Source: "column", Column: "ログイン情報", Steps: []model.ImportStep{step("split", "sep", "/", "keep", "right", "miss", "empty")}},
			{Key: "site", Source: "template", Template: "{拠点コード} {設置場所}"},
			{Key: "conn", Default: "ssh"},
		},
		Exclude: []model.ImportCond{{Target: "column", Key: "状態", Op: "contains", Value: "撤去"}},
		Match:   model.ImportMatch{IgnoreCase: true, Width: true, Space: true},
	}
}

func convert(t *testing.T, s *sheet.Sheet, r *model.ImportRule) *Converted {
	t.Helper()
	tb, err := ReadTable(s, r)
	if err != nil {
		t.Fatal(err)
	}
	return Convert(tb, r)
}

func TestReadAndConvert(t *testing.T) {
	r := fixtureRule()
	tb, err := ReadTable(fixture(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Headers) != 7 || tb.Headers[0].Name != "拠点コード" || tb.Headers[0].Col != 3 || tb.Headers[3].Name != "管理 IP" {
		t.Fatalf("headers = %+v", tb.Headers)
	}
	if len(tb.Rows) != 5 {
		t.Fatalf("rows = %d, want 5 (the blank row dropped)", len(tb.Rows))
	}
	c := Convert(tb, r)
	if len(c.Missing) != 0 {
		t.Errorf("missing = %v", c.Missing)
	}
	r0 := c.Rows[0]
	want := map[string]string{"name": "core-sw01", "host": "192.0.2.11", "osType": "cisco-ios", "username": "admin", "password": "x9/Lp2q", "site": "TKY 本社3F", "conn": "ssh"}
	for k, v := range want {
		if r0.Values[k] != v {
			t.Errorf("row 5 %s = %q, want %q", k, r0.Values[k], v)
		}
	}
	// The merged site reaches row 6.
	if got := c.Rows[1].Values["site"]; got != "TKY 本社3F" {
		t.Errorf("merged site = %q", got)
	}
	// "ops" alone: the user is ops, the password empty (Miss "empty").
	if c.Rows[2].Values["username"] != "ops" || c.Rows[2].Values["password"] != "" {
		t.Errorf("row 7 user=%q pass=%q", c.Rows[2].Values["username"], c.Rows[2].Values["password"])
	}
	// The FortiGate is not in the table: the default.
	if c.Rows[2].Values["osType"] != "generic" || !contains(c.Rows[2].Filled, "osType") {
		t.Errorf("row 7 osType = %q filled %v", c.Rows[2].Values["osType"], c.Rows[2].Filled)
	}
	if !strings.Contains(c.Rows[3].Excluded, "撤去") {
		t.Errorf("row 8 excluded = %q", c.Rows[3].Excluded)
	}
	if c.Rows[4].Excluded != "機器名が空です" {
		t.Errorf("row 9 excluded = %q", c.Rows[4].Excluded)
	}
	// Only the top-left cell of a merge.
	r.MergeOnlyFirst = true
	if got := convert(t, fixture(), r).Rows[1].Values["site"]; got != "TKY " {
		t.Errorf("unmerged site = %q", got)
	}
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

func TestStartColumnAndMissing(t *testing.T) {
	r := fixtureRule()
	r.StartCol = "E" // 機器名称 onwards: 拠点コード and 設置場所 are outside
	c := convert(t, fixture(), r)
	if len(c.Missing) != 2 || !contains(c.Missing, "拠点コード") || !contains(c.Missing, "設置場所") {
		t.Errorf("missing = %v", c.Missing)
	}
	r.HeaderRow = 20
	if _, err := ReadTable(fixture(), r); err == nil {
		t.Error("a header row past the end should fail")
	}
}

func lab() *model.Inventory {
	return &model.Inventory{DeviceGroups: []model.DeviceGroup{{Name: "拠点A"}}}
}

func plan(t *testing.T, inv *model.Inventory, s *sheet.Sheet, r *model.ImportRule, dec map[int]Decision) *Plan {
	t.Helper()
	return BuildPlan(inv, convert(t, s, r), r, Options{Group: "拠点A", Decisions: dec})
}

func rowOf(p *Plan, line int) *PlanRow {
	for i := range p.Rows {
		if p.Rows[i].Line == line {
			return &p.Rows[i]
		}
	}
	return nil
}

func dev(inv *model.Inventory, name string) *model.Device {
	for i := range inv.Devices {
		if inv.Devices[i].Name == name {
			return &inv.Devices[i]
		}
	}
	return nil
}

func TestFirstImport(t *testing.T) {
	inv := lab()
	r := fixtureRule()
	p := plan(t, inv, fixture(), r, nil)
	if p.Counts["add"] != 3 || p.Counts["skip"] != 2 {
		t.Fatalf("counts = %v", p.Counts)
	}
	for _, c := range rowOf(p, 5).Cells {
		if c.State != "new" {
			t.Errorf("cell %s state %s", c.Key, c.State)
		}
	}
	res := Apply(inv, p, "拠点A", nil)
	if res.Added != 3 || len(inv.Devices) != 3 {
		t.Fatalf("result %+v, devices %d", res, len(inv.Devices))
	}
	d := dev(inv, "core-sw01")
	if d == nil || d.Group != "拠点A" || d.Host != "192.0.2.11" || d.Username != "admin" || d.Password != "x9/Lp2q" || d.OSType != "cisco-ios" || d.ID == "" {
		t.Fatalf("device = %+v", d)
	}
	if rec := inv.ImportRecords[d.ID]; rec.Key != "core-sw01" || rec.Base["host"] != "192.0.2.11" {
		t.Errorf("import record = %+v", rec)
	}
	// The same file again: nothing changes.
	p = plan(t, inv, fixture(), r, nil)
	if p.Counts["same"] != 3 || len(p.Missing) != 0 {
		t.Errorf("re-import counts = %v missing %v", p.Counts, p.Missing)
	}
}

// TestThreeWay is the 3-way comparison: a value edited in PalaTerm stays,
// one changed in the source is taken, one changed on both sides is asked.
func TestThreeWay(t *testing.T) {
	inv := lab()
	r := fixtureRule()
	Apply(inv, plan(t, inv, fixture(), r, nil), "拠点A", nil)

	// Edited in PalaTerm: core-sw01's address and core-sw02's user.
	dev(inv, "core-sw01").Host = "192.0.2.111"
	dev(inv, "core-sw02").Username = "ops2"
	// Changed in the source: core-sw02's address and core-sw01's address.
	s := fixture()
	s.Rows[4][5] = "192.0.2.31/24" // core-sw01: both changed
	s.Rows[5][5] = "192.0.2.32/24" // core-sw02: source changed

	p := plan(t, inv, s, r, nil)
	r1, r2 := rowOf(p, 5), rowOf(p, 6)
	if r1.Status != "ask" || cell(r1, "host").State != "ask" || cell(r1, "host").Choice != "keep" {
		t.Errorf("core-sw01 = %s / %+v", r1.Status, cell(r1, "host"))
	}
	if r2.Status != "upd" || cell(r2, "host").State != "take" || cell(r2, "username").State != "keep" {
		t.Errorf("core-sw02 = %s host %+v user %+v", r2.Status, cell(r2, "host"), cell(r2, "username"))
	}
	Apply(inv, p, "拠点A", nil)
	if d := dev(inv, "core-sw01"); d.Host != "192.0.2.111" {
		t.Errorf("core-sw01 host = %s, the PalaTerm value should stay", d.Host)
	}
	if d := dev(inv, "core-sw02"); d.Host != "192.0.2.32" || d.Username != "ops2" {
		t.Errorf("core-sw02 = %s %s", d.Host, d.Username)
	}
	// Next time core-sw01's PalaTerm address counts as edited here only.
	p = plan(t, inv, s, r, nil)
	if c := cell(rowOf(p, 5), "host"); c.State != "keep" || rowOf(p, 5).Status != "same" {
		t.Errorf("after: %s %+v", rowOf(p, 5).Status, c)
	}
	// The user picks the file's value.
	p = plan(t, inv, s, r, map[int]Decision{5: {Choices: map[string]string{"host": "file"}}})
	Apply(inv, p, "拠点A", nil)
	if d := dev(inv, "core-sw01"); d.Host != "192.0.2.31" {
		t.Errorf("chosen file value: host = %s", d.Host)
	}
}

func cell(r *PlanRow, key string) Cell {
	for _, c := range r.Cells {
		if c.Key == key {
			return c
		}
	}
	return Cell{}
}

func TestMatching(t *testing.T) {
	inv := lab()
	r := fixtureRule()
	Apply(inv, plan(t, inv, fixture(), r, nil), "拠点A", nil)

	// Renamed in PalaTerm: found by the name it was imported under.
	dev(inv, "core-sw01").Name = "tky-core-sw01"
	p := plan(t, inv, fixture(), r, nil)
	if row := rowOf(p, 5); row.MatchBy != "key" || row.MatchName != "tky-core-sw01" {
		t.Errorf("renamed: %+v", row)
	}
	// The name cell differs from the record: PalaTerm's name stays.
	if c := cell(rowOf(p, 5), "name"); c.State != "keep" {
		t.Errorf("renamed name cell = %+v", c)
	}

	// A device made by hand before any import, spelled differently.
	inv2 := lab()
	inv2.Devices = []model.Device{{ID: "a1", Name: "CORE-SW01.lab.example", Group: "拠点A", Host: "192.0.2.11", Conn: "ssh", OSType: "cisco-ios", Enabled: true}}
	r.Match.Domain = true
	p = plan(t, inv2, fixture(), r, nil)
	row := rowOf(p, 5)
	if row.MatchBy != "norm" || row.Link != "a1" || row.Status != "ask" {
		t.Errorf("norm match: %+v", row)
	}
	// No record: differing cells are asked, PalaTerm's kept by default.
	if c := cell(row, "name"); c.State != "ask" || c.Choice != "keep" {
		t.Errorf("name cell = %+v", c)
	}

	// Only the address matches: proposed, never silent.
	inv3 := lab()
	inv3.Devices = []model.Device{{ID: "b1", Name: "dist-sw01", Group: "拠点A", Host: "192.0.2.12", Conn: "ssh", OSType: "cisco-ios", Enabled: true}}
	p = plan(t, inv3, fixture(), r, nil)
	if row := rowOf(p, 6); row.MatchBy != "ip" || row.Status != "ask" || row.Link != "b1" {
		t.Errorf("ip match: %+v", row)
	}
	// Told it is another device: added, the old one reported missing.
	p = plan(t, inv3, fixture(), r, map[int]Decision{6: {Link: "new"}})
	if row := rowOf(p, 6); row.Status != "add" || len(p.Missing) != 1 {
		t.Errorf("as new: %+v missing %v", row, p.Missing)
	}
	res := Apply(inv3, p, "拠点A", []string{"b1"})
	if res.Deleted != 1 || dev(inv3, "dist-sw01") != nil {
		t.Errorf("delete missing: %+v", res)
	}

	// Two devices fit the name: never a guess.
	inv4 := lab()
	inv4.Devices = []model.Device{
		{ID: "c1", Name: "Core-SW01", Group: "拠点A", Host: "192.0.2.201", Conn: "ssh", OSType: "generic", Enabled: true},
		{ID: "c2", Name: "CORE-SW01", Group: "拠点A", Host: "192.0.2.202", Conn: "ssh", OSType: "generic", Enabled: true},
	}
	p = plan(t, inv4, fixture(), r, nil)
	if row := rowOf(p, 5); row.Status != "ask" || row.Link != "skip" || len(row.Candidates) != 2 {
		t.Errorf("ambiguous: %+v", row)
	}
	// Other groups are never touched.
	inv5 := lab()
	inv5.Devices = []model.Device{{ID: "d1", Name: "core-sw01", Group: "拠点B", Host: "192.0.2.11", Conn: "ssh", OSType: "generic", Enabled: true}}
	p = plan(t, inv5, fixture(), r, nil)
	if row := rowOf(p, 5); row.Status != "add" {
		t.Errorf("other group: %+v", row)
	}
}

func TestKeyColumn(t *testing.T) {
	s := fixture()
	s.Rows[3] = append(s.Rows[3], "資産番号")
	s.Rows[4] = append(s.Rows[4], "N-0001")
	s.Rows[5] = append(s.Rows[5], "N-0002")
	r := fixtureRule()
	r.Match.KeyColumn = "資産番号"
	inv := lab()
	Apply(inv, plan(t, inv, s, r, nil), "拠点A", nil)
	if rec := inv.ImportRecords[dev(inv, "core-sw01").ID]; rec.Key != "N-0001" || rec.KeyBy != "資産番号" {
		t.Fatalf("record = %+v", rec)
	}
	// acc-sw21 has no asset number: its name is the key.
	if rec := inv.ImportRecords[dev(inv, "acc-sw21").ID]; rec.Key != "acc-sw21" || rec.KeyBy != "" {
		t.Errorf("fallback record = %+v", rec)
	}
	// Renamed in the source: the asset number still finds it.
	s.Rows[4][4] = "本社/tky-core-sw01"
	p := plan(t, inv, s, r, nil)
	if row := rowOf(p, 5); row.MatchBy != "key" || cell(row, "name").State != "take" {
		t.Errorf("renamed in source: %+v / %+v", row, cell(row, "name"))
	}
}

func TestNameClashAndDuplicates(t *testing.T) {
	s := fixture()
	s.Rows[5][4] = "本社/core-sw01" // the same name twice in the file
	inv := lab()
	p := plan(t, inv, s, fixtureRule(), nil)
	if rowOf(p, 5).Status != "add" || rowOf(p, 6).Status != "err" {
		t.Errorf("duplicate: %s %s %v", rowOf(p, 5).Status, rowOf(p, 6).Status, rowOf(p, 6).Errors)
	}
	// A new row may not take the name of a device the group keeps.
	inv.Devices = []model.Device{{ID: "e1", Name: "acc-sw21", Group: "拠点A", Host: "192.0.2.250", Conn: "ssh", OSType: "generic", Enabled: true}}
	p = plan(t, inv, fixture(), fixtureRule(), map[int]Decision{7: {Link: "new"}})
	if row := rowOf(p, 7); row.Status != "err" {
		t.Errorf("clash: %+v", row)
	}
	res := Apply(inv, p, "拠点A", nil)
	if len(res.Failed) != 1 || len(inv.Devices) != 3 {
		t.Errorf("apply with an error row: %+v (%d devices)", res, len(inv.Devices))
	}
}

func TestInvalidValues(t *testing.T) {
	s := fixture()
	s.Rows[4][5] = "192.0.2.300"
	r := fixtureRule()
	r.Fields = append(r.Fields, model.ImportField{Key: "port", Source: "column", Column: "状態"})
	p := plan(t, lab(), s, r, nil)
	row := rowOf(p, 5)
	if row.Status != "err" || len(row.Errors) == 0 {
		t.Errorf("bad values: %+v", row)
	}
}

func TestRuleFile(t *testing.T) {
	r := *fixtureRule()
	r.ID, r.LastUsed = "x", "2026-10-21"
	r.Fields = append(r.Fields, model.ImportField{Key: "enablePassword", Default: "en-secret"})
	if err := ValidateRule(&r); err != nil {
		t.Fatal(err)
	}
	b, err := ExportRule(r, "PalaTerm test")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "en-secret") || strings.Contains(string(b), "lastUsed") || strings.Contains(string(b), `"id": "x"`) {
		t.Errorf("secret or date exported: %s", b)
	}
	got, err := ParseRule(b)
	if err != nil {
		t.Fatal(err)
	}
	// enablePassword had only a default: nothing of it travels.
	if got.Name != r.Name || len(got.Fields) != len(r.Fields)-1 || got.ID != "" {
		t.Errorf("round trip: %+v", got)
	}
	if _, err := ParseRule([]byte(`{"format":"palaterm-import-rule/9","rule":{}}`)); err == nil || !strings.Contains(err.Error(), "新しい PalaTerm") {
		t.Errorf("newer format: %v", err)
	}
	if _, err := ParseRule([]byte(`{"format":"palaterm-bundle/1"}`)); err == nil {
		t.Error("another file accepted")
	}
	bad := r
	bad.Fields = append([]model.ImportField(nil), r.Fields...)
	bad.Fields = append(bad.Fields, model.ImportField{Key: "no-such", Source: "column", Column: "x"})
	if err := ValidateRule(&bad); err == nil {
		t.Error("unknown field accepted")
	}
	bad = r
	bad.Fields = []model.ImportField{{Key: "host", Source: "column", Column: "管理IP"}}
	if err := ValidateRule(&bad); err == nil {
		t.Error("a rule without a name source accepted")
	}
	bad = *fixtureRule()
	bad.Fields[0].Steps = []model.ImportStep{{Type: "regex", Pattern: "("}}
	if err := ValidateRule(&bad); err == nil {
		t.Error("a broken pattern accepted")
	}
}

// TestRecordsBesideDevices: an editor replacing the whole device keeps
// the record (it is not on the device); a deleted device's record goes.
func TestRecordsBesideDevices(t *testing.T) {
	inv := lab()
	Apply(inv, plan(t, inv, fixture(), fixtureRule(), nil), "拠点A", nil)
	d := dev(inv, "core-sw01")
	repl := *d
	repl.Host = "192.0.2.111"
	*d = repl // as the device editor saves
	if inv.ImportRecords[d.ID].Base["host"] != "192.0.2.11" {
		t.Error("record lost by a whole-device save")
	}
	id := d.ID
	inv.Devices = inv.Devices[1:]
	inv.PruneImportRecords()
	if _, ok := inv.ImportRecords[id]; ok && dev(inv, "core-sw01") == nil {
		t.Error("record of a deleted device kept")
	}
}
