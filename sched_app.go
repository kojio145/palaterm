package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kojio145/palaterm/internal/logstore"
	"github.com/kojio145/palaterm/internal/model"
	runpkg "github.com/kojio145/palaterm/internal/runner"
	"github.com/kojio145/palaterm/internal/schedule"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// 予約 (scheduled batch runs).
//
// The schedules live in the vault. The main window's process runs them —
// open, locked, or hidden in the tray after × — but not once it has quit:
// there is no service and no Task Scheduler entry, so nothing outside the
// encrypted vault ever holds a password.
//
// While the vault is locked, the devices the enabled schedules target are
// kept in memory with their credentials (schedCopy, taken at Lock): that is
// what lets a 03:00 run happen after the screen locked itself at 18:00.
//
// A loop looks at the wall clock every few seconds and compares it with
// where it looked last (schedule.Plan): a firing that came due runs; the
// ones a sleeping PC slept through are recorded as missed, never caught up
// on; one that could not start within schedule.MaxLate (another batch
// running) is skipped and recorded. Only one batch runs at a time, by hand
// or scheduled; the others wait their turn.

// schedTick is how often the clock is checked: firings are set to the
// second, and an interval can be as short as schedule.MinInterval.
const schedTick = time.Second

// schedAwakeBefore is how long before a firing the PC is kept from idle
// sleep (a woken PC goes back to sleep after about two minutes unattended).
const schedAwakeBefore = 3 * time.Minute

// schedWakeLead wakes the PC this long before a firing, so the network
// (VPN, Wi-Fi) is back when the batch connects.
const schedWakeLead = 90 * time.Second

// schedLogMax bounds the 予約の実行記録 file.
const schedLogMax = 500

// SchedLogEntry is one line of the 予約の実行記録 (data/schedule-log.json).
// Nothing secret: names, times and counts.
type SchedLogEntry struct {
	At         string `json:"at"`  // when it was recorded (RFC3339)
	Due        string `json:"due"` // the firing time it is about (RFC3339)
	ScheduleID string `json:"scheduleId"`
	Schedule   string `json:"schedule"`
	// Kind: run (it ran; see OK / Failed), missed (slept through or the app
	// was not able to look), skipped (waited past MaxLate), refused (change
	// commands in the set), error (could not start), stopped (disabled by
	// PalaTerm: its group was deleted or a device it names moved out).
	Kind    string `json:"kind"`
	OK      int    `json:"ok,omitempty"`
	Failed  int    `json:"failed,omitempty"`
	RunDir  string `json:"runDir,omitempty"`
	Message string `json:"message,omitempty"`
	Manual  bool   `json:"manual,omitempty"` // 今すぐ実行
	// Repeat: a run of a repeating (interval) schedule; Last: its last one.
	// A repeat only notifies on a failure and at its end, not every round.
	Repeat bool `json:"repeat,omitempty"`
	Last   bool `json:"last,omitempty"`
	// Logs are this round's log files, one per device (the 実行記録 opens
	// each with ログ表示; the folder holds every round's).
	Logs []SchedLogFile `json:"logs,omitempty"`
	// Missing: the devices the schedule names that were not found in its
	// group at this round (left out; also said in Message).
	Missing []string `json:"missing,omitempty"`
}

// SchedLogFile is one device's log of a 予約 round.
type SchedLogFile struct {
	Device string `json:"device"`
	Path   string `json:"path"`
}

type schedJob struct {
	sc     schedule.Schedule
	due    time.Time
	manual bool
}

// schedState is the scheduler's own state (App.sched).
type schedState struct {
	mu      sync.Mutex
	checked map[string]time.Time // per schedule ID: firings up to here are handled
	queue   []schedJob
	// progress of the scheduled batch running now
	runID, runName string
	runDone        int
	runTotal       int
	release        func() // keep-awake held ahead of a firing
	wake           string // armWake status
	wakeAt         time.Time
	logMu          sync.Mutex
	kick           chan struct{}
	notified       bool // the tray balloon about hiding was shown
	// skips: per schedule ID, the firings passed over because its previous
	// round was still waiting or running — written as one line when that
	// round ends, not one line a second.
	skips map[string]schedule.Span
	// statePath keeps checked across a restart (schedule-checked.json; ""
	// = not kept, as in tests). saved is what the file held at startup,
	// taken by each schedule at its first look; savedOn is how many
	// schedules were enabled when it was last written (the lock screen
	// says they wait for the unlock). stateDirty: written at the next tick.
	statePath   string
	stateLoaded bool
	saved       map[string]time.Time
	savedOn     int
	stateDirty  bool
	// noted: per schedule ID and kind, when a problem was last told on
	// screen and in the tray (a 10-second repeat failing every round would
	// otherwise pop up every 10 seconds).
	noted map[string]time.Time
	// log is the 実行記録 file in memory (under logMu; read once, then
	// appended to): a line a second must not re-read and re-parse the file.
	log       []SchedLogEntry
	logLoaded bool
	// runs counts the scheduled batches' goroutines, so the exit can wait
	// for the round that was running to write its line (waitSchedRuns).
	runs sync.WaitGroup
}

// schedNoteQuiet is how long the same problem of the same schedule is not
// told again.
const schedNoteQuiet = 10 * time.Minute

// schedCheckedFile is data/schedule-checked.json: per schedule ID, up to
// when its firings were handled. At the unlock after a restart the firings
// that came due in between — PalaTerm not running, or running but waiting
// for the vault — are planned like the ones a sleeping PC slept through
// (schedule.Plan): recorded as missed, except the newest, which runs then
// when it is at most schedule.MaxLate old (a nightly 03:00 still runs when
// PalaTerm is started and unlocked at 03:20; at 03:40 it is missed). IDs
// and times only, nothing secret (the file sits outside the vault).
type schedCheckedFile struct {
	Checked map[string]time.Time `json:"checked"`
	Enabled int                  `json:"enabled"`
}

func schedCheckedPath() string { return filepath.Join(exeDir(), "data", "schedule-checked.json") }

// loadStateLocked reads the file once (the caller holds s.mu).
func (s *schedState) loadStateLocked() {
	if s.stateLoaded || s.statePath == "" {
		return
	}
	s.stateLoaded = true
	var f schedCheckedFile
	if readJSONFile(s.statePath, &f) {
		s.saved = map[string]time.Time{}
		for id, at := range f.Checked {
			s.saved[id] = at.Round(0)
		}
		s.savedOn = f.Enabled
	}
}

// writeSchedState writes the file (outside s.mu).
func writeSchedState(path string, f schedCheckedFile) { _ = writeJSONAtomic(path, f) }

