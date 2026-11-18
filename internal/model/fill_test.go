package model

import (
	"reflect"
	"strings"
	"testing"
)

func cmdsOf(lines ...string) []Command {
	out := make([]Command, len(lines))
	for i, l := range lines {
		out[i] = Command{Text: l, PauseSec: 1, SerialSec: 2}
	}
	return out
}

func textsOf(cmds []Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Text
	}
	return out
}

func TestFillExpand(t *testing.T) {
	set := cmdsOf(
		"configure terminal",
		"interface {{if}}",
		" description {{desc?}}",
		"{{#each}}",
		"vlan {{ vlan }}",
		" name {{vlan_name?}}",
		"{{/each}}",
		"ip route 198.51.100.0 255.255.255.0 {{nexthop}}",
		"end",
	)
	rows := []map[string]string{
		{"if": "Gi1/0/2", "desc": "", "vlan": "110", "vlan_name": "", "nexthop": " 192.0.2.1 "},
		{"vlan": "130", "vlan_name": "lab-print"},
	}
	got, err := ExpandFill(set, rows, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"configure terminal",
		"interface Gi1/0/2",
		"vlan 110",
		"vlan 130",
		" name lab-print",
		"ip route 198.51.100.0 255.255.255.0 192.0.2.1",
		"end",
	}
	if !reflect.DeepEqual(textsOf(got), want) {
		t.Fatalf("got %q\nwant %q", textsOf(got), want)
	}
	// Each line keeps its own waits.
	for _, c := range got {
		if c.PauseSec != 1 || c.SerialSec != 2 {
			t.Fatalf("waits lost: %+v", c)
		}
	}
	names := FillNames(set)
	wantNames := []FillName{{Name: "if"}, {Name: "desc", Optional: true}, {Name: "vlan", InEach: true},
		{Name: "vlan_name", Optional: true, InEach: true}, {Name: "nexthop"}}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("names %+v", names)
	}
}

func TestFillEmptyRequiredStops(t *testing.T) {
	set := cmdsOf("{{#each}}", "vlan {{vlan}}", "{{/each}}")
	_, err := ExpandFill(set, []map[string]string{{"vlan": "110"}, {"vlan": ""}}, nil)
	if err == nil || !strings.Contains(err.Error(), "「vlan」の値が空です") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ExpandFill(set, nil, nil); err == nil {
		t.Fatal("no rows must fail")
	}
}

func TestFillValueRefused(t *testing.T) {
	set := cmdsOf("description {{desc}}")
	for _, v := range []string{"a\rreload", "x{enter}", "{password}", "a\nb"} {
		if _, err := ExpandFill(set, []map[string]string{{"desc": v}}, nil); err == nil {
			t.Fatalf("value %q must be refused", v)
		}
	}
}

func TestFillLeavesOtherBracesAlone(t *testing.T) {
	// Go templates of a Linux command, Junos braces, key tokens, login
	// variables: none of them is a placeholder.
	set := cmdsOf(`docker ps --format '{{.Names}}'`, `docker inspect -f '{{json .}}' x`, "set interfaces { ge-0/0/0 }", "{ctrl+z}", "enable {enable}")
	if HasFill(set) {
		t.Fatal("no placeholder expected")
	}
	if err := CheckCommands(set); err != nil {
		t.Fatal(err)
	}
}

func TestFillCheck(t *testing.T) {
	bad := map[string][]Command{
		"marker shares line": cmdsOf("x {{#each}}", "{{/each}}"),
		"not closed":         cmdsOf("{{#each}}", "vlan {{vlan}}"),
		"no open":            cmdsOf("vlan {{vlan}}", "{{/each}}"),
		"nested":             cmdsOf("{{#each}}", "{{#each}}", "{{/each}}", "{{/each}}"),
		"other block":        cmdsOf("{{#if}}", "{{/if}}"),
		"key token name":     cmdsOf("x {{enter}}"),
		"variable name":      cmdsOf("x {{password}}"),
	}
	for name, cmds := range bad {
		if err := CheckCommands(cmds); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	if err := CheckCommands(cmdsOf("{{#each}}", "vlan {{vlan}}", "{{/each}}", "{{#each}}", " name {{ネクストホップ}}", "{{/each}}")); err != nil {
		t.Fatalf("two blocks one after the other: %v", err)
	}
}

func TestFillSkipFromRule(t *testing.T) {
	set := cmdsOf("interface {{if}}", " description {{desc}}", "exit")
	got, err := ExpandFill(set, []map[string]string{{"if": "Gi1/0/1", "desc": ""}}, map[string]bool{"desc": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "interface Gi1/0/1" || got[1].Text != "exit" {
		t.Fatalf("the empty skip value's line must be left out: %+v", got)
	}
	if _, err := ExpandFill(set, []map[string]string{{"if": "", "desc": "x"}}, map[string]bool{"desc": true}); err == nil {
		t.Fatal("an empty value not in skip must stop the device")
	}
}

func TestFillRuleCheck(t *testing.T) {
	ok := FillRule{Name: "r", HeaderRow: 1, Key: ImportField{Source: "column", Column: "ホスト名"},
		Items: []FillItem{{Name: "NH", ImportField: ImportField{Source: "column", Column: "ネクストホップ"}}}}
	if err := CheckFillRule(&ok); err != nil {
		t.Fatal(err)
	}
	ir := ok.AsImportRule()
	if len(ir.Fields) != 2 || ir.Fields[0].Key != "name" || ir.Fields[1].Key != FillItemKey("NH") {
		t.Fatalf("as an import rule: %+v", ir.Fields)
	}
	bad := map[string]func(r *FillRule){
		"no name":        func(r *FillRule) { r.Name = " " },
		"no key":         func(r *FillRule) { r.Key.Source = "" },
		"no items":       func(r *FillRule) { r.Items = nil },
		"item no column": func(r *FillRule) { r.Items[0].Source = "" },
		"bad item name":  func(r *FillRule) { r.Items[0].Name = "1st" },
		"key token name": func(r *FillRule) { r.Items[0].Name = "enter" },
		"two of a name": func(r *FillRule) {
			r.Items = append(r.Items, FillItem{Name: "nh", ImportField: ImportField{Source: "column", Column: "x"}})
		},
	}
	for name, f := range bad {
		r := ok
		r.Items = append([]FillItem(nil), ok.Items...)
		f(&r)
		if err := CheckFillRule(&r); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestSetFillRows(t *testing.T) {
	f := &SetFill{Devices: []FillDevice{{Name: " Lab-SW1 ", Rows: []map[string]string{{"NH": "192.0.2.1"}}}}}
	if rows := f.RowsFor("lab-sw1"); len(rows) != 1 {
		t.Fatalf("case and spaces must not count: %v", rows)
	}
	if f.RowsFor("lab-sw2") != nil {
		t.Fatal("a device the list lacks has no rows")
	}
	c := f.Clone()
	c.Devices[0].Rows[0]["NH"] = "x"
	if f.Devices[0].Rows[0]["NH"] != "192.0.2.1" {
		t.Fatal("a clone must not share the values")
	}
}
