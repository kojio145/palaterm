package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/kojio145/palaterm/internal/csvio"
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
	// Materialize the tool's folder layout next to the exe so users can find
	// everything: logs / export destinations / internal parts (data).
	for _, d := range []string{"logs", filepath.Join("export", "devices"), filepath.Join("export", "command-sets"), filepath.Join("export", "os-profiles"), "data"} {
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
		path := filepath.Join(profDir, fileNameSafe(p.Name)+".json")
		if data, err := os.ReadFile(path); err == nil && !strings.Contains(string(data), `"regex"`) {
			continue
		}
		_ = writeProfileJSON(profDir, p)
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
		Version:        1,
		Settings:       model.DefaultSettings(),
		CommandSets:    model.DefaultCommandSets(),
		DeviceGroups:   []model.DeviceGroup{{Name: "サンプルグループ"}},
		CustomProfiles: profile.Defaults(),
		ProfilesSeeded: true,
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
	a.inv.Settings = s
	a.mu.Unlock()
	return a.persist()
}

// ---- Execution ----

// RunBatch runs the currently enabled devices, emitting live progress events
// ("run:event") and a final "run:done" with the results.
func (a *App) RunBatch() error {
	return a.run(nil)
}

// RunSelected runs a specific set of devices by name (ignores Enabled flag).
func (a *App) RunSelected(names []string) error {
	return a.run(names)
}

func (a *App) run(only []string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	// Build a snapshot inventory for this run.
	snap := *a.inv
	if only != nil {
		want := make(map[string]bool, len(only))
		for _, n := range only {
			want[n] = true
		}
		var sel []model.Device
		for _, d := range a.inv.Devices {
			if want[d.Name] {
				d.Enabled = true
				sel = append(sel, d)
			}
		}
		snap.Devices = sel
	}
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
		results := a.runner.RunBatch(ctx, &snap, emit)
		a.mu.Lock()
		a.runActive = false
		a.mu.Unlock()
		runtime.EventsEmit(a.ctx, "run:done", results)
	}()
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
}

// SpawnTerminal opens the interactive terminal for a device in a separate,
// independent OS window (a child PalaTerm process). Several can run at once, and
// each window resizes freely. The master password is handed to the child on
// stdin so it never appears in the process arguments.
func (a *App) SpawnTerminal(name string) error {
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
	cmd := exec.Command(exe, "--connect", name)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_, _ = io.WriteString(stdin, pw+"\n")
		_ = stdin.Close()
		_ = cmd.Wait()
	}()
	return nil
}

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
			d := a.inv.Devices[i]
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
					logstore.Fields{Host: dev.Name, IP: dev.Host, OS: dev.OSType, Group: dev.Group, Site: dev.Site},
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

// fileNameSafe replaces characters Windows forbids in file names.
func fileNameSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, s)
}

