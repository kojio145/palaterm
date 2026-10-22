package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// A damaged vault comes back from its backup with the same password; the
// damaged file is kept beside it under a .broken- name.
func TestRestoreBackupKeepsDamagedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "palaterm_vault.enc")
	if err := Save(path, "pw", &model.Inventory{Devices: []model.Device{{Name: "sw1", Host: "192.0.2.1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, "pw", &model.Inventory{Devices: []model.Device{{Name: "sw2", Host: "192.0.2.2"}}}); err != nil {
		t.Fatal(err)
	}
	// Damage the vault: the tag no longer matches.
	b, _ := os.ReadFile(path)
	b[len(b)-1] ^= 0xff
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, "pw"); err != ErrWrongPassword {
		t.Fatalf("damaged load = %v", err)
	}
	if _, _, err := LoadBackup(path, "wrong"); err == nil {
		t.Fatal("the backup must not open with another password")
	}
	inv, at, err := LoadBackup(path, "pw")
	if err != nil || inv.Devices[0].Name != "sw1" {
		t.Fatalf("backup = %+v, %v", inv, err)
	}
	if at.IsZero() || time.Since(at) > time.Hour {
		t.Errorf("backup time = %v", at)
	}
	kept, err := RestoreBackup(path, time.Date(2026, 10, 22, 9, 30, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(kept, ".broken-20261022-093000") {
		t.Errorf("kept = %q", kept)
	}
	if got, _ := os.ReadFile(kept); string(got) != string(b) {
		t.Error("the damaged file must be kept as it was")
	}
	cur, err := Load(path, "pw")
	if err != nil || cur.Devices[0].Name != "sw1" {
		t.Fatalf("restored = %+v, %v", cur, err)
	}
}

// Save stamps the format and the writing version.
func TestSaveStampsVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.enc")
	old := Writer
	Writer = "9.9.9"
	defer func() { Writer = old }()
	if err := Save(path, "pw", &model.Inventory{Version: 1}); err != nil {
		t.Fatal(err)
	}
	inv, err := Load(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Version != model.InventoryVersion || inv.SavedBy != "9.9.9" {
		t.Errorf("version = %d, savedBy = %q", inv.Version, inv.SavedBy)
	}
}
