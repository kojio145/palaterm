package history

import (
	"strings"
	"testing"
)

// 範囲: コマンドごと (DiffOpts.Command, v1.9.36). All device data is made
// up for the lab (sw-lab-01, 192.0.2.x).

const cmdLogA = `sw-lab-01#terminal length 0
sw-lab-01#show version
Cisco IOS Software, lab build
sw-lab-01 uptime is 3 days, 2 hours
sw-lab-01#show running-config
Building configuration...
!
hostname sw-lab-01
!
interface GigabitEthernet0/1
 description uplink
!
end
sw-lab-01#show interfaces GigabitEthernet0/1
GigabitEthernet0/1 is up, line protocol is up
     1000 packets input, 2000 bytes
sw-lab-01#show interfaces GigabitEthernet0/1
GigabitEthernet0/1 is up, line protocol is up
     1500 packets input, 3000 bytes
sw-lab-01#configure terminal
Enter configuration commands, one per line.
sw-lab-01(config)#interface GigabitEthernet0/1
sw-lab-01(config-if)#end
sw-lab-01#write memory
[OK]
sw-lab-01#
`

const cmdLogB = `sw-lab-01#show version
Cisco IOS Software, lab build
sw-lab-01 uptime is 3 days, 5 hours
sw-lab-01#SHOW   running-config
Building configuration...
!
hostname sw-lab-01
!
interface GigabitEthernet0/1
 description uplink to core
!
end
sw-lab-01#show interfaces GigabitEthernet0/1
GigabitEthernet0/1 is down, line protocol is down
     5000 packets input, 9000 bytes
sw-lab-01#show ip interface brief
Interface              IP-Address      OK? Method Status   Protocol
GigabitEthernet0/1     192.0.2.10      YES manual down     down
sw-lab-01#
`

func TestCommandListIsTheCommandsWithOutput(t *testing.T) {
	r := DiffWith(cmdLogA, cmdLogB, optsAll)
	want := []string{"show version", "show running-config", "show interfaces GigabitEthernet0/1", "show ip interface brief"}
	if strings.Join(r.CommandList, "|") != strings.Join(want, "|") {
		t.Fatalf("CommandList = %q, want %q", r.CommandList, want)
	}
	if r.Mode != "whole" || r.Command != "" {
		t.Errorf("mode without Command: %q %q", r.Mode, r.Command)
	}
}

func TestDiffWithOneCommandOnly(t *testing.T) {
	o := optsAll
	o.Command = "show interfaces GigabitEthernet0/1"
	r := DiffWith(cmdLogA, cmdLogB, o)
	if r.Mode != "command" || r.Command != o.Command || !r.CommandA || !r.CommandB {
		t.Fatalf("mode %q command %q A %v B %v", r.Mode, r.Command, r.CommandA, r.CommandB)
	}
	var text []string
	for _, l := range r.Lines {
		text = append(text, l.A+"|"+l.B)
	}
	all := strings.Join(text, "\n")
	for _, bad := range []string{"hostname", "uptime", "sw-lab-01#", "1000 packets"} {
		if strings.Contains(all, bad) {
			t.Errorf("line outside the command's last output: %q in\n%s", bad, all)
		}
	}
	if !strings.Contains(all, "1500 packets") || !strings.Contains(all, "is down") {
		t.Errorf("the last output of the command is compared:\n%s", all)
	}
	// The state line changed, and the counter line too: the count before
	// "packets input" is not blanked by the noise rules (only what follows).
	if r.Changed != 2 || r.Added != 0 || r.Removed != 0 {
		t.Errorf("counts: changed %d added %d removed %d", r.Changed, r.Added, r.Removed)
	}
	// Still ConfigOnly set: Command wins.
	o.ConfigOnly = true
	if r2 := DiffWith(cmdLogA, cmdLogB, o); r2.Mode != "command" {
		t.Errorf("ConfigOnly with Command: mode %q", r2.Mode)
	}
}

func TestDiffWithCommandKeyIgnoresCaseAndSpacing(t *testing.T) {
	o := optsAll
	o.Command = "show running-config"
	r := DiffWith(cmdLogA, cmdLogB, o)
	if !r.CommandA || !r.CommandB {
		t.Fatalf("B's \"SHOW   running-config\" not matched: A %v B %v", r.CommandA, r.CommandB)
	}
	if r.Changed != 1 {
		t.Errorf("the description change: changed %d", r.Changed)
	}
	if r.Command != "show running-config" {
		t.Errorf("named as typed in A: %q", r.Command)
	}
}

func TestDiffWithCommandOnlyInOneLog(t *testing.T) {
	o := optsAll
	o.Command = "show ip interface brief"
	r := DiffWith(cmdLogA, cmdLogB, o)
	if r.CommandA || !r.CommandB {
		t.Fatalf("A %v B %v", r.CommandA, r.CommandB)
	}
	if r.Added != 2 || r.Removed != 0 || r.Changed != 0 {
		t.Errorf("B's output alone, all added: added %d removed %d changed %d", r.Added, r.Removed, r.Changed)
	}
	o.Command = "show nothing"
	r = DiffWith(cmdLogA, cmdLogB, o)
	if r.CommandA || r.CommandB || len(r.Lines) != 0 || r.Command != "show nothing" {
		t.Errorf("a command neither typed: A %v B %v lines %d command %q", r.CommandA, r.CommandB, len(r.Lines), r.Command)
	}
}

func TestTypedCommandsLeavesOutNavigationAndSilentCommands(t *testing.T) {
	order, outs := typedCommands(splitLines(cmdLogA))
	for _, k := range order {
		switch k {
		case "terminal length 0", "configure terminal", "write memory", "interface gigabitethernet0/1", "end":
			t.Errorf("listed: %q", k)
		}
	}
	if c := outs["interface gigabitethernet0/1"]; c == nil || c.output {
		t.Errorf("a setting typed at the config prompt is known but has no output: %+v", c)
	}
	if c := outs["show interfaces gigabitethernet0/1"]; c == nil || len(c.lines) != 2 {
		t.Errorf("the last output of a command typed twice: %+v", c)
	}
}
