package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A file dialog must open even when the remembered folder is gone: the
// nearest existing ancestor is used, and a path with no existing ancestor
// (or none at all) falls back to "" so the dialog chooses its own place.
func TestDialogDir(t *testing.T) {
	root := t.TempDir()
	keys := filepath.Join(root, "keys")
	if err := os.MkdirAll(filepath.Join(keys, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := dialogDir(filepath.Join(keys, "old")); got != filepath.Join(keys, "old") {
		t.Errorf("existing folder: got %q", got)
	}
	if got := dialogDir(filepath.Join(keys, "gone", "deeper")); got != keys {
		t.Errorf("missing folder: got %q, want %q", got, keys)
	}
	if got := dialogDir(filepath.Join(keys, "old", "id_lab")); got != filepath.Join(keys, "old") {
		t.Errorf("file path: got %q", got)
	}
	if got := dialogDir(""); got != "" {
		t.Errorf("empty: got %q", got)
	}
	if got := dialogDir(`Q:\no-such-drive\keys`); got != "" {
		t.Errorf("no existing ancestor: got %q", got)
	}
}
