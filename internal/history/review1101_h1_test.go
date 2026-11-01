package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/logstore"
)

// Review of 2026-11-01 (C_history, items of agent H1), lab values only.

// S-1: "no <free-text keyword>" while the text is still there is missing,
// not already.
func TestH1NoFreeTextKeyword(t *testing.T) {
	work := lines("lab-sw1(config)#interface gi0/1", "lab-sw1(config-if)#no description", "lab-sw1(config-if)#exit", "lab-sw1(config)#vlan 10", "lab-sw1(config-vlan)#no name", "lab-sw1(config-vlan)#end")
	cfg := lines("lab-sw1#show running-config", "interface GigabitEthernet0/1", " description uplink", "vlan 10", " name LAB")
	got, _ := statuses(t, work, cfg, cfg)
	expect(t, got, map[string]string{
		" > interface gi0/1":               "already/abbrev",
		"interface gi0/1 > no description": "missing",
		" > vlan 10":                       "already",
		"vlan 10 > no name":                "missing",
	})
	// And reflected once the text is gone.
	after := lines("lab-sw1#show running-config", "interface GigabitEthernet0/1", "vlan 10")
	got, _ = statuses(t, work, cfg, after)
	if got["interface gi0/1 > no description"] != "reflected" || got["vlan 10 > no name"] != "reflected" {
		t.Errorf("gone: %v", got)
	}
}

// S-2: names are never abbreviations.
func TestH1NamesAreExact(t *testing.T) {
	work := lines("lab-sw1(config)#interface gi0/1", "lab-sw1(config-if)#ip access-group lab in", "lab-sw1(config-if)#exit",
		"lab-sw1(config)#hostname lab", "lab-sw1(config)#username ops privilege 15", "lab-sw1(config)#snmp-server community lab RO", "lab-sw1(config)#end")
	before := lines("lab-sw1#show running-config", "hostname old", "interface GigabitEthernet0/1")
	after := lines("lab-sw1#show running-config", "hostname lab-core", "username opsadmin privilege 15", "snmp-server community labro RO", "interface GigabitEthernet0/1", " ip access-group labx in")
	got, r := statuses(t, work, before, after)
	for _, k := range []string{"interface gi0/1 > ip access-group lab in", " > hostname lab", " > username ops privilege 15", " > snmp-server community lab RO"} {
		if got[k] != "missing" {
			t.Errorf("%q: %s, want missing", k, got[k])
		}
	}
	if r.Missing != 4 {
		t.Errorf("missing %d: %v", r.Missing, got)
	}
	// Keywords still abbreviate; an exact line wins over an abbreviation.
	for _, c := range []struct {
		typed, cand string
		want        bool
	}{
		{"user ops priv 15", "username ops privilege 15", true},
		{"username ops privilege 15", "username opsadmin privilege 15", false},
		{"ip access-list extended lab", "ip access-list extended labx", false},
		{"ip access-l ext lab", "ip access-list extended lab", true},
		{"route-map lab permit 10", "route-map lab-out permit 10", false},
		{"hostname lab", "hostname lab-core", false},
		{"set system host-name lab", "set system host-name lab-core", false},
	} {
		if got := tokMatch(strings.Fields(c.typed), strings.Fields(c.cand), whole); got != c.want {
			t.Errorf("%q vs %q: %v", c.typed, c.cand, got)
		}
	}
	// block(): the ACL typed is not another ACL's block.
	x := indexConfig(lines("lab-sw1#show running-config", "ip access-list extended labx", " permit ip any any", "ip access-list extended lab", " deny ip any any"))
	if h, _ := x.block("ip access-list extended lab"); h != "ip access-list extended lab" {
		t.Errorf("block %q", h)
	}
	x = indexConfig(lines("lab-sw1#show running-config", "ip access-list extended labx", " permit ip any any"))
	if h, b := x.block("ip access-list extended lab"); b != nil {
		t.Errorf("another ACL's block taken: %q", h)
	}
}

