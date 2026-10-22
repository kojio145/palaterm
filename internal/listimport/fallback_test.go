package listimport

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// A device's address in whichever of two columns its line filled in (the
// PPPoE one or the gateway one).
func TestFallbackColumns(t *testing.T) {
	s := &sheet.Sheet{Rows: [][]string{
		{"機器名", "IP(PPPoE)", "IP(GW)"},
		{"rt01", "192.0.2.1/32", ""},
		{"rt02", "", "192.0.2.2/24"},
		{"rt03", "192.0.2.3/32", "192.0.2.33/24"}, // both: the first wins
		{"rt04", "", ""},                          // neither: the default
	}}
	r := &model.ImportRule{Name: "fb", HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "機器名"},
		{Key: "host", Source: "column", Column: "IP(PPPoE)", Fallback: []string{"IP(GW)"}, Default: "192.0.2.254",
			Steps: []model.ImportStep{{Type: "split", Sep: "/", Pos: "last", Keep: "left"}}},
	}}
	if err := ValidateRule(r); err != nil {
		t.Fatal(err)
	}
	c := convert(t, s, r)
	want := []struct{ host, from string }{{"192.0.2.1", ""}, {"192.0.2.2", "IP(GW)"}, {"192.0.2.3", ""}, {"192.0.2.254", ""}}
	for i, w := range want {
		row := c.Rows[i]
		if row.Values["host"] != w.host || row.From["host"] != w.from {
			t.Errorf("row %d: host %q from %q, want %q from %q", i+2, row.Values["host"], row.From["host"], w.host, w.from)
		}
	}
	// A fallback column the file does not have is reported missing.
	r.Fields[1].Fallback = []string{"IP(予備)", "IP(GW)"}
	c = convert(t, s, r)
	if len(c.Missing) != 1 || c.Missing[0] != "IP(予備)" || c.Rows[1].Values["host"] != "192.0.2.2" {
		t.Errorf("missing %v, row 3 %v", c.Missing, c.Rows[1].Values)
	}
	// An exclusion comparing a password's fallback column with a value
	// does not travel in an exported rule.
	r.Fields = append(r.Fields, model.ImportField{Key: "password", Source: "column", Column: "PW", Fallback: []string{"PW(旧)"}})
	r.Exclude = []model.ImportCond{{Target: "column", Key: "PW(旧)", Op: "equals", Value: "Init-Pw9"}}
	b, err := ExportRule(*r, "t")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Init-Pw9") || !strings.Contains(string(b), "IP(GW)") {
		t.Errorf("export:\n%s", b)
	}
}
