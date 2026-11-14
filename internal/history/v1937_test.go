package history

import (
	"strings"
	"testing"
)

// v1.9.37: a command typed with output in two contexts of one log is
// listed once per context; a device named with one letter keeps the rows
// of its routing tables.

func listedNames(log string) string {
	order, outs := typedCommands(splitLines(log))
	var names []string
	for _, k := range order {
		names = append(names, outs[k].name)
	}
	return strings.Join(names, "|")
}

const viewsLogA = `<sw-lab-01>display version
lab build
<sw-lab-01>system-view
System View: return to User View with Ctrl+Z.
[sw-lab-01]interface GigabitEthernet1/0/1
[sw-lab-01-GigabitEthernet1/0/1]display this
#
interface GigabitEthernet1/0/1
 port access vlan 10
#
return
[sw-lab-01-GigabitEthernet1/0/1]interface GigabitEthernet1/0/2
[sw-lab-01-GigabitEthernet1/0/2]display this
#
interface GigabitEthernet1/0/2
 port access vlan 20
#
return
[sw-lab-01-GigabitEthernet1/0/2]display vlan
 Total VLANs: 2
[sw-lab-01-GigabitEthernet1/0/2]quit
[sw-lab-01]display vlan
 Total VLANs: 2
[sw-lab-01]quit
<sw-lab-01>
`

// "display this" in two interface views is two entries, named by the
// view; "display vlan" typed in two views asks the same in both and stays
// one.
func TestSameCommandInTwoViewsIsListedPerView(t *testing.T) {
	want := "display version|display this (GigabitEthernet1/0/1)|display this (GigabitEthernet1/0/2)|display vlan"
	if got := listedNames(viewsLogA); got != want {
		t.Fatalf("list = %q, want %q", got, want)
	}
	b := strings.Replace(viewsLogA, " port access vlan 10", " port access vlan 30", 1)
	r := DiffWith(viewsLogA, b, DiffOpts{})
	if got := strings.Join(r.CommandList, "|"); got != want {
		t.Errorf("CommandList = %q, want %q", got, want)
	}
	// Each view's output is compared with the same view's.
	r = DiffWith(viewsLogA, b, DiffOpts{Command: "display this (GigabitEthernet1/0/1)"})
	if r.Mode != "command" || r.Command != "display this (GigabitEthernet1/0/1)" || !r.CommandA || !r.CommandB || r.Changed != 1 || r.Added+r.Removed != 0 || len(r.Lines) != 5 {
		t.Errorf("view 1: mode %q command %q A %v B %v changed %d added %d removed %d lines %d", r.Mode, r.Command, r.CommandA, r.CommandB, r.Changed, r.Added, r.Removed, len(r.Lines))
	}
	r = DiffWith(viewsLogA, b, DiffOpts{Command: "display this (GigabitEthernet1/0/2)"})
	if !r.CommandA || !r.CommandB || r.Changed+r.Added+r.Removed != 0 || len(r.Lines) != 5 {
		t.Errorf("view 2: A %v B %v changed %d added %d removed %d lines %d", r.CommandA, r.CommandB, r.Changed, r.Added, r.Removed, len(r.Lines))
	}
	// The name is matched whatever its case (the key is lower case).
	r = DiffWith(viewsLogA, b, DiffOpts{Command: "DISPLAY THIS (gigabitethernet1/0/1)"})
	if r.Command != "display this (GigabitEthernet1/0/1)" || r.Changed != 1 {
		t.Errorf("case: command %q changed %d", r.Command, r.Changed)
	}
}

