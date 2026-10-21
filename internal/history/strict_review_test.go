package history

import (
	"strings"
	"testing"
)

// Findings of the 2026-10-20 code review of v1.7.11–v1.7.12, each as it
// was reported (lab values only).

func lines(l ...string) string { return strings.Join(l, "\n") }

// 1. Lines typed under "interface range" are judged in each member and
// explain the changes there.
func TestReviewInterfaceRange(t *testing.T) {
	work := lines(
		"sw1(config)#interface range gi0/1-2",
		"sw1(config-if-range)#description new",
		"sw1(config-if-range)#end",
	)
	before := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description old", "interface GigabitEthernet0/2", " description old")
	after := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description new", "interface GigabitEthernet0/2", " description new")
	got, r := statuses(t, work, before, after)
	expect(t, got, map[string]string{
		" > interface range gi0/1-2":                "unverified/notStored",
		"interface range gi0/1-2 > description new": "reflected",
	})
	if r.UnexpectedCount != 0 {
		t.Errorf("unexpected: %+v", r.Unexpected)
	}
	// Missing on one member: missing.
	after2 := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description new", "interface GigabitEthernet0/2", " description old")
	got, _ = statuses(t, work, before, after2)
	if got["interface range gi0/1-2 > description new"] != "missing" {
		t.Errorf("one member without it: %v", got)
	}
	// AlaxalA's spaced names.
	m, ok := rangeMembers("interface range gigabitethernet 0/1-3")
	if !ok || strings.Join(m, "|") != "interface gigabitethernet 0/1|interface gigabitethernet 0/2|interface gigabitethernet 0/3" {
		t.Errorf("spaced range: %q", m)
	}
	m, ok = rangeMembers("interface range gi0/1 - 2, po1")
	if !ok || strings.Join(m, "|") != "interface gi0/1|interface gi0/2|interface po1" {
		t.Errorf("list range: %q", m)
	}
}

// 2. "no interface X" removes the whole block: its lines are not changes
// outside the work.
func TestReviewNoInterface(t *testing.T) {
	work := lines("sw1(config)#no interface Loopback1", "sw1(config)#end")
	before := lines("sw1#show running-config", "hostname sw1", "interface Loopback1", " ip address 192.0.2.1 255.255.255.255")
	after := lines("sw1#show running-config", "hostname sw1")
	got, r := statuses(t, work, before, after)
	expect(t, got, map[string]string{" > no interface Loopback1": "reflected"})
	if r.UnexpectedCount != 0 {
		t.Errorf("unexpected: %+v", r.Unexpected)
	}
}

// 3. "default interface X" cannot be checked; it is not a missing removal.
func TestReviewDefaultInterface(t *testing.T) {
	cfg := lines("sw1#show running-config", "interface GigabitEthernet0/3", " description x")
	got, r := statuses(t, "sw1(config)#default interface gi0/3\nsw1(config)#end\n", cfg, cfg)
	expect(t, got, map[string]string{" > default interface gi0/3": "unverified/notStored"})
	if r.Missing != 0 {
		t.Errorf("missing %d", r.Missing)
	}
}

// 4. Coming back out of a nested mode keeps the outer block.
func TestReviewNestedModeExit(t *testing.T) {
	work := lines(
		"r1(config)#router bgp 65000",
		"r1(config-router)#address-family ipv4",
		"r1(config-router-af)#network 192.0.2.0 mask 255.255.255.0",
		"r1(config-router-af)#exit-address-family",
		"r1(config-router)#neighbor 192.0.2.9 remote-as 65001",
		"r1(config-router)#exit",
		"r1(config)#policy-map PM1",
		"r1(config-pmap)#class C1",
		"r1(config-pmap-c)#set dscp ef",
		"r1(config-pmap-c)#exit",
		"r1(config-pmap)#class C2",
		"r1(config-pmap-c)#end",
	)
	cmds, _ := extractTyped(work)
	par := map[string]string{}
	for _, c := range cmds {
		par[c.line] = c.parent
	}
	for line, want := range map[string]string{
		"neighbor 192.0.2.9 remote-as 65001":   "router bgp 65000",
		"network 192.0.2.0 mask 255.255.255.0": "router bgp 65000",
		"class C2":                             "policy-map PM1",
		"set dscp ef":                          "policy-map PM1",
	} {
		if par[line] != want {
			t.Errorf("%q: parent %q, want %q", line, par[line], want)
		}
	}
}