// writeJSONAtomic writes v as indented JSON to path through a temporary
// file and a rename, so a crash mid-write leaves the old file, not half of
// a new one (the scheduler's three data\ files all go through here).
func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readJSONFile reads path into v; false when there is no readable file.
func readJSONFile(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

func schedLogPath() string { return filepath.Join(exeDir(), "data", "schedule-log.json") }

// schedFiles are the scheduler's files in data\ (outside the vault): the
// 実行記録, the log folders, the last look. They name schedules, devices
// and log paths, so they go with the vault when it is reset.
func schedFiles() []string { return []string{schedLogPath(), schedDirsPath(), schedCheckedPath()} }

// resetSchedFiles removes the scheduler's files and forgets what they held
// (ResetVault: the next vault starts without the old one's schedule names,
// device names, messages and log paths, and without its last look).
func (a *App) resetSchedFiles() {
	s := &a.sched
	s.mu.Lock()
	s.checked, s.saved, s.savedOn, s.stateDirty = map[string]time.Time{}, nil, 0, false
	s.stateLoaded = true // nothing to read any more
	s.queue, s.skips, s.noted = nil, nil, nil
	s.mu.Unlock()
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.log, s.logLoaded = nil, true
	for _, p := range schedFiles() {
		_ = os.Remove(p)
		_ = os.Remove(p + ".tmp")
	}
}

// waitSchedRuns waits up to max for the scheduled batches' goroutines to
// end (the exit: execBatch has freed the batch slot, but the round's line,
// its passed-over firings and sched:done are written by the goroutine after
// that). It reports whether they all ended in time.
func (a *App) waitSchedRuns(max time.Duration) bool {
	done := make(chan struct{})
	go func() {
		a.sched.runs.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(max):
		return false
	}
}

func (a *App) schedLog(e SchedLogEntry) {
	e.At = time.Now().Format(time.RFC3339)
	a.writeSchedLog(e)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "sched:changed", nil)
	}
	a.schedNote(e)
}

// schedPublic is the part of a line that can be shown while the vault is
// locked: when and what kind, and the counts — not the schedule's name, the
// devices, the message or the log paths (the lock screen, the tray and
// whoever reads them must learn nothing of the vault).
func schedPublic(e SchedLogEntry) SchedLogEntry {
	return SchedLogEntry{At: e.At, Due: e.Due, ScheduleID: e.ScheduleID, Kind: e.Kind,
		OK: e.OK, Failed: e.Failed, Manual: e.Manual, Repeat: e.Repeat, Last: e.Last}
}

// schedProblem: a line of the 実行記録 that is not a run (missed, skipped,
// refused, error, stopped) — the ones a user has to be told, since nothing
// else on screen or in the tray says them.
func schedProblem(kind string) bool {
	switch kind {
	case "missed", "skipped", "refused", "error", "stopped":
		return true
	}
	return false
}

// schedNoteTitle is the tray balloon's title for a problem.
func schedNoteTitle(kind string) string {
	switch kind {
	case "missed":
		return winWord("予約を実行できませんでした（見逃し）", "A scheduled run was missed")
	case "skipped":
		return winWord("予約の回をスキップしました", "A scheduled run was skipped")
	case "refused":
		return winWord("予約を実行しませんでした（設定を変えるコマンド）", "A scheduled run was refused (commands that change the device)")
	case "stopped":
		return winWord("予約を停止しました", "A schedule was stopped")
	}
	return winWord("予約を実行できませんでした", "A scheduled run could not start")
}

// schedNote tells a problem line on screen (sched:problem: the page shows a
// toast) and, while the window is in the tray, in a balloon. The same
// problem of the same schedule is told once per schedNoteQuiet; a stop
// always. With the vault locked neither the page (the lock screen is
// showing) nor the balloon gets a name, a message or a path (schedPublic):
// only that a 予約 went wrong, and what kind. It reports whether it told.
func (a *App) schedNote(e SchedLogEntry) bool {
	if !schedProblem(e.Kind) {
		return false
	}
	s := &a.sched
	key := e.ScheduleID + "\x00" + e.Kind
	now := time.Now()
	s.mu.Lock()
	if last, ok := s.noted[key]; ok && e.Kind != "stopped" && now.Sub(last) < schedNoteQuiet {
		s.mu.Unlock()
		return false
	}
	if s.noted == nil {
		s.noted = map[string]time.Time{}
	}
	s.noted[key] = now
	s.mu.Unlock()
	locked := a.locked()
	if a.ctx != nil {
		ev := e
		if locked {
			ev = schedPublic(e)
		}
		runtime.EventsEmit(a.ctx, "sched:problem", ev)
	}
	if a.windowHidden() {
		text := winWord(fmt.Sprintf("「%s」%s", e.Schedule, e.Message),
			fmt.Sprintf("\"%s\" — see the schedule log (予約の実行記録)", e.Schedule))
		if locked {
			text = winWord("ロック解除後に「予約の実行記録」で確かめてください", "Unlock PalaTerm and see the schedule log (予約の実行記録)")
		}
		trayBalloon(schedNoteTitle(e.Kind), text)
	}
	return true
}

// loadSchedLogLocked reads the 実行記録 file into memory once (the caller
// holds logMu).
func (a *App) loadSchedLogLocked() {
	s := &a.sched
	if s.logLoaded {
		return
	}
	s.logLoaded = true
	var list []SchedLogEntry
	if readJSONFile(schedLogPath(), &list) {
		s.log = list
	}
}

func (a *App) writeSchedLog(e SchedLogEntry) {
	s := &a.sched
	s.logMu.Lock()
	defer s.logMu.Unlock()
	a.loadSchedLogLocked()
	s.log = append(s.log, e)
	if len(s.log) > schedLogMax {
		s.log = slices.Clone(s.log[len(s.log)-schedLogMax:]) // let the old array go
	}
	_ = writeJSONAtomic(schedLogPath(), s.log)
}

// ScheduleLog returns the 予約の実行記録, newest first (at most limit).
func (a *App) ScheduleLog(limit int) []SchedLogEntry {
	if a.locked() {
		return []SchedLogEntry{} // names devices and commands: after unlocking
	}
	return a.readSchedLog(limit)
}

// readSchedLog is ScheduleLog for the app's own use (locked or not).
func (a *App) readSchedLog(limit int) []SchedLogEntry {
	s := &a.sched
	s.logMu.Lock()
	defer s.logMu.Unlock()
	a.loadSchedLogLocked()
	out := []SchedLogEntry{}
	for i := len(s.log) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, s.log[i])
	}
	return out
}

// schedCopy is what a locked vault leaves the scheduler: an inventory built
// from a list of what a firing needs — the devices an enabled schedule
// targets (credentials filled in from their group, the own jump host put in
// front), the command sets, the settings, the pinned host keys and the
// schedules themselves. Nothing else is copied: the other devices, the group
// credentials, the own jump hosts, the table drafts, the GUI pins, the
// import rules — and whatever is added to the vault later, until it is put
// on this list on purpose. nil when nothing is scheduled.
func schedCopy(inv *model.Inventory) *model.Inventory {
	if inv == nil {
		return nil
	}
	var on []schedule.Schedule
	for _, sc := range inv.Schedules {
		if sc.Enabled {
			on = append(on, sc)
		}
	}
	if len(on) == 0 {
		return nil
	}
	// A deep copy to pick from: the vault's own slices and maps stay the
	// vault's (it is about to be dropped from memory, but a run in progress
	// still holds it).
	src := inv.Clone()
	var keep []model.Device
	for _, d := range src.Devices {
		for _, sc := range on {
			if d.Group != sc.Group || (sc.Ungrouped && d.Group != "") {
				continue
			}
			if sc.Names != nil && !slices.Contains(sc.Names, d.Name) {
				continue
			}
			r := src.ResolveCredentials(d)
			r.UseGroupCreds = false
			keep = append(keep, r)
			break
		}
	}
	// The groups by name only: the schedules and snapshotOf go by the name,
	// and the kept devices carry the group's credentials themselves.
	groups := make([]model.DeviceGroup, 0, len(src.DeviceGroups))
	for _, g := range src.DeviceGroups {
		groups = append(groups, model.DeviceGroup{Name: g.Name, Members: g.Members})
	}
	settings := src.Settings
	settings.LastKeyFile = ""
	return &model.Inventory{
		Version:       src.Version,
		SavedBy:       src.SavedBy,
		Devices:       keep,
		CommandSets:   src.CommandSets,
		DeviceGroups:  groups,
		Settings:      settings,
		KnownHostKeys: src.KnownHostKeys,
		Schedules:     src.Schedules,
	}
}

