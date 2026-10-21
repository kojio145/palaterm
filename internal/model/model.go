// Package model defines the core data types shared across PalaTerm:
// devices, bastions, command sets, and the encrypted inventory.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/kojio145/palaterm/internal/logstore"
	"github.com/kojio145/palaterm/internal/profile"
	"github.com/kojio145/palaterm/internal/schedule"
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

// BastionMode is how a jump-host chain is passed through.
type BastionMode string

const (
	// BastionViaShell types "ssh user@host" / "telnet host" on each hop's
	// shell: works through any hop with a shell (routers included), but the
	// hops after the first and the device are logged into by the hop's own
	// client, so this PC's keys, algorithm settings and host-key pins do not
	// reach them.
	BastionViaShell BastionMode = ""
	// BastionViaTunnel opens each next hop, and the device, as a TCP channel
	// through the previous SSH hop (OpenSSH's ProxyJump): every hop is an SSH
	// login made from this PC, with its key, its algorithms and its host-key
	// check. Needs SSH hops that allow TCP forwarding.
	BastionViaTunnel BastionMode = "tunnel"
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

	// Mine marks the user's own jump host (自分の踏み台) put in front of a
	// device's hops by Inventory.ResolveCredentials. It only exists on those
	// resolved copies, never on a saved device.
	Mine bool `json:"mine,omitempty"`
}

// Configured reports whether a bastion has a usable host + method. The
// user's own jump host counts even without a host (none registered): the
// dial then says so instead of the device being reached directly.
func (b *Bastion) Configured() bool {
	return b != nil && (b.Mine || (b.Host != "" && b.Method != BastionNone))
}

// Device is a single network device in the inventory.
type Device struct {
	// Identity. ID is the internal key every edit, delete, reorder and
	// terminal launch goes by; it is never shown. Name only has to be unique
	// within its group (it names the log files, and a run is one group), so
	// two companies can both have a "core-sw1".
	ID   string `json:"id,omitempty"`
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

	// BastionMode is how Bastions is passed through (empty: typed on each
	// hop's shell).
	BastionMode BastionMode `json:"bastionMode,omitempty"`

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

	// CLIOff: the device has no CLI to log into (GUI only) — no terminal, no
	// batch. Off (CLI used) by default.
	CLIOff bool `json:"cliOff,omitempty"`
	// GUIOn: the device's web GUI can be opened (GUI を開く). Off by default.
	GUIOn bool `json:"guiOn,omitempty"`
	// GUIScheme is "https" (default, also when empty) or "http".
	GUIScheme string `json:"guiScheme,omitempty"`
	// GUIPort is the GUI's port; 0 means the scheme's (443 / 80).
	GUIPort int `json:"guiPort,omitempty"`

	// UseMyBastion reaches the device through the user's own jump host
	// (自分の踏み台, Inventory.MyBastion) as its first hop, before the hops
	// above: a company jump host everyone enters with their own key, which a
	// shared device list then carries as this one flag.
	UseMyBastion bool `json:"useMyBastion,omitempty"`

	// TermColor tints the device's terminal window (端末の色): one of
	// TermColors' keys, or empty for the window's usual colours. A
	// production device in red stands out among windows side by side.
	TermColor string `json:"termColor,omitempty"`

	// Whether this device is included in batch runs.
	Enabled bool `json:"enabled"`
}

// TermColors are the device tints (Device.TermColor), with their Japanese
// names; the page draws them (colors.js DEVICE_TINTS has the same keys).
var TermColors = []struct{ Key, Ja string }{
	{"red", "赤"}, {"orange", "橙"}, {"yellow", "黄"}, {"green", "緑"}, {"blue", "青"}, {"purple", "紫"},
}

// ParseTermColor reads a tint by key or Japanese name ("" for none); ok is
// false for anything else.
func ParseTermColor(v string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "なし", "none":
		return "", true
	}
	for _, c := range TermColors {
		if v == c.Key || v == c.Ja {
			return c.Key, true
		}
	}
	return "", false
}

// TermColorJa is a tint's Japanese name ("" for none or unknown).
func TermColorJa(key string) string {
	for _, c := range TermColors {
		if c.Key == key {
			return c.Ja
		}
	}
	return ""
}

// UsesBastion reports whether the device goes through any jump host: its own
// or the user's.
func (d *Device) UsesBastion() bool {
	return d.UseMyBastion || len(d.ActiveBastions()) > 0
}

