package listimport

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// A unit's name does not take the first device's default: a line without
// a backup (an empty backup name cell) gives one device, not a second one
// named "unnamed". The unit's other fields still keep the first's defaults.
func TestUnitNameTakesNoDefault(t *testing.T) {
	r := pairRule()
	r.Fields[0].Default = "unnamed"
	r.Fields = append(r.Fields, model.ImportField{Key: "port", Default: "2222"})
	r.Units[0].Fields = append(r.Units[0].Fields, model.ImportField{Key: "port", Source: "column", Column: "ポート(副)"})
	s := pairSheet()
	s.Rows[0] = append(s.Rows[0], "ポート(副)")
	c := convert(t, s, r)
	var names []string
	for _, row := range c.Rows {
		if row.Excluded == "" {
			names = append(names, row.Values["name"])
		}
	}
	// Line 3 (core01, no backup) and line 4 (no primary: the default) give
	// one device each.
	if got := strings.Join(names, ","); got != "fw01a,fw01b,core01,unnamed,edge01b" {
		t.Fatalf("names = %s", got)
	}
	if c.Rows[1].Unit != 2 || c.Rows[1].Values["port"] != "2222" {
		t.Errorf("the unit's port = %+v", c.Rows[1])
	}
}

// An exclusion on a column the file does not have is not judged (every
// row would be left out by 状態 が空 / でない / 含まない); the column is
// reported missing. The same for a field whose value is lost with its column.
func TestExclusionOnMissingColumnNotJudged(t *testing.T) {
	for _, op := range []string{"empty", "notequals", "notcontains"} {
		r := fixtureRule()
		r.Exclude = []model.ImportCond{{Target: "column", Key: "状態", Op: op, Value: "稼働"}}
		s := fixture()
		s.Rows[3][8] = "ステータス" // renamed at the source
		c := convert(t, s, r)
		if !contains(c.Missing, "状態") {
			t.Errorf("%s: missing = %v", op, c.Missing)
		}
		for _, row := range c.Rows {
			if strings.HasPrefix(row.Excluded, "除外条件") {
				t.Errorf("%s: row %d excluded: %s", op, row.Line, row.Excluded)
			}
		}
	}
	// A field read from a missing column: its empty value is not the file's.
	r := fixtureRule()
	r.Fields = append(r.Fields, model.ImportField{Key: "role", Source: "column", Column: "役割"})
	r.Exclude = []model.ImportCond{{Target: "field", Key: "role", Op: "empty"}}
	c := convert(t, fixture(), r)
	if !contains(c.Missing, "役割") {
		t.Errorf("missing = %v", c.Missing)
	}
	for _, row := range c.Rows {
		if strings.HasPrefix(row.Excluded, "除外条件") {
			t.Errorf("row %d excluded by a lost field: %s", row.Line, row.Excluded)
		}
	}
	// The column there: judged as before (撤去済 is not 稼働).
	r = fixtureRule()
	r.Exclude = []model.ImportCond{{Target: "column", Key: "状態", Op: "notequals", Value: "稼働"}}
	c = convert(t, fixture(), r)
	if !strings.HasPrefix(rowByLine(c, 8).Excluded, "除外条件") || strings.HasPrefix(rowByLine(c, 5).Excluded, "除外条件") {
		t.Errorf("rows = %+v", c.Rows)
	}
}

func rowByLine(c *Converted, line int) *Row {
	for i := range c.Rows {
		if c.Rows[i].Line == line {
			return &c.Rows[i]
		}
	}
	return nil
}

// An invalid value in the file (接続方式 "稼働") on a device whose value is
// the fallback it canonicalises to (ssh) is not "same": the cell shows the
// file's own text, and taking it is the error it is — the row fails with
// it instead of recording the fallback as the source's value.
func TestInvalidValueIsNotSame(t *testing.T) {
	r := fixtureRule()
	r.Fields[6] = model.ImportField{Key: "conn", Source: "column", Column: "状態"}
	inv := lab()
	// As the file has it in every other cell.
	inv.Devices = []model.Device{{ID: "a1", Name: "core-sw01", Group: "拠点A", Host: "192.0.2.11", Conn: "ssh", OSType: "cisco-ios",
		Username: "admin", Password: "x9/Lp2q", Site: "TKY 本社3F", Enabled: true}}
	// No record: asked, kept by default — the file's text in the cell.
	p := plan(t, inv, fixture(), r, nil)
	row := rowOf(p, 5)
	if c := cell(row, "conn"); c.State != "ask" || c.File != "稼働" || c.Cur != "ssh" {
		t.Fatalf("conn cell = %+v", c)
	}
	if row.Status != "ask" || len(row.Errors) != 0 {
		t.Fatalf("row = %+v", row)
	}
	// Chosen from the file: the error shows.
	p = plan(t, inv, fixture(), r, map[int]Decision{5: {Choices: map[string]string{"conn": "file"}}})
	if row := rowOf(p, 5); row.Status != "err" || len(row.Errors) != 1 || !strings.Contains(row.Errors[0], "接続方式") {
		t.Fatalf("file chosen: %+v", row)
	}
	// With a record saying the last import wrote ssh: the source changed
	// it (to something invalid) — taken, hence the error, not quietly same.
	inv.ImportRecords = map[string]model.ImportRecord{"a1": {Base: map[string]string{"conn": "ssh"}}}
	p = plan(t, inv, fixture(), r, nil)
	if row := rowOf(p, 5); row.Status != "err" || cell(row, "conn").State != "take" {
		t.Fatalf("recorded: %+v", row)
	}
	// Kept by choice: the record now holds the file's text, so the next
	// import (the source unchanged) keeps without asking.
	p = plan(t, inv, fixture(), r, map[int]Decision{5: {Choices: map[string]string{"conn": "keep"}}})
	row = rowOf(p, 5)
	if row.Status != "same" || row.rec.Base["conn"] != "稼働" {
		t.Fatalf("kept: %+v rec %+v", row, row.rec)
	}
	Apply(inv, p, "拠点A", nil)
	p = plan(t, inv, fixture(), r, nil)
	if row := rowOf(p, 5); row.Status != "same" || cell(row, "conn").State != "keep" {
		t.Fatalf("next import: %+v", row)
	}
}