// S-3: a block typed in but not listed afterwards: a line found in another
// block is "?" (otherBlock), never reflected or already.
func TestH1UnlistedBlockNotFromOtherBlock(t *testing.T) {
	work := lines("lab-sw1(config)#interface range macro lab", "lab-sw1(config-if-range)#shutdown", "lab-sw1(config-if-range)#exit",
		"lab-sw1(config)#class-map c1", "lab-sw1(config-cmap)#match protocol http", "lab-sw1(config-cmap)#end")
	cfg := lines("lab-sw1#show running-config", "class-map match-all other", " match protocol http", "interface GigabitEthernet0/9", " shutdown")
	got, r := statuses(t, work, cfg, cfg)
	if got["interface range macro lab > shutdown"] != "unverified/otherBlock" || got["class-map c1 > match protocol http"] != "unverified/otherBlock" {
		t.Errorf("%v", got)
	}
	if r.Reflected != 0 {
		t.Errorf("reflected %d", r.Reflected)
	}
	// Nothing found anywhere stays missing; a line of a filtered part (no
	// block at all) still counts (TestFinalParts).
	got, _ = statuses(t, work, cfg, lines("lab-sw1#show running-config", "hostname lab-sw1"))
	if got["class-map c1 > match protocol http"] != "missing" {
		t.Errorf("%v", got)
	}
}

// S-4: FortiOS context from the typed config / edit lines and the prompts.
func TestH1FortiContext(t *testing.T) {
	work := lines("FG # config system ntp", "FG (ntp) # config ntpserver", "FG (ntpserver) # edit 1", "FG (1) # set server 192.0.2.10", "FG (1) # next",
		"FG (ntpserver) # edit 2", "FG (2) # set server 192.0.2.11", "FG (2) # next", "FG (ntpserver) # end", "FG (ntp) # end")
	after := lines("FG # show", "config system ntp", "    config ntpserver", "        edit 2", "            set server \"192.0.2.11\"", "        next", "    end", "end")
	got, r := statuses(t, work, "FG # show\nconfig system ntp\nend\n", after)
	if got[" > set server 192.0.2.10"] != "missing" || got[" > set server 192.0.2.11"] != "reflected" || r.Missing != 1 {
		t.Errorf("edit 1 missing, edit 2 reflected: %v", got)
	}
	// The same line in another entry does not count.
	work2 := lines("FG # config firewall policy", "FG (policy) # edit 5", "FG (5) # set status disable", "FG (5) # next", "FG (policy) # end")
	cfg2 := lines("FG # show", "config firewall policy", "    edit 5", "        set name \"a\"", "    next", "    edit 9", "        set status disable", "    next", "end")
	got, r = statuses(t, work2, cfg2, cfg2)
	if got[" > set status disable"] != "unverified/otherBlock" || r.Reflected != 0 {
		t.Errorf("%v", got)
	}
	// The context itself.
	cmds, heads, _ := extractTypedAll([]string{work}, nil)
	if len(cmds) != 2 || cmds[0].ctx != "config system ntp / config ntpserver / edit 1" || cmds[1].ctx != "config system ntp / config ntpserver / edit 2" {
		t.Errorf("ctx: %+v", cmds)
	}
	if strings.Join(heads, "|") != "config system ntp|config system ntp / config ntpserver|config system ntp / config ntpserver / edit 1|config system ntp / config ntpserver / edit 2" {
		t.Errorf("heads: %q", heads)
	}
	// Cisco's "configure terminal" → (config) is no FortiOS context.
	cmds, heads, _ = extractTypedAll([]string{lines("sw1#configure terminal", "sw1(config)#hostname lab", "sw1(config)#end")}, nil)
	if len(heads) != 0 || len(cmds) != 1 || cmds[0].ctx != "" {
		t.Errorf("cisco: %+v %q", cmds, heads)
	}
}

