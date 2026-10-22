package history

import (
	"strings"
	"testing"
)

// A port bounce and a corrected value: the earlier line is superseded, not
// missing. A list setting (ntp server) is not replaced by the next one.
func TestStrictVerifySuperseded(t *testing.T) {
	work := strings.Join([]string{
		"sw1(config)#interface Gi0/1",
		"sw1(config-if)#shutdown",
		"sw1(config-if)#no shutdown",
		"sw1(config-if)#ip address 192.0.2.1 255.255.255.0",
		"sw1(config-if)#ip address 192.0.2.2 255.255.255.0",
		"sw1(config-if)#desc old text",
		"sw1(config-if)#desc uplink",
		"sw1(config-if)#exit",
		"sw1(config)#ntp server 192.0.2.10",
		"sw1(config)#ntp server 192.0.2.11",
		"sw1(config)#interface Gi0/2",
		"sw1(config-if)#no shutdown",
		"sw1(config-if)#shutdown",
		"sw1(config-if)#no shutdown", // typed again: the last one counts
		"sw1(config-if)#end",
	}, "\n")
	before := "sw1#show run\ninterface GigabitEthernet0/1\n no ip address\n shutdown\ninterface GigabitEthernet0/2\n shutdown\n"
	after := "sw1#show run\nntp server 192.0.2.11\ninterface GigabitEthernet0/1\n description uplink\n ip address 192.0.2.2 255.255.255.0\ninterface GigabitEthernet0/2\n"
	got, r := statuses(t, work, before, after)
	g1, g2 := "interface Gi0/1 > ", "interface Gi0/2 > "
	expect(t, got, map[string]string{
		" > interface Gi0/1":                      "already/abbrev",
		g1 + "shutdown":                           "superseded",
		g1 + "no shutdown":                        "reflected",
		g1 + "ip address 192.0.2.1 255.255.255.0": "superseded",
		g1 + "ip address 192.0.2.2 255.255.255.0": "reflected",
		g1 + "desc old text":                      "superseded",
		g1 + "desc uplink":                        "reflected/abbrev",
		" > ntp server 192.0.2.10":                "missing", // a list: still missing
		" > ntp server 192.0.2.11":                "reflected",
		" > interface Gi0/2":                      "already/abbrev",
		g2 + "no shutdown":                        "reflected",
		g2 + "shutdown":                           "superseded",
	})
	if r.Missing != 1 {
		t.Errorf("missing %d", r.Missing)
	}
}

// Configuration stamps that change by themselves are not differences.
func TestDiffIgnoresConfigStamps(t *testing.T) {
	a := "sw1#show run\nBuilding configuration...\n\nCurrent configuration : 4321 bytes\n!\n! Last configuration change at 09:01:02 JST Tue Oct 20 2026 by ops\n! NVRAM config last updated at 09:01:05 JST Tue Oct 20 2026 by ops\n!\nversion 15.2\nntp clock-period 36028797\nhostname sw1\n#config-version=FGT60F-7.2.5:opmode=0:vdom=0\n#conf_file_ver=1234\n## Last commit: 2026-10-20 09:00:00 JST by ops\n"
	b := "sw1#show run\nBuilding configuration...\n\nCurrent configuration : 4325 bytes\n!\n! Last configuration change at 11:12:13 JST Tue Oct 20 2026 by ops\n! NVRAM config last updated at 11:12:20 JST Tue Oct 20 2026 by ops\n!\nversion 15.2\nntp clock-period 36028801\nhostname sw1\n#config-version=FGT60F-7.2.5:opmode=0:vdom=0\n#conf_file_ver=1240\n## Last commit: 2026-10-20 11:00:00 JST by ops\n"
	if d := Diff(a, b, true); d.Added != 0 || d.Removed != 0 {
		t.Errorf("stamps only: +%d -%d", d.Added, d.Removed)
	}
	if d := Diff(a, "sw1#show run\nhostname sw2\n", true); d.Added == 0 {
		t.Errorf("a real change must still show")
	}
}

