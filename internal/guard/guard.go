// Package guard spots commands in a command set that change a device rather
// than read from it — entering configuration mode, writing or erasing
// configuration, rebooting — so the run tab can warn before a batch that is
// supposed to be a harmless "show" sweep quietly reconfigures ten routers.
//
// The patterns are deliberately broad and vendor-mixed (Cisco IOS / NX-OS /
// ASA / XR, Juniper, PAN-OS, FortiGate, Comware / VRP, Yamaha, NEC IX,
// ArubaOS-Switch, AlliedWare Plus, EOS, VyOS, Extreme, Linux shells): a
// false alarm costs one extra click, a miss costs a device. The vocabulary
// is kept as one string per vendor (or per theme) and joined into a rule per
// reason, like strict.go's notSettingWords, so a miss is added where it
// belongs and read in context.
package guard

import (
	"regexp"
	"strings"
)

// Hit is one flagged command.
type Hit struct {
	Index  int    `json:"index"`  // position in the command set (0-based)
	Text   string `json:"text"`   // the command as written
	Reason string `json:"reason"` // what kind of change it makes (Japanese, translated by the UI)
}

type rule struct {
	re     *regexp.Regexp
	reason string
}

// Reasons are the UI's keys; i18n.js maps them to English.
const (
	ReasonConfigMode = "設定モードに入る"
	ReasonWrite      = "設定を保存・上書きする"
	ReasonReload     = "再起動・停止する"
	ReasonErase      = "消去・初期化する"
	ReasonInterface  = "インターフェースや設定を変更する"
	ReasonCommit     = "設定を確定する"
)

