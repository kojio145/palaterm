package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/schedule"
)

func schedInventory() *model.Inventory {
	return &model.Inventory{
		DeviceGroups: []model.DeviceGroup{
			{Name: "A社", Username: "ga", Password: "gpa"},
			{Name: "B社", Username: "gb", Password: "gpb"},
		},
		Devices: []model.Device{
			{Name: "a1", Group: "A社", Host: "192.0.2.1", Conn: model.ConnSSH, UseGroupCreds: true, Enabled: true, CommandSet: "show"},
			{Name: "a2", Group: "A社", Host: "192.0.2.2", Conn: model.ConnSSH, Username: "own", Password: "ownpw", Enabled: true, CommandSet: "show"},
			{Name: "b1", Group: "B社", Host: "192.0.2.3", Conn: model.ConnSSH, Username: "bu", Password: "bpw", Enabled: true, CommandSet: "show"},
			{Name: "n1", Host: "192.0.2.4", Conn: model.ConnSSH, Username: "nu", Password: "npw", Enabled: true, CommandSet: "show"},
		},
		CommandSets: []model.CommandSet{
			{Name: "show", Commands: []model.Command{{Text: "show clock"}}},
			{Name: "change", Commands: []model.Command{{Text: "configure terminal"}, {Text: "write memory"}}},
		},
		Settings: model.Settings{LastKeyFile: `C:\k\id`},
	}
}

