package history

import (
	"regexp"
	"strconv"
	"strings"
)

// rangeItem is one item of an "interface range" list: "gi0/1-4",
// "gi0/1 - 4", "gigabitethernet 0/1-4", "gi0/1 - gi0/4" (spaces already
// taken out around the dash).
var rangeDash = regexp.MustCompile(`\s*-\s*`)

var rangeItem = regexp.MustCompile(`^([a-z][a-z-]*?)(\s*)((?:\d+[/.:])*)(\d+)(?:-(?:[a-z][a-z-]*?\s*((?:\d+[/.:])*))?(\d+))?$`)

// rangeMembers spells out the interfaces of an "interface range …" parent
// ("interface range gi0/1-2, gi0/5" → "interface gi0/1", "interface gi0/2",
// "interface gi0/5"). ok is false when the parent is not a range or cannot
// be read.
func rangeMembers(parent string) ([]string, bool) {
	t := strings.Fields(normKey(parent))
	if len(t) < 3 || !startsWith(t[:2], "interface", "range") {
		return nil, false
	}
	list := rangeDash.ReplaceAllString(strings.Join(t[2:], " "), "-")
	var out []string
	for _, item := range strings.Split(list, ",") {
		m := rangeItem.FindStringSubmatch(strings.TrimSpace(item))
		if m == nil {
			return nil, false
		}
		if m[5] != "" && m[5] != m[3] {
			return nil, false // "gi0/1 - gi1/4": another slot at the upper end cannot be spelled out
		}
		lo, _ := strconv.Atoi(m[4])
		hi := lo
		if m[6] != "" {
			hi, _ = strconv.Atoi(m[6])
		}
		if hi < lo || hi-lo > 512 {
			return nil, false
		}
		for n := lo; n <= hi; n++ {
			// keep the space AlaxalA puts between name and number
			sp := ""
			if m[2] != "" {
				sp = " "
			}
			out = append(out, "interface "+m[1]+sp+m[3]+strconv.Itoa(n))
		}
	}
	return out, len(out) > 0
}

// parentHead is the head line of the block a command was typed in ("" for
// a global line), as the after log names it, else the before log, else as
// typed.
func parentHead(c typedCmd, before, after *cfgIndex) string {
	if c.parent == "" {
		return ""
	}
	for _, x := range []*cfgIndex{after, before} {
		if h, _ := x.block(c.parent); h != "" {
			return h
		}
	}
	return normKey(c.parent)
}

// parentHeads is every block a command was typed into: its parent's, or
// for an "interface range" each member interface's (as either log names
// it).
func parentHeads(c typedCmd, before, after *cfgIndex) []string {
	members, ok := rangeMembers(c.parent)
	if !ok {
		return []string{parentHead(c, before, after)}
	}
	var out []string
	for _, m := range members {
		h, _ := after.block(m)
		if h == "" {
			h, _ = before.block(m)
		}
		if h == "" {
			h = normKey(m)
		}
		out = append(out, h)
	}
	return out
}

// markSuperseded: a setting not found afterwards is not "missing" when a
// later line of the same work, in the same block, undid it ("shutdown"
// then "no shutdown" — a port bounce) or replaced its value ("ip address
// 192.0.2.1 …" corrected to "ip address 192.0.2.2 …"). Settings that add to
// a list (ntp server, ip route, logging host, vlan, secondary addresses …)
// are not replaced by the next one, so they stay missing.
//
// missingIn[i] is where command i is missing: its block, or for a range
// the members that lack it. It is superseded only when a later line undid
// or replaced it in every one of those — a change on one member does not
// hide the others.
func markSuperseded(cmds []typedCmd, ccs []CommandCheck, missingIn [][]string) {
	in := func(c typedCmd, h string) bool {
		if len(c.blocks) == 0 {
			return h == ""
		}
		for _, b := range c.blocks {
			if sameBlock(b, h) {
				return true
			}
		}
		return false
	}
	for i := range cmds {
		if ccs[i].Status != "missing" {
			continue
		}
		ti := strings.Fields(cmds[i].key)
		where := missingIn[i]
		if len(where) == 0 {
			where = []string{""}
		}
		all := true
		for _, h := range where {
			done := false
			for j := range cmds {
				if j == i || cmds[j].last <= cmds[i].last || !in(cmds[j], h) {
					continue
				}
				tj := strings.Fields(cmds[j].key)
				if undoes(tj, ti) || (ccs[j].Status != "missing" && replaces(tj, ti)) {
					done = true
					break
				}
			}
			all = all && done
		}
		if all {
			ccs[i].Status, ccs[i].Match, ccs[i].Note = "superseded", "", ""
		}
	}
}

