package listimport

import (
	"testing"

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
