package history

import (
	"strings"
	"testing"
)

// statuses runs StrictVerify and returns each typed line's status (with the
// note after a slash when there is one).
func statuses(t *testing.T, work, before, after string) (map[string]string, StrictResult) {
	t.Helper()
	r := StrictVerify(work, before, after)
	got := map[string]string{}
	for _, c := range r.Commands {
		s := c.Status
		if c.Note != "" {
			s += "/" + c.Note
		}
		got[c.Parent+" > "+c.Line] = s
	}
	return got, r
}

func expect(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%q: %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d commands, want %d: %v", len(got), len(want), got)
	}
}

// User report 2026-10-20: settings that had been applied showed as "not
// reflected". Abbreviations, rewritten values and merged lists are found.
func TestStrictVerifyAbbreviationsAndRewrites(t *testing.T) {
	work := strings.Join([]string{
		"sw1#conf t",
		"sw1(config)#int gi0/1",
		"sw1(config-if)#desc uplink to core",
		"sw1(config-if)#sw mo acc",
		"sw1(config-if)#sw acc vl 10",
		"sw1(config-if)#spanning-tree portfast",
		"sw1(config-if)#no shut",
		"sw1(config-if)#int gi 0/2",
		"sw1(config-if)#sw tr al vl add 30",
		"sw1(config-if)#ip add 192.0.2.1 255.255.255.0",
		"sw1(config-if)#exit",
		"sw1(config)#username ops privilege 15 password Secret123",
		"sw1(config)#vlan 10,30",
		"sw1(config-vlan)#exit",
		"sw1(config)#logging host 192.0.2.50",
		"sw1(config)#end",
		"sw1#wr",
	}, "\n")
	before := strings.Join([]string{
		"sw1#show running-config",
		"hostname sw1",
		"vlan 10",
		"vlan 20",
		"interface GigabitEthernet0/1",
		" shutdown",
		"interface GigabitEthernet0/2",
		" switchport trunk allowed vlan 10,20",
		" switchport mode trunk",
	}, "\n")
	after := strings.Join([]string{
		"sw1#show running-config",
		"hostname sw1",
		"username ops privilege 15 secret 9 $9$abcdefghijkl",
		"vlan 10",
		"vlan 20",
		"vlan 30",
		"interface GigabitEthernet0/1",
		" description uplink to core",
		" switchport access vlan 10",
		" switchport mode access",
		" spanning-tree portfast edge",
		"interface GigabitEthernet0/2",
		" switchport trunk allowed vlan 10,20,30",
		" switchport mode trunk",
		" ip address 192.0.2.1 255.255.255.0",
		"logging host 192.0.2.50",
	}, "\n")
	got, r := statuses(t, work, before, after)
	expect(t, got, map[string]string{
		" > int gi0/1":                                    "already/abbrev",
		"int gi0/1 > desc uplink to core":                 "reflected/abbrev",
		"int gi0/1 > sw mo acc":                           "reflected/abbrev",
		"int gi0/1 > sw acc vl 10":                        "reflected/abbrev",
		"int gi0/1 > spanning-tree portfast":              "reflected/similar",
		"int gi0/1 > no shut":                             "reflected",
		"int gi 0/2 > sw tr al vl add 30":                 "reflected/vlan",
		"int gi 0/2 > ip add 192.0.2.1 255.255.255.0":     "reflected/abbrev",
		" > username ops privilege 15 password Secret123": "reflected/secret",
		" > vlan 10,30":                                   "reflected/vlan",
		" > logging host 192.0.2.50":                      "reflected",
		// "int gi 0/2" was typed at the (config-if) prompt: it opens the next
		// block, and is itself looked up at the top.
		" > int gi 0/2": "already/abbrev",
	})
	if r.Missing != 0 || r.Unverified != 0 {
		t.Errorf("missing %d unverified %d: %+v", r.Missing, r.Unverified, r.Commands)
	}
}

