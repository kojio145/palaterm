package history

import (
	"regexp"
	"strconv"
	"strings"
)

// How a typed setting is found in the after log. Devices keep a setting the
// way they print it, not the way it was typed, and matching whole lines
// only made settings that had been applied look missing (user report
// 2026-10-20). In order:
//
//  1. the typed line itself, or its abbreviations spelled out ("int gi0/1"
//     → "interface GigabitEthernet0/1", "sw mo acc" → "switchport mode
//     access"), inside the block it was typed in when the work log tells
//     (the prompt's mode) and the after log has that block;
//  2. what devices rewrite: a password or secret stored encrypted, a VLAN
//     list merged ("allowed vlan add 30" → "allowed vlan 10,20,30"),
//     quotes added (FortiGate), Junos's braces (set lines are derived);
//  3. a line this work added (after, not before) that holds every typed
//     word in order with words of its own in between ("spanning-tree
//     portfast" → "spanning-tree portfast edge");
//
// and what cannot be told is "unverified" rather than missing: a line found
// only in another block, a command that is not kept in the configuration
// (interface range, crypto key generate, clock set, banner), a default the
// device does not print (duplex auto), and an after log with no
// configuration in it at all.

// normKey is normCmd with quotes dropped (FortiGate and Junos print
// set alias "x" for a typed set alias x).
func normKey(s string) string {
	return normCmd(strings.ReplaceAll(s, `"`, ""))
}

// configMark matches a line that says a log holds a configuration listing.
var configMark = regexp.MustCompile(`^(building configuration|current configuration|hostname |sysname |set system host-name |config system |## last commit|#config-version)|running-config|startup-config|show conf|show run|full-configuration|current-configuration`)

// scope is a set of normalised lines to look a setting up in: a whole log
// or one block of it.
type scope struct {
	lines []string // distinct, in order
	count map[string]int
}

func newScope() *scope { return &scope{count: map[string]int{}} }

func (s *scope) add(k string) {
	if s.count[k] == 0 {
		s.lines = append(s.lines, k)
	}
	s.count[k]++
}

// cfgIndex is a log indexed for lookups: every line, and each unindented
// line's block (the indented lines under it).
type cfgIndex struct {
	all    *scope
	heads  []string
	blocks map[string]*scope
	config bool
	orig   map[string]string // a normalised line as the log prints it
}

func indexConfig(log string) *cfgIndex {
	x := &cfgIndex{all: newScope(), blocks: map[string]*scope{}, orig: map[string]string{}}
	lines := splitLines(log)
	head := ""
	for _, l := range lines {
		k := normKey(l)
		if k == "" {
			continue
		}
		if configMark.MatchString(k) {
			x.config = true
		}
		x.all.add(k)
		if _, ok := x.orig[k]; !ok {
			x.orig[k] = strings.TrimSpace(ws.ReplaceAllString(l, " "))
		}
		if l[0] != ' ' && l[0] != '\t' {
			head = k
			if _, ok := x.blocks[k]; !ok {
				x.blocks[k] = newScope()
				x.heads = append(x.heads, k)
			}
		} else if head != "" {
			x.blocks[head].add(k)
		}
	}
	for _, l := range junosSet(lines) {
		k := normKey(l)
		x.all.add(k)
		if _, ok := x.orig[k]; !ok {
			x.orig[k] = strings.TrimSpace(ws.ReplaceAllString(l, " "))
		}
		x.config = true
	}
	return x
}

// block returns the block of the head line that the typed parent names
// (abbreviations allowed), or nil.
func (x *cfgIndex) block(parent string) (string, *scope) {
	typed := strings.Fields(normKey(parent))
	if b, ok := x.blocks[strings.Join(typed, " ")]; ok {
		return strings.Join(typed, " "), b
	}
	for _, h := range x.heads {
		if tokMatch(typed, strings.Fields(h), whole) {
			return h, x.blocks[h]
		}
	}
	return "", nil
}

// junosSet turns Junos's brace format ("interfaces {" / "ge-0/0/0 {" /
// "description x;") into the set lines it stands for, so that typed set
// commands can be found. A log without braces gives nothing.
func junosSet(lines []string) []string {
	var out, stack []string
	braces := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if i := strings.Index(t, " ## "); i >= 0 {
			t = strings.TrimSpace(t[:i])
		}
		t = strings.TrimPrefix(t, "inactive: ")
		switch {
		case strings.HasSuffix(t, "{"):
			braces = true
			stack = append(stack, strings.TrimSpace(strings.TrimSuffix(t, "{")))
		case t == "}":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case strings.HasSuffix(t, ";") && len(stack) > 0:
			stmt := strings.TrimSpace(strings.TrimSuffix(t, ";"))
			path := "set " + strings.Join(stack, " ") + " "
			// "members [ v10 v20 ]" is one set line per member.
			if i, j := strings.Index(stmt, "["), strings.LastIndex(stmt, "]"); i >= 0 && j > i {
				for _, m := range strings.Fields(stmt[i+1 : j]) {
					out = append(out, path+strings.TrimSpace(stmt[:i])+" "+m)
				}
				continue
			}
			out = append(out, path+stmt)
		}
	}
	if !braces {
		return nil
	}
	return out
}

