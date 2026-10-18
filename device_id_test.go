package main

import (
	"os"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
	"github.com/kojio145/palaterm/internal/vault"
)

// devByGroup returns the device named name in group (fails the test if absent).
func devByGroup(t *testing.T, a *App, group, name string) model.Device {
	t.Helper()
	for _, d := range a.GetInventory().Devices {
		if d.Group == group && d.Name == name {
			return d
		}
	}
	t.Fatalf("no device %q in group %q", name, group)
	return model.Device{}
}

func newDev(name, group, host string) model.Device {
	return model.Device{Name: name, Host: host, Conn: model.ConnSSH, OSType: "cisco-ios", Group: group, Enabled: true}
}

// The same name may live in two groups; every edit, delete, copy and
// toggle reaches only the device it was aimed at.
func TestSameNameInTwoGroups(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDevice(newDev("sw1", "A社", "192.0.2.1")); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDevice(newDev("sw1", "B社", "192.0.2.2")); err != nil {
		t.Fatalf("same name in another group refused: %v", err)
	}
	if err := a.SaveDevice(newDev("sw1", "A社", "192.0.2.3")); err == nil {
		t.Fatal("same name twice in one group was accepted")
	}
	da, db := devByGroup(t, a, "A社", "sw1"), devByGroup(t, a, "B社", "sw1")
	if da.ID == "" || db.ID == "" || da.ID == db.ID {
		t.Fatalf("IDs not distinct: %q %q", da.ID, db.ID)
	}

	// Edit B's device: A's stays.
	db.Host = "192.0.2.22"
	if err := a.SaveDevice(db); err != nil {
		t.Fatal(err)
	}
	if got := devByGroup(t, a, "A社", "sw1").Host; got != "192.0.2.1" {
		t.Errorf("A社 sw1 host = %s after editing B社 sw1", got)
	}
	if got := devByGroup(t, a, "B社", "sw1").Host; got != "192.0.2.22" {
		t.Errorf("B社 sw1 host = %s", got)
	}

	// Moving B's device into A (where the name is taken) is refused.
	moved := db
	moved.Group = "A社"
	if err := a.SaveDevice(moved); err == nil {
		t.Error("moving into a group holding the name was accepted")
	}

	// Toggle, copy, rename by ID touch one device only.
	if err := a.SetAllDevicesEnabled(false, []string{db.ID}); err != nil {
		t.Fatal(err)
	}
	if !devByGroup(t, a, "A社", "sw1").Enabled || devByGroup(t, a, "B社", "sw1").Enabled {
		t.Error("enable toggle crossed groups")
	}
	if n, err := a.CopyDevice(da.ID); err != nil || n != "sw1_copy" {
		t.Errorf("copy = %q, %v", n, err)
	}
	devByGroup(t, a, "A社", "sw1_copy")
	if err := a.RenameDevice(db.ID, "sw1_copy"); err != nil {
		t.Errorf("rename to a name used only in another group refused: %v", err)
	}
	if err := a.RenameDevice(da.ID, "sw1_copy"); err == nil {
		t.Error("rename onto a name taken in the same group was accepted")
	}

	// Delete A's: B's survives.
	if err := a.DeleteDevice(da.ID); err != nil {
		t.Fatal(err)
	}
	devByGroup(t, a, "B社", "sw1_copy")
	for _, d := range a.GetInventory().Devices {
		if d.ID == da.ID {
			t.Error("deleted device still there")
		}
	}
}

// A run is one group: a name shared with another group never pulls that
// group's device into the batch or into the change warning.
func TestRunAndDangerCheckStayInTheGroup(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "change", Commands: []model.Command{{Text: "reload"}}}); err != nil {
		t.Fatal(err)
	}
	da, db := newDev("sw1", "A社", "192.0.2.1"), newDev("sw1", "B社", "192.0.2.2")
	db.CommandSet = "change"
	for _, d := range []model.Device{da, db} {
		if err := a.SaveDevice(d); err != nil {
			t.Fatal(err)
		}
	}
	if w := a.CheckDangerousCommands(RunRequest{Names: []string{"sw1"}, Group: "A社"}); len(w) != 0 {
		t.Errorf("A社 run warned about B社's set: %+v", w)
	}
	if w := a.CheckDangerousCommands(RunRequest{Names: []string{"sw1"}, Group: "B社"}); len(w) != 1 {
		t.Errorf("B社 run warnings = %+v", w)
	}
}

// Copying a group keeps the device names when no suffix is given.
func TestCopyDeviceGroupWithoutSuffix(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDevice(newDev("sw1", "A社", "192.0.2.1")); err != nil {
		t.Fatal(err)
	}
	if n, err := a.CopyDeviceGroup("A社", "B社", ""); err != nil || n != 1 {
		t.Fatalf("copy = %d, %v", n, err)
	}
	if devByGroup(t, a, "A社", "sw1").ID == devByGroup(t, a, "B社", "sw1").ID {
		t.Error("copy shares the original's ID")
	}
	if err := a.RenameDeviceGroup("B社", "A社"); err == nil {
		t.Error("renaming a group onto an existing one was accepted")
	}
}

