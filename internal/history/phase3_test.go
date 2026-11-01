package history

import (
	"strings"
	"testing"
)

// Checklists, impact and the HTML report. Lab data only (192.0.2.x,
// sw-lab-01, ops).

const chkAfter = `sw-lab-01#show running-config
Building configuration...
!
hostname sw-lab-01
!
ntp server 192.0.2.123
ip name-server 192.0.2.53
logging host 192.0.2.50
snmp-server community labro RO
!
interface GigabitEthernet0/1
 description uplink to core
 switchport trunk allowed vlan 10,20,30
 switchport mode trunk
!
ip route 0.0.0.0 0.0.0.0 192.0.2.1
!
end
sw-lab-01#
`

func TestVerifyChecklist(t *testing.T) {
	lines := []CheckLine{
		{Text: "hostname sw-lab-01"},                                               // pass
		{Text: "ntp server 192.0.2.124"},                                           // fail: another value
		{Text: "ip domain-name lab.example"},                                       // missing
		{Text: "switchport mode trunk", Block: "interface GigabitEthernet0/1"},     // pass in block
		{Text: "switchport access vlan 10", Block: "interface GigabitEthernet0/1"}, // missing in block (different keyword)
		{Text: "sw tr al vl add 20", Block: "interface GigabitEthernet0/1"},            // pass by abbreviation and merged list
		{Text: "snmp-server community labro RO"},                                   // pass
	}
	r := VerifyChecklist(lines, chkAfter)
	got := map[string]string{}
	for _, x := range r.Results {
		got[x.Text] = x.Status
	}
	want := map[string]string{"hostname sw-lab-01": "pass", "ntp server 192.0.2.124": "fail", "ip domain-name lab.example": "missing", "switchport mode trunk": "pass", "switchport access vlan 10": "missing", "sw tr al vl add 20": "pass", "snmp-server community labro RO": "pass"}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%q: %s, want %s (%+v)", k, got[k], w, r.Results)
		}
	}
	if r.Pass != 4 || r.Fail != 1 || r.Missing != 2 {
		t.Fatalf("counts pass %d fail %d missing %d", r.Pass, r.Fail, r.Missing)
	}
	for _, x := range r.Results {
		if x.Status == "fail" && !strings.Contains(x.Found, "192.0.2.123") {
			t.Fatalf("fail without the value found: %+v", x)
		}
	}
	// No listing: everything skipped and said so.
	r = VerifyChecklist(lines, "sw-lab-01#show version\nlab\nsw-lab-01#\n")
	if !r.NoConfig || r.Pass+r.Fail+r.Missing != 0 {
		t.Fatalf("no config: %+v", r)
	}
}

func TestChecklistTextRoundTrip(t *testing.T) {
	text := "hostname sw-lab-01\n! comment\ninterface GigabitEthernet0/1\n switchport mode trunk\n description x\nntp server 192.0.2.123\n"
	lines := ParseChecklist(text)
	want := []CheckLine{{Text: "hostname sw-lab-01"}, {Text: "switchport mode trunk", Block: "interface GigabitEthernet0/1"}, {Text: "description x", Block: "interface GigabitEthernet0/1"}, {Text: "ntp server 192.0.2.123"}}
	if len(lines) != len(want) {
		t.Fatalf("%+v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("%d: %+v want %+v", i, lines[i], want[i])
		}
	}
	if back := ParseChecklist(FormatChecklist(lines)); len(back) != len(lines) || back[1] != lines[1] {
		t.Fatalf("round trip: %+v", back)
	}
	tpl := ChecklistTemplate("cisco", "ntp", []string{"192.0.2.123", " 192.0.2.124 "})
	if len(tpl) != 2 || tpl[1].Text != "ntp server 192.0.2.124" {
		t.Fatalf("template: %+v", tpl)
	}
	if fg := ChecklistTemplate("fortigate", "hostname", []string{"fw-lab-01"}); len(fg) != 1 || fg[0].Block != "config system global" || fg[0].Text != `set hostname "fw-lab-01"` {
		t.Fatalf("fortigate template: %+v", fg)
	}
}

func TestImpact(t *testing.T) {
	before := wrapCisco("interface GigabitEthernet0/1\n description uplink\n ip address 192.0.2.1 255.255.255.0\n!\nip access-list extended LAB-IN\n permit ip any any\n!\ninterface GigabitEthernet0/2\n ip access-group LAB-IN in\n!\n")
	after := strings.Replace(before, " description uplink", " description uplink to core", 1)
	r := DiffWith(before, after, DiffOpts{IgnoreNoise: true, IgnoreSecrets: true, ConfigOnly: true})
	if r.Impact == nil || r.Impact.Level != "low" || r.Impact.Low != 1 {
		t.Fatalf("description only: %+v", r.Impact)
	}
	after = strings.Replace(before, " ip address 192.0.2.1 255.255.255.0", " ip address 192.0.2.2 255.255.255.0", 1)
	after = strings.Replace(after, " permit ip any any", " permit ip host 192.0.2.10 any", 1)
	r = DiffWith(before, after, DiffOpts{IgnoreNoise: true, IgnoreSecrets: true, ConfigOnly: true})
	if r.Impact == nil || r.Impact.Level != "high" || r.Impact.High != 2 {
		t.Fatalf("address + acl: %+v", r.Impact)
	}
	// The changed ACL is referenced from Gi0/2.
	var ref *RefItem
	for i := range r.Impact.Refs {
		if r.Impact.Refs[i].Name == "LAB-IN" {
			ref = &r.Impact.Refs[i]
		}
	}
	if ref == nil || len(ref.Rows) != 1 || !strings.Contains(r.Lines[ref.Rows[0]].B, "ip access-group LAB-IN in") {
		t.Fatalf("reference: %+v", r.Impact.Refs)
	}
	s := Summarize(before, after, DiffOpts{IgnoreNoise: true, IgnoreSecrets: true})
	if s.Impact != "high" || s.High != 2 {
		t.Fatalf("summary impact: %+v", s)
	}
}

func TestReportHTML(t *testing.T) {
	before := wrapCisco(ciscoBefore)
	after := strings.Replace(before, " description uplink to core", " description uplink to core-2", 1)
	h := ReportHTML(ReportInput{Title: "lab report", RunA: "before", RunB: "after", Devices: []ReportDevice{{Name: "sw-lab-01", LogA: before, LogB: after, Verdict: "ok"}, {Name: "sw-lab-02", Error: "no log"}}})
	for _, w := range []string{"<!doctype html>", "lab report", "sw-lab-01", "sw-lab-02", "no log", `<span class="sa">-2</span>`, "設定の差分あり", "行同じ"} {
		if !strings.Contains(h, w) {
			t.Fatalf("report lacks %q", w)
		}
	}
	if strings.Contains(h, "<script") || strings.Contains(h, "http://") {
		t.Fatalf("report must be self-contained")
	}
	en := ReportHTML(ReportInput{Lang: "en", Devices: []ReportDevice{{Name: "sw-lab-01", LogA: before, LogB: after}}})
	if !strings.Contains(en, "configuration changed") {
		t.Fatalf("english labels")
	}
}
