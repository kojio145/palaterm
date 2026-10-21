package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"github.com/kojio145/palaterm/internal/csvio"
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
	runActive bool   // a batch is in progress (blocks a second concurrent batch)
	runSched  string // the 予約 running it ("" = started by hand)

	// Host keys pinned during a batch are saved shortly after, together
	// (persistSoon), not one vault write per device inside the handshake.
	persistMu      sync.Mutex
	persistTimer   *time.Timer
	persistPending bool
	persistRun     sync.Mutex // held while a pending save is written

	// Open terminal windows → their stdin (LockTerminals).
	termsMu sync.Mutex
	terms   map[*exec.Cmd]*termChild
	termSeq int

	lang string // UI language, for the backend's own dialogs

	// Host keys pinned by a batch while the vault was locked, saved at the
	// next unlock.
	pendingPins map[string]string

	lastDone *RunDone // the last batch's results (GetRunStatus)
	quitting bool     // ConfirmQuit: close without asking again

	// 予約: the scheduler's state, the copy of what the enabled schedules
	// need kept while the vault is locked (schedCopy), and the tray menu's
	// 終了 (close for real instead of hiding into the tray).
	sched    schedState
	schedInv *model.Inventory
	trayQuit bool
}

// NewApp constructs the backend with the vault stored next to the executable
// (portable: the whole tool + its encrypted data travel together).
func NewApp() *App {
	reg := profile.NewRegistry()
	a := &App{
		terms:     map[*exec.Cmd]*termChild{},
		profiles:  reg,
		runner:    runpkg.New(reg),
		vaultPath: defaultVaultPath(),
	}
	a.sched.kick = make(chan struct{}, 1)
	a.runner.HostKeys = a // TOFU host key pinning backed by the vault
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// On screen at once (the old fit), then where it was last closed:
	// placeMainWindow runs in the background and may not find the window
	// on a slow start.
	mainWin.fit(ctx)
	placeMainWindow()
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
	// GUI windows killed before they could delete their WebView2 profile
	// (a forced quit, a power cut) left the device's session on disk.
	go cleanStaleGUIProfiles()
	go a.schedLoop()
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
		// Locked while a batch runs: keep the pin for the next unlock.
		if a.pendingPins == nil {
			a.pendingPins = map[string]string{}
		}
		a.pendingPins[addr] = fingerprint
		a.mu.Unlock()
		return
	}
	if a.inv.KnownHostKeys == nil {
		a.inv.KnownHostKeys = map[string]string{}
	}
	a.inv.KnownHostKeys[addr] = fingerprint
	a.mu.Unlock()
	a.persistSoon()
}

// persistSoon saves the vault a moment from now, folding the requests that
// arrive meanwhile into one write. Pinning a host key happens inside the SSH
// handshake: saving the vault there (its key derivation takes a while) under
// a.mu made a first run against many new devices queue every handshake
// behind the previous save until connections timed out, and froze the UI.
func (a *App) persistSoon() {
	a.persistMu.Lock()
	defer a.persistMu.Unlock()
	a.persistPending = true
	if a.persistTimer != nil {
		return
	}
	a.persistTimer = time.AfterFunc(700*time.Millisecond, func() { a.writePending() })
}

// writePending saves if a save is still pending. persistRun is held through
// the write, so flushPersist (before Lock clears the vault from memory, and
// on exit) waits for a save already under way instead of returning while it
// runs — the timer firing just as the vault was locked used to lose the pin.
func (a *App) writePending() {
	a.persistRun.Lock()
	defer a.persistRun.Unlock()
	a.persistMu.Lock()
	pending := a.persistPending
	a.persistPending = false
	if a.persistTimer != nil {
		a.persistTimer.Stop()
		a.persistTimer = nil
	}
	a.persistMu.Unlock()
	if pending {
		_ = a.persist()
	}
}

// flushPersist writes a save persistSoon still has pending, now.
func (a *App) flushPersist() { a.writePending() }

// shutdown runs when the main window closes.
func (a *App) shutdown(ctx context.Context) {
	saveMainPlacement()
	closeGUIWindows()
	a.flushPersist()
	trayHide()
	armWake(time.Time{})
	// Ask the terminal windows to close their sessions first, so each writes
	// its log summary (killed outright, they left an empty or unsummarised
	// run in 実行履歴), and give the GUI windows the moment they need to
	// delete their WebView2 profiles (killed, they leave the device's
	// cookies on disk until the next GUI window cleans up); then end
	// whatever is left.
	if a.openTerminals() > 0 {
		a.sendToTerminals(termCloseLine)
	}
	for i := 0; i < 30 && (a.openTerminals() > 0 || openGUIWindows() > 0); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	endChildren()
}

