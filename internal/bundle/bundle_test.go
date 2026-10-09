package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

func sampleInventory() *model.Inventory {
	return &model.Inventory{
		Devices: []model.Device{
			{Name: "a-rt1", Host: "192.0.2.1", Conn: model.ConnSSH, Group: "A社", OSType: "cisco-ios", CommandSet: "backup", Username: "u", Password: "p", Enabled: true},
			{Name: "a-fw1", Host: "192.0.2.2", Conn: model.ConnSSH, Group: "A社", OSType: "custom:MyOS", CommandSet: "fw-check", Enabled: true,
				Bastions: []model.Bastion{{Host: "192.0.2.9", Method: model.BastionSSH, Username: "j", JumpCommand: "ssh -l {user} {host}", LegacyAlgos: true}}},
			{Name: "b-sw1", Host: "192.0.2.3", Conn: model.ConnTelnet, Group: "B社", OSType: "generic", CommandSet: "backup", Enabled: true},
		},
		CommandSets: []model.CommandSet{
			{Name: "backup", Commands: []model.Command{{Text: "show run", PauseSec: 1, SerialSec: 2}}},
			{Name: "fw-check", Commands: []model.Command{{Text: "get system status", PauseSec: 1, SerialSec: 1}}},
			{Name: "unused", Commands: []model.Command{{Text: "show ver", PauseSec: 1, SerialSec: 1}}},
		},
		CustomProfiles: []profile.Profile{
			{Key: "cisco-ios", Name: "Cisco IOS", Prompt: "#"},
			{Key: "generic", Name: "Generic", Prompt: "$"},
			{Key: "custom:MyOS", Name: "MyOS", Prompt: ">"},
		},
		DeviceGroups: []model.DeviceGroup{{Name: "A社"}, {Name: "B社"}},
	}
}

func names(c Contents) (devs, sets, profs []string) {
	for _, d := range c.Devices {
		devs = append(devs, d.Name)
	}
	for _, s := range c.CommandSets {
		sets = append(sets, s.Name)
	}
	for _, p := range c.Profiles {
		profs = append(profs, p.Key)
	}
	return
}

func TestSelectScopesToGroupAndReferences(t *testing.T) {
	c := Select(sampleInventory(), "A社")
	devs, sets, profs := names(c)
	if strings.Join(devs, ",") != "a-rt1,a-fw1" {
		t.Errorf("devices = %v", devs)
	}
	if strings.Join(sets, ",") != "backup,fw-check" {
		t.Errorf("sets = %v (unused set must not travel, B社's set only if shared)", sets)
	}
	if strings.Join(profs, ",") != "cisco-ios,custom:MyOS" {
		t.Errorf("profiles = %v", profs)
	}
	// Whole inventory: everything, including the unused set.
	all := Select(sampleInventory(), "")
	_, sets, profs = names(all)
	if len(all.Devices) != 3 || len(sets) != 3 || len(profs) != 3 {
		t.Errorf("all-devices bundle should be complete: %d devices %v %v", len(all.Devices), sets, profs)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "A社")
	src := Select(sampleInventory(), "A社")
	mp, err := Write(dir, src, "PalaTerm test")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(mp) != ManifestName {
		t.Errorf("manifest path = %s", mp)
	}
	for _, f := range []string{"devices.csv", "command-sets/backup.csv", "command-sets/fw-check.csv", "os-profiles/Cisco IOS.json", "os-profiles/MyOS.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	got, err := Read(mp)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest.Group != "A社" || got.Manifest.Format != Format || got.Manifest.DeviceCount != 2 {
		t.Errorf("manifest = %+v", got.Manifest)
	}
	devs, sets, profs := names(got)
	if strings.Join(devs, ",") != "a-rt1,a-fw1" || strings.Join(sets, ",") != "backup,fw-check" || strings.Join(profs, ",") != "cisco-ios,custom:MyOS" {
		t.Errorf("round trip lost items: %v %v %v", devs, sets, profs)
	}
	// The CSV is complete: jump command and legacy flag on the hop survive.
	b := got.Devices[1].Bastions
	if len(b) != 1 || b[0].JumpCommand != "ssh -l {user} {host}" || !b[0].LegacyAlgos {
		t.Errorf("bastion extras lost: %+v", b)
	}
	if got.CommandSets[0].Commands[0].SerialSec != 2 {
		t.Errorf("command pauses lost: %+v", got.CommandSets[0].Commands)
	}
}

