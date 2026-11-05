package history

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A log root with two run folders and a few files, as the grep sees them.
func grepRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	w := func(dir, name, text string) string {
		d := filepath.Join(root, dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(d, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	w("20260101_090000_lab_before", "lab-sw1_before.txt", "lab-sw1#show int status\r\nGi1/0/1  connected  10\r\nGi1/0/2  notconnect 20\r\nlab-sw1#")
	w("20260101_090000_lab_before", "lab-sw2_before.txt", "lab-sw2#show int status\r\nGi1/0/1  NotConnect 10\r\nlab-sw2#")
	w("20260101_090000_lab_before", "palaterm-run.json", "{\"notconnect\": true}")
	w("20260102_100000_lab_after", "lab-sw1_after.txt", "lab-sw1#show int status\r\nGi1/0/1  connected  10\r\nGi1/0/2  connected  20\r\nlab-sw1#")
	w("20260102_100000_lab_after", "note.png", "notconnect")
	w(".hidden", "x.txt", "notconnect")
	return root
}

func TestGrepLiteralAcrossFolders(t *testing.T) {
	root := grepRoot(t)
	res, err := Grep(context.Background(), GrepRequest{Root: root, Query: "notconnect"})
	if err != nil {
		t.Fatal(err)
	}
	// Case-insensitive by default; the json, the png and the hidden folder
	// are not searched; the newest folder comes first.
	if res.Searched != 3 || res.Hits != 2 || len(res.Files) != 2 || res.Truncated {
		t.Fatalf("searched %d hits %d files %d truncated %v", res.Searched, res.Hits, len(res.Files), res.Truncated)
	}
	if !strings.HasSuffix(res.Files[0].Rel, "lab-sw1_before.txt") || !strings.HasSuffix(res.Files[1].Rel, "lab-sw2_before.txt") {
		t.Fatalf("order: %s, %s", res.Files[0].Rel, res.Files[1].Rel)
	}
	// Folder order: newest first.
	if len(res.NoHit) != 1 || !strings.Contains(res.NoHit[0], "20260102_100000_lab_after") {
		t.Fatalf("no-hit list: %v", res.NoHit)
	}
	h := res.Files[0].Hits[0]
	if h.Line != 3 || h.Text != "Gi1/0/2  notconnect 20" || len(h.Spans) != 1 || h.Spans[0] != [2]int{9, 19} {
		t.Fatalf("hit: %+v", h)
	}
	if res.Files[1].Hits[0].Text != "Gi1/0/1  NotConnect 10" {
		t.Fatalf("case-insensitive: %+v", res.Files[1].Hits[0])
	}

	// Case-sensitive: the lower-case one only.
	res, err = Grep(context.Background(), GrepRequest{Root: root, Query: "notconnect", CaseSensitive: true})
	if err != nil || res.Hits != 1 || len(res.NoHit) != 2 {
		t.Fatalf("case-sensitive: hits %d nohit %d err %v", res.Hits, len(res.NoHit), err)
	}
}

func TestGrepOneFolderRegexAndCap(t *testing.T) {
	root := grepRoot(t)
	dir := filepath.Join(root, "20260101_090000_lab_before")
	res, err := Grep(context.Background(), GrepRequest{Root: root, Dir: dir, Query: `Gi1/0/\d`, Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Searched != 2 || res.Hits != 3 {
		t.Fatalf("searched %d hits %d", res.Searched, res.Hits)
	}
	// Rel is under the root even when one folder is searched.
	if res.Files[0].Rel != filepath.Join("20260101_090000_lab_before", "lab-sw1_before.txt") {
		t.Fatalf("rel: %s", res.Files[0].Rel)
	}
	// The cap: the count goes on, the lines stop.
	res, err = Grep(context.Background(), GrepRequest{Root: root, Dir: dir, Query: `Gi1/0/\d`, Regex: true, MaxHits: 2})
	shown := 0
	for _, f := range res.Files {
		shown += len(f.Hits)
	}
	if err != nil || !res.Truncated || res.Hits != 3 || shown != 2 {
		t.Fatalf("cap: truncated %v hits %d shown %d err %v", res.Truncated, res.Hits, shown, err)
	}
	// The second file is still listed with its count, its lines held back.
	if len(res.Files) != 2 || res.Files[1].Count != 1 || len(res.Files[1].Hits) != 0 {
		t.Fatalf("capped files: %+v", res.Files)
	}
	// A bad regex says so.
	if _, err := Grep(context.Background(), GrepRequest{Root: root, Query: `(`, Regex: true}); err == nil || !strings.Contains(err.Error(), "正規表現") {
		t.Fatalf("bad regex: %v", err)
	}
	// Not a regex: the same text is literal.
	res, err = Grep(context.Background(), GrepRequest{Root: root, Query: `Gi1/0/\d`})
	if err != nil || res.Hits != 0 {
		t.Fatalf("literal backslash: hits %d err %v", res.Hits, err)
	}
	if _, err := Grep(context.Background(), GrepRequest{Root: root, Query: ""}); err != ErrGrepQuery {
		t.Fatalf("empty query: %v", err)
	}
}

func TestGrepDaysAndSpans(t *testing.T) {
	root := grepRoot(t)
	old := filepath.Join(root, "20260101_090000_lab_before", "lab-sw2_before.txt")
	past := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	res, err := Grep(context.Background(), GrepRequest{Root: root, Query: "notconnect", Days: 1})
	if err != nil || res.Searched != 2 || res.Hits != 1 {
		t.Fatalf("days: searched %d hits %d err %v", res.Searched, res.Hits, err)
	}
	// Several matches on a line, character offsets past multi-byte text.
	p := filepath.Join(root, "20260102_100000_lab_after", "jp.log")
	if err := os.WriteFile(p, []byte("拠点A: up up\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = Grep(context.Background(), GrepRequest{Root: root, Query: "up"})
	if err != nil || res.Hits != 1 {
		t.Fatalf("spans: hits %d err %v", res.Hits, err)
	}
	if sp := res.Files[0].Hits[0].Spans; len(sp) != 2 || sp[0] != [2]int{5, 7} || sp[1] != [2]int{8, 10} {
		t.Fatalf("spans: %v", sp)
	}
}