// ClearHostKeys drops the pinned host keys for one device (its own address
// and its bastions'), so a legitimately replaced device can be re-pinned on
// the next connection.
func (a *App) ClearHostKeys(id string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	idx := a.inv.DeviceIndex(id)
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("機器が見つかりません")
	}
	// The keys are pinned under the chain as connected: the own jump host
	// in front when the device goes through it. The names without it go too
	// (pinned before the device used the own jump host).
	dev := a.inv.Devices[idx]
	resolved := a.inv.ResolveCredentials(dev)
	direct := dev.WithoutBastions() // 踏み台を使わない: pinned under the bare address
	names := append(dev.HostKeyNames(), resolved.HostKeyNames()...)
	for _, addr := range append(names, direct.HostKeyNames()...) {
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

// runHostKeys is a batch's host-key store: checks go to the copy taken at
// the start of the run, new pins to the copy and to the vault (held for the
// next unlock if the vault is locked by then).
type runHostKeys struct {
	mem *memHostKeys
	app *App
}

func (r *runHostKeys) GetHostKey(addr string) string { return r.mem.GetHostKey(addr) }

func (r *runHostKeys) SetHostKey(addr, fp string) {
	r.mem.SetHostKey(addr, fp)
	r.app.SetHostKey(addr, fp)
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
	// The lock screen only offers this when no vault exists; an existing one
	// is replaced through 初期化 (which asks for the master password), never
	// by a call that happens to arrive while locked.
	if vault.Exists(a.vaultPath) {
		return fmt.Errorf("vault already exists")
	}
	// The samples are named in the UI's language: an English UI used to
	// open on 「サンプルグループ」 and 「【サンプル】Cisco_ルータ」.
	lang := a.uiLang()
	inv := &model.Inventory{
		Version:           1,
		Settings:          model.DefaultSettings(),
		CommandSets:       model.LocalizeSampleSets(model.DefaultCommandSets(), lang),
		CommandSetSeedGen: model.CommandSetSeedGen,
		DeviceGroups:      []model.DeviceGroup{{Name: model.SampleGroupName(lang)}},
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
	a.schedInv = nil    // the vault itself again
	a.pendingPins = nil // a new vault starts with no pins from an old one
	a.mu.Unlock()
	return nil
}

// Unlock decrypts the inventory with the master password.
func (a *App) Unlock(password string) error {
	inv, err := vault.Load(a.vaultPath, password)
	if err != nil {
		// Said in the UI's words (the page translates these): the raw
		// "wrong master password or corrupted vault" reached the Japanese
		// lock screen as it was.
		switch {
		case errors.Is(err, vault.ErrWrongPassword):
			return errors.New("パスワードが違うか、ファイルが壊れています")
		case strings.Contains(err.Error(), "not a PalaTerm vault file"):
			return errors.New("PalaTerm の vault ファイルではありません（data\\palaterm_vault.enc に別のファイルが置かれています）")
		}
		return err
	}
	inv.NormalizeGroups()
	lang := a.uiLang()
	seed := false
	// v1.7.3's per-host jump-host logins become the single own jump host.
	if inv.MigrateMyBastion() {
		seed = true
	}
	// Devices from before IDs get one now, saved at once: the terminal window
	// loads the vault on its own and must find the same IDs.
	for _, d := range inv.Devices {
		if d.ID == "" {
			inv.EnsureDeviceIDs()
			seed = true
			break
		}
	}
	// Seed the built-in command sets into vaults created before they existed.
	// A vault still holding only the pre-【サンプル】 sample sets (all names
	// untouched, so nothing user-made) is reseeded under the new names.
	if len(inv.CommandSets) == 0 || onlyLegacyDefaultSets(inv.CommandSets) {
		inv.CommandSets = model.LocalizeSampleSets(model.DefaultCommandSets(), lang)
		inv.CommandSetSeedGen = model.CommandSetSeedGen
		seed = true
	}
	// Sample sets added by a later release (ALAXALA_L2SW in generation 1) are
	// appended to an older vault once, by name: a set the user already has
	// under that name is left alone, and one deleted afterwards stays deleted
	// because the vault remembers the generation.
	if inv.CommandSetSeedGen < model.CommandSetSeedGen {
		// The sample sets' 待つ文字列 (generation 2) go onto the samples a
		// vault already has, where the user left the field empty.
		if inv.CommandSetSeedGen < model.SampleWaitsAddedIn {
			model.FillSampleWaits(inv.CommandSets)
		}
		have := map[string]bool{}
		for _, cs := range inv.CommandSets {
			have[cs.Name] = true
		}
		for _, cs := range model.CommandSetsAddedAfter(inv.CommandSetSeedGen) {
			// A vault whose samples are the English ones gets the new
			// sample in English too (either name present = already there).
			en := model.SampleSetNameEN(cs.Name)
			if !have[cs.Name] && !(en != "" && have[en]) {
				inv.CommandSets = append(inv.CommandSets, model.LocalizeSampleSets([]model.CommandSet{cs}, lang)...)
			}
		}
		inv.CommandSetSeedGen = model.CommandSetSeedGen
		seed = true
	}
	// A vault with no groups and no devices also gets the starter group.
	if len(inv.DeviceGroups) == 0 && len(inv.Devices) == 0 {
		inv.DeviceGroups = []model.DeviceGroup{{Name: model.SampleGroupName(lang)}}
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
		// Default answers to mid-command questions (generation 3) go onto
		// the default profiles that have none yet — an edited one keeps
		// whatever its owner put there.
		if inv.ProfileSeedGen < profile.AnswersAddedIn {
			for i := range inv.CustomProfiles {
				p := &inv.CustomProfiles[i]
				if len(p.Answers) == 0 {
					p.Answers = profile.DefaultAnswers(p.Key)
				}
			}
		}
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
	// NEC QX asks for the user name with "login:", not "Username:", so the
	// built-in row (copied from the Cisco profiles) never matched: over Telnet,
	// serial or a bastion the login stalled until the command timeout. Fix the
	// row where it is still the built-in one; one the user edited is theirs.
	for i := range inv.CustomProfiles {
		p := &inv.CustomProfiles[i]
		if p.Key != "nec-qx" || len(p.Login) == 0 {
			continue
		}
		if first := &p.Login[0]; first.Expect == "sername:" && first.Send == "{user}" {
			first.Expect = "ogin:"
			seed = true
		}
	}
	// QX's "screen-length disable" only exists in user view, but the pager is
	// sent after login, by then in system-view, so it was refused and output
	// kept paging. Move it into the login steps just before system-view, where
	// both the pager and the system-view row are still the built-in ones.
	for i := range inv.CustomProfiles {
		p := &inv.CustomProfiles[i]
		if p.Key != "nec-qx" || len(p.Pager) != 1 || p.Pager[0] != (profile.Step{Send: "screen-length disable"}) {
			continue
		}
		n := len(p.Login)
		if n == 0 || p.Login[n-1] != (profile.Step{Expect: ">", Send: "system-view"}) {
			continue
		}
		login := append([]profile.Step{}, p.Login[:n-1]...)
		p.Login = append(login, profile.Step{Expect: ">", Send: "screen-length disable"}, p.Login[n-1])
		p.Pager = nil
		seed = true
	}
	// Yamaha RTX over Telnet / serial asks for the login password before the
	// ">" the built-in first row waited for, so the login sat until the
	// command timeout. Put the password row first where the login still
	// starts with the built-in administrator row.
	for i := range inv.CustomProfiles {
		p := &inv.CustomProfiles[i]
		if p.Key != "yamaha-rtx" || len(p.Login) == 0 {
			continue
		}
		if p.Login[0] == (profile.Step{Expect: ">", Send: "administrator"}) {
			p.Login = append([]profile.Step{{Expect: "assword:", Send: "{password}"}}, p.Login...)
			seed = true
		}
	}
	// FortiGate / FortiAnalyzer: a VDOM-enabled unit refuses the pager
	// commands outside "config global", and every long show then stopped at
	// --More-- until the command timeout. The prompt is the net under them.
	for i := range inv.CustomProfiles {
		p := &inv.CustomProfiles[i]
		if (p.Key == "fortinet-fortios" || p.Key == "fortinet-analyzer") && p.MorePrompt == "" {
			p.MorePrompt = "--More--"
			seed = true
		}
	}
	// Vaults still on an earlier default log name move to the current one
	// (site, role and stage in the name); a template the user changed is theirs.
	if inv.Settings.LogNameTemplate == "" || logstore.IsOldDefaultTemplate(inv.Settings.LogNameTemplate) {
		inv.Settings.LogNameTemplate = logstore.DefaultTemplate
		seed = true
	}
	// The same for the run-folder name (log_{date}_{time}_{stage} before
	// v1.5.12). Empty already means the current default.
	if logstore.IsOldDefaultDirTemplate(inv.Settings.LogDirTemplate) {
		inv.Settings.LogDirTemplate = logstore.DefaultDirTemplate
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
	a.schedInv = nil // the vault itself again
	// Keys a batch pinned while the vault was locked (see runHostKeys).
	pins := a.pendingPins
	a.pendingPins = nil
	if len(pins) > 0 {
		if inv.KnownHostKeys == nil {
			inv.KnownHostKeys = map[string]string{}
		}
		for addr, fp := range pins {
			inv.KnownHostKeys[addr] = fp
		}
	}
	a.mu.Unlock()
	if len(pins) > 0 {
		a.persistSoon()
	}
	// Read the log folder once in the background, so the 実行履歴 tab opens
	// from the cache (see history.List) instead of reading every run folder.
	go func() { _, _ = a.ListRunHistory() }()
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

// Lock forgets the password and inventory from memory — all but what the
// enabled 予約 need to run while locked (schedCopy).
func (a *App) Lock() {
	// The GUI windows are logged-in sessions: they close with the vault.
	closeGUIWindows()
	a.flushPersist()
	a.mu.Lock()
	if a.inv != nil {
		a.schedInv = schedCopy(a.inv)
	}
	a.password, a.inv = "", nil
	a.mu.Unlock()
}

// locked reports that no vault is open. The bindings the page can reach
// only after unlocking refuse with errVaultLocked while it is: the lock screen
// is the only thing it should get at.
func (a *App) locked() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inv == nil
}

var errVaultLocked = fmt.Errorf("vault is locked")

// Unlocked tells a page that reloaded itself (the language switch) whether
// the vault is still open, so it can go straight back in instead of asking
// for the master password the backend never forgot.
func (a *App) Unlocked() bool { return !a.locked() }

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
	// The save just kept the old file as the backup — still readable with the
	// old password, which may be the reason for the change. Drop it; the
	// next save makes a new one under the new password.
	vault.RemoveBackups(a.vaultPath)
	a.password = next
	// Open terminal windows unlock and reconnect with the master password;
	// they get the new one too (the old one may be why it was changed).
	a.sendToTerminals(termPWLine + " " + base64.StdEncoding.EncodeToString([]byte(next)))
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
	a.schedInv = nil
	a.pendingPins = nil // nothing of the old vault goes into the next one
	a.mu.Unlock()
	if err := os.Remove(a.vaultPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	// "Everything is deleted" must include the backup (the old credentials
	// under the old password) and any half-written temporary file.
	vault.RemoveBackups(a.vaultPath)
	return nil
}

func (a *App) persist() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return fmt.Errorf("vault is locked")
	}
	// Safety net: whatever path added a device, it is saved with an ID.
	a.inv.EnsureDeviceIDs()
	return vault.Save(a.vaultPath, a.password, a.inv)
}

// ---- Inventory reads ----

// GetInventory returns a copy of the decrypted inventory (locked => nil).
// A copy, taken under the lock: Wails turns the result into JSON after this
// returns, and walking the live maps then raced a batch pinning a host key —
// a concurrent map read and write stops the whole process.
func (a *App) GetInventory() *model.Inventory {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return nil
	}
	return a.inv.Clone()
}

// ListProfiles returns the available OS profiles for dropdowns, in the
// user-arranged vault order (OSタイプ設定 rows and dropdowns match).
func (a *App) ListProfiles() []profile.Profile {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		// The registry still holds the vault's profiles for the schedules
		// that run while locked; the page gets nothing until it unlocks.
		return []profile.Profile{}
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
	if err := model.CheckProfileSends(p); err != nil {
		return err
	}
	// Answers go to whatever question matches, mid-command: a credential
	// there would be typed to any prompt that happens to look alike. So the
	// variables are not expanded in them, and refused rather than sent as
	// literal text.
	for _, ans := range p.Answers {
		for _, v := range []string{"{user}", "{password}", "{enable}"} {
			if strings.Contains(ans.Send, v) {
				return fmt.Errorf("確認への自動応答の「送る文字」に %s は使えません（パスワード等を想定外の質問へ送らないため）", v)
			}
		}
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

// ---- Device CRUD (keyed by the internal Device.ID) ----

// errDeviceNameTaken is the refusal for a name already used in the group.
func errDeviceNameTaken(group, name string) error {
	if group == "" {
		return fmt.Errorf("機器名「%s」はグループ未設定の機器に既にあります", name)
	}
	return fmt.Errorf("機器名「%s」はグループ「%s」に既にあります", name, group)
}

// SaveDevice inserts (no ID, or an ID nobody has) or updates (by ID) a
// device after validation. A name only has to be unique within its group;
// the check runs when the name or group changes, so a device left as it was
// always saves.
func (a *App) SaveDevice(d model.Device) error {
	if err := d.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	if err := a.inv.ValidateIn(&d); err != nil {
		a.mu.Unlock()
		return err
	}
	idx := -1
	if d.ID != "" {
		for i := range a.inv.Devices {
			if a.inv.Devices[i].ID == d.ID {
				idx = i
				break
			}
		}
	}
	if idx < 0 || a.inv.Devices[idx].Name != d.Name || a.inv.Devices[idx].Group != d.Group {
		if a.inv.DeviceNameTaken(d.Group, d.Name, d.ID) {
			a.mu.Unlock()
			return errDeviceNameTaken(d.Group, d.Name)
		}
	}
	if idx >= 0 {
		if old := a.inv.Devices[idx]; old.Group == d.Group && old.Name != d.Name {
			renameSchedDevice(a.inv, d.Group, old.Name, d.Name)
		}
		// A GUI pin names the address: a device moved to another one starts
		// over there (trust on first use), the old pin goes.
		if old := a.inv.Devices[idx]; old.GUICertKey() != d.GUICertKey() {
			delete(a.inv.KnownGUICerts, old.GUICertKey())
		}
		a.inv.Devices[idx] = d
	} else {
		if d.ID == "" {
			d.ID = model.NewDeviceID()
		}
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

// DeleteDevice removes one device by ID.
func (a *App) DeleteDevice(id string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	idx := a.inv.DeviceIndex(id)
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("機器が見つかりません")
	}
	delete(a.inv.KnownGUICerts, a.inv.Devices[idx].GUICertKey())
	a.inv.Devices = append(a.inv.Devices[:idx], a.inv.Devices[idx+1:]...)
	a.mu.Unlock()
	return a.persist()
}

// DevicePatch is a bulk edit from the device list: each non-nil field is set
// on every selected device; nil leaves it as it is.
type DevicePatch struct {
	Group         *string `json:"group,omitempty"`
	Site          *string `json:"site,omitempty"`
	Role          *string `json:"role,omitempty"`
	OSType        *string `json:"osType,omitempty"`
	CommandSet    *string `json:"commandSet,omitempty"`
	UseGroupCreds *bool   `json:"useGroupCreds,omitempty"`
	UseMyBastion  *bool   `json:"useMyBastion,omitempty"`
	CLI           *bool   `json:"cli,omitempty"`       // CLI を使う (Device.CLIOff = !CLI)
	GUI           *bool   `json:"gui,omitempty"`       // GUI を使う
	TermColor     *string `json:"termColor,omitempty"` // 端末の色 ("" = none)
	// The devices' own credentials (a password rotated on fifty devices at
	// once), and one jump-host hop's (BastionHop is 1-based; devices
	// without that hop are left as they are).
	Username        *string `json:"username,omitempty"`
	Password        *string `json:"password,omitempty"`
	EnablePassword  *string `json:"enablePassword,omitempty"`
	BastionHop      *int    `json:"bastionHop,omitempty"`
	BastionUsername *string `json:"bastionUsername,omitempty"`
	BastionPassword *string `json:"bastionPassword,omitempty"`
}

// UpdateReport says what a bulk edit touched: how many devices, and — for a
// jump-host credential — how many of them had that hop (the others were
// left alone, which the toast must say: "50 台を変更しました" over a hop
// set on 12 of them hid a rotation that missed 38).
type UpdateReport struct {
	Devices    int `json:"devices"`
	HopApplied int `json:"hopApplied"`
	HopMissing int `json:"hopMissing"`
}

// UpdateDevices applies p to the devices with the given IDs, all or none:
// a move into a group that already has one of the names (or two selected
// devices sharing a name) is refused whole, naming them.
func (a *App) UpdateDevices(ids []string, p DevicePatch) (UpdateReport, error) {
	var rep UpdateReport
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return rep, fmt.Errorf("vault is locked")
	}
	sel := map[string]bool{}
	for _, id := range ids {
		sel[id] = true
	}
	hopEdit := p.BastionHop != nil && (p.BastionUsername != nil || p.BastionPassword != nil)
	next := append([]model.Device(nil), a.inv.Devices...)
	for i := range next {
		d := &next[i]
		if !sel[d.ID] {
			continue
		}
		if p.Group != nil {
			d.Group = strings.TrimSpace(*p.Group)
		}
		if p.Site != nil {
			d.Site = strings.TrimSpace(*p.Site)
		}
		if p.Role != nil {
			d.Role = strings.TrimSpace(*p.Role)
		}
		if p.OSType != nil {
			d.OSType = *p.OSType
		}
		if p.CommandSet != nil {
			d.CommandSet = *p.CommandSet
		}
		if p.UseGroupCreds != nil {
			d.UseGroupCreds = *p.UseGroupCreds && d.Group != ""
		}
		if p.UseMyBastion != nil {
			d.UseMyBastion = *p.UseMyBastion
		}
		if p.CLI != nil {
			d.CLIOff = !*p.CLI
		}
		if p.GUI != nil {
			d.GUIOn = *p.GUI
		}
		if p.TermColor != nil {
			d.TermColor = *p.TermColor // checked and normalised by ValidateIn below
		}
		if p.Username != nil {
			d.Username = strings.TrimSpace(*p.Username)
		}
		if p.Password != nil {
			d.Password = *p.Password
		}
		if p.EnablePassword != nil {
			d.EnablePassword = *p.EnablePassword
		}
		rep.Devices++
		if hopEdit {
			// A fresh slice: the copy in next shares the hops with a.inv
			// until the save goes through (all or none).
			hops := append([]model.Bastion(nil), d.Bastions...)
			if i := *p.BastionHop - 1; i >= 0 && i < len(hops) && hops[i].Host != "" {
				if p.BastionUsername != nil {
					hops[i].Username = strings.TrimSpace(*p.BastionUsername)
				}
				if p.BastionPassword != nil {
					hops[i].Password = *p.BastionPassword
				}
				d.Bastions = hops
				rep.HopApplied++
			} else {
				rep.HopMissing++
			}
		}
		if err := a.inv.ValidateIn(d); err != nil {
			a.mu.Unlock()
			return UpdateReport{}, fmt.Errorf("%s: %w", d.Name, err)
		}
		// A device moved out of every group has no group defaults to use.
		if d.Group == "" {
			d.UseGroupCreds = false
		}
	}
	if p.Group != nil {
		seen := map[string]bool{}
		var clash []string
		for _, d := range next {
			k := d.Group + "\x00" + d.Name
			if seen[k] {
				clash = append(clash, d.Name)
			}
			seen[k] = true
		}
		if len(clash) > 0 {
			a.mu.Unlock()
			return UpdateReport{}, fmt.Errorf("移動先のグループに同じ機器名があるため移動できません: %s", strings.Join(clash, ", "))
		}
	}
	// A schedule naming a device that leaves its group would keep the name
	// and run on whatever gets it next: stop it, and say so in the record.
	var stopped []SchedLogEntry
	if p.Group != nil {
		moved := map[string]map[string]bool{} // old group → names
		for i, d := range a.inv.Devices {
			if sel[d.ID] && next[i].Group != d.Group {
				if moved[d.Group] == nil {
					moved[d.Group] = map[string]bool{}
				}
				moved[d.Group][d.Name] = true
			}
		}
		for g, names := range moved {
			list := make([]string, 0, len(names))
			for n := range names {
				list = append(list, n)
			}
			sort.Strings(list)
			stopped = append(stopped, stopSchedules(a.inv, g, names, fmt.Sprintf("機器「%s」が別のグループへ移動したため停止しました", strings.Join(list, ", ")))...)
		}
	}
	a.inv.Devices = next
	if p.Group != nil {
		a.ensureGroupExists(strings.TrimSpace(*p.Group))
	}
	a.mu.Unlock()
	a.logStopped(stopped)
	if err := a.persist(); err != nil {
		return UpdateReport{}, err
	}
	return rep, nil
}

// DeleteDevices removes the devices with the given IDs; returns how many.
func (a *App) DeleteDevices(ids []string) (int, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return 0, fmt.Errorf("vault is locked")
	}
	sel := map[string]bool{}
	for _, id := range ids {
		sel[id] = true
	}
	out := a.inv.Devices[:0]
	n := 0
	for _, d := range a.inv.Devices {
		if sel[d.ID] {
			n++
			delete(a.inv.KnownGUICerts, d.GUICertKey())
			continue
		}
		out = append(out, d)
	}
	a.inv.Devices = out
	a.mu.Unlock()
	return n, a.persist()
}

// SetAllDevicesEnabled enables or disables every device in one shot (bulk
// check / uncheck). If ids is non-empty, only those devices are affected.
func (a *App) SetAllDevicesEnabled(enabled bool, ids []string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	var only map[string]bool
	if len(ids) > 0 {
		only = make(map[string]bool, len(ids))
		for _, id := range ids {
			only[id] = true
		}
	}
	for i := range a.inv.Devices {
		if only == nil || only[a.inv.Devices[i].ID] {
			a.inv.Devices[i].Enabled = enabled
		}
	}
	a.mu.Unlock()
	return a.persist()
}

// CopyDevice duplicates a device (by ID) in its own group under a name free
// there ("<名前>_copy", then _copy2, …) and returns that name.
func (a *App) CopyDevice(id string) (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	idx := a.inv.DeviceIndex(id)
	if idx < 0 {
		a.mu.Unlock()
		return "", fmt.Errorf("機器が見つかりません")
	}
	dup := a.inv.Devices[idx]
	newName := dup.Name + "_copy"
	for i := 2; a.inv.DeviceNameTaken(dup.Group, newName, ""); i++ {
		newName = fmt.Sprintf("%s_copy%d", dup.Name, i)
	}
	dup.ID = model.NewDeviceID()
	dup.Name = newName
	dup.Bastions = append([]model.Bastion(nil), dup.Bastions...)
	a.inv.Devices = append(a.inv.Devices, dup)
	a.mu.Unlock()
	return newName, a.persist()
}

// RenameDevice changes a device's name in place, e.g. from the inline name
// cell in the device list. The new name must be free within the group.
func (a *App) RenameDevice(id, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("名前は必須です")
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	idx := a.inv.DeviceIndex(id)
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("機器が見つかりません")
	}
	d := &a.inv.Devices[idx]
	if d.Name == newName {
		a.mu.Unlock()
		return nil
	}
	if a.inv.DeviceNameTaken(d.Group, newName, d.ID) {
		a.mu.Unlock()
		return errDeviceNameTaken(d.Group, newName)
	}
	renameSchedDevice(a.inv, d.Group, d.Name, newName)
	d.Name = newName
	a.mu.Unlock()
	return a.persist()
}

// DeviceNameExists reports whether name is already taken in group by a
// device other than exceptID (the UI's check before saving).
func (a *App) DeviceNameExists(name, group, exceptID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return false
	}
	return a.inv.DeviceNameTaken(group, name, exceptID)
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
// one of its devices — under newName. Names only have to be unique within a
// group, so the copies keep their names unless suffix is given (then each
// gets it appended). Returns the device count.
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
	var copies []model.Device
	for _, d := range a.inv.Devices {
		if d.Group != name {
			continue
		}
		c := d
		c.ID = model.NewDeviceID()
		c.Name = d.Name + suffix
		c.Group = newName
		c.Bastions = append([]model.Bastion(nil), d.Bastions...)
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

// DeleteDeviceGroup removes a device group. Its devices are kept but
// ungrouped; one whose name an ungrouped device already has gets "_2" (or
// the next free number), since names must stay unique within a group — the
// ungrouped devices count as one. Returns those renames ("old → new").
func (a *App) DeleteDeviceGroup(name string) ([]string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("vault is locked")
	}
	out := a.inv.DeviceGroups[:0]
	for _, g := range a.inv.DeviceGroups {
		if g.Name != name {
			out = append(out, g)
		}
	}
	a.inv.DeviceGroups = out
	renamed := []string{}
	for i := range a.inv.Devices {
		d := &a.inv.Devices[i]
		if d.Group != name {
			continue
		}
		d.Group = ""
		if a.inv.DeviceNameTaken("", d.Name, d.ID) {
			n := 2
			for a.inv.DeviceNameTaken("", fmt.Sprintf("%s_%d", d.Name, n), d.ID) {
				n++
			}
			nn := fmt.Sprintf("%s_%d", d.Name, n)
			renamed = append(renamed, d.Name+" → "+nn)
			d.Name = nn
		}
	}
	// The group's table draft was read against devices that are now
	// ungrouped (and maybe renamed): it would only come back as conflicts.
	delete(a.inv.GridDrafts, name)
	stopped := stopSchedules(a.inv, name, nil, fmt.Sprintf("グループ「%s」が削除されたため停止しました", name))
	a.mu.Unlock()
	a.logStopped(stopped)
	return renamed, a.persist()
}

// RenameDeviceGroup renames a group and updates its devices' Group field.
func (a *App) RenameDeviceGroup(oldName, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("新しいグループ名を入力してください")
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	// Renaming onto another group would merge the two, and with it any
	// device names they share.
	if newName != oldName && a.inv.Group(newName) != nil {
		a.mu.Unlock()
		return fmt.Errorf("同名のグループ「%s」が既にあります", newName)
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
	renameSchedGroup(a.inv, oldName, newName)
	renameGridDrafts(a.inv, oldName, newName)
	a.mu.Unlock()
	return a.persist()
}

// ---- Command sets ----

// SaveCommandSet inserts or updates a command set by name.
func (a *App) SaveCommandSet(cs model.CommandSet) error {
	if err := model.CheckCommands(cs.Commands); err != nil {
		return err
	}
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

// CreateCommandSet adds a new set and refuses a name already in use: the
// editor's 「＋ セットを追加」 used to save through SaveCommandSet, which
// replaces by name, so a typo of an existing name silently overwrote that
// set's commands on every device that used it.
func (a *App) CreateCommandSet(cs model.CommandSet) error {
	cs.Name = strings.TrimSpace(cs.Name)
	if cs.Name == "" {
		return fmt.Errorf("セット名は必須です")
	}
	if err := model.CheckCommands(cs.Commands); err != nil {
		return err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == cs.Name {
			a.mu.Unlock()
			return fmt.Errorf("「%s」は既にあります", cs.Name)
		}
	}
	a.inv.CommandSets = append(a.inv.CommandSets, cs)
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
	renameSchedSet(a.inv, oldName, newName)
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
// SaveMyBastions sets the user's own jump host chain (自分の踏み台), the
// first hops of every device marked 自分の踏み台を使う. Rows without a host
// are dropped; none left removes it.
func (a *App) SaveMyBastions(chain []model.Bastion) error {
	var out []model.Bastion
	for _, b := range chain {
		b.Host, b.Username = strings.TrimSpace(b.Host), strings.TrimSpace(b.Username)
		b.KeyFile, b.JumpCommand = strings.TrimSpace(b.KeyFile), strings.TrimSpace(b.JumpCommand)
		b.Mine = false
		if b.Method == "" {
			b.Method = model.BastionSSH
		}
		if b.AuthMethod != model.AuthPublicKey {
			b.KeyFile, b.KeyPassphrase = "", ""
		}
		if b.Host != "" {
			out = append(out, b)
		} else if b.Username != "" || b.Password != "" || b.KeyFile != "" || b.JumpCommand != "" {
			return fmt.Errorf("自分の踏み台%d段目のホストが空です（この段を使わないなら「削除」してください）", len(out)+1)
		}
	}
	if err := model.ValidateMyBastions(out); err != nil {
		return err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	// A device reaching its jump hosts by SSH tunnel needs every hop SSH,
	// the own ones included: refused here, not on every connection later.
	for i, b := range out {
		if b.Method == model.BastionSSH {
			continue
		}
		var tun []string
		for _, d := range a.inv.Devices {
			if d.UseMyBastion && !d.CLIOff && d.BastionMode == model.BastionViaTunnel {
				tun = append(tun, d.Name)
			}
		}
		if len(tun) > 0 {
			a.mu.Unlock()
			if len(tun) > 5 {
				tun = append(tun[:5], "…")
			}
			return fmt.Errorf("自分の踏み台%d段目が Telnet ですが、自分の踏み台を SSH トンネルで通る機器があります（%s）。トンネルではすべて SSH にしてください", i+1, strings.Join(tun, ", "))
		}
		break
	}
	a.inv.MyBastions = out
	a.mu.Unlock()
	return a.persist()
}

func (a *App) SaveSettings(s model.Settings) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	// The form never shows LastKeyFile or the 予約 tab's keep-awake; keep
	// them instead of blanking them.
	s.LastKeyFile = a.inv.Settings.LastKeyFile
	s.ScheduleKeepAwake = a.inv.Settings.ScheduleKeepAwake
	// Stage words: trimmed, defaults stored as empty, and checked before
	// they can produce a bad file name or a mislabelled history row.
	s.StageTokenBefore, s.StageTokenWork, s.StageTokenAfter = strings.TrimSpace(s.StageTokenBefore), strings.TrimSpace(s.StageTokenWork), strings.TrimSpace(s.StageTokenAfter)
	s.StageTokenSchedule = strings.TrimSpace(s.StageTokenSchedule)
	s.StageTokenNone = strings.TrimSpace(s.StageTokenNone)
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
	if s.StageTokenSchedule == logstore.DefaultStageTokens.Schedule {
		s.StageTokenSchedule = ""
	}
	if s.StageTokenNone == logstore.DefaultStageTokens.None {
		s.StageTokenNone = ""
	}
	a.inv.Settings = s
	a.mu.Unlock()
	return a.persist()
}

// ---- Execution ----

// RunRequest is what the run tab sends to start a batch.
type RunRequest struct {
	Names []string `json:"names"` // devices to run, by name within the group (checkbox state is left alone)
	// Group is the group shown in the run tab: only its devices run (names
	// are unique within a group, not across groups). Recorded in the run
	// summary. Ungrouped picks the devices without a group instead.
	Group     string `json:"group"`
	Ungrouped bool   `json:"ungrouped,omitempty"`
	Stage     string `json:"stage"`  // "" / before / during / after
	DryRun    bool   `json:"dryRun"` // connect + login only, no commands, no logs
	// CommandSet, when set, is run on every target in place of its own
	// assigned set — for this run only; nothing is saved. AdHoc, commands
	// typed in the run tab, wins over it.
	CommandSet string          `json:"commandSet,omitempty"`
	AdHoc      []model.Command `json:"adHoc,omitempty"`
	// Direct reaches every device without its jump hosts, for this run only
	// (踏み台を使わずに接続: on site, on the devices' own network).
	Direct bool `json:"direct,omitempty"`
}

// AdHocSetName names the commands typed in the run tab for one run (in the
// run summary and the change warning).
const AdHocSetName = "（今回のみ入力したコマンド）"

// RunDone is the "run:done" payload: the per-device results plus the folder
// the logs were written to ("" for a dry run).
type RunDone struct {
	Results []runpkg.DeviceResult `json:"results"`
	RunDir  string                `json:"runDir,omitempty"`
	DryRun  bool                  `json:"dryRun,omitempty"`
}

// RunWith runs with the full set of per-run choices (stage, dry run, group,
// command set for this run).
func (a *App) RunWith(req RunRequest) error {
	return a.run(req)
}

// runSnapshot (caller holds a.mu) is the inventory a batch runs on: a deep
// copy, so editing a command set or device while the batch is going cannot
// change what the waiting devices are sent — a shallow copy shared the
// command-set array with the vault, and deleting a set shifted another into
// the slot a queued device pointed at. It holds only the request's devices,
// with group credentials filled in and any per-run command set applied.
func (a *App) runSnapshot(req RunRequest) (*model.Inventory, error) {
	return snapshotOf(a.inv, req)
}

// snapshotOf is runSnapshot over any inventory: the vault, or — for a 予約
// firing while the vault is locked — the copy kept for the schedules.
func snapshotOf(inv *model.Inventory, req RunRequest) (*model.Inventory, error) {
	if req.Group == "" && !req.Ungrouped {
		return nil, fmt.Errorf("実行するグループを選んでください")
	}
	snap := inv.Clone()
	var want map[string]bool
	if req.Names != nil {
		want = make(map[string]bool, len(req.Names))
		for _, n := range req.Names {
			want[n] = true
		}
	}
	var sel []model.Device
	for _, d := range snap.Devices {
		if d.Group != req.Group {
			continue
		}
		if d.CLIOff {
			continue // GUI only: nothing to log into
		}
		if want != nil {
			if !want[d.Name] {
				continue
			}
			d.Enabled = true
		}
		if req.Direct {
			d = d.WithoutBastions()
		}
		sel = append(sel, snap.ResolveCredentials(d))
	}
	override := req.CommandSet
	if len(req.AdHoc) > 0 {
		var cmds []model.Command
		for _, c := range req.AdHoc {
			if c.Text = strings.TrimSpace(c.Text); c.Text != "" {
				cmds = append(cmds, c)
			}
		}
		if len(cmds) == 0 {
			return nil, fmt.Errorf("今回実行するコマンドを入力してください")
		}
		if err := model.CheckCommands(cmds); err != nil {
			return nil, err
		}
		snap.CommandSets = append(snap.CommandSets, model.CommandSet{Name: AdHocSetName, Commands: cmds})
		override = AdHocSetName
	} else if override != "" {
		found := false
		for _, cs := range snap.CommandSets {
			if cs.Name == override {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("コマンドセット「%s」が見つかりません", override)
		}
	}
	if override != "" {
		for i := range sel {
			sel[i].CommandSet = override
		}
	}
	// A set saved before control characters were refused (or edited in the
	// vault by hand) may still hide "show clock\rreload": checked again here,
	// for every run and every 予約 firing, not only when a set is saved.
	checked := map[string]bool{}
	for _, d := range sel {
		if checked[d.CommandSet] {
			continue
		}
		checked[d.CommandSet] = true
		for _, cs := range snap.CommandSets {
			if cs.Name == d.CommandSet {
				if err := model.CheckCommands(cs.Commands); err != nil {
					return nil, fmt.Errorf("コマンドセット「%s」: %w", cs.Name, err)
				}
			}
		}
	}
	snap.Devices = sel
	return snap, nil
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
	snap, err := a.runSnapshot(req)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	if a.runActive {
		err := a.busyErrLocked()
		a.mu.Unlock()
		return err
	}
	ctx, cancel := a.beginRunLocked("")
	a.mu.Unlock()

	go func() {
		emit := func(ev runpkg.Event) {
			runtime.EventsEmit(a.ctx, "run:event", ev)
		}
		done := a.execBatch(ctx, cancel, snap, req, "", "", emit)
		runtime.EventsEmit(a.ctx, "run:done", done)
	}()
	return nil
}

// busyErrLocked (caller holds a.mu) says why a second batch cannot start.
func (a *App) busyErrLocked() error {
	if a.runSched != "" {
		return fmt.Errorf("予約「%s」の一括実行中です。終わるまでお待ちください（予約タブで中止できます）", a.runSched)
	}
	return fmt.Errorf("一括実行中です。完了または中止を待ってください")
}

// beginRunLocked (caller holds a.mu and has checked !runActive) claims the
// one batch slot. sched names the 予約 running it ("" = started by hand).
func (a *App) beginRunLocked(sched string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(a.ctx)
	a.runCancel = cancel
	a.runActive = true
	a.runSched = sched
	return ctx, cancel
}

// execBatch runs a claimed batch to the end and frees the slot: the part a
// run started by hand and one started by a 予約 share.
//
// dir is a 予約's own folder ("" = a new run folder): its rounds all go
// there, and the folder's summary counts them (history.AddRound).
func (a *App) execBatch(ctx context.Context, cancel context.CancelFunc, snap *model.Inventory, req RunRequest, sched, dir string, emit runpkg.EmitFunc) RunDone {
	defer cancel()
	// No idle sleep until the batch ends (done, 中止 or error).
	defer keepSystemAwake()()
	started := time.Now()
	// The batch checks host keys against a copy taken now, not the vault:
	// the vault can be locked mid-run (ロック), and a locked vault knows no
	// keys — every device would have looked new, a replaced one included.
	a.runner.HostKeys = &runHostKeys{mem: newMemHostKeys(snap.KnownHostKeys), app: a}
	results, runDir := a.runner.RunBatchOpts(ctx, snap, runpkg.Options{Stage: model.Stage(req.Stage), DryRun: req.DryRun, Group: req.Group, Dir: dir, LogStage: schedLogStage(dir, req.Stage)}, emit)
	// Back to the vault before the next batch may start (runActive below):
	// restored later, it could undo the next batch's own copy.
	a.runner.HostKeys = a
	if runDir != "" {
		writeRunSummary(runDir, snap, req, sched, dir != "", started, results)
	}
	done := RunDone{Results: results, RunDir: runDir, DryRun: req.DryRun}
	a.mu.Lock()
	a.runActive = false
	a.runSched = ""
	if sched == "" {
		a.lastDone = &done
	}
	a.mu.Unlock()
	return done
}

// writeRunSummary records the batch in its log folder for the 実行履歴 tab.
// Best effort: a summary that cannot be written costs the history row, not
// the logs. With addRound (a 予約's folder) the round is added to the
// summary already there instead of replacing it.
func writeRunSummary(runDir string, inv *model.Inventory, req RunRequest, sched string, addRound bool, started time.Time, results []runpkg.DeviceResult) {
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
		Schedule:   sched,
		Devices:    []history.DeviceSummary{},
	}
	for _, r := range results {
		d := byName[r.Device]
		ds := history.DeviceSummary{
			Name: r.Device, Host: d.Host, Site: d.Site, CommandSet: d.CommandSet,
			Success: r.Success, Canceled: r.Canceled, Error: r.Error,
			ElapsedSec: r.Elapsed.Seconds(), Warnings: r.Warnings,
		}
		if r.LogPath != "" {
			ds.LogFile = filepath.Base(r.LogPath)
		}
		s.Devices = append(s.Devices, ds)
	}
	if addRound {
		if prev, ok := history.Read(runDir); ok {
			s = history.AddRound(prev, s)
		}
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

// CheckDangerousCommands scans what a run would send — each target's own
// command set, or the set / typed commands chosen for this run — for
// configuration-changing commands, so the run tab can warn before starting.
// Devices sharing a command set are each listed (the user reads it per
// device), but the scan itself runs once per set.
func (a *App) CheckDangerousCommands(req RunRequest) []DangerWarning {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []DangerWarning{}
	if a.inv == nil {
		return out
	}
	snap, err := a.runSnapshot(req)
	if err != nil {
		return out
	}
	return dangerOf(snap)
}

// dangerOf lists, per device of a run snapshot, the commands of its set
// that change the device.
func dangerOf(snap *model.Inventory) []DangerWarning {
	out := []DangerWarning{}
	sets := map[string]*model.CommandSet{}
	for i := range snap.CommandSets {
		sets[snap.CommandSets[i].Name] = &snap.CommandSets[i]
	}
	scanned := map[string][]guard.Hit{}
	for _, d := range snap.Devices {
		if d.CommandSet == "" {
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
	if a.locked() {
		return nil, errVaultLocked
	}
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
	if a.locked() {
		return nil, errVaultLocked
	}
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
	if a.locked() {
		return errVaultLocked
	}
	if err := a.underLogRoot(dir); err != nil {
		return err
	}
	return startExplorer(dir)
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
// RunStatus tells a page reloaded mid-run (the ロック button reloads it)
// whether the batch is still going, and the last batch's results in case it
// finished while nobody was listening.
type RunStatus struct {
	Active bool     `json:"active"`
	Last   *RunDone `json:"last,omitempty"`
}

// GetRunStatus returns the RunStatus (it needs no unlocked vault).
func (a *App) GetRunStatus() RunStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return RunStatus{} // the last results name devices: after unlocking
	}
	// Active is about a batch started by hand: a 予約 runs apart from the run tab.
	return RunStatus{Active: a.runActive && a.runSched == "", Last: a.lastDone}
}

func (a *App) CancelRun() {
	a.mu.Lock()
	c := a.runCancel
	a.mu.Unlock()
	if c != nil {
		c()
	}
}

// CancelDevice stops one device of the running batch (the run table's 中止
// on its row); the other devices go on. False when nothing is running.
func (a *App) CancelDevice(name string) bool {
	a.mu.Lock()
	active := a.runActive
	a.mu.Unlock()
	return active && a.runner.CancelDevice(name)
}

// ---- Convenience ----

// PickKeyFile opens a file dialog for choosing an SSH private key. The chosen
// path is remembered in Settings.LastKeyFile so the device editor can pre-fill
// it next time. Returns "" if the dialog was cancelled.
// PickLogDir opens a folder picker for the log output directory.
// uiLang is the page's language (SetLang), for words the backend itself
// shows: the native file dialogs' titles and filter names.
func (a *App) uiLang() string {
	l, _ := uiLang.Load().(string)
	return l
}

// word picks the UI language's word for a native dialog.
func (a *App) word(ja, en string) string {
	if a.uiLang() == "en" {
		return en
	}
	return ja
}

func (a *App) PickLogDir() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: a.word("ログ保存フォルダを選択", "Choose the log folder"),
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
		Title:            a.word("秘密鍵ファイルを選択", "Choose the private key file"),
		DefaultDirectory: def,
		Filters: []runtime.FileFilter{
			{DisplayName: a.word("秘密鍵 (id_*;*.pem;*.key)", "Private keys (id_*;*.pem;*.key)"), Pattern: "id_*;*.pem;*.key"},
			{DisplayName: a.word("すべてのファイル", "All files"), Pattern: "*.*"},
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
	if a.locked() {
		return errVaultLocked
	}
	a.mu.Lock()
	dir := "logs"
	if a.inv != nil && a.inv.Settings.LogDir != "" {
		dir = a.inv.Settings.LogDir
	}
	a.mu.Unlock()
	abs := logstore.ResolveRoot(dir)
	_ = os.MkdirAll(abs, 0o755)
	return startExplorer(abs)
}

// StrictVerify picks the setting lines typed in the work logs and reports
// whether each is present in the after log (see history.StrictVerify). A
// work done over several sessions (a reconnect) has one log per session:
// each is read on its own, in the order given (oldest first), and the
// settings of all of them are checked together. All files must lie under
// the log folder.
func (a *App) StrictVerify(workPaths []string, beforePath, afterPath string) (*history.StrictResult, error) {
	if a.locked() {
		return nil, errVaultLocked
	}
	read := func(p string) (string, error) {
		if err := a.underLogRoot(p); err != nil {
			return "", err
		}
		b, err := os.ReadFile(p)
		return string(b), err
	}
	var work []string
	for _, p := range workPaths {
		w, err := read(p)
		if err != nil {
			return nil, err
		}
		work = append(work, w)
	}
	bf, err := read(beforePath)
	if err != nil {
		return nil, err
	}
	af, err := read(afterPath)
	if err != nil {
		return nil, err
	}
	r := history.StrictVerifyLogs(work, bf, af)
	return &r, nil
}

// ExportCompareResult writes the run history comparison, as the UI shows
// it (rows, the first one the header), to export\compare\compare_<date>_<time>.csv
// and returns the file.
func (a *App) ExportCompareResult(rows [][]string) (string, error) {
	if a.locked() {
		return "", errVaultLocked
	}
	if len(rows) < 2 {
		return "", fmt.Errorf("書き出す行がありません")
	}
	text, err := csvio.ExportTable(rows)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(exeDir(), "export", "compare")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Two exports within the same second get _2, _3 … rather than one
	// overwriting the other.
	base := "compare_" + time.Now().Format("20060102_150405")
	path := filepath.Join(dir, base+".csv")
	for n := 2; ; n++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, err = f.WriteString(text)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				os.Remove(path) // no half-written CSV left to be taken for a whole one
				return "", err
			}
			return path, nil
		}
		if !os.IsExist(err) || n > 100 {
			return "", err
		}
		path = filepath.Join(dir, fmt.Sprintf("%s_%d.csv", base, n))
	}
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
// Only files under the log folder are opened. The log of a device still
// running is written out first and opens following its growth, the way an
// interactive window's log does.
func (a *App) OpenLogWindow(path string) error {
	if a.locked() {
		return errVaultLocked
	}
	if err := a.underLogRoot(path); err != nil {
		return err
	}
	var env []string
	if a.runner.FlushLive(path) {
		env = []string{"PALATERM_VIEW_LIVE=1"}
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return spawnWindow(env, "--view", path)
}

// OpenWatchWindow shows a batch device's log replayed into a read-only
// terminal (PalaTerm.exe --watch) that follows it as it grows: the device's
// screen, as a Tera Term macro window shows it. Same rule as OpenLogWindow:
// only files under the log folder.
func (a *App) OpenWatchWindow(path string) error {
	if a.locked() {
		return errVaultLocked
	}
	if err := a.underLogRoot(path); err != nil {
		return err
	}
	a.runner.FlushLive(path)
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return spawnWindow(nil, "--watch", path)
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
//
// direct opens it without the device's jump hosts (踏み台を使わずに接続).
func (a *App) SpawnTerminal(id string, manual, direct bool) error {
	_, err := a.spawnTerminal(id, manual, direct, nil)
	return err
}

// spawnTerminal is SpawnTerminal with extra environment for the child
// (並べて接続: its grid cell and 同時入力 on, see broadcast.go).
func (a *App) spawnTerminal(id string, manual, direct bool, extra []string) (*termChild, error) {
	a.mu.Lock()
	locked := a.inv == nil
	pw := a.password
	label, cliOff := "", false
	if !locked {
		if i := a.inv.DeviceIndex(id); i >= 0 {
			label = a.inv.Devices[i].Name
			cliOff = a.inv.Devices[i].CLIOff
		}
	}
	a.mu.Unlock()
	if locked {
		return nil, fmt.Errorf("vault is locked")
	}
	if label == "" {
		return nil, fmt.Errorf("機器が見つかりません")
	}
	if cliOff {
		return nil, fmt.Errorf("この機器は CLI を使わない設定です（機器の編集で「CLI を使う」をオンにしてください）")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{"--connect", id, "--label", label}
	if manual {
		args = append(args, "--manual")
	}
	if direct {
		args = append(args, "--direct")
	}
	cmd := exec.Command(exe, args...)
	// The window's place in the cascade: five sessions opened in a row used
	// to land on exactly the same spot, one hiding the next. A window of
	// 並べて接続 has its grid cell instead.
	env := append([]string{fmt.Sprintf("%s=%d", cascadeEnv, a.openTerminals())}, extra...)
	cmd.Env = childEnv(env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// The child never writes the vault; it reports the one thing it needs
	// persisted — the user allowing a changed host key — on stdout, and the
	// main window applies it. 同時入力 and 並べ直す come up the same way
	// (broadcast.go).
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	tieToMainWindow(cmd.Process.Pid)
	trackChild(cmd.Process)
	// stdin stays open after the password: it is how the main window asks the
	// terminal to lock (LockTerminals).
	// The password goes first, then the window is registered: a LOCK line
	// written in between would be read as the password.
	_, _ = io.WriteString(stdin, pw+"\n")
	a.termsMu.Lock()
	a.termSeq++
	child := newTermChild(stdin, a.termSeq)
	a.terms[cmd] = child
	a.termsMu.Unlock()
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024) // relayed keys (bcastChunk) fit
		for sc.Scan() {
			a.termLine(cmd, sc.Text())
		}
		_ = cmd.Wait()
		a.termsMu.Lock()
		delete(a.terms, cmd)
		a.termsMu.Unlock()
		child.out.close()
		_ = stdin.Close()
		a.sendBcastCounts()
	}()
	return child, nil
}

// termLockLine is what LockTerminals writes to a terminal window's stdin.
const termLockLine = "LOCK"

// LockTerminals locks every open terminal window (the ロック button): each
// covers its screen until the master password is typed there. The sessions
// stay connected. The idle auto-lock does not call this — a terminal in use
// would lock because the main window sat idle; terminals time out on their
// own inactivity instead.
func (a *App) LockTerminals() { a.sendToTerminals(termLockLine) }

// termPWLine prefixes the new master password (base64) sent to the terminal
// windows after ChangeMasterPassword.
const termPWLine = "PW"

// termCloseLine asks a terminal window to end its session and quit (main
// window exit), so it writes its log summary before it is ended.
const termCloseLine = "CLOSE"

// sendToTerminals writes one line to every open terminal window's stdin.
func (a *App) sendToTerminals(line string) {
	a.termsMu.Lock()
	defer a.termsMu.Unlock()
	for _, c := range a.terms {
		c.put(line)
	}
}

// openTerminals counts the terminal windows still running.
func (a *App) openTerminals() int {
	a.termsMu.Lock()
	defer a.termsMu.Unlock()
	return len(a.terms)
}

// SetLang tells the backend the UI language ("ja" / "en") for the few
// messages it shows itself (the close confirmation).
func (a *App) SetLang(lang string) {
	a.mu.Lock()
	a.lang = lang
	uiLang.Store(lang)
	a.mu.Unlock()
}

// QuitRequest is what app:quit-request asks about: the terminal windows that
// close with the main window and whether a batch would be cut short.
type QuitRequest struct {
	Terminals int  `json:"terminals"`
	Running   bool `json:"running"`
}

// beforeClose holds the main window's close while terminal windows are open
// (they close with it, see winjob.go) or a batch runs (it stops with the
// process: × used to end a batch by hand without a word) and asks in the
// page, styled like the rest of the app: ConfirmQuit finishes the close.
func (a *App) beforeClose(ctx context.Context) bool {
	saveMainPlacement()
	a.mu.Lock()
	quitting, trayQuit, running := a.quitting, a.trayQuit, a.runActive
	// The tray's 終了 counts for this close only: cancelled at the
	// open-terminals question, the next × goes to the tray again.
	a.trayQuit = false
	a.mu.Unlock()
	// × while a 予約 is still to fire: into the tray, not closed (the
	// schedules run only while this process does).
	if !quitting && !trayQuit && a.hasPendingSchedules() {
		a.hideToTray()
		return true
	}
	n := a.openTerminals()
	if quitting || (n == 0 && !running) {
		return false
	}
	runtime.EventsEmit(ctx, "app:quit-request", QuitRequest{Terminals: n, Running: running})
	return true
}

// ConfirmQuit is the answer "quit" to app:quit-request.
func (a *App) ConfirmQuit() {
	a.mu.Lock()
	a.quitting = true
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		runtime.Quit(ctx)
	}
}

// termEventClearHostKey is the stdout line a terminal window prints when the
// user accepted a changed host key, followed by the device ID.
const termEventClearHostKey = "PALATERM clear-hostkey"

// ---- command set file export / import (旧 Show_Command_Pattern_*.list 相当) ----

// appVersion is recorded in bundle manifests. The About screen has its own
// copy (APP_VERSION in frontend/dist/app.js) and the exe resource lives in
// build/windows/winres.json — bump all three together.
const appVersion = "1.8.0"

// ImportCommandSetFile reads one "コマンド,リモート秒,シリアル秒" file into a
// command set named after the file (an existing set of the same name is
// replaced). Legacy Show_Command_Pattern .list files import as-is. This is
// the single-file path; a whole bundle goes through PickImportFile.
func (a *App) ImportCommandSetFile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            a.word("コマンドセットのファイルを読み込む", "Import a command set file"),
		DefaultDirectory: filepath.Join(exeDir(), "export", "command-sets"),
		Filters: []runtime.FileFilter{
			{DisplayName: a.word("コマンドセット (*.csv;*.list;*.txt)", "Command sets (*.csv;*.list;*.txt)"), Pattern: "*.csv;*.list;*.txt"},
			{DisplayName: a.word("すべてのファイル", "All files"), Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	set, err := bundle.ParseCommandSet(string(data))
	if err != nil {
		return "", err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	set.Name = name

	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	found := false
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == name {
			a.inv.CommandSets[i].Commands, a.inv.CommandSets[i].PauseOff = set.Commands, set.PauseOff
			found = true
			break
		}
	}
	if !found {
		a.inv.CommandSets = append(a.inv.CommandSets, set)
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
		Title:            a.word("OSタイププロファイルのファイルを読み込む", "Import an OS profile file"),
		DefaultDirectory: filepath.Join(exeDir(), "export", "os-profiles"),
		Filters: []runtime.FileFilter{
			{DisplayName: a.word("OSプロファイル (*.json)", "OS profiles (*.json)"), Pattern: "*.json"},
			{DisplayName: a.word("すべてのファイル", "All files"), Pattern: "*.*"},
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

// BundleExportInfo is what a 一式書出 of a group would write over: the
// folder and, when a bundle is already there, when it was exported.
type BundleExportInfo struct {
	Dir        string `json:"dir"`
	Exists     bool   `json:"exists"`
	ExportedAt string `json:"exportedAt"`
}

// BundleExportTarget tells the page whether export\bundles\<group>\ holds an
// earlier export, so it can ask before devices.csv (perhaps open in Excel,
// half-edited) is overwritten.
func (a *App) BundleExportTarget(group string) BundleExportInfo {
	info := BundleExportInfo{Dir: bundleDir(group)}
	b, err := os.ReadFile(filepath.Join(info.Dir, bundle.ManifestName))
	if err != nil {
		return info
	}
	info.Exists = true
	var m bundle.Manifest
	if json.Unmarshal(b, &m) == nil {
		info.ExportedAt = m.ExportedAt
	}
	return info
}

// ExportBundle writes one group's devices together with the command sets and
// OS profiles they use — or, for group "", every device, set and profile —
// to export\bundles\<group>\ and returns that folder. The folder is the
// hand-over unit: copy it to another PalaTerm and pick its
// palaterm-bundle.json in 読込. devices.csv inside it is the same CSV as
// before, so Excel editing still works.
//
// noBastionCreds leaves the jump hosts' logins out (each person's own key;
// the receiver's 自分の踏み台認証 fills them in).
func (a *App) ExportBundle(group string, noBastionCreds bool) (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	c := bundle.Select(a.inv, group, noBastionCreds)
	c.Lang = a.lang
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
		Title:            a.word("一式の目録（palaterm-bundle.json）または機器CSVを読み込む", "Import a bundle (palaterm-bundle.json) or a device CSV"),
		DefaultDirectory: filepath.Join(exeDir(), "export", "bundles"),
		Filters: []runtime.FileFilter{
			{DisplayName: a.word("一式の目録 / 機器CSV", "Bundle manifest / device CSV"), Pattern: "palaterm-bundle.json;*.csv"},
			{DisplayName: a.word("すべてのファイル", "All files"), Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return nil, err
	}
	return a.previewImport(path, targetGroup, overwriteShared)
}

// PreviewImportFile previews the file PickImportFile chose again with other
// options (the group to put the devices in, overwriting shared sets and
// profiles): the import screen shows the options next to what they do.
func (a *App) PreviewImportFile(path, targetGroup string, overwriteShared bool) (*ImportPreview, error) {
	if path == "" {
		return nil, fmt.Errorf("ファイルが選ばれていません")
	}
	return a.previewImport(path, targetGroup, overwriteShared)
}

func (a *App) previewImport(path, targetGroup string, overwriteShared bool) (*ImportPreview, error) {
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

// RunFailure is one failed row of the run tab as the table shows it.
type RunFailure struct {
	Device  string `json:"device"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ExportRunFailures writes the run tab's failed devices to
// export\run-results\<group>_failed_<date>_<time>.csv (dryRun: connect-failed)
// and returns the file, so the failures can be sorted out from a table in
// Excel. The UI passes what it showed; the device details come from the
// inventory, credentials left out.
func (a *App) ExportRunFailures(group string, dryRun bool, rows []RunFailure) (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	byName := map[string]model.Device{}
	for _, d := range a.inv.Devices {
		if d.Group == group {
			byName[d.Name] = d
		}
	}
	lang := a.lang
	a.mu.Unlock()
	var out []csvio.RunResult
	for _, r := range rows {
		d, ok := byName[r.Device]
		if !ok {
			d = model.Device{Name: r.Device} // deleted since the run: name only
		}
		out = append(out, csvio.RunResult{Device: d, Status: r.Status, Message: r.Message})
	}
	if len(out) == 0 {
		return "", fmt.Errorf("失敗した機器がありません")
	}
	text, err := csvio.ExportRunResults(out, lang)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(exeDir(), "export", "run-results")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	kind := "failed"
	if dryRun {
		kind = "connect-failed"
	}
	name := kind + "_" + time.Now().Format("20060102_150405") + ".csv"
	if group != "" {
		name = bundle.FileNameSafe(group) + "_" + name
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", err
	}
	return path, nil
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
	return startExplorer(abs)
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

// ReorderDevices rewrites the relative order of the listed devices (by ID;
// typically one group's rows) without moving any other device.
func (a *App) ReorderDevices(order []string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	reorderSlots(a.inv.Devices, func(d model.Device) string { return d.ID }, order)
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
