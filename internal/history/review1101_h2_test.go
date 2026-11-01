package history

import (
	"strings"
	"testing"
)

// Review 2026-11-01 (C: history, H2 part): generated commands, the
// comparison tree and the impact rules. All device data is made up for
// the lab (192.0.2.x / 198.51.100.x, lab-sw1, ops).

func h2has(t *testing.T, got []string, want ...string) {
	t.Helper()
	s := strings.Join(got, "\n")
	for _, w := range want {
		if !strings.Contains(s+"\n", w+"\n") && !strings.Contains(s, w) {
			t.Fatalf("missing %q in:\n%s", w, s)
		}
	}
}

func h2lacks(t *testing.T, got []string, bad ...string) {
	t.Helper()
	s := strings.Join(got, "\n")
	for _, b := range bad {
		if strings.Contains(s, b) {
			t.Fatalf("must not contain %q:\n%s", b, s)
		}
	}
}

func h2pos(t *testing.T, got []string, s string) int {
	t.Helper()
	for i, l := range got {
		if l == s {
			return i
		}
	}
	t.Fatalf("missing %q in:\n%s", s, strings.Join(got, "\n"))
	return -1
}

// S-6: a changed list entry (ip route, ntp server, snmp-server community,
// ip helper-address) removes B's value before A's comes back; a
// single-valued setting (description, hostname, ip address) is put back
// by A's line alone.
func TestH2CiscoListChangeRemovesB(t *testing.T) {
	a := wrapCisco("ip route 0.0.0.0 0.0.0.0 192.0.2.1\nntp server 192.0.2.10\nsnmp-server community labro RO\nlogging host 192.0.2.50\ninterface GigabitEthernet0/1\n description uplink\n ip address 192.0.2.1 255.255.255.0\n ip helper-address 192.0.2.5\n!\n")
	b := strings.NewReplacer("192.0.2.1\n", "192.0.2.2\n", "192.0.2.10", "192.0.2.11", "labro", "labrw", "192.0.2.50", "192.0.2.51", "uplink", "downlink", "192.0.2.1 255", "192.0.2.9 255", "192.0.2.5", "192.0.2.6").Replace(a)
	g := GenerateCommands(a, b, "rollback", "cisco-ios")
	got := cmdTexts(g)
	h2has(t, got, "no ip route 0.0.0.0 0.0.0.0 192.0.2.2", "ip route 0.0.0.0 0.0.0.0 192.0.2.1",
		"no ntp server 192.0.2.11", "ntp server 192.0.2.10",
		"no snmp-server community labrw RO", "snmp-server community labro RO",
		"no logging host 192.0.2.51", "logging host 192.0.2.50",
		"no ip helper-address 192.0.2.6", "ip helper-address 192.0.2.5",
		"description uplink", "ip address 192.0.2.1 255.255.255.0")
	h2lacks(t, got, "no description", "no ip address", "no hostname")
	if h2pos(t, got, "no ntp server 192.0.2.11") > h2pos(t, got, "ntp server 192.0.2.10") {
		t.Fatalf("B's entry must go before A's comes back:\n%s", strings.Join(got, "\n"))
	}
	// Inside a block the "no" lines come first.
	if h2pos(t, got, "no ip helper-address 192.0.2.6") > h2pos(t, got, "description uplink") {
		t.Fatalf("no-lines first in a block:\n%s", strings.Join(got, "\n"))
	}
	// A changed hostname is one line (no "no hostname").
	a2, b2 := wrapCisco(""), strings.Replace(wrapCisco(""), "hostname sw-lab-01", "hostname sw-lab-02", 1)
	got = cmdTexts(GenerateCommands(a2, b2, "rollback", "cisco-ios"))
	h2has(t, got, "hostname sw-lab-01")
	h2lacks(t, got, "no hostname")
}