// 5. Sessions are read one by one: the last line before a reconnect keeps
// its block.
func TestReviewSessions(t *testing.T) {
	s1 := lines("r1#conf t", "r1(config)#interface gi0/1", "r1(config-if)#shutdown")
	s2 := lines("r1>enable", "r1#conf t", "r1(config)#ntp server 192.0.2.10", "r1(config)#end")
	before := lines("r1#show running-config", "interface GigabitEthernet0/1", "interface GigabitEthernet0/2", " shutdown")
	after := lines("r1#show running-config", "ntp server 192.0.2.10", "interface GigabitEthernet0/1", " shutdown", "interface GigabitEthernet0/2", " shutdown")
	r := StrictVerifyLogs([]string{s1, s2}, before, after)
	got := map[string]string{}
	for _, c := range r.Commands {
		got[c.Parent+" > "+c.Line] = c.Status
	}
	if got["interface gi0/1 > shutdown"] != "reflected" || got[" > ntp server 192.0.2.10"] != "reflected" || r.UnexpectedCount != 0 {
		t.Errorf("sessions: %v %+v", got, r.Unexpected)
	}
}

// 8. Configuration lines are not cut by the clock / counter noise.
func TestReviewNoiseWordsInConfig(t *testing.T) {
	work := lines("sw1(config)#interface gi0/1", "sw1(config-if)#description used for backup", "sw1(config-if)#end")
	before := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description free port", "interface GigabitEthernet0/2", " description used by lab")
	after := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description used for backup", "interface GigabitEthernet0/2", " description used by lab-2")
	r := StrictVerify(work, before, after)
	if r.Missing != 0 {
		t.Errorf("missing: %+v", r.Commands)
	}
	// Gi0/2's change was not typed: it is reported, not hidden by the
	// noise collapsing both descriptions to "description".
	if r.UnexpectedCount != 2 || !strings.Contains(r.Unexpected[1].Line, "used by lab-2") {
		t.Errorf("unexpected: %d %+v", r.UnexpectedCount, r.Unexpected)
	}
}

// 9. A log listing the configuration twice does not count every line
// twice.
func TestReviewSeveralListings(t *testing.T) {
	work := lines("sw1(config)#ntp server 192.0.2.10", "sw1(config)#end")
	cfg := lines("hostname sw1", "interface GigabitEthernet0/1", " description up")
	before := lines("sw1#show running-config", cfg, "sw1#show startup-config", cfg, "sw1#show running-config | include hostname", "hostname sw1")
	after := lines("sw1#show running-config", cfg, "ntp server 192.0.2.10")
	r := StrictVerify(work, before, after)
	if r.Missing != 0 || r.UnexpectedCount != 0 {
		t.Errorf("missing %d, unexpected %+v", r.Missing, r.Unexpected)
	}
}

// 10. A VLAN list typed where the block is not known is judged per block,
// not as the union of every interface's VLANs.
func TestReviewVlanPerBlock(t *testing.T) {
	work := "sw1# switchport trunk allowed vlan 10-11\n"
	cfg := lines("sw1#show running-config", "interface GigabitEthernet0/1", " switchport trunk allowed vlan 10,11", "interface GigabitEthernet0/2", " switchport trunk allowed vlan 20")
	got, _ := statuses(t, work, "", cfg)
	expect(t, got, map[string]string{" > switchport trunk allowed vlan 10-11": "reflected/vlan"})
}

// Smaller ones: "comm" is not a free-text keyword; "enable 15" is not a
// setting.
func TestReviewSmall(t *testing.T) {
	if isFreeKeyword("comm") || !isFreeKeyword("desc") || !isFreeKeyword("description") {
		t.Errorf("isFreeKeyword")
	}
	if cmds, _ := ExtractConfigCommands("r1>enable 15\nr1#show run\n"); len(cmds) != 0 {
		t.Errorf("enable 15: %q", cmds)
	}
}

// Second review (same day), each as reported (lab values only).

