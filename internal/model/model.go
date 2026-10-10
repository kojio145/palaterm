// Package model defines the core data types shared across PalaTerm:
// devices, bastions, command sets, and the encrypted inventory.
package model

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/kojio145/palaterm/internal/logstore"
	"github.com/kojio145/palaterm/internal/profile"
)

// ConnMethod is how PalaTerm reaches a device.
type ConnMethod string

const (
	ConnSSH    ConnMethod = "ssh"
	ConnTelnet ConnMethod = "telnet"
	ConnSerial ConnMethod = "serial"
)

// BastionMethod is how PalaTerm reaches a device through a jump host.
type BastionMethod string

const (
	BastionNone   BastionMethod = ""
	BastionSSH    BastionMethod = "ssh"
	BastionTelnet BastionMethod = "telnet"
)

// AuthMethod selects password vs. public-key authentication (SSH only).
type AuthMethod string

const (
	AuthPassword  AuthMethod = "password"
	AuthPublicKey AuthMethod = "publickey"
)

// MaxBastions is the deepest jump-host chain PalaTerm will traverse.
const MaxBastions = 5

// Bastion describes one jump host in a chain placed in front of a device.
// Method is how this hop is reached from the previous one (or, for the first
// hop, the transport used to connect).
type Bastion struct {
	Host       string        `json:"host"`
	Method     BastionMethod `json:"method"`
	Port       int           `json:"port,omitempty"`
	Username   string        `json:"username"`
	Password   string        `json:"password,omitempty"`
	AuthMethod AuthMethod    `json:"authMethod,omitempty"`
	KeyFile    string        `json:"keyFile,omitempty"`
	// KeyPassphrase decrypts a passphrase-protected private key. Persisted
	// only inside the encrypted vault, like passwords.
	KeyPassphrase string `json:"keyPassphrase,omitempty"`

	// LegacyAlgos offers this hop the old SSH KEX/ciphers, independently of the
	// device's own setting: crypto policy belongs to the endpoint you are
	// talking to, and an ancient jump host must not force the device behind it
	// onto weak algorithms (nor the reverse).
	LegacyAlgos bool `json:"legacyAlgos,omitempty"`

	// JumpCommand overrides the command typed ON this hop's shell to reach the
	// next hop (or the device). Placeholders: {user} {host} {port}. Empty means
	// the standard "ssh user@host" / "telnet host" for the next hop's method.
	// Needed when this hop is not a Unix-like shell (e.g. Cisco IOS wants
	// "ssh -l {user} {host}").
	JumpCommand string `json:"jumpCommand,omitempty"`
}

// Configured reports whether a bastion has a usable host + method.
func (b *Bastion) Configured() bool {
	return b != nil && b.Host != "" && b.Method != BastionNone
}