// S-6: "username" of the same name is an overwrite; another name is a
// removal that is flagged (IOS asks for confirmation).
func TestH2CiscoUsername(t *testing.T) {
	a := wrapCisco("username ops privilege 15\n!\n")
	b := strings.Replace(a, "privilege 15", "privilege 1", 1)
	got := cmdTexts(GenerateCommands(a, b, "rollback", "cisco-ios"))
	h2has(t, got, "username ops privilege 15")
	h2lacks(t, got, "no username")
	c := strings.Replace(a, "username ops privilege 15", "username lab privilege 15", 1)
	g := GenerateCommands(a, c, "rollback", "cisco-ios")
	got = cmdTexts(g)
	h2has(t, got, "no username lab privilege 15", "username ops privilege 15")
	if g.Manual == 0 {
		t.Fatalf("the username removal must be flagged for a look:\n%+v", g.Lines)
	}
}

// S-7: FortiGate — a removed line and an added one of different keys in
// one entry are not one overwrite: B's key is unset, A's line set.
func TestH2FortiUnrelatedPair(t *testing.T) {
	fa := "fw # show full-configuration\nconfig firewall policy\n    edit 1\n        set name \"lab\"\n        set comments \"old\"\n    next\nend\nfw # \n"
	fb := strings.Replace(fa, "        set comments \"old\"\n", "        set status disable\n", 1)
	got := cmdTexts(GenerateCommands(fa, fb, "rollback", "fortigate"))
	h2has(t, got, "unset status", "set comments \"old\"")
	if h2pos(t, got, "unset status") > h2pos(t, got, "set comments \"old\"") {
		t.Fatalf("unset before set:\n%s", strings.Join(got, "\n"))
	}
	// The same key with another value stays one line.
	fc := strings.Replace(fa, "\"old\"", "\"new\"", 1)
	got = cmdTexts(GenerateCommands(fa, fc, "rollback", "fortigate"))
	h2has(t, got, "set comments \"old\"")
	h2lacks(t, got, "unset comments")
}

// S-7: Junos — B's leaf is deleted before A's is set, for an unrelated
// pair and for a list leaf alike; two unrelated "set system …" lines do
// not pair as one changed line.
func TestH2JunosDeleteBeforeSet(t *testing.T) {
	ja := "ops@jump1> show configuration | display set\nset system host-name lab\nset system ntp server 192.0.2.10\nops@jump1> \n"
	jb := strings.Replace(ja, "set system ntp server 192.0.2.10\n", "set system services ssh root-login allow\n", 1)
	got := cmdTexts(GenerateCommands(ja, jb, "rollback", "junos"))
	h2has(t, got, "delete system services ssh root-login allow", "set system ntp server 192.0.2.10", "commit")
	if h2pos(t, got, "delete system services ssh root-login allow") > h2pos(t, got, "set system ntp server 192.0.2.10") {
		t.Fatalf("delete before set:\n%s", strings.Join(got, "\n"))
	}
	jc := strings.Replace(ja, "192.0.2.10", "192.0.2.11", 1)
	got = cmdTexts(GenerateCommands(ja, jc, "rollback", "junos"))
	h2has(t, got, "delete system ntp server 192.0.2.11", "set system ntp server 192.0.2.10")
	if leafKey2("set system ntp server 192.0.2.10") != "set system ntp server" || leafKey2("set system services ssh root-login allow") == leafKey2("set system ntp server 192.0.2.10") {
		t.Fatalf("junos leaf key: %q", leafKey2("set system ntp server 192.0.2.10"))
	}
	if leafKey2("set ip 192.0.2.10 255.255.255.0") != "set ip" {
		t.Fatalf("fortigate set key: %q", leafKey2("set ip 192.0.2.10 255.255.255.0"))
	}
}

