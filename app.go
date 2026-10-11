package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/kojio145/palaterm/internal/bundle"
	"github.com/kojio145/palaterm/internal/guard"
	"github.com/kojio145/palaterm/internal/history"
	"github.com/kojio145/palaterm/internal/logstore"
	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
	runpkg "github.com/kojio145/palaterm/internal/runner"
	"github.com/kojio145/palaterm/internal/session"
	"github.com/kojio145/palaterm/internal/vault"
)

// App is the Wails-bound backend. Its exported methods are callable from the
// frontend as window.go.main.App.<Method>.
type App struct {
	ctx       context.Context
	profiles  *profile.Registry
	runner    *runpkg.Runner
	vaultPath string

	mu       sync.Mutex
	password string           // held in memory only after unlock
	inv      *model.Inventory // nil until unlocked

	runCancel context.CancelFunc
	runActive bool // a batch is in progress (blocks a second concurrent batch)

	interMu      sync.Mutex
	interactives map[string]*interactiveSession // device name -> live terminal
}

// interactiveSession is one open manual-operation terminal.
type interactiveSession struct {
	send   func(string) error
	resize func(cols, rows int) error
	close  func() error
}

// NewApp constructs the backend with the vault stored next to the executable
// (portable: the whole tool + its encrypted data travel together).
func NewApp() *App {
	reg := profile.NewRegistry()
	a := &App{
		profiles:     reg,
		runner:       runpkg.New(reg),
		vaultPath:    defaultVaultPath(),
		interactives: make(map[string]*interactiveSession),
	}
	a.runner.HostKeys = a // TOFU host key pinning backed by the vault
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	mainWin.fit(ctx)
	// Materialize the tool's folder layout next to the exe so users can find
	// everything: logs / export destinations / internal parts (data).
	for _, d := range []string{"logs", filepath.Join("export", "bundles"), filepath.Join("export", "command-sets"), filepath.Join("export", "os-profiles"), "data"} {
		_ = os.MkdirAll(filepath.Join(exeDir(), d), 0o755)
	}
	// Keep a JSON of every default OS profile on disk (written only when the
	// file is missing — user-edited exports are never overwritten). Combined
	// with delete never touching these files, a removed default can always be
	// brought back via ファイル読込. Files from the short-lived regex-export
	// era (they carry a "regex" field) are replaced: imported as plain text
	// they would never match.
	profDir := filepath.Join(exeDir(), "export", "os-profiles")
	for _, p := range profile.Defaults() {
		path := filepath.Join(profDir, bundle.FileNameSafe(p.Name)+".json")
		if data, err := os.ReadFile(path); err == nil && !strings.Contains(string(data), `"regex"`) {
			continue
		}
		_, _ = bundle.WriteProfileJSON(profDir, p)
	}
}

// ---- SSH host key pinning (session.HostKeyStore) ----

// GetHostKey returns the pinned fingerprint for addr ("host:port"), or "".
func (a *App) GetHostKey(addr string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return ""
	}
	return a.inv.KnownHostKeys[addr]
}

// SetHostKey pins addr's fingerprint on first sight and persists the vault.
func (a *App) SetHostKey(addr, fingerprint string) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return
	}
	if a.inv.KnownHostKeys == nil {
		a.inv.KnownHostKeys = map[string]string{}
	}
	a.inv.KnownHostKeys[addr] = fingerprint
	a.mu.Unlock()
	_ = a.persist()
}

// ClearHostKeys drops the pinned host keys for one device (its own address
// and its bastions'), so a legitimately replaced device can be re-pinned on
// the next connection.
func (a *App) ClearHostKeys(deviceName string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	var dev *model.Device
	for i := range a.inv.Devices {
		if a.inv.Devices[i].Name == deviceName {
			dev = &a.inv.Devices[i]
			break
		}
	}
	if dev == nil {
		a.mu.Unlock()
		return fmt.Errorf("device %q not found", deviceName)
	}
	addrs := []string{fmt.Sprintf("%s:%d", dev.Host, dev.EffectivePort())}
	for _, b := range dev.ActiveBastions() {
		p := b.Port
		if p == 0 {
			p = 22
		}
		addrs = append(addrs, fmt.Sprintf("%s:%d", b.Host, p))
	}
	for _, addr := range addrs {
		delete(a.inv.KnownHostKeys, addr)
	}
	a.mu.Unlock()
	return a.persist()
}

// memHostKeys is the terminal child process's in-memory HostKeyStore, seeded
// from the vault's pinned keys (the child never writes the vault back).
type memHostKeys struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemHostKeys(seed map[string]string) *memHostKeys {
	s := &memHostKeys{m: map[string]string{}}
	for k, v := range seed {
		s.m[k] = v
	}
	return s
}

func (s *memHostKeys) GetHostKey(addr string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[addr]
}

func (s *memHostKeys) SetHostKey(addr, fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[addr] = fp
}

// Delete forgets the pin for addr.
func (s *memHostKeys) Delete(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, addr)
}

// exeDir returns the folder PalaTerm.exe runs from (the user-facing tool folder).
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// defaultVaultPath keeps the encrypted vault under <exe>\data\ (internal
// parts live there so the exe's folder stays tidy). A vault from the old
// layout (directly next to the exe) is moved in on first access.
func defaultVaultPath() string {
	dir := exeDir()
	dataDir := filepath.Join(dir, "data")
	newPath := filepath.Join(dataDir, "palaterm_vault.enc")
	oldPath := filepath.Join(dir, "palaterm_vault.enc")
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		if _, err2 := os.Stat(oldPath); err2 == nil {
			if os.MkdirAll(dataDir, 0o755) != nil || os.Rename(oldPath, newPath) != nil {
				return oldPath
			}
			return newPath
		}
	}
	_ = os.MkdirAll(dataDir, 0o755)
	return newPath
}

// ---- Vault lifecycle ----

// VaultExists reports whether an encrypted inventory is already present.
func (a *App) VaultExists() bool { return vault.Exists(a.vaultPath) }

// CreateVault initializes a new encrypted inventory with a master password.
func (a *App) CreateVault(password string) error {
	if password == "" {
		return fmt.Errorf("master password required")
	}
	inv := &model.Inventory{
		Version:           1,
		Settings:          model.DefaultSettings(),
		CommandSets:       model.DefaultCommandSets(),
		CommandSetSeedGen: model.CommandSetSeedGen,
		DeviceGroups:      []model.DeviceGroup{{Name: "サンプルグループ"}},
		CustomProfiles:    profile.Defaults(),
		ProfilesSeeded:    true,
		ProfileSeedGen:    profile.SeedGen,
	}
	if err := vault.Save(a.vaultPath, password, inv); err != nil {
		return err
	}
	registerProfiles(a.profiles, inv)
	a.mu.Lock()
	a.password, a.inv = password, inv
	a.mu.Unlock()
	return nil
}

