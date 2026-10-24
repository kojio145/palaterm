package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/listimport"
	"github.com/kojio145/palaterm/internal/model"
)

// A rule file is refused by its size before it is read whole; a small one
// is read as it is.
func TestReadRuleFileSize(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, bytes.Repeat([]byte{'{'}, maxRuleFile+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRuleFile(big); err == nil || !strings.Contains(err.Error(), "変換ルールのファイルではありません") {
		t.Errorf("big file: %v", err)
	}
	small := filepath.Join(dir, "small.json")
	if err := os.WriteFile(small, []byte(`{"format":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := readRuleFile(small); err != nil || string(data) != `{"format":"x"}` {
		t.Errorf("small file: %q %v", data, err)
	}
}

// The editor's picture reaches the header row wherever it is set (and a
// few rows under it), up to the cap; a header near the top shows 12 rows.
func TestSampleGridReachesHeaderRow(t *testing.T) {
	rows := make([][]string, 300)
	for i := range rows {
		rows[i] = []string{"x"}
	}
	for _, c := range []struct{ header, want int }{{1, 12}, {6, 12}, {46, 52}, {194, 200}, {250, maxSampleRows}} {
		if g, _ := sampleGrid(rows, c.header); len(g) != c.want {
			t.Errorf("header row %d: %d rows, want %d", c.header, len(g), c.want)
		}
	}
	if g, _ := sampleGrid(rows[:30], 46); len(g) != 30 {
		t.Errorf("a short sheet: %d rows, want 30", len(g))
	}
}

// A rule renamed between the preview and the import reads the same rows:
// the import goes through on the preview's stamp.
func TestRuleRenameKeepsThePreview(t *testing.T) {
	a, path, id := listImportApp(t)
	defer forgetBook()
	req := ListImportReq{Path: path, RuleID: id, Group: "g", NewGroup: true}
	pv, err := a.PreviewListImport(req)
	if err != nil {
		t.Fatal(err)
	}
	r := a.GetInventory().ImportRules[0]
	r.Name = "renamed"
	if _, err := a.SaveImportRule(r); err != nil {
		t.Fatal(err)
	}
	req.Stamp = pv.Stamp
	res, err := a.ApplyListImport(req)
	if err != nil || res.Added != 2 {
		t.Fatalf("after a rename: %+v %v", res, err)
	}
	a1, b1 := model.ImportRule{ID: "1", Name: "a", HeaderRow: 1}, model.ImportRule{ID: "2", Name: "b", HeaderRow: 1}
	if ruleSig(&a1) != ruleSig(&b1) {
		t.Error("the signature names the rule")
	}
	b1.HeaderRow = 2
	if ruleSig(&a1) == ruleSig(&b1) {
		t.Error("the signature misses the content")
	}
}

// An import into a new group whose every row is left out (or fails) makes
// no group; one that adds a device does.
func TestNewGroupOnlyWithADevice(t *testing.T) {
	a, path, id := listImportApp(t)
	defer forgetBook()
	req := ListImportReq{Path: path, RuleID: id, Group: "g", NewGroup: true,
		Decisions: map[int]listimport.Decision{2: {Link: "skip"}, 3: {Link: "skip"}}}
	pv, err := a.PreviewListImport(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Stamp = pv.Stamp
	res, err := a.ApplyListImport(req)
	if err != nil || res.Added != 0 || res.Skipped != 2 {
		t.Fatalf("all skipped: %+v %v", res, err)
	}
	if a.GetInventory().Group("g") != nil {
		t.Fatal("an empty group was made")
	}
	req.Decisions = nil
	if pv, err = a.PreviewListImport(req); err != nil {
		t.Fatal(err)
	}
	req.Stamp = pv.Stamp
	if res, err := a.ApplyListImport(req); err != nil || res.Added != 2 {
		t.Fatalf("imported: %+v %v", res, err)
	}
	if a.GetInventory().Group("g") == nil {
		t.Fatal("the group was not made")
	}
}
