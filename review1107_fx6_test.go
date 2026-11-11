package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/history"
	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
	runpkg "github.com/kojio145/palaterm/internal/runner"
)

// fx6App is an unlocked app with a group, two devices, a set and a profile.
func fx6App(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []model.Device{
		{Name: "lab-sw1", Group: "lab", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Password: "pw", CommandSet: "bk", Enabled: true},
		{Name: "lab-sw2", Group: "lab", Host: "192.0.2.2", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Password: "pw", Enabled: true},
	} {
		if err := a.SaveDevice(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "bk", Commands: []model.Command{{Text: "show version"}}}); err != nil {
		t.Fatal(err)
	}
	return a
}

// blockVault makes every save fail (the vault path's parent "folder" is a
// file) and returns the way back.
func blockVault(t *testing.T, a *App) func() {
	t.Helper()
	good := a.vaultPath
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.vaultPath = filepath.Join(blocker, "x", "vault.enc")
	return func() { a.vaultPath = good }
}

// A-4 / F-A-5 / F-B-6 (review 2026-11-07): a save that fails leaves memory
// as it was — devices, groups, sets, profiles (the registry too), settings
// — for every binding that writes the vault.
func TestFailedSaveRollsBackEveryBinding(t *testing.T) {
	a := fx6App(t)
	before := a.GetInventory()
	unblock := blockVault(t, a)
	same := func(what string) {
		t.Helper()
		after := a.GetInventory()
		if len(after.Devices) != len(before.Devices) || len(after.DeviceGroups) != len(before.DeviceGroups) ||
			len(after.CommandSets) != len(before.CommandSets) || len(after.CustomProfiles) != len(before.CustomProfiles) {
			t.Errorf("%s: a failed save changed memory: %d/%d devices, %d/%d groups, %d/%d sets, %d/%d profiles",
				what, len(after.Devices), len(before.Devices), len(after.DeviceGroups), len(before.DeviceGroups),
				len(after.CommandSets), len(before.CommandSets), len(after.CustomProfiles), len(before.CustomProfiles))
		}
		for i := range before.Devices {
			if i < len(after.Devices) && (after.Devices[i].Name != before.Devices[i].Name || after.Devices[i].Group != before.Devices[i].Group || after.Devices[i].Site != before.Devices[i].Site || after.Devices[i].Enabled != before.Devices[i].Enabled) {
				t.Errorf("%s: device %d changed: %+v", what, i, after.Devices[i])
			}
		}
		if after.Settings.MaxParallel != before.Settings.MaxParallel || after.Settings.LastKeyFile != before.Settings.LastKeyFile {
			t.Errorf("%s: settings changed", what)
		}
	}
	id1, id2 := before.Devices[0].ID, before.Devices[1].ID
	site := "site-x"
	if _, err := a.UpdateDevices([]string{id1, id2}, DevicePatch{Site: &site}); err == nil {
		t.Fatal("UpdateDevices saved into an impossible path")
	}
	same("UpdateDevices")
	if _, err := a.DeleteDevices([]string{id1}); err == nil {
		t.Fatal("DeleteDevices")
	}
	same("DeleteDevices")
	if err := a.DeleteDevice(id1); err == nil {
		t.Fatal("DeleteDevice")
	}
	same("DeleteDevice")
	if err := a.RenameDevice(id1, "lab-sw9"); err == nil {
		t.Fatal("RenameDevice")
	}
	same("RenameDevice")
	if _, err := a.CopyDevice(id1); err == nil {
		t.Fatal("CopyDevice")
	}
	same("CopyDevice")
	if err := a.SetAllDevicesEnabled(false, []string{id1, id2}); err == nil {
		t.Fatal("SetAllDevicesEnabled")
	}
	same("SetAllDevicesEnabled")
	// SaveDevice: a new group, the key path and a group move go back too.
	d := before.Devices[0]
	d.Group, d.KeyFile, d.AuthMethod = "new-group", `C:\keys\lab.pem`, model.AuthPublicKey
	if err := a.SaveDevice(d); err == nil {
		t.Fatal("SaveDevice")
	}
	same("SaveDevice")
	if err := a.CreateDeviceGroup(model.DeviceGroup{Name: "g2"}); err == nil {
		t.Fatal("CreateDeviceGroup")
	}
	same("CreateDeviceGroup")
	if _, err := a.CopyDeviceGroup("lab", "lab2", ""); err == nil {
		t.Fatal("CopyDeviceGroup")
	}
	same("CopyDeviceGroup")
	if err := a.RenameDeviceGroup("lab", "lab3"); err == nil {
		t.Fatal("RenameDeviceGroup")
	}
	same("RenameDeviceGroup")
	if _, err := a.DeleteDeviceGroup("lab"); err == nil {
		t.Fatal("DeleteDeviceGroup")
	}
	same("DeleteDeviceGroup")
	if err := a.SaveCommandSet(model.CommandSet{Name: "bk2", Commands: []model.Command{{Text: "show clock"}}}); err == nil {
		t.Fatal("SaveCommandSet")
	}
	same("SaveCommandSet")
	if err := a.DeleteCommandSet("bk"); err == nil {
		t.Fatal("DeleteCommandSet")
	}
	same("DeleteCommandSet")
	if _, err := a.CopyCommandSet("bk"); err == nil {
		t.Fatal("CopyCommandSet")
	}
	same("CopyCommandSet")
	if err := a.RenameCommandSet("bk", "bk9"); err == nil {
		t.Fatal("RenameCommandSet")
	}
	same("RenameCommandSet")
	if err := a.ReorderDevices([]string{id2, id1}); err == nil {
		t.Fatal("ReorderDevices")
	}
	same("ReorderDevices")
	s := before.Settings
	s.MaxParallel = 7
	if err := a.SaveSettings(s); err == nil {
		t.Fatal("SaveSettings")
	}
	same("SaveSettings")
	// Profiles: the registry follows memory back.
	p := profile.Profile{Name: "fx6 new", Prompt: "#"}
	if err := a.SaveProfile(p); err == nil {
		t.Fatal("SaveProfile")
	}
	same("SaveProfile")
	for _, q := range a.profiles.List() {
		if q.Name == "fx6 new" {
			t.Error("SaveProfile: the registry kept a profile the vault never got")
		}
	}
	unblock()
	if _, err := a.UpdateDevices([]string{id1}, DevicePatch{Site: &site}); err != nil {
		t.Fatalf("after unblocking: %v", err)
	}
	if got := a.GetInventory().Devices[0].Site; got != "site-x" {
		t.Errorf("a good save after the failed ones: site %q", got)
	}
}