// Unlock decrypts the inventory with the master password.
func (a *App) Unlock(password string) error {
	inv, err := vault.Load(a.vaultPath, password)
	if err != nil {
		return err
	}
	inv.NormalizeGroups()
	// Seed the built-in command sets into vaults created before they existed.
	// A vault still holding only the pre-【サンプル】 sample sets (all names
	// untouched, so nothing user-made) is reseeded under the new names.
	seed := false
	if len(inv.CommandSets) == 0 || onlyLegacyDefaultSets(inv.CommandSets) {
		inv.CommandSets = model.DefaultCommandSets()
		inv.CommandSetSeedGen = model.CommandSetSeedGen
		seed = true
	}
	// Sample sets added by a later release (ALAXALA_L2SW in generation 1) are
	// appended to an older vault once, by name: a set the user already has
	// under that name is left alone, and one deleted afterwards stays deleted
	// because the vault remembers the generation.
	if inv.CommandSetSeedGen < model.CommandSetSeedGen {
		have := map[string]bool{}
		for _, cs := range inv.CommandSets {
			have[cs.Name] = true
		}
		for _, cs := range model.CommandSetsAddedAfter(inv.CommandSetSeedGen) {
			if !have[cs.Name] {
				inv.CommandSets = append(inv.CommandSets, cs)
			}
		}
		inv.CommandSetSeedGen = model.CommandSetSeedGen
		seed = true
	}
	// A vault with no groups and no devices also gets the starter group.
	if len(inv.DeviceGroups) == 0 && len(inv.Devices) == 0 {
		inv.DeviceGroups = []model.DeviceGroup{{Name: "サンプルグループ"}}
		seed = true
	}
	// Seed the default OS profiles into vaults created before profiles became
	// editable vault data (exactly once; user-made profiles keep their spot).
	if !inv.ProfilesSeeded {
		have := map[string]bool{}
		for _, p := range inv.CustomProfiles {
			have[p.Key] = true
		}
		var merged []profile.Profile
		for _, p := range profile.Defaults() {
			if !have[p.Key] {
				merged = append(merged, p)
			}
		}
		inv.CustomProfiles = append(merged, inv.CustomProfiles...)
		inv.ProfilesSeeded = true
		inv.ProfileSeedGen = profile.SeedGen
		seed = true
	}
	// Defaults added by a later release (ALAXALA AX in generation 1) go into
	// vaults seeded before them, once: the vault remembers the generation, so
	// deleting such a profile afterwards sticks like deleting any other
	// default. The new ones are placed before "generic" (the built-in order)
	// when it is still there, else at the end.
	if inv.ProfileSeedGen < profile.SeedGen {
		have := map[string]bool{}
		for _, p := range inv.CustomProfiles {
			have[p.Key] = true
		}
		var added []profile.Profile
		for _, p := range profile.AddedAfter(inv.ProfileSeedGen) {
			if !have[p.Key] {
				added = append(added, p)
			}
		}
		if len(added) > 0 {
			at := len(inv.CustomProfiles)
			for i, p := range inv.CustomProfiles {
				if p.Key == "generic" {
					at = i
					break
				}
			}
			merged := make([]profile.Profile, 0, len(inv.CustomProfiles)+len(added))
			merged = append(merged, inv.CustomProfiles[:at]...)
			merged = append(merged, added...)
			merged = append(merged, inv.CustomProfiles[at:]...)
			inv.CustomProfiles = merged
		}
		inv.ProfileSeedGen = profile.SeedGen
		seed = true
	}
	// The runner now waits for the operational prompt itself after the last
	// sending login step, so a trailing 「目印を待つだけ」 row is redundant —
	// drop such rows from profiles seeded before this change (idempotent; a
	// trailing expect that differs from the prompt is left alone).
	for i := range inv.CustomProfiles {
		p := &inv.CustomProfiles[i]
		for n := len(p.Login); n > 0; n = len(p.Login) {
			last := p.Login[n-1]
			if last.Sends() || last.Expect != p.Prompt {
				break
			}
			p.Login = p.Login[:n-1]
			seed = true
		}
	}
	// NEC IX has no ">" privilege level, so the escalation row copied from the
	// Cisco profiles waits for a prompt the device never prints. An
	// administrator reaches "#" directly and the row is merely skipped; a
	// monitor user lands on "%", where neither ">" nor the profile's "#" ever
	// appears, and the login fails after burning the whole command timeout.
	// Drop the row where it is still the built-in one; a row the user has
	// edited into something else is theirs.
	for i := range inv.CustomProfiles {
		p := &inv.CustomProfiles[i]
		if p.Key != "nec-ix" {
			continue
		}
		n := len(p.Login)
		if n == 0 {
			continue
		}
		if last := p.Login[n-1]; last.Expect == ">" && last.Send == "enable" {
			p.Login = p.Login[:n-1]
			seed = true
		}
	}
	// NEC IX keeps "terminal length 0" behind configure mode: run in operation
	// mode the device answers "% terminal -- Invalid command." and keeps
	// paging, so the first long output hangs the run at "--More--". It enters
	// with svintr-config because plain "configure" fails while another session
	// holds the mode. Upgrade profiles still carrying a built-in pager; one the
	// user has since edited (a pager of their own, or a MorePrompt) is left
	// exactly as they wrote it.
	for i := range inv.CustomProfiles {
		p := &inv.CustomProfiles[i]
		if p.Key != "nec-ix" || p.MorePrompt != "" {
			continue
		}
		pristine := len(p.Pager) == 1 && p.Pager[0].Send == "terminal length 0"
		// An intermediate build entered with "configure", which loses the pager
		// (and every show running-config) whenever someone else holds the mode.
		interim := len(p.Pager) == 2 && p.Pager[0].Send == "configure" && p.Pager[1].Send == "terminal length 0"
		if pristine || interim {
			p.MorePrompt = "--More--"
			p.Pager = []profile.Step{{Send: "svintr-config"}, {Send: "terminal length 0"}}
			// Logging out now has one extra mode to climb back out of.
			if len(p.Disconnect) == 1 && p.Disconnect[0].Send == "exit" {
				p.Disconnect = []profile.Step{{Send: "exit"}, {Send: "exit"}}
			}
			seed = true
		}
	}
	// Vaults still on an earlier default log name move to the current one
	// (site, role and stage in the name); a template the user changed is theirs.
	if inv.Settings.LogNameTemplate == "" || logstore.IsOldDefaultTemplate(inv.Settings.LogNameTemplate) {
		inv.Settings.LogNameTemplate = logstore.DefaultTemplate
		seed = true
	}
	if seed {
		if err := vault.Save(a.vaultPath, password, inv); err != nil {
			return err
		}
	}
	registerProfiles(a.profiles, inv)
	a.mu.Lock()
	a.password, a.inv = password, inv
	a.mu.Unlock()
	return nil
}

// onlyLegacyDefaultSets reports whether every command set carries a name from
// the first (2026-08-27, pre-【サンプル】) built-in seeding, meaning reseeding
// loses nothing user-made.
func onlyLegacyDefaultSets(sets []model.CommandSet) bool {
	legacy := map[string]bool{
		"FortiGate 状態取得":               true,
		"Cisco IOSルータ 状態取得":            true,
		"Cisco Catalyst L2SW 状態取得":     true,
		"Cisco Catalyst L3SW 状態取得":     true,
		"ProCurve/ArubaOS-Switch 状態取得": true,
		"Yamaha RTX 状態取得":              true,
		"汎用スイッチ show一式":                true,
	}
	for _, cs := range sets {
		if !legacy[cs.Name] {
			return false
		}
	}
	return len(sets) > 0
}