// 1/2. Moving between "interface range" and a single interface opens the
// next block either way.
func TestReReviewRangeAndSingle(t *testing.T) {
	before := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description old", "interface GigabitEthernet0/2", " description old", "interface GigabitEthernet0/5")
	after := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description new", "interface GigabitEthernet0/2", " description new", "interface GigabitEthernet0/5", " description five")
	for name, work := range map[string]string{
		"range then single": lines("sw1(config)#interface range gi0/1-2", "sw1(config-if-range)#description new", "sw1(config-if-range)#interface gi0/5", "sw1(config-if)#description five", "sw1(config-if)#end"),
		"single then range": lines("sw1(config)#interface gi0/5", "sw1(config-if)#description five", "sw1(config-if)#interface range gi0/1-2", "sw1(config-if-range)#description new", "sw1(config-if-range)#end"),
	} {
		got, r := statuses(t, work, before, after)
		if got["interface gi0/5 > description five"] != "reflected" || got["interface range gi0/1-2 > description new"] != "reflected" || r.Missing != 0 || r.UnexpectedCount != 0 {
			t.Errorf("%s: %v %+v", name, got, r.Unexpected)
		}
	}
}

// 3. Whether the gear has (config) prompts is known from every session.
func TestReReviewExecInLaterSession(t *testing.T) {
	s1 := lines("r1#conf t", "r1(config)#ntp server 192.0.2.10", "r1(config)#end")
	s2 := lines("r1#delete flash:old.bin", "r1#install activate")
	cmds, _ := extractTypedLogs([]string{s1, s2}, nil)
	if len(cmds) != 1 || cmds[0].line != "ntp server 192.0.2.10" {
		t.Errorf("exec commands taken for settings: %+v", cmds)
	}
}

// 4. FortiGate (no block from the prompts): replacing port1's value does
// not hide a change made to port2.
func TestReReviewFortiOtherPort(t *testing.T) {
	work := lines("FG # config system interface", "FG (interface) # edit port1", "FG (port1) # set description new", "FG (port1) # next", "FG (interface) # end")
	listing := func(p1, p2 string) string {
		return lines("FG # show", "config system interface", "    edit \"port1\"", "        set description \""+p1+"\"", "    next",
			"    edit \"port2\"", "        set description \""+p2+"\"", "    next", "end", "FG # ")
	}
	r := StrictVerify(work, listing("old", "p2"), listing("new", "p2-changed"))
	var got []string
	for _, u := range r.Unexpected {
		got = append(got, u.Kind+" "+u.Line)
	}
	if r.Missing != 0 || strings.Join(got, "|") != `- set description "p2"|+ set description "p2-changed"` {
		t.Errorf("missing %d, unexpected %q", r.Missing, got)
	}
}

// 5. "default interface X": the lines it reset are accounted for.
func TestReReviewDefaultInterfaceReset(t *testing.T) {
	before := lines("sw1#show running-config", "interface GigabitEthernet0/3", " description x", " switchport mode access")
	after := lines("sw1#show running-config", "interface GigabitEthernet0/3")
	got, r := statuses(t, "sw1(config)#default interface gi0/3\nsw1(config)#end\n", before, after)
	if got[" > default interface gi0/3"] != "unverified/notStored" || r.UnexpectedCount != 0 {
		t.Errorf("%v %+v", got, r.Unexpected)
	}
}

// 6. Junos: "| display set" is the whole configuration, "show
// configuration interfaces" a part.
func TestReReviewJunosListings(t *testing.T) {
	if !fullListing("show configuration | display set") || fullListing("show configuration interfaces") || fullListing("show run | include ntp") || !fullListing("show running-config all") {
		t.Fatal("fullListing")
	}
	set := lines("set system host-name srv1", "set interfaces ge-0/0/0 description uplink")
	before := lines("ops@srv1> show configuration | display set", set, "ops@srv1> show configuration interfaces", "ge-0/0/0 {", "    description uplink;", "}")
	after := lines("ops@srv1> show configuration | display set", set, "set system ntp server 192.0.2.10")
	r := StrictVerify("ops@srv1# set system ntp server 192.0.2.10\nops@srv1# commit\n", before, after)
	if r.Missing != 0 || r.UnexpectedCount != 0 {
		t.Errorf("missing %d, unexpected %+v", r.Missing, r.Unexpected)
	}
}

// 7. A line typed under a range and undone later on one member is
// superseded.
func TestReReviewRangeUndoneOnMember(t *testing.T) {
	work := lines("sw1(config)#interface range gi0/1-2", "sw1(config-if-range)#shutdown", "sw1(config-if-range)#interface gi0/1", "sw1(config-if)#no shutdown", "sw1(config-if)#end")
	before := lines("sw1#show running-config", "interface GigabitEthernet0/1", "interface GigabitEthernet0/2")
	after := lines("sw1#show running-config", "interface GigabitEthernet0/1", "interface GigabitEthernet0/2", " shutdown")
	got, r := statuses(t, work, before, after)
	if got["interface range gi0/1-2 > shutdown"] != "superseded" || r.Missing != 0 {
		t.Errorf("%v", got)
	}
}