// stopSchedules disables the group's schedules that target any of names
// (every one of the group's when names is nil) and prepares the 実行記録
// lines saying why: a schedule left pointing at a deleted group or a moved
// device would run, unattended, on whatever later takes that name (a
// colleague's bundle read in under the same group name). The caller holds
// a.mu and logs the returned entries after releasing it.
func stopSchedules(inv *model.Inventory, group string, names map[string]bool, why string) []SchedLogEntry {
	var out []SchedLogEntry
	for i := range inv.Schedules {
		sc := &inv.Schedules[i]
		if !sc.Enabled || sc.Group != group {
			continue
		}
		if names != nil {
			hit := false
			for _, n := range sc.Names {
				if names[n] {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		sc.Enabled = false
		out = append(out, SchedLogEntry{ScheduleID: sc.ID, Schedule: sc.Name, Kind: "stopped", Message: why})
	}
	return out
}

// stopLeaving stops the schedules naming a device that is in before but not
// in after under the same group — deleted, or moved to another group (a
// rename within its group is followed, not stopped). Called with the
// schedules' names as they were before the change (before following any
// rename), under a.mu; the caller logs the lines after releasing it.
func stopLeaving(inv *model.Inventory, before, after []model.Device) []SchedLogEntry {
	now := map[string]string{} // ID → group after
	for _, d := range after {
		now[d.ID] = d.Group
	}
	type leave struct{ deleted, moved map[string]bool }
	byGroup := map[string]*leave{}
	var groups []string
	for _, d := range before {
		g, ok := now[d.ID]
		if ok && g == d.Group {
			continue
		}
		l := byGroup[d.Group]
		if l == nil {
			l = &leave{deleted: map[string]bool{}, moved: map[string]bool{}}
			byGroup[d.Group] = l
			groups = append(groups, d.Group)
		}
		if ok {
			l.moved[d.Name] = true
		} else {
			l.deleted[d.Name] = true
		}
	}
	slices.Sort(groups)
	var out []SchedLogEntry
	for _, g := range groups {
		l := byGroup[g]
		if len(l.deleted) > 0 {
			out = append(out, stopSchedules(inv, g, l.deleted, fmt.Sprintf("機器「%s」が削除されたため停止しました", strings.Join(slices.Sorted(maps.Keys(l.deleted)), ", ")))...)
		}
		if len(l.moved) > 0 {
			out = append(out, stopSchedules(inv, g, l.moved, fmt.Sprintf("機器「%s」が別のグループへ移動したため停止しました", strings.Join(slices.Sorted(maps.Keys(l.moved)), ", ")))...)
		}
	}
	return out
}

// logStopped writes stopSchedules' lines and wakes the scheduler.
func (a *App) logStopped(stopped []SchedLogEntry) {
	for _, e := range stopped {
		a.schedLog(e)
	}
	if len(stopped) > 0 {
		a.schedKick()
	}
}

// schedules returns the schedules in force: the vault's, or the locked copy's.
func (a *App) schedules() []schedule.Schedule {
	list, _ := a.schedulesIn()
	return list
}

// schedulesIn is schedules, and whether there is anything to read them from
// (false after a restart until the vault is unlocked: no schedules is then
// not known to mean none).
func (a *App) schedulesIn() ([]schedule.Schedule, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	src := a.inv
	if src == nil {
		src = a.schedInv
	}
	if src == nil {
		return nil, false
	}
	return append([]schedule.Schedule(nil), src.Schedules...), true
}

// SchedulesWaitUnlock: PalaTerm was started again and its vault not yet
// unlocked, and schedules were enabled when it last looked — they do not
// run until the unlock (the lock screen says so). The firings that come
// due meanwhile are recorded as missed at the unlock.
func (a *App) SchedulesWaitUnlock() bool {
	a.mu.Lock()
	idle := a.inv == nil && a.schedInv == nil
	a.mu.Unlock()
	if !idle {
		return false
	}
	s := &a.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadStateLocked()
	return s.savedOn > 0
}

// schedKeepAwake is the 予約 tab's 予約がある間はスリープしない (from the
// vault, or the locked copy).
func (a *App) schedKeepAwake() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv != nil {
		return a.inv.Settings.ScheduleKeepAwake
	}
	return a.schedInv != nil && a.schedInv.Settings.ScheduleKeepAwake
}

// SetScheduleKeepAwake turns 予約がある間はスリープしない on or off.
func (a *App) SetScheduleKeepAwake(on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return errVaultLocked
	}
	was := a.inv.Settings.ScheduleKeepAwake
	a.inv.Settings.ScheduleKeepAwake = on
	if err := a.saveVaultLocked(a.password); err != nil {
		a.inv.Settings.ScheduleKeepAwake = was
		return err
	}
	a.schedKick()
	return nil
}

// saveSchedulesLocked writes the vault after a change to its schedules; the
// caller holds a.mu and passes the schedules as they were. A write that
// fails (a vault saved by a newer PalaTerm is read-only) is undone in
// memory before the lock is released: the scheduler, which reads the
// schedules under a.mu, never sees a schedule the user was told was not
// saved — it would otherwise fire, unattended, after an error message that
// said nothing was kept. The callers arm the scheduler only after this
// succeeded.
func (a *App) saveSchedulesLocked(before []schedule.Schedule) error {
	if err := a.saveVaultLocked(a.password); err != nil {
		a.inv.Schedules = before
		return err
	}
	return nil
}

// hasPendingSchedules: an enabled schedule will still fire, or one is
// running or waiting its turn now (× then must not cut it off).
func (a *App) hasPendingSchedules() bool {
	a.sched.mu.Lock()
	busy := a.sched.runID != "" || len(a.sched.queue) > 0
	a.sched.mu.Unlock()
	return busy || !schedule.NextOf(a.schedules(), time.Now()).IsZero()
}

func (a *App) schedKick() {
	select {
	case a.sched.kick <- struct{}{}:
	default:
	}
}

// schedLoop is the scheduler (started at startup, ends with the process).
func (a *App) schedLoop() {
	t := time.NewTicker(schedTick)
	defer t.Stop()
	for {
		// A panic in one tick is logged and the loop goes on: the 予約 keep
		// running, and the app with them.
		func() {
			defer recoverGo("schedTick")
			a.schedTick(time.Now())
		}()
		select {
		case <-t.C:
		case <-a.sched.kick:
		}
	}
}

func (a *App) schedTick(now time.Time) {
	// The wall clock only: times carrying Go's monotonic reading compare by
	// it, and a clock set back (time sync) would go unseen below.
	now = now.Round(0)
	list, have := a.schedulesIn()
	locked := a.locked()
	s := &a.sched
	save, missed, busy := a.schedPlan(now, list, have)
	if save != nil {
		writeSchedState(s.statePath, *save)
	}
	for _, e := range missed {
		a.schedLog(e)
	}

	a.schedStartNext(now)

	// Keep the PC up ahead of the next firing and while jobs wait; arm the
	// wake-up timer for the one after.
	next := schedule.NextOf(list, now)
	hold := busy || (!next.IsZero() && (next.Sub(now) <= schedAwakeBefore || a.schedKeepAwake()))
	wakeAt := time.Time{}
	if !next.IsZero() {
		wakeAt = next.Add(-schedWakeLead)
	}
	a.schedHold(hold, wakeAt)
	st := armWake(wakeAt)
	s.mu.Lock()
	s.wake = st
	running, done, total := s.runName, s.runDone, s.runTotal
	s.mu.Unlock()
	trayTip(a.trayStatusText(list, now, running, done, total, locked))
}

// schedHold takes or releases the keep-awake and notes the wake-up time
// (under s.mu).
func (a *App) schedHold(hold bool, wakeAt time.Time) {
	s := &a.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	if hold && s.release == nil {
		s.release = keepSystemAwake()
	} else if !hold && s.release != nil {
		s.release()
		s.release = nil
	}
	s.wakeAt = wakeAt
}

// schedPlan is the tick's look at the clock, under s.mu (released however
// it ends: a panic here must not leave the mutex held, or the next tick and
// every 予約 binding would hang). It queues the firings that came due and
// returns the last-look file to write (nil: unchanged), the missed firings
// to record and whether a batch is queued or running.
func (a *App) schedPlan(now time.Time, list []schedule.Schedule, have bool) (save *schedCheckedFile, missed []SchedLogEntry, busy bool) {
	s := &a.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checked == nil {
		s.checked = map[string]time.Time{}
	}
	s.loadStateLocked()
	dirty := s.stateDirty
	seen := map[string]bool{}
	for _, sc := range list {
		seen[sc.ID] = true
		from, ok := s.checked[sc.ID]
		restart := false
		if !ok {
			// Its first look since PalaTerm started: from where the last
			// run of PalaTerm had looked (the firings in between — PalaTerm
			// not running, or waiting for the unlock — are missed ones).
			if from, ok = s.saved[sc.ID]; ok {
				restart = true
				delete(s.saved, sc.ID)
			}
			dirty = true
		}
		if ok && now.Before(from) {
			// The clock was set back (time sync): the firings up to from
			// were handled already, and running them again would repeat them.
			if restart {
				s.checked[sc.ID] = from
			}
			continue
		}
		s.checked[sc.ID] = now
		if !ok || !sc.Enabled {
			continue // first look: only firings from now on count
		}
		run, miss := sc.Plan(from, now)
		if !run.IsZero() || miss.N > 0 {
			// Written now: a firing handled must not come back as missed
			// after a restart.
			dirty = true
		}
		// One line for all the firings missed in one gap: a short interval
		// slept through overnight would otherwise be thousands of lines.
		switch {
		case miss.N == 1 && restart:
			missed = append(missed, SchedLogEntry{Due: miss.First.Format(time.RFC3339), ScheduleID: sc.ID, Schedule: sc.Name, Kind: "missed",
				Message: "PalaTerm の終了中・ロック解除前などで実行できなかった回です（さかのぼって実行はしません）"})
		case miss.N > 1 && restart:
			missed = append(missed, SchedLogEntry{Due: miss.First.Format(time.RFC3339), ScheduleID: sc.ID, Schedule: sc.Name, Kind: "missed",
				Message: fmt.Sprintf("PalaTerm の終了中・ロック解除前などで %d 回分（%s〜%s）実行できませんでした（さかのぼって実行はしません）",
					miss.N, miss.First.Format("01/02 15:04:05"), miss.Last.Format("01/02 15:04:05"))})
		case miss.N == 1:
			missed = append(missed, SchedLogEntry{Due: miss.First.Format(time.RFC3339), ScheduleID: sc.ID, Schedule: sc.Name, Kind: "missed",
				Message: "スリープ中などで実行できなかった回です（さかのぼって実行はしません）"})
		case miss.N > 1:
			missed = append(missed, SchedLogEntry{Due: miss.First.Format(time.RFC3339), ScheduleID: sc.ID, Schedule: sc.Name, Kind: "missed",
				Message: fmt.Sprintf("スリープ中などで %d 回分（%s〜%s）実行できませんでした（さかのぼって実行はしません）",
					miss.N, miss.First.Format("01/02 15:04:05"), miss.Last.Format("01/02 15:04:05"))})
		}
		if !run.IsZero() {
			// The previous firing of this schedule still waiting or running
			// (a short interval, a slow device): this one is not stacked
			// behind it.
			busySame := s.runID == sc.ID
			for _, j := range s.queue {
				busySame = busySame || j.sc.ID == sc.ID
			}
			if busySame {
				if s.skips == nil {
					s.skips = map[string]schedule.Span{}
				}
				sp := s.skips[sc.ID]
				if sp.N == 0 {
					sp.First = run
				}
				sp.N++
				sp.Last = run
				s.skips[sc.ID] = sp
			} else {
				s.queue = append(s.queue, schedJob{sc: sc, due: run})
			}
		}
	}
	// Nothing to read the schedules from (started again, not yet unlocked):
	// what is known of them stays as it is, here and in the file.
	if have {
		for id := range s.checked {
			if !seen[id] {
				delete(s.checked, id)
				dirty = true
			}
		}
		on := 0
		for _, sc := range list {
			if sc.Enabled {
				on++
			}
		}
		if on != s.savedOn {
			dirty = true
		}
		if dirty && s.statePath != "" {
			f := schedCheckedFile{Checked: maps.Clone(s.checked), Enabled: on}
			save = &f
			s.savedOn = on
		}
		s.stateDirty = false
	}
	busy = len(s.queue) > 0 || s.runID != ""
	return save, missed, busy
}

// trayStatusText is what the tray icon says: the schedule running now with
// its progress, else the next firing and whose it is, else that nothing is
// due. With the vault locked, no names: the progress and the time alone.
func (a *App) trayStatusText(list []schedule.Schedule, now time.Time, running string, done, total int, locked bool) string {
	if running != "" {
		if locked {
			return "PalaTerm — " + winWord(fmt.Sprintf("予約を実行中（%d / %d 台）", done, total), fmt.Sprintf("running a schedule (%d / %d devices)", done, total))
		}
		return "PalaTerm — " + winWord(fmt.Sprintf("実行中: %s（%d / %d 台）", running, done, total), fmt.Sprintf("running: %s (%d / %d devices)", running, done, total))
	}
	best, name := schedule.NextOfNamed(list, now)
	if best.IsZero() {
		return "PalaTerm — " + winWord("予約はありません", "no schedule due")
	}
	when := best.Format("01/02 15:04")
	if best.YearDay() == now.YearDay() && best.Year() == now.Year() {
		when = best.Format("15:04")
	}
	if locked {
		return "PalaTerm — " + winWord("次の実行 "+when, "next run "+when)
	}
	return "PalaTerm — " + winWord(fmt.Sprintf("次の実行 %s %s", when, name), fmt.Sprintf("next run %s %s", when, name))
}

// schedStartNext starts the first waiting job if no batch is running.
func (a *App) schedStartNext(now time.Time) {
	for {
		s := &a.sched
		s.mu.Lock()
		if len(s.queue) == 0 || s.runID != "" {
			s.mu.Unlock()
			return
		}
		job := s.queue[0]
		s.mu.Unlock()
		if !job.manual && now.Sub(job.due) > schedule.MaxLate {
			a.schedPop(job)
			a.schedLog(SchedLogEntry{Due: job.due.Format(time.RFC3339), ScheduleID: job.sc.ID, Schedule: job.sc.Name, Kind: "skipped",
				Message: fmt.Sprintf("ほかの一括実行が終わるのを待つうちに %d 分を過ぎたため実行しませんでした", int(schedule.MaxLate.Minutes()))})
			a.schedFlushSkips(job.sc.ID, job.sc.Name)
			continue
		}
		err := a.startScheduled(job)
		if errors.Is(err, errBatchBusy) {
			return // waits its turn
		}
		a.schedPop(job)
		if errors.Is(err, errSchedGone) {
			continue // deleted or stopped while it waited: nothing to record
		}
		if err != nil {
			a.schedFlushSkips(job.sc.ID, job.sc.Name)
			kind := "error"
			if errors.Is(err, errChangeCommands) {
				kind = "refused"
			}
			a.schedLog(SchedLogEntry{Due: job.due.Format(time.RFC3339), ScheduleID: job.sc.ID, Schedule: job.sc.Name, Kind: kind,
				Message: err.Error(), Manual: job.manual})
			continue
		}
		return
	}
}

func (a *App) schedPop(job schedJob) {
	s := &a.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, j := range s.queue {
		if j.sc.ID == job.sc.ID && j.due.Equal(job.due) {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return
		}
	}
}

// schedFlushSkips writes the firings of id passed over while its previous
// round ran (see schedState.skips) as one line.
func (a *App) schedFlushSkips(id, name string) {
	s := &a.sched
	s.mu.Lock()
	sp := s.skips[id]
	delete(s.skips, id)
	s.mu.Unlock()
	switch {
	case sp.N == 1:
		a.schedLog(SchedLogEntry{Due: sp.First.Format(time.RFC3339), ScheduleID: id, Schedule: name, Kind: "skipped",
			Message: "前の回の実行がまだ終わっていないため、この回は実行しませんでした"})
	case sp.N > 1:
		a.schedLog(SchedLogEntry{Due: sp.First.Format(time.RFC3339), ScheduleID: id, Schedule: name, Kind: "skipped",
			Message: fmt.Sprintf("前の回の実行がまだ終わっていないため、%d 回分（%s〜%s）は実行しませんでした",
				sp.N, sp.First.Format("01/02 15:04:05"), sp.Last.Format("01/02 15:04:05"))})
	}
}

var (
	errBatchBusy      = errors.New("batch busy")
	errChangeCommands = errors.New("change commands")
	errSchedGone      = errors.New("schedule deleted or stopped")
)

func schedRequest(sc schedule.Schedule) RunRequest {
	req := RunRequest{Names: sc.Names, Group: sc.Group, Ungrouped: sc.Ungrouped, Stage: sc.Stage, CommandSet: sc.CommandSet}
	// Typed commands go like the run tab's 今回だけ入力, without pauses: each
	// is sent once the prompt is back (the usual case is a short show list
	// repeated every few seconds).
	for _, c := range sc.Commands {
		if c = strings.TrimSpace(c); c != "" {
			req.AdHoc = append(req.AdHoc, model.Command{Text: c})
		}
	}
	return req
}

// changeCmdError is changeCommandsErr's error: its own message, and
// errors.Is(err, errChangeCommands).
type changeCmdError struct{ msg string }

func (e changeCmdError) Error() string        { return e.msg }
func (e changeCmdError) Is(target error) bool { return target == errChangeCommands }

// changeCommandsErr refuses a snapshot whose sets change the devices: a
// 予約 runs unattended, so it only collects (show …), never configures.
func changeCommandsErr(snap *model.Inventory) error {
	warns := dangerOf(snap)
	if len(warns) == 0 {
		return nil
	}
	var parts []string
	seen := map[string]bool{}
	for _, w := range warns {
		if seen[w.CommandSet] {
			continue
		}
		seen[w.CommandSet] = true
		var cmds []string
		for _, h := range w.Hits {
			cmds = append(cmds, h.Text)
		}
		parts = append(parts, fmt.Sprintf("「%s」: %s", w.CommandSet, strings.Join(cmds, " / ")))
	}
	return changeCmdError{fmt.Sprintf("設定を変更するコマンドを含むため予約では実行できません（%s）", strings.Join(parts, "、"))}
}

// startScheduled claims the batch slot for job and runs it in the
// background; errBatchBusy when another batch holds the slot.
func (a *App) startScheduled(job schedJob) error {
	a.mu.Lock()
	if a.runActive {
		a.mu.Unlock()
		return errBatchBusy
	}
	base := a.inv
	if base == nil {
		base = a.schedInv
	}
	if base == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault が開かれていないため実行できません")
	}
	// The schedule as it is now: deleted or stopped (止める) while this
	// firing waited, it does not run; edited, it runs as edited.
	cur := -1
	for i := range base.Schedules {
		if base.Schedules[i].ID == job.sc.ID {
			cur = i
		}
	}
	if cur < 0 || (!job.manual && !base.Schedules[cur].Enabled) {
		a.mu.Unlock()
		return errSchedGone
	}
	job.sc = base.Schedules[cur]
	req := schedRequest(job.sc)
	if !model.Stage(req.Stage).Valid() {
		a.mu.Unlock()
		return fmt.Errorf("作業タイミングの値が不正です: %q", req.Stage)
	}
	snap, err := snapshotOf(base, req)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	if err := changeCommandsErr(snap); err != nil {
		a.mu.Unlock()
		return err
	}
	if len(snap.Devices) == 0 {
		a.mu.Unlock()
		return fmt.Errorf("実行する機器がありません（グループ「%s」）", job.sc.Group)
	}
	missing := schedMissing(base, job.sc)
	total := 0
	for _, d := range snap.Devices {
		if d.Enabled {
			total++
		}
	}
	settings := base.Settings
	var ids []string
	for _, sc := range base.Schedules {
		ids = append(ids, sc.ID)
	}
	ctx, cancel := a.beginRunLocked(job.sc.Name)
	// Marked running under the same lock that claimed the batch: a 止める
	// or delete (which change the vault under a.mu first) either finds the
	// schedule gone above or finds it running here, never neither.
	s := &a.sched
	s.mu.Lock()
	s.runID, s.runName, s.runDone, s.runTotal = job.sc.ID, job.sc.Name, 0, total
	s.mu.Unlock()
	a.mu.Unlock()
	a.emitSched()
	dir := a.schedRunDir(job.sc, settings, ids)

	s.runs.Add(1)
	go func() {
		defer s.runs.Done()
		// However the goroutine ends (a panic after execBatch returned is
		// logged, not the end of the process and every terminal window
		// with it), the schedule is no longer the one running and the
		// scheduler takes the next job.
		defer func() {
			s.mu.Lock()
			if s.runID == job.sc.ID {
				s.runID, s.runName = "", ""
			}
			s.mu.Unlock()
			a.schedKick()
		}()
		defer recoverGo("schedRun")
		emit := func(ev runpkg.Event) {
			switch ev.Phase {
			case runpkg.PhaseDone, runpkg.PhaseError, runpkg.PhaseCanceled:
				s.mu.Lock()
				s.runDone++
				s.mu.Unlock()
				a.emitSched()
			}
		}
		done := a.execBatch(ctx, cancel, snap, req, job.sc.Name, dir, emit)
		ok, failed, canceled := 0, 0, false
		var logs []SchedLogFile
		for _, r := range done.Results {
			if r.LogPath != "" {
				logs = append(logs, SchedLogFile{Device: r.Device, Path: r.LogPath})
			}
			if r.Success {
				ok++
			} else {
				failed++
			}
			canceled = canceled || r.Canceled
		}
		s.mu.Lock()
		s.runID, s.runName = "", ""
		s.mu.Unlock()
		e := SchedLogEntry{Due: job.due.Format(time.RFC3339), ScheduleID: job.sc.ID, Schedule: job.sc.Name, Kind: "run",
			OK: ok, Failed: failed, RunDir: done.RunDir, Manual: job.manual, Logs: logs}
		for _, sl := range job.sc.Slots {
			e.Repeat = e.Repeat || sl.Kind == schedule.KindInterval
		}
		e.Last = job.sc.Next(time.Now()).IsZero()
		// Not ctx.Err(): execBatch cancels the context on its way out.
		if canceled {
			e.Message = "中止しました"
		}
		// The batch stopped on an internal error: an error entry (told like
		// any other), not a run of 0 OK / 0 failed.
		if done.Error != "" {
			e.Kind, e.Message = "error", done.Error
		}
		// The named devices no longer in the group were left out: said, not
		// dropped silently.
		if len(missing) > 0 {
			if e.Message != "" {
				e.Message += "。"
			}
			e.Message += "見つからない機器を外して実行しました: " + strings.Join(missing, ", ")
			e.Missing = missing
		}
		a.schedLog(e)
		a.schedFlushSkips(job.sc.ID, job.sc.Name)
		a.emitSched()
		// Locked: the page (its lock screen) and the balloon get the counts,
		// not the schedule's name, the devices left out or the paths.
		locked := a.locked()
		if a.ctx != nil {
			ev := e
			if locked {
				ev = schedPublic(e)
			}
			runtime.EventsEmit(a.ctx, "sched:done", ev)
		}
		// From the tray, a balloon — but a repeat every 10 seconds must not
		// pop one up every 10 seconds: only a failure, or its last round.
		if a.windowHidden() && (!e.Repeat || e.Last || failed > 0) {
			text := fmt.Sprintf(winWord("「%s」 成功 %d・失敗 %d", "\"%s\": %d ok, %d failed"), job.sc.Name, ok, failed)
			if locked {
				text = fmt.Sprintf(winWord("成功 %d・失敗 %d", "%d ok, %d failed"), ok, failed)
			}
			trayBalloon(winWord("予約の一括実行が終わりました", "Scheduled run finished"), text)
		}
	}()
	return nil
}