// The copy kept while locked holds the targeted devices only, with their
// credentials resolved, and no group credentials at all.
func TestSchedCopyKeepsOnlyTargets(t *testing.T) {
	inv := schedInventory()
	if schedCopy(inv) != nil {
		t.Fatal("no schedules: nothing should be kept")
	}
	inv.Schedules = []schedule.Schedule{
		{ID: "1", Enabled: true, Group: "A社", Names: []string{"a1"}},
		{ID: "2", Enabled: false, Group: "B社"},
		{ID: "3", Enabled: true, Ungrouped: true},
	}
	c := schedCopy(inv)
	var names []string
	for _, d := range c.Devices {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "a1,n1" {
		t.Fatalf("kept devices = %v, want a1,n1 (a2 not named, b1's schedule disabled)", names)
	}
	if d := c.Devices[0]; d.Username != "ga" || d.Password != "gpa" || d.UseGroupCreds {
		t.Errorf("a1 should carry its group's credentials itself: %+v", d)
	}
	for _, g := range c.DeviceGroups {
		if g.Username != "" || g.Password != "" {
			t.Errorf("group credentials kept: %+v", g)
		}
	}
	if c.Settings.LastKeyFile != "" {
		t.Error("LastKeyFile kept")
	}
	// The vault itself is untouched.
	if inv.DeviceGroups[0].Password != "gpa" || len(inv.Devices) != 4 {
		t.Error("schedCopy changed the inventory")
	}
	// A run from the copy works like one from the vault.
	snap, err := snapshotOf(c, schedRequest(inv.Schedules[0]))
	if err != nil || len(snap.Devices) != 1 || snap.Devices[0].Password != "gpa" {
		t.Fatalf("run from the copy: %v %+v", err, snap)
	}
	// "Names nil" schedules take the group's checked devices at firing time.
	inv.Schedules[0].Names = nil
	c = schedCopy(inv)
	if len(c.Devices) != 3 {
		t.Errorf("whole group + ungrouped expected, got %d", len(c.Devices))
	}
}

func TestChangeCommandsRefused(t *testing.T) {
	inv := schedInventory()
	snap, err := snapshotOf(inv, RunRequest{Group: "A社", CommandSet: "change"})
	if err != nil {
		t.Fatal(err)
	}
	err = changeCommandsErr(snap)
	if err == nil || !errors.Is(err, errChangeCommands) || !strings.Contains(err.Error(), "configure terminal / write memory") {
		t.Fatalf("got %v", err)
	}
	if strings.Count(err.Error(), "「change」") != 1 {
		t.Errorf("each set named once: %v", err)
	}
	snap, _ = snapshotOf(inv, RunRequest{Group: "A社"})
	if err := changeCommandsErr(snap); err != nil {
		t.Errorf("show set refused: %v", err)
	}
}

// Renaming a device, group or set keeps the schedules pointing at it.
func TestScheduleFollowsRenames(t *testing.T) {
	inv := schedInventory()
	inv.Schedules = []schedule.Schedule{
		{ID: "1", Group: "A社", Names: []string{"a1", "a2"}, CommandSet: "show"},
		{ID: "2", Group: "B社", Names: []string{"a1"}},
		{ID: "3", Ungrouped: true},
	}
	renameSchedDevice(inv, "A社", "a1", "a1-new")
	renameSchedGroup(inv, "A社", "A社2")
	renameSchedSet(inv, "show", "show2")
	s1, s2 := inv.Schedules[0], inv.Schedules[1]
	if s1.Names[0] != "a1-new" || s1.Group != "A社2" || s1.CommandSet != "show2" {
		t.Errorf("schedule 1 = %+v", s1)
	}
	if s2.Names[0] != "a1" || s2.Group != "B社" {
		t.Errorf("another group's a1 must stay: %+v", s2)
	}
	if inv.Schedules[2].Group != "" {
		t.Errorf("ungrouped schedule changed: %+v", inv.Schedules[2])
	}
}

// 予約がある間はスリープしない holds the keep-awake for a schedule far
// ahead; without it, only the last minutes before a firing do.
func TestScheduleKeepAwakeSetting(t *testing.T) {
	inv := schedInventory()
	inv.Schedules = []schedule.Schedule{{ID: "1", Name: "n", Enabled: true, Group: "A社",
		Slots: []schedule.Slot{{Kind: schedule.KindOnce, Date: "2099-01-01", Time: "03:00:00"}}}}
	a := NewApp()
	a.inv = inv
	defer armWake(time.Time{})
	now := time.Now()
	a.schedTick(now)
	if a.sched.release != nil {
		t.Fatal("kept awake for a firing years away without the setting")
	}
	inv.Settings.ScheduleKeepAwake = true
	a.schedTick(now.Add(time.Second))
	if a.sched.release == nil {
		t.Fatal("setting on: not kept awake")
	}
	// Locked: the copy carries the setting.
	a.schedInv, a.inv = schedCopy(inv), nil
	a.schedTick(now.Add(2 * time.Second))
	if a.sched.release == nil {
		t.Fatal("locked: keep-awake dropped")
	}
	a.schedInv.Settings.ScheduleKeepAwake = false
	a.schedTick(now.Add(3 * time.Second))
	if a.sched.release != nil {
		t.Fatal("setting off: still kept awake")
	}
}

// A set saved before control characters were refused still cannot hide a
// second command behind a carriage return: refused at run time too.
func TestHiddenCRInSavedSetRefused(t *testing.T) {
	inv := schedInventory()
	inv.CommandSets[0].Commands = []model.Command{{Text: "show clock\rreload"}}
	if _, err := snapshotOf(inv, RunRequest{Group: "A社"}); err == nil || !strings.Contains(err.Error(), "「show」") {
		t.Fatalf("got %v, want the set refused", err)
	}
}

// A firing that waited its turn does not run once its schedule was deleted
// or stopped.
func TestGoneScheduleDoesNotRun(t *testing.T) {
	inv := schedInventory()
	inv.Schedules = []schedule.Schedule{{ID: "1", Name: "n", Enabled: false, Group: "A社"}}
	a := NewApp()
	a.inv = inv
	if err := a.startScheduled(schedJob{sc: schedule.Schedule{ID: "1", Enabled: true, Group: "A社"}, due: time.Now()}); !errors.Is(err, errSchedGone) {
		t.Errorf("stopped: got %v", err)
	}
	if err := a.startScheduled(schedJob{sc: schedule.Schedule{ID: "deleted", Enabled: true, Group: "A社"}, due: time.Now()}); !errors.Is(err, errSchedGone) {
		t.Errorf("deleted: got %v", err)
	}
	if a.runActive {
		t.Error("a batch was started")
	}
}

// A 10-second repeat whose round outlasts the period: the passed-over
// firings are counted, not written one line per second, and × keeps the
// process (it is running).
func TestSkippedFiringsCollected(t *testing.T) {
	inv := schedInventory()
	start := time.Now().Add(-time.Minute).Truncate(time.Second)
	inv.Schedules = []schedule.Schedule{{ID: "1", Name: "n", Enabled: true, Group: "A社",
		Slots: []schedule.Slot{{Kind: schedule.KindInterval, Date: start.Format("2006-01-02"), Time: start.Format("15:04:05"), EverySeconds: 10}}}}
	a := NewApp()
	a.inv = inv
	defer armWake(time.Time{})
	defer func() {
		if a.sched.release != nil {
			a.sched.release()
		}
	}()
	a.sched.runID = "1" // its previous round is still running
	now := time.Now()
	a.schedTick(now)
	for i := 1; i <= 35; i++ {
		a.schedTick(now.Add(time.Duration(i) * time.Second))
	}
	if n := a.sched.skips["1"].N; n < 3 || n > 4 {
		t.Errorf("skipped firings = %d, want 3 or 4 in 35 s", n)
	}
	if len(a.sched.queue) != 0 {
		t.Errorf("stacked behind the running round: %v", a.sched.queue)
	}
	if !a.hasPendingSchedules() {
		t.Error("running: × must not quit")
	}
	a.schedFlushSkips("1", "n")
	if _, ok := a.sched.skips["1"]; ok {
		t.Error("not cleared after writing")
	}
}

// The clock set back does not run the same firings again.
func TestClockBackDoesNotRepeat(t *testing.T) {
	inv := schedInventory()
	inv.Schedules = []schedule.Schedule{{ID: "1", Name: "n", Enabled: true, Group: "A社",
		Slots: []schedule.Slot{{Kind: schedule.KindDaily, Time: "03:00:00"}}}}
	a := NewApp()
	a.inv = inv
	defer armWake(time.Time{})
	defer func() {
		if a.sched.release != nil {
			a.sched.release()
		}
	}()
	now := time.Now()
	a.schedTick(now)
	a.schedTick(now.Add(-time.Hour))
	if got := a.sched.checked["1"]; !got.Equal(now) {
		t.Errorf("checked moved back to %v", got)
	}
}