// B typed the command in one view only: it is named by that view all the
// same, so the entry of A's other view says that B has none, and nothing
// is compared across views.
func TestViewsWhenOnlyOneLogTypedTwo(t *testing.T) {
	b := `<sw-lab-01>system-view
[sw-lab-01]interface GigabitEthernet1/0/2
[sw-lab-01-GigabitEthernet1/0/2]display this
#
interface GigabitEthernet1/0/2
 port access vlan 20
#
return
[sw-lab-01-GigabitEthernet1/0/2]quit
[sw-lab-01]quit
<sw-lab-01>
`
	if got := listedNames(b); got != "display this" {
		t.Fatalf("B by itself is one entry: %q", got)
	}
	r := DiffWith(viewsLogA, b, DiffOpts{})
	want := "display version|display this (GigabitEthernet1/0/1)|display this (GigabitEthernet1/0/2)|display vlan"
	if got := strings.Join(r.CommandList, "|"); got != want {
		t.Errorf("CommandList = %q, want %q", got, want)
	}
	r = DiffWith(viewsLogA, b, DiffOpts{Command: "display this (GigabitEthernet1/0/2)"})
	if !r.CommandA || !r.CommandB || r.Changed+r.Added+r.Removed != 0 || len(r.Lines) != 5 {
		t.Errorf("same view: A %v B %v changed %d added %d removed %d lines %d", r.CommandA, r.CommandB, r.Changed, r.Added, r.Removed, len(r.Lines))
	}
	r = DiffWith(viewsLogA, b, DiffOpts{Command: "display this (GigabitEthernet1/0/1)"})
	if !r.CommandA || r.CommandB || r.Removed != 5 {
		t.Errorf("A's other view: A %v B %v removed %d", r.CommandA, r.CommandB, r.Removed)
	}
}

// One view per log, a different one in each: one entry, compared as in
// v1.9.36 (nothing in either log says there are two).
func TestOneViewPerLogStaysOneEntry(t *testing.T) {
	log := func(port, vlan string) string {
		return "<sw-lab-01>system-view\n[sw-lab-01]interface GigabitEthernet1/0/" + port + "\n[sw-lab-01-GigabitEthernet1/0/" + port + "]display this\n#\ninterface GigabitEthernet1/0/" + port + "\n port access vlan " + vlan + "\n#\nreturn\n[sw-lab-01-GigabitEthernet1/0/" + port + "]quit\n[sw-lab-01]quit\n<sw-lab-01>\n"
	}
	r := DiffWith(log("1", "10"), log("2", "20"), DiffOpts{Command: "display this"})
	if got := strings.Join(r.CommandList, "|"); got != "display this" {
		t.Errorf("CommandList = %q", got)
	}
	if r.Command != "display this" || !r.CommandA || !r.CommandB || r.Changed+r.Added+r.Removed == 0 {
		t.Errorf("command %q A %v B %v changed %d added %d removed %d", r.Command, r.CommandA, r.CommandB, r.Changed, r.Added, r.Removed)
	}
}

