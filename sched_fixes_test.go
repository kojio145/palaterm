package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/schedule"
)

// 2026-10-22 review (G3): schedules and the device operations around them.

func schedByID(t *testing.T, a *App, id string) schedule.Schedule {
	t.Helper()
	for _, sc := range a.inv.Schedules {
		if sc.ID == id {
			return sc
		}
	}
	t.Fatalf("schedule %s gone", id)
	return schedule.Schedule{}
}

func stoppedLines(a *App, id string) []SchedLogEntry {
	var out []SchedLogEntry
	for _, e := range a.readSchedLog(0) {
		if e.ScheduleID == id && e.Kind == "stopped" {
			out = append(out, e)
		}
	}
	return out
}

// B3: a device moved to another group in its own editor (renamed at the
// same time or not), deleted alone or several at once, stops the schedules
// naming it; a rename within its group is followed; the group-wide
// schedule goes on.
func TestDeviceEditsStopSchedules(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	u := "s" + schedule.NewID() + "-" // the 実行記録 file is shared between tests
	a.mu.Lock()
	a.inv.DeviceGroups = []model.DeviceGroup{{Name: "lab"}, {Name: "lab2"}}
	for i, n := range []string{"sw1", "sw2", "sw3", "sw4", "sw5"} {
		a.inv.Devices = append(a.inv.Devices, model.Device{ID: "d" + n, Name: n, Group: "lab", Host: "192.0.2." + string(rune('1'+i)), Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Enabled: true})
	}
	daily := []schedule.Slot{{Kind: schedule.KindDaily, Time: "03:00:00"}}
	for _, n := range []string{"sw1", "sw2", "sw3", "sw4", "sw5"} {
		a.inv.Schedules = append(a.inv.Schedules, schedule.Schedule{ID: u + n, Name: n + " only", Enabled: true, Group: "lab", Names: []string{n}, Slots: daily})
	}
	a.inv.Schedules = append(a.inv.Schedules, schedule.Schedule{ID: u + "all", Name: "all of lab", Enabled: true, Group: "lab", Slots: daily})
	a.mu.Unlock()

	// Moved and renamed at once in the device editor.
	d := a.inv.Devices[0]
	d.Group, d.Name = "lab2", "sw1-new"
	if err := a.SaveDevice(d); err != nil {
		t.Fatal(err)
	}
	if schedByID(t, a, u+"sw1").Enabled {
		t.Error("sw1 moved to lab2: its schedule still on")
	}
	if l := stoppedLines(a, u+"sw1"); len(l) != 1 || !strings.Contains(l[0].Message, "sw1") || !strings.Contains(l[0].Message, "移動") {
		t.Errorf("sw1's record: %+v", l)
	}
	// Renamed within its group: followed, not stopped.
	d = a.inv.Devices[1]
	d.Name = "sw2b"
	if err := a.SaveDevice(d); err != nil {
		t.Fatal(err)
	}
	if sc := schedByID(t, a, u+"sw2"); !sc.Enabled || sc.Names[0] != "sw2b" {
		t.Errorf("renamed in its group: %+v", sc)
	}
	// Deleted alone, and several at once.
	if err := a.DeleteDevice("dsw3"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeleteDevices([]string{"dsw4", "dsw5"}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"sw3", "sw4", "sw5"} {
		if schedByID(t, a, u+n).Enabled {
			t.Errorf("%s deleted: its schedule still on", n)
		}
		if l := stoppedLines(a, u+n); len(l) != 1 || !strings.Contains(l[0].Message, "削除") {
			t.Errorf("%s's record: %+v", n, l)
		}
	}
	if !schedByID(t, a, u+"all").Enabled {
		t.Error("the group-wide schedule was stopped")
	}
	// A device of that name added later is not run by the stopped schedule.
	if err := a.SaveDevice(model.Device{Name: "sw3", Group: "lab", Host: "192.0.2.30", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if schedByID(t, a, u+"sw3").Enabled {
		t.Error("a new sw3 brought the old schedule back")
	}
}

// B3: the table's deletions and group changes stop the schedules naming
// those devices — against the names as they were, so a renumbering in the
// same save (rt1 → rt3 once rt3 is deleted) does not stop rt1's.
func TestApplyGridStopsSchedules(t *testing.T) {
	a := gridApp(t)
	u := "g" + schedule.NewID() + "-"
	a.mu.Lock()
	for _, n := range []string{"rt1", "rt2", "rt3"} {
		a.inv.Schedules = append(a.inv.Schedules, schedule.Schedule{ID: u + n, Name: n, Enabled: true, Group: "lab", Names: []string{n}})
	}
	a.mu.Unlock()
	rows := gridRowsOf(t, a, "lab")
	rows[0].Cells["name"] = "rt3"   // rt1 takes the deleted rt3's name
	rows[1].Cells["group"] = "lab2" // rt2 moves
	if rep, err := a.ApplyGrid(GridApply{Scope: "lab", Rows: rows[:2], Deleted: []model.GridRow{rows[2]}, Mode: "all"}); err != nil || !rep.Applied {
		t.Fatalf("%v %+v", err, rep)
	}
	if sc := schedByID(t, a, u+"rt1"); !sc.Enabled || sc.Names[0] != "rt3" {
		t.Errorf("rt1 renamed: %+v (want on, following the name)", sc)
	}
	if schedByID(t, a, u+"rt2").Enabled || schedByID(t, a, u+"rt3").Enabled {
		t.Error("moved rt2 / deleted rt3: schedules still on")
	}
}

// B3: 一覧の取り込み deleting the group's devices the file no longer has.
func TestListImportDeleteStopsSchedules(t *testing.T) {
	a, path, id := listImportApp(t)
	req := ListImportReq{Path: path, RuleID: id, Group: "lab", NewGroup: true}
	pv, err := a.PreviewListImport(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Stamp = pv.Stamp
	if _, err := a.ApplyListImport(req); err != nil {
		t.Fatal(err)
	}
	sid := "l" + schedule.NewID()
	a.mu.Lock()
	a.inv.Schedules = append(a.inv.Schedules, schedule.Schedule{ID: sid, Name: "sw2", Enabled: true, Group: "lab", Names: []string{"sw2"}})
	a.mu.Unlock()
	later := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(path, []byte("機器名称,IP\nsw1,192.0.2.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(path, later, later)
	req = ListImportReq{Path: path, RuleID: id, Group: "lab"}
	pv, err = a.PreviewListImport(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range pv.Plan.Missing {
		req.DeleteIDs = append(req.DeleteIDs, c.ID)
	}
	req.Stamp = pv.Stamp
	res, err := a.ApplyListImport(req)
	if err != nil || res.Deleted != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if schedByID(t, a, sid).Enabled {
		t.Error("sw2 deleted by the import: its schedule still on")
	}
}

// B3: deleting a group moves its devices to 「グループ未設定」: the
// ungrouped schedules they would silently join stop; one naming another
// ungrouped device goes on.
func TestDeleteGroupStopsUngroupedSchedules(t *testing.T) {
	a := gridApp(t)
	u := "u" + schedule.NewID() + "-"
	a.mu.Lock()
	a.inv.Schedules = append(a.inv.Schedules,
		schedule.Schedule{ID: u + "all", Name: "all ungrouped", Enabled: true, Ungrouped: true},
		schedule.Schedule{ID: u + "solo", Name: "solo", Enabled: true, Ungrouped: true, Names: []string{"solo"}},
		schedule.Schedule{ID: u + "stale", Name: "rt1 (gone)", Enabled: true, Ungrouped: true, Names: []string{"rt1"}})
	a.mu.Unlock()
	if _, err := a.DeleteDeviceGroup("lab"); err != nil {
		t.Fatal(err)
	}
	if schedByID(t, a, u+"all").Enabled || schedByID(t, a, u+"stale").Enabled {
		t.Error("the ungrouped schedules the group's devices fall into are still on")
	}
	if !schedByID(t, a, u+"solo").Enabled {
		t.Error("a schedule on another ungrouped device was stopped")
	}
	if l := stoppedLines(a, u+"all"); len(l) != 1 || !strings.Contains(l[0].Message, "グループ未設定") {
		t.Errorf("record: %+v", l)
	}
}

// B4: the last look is kept in data\ across a restart: started again with
// the vault locked, nothing runs and the lock screen says so; at the unlock
// the firings in between are recorded as missed.
func TestScheduleCheckedAcrossRestart(t *testing.T) {
	state := filepath.Join(t.TempDir(), "schedule-checked.json")
	t0 := time.Now().Round(0)
	at := t0.Add(-2 * time.Hour) // the daily firing: 2 h before t0, so none is within MaxLate below
	id := "r" + schedule.NewID()
	inv := schedInventory()
	inv.Schedules = []schedule.Schedule{{ID: id, Name: "nightly", Enabled: true, Group: "A社",
		Slots: []schedule.Slot{{Kind: schedule.KindDaily, Time: at.Format("15:04:05")}}}}
	defer armWake(time.Time{})

	a := NewApp()
	a.sched.statePath = state
	a.inv = inv
	a.schedTick(t0)
	if a.sched.release != nil {
		a.sched.release()
	}
	b, err := os.ReadFile(state)
	if err != nil || strings.Contains(string(b), "password") || !strings.Contains(string(b), id) {
		t.Fatalf("state file: %v %s", err, b)
	}

	// Started again two days later: locked, nothing to read the schedules from.
	c := NewApp()
	c.sched.statePath = state
	c.schedTick(t0.Add(47 * time.Hour))
	if !c.SchedulesWaitUnlock() {
		t.Error("lock screen: schedules waiting for the unlock not said")
	}
	if _, ok := c.sched.checked[id]; ok {
		t.Error("looked at a schedule it cannot read")
	}
	c.inv = inv // unlocked
	c.schedTick(t0.Add(48 * time.Hour))
	if c.sched.release != nil {
		c.sched.release()
	}
	if c.SchedulesWaitUnlock() {
		t.Error("unlocked: still says waiting")
	}
	var got []SchedLogEntry
	for _, e := range c.readSchedLog(0) {
		if e.ScheduleID == id {
			got = append(got, e)
		}
	}
	if len(got) != 1 || got[0].Kind != "missed" || !strings.Contains(got[0].Message, "2 回分") || !strings.Contains(got[0].Message, "ロック解除前") {
		t.Fatalf("missed record: %+v", got)
	}
	if len(c.sched.queue) != 0 {
		t.Errorf("a missed firing was queued: %+v", c.sched.queue)
	}

	// No schedule enabled any more: the lock screen says nothing.
	inv.Schedules[0].Enabled = false
	c.schedTick(t0.Add(48*time.Hour + time.Second))
	d := NewApp()
	d.sched.statePath = state
	if d.SchedulesWaitUnlock() {
		t.Error("nothing enabled: still says waiting")
	}
}

// B8: the times compared are wall-clock ones (a monotonic reading would
// hide a clock set back), and a clock set back keeps the handled firings.
func TestSchedCheckedIsWallClock(t *testing.T) {
	inv := schedInventory()
	inv.Schedules = []schedule.Schedule{{ID: "1", Name: "n", Enabled: true, Group: "A社",
		Slots: []schedule.Slot{{Kind: schedule.KindDaily, Time: "03:00:00"}}}}
	a := NewApp()
	a.inv = inv
	defer armWake(time.Time{})
	now := time.Now() // carries a monotonic reading
	a.schedTick(now)
	if a.sched.release != nil {
		a.sched.release()
		a.sched.release = nil
	}
	got := a.sched.checked["1"]
	if got != got.Round(0) {
		t.Fatal("checked keeps a monotonic reading: a clock set back would compare by it")
	}
	// The wall clock set back an hour (the monotonic reading went on).
	a.schedTick(now.Round(0).Add(-time.Hour))
	if a.sched.release != nil {
		a.sched.release()
	}
	if c := a.sched.checked["1"]; !c.Equal(got) {
		t.Errorf("checked moved back to %v", c)
	}
}

// B5: the list marks a schedule whose newest record went wrong; a problem
// is told once in a while, a stop every time.
func TestSchedProblemsTold(t *testing.T) {
	log := []SchedLogEntry{ // newest first
		{ScheduleID: "a", Kind: "error", Message: "x"},
		{ScheduleID: "a", Kind: "run", OK: 1},
		{ScheduleID: "b", Kind: "run", OK: 2},
		{ScheduleID: "b", Kind: "missed"},
		{ScheduleID: "c", Kind: "run", OK: 1, Failed: 1},
		{ScheduleID: "d", Kind: "run", OK: 1, Missing: []string{"sw9"}},
		{ScheduleID: "e", Kind: "stopped"},
	}
	m := schedLastProblems(log)
	if m["a"] == nil || m["a"].Kind != "error" || m["b"] != nil || m["c"] == nil || m["d"] == nil || m["e"] == nil {
		t.Errorf("last problems: %+v", m)
	}
	a := NewApp()
	miss := SchedLogEntry{ScheduleID: "a", Schedule: "n", Kind: "missed"}
	if !a.schedNote(miss) || a.schedNote(miss) {
		t.Error("the same miss should be told once in a while")
	}
	if !a.schedNote(SchedLogEntry{ScheduleID: "a", Kind: "error"}) {
		t.Error("another kind is told")
	}
	stop := SchedLogEntry{ScheduleID: "a", Kind: "stopped"}
	if !a.schedNote(stop) || !a.schedNote(stop) {
		t.Error("a stop is always told")
	}
	if a.schedNote(SchedLogEntry{ScheduleID: "a", Kind: "run"}) {
		t.Error("a run is told by sched:done, not here")
	}
}

// B7: the devices a schedule names that its group no longer has.
func TestSchedMissing(t *testing.T) {
	inv := schedInventory()
	inv.Devices[1].CLIOff = true // GUI-only a2: there, left out on purpose
	sc := schedule.Schedule{Group: "A社", Names: []string{"a1", "a2", "gone", "b1"}}
	if got := strings.Join(schedMissing(inv, sc), ","); got != "gone,b1" {
		t.Errorf("missing = %s, want gone,b1 (b1 is another group's)", got)
	}
	sc.Names = nil
	if got := schedMissing(inv, sc); got != nil {
		t.Errorf("whole group: %v", got)
	}
}

// B9: a jump-host credential set by hop number only reaches the devices
// whose hop is the chosen host.
func TestUpdateDevicesHopByHost(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []model.Device{
		{Name: "sw1", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Enabled: true,
			Bastions: []model.Bastion{{Host: "192.0.2.9", Method: model.BastionSSH, Username: "jump1", Password: "old"}}},
		{Name: "sw2", Host: "192.0.2.2", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Enabled: true,
			Bastions: []model.Bastion{{Host: "192.0.2.10", Method: model.BastionSSH, Username: "jump2", Password: "old"}}},
		{Name: "sw3", Host: "192.0.2.3", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Enabled: true},
	} {
		if err := a.SaveDevice(d); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{a.inv.Devices[0].ID, a.inv.Devices[1].ID, a.inv.Devices[2].ID}
	hop, pw, host := 1, "new", "192.0.2.9"
	rep, err := a.UpdateDevices(ids, DevicePatch{BastionHop: &hop, BastionPassword: &pw, BastionHost: &host})
	if err != nil {
		t.Fatal(err)
	}
	if rep.HopApplied != 1 || rep.HopOther != 1 || rep.HopMissing != 1 {
		t.Errorf("report %+v, want 1 applied, 1 other host, 1 without the hop", rep)
	}
	if a.inv.Devices[0].Bastions[0].Password != "new" || a.inv.Devices[1].Bastions[0].Password != "old" {
		t.Errorf("hops: %+v / %+v", a.inv.Devices[0].Bastions, a.inv.Devices[1].Bastions)
	}
}
