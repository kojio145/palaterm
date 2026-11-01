package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/schedule"
)

// 2026-11-01 review E (schedules, GUI windows). Lab values only.

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// E S-1: a refused schedule names the set, the line and the kind of change
// — never the command itself (it may carry a password, and the message goes
// to data\schedule-log.json, the tray and a toast).
func TestRefusedMessageHasNoCommandText(t *testing.T) {
	snap := &model.Inventory{
		Devices: []model.Device{{Name: "lab-sw1", Group: "lab", Host: "192.0.2.10", Conn: model.ConnSSH, Enabled: true, CommandSet: "lab-set"}},
		CommandSets: []model.CommandSet{{Name: "lab-set", Commands: []model.Command{
			{Text: "show version"}, {Text: "configure terminal"}, {Text: "username ops password Lab-Pass-1"}}}},
	}
	err := changeCommandsErr(snap)
	if err == nil {
		t.Fatal("a set with change commands must be refused")
	}
	msg := err.Error()
	for _, secret := range []string{"Lab-Pass-1", "configure terminal", "username ops"} {
		if strings.Contains(msg, secret) {
			t.Errorf("message carries the command text %q: %s", secret, msg)
		}
	}
	if !strings.Contains(msg, "「lab-set」") || !strings.Contains(msg, "2 行目") {
		t.Errorf("message should name the set and the line: %s", msg)
	}
}

// E B-9: the locked copy holds the command sets the enabled schedules and
// their devices name, and the enabled schedules only.
func TestSchedCopyLimitedToWhatFires(t *testing.T) {
	inv := schedInventory()
	inv.CommandSets = append(inv.CommandSets, model.CommandSet{Name: "secret-set", Commands: []model.Command{{Text: "show run"}}})
	inv.Schedules = []schedule.Schedule{
		{ID: "1", Enabled: true, Group: "A社", Names: []string{"a1"}},
		{ID: "2", Enabled: false, Group: "B社", CommandSet: "secret-set"},
	}
	c := schedCopy(inv)
	var sets []string
	for _, cs := range c.CommandSets {
		sets = append(sets, cs.Name)
	}
	if strings.Join(sets, ",") != "show" {
		t.Errorf("kept sets = %v, want show only (a1's own set)", sets)
	}
	if len(c.Schedules) != 1 || c.Schedules[0].ID != "1" {
		t.Errorf("kept schedules = %+v, want the enabled one only", c.Schedules)
	}
}

// A A-3: deleting a set named "" (各機器のセット is "", not a set) stops nothing.
func TestStopSchedSetEmptyNameIsNoop(t *testing.T) {
	inv := schedInventory()
	inv.Schedules = []schedule.Schedule{{ID: "1", Enabled: true, Group: "A社"}}
	if out := stopSchedSet(inv, ""); len(out) != 0 || !inv.Schedules[0].Enabled {
		t.Errorf("an empty set name stopped schedules: %+v / %+v", out, inv.Schedules)
	}
}

// E A-7: 今すぐ実行 does not queue a schedule that is running or waiting.
func TestRunScheduleNowRefusesDuplicate(t *testing.T) {
	a := NewApp()
	a.inv = schedInventory()
	a.inv.Schedules = []schedule.Schedule{{ID: "1", Name: "n", Enabled: true, Group: "A社"}}
	if err := a.RunScheduleNow("1"); err != nil {
		t.Fatal(err)
	}
	if err := a.RunScheduleNow("1"); err == nil {
		t.Error("the same schedule queued twice")
	}
	a.sched.mu.Lock()
	n := len(a.sched.queue)
	a.sched.queue = nil
	a.sched.runID = "1"
	a.sched.mu.Unlock()
	if n != 1 {
		t.Errorf("queue = %d, want 1", n)
	}
	if err := a.RunScheduleNow("1"); err == nil {
		t.Error("queued while its round runs")
	}
}

// E A-6 / A A-6: the cancel is for one schedule's round, checked under both
// locks; nothing from the lock screen.
func TestCancelScheduledRunByID(t *testing.T) {
	a := NewApp()
	called := 0
	a.runCancel = func() { called++ }
	a.runSched = "n"
	a.sched.runID = "1"
	if a.cancelScheduledRun("2") || called != 0 {
		t.Error("another schedule's round was cancelled")
	}
	if a.cancelScheduledRun("") || called != 0 {
		t.Error("an empty ID cancelled a round")
	}
	if !a.cancelScheduledRun("1") || called != 1 {
		t.Error("the schedule's own round was not cancelled")
	}
	if err := a.CancelScheduledRun("1"); err != errVaultLocked {
		t.Errorf("locked: err = %v, want errVaultLocked", err)
	}
	if called != 1 {
		t.Error("a cancel went through from the lock screen")
	}
}