func TestWriteDisambiguatesFileNames(t *testing.T) {
	dir := t.TempDir()
	c := Contents{CommandSets: []model.CommandSet{
		{Name: "a/b", Commands: []model.Command{{Text: "x"}}},
		{Name: "a_b", Commands: []model.Command{{Text: "y"}}},
	}}
	mp, err := Write(dir, c, "t")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(mp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.CommandSets) != 2 || got.CommandSets[0].Name != "a/b" || got.CommandSets[1].Name != "a_b" ||
		got.CommandSets[0].Commands[0].Text != "x" || got.CommandSets[1].Commands[0].Text != "y" {
		t.Errorf("names/contents mixed up: %+v", got.CommandSets)
	}
}

func TestReadRejectsEscapingPaths(t *testing.T) {
	dir := t.TempDir()
	mp := filepath.Join(dir, ManifestName)
	os.WriteFile(mp, []byte(`{"format":"palaterm-bundle/1","devices":"../outside.csv"}`), 0o644)
	if _, err := Read(mp); err == nil {
		t.Fatal("expected error for path outside the bundle folder")
	}
	os.WriteFile(mp, []byte(`{"format":"something-else"}`), 0o644)
	if _, err := Read(mp); err == nil {
		t.Fatal("expected error for foreign manifest")
	}
}

func TestApplySkipsSharedByDefaultAndRemapsProfileKeys(t *testing.T) {
	// Target vault already has a "backup" set and a profile *named* MyOS under
	// a different key; the bundle's a-fw1 references the bundle's key.
	inv := &model.Inventory{
		CommandSets:    []model.CommandSet{{Name: "backup", Commands: []model.Command{{Text: "old"}}}},
		CustomProfiles: []profile.Profile{{Key: "custom:MyOS-2", Name: "myos", Prompt: "%"}},
	}
	c := Select(sampleInventory(), "A社")
	pre := Preview(inv, c, Options{})
	if pre.DevicesNew != 2 || pre.SetsNew != 1 || pre.SetsSkipped != 1 || pre.ProfilesNew != 1 || pre.ProfilesSkip != 1 {
		t.Errorf("preview = %+v", pre)
	}
	if len(inv.Devices) != 0 || inv.CommandSets[0].Commands[0].Text != "old" {
		t.Fatal("Preview must not modify the inventory")
	}
	s, changed := Apply(inv, c, Options{})
	if s.DevicesNew != 2 || s.SetsSkipped != 1 || s.ProfilesSkip != 1 {
		t.Errorf("apply = %+v", s)
	}
	if inv.CommandSets[0].Commands[0].Text != "old" {
		t.Error("existing shared set was overwritten without OverwriteShared")
	}
	if len(changed) != 1 || changed[0].Key != "cisco-ios" {
		t.Errorf("changed profiles = %+v", changed)
	}
	// a-fw1 now points at the existing key, not at the bundle's custom:MyOS.
	var fw model.Device
	for _, d := range inv.Devices {
		if d.Name == "a-fw1" {
			fw = d
		}
	}
	if fw.OSType != "custom:MyOS-2" {
		t.Errorf("OSType not remapped: %q", fw.OSType)
	}
	if strings.Join(s.GroupsCreated, ",") != "A社" {
		t.Errorf("groups created = %v", s.GroupsCreated)
	}
	if len(inv.DeviceGroups) != 1 || inv.DeviceGroups[0].Name != "A社" {
		t.Errorf("group entry missing: %+v", inv.DeviceGroups)
	}
	if len(s.MissingSets) != 0 || len(s.MissingProfiles) != 0 {
		t.Errorf("nothing should be missing: %+v", s)
	}
}

