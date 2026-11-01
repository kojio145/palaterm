package history

import (
	"strings"
	"testing"
)

// A listing's time-stamp comment (NEC IX "! Current time …") is noise for
// the comparison and never a command for 戻す / 揃える; a comment with text
// is never turned into "no ! …" either (seen on a lab router, 2026-11-01).
func TestTimeCommentIsNotACommand(t *testing.T) {
	a := strings.Join([]string{
		"lab-rt1# show running-config",
		"! Current time Nov 01-Sun-2026 14:40:28 JST",
		"hostname lab-rt1",
		"!",
		"interface GigaEthernet0.0",
		"  ip address 192.0.2.1/24",
		"!",
		"lab-rt1# ",
	}, "\n")
	b := strings.ReplaceAll(a, "14:40:28", "14:41:16")
	b = strings.Replace(b, "  ip address 192.0.2.1/24", "  ip address 192.0.2.1/24\n  description uplink", 1)
	r := DiffWith(a, b, DiffOpts{ConfigOnly: true, IgnoreNoise: true})
	for _, l := range r.Lines {
		if strings.Contains(l.A+l.B, "Current time") && l.Kind != "=" && !l.Noise {
			t.Errorf("time comment counted as a change: %+v", l)
		}
	}
	for _, dir := range []string{"rollback", "forward"} {
		g := GenerateCommands(a, b, dir, "")
		for _, c := range g.Lines {
			if strings.Contains(c.Text, "Current time") || strings.HasPrefix(strings.TrimSpace(c.Text), "no !") {
				t.Errorf("%s: comment line generated: %q", dir, c.Text)
			}
		}
	}
	if !isCommentText("! Current time x") || !isCommentText("# saved by ops") || isCommentText("!") || isCommentText("#") || isCommentText("hostname lab") {
		t.Error("isCommentText")
	}
}
