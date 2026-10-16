package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/schedule"
)

// gridApp is an unlocked App with three devices in group "lab" (one of
// them named in a schedule) and one ungrouped.
func gridApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []model.Device{
		{Name: "rt1", Group: "lab", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios", Enabled: true},
		{Name: "rt2", Group: "lab", Host: "192.0.2.2", Conn: model.ConnSSH, OSType: "cisco-ios", Enabled: true},
		{Name: "rt3", Group: "lab", Host: "192.0.2.3", Conn: model.ConnSSH, OSType: "cisco-ios", Enabled: true},
		{Name: "solo", Host: "192.0.2.9", Conn: model.ConnTelnet, OSType: "cisco-ios", Enabled: true},
	} {
		if err := a.SaveDevice(d); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func gridRowsOf(t *testing.T, a *App, scope string) []model.GridRow {
	t.Helper()
	rows, err := a.GridRows(scope)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func devNamed(a *App, name string) *model.Device {
	for i := range a.inv.Devices {
		if a.inv.Devices[i].Name == name {
			return &a.inv.Devices[i]
		}
	}
	return nil
}

func TestGridRowsScope(t *testing.T) {
	a := gridApp(t)
	if n := len(gridRowsOf(t, a, "lab")); n != 3 {
		t.Errorf("lab rows = %d", n)
	}
	if r := gridRowsOf(t, a, model.GridUngrouped); len(r) != 1 || r[0].Cells["name"] != "solo" || r[0].Cells["conn"] != "telnet" {
		t.Errorf("ungrouped rows = %v", r)
	}
	if n := len(gridRowsOf(t, a, "")); n != 4 {
		t.Errorf("all rows = %d", n)
	}
}

// Edit, add (inserted between two rows) and delete in one save; the table's
// order becomes the stored order.
func TestApplyGridEditAddDelete(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	rows[0].Cells["host"] = "192.0.2.11"
	newRow := model.GridRow{UID: "n1", Cells: map[string]string{"name": "sw9", "group": "lab", "host": "192.0.2.99", "conn": "telnet", "osType": "cisco-ios"}}
	del := rows[2]
	req := GridApply{Scope: "lab", Rows: []model.GridRow{rows[0], newRow, rows[1]}, Deleted: []model.GridRow{del}, Mode: "all"}
	rep, err := a.ApplyGrid(req)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || rep.Added != 1 || rep.Changed != 1 || rep.Deleted != 1 || len(rep.Errors) != 0 {
		t.Fatalf("report %+v", rep)
	}
	if devNamed(a, "rt1").Host != "192.0.2.11" || devNamed(a, "rt3") != nil || devNamed(a, "sw9") == nil {
		t.Fatalf("devices %+v", a.inv.Devices)
	}
	var names []string
	for _, d := range a.inv.Devices {
		names = append(names, d.Name)
	}
	if got := strings.Join(names, ","); got != "rt1,sw9,rt2,solo" {
		t.Errorf("order = %s", got)
	}
	if s, ok := rep.Saved["n1"]; !ok || s.ID == "" || s.ID != devNamed(a, "sw9").ID || s.Base["host"] != "192.0.2.99" {
		t.Errorf("saved row for the new device: %+v", rep.Saved["n1"])
	}
}

// A row that will not save stops a "all" save whole; "valid" saves the rest.
func TestApplyGridErrorsAllOrValid(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	rows[0].Cells["port"] = "abc"
	rows[1].Cells["site"] = "本社"
	rows[2].Cells["host"] = "192.0.2.333"
	req := GridApply{Scope: "lab", Rows: rows, Mode: "all"}
	rep, err := a.ApplyGrid(req)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Applied || devNamed(a, "rt2").Site != "" {
		t.Fatal("an all-or-nothing save with errors must not save")
	}
	if e := rep.Errors[rows[0].UID]; len(e) == 0 || e[0].Col != "port" {
		t.Errorf("port error: %+v", e)
	}
	if e := rep.Errors[rows[2].UID]; len(e) == 0 || e[0].Col != "host" {
		t.Errorf("host error: %+v", e)
	}
	req.Mode = "valid"
	rep, _ = a.ApplyGrid(req)
	if !rep.Applied || devNamed(a, "rt2").Site != "本社" || devNamed(a, "rt3").Host != "192.0.2.3" || devNamed(a, "rt1").Port != 0 {
		t.Fatalf("valid-only save: %+v", rep)
	}
	if _, ok := rep.Saved[rows[1].UID]; !ok || len(rep.Saved) != 1 {
		t.Errorf("saved = %v", rep.Saved)
	}
}

// Two rows with one name in a group are both marked; so is a row taking the
// name of a device the table leaves alone.
func TestApplyGridDuplicateNames(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	rows[0].Cells["name"] = "rt2"
	rep, _ := a.ApplyGrid(GridApply{Rows: rows[:1], Mode: "check"})
	if e := rep.Errors[rows[0].UID]; len(e) != 1 || e[0].Col != "name" {
		t.Fatalf("rename onto an untouched device: %+v", rep.Errors)
	}
	// Swapping two names in one save is fine.
	rows[1].Cells["name"] = "rt1"
	rep, _ = a.ApplyGrid(GridApply{Rows: rows, Mode: "all"})
	if !rep.Applied {
		t.Fatalf("name swap refused: %+v", rep.Errors)
	}
	n1 := model.GridRow{UID: "a", Cells: map[string]string{"name": "x", "group": "lab", "host": "192.0.2.5", "osType": "cisco-ios"}}
	n2 := model.GridRow{UID: "b", Cells: map[string]string{"name": "x", "group": "lab", "host": "192.0.2.6", "osType": "cisco-ios"}}
	rep, _ = a.ApplyGrid(GridApply{Rows: []model.GridRow{n1, n2}, Mode: "valid"})
	if len(rep.Errors["a"]) == 0 || len(rep.Errors["b"]) == 0 || rep.Applied {
		t.Fatalf("both duplicate rows must be marked: %+v", rep)
	}
}

// A device edited elsewhere after the table read it is reported with what
// changed, and saved over only when asked.
func TestApplyGridConflicts(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	other := *devNamed(a, "rt1")
	other.Site = "東京DC"
	if err := a.SaveDevice(other); err != nil {
		t.Fatal(err)
	}
	rows[0].Cells["role"] = "コア"
	rep, _ := a.ApplyGrid(GridApply{Rows: rows[:1], Mode: "all"})
	if rep.Applied || len(rep.Conflicts) != 1 {
		t.Fatalf("want one conflict: %+v", rep)
	}
	c := rep.Conflicts[0]
	if c.Name != "rt1" || len(c.Diffs) != 1 || c.Diffs[0].Col != "site" || c.Diffs[0].Theirs != "東京DC" || c.Current["site"] != "東京DC" {
		t.Errorf("conflict %+v", c)
	}
	rep, _ = a.ApplyGrid(GridApply{Rows: rows[:1], Mode: "all", Overwrite: []string{rows[0].UID}})
	if !rep.Applied || devNamed(a, "rt1").Role != "コア" || devNamed(a, "rt1").Site != "" {
		t.Fatalf("overwrite: %+v", rep)
	}

	// Deleted elsewhere: reported as gone; overwriting brings it back as new.
	rows = gridRowsOf(t, a, "lab")
	if err := a.DeleteDevice(rows[1].ID); err != nil {
		t.Fatal(err)
	}
	rows[1].Cells["site"] = "x"
	rep, _ = a.ApplyGrid(GridApply{Rows: rows[1:2], Mode: "all"})
	if len(rep.Conflicts) != 1 || !rep.Conflicts[0].Gone {
		t.Fatalf("gone: %+v", rep.Conflicts)
	}
	rep, _ = a.ApplyGrid(GridApply{Rows: rows[1:2], Mode: "all", Overwrite: []string{rows[1].UID}})
	if !rep.Applied || devNamed(a, "rt2") == nil || devNamed(a, "rt2").ID == rows[1].ID {
		t.Fatalf("restore: %+v", rep)
	}

	// A deletion of a device changed meanwhile asks too.
	rows = gridRowsOf(t, a, "lab")
	d3 := *devNamed(a, "rt3")
	d3.Role = "FW"
	_ = a.SaveDevice(d3)
	var del model.GridRow
	for _, r := range rows {
		if r.Cells["name"] == "rt3" {
			del = r
		}
	}
	rep, _ = a.ApplyGrid(GridApply{Deleted: []model.GridRow{del}, Mode: "all"})
	if len(rep.Conflicts) != 1 || rep.Conflicts[0].UID != "del:"+del.ID || devNamed(a, "rt3") == nil {
		t.Fatalf("delete conflict: %+v", rep)
	}
}

func TestApplyGridUnknownSetAndOS(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	rows[0].Cells["osType"] = "no-such-os"
	rows[0].Cells["commandSet"] = "no-such-set"
	rep, _ := a.ApplyGrid(GridApply{Rows: rows[:1], Mode: "check"})
	cols := map[string]bool{}
	for _, e := range rep.Errors[rows[0].UID] {
		cols[e.Col] = true
	}
	if !cols["osType"] || !cols["commandSet"] {
		t.Fatalf("errors %+v", rep.Errors)
	}
}

// A renamed device keeps its place in the schedules that name it.
func TestApplyGridRenameFollowsSchedules(t *testing.T) {
	a := gridApp(t)
	a.mu.Lock()
	a.inv.Schedules = append(a.inv.Schedules, schedule.Schedule{ID: "s1", Name: "nightly", Group: "lab", Names: []string{"rt1"}})
	a.mu.Unlock()
	rows := gridRowsOf(t, a, "lab")
	rows[0].Cells["name"] = "rt1-new"
	if rep, _ := a.ApplyGrid(GridApply{Rows: rows[:1], Mode: "all"}); !rep.Applied {
		t.Fatal(rep.Errors)
	}
	if got := a.inv.Schedules[0].Names; len(got) != 1 || got[0] != "rt1-new" {
		t.Errorf("schedule names = %v", got)
	}
}

// The draft survives in the vault until saved or thrown away; an unchanged
// table leaves none, and the badge counts changed rows and deletions.
func TestGridDraftLifecycle(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	if err := a.SaveGridDraft("lab", model.GridDraft{Rows: rows}); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.GetGridDraft("lab"); d != nil {
		t.Fatal("an unchanged table must not leave a draft")
	}
	rows[0].Cells["site"] = "本社"
	_ = a.SaveGridDraft("lab", model.GridDraft{Rows: rows, Deleted: []model.GridRow{rows[2]}})
	if n := a.GridDraftCounts()["lab"]; n != 2 {
		t.Errorf("count = %d", n)
	}
	a.flushPersist()
	b := NewApp()
	b.vaultPath = a.vaultPath
	if err := b.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	d, _ := b.GetGridDraft("lab")
	if d == nil || d.Rows[0].Cells["site"] != "本社" || len(d.Deleted) != 1 || d.Saved == "" {
		t.Fatalf("draft after reopening: %+v", d)
	}
	if err := b.DiscardGridDraft("lab"); err != nil {
		t.Fatal(err)
	}
	if len(b.GridDraftCounts()) != 0 {
		t.Error("discarded draft still counted")
	}
}

// A group renamed while its table has a draft keeps the draft (under the
// new name, rows following) and saves without conflicts; a deleted group's
// draft goes with it.
func TestGridDraftFollowsGroupRenameAndDelete(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	rows[0].Cells["site"] = "本社"
	if err := a.SaveGridDraft("lab", model.GridDraft{Rows: rows}); err != nil {
		t.Fatal(err)
	}
	if err := a.RenameDeviceGroup("lab", "lab2"); err != nil {
		t.Fatal(err)
	}
	d, _ := a.GetGridDraft("lab2")
	if d == nil || d.Rows[0].Cells["group"] != "lab2" || d.Rows[0].Base["group"] != "lab2" {
		t.Fatalf("draft after rename: %+v", d)
	}
	if old, _ := a.GetGridDraft("lab"); old != nil {
		t.Fatal("draft left under the old name")
	}
	rep, _ := a.ApplyGrid(GridApply{Scope: "lab2", Rows: d.Rows, Mode: "all"})
	if !rep.Applied || len(rep.Conflicts) != 0 || devNamed(a, "rt1").Site != "本社" {
		t.Fatalf("save after rename: %+v", rep)
	}
	rows = gridRowsOf(t, a, "lab2")
	rows[1].Cells["site"] = "x"
	_ = a.SaveGridDraft("lab2", model.GridDraft{Rows: rows})
	if _, err := a.DeleteDeviceGroup("lab2"); err != nil {
		t.Fatal(err)
	}
	if len(a.GridDraftCounts()) != 0 {
		t.Error("a deleted group's draft is still counted")
	}
}

// Renaming by renumbering (rt1→rt2, rt2→rt3) or swapping moves each
// schedule's device name once: the schedule keeps pointing at the same
// device, not the next one in the chain.
func TestApplyGridRenumberKeepsSchedulesOnTheirDevice(t *testing.T) {
	a := gridApp(t)
	a.mu.Lock()
	a.inv.Schedules = append(a.inv.Schedules, schedule.Schedule{ID: "s1", Name: "n", Group: "lab", Names: []string{"rt1"}})
	a.mu.Unlock()
	rows := gridRowsOf(t, a, "lab")
	rows[0].Cells["name"] = "rt2"
	rows[1].Cells["name"] = "rt3"
	rows[2].Cells["name"] = "rt1"
	if rep, _ := a.ApplyGrid(GridApply{Rows: rows, Mode: "all"}); !rep.Applied {
		t.Fatal(rep.Errors)
	}
	if got := a.inv.Schedules[0].Names[0]; got != "rt2" {
		t.Errorf("schedule followed the chain to %q, want rt2 (the device that was rt1)", got)
	}
}

// Saving only the rows without errors must not leave two devices with one
// name: a row kept back keeps its stored name, and a saved row taking that
// name is held back too.
func TestApplyGridValidOnlyNoDuplicateNames(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	rows[0].Cells["name"] = "rt9"
	rows[0].Cells["port"] = "abc" // rt1 → rt9, but not saved
	rows[1].Cells["name"] = "rt1" // rt2 → rt1: would clash with rt1 staying
	rep, _ := a.ApplyGrid(GridApply{Rows: rows[:2], Mode: "valid"})
	if len(rep.Errors[rows[1].UID]) == 0 {
		t.Fatalf("the clashing rename was not held back: %+v", rep)
	}
	n := 0
	for _, d := range a.inv.Devices {
		if d.Group == "lab" && d.Name == "rt1" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d devices named rt1 in lab", n)
	}
}

// A deletion of a device already deleted elsewhere is reported done, so the
// table drops it (and its draft badge goes).
func TestApplyGridDeletionOfGoneDeviceIsDone(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	if err := a.DeleteDevice(rows[2].ID); err != nil {
		t.Fatal(err)
	}
	rep, _ := a.ApplyGrid(GridApply{Deleted: []model.GridRow{rows[2]}, Mode: "all"})
	if len(rep.SavedDeletes) != 1 || rep.SavedDeletes[0] != rows[2].ID {
		t.Fatalf("savedDeletes = %v", rep.SavedDeletes)
	}
}

// Pins of rows the table deletes go only once the vault write succeeded;
// a failed write puts them back with the devices.
func TestApplyGridDropsPinsAfterSave(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"g1", "g2"} {
		if err := a.SaveDevice(model.Device{Name: n, Host: "192.0.2.5", Conn: model.ConnSSH, OSType: "nec-ix", GUIOn: true, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	inv := a.GetInventory()
	for _, d := range inv.Devices {
		a.setGUICert(d.GUICertKey(), "ab")
	}
	g1, g2 := inv.Devices[0], inv.Devices[1]
	rows, _ := a.GridRows("")
	var keep []model.GridRow
	var del []model.GridRow
	for _, r := range rows {
		if r.ID == g1.ID {
			del = append(del, r)
		} else {
			keep = append(keep, r)
		}
	}
	// A write that cannot succeed: a path whose parent "directory" is a
	// plain file (the writer creates missing directories, but not inside a
	// file).
	good := a.vaultPath
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.vaultPath = filepath.Join(blocker, "x", "vault.enc")
	if _, err := a.ApplyGrid(GridApply{Mode: "all", Rows: keep, Deleted: del}); err == nil {
		t.Fatal("save into an impossible path succeeded")
	}
	after := a.GetInventory()
	if _, ok := after.KnownGUICerts[g1.GUICertKey()]; !ok {
		t.Error("failed save lost the deleted row's pin")
	}
	if len(after.Devices) != 2 {
		t.Errorf("failed save left %d devices", len(after.Devices))
	}
	a.vaultPath = good
	if _, err := a.ApplyGrid(GridApply{Mode: "all", Rows: keep, Deleted: del}); err != nil {
		t.Fatal(err)
	}
	after = a.GetInventory()
	if _, ok := after.KnownGUICerts[g1.GUICertKey()]; ok {
		t.Error("deleted row's pin still there after a successful save")
	}
	if _, ok := after.KnownGUICerts[g2.GUICertKey()]; !ok {
		t.Error("the kept device's pin went too")
	}
}