// S-8: a named ACL with entries in another order, or one entry changed, is
// removed and re-entered whole (like a numbered one); so is a prefix list.
func TestH2NamedACLRecreated(t *testing.T) {
	a := wrapCisco("ip access-list extended LAB\n permit tcp any any eq 22\n permit ip host 192.0.2.10 any\n deny ip any any\n!\n")
	b := wrapCisco("ip access-list extended LAB\n permit ip host 192.0.2.10 any\n permit tcp any any eq 22\n deny ip any any\n!\n")
	g := GenerateCommands(a, b, "rollback", "cisco-ios")
	if g.Empty {
		t.Fatalf("reorder gives no commands")
	}
	want := []string{"configure terminal", "no ip access-list extended LAB", "ip access-list extended LAB", "permit tcp any any eq 22", "permit ip host 192.0.2.10 any", "deny ip any any", "exit", "end"}
	if got := cmdTexts(g); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	c := strings.Replace(a, "192.0.2.10", "192.0.2.11", 1)
	if got := cmdTexts(GenerateCommands(a, c, "rollback", "cisco-ios")); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("entry change:\n%s", strings.Join(got, "\n"))
	}
	pa := wrapCisco("ip prefix-list LAB seq 5 permit 192.0.2.0/24\nip prefix-list LAB seq 10 permit 198.51.100.0/24\n!\n")
	pb := strings.Replace(pa, "seq 10 permit 198.51.100.0/24", "seq 10 permit 198.51.100.0/25", 1)
	got := cmdTexts(GenerateCommands(pa, pb, "rollback", "cisco-ios"))
	h2has(t, got, "no ip prefix-list LAB", "ip prefix-list LAB seq 5 permit 192.0.2.0/24", "ip prefix-list LAB seq 10 permit 198.51.100.0/24")
	h2lacks(t, got, "198.51.100.0/25")
}

// S-9: a banner with "#" as its delimiter: the delimiter is not a block
// closer, the banner comes back with its closing delimiter, and the lines
// after it are commands again.
func TestH2BannerHashDelimiter(t *testing.T) {
	a := wrapCisco("banner motd #\nAuthorized access only\n#\nip route 0.0.0.0 0.0.0.0 192.0.2.1\nlogging host 192.0.2.50\n!\n")
	b := strings.Replace(a, "Authorized access only", "Authorized access only!", 1)
	b = strings.Replace(b, "192.0.2.50", "192.0.2.51", 1)
	got := cmdTexts(GenerateCommands(a, b, "rollback", "cisco-ios"))
	h2has(t, got, "no banner motd", "banner motd #", "Authorized access only", "#", "no logging host 192.0.2.51", "logging host 192.0.2.50", "end")
	if h2pos(t, got, "#") != h2pos(t, got, "Authorized access only")+1 {
		t.Fatalf("closing delimiter must follow the text:\n%s", strings.Join(got, "\n"))
	}
	if got[len(got)-1] != "end" || h2pos(t, got, "logging host 192.0.2.50") < h2pos(t, got, "#") {
		t.Fatalf("logging host inside the banner:\n%s", strings.Join(got, "\n"))
	}
	// "^C" delimiter, and a one-line ASA banner is not a block.
	r := DiffWith(wrapCisco("banner login ^C\nlab only\n^C\nntp server 192.0.2.10\n!\n"), wrapCisco("banner login ^C\nlab only\n^C\nntp server 192.0.2.11\n!\n"), DiffOpts{IgnoreNoise: true, ConfigOnly: true})
	for _, l := range r.Lines {
		if strings.Contains(l.B, "ntp server") && l.Depth != 0 {
			t.Fatalf("ntp server swallowed by the banner: %+v", l)
		}
	}
	if _, _, ok := bannerBlock("banner motd Authorized access only"); ok {
		t.Fatalf("an ASA one-line banner is not a block")
	}
	if _, d, ok := bannerBlock("banner motd ^C"); !ok || d != "^C" {
		t.Fatalf("^C delimiter: %q %v", d, ok)
	}
	if _, _, ok := bannerBlock("banner motd #lab#"); ok {
		t.Fatalf("a banner closed on its own line is not a block")
	}
}