// B-3: FortiOS structure lines in the changes outside the work: "next" is
// never one, the typed "edit" and the lines under it are the work's.
func TestH1FortiNextNotUnexpected(t *testing.T) {
	work := lines("fw # config firewall address", "fw (address) # edit lab-net", "fw (lab-net) # set subnet 192.0.2.0 255.255.255.0", "fw (lab-net) # next", "fw (address) # end")
	before := lines("fw # show", "config firewall address", "    edit \"old\"", "        set subnet 198.51.100.0 255.255.255.0", "    next", "end", "fw # ")
	after := lines("fw # show", "config firewall address", "    edit \"old\"", "        set subnet 198.51.100.0 255.255.255.0", "    next",
		"    edit \"lab-net\"", "        set subnet 192.0.2.0 255.255.255.0", "        set comment \"x\"", "    next", "end", "fw # ")
	_, r := statuses(t, work, before, after)
	if r.Missing != 0 || r.UnexpectedCount != 0 {
		t.Errorf("missing %d, unexpected %s", r.Missing, unexpectedLines(r))
	}
	// A change in an entry the work did not open is still listed.
	after2 := strings.Replace(after, "198.51.100.0 255.255.255.0", "198.51.100.0 255.255.255.128", 1)
	_, r = statuses(t, work, before, after2)
	if r.UnexpectedCount != 2 {
		t.Errorf("other entry: %s", unexpectedLines(r))
	}
}

// S-5: inside a configuration listing only the stamps are dropped; the
// noise rules still apply to show output.
func TestH1ConfigNormalizer(t *testing.T) {
	a := "lab-sw1#show running-config\ninterface GigabitEthernet0/1\n description used for backup\n!\nend\nlab-sw1#show interfaces\n  5 minute input rate 1000 bits/sec\nlab-sw1#\n"
	b := strings.Replace(a, "used for backup", "used by lab", 1)
	b = strings.Replace(b, "1000 bits/sec", "2000 bits/sec", 1)
	for _, cfgOnly := range []bool{true, false} {
		r := DiffWith(a, b, DiffOpts{IgnoreNoise: true, IgnoreSecrets: true, ConfigOnly: cfgOnly})
		if r.Changed != 1 || r.Added+r.Removed != 0 {
			t.Errorf("configOnly=%v: ~%d +%d -%d", cfgOnly, r.Changed, r.Added, r.Removed)
		}
	}
	a2 := "fw#show running-config\ntimeout conn 1:00:00 half-closed 0:10:00\nend\nfw#\n"
	b2 := strings.Replace(a2, "1:00:00", "2:00:00", 1)
	if r := DiffWith(a2, b2, DiffOpts{IgnoreNoise: true, ConfigOnly: true}); r.Changed != 1 {
		t.Errorf("asa timeout: %d", r.Changed)
	}
	if normalizeConfig("! Last configuration change at 09:01:02 JST Tue Oct 20 2026 by ops") != "" || normalizeConfig(" description 3 days") != "description 3 days" {
		t.Error("normalizeConfig")
	}
}

