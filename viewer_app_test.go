package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/history"
)

// The viewer's follow timer reads only what the file grew by, and shows the
// same text as a read of the whole file (the line editing is cleaned over
// the whole, so a backspace at the start of what was added still erases
// what came before it). A file that shrank or was rewritten is read from
// the start; a missing file is an error, and the next read starts over.
func TestViewerLoadFollowsAppends(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r1.txt")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	add := func(s string) {
		t.Helper()
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(s)
		f.Close()
	}
	whole := func() string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return history.CleanTranscript(string(b))
	}
	v := NewViewer(p, "")
	write("r1#show clo")
	if d := v.Load(); d.Err != "" || d.Text != whole() || d.Name != "r1.txt" {
		t.Fatalf("first load: %+v", d)
	}
	add("\b \bck\r\nr1#")
	d := v.Load()
	if d.Err != "" || d.Text != whole() {
		t.Fatalf("after an append: %q, want %q", d.Text, whole())
	}
	if len(v.raw) != len("r1#show clo\b \bck\r\nr1#") {
		t.Fatalf("bytes kept: %d", len(v.raw))
	}
	if again := v.Load(); again.Text != d.Text {
		t.Fatalf("unchanged file read differently: %q", again.Text)
	}
	write("new")
	if d = v.Load(); d.Text != "new" {
		t.Fatalf("a replaced (shorter) file: %q", d.Text)
	}
	write("old")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if d = v.Load(); d.Text != "old" {
		t.Fatalf("rewritten at the same size: %q", d.Text)
	}
	// Another, larger file in its place: not the old text plus a tail.
	write("r2#show clock")
	if d = v.Load(); d.Text != "r2#show clock" {
		t.Fatalf("replaced by a larger file: %q", d.Text)
	}
	os.Remove(p)
	if d = v.Load(); d.Err == "" {
		t.Fatal("a missing file gave no error")
	}
	write("r1#show version")
	if d = v.Load(); d.Err != "" || d.Text != "r1#show version" {
		t.Fatalf("after the file came back: %+v", d)
	}
}
