package listimport

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// A column renamed at the source (ログイン情報 → 認証情報) is gone from the
// file: the fields it fed are not blanked on the existing devices — they
// keep their values, the record of the last import is kept, a choice of
// "file" cannot blank them either — while a new device takes the default.
func TestMissingColumnKeepsExisting(t *testing.T) {
	inv := lab()
	r := fixtureRule()
	Apply(inv, plan(t, inv, fixture(), r, nil), "拠点A", nil)
	before := *dev(inv, "core-sw01")
	baseUser := inv.ImportRecords[before.ID].Base["username"]

	s := fixture()
	s.Rows[3][7] = "認証情報" // renamed at the source
	s.Rows[3][3] = "設置"   // the site combination loses a column too
	// A new device on the list.
	s.Rows = append(s.Rows[:9], []string{"", "", "TKY", "本社4F", "本社/new-sw31", "192.0.2.31/24", "Catalyst 9300", "admin/n", "稼働"})
	c := convert(t, s, r)
	if !contains(c.Missing, "ログイン情報") || !contains(c.Missing, "設置場所") {
		t.Fatalf("missing = %v", c.Missing)
	}
	p := plan(t, inv, s, r, map[int]Decision{5: {Choices: map[string]string{"password": "file"}}})
	r1 := rowOf(p, 5)
	for _, k := range []string{"username", "password", "site"} {
		if cl := cell(r1, k); cl.State != "lost" || cl.Choice != "keep" {
			t.Errorf("%s = %+v", k, cl)
		}
	}
	if r1.Status != "same" || r1.Changes {
		t.Errorf("row 5 = %s changes %v", r1.Status, r1.Changes)
	}
	if nr := rowOf(p, 10); nr == nil || nr.Status != "add" {
		t.Fatalf("new row = %+v", nr)
	}
	Apply(inv, p, "拠点A", nil)
	d := dev(inv, "core-sw01")
	if d.Username != before.Username || d.Password != before.Password || d.Site != before.Site || d.Username == "" {
		t.Errorf("core-sw01 after = %q %q %q, before %q %q %q", d.Username, d.Password, d.Site, before.Username, before.Password, before.Site)
	}
	if b, ok := inv.ImportRecords[d.ID].Base["username"]; !ok || b != baseUser {
		t.Errorf("record of the lost field = %q, %v; want %q", b, ok, baseUser)
	}
	if n := dev(inv, "new-sw31"); n == nil || n.Username != "" || n.Password != "" {
		t.Errorf("new device = %+v", n)
	}
	// The column back as it was: nothing to do.
	p = plan(t, inv, fixture(), r, nil)
	if r1 := rowOf(p, 5); r1.Status != "same" || cell(r1, "username").State != "same" {
		t.Errorf("column back: %s %+v", r1.Status, cell(r1, "username"))
	}
}

// A field whose own column is gone but whose fallback column gives a value
// reads the file: not lost.
func TestFallbackValueIsNotLost(t *testing.T) {
	r := &model.ImportRule{Name: "x", HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "機器名"},
		{Key: "host", Source: "column", Column: "IP", Fallback: []string{"GW"}},
	}}
	s := &sheet.Sheet{Rows: [][]string{{"機器名", "GW"}, {"lab-sw01", "192.0.2.1"}, {"lab-sw02", ""}}}
	c := convert(t, s, r)
	if contains(c.Rows[0].Lost, "host") || c.Rows[0].Values["host"] != "192.0.2.1" {
		t.Errorf("row 1: lost %v host %q", c.Rows[0].Lost, c.Rows[0].Values["host"])
	}
	if !contains(c.Rows[1].Lost, "host") {
		t.Errorf("row 2: lost %v", c.Rows[1].Lost)
	}
}

// Filling merged areas counts the cells the rows grow by: many merges over
// cells already there never reach the limit, and an area too large is left
// unfilled with a warning — the areas after it still filled.
func TestMergeFillCountsGrowth(t *testing.T) {
	full := func() []string { r := make([]string, 1000); r[0] = "x"; return r }
	s := &sheet.Sheet{Name: "一覧", Rows: [][]string{full(), full(), full()}}
	for i := 0; i < 2000; i++ {
		s.Merges = append(s.Merges, sheet.Range{R1: 0, C1: 10, R2: 2, C2: 999})
	}
	s.Rows[0][5] = "本社"
	s.Merges = append(s.Merges, sheet.Range{R1: 0, C1: 5, R2: 1, C2: 6})
	g, warn := cellGrid(s, false)
	if warn != "" || g[1][6] != "本社" {
		t.Fatalf("warn %q, last merge %q", warn, g[1][6])
	}

	// One row 1000 wide over 6000 narrow ones: an area across all of them
	// would add six million cells.
	rows := [][]string{make([]string, 1000)}
	for i := 0; i < 6000; i++ {
		rows = append(rows, []string{"v"})
	}
	rows[1] = []string{"東京DC", ""}
	s = &sheet.Sheet{Name: "一覧", Rows: rows, Merges: []sheet.Range{{R1: 0, C1: 0, R2: 6000, C2: 999}, {R1: 1, C1: 0, R2: 2, C2: 0}}}
	g, warn = cellGrid(s, false)
	if !strings.Contains(warn, "1 個の結合範囲") || g[2][0] != "東京DC" {
		t.Fatalf("warn %q, small merge %q", warn, g[2][0])
	}
	tb, err := ReadTable(&sheet.Sheet{Name: "一覧", Rows: [][]string{{"機器名"}, {"lab-sw01"}}, Merges: s.Merges}, &model.ImportRule{HeaderRow: 1})
	if err != nil || len(tb.Warnings) != 0 {
		t.Errorf("small sheet: %v %v", err, tb.Warnings)
	}
}

// The rule editor's sample shows no password, whatever the header row is
// set to: by the column the field was chosen at, and under its header text
// wherever the sample shows it.
func TestMaskSample(t *testing.T) {
	grid := func() [][]string {
		return [][]string{
			{"機器台帳", "", ""},
			{"", "", ""},
			{"機器名", "IP", "PW"},
			{"lab-sw01", "192.0.2.1", "x9Lp2q"},
		}
	}
	r := &model.ImportRule{HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "機器名"},
		{Key: "password", Source: "column", Column: "PW", ColIndex: 3},
	}}
	// The table could not be read (no table): by number and by header text.
	g := grid()
	MaskSample(g, nil, r)
	if g[3][2] != "●●●●●●" || g[2][2] != "PW" || g[3][0] != "lab-sw01" {
		t.Errorf("no table: %q", g)
	}
	// Header row 1 (the title): the table reads, but has no 「PW」.
	g = grid()
	tb, err := ReadTable(&sheet.Sheet{Rows: grid()}, r)
	if err != nil {
		t.Fatal(err)
	}
	MaskSample(g, tb, r)
	if g[3][2] != "●●●●●●" {
		t.Errorf("header row off: %q", g)
	}
	// A header renamed since (パスワード, chosen at column 3): by number.
	r.Fields[1].Column = "パスワード"
	r.HeaderRow = 3
	g = grid()
	g[2][2] = "PW2"
	tb, _ = ReadTable(&sheet.Sheet{Rows: g}, r)
	MaskSample(g, tb, r)
	if g[3][2] != "●●●●●●" {
		t.Errorf("renamed: %q", g)
	}
}
