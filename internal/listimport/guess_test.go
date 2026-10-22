package listimport

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// stdEnv is a PalaTerm with the standard OS types and the sample command
// sets, plus a set that changes devices.
func stdEnv() *Env {
	env := &Env{}
	for _, p := range [][2]string{
		{"cisco-ios", "Cisco IOS"}, {"cisco-asa", "Cisco ASA"}, {"juniper-junos", "Juniper JUNOS"}, {"nec-ix", "NEC IX"},
		{"nec-qx", "NEC QX"}, {"fortinet-fortios", "Fortinet FortiGate"}, {"alaxala-ax", "ALAXALA AX"}, {"yamaha-rtx", "Yamaha RTX"},
		{"aruba-os-switch", "Aruba ArubaOS-Switch"}, {"hpe-comware", "HPE (Comware)"}, {"generic", "Generic (no automation)"},
	} {
		env.Profiles = append(env.Profiles, EnvProfile{Key: p[0], Name: p[1]})
	}
	for _, n := range []string{"【サンプル】Cisco_ルータ", "【サンプル】Cisco_L2SW", "【サンプル】Cisco_L3SW", "【サンプル】FortiGate", "【サンプル】IXルータ", "【サンプル】ALAXALA_L2SW", "月次 show"} {
		env.Sets = append(env.Sets, EnvSet{Name: n, Safe: true})
	}
	env.Sets = append(env.Sets, EnvSet{Name: "Cisco_L2SW 設定投入", Safe: false})
	return env
}

func TestGuessOSAndSet(t *testing.T) {
	env := stdEnv()
	for _, c := range []struct{ model, os, set string }{
		{"IX2215", "nec-ix", "【サンプル】IXルータ"},
		{"UNIVERGE IX3110", "nec-ix", "【サンプル】IXルータ"},
		{"NEC IX2106 (架空)", "nec-ix", "【サンプル】IXルータ"},
		{"C2960", "cisco-ios", "【サンプル】Cisco_L2SW"}, // the unsafe "Cisco_L2SW 設定投入" is never chosen
		{"WS-C2960X-24TS-L", "cisco-ios", "【サンプル】Cisco_L2SW"},
		{"Catalyst 9300", "cisco-ios", "【サンプル】Cisco_L3SW"},
		{"C3850", "cisco-ios", "【サンプル】Cisco_L3SW"},
		{"ISR4331", "cisco-ios", "【サンプル】Cisco_ルータ"},
		{"C2911", "cisco-ios", "【サンプル】Cisco_ルータ"},
		{"C8200-1N-4T", "cisco-ios", "【サンプル】Cisco_ルータ"},
		{"FortiGate 60F", "fortinet-fortios", ""}, // the sample set names no kind: FW wanted
		{"FG-100F", "fortinet-fortios", ""},
		{"AX2530S", "alaxala-ax", "【サンプル】ALAXALA_L2SW"},
		{"AX3660S", "alaxala-ax", ""}, // L3: no L3 set
		{"RTX1300", "yamaha-rtx", ""},
		{"SRX300", "juniper-junos", ""},
		{"ASA5506", "cisco-asa", ""},
		{"Aruba 2930F", "aruba-os-switch", ""},
		{"QX-S1016GT", "nec-qx", ""},
		{"Catalyst", "cisco-ios", ""}, // no number: kind unknown, several Cisco sets → none
		{"謎の機器 XZ-9", "", ""},
		{"", "", ""},
	} {
		if got := GuessOS(c.model, env); got != c.os {
			t.Errorf("GuessOS(%q) = %q, want %q", c.model, got, c.os)
		}
		if got := GuessSet(c.model, env); got != c.set {
			t.Errorf("GuessSet(%q) = %q, want %q", c.model, got, c.set)
		}
	}
	// A value already an OS type / a command set (a 置き換え表 before) stays.
	if GuessOS("nec-ix", env) != "nec-ix" || GuessOS("Cisco IOS", env) != "cisco-ios" || GuessSet("月次 show", env) != "月次 show" {
		t.Error("an exact value was not kept")
	}
	// The standard OS type gone, the one custom OS type naming the line.
	custom := &Env{Profiles: []EnvProfile{{"cisco-l2", "Cisco L2 (社内)"}, {"generic", "Generic"}}}
	if got := GuessOS("C2960", custom); got != "cisco-l2" {
		t.Errorf("custom = %q", got)
	}
	two := &Env{Profiles: []EnvProfile{{"c1", "Cisco A"}, {"c2", "Cisco B"}}}
	if got := GuessOS("C2960", two); got != "" {
		t.Errorf("two candidates = %q, want none", got)
	}
	// Short line words are words: "AX" is not in "MAX", "WA" not in "WAN".
	if hasWord("MAX_SET", "AX") || hasWord("WAN-SHOW", "WA") || !hasWord("CISCO_L2SW", "L2") || !hasWord("【サンプル】IXルータ", "IX") || hasWord("RTX", "RT") {
		t.Error("hasWord")
	}
}