// 8/9. "cont" is contact (free text) again; a range across slots is not
// spelled out wrongly.
func TestReReviewSmall(t *testing.T) {
	if !isFreeKeyword("cont") || isFreeKeyword("comm") {
		t.Errorf("isFreeKeyword")
	}
	if m, ok := rangeMembers("interface range gi0/1 - gi1/4"); ok {
		t.Errorf("across slots: %q", m)
	}
	if m, ok := rangeMembers("interface range gi0/1 - gi0/3"); !ok || len(m) != 3 {
		t.Errorf("same slot: %q", m)
	}
}

// Third review (same day), each as reported (lab values only).

func unexpectedLines(r StrictResult) string {
	var got []string
	for _, u := range r.Unexpected {
		got = append(got, u.Kind+" "+u.Line)
	}
	return strings.Join(got, "|")
}

// 1/4. A header of another kind ("interface vlan10" from config-if), and
// an abbreviated range ("int ra …"), open their own blocks.
func TestReReview3Headers(t *testing.T) {
	work := lines("sw1(config)#interface gi0/1", "sw1(config-if)#description a", "sw1(config-if)#interface vlan10",
		"sw1(config-if)#ip address 192.0.2.1 255.255.255.0", "sw1(config-if)#int ra gi0/2-3", "sw1(config-if-range)#shutdown", "sw1(config-if-range)#end")
	before := lines("sw1#show running-config", "interface GigabitEthernet0/1", "interface GigabitEthernet0/2", "interface GigabitEthernet0/3", "interface Vlan10")
	after := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description a", "interface GigabitEthernet0/2", " shutdown",
		"interface GigabitEthernet0/3", " shutdown", "interface Vlan10", " ip address 192.0.2.1 255.255.255.0")
	got, r := statuses(t, work, before, after)
	if got["interface vlan10 > ip address 192.0.2.1 255.255.255.0"] != "reflected" || got["int ra gi0/2-3 > shutdown"] != "reflected" || r.Missing != 0 || r.UnexpectedCount != 0 {
		t.Errorf("%v %s", got, unexpectedLines(r))
	}
}

// 2. A "vlan remove" that did not take effect is missing.
func TestReReview3VlanRemove(t *testing.T) {
	work := lines("sw1(config)#interface gi0/1", "sw1(config-if)#switchport trunk allowed vlan remove 30", "sw1(config-if)#end")
	cfg := lines("sw1#show running-config", "interface GigabitEthernet0/1", " switchport trunk allowed vlan 10,20,30", "interface GigabitEthernet0/2", " switchport trunk allowed vlan 10,20")
	got, _ := statuses(t, work, cfg, cfg)
	if got["interface gi0/1 > switchport trunk allowed vlan remove 30"] != "missing" {
		t.Errorf("%v", got)
	}
	// Taken out: reflected.
	after := lines("sw1#show running-config", "interface GigabitEthernet0/1", " switchport trunk allowed vlan 10,20", "interface GigabitEthernet0/2", " switchport trunk allowed vlan 10,20")
	got, _ = statuses(t, work, cfg, after)
	if got["interface gi0/1 > switchport trunk allowed vlan remove 30"] != "reflected/vlan" {
		t.Errorf("%v", got)
	}
}

// 3. A later change on one member does not hide the others' missing line.
func TestReReview3RangeOneMember(t *testing.T) {
	work := lines("sw1(config)#interface range gi0/1-3", "sw1(config-if-range)#shutdown", "sw1(config-if-range)#interface gi0/1", "sw1(config-if)#no shutdown", "sw1(config-if)#end")
	before := lines("sw1#show running-config", "interface GigabitEthernet0/1", "interface GigabitEthernet0/2", "interface GigabitEthernet0/3")
	after := lines("sw1#show running-config", "interface GigabitEthernet0/1", "interface GigabitEthernet0/2", " shutdown", "interface GigabitEthernet0/3")
	got, r := statuses(t, work, before, after)
	if got["interface range gi0/1-3 > shutdown"] != "missing" || r.Missing != 1 {
		t.Errorf("Gi0/3 lacks it: %v", got)
	}
}

