package history

import (
	"strings"
	"testing"
)

// The pre-publish review of 範囲: コマンドごと (v1.9.36). All device data
// is made up for the lab (sw-lab-01, rt-lab-01, 192.0.2.x, 198.51.100.x,
// 203.0.113.x).

// Output lines that read as a prompt with a command: a BGP table's status
// letters, a routing table's, the heading of show switch.
const promptLikeA = `rt-lab-01#terminal length 0
rt-lab-01#show ip bgp
   Network          Next Hop            Metric LocPrf Weight Path
*> 192.0.2.0/24     0.0.0.0                  0         32768 i
r>i198.51.100.0/24  192.0.2.1                0    100      0 i
s> 203.0.113.0/24   192.0.2.2                0             0 64500 i
*> 203.0.113.128/25 192.0.2.2                0             0 64500 i
rt-lab-01#show ip route
C>* 192.0.2.0/24 is directly connected, eth0
S>* 198.51.100.0/24 [1/0] via 192.0.2.1, eth0
S>* 203.0.113.0/24 [1/0] via 192.0.2.2, eth0
rt-lab-01#show switch
Switch/Stack Mac Address : 0000.5e00.5301
Switch#   Role    Mac Address     Priority Version  State
*1       Active   0000.5e00.5301     15     V01     Ready
 2       Standby  0000.5e00.5302     14     V01     Ready
rt-lab-01#
`

// B: the last line of each output is gone or changed — the lines after
// the prompt-like ones, which the comparison lost.
const promptLikeB = `rt-lab-01#terminal length 0
rt-lab-01#show ip bgp
   Network          Next Hop            Metric LocPrf Weight Path
*> 192.0.2.0/24     0.0.0.0                  0         32768 i
r>i198.51.100.0/24  192.0.2.1                0    100      0 i
s> 203.0.113.0/24   192.0.2.2                0             0 64500 i
rt-lab-01#show ip route
C>* 192.0.2.0/24 is directly connected, eth0
S>* 198.51.100.0/24 [1/0] via 192.0.2.1, eth0
rt-lab-01#show switch
Switch/Stack Mac Address : 0000.5e00.5301
Switch#   Role    Mac Address     Priority Version  State
*1       Active   0000.5e00.5301     15     V01     Ready
 2       Member   0000.5e00.5302     14     V01     Ready
rt-lab-01#
`

func TestCommandOutputIsNotCutByPromptLikeLines(t *testing.T) {
	r := DiffWith(promptLikeA, promptLikeB, optsAll)
	want := []string{"show ip bgp", "show ip route", "show switch"}
	if strings.Join(r.CommandList, "|") != strings.Join(want, "|") {
		t.Fatalf("CommandList = %q, want %q", r.CommandList, want)
	}
	for _, c := range []struct {
		cmd                     string
		added, removed, changed int
	}{
		{"show ip bgp", 0, 1, 0},
		{"show ip route", 0, 1, 0},
		{"show switch", 0, 0, 1},
	} {
		o := optsAll
		o.Command = c.cmd
		r := DiffWith(promptLikeA, promptLikeB, o)
		if r.Added != c.added || r.Removed != c.removed || r.Changed != c.changed {
			t.Errorf("%s: added %d removed %d changed %d, want %d %d %d", c.cmd, r.Added, r.Removed, r.Changed, c.added, c.removed, c.changed)
		}
	}
}

// A switch still named "Switch": the heading of show switch starts with
// the device's own prompt.
func TestCommandOutputOnADeviceNamedSwitch(t *testing.T) {
	log := func(role string) string {
		return "Switch#show switch\n" +
			"Switch/Stack Mac Address : 0000.5e00.5301\n" +
			"Switch#   Role    Mac Address     Priority Version  State\n" +
			"*1       Active   0000.5e00.5301     15     V01     Ready\n" +
			" 2       " + role + "  0000.5e00.5302     14     V01     Ready\n" +
			"Switch#show clock\n" +
			"12:00:00.000 UTC Mon Jan 1 2024\n" +
			"Switch#\n"
	}
	o := optsAll
	r := DiffWith(log("Standby"), log("Member "), o)
	if strings.Join(r.CommandList, "|") != "show switch|show clock" {
		t.Fatalf("CommandList = %q", r.CommandList)
	}
	o.Command = "show switch"
	if r = DiffWith(log("Standby"), log("Member "), o); r.Changed != 1 {
		t.Errorf("the member's role changed below the heading: changed %d", r.Changed)
	}
}

// A log with nothing to tell its prompts by (commands typed in capitals,
// no bare prompt) is read as before.
func TestTypedCommandsWithoutATellablePrompt(t *testing.T) {
	order, _ := typedCommands(splitLines("sw-lab-01#SHOW VERSION\nlab build\nsw-lab-01#SHOW CLOCK\n12:00:00.000 UTC Mon Jan 1 2024\n"))
	if strings.Join(order, "|") != "show version|show clock" {
		t.Errorf("order = %q", order)
	}
}

