// Package guard spots commands in a command set that change a device rather
// than read from it — entering configuration mode, writing or erasing
// configuration, rebooting — so the run tab can warn before a batch that is
// supposed to be a harmless "show" sweep quietly reconfigures ten routers.
//
// The patterns are deliberately broad and vendor-mixed (Cisco IOS / NX-OS,
// Juniper, FortiGate, Yamaha, NEC IX, Linux shells): a false alarm costs one
// extra click, a miss costs a device.
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

var rules = []rule{
	// Configuration mode (Cisco/NEC "configure terminal", "conf t", NX-OS
	// "configure", Juniper "configure"/"edit", FortiGate "config ...",
	// Yamaha "administrator", HP "configure", NEC IX "svintr-config").
	{regexp.MustCompile(`(?i)^(conf(igure)?(\s+t(erminal)?)?|svintr-config|edit(\s|$)|administrator)(\s|$)`), ReasonConfigMode},
	{regexp.MustCompile(`(?i)^config\s+\S`), ReasonConfigMode},
	// Reboot / power.
	{regexp.MustCompile(`(?i)^(reload|reboot|restart|shutdown\s*$|halt|poweroff|execute\s+(reboot|shutdown|factoryreset)|request\s+system\s+(reboot|halt|power-off))`), ReasonReload},
	// Erase / delete / format / reset.
	{regexp.MustCompile(`(?i)^(erase|format|delete\s|rm\s|clear\s+(config|startup|running)|write\s+erase|execute\s+(formatlogdisk|erase)|request\s+system\s+zeroize|cold\s+start|restart\s+factory|reset\s)`), ReasonErase},
	// Saving / overwriting configuration.
	{regexp.MustCompile(`(?i)^(wr(ite)?(\s+mem(ory)?)?|write\s+file|save(\s|$)|copy\s+\S+\s+(startup|running)-config|copy\s+running-config)(\s|$)`), ReasonWrite},
	{regexp.MustCompile(`(?i)^(execute\s+backup|execute\s+restore)`), ReasonWrite},
	// Change commands that are only legal inside config mode but betray intent
	// even when listed alone (interface / no / set / unset / ip / vlan …).
	{regexp.MustCompile(`(?i)^(no\s+\S|interface\s+\S|set\s+\S|unset\s+\S|ip\s+(route|address)\s|vlan\s+\d|hostname\s+\S|username\s+\S|enable\s+(secret|password)\s|router\s+\S|access-list\s|ip\s+access-list\s|crypto\s|line\s+(vty|con)|boot\s+system|license\s+\S|install\s|upgrade\s|execute\s+(update|install)|request\s+system\s+software)`), ReasonInterface},
	// Commit (Juniper / NX-OS checkpoint / FortiGate "end" closes config).
	{regexp.MustCompile(`(?i)^(commit(\s|$)|checkpoint\s|rollback\s)`), ReasonCommit},
}

// Check returns the commands that look like they change the device, in
// command-set order. Commands are matched on their trimmed text; a leading
// "do " (Cisco: run an exec command from config mode) is stripped first so
// "do show run" stays harmless and "do reload" does not.
func Check(cmds []string) []Hit {
	var hits []Hit
	for i, raw := range cmds {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		probe := strings.TrimSpace(strings.TrimPrefix(text, "do "))
		for _, r := range rules {
			if r.re.MatchString(probe) {
				hits = append(hits, Hit{Index: i, Text: text, Reason: r.reason})
				break
			}
		}
	}
	return hits
}
