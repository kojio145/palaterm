package vault

import (
	"path/filepath"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// Each save keeps the previous file as <vault>.bak, readable with the same
// password.
func TestSaveKeepsPreviousGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "palaterm_vault.enc")
	if err := Save(path, "pw", &model.Inventory{Devices: []model.Device{{Name: "first"}}}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, "pw", &model.Inventory{Devices: []model.Device{{Name: "second"}}}); err != nil {
		t.Fatal(err)
	}
	cur, err := Load(path, "pw")
	if err != nil || cur.Devices[0].Name != "second" {
		t.Fatalf("current = %+v, %v", cur, err)
	}
	bak, err := Load(path+".bak", "pw")
	if err != nil || bak.Devices[0].Name != "first" {
		t.Fatalf("backup = %+v, %v", bak, err)
	}
}