// 5/6. The verdict and the changes outside the work read the same listing;
// "show config" (NEC IX) is the running one.
func TestReReview3OneListing(t *testing.T) {
	work := lines("sw1(config)#no logging host 192.0.2.5", "sw1(config)#end")
	before := lines("sw1#show running-config", "hostname sw1", "logging host 192.0.2.5")
	after := lines("sw1#show running-config", "hostname sw1", "sw1#show startup-config", "hostname sw1", "logging host 192.0.2.5")
	got, r := statuses(t, work, before, after)
	if got[" > no logging host 192.0.2.5"] != "reflected" || r.UnexpectedCount != 0 {
		t.Errorf("%v %s", got, unexpectedLines(r))
	}
	ix := lines("IX(config)# ip route default 192.0.2.1", "IX(config)# exit")
	ixBefore := lines("IX# show config", "hostname IX")
	ixAfter := lines("IX# show config", "hostname IX", "ip route default 192.0.2.1", "IX# show startup-config", "hostname IX")
	got, r = statuses(t, ix, ixBefore, ixAfter)
	if got[" > ip route default 192.0.2.1"] != "reflected" || r.UnexpectedCount != 0 {
		t.Errorf("IX: %v %s", got, unexpectedLines(r))
	}
}

// 7. The same value put in another block by someone else is reported.
func TestReReview3SameValueElsewhere(t *testing.T) {
	work := lines("sw1(config)#interface gi0/1", "sw1(config-if)#description uplink", "sw1(config-if)#end")
	before := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description down", "interface GigabitEthernet0/2", " description old")
	after := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description uplink", "interface GigabitEthernet0/2", " description uplink")
	r := StrictVerify(work, before, after)
	if got := unexpectedLines(r); got != "- description old|+ description uplink" {
		t.Errorf("unexpected: %q", got)
	}
}

// 8. After "default interface", a line someone added there is reported.
func TestReReview3AddedAfterDefault(t *testing.T) {
	before := lines("sw1#show running-config", "interface GigabitEthernet0/1", " description x")
	after := lines("sw1#show running-config", "interface GigabitEthernet0/1", " switchport access vlan 99")
	r := StrictVerify("sw1(config)#default interface gi0/1\nsw1(config)#end\n", before, after)
	if got := unexpectedLines(r); got != "+ switchport access vlan 99" {
		t.Errorf("unexpected: %q", got)
	}
}

// Fourth round (regressions found by the third review, and the rest).

// 1. Parts only ("show run interface …" twice): both are read.
func TestReReview4PartialListings(t *testing.T) {
	work := lines("sw1(config)#interface Gi0/1", "sw1(config-if)#description new1", "sw1(config-if)#interface Gi0/2", "sw1(config-if)#description new2", "sw1(config-if)#end")
	part := func(d1, d2 string) string {
		return lines("sw1#show running-config interface Gi0/1", "interface GigabitEthernet0/1", " description "+d1,
			"sw1#show running-config interface Gi0/2", "interface GigabitEthernet0/2", " description "+d2)
	}
	got, r := statuses(t, work, part("a", "b"), part("new1", "new2"))
	if got["interface Gi0/1 > description new1"] != "reflected" || got["interface Gi0/2 > description new2"] != "reflected" || r.UnexpectedCount != 0 {
		t.Errorf("%v %s", got, unexpectedLines(r))
	}
}

// 2/3. Undone in a block the after log does not list: superseded, as in
// v1.7.12 — for one interface and for a range.
func TestReReview4UndoneInUnlistedBlock(t *testing.T) {
	cfg := lines("sw1#show running-config", "hostname sw1")
	for name, work := range map[string]string{
		"interface": lines("sw1(config)#interface Loopback9", "sw1(config-if)#shutdown", "sw1(config-if)#no shutdown", "sw1(config-if)#end"),
		"range":     lines("sw1(config)#interface range gi0/1 - 2", "sw1(config-if-range)#shutdown", "sw1(config-if-range)#no shutdown", "sw1(config-if-range)#end"),
	} {
		r := StrictVerify(work, cfg, cfg)
		for _, c := range r.Commands {
			if c.Line == "shutdown" && c.Status != "superseded" {
				t.Errorf("%s: shutdown is %s", name, c.Status)
			}
		}
	}
}

