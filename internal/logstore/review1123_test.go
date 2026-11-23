package logstore

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A group / site / role made only of dots must not climb out of the log
// folder through a nested file name template (review 2026-11-23).
func TestPathDotsStayInside(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.Local)
	for _, v := range []string{"..", ".", "...", " .. "} {
		p := Path(dir, `{group}\{site}\{host}_{date}.log`, Fields{Host: "jump1", Group: v, Site: v}, now)
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			t.Fatalf("value %q: %s is outside %s", v, p, dir)
		}
		if !strings.HasSuffix(p, "jump1_20261123.log") {
			t.Fatalf("value %q: unexpected name %s", v, p)
		}
	}
	// Ordinary names are unchanged.
	if got := sanitize("sw-a.lab"); got != "sw-a.lab" {
		t.Fatalf("sanitize changed a normal name: %q", got)
	}
	if got := sanitize("a..b"); got != "a..b" {
		t.Fatalf("sanitize changed a name with inner dots: %q", got)
	}
}