// Device is a single network device in the inventory.
type Device struct {
	// Identity
	Name string `json:"name"` // hostname / label, used in log filenames
	Host string `json:"host"` // IP address or DNS name

	// Transport
	Conn       ConnMethod `json:"conn"`
	Port       int        `json:"port,omitempty"`       // ssh/telnet; 0 => default (22/23)
	SerialPort string     `json:"serialPort,omitempty"` // "COM3"; empty => auto-detect
	Baud       int        `json:"baud,omitempty"`       // serial; 0 => 9600

	// Group is the device's owning group (e.g. a company). A device belongs to
	// at most one group; empty means ungrouped.
	Group string `json:"group,omitempty"`

	// Site is an optional free-text location/site label (e.g. 本社, 東京DC).
	Site string `json:"site,omitempty"`

	// Role is an optional free-text role label (e.g. コア, エッジ, FW); like
	// Site it is only a label, usable in log names via {role}.
	Role string `json:"role,omitempty"`

	// OS behavior
	OSType     string `json:"osType"`     // profile key, e.g. "cisco-ios"
	CommandSet string `json:"commandSet"` // named command set to run

	// Credentials (persisted only inside the encrypted inventory)
	AuthMethod     AuthMethod `json:"authMethod,omitempty"`    // ssh: password|publickey
	KeyFile        string     `json:"keyFile,omitempty"`       // ssh private key path
	KeyPassphrase  string     `json:"keyPassphrase,omitempty"` // decrypts a protected key
	Username       string     `json:"username,omitempty"`
	Password       string     `json:"password,omitempty"`
	EnablePassword string     `json:"enablePassword,omitempty"`

	// LegacyAlgos additionally offers old SSH KEX/ciphers (SHA-1 DH groups,
	// CBC, 3DES) for gear that supports nothing newer. Off by default.
	LegacyAlgos bool `json:"legacyAlgos,omitempty"`

	// Optional jump-host chain (up to MaxBastions hops, outermost first).
	Bastions []Bastion `json:"bastions,omitempty"`

	// Bastion is the legacy single jump-host field, kept only so older vaults
	// load; NormalizeBastions folds it into Bastions.
	Bastion *Bastion `json:"bastion,omitempty"`

	// UseGroupCreds makes the device log in with its group's default
	// credentials (DeviceGroup.Username / Password / EnablePassword) instead of
	// its own three fields above, so one shared account is typed once per
	// group. A device in the same group that needs different credentials
	// simply leaves this off and keeps its own. Resolved at run time by
	// Inventory.ResolveCredentials; the device's own fields are kept as typed.
	UseGroupCreds bool `json:"useGroupCreds,omitempty"`

	// Whether this device is included in batch runs.
	Enabled bool `json:"enabled"`
}

// ActiveBastions returns the configured jump hops in order, capped at
// MaxBastions, after folding any legacy single-bastion field in.
func (d *Device) ActiveBastions() []Bastion {
	d.NormalizeBastions()
	out := make([]Bastion, 0, len(d.Bastions))
	for _, b := range d.Bastions {
		if b.Configured() {
			out = append(out, b)
		}
		if len(out) >= MaxBastions {
			break
		}
	}
	return out
}

// NormalizeBastions migrates the deprecated single Bastion field into the
// Bastions slice, so old vaults keep working.
func (d *Device) NormalizeBastions() {
	if d.Bastion != nil && d.Bastion.Configured() && len(d.Bastions) == 0 {
		d.Bastions = []Bastion{*d.Bastion}
	}
	d.Bastion = nil
}

// EffectivePort returns the TCP port, applying protocol defaults.
func (d *Device) EffectivePort() int {
	if d.Port > 0 {
		return d.Port
	}
	switch d.Conn {
	case ConnSSH:
		return 22
	case ConnTelnet:
		return 23
	}
	return 0
}

// EffectiveBaud returns the serial baud rate, defaulting to 9600.
func (d *Device) EffectiveBaud() int {
	if d.Baud > 0 {
		return d.Baud
	}
	return 9600
}

// Command is one line executed on a device, with a post-send pause.
type Command struct {
	Text      string `json:"text"`
	PauseSec  int    `json:"pauseSec"`  // wait after this command (remote)
	SerialSec int    `json:"serialSec"` // wait after this command (serial console)
}

// CommandSet is a named, ordered list of commands (e.g. "config-backup").
type CommandSet struct {
	Name     string    `json:"name"`
	Commands []Command `json:"commands"`
}

// DeviceGroup is a named group that devices belong to (e.g. a company or site).
// Membership is driven by Device.Group; Members is legacy and kept only so old
// vaults migrate cleanly.
type DeviceGroup struct {
	Name    string   `json:"name"`
	Members []string `json:"members,omitempty"`

	// Default credentials for the group's devices (persisted only inside the
	// encrypted vault, like a device's own). A device opts in with
	// Device.UseGroupCreds; the rest keep their individual credentials.
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	EnablePassword string `json:"enablePassword,omitempty"`
}

// HasCreds reports whether the group carries any default credential.
func (g *DeviceGroup) HasCreds() bool {
	return g != nil && (g.Username != "" || g.Password != "" || g.EnablePassword != "")
}

