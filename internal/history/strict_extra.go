package history

import (
	"regexp"
	"strings"
)

// parentHead is the after-log head line of the block a command was typed
// in ("" for a global line), or the typed parent when the after log lacks
// that block.
func parentHead(c typedCmd, after *cfgIndex) string {
	if c.parent == "" {
		return ""
	}
	if h, _ := after.block(c.parent); h != "" {
		return h
	}
	return normKey(c.parent)
}

// markSuperseded: a setting not found afterwards is not "missing" when a
// later line of the same work, in the same block, undid it ("shutdown"
// then "no shutdown" — a port bounce) or replaced its value ("ip address
// 192.0.2.1 …" corrected to "ip address 192.0.2.2 …"). Settings that add to
// a list (ntp server, ip route, logging host, vlan, secondary addresses …)
// are not replaced by the next one, so they stay missing.
func markSuperseded(cmds []typedCmd, ccs []CommandCheck, after *cfgIndex) {
	for i := range cmds {
		if ccs[i].Status != "missing" {
			continue
		}
		ti := strings.Fields(cmds[i].key)
		pi := parentHead(cmds[i], after)
		for j := range cmds {
			if j == i || cmds[j].last <= cmds[i].last || parentHead(cmds[j], after) != pi {
				continue
			}
			tj := strings.Fields(cmds[j].key)
			if undoes(tj, ti) || (ccs[j].Status != "missing" && replaces(tj, ti)) {
				ccs[i].Status, ccs[i].Match, ccs[i].Note = "superseded", "", ""
				break
			}
		}
	}
}

// undoes: one of the two is the negation of the other.
func undoes(later, earlier []string) bool {
	tl, nl := negation(later)
	te, ne := negation(earlier)
	switch {
	case nl && !ne:
		return tokMatch(tl, earlier, trailing)
	case ne && !nl:
		return tokMatch(te, later, trailing)
	}
	return false
}

// valueShape splits a setting into its keywords and its values ("ip address
// 192.0.2.1 255.255.255.0" → "ip address"); ok is false when the setting is
// not one value of a keyword (words after the values, a list it adds to).
func valueShape(t []string) ([]string, bool) {
	n := 0
	for n < len(t) && isWord(t[n]) {
		n++
		if isFreeKeyword(t[n-1]) {
			return t[:n], n < len(t)
		}
	}
	if n == 0 || n == len(t) {
		return nil, false
	}
	for _, w := range t[n:] {
		if isWord(w) {
			return nil, false // "… secondary", "… area 0": not a plain value
		}
	}
	kw := t[:n]
	for _, p := range [][]string{
		{"ip", "route"}, {"ipv6", "route"}, {"ntp", "server"}, {"ntp", "peer"}, {"logging"}, {"logging", "host"},
		{"snmp-server", "host"}, {"ip", "helper-address"}, {"ip", "name-server"}, {"vlan"}, {"ip", "domain-list"},
		{"ip", "dhcp", "excluded-address"}, {"switchport", "trunk", "allowed", "vlan", "add"}, {"switchport", "trunk", "allowed", "vlan", "remove"},
	} {
		if len(kw) == len(p) && startsWith(kw, p...) {
			return nil, false
		}
	}
	// Junos paths ("set interfaces ge-0/0/0 …") carry names with digits:
	// only a short FortiGate-style "set x y" is one value.
	if kw[0] == "set" && len(t) > 4 {
		return nil, false
	}
	return kw, true
}

func isFreeKeyword(w string) bool {
	for k := range freeText {
		if w == k || (len(w) >= 4 && strings.HasPrefix(k, w)) {
			return true
		}
	}
	return false
}

// replaces: the same keywords with another value.
func replaces(later, earlier []string) bool {
	a, ok1 := valueShape(later)
	b, ok2 := valueShape(earlier)
	if !ok1 || !ok2 || len(a) != len(b) {
		return false
	}
	for k := range a {
		if !sameWord(a[k], b[k]) {
			return false
		}
	}
	return true
}

// maxUnexpected caps the changed lines returned per device (the count is
// complete).
const maxUnexpected = 50

// showConfig matches the commands that list the configuration.
var showConfig = regexp.MustCompile(`(?i)^(sh(o|ow)?\s+(run|runn|running-config|conf|config|configuration|full-configuration|start|startup-config)|display\s+(cur|current-configuration|saved)|wr(ite)?\s+t(erm)?|show\s*$)`)

type cfgLine struct {
	block, key, orig, blockOrig string
}

