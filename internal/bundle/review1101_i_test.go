package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A bundle written without the devices' credentials says so in its manifest
// (the probe of 2026-11-01 found the flag lost between Select and Write).
func TestWriteKeepsDeviceCredsOmitted(t *testing.T) {
	inv := &model.Inventory{
		DeviceGroups: []model.DeviceGroup{{Name: "lab"}},
		Devices:      []model.Device{{ID: "d1", Name: "lab-sw1", Host: "192.0.2.10", Group: "lab", Username: "ops", Password: "lab-pw1"}},
	}
	c := SelectWith(inv, "lab", false, true)
	dir := t.TempDir()
	if _, err := Write(dir, c, "PalaTerm test"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "palaterm-bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !m.DeviceCredsOmitted {
		t.Fatalf("manifest lost deviceCredsOmitted: %s", raw)
	}
}
