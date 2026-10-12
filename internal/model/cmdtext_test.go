package model

import (
	"testing"

	"github.com/kojio145/palaterm/internal/profile"
)

// A carriage return hidden inside a command (it shows as a space) would run
// a second command the change warning never sees.
func TestCheckCommandText(t *testing.T) {
	for s, bad := range map[string]bool{
		"show clock":         false,
		"show run | i ^int":  false,
		"show clock\rreload": true,
		"show clock\nreload": true,
		"tab\tcomplete":      true,
		"esc\x1b[2J":         true,
		"日本語のコメント":           false,
	} {
		if err := CheckCommandText(s); (err != nil) != bad {
			t.Errorf("%q: err=%v, want bad=%v", s, err, bad)
		}
	}
	if err := CheckCommands([]Command{{Text: "show clock"}, {Text: "a\rb"}}); err == nil {
		t.Error("CheckCommands missed the second command")
	}
	p := profile.Profile{Name: "x", Pager: []profile.Step{{Send: "terminal length 0\rreload"}}}
	if err := CheckProfileSends(p); err == nil {
		t.Error("CheckProfileSends missed a pager step")
	}
}