// schedMissing is the names a schedule lists that no device of its group
// has now (a GUI-only device is there: snapshotOf leaves it out on purpose).
func schedMissing(inv *model.Inventory, sc schedule.Schedule) []string {
	if sc.Names == nil {
		return nil
	}
	have := map[string]bool{}
	for _, d := range inv.Devices {
		if d.Group == sc.Group {
			have[d.Name] = true
		}
	}
	var out []string
	for _, n := range sc.Names {
		if !have[n] {
			out = append(out, n)
		}
	}
	return out
}

// schedDirsPath records, per schedule ID, the folder its rounds' logs go
// to (data/schedule-dirs.json; paths only, nothing secret).
func schedDirsPath() string { return filepath.Join(exeDir(), "data", "schedule-dirs.json") }

type schedDirRec struct {
	Dir   string `json:"dir"`
	Group string `json:"group"`
}

// schedLogStage is the stage a run's log file names are made with: for a
// 予約 (it has its own folder, dir) without a stage, the 予約 word set in
// 設定 (default "reserve") — not the plain capture's 指定なし word, which
// the first templates also wrote as a fixed "Config". Otherwise ""
// (the stage's own word).
func schedLogStage(dir, stage string) string {
	if dir != "" && stage == "" {
		return logstore.StageSchedule
	}
	return ""
}

