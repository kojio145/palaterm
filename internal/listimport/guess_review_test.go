package listimport

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

func guessRule(extra ...model.ImportStep) *model.ImportRule {
	return &model.ImportRule{Name: "g", HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "機器名"},
		{Key: "host", Source: "column", Column: "IP"},
		{Key: "osType", Source: "column", Column: "機種", Steps: []model.ImportStep{{Type: "guessOS"}}, Default: "generic"},
		{Key: "commandSet", Source: "column", Column: "機種", Steps: append(append([]model.ImportStep{}, extra...), model.ImportStep{Type: "guessSet"})},
	}}
}

func guessPlan(t *testing.T, inv *model.Inventory, r *model.ImportRule, env *Env) *Plan {
	t.Helper()
	s := &sheet.Sheet{Rows: [][]string{{"機器名", "IP", "機種"}, {"sw01", "192.0.2.2", "C2960"}}}
	tb, err := ReadTable(s, r)
	if err != nil {
		t.Fatal(err)
	}
	return BuildPlan(inv, Convert(tb, r, env), r, Options{Group: "拠点A"})
}

// A guess that comes back empty on a re-import (another matching set was
// added since) does not wipe the existing device's command set or OS type.
func TestGuessChangeKeepsExisting(t *testing.T) {
	inv := lab()
	r := guessRule()
	Apply(inv, guessPlan(t, inv, r, stdEnv()), "拠点A", nil)
	d := dev(inv, "sw01")
	if d.CommandSet != "【サンプル】Cisco_L2SW" || d.OSType != "cisco-ios" {
		t.Fatalf("first import: %q %q", d.CommandSet, d.OSType)
	}
	env := stdEnv()
	env.Sets = append(env.Sets, EnvSet{Name: "Cisco L2SW 月次", Safe: true})                                          // two matches now
	env.Profiles = append(env.Profiles[1:], EnvProfile{"cisco-l2", "Cisco L2"}, EnvProfile{"cisco-l3", "Cisco L3"}) // the standard one gone, two custom
	p := guessPlan(t, inv, r, env)
	row := rowOf(p, 2)
	if c := cell(row, "commandSet"); c.State != "guess" || c.Choice != "keep" {
		t.Errorf("command set cell: %+v", c)
	}
	if c := cell(row, "osType"); c.State != "guess" || c.Choice != "keep" {
		t.Errorf("os cell: %+v", c)
	}
	Apply(inv, p, "拠点A", nil)
	if d := dev(inv, "sw01"); d.CommandSet != "【サンプル】Cisco_L2SW" || d.OSType != "cisco-ios" {
		t.Errorf("re-import changed the device: %q %q", d.CommandSet, d.OSType)
	}
}

// Names with spaces match as words.
func TestGuessSpacedNames(t *testing.T) {
	env := &Env{
		Profiles: []EnvProfile{{"custom1", "Cisco Catalyst"}, {"generic", "Generic"}},
		Sets:     []EnvSet{{Name: "Cisco L2SW", Safe: true}, {Name: "FortiGate FW", Safe: true}},
	}
	if got := GuessOS("C2960", env); got != "custom1" {
		t.Errorf("os = %q", got)
	}
	if got := GuessSet("C2960", env); got != "Cisco L2SW" {
		t.Errorf("set = %q", got)
	}
	if got := GuessSet("FortiGate 60F", env); got != "FortiGate FW" {
		t.Errorf("forti set = %q", got)
	}
}

// UNIVERGE before a QX switch or a WA model is not an IX router.
func TestGuessUniverge(t *testing.T) {
	env := stdEnv()
	env.Profiles = append(env.Profiles, EnvProfile{"nec-wa", "NEC WA"})
	for model, want := range map[string]string{"UNIVERGE QX-S1016GT": "nec-qx", "UNIVERGE WA2612": "nec-wa", "UNIVERGE IX2215": "nec-ix", "IX3110": "nec-ix"} {
		if got := GuessOS(model, env); got != want {
			t.Errorf("GuessOS(%q) = %q, want %q", model, got, want)
		}
	}
	if got := GuessSet("UNIVERGE QX-S1016GT", env); got == "【サンプル】IXルータ" {
		t.Error("a QX switch got the IX router set")
	}
}

// A command set a 置き換え表 decided before the guess is the file's word:
// not a guess, and it reaches an existing device as usual.
func TestMappedSetIsNoGuess(t *testing.T) {
	inv := lab()
	inv.Devices = []model.Device{{ID: "a1", Name: "sw01", Group: "拠点A", Host: "192.0.2.2", Conn: "ssh", OSType: "cisco-ios", CommandSet: "【サンプル】Cisco_L3SW", Enabled: true}}
	inv.ImportRecords = map[string]model.ImportRecord{"a1": {Key: "sw01", Base: map[string]string{"commandSet": "【サンプル】Cisco_L3SW"}}}
	r := guessRule(model.ImportStep{Type: "map", Miss: "keep", Rows: []model.ImportMapRow{{Match: "equals", From: "C2960", To: "月次 show"}}})
	p := guessPlan(t, inv, r, stdEnv())
	c := cell(rowOf(p, 2), "commandSet")
	if c.Guessed || c.State != "take" || c.File != "月次 show" {
		t.Errorf("mapped set: %+v", c)
	}
	Apply(inv, p, "拠点A", nil)
	if d := dev(inv, "sw01"); d.CommandSet != "月次 show" {
		t.Errorf("command set = %q", d.CommandSet)
	}
}

// A kind column (ルータ, L2SW) is no model column, even left of the model.
func TestSuggestSkipsKindColumn(t *testing.T) {
	s := &sheet.Sheet{Rows: [][]string{{"機器名", "機器種別", "機種"}, {"rt01", "ルータ", "IX2215"}}}
	for _, f := range Suggest(s, model.ImportRule{HeaderRow: 1}).Fields {
		if (f.Key == "osType" || f.Key == "commandSet") && f.Column != "機種" {
			t.Errorf("%s from %q", f.Key, f.Column)
		}
	}
}