// 5. FortiGate's "show" inside a config context lists that section only.
func TestReReview4FortiContextShow(t *testing.T) {
	full := lines("config system global", "    set hostname FG1", "end", "config system interface", "    edit \"port1\"", "        set description \"a\"", "    next", "end")
	after := lines("FG # show full-configuration", full, "FG # config system interface", "FG (interface) # show", "config system interface", "    edit \"port1\"", "        set description \"a\"", "    next", "end")
	sec, _ := configSection(after)
	if !strings.Contains(strings.Join(sec, "\n"), "set hostname FG1") {
		t.Errorf("the section-only show was picked: %q", sec)
	}
}

// 6. A one-line global setting typed in a sub-mode is not a block head.
func TestReReview4OneLineGlobalNotHead(t *testing.T) {
	work := lines("sw1(config)#interface gi0/1", "sw1(config-if)#feature lacp", "sw1(config-if)#description x", "sw1(config-if)#end")
	cfg := lines("sw1#show running-config", "feature lacp", "interface GigabitEthernet0/1", " description x")
	got, _ := statuses(t, work, cfg, cfg)
	if _, ok := got["interface gi0/1 > description x"]; !ok {
		t.Errorf("description x lost its block: %v", got)
	}
}

// Final review of the fourth round.

// 1/2. A part run twice counts once; a part's lines are not filed under the
// block of the part before.
func TestFinalParts(t *testing.T) {
	part1 := lines("sw1#show run int gi0/1", "interface GigabitEthernet0/1", " switchport mode access")
	work := lines("sw1(config)#interface gi0/2", "sw1(config-if)#description new2", "sw1(config-if)#end")
	before := lines(part1, "sw1#show run | include description", " description old2")
	after := lines(part1, part1, "sw1#show run | include description", " description new2")
	r := StrictVerify(work, before, after)
	if r.UnexpectedCount != 0 {
		t.Errorf("unexpected: %s", unexpectedLines(r))
	}
	sec, _ := configSection(after)
	n := 0
	for _, l := range sec {
		if l == " switchport mode access" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("a part run twice is read %d times", n)
	}
}

// 3. An empty interface (an unused port) still opens its block.
func TestFinalEmptyInterfaceHead(t *testing.T) {
	work := lines("sw1(config)#interface Vlan10", "sw1(config-if)#description v", "sw1(config-if)#interface gi1/0/3", "sw1(config-if)#shutdown", "sw1(config-if)#end")
	before := lines("sw1#show running-config", "interface Vlan10", "interface GigabitEthernet1/0/3")
	after := lines("sw1#show running-config", "interface Vlan10", " description v", "interface GigabitEthernet1/0/3", " shutdown")
	got, r := statuses(t, work, before, after)
	if got["interface gi1/0/3 > shutdown"] != "reflected" || r.Missing != 0 {
		t.Errorf("%v", got)
	}
}

// 4. Junos's show inside [edit …] is a part; HP's show running-config in
// a context is the whole configuration.
func TestFinalContextShows(t *testing.T) {
	junos := lines("ops@srv1> show configuration", "system {", "    host-name srv1;", "}", "ops@srv1> configure", "[edit interfaces ge-0/0/0]", "ops@srv1# show", "description up;")
	if sec, _ := configSection(junos); !strings.Contains(strings.Join(sec, "\n"), "host-name srv1") {
		t.Errorf("junos subtree picked: %q", sec)
	}
	hp := lines("HP(eth-1)# show running-config", "hostname HP", "interface 1", "   name up", "   exit")
	if sec, kind := configSection(hp); kind != oneListing || !strings.Contains(strings.Join(sec, "\n"), "hostname HP") {
		t.Errorf("hp: %q", sec)
	}
}

// 6. One block typed two ways, neither log listing it.
func TestFinalBlockTypedTwoWays(t *testing.T) {
	work := lines("sw1(config)#int lo9", "sw1(config-if)#shutdown", "sw1(config-if)#exit", "sw1(config)#interface Loopback9", "sw1(config-if)#no shutdown", "sw1(config-if)#end")
	cfg := lines("sw1#show running-config", "hostname sw1")
	r := StrictVerify(work, cfg, cfg)
	for _, c := range r.Commands {
		if c.Line == "shutdown" && c.Status != "superseded" {
			t.Errorf("shutdown is %s", c.Status)
		}
	}
}