// E A-4: a bundle read in under a group with enabled schedules stops the
// whole-group ones and tells the fixed-list ones whose device changed
// address; other groups are untouched.
func TestSchedImportChanged(t *testing.T) {
	inv := schedInventory()
	before := append([]model.Device(nil), inv.Devices...)
	inv.Schedules = []schedule.Schedule{
		{ID: "all", Name: "all", Enabled: true, Group: "A社"},
		{ID: "fixed", Name: "fixed", Enabled: true, Group: "A社", Names: []string{"a1"}},
		{ID: "fixed2", Name: "fixed2", Enabled: true, Group: "A社", Names: []string{"a2"}},
		{ID: "b", Name: "b", Enabled: true, Group: "B社"},
	}
	inv.EnsureDeviceIDs()
	before = append([]model.Device(nil), inv.Devices...)
	// As bundle.Apply does: a1 replaced with another address, a new a9 added.
	inv.Devices[0].Host = "192.0.2.50"
	inv.Devices = append(inv.Devices, model.Device{Name: "a9", Group: "A社", Host: "192.0.2.59", Conn: model.ConnSSH, Enabled: true})
	inv.EnsureDeviceIDs()
	out := schedImportChanged(inv, before)
	kinds := map[string]string{}
	for _, e := range out {
		kinds[e.ScheduleID] = e.Kind
		if strings.Contains(e.Message, "192.0.2.") {
			t.Errorf("message carries an address: %s", e.Message)
		}
	}
	if kinds["all"] != "stopped" || inv.Schedules[0].Enabled {
		t.Errorf("whole-group schedule: kind %q, enabled %v", kinds["all"], inv.Schedules[0].Enabled)
	}
	if kinds["fixed"] != "changed" || !inv.Schedules[1].Enabled {
		t.Errorf("fixed list naming a1: kind %q, enabled %v", kinds["fixed"], inv.Schedules[1].Enabled)
	}
	if _, ok := kinds["fixed2"]; ok {
		t.Error("a fixed list whose device did not change was told")
	}
	if _, ok := kinds["b"]; ok || !inv.Schedules[3].Enabled {
		t.Error("another group's schedule was touched")
	}
	if !strings.Contains(out[0].Message, "追加 1 台・アドレス変更 1 台") {
		t.Errorf("counts: %s", out[0].Message)
	}
}

// E B-10: a clock set back by a day or more is said and the last look moved
// to now; a smaller step back (time sync, a zone put right) is waited out
// as before (TestClockBackDoesNotRepeat).
func TestClockBackFarIsToldAndReset(t *testing.T) {
	inv := schedInventory()
	inv.Schedules = []schedule.Schedule{{ID: "1", Name: "n", Enabled: true, Group: "A社",
		Slots: []schedule.Slot{{Kind: schedule.KindDaily, Time: "03:00:00"}}}}
	a := NewApp()
	a.inv = inv
	now := time.Now().Round(0)
	a.sched.checked = map[string]time.Time{"1": now.Add(3 * 24 * time.Hour)}
	_, lines, _ := a.schedPlan(now, inv.Schedules, true)
	if len(lines) != 1 || lines[0].Kind != "error" || !strings.Contains(lines[0].Message, "時計") {
		t.Fatalf("lines = %+v, want one error line about the clock", lines)
	}
	if got := a.sched.checked["1"]; !got.Equal(now) {
		t.Errorf("checked = %v, want now", got)
	}
	a.sched.checked["1"] = now.Add(2 * time.Hour)
	if _, lines, _ := a.schedPlan(now, inv.Schedules, true); len(lines) != 0 {
		t.Errorf("a step back of hours was told: %+v", lines)
	}
	if got := a.sched.checked["1"]; !got.Equal(now.Add(2 * time.Hour)) {
		t.Errorf("a step back of hours moved the last look: %v", got)
	}
}

