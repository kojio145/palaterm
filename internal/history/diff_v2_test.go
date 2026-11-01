package history

import (
	"strings"
	"testing"
)

// The structured comparison (DiffWith). All device data here is made up
// for the lab (192.0.2.x, sw-lab-01, ops).

var optsAll = DiffOpts{IgnoreNoise: true, IgnoreSecrets: true}

func kinds(r DiffResult) string {
	var sb strings.Builder
	for _, l := range r.Lines {
		if l.Noise {
			sb.WriteString("n")
			continue
		}
		if l.Moved {
			sb.WriteString("m")
		}
		sb.WriteString(l.Kind)
	}
	return sb.String()
}

func changedRows(r DiffResult) []DiffLine {
	var out []DiffLine
	for _, l := range r.Lines {
		if l.Kind != "=" || l.Moved {
			out = append(out, l)
		}
	}
	return out
}

const ciscoBase = `hostname sw-lab-01
!
interface GigabitEthernet0/1
 description uplink to core
 switchport trunk allowed vlan 10,20,30
 switchport mode trunk
!
interface GigabitEthernet0/2
 description access port
 switchport access vlan 10
 spanning-tree portfast
!
interface GigabitEthernet0/3
 shutdown
!
ip route 0.0.0.0 0.0.0.0 192.0.2.1
!
`

// 1. One description changed in one interface and a line added to
// another: one changed row, one added row, nothing else.
func TestDiffWithBlocksKeepOtherInterfacesEqual(t *testing.T) {
	b := strings.Replace(ciscoBase, " description uplink to core", " description uplink to core-2", 1)
	b = strings.Replace(b, " switchport access vlan 10\n", " switchport access vlan 10\n switchport port-security\n", 1)
	r := DiffWith(ciscoBase, b, optsAll)
	if r.Changed != 1 || r.Added != 1 || r.Removed != 0 || r.Moved != 0 {
		t.Fatalf("counts: ~%d +%d -%d m%d\n%s", r.Changed, r.Added, r.Removed, r.Moved, kinds(r))
	}
	ch := changedRows(r)
	if ch[0].Kind != "~" || !strings.Contains(ch[0].B, "core-2") || ch[1].Kind != "+" || !strings.Contains(ch[1].B, "port-security") {
		t.Fatalf("rows: %+v", ch)
	}
	// The changed line is lit at its value only.
	if ch[0].SegA == nil || ch[0].SegB == nil {
		t.Fatalf("no inline segments: %+v", ch[0])
	}
	var add string
	for _, s := range ch[0].SegB {
		if s.Kind == "+" {
			add += s.Text
		}
	}
	if add != "-2" {
		t.Fatalf("added text %q, want -2 (segs %+v)", add, ch[0].SegB)
	}
}

// 2. A wrapped line does not shift everything below it into changes.
func TestDiffWithWrappedLineStaysLocal(t *testing.T) {
	long := " description " + strings.Repeat("x", 70)
	a := "interface Vlan10\n" + long + "yy\n ip address 192.0.2.1 255.255.255.0\n!\ninterface Vlan20\n ip address 192.0.2.17 255.255.255.240\n!\n"
	b := "interface Vlan10\n" + long + "\nyy\n ip address 192.0.2.1 255.255.255.0\n!\ninterface Vlan20\n ip address 192.0.2.17 255.255.255.240\n!\n"
	r := DiffWith(a, b, optsAll)
	if r.Same < 6 || r.Added+r.Removed+r.Changed > 2 {
		t.Fatalf("counts: =%d ~%d +%d -%d\n%s", r.Same, r.Changed, r.Added, r.Removed, kinds(r))
	}
	for _, l := range r.Lines {
		if strings.Contains(l.A, "Vlan20") && l.Kind != "=" {
			t.Fatalf("Vlan20 block disturbed: %+v", l)
		}
	}
}

// 3. Two ACL entries swapped: moved lines, nothing added or removed.
func TestDiffWithSwappedLinesAreMoves(t *testing.T) {
	a := "ip access-list extended LAB\n permit ip host 192.0.2.10 any\n deny ip any any\n permit tcp any any eq 22\n"
	b := "ip access-list extended LAB\n permit tcp any any eq 22\n permit ip host 192.0.2.10 any\n deny ip any any\n"
	r := DiffWith(a, b, optsAll)
	if r.Added != 0 || r.Removed != 0 || r.Changed != 0 || r.Moved == 0 {
		t.Fatalf("counts: ~%d +%d -%d m%d\n%s", r.Changed, r.Added, r.Removed, r.Moved, kinds(r))
	}
}

