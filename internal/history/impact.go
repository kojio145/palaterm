package history

import (
	"regexp"
	"strings"
)

// Impact: how much a configuration change may affect traffic, judged
// from what kind of line changed and where. Not a verdict — a reading
// order: the devices and the blocks to look at first.

// ImpactItem is one changed line with its weight.
type ImpactItem struct {
	Row     int    `json:"row"`
	Section string `json:"section"` // the heading the line is under ("" global)
	Level   string `json:"level"`   // high / mid / low
	Reason  string `json:"reason"`  // the rule that fired (Japanese, shown as is)
	Text    string `json:"text"`
}

// RefItem: an object whose definition changed, and the lines that refer
// to it (so a changed ACL shows the interfaces it is applied to).
type RefItem struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Rows []int  `json:"rows"`
}

// ImpactResult is the reading of one comparison.
type ImpactResult struct {
	Level string       `json:"level"` // high / mid / low / none
	Score int          `json:"score"`
	High  int          `json:"high"`
	Mid   int          `json:"mid"`
	Low   int          `json:"low"`
	Items []ImpactItem `json:"items,omitempty"`
	Refs  []RefItem    `json:"refs,omitempty"`
}

type impactRule struct {
	re     *regexp.Regexp
	level  string
	reason string
}

// The rules, first match wins. Written against the lower-cased line
// (with "no " kept: removing a route weighs as much as adding one).
var impactRules = []impactRule{
	{regexp.MustCompile(`^(no )?shutdown$`), "high", "ポートの up/down"},
	{regexp.MustCompile(`^(no )?(ip |ipv6 )?(address|ip address|route|default-gateway|default-network)\b`), "high", "アドレス・経路"},
	{regexp.MustCompile(`^(no )?ip route\b|^(no )?set (gateway|device|dst|ip|allowaccess)\b`), "high", "アドレス・経路"},
	{regexp.MustCompile(`^(no )?(router|address-family|neighbor|network|redistribute|area|passive-interface)\b`), "high", "ルーティング"},
	{regexp.MustCompile(`^(no )?(ip |ipv6 )?(access-group|traffic-filter|access-class)\b`), "high", "ACL の適用"},
	{regexp.MustCompile(`^(no )?(permit|deny)\b|^(no )?access-list\b`), "high", "ACL の中身"},
	{regexp.MustCompile(`^(no )?set (srcintf|dstintf|srcaddr|dstaddr|service|action|status|nat|schedule)\b`), "high", "ポリシー"},
	{regexp.MustCompile(`^(no )?(vrf|ip vrf|crypto|tunnel|interface tunnel|channel-group|lacp|spanning-tree (mode|priority|vlan)|vtp|mtu|speed|duplex|hsrp|standby|vrrp|ip nat|nat)\b`), "high", "接続・冗長・NAT"},
	{regexp.MustCompile(`^(no )?(switchport|vlan|encapsulation|set vlanid|ip helper-address|ip dhcp|dhcp)\b`), "mid", "VLAN・DHCP"},
	{regexp.MustCompile(`^(no )?(ntp|clock|ip name-server|ip domain|dns|set (primary|secondary|server|source-ip|hostname))\b`), "mid", "時刻・名前解決"},
	{regexp.MustCompile(`^(no )?(logging|syslog|snmp|snmp-server|aaa|tacacs|radius|username|enable (secret|password)|line |ip ssh|ip http|set (admin|password|passwd)|service password)\b`), "mid", "管理・認証"},
	{regexp.MustCompile(`^(no )?(ip access-list|ipv6 access-list|route-map|ip prefix-list|class-map|policy-map|service-policy|set (name|member|subnet|start-ip|end-ip|interface))\b`), "mid", "オブジェクト・QoS"},
	{regexp.MustCompile(`^(no )?(description|banner|remark|alias|set (comment|comments|alias|description|uuid|color))\b|^(no )?(exec-timeout|history|length)\b`), "low", "説明・表示"},
}

// Headings whose whole block weighs high whatever the line.
var highSections = regexp.MustCompile(`(?i)^(config (firewall policy|firewall policy6|system interface|router static|router bgp|router ospf|system ha|vpn ipsec)|router |interface (vlan|tunnel|loopback|port-channel)|ip vrf|vrf definition)`)

// Objects a changed heading defines, and the word used to refer to them.
var objectHead = regexp.MustCompile(`(?i)^(ip access-list (?:extended|standard) (\S+)|ipv6 access-list (\S+)|route-map (\S+)|ip prefix-list (\S+)|class-map(?: match-\w+)? (\S+)|policy-map (\S+)|object-group \w+ (\S+)|edit "?([^"]+)"?|crypto map (\S+)|track (\d+)|vlan (\d+)|interface Port-channel(\d+)|ip sla (\d+))$`)