// E A-2 / B-14: the last look that cannot be written is told (the 予約 tab's
// FileError), and a firing planned from the saved look after a restart is
// recorded as missed instead of run — it could not be kept from running
// again after the next restart.
func TestStateWriteFailureDropsRestartFiring(t *testing.T) {
	inv := schedInventory()
	now := time.Now().Round(0)
	due := now.Add(-10 * time.Minute)
	inv.Schedules = []schedule.Schedule{{ID: "1", Name: "n", Enabled: true, Group: "A社",
		Slots: []schedule.Slot{{Kind: schedule.KindDaily, Time: due.Format("15:04:05")}}}}
	a := NewApp()
	a.inv = inv
	defer armWake(time.Time{})
	defer func() {
		if a.sched.release != nil {
			a.sched.release()
		}
	}()
	// A path under a file: MkdirAll fails, the write cannot go through.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.sched.statePath = filepath.Join(blocker, "schedule-checked.json")
	a.sched.stateLoaded = true
	a.sched.saved = map[string]time.Time{"1": now.Add(-time.Hour)}
	a.sched.savedOn = 1
	a.mu.Lock()
	a.runActive = true // the batch slot is taken: a queued firing would wait
	a.mu.Unlock()
	a.schedTick(now)
	a.sched.mu.Lock()
	queued := len(a.sched.queue)
	a.sched.mu.Unlock()
	if queued != 0 {
		t.Errorf("queue = %d, want the restart firing dropped", queued)
	}
	found := false
	for _, e := range a.readSchedLog(5) {
		if e.ScheduleID == "1" && e.Kind == "missed" && strings.Contains(e.Message, "保存できない") {
			found = true
		}
	}
	if !found {
		t.Errorf("no missed line about the unwritable last look: %+v", a.readSchedLog(5))
	}
	if st := a.GetScheduleStatus(); !strings.Contains(st.FileError, "schedule-checked.json") {
		t.Errorf("FileError = %q", st.FileError)
	}
	a.sched.mu.Lock()
	dirty := a.sched.stateDirty
	a.sched.mu.Unlock()
	if !dirty {
		t.Error("the write is not tried again")
	}
}

// E B-11: the exit records the firings still waiting as skipped.
func TestExitRecordsQueuedFiringsSkipped(t *testing.T) {
	a := NewApp()
	sc := schedule.Schedule{ID: "q" + schedule.NewID(), Name: "n"}
	a.sched.queue = []schedJob{{sc: sc, due: time.Now()}, {sc: sc, due: time.Now().Add(time.Minute), manual: true}}
	if !a.waitSchedRuns(time.Second) {
		t.Error("nothing running: should not wait")
	}
	n := 0
	for _, e := range a.readSchedLog(10) {
		if e.ScheduleID == sc.ID && e.Kind == "skipped" && strings.Contains(e.Message, "終了") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("skipped lines = %d, want 2", n)
	}
	a.sched.mu.Lock()
	defer a.sched.mu.Unlock()
	if len(a.sched.queue) != 0 {
		t.Error("queue not emptied")
	}
}