// sameBlock: two names of one block, one perhaps abbreviated ("int lo9"
// and "interface loopback9" when neither log lists it).
func sameBlock(a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	fa, fb := strings.Fields(a), strings.Fields(b)
	return tokMatch(fa, fb, whole) || tokMatch(fb, fa, whole)
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

// isFreeKeyword: a keyword the user's own text follows (description,
// remark …), also abbreviated ("desc"). An abbreviation that may stand for
// another keyword ("comm" — community or comment) does not count.
func isFreeKeyword(w string) bool {
	if freeText[w] {
		return true
	}
	if len(w) < 4 {
		return false
	}
	for _, other := range []string{"community", "named-key", "namespace"} {
		if strings.HasPrefix(other, w) {
			return false
		}
	}
	for k := range freeText {
		if strings.HasPrefix(k, w) {
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

// touchSet: the after-log lines a typed setting was found as, with the
// blocks it was found in ("" when the block was not known — FortiGate's set
// lines, a global line). A line counts as the work's only in a block it was
// found in: the same text put in another block by someone else does not.
type touchSet map[string]map[string]bool

func (t touchSet) add(line, block string) {
	if t[line] == nil {
		t[line] = map[string]bool{}
	}
	t[line][block] = true
}

func (t touchSet) at(line, block string) bool {
	return t[line][block] || t[line][""]
}

// maxUnexpected caps the changed lines returned per device (the count is
// complete).
const maxUnexpected = 50

// showConfig matches the commands that list the configuration.
var showConfig = regexp.MustCompile(`(?i)^(sh(o|ow)?\s+(run|runn|running-config|conf|config|configuration|full-configuration|start|startup-config)|display\s+(cur|current-configuration|saved)|wr(ite)?\s+t(erm)?|show\s*$)`)

// configFormat: pipes that change only how the whole listing is printed.
var configFormat = regexp.MustCompile(`(?i)\|\s*(display\s+set|no-more)\b`)

// fullListing: the command lists the whole configuration — no filter and
// nothing after the listing's name but "all" / "full" ("show running-config
// all", "show configuration | display set"); "show run interface gi0/1",
// "show configuration interfaces" and "show run | include x" are parts.
func fullListing(cmd string) bool {
	cmd = configFormat.ReplaceAllString(cmd, "")
	if strings.Contains(cmd, "|") {
		return false
	}
	t := strings.Fields(strings.ToLower(cmd))
	switch {
	case len(t) <= 2:
		return true
	case len(t) == 3:
		return t[2] == "all" || t[2] == "full"
	}
	return false
}

// bareShow: FortiGate's / Junos's listing of the current context.
var bareShow = regexp.MustCompile(`(?i)^show(\s+full-configuration)?\s*$`)

// partBreak separates the parts of a configuration joined by
// configSection: a new part starts outside any block.
const partBreak = "\x00part"

// savedConfig: the saved configuration rather than the running one.
var savedConfig = regexp.MustCompile(`(?i)\s(start|startup-config|saved)\b`)

type cfgLine struct {
	block, key, orig, blockOrig string
	// owner: the nearest line above that is less indented — the block head,
	// or a nested head such as FortiGate's "edit port1".
	owner string
}

// configLines returns the configuration listing of a log, or the whole
// file when it has no prompt lines at all (a configuration saved to a
// file). A log may list the configuration more than once (show
// running-config and show startup-config, or a filtered listing as well):
// the last complete running listing is taken, else the last complete one,
// else the last one — so two logs are compared listing to listing, not by
// how many listings each holds. Comments, the stamps a listing carries
// (configuration size, last change) and blank lines are left out; Junos's
// brace format becomes set lines.
func configLines(log string) []cfgLine {
	sec, kind := configSection(log)
	if kind == noListing {
		return nil
	}
	return listingLines(sec)
}

// What configSection found in a log.
const (
	wholeFile  = iota // no prompt lines: a configuration saved to a file
	oneListing        // the listing picked among the show running-config style outputs
	noListing         // prompt lines but no configuration listing
)

// configSection picks the configuration listing of a log (see configLines;
// a log with parts only gives them all, one after another),
// for the verdict and the changes outside the work alike, so the two never
// read different listings.
func configSection(log string) ([]string, int) {
	lines := splitLines(log)
	type listing struct {
		cmd   string
		ctx   bool // typed inside a context (FortiGate "FG (interface) # show"): a section only
		lines []string
	}
	var ls []*listing
	var cur *listing
	sawPrompt := false
	junosCtx := false // inside "[edit interfaces …]": a bare show lists that subtree
	for _, l := range lines {
		if m := promptLine.FindStringSubmatch(strings.TrimRight(l, " \t")); m != nil {
			sawPrompt = true
			cur = nil
			if cmd := strings.TrimSpace(m[2]); showConfig.MatchString(cmd) {
				// A bare "show" (or FortiGate's "show full-configuration")
				// typed inside a context lists that context only. Other gear
				// with modes in the prompt ("HP(eth-1)# show running-config")
				// still lists the whole configuration.
				mode := promptMode(m[1])
				inCtx := (mode != "" && !strings.HasPrefix(mode, "config")) || junosCtx
				cur = &listing{cmd: cmd, ctx: inCtx && bareShow.MatchString(cmd)}
				ls = append(ls, cur)
			}
			continue
		}
		if m := junosEdit.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			junosCtx = m[1] != ""
		}
		if cur != nil {
			cur.lines = append(cur.lines, l)
		}
	}
	switch {
	case !sawPrompt:
		return lines, wholeFile
	case len(ls) == 0:
		return lines, noListing
	default:
		pick := func(ok func(*listing) bool) *listing {
			for i := len(ls) - 1; i >= 0; i-- {
				if ok(ls[i]) {
					return ls[i]
				}
			}
			return nil
		}
		full := func(x *listing) bool { return fullListing(x.cmd) && !x.ctx }
		p := pick(func(x *listing) bool { return full(x) && !savedConfig.MatchString(x.cmd) })
		if p == nil {
			p = pick(full)
		}
		if p != nil {
			return p.lines, oneListing
		}
		// Only parts ("show run interface Gi0/1", then Gi0/2): together
		// they are what the log shows of the configuration — each command
		// once (the last time it was run), the running ones when there are
		// any, and a break between them so one part's lines are not filed
		// under the block before.
		running := false
		for _, x := range ls {
			if !savedConfig.MatchString(x.cmd) {
				running = true
			}
		}
		last := map[string]int{}
		for i, x := range ls {
			last[strings.ToLower(ws.ReplaceAllString(x.cmd, " "))] = i
		}
		var all []string
		for i, x := range ls {
			if last[strings.ToLower(ws.ReplaceAllString(x.cmd, " "))] != i || (running && savedConfig.MatchString(x.cmd)) {
				continue
			}
			all = append(append(all, partBreak), x.lines...)
		}
		return all, oneListing
	}
}

// listingLines reads a configuration listing into its lines with their
// blocks and owners.
func listingLines(sec []string) []cfgLine {
	var out []cfgLine
	if js := junosSet(sec); js != nil {
		for _, l := range js {
			out = append(out, cfgLine{key: normKey(l), orig: strings.TrimSpace(ws.ReplaceAllString(l, " "))})
		}
		return out
	}
	head, headOrig := "", ""
	type level struct {
		indent int
		key    string
	}
	var owners []level
	for _, l := range sec {
		if l == partBreak {
			head, headOrig, owners = "", "", nil
			continue
		}
		// Only the listing's own stamps are dropped: the clock / counter
		// noise of Diff would cut real lines ("description used for …").
		k := normKey(stripStamps(l))
		if k == "" || k == "end" || k == "exit" || k == "}" || strings.HasPrefix(k, "!") || strings.HasPrefix(k, "#") ||
			strings.HasPrefix(k, "building configuration") || barePrompt.MatchString(k) {
			continue
		}
		orig := strings.TrimSpace(ws.ReplaceAllString(l, " "))
		indent := len(l) - len(strings.TrimLeft(l, " \t"))
		for len(owners) > 0 && owners[len(owners)-1].indent >= indent {
			owners = owners[:len(owners)-1]
		}
		owner := ""
		if len(owners) > 0 {
			owner = owners[len(owners)-1].key
		}
		owners = append(owners, level{indent, k})
		if indent == 0 {
			head, headOrig = k, orig
			out = append(out, cfgLine{key: k, orig: orig})
			continue
		}
		out = append(out, cfgLine{block: head, key: k, orig: orig, blockOrig: headOrig, owner: owner})
	}
	return out
}

// unexpectedChanges lists the configuration lines that differ between the
// before and after logs and that no typed setting accounts for: what the
// work changed besides what was typed (a setting entered on the wrong
// port, someone else's change), or what the device added by itself. A line
// is accounted for when a setting was found as it, when a setting of the
// same block has the same leading words (a value replaced), when a "no …"
// removes it, or when it is inside a block the work created or removed
// (typed into, or removed with "no interface …").
func unexpectedChanges(cmds []typedCmd, touched touchSet, beforeLog, afterLog string) ([]ChangedLine, int) {
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
	// The blocks each command was typed into, worked out once.
	inBlocks := make([]map[string]bool, len(cmds))
	heads := map[string]bool{}
	for i, c := range cmds {
		inBlocks[i] = map[string]bool{}
		for _, h := range c.blocks {
			inBlocks[i][h] = true
			heads[h] = true
		}
	}
	// A block that exists on one side only was created or removed whole.
	wholeBlock := func(block string) bool {
		return block != "" && (ca[key{"", block}] == 0) != (cb[key{"", block}] == 0) && (heads[block] || touched.at(block, ""))
	}
	// The values a typed setting put in, by place (block and owner line):
	// an old value removed at the same place was replaced by the work
	// ("description old" → "description new"), wherever the line was typed
	// from (FortiGate's set lines have no block in the work log).
	type place struct{ block, owner string }
	putIn := map[place][][]string{}
	for _, l := range al {
		if touched.at(l.key, l.block) && ca[key{l.block, l.key}] > cb[key{l.block, l.key}] {
			putIn[place{l.block, l.owner}] = append(putIn[place{l.block, l.owner}], strings.Fields(l.key))
		}
	}
	explained := func(l cfgLine, added bool) bool {
		if added && touched.at(l.key, l.block) {
			return true
		}
		if !added {
			for _, nv := range putIn[place{l.block, l.owner}] {
				if replaces(nv, strings.Fields(l.key)) {
					return true
				}
			}
		}
		if wholeBlock(l.block) || (l.block == "" && wholeBlock(l.key)) {
			return true
		}
		t := strings.Fields(l.key)
		for i, c := range cmds {
			if c.parent != "" && !inBlocks[i][l.block] {
				continue
			}
			ct := strings.Fields(c.key)
			if target, neg := negation(ct); neg {
				if tokMatch(target, t, trailing) {
					return true
				}
				// "default interface X" resets the block, "no interface X"
				// removes it: every line removed from inside is accounted
				// for (not one someone added there afterwards).
				if !added && c.parent == "" && l.block != "" && tokMatch(target, strings.Fields(l.block), whole) {
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
