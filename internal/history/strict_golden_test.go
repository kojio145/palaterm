package history

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// The verdicts v1.7.12 (the release users confirmed on real logs) gave for
// a set of common work patterns, lab values only: each line's status by
// block, and the number of changes outside the work. They were checked
// against v1.7.12 itself; the only differences kept are fixes (the line
// after exit-address-family under router bgp, not under exit-address-family;
// no false changes between two partial listings). A later change that turns
// one of these into another result is a regression unless the scenario is
// updated on purpose (2026-10-21: three review rounds kept bringing back
// cases v1.7.12 had right).
func TestVerdictsAsInV1712(t *testing.T) {
	b, err := os.ReadFile("testdata/verdict_v1712.json")
	if err != nil {
		t.Fatal(err)
	}
	var sc map[string]struct {
		Work, Before, After string
		Expect              map[string]string
	}
	if err := json.Unmarshal(b, &sc); err != nil {
		t.Fatal(err)
	}
	for name, s := range sc {
		r := StrictVerify(s.Work, s.Before, s.After)
		got := map[string]string{}
		for _, c := range r.Commands {
			got[c.Parent+" > "+c.Line] = c.Status
		}
		got["#unexpected"] = fmt.Sprint(r.UnexpectedCount)
		for line, want := range s.Expect {
			if got[line] != want {
				t.Errorf("%s: %q is %q, pinned %q", name, line, got[line], want)
			}
		}
		if len(got) != len(s.Expect) {
			t.Errorf("%s: %d results, pinned %d: %v", name, len(got), len(s.Expect), got)
		}
	}
}