// S-10: "no vrf forwarding" and "switchport" / "no switchport" wipe the
// interface's addresses: A's address lines come back after them, flagged.
func TestH2VrfRestoresAddress(t *testing.T) {
	a := wrapCisco("interface GigabitEthernet0/1\n ip address 192.0.2.1 255.255.255.0\n!\n")
	b := wrapCisco("interface GigabitEthernet0/1\n vrf forwarding LAB\n ip address 192.0.2.1 255.255.255.0\n!\n")
	g := GenerateCommands(a, b, "rollback", "cisco-ios")
	got := cmdTexts(g)
	h2has(t, got, "no vrf forwarding LAB", "ip address 192.0.2.1 255.255.255.0")
	if h2pos(t, got, "no vrf forwarding LAB") > h2pos(t, got, "ip address 192.0.2.1 255.255.255.0") || g.Manual == 0 {
		t.Fatalf("address after the vrf removal, flagged:\n%s", strings.Join(got, "\n"))
	}
	c := wrapCisco("interface GigabitEthernet0/1\n switchport\n switchport access vlan 10\n!\n")
	d := wrapCisco("interface GigabitEthernet0/1\n no switchport\n ip address 192.0.2.1 255.255.255.0\n!\n")
	got = cmdTexts(GenerateCommands(d, c, "rollback", "cisco-ios"))
	h2has(t, got, "no switchport", "ip address 192.0.2.1 255.255.255.0")
	if h2pos(t, got, "no switchport") > h2pos(t, got, "ip address 192.0.2.1 255.255.255.0") {
		t.Fatalf("address after the switchport toggle:\n%s", strings.Join(got, "\n"))
	}
}

// A-11: a trunk's VLAN list with "add" lines is one set; none / all /
// except are left for a look.
func TestH2AllowedVlanAddLines(t *testing.T) {
	a := wrapCisco("interface GigabitEthernet0/1\n switchport trunk allowed vlan 1-100\n switchport trunk allowed vlan add 200-300\n!\n")
	b := strings.Replace(a, "add 200-300", "add 200-400", 1)
	got := cmdTexts(GenerateCommands(a, b, "rollback", "cisco-ios"))
	h2has(t, got, "switchport trunk allowed vlan remove 301-400")
	h2lacks(t, got, "add 200-300", "no switchport")
	c := strings.Replace(a, "add 200-300", "none", 1)
	g := GenerateCommands(a, c, "rollback", "cisco-ios")
	if g.Manual == 0 {
		t.Fatalf("none must be flagged:\n%s", joinCmds(g))
	}
	h2lacks(t, cmdTexts(g), "remove", "add 200-300")
}

// A-9: a short top-level line after a long indented one is not its
// wrapped tail when it starts like a command.
func TestH2WrappedLineKeepsCommands(t *testing.T) {
	a := "lab-sw1# show running-config\ninterface Vlan10\n  description " + strings.Repeat("x", 80) + "\nclock timezone JST 9 0\nntp server 192.0.2.10\nlab-sw1# \n"
	b := strings.Replace(a, "JST 9 0", "UTC 0 0", 1)
	r := DiffWith(a, b, DiffOpts{IgnoreNoise: true, ConfigOnly: true})
	for _, l := range r.Lines {
		if strings.Contains(l.B, "clock timezone") && l.Depth != 0 {
			t.Fatalf("clock timezone taken as a wrapped tail: %+v", l)
		}
	}
	got := cmdTexts(GenerateCommands(a, b, "rollback", "nxos"))
	h2has(t, got, "clock timezone JST 9 0")
	h2lacks(t, got, "interface Vlan10", "exit")
	// A real wrapped tail (a value, no keyword) still joins the line above.
	long := &dline{text: " description " + strings.Repeat("x", 70), depth: 1}
	if !wrapped(long, &dline{text: "yy", key: "yy"}) || wrapped(long, &dline{text: "clock timezone JST 9 0", key: "clock timezone JST 9 0"}) || !wrapped(long, &dline{text: "any eq 22", key: "any eq 22"}) {
		t.Fatalf("wrapped heuristic")
	}
}