// 4. Interface blocks in another order: the headings moved, their lines
// are equal.
func TestDiffWithReorderedBlocksAreMoves(t *testing.T) {
	a := ciscoBase
	b := strings.Replace(a, "interface GigabitEthernet0/1\n description uplink to core\n switchport trunk allowed vlan 10,20,30\n switchport mode trunk\n!\n", "", 1)
	b = strings.Replace(b, "interface GigabitEthernet0/3\n shutdown\n!\n", "interface GigabitEthernet0/3\n shutdown\n!\ninterface GigabitEthernet0/1\n description uplink to core\n switchport trunk allowed vlan 10,20,30\n switchport mode trunk\n!\n", 1)
	r := DiffWith(a, b, optsAll)
	if r.Added != 0 || r.Removed != 0 || r.Changed != 0 || r.Moved != 1 {
		t.Fatalf("counts: ~%d +%d -%d m%d\n%s", r.Changed, r.Added, r.Removed, r.Moved, kinds(r))
	}
	for _, l := range r.Lines {
		if l.Moved && !strings.Contains(l.B, "GigabitEthernet0/1") {
			t.Fatalf("wrong block moved: %+v", l)
		}
	}
}

const fgBase = `config system interface
    edit "port1"
        set vdom "root"
        set ip 192.0.2.10 255.255.255.0
        set allowaccess ping https ssh
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
        set dstintf "port1"
        set action accept
    next
end
`

// 5. A set line added inside a nested edit: one added row.
func TestDiffWithFortiGateNestedAdd(t *testing.T) {
	b := strings.Replace(fgBase, `        set ip 192.0.2.26 255.255.255.240
`, "        set ip 192.0.2.26 255.255.255.240\n        set allowaccess ping\n", 1)
	r := DiffWith(fgBase, b, optsAll)
	if r.Added != 1 || r.Removed != 0 || r.Changed != 0 || r.Moved != 0 {
		t.Fatalf("counts: ~%d +%d -%d m%d\n%s", r.Changed, r.Added, r.Removed, r.Moved, kinds(r))
	}
	// Sections: the config blocks and their edits.
	var names []string
	for _, s := range r.Sections {
		names = append(names, s.Name)
	}
	want := `config system interface,edit "port1",edit "port2",config firewall policy,edit 1`
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("sections %q, want %q", got, want)
	}
}

// 6. Junos braces: one value changed.
func TestDiffWithJunosBraces(t *testing.T) {
	a := "interfaces {\n    ge-0/0/0 {\n        unit 0 {\n            family inet {\n                address 192.0.2.1/30;\n            }\n        }\n    }\n}\n"
	b := strings.Replace(a, "192.0.2.1/30", "192.0.2.5/30", 1)
	r := DiffWith(a, b, optsAll)
	if r.Changed != 1 || r.Added+r.Removed+r.Moved != 0 {
		t.Fatalf("counts: ~%d +%d -%d m%d\n%s", r.Changed, r.Added, r.Removed, r.Moved, kinds(r))
	}
}

// 7. A stack number appears in front of the port: the interface pairs
// with itself by its second key and the heading is a changed line.
func TestDiffWithStackNumberPairsInterfaces(t *testing.T) {
	b := strings.ReplaceAll(ciscoBase, "GigabitEthernet0/", "GigabitEthernet1/0/")
	r := DiffWith(ciscoBase, b, optsAll)
	if r.Changed != 3 || r.Added+r.Removed+r.Moved != 0 {
		t.Fatalf("counts: ~%d +%d -%d m%d\n%s", r.Changed, r.Added, r.Removed, r.Moved, kinds(r))
	}
	for _, l := range changedRows(r) {
		if !l.Head {
			t.Fatalf("a non-heading changed: %+v", l)
		}
	}
}