func TestApplyOverwriteSharedAndTargetGroup(t *testing.T) {
	inv := sampleInventory()
	c := Select(sampleInventory(), "A社")
	c.CommandSets[0].Commands = []model.Command{{Text: "show run brief"}}
	c.Profiles[0].Prompt = "#>"
	c.Devices[0].Host = "192.0.2.100"
	s, changed := Apply(inv, c, Options{TargetGroup: "C社", OverwriteShared: true})
	if s.DevicesUpdated != 2 || s.DevicesNew != 0 || s.SetsUpdated != 2 || s.ProfilesUpd != 2 {
		t.Errorf("summary = %+v", s)
	}
	if len(changed) != 2 {
		t.Errorf("changed = %d", len(changed))
	}
	for _, d := range inv.Devices {
		if (d.Name == "a-rt1" || d.Name == "a-fw1") && d.Group != "C社" {
			t.Errorf("%s group = %q, want C社", d.Name, d.Group)
		}
		if d.Name == "a-rt1" && d.Host != "192.0.2.100" {
			t.Errorf("device not replaced: %+v", d)
		}
	}
	if inv.CommandSets[0].Commands[0].Text != "show run brief" || inv.CustomProfiles[0].Prompt != "#>" {
		t.Error("shared items not overwritten")
	}
	if strings.Join(s.GroupsCreated, ",") != "C社" {
		t.Errorf("groups created = %v", s.GroupsCreated)
	}
}

func TestApplyReportsMissingReferences(t *testing.T) {
	inv := &model.Inventory{}
	c := Contents{Devices: []model.Device{{Name: "x", OSType: "nope", CommandSet: "gone"}}}
	s, _ := Apply(inv, c, Options{})
	if strings.Join(s.MissingSets, ",") != "gone" || strings.Join(s.MissingProfiles, ",") != "nope" {
		t.Errorf("missing = %+v", s)
	}
}

func TestParseCommandSetLegacyList(t *testing.T) {
	cmds, err := ParseCommandSet("# comment\r\nshow run\r\nshow ip int brief,2,3\r\nterminal length 0, 1\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 3 || cmds[0].PauseSec != 1 || cmds[1].PauseSec != 2 || cmds[1].SerialSec != 3 || cmds[2].Text != "terminal length 0, 1" {
		t.Errorf("parsed = %+v", cmds)
	}
	if _, err := ParseCommandSet("# only comments\n"); err == nil {
		t.Error("expected error for no commands")
	}
}

func TestGroupCredentialsTravelInBundle(t *testing.T) {
	inv := sampleInventory()
	inv.DeviceGroups[0] = model.DeviceGroup{Name: "A社", Username: "gu", Password: "gp", EnablePassword: "ge"}
	inv.Devices[0].UseGroupCreds = true
	dir := t.TempDir()
	if _, err := Write(dir, Select(inv, "A社"), "test"); err != nil {
		t.Fatal(err)
	}
	c, err := Read(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Manifest.Groups) != 1 || c.Manifest.Groups[0].Password != "gp" {
		t.Fatalf("groups in manifest: %+v", c.Manifest.Groups)
	}
	if !c.Devices[0].UseGroupCreds || c.Devices[1].UseGroupCreds {
		t.Fatalf("useGroupCreds lost in CSV: %+v", c.Devices)
	}
	// Into an empty inventory: the group is created with its credentials.
	empty := &model.Inventory{}
	Apply(empty, c, Options{})
	if g := empty.Group("A社"); g == nil || g.Username != "gu" || g.EnablePassword != "ge" {
		t.Fatalf("group not created with creds: %+v", empty.DeviceGroups)
	}
	if r := empty.ResolveCredentials(empty.Devices[0]); r.Password != "gp" {
		t.Fatalf("resolve: %+v", r)
	}
	// Into an inventory that has the group: kept unless OverwriteShared.
	have := &model.Inventory{DeviceGroups: []model.DeviceGroup{{Name: "A社", Username: "old", Password: "old"}}}
	Apply(have, c, Options{})
	if have.Group("A社").Password != "old" {
		t.Fatalf("existing group creds overwritten without the option")
	}
	Apply(have, c, Options{OverwriteShared: true})
	if have.Group("A社").Password != "gp" {
		t.Fatalf("existing group creds not overwritten with the option")
	}
	// Re-targeted single-group bundle: the target group gets the creds.
	tg := &model.Inventory{}
	Apply(tg, c, Options{TargetGroup: "C社"})
	if g := tg.Group("C社"); g == nil || g.Password != "gp" {
		t.Fatalf("target group creds: %+v", tg.DeviceGroups)
	}
}
