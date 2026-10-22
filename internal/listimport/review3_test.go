package listimport

import (
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// A header renamed (or moved) at the source is a missing column: its field
// reads empty and the preview says so, rather than reading whatever column
// now stands where it was.
func TestRenamedHeaderIsMissing(t *testing.T) {
	r := &model.ImportRule{Name: "x", HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "機器名称", ColIndex: 1},
		{Key: "password", Source: "column", Column: "パスワード", ColIndex: 2},
	}}
	s := &sheet.Sheet{Rows: [][]string{{"機器名称", "コメント", "PW"}, {"sw1", "更新予定", "x9"}}}
	c := convert(t, s, r)
	if got := c.Rows[0].Values["password"]; got != "" {
		t.Errorf("password read %q from another column", got)
	}
	if len(c.Missing) != 1 || c.Missing[0] != "パスワード" {
		t.Errorf("missing = %v", c.Missing)
	}
	// A field without a header text still goes by its number.
	r.Fields[1] = model.ImportField{Key: "password", Source: "column", ColIndex: 3}
	if got := convert(t, s, r).Rows[0].Values["password"]; got != "x9" {
		t.Errorf("by number: %q", got)
	}
}

// The condition that left a row out travels with it (the page words it).
func TestExclusionTravels(t *testing.T) {
	c := convert(t, fixture(), fixtureRule())
	row := c.Rows[3]
	if row.Exclusion == nil || row.Exclusion.Key != "状態" || row.Exclusion.Op != "contains" {
		t.Errorf("exclusion = %+v (%q)", row.Exclusion, row.Excluded)
	}
	p := plan(t, lab(), fixture(), fixtureRule(), nil)
	if pr := rowOf(p, 8); pr.Exclusion == nil {
		t.Errorf("plan row: %+v", pr)
	}
	if pr := rowOf(p, 9); pr.Exclusion != nil || pr.Note != "機器名が空です" {
		t.Errorf("no name: %+v", pr)
	}
}

// Merged areas from a small hostile file stay within the sheet's cells:
// a thousand merges over A1:ALL100001 with one real cell far away.
func TestMergesStayWithinTheCells(t *testing.T) {
	rows := make([][]string, 3)
	rows[0] = []string{"機器名", "IP"}
	rows[1] = []string{"lab-sw01", "192.0.2.1"}
	rows[2] = []string{"", ""}
	var merges []sheet.Range
	for i := 0; i < 1000; i++ {
		merges = append(merges, sheet.Range{R1: 0, C1: 0, R2: 100000, C2: 999})
	}
	s := &sheet.Sheet{Rows: rows, Merges: merges}
	start := time.Now()
	g, _ := cellGrid(s, false)
	cells := 0
	for _, r := range g {
		cells += len(r)
	}
	if len(g) != 3 || cells > 6 || time.Since(start) > 2*time.Second {
		t.Fatalf("grid %d rows, %d cells, %v", len(g), cells, time.Since(start))
	}
}

// A rule file someone hands over cannot be oversized: steps, 置き換え表
// rows, a regexp huge once compiled. A received rule loses its sample name.
func TestRuleSizeLimits(t *testing.T) {
	base := func() *model.ImportRule {
		return &model.ImportRule{Name: "r", HeaderRow: 1, Fields: []model.ImportField{{Key: "name", Source: "column", Column: "機器名"}}}
	}
	r := base()
	for i := 0; i < 31; i++ {
		r.Fields[0].Steps = append(r.Fields[0].Steps, model.ImportStep{Type: "trim"})
	}
	if ValidateRule(r) == nil {
		t.Error("31 steps accepted")
	}
	r = base()
	r.Fields[0].Steps = []model.ImportStep{{Type: "map", Rows: make([]model.ImportMapRow, 5001)}}
	if ValidateRule(r) == nil {
		t.Error("5001 map rows accepted")
	}
	r = base()
	r.Fields[0].Steps = []model.ImportStep{{Type: "regex", Pattern: "(?:(?:a{1000}){1000})"}}
	if ValidateRule(r) == nil {
		t.Error("a huge regexp accepted")
	}
	r = base()
	r.Fields[0].Steps = []model.ImportStep{{Type: "regex", Pattern: `^([^/]+)/`}}
	if err := ValidateRule(r); err != nil {
		t.Errorf("a plain regexp refused: %v", err)
	}
	r = base()
	r.SampleName = "list.xls"
	data, err := ExportRule(*r, "test")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseRule(data)
	if err != nil || got.SampleName != "" {
		t.Errorf("parsed rule: %v, sample %q", err, got.SampleName)
	}
}