func impactLevel(text, section string) (string, string) {
	t := strings.ToLower(strings.TrimSpace(text))
	for _, r := range impactRules {
		if r.re.MatchString(t) {
			if r.level == "mid" && section != "" && highSections.MatchString(section) {
				return "high", r.reason + "（" + sectionWord(section) + " の中）"
			}
			return r.level, r.reason
		}
	}
	if section != "" && highSections.MatchString(section) {
		return "high", sectionWord(section) + " の中の変更"
	}
	return "mid", "その他の設定"
}

func sectionWord(s string) string {
	f := strings.Fields(s)
	if len(f) > 3 {
		f = f[:3]
	}
	return strings.Join(f, " ")
}

// Impact reads a comparison (configuration only).
func Impact(r DiffResult) ImpactResult {
	res := ImpactResult{Level: "none"}
	type level struct {
		depth int
		text  string
	}
	var stack []level
	section := func() string {
		if len(stack) == 0 {
			return ""
		}
		return stack[0].text
	}
	weight := map[string]int{"high": 5, "mid": 2, "low": 0}
	for i, l := range r.Lines {
		if l.Noise {
			continue
		}
		text := strings.TrimSpace(rowText(l))
		if l.Head {
			for len(stack) > 0 && stack[len(stack)-1].depth >= l.Depth {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, level{l.Depth, text})
		} else {
			for len(stack) > 0 && stack[len(stack)-1].depth >= l.Depth {
				stack = stack[:len(stack)-1]
			}
		}
		if l.Kind == "=" && !l.Moved {
			continue
		}
		sec := section()
		if l.Head && l.Depth == 0 {
			sec = ""
		}
		lv, why := impactLevel(text, sec)
		if l.Moved {
			lv, why = "mid", "並び順の変更"
			if strings.HasPrefix(strings.ToLower(text), "permit") || strings.HasPrefix(strings.ToLower(text), "deny") || strings.HasPrefix(strings.ToLower(sec), "config firewall policy") {
				lv, why = "high", "評価順が変わる並び替え"
			}
		}
		if l.Head && (l.Kind == "+" || l.Kind == "-") {
			why = "ブロックの追加・削除"
			if highSections.MatchString(text) {
				lv = "high"
			}
		}
		res.Items = append(res.Items, ImpactItem{Row: i, Section: sec, Level: lv, Reason: why, Text: text})
		res.Score += weight[lv]
		switch lv {
		case "high":
			res.High++
		case "mid":
			res.Mid++
		default:
			res.Low++
		}
	}
	switch {
	case res.High > 0:
		res.Level = "high"
	case res.Mid > 0:
		res.Level = "mid"
	case res.Low > 0:
		res.Level = "low"
	}
	res.Refs = references(r)
	return res
}

// references: for every changed block that defines a named object, the
// other lines of B (or A when B lacks them) that name it.
func references(r DiffResult) []RefItem {
	type obj struct {
		name, kind string
		start, end int
	}
	var objs []obj
	for i, l := range r.Lines {
		if !l.Head || l.Noise {
			continue
		}
		changed := l.Kind != "="
		// A block counts as changed when any line under it changed.
		end := i + 1
		for end < len(r.Lines) && (r.Lines[end].Depth > l.Depth || (r.Lines[end].Depth == l.Depth && isCloser(strings.TrimSpace(rowText(r.Lines[end]))))) {
			if r.Lines[end].Kind != "=" || r.Lines[end].Moved {
				changed = true
			}
			end++
		}
		if !changed {
			continue
		}
		m := objectHead.FindStringSubmatch(strings.TrimSpace(rowText(l)))
		if m == nil {
			continue
		}
		name := ""
		for _, g := range m[2:] {
			if g != "" {
				name = g
			}
		}
		if name == "" || len(name) < 2 {
			continue
		}
		kind := strings.Fields(m[1])[0]
		objs = append(objs, obj{name, kind, i, end})
	}
	var out []RefItem
	for _, o := range objs {
		re := regexp.MustCompile(`(?i)(^|[^\w.\-])` + regexp.QuoteMeta(o.name) + `($|[^\w.\-])`)
		var rows []int
		for i, l := range r.Lines {
			if i >= o.start && i < o.end || l.Noise {
				continue
			}
			t := rowText(l)
			if l.Kind == "-" {
				t = l.A
			}
			if re.MatchString(t) {
				rows = append(rows, i)
			}
		}
		if len(rows) > 0 {
			out = append(out, RefItem{Name: o.name, Kind: o.kind, Rows: rows})
		}
	}
	return out
}