// Other prompt forms keep working: config modes, Comware's views, Yamaha.
func TestTypedCommandsPromptForms(t *testing.T) {
	order, outs := typedCommands(splitLines(`<sw-lab-01>display version
lab build
<sw-lab-01>system-view
[sw-lab-01]display vlan
 Total VLANs: 2
[sw-lab-01]quit
<sw-lab-01>
`))
	if strings.Join(order, "|") != "display version|display vlan" {
		t.Errorf("Comware order = %q", order)
	}
	if c := outs["display vlan"]; c == nil || len(c.lines) != 1 {
		t.Errorf("display vlan: %+v", c)
	}
	order, outs = typedCommands(splitLines("# show config\n# RTX1210 Rev.14.01.00 (lab)\nip route default gateway 192.0.2.1\n# show environment\nRTX1210 lab\n# \n"))
	if strings.Join(order, "|") != "show config|show environment" {
		t.Errorf("Yamaha order = %q", order)
	}
	if c := outs["show environment"]; c == nil || len(c.lines) != 1 {
		t.Errorf("Yamaha: the bare prompt at the end is not output: %+v", c)
	}
}

// Each vendor's prompt is told as a prompt, the modes and contexts with it.
func TestTypedCommandsVendorPrompts(t *testing.T) {
	for _, c := range []struct{ name, log, want string }{
		{"Junos", "ops@srv1> show version\nlab build\nops@srv1> configure\nEntering configuration mode\n[edit]\nops@srv1# show interfaces\nge-0/0/0 {\n    unit 0;\n}\n[edit]\nops@srv1# exit\nops@srv1> \n", "show version|show interfaces"},
		{"IOS-XR", "RP/0/RP0/CPU0:rt-lab-01#show version\nlab build\nRP/0/RP0/CPU0:rt-lab-01#admin show platform\nNode  Type  State\nRP/0/RP0/CPU0:rt-lab-01#\n", "show version|admin show platform"},
		{"FortiOS", "fw-lab-01 # get system status\nVersion: lab build\nfw-lab-01 # config global\nfw-lab-01 (global) # show system dns\nconfig system dns\n    set primary 192.0.2.53\nend\nfw-lab-01 (global) # end\nfw-lab-01 # \n", "get system status|show system dns"},
		{"SR OS", "*A:rt-lab-01# show version\nlab build\n*A:rt-lab-01# configure router\n*A:rt-lab-01>config>router# info\n        interface \"system\"\n*A:rt-lab-01>config>router# exit all\n*A:rt-lab-01# \n", "show version|info"},
		{"AlaxalA", "sw-lab-01# show version\nlab build\nsw-lab-01# configure\nsw-lab-01(config)# vlan 10\n!sw-lab-01(config-vlan)# exit\n!sw-lab-01(config)# exit\n!sw-lab-01# show vlan\nVLAN counts:2\n!sw-lab-01# \n", "show version|show vlan"},
		{"ASA", "fw-lab-01/ctx1# show version\nlab build\nfw-lab-01/ctx1# show nameif\nInterface  Name  Security\nfw-lab-01/ctx1# \n", "show version|show nameif"},
		{"VRP", "<sw-lab-01>display version\nlab build\n<sw-lab-01>system-view\nEnter system view, return user view with Ctrl+Z.\n[sw-lab-01]interface GigabitEthernet0/0/1\n[sw-lab-01-GigabitEthernet0/0/1]display this\n#\ninterface GigabitEthernet0/0/1\n port link-type trunk\n#\nreturn\n[sw-lab-01-GigabitEthernet0/0/1]quit\n[sw-lab-01]quit\n<sw-lab-01>\n", "display version|display this"},
		{"jump host", "jump1#ssh -l ops 192.0.2.10\nPassword:\nsw-lab-01>enable\nPassword:\nsw-lab-01#show version\nlab build\nsw-lab-01#exit\njump1#\n", "ssh -l ops 192.0.2.10|show version"},
	} {
		lines := splitLines(c.log)
		order, outs := typedCommands(lines)
		var names []string
		for _, k := range order {
			names = append(names, outs[k].name)
		}
		if got := strings.Join(names, "|"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// VRP's "#" between the sections of display this is output, not the
	// end of it.
	lines := splitLines("<sw-lab-01>display this\n#\ninterface GigabitEthernet0/0/1\n port link-type trunk\n#\nreturn\n<sw-lab-01>\n")
	if _, outs := typedCommands(lines); len(outs["display this"].lines) != 5 {
		t.Errorf("display this: %d lines, want 5", len(outs["display this"].lines))
	}
}

// A bare prompt (an Enter with nothing typed) is not output.
func TestBarePromptIsNotACommandsOutput(t *testing.T) {
	log := `sw-lab-01#configure terminal
sw-lab-01(config)#hostname sw-lab-01
sw-lab-01(config)#
sw-lab-01(config)#end
sw-lab-01#show clock
12:00:00.000 UTC Mon Jan 1 2024
sw-lab-01#
sw-lab-01#
`
	lines := splitLines(log)
	order, outs := typedCommands(lines)
	if strings.Join(order, "|") != "show clock" {
		t.Errorf("order = %q", order)
	}
	if c := outs["show clock"]; c == nil || len(c.lines) != 1 || !strings.HasPrefix(lines[c.lines[0]], "12:00") {
		t.Errorf("show clock's output is its one line, without the prompts after it: %+v", c)
	}
	// With the noise kept, the prompt after the output is not compared.
	r := DiffWith(log, strings.Replace(log, "sw-lab-01#\nsw-lab-01#\n", "sw-lab-01#\n", 1), DiffOpts{Command: "show clock"})
	if len(r.Lines) != 1 || r.Added+r.Removed+r.Changed != 0 {
		t.Errorf("lines %d, added %d removed %d changed %d", len(r.Lines), r.Added, r.Removed, r.Changed)
	}
}

func TestNavigationKeepsTheCommandsWithOutput(t *testing.T) {
	for _, c := range []string{"configure terminal", "conf t", "exit", "end", "quit", "write memory", "wr", "wr mem", "write", "copy running-config startup-config",
		"terminal length 0", "admin", "top", "commit", "commit confirmed 5", "do write memory", "system-view", "save force", "enable"} {
		if !navigation(c) {
			t.Errorf("listed: %q", c)
		}
	}
	for _, c := range []string{"admin show platform", "admin display-config", "write terminal", "wr t", "wr term", "do wr t", "commit check", "top -bn1", "top show interfaces",
		"show version", "display vlan", "ping 192.0.2.1"} {
		if navigation(c) {
			t.Errorf("left out: %q", c)
		}
	}
}

// A listing picked by name gets the listing's rules on both sides, also
// when it is not the listing 設定だけ takes.
func cfgLog(runDesc, startTimeout string, running bool) string {
	s := ""
	if running {
		s += "fw-lab-01#show running-config\n!\nhostname fw-lab-01\n!\ninterface Ethernet0/1\n description " + runDesc + "\n!\nend\n"
	}
	return s + "fw-lab-01#show startup-config\n!\nhostname fw-lab-01\n!\ninterface Ethernet0/1\n description uplink 5 minutes\n!\ntimeout conn " + startTimeout + "\n!\nend\nfw-lab-01#\n"
}

func TestChosenListingIsComparedAsAListing(t *testing.T) {
	o := optsAll
	o.Command = "show startup-config"
	// Both logs list the running configuration too: the startup listing's
	// timeout change is a setting, not a clock reading.
	r := DiffWith(cfgLog("uplink", "1:00:00", true), cfgLog("uplink", "2:00:00", true), o)
	if r.Changed != 1 || r.Added != 0 || r.Removed != 0 {
		t.Errorf("timeout conn 1:00:00 → 2:00:00: changed %d added %d removed %d", r.Changed, r.Added, r.Removed)
	}
	// One log has the startup listing alone (its 設定だけ listing), the
	// other both: the same lines stay the same.
	r = DiffWith(cfgLog("uplink", "1:00:00", false), cfgLog("uplink", "1:00:00", true), o)
	if r.Changed+r.Added+r.Removed != 0 {
		t.Errorf("identical startup listings: changed %d added %d removed %d", r.Changed, r.Added, r.Removed)
	}
}

// The result names the command as the list does.
func TestCommandNameIsTheListedOne(t *testing.T) {
	a := "sw-lab-01#Show Inventory\nsw-lab-01#show clock\n12:00:00.000 UTC Mon Jan 1 2024\nsw-lab-01#\n"
	b := "sw-lab-01#show inventory\nNAME: \"Chassis\", DESCR: \"lab chassis\"\nsw-lab-01#\n"
	o := optsAll
	o.Command = "show inventory"
	r := DiffWith(a, b, o)
	if strings.Join(r.CommandList, "|") != "show clock|show inventory" {
		t.Fatalf("CommandList = %q", r.CommandList)
	}
	if r.Command != "show inventory" || !r.CommandA || !r.CommandB {
		t.Errorf("command %q A %v B %v", r.Command, r.CommandA, r.CommandB)
	}
}

func TestSummarizeIgnoresCommand(t *testing.T) {
	o := optsAll
	want := Summarize(cmdLogA, cmdLogB, o)
	o.Command = "show version"
	if got := Summarize(cmdLogA, cmdLogB, o); got != want {
		t.Errorf("with Command: %+v, want %+v", got, want)
	}
}
