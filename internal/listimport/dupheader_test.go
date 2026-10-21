package listimport

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// A list repeating its column names for a pair's primary and backup blocks:
// each column is its own choice, and a rule reads the one it names.
func TestDuplicateHeaders(t *testing.T) {
	s := &sheet.Sheet{Rows: [][]string{
		{"", "メイン", "", "バックアップ", ""},
		{"No", "ルータ", "IP", "ルータ", "IP"},
		{"1", "fw01a", "192.0.2.1", "fw01b", "192.0.2.2"},
		{"2", "core01", "192.0.2.11", "", ""},
	}}
	r := &model.ImportRule{Name: "dup", HeaderRow: 2,
		Fields: []model.ImportField{
			{Key: "name", Source: "column", Column: "ルータ"},
			{Key: "host", Source: "column", Column: "IP"},
		},
		Units: []model.ImportUnit{{Label: "バックアップ", Fields: []model.ImportField{
			{Key: "name", Source: "column", Column: "ルータ (2)"},
			{Key: "host", Source: "column", Column: "IP (2)"},
		}}},
	}
	tb, err := ReadTable(s, r)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range tb.Headers {
		names = append(names, h.Name+":"+model.ColumnLetter(h.Col))
	}
	want := []string{"No:A", "ルータ:B", "IP:C", "ルータ (2):D", "IP (2):E"}
	if len(names) != len(want) {
		t.Fatalf("headers = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("header %d = %s, want %s", i, names[i], want[i])
		}
	}
	c := Convert(tb, r)
	if len(c.Rows) != 3 {
		t.Fatalf("rows = %+v", c.Rows)
	}
	if v := c.Rows[1].Values; v["name"] != "fw01b" || v["host"] != "192.0.2.2" {
		t.Errorf("backup read from the first block: %v", v)
	}
	if v := c.Rows[0].Values; v["name"] != "fw01a" || v["host"] != "192.0.2.1" {
		t.Errorf("primary: %v", v)
	}
}
