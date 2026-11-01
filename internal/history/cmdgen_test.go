package history

import (
	"strings"
	"testing"
)

// Commands from a comparison (cmdgen.go). All device data is made up for
// the lab (192.0.2.x, sw-lab-01, ops).

func cmdTexts(g CmdGen) []string {
	var out []string
	for _, l := range g.Lines {
		out = append(out, strings.TrimSpace(l.Text))
	}
	return out
}

func joinCmds(g CmdGen) string { return strings.Join(cmdTexts(g), "\n") }

func wrapCisco(body string) string {
	return "sw-lab-01#show running-config\nBuilding configuration...\n!\nhostname sw-lab-01\n!\n" + body + "end\nsw-lab-01#\n"
}

const ciscoBefore = `interface GigabitEthernet0/1
 description uplink to core
 switchport trunk allowed vlan 10,20,30
 switchport mode trunk
!
interface GigabitEthernet0/2
 description access port
 switchport access vlan 10
!
interface GigabitEthernet0/3
!
ip route 0.0.0.0 0.0.0.0 192.0.2.1
!
`

// 1. A changed line, an added line, a new interface Vlan and settings on
// a physical interface that had none.
func TestCmdgenCiscoBasic(t *testing.T) {
	after := strings.Replace(ciscoBefore, " description uplink to core\n", " description uplink to core-2\n", 1)
	after = strings.Replace(after, " switchport access vlan 10\n", " switchport access vlan 10\n switchport port-security\n", 1)
	after = strings.Replace(after, "interface GigabitEthernet0/3\n!\n", "interface GigabitEthernet0/3\n description new port\n no shutdown\n!\ninterface Vlan20\n ip address 192.0.2.33 255.255.255.240\n!\n", 1)
	g := GenerateCommands(wrapCisco(ciscoBefore), wrapCisco(after), "rollback", "cisco-ios")
	if g.Family != "cisco" || g.FamilyBy != "osType" || g.Empty || g.NoConfig {
		t.Fatalf("family %s by %s empty %v noconfig %v", g.Family, g.FamilyBy, g.Empty, g.NoConfig)
	}
	// Phases: the object deletion (no interface Vlan) first, then the
	// "other" chunks in the order of the listing.
	want := []string{"configure terminal",
		"no interface Vlan20",
		"interface GigabitEthernet0/1", "description uplink to core", "exit",
		"interface GigabitEthernet0/2", "no switchport port-security", "exit",
		"default interface GigabitEthernet0/3",
		"end"}
	got := cmdTexts(g)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Forward: the same data the other way round.
	f := GenerateCommands(wrapCisco(ciscoBefore), wrapCisco(after), "forward", "cisco-ios")
	fs := joinCmds(f)
	for _, w := range []string{"description uplink to core-2", "switchport port-security", "interface Vlan20", "ip address 192.0.2.33 255.255.255.240", "interface GigabitEthernet0/3", "description new port"} {
		if !strings.Contains(fs, w) {
			t.Fatalf("forward lacks %q:\n%s", w, fs)
		}
	}
	if strings.Contains(fs, "no interface Vlan20") || strings.Contains(fs, "default interface") {
		t.Fatalf("forward removes what it should add:\n%s", fs)
	}
}

// 2. A setting replaced by one with another keyword: the new one is
// removed before the old one goes back.
func TestCmdgenCiscoKeywordChange(t *testing.T) {
	before := "interface GigabitEthernet0/1\n switchport mode trunk\n!\n"
	after := "interface GigabitEthernet0/1\n switchport access vlan 10\n!\n"
	g := GenerateCommands(wrapCisco(before), wrapCisco(after), "rollback", "")
	want := "configure terminal\ninterface GigabitEthernet0/1\nno switchport access vlan 10\nswitchport mode trunk\nexit\nend"
	if got := joinCmds(g); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if g.FamilyBy != "content" || g.Family != "cisco" {
		t.Fatalf("family %s by %s", g.Family, g.FamilyBy)
	}
}

