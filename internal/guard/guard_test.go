package guard

import "testing"

func TestCheckFlagsChanges(t *testing.T) {
	cases := map[string]string{
		"conf t":                             ReasonConfigMode,
		"configure terminal":                 ReasonConfigMode,
		"configure":                          ReasonConfigMode,
		"config system interface":            ReasonConfigMode,
		"svintr-config":                      ReasonConfigMode,
		"wr":                                 ReasonWrite,
		"write memory":                       ReasonWrite,
		"copy running-config startup-config": ReasonWrite,
		"save":                               ReasonWrite,
		"reload":                             ReasonReload,
		"reload in 5":                        ReasonReload,
		"execute reboot":                     ReasonReload,
		"request system reboot":              ReasonReload,
		"erase startup-config":               ReasonErase,
		"write erase":                        ReasonErase,
		"delete flash:old.bin":               ReasonErase,
		"no shutdown":                        ReasonInterface,
		"interface GigabitEthernet0/1":       ReasonInterface,
		"set system host-name r1":            ReasonInterface,
		"ip route 0.0.0.0 0.0.0.0 10.0.0.1":  ReasonInterface,
		"commit":                             ReasonCommit,
		"do reload":                          ReasonReload,
	}
	for cmd, want := range cases {
		hits := Check([]string{cmd})
		if len(hits) != 1 {
			t.Errorf("%q: want 1 hit, got %d", cmd, len(hits))
			continue
		}
		if hits[0].Reason != want {
			t.Errorf("%q: reason %q, want %q", cmd, hits[0].Reason, want)
		}
	}
}

func TestCheckPassesReads(t *testing.T) {
	safe := []string{
		"show running-config", "show startup-config", "show clock", "show interfaces status",
		"show ip route", "show version", "get system status", "diagnose sys ntp status",
		"show configuration", "show config", "terminal length 0", "do show run",
		"display current-configuration", "show tech-support", "ping 10.0.0.1",
		"show interfaces", "show vlan", "show arp neighbors", "exit", "",
		"get router info routing-table all", "show system interface | grep -i edit",
	}
	if hits := Check(safe); len(hits) != 0 {
		t.Errorf("safe commands flagged: %+v", hits)
	}
}

func TestCheckKeepsOrderAndIndex(t *testing.T) {
	hits := Check([]string{"show clock", "conf t", "hostname x", "end", "wr"})
	if len(hits) != 3 || hits[0].Index != 1 || hits[1].Index != 2 || hits[2].Index != 4 {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}