// Lock forgets the password and inventory from memory.
func (a *App) Lock() {
	a.mu.Lock()
	a.password, a.inv = "", nil
	a.mu.Unlock()
}

// ChangeMasterPassword re-encrypts the vault under a new master password
// after verifying the current one.
func (a *App) ChangeMasterPassword(current, next string) error {
	if next == "" {
		return fmt.Errorf("新しいマスターパスワードを入力してください")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return fmt.Errorf("vault is locked")
	}
	if current != a.password {
		return fmt.Errorf("現在のマスターパスワードが違います")
	}
	if err := vault.Save(a.vaultPath, next, a.inv); err != nil {
		return err
	}
	a.password = next
	return nil
}

// ResetVault permanently deletes the encrypted vault — every device, command
// set, group, and OS profile — after verifying the master password, so an
// unattended unlocked window can't be wiped casually. Log files and exported
// CSV/JSON files are left untouched. The UI reloads afterwards and lands on
// the create screen.
func (a *App) ResetVault(current string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	if current != a.password {
		a.mu.Unlock()
		return fmt.Errorf("現在のマスターパスワードが違います")
	}
	a.password, a.inv = "", nil
	a.mu.Unlock()
	if err := os.Remove(a.vaultPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (a *App) persist() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return fmt.Errorf("vault is locked")
	}
	return vault.Save(a.vaultPath, a.password, a.inv)
}

// ---- Inventory reads ----

// GetInventory returns the decrypted inventory (locked => nil).
func (a *App) GetInventory() *model.Inventory {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inv
}

// ListProfiles returns the available OS profiles for dropdowns, in the
// user-arranged vault order (OSタイプ設定 rows and dropdowns match).
func (a *App) ListProfiles() []profile.Profile {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return a.profiles.List()
	}
	out := make([]profile.Profile, len(a.inv.CustomProfiles))
	copy(out, a.inv.CustomProfiles)
	return out
}

// registerProfiles loads the vault's profiles (seeded defaults + user-made)
// into reg, quoting the plain-text waits into literal regexps.
func registerProfiles(reg *profile.Registry, inv *model.Inventory) {
	for _, p := range inv.CustomProfiles {
		reg.Add(profile.Quote(p))
	}
}

// SaveProfile creates or updates an OS profile (the seeded defaults are as
// editable as user-made ones). Prompts and expects are plain strings
// (matched as-is, partial match).
func (a *App) SaveProfile(p profile.Profile) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return fmt.Errorf("プロファイル名は必須です")
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return fmt.Errorf("「showコマンド完了の目印」は必須です")
	}
	if p.Key == "" {
		p.Key = "custom:" + p.Name
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	found := false
	for i := range a.inv.CustomProfiles {
		q := &a.inv.CustomProfiles[i]
		if q.Key != p.Key && strings.EqualFold(q.Name, p.Name) {
			a.mu.Unlock()
			return fmt.Errorf("同名のプロファイル「%s」が既にあります", q.Name)
		}
		if q.Key == p.Key {
			*q = p
			found = true
		}
	}
	if !found {
		a.inv.CustomProfiles = append(a.inv.CustomProfiles, p)
	}
	a.mu.Unlock()
	a.profiles.Add(profile.Quote(p))
	return a.persist()
}

// DeleteProfile removes an OS profile. The exported JSON files under
// export\os-profiles\ are never touched, so a deleted profile can always be
// restored via ファイル読込.
func (a *App) DeleteProfile(key string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	for _, d := range a.inv.Devices {
		if d.OSType == key {
			a.mu.Unlock()
			return fmt.Errorf("機器「%s」がこのプロファイルを使用中のため削除できません", d.Name)
		}
	}
	out := a.inv.CustomProfiles[:0]
	for _, p := range a.inv.CustomProfiles {
		if p.Key != key {
			out = append(out, p)
		}
	}
	a.inv.CustomProfiles = out
	a.mu.Unlock()
	a.profiles.Remove(key)
	return a.persist()
}

// CopyProfile duplicates a profile under "<名前>_copy" (then _copy2, …) and
// returns the new name.
func (a *App) CopyProfile(key string) (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	var src *profile.Profile
	existing := map[string]bool{}
	for i := range a.inv.CustomProfiles {
		existing[strings.ToLower(a.inv.CustomProfiles[i].Name)] = true
		if a.inv.CustomProfiles[i].Key == key {
			p := a.inv.CustomProfiles[i]
			src = &p
		}
	}
	if src == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("プロファイルが見つかりません")
	}
	newName := src.Name + "_copy"
	for i := 2; existing[strings.ToLower(newName)]; i++ {
		newName = fmt.Sprintf("%s_copy%d", src.Name, i)
	}
	dup := *src
	dup.Name = newName
	dup.Key = "custom:" + newName
	dup.Login = append([]profile.Step(nil), src.Login...)
	dup.Pager = append([]profile.Step(nil), src.Pager...)
	dup.Disconnect = append([]profile.Step(nil), src.Disconnect...)
	a.inv.CustomProfiles = append(a.inv.CustomProfiles, dup)
	a.mu.Unlock()
	a.profiles.Add(profile.Quote(dup))
	return newName, a.persist()
}

// ListSerialPorts returns COM ports currently present.
func (a *App) ListSerialPorts() []string {
	ports, err := session.ListSerialPorts()
	if err != nil {
		return nil
	}
	return ports
}

// ---- Device CRUD (keyed by unique Name) ----

// SaveDevice inserts or updates a device by name (after validation).
func (a *App) SaveDevice(d model.Device) error {
	if err := d.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	found := false
	for i := range a.inv.Devices {
		if a.inv.Devices[i].Name == d.Name {
			a.inv.Devices[i] = d
			found = true
			break
		}
	}
	if !found {
		a.inv.Devices = append(a.inv.Devices, d)
	}
	a.ensureGroupExists(d.Group)
	// Remember the key path (typed or picked) for pre-filling next time.
	if d.KeyFile != "" {
		a.inv.Settings.LastKeyFile = d.KeyFile
	}
	a.mu.Unlock()
	return a.persist()
}

// ensureGroupExists adds a group entry if the name is new (caller holds a.mu).
func (a *App) ensureGroupExists(name string) {
	if name == "" {
		return
	}
	for i := range a.inv.DeviceGroups {
		if a.inv.DeviceGroups[i].Name == name {
			return
		}
	}
	a.inv.DeviceGroups = append(a.inv.DeviceGroups, model.DeviceGroup{Name: name})
}

// DeleteDevice removes a device by name.
func (a *App) DeleteDevice(name string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	out := a.inv.Devices[:0]
	for _, d := range a.inv.Devices {
		if d.Name != name {
			out = append(out, d)
		}
	}
	a.inv.Devices = out
	a.mu.Unlock()
	return a.persist()
}

// SetDeviceEnabled toggles a device's inclusion in batch runs.
func (a *App) SetDeviceEnabled(name string, enabled bool) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	for i := range a.inv.Devices {
		if a.inv.Devices[i].Name == name {
			a.inv.Devices[i].Enabled = enabled
		}
	}
	a.mu.Unlock()
	return a.persist()
}