// A vault from before IDs gets them on unlock, saved at once so the terminal
// window (which loads the vault itself) finds the same ones.
func TestUnlockAssignsDeviceIDs(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.inv.Devices = []model.Device{newDev("sw1", "A社", "192.0.2.1"), newDev("sw2", "A社", "192.0.2.2")}
	if err := vault.Save(a.vaultPath, "master", a.inv); err != nil { // no persist(): it would add IDs
		t.Fatal(err)
	}
	a2 := NewApp()
	a2.vaultPath = a.vaultPath
	if err := a2.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	onDisk, err := vault.Load(a.vaultPath, "master")
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range a2.GetInventory().Devices {
		if d.ID == "" || onDisk.Devices[i].ID != d.ID {
			t.Errorf("%s: ID %q in memory, %q on disk", d.Name, d.ID, onDisk.Devices[i].ID)
		}
	}
	// A terminal launched by ID finds the device; an old name argument too.
	if onDisk.DeviceIndex(onDisk.Devices[1].ID) != 1 || onDisk.DeviceIndex("sw2") != 1 {
		t.Error("DeviceIndex by ID / by name failed")
	}
}

// The edit dialog saves the whole device by ID, so its name can change
// there too (a copy's "_copy" name is fixed this way); a name already used
// in the group is still refused.
func TestSaveDeviceRenamesByID(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDevice(newDev("sw1", "A社", "192.0.2.1")); err != nil {
		t.Fatal(err)
	}
	src := devByGroup(t, a, "A社", "sw1")
	if _, err := a.CopyDevice(src.ID); err != nil {
		t.Fatal(err)
	}
	cp := devByGroup(t, a, "A社", "sw1_copy")
	cp.Name = "sw2"
	if err := a.SaveDevice(cp); err != nil {
		t.Fatalf("rename via save refused: %v", err)
	}
	if got := devByGroup(t, a, "A社", "sw2"); got.ID != cp.ID {
		t.Errorf("renamed device has ID %q, want %q", got.ID, cp.ID)
	}
	if n := len(a.GetInventory().Devices); n != 2 {
		t.Errorf("devices = %d, want 2 (rename must not add one)", n)
	}
	cp.Name = "sw1"
	if err := a.SaveDevice(cp); err == nil {
		t.Error("renaming onto a name used in the same group was accepted")
	}
}

// Deleting groups leaves their devices ungrouped; a name clash there is
// resolved with a numbered suffix, so every device can still be saved.
func TestDeleteGroupsRenamesClashingDevices(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"A社", "B社", ""} {
		if err := a.SaveDevice(newDev("sw1", g, "192.0.2.1")); err != nil {
			t.Fatal(err)
		}
	}
	if r, err := a.DeleteDeviceGroup("A社"); err != nil || len(r) != 1 || r[0] != "sw1 → sw1_2" {
		t.Fatalf("delete A社 = %v, %v", r, err)
	}
	if r, err := a.DeleteDeviceGroup("B社"); err != nil || len(r) != 1 || r[0] != "sw1 → sw1_3" {
		t.Fatalf("delete B社 = %v, %v", r, err)
	}
	for _, n := range []string{"sw1", "sw1_2", "sw1_3"} {
		d := devByGroup(t, a, "", n)
		d.Host = "192.0.2.99"
		if err := a.SaveDevice(d); err != nil {
			t.Errorf("save %s after the delete: %v", n, err)
		}
	}
}

// Bulk edit sets the given fields on the selected devices only; a move that
// would put two devices of the same name in one group is refused whole.
func TestUpdateAndDeleteDevicesInBulk(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []model.Device{newDev("sw1", "A社", "192.0.2.1"), newDev("sw2", "A社", "192.0.2.2"), newDev("sw1", "B社", "192.0.2.3")} {
		if err := a.SaveDevice(d); err != nil {
			t.Fatal(err)
		}
	}
	a1, a2, b1 := devByGroup(t, a, "A社", "sw1"), devByGroup(t, a, "A社", "sw2"), devByGroup(t, a, "B社", "sw1")
	set, site := "backup", "本社"
	if _, err := a.UpdateDevices([]string{a1.ID, a2.ID}, DevicePatch{CommandSet: &set, Site: &site}); err != nil {
		t.Fatal(err)
	}
	if d := devByGroup(t, a, "A社", "sw2"); d.CommandSet != "backup" || d.Site != "本社" || d.Host != "192.0.2.2" {
		t.Errorf("sw2 after bulk edit: %+v", d)
	}
	if d := devByGroup(t, a, "B社", "sw1"); d.CommandSet != "" {
		t.Errorf("unselected device changed: %+v", d)
	}
	g := "B社"
	if _, err := a.UpdateDevices([]string{a1.ID, a2.ID}, DevicePatch{Group: &g}); err == nil {
		t.Error("moving A社/sw1 next to B社/sw1 was accepted")
	}
	if d := devByGroup(t, a, "A社", "sw2"); d.Group != "A社" {
		t.Error("a refused move changed something")
	}
	if n, err := a.DeleteDevices([]string{a1.ID, b1.ID}); err != nil || n != 2 {
		t.Fatalf("delete = %d, %v", n, err)
	}
	if len(a.GetInventory().Devices) != 1 {
		t.Errorf("devices left = %d", len(a.GetInventory().Devices))
	}
}

