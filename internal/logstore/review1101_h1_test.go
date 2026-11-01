package logstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review of 2026-11-01, A-12: the log root must be a folder of its own —
// not a drive's root, not the exe's folder, not data\ or export\ under it.
func TestH1RootPlaceProblem(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	exeDir := filepath.Dir(exe)
	for in, refused := range map[string]bool{
		`C:\`:                              true,
		`D:\`:                              true,
		exeDir:                             true,
		filepath.Join(exeDir, "data"):      true,
		filepath.Join(exeDir, "data", "x"): true,
		filepath.Join(exeDir, "export", "devices"): true,
		filepath.Join(exeDir, "logs"):              false,
		filepath.Join(exeDir, "data2"):             false,
		filepath.Join(exeDir, "export-old"):        false,
		`C:\logs`:                                  false,
		`\\lab\logs`:                               false,
	} {
		if got := RootPlaceProblem(in) != ""; got != refused {
			t.Errorf("RootPlaceProblem(%q) refused=%v, want %v", in, got, refused)
		}
	}
	// Through CheckRoot, as the settings call it: a relative "." is the exe
	// folder, "data" its data folder; the message says it is the place.
	for _, in := range []string{".", "data", `export\x`, `C:\`} {
		err := CheckRoot(in)
		if err == nil || !strings.Contains(err.Error(), "は使えません") {
			t.Errorf("CheckRoot(%q) = %v", in, err)
		}
	}
	if err := CheckRoot(filepath.Join(t.TempDir(), "logs")); err != nil {
		t.Errorf("a folder of its own refused: %v", err)
	}
}
