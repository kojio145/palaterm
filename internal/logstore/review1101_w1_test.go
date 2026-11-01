package logstore

import (
	"os"
	"path/filepath"
	"testing"
)

// UniquePath: a session's log in a folder shared with other sessions never
// takes an existing file's name (Stream appends; a template without
// {time} names a device's second session like its first).
func TestReview1101W1UniquePath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "lab-sw1_work.txt")
	if got := UniquePath(p); got != p {
		t.Fatalf("free name changed: %q", got)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p2 := filepath.Join(dir, "lab-sw1_work_2.txt")
	if got := UniquePath(p); got != p2 {
		t.Fatalf("second = %q, want %q", got, p2)
	}
	if err := os.WriteFile(p2, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := UniquePath(p); got != filepath.Join(dir, "lab-sw1_work_3.txt") {
		t.Fatalf("third = %q", got)
	}
	// No extension.
	q := filepath.Join(dir, "lab-rt1")
	if err := os.WriteFile(q, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := UniquePath(q); got != q+"_2" {
		t.Fatalf("no extension: %q", got)
	}
}