// D-A4: a list import whose save fails leaves no device in memory; D-A1:
// a successful one records the group on the rule.
func TestListImportRollbackAndLastGroup(t *testing.T) {
	a, path, id := listImportApp(t)
	req := ListImportReq{Path: path, RuleID: id, Group: "g", NewGroup: true}
	pv, err := a.PreviewListImport(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Stamp = pv.Stamp
	unblock := blockVault(t, a)
	if _, err := a.ApplyListImport(req); err == nil {
		t.Fatal("ApplyListImport saved into an impossible path")
	}
	inv := a.GetInventory()
	if len(inv.Devices) != 0 || inv.Group("g") != nil || inv.ImportRules[0].LastGroup != "" {
		t.Fatalf("failed import left: %d devices, group %v, lastGroup %q", len(inv.Devices), inv.Group("g"), inv.ImportRules[0].LastGroup)
	}
	unblock()
	if res, err := a.ApplyListImport(req); err != nil || res.Added != 2 {
		t.Fatalf("import: %+v %v", res, err)
	}
	inv = a.GetInventory()
	if inv.ImportRules[0].LastGroup != "g" {
		t.Errorf("LastGroup = %q", inv.ImportRules[0].LastGroup)
	}
	// A rule saved again from the editor keeps it.
	r := inv.ImportRules[0]
	r.Name = "r2"
	if _, err := a.SaveImportRule(r); err != nil {
		t.Fatal(err)
	}
	if got := a.GetInventory().ImportRules[0].LastGroup; got != "g" {
		t.Errorf("LastGroup after SaveImportRule = %q", got)
	}
}

// B-A10 / B-B1: a page reloaded mid-run gets each device's last event; the
// run tab's 結果をクリア forgets the last results for good.
func TestRunStatusEventsAndDiscard(t *testing.T) {
	a := fx6App(t)
	a.mu.Lock()
	a.runActive, a.runSched = true, ""
	a.runReq = &RunRequest{Group: "lab", Names: []string{"lab-sw1", "lab-sw2"}}
	a.mu.Unlock()
	a.noteRunEvent(runpkg.Event{Device: "lab-sw1", Phase: runpkg.PhaseRunning, Message: "show version", Done: 1, Total: 3})
	a.noteRunEvent(runpkg.Event{Device: "lab-sw2", Phase: runpkg.PhaseDone, Message: "ok"})
	st := a.GetRunStatus()
	if !st.Active || len(st.Events) != 2 || st.Events["lab-sw1"].Phase != runpkg.PhaseRunning || st.Events["lab-sw1"].Done != 1 || st.Events["lab-sw2"].Phase != runpkg.PhaseDone {
		t.Fatalf("status: %+v", st)
	}
	// The counts still count a device once.
	if c := a.GetRunCounts(); c.Done != 1 || c.Failed != 0 {
		t.Errorf("counts: %+v", c)
	}
	a.mu.Lock()
	a.runActive = false
	a.lastDone = &RunDone{Results: []runpkg.DeviceResult{{Device: "lab-sw1", Success: true}}}
	a.mu.Unlock()
	if st := a.GetRunStatus(); st.Active || st.Last == nil || len(st.Events) != 0 {
		t.Fatalf("after the batch: %+v", st)
	}
	a.DiscardLastRun()
	if st := a.GetRunStatus(); st.Last != nil {
		t.Error("DiscardLastRun kept the results")
	}
}

// C-A6: the versions of a device of one group leave out a device of the
// same name in another group.
func TestDeviceVersionsIn(t *testing.T) {
	a := fx6App(t)
	root := filepath.Join(t.TempDir(), "logs")
	write := func(dir, group string) string {
		run := filepath.Join(root, dir)
		if err := os.MkdirAll(run, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(run, "lab-sw1.txt")
		if err := os.WriteFile(p, []byte("hostname lab-sw1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := history.Write(run, history.Summary{StartedAt: "2026-11-01T12:00:00+09:00", Group: group, Devices: []history.DeviceSummary{{Name: "lab-sw1", Success: true, LogFile: "lab-sw1.txt"}}}); err != nil {
			t.Fatal(err)
		}
		return p
	}
	inLab := write("20261101_120000_lab", "lab")
	write("20261101_130000_other", "other")
	a.mu.Lock()
	a.inv.Settings.LogDir = root
	a.mu.Unlock()
	all, err := a.DeviceVersions("lab-sw1")
	if err != nil || len(all) != 2 {
		t.Fatalf("by name: %v %d", err, len(all))
	}
	vs, err := a.DeviceVersionsIn("lab", "lab-sw1")
	if err != nil || len(vs) != 1 || vs[0].LogPath != inLab {
		t.Fatalf("in lab: %v %+v", err, vs)
	}
	if vs, _ := a.DeviceVersionsIn("nowhere", "lab-sw1"); len(vs) != 0 {
		t.Errorf("a group with no runs: %+v", vs)
	}
}

// B-A9 / B-B6: the settings' numbers and name templates are checked when
// saved.
func TestSaveSettingsRangesAndTemplates(t *testing.T) {
	a := fx6App(t)
	base := a.GetInventory().Settings
	base.LogDir = t.TempDir()
	for _, c := range []struct {
		change func(s *model.Settings)
		want   string
	}{
		{func(s *model.Settings) { s.MaxParallel = 201 }, "並列実行数は 0〜200"},
		{func(s *model.Settings) { s.MaxParallel = -1 }, "並列実行数は 0〜200"},
		{func(s *model.Settings) { s.AutoLockMin = 1441 }, "自動ロックまでの分数は 0〜1440"},
		{func(s *model.Settings) { s.ConnectTimeout = 0 }, "接続タイムアウトは 1〜3600"},
		{func(s *model.Settings) { s.CommandTimeout = 3601 }, "コマンドタイムアウトは 1〜3600"},
		{func(s *model.Settings) { s.LoginTimeout = 3601 }, "ログイン待ちは 0〜3600"},
		{func(s *model.Settings) { s.LoginTimeout = -1 }, "ログイン待ちは 0〜3600"},
		{func(s *model.Settings) { s.PasteLineDelayMs = 5001 }, "貼り付けの行間待ち（シリアル）は 0〜5000"},
		{func(s *model.Settings) { s.PasteLineDelayMs = -2 }, "貼り付けの行間待ち（シリアル）は 0〜5000"},
		{func(s *model.Settings) { s.PasteLineDelayNetMs = -1 }, "貼り付けの行間待ち（SSH・Telnet）は 0〜5000"},
		{func(s *model.Settings) { s.LogNameTemplate = "{host}:{date}.txt" }, "ログファイル名の書式"},
		{func(s *model.Settings) { s.LogDirTemplate = "{group}|{date}" }, "ログフォルダ名の書式"},
	} {
		s := base
		c.change(&s)
		if err := a.SaveSettings(s); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
	s := base
	s.MaxParallel, s.AutoLockMin, s.ConnectTimeout, s.CommandTimeout = 0, 0, 1, 3600
	s.LogNameTemplate, s.LogDirTemplate = "{host}_{date}.txt", "{date}\\{group}"
	if err := a.SaveSettings(s); err != nil {
		t.Errorf("the bounds themselves refused: %v", err)
	}
}

// B-C2 / B-C4: the profile errors name the field as the screen does, and
// a profile in use says how many devices and what to do.
func TestProfileErrorWording(t *testing.T) {
	a := fx6App(t)
	if err := a.SaveProfile(profile.Profile{Name: "x"}); err == nil || err.Error() != "プロンプト（コマンド完了の目印）は必須です" {
		t.Errorf("empty prompt: %v", err)
	}
	var key string
	for _, p := range a.GetInventory().CustomProfiles {
		if p.Key == "cisco-ios" {
			key = p.Key
		}
	}
	if key == "" {
		t.Skip("no seeded cisco-ios profile")
	}
	err := a.DeleteProfile(key)
	if err == nil || !strings.Contains(err.Error(), "機器 2 台がこの OS タイプを使用中") || !strings.Contains(err.Error(), "lab-sw1, lab-sw2") || !strings.Contains(err.Error(), "機器の OS タイプを変えてから削除してください") {
		t.Errorf("in use: %v", err)
	}
}

// A B-6: an export folder holding another group's export (two names that
// make the same folder name) is said so.
func TestBundleExportTargetOtherGroup(t *testing.T) {
	a := fx6App(t)
	for _, d := range []model.Device{
		{Name: "a1", Group: "lab/A", Host: "192.0.2.11", Conn: model.ConnSSH, OSType: "cisco-ios", Enabled: true},
		{Name: "b1", Group: "lab_A", Host: "192.0.2.12", Conn: model.ConnSSH, OSType: "cisco-ios", Enabled: true},
	} {
		if err := a.SaveDevice(d); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := a.ExportBundle("lab/A", true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	info := a.BundleExportTarget("lab_A")
	if !info.Exists || !info.Other || info.OtherGroup != "lab/A" {
		t.Errorf("another group's export not told: %+v", info)
	}
	if info := a.BundleExportTarget("lab/A"); !info.Exists || info.Other {
		t.Errorf("the group's own export read as another's: %+v", info)
	}
}

// D-A9 / D-C1: the table editor refuses a hop typed after an empty one on
// that hop's own column, and marks a Validate message about the device's
// (renumbered) hop on the column the value was typed in.
func TestPlanGridHopGap(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	r := rows[0]
	r.Cells["bastion2Host"] = "192.0.2.20"
	r.Cells["bastion2Method"] = "ssh"
	r.Cells["bastion2AuthMethod"] = "publickey" // a key auth needs a key file: a Validate error on hop "1"
	rep, _, _ := planGrid(a.inv, GridApply{Mode: "all", Rows: []model.GridRow{r}})
	cols := map[string]string{}
	for _, e := range rep.Errors[r.UID] {
		cols[e.Col] = e.Msg
	}
	if msg, ok := cols["bastion1Host"]; !ok || !strings.Contains(msg, "踏み台1が空のまま踏み台2があります") {
		t.Errorf("no gap error on hop 1: %v", rep.Errors[r.UID])
	}
	for col := range cols {
		if strings.HasPrefix(col, "bastion1") && col != "bastion1Host" {
			t.Errorf("a hop-2 value's error landed on %s: %v", col, rep.Errors[r.UID])
		}
	}
	// A reserved group name marks the group column.
	r2 := rows[1]
	r2.Cells["group"] = "__x"
	rep, _, _ = planGrid(a.inv, GridApply{Mode: "all", Rows: []model.GridRow{r2}})
	found := false
	for _, e := range rep.Errors[r2.UID] {
		if e.Col == "group" && e.Msg == model.ReservedNameMsg {
			found = true
		}
	}
	if !found {
		t.Errorf("reserved group not on the group column: %v", rep.Errors[r2.UID])
	}
}
