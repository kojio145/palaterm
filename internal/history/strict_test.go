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

// An interactive (Tera Term style) capture keeps the device's line editing:
// tab completion is recorded as "sho\b \b\b \b\b \bshow ". The fixture is
// made-up lab data that reproduces only the symptom: such a capture once
// showed "5 / 6 件が未反映" although every setting had been applied.
func TestStrictVerifyInteractiveBackspaces(t *testing.T) {
	work := strings.Join([]string{
		"IX-A(config)# terminal length 0\r",
		"IX-A(config)# sho\b \b\b \b\b \bshow run\b \b\b \b\b \brunning-config \r",
		"hostname IX-A\r",
		"IX-A(config)# \r",
		"IX-A(config)# a\b \b\r",
		"IX-A(config)# inter\b \b\b \b\b \b\b \b\b \binterface gi\b \b\b \bGigaEthernet0.0\r",
		"IX-A(config-GigaEthernet0.0)# ip add\b \b\b \b\b \baddress 10.0.10.0\b \b1/24\r",
		"IX-A(config-GigaEthernet0.0)# no shu\b \b\b \b\b \bshutdown \r",
		"IX-A(config-GigaEthernet0.0)# exit\r",
		"IX-A(config)# wr\b \b\b \bwrite mem\b \b\b \b\b \bmemory \r",
		"Building configuration...\r",
	}, "\n")
	cmds, _ := ExtractConfigCommands(work)
	want := []string{"interface GigaEthernet0.0", "ip address 10.0.10.1/24", "no shutdown"}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
	before := "interface GigaEthernet0.0\r\n  no ip address\r\n  shutdown\r\n!\r\ninterface GigaEthernet1.0\r\n  ip address 192.0.2.201/24\r\n  no shutdown\r\n"
	after := "interface GigaEthernet0.0\r\n  ip address 10.0.10.1/24\r\n  no shutdown\r\n!\r\ninterface GigaEthernet1.0\r\n  ip address 192.0.2.201/24\r\n  no shutdown\r\n"
	r := StrictVerify(work, before, after)
	if r.Missing != 0 || r.Reflected != 3 {
		t.Fatalf("counts: %+v", r)
	}
	for _, c := range r.Commands {
		if c.Line != "interface GigaEthernet0.0" && c.Status != "reflected" {
			t.Errorf("%q: %s", c.Line, c.Status)
		}
	}
}