// freeText: the words after these are the user's own text, matched as
// typed (an abbreviation rule there would take "description up" for
// "description uplink").
var freeText = map[string]bool{"description": true, "name": true, "alias": true, "remark": true, "location": true, "contact": true, "comment": true, "comments": true, "banner": true}

func isWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// splitIf splits an interface name into its letters and its number
// ("gigabitethernet0/1" → "gigabitethernet", "0/1").
func splitIf(s string) (string, string) {
	i := strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' })
	if i <= 0 || !isWord(s[:i]) {
		return "", ""
	}
	return s[:i], s[i:]
}

type matchMode int

const (
	whole    matchMode = iota // the candidate has no more words than typed
	trailing                  // the candidate may go on after the typed words
	loose                     // and may have words of its own in between
)

// tokMatch reports whether typed words stand for a candidate line's words:
// equal, an abbreviation of a keyword (letters only: numbers, addresses and
// names with digits must be equal), an interface name with its letters
// abbreviated ("gi0/1", "gi 0/1", "po1"), and nothing but equal words after
// a free-text keyword.
func tokMatch(typed, cand []string, mode matchMode) bool {
	i, j := 0, 0
	free := false
	for i < len(typed) {
		if j >= len(cand) {
			return false
		}
		t, c := typed[i], cand[j]
		step := 1
		ok := t == c
		if !ok && !free {
			if isWord(t) && isWord(c) && strings.HasPrefix(c, t) {
				ok = true
			} else if lc, rc := splitIf(c); lc != "" {
				if lt, rt := splitIf(t); lt != "" && strings.HasPrefix(lc, lt) && rt == rc {
					ok = true
				} else if isWord(t) && strings.HasPrefix(lc, t) && i+1 < len(typed) && typed[i+1] == rc {
					ok, step = true, 2
				}
			}
		}
		if !ok {
			if mode == loose && j > 0 && !free {
				j++
				continue
			}
			return false
		}
		if freeText[c] {
			free = true
		}
		i += step
		j++
	}
	return j == len(cand) || (mode != whole && !free)
}

// find returns the line of s that the typed words stand for, the typed
// line itself first.
func (s *scope) find(typed []string, mode matchMode) string {
	if s == nil {
		return ""
	}
	if k := strings.Join(typed, " "); s.count[k] > 0 {
		return k
	}
	for _, l := range s.lines {
		if tokMatch(typed, strings.Fields(l), mode) {
			return l
		}
	}
	return ""
}

// countPrefix counts the lines of s that start with the typed words.
func (s *scope) countPrefix(typed []string) int {
	if s == nil {
		return 0
	}
	n := 0
	for _, l := range s.lines {
		if tokMatch(typed, strings.Fields(l), trailing) {
			n += s.count[l]
		}
	}
	return n
}

func (s *scope) n(k string) int {
	if s == nil {
		return 0
	}
	return s.count[k]
}

var secretWords = map[string]bool{"password": true, "secret": true, "passwd": true, "key": true, "key-string": true, "pre-shared-key": true, "authentication-key": true, "md5": true}

// findSecret finds a typed password or secret that the device stores
// encrypted ("username ops password x" → "username ops secret 9 $…",
// "set password x" → "set password ENC …"): the words before the secret
// keyword must match and the stored line has a secret keyword and a value.
func (s *scope) findSecret(typed []string) string {
	p := -1
	for i, t := range typed {
		if i > 0 && secretWords[t] {
			p = i
			break
		}
	}
	if s == nil || p < 0 {
		return ""
	}
	for _, l := range s.lines {
		c := strings.Fields(l)
		for q := 1; q+1 < len(c); q++ {
			if secretWords[c[q]] {
				if tokMatch(typed[:p], c[:q], whole) {
					return l
				}
				break
			}
		}
	}
	return ""
}

// vlanList reads "10,20-22" as a set of numbers.
func vlanList(s string) (map[int]bool, bool) {
	out := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		a, b, rng := strings.Cut(part, "-")
		lo, err1 := strconv.Atoi(a)
		hi := lo
		var err2 error
		if rng {
			hi, err2 = strconv.Atoi(b)
		}
		if err1 != nil || err2 != nil || hi < lo || hi-lo > 4094 {
			return nil, false
		}
		for v := lo; v <= hi; v++ {
			out[v] = true
		}
	}
	return out, len(out) > 0
}

