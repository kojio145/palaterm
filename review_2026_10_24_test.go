package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/bundle"
	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
	"github.com/kojio145/palaterm/internal/schedule"
)

// 2026-10-24 review (app.go): the run tab's header check over no rows used
// to pass an empty list, which meant every device of every group.
func TestSetAllDevicesEnabledEmptyListChangesNothing(t *testing.T) {
	a := gridApp(t)
	if err := a.SetAllDevicesEnabled(false, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.SetAllDevicesEnabled(false, []string{}); err != nil {
		t.Fatal(err)
	}
	for _, d := range a.GetInventory().Devices {
		if !d.Enabled {
			t.Fatalf("%s unchecked by an empty list", d.Name)
		}
	}
	id := devByGroup(t, a, "lab", "rt2").ID
	if err := a.SetAllDevicesEnabled(false, []string{id}); err != nil {
		t.Fatal(err)
	}
	for _, d := range a.GetInventory().Devices {
		if d.Enabled == (d.ID == id) {
			t.Errorf("%s enabled=%v", d.Name, d.Enabled)
		}
	}
}

// Deleting a group ungroups its devices: one on the group's credentials has
// none to use any more (UpdateDevices already turned the flag off on a move).
func TestDeleteDeviceGroupDropsGroupCreds(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDeviceGroup(model.DeviceGroup{Name: "lab", Username: "ops", Password: "gpw"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDevice(model.Device{Name: "rt1", Group: "lab", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios", UseGroupCreds: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if !devByGroup(t, a, "lab", "rt1").UseGroupCreds {
		t.Fatal("setup: the device is not on the group's credentials")
	}
	if _, err := a.DeleteDeviceGroup("lab"); err != nil {
		t.Fatal(err)
	}
	if d := devByGroup(t, a, "", "rt1"); d.UseGroupCreds {
		t.Errorf("ungrouped device still on group credentials: %+v", d)
	}
}

// A schedule set to run a deleted command set stops and says why, like one
// of a deleted group; schedules on other sets (or on each device's own) go on.
func TestDeleteCommandSetStopsSchedules(t *testing.T) {
	a := gridApp(t)
	for _, n := range []string{"bk", "other"} {
		if err := a.CreateCommandSet(model.CommandSet{Name: n, Commands: []model.Command{{Text: "show clock"}}}); err != nil {
			t.Fatal(err)
		}
	}
	u := "c" + schedule.NewID() + "-" // the 実行記録 file is shared between tests
	a.mu.Lock()
	a.inv.Schedules = append(a.inv.Schedules,
		schedule.Schedule{ID: u + "bk", Name: "nightly bk", Enabled: true, Group: "lab", CommandSet: "bk"},
		schedule.Schedule{ID: u + "off", Name: "bk off", Enabled: false, Group: "lab", CommandSet: "bk"},
		schedule.Schedule{ID: u + "other", Name: "other", Enabled: true, Group: "lab", CommandSet: "other"},
		schedule.Schedule{ID: u + "own", Name: "own sets", Enabled: true, Group: "lab"})
	a.mu.Unlock()
	if err := a.DeleteCommandSet("bk"); err != nil {
		t.Fatal(err)
	}
	if schedByID(t, a, u+"bk").Enabled {
		t.Error("the schedule on the deleted set is still on")
	}
	if !schedByID(t, a, u+"other").Enabled || !schedByID(t, a, u+"own").Enabled {
		t.Error("a schedule on another set was stopped")
	}
	if l := stoppedLines(a, u+"bk"); len(l) != 1 || !strings.Contains(l[0].Message, "コマンドセット「bk」") {
		t.Errorf("record: %+v", l)
	}
	if l := stoppedLines(a, u+"off"); len(l) != 0 {
		t.Errorf("a schedule already off got a record: %+v", l)
	}
	for _, cs := range a.GetInventory().CommandSets {
		if cs.Name == "bk" {
			t.Error("set not deleted")
		}
	}
}

// Lock and ResetVault forget the file read before them: the answer to its
// name conflict cannot apply it to the vault opened after.
func TestLockForgetsPendingImport(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	set := model.CommandSet{Name: "bk", Commands: []model.Command{{Text: "show clock"}}}
	if err := a.CreateCommandSet(set); err != nil {
		t.Fatal(err)
	}
	stale := model.CommandSet{Name: "bk", Commands: []model.Command{{Text: "reload"}}}
	a.mu.Lock()
	a.pendingImport = &pendingImport{set: &stale}
	a.importPath = `C:\lab\palaterm-bundle.json`
	a.mu.Unlock()
	a.Lock()
	if err := a.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	if res, err := a.ResolveFileImport(importReplace); err != nil || res.Imported {
		t.Errorf("a file read before the lock was applied after it: %+v, %v", res, err)
	}
	for _, cs := range a.GetInventory().CommandSets {
		if cs.Name == "bk" && cs.Commands[0].Text != "show clock" {
			t.Errorf("set replaced from the stale import: %+v", cs)
		}
	}
	if _, err := a.PreviewImportFile(`C:\lab\palaterm-bundle.json`, "", false); err == nil {
		t.Error("the import path survived the lock")
	}

	a.mu.Lock()
	a.pendingImport = &pendingImport{set: &stale}
	a.importPath = `C:\lab\palaterm-bundle.json`
	a.mu.Unlock()
	if err := a.ResetVault("master"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	pend, path := a.pendingImport, a.importPath
	a.mu.Unlock()
	if pend != nil || path != "" {
		t.Error("ResetVault kept the pending import")
	}
}

// The {user}/{password}/{enable} refusal in answers lives in
// model.CheckProfileSends now, so a profile file (and a bundle's) is refused
// the way the editor refuses it.
func TestProfileFileRefusesCredentialAnswers(t *testing.T) {
	js := []byte(`{"key":"custom:lab","name":"lab","prompt":"#","login":[],"answers":[{"expect":"Password:","send":"{password}"}]}`)
	if _, err := bundle.ParseProfileJSON(js); err == nil || !strings.Contains(err.Error(), "{password}") {
		t.Errorf("profile JSON with an answer sending {password} accepted: %v", err)
	}
	ok := []byte(`{"key":"custom:lab","name":"lab","prompt":"#","login":[],"answers":[{"expect":"[confirm]","send":"y"}]}`)
	if _, err := bundle.ParseProfileJSON(ok); err != nil {
		t.Errorf("plain answer refused: %v", err)
	}
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	p := profile.Profile{Name: "lab", Prompt: "#", Answers: []profile.Step{{Expect: "Username:", Send: "{user}"}}}
	if err := a.SaveProfile(p); err == nil || !strings.Contains(err.Error(), "{user}") {
		t.Errorf("SaveProfile: %v", err)
	}
}

// Only the file PickImportFile's dialog chose is previewed again or applied:
// any other path the page names is refused.
func TestImportReadsOnlyThePickedFile(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	csv := filepath.Join(dir, "devices.csv")
	if err := os.WriteFile(csv, []byte("name,group,host,conn\nrt1,lab,192.0.2.1,ssh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PreviewImportFile(csv, "", false); err == nil {
		t.Error("PreviewImportFile read a file the dialog did not choose")
	}
	if _, err := a.ApplyImport(csv, "", false); err == nil {
		t.Error("ApplyImport read a file the dialog did not choose")
	}
	if n := len(a.GetInventory().Devices); n != 0 {
		t.Fatalf("%d devices imported from an unpicked file", n)
	}
	// What PickImportFile records once its dialog returned.
	a.mu.Lock()
	a.importPath = csv
	a.mu.Unlock()
	pv, err := a.PreviewImportFile(csv, "", false)
	if err != nil || pv.Kind != "csv" || pv.Summary.Devices != 1 {
		t.Fatalf("preview of the picked file: %+v, %v", pv, err)
	}
	other := filepath.Join(dir, "other.csv")
	if err := os.WriteFile(other, []byte("name,group,host,conn\nrt2,lab,192.0.2.2,ssh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyImport(other, "", false); err == nil {
		t.Error("ApplyImport read another file than the one picked")
	}
	s, err := a.ApplyImport(csv, "", false)
	if err != nil || s.Devices != 1 {
		t.Fatalf("apply of the picked file: %+v, %v", s, err)
	}
	if d := devByGroup(t, a, "lab", "rt1"); d.Host != "192.0.2.1" {
		t.Errorf("imported = %+v", d)
	}
}

// Two failure exports within the same second are two files (as the
// comparison export already was).
func TestExportRunFailuresDoesNotOverwrite(t *testing.T) {
	a := gridApp(t)
	rows := []RunFailure{{Device: "rt1", Status: "失敗", Message: "timeout"}}
	var paths []string
	for i := 0; i < 3; i++ {
		p, err := a.ExportRunFailures("lab", false, rows)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(p) })
		paths = append(paths, p)
	}
	if paths[0] == paths[1] || paths[1] == paths[2] || paths[0] == paths[2] {
		t.Fatalf("same file: %q", paths)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), "rt1") || !strings.Contains(string(b), "timeout") {
			t.Errorf("%s: %q %v", p, b, err)
		}
	}
	if !strings.HasSuffix(paths[0], ".csv") || !strings.HasSuffix(paths[1], "_2.csv") {
		t.Errorf("names: %q", paths)
	}
}

// SaveCommandSet trims the name and refuses an empty one, as
// CreateCommandSet does: no "" or "bk " ghost set from the editor's save.
func TestSaveCommandSetTrimsName(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	cmds := []model.Command{{Text: "show clock"}}
	if err := a.SaveCommandSet(model.CommandSet{Name: "  ", Commands: cmds}); err == nil {
		t.Error("empty name saved")
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: " bk ", Commands: cmds}); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "bk", Commands: []model.Command{{Text: "show version"}}}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, cs := range a.GetInventory().CommandSets {
		if strings.TrimSpace(cs.Name) == "bk" {
			n++
			if cs.Name != "bk" || cs.Commands[0].Text != "show version" {
				t.Errorf("saved as %+v", cs)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d sets named bk", n)
	}
	if err := a.CreateCommandSet(model.CommandSet{Name: "bk ", Commands: cmds}); err == nil {
		t.Error("a name taken once trimmed was created again")
	}
}

// The log viewers open nothing while the vault is locked — the diff window
// was the one binding without the gate.
func TestOpenDiffWindowRefusesWhileLocked(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.Lock()
	if err := a.OpenDiffWindow("logs/a.log", "logs/b.log", "x"); err != errVaultLocked {
		t.Errorf("OpenDiffWindow while locked: %v", err)
	}
}

// underRoot is the one "inside the folder" check behind the log folder and
// export\ paths the page hands back.
func TestUnderRoot(t *testing.T) {
	root := t.TempDir()
	for p, bad := range map[string]bool{
		filepath.Join(root, "run1", "rt1.log"): false,
		root:                                   false,
		filepath.Join(root, ".."):              true,
		filepath.Join(root, "..", "other"):     true,
		filepath.Join(root, "..", filepath.Base(root)+"x", "a.log"): true,
	} {
		err := underRoot(root, p, "outside")
		if (err != nil) != bad {
			t.Errorf("%q: err=%v, want bad=%v", p, err, bad)
		}
		if err != nil && err.Error() != "outside" {
			t.Errorf("%q: message %q", p, err)
		}
	}
}
