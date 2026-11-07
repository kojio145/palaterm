package listimport

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

func fx6Sheet(rows ...[]string) *sheet.Sheet {
	return &sheet.Sheet{Name: "s", Rows: append([][]string{{"name", "ip", "os", "set", "j2", "j2user", "j3"}}, rows...)}
}

func fx6Rule(keys ...string) *model.ImportRule {
	r := &model.ImportRule{Name: "t", HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "name"},
		{Key: "host", Source: "column", Column: "ip"},
	}}
	cols := map[string]string{"osType": "os", "commandSet": "set", "bastion2Host": "j2", "bastion2Username": "j2user", "bastion3Host": "j3"}
	for _, k := range keys {
		r.Fields = append(r.Fields, model.ImportField{Key: k, Source: "column", Column: cols[k]})
	}
	return r
}

// D-A2 (withdrawn 2026-11-07 by the user): an OS type or command set the
// vault does not have is imported as written (the operator sets it after
// the import, as before); an OS type written as its display name becomes
// the key.
func TestPlanKeepsUnknownOSTypeAndSet(t *testing.T) {
	inv := lab()
	r := fx6Rule("osType", "commandSet")
	s := fx6Sheet(
		[]string{"lab-sw1", "192.0.2.11", "Cisco IOS", "月次 show", "", "", ""},
		[]string{"lab-sw2", "192.0.2.12", "no-such-os", "", "", "", ""},
		[]string{"lab-sw3", "192.0.2.13", "cisco-ios", "no-such-set", "", "", ""},
	)
	p := plan(t, inv, s, r, nil)
	if row := rowOf(p, 2); row.Status != "add" || cell(row, "osType").File != "cisco-ios" {
		t.Errorf("display name not taken as the key: %s %+v %v", row.Status, cell(row, "osType"), row.Errors)
	}
	if row := rowOf(p, 3); row.Status != "add" || len(row.Errors) != 0 || cell(row, "osType").File != "no-such-os" {
		t.Errorf("unknown OS type refused: %s %v", row.Status, row.Errors)
	}
	if row := rowOf(p, 4); row.Status != "add" || len(row.Errors) != 0 || cell(row, "commandSet").File != "no-such-set" {
		t.Errorf("unknown set refused: %s %v", row.Status, row.Errors)
	}
	Apply(inv, p, "拠点A", nil)
	if d := dev(inv, "lab-sw2"); d.OSType != "no-such-os" {
		t.Errorf("unknown OS type not written as it is: %q", d.OSType)
	}
	if d := dev(inv, "lab-sw3"); d.CommandSet != "no-such-set" {
		t.Errorf("unknown set not written as it is: %q", d.CommandSet)
	}
}

// D-B12: a differing cell with no record of the last import is "nobase",
// not "ask" (both changed); a choice of "file" still takes it.
func TestNoBaseState(t *testing.T) {
	inv := lab()
	inv.Devices = []model.Device{{ID: "d1", Name: "lab-sw1", Group: "拠点A", Host: "192.0.2.99", Conn: "ssh", OSType: "cisco-ios", Enabled: true}}
	s := fx6Sheet([]string{"lab-sw1", "192.0.2.11", "", "", "", "", ""})
	p := plan(t, inv, s, fx6Rule(), nil)
	row := rowOf(p, 2)
	if c := cell(row, "host"); c.State != "nobase" || c.Choice != "keep" || row.Status != "ask" {
		t.Fatalf("host = %+v status %s", c, row.Status)
	}
	p = plan(t, inv, s, fx6Rule(), map[int]Decision{2: {Choices: map[string]string{"host": "file"}}})
	if c := cell(rowOf(p, 2), "host"); c.State != "nobase" || c.Choice != "file" {
		t.Errorf("chosen: %+v", c)
	}
	Apply(inv, p, "拠点A", nil)
	if d := dev(inv, "lab-sw1"); d.Host != "192.0.2.11" {
		t.Errorf("file value not taken: %s", d.Host)
	}
}

// D-A8: an existing device given a jump host's user name with no host
// anywhere fails the row and says the host is not mapped — it used to be
// written without the hop, the preview saying nothing.
func TestExistingDeviceUnmappedHopHostIsAnError(t *testing.T) {
	inv := lab()
	inv.Devices = []model.Device{{ID: "d1", Name: "lab-sw1", Group: "拠点A", Host: "192.0.2.11", Conn: "ssh", OSType: "cisco-ios", Enabled: true}}
	s := fx6Sheet([]string{"lab-sw1", "192.0.2.11", "", "", "", "ops", ""})
	p := plan(t, inv, s, fx6Rule("bastion2Username"), nil)
	row := rowOf(p, 2)
	if row.Status != "err" || !strings.Contains(strings.Join(row.Errors, " "), "この項目はルールで紐付けていません") {
		t.Fatalf("status %s errors %v", row.Status, row.Errors)
	}
	// And only once: the merged device's error is not listed twice.
	n := 0
	for _, e := range row.Errors {
		if strings.Contains(e, "ホストが空") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("hop error listed %d times: %v", n, row.Errors)
	}
}

// D-B1: a row whose only hop is the third names hops 1 and 3 in the gap
// message, not "踏み台2".
func TestHopGapMessageNamesTheHopAfterTheGap(t *testing.T) {
	inv := lab()
	s := fx6Sheet([]string{"lab-sw1", "192.0.2.11", "", "", "", "", "192.0.2.3"})
	p := plan(t, inv, s, fx6Rule("bastion3Host"), nil)
	row := rowOf(p, 2)
	msg := strings.Join(row.Errors, " ")
	if row.Status != "err" || !strings.Contains(msg, "踏み台1が空のまま踏み台3があります") || strings.Contains(msg, "踏み台2") {
		t.Errorf("status %s: %v", row.Status, row.Errors)
	}
}

// D-C7: the check marks of a hand-kept list read as yes / no.
func TestNormalizeChoiceCheckMarks(t *testing.T) {
	for _, w := range []string{"✓", "✔", "レ", "済"} {
		if got := normalizeChoice("enabled", w); got != "true" {
			t.Errorf("%q → %q", w, got)
		}
	}
	for _, w := range []string{"x", "X", "✗"} {
		if got := normalizeChoice("enabled", w); got != "false" {
			t.Errorf("%q → %q", w, got)
		}
	}
}