// SetAllDevicesEnabled enables or disables every device in one shot (bulk
// check / uncheck). If names is non-empty, only those devices are affected.
func (a *App) SetAllDevicesEnabled(enabled bool, names []string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	var only map[string]bool
	if len(names) > 0 {
		only = make(map[string]bool, len(names))
		for _, n := range names {
			only[n] = true
		}
	}
	for i := range a.inv.Devices {
		if only == nil || only[a.inv.Devices[i].Name] {
			a.inv.Devices[i].Enabled = enabled
		}
	}
	a.mu.Unlock()
	return a.persist()
}

// CopyDevice duplicates a device under a new unique name and returns that name.
func (a *App) CopyDevice(name string) (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	var src *model.Device
	existing := map[string]bool{}
	for i := range a.inv.Devices {
		existing[a.inv.Devices[i].Name] = true
		if a.inv.Devices[i].Name == name {
			d := a.inv.Devices[i]
			src = &d
		}
	}
	if src == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("device %q not found", name)
	}
	newName := name + "_copy"
	for i := 2; existing[newName]; i++ {
		newName = fmt.Sprintf("%s_copy%d", name, i)
	}
	dup := *src
	dup.Name = newName
	a.inv.Devices = append(a.inv.Devices, dup)
	a.mu.Unlock()
	return newName, a.persist()
}

// RenameDevice changes a device's name (its key) in place, e.g. from the
// inline name cell in the device list.
func (a *App) RenameDevice(oldName, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("名前は必須です")
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	idx := -1
	for i := range a.inv.Devices {
		if a.inv.Devices[i].Name == newName {
			a.mu.Unlock()
			return fmt.Errorf("「%s」は既に登録されています", newName)
		}
		if a.inv.Devices[i].Name == oldName {
			idx = i
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("device %q not found", oldName)
	}
	a.inv.Devices[idx].Name = newName
	a.mu.Unlock()
	return a.persist()
}

// DeviceNameExists reports whether a device name is already taken (used by the
// UI to prevent duplicate names when adding).
func (a *App) DeviceNameExists(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return false
	}
	for i := range a.inv.Devices {
		if a.inv.Devices[i].Name == name {
			return true
		}
	}
	return false
}

// ---- Device groups (named device selections) ----

// SaveDeviceGroup inserts or updates a device group by name.
func (a *App) SaveDeviceGroup(g model.DeviceGroup) error {
	if g.Name == "" {
		return fmt.Errorf("グループ名は必須です")
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	found := false
	for i := range a.inv.DeviceGroups {
		if a.inv.DeviceGroups[i].Name == g.Name {
			a.inv.DeviceGroups[i] = g
			found = true
			break
		}
	}
	if !found {
		a.inv.DeviceGroups = append(a.inv.DeviceGroups, g)
	}
	a.mu.Unlock()
	return a.persist()
}

// CopyDeviceGroup duplicates a group — its default credentials and every
// one of its devices — under newName. Device names must stay unique (they
// name the log files), so each copy gets suffix appended; the copy is
// refused whole if any resulting name is taken. Returns the device count.
func (a *App) CopyDeviceGroup(name, newName, suffix string) (int, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return 0, fmt.Errorf("グループ名は必須です")
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return 0, fmt.Errorf("vault is locked")
	}
	src := a.inv.Group(name)
	if src == nil {
		a.mu.Unlock()
		return 0, fmt.Errorf("グループ「%s」が見つかりません", name)
	}
	if a.inv.Group(newName) != nil {
		a.mu.Unlock()
		return 0, fmt.Errorf("同名のグループ「%s」が既にあります", newName)
	}
	taken := map[string]bool{}
	for _, d := range a.inv.Devices {
		taken[d.Name] = true
	}
	var copies []model.Device
	for _, d := range a.inv.Devices {
		if d.Group != name {
			continue
		}
		c := d
		c.Name = d.Name + suffix
		c.Group = newName
		c.Bastions = append([]model.Bastion(nil), d.Bastions...)
		if taken[c.Name] {
			a.mu.Unlock()
			return 0, fmt.Errorf("機器名「%s」は既に使われています。別の接尾辞を指定してください", c.Name)
		}
		taken[c.Name] = true
		copies = append(copies, c)
	}
	g := *src
	g.Name = newName
	g.Members = nil
	a.inv.DeviceGroups = append(a.inv.DeviceGroups, g)
	a.inv.Devices = append(a.inv.Devices, copies...)
	a.mu.Unlock()
	return len(copies), a.persist()
}

// DeleteDeviceGroup removes a device group. Its devices are kept but ungrouped.
func (a *App) DeleteDeviceGroup(name string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	out := a.inv.DeviceGroups[:0]
	for _, g := range a.inv.DeviceGroups {
		if g.Name != name {
			out = append(out, g)
		}
	}
	a.inv.DeviceGroups = out
	for i := range a.inv.Devices {
		if a.inv.Devices[i].Group == name {
			a.inv.Devices[i].Group = ""
		}
	}
	a.mu.Unlock()
	return a.persist()
}

// RenameDeviceGroup renames a group and updates its devices' Group field.
func (a *App) RenameDeviceGroup(oldName, newName string) error {
	if newName == "" {
		return fmt.Errorf("新しいグループ名を入力してください")
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	for i := range a.inv.DeviceGroups {
		if a.inv.DeviceGroups[i].Name == oldName {
			a.inv.DeviceGroups[i].Name = newName
		}
	}
	for i := range a.inv.Devices {
		if a.inv.Devices[i].Group == oldName {
			a.inv.Devices[i].Group = newName
		}
	}
	a.mu.Unlock()
	return a.persist()
}

// ---- Command sets ----

// SaveCommandSet inserts or updates a command set by name.
func (a *App) SaveCommandSet(cs model.CommandSet) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	found := false
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == cs.Name {
			a.inv.CommandSets[i] = cs
			found = true
			break
		}
	}
	if !found {
		a.inv.CommandSets = append(a.inv.CommandSets, cs)
	}
	a.mu.Unlock()
	return a.persist()
}

// RenameCommandSet gives a command set a new name and points every device
// that used the old name at the new one, so the assignment survives the
// rename. Devices refer to sets by name (CSV, bundles and the run tab all
// do), which is why the set editor could not simply save under a new name:
// that would have left the devices on a set that no longer existed.
func (a *App) RenameCommandSet(oldName, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("セット名は必須です")
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	if oldName == newName {
		a.mu.Unlock()
		return nil
	}
	idx := -1
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == newName {
			a.mu.Unlock()
			return fmt.Errorf("「%s」は既にあります", newName)
		}
		if a.inv.CommandSets[i].Name == oldName {
			idx = i
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("command set %q not found", oldName)
	}
	a.inv.CommandSets[idx].Name = newName
	for i := range a.inv.Devices {
		if a.inv.Devices[i].CommandSet == oldName {
			a.inv.Devices[i].CommandSet = newName
		}
	}
	a.mu.Unlock()
	return a.persist()
}