// EffectiveGUIScheme is "http" or "https" (the default).
func (d *Device) EffectiveGUIScheme() string {
	if d.GUIScheme == "http" {
		return "http"
	}
	return "https"
}

// EffectiveGUIPort is the web GUI's port: the one set, or the scheme's.
func (d *Device) EffectiveGUIPort() int {
	if d.GUIPort > 0 {
		return d.GUIPort
	}
	if d.EffectiveGUIScheme() == "http" {
		return 80
	}
	return 443
}

// GUIURL is the address the GUI window opens for the device, without the
// port when it is the scheme's default.
func (d *Device) GUIURL() string {
	scheme, port, host := d.EffectiveGUIScheme(), d.EffectiveGUIPort(), d.Host
	if (scheme == "https" && port == 443) || (scheme == "http" && port == 80) {
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		return scheme + "://" + host + "/"
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"
}

// ActiveBastions returns the configured jump hops in order, after folding
// any legacy single-bastion field in. A saved device holds at most
// MaxBastions; a copy resolved for a connection may have the user's own
// chain (up to MaxBastions more) in front.
func (d *Device) ActiveBastions() []Bastion {
	d.NormalizeBastions()
	out := make([]Bastion, 0, len(d.Bastions))
	for _, b := range d.Bastions {
		if b.Configured() {
			out = append(out, b)
		}
		if len(out) >= 2*MaxBastions {
			break
		}
	}
	return out
}

// Tunneled reports that the device is reached through its jump hosts by SSH
// tunnel (BastionViaTunnel) rather than by commands typed on them.
func (d *Device) Tunneled() bool {
	return d.BastionMode == BastionViaTunnel && d.UsesBastion()
}

// HopAddr is the "host:port" an SSH endpoint is dialed and pinned under.
func HopAddr(host string, port int) string {
	return net.JoinHostPort(host, fmt.Sprintf("%d", port))
}

// TunnelName is the name the host key of an endpoint reached through a
// tunnel is pinned under: the chain of addresses that leads to it ("jump:22>
// 10.0.0.5:22"). Behind a jump host the address alone is someone's private
// network — two customers' 10.0.0.1 are different machines with different
// keys, and pinning both under "10.0.0.1:22" would call one of them an attack.
func TunnelName(via, addr string) string {
	if via == "" {
		return addr
	}
	return via + ">" + addr
}

// HostKeyNames lists every name the device's SSH host keys (its own and its
// jump hosts') are pinned under, so they can be dropped together when a
// device is legitimately replaced.
func (d *Device) HostKeyNames() []string {
	bastions := d.ActiveBastions()
	var names []string
	via := ""
	for _, b := range bastions {
		p := b.Port
		if p == 0 {
			p = 22
		}
		if d.Tunneled() {
			via = TunnelName(via, HopAddr(b.Host, p))
			names = append(names, via)
		} else {
			names = append(names, HopAddr(b.Host, p))
		}
	}
	return append(names, TunnelName(via, HopAddr(d.Host, d.EffectivePort())))
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
	// Wait is text the device shows once this command is done, such as
	// "(config)#": the next command goes when the line the device is on
	// contains it. Empty means the OS profile's prompt.
	Wait string `json:"wait,omitempty"`
}

// CommandSet is a named, ordered list of commands (e.g. "config-backup").
type CommandSet struct {
	Name     string    `json:"name"`
	Commands []Command `json:"commands"`
	// PauseOff ignores every command's PauseSec / SerialSec (the values are
	// kept for when it is turned back on): each command goes as soon as the
	// prompt, or its Wait, shows. 待機秒を使う = !PauseOff.
	PauseOff bool `json:"pauseOff,omitempty"`
}

// SecretCommands returns the commands that type a credential as part of a
// command line ({password} / {enable}; {user} alone is no secret). The
// device echoes a command line, unlike a password prompt, so such a run is
// logged masked whatever the setting says, and an import shows these lines
// as it shows jump commands.
func (cs *CommandSet) SecretCommands() []string {
	if cs == nil {
		return nil
	}
	var out []string
	for _, c := range cs.Commands {
		if SendsSecret(c.Text) {
			out = append(out, c.Text)
		}
	}
	return out
}

// SendsSecret reports a command text that expands a password.
func SendsSecret(text string) bool {
	return strings.Contains(text, "{password}") || strings.Contains(text, "{enable}")
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
//
// A device that uses the user's own jump host (UseMyBastion) gets it as
// its first hop (see resolveMyBastion).
func (inv *Inventory) ResolveCredentials(d Device) Device {
	d = inv.resolveMyBastion(d)
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

// resolveMyBastion puts the user's own jump hosts (自分の踏み台, a chain of
// up to MaxBastions) in front of the device's own hops when the device asks
// for them. Those hops are marked Mine; with none registered there is one
// Mine hop with no host, which the dial refuses by name — never a silent
// direct connection. The result no longer asks (UseMyBastion off), so
// resolving it again adds nothing.
func (inv *Inventory) resolveMyBastion(d Device) Device {
	if !d.UseMyBastion {
		return d
	}
	d.NormalizeBastions()
	var chain []Bastion
	for _, b := range inv.MyBastions {
		if b.Host != "" {
			b.Mine = true
			chain = append(chain, b)
		}
	}
	if len(chain) == 0 {
		chain = []Bastion{{Method: BastionSSH, Mine: true}}
	}
	d.Bastions = append(chain, d.Bastions...)
	d.UseMyBastion = false
	return d
}

// ValidateMyBastions checks the user's own jump hosts before they are saved.
func ValidateMyBastions(chain []Bastion) error {
	if len(chain) > MaxBastions {
		return fmt.Errorf("自分の踏み台は %d 段までです", MaxBastions)
	}
	for i, b := range chain {
		n := i + 1
		if !validHostOrIP(b.Host) {
			return fmt.Errorf("自分の踏み台%d段目のホスト形式が不正です: %q", n, b.Host)
		}
		if b.Method != BastionSSH && b.Method != BastionTelnet {
			return fmt.Errorf("自分の踏み台%d段目の接続方式が不正です", n)
		}
		if b.Port < 0 || b.Port > 65535 {
			return fmt.Errorf("自分の踏み台%d段目のポート番号が範囲外です: %d（0〜65535）", n, b.Port)
		}
		if n == 1 && b.Method == BastionSSH && b.Username == "" {
			return errors.New("自分の踏み台1段目のユーザー名を入力してください")
		}
		// A later hop's username is typed into the previous hop's shell.
		if !ShellSafeWord(b.Username) {
			return fmt.Errorf("自分の踏み台%d段目のユーザー名に使えない文字（空白・記号 ; | & $ など）が含まれています: %q", n, b.Username)
		}
		if b.AuthMethod != "" && b.AuthMethod != AuthPassword && b.AuthMethod != AuthPublicKey {
			return fmt.Errorf("自分の踏み台%d段目の認証方式が不正です", n)
		}
		if isUNCPath(b.KeyFile) {
			return fmt.Errorf("自分の踏み台%d段目の秘密鍵にネットワーク上のパスは指定できません: %q", n, b.KeyFile)
		}
	}
	return nil
}

// BastionLogin is a jump-host login of v1.7.3's 自分の踏み台認証 (logins
// matched to hops by host and port), kept only so MigrateMyBastion can turn
// it into MyBastions.
type BastionLogin struct {
	Host          string     `json:"host"`
	Port          int        `json:"port,omitempty"` // 0 => 22
	Username      string     `json:"username"`
	Password      string     `json:"password,omitempty"`
	AuthMethod    AuthMethod `json:"authMethod,omitempty"`
	KeyFile       string     `json:"keyFile,omitempty"`
	KeyPassphrase string     `json:"keyPassphrase,omitempty"`
}

// MigrateMyBastion moves v1.7.3's logins (any number, applied to every SSH
// hop without a login of its own whose host and port matched) over:
//   - the first one becomes the own jump host, and a device whose first hop
//     is that host — with no login, legacy-cipher switch or jump command of
//     its own, so nothing is lost — drops that hop for UseMyBastion;
//   - every other hop v1.7.3 would have filled gets the matching login
//     written into it, so it keeps logging in exactly as before.
//
// Reports whether anything changed.
func (inv *Inventory) MigrateMyBastion() bool {
	if len(inv.MyBastionLogins) == 0 {
		return false
	}
	port := func(p int) int {
		if p == 0 {
			return 22
		}
		return p
	}
	match := func(b Bastion, l BastionLogin) bool {
		return b.Method == BastionSSH && strings.EqualFold(b.Host, l.Host) && port(b.Port) == port(l.Port)
	}
	bare := func(b Bastion) bool { return b.Username == "" && b.Password == "" && b.KeyFile == "" }
	if len(inv.MyBastions) == 0 {
		l := inv.MyBastionLogins[0]
		inv.MyBastions = []Bastion{{Host: l.Host, Port: l.Port, Method: BastionSSH, Username: l.Username, Password: l.Password,
			AuthMethod: l.AuthMethod, KeyFile: l.KeyFile, KeyPassphrase: l.KeyPassphrase}}
		for i := range inv.Devices {
			d := &inv.Devices[i]
			d.NormalizeBastions()
			if len(d.Bastions) == 0 || d.UseMyBastion {
				continue
			}
			b := d.Bastions[0]
			if match(b, l) && bare(b) && !b.LegacyAlgos && b.JumpCommand == "" {
				d.Bastions = d.Bastions[1:]
				d.UseMyBastion = true
			}
		}
	}
	for i := range inv.Devices {
		d := &inv.Devices[i]
		d.NormalizeBastions()
		for j := range d.Bastions {
			b := &d.Bastions[j]
			if !bare(*b) {
				continue
			}
			for _, l := range inv.MyBastionLogins {
				if match(*b, l) {
					b.Username, b.Password, b.AuthMethod = l.Username, l.Password, l.AuthMethod
					b.KeyFile, b.KeyPassphrase = l.KeyFile, l.KeyPassphrase
					break
				}
			}
		}
	}
	inv.MyBastionLogins = nil
	return true
}

// WithoutBastions returns d reached directly, its jump hosts left out —
// for a run or a terminal the user asked to open without them (踏み台を使わ
// ずに接続: on site, plugged into the device's network).
func (d Device) WithoutBastions() Device {
	d.Bastions, d.Bastion, d.BastionMode, d.UseMyBastion = nil, nil, BastionViaShell, false
	return d
}

// StripBastionCredentials empties every hop's login (username, password,
// key and its passphrase) — 共有用 exports, where those are personal.
func (d *Device) StripBastionCredentials() {
	d.NormalizeBastions()
	if len(d.Bastions) == 0 {
		return
	}
	d.Bastions = append([]Bastion(nil), d.Bastions...)
	for i := range d.Bastions {
		b := &d.Bastions[i]
		b.Username, b.Password, b.AuthMethod, b.KeyFile, b.KeyPassphrase = "", "", "", "", ""
	}
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

// Clone returns a deep copy of the inventory (by a JSON round trip: every
// field is persisted as JSON anyway, so the copy is exactly what the vault
// would hold).
func (inv *Inventory) Clone() *Inventory {
	b, err := json.Marshal(inv)
	if err != nil {
		panic(err) // plain data: cannot fail
	}
	var out Inventory
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return &out
}

// NewDeviceID returns a fresh random device ID.
func NewDeviceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b[:])
}

// EnsureDeviceIDs gives every device without an ID (vaults from before IDs,
// CSV imports, new devices) — or with one another device already holds — a
// fresh one.
func (inv *Inventory) EnsureDeviceIDs() {
	seen := map[string]bool{}
	for i := range inv.Devices {
		if id := inv.Devices[i].ID; id == "" || seen[id] {
			inv.Devices[i].ID = NewDeviceID()
		}
		seen[inv.Devices[i].ID] = true
	}
}

// DeviceIndex finds a device by ID. A key no device has as ID is tried as a
// name, so a caller still holding a name (an older terminal argument) keeps
// working while names are unique; -1 if neither matches.
func (inv *Inventory) DeviceIndex(key string) int {
	if key == "" {
		return -1
	}
	for i := range inv.Devices {
		if inv.Devices[i].ID == key {
			return i
		}
	}
	for i := range inv.Devices {
		if inv.Devices[i].Name == key {
			return i
		}
	}
	return -1
}

// DeviceNameTaken reports whether another device (ID other than exceptID)
// already has name in group.
func (inv *Inventory) DeviceNameTaken(group, name, exceptID string) bool {
	for i := range inv.Devices {
		d := &inv.Devices[i]
		if d.Group == group && d.Name == name && (exceptID == "" || d.ID != exceptID) {
			return true
		}
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

	// CommandSetSeedGen is the same idea for the built-in 【サンプル】 command
	// sets (CommandSetSeedGen const): a sample set added in a later release is
	// offered to an older vault once, matched by name, and never re-added
	// after the user deletes it. Zero = the original five.
	CommandSetSeedGen int `json:"commandSetSeedGen,omitempty"`

	// KnownHostKeys pins SSH host key fingerprints per "host:port"
	// (trust-on-first-use); a later mismatch aborts the connection. Stored
	// inside the encrypted vault like everything else.
	KnownHostKeys map[string]string `json:"knownHostKeys,omitempty"`

	// Schedules are the 予約 (scheduled batch runs), run by the main window
	// while it is open, locked or in the tray (see sched_app.go).
	Schedules []schedule.Schedule `json:"schedules,omitempty"`

	// GridDrafts is the device table editor's unsaved work, per scope (a
	// group name, GridUngrouped, or "" for every device). It holds
	// passwords like the devices do, so it lives here in the vault and
	// survives a lock or a restart until saved or thrown away.
	GridDrafts map[string]GridDraft `json:"gridDrafts,omitempty"`

	// MyBastionLogins are v1.7.3's own jump-host logins, read only to be
	// migrated into MyBastion (MigrateMyBastion).
	MyBastionLogins []BastionLogin `json:"myBastionLogins,omitempty"`

	// MyBastions is the user's own jump host chain (自分の踏み台, up to
	// MaxBastions hops): addresses and their own logins, in their vault only
	// and never exported. A device with UseMyBastion goes through it first.
	MyBastions []Bastion `json:"myBastions,omitempty"`

	// KnownGUICerts pins the certificate (SHA-256, or "ca" for one the
	// browser trusted) of each device's web GUI opened in PalaTerm's GUI
	// window, trust on first use like the SSH host keys: a self-signed GUI
	// opens without the browser's warning page, and a different certificate
	// later stops it. Keyed "gui <device ID> <host:port>".
	KnownGUICerts map[string]string `json:"knownGUICerts,omitempty"`
}

// GridUngrouped is the table editor's scope for the devices in no group.
const GridUngrouped = "__ungrouped__"

// GridDraft is one scope's table: its rows in order, and the devices the
// table deleted.
type GridDraft struct {
	Rows    []GridRow `json:"rows"`
	Deleted []GridRow `json:"deleted,omitempty"`
	// Saved is when the draft was last written (RFC 3339).
	Saved string `json:"saved,omitempty"`
}

// GridRow is a row of the table editor: the device it edits (ID, empty for
// a new row), the cells as they were when the table read the device (Base,
// to tell changed cells and edits made elsewhere meanwhile) and as they are
// now (Cells). UID names the row in the table, new rows included.
type GridRow struct {
	UID   string            `json:"uid"`
	ID    string            `json:"id,omitempty"`
	Base  map[string]string `json:"base,omitempty"`
	Cells map[string]string `json:"cells"`
	// Init marks a new row that already got the table's defaults (so a
	// default cleared on purpose is not put back after a reload).
	Init bool `json:"init,omitempty"`
	// After is, for a deleted row, the row it was under (to go back there if
	// the deletion is called off); "" for the top.
	After string `json:"after,omitempty"`
}

// Dirty reports that the row is new or differs from what it was read as.
func (r GridRow) Dirty() bool {
	if r.ID == "" {
		return true
	}
	return !SameCells(r.Base, r.Cells)
}

// SameCells compares two rows' cells, a missing cell counting as empty.
func SameCells(a, b map[string]string) bool {
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
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
	// StageTokenSchedule is the word for a 予約 (empty = "reserve").
	StageTokenSchedule string `json:"stageTokenSchedule,omitempty"`
	// StageTokenNone is the word in the log file names of a run with no stage
	// (empty = "Current").
	StageTokenNone string `json:"stageTokenNone,omitempty"`
	ConnectTimeout int    `json:"connectTimeout"` // seconds
	CommandTimeout int    `json:"commandTimeout"` // seconds per expect

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

	// Right-click in the interactive window, as Tera Term's
	// DisablePasteMouseRButton / ConfirmPasteMouseRButton: Off makes it do
	// nothing; Confirm shows a small menu whose 貼り付け pastes. Both false
	// (the default) pastes at once. Off wins over Confirm, as in Tera Term.
	RightClickPasteOff     bool `json:"rightClickPasteOff,omitempty"`
	RightClickPasteConfirm bool `json:"rightClickPasteConfirm,omitempty"`

	// ScheduleKeepAwake keeps the PC from idle sleep for as long as an
	// enabled 予約 is still to fire (set in the 予約 tab): for PCs that
	// cannot be woken for one (wake timers off, modern standby).
	ScheduleKeepAwake bool `json:"scheduleKeepAwake,omitempty"`

	// TiledBroadcast opens the windows of 並べて接続 with 同時入力 already on.
	// Off by default (user request 2026-10-21): each window is turned on by
	// hand, so keys never reach several devices unasked.
	TiledBroadcast bool `json:"tiledBroadcast,omitempty"`
}

// RightClickPaste returns what a right-click does in the interactive
// window: "off", "menu" or "paste".
func (s Settings) RightClickPaste() string {
	switch {
	case s.RightClickPasteOff:
		return "off"
	case s.RightClickPasteConfirm:
		return "menu"
	}
	return "paste"
}

// StageTokens returns the stage words for log names, defaults filled in.
func (s Settings) StageTokens() logstore.StageTokens {
	return logstore.StageTokens{Before: s.StageTokenBefore, Work: s.StageTokenWork, After: s.StageTokenAfter, Schedule: s.StageTokenSchedule, None: s.StageTokenNone}.Filled()
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
	if c, ok := ParseTermColor(d.TermColor); ok {
		d.TermColor = c
	} else {
		return fmt.Errorf("端末の色が不正です: %q（赤・橙・黄・緑・青・紫 か空欄）", d.TermColor)
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
	if isUNCPath(d.KeyFile) {
		return fmt.Errorf("秘密鍵にネットワーク上のパス（\\\\サーバー\\…）は指定できません: %q", d.KeyFile)
	}
	if d.GUIPort < 0 || d.GUIPort > 65535 {
		return fmt.Errorf("GUIポートが範囲外です: %d（0〜65535）", d.GUIPort)
	}
	if d.GUIScheme != "" && d.GUIScheme != "http" && d.GUIScheme != "https" {
		return fmt.Errorf("GUIの方式は http / https のどちらかです: %q", d.GUIScheme)
	}
	if d.CLIOff && !d.GUIOn {
		return errors.New("CLI と GUI のどちらかはオンにしてください")
	}
	// A GUI is opened at the device's address: it needs one (a serial
	// device's CLI does not).
	if d.GUIOn && strings.TrimSpace(d.Host) == "" {
		return errors.New("GUI を使う機器には IPアドレスが必要です")
	}
	if d.GUIOn && !validHostOrIP(d.Host) {
		return fmt.Errorf("IPアドレスの形式が不正です: %q", d.Host)
	}
	// A GUI-only device is never logged into: its (hidden) jump-host and
	// connection fields are kept as they are, unchecked.
	if d.CLIOff {
		return nil
	}
	bastions := d.ActiveBastions()
	if d.BastionMode != BastionViaShell && d.BastionMode != BastionViaTunnel {
		return fmt.Errorf("踏み台の通り方が不正です: %q", d.BastionMode)
	}
	tunnel := d.Tunneled()
	if tunnel && d.Conn == ConnSerial {
		return errors.New("シリアル接続の機器は踏み台を経由できません")
	}
	if d.UseMyBastion && d.Conn == ConnSerial && !d.CLIOff {
		return errors.New("シリアル接続の機器は踏み台を経由できません")
	}
	// Behind a jump host the device's username is typed into the hop's shell
	// ("ssh user@host"), so shell syntax in it would run there. A tunnel
	// types nothing on the hops.
	if d.UsesBastion() && !tunnel && !ShellSafeWord(d.Username) {
		return fmt.Errorf("ユーザー名に使えない文字（空白・記号 ; | & $ など）が含まれています: %q", d.Username)
	}
	for i, b := range bastions {
		if !validHostOrIP(b.Host) {
			return fmt.Errorf("踏み台%d段目のホスト形式が不正です: %q", i+1, b.Host)
		}
		// Hop 1 is typed on the own jump host's shell when there is one.
		if (i > 0 || d.UseMyBastion) && !tunnel && !ShellSafeWord(b.Username) {
			return fmt.Errorf("踏み台%d段目のユーザー名に使えない文字（空白・記号 ; | & $ など）が含まれています: %q", i+1, b.Username)
		}
		if isUNCPath(b.KeyFile) {
			return fmt.Errorf("踏み台%d段目の秘密鍵にネットワーク上のパスは指定できません: %q", i+1, b.KeyFile)
		}
		if b.Method != BastionSSH && b.Method != BastionTelnet {
			return fmt.Errorf("踏み台%d段目の接続方式が不正です", i+1)
		}
		if tunnel && b.Method != BastionSSH {
			return fmt.Errorf("SSH トンネルで通る踏み台はすべて SSH にしてください（%d段目が %s）", i+1, b.Method)
		}
		if b.Port < 0 || b.Port > 65535 {
			return fmt.Errorf("踏み台%d段目のポート番号が範囲外です: %d（0〜65535）", i+1, b.Port)
		}
	}
	return nil
}

// ValidateIn is Validate plus the checks that need the inventory: a device
// reaching the user's own jump hosts by SSH tunnel needs every one of them
// SSH (said when the device is saved, not on every connection later).
func (inv *Inventory) ValidateIn(d *Device) error {
	if err := d.Validate(); err != nil {
		return err
	}
	if d.UseMyBastion && !d.CLIOff && d.BastionMode == BastionViaTunnel && inv != nil {
		for i, b := range inv.MyBastions {
			if b.Host != "" && b.Method != BastionSSH {
				return fmt.Errorf("自分の踏み台%d段目が Telnet なので、SSH トンネルでは通れません（踏み台の通り方を「踏み台上でコマンドを打つ」にするか、設定タブで自分の踏み台を SSH にしてください）", i+1)
			}
		}
	}
	return nil
}

// GUICertKey names the device's GUI certificate pin (Inventory.KnownGUICerts).
func (d *Device) GUICertKey() string {
	return "gui " + d.ID + " " + HopAddr(d.Host, d.EffectiveGUIPort())
}

// ShellSafeWord reports whether s can be typed into a shell as one plain
// word: no whitespace, control characters or shell syntax. Empty is fine.
func ShellSafeWord(s string) bool {
	// A leading "-" would be read by the hop's ssh as an option (-oXxx=…).
	if strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(";|&$<>`'\"\\()*?!{}[]#~", r) {
			return false
		}
	}
	return true
}

// isUNCPath reports a Windows network path (\\server\share\… or //server/…):
// opening one sends the user's Windows credentials to that server, so a key
// path pointing there (say, from an imported file) would leak them.
func isUNCPath(p string) bool {
	p = strings.TrimSpace(p)
	// Windows treats / and \ alike, so any two of them open a network path
	// ("\/srv/share" too); NT-style "\??\UNC\…" and file: URLs get there as well.
	sep := func(b byte) bool { return b == '\\' || b == '/' }
	if len(p) >= 2 && sep(p[0]) && sep(p[1]) {
		return true
	}
	low := strings.ToLower(p)
	return strings.HasPrefix(low, `\??\`) || strings.HasPrefix(low, "file:")
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

// SampleGroupName is the starter group of a new vault, in the UI language
// the vault is created under (an English UI used to open on a Japanese
// card as its very first screen).
func SampleGroupName(lang string) string {
	if lang == "en" {
		return "Sample group"
	}
	return "サンプルグループ"
}

// sampleSetNamesEN maps the built-in sample sets' names to their English
// form: a vault created under the English UI is seeded with these.
var sampleSetNamesEN = map[string]string{
	"【サンプル】Cisco_ルータ":    "[Sample] Cisco router",
	"【サンプル】Cisco_L2SW":   "[Sample] Cisco L2 switch",
	"【サンプル】Cisco_L3SW":   "[Sample] Cisco L3 switch",
	"【サンプル】FortiGate":    "[Sample] FortiGate",
	"【サンプル】IXルータ":        "[Sample] NEC IX router",
	"【サンプル】ALAXALA_L2SW": "[Sample] ALAXALA L2 switch",
}

// LocalizeSampleSets renames the sample sets in sets for lang ("en": the
// English names; anything else leaves them). Only the exact built-in names
// are touched, so a user's own set is never renamed.
func LocalizeSampleSets(sets []CommandSet, lang string) []CommandSet {
	if lang != "en" {
		return sets
	}
	for i := range sets {
		if en, ok := sampleSetNamesEN[sets[i].Name]; ok {
			sets[i].Name = en
		}
	}
	return sets
}

// SampleSetNameEN is the English name of a built-in sample set ("" for any
// other name).
func SampleSetNameEN(ja string) string { return sampleSetNamesEN[ja] }

// DefaultCommandSets returns the built-in command sets seeded into a new
// vault. They mirror the show-command patterns of the legacy Tera Term tool
// (Show_Command_Pattern_1..7.list); pause values keep the original
// remote/console split (PauseSec = SSH/Telnet, SerialSec = console).
//
// Every command waits for "#" (待つ文字列), written out so the user sees
// what the run waits for: it is the prompt of the OS profiles these sets
// are made for (Cisco IOS, FortiGate, NEC IX, ALAXALA AX), and a wait text
// matches only at the end of the line, as the profile's prompt does.
func DefaultCommandSets() []CommandSet {
	c := func(text string, pause, serial int) Command {
		return Command{Text: text, PauseSec: pause, SerialSec: serial, Wait: SampleWait}
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
		// From the legacy tool's SW(ALAXALA) verification list (AX series L2
		// switch), pauses as in that sheet. Not yet checked against a real
		// switch; the ALAXALA AX profile disables the pager first.
		{Name: "【サンプル】ALAXALA_L2SW", Commands: []Command{
			c("show clock", 1, 1),
			c("show running-config", 10, 10),
			c("show startup-config", 10, 10),
			c("show version", 1, 1),
			c("show system", 1, 1),
			c("show environment", 1, 1),
			c("show flash", 1, 1),
			c("show cpu days", 1, 1),
			c("show cpu hours", 1, 1),
			c("show processes cpu", 1, 1),
			c("show memory", 1, 1),
			c("show power", 1, 1),
			c("show snmp", 1, 1),
			c("show interfaces", 10, 10),
			c("show port", 1, 1),
			c("show port vlan", 1, 1),
			c("show power inline", 1, 1),
			c("show channel-group", 1, 1),
			c("show channel-group statistics", 1, 1),
			c("show vlan", 1, 1),
			c("show vlan mac-vlan", 1, 1),
			c("show spanning-tree", 1, 1),
			c("show spanning-tree statistics", 1, 1),
			c("show ip interface", 1, 1),
			c("show ip arp", 1, 1),
			c("show mac-address-table", 1, 1),
			c("show ip route", 1, 1),
			c("show access-filter", 1, 1),
			c("show qos-flow", 1, 1),
			c("show qos queueing", 30, 30),
			c("show mac-authentication", 1, 1),
			c("show gsrp aware", 1, 1),
			c("show switchport-backup statistics", 1, 1),
			c("show loop-detection", 1, 1),
			c("show storm-control", 1, 1),
			c("show lldp", 1, 1),
			c("show lldp statistics", 1, 1),
			c("show logging", 30, 30),
		}},
	}
}

// CommandSetSeedGen is the current generation of the built-in sample sets. A
// vault records the generation it was last seeded from
// (Inventory.CommandSetSeedGen); a sample set introduced in a later generation
// is added to such a vault once. Bump this and add the set's name to
// commandSetIntroducedIn whenever a sample set joins DefaultCommandSets.
const CommandSetSeedGen = 2

// SampleWait is the 待つ文字列 of every command in the sample sets.
const SampleWait = "#"

// SampleWaitsAddedIn is the generation that wrote SampleWait into the
// sample sets; FillSampleWaits brings an older vault's samples up to it.
const SampleWaitsAddedIn = 2

// FillSampleWaits gives the commands of the sample sets (by name) that
// still have no 待つ文字列 the sample's one, for a vault seeded before
// SampleWaitsAddedIn. A command the user added or retyped (its text not in
// the sample) and a wait the user set are left alone. Reports a change.
func FillSampleWaits(sets []CommandSet) bool {
	want := map[string]map[string]string{}
	for _, cs := range DefaultCommandSets() {
		m := map[string]string{}
		for _, c := range cs.Commands {
			m[c.Text] = c.Wait
		}
		want[cs.Name] = m
	}
	changed := false
	for i := range sets {
		m := want[sets[i].Name]
		if m == nil {
			continue
		}
		for j := range sets[i].Commands {
			c := &sets[i].Commands[j]
			if w, ok := m[c.Text]; ok && c.Wait == "" && w != "" {
				c.Wait, changed = w, true
			}
		}
	}
	return changed
}

// commandSetIntroducedIn maps the sample sets that joined after the original
// five to the generation that brought them. Original sets are absent (0).
var commandSetIntroducedIn = map[string]int{
	"【サンプル】ALAXALA_L2SW": 1,
}

// CommandSetsAddedAfter returns the sample sets introduced after generation
// gen, in DefaultCommandSets order.
func CommandSetsAddedAfter(gen int) []CommandSet {
	var out []CommandSet
	for _, cs := range DefaultCommandSets() {
		if commandSetIntroducedIn[cs.Name] > gen {
			out = append(out, cs)
		}
	}
	return out
}