// Every vocabulary below is an alternation of words as a command starts
// (matched case-insensitively, after the prefixes Check strips). A word is
// bounded by `(\s|$)` where a longer word must not match ("reload" is not
// "reloaded", "halt" is not "haltx"); a word followed by `\s` wants an
// argument.
const (
	// --- Entering a configuration mode (or a shell where anything goes) ---

	// Cisco / NEC "configure terminal", "conf t", NX-OS "configure", bare
	// "config" (IOS takes the prefix; FortiGate's global mode), Juniper
	// "configure" / "edit", Yamaha "administrator", NEC IX "svintr-config".
	configModeCommon = `conf(ig(ure)?)?(\s+t(erminal)?)?(\s|$)|config\s+\S|svintr-config(\s|$)|edit(\s|$)|administrator(\s|$)`
	// Comware / VRP "system-view" and its short form "sys" (bare).
	configModeComware = `system-view(\s|$)|sys(tem)?$`
	// A shell on the device: EOS "bash", Junos "start shell".
	configModeShell = `bash(\s|$)|start\s+shell(\s|$)`

	// --- Reboot / power / failover ---

	// The words themselves, Linux "shutdown" with any argument form (the
	// cancel, "shutdown -c", is a read: see reads), and IOS's bare
	// "shutdown" in any prefix from "shu" up ("sh" is show).
	reloadCommon = `(reload|reboot|restart|halt|poweroff)(\s|$)|shutdown\s|shu(t(d(o(w(n)?)?)?)?)?$`
	// IOS accepts any unambiguous prefix, and PalaTerm answers "[confirm]"
	// for it (Profile.Answers), so "rel" / "relo" would run unwarned.
	reloadCiscoShort = `rel(o(ad?)?)?(\s|$)`
	// IOS-XE / XR / NX-OS / ASA: modules, the standby, the HA peer.
	reloadCisco = `hw-module\s|admin\s+reload(\s|$)|redundancy\s+(force-switchover|reload)(\s|$)|system\s+switchover(\s|$)|failover\s+(active|reload-standby)(\s|$)`
	// Junos / PAN-OS "request …".
	reloadRequest = `request\s+(restart|shutdown)\s|request\s+system\s+(reboot|halt|power-off|private-data-reset)(\s|$)|request\s+(vmhost|chassis|high-availability)\s`
	// FortiGate "execute …" ("exe" and "exec" too).
	reloadFortiOS = `exe(c(ute)?)?\s+(reboot|shutdown|factoryreset|ha\s+failover|router\s+restart|disconnect-admin-session|wireless-controller\s+reset-wtp)(\s|$)`
	// Comware / VRP "schedule reboot", AlliedWare Plus "atmf reboot-rolling",
	// ArubaOS-Switch bare "boot", NetScreen bare "reset".
	reloadOthers = `schedule\s+reboot(\s|$)|atmf\s+reboot(-rolling)?(\s|$)|boot$|reset$`
	// Linux: init levels, systemctl's power verbs.
	reloadLinux = `init\s+[06](\s|$)|systemctl\s+(reboot|poweroff|halt|kexec|kill)(\s|$)|telinit\s+[06](\s|$)`

	// --- Erase / delete / format / factory reset ---

	// IOS and friends ("delete" bare is Junos: the whole hierarchy), the
	// IOS short forms, Yamaha "cold start", NEC "restart factory", "reset"
	// with an argument (Comware "reset saved-configuration"; the counters
	// and ARP forms are reads, see reads).
	eraseCommon     = `erase(\s|$)|format(\s|$)|delete(\s|$)|rm\s|rmdir\s|clear\s+(config|startup|running)|write\s+erase|cold\s+start|restart\s+factory|reset\s|default\s+(interface|config)(\s|$)|unconfigure(\s|$)`
	eraseCiscoShort = `(del(e(te?)?)?|era(se?)?|form(at?)?)\s|(wr(i(te?)?)?\s+er(a(se?)?)?|squeeze)(\s|$)`
	// Junos / PAN-OS / VyOS: zeroize, storage cleanup, the rescue config,
	// loading a config over the candidate (factory-default, override, …).
	eraseRequest = `request\s+system\s+(zeroize|storage\s+cleanup|configuration\s+rescue\s+delete)(\s|$)|load\s+(factory-default|override|replace|merge|config|/)`
	// Comware / VRP: restoring, choosing the startup config or image,
	// the boot loader, fixdisk.
	eraseComware = `restore\s|startup\s+(saved-configuration|system-software)(\s|$)|boot-loader\s|fixdisk(\s|$)`
	// FortiGate: "purge" (a whole table), log deletion, config reload.
	eraseFortiOS = `purge(\s|$)|exe(c(ute)?)?\s+(formatlogdisk(\s|$)|erase(\s|$)|log\s+delete|cfg\s+reload(\s|$))`
	// Linux: disks, file systems, moving files, cron, accounts.
	eraseLinux = `dd\s|mkfs(\s|\.|$)|wipefs(\s|$)|shred\s|fdisk\s|parted(\s|$)|mv\s|truncate\s|crontab\s+-r(\s|$)|userdel\s`

	// --- Saving / overwriting configuration ---

	// "wr" / "write [memory]" in any prefix ("write terminal" only shows
	// the config), "write file", "save", "copy … startup-config" /
	// "running-config" (the full-form backup "copy running-config tftp:"
	// passes: a copy is a change only by its destination), ASA "write
	// standby", Yamaha "copy config", FortiGate backup / restore / cfg save.
	writeCommon = `wr(i(te?)?)?(\s+me(m(o(ry?)?)?)?)?$|(write\s+file|write\s+standby|save(\s|$)|copy\s+\S+\s+(startup|running)-config|copy\s+config)(\s|$)|exe(c(ute)?)?\s+(backup|restore|cfg\s+save)(\s|$)`
	// A copy INTO the startup / running config (any prefix: "copy run
	// sta") or onto device storage (overwriting a boot image).
	writeCopy = `cop(y)?\s+\S+\s+(st(a(r(t)?)?)?|ru(n)?|nvram:|system:|(boot|sup-boot|usb)?flash\d*:|disk\d*:|slot\d*:|bootdisk:)|archive\s+(download-sw|upload-sw|tar\s+/x)`

	// --- Changing a setting (commands that betray intent even alone) ---

	// IOS-style config commands: no / interface / ip route / vlan / …,
	// install / upgrade, "default interface", "debug all", NX-OS
	// "feature", the service-disrupting clears (BGP, OSPF, ASA xlate).
	changeCisco = `no\s+\S|interface\s+\S|set\s+\S|unset\s+\S|ip\s+(route|address)\s|vlan\s+\d|hostname\s+\S|username\s+\S|enable\s+(secret|password)\s|router\s+\S|access-list\s|ip\s+access-list\s|crypto\s|line\s+(vty|con)|boot\s+system|license\s+\S|install\s|upgrade\s|request\s+system\s+software|request\s+platform\s+software\s+package\s+install|feature\s+\S|debug\s+all(\s|$)|clear\s+(ip\s+bgp|ip\s+ospf\s+process|xlate)(\s|$)`
	// Comware / VRP: "undo …" (the "no"), sysname, static routes, "vlan
	// batch".
	changeComware = `undo\s+\S|sysname\s|ip\s+route-static\s|vlan\s+batch(\s|$)`
	// FortiGate update / install.
	changeFortiOS = `exe(c(ute)?)?\s+(update|install)(\s|$)`
	// Yamaha RTX has no configuration mode: every setting is a command of
	// its own. Addresses and filters of lan / pp / tunnel / wan, the pp
	// and tunnel selectors, NAT, DHCP, VLAN, passwords, firmware update.
	changeYamaha = `ip\s+(lan|pp|tunnel|wan)\d*\s|pp\s+(select|disable|enable)(\s|$)|tunnel\s+(select|enable|disable)(\s|$)|ip\s+filter\s|nat\s+descriptor\s|dhcp\s+(scope|service)(\s|$)|vlan\s+lan\d|login\s+password(\s|$)|administrator\s+password(\s|$)|http\s+revision-up(\s|$)`
	// AlliedWare Plus boot config, Extreme "disable port", VyOS images.
	changeOthers = `boot\s+config-file(\s|$)|disable\s+port(\s|$)|add\s+system\s+image(\s|$)`
	// Linux: services, interfaces and addresses, firewalls, processes,
	// packages, accounts, recursive permission changes, containers.
	changeLinux = `systemctl\s+(stop|restart|disable|mask|isolate)(\s|$)|service\s+\S+\s+(stop|restart)(\s|$)|ifdown\s|ip\s+link\s+set\s+\S+\s+down(\s|$)|ip\s+addr(ess)?\s+(flush|del|add)(\s|$)|iptables(\s|$)|nft\s|ufw\s+(disable|reset|enable)(\s|$)|firewall-cmd\s|kill(all)?\s|pkill\s|(apt|apt-get|yum|dnf|pip3?)\s+(remove|purge|install|upgrade|autoremove)(\s|$)|passwd(\s|$)|chmod\s+-R(\s|$)|chown\s+-R(\s|$)|docker\s+(rm|rmi|system\s+prune)(\s|$)`

	// --- Commit (Juniper / NX-OS checkpoint / rollback) ---
	commitCommon = `commit(\s|$)|checkpoint\s|rollback\s`
)