// 3. A numbered ACL: the whole list is removed and re-entered as it was.
func TestCmdgenCiscoNumberedACL(t *testing.T) {
	before := "access-list 10 permit host 192.0.2.10\naccess-list 10 deny any\n!\n"
	after := "access-list 10 permit host 192.0.2.10\naccess-list 10 permit host 198.51.100.10\naccess-list 10 deny any\n!\n"
	g := GenerateCommands(wrapCisco(before), wrapCisco(after), "rollback", "")
	want := "configure terminal\nno access-list 10\naccess-list 10 permit host 192.0.2.10\naccess-list 10 deny any\nend"
	if got := joinCmds(g); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// 4. A VLAN list: only the numbers that differ are added or removed.
func TestCmdgenCiscoVlanList(t *testing.T) {
	before := "interface GigabitEthernet0/1\n switchport trunk allowed vlan 10,20,30\n!\n"
	after := "interface GigabitEthernet0/1\n switchport trunk allowed vlan 10,25-27,30\n!\n"
	g := GenerateCommands(wrapCisco(before), wrapCisco(after), "rollback", "")
	want := "configure terminal\ninterface GigabitEthernet0/1\nswitchport trunk allowed vlan remove 25-27\nswitchport trunk allowed vlan add 20\nexit\nend"
	if got := joinCmds(g); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// 5. Order: a reference is removed before its object, an object is
// created before it is referenced.
func TestCmdgenCiscoPhases(t *testing.T) {
	before := "ip access-list extended OLD\n permit ip any any\n!\ninterface GigabitEthernet0/1\n ip access-group OLD in\n!\n"
	after := "ip access-list extended NEW\n permit ip any any\n!\ninterface GigabitEthernet0/1\n ip access-group NEW in\n!\n"
	g := GenerateCommands(wrapCisco(before), wrapCisco(after), "rollback", "")
	got := cmdTexts(g)
	pos := func(s string) int {
		for i, l := range got {
			if l == s {
				return i
			}
		}
		t.Fatalf("missing %q in:\n%s", s, strings.Join(got, "\n"))
		return -1
	}
	// The interface block carries the reference removal and sorts by it:
	// before the ACL deletion, which comes before the OLD ACL's return.
	if !(pos("no ip access-group NEW in") < pos("no ip access-list extended NEW") && pos("no ip access-list extended NEW") < pos("ip access-list extended OLD")) {
		t.Fatalf("order:\n%s", strings.Join(got, "\n"))
	}
	if pos("ip access-group OLD in") < pos("no ip access-group NEW in") {
		t.Fatalf("old reference before the new one is removed:\n%s", strings.Join(got, "\n"))
	}
	if !g.Reordered {
		t.Fatalf("reordered flag not set")
	}
}

const fgBefore = `config system interface
    edit "port1"
        set vdom "root"
        set ip 192.0.2.10 255.255.255.0
    next
    edit "port2"
        set vdom "root"
        set ip 192.0.2.26 255.255.255.240
    next
end
config firewall policy
    edit 1
        set name "lab-out"
        set srcintf "port2"
    next
    edit 2
        set name "lab-dmz"
        set srcintf "port2"
    next
    edit 3
        set name "lab-deny"
    next
end
`

// 6. FortiGate: a changed set, an added edit, and policies in another
// order.
func TestCmdgenFortiGate(t *testing.T) {
	after := strings.Replace(fgBefore, "set ip 192.0.2.10 255.255.255.0", "set ip 192.0.2.11 255.255.255.0", 1)
	after = strings.Replace(after, "    next\nend\nconfig firewall policy", "    next\n    edit \"port3\"\n        set vdom \"root\"\n    next\nend\nconfig firewall policy", 1)
	// policies: 1,2,3 → 2,3,1
	after = strings.Replace(after, "    edit 1\n        set name \"lab-out\"\n        set srcintf \"port2\"\n    next\n", "", 1)
	after = strings.Replace(after, "    edit 3\n        set name \"lab-deny\"\n    next\nend\n", "    edit 3\n        set name \"lab-deny\"\n    next\n    edit 1\n        set name \"lab-out\"\n        set srcintf \"port2\"\n    next\nend\n", 1)
	g := GenerateCommands("fw-lab-01 # show full-configuration\n"+fgBefore+"fw-lab-01 # \n", "fw-lab-01 # show full-configuration\n"+after+"fw-lab-01 # \n", "rollback", "")
	if g.Family != "fortigate" {
		t.Fatalf("family %s", g.Family)
	}
	s := joinCmds(g)
	for _, w := range []string{"config system interface\nedit \"port1\"\nset ip 192.0.2.10 255.255.255.0\nnext\nend", "config system interface\ndelete \"port3\"\nend", "config firewall policy\nmove 1 before 2\nend"} {
		if !strings.Contains(s, w) {
			t.Fatalf("missing:\n%s\nin:\n%s", w, s)
		}
	}
	// The delete block (deleting direction) comes before the set block.
	if strings.Index(s, "delete \"port3\"") > strings.Index(s, "set ip 192.0.2.10") {
		t.Fatalf("delete after set:\n%s", s)
	}
}

// 7. Junos: a set becomes a delete, in set and brace format alike.
func TestCmdgenJunos(t *testing.T) {
	before := "set system host-name sw-lab-01\nset interfaces ge-0/0/0 unit 0 family inet address 192.0.2.1/30\n"
	after := before + "set system ntp server 192.0.2.123\n"
	g := GenerateCommands("sw-lab-01> show configuration | display set\n"+before+"sw-lab-01> \n", "sw-lab-01> show configuration | display set\n"+after+"sw-lab-01> \n", "rollback", "")
	if g.Family != "junos" || joinCmds(g) != "delete system ntp server 192.0.2.123\ncommit" {
		t.Fatalf("family %s:\n%s", g.Family, joinCmds(g))
	}
	bb := "system {\n    host-name sw-lab-01;\n}\n"
	ba := "system {\n    host-name sw-lab-01;\n    ntp {\n        server 192.0.2.123;\n    }\n}\n"
	g = GenerateCommands("sw-lab-01> show configuration\n"+bb+"sw-lab-01> \n", "sw-lab-01> show configuration\n"+ba+"sw-lab-01> \n", "rollback", "junos")
	if joinCmds(g) != "delete system ntp server 192.0.2.123\ncommit" {
		t.Fatalf("braces:\n%s", joinCmds(g))
	}
}

// 8. Yamaha: no-toggle and save.
func TestCmdgenYamaha(t *testing.T) {
	before := "ip route default gateway 192.0.2.1\nip filter 100 pass * * * * *\nip lan1 secure filter in 100\nadministrator password *\n"
	after := before + "ip filter 200 reject * * udp * 53\n"
	g := GenerateCommands("# show config\n"+before+"# \n", "# show config\n"+after+"# \n", "rollback", "yamaha-rtx")
	if g.Family != "yamaha" || joinCmds(g) != "no ip filter 200 reject * * udp * 53\nsave" {
		t.Fatalf("family %s:\n%s", g.Family, joinCmds(g))
	}
}

// 9. Generic: a labelled list, every line marked for a look.
func TestCmdgenGeneric(t *testing.T) {
	before := "alpha one\nbeta two\n"
	after := "alpha one\nbeta three\ngamma four\n"
	g := GenerateCommands(before, after, "rollback", "")
	if g.Family != "generic" || g.Manual == 0 || len(g.Warnings) == 0 {
		t.Fatalf("family %s manual %d warnings %v:\n%s", g.Family, g.Manual, g.Warnings, joinCmds(g))
	}
}

// 10. No configuration listing on a side: nothing, and the result says so.
func TestCmdgenNoConfig(t *testing.T) {
	g := GenerateCommands("sw-lab-01#show version\nlab build\nsw-lab-01#\n", wrapCisco(ciscoBefore), "rollback", "")
	if !g.NoConfig || !g.Empty {
		t.Fatalf("%+v", g)
	}
}

// The fingerprint of a configuration ignores noise and secrets and
// changes with the configuration.
func TestConfigFingerprint(t *testing.T) {
	a := wrapCisco(ciscoBefore)
	b := strings.Replace(a, "Building configuration...", "Building configuration...\nCurrent configuration : 999 bytes", 1)
	if ConfigFingerprint(a) == "" || ConfigFingerprint(a) != ConfigFingerprint(b) {
		t.Fatalf("stamp changed the fingerprint")
	}
	c := strings.Replace(a, "switchport mode trunk", "switchport mode access", 1)
	if ConfigFingerprint(a) == ConfigFingerprint(c) {
		t.Fatalf("a change did not change the fingerprint")
	}
	if ConfigFingerprint("sw-lab-01#show version\nlab\nsw-lab-01#\n") != "" {
		t.Fatalf("no listing should give no fingerprint")
	}
}