// vlans collects the VLAN list of the lines that start with base: "base
// 10,20" and the continuation lines devices wrap long lists into ("base
// add 30-40").
func (s *scope) vlans(base []string) (map[int]bool, string) {
	got := map[int]bool{}
	first := ""
	if s == nil {
		return got, ""
	}
	for _, l := range s.lines {
		c := strings.Fields(l)
		if len(c) <= len(base) {
			continue
		}
		rest := c[len(base):]
		if !tokMatch(base, c[:len(base)], whole) {
			continue
		}
		if len(rest) == 2 && rest[0] == "add" {
			rest = rest[1:]
		}
		if len(rest) != 1 {
			continue
		}
		if vs, ok := vlanList(rest[0]); ok {
			for v := range vs {
				got[v] = true
			}
			if first == "" {
				first = l
			}
		}
	}
	return got, first
}

// vlanRule judges a typed VLAN list: "… vlan add L" (all of L present),
// "… vlan remove L" (none of L), "… vlan L" (exactly L; for top-level
// "vlan L", which creates VLANs, every one of L). ok is false when the line
// is not a VLAN list or the rule does not hold.
func vlanRule(typed []string, after, before *scope) (status, match string, ok bool) {
	n := len(typed)
	if n < 2 {
		return "", "", false
	}
	verb, base := "", typed[:n-1]
	if n >= 3 && (typed[n-2] == "add" || typed[n-2] == "remove") {
		verb, base = typed[n-2], typed[:n-2]
	}
	if kw := base[len(base)-1]; !isWord(kw) || !strings.HasPrefix("vlan", kw) {
		return "", "", false
	}
	want, isList := vlanList(typed[n-1])
	if !isList {
		return "", "", false
	}
	holds := func(have map[int]bool) bool {
		switch {
		case verb == "add" || len(base) == 1:
			for v := range want {
				if !have[v] {
					return false
				}
			}
			return true
		case verb == "remove":
			for v := range want {
				if have[v] {
					return false
				}
			}
			return true
		}
		if len(have) != len(want) {
			return false
		}
		for v := range want {
			if !have[v] {
				return false
			}
		}
		return true
	}
	ha, m := after.vlans(base)
	if !holds(ha) || (verb != "remove" && m == "") {
		return "", "", false
	}
	if hb, _ := before.vlans(base); holds(hb) {
		return "already", m, true
	}
	return "reflected", m, true
}

func startsWith(typed []string, pat ...string) bool {
	if len(typed) < len(pat) {
		return false
	}
	for i, p := range pat {
		if !strings.HasPrefix(p, typed[i]) {
			return false
		}
	}
	return true
}

// notStored: commands that act once and are not kept in the configuration
// as typed.
func notStored(typed []string) bool {
	for _, p := range [][]string{
		{"interface", "range"}, {"crypto", "key", "generate"}, {"crypto", "key", "zeroize"},
		{"crypto", "pki", "authenticate"}, {"crypto", "pki", "enroll"}, {"clock", "set"},
		{"banner"}, {"license"}, {"macro"}, {"default", "interface"}, {"request"},
	} {
		if startsWith(typed, p...) {
			return true
		}
	}
	return false
}

// defaultHidden: settings that restore a default the device does not
// print.
func defaultHidden(typed []string) bool {
	if len(typed) != 2 || typed[1] != "auto" {
		return false
	}
	for _, w := range []string{"duplex", "speed", "mdix", "negotiation"} {
		if startsWith(typed, w) {
			return true
		}
	}
	return false
}

// negation splits "no X" / "default X" (Cisco-style), "unset X"
// (FortiGate) and "delete X" (Junos) into what they remove.
func negation(typed []string) ([]string, bool) {
	if len(typed) < 2 {
		return nil, false
	}
	switch t := typed[0]; {
	case t == "no" || (len(t) >= 3 && strings.HasPrefix("default", t)):
		return typed[1:], true
	case t == "unset" || t == "delete":
		return append([]string{"set"}, typed[1:]...), true
	}
	return nil, false
}