// schedRunDir is the one folder a 予約 keeps its logs in, every round and
// 今すぐ実行 alike: the one it already has, or a new one named like a run
// folder with the 予約 word (設定; default "reserve") where the stage word
// goes (not the schedule's name:
// the made-up names are long, and the history row names the schedule). A new folder is made
// when the old one is gone (deleted by hand), sits outside the log folder
// now set, or was made for another group. ids are the schedules that exist:
// the records of deleted ones are dropped.
func (a *App) schedRunDir(sc schedule.Schedule, s model.Settings, ids []string) string {
	a.sched.logMu.Lock()
	defer a.sched.logMu.Unlock()
	recs := map[string]schedDirRec{}
	readJSONFile(schedDirsPath(), &recs)
	root := logstore.ResolveRoot(s.LogDir)
	if r, ok := recs[sc.ID]; ok && r.Group == sc.Group && strings.EqualFold(filepath.Dir(r.Dir), filepath.Clean(root)) {
		if fi, err := os.Stat(r.Dir); err == nil && fi.IsDir() {
			return r.Dir
		}
	}
	dir := logstore.ClaimRunDir(s.LogDir, s.LogDirTemplate, sc.Group, logstore.StageSchedule, s.StageTokens(), time.Now())
	for id := range recs {
		if !slices.Contains(ids, id) {
			delete(recs, id)
		}
	}
	recs[sc.ID] = schedDirRec{Dir: dir, Group: sc.Group}
	_ = writeJSONAtomic(schedDirsPath(), recs)
	return dir
}

