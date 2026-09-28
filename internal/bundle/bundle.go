// Package bundle writes and reads a "一式" (bundle) folder: the devices of one
// group — or of the whole inventory — together with the command sets and OS
// profiles those devices use, so a company's configuration can be handed to
// another PalaTerm in one piece and read back in one step.
//
// Layout of a bundle folder:
//
//	palaterm-bundle.json      manifest (this file is what the user picks on import)
//	devices.csv               csvio format, complete (passwords in clear text)
//	command-sets/<名前>.csv   "コマンド,リモート待機秒,シリアル待機秒" per line
//	os-profiles/<名前>.json   profile.Profile
//
// The manifest lists the exact files that belong to the bundle, so a folder
// that is exported into repeatedly never picks up stale files on import. The
// per-file formats are the ones the single-file importers accept, so a file
// taken out of a bundle still loads on its own.
//
// Nothing here touches the App lock or the Wails runtime: Select / Write /
// Read / Preview / Apply are pure so they can be tested without a window.
package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kojio145/palaterm/internal/csvio"
	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// ManifestName is the file the user picks to import a bundle.
const ManifestName = "palaterm-bundle.json"

// Format identifies the manifest layout; bump when the layout changes.
const Format = "palaterm-bundle/1"

// FileRef names one command set or profile file inside the bundle. Name is
// the real name (a file name is sanitized and may not round-trip).
type FileRef struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// Manifest is palaterm-bundle.json.
type Manifest struct {
	Format      string    `json:"format"`
	App         string    `json:"app"`
	ExportedAt  string    `json:"exportedAt"`
	Group       string    `json:"group"` // "" = whole inventory
	Devices     string    `json:"devices"`
	DeviceCount int       `json:"deviceCount"`
	CommandSets []FileRef `json:"commandSets"`
	OSProfiles  []FileRef `json:"osProfiles"`
}

// Contents is what a bundle carries, in memory.
type Contents struct {
	Manifest    Manifest
	Devices     []model.Device
	CommandSets []model.CommandSet
	Profiles    []profile.Profile
}

// Select picks the devices of group (every device when group is ""), plus the
// command sets and OS profiles they reference. For the whole inventory every
// command set and profile is included, so an "all devices" bundle is a
// complete copy of the configuration (settings and host keys aside).
func Select(inv *model.Inventory, group string) Contents {
	c := Contents{Manifest: Manifest{Group: group}}
	if inv == nil {
		return c
	}
	all := group == ""
	useSet := map[string]bool{}
	useProf := map[string]bool{}
	for _, d := range inv.Devices {
		if !all && d.Group != group {
			continue
		}
		d.NormalizeBastions()
		c.Devices = append(c.Devices, d)
		useSet[d.CommandSet] = true
		useProf[d.OSType] = true
	}
	for _, s := range inv.CommandSets {
		if all || useSet[s.Name] {
			c.CommandSets = append(c.CommandSets, s)
		}
	}
	for _, p := range inv.CustomProfiles {
		if all || useProf[p.Key] {
			c.Profiles = append(c.Profiles, p)
		}
	}
	return c
}

// FileNameSafe replaces the characters Windows forbids in file names.
func FileNameSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, s)
}

// uniqueFile returns base+ext, or base_2+ext, … so two names that sanitize
// to the same file name do not overwrite each other inside one bundle.
func uniqueFile(taken map[string]bool, base, ext string) string {
	name := base + ext
	for i := 2; taken[strings.ToLower(name)]; i++ {
		name = base + "_" + strconv.Itoa(i) + ext
	}
	taken[strings.ToLower(name)] = true
	return name
}

// FormatCommandSet renders a command set in the one-line-per-command file
// format ("コマンド,リモート待機秒,シリアル待機秒", CRLF).
func FormatCommandSet(s model.CommandSet) string {
	var b strings.Builder
	for _, c := range s.Commands {
		fmt.Fprintf(&b, "%s,%d,%d\r\n", c.Text, c.PauseSec, c.SerialSec)
	}
	return b.String()
}