// Changes the work does not account for are listed.
func TestStrictVerifyUnexpected(t *testing.T) {
	work := strings.Join([]string{
		"sw1(config)#int gi0/1",
		"sw1(config-if)#sw acc vl 20",
		"sw1(config-if)#no shut",
		"sw1(config-if)#exit",
		"sw1(config)#interface Vlan20",
		"sw1(config-if)#ip address 192.0.2.1 255.255.255.0",
		"sw1(config-if)#end",
	}, "\n")
	before := strings.Join([]string{
		"sw1#show running-config",
		"Current configuration : 1000 bytes",
		"hostname sw1",
		"interface GigabitEthernet0/1",
		" switchport access vlan 10",
		" shutdown",
		"interface GigabitEthernet0/2",
		" switchport access vlan 10",
		"logging host 192.0.2.50",
		"sw1#show ip int brief",
		"Vlan1 unassigned YES unset up up",
	}, "\n")
	after := strings.Join([]string{
		"sw1#show running-config",
		"Current configuration : 1100 bytes",
		"hostname sw1",
		"interface GigabitEthernet0/1",
		" switchport access vlan 20",
		"interface GigabitEthernet0/2",
		" switchport access vlan 30", // not typed: the wrong port, or someone else
		"interface Vlan20",
		" ip address 192.0.2.1 255.255.255.0",
		" no shutdown", // inside a block the work created
		"sw1#show ip int brief",
		"Vlan1 unassigned YES unset down down", // state, not configuration
	}, "\n")
	r := StrictVerify(work, before, after)
	if r.Missing != 0 {
		t.Errorf("missing %d: %+v", r.Missing, r.Commands)
	}
	var got []string
	for _, u := range r.Unexpected {
		got = append(got, u.Kind+" "+u.Block+" | "+u.Line)
	}
	want := []string{
		"- interface GigabitEthernet0/2 | switchport access vlan 10",
		"-  | logging host 192.0.2.50",
		"+ interface GigabitEthernet0/2 | switchport access vlan 30",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") || r.UnexpectedCount != 3 {
		t.Errorf("unexpected:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// No configuration listing in the before log: nothing is reported.
	r = StrictVerify(work, "sw1#show ip int brief\n", after)
	if r.UnexpectedCount != 0 {
		t.Errorf("no before listing: %+v", r.Unexpected)
	}
}

// User report 2026-10-20 (AlaxalA, values replaced with lab ones): after the
// first change every prompt starts with "!" (configuration not saved), and
// a long line came back with a space where the terminal wrapped it
// ("ac tion"). All five settings had been applied; two were shown as
// changes outside the work and one as missing.
func TestStrictVerifyAlaxalaUnsavedPromptAndWrap(t *testing.T) {
	work := strings.Join([]string{
		"lab-sw01# ",
		"lab-sw01# configure terminal",
		"lab-sw01(config)# ",
		"lab-sw01(config)# flow detection mode layer3-2",
		"lab-sw01(config)# ",
		"lab-sw01(config)# ip qos-flow-list LAB_FLOW_A",
		"lab-sw01(config-ip-qos)# ",
		"lab-sw01(config-ip-qos)#   1 qos udp 192.0.2.0 255.255.255.0 any ac tion replace-dscp af41",
		"!lab-sw01(config-ip-qos)# ",
		"!lab-sw01(config-ip-qos)# !",
		"!lab-sw01(config)# ",
		"!lab-sw01(config)# interface vlan 220",
		"!lab-sw01(config-if)# ",
		"!lab-sw01(config-if)#   ip qos-flow-group LAB_FLOW_A in",
		"!lab-sw01(config-if)# ",
		"!lab-sw01(config-if)# !",
		"!lab-sw01(config)# ",
		"!lab-sw01(config)# save",
		"lab-sw01(config)# ",
		"lab-sw01(config)# exit",
		"lab-sw01# ",
	}, "\r\n")
	cfg := func(stamp string, extra bool) string {
		l := []string{
			"lab-sw01# show running-config",
			"#Last modified by ops at " + stamp + " 2026 with version 9.9",
			"!",
			`hostname "lab-sw01"`,
			"!",
			"flow detection mode layer3-2",
			"!",
			"interface vlan 210",
			"  ip address 192.0.2.32 255.255.255.0",
			"  ip qos-flow-group LAB_FLOW_B in",
			"!",
		}
		if extra {
			l = append(l, "interface vlan 220", "  ip qos-flow-group LAB_FLOW_A in", "!")
		}
		l = append(l, "ip qos-flow-list LAB_FLOW_B", "  1 qos ip any host 192.0.2.32 action replace-dscp af21", "!")
		if extra {
			l = append(l, "ip qos-flow-list LAB_FLOW_A", "  1 qos udp 192.0.2.0 255.255.255.0 any action replace-dscp af41", "!")
		}
		return strings.Join(append(l, "!lab-sw01# "), "\r\n")
	}
	before, after := cfg("Sat Jan 10 09:00:00", false), cfg("Sat Jan 10 10:30:00", true)
	got, r := statuses(t, work, before, after)
	expect(t, got, map[string]string{
		" > flow detection mode layer3-2": "already",
		" > ip qos-flow-list LAB_FLOW_A":  "reflected",
		"ip qos-flow-list LAB_FLOW_A > 1 qos udp 192.0.2.0 255.255.255.0 any ac tion replace-dscp af41": "reflected/spacing",
		" > interface vlan 220":                                "reflected",
		"interface vlan 220 > ip qos-flow-group LAB_FLOW_A in": "reflected",
	})
	if r.Missing != 0 || r.UnexpectedCount != 0 {
		t.Errorf("missing %d, unexpected %+v", r.Missing, r.Unexpected)
	}
	// Only the five added lines differ: the last-modified stamp and the
	// "!" prompts do not count.
	if d := Diff(before, after, true); d.Added != 6 || d.Removed != 0 {
		t.Errorf("diff: +%d -%d", d.Added, d.Removed)
	}
}