// ExportCommandSets writes every command set to export\command-sets\<セット名>.csv
// (one file per set; lines of "コマンド,リモート待機秒,シリアル待機秒" — the
// legacy Show_Command_Pattern format). Returns the folder written to.
func (a *App) ExportCommandSets() (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	sets := make([]model.CommandSet, len(a.inv.CommandSets))
	copy(sets, a.inv.CommandSets)
	a.mu.Unlock()

	dir := filepath.Join(exeDir(), "export", "command-sets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, s := range sets {
		var b strings.Builder
		for _, c := range s.Commands {
			fmt.Fprintf(&b, "%s,%d,%d\r\n", c.Text, c.PauseSec, c.SerialSec)
		}
		name := fileNameSafe(s.Name) + ".csv"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// ImportCommandSetFile reads one "コマンド,リモート秒,シリアル秒" file into a
// command set named after the file (an existing set of the same name is
// replaced). Legacy Show_Command_Pattern .list files import as-is.
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
	var cmds []model.Command
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// The command itself may contain commas: only the last two fields are
		// the pause seconds (both optional).
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
		return "", fmt.Errorf("有効なコマンド行がありません")
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

// writeProfileJSON writes one profile to dir as <名前>.json.
func writeProfileJSON(dir string, p profile.Profile) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	name := fileNameSafe(p.Name) + ".json"
	return os.WriteFile(filepath.Join(dir, name), append(data, '\r', '\n'), 0o644)
}

// ExportOSProfiles writes every OS profile to export\os-profiles\<名前>.json
// (one file per profile) and returns the folder written to. The tool only
// ever writes these files — deleting a profile in the app leaves its file, so
// ファイル読込 can always restore it.
func (a *App) ExportOSProfiles() (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	profiles := make([]profile.Profile, len(a.inv.CustomProfiles))
	copy(profiles, a.inv.CustomProfiles)
	a.mu.Unlock()

	dir := filepath.Join(exeDir(), "export", "os-profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, p := range profiles {
		if err := writeProfileJSON(dir, p); err != nil {
			return "", err
		}
	}
	return dir, nil
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
	var p profile.Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return "", fmt.Errorf("JSONの形式が不正です: %w", err)
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return "", fmt.Errorf("プロファイル名（name）がありません")
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return "", fmt.Errorf("「showコマンド完了の目印」（prompt）がありません")
	}
	if p.Key == "" {
		p.Key = "custom:" + p.Name
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

// ---- CSV import / export ----

// ExportDevicesCSV writes all devices to a chosen CSV file.
func (a *App) ExportDevicesCSV() (string, error) { return a.ExportDevicesCSVGroup("") }

// ExportDevicesCSVGroup writes the devices of one group (or all when group is
// "") to a chosen CSV file. Returns the written path (empty if cancelled).
func (a *App) ExportDevicesCSVGroup(group string) (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	var devices []model.Device
	for _, d := range a.inv.Devices {
		if group == "" || d.Group == group {
			devices = append(devices, d)
		}
	}
	a.mu.Unlock()

	defName := "palaterm_devices.csv"
	if group != "" {
		defName = "palaterm_" + group + ".csv"
	}
	devDir := filepath.Join(exeDir(), "export", "devices")
	_ = os.MkdirAll(devDir, 0o755)
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultDirectory: devDir,
		DefaultFilename:  defName,
		Title:            "機器一覧をCSVに書き出す",
		Filters:          []runtime.FileFilter{{DisplayName: "CSV", Pattern: "*.csv"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	text, err := csvio.Export(devices)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ImportDevicesCSV merges a chosen CSV keeping each row's own group.
func (a *App) ImportDevicesCSV() (int, error) { return a.ImportDevicesCSVToGroup("") }

// ImportDevicesCSVToGroup merges a chosen CSV, assigning every imported device
// to targetGroup when it is non-empty (otherwise the CSV's own group is kept).
// Matches by name: existing devices are updated, new ones added.
func (a *App) ImportDevicesCSVToGroup(targetGroup string) (int, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "機器一覧のCSVを読み込む",
		DefaultDirectory: filepath.Join(exeDir(), "export", "devices"),
		Filters:          []runtime.FileFilter{{DisplayName: "CSV", Pattern: "*.csv"}},
	})
	if err != nil || path == "" {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	devices, err := csvio.Import(string(data))
	if err != nil {
		return 0, err
	}

	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return 0, fmt.Errorf("vault is locked")
	}
	byName := map[string]int{}
	for i := range a.inv.Devices {
		byName[a.inv.Devices[i].Name] = i
	}
	for _, d := range devices {
		if targetGroup != "" {
			d.Group = targetGroup
		}
		a.ensureGroupExists(d.Group)
		if i, ok := byName[d.Name]; ok {
			a.inv.Devices[i] = d
		} else {
			byName[d.Name] = len(a.inv.Devices)
			a.inv.Devices = append(a.inv.Devices, d)
		}
	}
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		return 0, err
	}
	return len(devices), nil
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
