package listimport

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A row set to a new device that fails on its name still stands for the
// device of that name: it is not offered for deletion (deleting it and
// failing the row would lose the device).
func TestMissingKeepsDeviceOfAFailingRow(t *testing.T) {
	inv := lab()
	inv.Devices = []model.Device{labDev("a1", "sw1", "192.0.2.1"), labDev("a2", "sw9", "192.0.2.9")}
	p := plan(t, inv, small([]string{"", "sw1", "192.0.2.1", ""}), smallRule(), map[int]Decision{2: {Link: "new"}})
	if r := rowOf(p, 2); r.Status != "err" {
		t.Fatalf("row = %+v", r)
	}
	if len(p.Missing) != 1 || p.Missing[0].Name != "sw9" {
		t.Errorf("missing = %+v, want only sw9", p.Missing)
	}
}

// The same device picked by hand for two rows: the later row says so
// instead of quietly matching something else.
func TestPickedTwice(t *testing.T) {
	inv := lab()
	inv.Devices = []model.Device{labDev("a1", "sw1", "192.0.2.1"), labDev("a2", "sw2", "192.0.2.2")}
	s := small([]string{"", "sw1", "192.0.2.1", ""}, []string{"", "sw2", "192.0.2.2", ""})
	p := plan(t, inv, s, smallRule(), map[int]Decision{2: {Link: "a1"}, 3: {Link: "a1"}})
	if r := rowOf(p, 2); r.Link != "a1" || r.Status == "err" {
		t.Errorf("first pick: %+v", r)
	}
	r := rowOf(p, 3)
	if r.Status != "err" || r.Link != "a1" || len(r.Errors) == 0 || !strings.Contains(r.Errors[0], "2 行目で選ばれています") {
		t.Errorf("second pick: %+v", r)
	}
	// sw2's own row failed: sw2 is still in the file, not missing.
	if len(p.Missing) != 0 {
		t.Errorf("missing = %+v", p.Missing)
	}
}

// An exclusion comparing a column a password is read from with a value
// does not travel either.
func TestExportDropsSecretColumnConditions(t *testing.T) {
	r := model.ImportRule{
		Name: "x", HeaderRow: 1,
		Fields: []model.ImportField{
			{Key: "name", Source: "column", Column: "機器名称"},
			{Key: "password", Source: "column", Column: "パスワード"},
			{Key: "enablePassword", Source: "template", Template: "{特権}"},
		},
		Exclude: []model.ImportCond{
			{Target: "column", Key: "パスワード", Op: "equals", Value: "Init#1234"},
			{Target: "column", Key: "特権", Op: "contains", Value: "En#99"},
			{Target: "column", Key: "パスワード", Op: "empty"},
			{Target: "column", Key: "状態", Op: "contains", Value: "撤去"},
		},
	}
	b, err := ExportRule(r, "t")
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, s := range []string{"Init#1234", "En#99"} {
		if strings.Contains(out, s) {
			t.Errorf("%q exported", s)
		}
	}
	if !strings.Contains(out, "撤去") || strings.Count(out, `"op": "empty"`) != 1 {
		t.Errorf("the other conditions should stay:\n%s", out)
	}
}

func TestFieldLookups(t *testing.T) {
	if IsField("group") || !IsField("bastion1Host") || IsField("nope") {
		t.Error("IsField")
	}
	if !isSecret("password") || isSecret("host") || FieldLabel("host") != "IPアドレス" || FieldLabel("nope") != "nope" {
		t.Error("lookups")
	}
}