// Group returns the named group, or nil.
func (inv *Inventory) Group(name string) *DeviceGroup {
	if name == "" {
		return nil
	}
	for i := range inv.DeviceGroups {
		if inv.DeviceGroups[i].Name == name {
			return &inv.DeviceGroups[i]
		}
	}
	return nil
}

// ResolveCredentials returns a copy of d with the group's default
// credentials substituted when the device asks for them. A device whose
// group no longer exists keeps its own fields, so a deleted group never
// silently empties a login.
func (inv *Inventory) ResolveCredentials(d Device) Device {
	if !d.UseGroupCreds {
		return d
	}
	g := inv.Group(d.Group)
	if g == nil {
		return d
	}
	d.Username, d.Password, d.EnablePassword = g.Username, g.Password, g.EnablePassword
	return d
}

// Stage is when in a maintenance window a batch is run: before the work,
// during it, or after it. It travels into the run folder and log file names so
// the before/after logs of one job can be told apart and compared.
type Stage string

const (
	StageNone   Stage = ""
	StageBefore Stage = "before"
	StageDuring Stage = "during"
	StageAfter  Stage = "after"
)

// Valid reports whether s is one of the known stages (empty included).
func (s Stage) Valid() bool {
	switch s {
	case StageNone, StageBefore, StageDuring, StageAfter:
		return true
	}
	return false
}

// NormalizeGroups migrates the legacy Members-based grouping to the Device.Group
// field and makes sure every group a device references exists in DeviceGroups.
func (inv *Inventory) NormalizeGroups() {
	// Legacy migration: a member device with no Group inherits the group name.
	byName := map[string]int{}
	for i := range inv.Devices {
		byName[inv.Devices[i].Name] = i
	}
	for _, g := range inv.DeviceGroups {
		for _, m := range g.Members {
			if i, ok := byName[m]; ok && inv.Devices[i].Group == "" {
				inv.Devices[i].Group = g.Name
			}
		}
	}
	// Members are no longer the source of truth.
	for i := range inv.DeviceGroups {
		inv.DeviceGroups[i].Members = nil
	}
	// Ensure a group entry exists for every group a device names.
	have := map[string]bool{}
	for _, g := range inv.DeviceGroups {
		have[g.Name] = true
	}
	for i := range inv.Devices {
		g := inv.Devices[i].Group
		if g != "" && !have[g] {
			inv.DeviceGroups = append(inv.DeviceGroups, DeviceGroup{Name: g})
			have[g] = true
		}
	}
}

// Inventory is the full persisted state, stored encrypted on disk.
type Inventory struct {
	Version      int           `json:"version"`
	Devices      []Device      `json:"devices"`
	CommandSets  []CommandSet  `json:"commandSets"`
	DeviceGroups []DeviceGroup `json:"deviceGroups,omitempty"`
	Settings     Settings      `json:"settings"`

	// CustomProfiles holds every OS login profile — the default 15 seeded at
	// vault creation plus user-made ones — all equally editable. The JSON key
	// keeps its historic name for vault compatibility.
	CustomProfiles []profile.Profile `json:"customProfiles,omitempty"`

	// ProfilesSeeded records that the default profiles were copied into this
	// vault, so a vault from before profiles-as-data gets them exactly once
	// (and a deliberately deleted default stays deleted).
	ProfilesSeeded bool `json:"profilesSeeded,omitempty"`

	// ProfileSeedGen is the newest built-in profile generation this vault has
	// been offered (profile.SeedGen). Defaults added in a later release are
	// seeded once when the vault is unlocked by that release; a default the
	// user deletes afterwards stays deleted. Zero = the original set.
	ProfileSeedGen int `json:"profileSeedGen,omitempty"`

	// KnownHostKeys pins SSH host key fingerprints per "host:port"
	// (trust-on-first-use); a later mismatch aborts the connection. Stored
	// inside the encrypted vault like everything else.
	KnownHostKeys map[string]string `json:"knownHostKeys,omitempty"`
}

