package main

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// 2026-10-18 usability review: a new command set never lands on an
// existing name (SaveCommandSet replaces by name, and the editor's 追加
// used to go through it).
func TestCreateCommandSetRefusesDuplicate(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	cs := model.CommandSet{Name: "backup", Commands: []model.Command{{Text: "show run"}, {Text: "show ver"}}}
	if err := a.CreateCommandSet(cs); err != nil {
		t.Fatal(err)
	}
	dup := model.CommandSet{Name: "backup", Commands: []model.Command{{Text: "show clock"}}}
	if err := a.CreateCommandSet(dup); err == nil || !strings.Contains(err.Error(), "既にあります") {
		t.Fatalf("duplicate name accepted: %v", err)
	}
	var got *model.CommandSet
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == "backup" {
			got = &a.inv.CommandSets[i]
		}
	}
	if got == nil || len(got.Commands) != 2 {
		t.Fatalf("the existing set was replaced: %+v", got)
	}
	if err := a.CreateCommandSet(model.CommandSet{Name: "  "}); err == nil {
		t.Error("blank name accepted")
	}
}

// A bulk edit reports how many devices had the jump host the credentials
// were set on, so a rotation that missed devices is seen in the toast.
func TestUpdateDevicesReportsHops(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []model.Device{
		{Name: "r1", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios", Enabled: true,
			Bastions: []model.Bastion{{Host: "192.0.2.9", Method: model.BastionSSH, Username: "j", Password: "jpw"}}},
		{Name: "r2", Host: "192.0.2.2", Conn: model.ConnSSH, OSType: "cisco-ios", Enabled: true},
		{Name: "r3", Host: "192.0.2.3", Conn: model.ConnSSH, OSType: "cisco-ios", Enabled: true},
	} {
		if err := a.SaveDevice(d); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{a.inv.Devices[0].ID, a.inv.Devices[1].ID, a.inv.Devices[2].ID}
	hop, bpw := 1, "newjpw"
	rep, err := a.UpdateDevices(ids, DevicePatch{BastionHop: &hop, BastionPassword: &bpw})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Devices != 3 || rep.HopApplied != 1 || rep.HopMissing != 2 {
		t.Errorf("report = %+v, want 3 devices, 1 applied, 2 missing", rep)
	}
	site := "HQ"
	rep, err = a.UpdateDevices(ids, DevicePatch{Site: &site})
	if err != nil || rep.Devices != 3 || rep.HopApplied != 0 || rep.HopMissing != 0 {
		t.Errorf("site-only report = %+v (%v)", rep, err)
	}
}

// A vault created under the English UI gets English sample names; a
// Japanese one keeps the Japanese ones.
func TestSampleNamesFollowLanguage(t *testing.T) {
	en := model.LocalizeSampleSets(model.DefaultCommandSets(), "en")
	for _, cs := range en {
		if strings.Contains(cs.Name, "【サンプル】") {
			t.Errorf("English vault seeded with %q", cs.Name)
		}
		if !strings.HasPrefix(cs.Name, "[Sample] ") {
			t.Errorf("unexpected English sample name %q", cs.Name)
		}
	}
	ja := model.LocalizeSampleSets(model.DefaultCommandSets(), "ja")
	for _, cs := range ja {
		if !strings.HasPrefix(cs.Name, "【サンプル】") {
			t.Errorf("Japanese sample renamed: %q", cs.Name)
		}
	}
	if model.SampleGroupName("en") != "Sample group" || model.SampleGroupName("ja") != "サンプルグループ" {
		t.Error("sample group name")
	}
	user := model.LocalizeSampleSets([]model.CommandSet{{Name: "my set"}}, "en")
	if user[0].Name != "my set" {
		t.Error("a user's set was renamed")
	}
	a := newTestApp(t)
	a.SetLang("en")
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	if a.inv.DeviceGroups[0].Name != "Sample group" || !strings.HasPrefix(a.inv.CommandSets[0].Name, "[Sample] ") {
		t.Errorf("English vault: group %q set %q", a.inv.DeviceGroups[0].Name, a.inv.CommandSets[0].Name)
	}
}

// The terminal windows cascade instead of stacking on one spot.
func TestCascadeOffset(t *testing.T) {
	if cascadeOffset(0) != 0 || cascadeOffset(1) != cascadeStep || cascadeOffset(3) != 3*cascadeStep {
		t.Error("cascade offsets")
	}
	if cascadeOffset(8) != 0 || cascadeOffset(-2) != 0 {
		t.Error("cascade wraps and tolerates garbage")
	}
}

// The unlock error is said in the UI's words, not the vault package's.
func TestUnlockErrorWording(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.Lock()
	err := a.Unlock("wrong")
	if err == nil || !strings.Contains(err.Error(), "パスワードが違うか") {
		t.Errorf("unlock error = %v", err)
	}
}
