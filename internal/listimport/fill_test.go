package listimport

import (
	"strings"
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

// A record from v1.9.30 or before (V 0) that recorded a cell asked and
// left on 残す: the device never had the value, and every later import
// read it as PalaTerm's change. Filled once; the record written then (V 1)
// makes a hop removed here afterwards stay removed.
func TestFillRescuesOldRecord(t *testing.T) {
	s := &sheet.Sheet{Name: "s", Rows: [][]string{
		{"name", "ip", "j1", "j2", "j2user"},
		{"lab-sw1", "192.0.2.11", "192.0.2.1", "192.0.2.2", "ops"},
	}}
	r := &model.ImportRule{Name: "t", HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "name"},
		{Key: "host", Source: "column", Column: "ip"},
		{Key: "bastion1Host", Source: "column", Column: "j1"},
		{Key: "bastion2Host", Source: "column", Column: "j2"},
		{Key: "bastion2Username", Source: "column", Column: "j2user"},
	}}
	inv := lab()
	inv.Devices = []model.Device{{ID: "a1", Name: "lab-sw1", Group: "拠点A", Host: "192.0.2.11", Conn: "ssh", OSType: "generic", Enabled: true,
		Bastions: []model.Bastion{{Host: "192.0.2.1", Method: "ssh"}}}}
	inv.ImportRecords = map[string]model.ImportRecord{"a1": {Key: "lab-sw1", Base: map[string]string{
		"name": "lab-sw1", "host": "192.0.2.11", "bastion1Host": "192.0.2.1", "bastion2Host": "192.0.2.2", "bastion2Username": "ops"}}}
	p := plan(t, inv, s, r, nil)
	row := rowOf(p, 2)
	if c := cell(row, "bastion2Host"); c.State != "fill" || c.Choice != "file" {
		t.Errorf("old record: bastion2Host = %+v, want fill/file", c)
	}
	if row.Status != "upd" || len(row.Errors) != 0 {
		t.Errorf("status = %s errors %v", row.Status, row.Errors)
	}
	Apply(inv, p, "拠点A", nil)
	d := dev(inv, "lab-sw1")
	if len(d.Bastions) != 2 || d.Bastions[1].Host != "192.0.2.2" || d.Bastions[1].Username != "ops" {
		t.Fatalf("after: %+v", d.Bastions)
	}
	if inv.ImportRecords["a1"].V != 1 {
		t.Errorf("record V = %d, want 1", inv.ImportRecords["a1"].V)
	}
	// Removed here after that: a record of V 1 says so.
	d.Bastions = d.Bastions[:1]
	p = plan(t, inv, s, r, nil)
	if c := cell(rowOf(p, 2), "bastion2Host"); c.State != "keep" || rowOf(p, 2).Status != "same" {
		t.Errorf("removed here: %+v / %s", c, rowOf(p, 2).Status)
	}
}

// The list gives the second hop only (the first is the device's own, set
// by hand or by 一括変更): the file's hop keeps its number, so an existing
// device keeps its first hop and takes the second.
func TestHopNumbersKept(t *testing.T) {
	s := &sheet.Sheet{Name: "s", Rows: [][]string{
		{"name", "ip", "j2", "j2port"},
		{"lab-sw1", "192.0.2.11", "192.0.2.2", "0022"},
		{"lab-sw2", "192.0.2.12", "192.0.2.3", ""},
	}}
	r := &model.ImportRule{Name: "t", HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "name"},
		{Key: "host", Source: "column", Column: "ip"},
		{Key: "bastion2Host", Source: "column", Column: "j2"},
		{Key: "bastion2Port", Source: "column", Column: "j2port"},
	}}
	inv := lab()
	inv.Devices = []model.Device{{ID: "a1", Name: "lab-sw1", Group: "拠点A", Host: "192.0.2.11", Conn: "ssh", OSType: "generic", Enabled: true,
		Bastions: []model.Bastion{{Host: "192.0.2.1", Method: "ssh", Username: "jump"}}}}
	p := plan(t, inv, s, r, nil)
	row := rowOf(p, 2)
	if c := cell(row, "bastion2Host"); c.State != "fill" || c.File != "192.0.2.2" {
		t.Errorf("bastion2Host = %+v", c)
	}
	if c := cell(row, "bastion2Port"); c.File != "22" {
		t.Errorf("bastion2Port = %+v (canonical 22)", c)
	}
	if cell(row, "bastion1Host").Key != "" {
		t.Errorf("bastion1Host is not a cell of the file: %+v", cell(row, "bastion1Host"))
	}
	if len(row.Errors) != 0 || row.Status != "upd" {
		t.Errorf("row = %s %v", row.Status, row.Errors)
	}
	// A new device cannot hold the gap: an error that says what to do.
	r3 := rowOf(p, 3)
	if len(r3.Errors) == 0 || !strings.Contains(r3.Errors[0], "踏み台1が空のまま踏み台2") {
		t.Errorf("new device with a gap: %v", r3.Errors)
	}
	Apply(inv, p, "拠点A", nil)
	d := dev(inv, "lab-sw1")
	if len(d.Bastions) != 2 || d.Bastions[0].Host != "192.0.2.1" || d.Bastions[0].Username != "jump" || d.Bastions[1].Host != "192.0.2.2" || d.Bastions[1].Port != 22 {
		t.Errorf("after: %+v", d.Bastions)
	}
	if dev(inv, "lab-sw2") != nil {
		t.Errorf("the row with the gap was written")
	}
	// An existing device without a first hop (made by hand, no record) and
	// the file's second: the same error, nothing closed quietly.
	inv2 := lab()
	inv2.Devices = []model.Device{{ID: "a2", Name: "lab-sw1", Group: "拠点A", Host: "192.0.2.11", Conn: "ssh", OSType: "generic", Enabled: true}}
	p = plan(t, inv2, s, r, nil)
	if e := rowOf(p, 2).Errors; len(e) == 0 || !strings.Contains(e[0], "踏み台1が空のまま") {
		t.Errorf("existing device with a gap: %v", e)
	}
	Apply(inv2, p, "拠点A", nil)
	if d := dev(inv2, "lab-sw1"); len(d.Bastions) != 0 {
		t.Errorf("written despite the gap: %+v", d.Bastions)
	}
}