// Settings holds run-wide options.
type Settings struct {
	MaxParallel     int    `json:"maxParallel"`     // 0 => unlimited
	LogDir          string `json:"logDir"`          // output root; empty => ./logs
	LogNameTemplate string `json:"logNameTemplate"` // e.g. logstore.DefaultTemplate
	// LogDirTemplate names each run's folder under LogDir (see
	// logstore.RunDir); empty means logstore.DefaultDirTemplate.
	LogDirTemplate string `json:"logDirTemplate,omitempty"`
	// StageTokenBefore / Work / After are the words {stage} is written as in
	// folder and file names for 作業前 / 作業中 / 作業後 (see
	// logstore.StageTokens); empty means the default before / work / after.
	StageTokenBefore string `json:"stageTokenBefore,omitempty"`
	StageTokenWork   string `json:"stageTokenWork,omitempty"`
	StageTokenAfter  string `json:"stageTokenAfter,omitempty"`
	ConnectTimeout   int    `json:"connectTimeout"` // seconds
	CommandTimeout   int    `json:"commandTimeout"` // seconds per expect

	// LastKeyFile is the most recently chosen SSH private-key path; the device
	// editor pre-fills it when public-key auth is selected.
	LastKeyFile string `json:"lastKeyFile,omitempty"`

	// Idle auto-lock: the main window locks the vault after AutoLockMin minutes
	// without keyboard or mouse input (never during a batch). Zero means the
	// default (DefaultAutoLockMin) so vaults from before the setting existed
	// get it; AutoLockOff disables it outright.
	AutoLockMin int  `json:"autoLockMin,omitempty"`
	AutoLockOff bool `json:"autoLockOff,omitempty"`

	// MaskLogSecrets replaces the device's passwords (and its bastions')
	// with **** in saved logs. Off by default since v1.5.3: a password that
	// also appears in a running-config (a username, a community string) was
	// masked there too and the log no longer matched the device (user
	// request 2026-10-10). Devices do not echo a typed password, so the
	// login itself leaves nothing in the log either way.
	MaskLogSecrets bool `json:"maskLogSecrets,omitempty"`
}

// StageTokens returns the stage words for log names, defaults filled in.
func (s Settings) StageTokens() logstore.StageTokens {
	return logstore.StageTokens{Before: s.StageTokenBefore, Work: s.StageTokenWork, After: s.StageTokenAfter}.Filled()
}

// DefaultAutoLockMin is the idle time before the vault locks itself when the
// user has not chosen one. Long on purpose: a lock that fires during a short
// break on a maintenance night is an irritation, not a safeguard.
const DefaultAutoLockMin = 30

// EffectiveAutoLockMin returns the idle minutes before auto-lock, or 0 when
// auto-lock is off.
func (s Settings) EffectiveAutoLockMin() int {
	if s.AutoLockOff {
		return 0
	}
	if s.AutoLockMin <= 0 {
		return DefaultAutoLockMin
	}
	return s.AutoLockMin
}

// Validate checks a device's fields and returns a human-readable (Japanese)
// error describing the first problem, or nil if it is acceptable to save.
func (d *Device) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("名前は必須です")
	}
	switch d.Conn {
	case ConnSSH, ConnTelnet:
		if strings.TrimSpace(d.Host) == "" {
			return errors.New("IPアドレスは必須です")
		}
		if !validHostOrIP(d.Host) {
			return fmt.Errorf("IPアドレスの形式が不正です: %q", d.Host)
		}
		if d.Port < 0 || d.Port > 65535 {
			return fmt.Errorf("ポート番号が範囲外です: %d（0〜65535）", d.Port)
		}
	case ConnSerial:
		if d.Baud < 0 {
			return errors.New("ボーレートが不正です")
		}
	default:
		return fmt.Errorf("接続方式が不正です: %q", d.Conn)
	}
	for i, b := range d.ActiveBastions() {
		if !validHostOrIP(b.Host) {
			return fmt.Errorf("踏み台%d段目のホスト形式が不正です: %q", i+1, b.Host)
		}
		if b.Method != BastionSSH && b.Method != BastionTelnet {
			return fmt.Errorf("踏み台%d段目の接続方式が不正です", i+1)
		}
		if b.Port < 0 || b.Port > 65535 {
			return fmt.Errorf("踏み台%d段目のポート番号が範囲外です: %d（0〜65535）", i+1, b.Port)
		}
	}
	return nil
}