// Loosening must not hide settings that really are not there.
func TestStrictVerifyStillCatchesMissing(t *testing.T) {
	work := strings.Join([]string{
		"sw1(config)#interface GigabitEthernet0/1",
		"sw1(config-if)#description up",           // after has "uplink": free text is exact
		"sw1(config-if)#switchport access vlan 1", // after has vlan 10: numbers are exact
		"sw1(config-if)#no shutdown",              // still shut afterwards
		"sw1(config-if)#ip address 192.0.2.9 255.255.255.0",
		"sw1(config-if)#exit",
		"sw1(config)#snmp-server community labro RO", // not there at all
		"sw1(config)#interface GigabitEthernet0/2",
		"sw1(config-if)#switchport mode access", // only in Gi0/1's block
		"sw1(config-if)#end",
	}, "\n")
	before := "sw1#show run\ninterface GigabitEthernet0/1\n shutdown\ninterface GigabitEthernet0/2\n"
	after := "sw1#show run\ninterface GigabitEthernet0/1\n description uplink\n switchport access vlan 10\n switchport mode access\n shutdown\n ip address 192.0.2.99 255.255.255.0\ninterface GigabitEthernet0/2\n"
	got, r := statuses(t, work, before, after)
	g1 := "interface GigabitEthernet0/1 > "
	expect(t, got, map[string]string{
		" > interface GigabitEthernet0/1":                       "already",
		g1 + "description up":                                   "missing",
		g1 + "switchport access vlan 1":                         "missing",
		g1 + "no shutdown":                                      "missing",
		g1 + "ip address 192.0.2.9 255.255.255.0":               "missing",
		" > snmp-server community labro RO":                     "missing",
		" > interface GigabitEthernet0/2":                       "already",
		"interface GigabitEthernet0/2 > switchport mode access": "unverified/otherBlock",
	})
	if r.Missing != 5 || r.Unverified != 1 {
		t.Errorf("missing %d unverified %d", r.Missing, r.Unverified)
	}
}

// A one-word "not stored" pattern takes only that word: "mac address-table
// static …" is not "macro", and when the after log lacks it, it is missing.
// The multi-word patterns still accept abbreviations.
func TestStrictVerifyNotStoredWholeWord(t *testing.T) {
	work := strings.Join([]string{
		"sw1(config)#mac address-table static 0000.5e00.5301 vlan 10 interface gi0/1",
		"sw1(config)#macro apply lab-access",
		"sw1(config)#int ra gi0/1-2",
		"sw1(config-if-range)#exit",
		"sw1(config)#end",
	}, "\n")
	cfg := "sw1#show run\nhostname sw1\ninterface GigabitEthernet0/1\ninterface GigabitEthernet0/2\n"
	got, r := statuses(t, work, cfg, cfg)
	expect(t, got, map[string]string{
		" > mac address-table static 0000.5e00.5301 vlan 10 interface gi0/1": "missing",
		" > macro apply lab-access": "unverified/notStored",
		" > int ra gi0/1-2":         "unverified/notStored",
	})
	if r.Missing != 1 || r.Unverified != 2 {
		t.Errorf("missing %d unverified %d", r.Missing, r.Unverified)
	}
}

// What the logs cannot tell is "unverified", not missing.
func TestStrictVerifyUnverified(t *testing.T) {
	work := strings.Join([]string{
		"sw1(config)#interface range gi0/1-4",
		"sw1(config-if-range)#duplex auto",
		"sw1(config-if-range)#exit",
		"sw1(config)#crypto key generate rsa modulus 2048",
		"sw1(config)#banner motd ^C lab ^C",
		"sw1(config)#enable secret Secret123",
		"sw1(config)#end",
		"sw1#delete flash:old.bin", // an exec command, not a setting
	}, "\n")
	cfg := "sw1#show run\nhostname sw1\nenable secret 9 $9$abcdefghijkl\ninterface GigabitEthernet0/1\n"
	got, r := statuses(t, work, cfg, cfg)
	expect(t, got, map[string]string{
		" > interface range gi0/1-4":              "unverified/notStored",
		"interface range gi0/1-4 > duplex auto":   "unverified/default",
		" > crypto key generate rsa modulus 2048": "unverified/notStored",
		" > banner motd ^C lab ^C":                "unverified/notStored",
		" > enable secret Secret123":              "unverified/secret", // same encrypted line before and after
	})
	if r.Missing != 0 || r.Unverified != 5 || r.NoConfig {
		t.Errorf("%+v", r)
	}

	// An after log with no configuration listing in it at all.
	work2 := "sw1(config)#ntp server 192.0.2.10\nsw1(config)#no ip domain-lookup\nsw1(config)#end\n"
	got, r = statuses(t, work2, "sw1#show ip int brief\n", "sw1#show ip int brief\nInterface IP-Address OK? Method Status Protocol\n")
	expect(t, got, map[string]string{
		" > ntp server 192.0.2.10": "unverified/noConfig",
		" > no ip domain-lookup":   "unverified/noConfig",
	})
	if !r.NoConfig || r.Missing != 0 {
		t.Errorf("%+v", r)
	}
}