// CopyCommandSet duplicates a command set under a new unique name and returns
// that name.
func (a *App) CopyCommandSet(name string) (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	var src *model.CommandSet
	existing := map[string]bool{}
	for i := range a.inv.CommandSets {
		existing[a.inv.CommandSets[i].Name] = true
		if a.inv.CommandSets[i].Name == name {
			cs := a.inv.CommandSets[i]
			src = &cs
		}
	}
	if src == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("command set %q not found", name)
	}
	newName := name + "_copy"
	for i := 2; existing[newName]; i++ {
		newName = fmt.Sprintf("%s_copy%d", name, i)
	}
	dup := *src
	dup.Name = newName
	dup.Commands = append([]model.Command(nil), src.Commands...)
	a.inv.CommandSets = append(a.inv.CommandSets, dup)
	a.mu.Unlock()
	return newName, a.persist()
}

// DeleteCommandSet removes a command set by name.
func (a *App) DeleteCommandSet(name string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	out := a.inv.CommandSets[:0]
	for _, cs := range a.inv.CommandSets {
		if cs.Name != name {
			out = append(out, cs)
		}
	}
	a.inv.CommandSets = out
	a.mu.Unlock()
	return a.persist()
}

// SaveSettings replaces the run-wide settings.
func (a *App) SaveSettings(s model.Settings) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	// The form never shows LastKeyFile; keep it instead of blanking it.
	s.LastKeyFile = a.inv.Settings.LastKeyFile
	// Stage words: trimmed, defaults stored as empty, and checked before
	// they can produce a bad file name or a mislabelled history row.
	s.StageTokenBefore, s.StageTokenWork, s.StageTokenAfter = strings.TrimSpace(s.StageTokenBefore), strings.TrimSpace(s.StageTokenWork), strings.TrimSpace(s.StageTokenAfter)
	if err := s.StageTokens().Validate(); err != nil {
		a.mu.Unlock()
		return err
	}
	if s.StageTokenBefore == logstore.DefaultStageTokens.Before {
		s.StageTokenBefore = ""
	}
	if s.StageTokenWork == logstore.DefaultStageTokens.Work {
		s.StageTokenWork = ""
	}
	if s.StageTokenAfter == logstore.DefaultStageTokens.After {
		s.StageTokenAfter = ""
	}
	a.inv.Settings = s
	a.mu.Unlock()
	return a.persist()
}

// ---- Execution ----

// RunRequest is what the run tab sends to start a batch.
type RunRequest struct {
	Names  []string `json:"names"`  // devices to run (checkbox state is left alone)
	Group  string   `json:"group"`  // group shown in the run tab (recorded in the run summary)
	Stage  string   `json:"stage"`  // "" / before / during / after
	DryRun bool     `json:"dryRun"` // connect + login only, no commands, no logs
}

// RunDone is the "run:done" payload: the per-device results plus the folder
// the logs were written to ("" for a dry run).
type RunDone struct {
	Results []runpkg.DeviceResult `json:"results"`
	RunDir  string                `json:"runDir,omitempty"`
	DryRun  bool                  `json:"dryRun,omitempty"`
}

// RunBatch runs the currently enabled devices, emitting live progress events
// ("run:event") and a final "run:done" with the results.
func (a *App) RunBatch() error {
	return a.run(RunRequest{})
}

// RunSelected runs a specific set of devices by name (ignores Enabled flag).
func (a *App) RunSelected(names []string) error {
	return a.run(RunRequest{Names: names})
}

// RunWith runs with the full set of per-run choices (stage, dry run).
func (a *App) RunWith(req RunRequest) error {
	return a.run(req)
}

func (a *App) run(req RunRequest) error {
	stage := model.Stage(req.Stage)
	if !stage.Valid() {
		return fmt.Errorf("作業タイミングの値が不正です: %q", req.Stage)
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	// Build a snapshot inventory for this run, with each device's group
	// default credentials substituted where the device asks for them.
	snap := *a.inv
	var want map[string]bool
	if req.Names != nil {
		want = make(map[string]bool, len(req.Names))
		for _, n := range req.Names {
			want[n] = true
		}
	}
	var sel []model.Device
	for _, d := range a.inv.Devices {
		if want != nil {
			if !want[d.Name] {
				continue
			}
			d.Enabled = true
		}
		sel = append(sel, a.inv.ResolveCredentials(d))
	}
	snap.Devices = sel
	if a.runActive {
		a.mu.Unlock()
		return fmt.Errorf("一括実行中です。完了または中止を待ってください")
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.runCancel = cancel
	a.runActive = true
	a.mu.Unlock()

	go func() {
		defer cancel()
		emit := func(ev runpkg.Event) {
			runtime.EventsEmit(a.ctx, "run:event", ev)
		}
		started := time.Now()
		results, runDir := a.runner.RunBatchOpts(ctx, &snap, runpkg.Options{Stage: stage, DryRun: req.DryRun, Group: req.Group}, emit)
		if runDir != "" {
			writeRunSummary(runDir, &snap, req, started, results)
		}
		a.mu.Lock()
		a.runActive = false
		a.mu.Unlock()
		runtime.EventsEmit(a.ctx, "run:done", RunDone{Results: results, RunDir: runDir, DryRun: req.DryRun})
	}()
	return nil
}

// writeRunSummary records the batch in its log folder for the 実行履歴 tab.
// Best effort: a summary that cannot be written costs the history row, not
// the logs.
func writeRunSummary(runDir string, inv *model.Inventory, req RunRequest, started time.Time, results []runpkg.DeviceResult) {
	byName := map[string]model.Device{}
	for _, d := range inv.Devices {
		byName[d.Name] = d
	}
	s := history.Summary{
		App:        "PalaTerm " + appVersion,
		StartedAt:  started.Format(time.RFC3339),
		FinishedAt: time.Now().Format(time.RFC3339),
		Group:      req.Group,
		Stage:      req.Stage,
		DryRun:     req.DryRun,
		Devices:    []history.DeviceSummary{},
	}
	for _, r := range results {
		d := byName[r.Device]
		ds := history.DeviceSummary{
			Name: r.Device, Host: d.Host, Site: d.Site, CommandSet: d.CommandSet,
			Success: r.Success, Canceled: r.Canceled, Error: r.Error,
			ElapsedSec: r.Elapsed.Seconds(),
		}
		if r.LogPath != "" {
			ds.LogFile = filepath.Base(r.LogPath)
		}
		s.Devices = append(s.Devices, ds)
	}
	_ = history.Write(runDir, s)
}

// DangerWarning lists the commands in one device's command set that would
// change the device (see package guard).
type DangerWarning struct {
	Device     string      `json:"device"`
	CommandSet string      `json:"commandSet"`
	Hits       []guard.Hit `json:"hits"`
}

// CheckDangerousCommands scans the named devices' command sets for
// configuration-changing commands, so the run tab can warn before starting.
// Devices sharing a command set are each listed (the user reads it per
// device), but the scan itself runs once per set.
func (a *App) CheckDangerousCommands(names []string) []DangerWarning {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []DangerWarning{}
	if a.inv == nil {
		return out
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	sets := map[string]*model.CommandSet{}
	for i := range a.inv.CommandSets {
		sets[a.inv.CommandSets[i].Name] = &a.inv.CommandSets[i]
	}
	scanned := map[string][]guard.Hit{}
	for _, d := range a.inv.Devices {
		if !want[d.Name] || d.CommandSet == "" {
			continue
		}
		hits, ok := scanned[d.CommandSet]
		if !ok {
			if cs := sets[d.CommandSet]; cs != nil {
				texts := make([]string, len(cs.Commands))
				for i, c := range cs.Commands {
					texts[i] = c.Text
				}
				hits = guard.Check(texts)
			}
			scanned[d.CommandSet] = hits
		}
		if len(hits) > 0 {
			out = append(out, DangerWarning{Device: d.Name, CommandSet: d.CommandSet, Hits: hits})
		}
	}
	return out
}

// ---- 実行履歴 ----

// ListRunHistory returns past batches (newest first) found under the log
// folder: one row per log_<timestamp>[_<stage>] sub-folder.
func (a *App) ListRunHistory() ([]history.Run, error) {
	a.mu.Lock()
	var tok logstore.StageTokens
	if a.inv != nil {
		tok = a.inv.Settings.StageTokens()
	}
	a.mu.Unlock()
	runs, err := history.List(a.logRoot(), tok)
	if runs == nil {
		runs = []history.Run{}
	}
	return runs, err
}

// DiffLogFiles compares two saved logs line by line (A = before, B = after).
// Both must sit under the log folder. With ignoreNoise, lines that differ
// only in clock readings, uptimes and traffic counters count as unchanged.
func (a *App) DiffLogFiles(pathA, pathB string, ignoreNoise bool) (*history.DiffResult, error) {
	for _, p := range []string{pathA, pathB} {
		if err := a.underLogRoot(p); err != nil {
			return nil, err
		}
	}
	ba, err := os.ReadFile(pathA)
	if err != nil {
		return nil, err
	}
	bb, err := os.ReadFile(pathB)
	if err != nil {
		return nil, err
	}
	r := history.Diff(string(ba), string(bb), ignoreNoise)
	if r.Lines == nil {
		r.Lines = []history.DiffLine{}
	}
	return &r, nil
}

// OpenRunFolder opens one run's folder in Explorer (log folder only).
func (a *App) OpenRunFolder(dir string) error {
	if err := a.underLogRoot(dir); err != nil {
		return err
	}
	return exec.Command("explorer", dir).Start()
}

// logRoot is the absolute log folder from the settings (locked: logs/).
func (a *App) logRoot() string {
	a.mu.Lock()
	dir := "logs"
	if a.inv != nil && a.inv.Settings.LogDir != "" {
		dir = a.inv.Settings.LogDir
	}
	a.mu.Unlock()
	return logstore.ResolveRoot(dir)
}

// underLogRoot refuses a path outside the configured log folder, so the
// history tab's file arguments cannot be pointed at arbitrary files.
func (a *App) underLogRoot(p string) error {
	root, err := filepath.Abs(a.logRoot())
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("ログフォルダの外は開けません")
	}
	return nil
}

// CancelRun aborts an in-progress run.
func (a *App) CancelRun() {
	a.mu.Lock()
	c := a.runCancel
	a.mu.Unlock()
	if c != nil {
		c()
	}
}

// ---- Convenience ----

// PickKeyFile opens a file dialog for choosing an SSH private key. The chosen
// path is remembered in Settings.LastKeyFile so the device editor can pre-fill
// it next time. Returns "" if the dialog was cancelled.
// PickLogDir opens a folder picker for the log output directory.
func (a *App) PickLogDir() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "ログ保存フォルダを選択",
	})
}