// A device matched by its address only is a device the list has: a row
// set to 取り込まない (or undecided) leaves it out of Missing. Declared
// another device (新規), the address is free and the old one is offered.
func TestAddressMatchKeepsOutOfMissing(t *testing.T) {
	r := fixtureRule()
	inv := lab()
	inv.Devices = []model.Device{{ID: "b1", Name: "dist-sw01", Group: "拠点A", Host: "192.0.2.12", Conn: "ssh", OSType: "cisco-ios", Enabled: true}}
	p := plan(t, inv, fixture(), r, nil)
	if row := rowOf(p, 6); row.MatchBy != "ip" || len(p.Missing) != 0 {
		t.Fatalf("proposed: %+v missing %v", row, p.Missing)
	}
	p = plan(t, inv, fixture(), r, map[int]Decision{6: {Link: "skip"}})
	if row := rowOf(p, 6); row.Status != "skip" || len(p.Missing) != 0 {
		t.Fatalf("skipped: %+v missing %v", row, p.Missing)
	}
	p = plan(t, inv, fixture(), r, map[int]Decision{6: {Link: "new"}})
	if row := rowOf(p, 6); row.Status != "add" || len(p.Missing) != 1 {
		t.Fatalf("as new: %+v missing %v", row, p.Missing)
	}
}

// Only the mark ReadTable gives a repeated header ("ホスト名 (2)") makes a
// column the second device's: "ポート (TCP)" is proposed as any header.
func TestSuggestParenthesisedHeader(t *testing.T) {
	s := &sheet.Sheet{Rows: [][]string{
		{"ホスト名", "IPアドレス", "ポート (TCP)", "ホスト名", "IPアドレス"},
		{"fw01a", "192.0.2.1", "22", "fw01b", "192.0.2.2"},
	}}
	sg := Suggest(s, model.ImportRule{HeaderRow: 1})
	if m := sugMap(sg.Fields); m["port"] != "ポート (TCP)" || m["name"] != "ホスト名" {
		t.Errorf("fields = %v", m)
	}
	if u := sugMap(sg.Unit); u["name"] != "ホスト名 (2)" || u["host"] != "IPアドレス (2)" || u["port"] != "" {
		t.Errorf("unit = %v", u)
	}
}

// A 置き換え表 row's From is trimmed in contains mode as in equals mode.
func TestMapContainsTrimsFrom(t *testing.T) {
	st := model.ImportStep{Type: "map", Rows: []model.ImportMapRow{
		{Match: "contains", From: " catalyst ", To: "cisco-ios"},
		{Match: "contains", From: "   ", To: "never"},
	}}
	if got := ApplySteps("Catalyst 9300", []model.ImportStep{st}, nil); got != "cisco-ios" {
		t.Errorf("contains = %q", got)
	}
	if got := ApplySteps("FortiGate", []model.ImportStep{st}, nil); got != "FortiGate" {
		t.Errorf("a blank From matched: %q", got)
	}
}

// ExportRule drops an exclusion comparing a password column — its own, a
// fallback or one of a combination — with a value, by the same list the
// sample masking uses.
func TestExportDropsSecretColumnExclusions(t *testing.T) {
	r := *fixtureRule()
	r.Fields[4].Fallback = []string{"PW2"}
	r.Units = []model.ImportUnit{{Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "機器名称(副)"},
		{Key: "password", Source: "template", Template: "{PW3}"},
	}}}
	r.Exclude = []model.ImportCond{
		{Target: "column", Key: "ログイン情報", Op: "equals", Value: "x9"},
		{Target: "column", Key: "PW2", Op: "contains", Value: "x9"},
		{Target: "column", Key: "PW3", Op: "notequals", Value: "x9"},
		{Target: "column", Key: "ログイン情報", Op: "empty"},
		{Target: "column", Key: "状態", Op: "contains", Value: "撤去"},
	}
	b, err := ExportRule(r, "t")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseRule(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Exclude) != 2 || got.Exclude[0].Op != "empty" || got.Exclude[1].Key != "状態" {
		t.Errorf("exclusions = %+v", got.Exclude)
	}
}