// A run can take a command set (or typed commands) for this run only; the
// devices' own assignments are left as they are.
func TestRunSnapshotOverrides(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "clock", Commands: []model.Command{{Text: "show clock"}}}); err != nil {
		t.Fatal(err)
	}
	d := newDev("sw1", "A社", "192.0.2.1")
	d.CommandSet = "own"
	if err := a.SaveDevice(d); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDevice(newDev("u1", "", "192.0.2.5")); err != nil {
		t.Fatal(err)
	}
	snap, err := a.runSnapshot(RunRequest{Names: []string{"sw1"}, Group: "A社", CommandSet: "clock"})
	if err != nil || len(snap.Devices) != 1 || snap.Devices[0].CommandSet != "clock" {
		t.Fatalf("override: %+v, %v", snap, err)
	}
	snap, err = a.runSnapshot(RunRequest{Names: []string{"sw1"}, Group: "A社", AdHoc: []model.Command{{Text: " reload "}, {Text: ""}}})
	if err != nil || snap.Devices[0].CommandSet != AdHocSetName {
		t.Fatalf("ad hoc: %v", err)
	}
	if w := a.CheckDangerousCommands(RunRequest{Names: []string{"sw1"}, Group: "A社", AdHoc: []model.Command{{Text: "reload"}}}); len(w) != 1 {
		t.Errorf("typed reload not warned: %+v", w)
	}
	if _, err := a.runSnapshot(RunRequest{Group: "A社", CommandSet: "nosuch"}); err == nil {
		t.Error("unknown set accepted")
	}
	snap, err = a.runSnapshot(RunRequest{Names: []string{"u1"}, Ungrouped: true})
	if err != nil || len(snap.Devices) != 1 || snap.Devices[0].Name != "u1" {
		t.Fatalf("ungrouped: %+v, %v", snap, err)
	}
	if devByGroup(t, a, "A社", "sw1").CommandSet != "own" {
		t.Error("the device's own set was changed")
	}
}

// The vault backup holds every credential under the password it was saved
// with: 初期化 removes it, and a password change does not leave one behind
// under the old password.
func TestBackupGoesWithResetAndPasswordChange(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"sw1", "sw2"} {
		if err := a.SaveDevice(newDev(n, "A社", "192.0.2.1")); err != nil {
			t.Fatal(err)
		}
	}
	bak := a.vaultPath + ".bak"
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("no backup after two saves: %v", err)
	}
	if err := a.ChangeMasterPassword("master", "next"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Errorf("backup under the old password left after the change: %v", err)
	}
	if err := a.SaveDevice(newDev("sw3", "A社", "192.0.2.1")); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Load(bak, "next"); err != nil {
		t.Errorf("new backup not under the new password: %v", err)
	}
	if err := a.ResetVault("next"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Errorf("backup left after 初期化: %v", err)
	}
}

// Moved out of every group, a device on its group's defaults goes back to
// its own credentials; an auto-answer may not carry a credential variable.
func TestUngroupedMoveAndAnswerVariables(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	d := newDev("sw1", "A社", "192.0.2.1")
	d.UseGroupCreds = true
	if err := a.SaveDevice(d); err != nil {
		t.Fatal(err)
	}
	id := devByGroup(t, a, "A社", "sw1").ID
	none := ""
	if _, err := a.UpdateDevices([]string{id}, DevicePatch{Group: &none}); err != nil {
		t.Fatal(err)
	}
	if got := devByGroup(t, a, "", "sw1"); got.UseGroupCreds {
		t.Error("ungrouped device still on group defaults")
	}
	p := profile.Profile{Name: "x", Prompt: "#", Answers: []profile.Step{{Expect: "Password:", Send: "{password}"}}}
	if err := a.SaveProfile(p); err == nil {
		t.Error("an answer sending {password} was accepted")
	}
	p.Answers[0].Send = "y"
	if err := a.SaveProfile(p); err != nil {
		t.Errorf("plain answer refused: %v", err)
	}
}
