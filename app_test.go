package main

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// newTestApp builds an App backed by a throwaway vault file.
func newTestApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	a.vaultPath = filepath.Join(t.TempDir(), "vault.enc")
	return a
}

func TestAppVaultAndCRUD(t *testing.T) {
	a := newTestApp(t)

	if a.VaultExists() {
		t.Fatal("fresh temp dir should have no vault")
	}
	if err := a.CreateVault("master"); err != nil {
		t.Fatalf("create vault: %v", err)
	}
	if !a.VaultExists() {
		t.Fatal("vault should exist after create")
	}

	dev := model.Device{Name: "sw1", Host: "10.0.0.1", Conn: model.ConnSSH,
		OSType: "cisco-ios", Username: "admin", Password: "pw", Enabled: true}
	if err := a.SaveDevice(dev); err != nil {
		t.Fatalf("save device: %v", err)
	}
	if err := a.SaveCommandSet(model.CommandSet{Name: "bk",
		Commands: []model.Command{{Text: "show version"}}}); err != nil {
		t.Fatalf("save cmdset: %v", err)
	}

	// Re-open with a fresh App instance to prove persistence + encryption.
	a2 := NewApp()
	a2.vaultPath = a.vaultPath
	if err := a2.Unlock("master"); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	inv := a2.GetInventory()
	if len(inv.Devices) != 1 || inv.Devices[0].Password != "pw" {
		t.Fatalf("device not persisted: %+v", inv.Devices)
	}
	// A new vault is seeded with the built-in default sets plus the saved one.
	wantSets := len(model.DefaultCommandSets()) + 1
	if len(inv.CommandSets) != wantSets {
		t.Fatalf("command sets = %d, want %d", len(inv.CommandSets), wantSets)
	}
	if inv.CommandSets[len(inv.CommandSets)-1].Name != "bk" {
		t.Fatalf("saved command set not persisted: %+v", inv.CommandSets)
	}

	if err := a2.DeleteDevice("sw1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(a2.GetInventory().Devices) != 0 {
		t.Fatal("device not deleted")
	}
}

func TestAppUnlockWrongPassword(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("right"); err != nil {
		t.Fatal(err)
	}
	a2 := NewApp()
	a2.vaultPath = a.vaultPath
	if err := a2.Unlock("wrong"); err == nil {
		t.Fatal("unlock with wrong password should fail")
	}
}

func TestProfilesExposed(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if len(a.ListProfiles()) != 14 {
		t.Fatalf("expected 14 profiles exposed to UI, got %d", len(a.ListProfiles()))
	}
}

// A vault from before profiles-as-data gets the 14 defaults seeded exactly
// once on unlock; a default the user then deletes stays deleted.
func TestProfileSeedingAndDeletion(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-seeding vault: strip the profiles and the flag.
	a.mu.Lock()
	a.inv.CustomProfiles = nil
	a.inv.ProfilesSeeded = false
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		t.Fatal(err)
	}

	a2 := NewApp()
	a2.vaultPath = a.vaultPath
	if err := a2.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	if n := len(a2.GetInventory().CustomProfiles); n != 14 {
		t.Fatalf("unlock should seed 14 default profiles, got %d", n)
	}
	if err := a2.DeleteProfile("yamaha-rtx"); err != nil {
		t.Fatalf("defaults should be deletable: %v", err)
	}

	a3 := NewApp()
	a3.vaultPath = a.vaultPath
	if err := a3.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	if a3.profiles.Has("yamaha-rtx") {
		t.Fatal("deleted default must not be reseeded on the next unlock")
	}
	if n := len(a3.GetInventory().CustomProfiles); n != 13 {
		t.Fatalf("profiles after deletion = %d, want 13", n)
	}
}

