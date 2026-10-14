package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// The screen window reads a growing log piece by piece: each read returns
// what was added since the last one, a long file comes in bounded chunks,
// and a file that shrank (replaced) starts over.
func TestWatcherReadsGrowingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r1.txt")
	os.WriteFile(p, []byte("login: \x1b[1mR1#\x1b[0m"), 0o644)
	w := NewWatcher(p, "")
	dec := func(c watchChunk) string {
		b, _ := base64.StdEncoding.DecodeString(c.Data)
		return string(b)
	}
	c := w.Read(0)
	if c.Err != "" || dec(c) != "login: \x1b[1mR1#\x1b[0m" || c.Off != int64(len("login: \x1b[1mR1#\x1b[0m")) || c.ModUnixMs == 0 {
		t.Fatalf("first read: %+v", c)
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("show clock\r\n")
	f.Close()
	c2 := w.Read(c.Off)
	if dec(c2) != "show clock\r\n" || c2.Off != c.Off+12 {
		t.Fatalf("second read: %q %d", dec(c2), c2.Off)
	}
	if c3 := w.Read(c2.Off); dec(c3) != "" || c3.Off != c2.Off {
		t.Fatalf("nothing new: %q %d", dec(c3), c3.Off)
	}

	big := make([]byte, watchChunkMax+100)
	for i := range big {
		big[i] = 'x'
	}
	os.WriteFile(p, big, 0o644)
	a := w.Read(0)
	b := w.Read(a.Off)
	if len(dec(a)) != watchChunkMax || len(dec(b)) != 100 || b.Off != int64(len(big)) {
		t.Fatalf("chunks: %d %d %d", len(dec(a)), len(dec(b)), b.Off)
	}

	os.WriteFile(p, []byte("new"), 0o644)
	if r := w.Read(b.Off); dec(r) != "new" || r.Off != 3 {
		t.Fatalf("replaced file: %q %d", dec(r), r.Off)
	}
	if r := NewWatcher(filepath.Join(t.TempDir(), "none"), "").Read(0); r.Err == "" {
		t.Fatal("missing file gave no error")
	}
}