func (a *App) emitSched() {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "sched:changed", nil)
	}
}

// ---- bound to the 予約 tab ----

// ScheduleView is one row of the 予約 tab.
type ScheduleView struct {
	schedule.Schedule
	Next string `json:"next"` // RFC3339, "" = never again
	// Remaining firings of a repeating (interval) schedule; -1 = until
	// stopped, or not a repeating schedule.
	Remaining int  `json:"remaining"`
	Running   bool `json:"running"` // its batch is running now
	Queued    bool `json:"queued"`  // waiting for another batch to end
	// LastProblem is its newest 実行記録 line when that one went wrong (a
	// run with failures or devices left out, missed, skipped, refused,
	// error, stopped): the list marks it. nil when the newest was fine.
	LastProblem *SchedLogEntry `json:"lastProblem,omitempty"`
}

// schedLastProblems: per schedule ID, its newest line if that is a problem
// (log is newest first).
func schedLastProblems(log []SchedLogEntry) map[string]*SchedLogEntry {
	out := map[string]*SchedLogEntry{}
	seen := map[string]bool{}
	for i := range log {
		e := &log[i]
		if seen[e.ScheduleID] {
			continue
		}
		seen[e.ScheduleID] = true
		if schedProblem(e.Kind) || (e.Kind == "run" && (e.Failed > 0 || len(e.Missing) > 0)) {
			c := *e
			c.Logs = nil // the list only says what happened
			out[e.ScheduleID] = &c
		}
	}
	return out
}