// S-11 / A-3: only single-valued settings are superseded or explain a
// removed value; list settings stay missing and their other entries show.
func TestH1SingleValuedOnly(t *testing.T) {
	work := lines("lab-sw1(config)#radius-server host 192.0.2.20", "lab-sw1(config)#radius-server host 192.0.2.21",
		"lab-sw1(config)#tacacs-server host 192.0.2.30", "lab-sw1(config)#tacacs-server host 192.0.2.31",
		"lab-sw1(config)#interface gi0/1", "lab-sw1(config-if)#ipv6 address 2001:db8::1/64", "lab-sw1(config-if)#ipv6 address 2001:db8:1::1/64",
		"lab-sw1(config-if)#ip helper-address 192.0.2.5", "lab-sw1(config-if)#ip helper-address 192.0.2.6",
		"lab-sw1(config-if)#speed 100", "lab-sw1(config-if)#speed 1000", "lab-sw1(config-if)#switchport mode trunk", "lab-sw1(config-if)#switchport mode access",
		"lab-sw1(config-if)#end")
	after := lines("lab-sw1#show running-config", "radius-server host 192.0.2.21", "tacacs-server host 192.0.2.31", "interface GigabitEthernet0/1",
		" ipv6 address 2001:db8:1::1/64", " ip helper-address 192.0.2.6", " speed 1000", " switchport mode access")
	got, r := statuses(t, work, "lab-sw1#show running-config\ninterface GigabitEthernet0/1\n", after)
	for _, k := range []string{" > radius-server host 192.0.2.20", " > tacacs-server host 192.0.2.30", "interface gi0/1 > ipv6 address 2001:db8::1/64", "interface gi0/1 > ip helper-address 192.0.2.5"} {
		if got[k] != "missing" {
			t.Errorf("%q: %s, want missing", k, got[k])
		}
	}
	for _, k := range []string{"interface gi0/1 > speed 100", "interface gi0/1 > switchport mode trunk"} {
		if got[k] != "superseded" {
			t.Errorf("%q: %s, want superseded", k, got[k])
		}
	}
	if r.Missing != 4 {
		t.Errorf("missing %d", r.Missing)
	}
	// A-3: a typed list line does not explain the other entries.
	work2 := lines("lab-sw1(config)#ip route 0.0.0.0 0.0.0.0 192.0.2.1", "lab-sw1(config)#ntp server 192.0.2.10", "lab-sw1(config)#end")
	before := lines("lab-sw1#show running-config", "ip route 198.51.100.0 255.255.255.0 192.0.2.9", "ntp server 192.0.2.99")
	after2 := lines("lab-sw1#show running-config", "ip route 0.0.0.0 0.0.0.0 192.0.2.1", "ntp server 192.0.2.10", "ntp server 192.0.2.77")
	_, r = statuses(t, work2, before, after2)
	if r.Missing != 0 || r.UnexpectedCount != 3 {
		t.Errorf("missing %d, unexpected %s", r.Missing, unexpectedLines(r))
	}
	for _, c := range []struct {
		later, earlier string
		want           bool
	}{
		{"ip address 192.0.2.2 255.255.255.0", "ip address 192.0.2.1 255.255.255.0", true},
		{"ip address 192.0.2.2 255.255.255.0 secondary", "ip address 192.0.2.1 255.255.255.0", false},
		{"desc new", "description old", true},
		{"username ops privilege 15", "username alice privilege 15", false},
		{"username ops privilege 15", "username ops privilege 1", true},
		{"switchport trunk allowed vlan 20", "switchport trunk allowed vlan 10", true},
		{"switchport trunk allowed vlan add 20", "switchport trunk allowed vlan add 10", false},
		{"set status disable", "set status enable", true},
		{"set server 192.0.2.11", "set server 192.0.2.10", true},
		{"set system ntp server 192.0.2.11", "set system ntp server 192.0.2.10", false},
		{"set system host-name b", "set system domain-name a", false},
		{"ntp server 192.0.2.11", "ntp server 192.0.2.10", false},
		{"logging host 192.0.2.11", "logging host 192.0.2.10", false},
		{"snmp-server community b ro", "snmp-server community a ro", false},
	} {
		if got := replaces(strings.Fields(c.later), strings.Fields(c.earlier)); got != c.want {
			t.Errorf("replaces(%q, %q) = %v", c.later, c.earlier, got)
		}
	}
}

