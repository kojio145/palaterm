package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	// commands in the set), error (could not start).
	Kind    string `json:"kind"`
	OK      int    `json:"ok,omitempty"`
	Failed  int    `json:"failed,omitempty"`
	RunDir  string `json:"runDir,omitempty"`
	Message string `json:"message,omitempty"`
	Manual  bool   `json:"manual,omitempty"` // 今すぐ実行
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
}

func schedLogPath() string { return filepath.Join(exeDir(), "data", "schedule-log.json") }

func (a *App) schedLog(e SchedLogEntry) {
	e.At = time.Now().Format(time.RFC3339)
	a.sched.logMu.Lock()
	defer a.sched.logMu.Unlock()
	var list []SchedLogEntry
	if b, err := os.ReadFile(schedLogPath()); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	list = append(list, e)
	if len(list) > schedLogMax {
		list = list[len(list)-schedLogMax:]
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	tmp := schedLogPath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, schedLogPath())
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "sched:changed", nil)
	}
}

// ScheduleLog returns the 予約の実行記録, newest first (at most limit).
func (a *App) ScheduleLog(limit int) []SchedLogEntry {
	a.sched.logMu.Lock()
	defer a.sched.logMu.Unlock()
	var list []SchedLogEntry
	if b, err := os.ReadFile(schedLogPath()); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	out := []SchedLogEntry{}
	for i := len(list) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, list[i])
	}
	return out
}

// schedCopy is what a locked vault leaves the scheduler: the inventory with
// only the devices an enabled schedule targets (credentials filled in from
// their group), the command sets, settings and host keys — no other device,
// no group credentials. nil when nothing is scheduled.
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
	c := inv.Clone()
	var keep []model.Device
	for _, d := range c.Devices {
		for _, sc := range on {
			if d.Group != sc.Group || (sc.Ungrouped && d.Group != "") {
				continue
			}
			if sc.Names != nil && !containsStr(sc.Names, d.Name) {
				continue
			}
			r := c.ResolveCredentials(d)
			r.UseGroupCreds = false
			keep = append(keep, r)
			break
		}
	}
	c.Devices = keep
	for i := range c.DeviceGroups {
		g := &c.DeviceGroups[i]
		g.Username, g.Password, g.EnablePassword = "", "", ""
	}
	c.Settings.LastKeyFile = ""
	return c
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// schedules returns the schedules in force: the vault's, or the locked copy's.
func (a *App) schedules() []schedule.Schedule {
	a.mu.Lock()
	defer a.mu.Unlock()
	src := a.inv
	if src == nil {
		src = a.schedInv
	}
	if src == nil {
		return nil
	}
	return append([]schedule.Schedule(nil), src.Schedules...)
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
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	a.inv.Settings.ScheduleKeepAwake = on
	a.mu.Unlock()
	a.schedKick()
	return a.persist()
}

// hasPendingSchedules: an enabled schedule will still fire.
func (a *App) hasPendingSchedules() bool {
	return !schedule.NextOf(a.schedules(), time.Now()).IsZero()
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
		a.schedTick(time.Now())
		select {
		case <-t.C:
		case <-a.sched.kick:
		}
	}
}