// ParseCommandSet reads the command set file format. Blank lines and lines
// starting with "#" are skipped. The command itself may contain commas: only
// the last two fields are the pause seconds, and both are optional (default
// 1 second each), so the legacy Show_Command_Pattern .list files load as-is.
func ParseCommandSet(data string) ([]model.Command, error) {
	var cmds []model.Command
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		text, p1, p2 := line, 1, 1
		if parts := strings.Split(line, ","); len(parts) >= 3 {
			if n1, e1 := strconv.Atoi(strings.TrimSpace(parts[len(parts)-2])); e1 == nil {
				if n2, e2 := strconv.Atoi(strings.TrimSpace(parts[len(parts)-1])); e2 == nil {
					text = strings.TrimSpace(strings.Join(parts[:len(parts)-2], ","))
					p1, p2 = n1, n2
				}
			}
		}
		if text == "" {
			continue
		}
		cmds = append(cmds, model.Command{Text: text, PauseSec: p1, SerialSec: p2})
	}
	if len(cmds) == 0 {
		return nil, fmt.Errorf("有効なコマンド行がありません")
	}
	return cmds, nil
}

// WriteProfileJSON writes one profile to dir as <名前>.json and returns the
// file name used.
func WriteProfileJSON(dir string, p profile.Profile) (string, error) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	name := FileNameSafe(p.Name) + ".json"
	return name, os.WriteFile(filepath.Join(dir, name), append(data, '\r', '\n'), 0o644)
}

// ParseProfileJSON reads one exported profile, applying the same checks the
// editor does. A missing key becomes "custom:<名前>".
func ParseProfileJSON(data []byte) (profile.Profile, error) {
	var p profile.Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("JSONの形式が不正です: %w", err)
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return p, fmt.Errorf("プロファイル名（name）がありません")
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return p, fmt.Errorf("「showコマンド完了の目印」（prompt）がありません")
	}
	if p.Key == "" {
		p.Key = "custom:" + p.Name
	}
	return p, nil
}

// Write writes c into dir (created if needed) and returns the manifest path.
// Files from an earlier export into the same folder are overwritten when
// they have the same name and otherwise left alone — the manifest decides
// what belongs to the bundle.
func Write(dir string, c Contents, app string) (string, error) {
	setDir := filepath.Join(dir, "command-sets")
	profDir := filepath.Join(dir, "os-profiles")
	for _, d := range []string{dir, setDir, profDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
	}
	m := Manifest{
		Format:      Format,
		App:         app,
		ExportedAt:  time.Now().Format(time.RFC3339),
		Group:       c.Manifest.Group,
		Devices:     "devices.csv",
		DeviceCount: len(c.Devices),
		CommandSets: []FileRef{},
		OSProfiles:  []FileRef{},
	}
	text, err := csvio.Export(c.Devices)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, m.Devices), []byte(text), 0o600); err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, s := range c.CommandSets {
		name := uniqueFile(taken, FileNameSafe(s.Name), ".csv")
		if err := os.WriteFile(filepath.Join(setDir, name), []byte(FormatCommandSet(s)), 0o644); err != nil {
			return "", err
		}
		m.CommandSets = append(m.CommandSets, FileRef{Name: s.Name, File: "command-sets/" + name})
	}
	taken = map[string]bool{}
	for _, p := range c.Profiles {
		data, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return "", err
		}
		name := uniqueFile(taken, FileNameSafe(p.Name), ".json")
		if err := os.WriteFile(filepath.Join(profDir, name), append(data, '\r', '\n'), 0o644); err != nil {
			return "", err
		}
		m.OSProfiles = append(m.OSProfiles, FileRef{Name: p.Name, File: "os-profiles/" + name})
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	mp := filepath.Join(dir, ManifestName)
	return mp, os.WriteFile(mp, append(data, '\r', '\n'), 0o644)
}

