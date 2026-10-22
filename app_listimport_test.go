package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

func listImportApp(t *testing.T) (*App, string, string) {
	t.Helper()
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "list.csv")
	if err := os.WriteFile(path, []byte("機器名称,IP\nsw1,192.0.2.1\nsw2,192.0.2.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := a.SaveImportRule(model.ImportRule{Name: "r", HeaderRow: 1, Fields: []model.ImportField{
		{Key: "name", Source: "column", Column: "機器名称"},
		{Key: "host", Source: "column", Column: "IP"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return a, path, id
}

// The choices of a preview fit its rows only: a file exported again (or a
// rule edited) since is refused, and the import goes through on the
// stamp of a fresh preview.
func TestApplyRefusesAChangedFile(t *testing.T) {
	a, path, id := listImportApp(t)
	req := ListImportReq{Path: path, RuleID: id, Group: "g", NewGroup: true}
	pv, err := a.PreviewListImport(req)
	if err != nil {
		t.Fatal(err)
	}
	// Exported again with other devices on the same rows.
	later := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(path, []byte("機器名称,IP\nsw9,192.0.2.9\nsw2,192.0.2.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(path, later, later)
	req.Stamp = pv.Stamp
	if _, err := a.ApplyListImport(req); err == nil || !strings.Contains(err.Error(), "プレビューのあとで変わりました") {
		t.Fatalf("a changed file was imported: %v", err)
	}
	if len(a.GetInventory().Devices) != 0 {
		t.Fatal("devices written")
	}
	pv, err = a.PreviewListImport(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Stamp = pv.Stamp
	res, err := a.ApplyListImport(req)
	if err != nil || res.Added != 2 {
		t.Fatalf("fresh preview: %+v %v", res, err)
	}
	// The rule edited since the preview: refused too.
	pv, _ = a.PreviewListImport(ListImportReq{Path: path, RuleID: id, Group: "g"})
	r := a.GetInventory().ImportRules[0]
	r.Fields = append(r.Fields, model.ImportField{Key: "site", Default: "本社"})
	if _, err := a.SaveImportRule(r); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyListImport(ListImportReq{Path: path, RuleID: id, Group: "g", Stamp: pv.Stamp}); err == nil {
		t.Error("an edited rule was imported on an old preview")
	}
}

// A lock forgets the file read; a read while locked keeps nothing.
func TestLockForgetsTheList(t *testing.T) {
	a, path, id := listImportApp(t)
	if _, err := a.PreviewListImport(ListImportReq{Path: path, RuleID: id, Group: "g", NewGroup: true}); err != nil {
		t.Fatal(err)
	}
	if bookCache.book == nil {
		t.Fatal("the file is not kept while the screen uses it")
	}
	a.Lock()
	if bookCache.book != nil || bookCache.conv != nil {
		t.Error("the file kept after a lock")
	}
	if _, _, _, _, err := a.readList(path, nil, nil, ""); err == nil || bookCache.book != nil {
		t.Errorf("read while locked: %v (kept %v)", err, bookCache.book != nil)
	}
}
