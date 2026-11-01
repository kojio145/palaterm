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
// line's block (the indented lines under it). FortiOS's nested tables and
// entries are blocks too, named by their path ("config system ntp / config
// ntpserver / edit 1"), holding the lines under them.
type cfgIndex struct {
	all    *scope
	top    *scope // the unindented lines
	heads  []string
	blocks map[string]*scope
	config bool
	orig   map[string]string // a normalised line as the log prints it
}

// nestSep joins the heads of a nested block's path.
const nestSep = " / "

func indexConfig(log string) *cfgIndex {
	x := &cfgIndex{all: newScope(), top: newScope(), blocks: map[string]*scope{}, orig: map[string]string{}}
	// The listing picked for the changes outside the work too, so the
	// verdict and that list read the same configuration; a log with no
	// listing at all is indexed whole.
	lines, kind := configSection(log)
	if kind == oneListing {
		x.config = true
	}
	head := ""
	type nest struct {
		indent int
		path   string
	}
	var nests []nest // the open nested heads, by indent
	for _, l := range lines {
		if l == partBreak {
			head, nests = "", nil
			continue
		}
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
		indent := len(l) - len(strings.TrimLeft(l, " \t"))
		for len(nests) > 0 && nests[len(nests)-1].indent >= indent {
			nests = nests[:len(nests)-1]
		}
		if indent == 0 {
			head = k
			x.top.add(k)
			if _, ok := x.blocks[k]; !ok {
				x.blocks[k] = newScope()
				x.heads = append(x.heads, k)
			}
			continue
		}
		if head == "" {
			continue
		}
		x.blocks[head].add(k)
		for _, n := range nests {
			x.blocks[n.path].add(k)
		}
		if fgHead.MatchString(k) {
			path := head
			if len(nests) > 0 {
				path = nests[len(nests)-1].path
			}
			path += nestSep + k
			if _, ok := x.blocks[path]; !ok {
				x.blocks[path] = newScope()
				x.heads = append(x.heads, path)
			}
			nests = append(nests, nest{indent, path})
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

// inBlock reports whether a normalised line is inside some block of the
// log (not a top-level line, not a line of a filtered part).
func (x *cfgIndex) inBlock(k string) bool {
	if k == "" {
		return false
	}
	for _, b := range x.blocks {
		if b.count[k] > 0 {
			return true
		}
	}
	return false
}

// block returns the block of the head line that the typed parent names
// (abbreviations allowed), or nil.
func (x *cfgIndex) block(parent string) (string, *scope) {
	typed := strings.Fields(normKey(parent))
	if b, ok := x.blocks[strings.Join(typed, " ")]; ok {
		return strings.Join(typed, " "), b
	}
	for _, exact := range []bool{true, false} {
		for _, h := range x.heads {
			if tokMatchOpt(typed, strings.Fields(h), whole, exact) {
				return h, x.blocks[h]
			}
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
// "description uplink", "hostname lab" for "hostname lab-core").
var freeText = map[string]bool{"description": true, "name": true, "alias": true, "remark": true, "location": true, "contact": true, "comment": true, "comments": true, "banner": true,
	"hostname": true, "host-name": true, "sysname": true, "community": true}

// nameWord / namePair: the word after these is a name the device prints as
// it was typed (a user, an ACL, a route-map, a VRF, a FortiOS entry), so it
// must be equal — "username ops" is not "username opsadmin", and
// "ip access-group lab in" is not "… labx in" (review 2026-11-01, S-2). The
// words after the name follow the usual rules again.
var (
	nameWord = map[string]bool{"username": true, "access-group": true, "route-map": true, "class-map": true, "policy-map": true, "vrf": true,
		"object-group": true, "prefix-list": true, "zone": true, "edit": true}
	namePair = map[string]bool{"access-list extended": true, "access-list standard": true, "key chain": true, "crypto map": true}
)

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
// a free-text keyword. A line typed down to a free-text keyword ("no
// description") stands for every line that goes on from there, in the
// trailing mode.
func tokMatch(typed, cand []string, mode matchMode) bool {
	return tokMatchOpt(typed, cand, mode, false)
}

// tokMatchOpt is tokMatch; with exact, abbreviations are not accepted (a
// keyword must be spelled out), so that a line that is there as typed wins
// over one that an abbreviation would also fit (see find, block).
func tokMatchOpt(typed, cand []string, mode matchMode, exact bool) bool {
	i, j := 0, 0
	free, freeAt := false, -1 // freeAt: typed words consumed when the free text began
	name := false             // the next word is a name: equal only
	for i < len(typed) {
		if j >= len(cand) {
			return false
		}
		t, c := typed[i], cand[j]
		step := 1
		ok := t == c
		if !ok && !free && !name && !exact {
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
			if mode == loose && j > 0 && !free && !name {
				j++
				continue
			}
			return false
		}
		name = nameWord[c] || (j > 0 && namePair[cand[j-1]+" "+c])
		i += step
		j++
		if !free && freeText[c] {
			free, freeAt = true, i
		}
	}
	return j == len(cand) || (mode != whole && (!free || (mode == trailing && freeAt == len(typed))))
}

// find returns the line of s that the typed words stand for, the typed
// line itself first, then a line it spells out exactly, then one it
// abbreviates.
func (s *scope) find(typed []string, mode matchMode) string {
	if s == nil {
		return ""
	}
	if k := strings.Join(typed, " "); s.count[k] > 0 {
		return k
	}
	for _, exact := range []bool{true, false} {
		for _, l := range s.lines {
			if tokMatchOpt(typed, strings.Fields(l), mode, exact) {
				return l
			}
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

// secretAt is where a line's secret value is: the position of the first
// secret keyword that has a value after it (when a key number and another
// keyword follow — "ntp authentication-key 1 md5 X" — that keyword, so the
// number is compared too), the value's position (after an encryption type
// — "secret 5 $1$…", "password 7 …", "password ENC …"), and where the words
// after the value start (an IOS "md5 <hash> 7" puts the type after the
// value). -1 when there is none.
func secretAt(f []string) (kw, val, rest int) {
	kw = -1
	for i := 1; i+1 < len(f); i++ {
		if secretWords[f[i]] {
			kw = i
			break
		}
	}
	if kw < 0 {
		return -1, -1, -1
	}
	if kw+3 < len(f) && encType(f[kw+1]) && secretWords[f[kw+2]] {
		kw += 2
	}
	val = kw + 1
	if encType(f[val]) && val+1 < len(f) {
		val++
	}
	rest = val + 1
	if rest < len(f) && encType(f[rest]) {
		rest++
	}
	return kw, val, rest
}

// encType: an encryption-type word before or after a stored secret ("5",
// "7", "9", FortiOS's "ENC").
func encType(w string) bool {
	return (len(w) == 1 && w[0] >= '0' && w[0] <= '9') || strings.EqualFold(w, "enc")
}

// findSecret finds a typed password or secret that the device stores
// encrypted ("username ops password x" → "username ops secret 9 $…",
// "set password x" → "set password ENC …"): the words before the secret
// keyword must match, the stored line has a secret keyword and a value,
// and the words after the value match too ("crypto isakmp key X address
// 192.0.2.1" is not "… key 6 … address 192.0.2.99"; review 2026-11-01,
// A-10).
func (s *scope) findSecret(typed []string) string {
	p, _, tr := secretAt(typed)
	if s == nil || p < 0 {
		return ""
	}
	for _, l := range s.lines {
		c := strings.Fields(l)
		q, _, cr := secretAt(c)
		if q < 0 || !tokMatch(typed[:p], c[:q], whole) {
			continue
		}
		if tr >= len(typed) || tokMatch(typed[tr:], c[min(cr, len(c)):], loose) {
			return l
		}
	}
	return ""
}

// MaskSecretLine returns a typed line with the value after a secret
// keyword (the words findSecret knows: password, secret, key, md5 …)
// replaced by "●●●", for the verdict list, the CSV and the HTML report: a
// password typed during the work is not one of the device's known secrets,
// so the log masking does not cover it. Spacing is kept as typed.
func MaskSecretLine(line string) string {
	idx := wordRun.FindAllStringIndex(line, -1)
	if len(idx) < 3 {
		return line
	}
	f := make([]string, len(idx))
	for i, ix := range idx {
		f[i] = strings.ToLower(line[ix[0]:ix[1]])
	}
	hide := map[int]bool{}
	for i := 1; i+1 < len(f); i++ {
		if !secretWords[f[i]] {
			continue
		}
		if i+3 < len(f) && encType(f[i+1]) && secretWords[f[i+2]] {
			continue // "authentication-key 1 md5 X": the number is a key id
		}
		v := i + 1
		if encType(f[v]) && v+1 < len(f) {
			v++
		}
		hide[v] = true
		i = v
	}
	if len(hide) == 0 {
		return line
	}
	var sb strings.Builder
	last := 0
	for i, ix := range idx {
		sb.WriteString(line[last:ix[0]])
		if hide[i] {
			sb.WriteString("●●●")
		} else {
			sb.WriteString(line[ix[0]:ix[1]])
		}
		last = ix[1]
	}
	sb.WriteString(line[last:])
	return sb.String()
}

var wordRun = regexp.MustCompile(`\S+`)

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
	hb, mb := before.vlans(base)
	if !holds(ha) || (verb != "remove" && m == "") || (verb == "remove" && m == "" && mb == "") {
		return "", "", false
	}
	if holds(hb) {
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
// as typed. A one-word pattern must be the whole typed word: by prefix,
// "macro" took "mac address-table static …" too, and a missing MAC entry
// was reported "unverified" instead of missing. The multi-word patterns
// keep the abbreviation rule ("int ra gi0/1-2", "cry key gen"), as the
// rest of the matching does: two words together name the command.
func notStored(typed []string) bool {
	for _, p := range [][]string{
		{"interface", "range"}, {"crypto", "key", "generate"}, {"crypto", "key", "zeroize"},
		{"crypto", "pki", "authenticate"}, {"crypto", "pki", "enroll"}, {"clock", "set"},
		{"banner"}, {"license"}, {"macro"}, {"default", "interface"}, {"request"},
	} {
		if len(p) == 1 {
			if len(typed) >= 1 && typed[0] == p[0] {
				return true
			}
			continue
		}
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

// negation splits "no X" / "default X" (Cisco-style), "undo X" (Comware /
// VRP), "unset X" (FortiGate) and "delete X" (Junos) into what they remove.
func negation(typed []string) ([]string, bool) {
	if len(typed) < 2 {
		return nil, false
	}
	switch t := typed[0]; {
	case t == "no" || t == "undo" || (len(t) >= 3 && strings.HasPrefix("default", t)):
		return typed[1:], true
	case t == "unset" || t == "delete":
		return append([]string{"set"}, typed[1:]...), true
	}
	return nil, false
}

// positive looks a typed setting up in one scope. ax / bx are the whole
// logs when the scope is the whole after log (the block the line was typed
// in is not known): a VLAN list is then judged block by block, not as the
// union of every interface's VLANs.
func positive(typed []string, key string, after, before *scope, ax, bx *cfgIndex) (CommandCheck, bool) {
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
	if ax == nil {
		if st, m, ok := vlanRule(typed, after, before); ok {
			return CommandCheck{Status: st, Match: m, Note: "vlan"}, true
		}
	} else {
		if st, m, ok := vlanRule(typed, ax.top, bx.top); ok {
			return CommandCheck{Status: st, Match: m, Note: "vlan"}, true
		}
		for _, h := range ax.heads {
			if st, m, ok := vlanRule(typed, ax.blocks[h], bx.blocks[h]); ok {
				return CommandCheck{Status: st, Match: m, Note: "vlan"}, true
			}
		}
	}
	if m := after.findSecret(typed); m != "" {
		if before.n(m) > 0 {
			// The same encrypted line before and after: whether the value
			// changed cannot be told.
			return CommandCheck{Status: "unverified", Match: m, Note: "secret"}, true
		}
		return CommandCheck{Status: "reflected", Match: m, Note: "secret"}, true
	}
	if m := after.findSpaced(key); m != "" {
		return CommandCheck{Status: by(m), Match: m, Note: "spacing"}, true
	}
	return CommandCheck{}, false
}

// findSpaced finds the line that is the typed one but for spaces. A long
// line typed into a terminal is echoed with a space where it wrapped at the
// screen edge ("… any ac tion replace-dscp af41" for "… any action …"; user
// report 2026-10-20, AlaxalA via another terminal's log). Only the exact
// characters in the same order count, so no other line passes for it.
func (s *scope) findSpaced(key string) string {
	if s == nil {
		return ""
	}
	want := strings.ReplaceAll(key, " ", "")
	if len(want) < 8 {
		return ""
	}
	for _, l := range s.lines {
		if strings.ReplaceAll(l, " ", "") == want {
			return l
		}
	}
	return ""
}

// StrictVerify checks every setting line of workLog against afterLog, using
// beforeLog to tell a line the work added from one that was already there.
// It also lists the configuration lines that changed between beforeLog and
// afterLog without being typed in the work (see unexpectedChanges).
func StrictVerify(workLog, beforeLog, afterLog string) StrictResult {
	return StrictVerifyLogs([]string{workLog}, beforeLog, afterLog)
}

// StrictVerifyLogs is StrictVerify for a work done over several sessions
// (one log per session, oldest first).
func StrictVerifyLogs(workLogs []string, beforeLog, afterLog string) StrictResult {
	before := indexConfig(beforeLog)
	after := indexConfig(afterLog)
	// A line naming a block of either log opens that block when typed in a
	// sub-mode ("interface vlan10" in config-if).
	// Only a head with lines under it counts, or an interface (an unused
	// port is listed bare): a one-line global setting ("feature lacp") or an
	// output line at column 0 is not a block.
	isHead := func(cmd string) bool {
		t := strings.Fields(normKey(cmd))
		if len(t) < 2 {
			return false
		}
		for _, x := range []*cfgIndex{after, before} {
			if _, b := x.block(cmd); b != nil && (len(b.lines) > 0 || startsWith(t, "interface")) {
				return true
			}
		}
		return false
	}
	cmds, fgHeads, typed := extractTypedAll(workLogs, isHead)
	for i := range cmds {
		if cmds[i].scope() != "" {
			cmds[i].blocks = parentHeads(cmds[i], before, after)
		}
	}
	res := StrictResult{Commands: []CommandCheck{}, Unexpected: []ChangedLine{}, Typed: typed}
	hasConfig := after.config
	for _, c := range cmds {
		if !hasConfig && after.all.find(strings.Fields(c.key), whole) != "" {
			hasConfig = true
		}
	}
	res.NoConfig = !hasConfig && len(cmds) > 0
	touched := touchSet{}                    // after-log lines a typed setting was found as, by block
	missingIn := make([][]string, len(cmds)) // where each missing line is missing
	found := func(cc CommandCheck) bool {
		return (cc.Match != "" && cc.Note != "otherBlock") || cc.Status == "reflected" || cc.Status == "already"
	}
	for i, c := range cmds {
		var cc CommandCheck
		if _, inRange := rangeMembers(c.parent); inRange {
			// A line typed under "interface range" is in every member
			// interface: each one is judged, and the line is reflected only
			// if it is in all.
			var all []CommandCheck
			for _, h := range c.blocks {
				if after.blocks[h] == nil {
					continue
				}
				sub := judge(typedCmd{line: c.line, key: c.key, parent: h, blocks: []string{h}, last: c.last}, before, after, hasConfig)
				all = append(all, sub)
				if sub.Status == "missing" || sub.Note == "otherBlock" {
					missingIn[i] = append(missingIn[i], h)
				} else if found(sub) {
					touched.add(firstNonEmpty(sub.Match, c.key), h)
				}
			}
			if len(all) > 0 {
				cc = combineMembers(all)
			} else {
				// No member in the after log: judged as one line, missing in
				// every member — undone only by a later line in each of them
				// (or under the same range).
				cc = judge(c, before, after, hasConfig)
				if found(cc) {
					touched.add(firstNonEmpty(cc.Match, c.key), "")
				}
				missingIn[i] = c.blocks
			}
		} else {
			cc = judge(c, before, after, hasConfig)
			// The block it was found in; when the after log lacks the block
			// the match is not tied to one ("").
			block := ""
			if len(c.blocks) > 0 && after.blocks[c.blocks[0]] != nil {
				block = c.blocks[0]
			}
			if found(cc) {
				touched.add(firstNonEmpty(cc.Match, c.key), block)
			}
			// Where it is missing is the block it was typed in, known to the
			// logs or not, so a later undo there supersedes it (a global
			// line: markSuperseded takes it as "").
			missingIn[i] = c.blocks
		}
		cc.Line, cc.Parent = c.line, c.parent
		if o := after.orig[cc.Match]; o != "" {
			cc.Match = o
		}
		res.Commands = append(res.Commands, cc)
	}
	markSuperseded(cmds, res.Commands, missingIn)
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
		res.Unexpected, res.UnexpectedCount = unexpectedChanges(cmds, fgHeads, touched, beforeLog, afterLog)
	}
	return res
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func judge(c typedCmd, before, after *cfgIndex, hasConfig bool) CommandCheck {
	typed := strings.Fields(c.key)
	// "default interface X" resets an interface: it is not kept as a line,
	// and the interface stays — it is not a removal.
	if notStored(typed) && len(typed[0]) >= 3 && strings.HasPrefix("default", typed[0]) {
		return CommandCheck{Status: "unverified", Note: "notStored"}
	}
	sa, sb := after.all, before.all
	ax, bx := after, before // the whole logs, for a VLAN list judged per block
	scoped := false
	// unresolved: the line was typed in a block the after log does not
	// list (a new block the partial listing lacks, "interface range macro",
	// a FortiOS entry that is gone). The whole log is searched, but a line
	// found there is in some other block, and says nothing about this one:
	// it is "unverified", never reflected or already (review 2026-11-01,
	// S-3). Nothing found anywhere is still missing.
	unresolved := false
	if p := c.scope(); p != "" {
		if h, b := after.block(p); b != nil {
			sa, sb, scoped = b, before.blocks[h], true
			ax, bx = nil, nil
		} else {
			unresolved = true
		}
	}
	// elsewhere: a match found while the block is unresolved, when the
	// line sits inside some block of the after log (a line of a filtered
	// part — "show run | include …" — or a top-level line is in no block,
	// and still counts).
	elsewhere := func(m string) (CommandCheck, bool) {
		if !unresolved || !after.inBlock(m) {
			return CommandCheck{}, false
		}
		return CommandCheck{Status: "unverified", Match: m, Note: "otherBlock"}, true
	}
	if target, ok := negation(typed); ok {
		// Two conventions: Cisco-style gear drops the thing ("no shutdown"
		// makes the "shutdown" line disappear); NEC IX / Juniper-style gear
		// keeps the negation itself in the configuration as a line. Take
		// the literal line first, the removal rule otherwise.
		if m := sa.find(typed, whole); m != "" {
			if cc, ok := elsewhere(m); ok {
				return cc
			}
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
		case unresolved && after.inBlock(sa.find(target, trailing)):
			cc, _ := elsewhere(sa.find(target, trailing))
			return cc
		case na == 0 || na < nb:
			return CommandCheck{Status: "reflected"} // the thing it negates is gone
		}
		return CommandCheck{Status: "missing"}
	}
	if cc, ok := positive(typed, c.key, sa, sb, ax, bx); ok {
		if cc.Status != "unverified" {
			if e, ok := elsewhere(firstNonEmpty(cc.Match, c.key)); ok {
				return e
			}
		}
		return cc
	}
	if notStored(typed) {
		return CommandCheck{Status: "unverified", Note: "notStored"}
	}
	// A line this work added that holds every typed word.
	if len(typed) >= 2 {
		for _, l := range sa.lines {
			if sa.n(l) > sb.n(l) && tokMatch(typed, strings.Fields(l), loose) {
				if cc, ok := elsewhere(l); ok {
					return cc
				}
				return CommandCheck{Status: "reflected", Match: l, Note: "similar"}
			}
		}
	}
	// Found as it is in another block: the logs cannot tell. A VLAN list
	// does not count here — another interface's list says nothing about
	// this one ("remove 30" holds wherever 30 was never allowed).
	if scoped {
		if cc, ok := positive(typed, c.key, after.all, before.all, after, before); ok && cc.Status != "unverified" && cc.Note != "vlan" {
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

// combineMembers is one verdict for a line typed under "interface range":
// missing if any member lacks it, else to check if any cannot be told,
// else already if every member had it before, else reflected.
func combineMembers(all []CommandCheck) CommandCheck {
	// Found only in another block, when the blocks are the range's own
	// members, means this member lacks it.
	for _, cc := range all {
		if cc.Status == "missing" || cc.Note == "otherBlock" {
			return CommandCheck{Status: "missing"}
		}
	}
	for _, cc := range all {
		if cc.Status == "unverified" {
			return cc
		}
	}
	for _, cc := range all {
		if cc.Status != "already" {
			return cc
		}
	}
	return all[0]
}