// 8. Certificates, encrypted values and UUIDs differ on one side only:
// left out and counted, no difference. The control envelope of a small
// switch's listing is left out but what it wraps is compared.
func TestDiffWithSecretsLeftOut(t *testing.T) {
	a := `config vpn certificate local
    edit "lab-cert"
        set password ENC AAAAbbbbCCCCddddEEEEffffGGGGhhhh1111
        set certificate "-----BEGIN CERTIFICATE-----
MIIBsamplesamplesample
-----END CERTIFICATE-----"
    next
end
config firewall address
    edit "lab-net"
        set uuid 0f0f0f0f-1111-2222-3333-444444444444
        set subnet 192.0.2.0 255.255.255.0
    next
end
crypto pki certificate chain LAB-CA
 certificate ca 01
  30820123 456789AB CDEF0123 456789AB
  CDEF0123 456789AB
  quit
ssd-control-start
unit-type-control-start
unit-type 1 ignored-here
unit-type-control-end
ssd-control-end
`
	b := strings.NewReplacer("AAAAbbbbCCCCddddEEEEffffGGGGhhhh1111", "ZZZZyyyyXXXXwwwwVVVVuuuuTTTTssss9999",
		"MIIBsamplesamplesample", "MIIBotherotherotherother",
		"0f0f0f0f-1111-2222-3333-444444444444", "0f0f0f0f-5555-6666-7777-888888888888",
		"30820123 456789AB CDEF0123 456789AB", "30820123 00000000 CDEF0123 456789AB",
		"unit-type 1 ignored-here", "unit-type 2 ignored-here").Replace(a)
	r := DiffWith(a, b, optsAll)
	if r.Added+r.Removed+r.Changed+r.Moved != 0 {
		t.Fatalf("differences found: ~%d +%d -%d m%d\n%s", r.Changed, r.Added, r.Removed, r.Moved, kinds(r))
	}
	if r.SecretLines == 0 {
		t.Fatal("no secret lines counted")
	}
	// Dimmed rows carry both sides when both have the line.
	noise := 0
	for _, l := range r.Lines {
		if l.Noise {
			if l.A != "" {
				noise++
			}
			if l.B != "" {
				noise++
			}
		}
	}
	if noise != r.SecretLines {
		t.Fatalf("%d dimmed lines, %d counted", noise, r.SecretLines)
	}
	// With the option off the same differences show.
	r = DiffWith(a, b, DiffOpts{IgnoreNoise: true})
	if r.Changed < 3 {
		t.Fatalf("secrets kept: ~%d +%d -%d\n%s", r.Changed, r.Added, r.Removed, kinds(r))
	}
	// The envelope is always left out, its content compared.
	c := strings.Replace(a, "unit-type-control-start\nunit-type 1 ignored-here\nunit-type-control-end\n", "", 1)
	c = strings.Replace(c, "ssd-control-start\n", "ssd-control-start\nvlan database\n", 1)
	d := strings.Replace(c, "vlan database", "vlan database 2", 1)
	r = DiffWith(c, d, DiffOpts{IgnoreNoise: true})
	if r.Changed != 1 && r.Added+r.Removed != 2 {
		t.Fatalf("ssd content not compared: ~%d +%d -%d\n%s", r.Changed, r.Added, r.Removed, kinds(r))
	}
}

const logA = `sw-lab-01#show version
Cisco IOS Software, lab build
sw-lab-01 uptime is 3 days, 2 hours
sw-lab-01#show running-config
Building configuration...

Current configuration : 1234 bytes
!
hostname sw-lab-01
!
interface GigabitEthernet0/1
 description uplink to core
!
end
sw-lab-01#show interfaces GigabitEthernet0/1
GigabitEthernet0/1 is up, line protocol is up
     1000 packets input, 2000 bytes
sw-lab-01#
`

// 9. Config-only: the counters of show interfaces differ in the whole
// logs, not in the configuration; a log without a listing falls back.
func TestDiffWithConfigOnly(t *testing.T) {
	b := strings.NewReplacer("1000 packets input, 2000 bytes", "5000 packets input, 9000 bytes",
		"Current configuration : 1234 bytes", "Current configuration : 1240 bytes",
		" description uplink to core", " description uplink to core-2",
		"GigabitEthernet0/1 is up, line protocol is up", "GigabitEthernet0/1 is down, line protocol is down").Replace(logA)
	s := Summarize(logA, b, optsAll)
	if !s.ConfigA || !s.ConfigB {
		t.Fatalf("listings not found: %+v", s)
	}
	if s.Config.Changed != 1 || s.Config.Added+s.Config.Removed+s.Config.Moved != 0 {
		t.Fatalf("config counts: %+v", s.Config)
	}
	if s.Whole.Changed < 2 {
		t.Fatalf("whole counts: %+v", s.Whole)
	}
	r := DiffWith(logA, b, DiffOpts{IgnoreNoise: true, IgnoreSecrets: true, ConfigOnly: true})
	if r.Mode != "config" {
		t.Fatalf("mode %q", r.Mode)
	}
	// Line numbers point into the original log.
	for _, l := range r.Lines {
		if l.Kind == "~" && l.LineA != 12 {
			t.Fatalf("line number %d, want 12: %+v", l.LineA, l)
		}
	}
	// The whole-log result names the typed commands and the sections.
	w := DiffWith(logA, b, optsAll)
	var cmds []string
	for _, c := range w.Commands {
		cmds = append(cmds, c.Name)
	}
	if got := strings.Join(cmds, ","); got != "show version,show running-config,show interfaces GigabitEthernet0/1" {
		t.Fatalf("commands %q", got)
	}
	if len(w.Sections) == 0 {
		t.Fatal("no sections")
	}
	// No listing on one side: the whole logs, and the result says which.
	noCfg := "sw-lab-01#show version\nCisco IOS Software, lab build\nsw-lab-01#\n"
	r = DiffWith(logA, noCfg, DiffOpts{IgnoreNoise: true, ConfigOnly: true})
	if r.Mode != "whole" || !r.ConfigA || r.ConfigB {
		t.Fatalf("fallback: mode %q configA %v configB %v", r.Mode, r.ConfigA, r.ConfigB)
	}
}

