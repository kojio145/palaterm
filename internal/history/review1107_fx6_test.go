package history

import (
	"strings"
	"testing"
)

// C-A1 (review 2026-11-07): a line to check by hand is a comment in the
// result's text — "! 手で確認（why）: line" on Cisco, "#" elsewhere — so a
// copy, a command set made of it or the run tab's 今回だけ入力 never types
// it as a command. Note keeps the reason.
func TestNoteLinesAreComments(t *testing.T) {
	a := wrapCisco("interface GigabitEthernet0/1\n!\n")
	b := wrapCisco("interface GigabitEthernet0/1\n!\ninterface GigabitEthernet0/2\n description x\n!\n")
	g := GenerateCommands(a, b, "rollback", "alaxala-ax")
	found := false
	for _, l := range g.Lines {
		if l.Note == "" {
			continue
		}
		found = true
		if !strings.HasPrefix(strings.TrimSpace(l.Text), "! 手で確認（"+l.Note+"）: ") {
			t.Errorf("note line not a comment: %q (note %q)", l.Text, l.Note)
		}
	}
	if !found || g.Manual == 0 {
		t.Fatalf("no line to check by hand:\n%s", joinCmds(g))
	}
	// Junos: the family's marker.
	before := "set system ntp server 192.0.2.123\n"
	g = GenerateCommands("sw-lab-01> show configuration | display set\n"+before+"sw-lab-01> \n", "sw-lab-01> show configuration | display set\n"+before+"set interfaces ge-0/0/0 unit 0 family inet address 192.0.2.1/24\n"+"sw-lab-01> \n", "rollback", "juniper-junos")
	for _, l := range g.Lines {
		if l.Note != "" && !strings.HasPrefix(strings.TrimSpace(l.Text), "# 手で確認（") {
			t.Errorf("junos note line: %q", l.Text)
		}
	}
}