// positive looks a typed setting up in one scope.
func positive(typed []string, key string, after, before *scope) (CommandCheck, bool) {
	by := func(m string) string {
		if before.n(m) >= after.n(m) {
			return "already"
		}
		return "reflected"
	}
	if m := after.find(typed, whole); m != "" {
		cc := CommandCheck{Status: by(m)}
		if m != key {
			cc.Match, cc.Note = m, "abbrev"
		}
		return cc, true
	}
	if st, m, ok := vlanRule(typed, after, before); ok {
		return CommandCheck{Status: st, Match: m, Note: "vlan"}, true
	}
	if m := after.findSecret(typed); m != "" {
		if before.n(m) > 0 {
			// The same encrypted line before and after: whether the value
			// changed cannot be told.
			return CommandCheck{Status: "unverified", Match: m, Note: "secret"}, true
		}
		return CommandCheck{Status: "reflected", Match: m, Note: "secret"}, true
	}
	return CommandCheck{}, false
}

// StrictVerify checks every setting line of workLog against afterLog, using
// beforeLog to tell a line the work added from one that was already there.
// It also lists the configuration lines that changed between beforeLog and
// afterLog without being typed in the work (see unexpectedChanges).
func StrictVerify(workLog, beforeLog, afterLog string) StrictResult {
	cmds, typed := extractTyped(workLog)
	res := StrictResult{Commands: []CommandCheck{}, Unexpected: []ChangedLine{}, Typed: typed}
	before := indexConfig(beforeLog)
	after := indexConfig(afterLog)
	hasConfig := after.config
	for _, c := range cmds {
		if !hasConfig && after.all.find(strings.Fields(c.key), whole) != "" {
			hasConfig = true
		}
	}
	res.NoConfig = !hasConfig && len(cmds) > 0
	touched := map[string]bool{} // after-log lines a typed setting was found as
	for _, c := range cmds {
		cc := judge(c, before, after, hasConfig)
		cc.Line, cc.Parent = c.line, c.parent
		if cc.Match != "" && cc.Note != "otherBlock" {
			touched[cc.Match] = true
		} else if cc.Status == "reflected" || cc.Status == "already" {
			touched[c.key] = true
		}
		if o := after.orig[cc.Match]; o != "" {
			cc.Match = o
		}
		res.Commands = append(res.Commands, cc)
	}
	markSuperseded(cmds, res.Commands, after)
	for _, cc := range res.Commands {
		switch cc.Status {
		case "missing":
			res.Missing++
		case "unverified":
			res.Unverified++
		default:
			res.Reflected++
		}
	}
	if hasConfig {
		res.Unexpected, res.UnexpectedCount = unexpectedChanges(cmds, touched, beforeLog, afterLog, after)
	}
	return res
}

func judge(c typedCmd, before, after *cfgIndex, hasConfig bool) CommandCheck {
	typed := strings.Fields(c.key)
	sa, sb := after.all, before.all
	scoped := false
	if c.parent != "" {
		if h, b := after.block(c.parent); b != nil {
			sa, sb, scoped = b, before.blocks[h], true
		}
	}
	if target, ok := negation(typed); ok {
		// Two conventions: Cisco-style gear drops the thing ("no shutdown"
		// makes the "shutdown" line disappear); NEC IX / Juniper-style gear
		// keeps the negation itself in the configuration as a line. Take
		// the literal line first, the removal rule otherwise.
		if m := sa.find(typed, whole); m != "" {
			if sa.n(m) > sb.n(m) {
				return CommandCheck{Status: "reflected"} // "no shutdown" is itself in the config now
			}
			return CommandCheck{Status: "already"} // and was already there before
		}
		na, nb := sa.countPrefix(target), sb.countPrefix(target)
		switch {
		case na == 0 && nb == 0:
			if !hasConfig {
				return CommandCheck{Status: "unverified", Note: "noConfig"}
			}
			return CommandCheck{Status: "already"} // nothing to remove either time
		case na == 0 || na < nb:
			return CommandCheck{Status: "reflected"} // the thing it negates is gone
		}
		return CommandCheck{Status: "missing"}
	}
	if cc, ok := positive(typed, c.key, sa, sb); ok {
		return cc
	}
	if notStored(typed) {
		return CommandCheck{Status: "unverified", Note: "notStored"}
	}
	// A line this work added that holds every typed word.
	if len(typed) >= 2 {
		for _, l := range sa.lines {
			if sa.n(l) > sb.n(l) && tokMatch(typed, strings.Fields(l), loose) {
				return CommandCheck{Status: "reflected", Match: l, Note: "similar"}
			}
		}
	}
	if scoped {
		if cc, ok := positive(typed, c.key, after.all, before.all); ok && cc.Status != "unverified" {
			m := cc.Match
			if m == "" {
				m = c.key
			}
			return CommandCheck{Status: "unverified", Match: m, Note: "otherBlock"}
		}
	}
	switch {
	case !hasConfig:
		return CommandCheck{Status: "unverified", Note: "noConfig"}
	case defaultHidden(typed):
		return CommandCheck{Status: "unverified", Note: "default"}
	}
	return CommandCheck{Status: "missing"}
}