// Read loads a bundle from its manifest path. Every file the manifest names
// must be present and valid; a bundle is read whole or not at all.
func Read(manifestPath string) (Contents, error) {
	var c Contents
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c.Manifest); err != nil {
		return c, fmt.Errorf("目録（%s）の形式が不正です: %w", ManifestName, err)
	}
	if !strings.HasPrefix(c.Manifest.Format, "palaterm-bundle/") {
		return c, fmt.Errorf("PalaTerm の一式ではありません（format=%q）", c.Manifest.Format)
	}
	dir := filepath.Dir(manifestPath)
	in := func(rel string) (string, error) {
		// Manifest paths are relative and forward-slashed; refuse anything
		// that escapes the bundle folder.
		clean := filepath.Clean(filepath.FromSlash(rel))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("目録のパスが不正です: %s", rel)
		}
		return filepath.Join(dir, clean), nil
	}
	if c.Manifest.Devices != "" {
		p, err := in(c.Manifest.Devices)
		if err != nil {
			return c, err
		}
		text, err := os.ReadFile(p)
		if err != nil {
			return c, fmt.Errorf("機器CSVが読めません: %w", err)
		}
		if c.Devices, err = csvio.Import(string(text)); err != nil {
			return c, fmt.Errorf("機器CSV: %w", err)
		}
	}
	for _, ref := range c.Manifest.CommandSets {
		p, err := in(ref.File)
		if err != nil {
			return c, err
		}
		text, err := os.ReadFile(p)
		if err != nil {
			return c, fmt.Errorf("コマンドセット「%s」が読めません: %w", ref.Name, err)
		}
		cmds, err := ParseCommandSet(string(text))
		if err != nil {
			return c, fmt.Errorf("コマンドセット「%s」: %w", ref.Name, err)
		}
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		}
		c.CommandSets = append(c.CommandSets, model.CommandSet{Name: name, Commands: cmds})
	}
	for _, ref := range c.Manifest.OSProfiles {
		p, err := in(ref.File)
		if err != nil {
			return c, err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return c, fmt.Errorf("OSプロファイル「%s」が読めません: %w", ref.Name, err)
		}
		prof, err := ParseProfileJSON(data)
		if err != nil {
			return c, fmt.Errorf("OSプロファイル「%s」: %w", ref.Name, err)
		}
		c.Profiles = append(c.Profiles, prof)
	}
	return c, nil
}

// ReadDevicesCSV loads a bare devices CSV as a bundle with only devices, so
// the one import path handles both.
func ReadDevicesCSV(path string) (Contents, error) {
	var c Contents
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	c.Devices, err = csvio.Import(string(data))
	if err != nil {
		return c, err
	}
	c.Manifest = Manifest{Devices: filepath.Base(path), DeviceCount: len(c.Devices)}
	return c, nil
}

// Options steer Apply.
type Options struct {
	// TargetGroup, when set, puts every imported device into that group
	// instead of the group named in the file.
	TargetGroup string
	// OverwriteShared replaces existing command sets and OS profiles of the
	// same name with the bundle's copies. Off by default: a company bundle
	// should not silently rewrite the profiles other groups depend on.
	OverwriteShared bool
}

// Summary says what Apply did (or, from Preview, would do).
type Summary struct {
	Devices        int      `json:"devices"`
	DevicesNew     int      `json:"devicesNew"`
	DevicesUpdated int      `json:"devicesUpdated"`
	Sets           int      `json:"sets"`
	SetsNew        int      `json:"setsNew"`
	SetsUpdated    int      `json:"setsUpdated"`
	SetsSkipped    int      `json:"setsSkipped"`
	Profiles       int      `json:"profiles"`
	ProfilesNew    int      `json:"profilesNew"`
	ProfilesUpd    int      `json:"profilesUpdated"`
	ProfilesSkip   int      `json:"profilesSkipped"`
	GroupsCreated  []string `json:"groupsCreated"`
	// Missing lists command sets / profiles the imported devices name that
	// exist neither in the bundle nor in the inventory (the device still
	// imports; it runs with the generic profile / no command set).
	MissingSets     []string `json:"missingSets"`
	MissingProfiles []string `json:"missingProfiles"`
}

// Preview reports what Apply would do without changing inv.
func Preview(inv *model.Inventory, c Contents, opt Options) Summary {
	cp := *inv
	cp.Devices = append([]model.Device(nil), inv.Devices...)
	cp.CommandSets = append([]model.CommandSet(nil), inv.CommandSets...)
	cp.CustomProfiles = append([]profile.Profile(nil), inv.CustomProfiles...)
	cp.DeviceGroups = append([]model.DeviceGroup(nil), inv.DeviceGroups...)
	s, _ := Apply(&cp, c, opt)
	return s
}

