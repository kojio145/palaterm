package listimport

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// A second jump host mapped into the rule after the first import (2026-11-06
// user: "踏み台2台目も読み込んだけど機器一覧に反映されない"): the device
// has nothing there, the file has a value, and no record of the last import
// says anything about it — it is filled, not asked and kept by default.
func TestFillEmptyFieldAddedToRule(t *testing.T) {
	s := &sheet.Sheet{Name: "s", Rows: [][]string{
		{"name", "ip", "j1", "j2", "j2user", "legacy"},
		{"lab-sw1", "192.0.2.11", "192.0.2.1", "192.0.2.2", "ops", "true"},
	}}
	r := &model.ImportRule{Name: "t", HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "name"},
		{Key: "host", Source: "column", Column: "ip"},
		{Key: "bastion1Host", Source: "column", Column: "j1"},
	}}
	inv := lab()
	Apply(inv, plan(t, inv, s, r, nil), "拠点A", nil)
	if d := dev(inv, "lab-sw1"); d == nil || len(d.Bastions) != 1 {
		t.Fatalf("first import: %+v", d)
	}
	r.Fields = append(r.Fields,
		model.ImportField{Key: "bastion2Host", Source: "column", Column: "j2"},
		model.ImportField{Key: "bastion2Username", Source: "column", Column: "j2user"},
		model.ImportField{Key: "bastion2LegacyAlgos", Source: "column", Column: "legacy"},
	)
	p := plan(t, inv, s, r, nil)
	row := rowOf(p, 2)
	if c := cell(row, "bastion2Host"); c.State != "fill" || c.Choice != "file" {
		t.Errorf("bastion2Host = %+v, want fill/file", c)
	}
	if c := cell(row, "bastion2Username"); c.State != "fill" || c.Choice != "file" {
		t.Errorf("bastion2Username = %+v, want fill/file", c)
	}
	// A yes/no has no "empty": its empty cell is a "no", so it is asked.
	if c := cell(row, "bastion2LegacyAlgos"); c.State != "ask" || c.Choice != "keep" {
		t.Errorf("bastion2LegacyAlgos = %+v, want ask/keep", c)
	}
	if row.Status != "ask" {
		t.Errorf("status = %s (the yes/no is asked)", row.Status)
	}
	Apply(inv, p, "拠点A", nil)
	d := dev(inv, "lab-sw1")
	if len(d.Bastions) != 2 || d.Bastions[1].Host != "192.0.2.2" || d.Bastions[1].Username != "ops" || d.Bastions[1].LegacyAlgos {
		t.Errorf("after: %+v", d.Bastions)
	}
	// The hop removed in PalaTerm after that import: the record says the
	// source had it, so this is PalaTerm's change and it stays removed.
	d.Bastions = d.Bastions[:1]
	p = plan(t, inv, s, r, nil)
	if c := cell(rowOf(p, 2), "bastion2Host"); c.State != "keep" || rowOf(p, 2).Status != "same" {
		t.Errorf("removed in PalaTerm: %+v / %s", c, rowOf(p, 2).Status)
	}

	// Without the yes/no the row is a plain update; 「残す」 chosen for the
	// row still keeps PalaTerm's empty value.
	r.Fields = r.Fields[:len(r.Fields)-1]
	fresh := func() *model.Inventory {
		inv := lab()
		Apply(inv, plan(t, inv, s, &model.ImportRule{Name: "t", HeaderRow: 1, Fields: r.Fields[:3]}, nil), "拠点A", nil)
		return inv
	}
	inv = fresh()
	p = plan(t, inv, s, r, nil)
	if rowOf(p, 2).Status != "upd" {
		t.Errorf("status = %s, want upd", rowOf(p, 2).Status)
	}
	Apply(inv, p, "拠点A", nil)
	if d := dev(inv, "lab-sw1"); len(d.Bastions) != 2 {
		t.Errorf("filled: %+v", d.Bastions)
	}
	inv = fresh()
	p = plan(t, inv, s, r, map[int]Decision{2: {Choices: map[string]string{"bastion2Host": "keep", "bastion2Username": "keep"}}})
	Apply(inv, p, "拠点A", nil)
	if d := dev(inv, "lab-sw1"); len(d.Bastions) != 1 {
		t.Errorf("kept: %+v", d.Bastions)
	}
}

// A device made by hand before any import: what it has is asked, what it
// lacks is filled.
func TestFillHandMadeDevice(t *testing.T) {
	inv := lab()
	inv.Devices = []model.Device{{ID: "a1", Name: "core-sw01", Group: "拠点A", Host: "192.0.2.99", Conn: "ssh", OSType: "cisco-ios", Enabled: true}}
	p := plan(t, inv, fixture(), fixtureRule(), nil)
	row := rowOf(p, 5)
	if c := cell(row, "host"); c.State != "ask" || c.Choice != "keep" {
		t.Errorf("host = %+v, want ask/keep", c)
	}
	for _, k := range []string{"username", "password", "site"} {
		if c := cell(row, k); c.State != "fill" || c.Choice != "file" {
			t.Errorf("%s = %+v, want fill/file", k, c)
		}
	}
	if row.Status != "ask" {
		t.Errorf("status = %s", row.Status)
	}
}
