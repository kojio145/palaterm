package vault

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// RemoveBackups (a master password change, a reset) leaves no other copy of
// the vault behind: the .bak, the damaged files RestoreBackup set aside and
// a failed save's temporary file all go; the vault itself and unrelated
// files stay.
func TestRemoveBackupsDropsBrokenCopies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "palaterm_vault.enc")
	for _, name := range []string{"sw1", "sw2"} {
		if err := Save(path, "pw", &model.Inventory{Devices: []model.Device{{Name: name}}}); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	b[len(b)-1] ^= 0xff
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := RestoreBackup(path, time.Date(2026, 10, 24, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, ".palaterm-123.tmp")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	RemoveBackups(path)
	for _, p := range []string{kept, tmp, path + ".bak"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still there (err %v)", filepath.Base(p), err)
		}
	}
	for _, p := range []string{path, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", filepath.Base(p), err)
		}
	}
}
