package main

import (
	"os"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/csvio"
)

// Two comparison exports within the same second are two files (code review
// 2026-10-20: the second used to overwrite the first).
func TestExportCompareResultDoesNotOverwrite(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	rows := [][]string{{"機器名", "差分"}, {"sw1", "+1"}}
	var paths []string
	for i := 0; i < 3; i++ {
		p, err := a.ExportCompareResult(rows)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(p) })
		paths = append(paths, p)
	}
	if paths[0] == paths[1] || paths[1] == paths[2] || paths[0] == paths[2] {
		t.Fatalf("same file: %q", paths)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil || !strings.HasPrefix(string(b), csvio.BOM) || !strings.Contains(string(b), "sw1") {
			t.Errorf("%s: %q %v", p, b, err)
		}
	}
	if _, err := a.ExportCompareResult(rows[:1]); err == nil {
		t.Errorf("header only should be refused")
	}
}
