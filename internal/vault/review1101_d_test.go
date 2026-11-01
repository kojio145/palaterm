package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A copy that cannot be removed is reported (it still opens with the old
// password): here the .bak is a folder with a file in it, which os.Remove
// refuses everywhere. The other copies still go.
func TestRemoveBackupsReportsWhatStays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "palaterm_vault.enc")
	if err := os.MkdirAll(path+".bak", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path+".bak", "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := path + ".broken-20261101-120000"
	if err := os.WriteFile(broken, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := RemoveBackups(path)
	if err == nil {
		t.Fatal("RemoveBackups returned nil although the .bak stayed")
	}
	if _, serr := os.Stat(broken); !os.IsNotExist(serr) {
		t.Errorf("broken copy still there: %v", serr)
	}
}

// Nothing to remove is no error: a vault created in a folder that does not
// exist yet (CreateVault calls RemoveBackups first), and one with no copies.
func TestRemoveBackupsNothingThere(t *testing.T) {
	if err := RemoveBackups(filepath.Join(t.TempDir(), "nodir", "palaterm_vault.enc")); err != nil {
		t.Errorf("missing folder: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "palaterm_vault.enc")
	if err := Save(path, "pw", &model.Inventory{}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBackups(path); err != nil {
		t.Errorf("no copies: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the vault itself went: %v", err)
	}
}

// The .bak is replaced whole (temp + rename): after two saves it opens
// with the password and holds the first generation, and no temporary file
// is left beside it.
func TestBackupReplacedWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "palaterm_vault.enc")
	for _, name := range []string{"lab-sw1", "lab-sw2"} {
		if err := Save(path, "pw", &model.Inventory{Devices: []model.Device{{Name: name}}}); err != nil {
			t.Fatal(err)
		}
	}
	inv, _, err := LoadBackup(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Devices) != 1 || inv.Devices[0].Name != "lab-sw1" {
		t.Errorf("backup holds %+v, want the first generation", inv.Devices)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left: %s", e.Name())
		}
	}
}
