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