// E B-12: after a restart, schedules waiting for the unlock keep × in the tray.
func TestPendingSchedulesIncludeWaitUnlock(t *testing.T) {
	a := NewApp()
	p := filepath.Join(t.TempDir(), "schedule-checked.json")
	if err := os.WriteFile(p, []byte(`{"checked":{},"enabled":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.sched.statePath = p
	if !a.hasPendingSchedules() {
		t.Error("schedules waiting for the unlock do not count as pending")
	}
}

// I H-2: a device's result rides with its log; an old line without it reads
// as not known, not as failed.
func TestSchedLogFileResult(t *testing.T) {
	var old SchedLogFile
	if err := json.Unmarshal([]byte(`{"device":"lab-sw1","path":"x.log"}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.OK != nil {
		t.Error("an old line should have no result")
	}
	no := false
	b, _ := json.Marshal(SchedLogFile{Device: "lab-sw1", Path: "x.log", OK: &no, Error: "ユーザー名またはパスワードが違います"})
	if s := string(b); !strings.Contains(s, `"ok":false`) || !strings.Contains(s, `"error":`) {
		t.Errorf("json = %s", s)
	}
}

// E B-1: the GUI window's environment carries no WEBVIEW2_* variable.
func TestGUIChildEnvDropsWebView2Vars(t *testing.T) {
	t.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--remote-debugging-port=9222")
	t.Setenv("webview2_user_data_folder", `C:\lab`)
	t.Setenv("PALATERM_E_TEST", "1")
	for _, kv := range guiChildEnv() {
		if strings.HasPrefix(strings.ToUpper(kv), "WEBVIEW2_") {
			t.Errorf("passed on: %s", kv)
		}
	}
	found := false
	for _, kv := range guiChildEnv() {
		found = found || kv == "PALATERM_E_TEST=1"
	}
	if !found {
		t.Error("ordinary variables must stay")
	}
}

// E B-7: a refused login is remembered per device and credentials; other
// credentials (edited since) are tried again.
func TestGUILoginRefusedMemory(t *testing.T) {
	id := "dev-" + schedule.NewID()
	if guiLoginRefused(id, "ops", "pw1") {
		t.Fatal("nothing refused yet")
	}
	noteGUILoginRefused(id, "ops", "pw1")
	if !guiLoginRefused(id, "ops", "pw1") {
		t.Error("the refused credentials are not remembered")
	}
	if guiLoginRefused(id, "ops", "pw2") || guiLoginRefused(id, "admin", "pw1") || guiLoginRefused("other", "ops", "pw1") {
		t.Error("other credentials or devices count as refused")
	}
	guiWinMu.Lock()
	if strings.Contains(guiLoginFailed[id], "pw1") {
		t.Error("the password itself is kept")
	}
	guiWinMu.Unlock()
	noteGUILoginRefused("", "ops", "pw1") // no device: nothing kept
	if guiLoginRefused("", "ops", "pw1") {
		t.Error("an empty device ID was remembered")
	}
}

// E A-3 / B-2 / B-8 / A-1: the fill script is guarded by origin, the copied
// URL loses its query, NoAutoLogin fills in without sending, and a trusted
// certificate is noted with or without a login form.
func TestGUIWindowGuards(t *testing.T) {
	js := guiFillScript("https://192.0.2.10", "ops", "pw", true)
	if !strings.HasPrefix(js, `(function(o,u,p,submit){if(location.origin!==o)return;`) || !strings.Contains(js, `"https://192.0.2.10","ops","pw",true)`) {
		t.Errorf("fill script: %s", js)
	}
	if s, stripped := bareURL("https://192.0.2.10/ng/page?session=abc#x"); s != "https://192.0.2.10/ng/page" || !stripped {
		t.Errorf("bareURL = %q, %v", s, stripped)
	}
	if s, stripped := bareURL("http://192.0.2.10/"); s != "http://192.0.2.10/" || stripped {
		t.Errorf("bareURL plain = %q, %v", s, stripped)
	}
	if s, _ := bareURL("about:blank"); s != "" {
		t.Errorf("bareURL about:blank = %q", s)
	}
	w := &guiWin{cfg: guiLaunch{User: "ops", Password: "pw", NoAutoLogin: true}}
	if w.loginSubmit(false) {
		t.Error("sent after an earlier window had these credentials refused")
	}
	// Trusted certificate, no pin: "ca" is recorded at the first note.
	w = &guiWin{cfg: guiLaunch{}, base: mustURL("https://192.0.2.10/")}
	if w.noteTrustedLocked() || w.cfg.Pin != "ca" {
		t.Errorf("no pin: pin = %q (want ca, not a swap)", w.cfg.Pin)
	}
	// Self-signed pin, now trusted: a swap, told once.
	w = &guiWin{cfg: guiLaunch{Pin: strings.Repeat("ab", 32)}, base: mustURL("https://192.0.2.10/")}
	if !w.noteTrustedLocked() || !w.swappedNow || w.badCert != "ca" {
		t.Errorf("swap not noted: %+v", w)
	}
	if w.noteTrustedLocked() {
		t.Error("the swap was told twice")
	}
	// http, or a certificate error in this window: nothing noted.
	w = &guiWin{cfg: guiLaunch{}, base: mustURL("http://192.0.2.10/")}
	if w.noteTrustedLocked() || w.cfg.Pin != "" {
		t.Error("http noted as trusted")
	}
	w = &guiWin{cfg: guiLaunch{}, base: mustURL("https://192.0.2.10/"), certErr: true}
	if w.noteTrustedLocked() || w.cfg.Pin != "" {
		t.Error("a certificate error noted as trusted")
	}
}
