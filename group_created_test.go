package main

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A group gets its creation time when it is made (new, copy, from a
// device's group field), keeps it across an edit and a rename, and a group
// from before the field stays 0 (the chooser sorts those as oldest).
func TestGroupCreated(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	created := func(name string) int64 {
		t.Helper()
		g := a.GetInventory().Group(name)
		if g == nil {
			t.Fatalf("group %q missing", name)
		}
		return g.Created
	}

	before := model.NowCreated()
	if err := a.CreateDeviceGroup(model.DeviceGroup{Name: "lab"}); err != nil {
		t.Fatal(err)
	}
	c := created("lab")
	if c < before {
		t.Fatalf("new group: Created = %d, want >= %d", c, before)
	}

	// The page sends the group without the field: the stored value stays.
	if err := a.SaveDeviceGroup(model.DeviceGroup{Name: "lab", Username: "ops"}); err != nil {
		t.Fatal(err)
	}
	if got := created("lab"); got != c {
		t.Errorf("edit changed Created: %d -> %d", c, got)
	}
	if err := a.RenameDeviceGroup("lab", "lab-a"); err != nil {
		t.Fatal(err)
	}
	if got := created("lab-a"); got != c {
		t.Errorf("rename changed Created: %d -> %d", c, got)
	}

	if _, err := a.CopyDeviceGroup("lab-a", "lab-b", ""); err != nil {
		t.Fatal(err)
	}
	if got := created("lab-b"); got < c {
		t.Errorf("copy kept the source's time: %d (source %d)", got, c)
	}

	// A device naming a group that does not exist yet makes a dated one.
	if err := a.SaveDevice(model.Device{Name: "sw1", Host: "192.0.2.1", Conn: model.ConnSSH,
		OSType: "cisco-ios", Group: "jump1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if created("jump1") < before {
		t.Error("group made from a device's group field has no time")
	}

	// A group saved before the field (0) stays 0 through an edit.
	a.mu.Lock()
	a.inv.DeviceGroups = append(a.inv.DeviceGroups, model.DeviceGroup{Name: "old"})
	a.mu.Unlock()
	if err := a.SaveDeviceGroup(model.DeviceGroup{Name: "old", Username: "ops"}); err != nil {
		t.Fatal(err)
	}
	if got := created("old"); got != 0 {
		t.Errorf("undated group got a time on edit: %d", got)
	}
}
