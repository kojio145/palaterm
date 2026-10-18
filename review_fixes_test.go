package main

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/schedule"
)

// 2026-10-18 review: the bindings a page reaches only after unlocking
// refuse while the vault is locked, and a vault is never replaced by
// CreateVault.
func TestLockedBindingsRefuse(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateVault("other"); err == nil {
		t.Fatal("CreateVault over an existing vault must refuse")
	}
	a.Lock()
	if _, err := a.ListRunHistory(); err == nil {
		t.Error("ListRunHistory while locked")
	}
	if err := a.OpenLogDir(); err == nil {
		t.Error("OpenLogDir while locked")
	}
	if err := a.RunScheduleNow("x"); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("RunScheduleNow while locked: %v", err)
	}
	if n := len(a.ListSchedules()); n != 0 {
		t.Errorf("ListSchedules while locked = %d", n)
	}
	if n := len(a.ListProfiles()); n != 0 {
		t.Errorf("ListProfiles while locked = %d", n)
	}
	if !a.locked() || a.Unlocked() {
		t.Error("locked() / Unlocked() disagree with the lock")
	}
}

// 一括変更 of credentials: the devices' own, and one jump-host hop's on the
// devices that have that hop.
func TestUpdateDevicesCredentials(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []model.Device{
		{Name: "r1", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "old", Password: "oldpw", Enabled: true,
			Bastions: []model.Bastion{{Host: "192.0.2.9", Method: model.BastionSSH, Username: "j", Password: "jpw"}}},
		{Name: "r2", Host: "192.0.2.2", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "old", Password: "oldpw", Enabled: true},
	} {
		if err := a.SaveDevice(d); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{a.inv.Devices[0].ID, a.inv.Devices[1].ID}
	pw, en, hop, bpw := "newpw", "newen", 1, "newjpw"
	if _, err := a.UpdateDevices(ids, DevicePatch{Password: &pw, EnablePassword: &en, BastionHop: &hop, BastionPassword: &bpw}); err != nil {
		t.Fatal(err)
	}
	r1, r2 := a.inv.Devices[0], a.inv.Devices[1]
	if r1.Password != "newpw" || r1.EnablePassword != "newen" || r2.Password != "newpw" || r1.Username != "old" {
		t.Errorf("device credentials: r1=%+v r2=%+v", r1, r2)
	}
	if r1.Bastions[0].Password != "newjpw" || r1.Bastions[0].Username != "j" {
		t.Errorf("hop 1 of r1 = %+v", r1.Bastions[0])
	}
	if len(r2.Bastions) != 0 {
		t.Errorf("r2 has no hop and must not get one: %+v", r2.Bastions)
	}
}

// A schedule on a deleted group, or naming a device moved out of its
// group, is stopped (it would otherwise run on whatever takes the name).
func TestGroupChangesStopSchedules(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.inv.DeviceGroups = []model.DeviceGroup{{Name: "lab"}, {Name: "lab2"}}
	a.inv.Devices = []model.Device{
		{ID: "d1", Name: "r1", Group: "lab", Host: "192.0.2.1", Conn: model.ConnSSH, Username: "ops", Enabled: true},
		{ID: "d2", Name: "r2", Group: "lab", Host: "192.0.2.2", Conn: model.ConnSSH, Username: "ops", Enabled: true},
	}
	a.inv.Schedules = []schedule.Schedule{
		{ID: "s1", Name: "all of lab", Enabled: true, Group: "lab", Slots: []schedule.Slot{{Kind: "daily", Time: "03:00:00"}}},
		{ID: "s2", Name: "r1 only", Enabled: true, Group: "lab", Names: []string{"r1"}, Slots: []schedule.Slot{{Kind: "daily", Time: "03:00:00"}}},
		{ID: "s3", Name: "r2 only", Enabled: true, Group: "lab", Names: []string{"r2"}, Slots: []schedule.Slot{{Kind: "daily", Time: "03:00:00"}}},
	}
	a.mu.Unlock()
	g := "lab2"
	if _, err := a.UpdateDevices([]string{"d1"}, DevicePatch{Group: &g}); err != nil {
		t.Fatal(err)
	}
	by := func(id string) schedule.Schedule {
		for _, sc := range a.inv.Schedules {
			if sc.ID == id {
				return sc
			}
		}
		t.Fatalf("schedule %s gone", id)
		return schedule.Schedule{}
	}
	if !by("s1").Enabled || by("s2").Enabled || !by("s3").Enabled {
		t.Errorf("after moving r1: s1=%v s2=%v s3=%v (want on, off, on)", by("s1").Enabled, by("s2").Enabled, by("s3").Enabled)
	}
	if _, err := a.DeleteDeviceGroup("lab"); err != nil {
		t.Fatal(err)
	}
	if by("s1").Enabled || by("s3").Enabled {
		t.Errorf("after deleting lab: s1=%v s3=%v (want both off)", by("s1").Enabled, by("s3").Enabled)
	}
	found := 0
	for _, e := range a.readSchedLog(0) {
		if e.Kind == "stopped" {
			found++
		}
	}
	if found != 3 {
		t.Errorf("stopped records = %d, want 3", found)
	}
}

// The locked copy for the schedules carries no table drafts (plain
// passwords of devices no schedule targets).
func TestSchedCopyDropsDrafts(t *testing.T) {
	inv := schedInventory()
	inv.Schedules = []schedule.Schedule{{ID: "1", Enabled: true, Group: "A社"}}
	inv.GridDrafts = map[string]model.GridDraft{"B社": {}}
	inv.KnownGUICerts = map[string]string{"gui x": "ca"}
	c := schedCopy(inv)
	if c.GridDrafts != nil || c.KnownGUICerts != nil {
		t.Errorf("drafts / pins kept while locked: %v %v", c.GridDrafts, c.KnownGUICerts)
	}
}