// FortiGate prints quotes; Junos prints braces, and "edit" makes the typed
// set lines relative.
func TestStrictVerifyFortiAndJunos(t *testing.T) {
	forti := "FG # config system interface\nFG (interface) # edit port1\nFG (port1) # set alias lab-wan\nFG (port1) # set password Secret123\nFG (port1) # next\nFG (interface) # end\n"
	fAfter := "FG # show system interface\nconfig system interface\n    edit \"port1\"\n        set alias \"lab-wan\"\n        set password ENC abcdefgh\n    next\nend\n"
	got, r := statuses(t, forti, "config system interface\n", fAfter)
	expect(t, got, map[string]string{
		" > set alias lab-wan":      "reflected",
		" > set password Secret123": "reflected/secret",
	})
	if r.Missing != 0 {
		t.Errorf("forti: %+v", r)
	}

	junos := strings.Join([]string{
		"ops@srv1> configure",
		"Entering configuration mode",
		"",
		"[edit]",
		"ops@srv1# edit interfaces ge-0/0/0",
		"",
		"[edit interfaces ge-0/0/0]",
		"ops@srv1# set description \"lab uplink\"",
		"",
		"[edit interfaces ge-0/0/0]",
		"ops@srv1# top",
		"",
		"[edit]",
		"ops@srv1# set system ntp server 192.0.2.10",
		"ops@srv1# delete system syslog host 192.0.2.60",
		"ops@srv1# set vlans v10 vlan-id 10",
		"ops@srv1# commit",
	}, "\n")
	jBefore := "system {\n    syslog {\n        host 192.0.2.60 {\n            any any;\n        }\n    }\n}\n"
	jAfter := strings.Join([]string{
		"ops@srv1> show configuration",
		"## Last commit: 2026-10-20 10:00:00 JST by ops",
		"system {",
		"    ntp {",
		"        server 192.0.2.10;",
		"    }",
		"}",
		"interfaces {",
		"    ge-0/0/0 {",
		"        description \"lab uplink\";",
		"    }",
		"}",
		"vlans {",
		"    v10 {",
		"        vlan-id 10;",
		"    }",
		"}",
	}, "\n")
	got, r = statuses(t, junos, jBefore, jAfter)
	expect(t, got, map[string]string{
		" > set description \"lab uplink\"":       "reflected",
		" > set system ntp server 192.0.2.10":     "reflected",
		" > delete system syslog host 192.0.2.60": "reflected",
		" > set vlans v10 vlan-id 10":             "reflected",
	})
	if r.Missing != 0 {
		t.Errorf("junos: %+v", r)
	}
}

func TestTokMatch(t *testing.T) {
	cases := []struct {
		typed, cand string
		mode        matchMode
		want        bool
	}{
		{"int gi0/1", "interface gigabitethernet0/1", whole, true},
		{"int gi0/1", "interface gigabitethernet0/10", whole, false},
		{"int po1", "interface port-channel1", whole, true},
		{"int te 1/0/1", "interface tengigabitethernet1/0/1", whole, true},
		{"desc up", "description uplink", whole, false},
		{"desc up", "description up link", trailing, false},
		{"ip route 0.0.0.0 0.0.0.0 192.0.2.1", "ip route 0.0.0.0 0.0.0.0 192.0.2.10", whole, false},
		{"spanning-tree portfast", "spanning-tree portfast edge", whole, false},
		{"spanning-tree portfast", "spanning-tree portfast edge", loose, true},
		{"logging 192.0.2.5", "logging host 192.0.2.5", loose, true},
		{"vlan 10", "vlan 100", loose, false},
	}
	for _, c := range cases {
		if got := tokMatch(strings.Fields(c.typed), strings.Fields(c.cand), c.mode); got != c.want {
			t.Errorf("%q vs %q (%d): %v", c.typed, c.cand, c.mode, got)
		}
	}
}
