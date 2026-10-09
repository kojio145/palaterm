package history

import (
	"strings"
	"testing"
)

func TestExtractConfigCommands(t *testing.T) {
	work := strings.Join([]string{
		"R1#show running-config",
		"Building configuration...",
		"R1#conf t",
		"Enter configuration commands, one per line.  End with CNTL/Z.",
		"R1(config)#hostname R1-NEW",
		"R1-NEW(config)#interface GigabitEthernet0/1",
		"R1-NEW(config-if)# description  uplink   to core",
		"R1-NEW(config-if)#no shutdown",
		"R1-NEW(config-if)#end",
		"R1-NEW#wr",
		"% Invalid input detected",
		"IX-A(config)# terminal length 0",
		"FG (port1) # set ip 10.0.0.1 255.255.255.0",
		"user@host> show version",
		"R1-NEW(config)#hostname R1-NEW", // duplicate, ignored
	}, "\n")
	cmds, typed := ExtractConfigCommands(work)
	want := []string{"hostname R1-NEW", "interface GigabitEthernet0/1", "description  uplink   to core", "no shutdown", "set ip 10.0.0.1 255.255.255.0"}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
	if typed != 12 {
		t.Fatalf("typed = %d", typed)
	}
}

func TestStrictVerify(t *testing.T) {
	before := "R1#show run\nhostname R1\ninterface GigabitEthernet0/1\n shutdown\n!\nntp server 10.0.0.9\n"
	work := "R1#conf t\nR1(config)#hostname R1-NEW\nR1-NEW(config)#interface GigabitEthernet0/1\nR1-NEW(config-if)#description uplink\nR1-NEW(config-if)#no shutdown\nR1-NEW(config-if)#no ntp server 10.0.0.9\nR1-NEW(config)#snmp-server community public\nR1-NEW(config)#end\n"
	after := "R1-NEW#show run\nhostname R1-NEW\ninterface GigabitEthernet0/1\n description uplink\n!\n"
	r := StrictVerify(work, before, after)
	got := map[string]string{}
	for _, c := range r.Commands {
		got[c.Line] = c.Status
	}
	exp := map[string]string{
		"hostname R1-NEW":              "reflected",
		"interface GigabitEthernet0/1": "already",   // was there before too
		"description uplink":           "reflected", // indented in the after log
		"no shutdown":                  "reflected", // "shutdown" gone
		"no ntp server 10.0.0.9":       "reflected",
		"snmp-server community public": "missing",
	}
	for line, st := range exp {
		if got[line] != st {
			t.Errorf("%q: %s, want %s", line, got[line], st)
		}
	}
	if r.Reflected != 5 || r.Missing != 1 {
		t.Errorf("counts: reflected %d missing %d", r.Reflected, r.Missing)
	}
	// A "no" of something still present afterwards is missing.
	r2 := StrictVerify("R1(config)#no shutdown\n", "interface x\n shutdown\n", "interface x\n shutdown\n")
	if r2.Commands[0].Status != "missing" {
		t.Errorf("no-command still present: %s", r2.Commands[0].Status)
	}
	// Gear that keeps the negation as a config line (NEC IX, Juniper): the
	// literal "no shutdown" in the after log is the reflection.
	r4 := StrictVerify("IX(config)# no shutdown\n", "interface GigabitEthernet0.0\n  shutdown\n", "interface GigabitEthernet0.0\n  no shutdown\n")
	if r4.Commands[0].Status != "reflected" {
		t.Errorf("literal no-line kept by the device: %s", r4.Commands[0].Status)
	}
	r5 := StrictVerify("IX(config)# no shutdown\n", "interface x\n  no shutdown\n", "interface x\n  no shutdown\n")
	if r5.Commands[0].Status != "already" {
		t.Errorf("literal no-line present before and after: %s", r5.Commands[0].Status)
	}
	// No settings typed at all.
	r3 := StrictVerify("R1#show version\nR1#show clock\n", "", "")
	if len(r3.Commands) != 0 || r3.Typed != 2 {
		t.Errorf("show-only work: %+v", r3)
	}
}