func TestCopyCommandSet(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	cs := model.CommandSet{Name: "backup", Commands: []model.Command{
		{Text: "show run", PauseSec: 1, SerialSec: 2},
	}}
	if err := a.SaveCommandSet(cs); err != nil {
		t.Fatal(err)
	}
	nm, err := a.CopyCommandSet("backup")
	if err != nil {
		t.Fatal(err)
	}
	if nm != "backup_copy" {
		t.Errorf("copy name = %q, want backup_copy", nm)
	}
	// Copying again picks the next free suffix.
	nm2, err := a.CopyCommandSet("backup")
	if err != nil {
		t.Fatal(err)
	}
	if nm2 != "backup_copy2" {
		t.Errorf("second copy name = %q, want backup_copy2", nm2)
	}
	inv := a.GetInventory()
	wantSets := len(model.DefaultCommandSets()) + 3
	if len(inv.CommandSets) != wantSets {
		t.Fatalf("command sets = %d, want %d", len(inv.CommandSets), wantSets)
	}
	// The copy owns its own command slice (no aliasing with the original).
	var orig, dup *model.CommandSet
	for i := range inv.CommandSets {
		switch inv.CommandSets[i].Name {
		case "backup":
			orig = &inv.CommandSets[i]
		case "backup_copy":
			dup = &inv.CommandSets[i]
		}
	}
	dup.Commands[0].Text = "changed"
	if orig.Commands[0].Text != "show run" {
		t.Error("copy shares the command slice with the original")
	}
	if _, err := a.CopyCommandSet("nosuch"); err == nil {
		t.Error("copying a missing set should fail")
	}
}