func (a *App) PickKeyFile() (string, error) {
	a.mu.Lock()
	def := ""
	if a.inv != nil && a.inv.Settings.LastKeyFile != "" {
		def = filepath.Dir(a.inv.Settings.LastKeyFile)
	}
	a.mu.Unlock()
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "秘密鍵ファイルを選択",
		DefaultDirectory: def,
		Filters: []runtime.FileFilter{
			{DisplayName: "秘密鍵 (id_*;*.pem;*.key)", Pattern: "id_*;*.pem;*.key"},
			{DisplayName: "すべてのファイル", Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	a.rememberKeyFile(path)
	return path, nil
}

// rememberKeyFile stores the last used private-key path (best effort).
func (a *App) rememberKeyFile(path string) {
	if path == "" {
		return
	}
	a.mu.Lock()
	if a.inv == nil || a.inv.Settings.LastKeyFile == path {
		a.mu.Unlock()
		return
	}
	a.inv.Settings.LastKeyFile = path
	a.mu.Unlock()
	_ = a.persist()
}

// OpenLogDir opens the log output folder in the system file manager.
func (a *App) OpenLogDir() error {
	a.mu.Lock()
	dir := "logs"
	if a.inv != nil && a.inv.Settings.LogDir != "" {
		dir = a.inv.Settings.LogDir
	}
	a.mu.Unlock()
	abs := logstore.ResolveRoot(dir)
	_ = os.MkdirAll(abs, 0o755)
	return exec.Command("explorer", abs).Start()
}

// StrictVerify picks the setting lines typed in the work log and reports
// whether each is present in the after log (see history.StrictVerify). All
// three files must lie under the log folder.
func (a *App) StrictVerify(workPath, beforePath, afterPath string) (*history.StrictResult, error) {
	read := func(p string) (string, error) {
		if err := a.underLogRoot(p); err != nil {
			return "", err
		}
		b, err := os.ReadFile(p)
		return string(b), err
	}
	w, err := read(workPath)
	if err != nil {
		return nil, err
	}
	bf, err := read(beforePath)
	if err != nil {
		return nil, err
	}
	af, err := read(afterPath)
	if err != nil {
		return nil, err
	}
	r := history.StrictVerify(w, bf, af)
	return &r, nil
}

// OpenDiffWindow compares two saved logs in their own window (PalaTerm.exe
// --diff). Both files must lie under the log folder.
func (a *App) OpenDiffWindow(pathA, pathB, label string) error {
	for _, p := range []string{pathA, pathB} {
		if err := a.underLogRoot(p); err != nil {
			return err
		}
		if _, err := os.Stat(p); err != nil {
			return err
		}
	}
	return spawnWindow(nil, "--diff", pathA, pathB, label)
}

// OpenLogWindow shows a saved log in its own window (PalaTerm.exe --view).
// Only files under the log folder are opened.
func (a *App) OpenLogWindow(path string) error {
	if err := a.underLogRoot(path); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return spawnWindow(nil, "--view", path)
}

// ReadLogFile returns the contents of a saved log for the in-app viewer.
func (a *App) ReadLogFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---- Interactive terminal (single-device manual operation) ----

// termMsg is the payload for term:data events (data is base64-encoded bytes).
type termMsg struct {
	Device string `json:"device"`
	Data   string `json:"data"`
}

// termClosed is the payload for term:closed events.
type termClosed struct {
	Device  string `json:"device"`
	Error   string `json:"error,omitempty"`
	LogPath string `json:"logPath,omitempty"`
	// Takeover: the automatic login failed but the line is still open, and
	// the window offers to hand it to the keyboard (Term.Takeover).
	Takeover bool `json:"takeover,omitempty"`
}

// SpawnTerminal opens the interactive terminal for a device in a separate,
// independent OS window (a child PalaTerm process). Several can run at once, and
// each window resizes freely. The master password is handed to the child on
// stdin so it never appears in the process arguments. manual opens the line
// without the automatic login (see Term.manual).
func (a *App) SpawnTerminal(name string, manual bool) error {
	a.mu.Lock()
	locked := a.inv == nil
	pw := a.password
	a.mu.Unlock()
	if locked {
		return fmt.Errorf("vault is locked")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"--connect", name}
	if manual {
		args = append(args, "--manual")
	}
	cmd := exec.Command(exe, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	// The child never writes the vault; it reports the one thing it needs
	// persisted — the user allowing a changed host key — on stdout, and the
	// main window applies it.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_, _ = io.WriteString(stdin, pw+"\n")
		_ = stdin.Close()
	}()
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if dev, ok := strings.CutPrefix(sc.Text(), termEventClearHostKey+" "); ok {
				_ = a.ClearHostKeys(strings.TrimSpace(dev))
			}
		}
		_ = cmd.Wait()
	}()
	return nil
}

