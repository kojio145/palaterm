package logstore

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanRoot(t *testing.T) {
	for in, want := range map[string]string{
		`"D:\logs"`:      `D:\logs`,
		`  "D:\logs"  `:  `D:\logs`,
		`'\\lab\logs'`:   `\\lab\logs`,
		`logs`:           `logs`,
		`" "`:            ``,
		`D:\lab "x" log`: `D:\lab "x" log`,
	} {
		if got := CleanRoot(in); got != want {
			t.Errorf("CleanRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

// A root that can never exist (quotes or other characters Windows refuses)
// used to make RunDir loop forever: os.Stat answered "invalid name", not
// "does not exist". It now returns at once.
func TestRunDirReturnsOnUnusableRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), `a<b>|c`)
	done := make(chan string, 1)
	go func() {
		done <- RunDir(root, "", "lab", StageWork, DefaultStageTokens, time.Now())
	}()
	select {
	case d := <-done:
		if !strings.HasPrefix(d, root) {
			t.Errorf("RunDir = %q", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunDir did not return")
	}
}

func TestCheckRoot(t *testing.T) {
	ok := filepath.Join(t.TempDir(), "logs", "new")
	if err := CheckRoot(`"` + ok + `"`); err != nil {
		t.Errorf("quoted usable folder refused: %v", err)
	}
	if err := CheckRoot(filepath.Join(t.TempDir(), `a<b>|c`)); err == nil {
		t.Error("a folder Windows cannot create must be refused")
	}
}
