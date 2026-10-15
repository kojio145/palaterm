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
	c := Select(sampleInventory(), "A社", false)
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
	all := Select(sampleInventory(), "", false)
	_, sets, profs = names(all)
	if len(all.Devices) != 3 || len(sets) != 3 || len(profs) != 3 {
		t.Errorf("all-devices bundle should be complete: %d devices %v %v", len(all.Devices), sets, profs)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "A社")
	src := Select(sampleInventory(), "A社", false)
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
	c := Select(sampleInventory(), "A社", false)
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
	c := Select(sampleInventory(), "A社", false)
	c.CommandSets[0].Commands = []model.Command{{Text: "show run brief"}}
	c.Profiles[0].Prompt = "#>"
	c.Devices[0].Host = "192.0.2.100"
	s, changed := Apply(inv, c, Options{TargetGroup: "C社", OverwriteShared: true})
	// Devices match by group + name, so importing A社's rows into C社 adds them
	// there; A社's own devices stay as they were.
	if s.DevicesUpdated != 0 || s.DevicesNew != 2 || s.SetsUpdated != 2 || s.ProfilesUpd != 2 {
		t.Errorf("summary = %+v", s)
	}
	if len(changed) != 2 {
		t.Errorf("changed = %d", len(changed))
	}
	inC, inA := 0, 0
	for _, d := range inv.Devices {
		if d.Name != "a-rt1" && d.Name != "a-fw1" {
			continue
		}
		switch d.Group {
		case "C社":
			inC++
			if d.Name == "a-rt1" && d.Host != "192.0.2.100" {
				t.Errorf("imported row not applied: %+v", d)
			}
		case "A社":
			inA++
			if d.Name == "a-rt1" && d.Host == "192.0.2.100" {
				t.Errorf("A社's device was overwritten: %+v", d)
			}
		}
		if d.ID == "" {
			t.Errorf("%s has no ID", d.Name)
		}
	}
	if inC != 2 || inA != 2 {
		t.Errorf("devices in C社 = %d, A社 = %d; want 2 and 2", inC, inA)
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
	c := Contents{Devices: []model.Device{{Name: "x", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "nope", CommandSet: "gone"}}}
	s, _ := Apply(inv, c, Options{})
	if strings.Join(s.MissingSets, ",") != "gone" || strings.Join(s.MissingProfiles, ",") != "nope" {
		t.Errorf("missing = %+v", s)
	}
}