// startsWith is a rule's regexp: the command starts with one of the words.
func startsWith(groups ...string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)^(?:` + strings.Join(groups, "|") + `)`)
}

var rules = []rule{
	{startsWith(configModeCommon, configModeComware, configModeShell), ReasonConfigMode},
	{startsWith(reloadCommon, reloadCiscoShort, reloadCisco, reloadRequest, reloadFortiOS, reloadOthers, reloadLinux), ReasonReload},
	{startsWith(eraseCommon, eraseCiscoShort, eraseRequest, eraseComware, eraseFortiOS, eraseLinux), ReasonErase},
	{startsWith(writeCommon, writeCopy), ReasonWrite},
	{startsWith(changeCisco, changeComware, changeFortiOS, changeYamaha, changeOthers, changeLinux), ReasonInterface},
	{startsWith(commitCommon), ReasonCommit},
}

// reRedirect is a shell writing its output into a file, anywhere on the
// line: "… > /etc/hosts", "… >> file", "… | tee file". (A redirection to
// /dev/null is dropped first, see reDevNull.)
var reRedirect = regexp.MustCompile(`\s>{1,2}\s|\|\s*tee\s`)

// reDevNull is output thrown away, which writes nothing.
var reDevNull = regexp.MustCompile(`\s[12]?>{1,2}\s*/dev/null`)

// reads are commands the rules would flag but that only read or only
// change the session: Comware / VRP / VyOS "reset counters" / "reset arp" /
// "reset logbuffer" / "reset ip bgp" (exec-level, not the saved config),
// the pager-off lines a set may spell out ("set cli screen-length 0",
// "set terminal length 0", "set console page 0", "set terminal pager
// disable", Aruba "no page"), Linux "ip route show" and its kin, "reload
// cancel", "shutdown -c" (the cancel). A pager-off line must never warn:
// it is the line every set is told to start with.
var reads = regexp.MustCompile(`(?i)^(?:reset\s+(counters|arp|logbuffer|session|ip\s+bgp)(\s|$)|set\s+(cli|terminal)\s|set\s+console\s+page(\s|$)|no\s+page$|ip\s+route\s+(show|list|get)(\s|$)|reload\s+cancel$|shutdown\s+-c(\s|$))`)

// rePrefix is what only changes who runs the command, not what it does:
// "do " (IOS: an exec command from config mode), Junos / VyOS "run ",
// Linux "sudo" / "doas" with their options ("sudo -i", "sudo -S", "sudo -u
// root"), "su -c". Stripped repeatedly ("sudo su -c …").
var rePrefix = regexp.MustCompile(`(?i)^(?:do|run|(?:sudo|doas)(?:\s+-\S+(?:\s+[^-\s]\S*)?)*|su\s+-c)\s+`)

// Check returns the commands that look like they change the device, in
// command-set order. Commands are matched on their trimmed text; a leading
// "do " / "sudo " / "run " (see rePrefix) is stripped first so "do show
// run" stays harmless and "do reload" does not.
func Check(cmds []string) []Hit {
	var hits []Hit
	for i, raw := range cmds {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		probe := strip(text)
		if reads.MatchString(probe) {
			continue
		}
		hit := false
		for _, r := range rules {
			if r.re.MatchString(probe) {
				hits = append(hits, Hit{Index: i, Text: text, Reason: r.reason})
				hit = true
				break
			}
		}
		if !hit && reRedirect.MatchString(reDevNull.ReplaceAllString(probe, "")) {
			hits = append(hits, Hit{Index: i, Text: text, Reason: ReasonWrite})
		}
	}
	return hits
}

// strip removes the run-as prefixes (rePrefix) and the quotes "su -c"
// takes its command in.
func strip(text string) string {
	for {
		loc := rePrefix.FindStringIndex(text)
		if loc == nil || loc[1] >= len(text) {
			return text
		}
		text = strings.TrimSpace(text[loc[1]:])
		text = strings.TrimLeft(text, `"'`)
	}
}
