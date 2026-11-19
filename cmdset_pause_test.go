package main

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A negative wait is refused where a set is saved or created, with its row;
// 0 passes, and so does a set whose waits are off (its fields are greyed in
// the editor, the values are kept and never used).
func TestCheckCommandSetRefusesNegativeWait(t *testing.T) {
	neg := []model.Command{{Text: "show clock", PauseSec: 1, SerialSec: 1}, {Text: "show version", PauseSec: -2, SerialSec: 1}}
	cs := model.CommandSet{Name: "lab-show", Commands: neg}
	if err := checkCommandSet(&cs); err == nil || !strings.Contains(err.Error(), "2 行目の待機秒がマイナス") {
		t.Errorf("a negative wait must be refused with its row: %v", err)
	}
	off := model.CommandSet{Name: "lab-show", Commands: neg, PauseOff: true}
	if err := checkCommandSet(&off); err != nil {
		t.Errorf("a set whose waits are off must still save: %v", err)
	}
	zero := model.CommandSet{Name: "lab-show", Commands: []model.Command{{Text: "show clock"}, {Text: "show version", PauseSec: 0, SerialSec: 30}}}
	if err := checkCommandSet(&zero); err != nil {
		t.Errorf("0 must pass: %v", err)
	}
}

// 全行の待ち方 is checked as a line's wait is (a negative second refused
// while the waits are used) and is kept by a save and a copy.
func TestCommandSetDefaultWait(t *testing.T) {
	cmds := []model.Command{{Text: "show clock", Wait: "#"}}
	neg := model.CommandSet{Name: "lab-show", Commands: cmds, Default: &model.WaitDefault{Wait: "#", PauseSec: -1}}
	if err := checkCommandSet(&neg); err == nil || !strings.Contains(err.Error(), "全行の待ち方の秒がマイナス") {
		t.Errorf("a negative default must be refused: %v", err)
	}
	neg.PauseOff = true
	if err := checkCommandSet(&neg); err != nil {
		t.Errorf("waits off: the default seconds are not used: %v", err)
	}
	bad := model.CommandSet{Name: "lab-show", Commands: cmds, Default: &model.WaitDefault{Wait: "a\nb"}}
	if err := checkCommandSet(&bad); err == nil || !strings.Contains(err.Error(), "全行の待ち方の待つ文字列に") {
		t.Errorf("a line break in the default wait must be refused: %v", err)
	}

	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateCommandSet(model.CommandSet{Name: "lab-show", Commands: cmds, Default: &model.WaitDefault{Wait: " # ", PauseSec: 0, SerialSec: 2}}); err != nil {
		t.Fatal(err)
	}
	nm, err := a.CopyCommandSet("lab-show")
	if err != nil {
		t.Fatal(err)
	}
	for _, cs := range a.GetInventory().CommandSets {
		if cs.Name != "lab-show" && cs.Name != nm {
			continue
		}
		if cs.Default == nil || *cs.Default != (model.WaitDefault{Wait: "#", PauseSec: 0, SerialSec: 2}) {
			t.Errorf("%s: default = %+v", cs.Name, cs.Default)
		}
	}
}