// validHostOrIP accepts a valid IPv4/IPv6 literal or a plausible hostname.
// Dotted-decimal strings are held to strict IPv4 rules, so typos like
// "192.168.1111.1" are rejected instead of being treated as hostnames.
func validHostOrIP(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	// Looks like an attempted IPv4 (all dot-separated parts numeric) but failed
	// ParseIP → it's a malformed address, not a hostname.
	if looksLikeDottedNumeric(s) {
		return false
	}
	return validHostname(s)
}

func looksLikeDottedNumeric(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func validHostname(s string) bool {
	if len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for i, r := range label {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_'
			if !ok {
				return false
			}
			if r == '-' && (i == 0 || i == len(label)-1) {
				return false
			}
		}
	}
	return true
}

// DefaultSettings returns sensible defaults matching the legacy tool.
func DefaultSettings() Settings {
	return Settings{
		MaxParallel:     10,
		LogDir:          "logs",
		LogNameTemplate: logstore.DefaultTemplate,
		LogDirTemplate:  logstore.DefaultDirTemplate,
		ConnectTimeout:  20,
		CommandTimeout:  30,
		AutoLockMin:     DefaultAutoLockMin,
	}
}

// DefaultCommandSets returns the built-in command sets seeded into a new
// vault. They mirror the show-command patterns of the legacy Tera Term tool
// (Show_Command_Pattern_1..7.list); pause values keep the original
// remote/console split (PauseSec = SSH/Telnet, SerialSec = console).
func DefaultCommandSets() []CommandSet {
	c := func(text string, pause, serial int) Command {
		return Command{Text: text, PauseSec: pause, SerialSec: serial}
	}
	return []CommandSet{
		{Name: "【サンプル】Cisco_ルータ", Commands: []Command{
			c("show clock", 1, 1),
			c("show running-config", 10, 10),
			c("show startup-config", 10, 10),
			c("show version", 1, 1),
			c("show inventory", 1, 1),
			c("show ip ssh", 1, 1),
			c("show ip interface brief", 1, 1),
			c("show ip arp", 1, 1),
			c("show mac address-table", 1, 1),
			c("show vlan-switch", 1, 1),
			c("show cdp neighbors", 1, 1),
			c("show interfaces status", 1, 1),
			c("show interfaces", 5, 1),
			c("show ip route", 1, 1),
			c("show ip dhcp binding", 1, 1),
			c("show ntp associations", 1, 1),
			c("show memory statistics", 1, 1),
			c("show flash:", 1, 1),
			c("show processes cpu history", 1, 1),
			c("show env all", 1, 1),
			c("show logging", 15, 15),
		}},
		{Name: "【サンプル】Cisco_L2SW", Commands: []Command{
			c("show clock", 1, 1),
			c("show running-config", 10, 10),
			c("show startup-config", 10, 10),
			c("show version", 1, 1),
			c("show inventory", 1, 1),
			c("show ip ssh", 1, 1),
			c("show ip interface brief", 1, 1),
			c("show ip arp", 1, 1),
			c("show mac address-table", 1, 1),
			c("show vlan", 1, 1),
			c("show interfaces trunk", 1, 1),
			c("show etherchannel summary", 1, 1),
			c("show spanning-tree", 1, 1),
			c("show interfaces status", 5, 1),
			c("show interfaces", 1, 1),
			c("show cdp neighbors", 1, 1),
			c("show lldp neighbors", 1, 1),
			c("show power inline", 1, 1),
			c("show ntp associations", 1, 1),
			c("show memory statistics", 10, 1),
			c("show flash:", 1, 1),
			c("show processes cpu history", 1, 1),
			c("show env all", 1, 1),
			c("show logging", 30, 30),
		}},
		{Name: "【サンプル】Cisco_L3SW", Commands: []Command{
			c("show clock", 1, 1),
			c("show running-config", 10, 10),
			c("show startup-config", 10, 10),
			c("show version", 1, 1),
			c("show inventory", 1, 1),
			c("show ip ssh", 1, 1),
			c("show ip interface brief", 1, 1),
			c("show ip arp", 1, 1),
			c("show mac address-table", 1, 1),
			c("show vlan", 1, 1),
			c("show interfaces trunk", 1, 1),
			c("show etherchannel summary", 1, 1),
			c("show spanning-tree", 1, 1),
			c("show interfaces status", 5, 1),
			c("show interfaces", 1, 1),
			c("show cdp neighbors", 1, 1),
			c("show ip route", 1, 1),
			c("show ntp associations", 1, 1),
			c("show flash:", 1, 1),
			c("show processes cpu history", 1, 1),
			c("show env all", 1, 1),
			c("show logging", 30, 30),
		}},
		{Name: "【サンプル】FortiGate", Commands: []Command{
			c("execute time", 1, 1),
			c("show", 30, 30),
			c("show system interface", 1, 1),
			c("show system interface | grep -i edit", 1, 1),
			c("show system zone", 1, 1),
			c("show system zone | grep -i edit", 1, 1),
			c("show vpn ipsec phase1-interface", 1, 1),
			c("show vpn ipsec phase1-interface | grep -i edit", 1, 1),
			c("show vpn ipsec phase2-interface", 1, 1),
			c("show vpn ipsec phase2-interface | grep -i edit", 1, 1),
			c("show router static", 1, 1),
			c("show router static | grep -i edit", 1, 1),
			c("show router bgp", 1, 1),
			c("show router bgp | grep -i edit", 1, 1),
			c("show firewall address", 1, 1),
			c("show firewall address | grep -i edit", 1, 1),
			c("show firewall addrgrp", 1, 1),
			c("show firewall addrgrp | grep -i edit", 1, 1),
			c("show firewall policy", 1, 1),
			c("show firewall policy | grep -i edit", 1, 1),
			c("show firewall sniffer", 1, 1),
			c("show firewall sniffer | grep -i edit", 1, 1),
			c("show system link-monitor", 1, 1),
			c("show system link-monitor | grep -i edit", 1, 1),
			c("get system status", 1, 1),
			c("get system interface", 1, 1),
			c("get sys session-info statistics", 1, 1),
			c("get system performance status", 1, 1),
			c("get hardware memory", 1, 1),
			c("get router info routing-table all", 1, 1),
			c("get router info bgp summary", 1, 1),
			c("diagnose sys link-monitor status all", 1, 1),
			c("get vpn ike gateway", 1, 1),
			c("get vpn ipsec tunnel summary", 1, 1),
			c("get vpn ipsec tunnel details", 1, 1),
			c("get system ntp", 1, 1),
			c("diagnose sys ntp status", 1, 1),
			c("get system arp", 1, 1),
			c("diagnose ip arp list", 1, 1),
		}},
		// Verified against a real NEC IX series router. Nearly every show command
		// here needs configure mode — operation mode answers "% ... Invalid
		// command." even for "show running-config" — which the NEC IX profile's
		// pager step enters (see internal/profile/builtin.go). The one genuine
		// difference from Cisco is ARP: plain "show arp" returns "% Expects a
		// subcommand", and the device's own show tech-support spells it
		// "show arp neighbors".
		{Name: "【サンプル】IXルータ", Commands: []Command{
			c("show running-config", 10, 10),
			c("show startup-config", 10, 10),
			c("show version", 1, 1),
			c("show interfaces", 5, 1),
			c("show ip route", 1, 1),
			c("show arp neighbors", 1, 1),
			c("show ntp", 1, 1),
			c("show memory", 1, 1),
			c("show processes", 1, 1),
			c("show logging", 15, 15),
			c("show tech-support", 10, 10),
		}},
	}
}
