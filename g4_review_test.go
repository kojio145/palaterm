package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
	runpkg "github.com/kojio145/palaterm/internal/runner"
	"github.com/kojio145/palaterm/internal/vault"
)

// profileByName returns the vault's profile called name (nil if none).
func profileByName(a *App, name string) *profile.Profile {
	for _, p := range a.GetInventory().CustomProfiles {
		if p.Name == name {
			p := p
			return &p
		}
	}
	return nil
}

// B2: copy → rename the copy → copy again used to key the second copy like
// the renamed first one.
func TestCopyProfileNeverReusesAKey(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveProfile(profile.Profile{Name: "LabOS", Prompt: "lab#"}); err != nil {
		t.Fatal(err)
	}
	first, err := a.CopyProfile("custom:LabOS")
	if err != nil {
		t.Fatal(err)
	}
	cp := profileByName(a, first)
	cp.Name = "LabOS edge"
	if err := a.SaveProfile(*cp); err != nil {
		t.Fatal(err)
	}
	second, err := a.CopyProfile("custom:LabOS")
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("second copy name = %q, want %q again", second, first)
	}
	renamed, again := profileByName(a, "LabOS edge"), profileByName(a, second)
	if renamed == nil || again == nil || renamed.Key == again.Key {
		t.Fatalf("keys collide: %+v / %+v", renamed, again)
	}
}

// B2: a vault that already has two profiles under one key gets them apart
// on unlock; the first keeps the key, so the devices on it stay on it.
func TestUnlockRekeysDuplicateProfileKeys(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.inv.CustomProfiles = append(a.inv.CustomProfiles,
		profile.Profile{Key: "custom:LabOS_copy", Name: "LabOS edge", Prompt: "edge#"},
		profile.Profile{Key: "custom:LabOS_copy", Name: "LabOS_copy", Prompt: "copy#"})
	a.inv.Devices = append(a.inv.Devices, model.Device{ID: "d1", Name: "edge1", Host: "192.0.2.10", Conn: model.ConnSSH, OSType: "custom:LabOS_copy", Username: "ops", Password: "pw"})
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		t.Fatal(err)
	}
	b := NewApp()
	b.vaultPath = a.vaultPath
	if err := b.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	if got := b.GetUnlockInfo().Rekeyed; len(got) != 1 || got[0] != "LabOS_copy" {
		t.Errorf("rekeyed = %v", got)
	}
	edge, cp := profileByName(b, "LabOS edge"), profileByName(b, "LabOS_copy")
	if edge.Key != "custom:LabOS_copy" || cp.Key == edge.Key {
		t.Fatalf("keys: edge %q, copy %q", edge.Key, cp.Key)
	}
	if b.profiles.Get(edge.Key).Prompt != `edge#` {
		t.Errorf("registry under the shared key = %+v", b.profiles.Get(edge.Key))
	}
	if d := b.GetInventory().Devices[0]; d.OSType != edge.Key {
		t.Errorf("device OSType = %q", d.OSType)
	}
	// Saved: a third unlock finds nothing to fix.
	c := NewApp()
	c.vaultPath = a.vaultPath
	if err := c.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	if got := c.GetUnlockInfo().Rekeyed; len(got) != 0 {
		t.Errorf("rekeyed again: %v", got)
	}
}