// B-4: more physical port kinds are known; none gets "no interface".
// "default interface" only where the device has it.
func TestH2PhysicalInterfaces(t *testing.T) {
	a := wrapCisco("interface GigabitEthernet0/1\n!\n")
	b := wrapCisco("interface GigabitEthernet0/1\n!\ninterface TwoGigabitEthernet1/0/1\n description x\n!\ninterface TwentyFiveGigE1/0/1\n description x\n!\ninterface HundredGigE1/0/1\n description x\n!\ninterface AppGigabitEthernet1/0/1\n description x\n!\ninterface GigaEthernet0.0\n ip address 192.0.2.1/24\n!\ninterface gigabitethernet 0/5\n  description y\n!\ninterface Vlan20\n description z\n!\n")
	got := cmdTexts(GenerateCommands(a, b, "rollback", "cisco-ios"))
	h2lacks(t, got, "no interface TwoGigabitEthernet", "no interface TwentyFiveGigE", "no interface HundredGigE", "no interface AppGigabitEthernet", "no interface GigaEthernet", "no interface gigabitethernet")
	h2has(t, got, "default interface TwoGigabitEthernet1/0/1", "default interface GigaEthernet0.0", "default interface gigabitethernet 0/5", "no interface Vlan20")
	g := GenerateCommands(a, b, "rollback", "alaxala-ax")
	if g.Manual == 0 || strings.Contains(joinCmds(g), "default interface") {
		t.Fatalf("AlaxalA has no default interface:\n%s", joinCmds(g))
	}
	for _, s := range []string{"interface GigabitEthernet1/0/1", "interface Ethernet1/1", "interface FastEthernet0/1", "interface Serial0/0/0", "interface mgmt0", "interface GE0/0/1", "interface Ten-GigabitEthernet1/0/49"} {
		if !physIface.MatchString(s) {
			t.Fatalf("%q not physical", s)
		}
	}
	for _, s := range []string{"interface Vlan10", "interface Loopback0", "interface Port-channel1", "interface Tunnel0", "interface Eth-Trunk1", "interface Vlanif10"} {
		if physIface.MatchString(s) {
			t.Fatalf("%q taken as physical", s)
		}
	}
}

// B-6: two stack members' ports are two ports, not one renamed.
func TestH2StackMembersNotRenamed(t *testing.T) {
	a := wrapCisco("interface GigabitEthernet1/0/1\n description uplink\n!\n")
	b := wrapCisco("interface GigabitEthernet2/0/1\n description uplink\n!\n")
	r := DiffWith(a, b, DiffOpts{IgnoreNoise: true, ConfigOnly: true})
	if r.Changed != 0 || r.Added == 0 || r.Removed == 0 {
		t.Fatalf("paired as a rename: ~%d +%d -%d", r.Changed, r.Added, r.Removed)
	}
	got := cmdTexts(GenerateCommands(a, b, "rollback", "cisco-ios"))
	h2lacks(t, got, "no interface GigabitEthernet2/0/1")
	h2has(t, got, "default interface GigabitEthernet2/0/1", "interface GigabitEthernet1/0/1", "description uplink")
	// A stack number added in front still pairs.
	if !headPairOK("interface GigabitEthernet0/1", "interface GigabitEthernet1/0/1") || headPairOK("interface GigabitEthernet1/0/1", "interface GigabitEthernet2/0/1") {
		t.Fatalf("headPairOK")
	}
}

// B-5 / A-7: a Comware / VRP listing ("#" separators, sysname, undo) is
// its own family: undo as the negation, system-view … return, quit.
func TestH2ComwareFamily(t *testing.T) {
	cw := "<lab-sw1>display current-configuration\n#\n version 7.1\n#\nsysname lab-sw1\n#\nntp-service unicast-server 192.0.2.10\n#\ninterface GigabitEthernet1/0/1\n port link-mode bridge\n description uplink\n shutdown\n#\nvlan 10\n#\nreturn\n<lab-sw1>\n"
	cw2 := strings.NewReplacer(" shutdown\n", "", "192.0.2.10", "192.0.2.11", "vlan 10\n", "vlan 10\n#\nvlan 20\n", "description uplink", "description downlink").Replace(cw)
	g := GenerateCommands(cw, cw2, "rollback", "")
	if g.Family != "comware" || g.FamilyBy != "content" {
		t.Fatalf("family %s by %s", g.Family, g.FamilyBy)
	}
	got := cmdTexts(g)
	h2has(t, got, "system-view", "undo ntp-service unicast-server 192.0.2.11", "ntp-service unicast-server 192.0.2.10", "interface GigabitEthernet1/0/1", "shutdown", "description uplink", "quit", "undo vlan 20", "return")
	h2lacks(t, got, "configure terminal", "no ", "undo description", "end")
	if f, _ := detectFamily(nil, "hpe-comware"); f != "comware" {
		t.Fatalf("osType hpe-comware → %s", f)
	}
	// A listing with "interface" lines but nothing Cisco-like is generic,
	// not Cisco.
	if f, _ := detectFamily([]string{"interface ethernet 1/1", "foo bar", "baz qux"}, ""); f != "generic" {
		t.Fatalf("interface alone → %s", f)
	}
	if f, _ := detectFamily([]string{"interface Ethernet1/1", "  switchport", "  ip address 192.0.2.1/24", "hostname lab"}, ""); f != "cisco" {
		t.Fatalf("NX-OS style → %s", f)
	}
}