func (a *App) schedTick(now time.Time) {
	list := a.schedules()
	s := &a.sched
	s.mu.Lock()
	if s.checked == nil {
		s.checked = map[string]time.Time{}
	}
	seen := map[string]bool{}
	var missed []SchedLogEntry
	for _, sc := range list {
		seen[sc.ID] = true
		from, ok := s.checked[sc.ID]
		s.checked[sc.ID] = now
		if !ok || !sc.Enabled {
			continue // first look: only firings from now on count
		}
		run, miss := sc.Plan(from, now)
		// One line for all the firings missed in one gap: a short interval
		// slept through overnight would otherwise be thousands of lines.
		switch {
		case len(miss) == 1:
			missed = append(missed, SchedLogEntry{Due: miss[0].Format(time.RFC3339), ScheduleID: sc.ID, Schedule: sc.Name, Kind: "missed",
				Message: "スリープ中などで実行できなかった回です（さかのぼって実行はしません）"})
		case len(miss) > 1:
			missed = append(missed, SchedLogEntry{Due: miss[0].Format(time.RFC3339), ScheduleID: sc.ID, Schedule: sc.Name, Kind: "missed",
				Message: fmt.Sprintf("スリープ中などで %d 回分（%s〜%s）実行できませんでした（さかのぼって実行はしません）",
					len(miss), miss[0].Format("01/02 15:04:05"), miss[len(miss)-1].Format("01/02 15:04:05"))})
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
				missed = append(missed, SchedLogEntry{Due: run.Format(time.RFC3339), ScheduleID: sc.ID, Schedule: sc.Name, Kind: "skipped",
					Message: "前の回の実行がまだ終わっていないため、この回は実行しませんでした"})
			} else {
				s.queue = append(s.queue, schedJob{sc: sc, due: run})
			}
		}
	}
	for id := range s.checked {
		if !seen[id] {
			delete(s.checked, id)
		}
	}
	busy := len(s.queue) > 0 || s.runID != ""
	s.mu.Unlock()
	for _, e := range missed {
		a.schedLog(e)
	}

	a.schedStartNext(now)

	// Keep the PC up ahead of the next firing and while jobs wait; arm the
	// wake-up timer for the one after.
	next := schedule.NextOf(list, now)
	hold := busy || (!next.IsZero() && (next.Sub(now) <= schedAwakeBefore || a.schedKeepAwake()))
	s.mu.Lock()
	if hold && s.release == nil {
		s.release = keepSystemAwake()
	} else if !hold && s.release != nil {
		s.release()
		s.release = nil
	}
	wakeAt := time.Time{}
	if !next.IsZero() {
		wakeAt = next.Add(-schedWakeLead)
	}
	s.wakeAt = wakeAt
	s.mu.Unlock()
	st := armWake(wakeAt)
	s.mu.Lock()
	s.wake = st
	s.mu.Unlock()
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
			continue
		}
		err := a.startScheduled(job)
		if errors.Is(err, errBatchBusy) {
			return // waits its turn
		}
		a.schedPop(job)
		if err != nil {
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

var (
	errBatchBusy      = errors.New("batch busy")
	errChangeCommands = errors.New("change commands")
)

func schedRequest(sc schedule.Schedule) RunRequest {
	return RunRequest{Names: sc.Names, Group: sc.Group, Ungrouped: sc.Ungrouped, Stage: sc.Stage, CommandSet: sc.CommandSet}
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
	ctx, cancel := a.beginRunLocked(job.sc.Name)
	a.mu.Unlock()

	total := 0
	for _, d := range snap.Devices {
		if d.Enabled {
			total++
		}
	}
	s := &a.sched
	s.mu.Lock()
	s.runID, s.runName, s.runDone, s.runTotal = job.sc.ID, job.sc.Name, 0, total
	s.mu.Unlock()
	a.emitSched()

	go func() {
		emit := func(ev runpkg.Event) {
			switch ev.Phase {
			case runpkg.PhaseDone, runpkg.PhaseError, runpkg.PhaseCanceled:
				s.mu.Lock()
				s.runDone++
				s.mu.Unlock()
				a.emitSched()
			}
		}
		done := a.execBatch(ctx, cancel, snap, req, job.sc.Name, emit)
		ok, failed, canceled := 0, 0, false
		for _, r := range done.Results {
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
			OK: ok, Failed: failed, RunDir: done.RunDir, Manual: job.manual}
		// Not ctx.Err(): execBatch cancels the context on its way out.
		if canceled {
			e.Message = "中止しました"
		}
		a.schedLog(e)
		a.emitSched()
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "sched:done", e)
		}
		trayBalloon(winWord("予約の一括実行が終わりました", "Scheduled run finished"),
			fmt.Sprintf(winWord("「%s」 成功 %d・失敗 %d", "\"%s\": %d ok, %d failed"), job.sc.Name, ok, failed))
		a.schedKick()
	}()
	return nil
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
}

// ScheduleStatus is the scheduler's state for the 予約 tab.
type ScheduleStatus struct {
	Running  string `json:"running"` // the 予約 running now
	Done     int    `json:"done"`
	Total    int    `json:"total"`
	Queued   int    `json:"queued"`
	Wake     string `json:"wake"`   // armWake status: ok / unsupported / none / error text
	WakeAt   string `json:"wakeAt"` // RFC3339
	Locked   bool   `json:"locked"`
	HasCopy  bool   `json:"hasCopy"` // locked with schedules kept in memory
	Next     string `json:"next"`
	NextName string `json:"nextName"`
	// Power: can this PC be woken for a 予約 (see readPowerInfo).
	Power     PowerInfo `json:"power"`
	KeepAwake bool      `json:"keepAwake"` // 予約がある間はスリープしない
}

// ListSchedules returns the vault's schedules with their next firing.
func (a *App) ListSchedules() []ScheduleView {
	now := time.Now()
	out := []ScheduleView{}
	for _, sc := range a.schedules() {
		v := ScheduleView{Schedule: sc}
		if n := sc.Next(now); !n.IsZero() {
			v.Next = n.Format(time.RFC3339)
		}
		out = append(out, v)
	}
	return out
}

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
	var best time.Time
	for _, sc := range list {
		if n := sc.Next(now); !n.IsZero() && (best.IsZero() || n.Before(best)) {
			best, st.NextName = n, sc.Name
		}
	}
	if !best.IsZero() {
		st.Next = best.Format(time.RFC3339)
	}
	st.Power = readPowerInfo()
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
	a.mu.Unlock()
	// Edited: its firings count from now (not from the old rule's last look).
	a.sched.mu.Lock()
	if a.sched.checked == nil {
		a.sched.checked = map[string]time.Time{}
	}
	a.sched.checked[sc.ID] = time.Now()
	a.sched.mu.Unlock()
	if err := a.persist(); err != nil {
		return "", err
	}
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
	for i := range a.inv.Schedules {
		if a.inv.Schedules[i].ID == id {
			a.inv.Schedules[i].Enabled = false
		}
	}
	a.mu.Unlock()
	a.schedKick()
	return a.persist()
}

// DeleteSchedule removes a schedule.
func (a *App) DeleteSchedule(id string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	out := a.inv.Schedules[:0]
	for _, sc := range a.inv.Schedules {
		if sc.ID != id {
			out = append(out, sc)
		}
	}
	a.inv.Schedules = out
	a.mu.Unlock()
	a.schedKick()
	return a.persist()
}

// RunScheduleNow queues one schedule to run at once (今すぐ実行), whatever
// its slots say; it waits its turn like any firing.
func (a *App) RunScheduleNow(id string) error {
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
		for j, n := range sc.Names {
			if n == oldName {
				sc.Names[j] = newName
			}
		}
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