// A-1: Comware / VRP, SR OS and Yamaha prompts.
func TestH1Prompts(t *testing.T) {
	for l, want := range map[string]bool{
		"<lab-sw1>display current-configuration":    true,
		"[lab-sw1]sysname lab2":                     true,
		"[lab-sw1-GigabitEthernet1/0/1]shutdown":    true,
		"*A:lab-sw1>config>router# info":            true,
		"# ip route default gateway 192.0.2.1":      true,
		"> show config":                             true,
		"# RTX1210 Rev.14.01.38 (Mon Oct 20 2026)":  false, // Yamaha's listing header
		"# Reporting Date: Oct 20 10:00:00 2026":    false,
		"[edit interfaces ge-0/0/0]":                false,
		"[edit]":                                    false,
		"lab-sw1(config)#hostname x":                true,
		"GE1/0/1 UP 1G(a) F(a)":                     false,
		"Interface  IP-Address  OK?  Method Status": false,
	} {
		if got := promptParts(l) != nil; got != want {
			t.Errorf("%q: prompt %v", l, got)
		}
	}
	for l, want := range map[string]bool{"<lab-sw1>": true, "[lab-sw1]": true, "*A:lab-sw1>config#": true, "#": true, "lab-sw1#": true, "# x": false} {
		if got := barePrompt.MatchString(l); got != want {
			t.Errorf("bare %q: %v", l, got)
		}
	}
	cw := lines("<lab-sw1>display current-configuration", "#", " version 7.1", "#", "sysname lab-sw1", "#", "interface GigabitEthernet1/0/1", " port link-mode bridge", "#",
		"<lab-sw1>display interface brief", "GE1/0/1 UP 1G(a) F(a)", "<lab-sw1>")
	if _, kind := configSection(cw); kind != oneListing {
		t.Errorf("comware kind %d", kind)
	}
	work := lines("<lab-sw1>system-view", "[lab-sw1]interface GigabitEthernet1/0/1", "[lab-sw1-GigabitEthernet1/0/1]undo shutdown", "[lab-sw1-GigabitEthernet1/0/1]description up", "[lab-sw1-GigabitEthernet1/0/1]return")
	got, r := statuses(t, work, lines("<lab-sw1>display current-configuration", "interface GigabitEthernet1/0/1", " shutdown"), lines("<lab-sw1>display current-configuration", "interface GigabitEthernet1/0/1", " description up"))
	if r.Typed != 5 || r.Missing != 0 || r.UnexpectedCount != 0 {
		t.Errorf("comware work: typed %d missing %d unexpected %s: %v", r.Typed, r.Missing, unexpectedLines(r), got)
	}
	// A-7: "undo shutdown" removed the shutdown line.
	if got[" > undo shutdown"] != "reflected" && got["interface GigabitEthernet1/0/1 > undo shutdown"] != "reflected" {
		t.Errorf("undo: %v", got)
	}
	ya := lines("# show config", "# RTX1210 Rev.14.01.38 (Mon Oct 20 2026)", "# Reporting Date: Oct 20 10:00:00 2026", "ip route default gateway 192.0.2.1", "# ")
	if _, kind := configSection(ya); kind != oneListing {
		t.Errorf("yamaha kind %d", kind)
	}
}

// A-2: the stamps of NX-OS, ASA, IOS-XR and Yamaha listings.
func TestH1ConfigStamps(t *testing.T) {
	nx := "lab-sw1# show running-config\n!Command: show running-config\n!Running configuration last done at: Mon Oct 20 10:00:00 2026\n!Time: Mon Oct 20 10:05:00 2026\nversion 9.3(5)\nhostname lab-sw1\nlab-sw1# \n"
	nx2 := strings.NewReplacer("10:00:00 2026", "11:00:00 2026", "Oct 20 10:05", "Oct 21 11:05").Replace(nx)
	if s := Summarize(nx, nx2, DiffOpts{IgnoreNoise: true}); s.Config.Any() {
		t.Errorf("nxos %+v", s.Config)
	}
	asa := "fw# show running-config\n: Saved\n: Written by ops at 10:00:00.000 JST Mon Oct 20 2026\nASA Version 9.12\nhostname fw\nfw# \n"
	if s := Summarize(asa, strings.Replace(asa, "Oct 20", "Oct 21", 1), DiffOpts{IgnoreNoise: true}); s.Config.Any() {
		t.Errorf("asa %+v", s.Config)
	}
	xr := "RP/0/RP0/CPU0:lab-rt1#show running-config\n!! IOS XR Configuration 7.3.2\n!! Last configuration change at Mon Oct 20 10:00:00 2026 by ops\nhostname lab-rt1\nRP/0/RP0/CPU0:lab-rt1#\n"
	if s := Summarize(xr, strings.Replace(xr, "Oct 20", "Oct 21", 1), DiffOpts{IgnoreNoise: true}); s.Config.Any() {
		t.Errorf("xr %+v", s.Config)
	}
	ya := "# show config\n# RTX1210 Rev.14.01.38\n# Reporting Date: Oct 20 10:00:00 2026\nip route default gateway 192.0.2.1\n# \n"
	if s := Summarize(ya, strings.Replace(ya, "Oct 20", "Oct 21", 1), DiffOpts{IgnoreNoise: true}); s.Config.Any() || s.Whole.Any() {
		t.Errorf("yamaha %+v %+v", s.Whole, s.Config)
	}
	// A real change still shows through the stamps.
	if s := Summarize(nx, strings.Replace(nx2, "hostname lab-sw1", "hostname lab-sw2", 1), DiffOpts{IgnoreNoise: true}); !s.Config.Any() {
		t.Error("nxos change hidden")
	}
}