func TestParseCommandSetLegacyList(t *testing.T) {
	set, err := ParseCommandSet("# comment\r\nshow run\r\nshow ip int brief,2,3\r\nterminal length 0, 1\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	cmds := set.Commands
	if set.PauseOff || len(cmds) != 3 || cmds[0].PauseSec != 1 || cmds[1].PauseSec != 2 || cmds[1].SerialSec != 3 || cmds[2].Text != "terminal length 0, 1" {
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
	if _, err := Write(dir, Select(inv, "A社", false), "test"); err != nil {
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

// An all-devices CSV can hold the same name in two groups: both rows import
// as their own devices, and re-importing updates each in place.
func TestApplySameNameInTwoGroups(t *testing.T) {
	inv := &model.Inventory{}
	row := func(group, host string) model.Device {
		return model.Device{Name: "sw1", Group: group, Host: host, Conn: model.ConnSSH, OSType: "cisco-ios"}
	}
	c := Contents{Devices: []model.Device{row("A社", "192.0.2.1"), row("B社", "192.0.2.2")}}
	if s, _ := Apply(inv, c, Options{}); s.DevicesNew != 2 || s.DevicesUpdated != 0 {
		t.Fatalf("first import = %+v", s)
	}
	ids := map[string]string{}
	for _, d := range inv.Devices {
		ids[d.Group] = d.ID
	}
	c.Devices[1].Host = "192.0.2.22"
	if s, _ := Apply(inv, c, Options{}); s.DevicesNew != 0 || s.DevicesUpdated != 2 {
		t.Fatalf("re-import = %+v", s)
	}
	if len(inv.Devices) != 2 {
		t.Fatalf("devices = %d", len(inv.Devices))
	}
	for _, d := range inv.Devices {
		if d.ID != ids[d.Group] || d.ID == "" {
			t.Errorf("%s/%s ID changed: %q → %q", d.Group, d.Name, ids[d.Group], d.ID)
		}
		if d.Group == "B社" && d.Host != "192.0.2.22" || d.Group == "A社" && d.Host != "192.0.2.1" {
			t.Errorf("wrong device updated: %+v", d)
		}
	}
}

// Rows that fail the device checks, and a second row for the same group +
// name (as an all-devices file imported into one target group produces), are
// left out and listed instead of silently overwriting each other.
func TestApplySkipsInvalidAndDuplicateRows(t *testing.T) {
	inv := &model.Inventory{}
	row := func(group, name, host string) model.Device {
		return model.Device{Name: name, Group: group, Host: host, Conn: model.ConnSSH, OSType: "cisco-ios"}
	}
	bad := row("A社", "evil", "192.0.2.9")
	bad.Bastions = []model.Bastion{{Host: "192.0.2.8", Method: model.BastionSSH}}
	bad.Username = "x;reboot"
	c := Contents{Devices: []model.Device{
		row("A社", "sw1", "192.0.2.1"), row("B社", "sw1", "192.0.2.2"), bad, row("A社", "sw2", "999.1.1.1"),
	}}
	s, _ := Apply(inv, c, Options{TargetGroup: "C社"})
	if s.DevicesNew != 1 || len(inv.Devices) != 1 || inv.Devices[0].Host != "192.0.2.1" {
		t.Errorf("imported %d (%+v), want only the first sw1", s.DevicesNew, inv.Devices)
	}
	if strings.Join(s.Duplicate, ",") != "C社 / sw1" {
		t.Errorf("duplicate = %v", s.Duplicate)
	}
	if len(s.Invalid) != 2 {
		t.Errorf("invalid = %v", s.Invalid)
	}
}

// A command file with a carriage return inside a line is refused.
func TestParseCommandSetRefusesHiddenCR(t *testing.T) {
	if _, err := ParseCommandSet("show clock\rreload,1,1\r\nshow version\r\n"); err == nil {
		t.Error("hidden CR accepted")
	}
}

// Credentials left empty in what is read in (cells blanked by hand, or a
// 共有用 export from v1.6.5 and earlier) keep the ones this PalaTerm
// already has, while the other fields update.
func TestBlankCredentialsKeepExisting(t *testing.T) {
	inv := sampleInventory()
	inv.Devices[0].EnablePassword, inv.Devices[0].KeyFile, inv.Devices[0].KeyPassphrase = "en", `C:\k\id`, "kp"
	inv.Devices[1].Bastions[0].Password, inv.Devices[1].Bastions[0].KeyPassphrase = "jp", "jkp"
	inv.DeviceGroups[0].Username, inv.DeviceGroups[0].Password = "gu", "gp"
	src := Select(inv, "A社", false)
	for i := range src.Devices {
		d := &src.Devices[i]
		d.Username, d.Password, d.EnablePassword, d.KeyFile, d.KeyPassphrase = "", "", "", "", ""
		d.Bastions = append([]model.Bastion(nil), d.Bastions...)
		for j := range d.Bastions {
			d.Bastions[j].Username, d.Bastions[j].Password, d.Bastions[j].KeyFile, d.Bastions[j].KeyPassphrase = "", "", "", ""
		}
	}
	for i := range src.Manifest.Groups {
		g := &src.Manifest.Groups[i]
		g.Username, g.Password, g.EnablePassword = "", "", ""
	}
	mp, err := Write(filepath.Join(t.TempDir(), "A社"), src, "PalaTerm test")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(mp)
	if err != nil {
		t.Fatal(err)
	}
	got.Devices[0].Host = "192.0.2.11"
	s, _ := Apply(inv, got, Options{})
	d, j := inv.Devices[0], inv.Devices[1].Bastions[0]
	if d.Host != "192.0.2.11" {
		t.Errorf("non-credential field not updated: %s", d.Host)
	}
	if d.Username != "u" || d.Password != "p" || d.EnablePassword != "en" || d.KeyFile != `C:\k\id` || d.KeyPassphrase != "kp" {
		t.Errorf("device credentials lost: %+v", d)
	}
	if j.Username != "j" || j.Password != "jp" || j.KeyPassphrase != "jkp" || j.JumpCommand != "ssh -l {user} {host}" {
		t.Errorf("bastion credentials lost: %+v", j)
	}
	if inv.DeviceGroups[0].Username != "gu" || inv.DeviceGroups[0].Password != "gp" {
		t.Errorf("group credentials lost: %+v", inv.DeviceGroups[0])
	}
	if strings.Join(s.CredsKept, ",") != "A社 / a-rt1,A社 / a-fw1" {
		t.Errorf("summary: kept=%v", s.CredsKept)
	}
}

// A row that does carry credentials still replaces them; a jump hop whose
// host changed does not inherit the old hop's password.
func TestApplyCredentialsInRowWin(t *testing.T) {
	inv := sampleInventory()
	inv.Devices[1].Bastions[0].Password = "old"
	c := Contents{Devices: []model.Device{
		{Name: "a-rt1", Host: "192.0.2.1", Conn: model.ConnSSH, Group: "A社", Username: "u2", Password: "p2", Enabled: true},
		{Name: "a-fw1", Host: "192.0.2.2", Conn: model.ConnSSH, Group: "A社", Enabled: true,
			Bastions: []model.Bastion{{Host: "192.0.2.99", Method: model.BastionSSH}}},
	}}
	s, _ := Apply(inv, c, Options{})
	if d := inv.Devices[0]; d.Username != "u2" || d.Password != "p2" {
		t.Errorf("row credentials should win: %+v", d)
	}
	if b := inv.Devices[1].Bastions[0]; b.Password != "" || b.Username != "" {
		t.Errorf("a different jump host must not inherit the old one's credentials: %+v", b)
	}
	if len(s.CredsKept) != 0 {
		t.Errorf("nothing was kept, got %v", s.CredsKept)
	}
}

// 待機秒を使う off and each command's 待つ文字列 survive the file round
// trip as "# palaterm:" comment lines, which older readers skip.
func TestCommandSetPauseOffAndWaitRoundTrip(t *testing.T) {
	in := model.CommandSet{Name: "cfg", PauseOff: true, Commands: []model.Command{
		{Text: "configure terminal", PauseSec: 3, SerialSec: 5, Wait: "(config)#"},
		{Text: "interface Gi0/1", PauseSec: 1, SerialSec: 1, Wait: "(config-if)#"},
		{Text: "show run, brief", PauseSec: 2, SerialSec: 2},
	}}
	text := FormatCommandSet(in)
	got, err := ParseCommandSet(text)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PauseOff || len(got.Commands) != 3 {
		t.Fatalf("parsed = %+v", got)
	}
	for i, c := range in.Commands {
		if got.Commands[i] != c {
			t.Errorf("command %d = %+v, want %+v", i+1, got.Commands[i], c)
		}
	}
	// Without the directives (an older PalaTerm's view) the commands and
	// pauses are still all there.
	var plain []string
	for _, l := range strings.Split(strings.TrimPrefix(text, string(rune(0xfeff))), "\r\n") {
		if !strings.HasPrefix(l, "#") {
			plain = append(plain, l)
		}
	}
	old, err := ParseCommandSet(strings.Join(plain, "\r\n"))
	if err != nil || old.PauseOff || len(old.Commands) != 3 || old.Commands[0].Wait != "" || old.Commands[0].SerialSec != 5 {
		t.Errorf("legacy view: %v %+v", err, old)
	}
	// A set with pauses on and no waits writes no directive at all.
	if s := FormatCommandSet(model.CommandSet{Commands: []model.Command{{Text: "show clock", PauseSec: 1, SerialSec: 1}}}); strings.Contains(s, "palaterm") {
		t.Errorf("unexpected directive: %q", s)
	}
}

// 踏み台の認証情報を含めない: the hops' logins stay out of the files, the
// rest (device logins, hop hosts) travel, and on the receiving side the
// user's own jump-host login fills the hop in.
func TestSelectWithoutBastionCredsRoundTrip(t *testing.T) {
	inv := sampleInventory()
	inv.Devices[1].Bastions[0].Password = "jp"
	inv.Devices[1].Bastions[0].AuthMethod = model.AuthPublicKey
	inv.Devices[1].Bastions[0].KeyFile = `C:\Users\me\.ssh\id_ed25519`
	inv.Devices[1].Bastions[0].KeyPassphrase = "secret-pass"
	dir := t.TempDir()
	mp, err := Write(dir, Select(inv, "A社", true), "test")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Devices[1].Bastions[0].Username != "j" {
		t.Fatal("exporting changed the vault's device")
	}
	for _, f := range []string{"devices.csv", ManifestName} {
		data, _ := os.ReadFile(filepath.Join(dir, f))
		for _, secret := range []string{"secret-pass", `id_ed25519`, "jp"} {
			if strings.Contains(string(data), secret) {
				t.Errorf("%s contains %q", f, secret)
			}
		}
	}
	c, err := Read(mp)
	if err != nil {
		t.Fatal(err)
	}
	var fw model.Device
	for _, d := range c.Devices {
		if d.Name == "a-fw1" {
			fw = d
		}
	}
	if len(fw.Bastions) != 1 || fw.Bastions[0].Host != "192.0.2.9" || fw.Bastions[0].Username != "" || fw.Bastions[0].JumpCommand == "" {
		t.Fatalf("hop after round trip = %+v", fw.Bastions)
	}
	if c.Devices[0].Username != "u" || c.Devices[0].Password != "p" {
		t.Errorf("device login must still travel: %+v", c.Devices[0])
	}
	recv := &model.Inventory{MyBastionLogins: []model.BastionLogin{{Host: "192.0.2.9", Username: "theirs", AuthMethod: model.AuthPublicKey, KeyFile: `C:\k`}}}
	Apply(recv, c, Options{})
	for _, d := range recv.Devices {
		if d.Name != "a-fw1" {
			continue
		}
		r := recv.ResolveCredentials(d)
		if r.Bastions[0].Username != "theirs" || r.Bastions[0].KeyFile != `C:\k` {
			t.Errorf("receiver's own login not used: %+v", r.Bastions[0])
		}
	}
}
