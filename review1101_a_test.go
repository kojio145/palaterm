package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/history"
	"github.com/kojio145/palaterm/internal/model"
	runpkg "github.com/kojio145/palaterm/internal/runner"
	"github.com/kojio145/palaterm/internal/session"
)

// 2026-11-01 review A (binding surface). Lab values only: 192.0.2.x, ops,
// lab-sw1, jump1.

// A-1: the list bindings read the file PickListFile chose, no other.
func TestListBindingsReadOnlyThePickedFile(t *testing.T) {
	a, path, id := listImportApp(t)
	other := filepath.Join(t.TempDir(), "other.csv")
	if err := os.WriteFile(other, []byte("機器名称,IP\nlab-sw1,192.0.2.10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rule := model.ImportRule{Name: "r", HeaderRow: 1, Fields: []model.ImportField{{Key: "name", Source: "column", Column: "機器名称"}}}
	if _, err := a.PreviewListImport(ListImportReq{Path: other, RuleID: id, Group: "g", NewGroup: true}); err == nil || !strings.Contains(err.Error(), "ファイルが選ばれていません") {
		t.Errorf("PreviewListImport on a path not picked: %v", err)
	}
	if _, err := a.TestImportRule(other, rule); err == nil {
		t.Error("TestImportRule on a path not picked went through")
	}
	if _, err := a.SuggestImportRule(other, rule); err == nil {
		t.Error("SuggestImportRule on a path not picked went through")
	}
	if _, err := a.ApplyListImport(ListImportReq{Path: other, RuleID: id, Group: "g", NewGroup: true}); err == nil {
		t.Error("ApplyListImport on a path not picked went through")
	}
	if _, err := a.TestImportRule(path, rule); err != nil {
		t.Errorf("the picked file: %v", err)
	}
	a.ForgetListFile()
	if _, err := a.TestImportRule(path, rule); err == nil {
		t.Error("after ForgetListFile the file is still read")
	}
	a.listPath = path
	a.Lock()
	if a.listPath != "" {
		t.Error("Lock keeps the picked list")
	}
}

// A-2 / A-6: no host-key store methods and no ProfileKeys on App (every
// exported method is a binding); the store is the unexported adapter.
func TestHostKeyStoreIsNotABinding(t *testing.T) {
	typ := reflect.TypeOf(&App{})
	for _, name := range []string{"GetHostKey", "SetHostKey", "ProfileKeys"} {
		if _, ok := typ.MethodByName(name); ok {
			t.Errorf("App.%s is still exported (a Wails binding)", name)
		}
	}
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.hostKeys().SetHostKey("192.0.2.10:22", "ssh-ed25519 SHA256:lab")
	if got := a.hostKeys().GetHostKey("192.0.2.10:22"); got != "ssh-ed25519 SHA256:lab" {
		t.Errorf("adapter pin = %q", got)
	}
	a.flushPersist()
}

// A-3: an empty name is the default, never an item: refused.
func TestEmptyNameIsRefused(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "bk", Commands: []model.Command{{Text: "show version"}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteCommandSet(" "); !errors.Is(err, errNameEmpty) {
		t.Errorf("DeleteCommandSet(\" \") = %v", err)
	}
	if _, err := a.DeleteDeviceGroup(""); !errors.Is(err, errNameEmpty) {
		t.Errorf("DeleteDeviceGroup(\"\") = %v", err)
	}
	if err := a.RenameDeviceGroup("", "lab"); !errors.Is(err, errNameEmpty) {
		t.Errorf("RenameDeviceGroup(\"\", lab) = %v", err)
	}
	if err := a.DeleteChecklist(""); !errors.Is(err, errNameEmpty) {
		t.Errorf("DeleteChecklist(\"\") = %v", err)
	}
	if err := a.DeleteProfile(""); !errors.Is(err, errNameEmpty) {
		t.Errorf("DeleteProfile(\"\") = %v", err)
	}
	if n := len(a.GetInventory().CommandSets); n == 0 {
		t.Error("the sets went with the empty delete")
	}
}

// A-4: ResetVault / CreateVault forget the last batch's results, and
// ResetVault tells the terminal windows to close.
func TestResetVaultForgetsResultsAndClosesTerminals(t *testing.T) {
	a := newTestApp(t)
	a.lastDone = &RunDone{Results: []runpkg.DeviceResult{{Device: "lab-sw1"}}}
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if a.lastDone != nil {
		t.Error("CreateVault keeps the old vault's results")
	}
	a.lastDone = &RunDone{Results: []runpkg.DeviceResult{{Device: "lab-sw1"}}}
	buf := &nopBuf{}
	a.terms[&exec.Cmd{}] = newTermChild(buf, 1)
	if err := a.ResetVault("master"); err != nil {
		t.Fatal(err)
	}
	if a.lastDone != nil {
		t.Error("ResetVault keeps the results")
	}
	if !waitFor(func() bool { return strings.Contains(buf.String(), termCloseLine+"\n") }) {
		t.Errorf("the terminal window was not told to close: %q", buf.String())
	}
	if st := a.GetRunStatus(); st.Last != nil {
		t.Error("GetRunStatus still returns the old results")
	}
}

// A-5: Lock forgets what the last Unlock found.
func TestLockClearsUnlockInfo(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.unlockInfo = UnlockInfo{Rekeyed: []string{"custom:x"}, SavedBy: "PalaTerm 9.9"}
	a.Lock()
	if info := a.GetUnlockInfo(); len(info.Rekeyed) != 0 || info.SavedBy != "" {
		t.Errorf("after Lock: %+v", info)
	}
}

// A-6 / H 型7 / J #12: the bindings that act or read while locked.
func TestMoreLockedBindingsRefuse(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.Lock()
	called := false
	a.runCancel = func() { called = true }
	a.CancelRun()
	if called {
		t.Error("CancelRun while locked stopped the batch")
	}
	a.cancelRun()
	if !called {
		t.Error("cancelRun (exit) did not stop the batch")
	}
	if a.CancelDevice("lab-sw1") {
		t.Error("CancelDevice while locked")
	}
	if err := a.OpenExportFolder(filepath.Join(exeDir(), "export")); err == nil {
		t.Error("OpenExportFolder while locked")
	}
	if err := a.OpenExportFile(filepath.Join(exeDir(), "export", "x.html")); err == nil {
		t.Error("OpenExportFile while locked")
	}
	if info := a.BundleExportTarget("lab"); info.Exists {
		t.Error("BundleExportTarget while locked reads the manifest")
	}
	if err := a.SaveSettings(model.Settings{LogDir: filepath.Join(t.TempDir(), "never-made")}); !errors.Is(err, errVaultLocked) {
		t.Errorf("SaveSettings while locked = %v", err)
	}
	if err := a.LeaveAllBroadcast(); !errors.Is(err, errVaultLocked) {
		t.Errorf("LeaveAllBroadcast while locked = %v", err)
	}
	if n := len(a.JoinedTerminals()); n != 0 {
		t.Error("JoinedTerminals while locked names windows")
	}
	if err := a.ClearHopHostKeys("x", 0); !errors.Is(err, errVaultLocked) {
		t.Errorf("ClearHopHostKeys while locked = %v", err)
	}
	if err := a.ClearMyBastionHostKeys(); !errors.Is(err, errVaultLocked) {
		t.Errorf("ClearMyBastionHostKeys while locked = %v", err)
	}
	tm := &Term{locked: true}
	if err := tm.SetClipboard("x"); err == nil {
		t.Error("Term.SetClipboard while locked")
	}
}

// A-7 / H 型3-b: names are trimmed; a new group never replaces one.
func TestSaveDeviceAndGroupTrim(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	dev := model.Device{Name: " lab-sw1 ", Group: " lab ", Site: " dc1 ", Role: " edge ", Host: "192.0.2.10", Conn: model.ConnSSH,
		OSType: "cisco-ios", Username: "ops", Password: "pw", Enabled: true}
	if err := a.SaveDevice(dev); err != nil {
		t.Fatal(err)
	}
	inv := a.GetInventory()
	d := inv.Devices[0]
	if d.Name != "lab-sw1" || d.Group != "lab" || d.Site != "dc1" || d.Role != "edge" {
		t.Errorf("not trimmed: %+v", d)
	}
	if inv.Group("lab") == nil || inv.Group(" lab ") != nil {
		t.Error("the group row was made with the untrimmed name")
	}
	if err := a.CreateDeviceGroup(model.DeviceGroup{Name: " lab "}); err == nil || !strings.Contains(err.Error(), "既にあります") {
		t.Errorf("CreateDeviceGroup over an existing group: %v", err)
	}
	if err := a.CreateDeviceGroup(model.DeviceGroup{Name: " lab2 "}); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDeviceGroup(model.DeviceGroup{Name: " lab2 ", Username: "ops"}); err != nil {
		t.Fatal(err)
	}
	inv = a.GetInventory()
	if g := inv.Group("lab2"); g == nil || g.Username != "ops" || inv.Group(" lab2 ") != nil {
		t.Errorf("SaveDeviceGroup trim / update: %+v", inv.DeviceGroups)
	}
}

// A-10: a second connect while one runs (or a session is up) is refused.
func TestTermStartConnectOnce(t *testing.T) {
	tm := &Term{sizeKnown: make(chan struct{}), connecting: true}
	if tm.startConnect("pw") {
		t.Error("a connect started beside the running one")
	}
	tm.connecting = false
	tm.send = func(string) error { return nil }
	if tm.startConnect("pw") {
		t.Error("a connect started over a live session")
	}
}

// A-13: the shared report names the file it could not read, not the OS
// error with its absolute path.
func TestCompareReportHidesOSErrors(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	a.mu.Lock()
	a.inv.Settings.LogDir = root
	a.mu.Unlock()
	missing := filepath.Join(root, "20261101_120000_lab", "lab-sw1.txt")
	p, err := a.ExportCompareReport(ReportIn{Title: "t", Devices: []ReportDeviceIn{{Name: "lab-sw1", PathA: missing, PathB: missing, Verdict: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(p) })
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ログを読めません: lab-sw1.txt") {
		t.Error("the summary line is missing")
	}
	if strings.Contains(string(b), root) {
		t.Error("the report carries the absolute path")
	}
}

// J #3: a summary whose logFile points outside the log folder is not
// stat'ed or read by DeviceVersions.
func TestDeviceVersionsStayUnderLogRoot(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	root := filepath.Join(base, "logs")
	outside := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(outside, []byte("hostname lab-sw1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(root, "20261101_120000_lab")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(run, "lab-sw1.txt")
	if err := os.WriteFile(inside, []byte("hostname lab-sw1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := history.Write(run, history.Summary{StartedAt: "2026-11-01T12:00:00+09:00", Group: "lab", Devices: []history.DeviceSummary{
		{Name: "lab-sw1", Success: true, LogFile: "lab-sw1.txt"},
		{Name: "lab-rt1", Success: true, LogFile: filepath.Join("..", "..", "secret.txt")},
	}}); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.inv.Settings.LogDir = root
	a.mu.Unlock()
	vs, err := a.DeviceVersions("lab-rt1")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if a.underLogRoot(v.LogPath) != nil {
			t.Errorf("a version outside the log folder: %s", v.LogPath)
		}
	}
	vs, err = a.DeviceVersions("lab-sw1")
	if err != nil || len(vs) != 1 || vs[0].LogPath != inside {
		t.Errorf("the device's own version: %v %+v", err, vs)
	}
}

// H 型10: a picked file over the limit is refused before it is read whole.
func TestReadFileLimited(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "s.json")
	if err := os.WriteFile(small, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := readFileLimited(small, maxJSONImport); err != nil || string(b) != "{}" {
		t.Errorf("small: %v %q", err, b)
	}
	big := filepath.Join(dir, "b.json")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), maxJSONImport+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFileLimited(big, maxJSONImport); err == nil || !strings.Contains(err.Error(), "大きすぎます") {
		t.Errorf("big: %v", err)
	}
}

// H 型11: a remembered key file that is gone is forgotten at Unlock.
func TestUnlockForgetsAMissingKeyFile(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	kept := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(kept, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{filepath.Join(dir, "gone_key"), kept} {
		a.mu.Lock()
		a.inv.Settings.LastKeyFile = k
		a.mu.Unlock()
		if err := a.persist(); err != nil {
			t.Fatal(err)
		}
		a.Lock()
		if err := a.Unlock("master"); err != nil {
			t.Fatal(err)
		}
		want := k
		if k != kept {
			want = ""
		}
		if got := a.GetInventory().Settings.LastKeyFile; got != want {
			t.Errorf("LastKeyFile after Unlock = %q, want %q", got, want)
		}
	}
}

// G A-4 / H 型3-a: RenameChecklist is one save and refuses a taken name;
// ChecklistExists is the page's check before creating.
func TestRenameChecklist(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"base", "other"} {
		if err := a.SaveChecklist(model.Checklist{Name: n, Lines: []model.CheckLine{{Text: "hostname " + n}}}); err != nil {
			t.Fatal(err)
		}
	}
	if !a.ChecklistExists(" base ") || a.ChecklistExists("none") {
		t.Error("ChecklistExists")
	}
	if err := a.RenameChecklist("base", "other"); err == nil {
		t.Error("renamed onto another checklist")
	}
	if err := a.RenameChecklist("none", "x"); err == nil {
		t.Error("renamed a checklist that is not there")
	}
	if err := a.RenameChecklist("base", " base2 "); err != nil {
		t.Fatal(err)
	}
	inv := a.GetInventory()
	names := []string{}
	for _, c := range inv.Checklists {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "base2,other" {
		t.Errorf("checklists = %v", names)
	}
	if inv.Checklists[0].Lines[0].Text != "hostname base" {
		t.Error("the lines did not follow the rename")
	}
}

// G B-8 / I M-4: the terminal windows learn which windows take part, and
// the main window can name them and turn them all off.
func TestBroadcastNames(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	mk := func(name string, join bool, seq int) *nopBuf {
		b := &nopBuf{}
		c := newTermChild(b, seq)
		c.name, c.join, c.ready = name, join, join
		a.terms[&exec.Cmd{}] = c
		return b
	}
	b1 := mk("lab-sw2", true, 2)
	b2 := mk("lab-sw1", true, 1)
	b3 := mk("lab-rt1", false, 3)
	if got := strings.Join(a.JoinedTerminals(), ","); got != "lab-sw1,lab-sw2" {
		t.Errorf("JoinedTerminals = %q", got)
	}
	a.sendBcastCounts()
	want := termLineBcNames + ` ["lab-sw1","lab-sw2"]` + "\n" + termLineBcN + " 2 2\n"
	if !waitFor(func() bool { return strings.Contains(b1.String(), want) && strings.Contains(b3.String(), want) }) {
		t.Errorf("BCNAMES/BCN: %q / %q", b1.String(), b3.String())
	}
	if err := a.LeaveAllBroadcast(); err != nil {
		t.Fatal(err)
	}
	if !waitFor(func() bool {
		return strings.HasSuffix(b1.String(), termLineBcOff+"\n") && strings.HasSuffix(b2.String(), termLineBcOff+"\n")
	}) {
		t.Errorf("BCOFF did not reach the joined windows: %q", b1.String())
	}
	time.Sleep(30 * time.Millisecond)
	if strings.Contains(b3.String(), termLineBcOff) {
		t.Error("BCOFF reached a window with 同時入力 off")
	}
	// The window's side: the names arrive with the counts' event.
	tm := &Term{}
	tm.bcastNamesLine(`["lab-sw1","lab-sw2"]`)
	tm.bcastCounts("2 1")
	if st := tm.BroadcastState(); st.Joined != 2 || st.ReadyN != 1 || strings.Join(st.Names, ",") != "lab-sw1,lab-sw2" {
		t.Errorf("term:bcast = %+v", st)
	}
	tm.bcastCounts("2 1 extra")
	if st := tm.BroadcastState(); st.Joined != 2 {
		t.Errorf("a malformed BCN changed the state: %+v", st)
	}
}

// G B-17: term:closed says what kind of error it carries.
func TestClosedKind(t *testing.T) {
	if k := closedKind(&session.HostKeyMismatchError{Name: "192.0.2.10:22", Got: "a", Stored: "b"}); k != "hostkey" {
		t.Errorf("mismatch kind = %q", k)
	}
	if k := closedKind(errors.New("connection refused")); k != "" {
		t.Errorf("other kind = %q", k)
	}
	if k := closedKind(nil); k != "" {
		t.Errorf("nil kind = %q", k)
	}
}

// I M-3: the lock screen's counts follow the batch, each device once.
func TestRunCounts(t *testing.T) {
	a := newTestApp(t)
	a.runProg, a.runActive = runCounts{Running: true, Total: 3}, true
	a.noteRunEvent(runpkg.Event{Device: "lab-sw1", Phase: runpkg.PhaseRunning})
	a.noteRunEvent(runpkg.Event{Device: "lab-sw1", Phase: runpkg.PhaseDone})
	a.noteRunEvent(runpkg.Event{Device: "lab-sw1", Phase: runpkg.PhaseDone})
	a.noteRunEvent(runpkg.Event{Device: "lab-sw2", Phase: runpkg.PhaseError})
	a.noteRunEvent(runpkg.Event{Device: "lab-sw2", Phase: runpkg.PhaseCanceled})
	if c := a.GetRunCounts(); !c.Running || c.Done != 2 || c.Failed != 1 || c.Total != 3 || c.Finished {
		t.Errorf("counts = %+v", c)
	}
	c := countsOf([]runpkg.DeviceResult{{Device: "a", Success: true}, {Device: "b"}, {Device: "c", Canceled: true}})
	if !c.Finished || c.Done != 3 || c.Total != 3 || c.Failed != 2 {
		t.Errorf("countsOf = %+v", c)
	}
	// Numbers only: nothing of the result names a device.
	if s, _ := jsonOf(c); strings.Contains(s, "lab-") || strings.Contains(s, `"a"`) {
		t.Errorf("counts carry names: %s", s)
	}
}

// I H-4: a replaced jump host's pin can be let go of, by hop; the device's
// own pin and the other hops stay.
func TestClearHopHostKeys(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	dev := model.Device{ID: model.NewDeviceID(), Name: "lab-sw1", Group: "lab", Host: "192.0.2.10", Conn: model.ConnSSH,
		OSType: "cisco-ios", Username: "ops", Password: "pw", Enabled: true, UseMyBastion: true,
		Bastions: []model.Bastion{{Host: "192.0.2.1", Method: model.BastionSSH, Username: "ops", Password: "pw"}}}
	pins := map[string]string{
		"192.0.2.2:22":    "ssh-ed25519 SHA256:mine",
		"192.0.2.1:22":    "ssh-ed25519 SHA256:jump1",
		"192.0.2.10:22":   "ssh-ed25519 SHA256:own",
		"198.51.100.1:22": "ssh-ed25519 SHA256:unrelated",
	}
	a.mu.Lock()
	a.inv.Devices = append(a.inv.Devices, dev)
	a.inv.MyBastions = []model.Bastion{{Host: "192.0.2.2", Method: model.BastionSSH, Username: "ops", Password: "pw"}}
	a.inv.KnownHostKeys = map[string]string{}
	for k, v := range pins {
		a.inv.KnownHostKeys[k] = v
	}
	a.mu.Unlock()
	keys := func() map[string]string { return a.GetInventory().KnownHostKeys }
	if err := a.ClearHopHostKeys(dev.ID, 2); err == nil {
		t.Error("a hop past the chain was accepted")
	}
	if err := a.ClearHopHostKeys("no-such", 0); err == nil {
		t.Error("an unknown device was accepted")
	}
	if err := a.ClearHopHostKeys(dev.ID, 0); err != nil {
		t.Fatal(err)
	}
	if k := keys(); k["192.0.2.2:22"] != "" || k["192.0.2.1:22"] == "" || k["192.0.2.10:22"] == "" {
		t.Errorf("after hop 0: %v", k)
	}
	if err := a.ClearHopHostKeys(dev.ID, -1); err != nil {
		t.Fatal(err)
	}
	if k := keys(); k["192.0.2.1:22"] != "" || k["192.0.2.10:22"] == "" || k["198.51.100.1:22"] == "" {
		t.Errorf("after every hop: %v", k)
	}
	a.mu.Lock()
	a.inv.KnownHostKeys["192.0.2.2:22"] = "ssh-ed25519 SHA256:mine"
	a.mu.Unlock()
	if err := a.ClearMyBastionHostKeys(); err != nil {
		t.Fatal(err)
	}
	if k := keys(); k["192.0.2.2:22"] != "" || k["192.0.2.10:22"] == "" {
		t.Errorf("after ClearMyBastionHostKeys: %v", k)
	}
	a.mu.Lock()
	a.inv.MyBastions = nil
	a.mu.Unlock()
	if err := a.ClearMyBastionHostKeys(); err == nil {
		t.Error("no own jump host: accepted")
	}
}

// G B-15: OpenExportFile opens nothing outside export\, and no folder.
func TestOpenExportFileRefuses(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.OpenExportFile(filepath.Join(t.TempDir(), "x.html")); err == nil || !strings.Contains(err.Error(), "export") {
		t.Errorf("outside export: %v", err)
	}
	if err := a.OpenExportFile(filepath.Join(exeDir(), "export", "..", "x.html")); err == nil {
		t.Error("export\\..\\ was accepted")
	}
	if err := a.OpenExportFile(filepath.Join(exeDir(), "export")); err == nil {
		t.Error("the export folder itself was accepted as a file")
	}
}

// A-8 / H 型8: a panic in one queued line's consumer ends neither the
// process nor the queue.
func TestLineQueueSurvivesAPanic(t *testing.T) {
	old := errorLogPath
	logFile := filepath.Join(t.TempDir(), "error.log")
	errorLogPath = func() string { return logFile }
	defer func() { errorLogPath = old }()
	got := make(chan string, 2)
	q := newLineQueue(func(s string) {
		if s == "boom" {
			panic("consumer")
		}
		got <- s
	})
	q.put("boom")
	q.put("ok")
	select {
	case s := <-got:
		if s != "ok" {
			t.Errorf("got %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the queue stopped after the panic")
	}
	if b, _ := os.ReadFile(logFile); !strings.Contains(string(b), "lineQueue") {
		t.Error("the panic was not logged")
	}
}

// B-14 (G): a typed secret value does not reach the page or the exports.
func TestMaskSecretLineHelper(t *testing.T) {
	if maskSecretLine("") != "" {
		t.Error("empty")
	}
	if s := maskSecretLine("interface Gi0/1"); s != "interface Gi0/1" {
		t.Errorf("a plain line changed: %q", s)
	}
}

// jsonOf is json.Marshal as a string, for the names check.
func jsonOf(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