// A-4: a two-digit number is a device's number unless a date or time is
// next to it; "作業前後比較" names no stage.
func TestH1ExternalNumbers(t *testing.T) {
	tok := logstore.StageTokens{}
	for in, want := range map[string]string{
		"lab-sw-01_before.txt":            "lab-sw-01",
		"lab-sw-02_before.txt":            "lab-sw-02",
		"core_01_after.log":               "core_01",
		"fw-10-before.txt":                "fw-10",
		"sw2_2026-09-01_14h30.log":        "sw2",
		"sw01_20260901_14_30.txt":         "sw01",
		"sw01_before_20260901_143000.txt": "sw01",
		"s1_before.txt":                   "s1",
	} {
		if got := externalDeviceKey(in, tok); got != want {
			t.Errorf("externalDeviceKey(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"作業前後比較": "", "作業前ログ": "before", "変更後": "after", "事前事後比較": "", "作業中ログ": "during"} {
		if got := stageFromName(in, tok); got != want {
			t.Errorf("stageFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A-5: ssh / telnet lines at a (config) prompt are settings; at "#" they
// are exec commands.
func TestH1SSHSettings(t *testing.T) {
	work := "fw(config)# ssh 192.0.2.0 255.255.255.0 inside\nfw(config)# ssh timeout 30\nfw(config)# telnet 192.0.2.0 255.255.255.0 inside\nfw(config)# end\nfw# ssh ops@192.0.2.10\n"
	cmds, typed := ExtractConfigCommands(work)
	if typed != 5 || strings.Join(cmds, "|") != "ssh 192.0.2.0 255.255.255.0 inside|ssh timeout 30|telnet 192.0.2.0 255.255.255.0 inside" {
		t.Errorf("asa: %q typed %d", cmds, typed)
	}
	cmds, _ = ExtractConfigCommands("lab-sw1(config)# ssh key rsa 2048\nlab-sw1(config)# ssh login-attempts 3\nlab-sw1(config)# end\nlab-sw1# ssh 192.0.2.10\n")
	if strings.Join(cmds, "|") != "ssh key rsa 2048|ssh login-attempts 3" {
		t.Errorf("nxos: %q", cmds)
	}
	if cmds, _ = ExtractConfigCommands("ops@jump1:~$ ssh 192.0.2.10\n"); len(cmds) != 0 {
		t.Errorf("shell: %q", cmds)
	}
}

// A-6: a warning between a command and its longer retyping keeps both; a
// help list still folds them.
func TestH1HelpRedisplay(t *testing.T) {
	work := "lab-sw1(config)#interface gi0/1\nlab-sw1(config-if)#ip address 192.0.2.1 255.255.255.0\n% Warning: overlapping with another interface\nlab-sw1(config-if)#ip address 192.0.2.1 255.255.255.0 secondary\nlab-sw1(config-if)#end\n"
	cmds, _ := ExtractConfigCommands(work)
	if strings.Join(cmds, "|") != "interface gi0/1|ip address 192.0.2.1 255.255.255.0|ip address 192.0.2.1 255.255.255.0 secondary" {
		t.Errorf("warning: %q", cmds)
	}
	help := "lab-sw1(config)#interface gi0/1\nlab-sw1(config-if)#de\n  description  Interface specific description\n  delay        Specify interface throughput delay\nlab-sw1(config-if)#description uplink\nlab-sw1(config-if)#end\n"
	if cmds, _ = ExtractConfigCommands(help); strings.Join(cmds, "|") != "interface gi0/1|description uplink" {
		t.Errorf("help: %q", cmds)
	}
}

// A-8: checklist blocks — nested FortiOS blocks, an unknown block is "?",
// and the FAIL lookup does not take Gi0/10 for Gi0/1.
func TestH1ChecklistBlocks(t *testing.T) {
	after := lines("fw # show", "config system ntp", "    config ntpserver", "        edit 1", "            set server \"192.0.2.5\"", "        next", "    end", "end",
		"config log syslogd setting", "    set server \"192.0.2.123\"", "end", "fw # ")
	r := VerifyChecklist(ChecklistTemplate("fortigate", "ntp", []string{"192.0.2.123"}), after)
	if r.Pass != 0 || len(r.Results) != 1 || r.Results[0].Status == "pass" {
		t.Errorf("syslog's server passed for ntp: %+v", r.Results)
	}
	r = VerifyChecklist(ChecklistTemplate("fortigate", "ntp", []string{"192.0.2.5"}), after)
	if r.Pass != 1 {
		t.Errorf("ntp .5: %+v", r.Results)
	}
	r = VerifyChecklist([]CheckLine{{Text: "set server \"192.0.2.5\"", Block: "config system ntp / config ntpserver / edit 2"}}, after)
	if r.Unverified != 1 || r.Results[0].Note != "noBlock" {
		t.Errorf("unknown entry: %+v", r.Results)
	}
	cfg := lines("lab-sw1#show running-config", "interface GigabitEthernet0/1", " description a", "interface GigabitEthernet0/10", " switchport access vlan 99")
	r = VerifyChecklist([]CheckLine{{Text: "switchport access vlan 20", Block: "interface GigabitEthernet0/1"}}, cfg)
	if r.Missing != 1 || r.Results[0].Found != "" {
		t.Errorf("gi0/10 taken for gi0/1: %+v", r.Results)
	}
	r = VerifyChecklist([]CheckLine{{Text: "switchport access vlan 20", Block: "interface Vlan99"}}, cfg)
	if r.Unverified != 1 || r.Results[0].Note != "noBlock" {
		t.Errorf("unknown block: %+v", r.Results)
	}
}

// A-10: a secret is matched with the words after it too.
func TestH1SecretTail(t *testing.T) {
	work := "r1(config)#ntp authentication-key 1 md5 Secret123\nr1(config)#crypto isakmp key Secret address 192.0.2.1\nr1(config)#username ops password LabPass1\nr1(config)#end\n"
	before := "r1#show running-config\nhostname r1\n"
	after := "r1#show running-config\nhostname r1\nntp authentication-key 2 md5 0123456789ABCDEF 7\ncrypto isakmp key 6 ABCDEFGHIJ address 192.0.2.99\nusername ops secret 9 $9$abcdefgh\n"
	got, _ := statuses(t, work, before, after)
	expect(t, got, map[string]string{
		" > ntp authentication-key 1 md5 Secret123":     "missing",
		" > crypto isakmp key Secret address 192.0.2.1": "missing",
		" > username ops password LabPass1":             "reflected/secret",
	})
	after2 := "r1#show running-config\nhostname r1\nntp authentication-key 1 md5 0123456789ABCDEF 7\ncrypto isakmp key 6 ABCDEFGHIJ address 192.0.2.1\n"
	got, _ = statuses(t, work, before, after2)
	if got[" > ntp authentication-key 1 md5 Secret123"] != "reflected/secret" || got[" > crypto isakmp key Secret address 192.0.2.1"] != "reflected/secret" {
		t.Errorf("%v", got)
	}
}

// G B-14: MaskSecretLine hides the value after a secret keyword.
func TestH1MaskSecretLine(t *testing.T) {
	for in, want := range map[string]string{
		"username ops password LabPass1":             "username ops password ●●●",
		"username ops privilege 15 secret 5 $1$abc":  "username ops privilege 15 secret 5 ●●●",
		"enable secret Lab1":                         "enable secret ●●●",
		"ntp authentication-key 1 md5 Secret123":     "ntp authentication-key 1 md5 ●●●",
		"set password ENC abcdefgh":                  "set password ENC ●●●",
		"set  password   Secret123":                  "set  password   ●●●",
		"tacacs-server key lab":                      "tacacs-server key ●●●",
		"crypto isakmp key Secret address 192.0.2.1": "crypto isakmp key ●●● address 192.0.2.1",
		"key chain LAB":                              "key chain LAB",
		"interface gi0/1":                            "interface gi0/1",
		"no shutdown":                                "no shutdown",
	} {
		if got := MaskSecretLine(in); got != want {
			t.Errorf("MaskSecretLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// B-1: cursor moves are bounded.
func TestH1CursorBound(t *testing.T) {
	if got := ResolveLineEdits("\x1b[50000000Cx"); len(got) > 5000 {
		t.Errorf("len %d", len(got))
	}
	if got := ResolveLineEdits("\x1b[99999999999999Gx"); len(got) > 5000 {
		t.Errorf("len %d", len(got))
	}
	if got := ResolveLineEdits("ab\x1b[1Gx"); got != "xb" {
		t.Errorf("%q", got)
	}
}

// H 型4: pager lines of every vendor are noise.
func TestH1PagerLines(t *testing.T) {
	for l, want := range map[string]bool{
		"--More--":         true,
		" ---- More ----":  true,
		"---(more 25%)---": true,
		"---(more)---":     true,
		"-- MORE --, next page: Space, next line: Enter, quit: Control-C": true,
		"Press <SPACE> to continue or <Q> to quit:":                       true,
		"Press any key to continue (Q to quit)":                           true,
		"More: <space>,  Quit: q or CTRL+Z, One line: <return> ":          true,
		"interface GigabitEthernet0/1":                                    false,
		"more lab":                                                        false,
	} {
		if got := pagerLine.MatchString(l); got != want {
			t.Errorf("%q: %v", l, got)
		}
	}
	a := "ops@jump1> show configuration\nsystem {\n    host-name lab;\n}\n---(more 25%)---\ninterfaces {\n}\nops@jump1> \n"
	b := strings.Replace(a, "---(more 25%)---\n", "", 1)
	if r := DiffWith(a, b, DiffOpts{IgnoreNoise: true}); r.Added+r.Removed+r.Changed != 0 {
		t.Errorf("junos pager counted: +%d -%d ~%d", r.Added, r.Removed, r.Changed)
	}
}

// J-3: a summary's logFile is a file name in the run folder, nothing else.
func TestH1LogFileStaysInFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "20261001_120000_lab")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "lab-sw1.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "outside.txt"), []byte("y"), 0o644)
	if err := Write(dir, Summary{Devices: []DeviceSummary{
		{Name: "lab-sw1", Success: true, LogFile: "lab-sw1.txt"},
		{Name: "evil", Success: true, LogFile: `..\outside.txt`},
		{Name: "evil2", Success: true, LogFile: `../outside.txt`},
		{Name: "evil3", Success: true, LogFile: `..`},
	}}); err != nil {
		t.Fatal(err)
	}
	r := readRun(dir, logstore.StageTokens{})
	if len(r.Devices) != 4 || r.Devices[0].LogPath == "" {
		t.Fatalf("%+v", r.Devices)
	}
	for _, d := range r.Devices[1:] {
		if d.LogPath != "" {
			t.Errorf("%s: %q", d.Name, d.LogPath)
		}
	}
}

// C S-9 (H2 handoff): a banner's closing "#" line is kept in the compared
// lines while the banner is open, so the tree sees where it ends.
func TestH1BannerDelimiterKept(t *testing.T) {
	a := lines("lab-sw1#show running-config", "banner motd #", "Authorized access only", "#", "ip route 0.0.0.0 0.0.0.0 192.0.2.1", "lab-sw1#")
	la := splitLines(a)
	sel, kind := configSectionIdx(la)
	out, _, _, _ := prepLines(la, sel, listingSet(len(la), sel, kind), DiffOpts{IgnoreNoise: true})
	var keys []string
	for _, d := range out {
		keys = append(keys, d.key)
	}
	if strings.Join(keys, "|") != "banner motd #|Authorized access only|#|ip route 0.0.0.0 0.0.0.0 192.0.2.1" {
		t.Errorf("keys %q", keys)
	}
	// A bare prompt outside a banner is still noise.
	b := lines("lab-sw1#show running-config", "hostname lab-sw1", "lab-sw1#", "#")
	lb := splitLines(b)
	sel, kind = configSectionIdx(lb)
	out, _, noise, _ := prepLines(lb, sel, listingSet(len(lb), sel, kind), DiffOpts{IgnoreNoise: true})
	if len(out) != 1 || noise != 2 {
		t.Errorf("out %d noise %d", len(out), noise)
	}
}