// configLines returns the configuration listing of a log: the output of
// the show running-config style commands in it, or the whole file when it
// has no prompt lines at all (a configuration saved to a file). Comments,
// noise (configuration size, last-change stamps) and blank lines are left
// out; Junos's brace format becomes set lines.
func configLines(log string) []cfgLine {
	lines := splitLines(log)
	var sec []string
	sawPrompt, sawShow, in := false, false, false
	for _, l := range lines {
		if m := promptLine.FindStringSubmatch(strings.TrimRight(l, " \t")); m != nil {
			sawPrompt = true
			in = showConfig.MatchString(strings.TrimSpace(m[2]))
			sawShow = sawShow || in
			continue
		}
		if in {
			sec = append(sec, l)
		}
	}
	if !sawPrompt {
		sec = lines
	} else if !sawShow {
		return nil
	}
	var out []cfgLine
	if js := junosSet(sec); js != nil {
		for _, l := range js {
			out = append(out, cfgLine{key: normKey(l), orig: strings.TrimSpace(ws.ReplaceAllString(l, " "))})
		}
		return out
	}
	head, headOrig := "", ""
	for _, l := range sec {
		k := normKey(normalize(l))
		if k == "" || k == "end" || k == "exit" || k == "}" || strings.HasPrefix(k, "!") || strings.HasPrefix(k, "#") ||
			strings.HasPrefix(k, "building configuration") || barePrompt.MatchString(k) {
			continue
		}
		orig := strings.TrimSpace(ws.ReplaceAllString(l, " "))
		if l[0] != ' ' && l[0] != '\t' {
			head, headOrig = k, orig
			out = append(out, cfgLine{key: k, orig: orig})
			continue
		}
		out = append(out, cfgLine{block: head, key: k, orig: orig, blockOrig: headOrig})
	}
	return out
}

// unexpectedChanges lists the configuration lines that differ between the
// before and after logs and that no typed setting accounts for: what the
// work changed besides what was typed (a setting entered on the wrong
// port, someone else's change), or what the device added by itself. A line
// is accounted for when a setting was found as it, when a setting of the
// same block has the same leading words (a value replaced), when a "no …"
// removes it, or when it is inside a block the work created or removed.
func unexpectedChanges(cmds []typedCmd, touched map[string]bool, beforeLog, afterLog string, after *cfgIndex) ([]ChangedLine, int) {
	bl, al := configLines(beforeLog), configLines(afterLog)
	if len(bl) == 0 || len(al) == 0 {
		return []ChangedLine{}, 0
	}
	type key struct{ block, line string }
	count := func(ls []cfgLine) map[key]int {
		m := map[key]int{}
		for _, l := range ls {
			m[key{l.block, l.key}]++
		}
		return m
	}
	cb, ca := count(bl), count(al)
	heads := map[string]bool{}
	for _, c := range cmds {
		heads[parentHead(c, after)] = true
	}
	// A block that exists on one side only was created or removed whole.
	whole := func(block string) bool {
		return block != "" && (ca[key{"", block}] == 0) != (cb[key{"", block}] == 0) && (heads[block] || touched[block])
	}
	explained := func(l cfgLine, added bool) bool {
		if added && touched[l.key] {
			return true
		}
		if whole(l.block) || (l.block == "" && whole(l.key)) {
			return true
		}
		t := strings.Fields(l.key)
		for _, c := range cmds {
			if c.parent != "" && parentHead(c, after) != l.block {
				continue
			}
			ct := strings.Fields(c.key)
			if target, neg := negation(ct); neg {
				if tokMatch(target, t, trailing) {
					return true
				}
				ct = target
			}
			if sameLead(ct, t) {
				return true
			}
		}
		return false
	}
	out := []ChangedLine{}
	n := 0
	emit := func(ls []cfgLine, mine, other map[key]int, kind string) {
		done := map[key]bool{}
		for _, l := range ls {
			k := key{l.block, l.key}
			if done[k] || mine[k] <= other[k] {
				continue
			}
			done[k] = true
			if explained(l, kind == "+") {
				continue
			}
			n++
			if len(out) < maxUnexpected {
				out = append(out, ChangedLine{Kind: kind, Line: l.orig, Block: l.blockOrig})
			}
		}
	}
	emit(bl, cb, ca, "-")
	emit(al, ca, cb, "+")
	return out, n
}

// sameLead: the same leading words — two for most gear, three for a set
// line ("set alias …", "set system ntp …").
func sameLead(typed, line []string) bool {
	n := 2
	if len(line) > 0 && line[0] == "set" {
		n = 3
	}
	if len(typed) < n || len(line) < n {
		n = min(len(typed), len(line))
		if n == 0 || len(typed) != len(line) {
			return false
		}
	}
	for k := 0; k < n; k++ {
		if !sameWord(typed[k], line[k]) {
			return false
		}
	}
	return true
}

// sameWord: equal, or a keyword or interface name abbreviated; numbers and
// addresses must be equal ("vlan 10" is not "vlan 100").
func sameWord(a, b string) bool {
	return a == b || tokMatch([]string{a}, []string{b}, whole) || tokMatch([]string{b}, []string{a}, whole)
}
