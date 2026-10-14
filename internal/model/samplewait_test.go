package model

import "testing"

// An older vault's sample sets get the sample 待つ文字列 where the field is
// empty; a wait the user set and a command the user added are left alone.
func TestFillSampleWaits(t *testing.T) {
	sets := DefaultCommandSets()
	for i := range sets {
		for j := range sets[i].Commands {
			sets[i].Commands[j].Wait = ""
		}
	}
	sets[0].Commands[0].Wait = "(mine)#"
	sets[0].Commands = append(sets[0].Commands, Command{Text: "show my thing"})
	other := CommandSet{Name: "mine", Commands: []Command{{Text: "show clock"}}}
	sets = append(sets, other)
	if !FillSampleWaits(sets) {
		t.Fatal("nothing filled")
	}
	if sets[0].Commands[0].Wait != "(mine)#" || sets[0].Commands[1].Wait != SampleWait {
		t.Errorf("sample commands: %+v", sets[0].Commands[:2])
	}
	if last := sets[0].Commands[len(sets[0].Commands)-1]; last.Wait != "" {
		t.Errorf("user's own command got a wait: %+v", last)
	}
	if sets[len(sets)-1].Commands[0].Wait != "" {
		t.Errorf("a set of the user's got a wait")
	}
	if FillSampleWaits(sets) {
		t.Error("second fill changed something")
	}
}