// B-2 / 型5 / A-7: impact rules read Junos, Yamaha and Comware lines;
// "network-qos" is not routing; "undo" counts as "no".
func TestH2ImpactVendors(t *testing.T) {
	cases := map[string]string{
		"set routing-options static route 0.0.0.0/0 next-hop 192.0.2.1":                 "high",
		"set interfaces ge-0/0/0 unit 0 family inet address 192.0.2.1/30":               "high",
		"set security policies from-zone a to-zone b policy p match source-address any": "high",
		"set interfaces ge-0/0/0 unit 0 description lab":                                "low",
		"set system host-name lab":                                                      "mid",
		"ip route-static 192.0.2.0 24 192.0.2.1":                                        "high",
		"ip lan1 address 192.0.2.1/24":                                                  "high",
		"ip filter 100 reject * * * * *":                                                "high",
		"ip lan1 secure filter in 100":                                                  "high",
		"undo shutdown":                                                                 "high",
		"undo ip route-static 192.0.2.0 24 192.0.2.1":                                   "high",
		"network-qos lab":                                                               "mid",
		"network 192.0.2.0 0.0.0.0 area 0":                                            "high",
		"no shutdown":                                                                   "high",
	}
	for line, want := range cases {
		if lv, why := impactLevel(line, ""); lv != want {
			t.Errorf("%q → %s (%s), want %s", line, lv, why, want)
		}
	}
	// A numbered object is referenced only after its keyword.
	a := wrapCisco("vlan 10\n name lab\n!\ninterface GigabitEthernet0/1\n switchport access vlan 10\n!\ninterface Vlan10\n ip address 192.0.2.1 255.255.255.0\n!\nline vty 0 4\n exec-timeout 10 0\n!\n")
	b := strings.Replace(a, " name lab\n", " name lab2\n", 1)
	r := DiffWith(a, b, DiffOpts{IgnoreNoise: true, ConfigOnly: true})
	if r.Impact == nil {
		t.Fatalf("no impact")
	}
	for _, ref := range r.Impact.Refs {
		if ref.Name != "10" {
			continue
		}
		for _, row := range ref.Rows {
			txt := rowText(r.Lines[row])
			if strings.Contains(txt, "exec-timeout") {
				t.Fatalf("exec-timeout 10 0 listed as a reference to vlan 10")
			}
		}
		if len(ref.Rows) < 2 {
			t.Fatalf("vlan 10 references (access vlan 10, interface Vlan10) missing: %+v", ref)
		}
	}
}

// Moved lines count as a change for the generator, but a block whose only
// change is a moved line (order does not matter there) gives no empty
// "interface … / exit".
func TestH2MovedCountsAsChange(t *testing.T) {
	a := wrapCisco("interface GigabitEthernet0/1\n description uplink\n speed 1000\n!\n")
	b := wrapCisco("interface GigabitEthernet0/1\n speed 1000\n description uplink\n!\n")
	g := GenerateCommands(a, b, "rollback", "cisco-ios")
	if !g.Empty {
		t.Fatalf("a reordered interface body needs no commands:\n%s", joinCmds(g))
	}
}