// termEventClearHostKey is the stdout line a terminal window prints when the
// user accepted a changed host key, followed by the device name.
const termEventClearHostKey = "PALATERM clear-hostkey"

// ConnectInteractive logs into a single device (running the OS profile login
// and pager-disable) and then hands the session to a live terminal. Device
// output arrives on "term:data" events; the session ending fires "term:closed".
func (a *App) ConnectInteractive(name string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	var dev *model.Device
	for i := range a.inv.Devices {
		if a.inv.Devices[i].Name == name {
			d := a.inv.ResolveCredentials(a.inv.Devices[i])
			dev = &d
			break
		}
	}
	settings := a.inv.Settings
	a.mu.Unlock()
	if dev == nil {
		return fmt.Errorf("device %q not found", name)
	}

	a.interMu.Lock()
	if _, ok := a.interactives[name]; ok {
		a.interMu.Unlock()
		return fmt.Errorf("%s は既に接続中です", name)
	}
	// Reserve the slot so a second click can't double-connect.
	a.interactives[name] = &interactiveSession{}
	a.interMu.Unlock()

	go func() {
		// nil emit: interactive connect must NOT post run:event, or it would
		// mark the device "接続中" in the Run tab (and never clear on failure).
		exp, _, err := a.runner.Connect(context.Background(), dev, settings, nil)
		if err != nil {
			if exp != nil {
				exp.Close()
			}
			a.interMu.Lock()
			delete(a.interactives, name)
			a.interMu.Unlock()
			runtime.EventsEmit(a.ctx, "term:closed", termClosed{Device: name, Error: err.Error()})
			return
		}

		// Accumulate the whole session (login banner + everything typed/seen)
		// so it can be written to a log file when the terminal closes.
		var logMu sync.Mutex
		var logBuf []byte
		sink := func(b []byte) {
			logMu.Lock()
			logBuf = append(logBuf, b...)
			logMu.Unlock()
			runtime.EventsEmit(a.ctx, "term:data", termMsg{Device: name, Data: base64.StdEncoding.EncodeToString(b)})
		}
		onClose := func() {
			a.interMu.Lock()
			delete(a.interactives, name)
			a.interMu.Unlock()
			logMu.Lock()
			data := append([]byte(nil), logBuf...)
			logMu.Unlock()
			logPath := ""
			if len(data) > 0 {
				if p, e := logstore.Write(settings.LogDir, "{host}_interactive_{date}_{time}.txt",
					logstore.Fields{Host: dev.Name, IP: dev.Host, OS: dev.OSType, Group: dev.Group, Site: dev.Site, Role: dev.Role},
					string(data), time.Now()); e == nil {
					logPath = p
				}
			}
			runtime.EventsEmit(a.ctx, "term:closed", termClosed{Device: name, LogPath: logPath})
		}
		send, resize, closeFn := a.runner.StartInteractive(exp, sink, onClose)
		a.interMu.Lock()
		a.interactives[name] = &interactiveSession{send: send, resize: resize, close: closeFn}
		a.interMu.Unlock()
		runtime.EventsEmit(a.ctx, "term:ready", name)
	}()
	return nil
}

// SendInteractive forwards keystrokes (base64-encoded) to a live terminal.
func (a *App) SendInteractive(name, dataB64 string) error {
	a.interMu.Lock()
	s := a.interactives[name]
	a.interMu.Unlock()
	if s == nil || s.send == nil {
		return fmt.Errorf("%s は接続していません", name)
	}
	raw, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return err
	}
	return s.send(string(raw))
}

// ResizeInteractive tells the device its terminal size changed (SSH only).
func (a *App) ResizeInteractive(name string, cols, rows int) error {
	a.interMu.Lock()
	s := a.interactives[name]
	a.interMu.Unlock()
	if s == nil || s.resize == nil {
		return nil
	}
	return s.resize(cols, rows)
}

// CloseInteractive ends a live terminal session.
func (a *App) CloseInteractive(name string) {
	a.interMu.Lock()
	s := a.interactives[name]
	a.interMu.Unlock()
	if s != nil && s.close != nil {
		_ = s.close()
	}
}

// ---- command set file export / import (旧 Show_Command_Pattern_*.list 相当) ----

// appVersion is recorded in bundle manifests. The About screen has its own
// copy (APP_VERSION in frontend/dist/app.js) and the exe resource lives in
// build/windows/winres.json — bump all three together.
const appVersion = "1.5.10"