// ScheduleStatus is the scheduler's state for the 予約 tab. While the vault
// is locked the names (Running, NextName) are left out: the page is on its
// lock screen, and the status is readable without the master password.
type ScheduleStatus struct {
	Running  string `json:"running"` // the 予約 running now ("" when locked)
	Done     int    `json:"done"`
	Total    int    `json:"total"`
	Queued   int    `json:"queued"`
	Wake     string `json:"wake"`   // armWake status: ok / unsupported / none / error text
	WakeAt   string `json:"wakeAt"` // RFC3339
	Locked   bool   `json:"locked"`
	HasCopy  bool   `json:"hasCopy"` // locked with schedules kept in memory
	Next     string `json:"next"`
	NextName string `json:"nextName"` // "" when locked
	// Power: can this PC be woken for a 予約 (see readPowerInfo).
	Power     PowerInfo `json:"power"`
	KeepAwake bool      `json:"keepAwake"` // 予約がある間はスリープしない
}

// ListSchedules returns the vault's schedules with their next firing.
func (a *App) ListSchedules() []ScheduleView {
	if a.locked() {
		return []ScheduleView{}
	}
	now := time.Now()
	a.sched.mu.Lock()
	runID := a.sched.runID
	queued := map[string]bool{}
	for _, j := range a.sched.queue {
		queued[j.sc.ID] = true
	}
	a.sched.mu.Unlock()
	out := []ScheduleView{}
	last := schedLastProblems(a.readSchedLog(0))
	for _, sc := range a.schedules() {
		v := ScheduleView{Schedule: sc, Remaining: -1, Running: sc.ID == runID, Queued: queued[sc.ID], LastProblem: last[sc.ID]}
		if n := sc.Next(now); !n.IsZero() {
			v.Next = n.Format(time.RFC3339)
		}
		if len(sc.Slots) == 1 && sc.Slots[0].Kind == schedule.KindInterval {
			v.Remaining = sc.Slots[0].Remaining(now)
		}
		out = append(out, v)
	}
	return out
}

// GetPowerInfo reads this PC's sleep and lid settings (read only: nothing
// is changed). The run tab uses it to say whether closing the lid will stop
// the batch that is running.
func (a *App) GetPowerInfo() PowerInfo { return cachedPowerInfo() }

// GetScheduleStatus reports what the scheduler is doing.
func (a *App) GetScheduleStatus() ScheduleStatus {
	now := time.Now()
	list := a.schedules()
	a.mu.Lock()
	st := ScheduleStatus{Locked: a.inv == nil, HasCopy: a.inv == nil && a.schedInv != nil}
	a.mu.Unlock()
	s := &a.sched
	s.mu.Lock()
	st.Running, st.Done, st.Total, st.Queued, st.Wake = s.runName, s.runDone, s.runTotal, len(s.queue), s.wake
	if !s.wakeAt.IsZero() {
		st.WakeAt = s.wakeAt.Format(time.RFC3339)
	}
	s.mu.Unlock()
	best, name := schedule.NextOfNamed(list, now)
	if !best.IsZero() {
		st.Next, st.NextName = best.Format(time.RFC3339), name
	}
	if st.Locked {
		st.Running, st.NextName = "", ""
	}
	st.Power = cachedPowerInfo()
	st.KeepAwake = a.schedKeepAwake()
	return st
}

// SaveSchedule adds or replaces a schedule (by ID; a new one gets an ID).
// It refuses one whose commands would change the devices, checked against
// the run it would make right now (it is checked again at every firing).
func (a *App) SaveSchedule(sc schedule.Schedule) (string, error) {
	sc.Name = strings.TrimSpace(sc.Name)
	for i := range sc.Slots {
		sc.Slots[i].Weekdays = schedule.SortedWeekdays(sc.Slots[i].Weekdays)
		if sc.Slots[i].Kind != schedule.KindWeekly {
			sc.Slots[i].Weekdays = nil
		}
	}
	if err := sc.Validate(time.Now()); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	req := schedRequest(sc)
	if !model.Stage(req.Stage).Valid() {
		a.mu.Unlock()
		return "", fmt.Errorf("作業タイミングの値が不正です: %q", req.Stage)
	}
	snap, err := snapshotOf(a.inv, req)
	if err != nil {
		a.mu.Unlock()
		return "", err
	}
	if err := changeCommandsErr(snap); err != nil {
		a.mu.Unlock()
		return "", err
	}
	if sc.ID == "" {
		sc.ID = schedule.NewID()
	}
	before := slices.Clone(a.inv.Schedules)
	found := false
	for i := range a.inv.Schedules {
		if a.inv.Schedules[i].ID == sc.ID {
			a.inv.Schedules[i] = sc
			found = true
			break
		}
	}
	if !found {
		a.inv.Schedules = append(a.inv.Schedules, sc)
	}
	// Written before the scheduler is armed, still under a.mu: a schedule
	// the vault refused is not the vault's, and never fires.
	if err := a.saveSchedulesLocked(before); err != nil {
		a.mu.Unlock()
		return "", err
	}
	// Edited: its firings count from now (not from the old rule's last
	// look) — set before the scheduler can read the new rule (a.mu still
	// held; it takes s.mu after a.mu, as startScheduled does).
	a.sched.mu.Lock()
	if a.sched.checked == nil {
		a.sched.checked = map[string]time.Time{}
	}
	a.sched.checked[sc.ID] = time.Now().Round(0) // wall clock (see schedTick)
	a.sched.stateDirty = true
	a.sched.mu.Unlock()
	a.mu.Unlock()
	a.schedKick()
	return sc.ID, nil
}

// SetScheduleEnabled turns one schedule on or off.
func (a *App) SetScheduleEnabled(id string, on bool) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	var sc *schedule.Schedule
	for i := range a.inv.Schedules {
		if a.inv.Schedules[i].ID == id {
			sc = &a.inv.Schedules[i]
		}
	}
	if sc == nil {
		a.mu.Unlock()
		return fmt.Errorf("予約が見つかりません")
	}
	copy := *sc
	a.mu.Unlock()
	copy.Enabled = on
	if on {
		_, err := a.SaveSchedule(copy) // re-checked like a new one
		return err
	}
	a.mu.Lock()
	if a.inv == nil { // locked in between
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	before := slices.Clone(a.inv.Schedules)
	for i := range a.inv.Schedules {
		if a.inv.Schedules[i].ID == id {
			a.inv.Schedules[i].Enabled = false
		}
	}
	// Saved first: a stop the vault refused is told, not done by halves (in
	// memory until the restart, enabled in the file after it).
	if err := a.saveSchedulesLocked(before); err != nil {
		a.mu.Unlock()
		return err
	}
	a.mu.Unlock()
	a.stopScheduleRun(id)
	a.schedKick()
	return nil
}