// Profiles are plain text: patterns are QuoteMeta'd on registration, seeded
// defaults are editable like any other profile, and CopyProfile duplicates.
func TestProfileEditCopyPlainText(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	plain := profile.Profile{Name: "plain", Prompt: "sw#",
		Login: []profile.Step{{Expect: "Password:", Send: "{password}"}}}
	if err := a.SaveProfile(plain); err != nil {
		t.Fatal(err)
	}
	if got := a.profiles.Get("custom:plain").Prompt; got != regexp.QuoteMeta("sw#") {
		t.Errorf("plain prompt = %q, want QuoteMeta'd", got)
	}
	// A name colliding with another profile is rejected.
	dupName := profile.Profile{Name: "Cisco IOS", Prompt: "#"}
	if err := a.SaveProfile(dupName); err == nil {
		t.Error("saving a new profile under an existing name should fail")
	}
	// Editing a seeded default sticks and survives the vault round-trip.
	edited := profile.Profile{Key: "cisco-ios", Name: "Cisco IOS", Prompt: "R1#"}
	if err := a.SaveProfile(edited); err != nil {
		t.Fatalf("defaults should be editable: %v", err)
	}
	a2 := NewApp()
	a2.vaultPath = a.vaultPath
	if err := a2.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	if got := a2.profiles.Get("cisco-ios").Prompt; got != regexp.QuoteMeta("R1#") {
		t.Errorf("edited default prompt = %q, want quoted R1#", got)
	}
	// Duplicate a default; the copy gets a custom key and _copy name.
	nm, err := a2.CopyProfile("cisco-ios")
	if err != nil {
		t.Fatal(err)
	}
	if nm != "Cisco IOS_copy" {
		t.Errorf("copy name = %q, want Cisco IOS_copy", nm)
	}
	if !a2.profiles.Has("custom:Cisco IOS_copy") {
		t.Error("copied profile not registered")
	}
	// In-use protection: a device on the profile blocks deletion.
	if err := a2.SaveDevice(model.Device{Name: "sw1", Host: "10.0.0.1",
		Conn: model.ConnSSH, OSType: "cisco-ios", Username: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := a2.DeleteProfile("cisco-ios"); err == nil {
		t.Error("deleting an in-use profile should fail")
	}
	if err := a2.DeleteProfile("custom:Cisco IOS_copy"); err != nil {
		t.Errorf("deleting an unused copy should work: %v", err)
	}
}

// Changing the master password re-encrypts the vault; reset deletes it.
func TestChangeMasterPasswordAndReset(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("old"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDevice(model.Device{Name: "sw1", Host: "10.0.0.1",
		Conn: model.ConnSSH, OSType: "cisco-ios", Username: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := a.ChangeMasterPassword("wrong", "new"); err == nil {
		t.Fatal("wrong current password should be rejected")
	}
	if err := a.ChangeMasterPassword("old", ""); err == nil {
		t.Fatal("empty new password should be rejected")
	}
	if err := a.ChangeMasterPassword("old", "new"); err != nil {
		t.Fatal(err)
	}
	a2 := NewApp()
	a2.vaultPath = a.vaultPath
	if err := a2.Unlock("old"); err == nil {
		t.Fatal("old password should no longer unlock")
	}
	if err := a2.Unlock("new"); err != nil {
		t.Fatalf("new password should unlock: %v", err)
	}
	if len(a2.GetInventory().Devices) != 1 {
		t.Fatal("data lost across password change")
	}
	if err := a2.ResetVault("wrong"); err == nil {
		t.Fatal("reset with a wrong master password should be rejected")
	}
	if !a2.VaultExists() {
		t.Fatal("vault must survive a rejected reset")
	}
	if err := a2.ResetVault("new"); err != nil {
		t.Fatal(err)
	}
	if a2.VaultExists() {
		t.Fatal("vault file should be gone after reset")
	}
	if a2.GetInventory() != nil {
		t.Fatal("inventory should be locked out after reset")
	}
}

// Reordering rewrites only the listed items' relative order; everything else
// stays in place (a group view must not disturb other groups' devices).
func TestReorderDevicesWithinScope(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	add := func(name, group string) {
		if err := a.SaveDevice(model.Device{Name: name, Host: "10.0.0.1",
			Conn: model.ConnSSH, OSType: "cisco-ios", Group: group}); err != nil {
			t.Fatal(err)
		}
	}
	add("a1", "A")
	add("b1", "B")
	add("a2", "A")
	add("b2", "B")
	add("a3", "A")
	// Reorder group A (a3, a1, a2) — B devices must not move.
	if err := a.ReorderDevices([]string{"a3", "a1", "a2"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range a.GetInventory().Devices {
		got = append(got, d.Name)
	}
	want := []string{"a3", "b1", "a1", "b2", "a2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	// A stale order (unknown name) must be ignored, not corrupt the list.
	if err := a.ReorderDevices([]string{"nosuch", "a1"}); err != nil {
		t.Fatal(err)
	}
	if a.GetInventory().Devices[0].Name != "a3" {
		t.Fatal("stale reorder should be a no-op")
	}
}

// ListProfiles follows the vault order, so ReorderProfiles also reorders the
// OS-type dropdowns.
func TestReorderProfilesDrivesListOrder(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	list := a.ListProfiles()
	rev := make([]string, len(list))
	for i, p := range list {
		rev[len(list)-1-i] = p.Key
	}
	if err := a.ReorderProfiles(rev); err != nil {
		t.Fatal(err)
	}
	after := a.ListProfiles()
	if after[0].Key != rev[0] || after[len(after)-1].Key != rev[len(rev)-1] {
		t.Fatalf("ListProfiles does not follow reordered vault: first=%s last=%s",
			after[0].Key, after[len(after)-1].Key)
	}
}

// TestNecIXPagerMigration covers the upgrade of a vault seeded before the NEC
// IX pager fix. On that hardware "terminal length 0" is only valid inside
// configure mode — in operation mode the device answers "% terminal --
// Invalid command." and keeps paging, so the first long output hangs the run
// at "--More--". Unlocking must repair a pristine profile and leave an edited
// one untouched.
func TestNecIXPagerMigration(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	// Rewind both NEC profiles to how they were seeded before the fix.
	a.mu.Lock()
	for i := range a.inv.CustomProfiles {
		p := &a.inv.CustomProfiles[i]
		if p.Key == "nec-ix" || p.Key == "nec-wa" {
			p.MorePrompt = ""
			p.Pager = []profile.Step{{Send: "terminal length 0"}}
			p.Disconnect = []profile.Step{{Send: "exit"}}
		}
	}
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		t.Fatal(err)
	}

	a2 := NewApp()
	a2.vaultPath = a.vaultPath
	if err := a2.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	find := func(app *App, key string) profile.Profile {
		for _, p := range app.GetInventory().CustomProfiles {
			if p.Key == key {
				return p
			}
		}
		t.Fatalf("profile %q missing", key)
		return profile.Profile{}
	}
	ix := find(a2, "nec-ix")
	if ix.MorePrompt != "--More--" {
		t.Errorf("MorePrompt = %q, want --More--", ix.MorePrompt)
	}
	if len(ix.Pager) != 2 || ix.Pager[0].Send != "svintr-config" || ix.Pager[1].Send != "terminal length 0" {
		t.Errorf("pager = %+v, want svintr-config then terminal length 0", ix.Pager)
	}
	if len(ix.Disconnect) != 2 {
		t.Errorf("disconnect = %+v, want two exits (configure mode then session)", ix.Disconnect)
	}
	// The migration is keyed to nec-ix only: NEC WA is a different product and
	// must not be dragged into configure mode alongside it.
	if wa := find(a2, "nec-wa"); wa.MorePrompt != "" || len(wa.Pager) != 1 {
		t.Errorf("nec-wa must be untouched, got morePrompt=%q pager=%+v", wa.MorePrompt, wa.Pager)
	}

	// A profile the user has edited keeps exactly what they wrote.
	custom := []profile.Step{{Send: "my-own-pager-command"}}
	a3 := NewApp()
	a3.vaultPath = a.vaultPath
	if err := a3.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	a3.mu.Lock()
	for i := range a3.inv.CustomProfiles {
		if a3.inv.CustomProfiles[i].Key == "nec-ix" {
			a3.inv.CustomProfiles[i].MorePrompt = ""
			a3.inv.CustomProfiles[i].Pager = custom
		}
	}
	a3.mu.Unlock()
	if err := a3.persist(); err != nil {
		t.Fatal(err)
	}
	a4 := NewApp()
	a4.vaultPath = a.vaultPath
	if err := a4.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	if got := find(a4, "nec-ix"); len(got.Pager) != 1 || got.Pager[0].Send != "my-own-pager-command" {
		t.Errorf("user-edited pager was overwritten: %+v", got.Pager)
	}
}

// TestNecIXPagerMigrationFromInterim covers the vault of an intermediate build
// that entered configure mode with plain "configure": that fails outright with
// "% CONFIG process is occupied." whenever another session holds the mode, so
// it is upgraded to svintr-config too.
func TestNecIXPagerMigrationFromInterim(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	for i := range a.inv.CustomProfiles {
		if a.inv.CustomProfiles[i].Key == "nec-ix" {
			a.inv.CustomProfiles[i].MorePrompt = ""
			a.inv.CustomProfiles[i].Pager = []profile.Step{{Send: "configure"}, {Send: "terminal length 0"}}
		}
	}
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		t.Fatal(err)
	}
	a2 := NewApp()
	a2.vaultPath = a.vaultPath
	if err := a2.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	for _, p := range a2.GetInventory().CustomProfiles {
		if p.Key != "nec-ix" {
			continue
		}
		if len(p.Pager) != 2 || p.Pager[0].Send != "svintr-config" {
			t.Fatalf("pager = %+v, want svintr-config first", p.Pager)
		}
		if p.MorePrompt != "--More--" {
			t.Fatalf("MorePrompt = %q, want --More--", p.MorePrompt)
		}
		return
	}
	t.Fatal("nec-ix profile missing")
}