// ImportCommandSetFile reads one "コマンド,リモート秒,シリアル秒" file into a
// command set named after the file (an existing set of the same name is
// replaced). Legacy Show_Command_Pattern .list files import as-is. This is
// the single-file path; a whole bundle goes through PickImportFile.
func (a *App) ImportCommandSetFile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "コマンドセットのファイルを読み込む",
		DefaultDirectory: filepath.Join(exeDir(), "export", "command-sets"),
		Filters: []runtime.FileFilter{
			{DisplayName: "コマンドセット (*.csv;*.list;*.txt)", Pattern: "*.csv;*.list;*.txt"},
			{DisplayName: "すべてのファイル", Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	cmds, err := bundle.ParseCommandSet(string(data))
	if err != nil {
		return "", err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	found := false
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == name {
			a.inv.CommandSets[i].Commands = cmds
			found = true
			break
		}
	}
	if !found {
		a.inv.CommandSets = append(a.inv.CommandSets, model.CommandSet{Name: name, Commands: cmds})
	}
	a.mu.Unlock()
	return name, a.persist()
}

// ImportOSProfileFile reads one exported profile JSON. A profile with the
// same key (or, failing that, the same name) is replaced — so re-importing a
// deleted default restores it under its original key and devices that
// referenced it work again; otherwise the file is added as a new profile.
func (a *App) ImportOSProfileFile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "OSタイププロファイルのファイルを読み込む",
		DefaultDirectory: filepath.Join(exeDir(), "export", "os-profiles"),
		Filters: []runtime.FileFilter{
			{DisplayName: "OSプロファイル (*.json)", Pattern: "*.json"},
			{DisplayName: "すべてのファイル", Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	p, err := bundle.ParseProfileJSON(data)
	if err != nil {
		return "", err
	}

	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	found := false
	for i := range a.inv.CustomProfiles {
		if a.inv.CustomProfiles[i].Key == p.Key {
			a.inv.CustomProfiles[i] = p
			found = true
			break
		}
	}
	if !found {
		// Same name under a different key: replace that profile but keep its
		// key, so devices referencing it stay valid.
		for i := range a.inv.CustomProfiles {
			if strings.EqualFold(a.inv.CustomProfiles[i].Name, p.Name) {
				p.Key = a.inv.CustomProfiles[i].Key
				a.inv.CustomProfiles[i] = p
				found = true
				break
			}
		}
	}
	if !found {
		a.inv.CustomProfiles = append(a.inv.CustomProfiles, p)
	}
	a.mu.Unlock()
	a.profiles.Add(profile.Quote(p))
	return p.Name, a.persist()
}

// ---- 一式 (bundle) export / import ----

// bundleDir is where ExportBundle writes: export\bundles\<グループ名>\, or
// export\bundles\all-devices\ for the whole inventory.
func bundleDir(group string) string {
	name := "all-devices"
	if group != "" {
		name = bundle.FileNameSafe(group)
	}
	return filepath.Join(exeDir(), "export", "bundles", name)
}

// ExportBundle writes one group's devices together with the command sets and
// OS profiles they use — or, for group "", every device, set and profile —
// to export\bundles\<group>\ and returns that folder. The folder is the
// hand-over unit: copy it to another PalaTerm and pick its
// palaterm-bundle.json in 読込. devices.csv inside it is the same CSV as
// before, so Excel editing still works.
func (a *App) ExportBundle(group string) (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	c := bundle.Select(a.inv, group)
	a.mu.Unlock()
	if group != "" && len(c.Devices) == 0 {
		return "", fmt.Errorf("グループ「%s」に機器がありません", group)
	}
	dir := bundleDir(group)
	if _, err := bundle.Write(dir, c, "PalaTerm "+appVersion); err != nil {
		return "", err
	}
	return dir, nil
}

// ImportPreview is what PickImportFile hands the UI to confirm before
// ApplyImport: the chosen file, what it contains and what applying it would do.
type ImportPreview struct {
	Path     string         `json:"path"`
	Kind     string         `json:"kind"`  // "bundle" | "csv"
	Group    string         `json:"group"` // group named in the manifest ("" = all devices / plain CSV)
	Sets     []string       `json:"sets"`
	Profiles []string       `json:"profiles"`
	Summary  bundle.Summary `json:"summary"`
}

// PickImportFile lets the user choose a bundle manifest or a bare devices CSV
// and returns a preview of applying it with the given options. Nothing is
// changed yet: the UI shows the counts, then calls ApplyImport with the same
// path and options. Returns nil when the dialog is cancelled.
func (a *App) PickImportFile(targetGroup string, overwriteShared bool) (*ImportPreview, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "一式の目録（palaterm-bundle.json）または機器CSVを読み込む",
		DefaultDirectory: filepath.Join(exeDir(), "export", "bundles"),
		Filters: []runtime.FileFilter{
			{DisplayName: "一式の目録 / 機器CSV", Pattern: "palaterm-bundle.json;*.csv"},
			{DisplayName: "すべてのファイル", Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return nil, err
	}
	c, kind, err := readImportFile(path)
	if err != nil {
		return nil, err
	}
	pv := &ImportPreview{Path: path, Kind: kind, Group: c.Manifest.Group, Sets: []string{}, Profiles: []string{}}
	for _, s := range c.CommandSets {
		pv.Sets = append(pv.Sets, s.Name)
	}
	for _, p := range c.Profiles {
		pv.Profiles = append(pv.Profiles, p.Name)
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("vault is locked")
	}
	pv.Summary = bundle.Preview(a.inv, c, bundle.Options{TargetGroup: targetGroup, OverwriteShared: overwriteShared})
	a.mu.Unlock()
	return pv, nil
}

// readImportFile loads a manifest (.json) as a bundle or anything else as a
// devices CSV.
func readImportFile(path string) (bundle.Contents, string, error) {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		c, err := bundle.Read(path)
		return c, "bundle", err
	}
	c, err := bundle.ReadDevicesCSV(path)
	return c, "csv", err
}

// ApplyImport merges the previewed file into the vault (rules: bundle.Apply)
// and returns what happened.
func (a *App) ApplyImport(path, targetGroup string, overwriteShared bool) (*bundle.Summary, error) {
	c, _, err := readImportFile(path)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("vault is locked")
	}
	s, changed := bundle.Apply(a.inv, c, bundle.Options{TargetGroup: targetGroup, OverwriteShared: overwriteShared})
	a.mu.Unlock()
	for _, p := range changed {
		a.profiles.Add(profile.Quote(p))
	}
	if err := a.persist(); err != nil {
		return nil, err
	}
	return &s, nil
}

// OpenExportFolder opens a folder under export\ in Explorer (offered after
// 一式書出). Anything outside export\ is refused.
func (a *App) OpenExportFolder(dir string) error {
	root, err := filepath.Abs(filepath.Join(exeDir(), "export"))
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("export フォルダの外は開けません")
	}
	return exec.Command("explorer", abs).Start()
}

// ReorderDeviceGroups sets the display order of groups to match the given list
// of names (any groups not listed keep their relative order at the end).
func (a *App) ReorderDeviceGroups(order []string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	sort.SliceStable(a.inv.DeviceGroups, func(i, j int) bool {
		pi, oki := pos[a.inv.DeviceGroups[i].Name]
		pj, okj := pos[a.inv.DeviceGroups[j].Name]
		if oki && okj {
			return pi < pj
		}
		if oki != okj {
			return oki
		}
		return false
	})
	a.mu.Unlock()
	return a.persist()
}

// reorderSlots rewrites the relative order of the items whose key is listed
// in order, leaving every other item exactly where it is: the listed items'
// current slots are refilled left-to-right in the requested order. This lets
// a scoped view (e.g. one group's devices) reorder its own rows without
// disturbing the rest of the slice.
func reorderSlots[T any](items []T, keyOf func(T) string, order []string) {
	want := map[string]int{}
	for i, k := range order {
		want[k] = i
	}
	byKey := map[string]T{}
	var slots []int
	for i := range items {
		if _, ok := want[keyOf(items[i])]; ok {
			byKey[keyOf(items[i])] = items[i]
			slots = append(slots, i)
		}
	}
	if len(slots) != len(order) {
		return // stale view: keys changed underneath; keep the stored order
	}
	for i, k := range order {
		items[slots[i]] = byKey[k]
	}
}

// ReorderDevices rewrites the relative order of the named devices (typically
// one group's rows) without moving any other device.
func (a *App) ReorderDevices(order []string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	reorderSlots(a.inv.Devices, func(d model.Device) string { return d.Name }, order)
	a.mu.Unlock()
	return a.persist()
}

// ReorderCommandSets rewrites the command-set list order.
func (a *App) ReorderCommandSets(order []string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	reorderSlots(a.inv.CommandSets, func(s model.CommandSet) string { return s.Name }, order)
	a.mu.Unlock()
	return a.persist()
}

// ReorderProfiles rewrites the OS profile list order (by key). Dropdowns
// follow this order too (see ListProfiles).
func (a *App) ReorderProfiles(order []string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	reorderSlots(a.inv.CustomProfiles, func(p profile.Profile) string { return p.Key }, order)
	a.mu.Unlock()
	return a.persist()
}

// ProfileKeys returns sorted profile keys (helper for the UI).
func (a *App) ProfileKeys() []string {
	var keys []string
	for _, p := range a.profiles.List() {
		keys = append(keys, p.Key)
	}
	sort.Strings(keys)
	return keys
}