// A guessed command set goes to new devices only: an existing device keeps
// its own, the preview marking the guess.
func TestGuessedSetKeepsExisting(t *testing.T) {
	s := &sheet.Sheet{Rows: [][]string{{"機器名", "IP", "機種"}, {"rt01", "192.0.2.1", "IX2215"}, {"sw01", "192.0.2.2", "C2960"}}}
	r := &model.ImportRule{Name: "g", HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "機器名"},
		{Key: "host", Source: "column", Column: "IP"},
		{Key: "osType", Source: "column", Column: "機種", Steps: []model.ImportStep{{Type: "guessOS"}}, Default: "generic"},
		{Key: "commandSet", Source: "column", Column: "機種", Steps: []model.ImportStep{{Type: "guessSet"}}},
	}}
	if err := ValidateRule(r); err != nil {
		t.Fatal(err)
	}
	tb, err := ReadTable(s, r)
	if err != nil {
		t.Fatal(err)
	}
	inv := lab()
	inv.Devices = []model.Device{{ID: "a1", Name: "sw01", Group: "拠点A", Host: "192.0.2.2", Conn: "ssh", OSType: "cisco-ios", CommandSet: "月次 show", Enabled: true}}
	p := BuildPlan(inv, Convert(tb, r, stdEnv()), r, Options{Group: "拠点A"})
	add, upd := rowOf(p, 2), rowOf(p, 3)
	if c := cell(add, "commandSet"); add.Status != "add" || c.File != "【サンプル】IXルータ" || !c.Guessed {
		t.Errorf("new device: %s %+v", add.Status, c)
	}
	if c := cell(add, "osType"); c.File != "nec-ix" || !c.Guessed {
		t.Errorf("new device os: %+v", c)
	}
	if c := cell(upd, "commandSet"); c.State != "guess" || c.Choice != "keep" {
		t.Errorf("existing device: %+v", c)
	}
	Apply(inv, p, "拠点A", nil)
	if d := dev(inv, "sw01"); d.CommandSet != "月次 show" {
		t.Errorf("existing command set changed to %q", d.CommandSet)
	}
	if d := dev(inv, "rt01"); d.CommandSet != "【サンプル】IXルータ" || d.OSType != "nec-ix" {
		t.Errorf("new device = %q %q", d.CommandSet, d.OSType)
	}
	// Without an environment (an old caller), the steps pass values on.
	if v := ApplySteps("IX2215", []model.ImportStep{{Type: "guessOS"}}, nil); v != "IX2215" {
		t.Errorf("no env: %q", v)
	}
}

func TestSuggestModelColumn(t *testing.T) {
	s := &sheet.Sheet{Rows: [][]string{{"ホスト名", "管理IP", "機種", "機種コード"}, {"rt01", "192.0.2.1", "IX2215", "X1"}}}
	sg := Suggest(s, model.ImportRule{HeaderRow: 1})
	var os, set model.ImportField
	for _, f := range sg.Fields {
		switch f.Key {
		case "osType":
			os = f
		case "commandSet":
			set = f
		}
	}
	if os.Column != "機種" || len(os.Steps) != 1 || os.Steps[0].Type != "guessOS" || set.Column != "機種" || set.Steps[0].Type != "guessSet" {
		t.Errorf("os %+v set %+v", os, set)
	}
}