// B6 / B2: a profile file whose name or key is already there is not put in
// without asking, and never over a renamed profile that only shares the key.
func TestImportProfileAsksBeforeReplacing(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.inv.CustomProfiles = append(a.inv.CustomProfiles, profile.Profile{Key: "custom:LabOS", Name: "Renamed", Prompt: "r#"})
	defaults := len(a.inv.CustomProfiles)

	// Same key, other name, other steps: a profile of its own, fresh key.
	res, ch := a.importProfileLocked(profile.Profile{Key: "custom:LabOS", Name: "LabOS", Prompt: "lab#"}, "")
	if !res.Imported || res.Conflict != "" || ch == nil || ch.Key == "custom:LabOS" {
		t.Fatalf("key-only match: %+v %+v", res, ch)
	}
	if a.inv.CustomProfiles[defaults-1].Name != "Renamed" || a.inv.CustomProfiles[defaults-1].Prompt != "r#" {
		t.Fatal("the renamed profile was touched")
	}

	// Same name, other steps: asks, nothing changed yet.
	res, ch = a.importProfileLocked(profile.Profile{Key: "cisco-ios", Name: "Cisco IOS", Prompt: "R9#"}, "")
	if res.Imported || res.Conflict != "Cisco IOS" || res.AltName != "Cisco IOS_2" || ch != nil {
		t.Fatalf("conflict = %+v", res)
	}
	var ios profile.Profile
	for _, p := range a.inv.CustomProfiles {
		if p.Key == "cisco-ios" {
			ios = p
		}
	}
	if ios.Prompt == "R9#" {
		t.Fatal("replaced before the answer")
	}
	// Identical: nothing to ask.
	if res, _ := a.importProfileLocked(ios, ""); !res.Same {
		t.Errorf("identical import = %+v", res)
	}
	a.mu.Unlock()

	// 別名で追加 through the pending answer.
	a.mu.Lock()
	p := profile.Profile{Key: "cisco-ios", Name: "Cisco IOS", Prompt: "R9#"}
	a.pendingImport = &pendingImport{prof: &p}
	a.mu.Unlock()
	res, err := a.ResolveFileImport(importRename)
	if err != nil || res.Name != "Cisco IOS_2" {
		t.Fatalf("rename = %+v, %v", res, err)
	}
	if got := profileByName(a, "Cisco IOS_2"); got == nil || got.Key == "cisco-ios" || got.Prompt != "R9#" {
		t.Errorf("added = %+v", got)
	}
	// 置き換える keeps the key.
	a.mu.Lock()
	a.pendingImport = &pendingImport{prof: &p}
	a.mu.Unlock()
	if _, err := a.ResolveFileImport(importReplace); err != nil {
		t.Fatal(err)
	}
	if got := profileByName(a, "Cisco IOS"); got.Key != "cisco-ios" || got.Prompt != "R9#" {
		t.Errorf("replaced = %+v", got)
	}
	// やめる: nothing.
	a.mu.Lock()
	a.pendingImport = &pendingImport{prof: &profile.Profile{Key: "x", Name: "Cisco IOS", Prompt: "zz"}}
	a.mu.Unlock()
	if res, err := a.ResolveFileImport("cancel"); err != nil || res.Imported {
		t.Errorf("cancel = %+v, %v", res, err)
	}
	if got := profileByName(a, "Cisco IOS"); got.Prompt != "R9#" {
		t.Errorf("cancel changed it: %+v", got)
	}

	// A deleted default comes back under its own key without a question.
	if err := a.DeleteProfile("yamaha-rtx"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	res, ch = a.importProfileLocked(profile.Profile{Key: "yamaha-rtx", Name: "Yamaha RTX", Prompt: ">"}, "")
	a.mu.Unlock()
	if !res.Imported || ch == nil || ch.Key != "yamaha-rtx" {
		t.Errorf("restore default = %+v %+v", res, ch)
	}
}

// B6: a command-set file named like a set already there asks first.
func TestImportCommandSetAsksBeforeReplacing(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateCommandSet(model.CommandSet{Name: "lab-backup", Commands: []model.Command{{Text: "show run"}}}); err != nil {
		t.Fatal(err)
	}
	set := model.CommandSet{Name: "lab-backup", Commands: []model.Command{{Text: "show ver"}}}
	a.mu.Lock()
	res := a.importSetLocked(set, "")
	a.mu.Unlock()
	if res.Imported || res.Conflict != "lab-backup" || res.AltName != "lab-backup_2" {
		t.Fatalf("conflict = %+v", res)
	}
	a.mu.Lock()
	a.pendingImport = &pendingImport{set: &set}
	a.mu.Unlock()
	if res, err := a.ResolveFileImport(importRename); err != nil || res.Name != "lab-backup_2" {
		t.Fatalf("rename = %+v, %v", res, err)
	}
	names := map[string]string{}
	for _, cs := range a.GetInventory().CommandSets {
		names[cs.Name] = cs.Commands[0].Text
	}
	if names["lab-backup"] != "show run" || names["lab-backup_2"] != "show ver" {
		t.Errorf("sets = %v", names)
	}
	a.mu.Lock()
	a.pendingImport = &pendingImport{set: &set}
	a.mu.Unlock()
	if _, err := a.ResolveFileImport(importReplace); err != nil {
		t.Fatal(err)
	}
	for _, cs := range a.GetInventory().CommandSets {
		if cs.Name == "lab-backup" && cs.Commands[0].Text != "show ver" {
			t.Errorf("replace = %+v", cs)
		}
	}
}

// E3: the log folder is saved without quotes, and one that cannot be made is
// refused with the reason.
func TestSaveSettingsLogDir(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	s := a.GetInventory().Settings
	dir := filepath.Join(t.TempDir(), "lab logs")
	s.LogDir = ` "` + dir + `" `
	if err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if got := a.GetInventory().Settings.LogDir; got != dir {
		t.Errorf("LogDir = %q, want %q", got, dir)
	}
	s.LogDir = filepath.Join(t.TempDir(), `a<b>|c`)
	if err := a.SaveSettings(s); err == nil || !strings.Contains(err.Error(), "ログの保存先フォルダ") {
		t.Errorf("unusable folder: %v", err)
	}
	if got := a.GetInventory().Settings.LogDir; got != dir {
		t.Errorf("refused save changed LogDir to %q", got)
	}
}

// E5: a damaged vault is offered its backup (same password only), and the
// restore keeps the damaged file.
func TestUnlockOffersBackupOfDamagedVault(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDevice(model.Device{Name: "sw1", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(a.vaultPath)
	b[len(b)-1] ^= 0xff
	if err := os.WriteFile(a.vaultPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	x := NewApp()
	x.vaultPath = a.vaultPath
	if err := x.Unlock("wrong"); err == nil {
		t.Fatal("wrong password unlocked")
	}
	if x.GetUnlockInfo().BackupAt != "" {
		t.Error("a wrong password must not be offered the backup")
	}
	if _, err := x.RestoreVaultBackup("wrong"); err == nil {
		t.Error("restore with a wrong password went through")
	}
	if err := x.Unlock("master"); err == nil {
		t.Fatal("damaged vault unlocked")
	}
	if x.GetUnlockInfo().BackupAt == "" {
		t.Fatal("no backup offered")
	}
	kept, err := x.RestoreVaultBackup("master")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(kept, "vault.enc.broken-") {
		t.Errorf("kept = %q", kept)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(a.vaultPath), kept)); err != nil {
		t.Errorf("damaged file not kept: %v", err)
	}
	if x.locked() {
		t.Fatal("not unlocked after restore")
	}
}

// E6: a vault from a newer format opens read-only; saving is refused until
// the user allows it.
func TestNewerVaultOpensReadOnly(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	vault.StampVersion = model.InventoryVersion + 1
	err := a.persist()
	vault.StampVersion = model.InventoryVersion
	if err != nil {
		t.Fatal(err)
	}
	x := NewApp()
	x.vaultPath = a.vaultPath
	if err := x.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	info := x.GetUnlockInfo()
	if !info.Newer || !info.ReadOnly || info.SavedBy != appVersion {
		t.Fatalf("info = %+v", info)
	}
	before, _ := os.ReadFile(a.vaultPath)
	if err := x.SaveDevice(model.Device{Name: "sw1", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Password: "pw"}); !errors.Is(err, errReadOnly) {
		t.Fatalf("save in read-only = %v", err)
	}
	if err := x.ChangeMasterPassword("master", "next"); !errors.Is(err, errReadOnly) {
		t.Fatalf("password change in read-only = %v", err)
	}
	if after, _ := os.ReadFile(a.vaultPath); string(after) != string(before) {
		t.Fatal("the vault was written")
	}
	x.AllowOlderSave()
	if x.GetUnlockInfo().ReadOnly {
		t.Error("still read-only")
	}
	if err := x.persist(); err != nil {
		t.Fatal(err)
	}
	y := NewApp()
	y.vaultPath = a.vaultPath
	if err := y.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	if y.GetUnlockInfo().Newer {
		t.Error("saved by this version, the vault is no longer newer")
	}
}

// E10: closing stops a running batch and waits a moment for it.
func TestStopRunForExit(t *testing.T) {
	a := newTestApp(t)
	cancelled := make(chan struct{})
	a.mu.Lock()
	a.runActive = true
	a.runCancel = func() {
		close(cancelled)
		go func() {
			time.Sleep(100 * time.Millisecond)
			a.mu.Lock()
			a.runActive = false
			a.mu.Unlock()
		}()
	}
	a.mu.Unlock()
	if !a.stopRunForExit(3 * time.Second) {
		t.Fatal("did not see the batch end")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("the batch was not cancelled")
	}
	// One that never ends: given up on after the wait.
	a.mu.Lock()
	a.runActive, a.runCancel = true, func() {}
	a.mu.Unlock()
	start := time.Now()
	if a.stopRunForExit(200*time.Millisecond) || time.Since(start) > 2*time.Second {
		t.Error("waited wrongly for a batch that does not end")
	}
}

// E4: a panic inside a batch frees the batch slot and reports an error
// instead of ending the process.
func TestExecBatchRecoversPanic(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "error.log")
	old := errorLogPath
	errorLogPath = func() string { return logFile }
	defer func() { errorLogPath = old }()
	a := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.runActive = true
	a.mu.Unlock()
	// A nil inventory makes the runner dereference nil.
	done := a.execBatch(ctx, cancel, nil, RunRequest{Group: "lab"}, "", "", func(runpkg.Event) {})
	if done.Error == "" {
		t.Fatal("no error reported")
	}
	a.mu.Lock()
	active := a.runActive
	a.mu.Unlock()
	if active {
		t.Error("batch slot not freed")
	}
	if b, err := os.ReadFile(logFile); err != nil || !strings.Contains(string(b), "execBatch") {
		t.Errorf("error log = %q, %v", b, err)
	}
}

// E4: a bound method that panics returns instead of ending the process.
func TestRecoverBound(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "error.log")
	old := errorLogPath
	errorLogPath = func() string { return logFile }
	defer func() { errorLogPath = old }()
	f := func() (err error) {
		defer recoverBound(nil, "lab")
		var m map[string]int
		m["x"] = 1
		return nil
	}
	if err := f(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(logFile); !strings.Contains(string(b), "panic in lab") {
		t.Errorf("error log = %q", b)
	}
}

// E7: the data folder's lock is held exclusively; a second claim is refused
// with the holder, and the lock is free again once the holder lets go.
func TestDataLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	h, line, err := acquireDataLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "|") {
		t.Errorf("holder line = %q", line)
	}
	_, holder, err := acquireDataLock(dir)
	if !errors.Is(err, errDataLocked) {
		t.Fatalf("second claim = %v", err)
	}
	if holder != line {
		t.Errorf("holder = %q, want %q", holder, line)
	}
	if lockHolderText(holder) == "" {
		t.Error("no holder text")
	}
	syscall.CloseHandle(h)
	h2, _, err := acquireDataLock(dir)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	syscall.CloseHandle(h2)
}