// The other vendors' contexts.
func TestContextsOfOtherPrompts(t *testing.T) {
	for _, c := range []struct{ name, log, want string }{
		// FortiOS: every command typed in a table or an entry depends on it.
		{"FortiOS", "fw-lab-01 # config system interface\nfw-lab-01 (interface) # edit port1\nfw-lab-01 (port1) # show\nconfig system interface\n    edit \"port1\"\n        set ip 192.0.2.1 255.255.255.0\n    next\nend\nfw-lab-01 (port1) # next\nfw-lab-01 (interface) # edit port2\nfw-lab-01 (port2) # show\nconfig system interface\n    edit \"port2\"\n        set ip 198.51.100.1 255.255.255.0\n    next\nend\nfw-lab-01 (port2) # end\nfw-lab-01 # \n",
			"show (port1)|show (port2)"},
		{"FortiOS VDOM", "fw-lab-01 # config vdom\nfw-lab-01 (vdom) # edit root\nfw-lab-01 (root) # get router info routing-table all\nS*      0.0.0.0/0 [10/0] via 192.0.2.254, port1\nfw-lab-01 (root) # next\nfw-lab-01 (vdom) # edit lab\nfw-lab-01 (lab) # get router info routing-table all\nS*      0.0.0.0/0 [10/0] via 198.51.100.254, port2\nfw-lab-01 (lab) # end\nfw-lab-01 # \n",
			"get router info routing-table all (root)|get router info routing-table all (lab)"},
		// Junos: the operational command and the configuration's are two;
		// "run" is the operational one from the configuration mode.
		{"Junos", "ops@srv1> show interfaces\nPhysical interface: ge-0/0/0, Enabled, Physical link is Up\nops@srv1> configure\nEntering configuration mode\n\n[edit]\nops@srv1# show interfaces\nge-0/0/0 {\n    unit 0;\n}\n\n[edit]\nops@srv1# edit interfaces ge-0/0/0\n\n[edit interfaces ge-0/0/0]\nops@srv1# show\nunit 0;\n\n[edit interfaces ge-0/0/0]\nops@srv1# run show version\nlab build\n\n[edit interfaces ge-0/0/0]\nops@srv1# top\n\n[edit]\nops@srv1# run show version\nlab build\n\n[edit]\nops@srv1# show\ninterfaces {\n    ge-0/0/0 {\n        unit 0;\n    }\n}\n\n[edit]\nops@srv1# exit\nops@srv1> \n",
			"show interfaces|show interfaces (edit)|show (edit interfaces ge-0/0/0)|run show version|show (edit)"},
		{"SR OS", "*A:rt-lab-01# configure router\n*A:rt-lab-01>config>router# info\n        interface \"system\"\n*A:rt-lab-01>config>router# exit\n*A:rt-lab-01>config# service\n*A:rt-lab-01>config>service# info\n        customer 1 create\n*A:rt-lab-01>config>service# exit all\n*A:rt-lab-01# \n",
			"info (config>router)|info (config>service)"},
		// A config mode's bare "show" (AlaxalA) is the mode's; "do show" and
		// a show typed in two modes ask the same and stay one entry.
		{"config modes", "sw-lab-01# configure\nsw-lab-01(config)# interface gigabitethernet 0/1\nsw-lab-01(config-if)# show\ninterface gigabitethernet 0/1\n  switchport access vlan 10\nsw-lab-01(config-if)# show vlan summary\nVLAN counts:2\nsw-lab-01(config-if)# do show clock\n12:00:00 UTC Mon Jan 1 2024\nsw-lab-01(config-if)# vlan 10\nsw-lab-01(config-vlan)# show\nvlan 10\n  name lab\nsw-lab-01(config-vlan)# do show clock\n12:00:05 UTC Mon Jan 1 2024\nsw-lab-01(config-vlan)# exit\nsw-lab-01(config)# show vlan summary\nVLAN counts:2\nsw-lab-01(config)# end\nsw-lab-01# show vlan summary\nVLAN counts:2\nsw-lab-01# \n",
			"show (config-if)|show vlan summary|do show clock|show (config-vlan)"},
		// VRP's mark of a configuration not committed is not part of the view.
		{"VRP", "<sw-lab-01>system-view\n[~sw-lab-01]interface 10GE1/0/1\n[~sw-lab-01-10GE1/0/1]display this\n#\ninterface 10GE1/0/1\n#\nreturn\n[~sw-lab-01-10GE1/0/1]description lab\n[*sw-lab-01-10GE1/0/1]interface 10GE1/0/2\n[*sw-lab-01-10GE1/0/2]display this\n#\ninterface 10GE1/0/2\n#\nreturn\n[*sw-lab-01-10GE1/0/2]quit\n[*sw-lab-01]display this\n#\nsysname sw-lab-01\n#\nreturn\n[*sw-lab-01]quit\n<sw-lab-01>\n",
			"display this (10GE1/0/1)|display this (10GE1/0/2)|display this"},
	} {
		if got := listedNames(c.log); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A context's entry holds the last output typed there, and a listing
// asked for in a context ("show" in a FortiOS entry) is compared as a
// listing: "set timeout 1:00:00" and "2:00:00" are two settings, not two
// readings of a clock.
func TestContextEntryIsItsLastOutputAndAListing(t *testing.T) {
	a := "fw-lab-01 # config system interface\nfw-lab-01 (interface) # edit port1\nfw-lab-01 (port1) # show\nconfig system interface\n    edit \"port1\"\n        set timeout 9:00:00\n    next\nend\nfw-lab-01 (port1) # show\nconfig system interface\n    edit \"port1\"\n        set timeout 1:00:00\n    next\nend\nfw-lab-01 (port1) # next\nfw-lab-01 (interface) # edit port2\nfw-lab-01 (port2) # show\nconfig system interface\n    edit \"port2\"\n        set timeout 5:00:00\n    next\nend\nfw-lab-01 (port2) # end\nfw-lab-01 # \n"
	b := strings.Replace(a, "set timeout 1:00:00", "set timeout 2:00:00", 1)
	r := DiffWith(a, b, DiffOpts{Command: "show (port1)", IgnoreNoise: true})
	if r.Command != "show (port1)" || len(r.Lines) != 5 || r.Changed != 1 {
		t.Errorf("command %q lines %d changed %d added %d removed %d noise %d", r.Command, len(r.Lines), r.Changed, r.Added, r.Removed, r.NoiseLines)
	}
	r = DiffWith(a, b, DiffOpts{Command: "show (port2)", IgnoreNoise: true})
	if len(r.Lines) != 5 || r.Changed+r.Added+r.Removed != 0 {
		t.Errorf("port2: lines %d changed %d", len(r.Lines), r.Changed)
	}
}

// A device named with one letter: the rows of a BGP or routing table that
// start with that letter and ">" are output, not commands.
func TestStatusRowsOnADeviceNamedWithOneLetter(t *testing.T) {
	for _, c := range []struct{ name, log, want, cmd string }{
		{"r", "r>show ip bgp\n   Network          Next Hop            Metric LocPrf Weight Path\nr>i198.51.100.0/24  192.0.2.1                0    100      0 i\n*>i203.0.113.0/24   192.0.2.2                0    100      0 i\nr> 192.0.2.128/25   192.0.2.3                0             0 65001 i\nr>i10.0.0.0         192.0.2.4                0    100      0 i\nr>show clock\n12:00:00.000 UTC Mon Jan 1 2024\nr>\n", "show ip bgp|show clock", "show ip bgp"},
		{"s", "s#show ip bgp\n   Network          Next Hop            Metric LocPrf Weight Path\ns> 203.0.113.0/24   192.0.2.1                0             0 65001 i\ns>i198.51.100.0/24  192.0.2.2                0    100      0 i\n*> 192.0.2.128/25   192.0.2.3                0             0 65001 i\ns> 10.0.0.0         192.0.2.4                0             0 65001 i\ns#show clock\n12:00:00.000 UTC Mon Jan 1 2024\ns#\n", "show ip bgp|show clock", "show ip bgp"},
		{"C", "C> show ip route\nCodes: K - kernel route, C - connected, S - static\nS>* 0.0.0.0/0 [1/0] via 192.0.2.254, eth0\nC>* 192.0.2.0/24 is directly connected, eth0\nC>* 2001:db8::/64 is directly connected, eth0\nO>* 198.51.100.0/24 [110/20] via 192.0.2.2, eth0\nC> show clock\n12:00:00\nC> \n", "show ip route|show clock", "show ip route"},
	} {
		lines := splitLines(c.log)
		order, outs := typedCommands(lines)
		if got := strings.Join(order, "|"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
			continue
		}
		if n := len(outs[c.cmd].lines); n != 5 {
			t.Errorf("%s: %d lines of output, want 5", c.name, n)
		}
	}
	// An address typed at such a prompt is still a command.
	if got := listedNames("r>show clock\n12:00:00\nr>192.0.2.1\nTrying 192.0.2.1 ... Open\nr>192.0.2.9 /source-interface Loopback0\nTrying 192.0.2.9 ... Open\nr>\n"); got != "show clock|192.0.2.1|192.0.2.9 /source-interface Loopback0" {
		t.Errorf("typed address: %q", got)
	}
}