// Apply merges c into inv. Devices match by name and are replaced whole (a
// CSV row is a complete device). Command sets match by name; profiles by key,
// or failing that by name — in which case the existing key is kept and the
// imported devices are re-pointed at it, so nothing ends up orphaned. Existing
// shared items are left alone unless opt.OverwriteShared. Returns the summary
// and the profiles that were added or replaced (for the runtime registry).
func Apply(inv *model.Inventory, c Contents, opt Options) (Summary, []profile.Profile) {
	var s Summary
	var changed []profile.Profile
	s.GroupsCreated = []string{}
	s.MissingSets = []string{}
	s.MissingProfiles = []string{}

	// Profiles first: they may rename keys the devices use.
	keyMap := map[string]string{}
	profByKey := map[string]int{}
	profByName := map[string]int{}
	for i, p := range inv.CustomProfiles {
		profByKey[p.Key] = i
		profByName[strings.ToLower(p.Name)] = i
	}
	for _, p := range c.Profiles {
		s.Profiles++
		if i, ok := profByKey[p.Key]; ok {
			if opt.OverwriteShared {
				inv.CustomProfiles[i] = p
				changed = append(changed, p)
				s.ProfilesUpd++
			} else {
				s.ProfilesSkip++
			}
			continue
		}
		if i, ok := profByName[strings.ToLower(p.Name)]; ok {
			existing := inv.CustomProfiles[i].Key
			keyMap[p.Key] = existing
			if opt.OverwriteShared {
				p.Key = existing
				inv.CustomProfiles[i] = p
				changed = append(changed, p)
				s.ProfilesUpd++
			} else {
				s.ProfilesSkip++
			}
			continue
		}
		profByKey[p.Key] = len(inv.CustomProfiles)
		profByName[strings.ToLower(p.Name)] = len(inv.CustomProfiles)
		inv.CustomProfiles = append(inv.CustomProfiles, p)
		changed = append(changed, p)
		s.ProfilesNew++
	}

	setByName := map[string]int{}
	for i, cs := range inv.CommandSets {
		setByName[cs.Name] = i
	}
	for _, cs := range c.CommandSets {
		s.Sets++
		if i, ok := setByName[cs.Name]; ok {
			if opt.OverwriteShared {
				inv.CommandSets[i].Commands = cs.Commands
				s.SetsUpdated++
			} else {
				s.SetsSkipped++
			}
			continue
		}
		setByName[cs.Name] = len(inv.CommandSets)
		inv.CommandSets = append(inv.CommandSets, cs)
		s.SetsNew++
	}

	groups := map[string]bool{}
	for _, g := range inv.DeviceGroups {
		groups[g.Name] = true
	}
	devByName := map[string]int{}
	for i, d := range inv.Devices {
		devByName[d.Name] = i
	}
	missingSet := map[string]bool{}
	missingProf := map[string]bool{}
	for _, d := range c.Devices {
		s.Devices++
		if opt.TargetGroup != "" {
			d.Group = opt.TargetGroup
		}
		if k, ok := keyMap[d.OSType]; ok {
			d.OSType = k
		}
		if d.Group != "" && !groups[d.Group] {
			inv.DeviceGroups = append(inv.DeviceGroups, model.DeviceGroup{Name: d.Group})
			groups[d.Group] = true
			s.GroupsCreated = append(s.GroupsCreated, d.Group)
		}
		if d.CommandSet != "" {
			if _, ok := setByName[d.CommandSet]; !ok && !missingSet[d.CommandSet] {
				missingSet[d.CommandSet] = true
				s.MissingSets = append(s.MissingSets, d.CommandSet)
			}
		}
		if d.OSType != "" {
			if _, ok := profByKey[d.OSType]; !ok && !missingProf[d.OSType] {
				missingProf[d.OSType] = true
				s.MissingProfiles = append(s.MissingProfiles, d.OSType)
			}
		}
		if i, ok := devByName[d.Name]; ok {
			inv.Devices[i] = d
			s.DevicesUpdated++
		} else {
			devByName[d.Name] = len(inv.Devices)
			inv.Devices = append(inv.Devices, d)
			s.DevicesNew++
		}
	}
	return s, changed
}