// stopScheduleRun drops a stopped or deleted schedule's waiting firings and
// stops its batch if it is the one running (止める means now, not after
// this round).
func (a *App) stopScheduleRun(id string) {
	a.sched.mu.Lock()
	q := a.sched.queue[:0]
	for _, j := range a.sched.queue {
		if j.sc.ID != id {
			q = append(q, j)
		}
	}
	a.sched.queue = q
	running := a.sched.runID == id
	a.sched.mu.Unlock()
	if running {
		a.CancelScheduledRun() // its end writes the passed-over firings
	} else {
		a.schedFlushSkips(id, a.schedNameOf(id))
	}
}

// schedNameOf is the name a schedule last had in the 実行記録 (for a line
// written after it was deleted).
func (a *App) schedNameOf(id string) string {
	for _, e := range a.readSchedLog(0) {
		if e.ScheduleID == id {
			return e.Schedule
		}
	}
	return ""
}

// DeleteFinishedSchedules removes the schedules that will not fire again
// (a 1回だけ that has run, a repeat whose count or end time is behind) and
// returns how many.
func (a *App) DeleteFinishedSchedules() (int, error) {
	now := time.Now()
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return 0, fmt.Errorf("vault is locked")
	}
	before := slices.Clone(a.inv.Schedules)
	keep := a.inv.Schedules[:0]
	gone := map[string]string{} // ID → name
	for _, sc := range a.inv.Schedules {
		on := sc
		on.Enabled = true // a stopped schedule that could still fire is not finished
		if on.Next(now).IsZero() {
			gone[sc.ID] = sc.Name
			continue
		}
		keep = append(keep, sc)
	}
	if len(gone) == 0 {
		a.mu.Unlock()
		return 0, nil
	}
	a.inv.Schedules = keep
	if err := a.saveSchedulesLocked(before); err != nil {
		a.mu.Unlock()
		return 0, err
	}
	a.mu.Unlock()
	// A 1回だけ still waiting its turn (or a 今すぐ実行) goes with it; one
	// running now finishes (it is not 止める). The firings passed over while
	// a round of it ran are written now, under the name it had: the entry
	// would otherwise outlive the schedule and never be written.
	a.sched.mu.Lock()
	q := a.sched.queue[:0]
	for _, j := range a.sched.queue {
		if _, ok := gone[j.sc.ID]; !ok {
			q = append(q, j)
		}
	}
	a.sched.queue = q
	a.sched.mu.Unlock()
	for _, id := range slices.Sorted(maps.Keys(gone)) {
		a.schedFlushSkips(id, gone[id])
	}
	a.schedKick()
	return len(gone), nil
}

// DeleteSchedule removes a schedule.
func (a *App) DeleteSchedule(id string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	before := slices.Clone(a.inv.Schedules)
	out := a.inv.Schedules[:0]
	for _, sc := range a.inv.Schedules {
		if sc.ID != id {
			out = append(out, sc)
		}
	}
	a.inv.Schedules = out
	if err := a.saveSchedulesLocked(before); err != nil {
		a.mu.Unlock()
		return err
	}
	a.mu.Unlock()
	a.stopScheduleRun(id)
	a.schedKick()
	return nil
}

// RunScheduleNow queues one schedule to run at once (今すぐ実行), whatever
// its slots say; it waits its turn like any firing.
func (a *App) RunScheduleNow(id string) error {
	// A click, not a firing: it needs the vault open like every other
	// action from the page (the lock screen is not a way to start a run).
	a.mu.Lock()
	locked := a.inv == nil
	a.mu.Unlock()
	if locked {
		return fmt.Errorf("vault is locked")
	}
	var job *schedJob
	for _, sc := range a.schedules() {
		if sc.ID == id {
			job = &schedJob{sc: sc, due: time.Now(), manual: true}
		}
	}
	if job == nil {
		return fmt.Errorf("予約が見つかりません")
	}
	a.sched.mu.Lock()
	a.sched.queue = append(a.sched.queue, *job)
	a.sched.mu.Unlock()
	a.schedKick()
	return nil
}

// CancelScheduledRun stops the scheduled batch running now (中止).
func (a *App) CancelScheduledRun() {
	a.mu.Lock()
	c := a.runCancel
	sched := a.runSched
	a.mu.Unlock()
	if sched != "" && c != nil {
		c()
	}
}

// ---- tray (× while schedules are pending) ----

// hideToTray hides the main window into the notification area.
func (a *App) hideToTray() {
	runtime.WindowHide(a.ctx)
	a.sched.mu.Lock()
	first := !a.sched.notified
	a.sched.notified = true
	a.sched.mu.Unlock()
	msg := ""
	if first {
		msg = winWord("予約を実行するため、タスクトレイで動作を続けます。終了するにはトレイのアイコンを右クリック →「終了」。",
			"PalaTerm keeps running in the tray to run the schedules. To quit, right-click the tray icon → Quit.")
	}
	trayShow(a.showFromTray, a.quitFromTray, "PalaTerm", msg)
}

// windowHidden: the main window is not on screen (in the tray). Asked of
// Windows, so a window shown by a second start of PalaTerm counts as shown.
func (a *App) windowHidden() bool { return ownTopLevelWindow() == 0 }

func (a *App) showFromTray() {
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
	focusOwnWindow()
}

// quitFromTray is the tray menu's 終了: the normal close, minus the hiding.
func (a *App) quitFromTray() {
	a.mu.Lock()
	a.trayQuit = true
	a.mu.Unlock()
	runtime.WindowShow(a.ctx)
	runtime.Quit(a.ctx)
}

// ---- keeping schedules pointed at renamed things ----

// renameSchedDevice follows a device renamed within its group in the
// schedules that name it (fixed device lists).
func renameSchedDevice(inv *model.Inventory, group, oldName, newName string) {
	for i := range inv.Schedules {
		sc := &inv.Schedules[i]
		if sc.Group != group || sc.Names == nil {
			continue
		}
		names := append([]string(nil), sc.Names...) // copies handed out share the old slice
		for j, n := range names {
			if n == oldName {
				names[j] = newName
			}
		}
		sc.Names = names
	}
}

// renameSchedGroup follows a renamed group.
func renameSchedGroup(inv *model.Inventory, oldName, newName string) {
	for i := range inv.Schedules {
		if !inv.Schedules[i].Ungrouped && inv.Schedules[i].Group == oldName {
			inv.Schedules[i].Group = newName
		}
	}
}

// renameSchedSet follows a renamed command set.
func renameSchedSet(inv *model.Inventory, oldName, newName string) {
	for i := range inv.Schedules {
		if inv.Schedules[i].CommandSet == oldName {
			inv.Schedules[i].CommandSet = newName
		}
	}
}

// stopSchedSet stops the enabled schedules set to run a deleted command set
// and prepares the 実行記録 lines saying why (as stopSchedules does for a
// deleted group). Called under a.mu; the caller logs the lines after
// releasing it.
func stopSchedSet(inv *model.Inventory, name string) []SchedLogEntry {
	var out []SchedLogEntry
	for i := range inv.Schedules {
		sc := &inv.Schedules[i]
		if !sc.Enabled || sc.CommandSet != name {
			continue
		}
		sc.Enabled = false
		out = append(out, SchedLogEntry{ScheduleID: sc.ID, Schedule: sc.Name, Kind: "stopped",
			Message: fmt.Sprintf("コマンドセット「%s」が削除されたため停止しました", name)})
	}
	return out
}