func TestResolveLineEdits(t *testing.T) {
	cases := map[string]string{
		"sho\b \b\b \b\b \bshow run":    "show run",
		"abc\b\bX":                      "aXc",
		"\x1b[32mR1#\x1b[0m conf t":     "R1# conf t",
		"\x1b]0;title\x07R1#":           "R1#",
		"first line\rsecond":            "secondline",
		"\b\bx":                         "x",
		"plain":                         "plain",
		"ip address 10.0.10.0\b \b1/24": "ip address 10.0.10.1/24",
	}
	for in, want := range cases {
		if got := ResolveLineEdits(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// The diff compares and shows what was on screen, not the raw line editing
// bytes of an interactive capture.
func TestDiffResolvesLineEdits(t *testing.T) {
	a := "R1# sho\b \b\b \b\b \bshow run\r\nhostname R1\r\n"
	b := "R1# show run\r\nhostname R1-NEW\r\n"
	r := Diff(a, b, true)
	for _, l := range r.Lines {
		if strings.ContainsRune(l.A, '\b') || strings.ContainsRune(l.B, '\b') {
			t.Fatalf("raw backspace in diff line: %+v", l)
		}
	}
	if r.Added != 1 || r.Removed != 1 {
		t.Fatalf("only the hostname should differ: %+v", r)
	}
	if CleanTranscript("a\b \bb\r\nc\x1b[0m\r\n") != "b\nc" {
		t.Fatalf("CleanTranscript: %q", CleanTranscript("a\b \bb\r\nc\x1b[0m\r\n"))
	}
}

// readline-style gear (Linux, Juniper, Arista) corrects with "\b" plus
// erase-to-end-of-line, and pagers rewrite the line with a bare "\r".
func TestResolveLineEditsCSI(t *testing.T) {
	cases := map[string]string{
		"abc\b\x1b[K":                     "ab",
		"sho\b\b\b\x1b[Kshow":             "show",
		"show vers\b\x1b[Ksion":           "show version",
		"x\x1b[2Dab":                      "ab",
		"abcd\x1b[3D\x1b[2P":              "ad",
		"\x1b[?25l\x1b[1;32mR1#\x1b[0m x": "R1# x",
		"abc\x1b[5Gz":                     "abc z",
		"abc\x1b[2K":                      "",
		"\x1b7text\x1b8":                  "text",
	}
	for in, want := range cases {
		if got := ResolveLineEdits(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	// Through the real path (splitLines): the pager line is overwritten,
	// not split into extra lines.
	if got := CleanTranscript(" --More-- \r         \rhostname R1\r\nx\r\n"); got != "hostname R1\nx" {
		t.Errorf("pager via splitLines: %q", got)
	}
	r := Diff(" --More-- \r         \rhostname R1\r\n", "hostname R1\r\n", true)
	if r.Added != 0 || r.Removed != 0 {
		t.Errorf("pager rewrite should not count as a difference: %+v", r)
	}
	// Strict verification with readline-style editing.
	work := "host(config)# ntp serv\b\b\b\b\x1b[Kserver 10.0.0.9\r\n"
	cmds, _ := ExtractConfigCommands(work)
	if len(cmds) != 1 || cmds[0] != "ntp server 10.0.0.9" {
		t.Errorf("readline edit: %q", cmds)
	}
}

// Exec-style words typed inside a sub-mode (reset / restart / request) run
// something; they are not settings and must not count as missing.
func TestStrictVerifyExecWordsNotSettings(t *testing.T) {
	before := "R1#show run\ninterface Cellular0\n!\n"
	work := strings.Join([]string{
		"R1#conf t",
		"R1(config)#interface Cellular0",
		"R1(config-if)#profile lab",
		"R1(config-if)#reset",
		"R1(config-if)#restart",
		"R1(config-if)#end",
		"R1#reset counters",
		"R1#request system reboot",
	}, "\n") + "\n"
	after := "R1#show run\ninterface Cellular0\n profile lab\n!\n"
	r := StrictVerify(work, before, after)
	for _, c := range r.Commands {
		switch c.Line {
		case "reset", "restart", "reset counters", "request system reboot":
			t.Errorf("%q counted as a setting (%s)", c.Line, c.Status)
		}
	}
	if r.Missing != 0 {
		t.Errorf("missing = %d, want 0", r.Missing)
	}
}

func TestIsNotSettingBothWays(t *testing.T) {
	exec := []string{
		"reset", "reset counters", "restart", "request system reboot", "reload in 5",
		"clear counters", "write memory", "copy running-config startup-config",
		"display current-configuration", "save", "return", "system-view", "reboot",
		"screen-length 0 temporary", "screen-length disable", "startup saved-configuration flash:/a.cfg",
		"execute ping 192.0.2.1", "diagnose sys top", "get system status", "fnsysctl ls",
		"commit", "rollback 1", "validate", "check pending-changes", "revert config",
		"set cli screen-length 0", "clock set 10:00:00 1 Jan 2026", "calendar set 10:00:00 1 Jan 2026",
		"cold start", "administrator", "ppupdate", "checkpoint c1", "tac-pac", "attach module 1",
		"packet-tracer input inside tcp 192.0.2.1 1 192.0.2.2 80", "bash", "sudo systemctl status",
		"expert", "cpstat fw", "cphaprob stat", "lock database override", "tmsh", "list ltm pool",
		"reset-stats ltm pool", "tools dump router", "admin save", "oam ping", "file list",
		"start shell", "monitor traffic interface ge-0/0/0", "test aaa group lab ops x legacy",
		"erase startup-config", "format flash:", "upgrade rom-monitor file flash:a.bin",
		"install add file bootflash:a.bin", "backup running-config to flash:/a.cfg", "release dhcp",
		"transfer download start", "halt", "poweroff", "q", "y", "no", "send log test",
	}
	for _, c := range exec {
		if !isNotSetting(c) {
			t.Errorf("%q should be an exec-style line", c)
		}
	}
	setting := []string{
		"monitor session 1 source interface GigabitEthernet0/1", "monitor event-trace all-traces",
		"history size 100", "telnet server enable", "ssh server enable", "ssh user ops authentication-type password",
		"ftp server enable", "file prompt quiet", "file verify auto", "backup interface Serial0/0",
		"cli alias name wr copy run start", "install feature-set fex", "shell processing full",
		"upgrade fpd auto", "screen-length 0", "shutdown", "no shutdown", "no ip domain-lookup",
		"import map LAB-IN", "export map LAB-OUT", "enable secret 0 lab", "enable password lab",
		"delete 5", "move 5 before 3", "clone 1 to 9", "purge", "boot system flash:a.bin",
		"archive", "license boot level lab", "delete ssh", "set system host-name lab1",
		"deactivate interfaces ge-0/0/0", "undo info-center enable", "info-center enable",
	}
	for _, c := range setting {
		if isNotSetting(c) {
			t.Errorf("%q should stay a setting line", c)
		}
	}
}