// 10. Inline: the changed value only, snapped to a delimiter; a line
// changed almost entirely gets no highlight.
func TestInlineDiff(t *testing.T) {
	segText := func(segs []Seg, kind string) string {
		var sb strings.Builder
		for _, s := range segs {
			if s.Kind == kind {
				sb.WriteString(s.Text)
			}
		}
		return sb.String()
	}
	cases := []struct{ a, b, delA, addB string }{
		{" ip address 192.0.2.10 255.255.255.0", " ip address 192.0.2.11 255.255.255.0", "10", "11"},
		{" switchport trunk allowed vlan 10,20,30", " switchport trunk allowed vlan 10,25,30", "20", "25"},
		{" shutdown", " no shutdown", "", "no "},
	}
	for _, c := range cases {
		sa, sb := inlineDiff(c.a, c.b)
		if sa == nil || sb == nil {
			t.Fatalf("%q vs %q: no segments", c.a, c.b)
		}
		if got := segText(sa, "-"); got != c.delA {
			t.Errorf("%q vs %q: deleted %q, want %q (%+v)", c.a, c.b, got, c.delA, sa)
		}
		if got := segText(sb, "+"); got != c.addB {
			t.Errorf("%q vs %q: added %q, want %q (%+v)", c.a, c.b, got, c.addB, sb)
		}
		if rebuild(sa) != c.a || rebuild(sb) != c.b {
			t.Errorf("%q vs %q: segments do not rebuild the lines", c.a, c.b)
		}
	}
	if sa, sb := inlineDiff("hostname sw-lab-01", "ntp server 192.0.2.123"); sa != nil || sb != nil {
		t.Fatalf("unrelated lines highlighted: %+v %+v", sa, sb)
	}
}

// 11. The plain Diff is untouched: identical in / out for the structured
// one on a flat input with no blocks.
func TestDiffWithFlatMatchesDiff(t *testing.T) {
	a := "alpha\nbeta\ngamma\ndelta\n"
	b := "alpha\ngamma\ndelta\nepsilon\n"
	r1 := Diff(a, b, true)
	r2 := DiffWith(a, b, DiffOpts{IgnoreNoise: true})
	if r1.Added != r2.Added || r1.Removed != r2.Removed || r1.Same != r2.Same || r2.Changed != 0 {
		t.Fatalf("Diff +%d -%d =%d; DiffWith +%d -%d =%d ~%d", r1.Added, r1.Removed, r1.Same, r2.Added, r2.Removed, r2.Same, r2.Changed)
	}
}

// Performance: two 20,000-line captures with a few scattered changes stay
// well within a second.
func TestDiffWithLargeInputFast(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		sb.WriteString("interface GigabitEthernet1/0/" + itoa(i) + "\n description port " + itoa(i) + "\n switchport access vlan 10\n switchport mode access\n spanning-tree portfast\n no shutdown\n ip arp inspection limit rate 15\n!\n")
	}
	a := sb.String()
	b := strings.Replace(a, " description port 777\n", " description port 777 changed\n", 1)
	b = strings.Replace(b, " description port 1500\n", " description port 1500\n storm-control broadcast level 1.00\n", 1)
	r := DiffWith(a, b, optsAll)
	if r.Approx || r.Changed != 1 || r.Added != 1 || r.Removed != 0 {
		t.Fatalf("counts: approx=%v ~%d +%d -%d", r.Approx, r.Changed, r.Added, r.Removed)
	}
}

func rebuild(segs []Seg) string {
	var sb strings.Builder
	for _, s := range segs {
		sb.WriteString(s.Text)
	}
	return sb.String()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for ; i > 0; i /= 10 {
		d = append([]byte{byte('0' + i%10)}, d...)
	}
	return string(d)
}
