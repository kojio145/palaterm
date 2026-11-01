package history

import (
	"regexp"
	"strings"
)

// Structured comparison: the lines of a capture form a tree by their
// indentation (an "interface" block and its settings, FortiGate's config →
// edit → set, Junos's braces), the blocks of one level are paired by their
// heading, and only the lines inside a pair are compared line by line. A
// line inserted into one interface then no longer shifts every interface
// below it, a block that moved is a block that moved, and a changed
// setting is one changed line with the value lit — not a removed line and
// an added one.

// dline is one line that takes part in the comparison.
type dline struct {
	text  string // as captured
	key   string // what is compared
	idx   int    // 0-based line number in the capture
	depth int    // indentation, in characters
	brk   bool   // a break between the parts of a listing (configSection)
	noise []dline
	// noise: the secret / control lines that followed this one and were
	// left out of the comparison; they are shown dimmed after it.
}

// node is a block (a heading with the lines under it) or a single line.
type node struct {
	line    dline
	head    bool
	kids    []*node
	trailer []dline // the lines that close the block ("!", "end", "next", "}")
}

// closers end a block: the line is kept with the block it closes.
var closers = map[string]bool{"!": true, "end": true, "exit": true, "next": true, "quit": true, "}": true, "#": true}

func isCloser(key string) bool { return closers[strings.ToLower(key)] }

// forcedHead: a line that heads a block even when nothing is indented under
// it (an empty FortiGate edit, a Junos stanza).
func forcedHead(key string) bool {
	lk := strings.ToLower(key)
	return strings.HasPrefix(lk, "config ") || strings.HasPrefix(lk, "edit ") || strings.HasSuffix(lk, " {") || lk == "{"
}

// headWords are first words a configuration line starts with: a line that
// starts with one is a line of its own, not the wrapped tail of the line
// above (see wrapped).
var headWords = map[string]bool{}

// tailWords are words a long line's wrapped tail often starts with ("any
// eq 22", "host 192.0.2.1", "in", "log"): a line that starts with one may
// still be a continuation even though it looks like "<keyword> …".
var tailWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`interface router config edit set unset delete ip ipv6 vlan line access-list policy address service user system hostname no aaa snmp-server logging ntp spanning-tree crypto banner username route-map class-map policy-map mac port lacp enable service-policy switchport description shutdown exit end next quit ! # } { tacacs-server radius-server ip-access-list mls vrf track object-group zone security firewall protocols interfaces routing-options
		clock boot version radius tacacs errdisable vtp power license archive alias key event monitor feature ssh telnet sysname undo rule acl display return domain dhcp nat qos class diagnostic exception default dot1x cdp lldp udld rmon sflow ntp-service info-center snmp-agent stp link-aggregation voice-vlan local-user header super password transceiver irf mirror arp igmp pim bgp ospf isis rip mpls tunnel pp pppoe bridge administrator login schedule syslog dns ethernet lan wan`) {
		headWords[w] = true
	}
	for _, w := range strings.Fields(`any eq host in out log established range gt lt neq ack fin psh rst syn urg icmp tcp udp precedence tos dscp fragments time-range reflect evaluate`) {
		tailWords[w] = true
	}
}

// keywordStart matches a line that begins like a configuration command:
// a lower-case keyword followed by a space. A wrapped tail is the rest of
// a value (the middle of a word, a number, a name), not a command.
var keywordStart = regexp.MustCompile(`^[a-z][a-z-]*\s`)

// wrapped says whether a line at the left margin is the wrapped tail of
// the long line before it: the terminal broke the line, the capture kept
// the break. Thresholds from the reference spec (§7.2). A line that starts
// with a known first word, or that looks like "<keyword> …" (and the
// keyword is not one a long ACL line's tail begins with), is a line of
// its own.
func wrapped(prev, cur *dline) bool {
	if prev == nil || cur.depth != 0 || len(prev.text) < 70 || len(cur.text) >= 60 {
		return false
	}
	f := strings.Fields(cur.key)
	if len(f) == 0 || isCloser(cur.key) {
		return false
	}
	w := strings.ToLower(f[0])
	if headWords[w] {
		return false
	}
	if keywordStart.MatchString(cur.key) && !tailWords[w] {
		return false
	}
	return true
}

// bannerHead matches the first line of a Cisco-style banner. The word
// after the kind is the delimiter when it is one character ("#", "$",
// "^") or a caret pair ("^C"); a word ("banner motd Authorized …", ASA)
// is the banner's own text and the banner is one line.
var bannerHead = regexp.MustCompile(`(?i)^banner\s+(motd|login|exec|incoming|slip-ppp|prompt-timeout)\s+(\S+)(.*)$`)

// bannerBlock tells whether a line opens a multi-line banner and gives
// its kind and delimiter. Nothing is opened when the delimiter is closed
// on the same line ("banner motd #text#").
func bannerBlock(line string) (kind, delim string, ok bool) {
	m := bannerHead.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", "", false
	}
	tok, rest := m[2], m[3]
	switch c := tok[0]; {
	case c >= 0x80:
		return "", "", false // not an ASCII delimiter
	case len(tok) == 2 && c == '^':
		delim = tok
	case len(tok) == 1:
		delim = tok
	case !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'):
		// "#Authorized": the delimiter and the first line of the text.
		delim, rest = tok[:1], tok[1:]+rest
	default:
		return "", "", false
	}
	if strings.Contains(rest, delim) {
		return "", "", false
	}
	return strings.ToLower(m[1]), delim, true
}

// buildTree turns the lines into blocks. A line heads a block when the
// next line is indented under it (or it is a forcedHead); a closer at the
// depth of an open heading closes that block; a break between parts closes
// everything.
func buildTree(lines []dline) []*node {
	root := &node{}
	var stack []*node
	var prev *dline
	// An open multi-line banner: its text lines (and the closing
	// delimiter) go under it as they are — a "#" or "end" in the text is
	// the text, not a closer — until the line that holds the delimiter.
	var banner *node
	delim := ""
	for i := range lines {
		l := &lines[i]
		if l.brk {
			stack, prev, banner = nil, nil, nil
			continue
		}
		if banner != nil {
			// prepLines keeps the closing delimiter line (a bare "#" / "$"
			// is a prompt everywhere else), so the banner ends on it.
			l.depth = banner.line.depth + 1
			banner.kids = append(banner.kids, &node{line: *l})
			if strings.Contains(l.text, delim) {
				banner = nil
			}
			prev = l
			continue
		}
		if wrapped(prev, l) {
			l.depth = prev.depth
		}
		prev = l
		if isCloser(l.key) {
			closed := false
			for k := len(stack) - 1; k >= 0; k-- {
				if stack[k].line.depth < l.depth {
					break
				}
				if stack[k].line.depth == l.depth {
					stack[k].trailer = append(stack[k].trailer, *l)
					stack = stack[:k]
					closed = true
					break
				}
			}
			if closed {
				continue
			}
			// A closer with no block to close ("!" after a flat line)
			// belongs to the line before it, so two listings that differ
			// in the number of separators do not differ in lines.
			for len(stack) > 0 && stack[len(stack)-1].line.depth >= l.depth {
				stack = stack[:len(stack)-1]
			}
			parent := root
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			if k := len(parent.kids); k > 0 {
				parent.kids[k-1].trailer = append(parent.kids[k-1].trailer, *l)
				continue
			}
		}
		for len(stack) > 0 && stack[len(stack)-1].line.depth >= l.depth {
			stack = stack[:len(stack)-1]
		}
		n := &node{line: *l}
		if _, d, ok := bannerBlock(l.key); ok {
			n.head = true
			banner, delim = n, d
		} else if forcedHead(l.key) {
			n.head = true
		} else if j := i + 1; j < len(lines) && !lines[j].brk && lines[j].depth > l.depth && !isCloser(lines[j].key) {
			n.head = true
		}
		parent := root
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		parent.kids = append(parent.kids, n)
		if n.head && banner != n {
			stack = append(stack, n)
		}
	}
	return root.kids
}

// ifaceHead matches "interface <type><number>" (Cisco, AlaxalA, Aruba) so
// a stack number added in front of the port ("1/0/1" for "0/1") still
// pairs the interface with itself.
var ifaceHead = regexp.MustCompile(`(?i)^(interface\s+[A-Za-z][A-Za-z\-]*\s*)(\d+(?:/\d+)+)$`)

// headKey2 is the second key of a heading: the heading with what may
// legitimately differ taken out. Empty when there is nothing to take out.
func headKey2(key string) string {
	if m := ifaceHead.FindStringSubmatch(key); m != nil {
		segs := strings.Split(m[2], "/")
		if len(segs) >= 3 {
			segs = segs[1:]
		}
		return strings.ToLower(strings.TrimSpace(m[1])) + " " + strings.Join(segs, "/")
	}
	lk := strings.ToLower(key)
	if strings.HasPrefix(lk, "edit ") && strings.Contains(key, `"`) {
		return "edit " + strings.Trim(strings.TrimSpace(key[5:]), `"`)
	}
	return ""
}

// headPairOK says whether two headings that share a second key may be a
// renamed block. Two physical interfaces whose numbers have the same
// number of parts ("GigabitEthernet1/0/1" and "GigabitEthernet2/0/1",
// two stack members) are two ports, not one port renamed: only a stack
// number added or dropped ("0/1" against "1/0/1") pairs.
func headPairOK(ka, kb string) bool {
	ma, mb := ifaceHead.FindStringSubmatch(ka), ifaceHead.FindStringSubmatch(kb)
	if ma == nil || mb == nil {
		return true
	}
	return strings.Count(ma[2], "/") != strings.Count(mb[2], "/")
}

// leafKey2 names the setting a line makes, without its value: "ip address
// 192.0.2.1 255.255.255.0" → "ip address", "set ip …" → "set ip",
// "description …" → "description". A line that has no value part (
// "shutdown") gives "": it pairs by its words instead. A long "set" line
// is a Junos path ("set system ntp server 192.0.2.10"): its key is the
// path up to the value, so two unrelated "set system …" lines do not pair
// as one changed line.
func leafKey2(key string) string {
	f := strings.Fields(key)
	if len(f) < 2 {
		return ""
	}
	lf := strings.ToLower(f[0])
	switch {
	case lf == "set" && len(f) > 4:
		return strings.ToLower(strings.Join(f[:len(f)-1], " "))
	case lf == "set" || lf == "unset":
		return lf + " " + strings.ToLower(f[1])
	case freeText[lf] || lf == "hostname":
		return lf
	}
	n := 0
	for n < len(f) && isWordToken(f[n]) {
		n++
	}
	if n == 0 || n == len(f) {
		return ""
	}
	return strings.ToLower(strings.Join(f[:n], " "))
}

// isWordToken: a keyword rather than a value — letters, hyphens and
// underscores only.
func isWordToken(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_') {
			return false
		}
	}
	return s != ""
}

// Pairing thresholds (spec §4.8, §4.11).
const (
	pairSimilar   = 0.5 // lines this alike are one changed line
	pairHighlight = 0.3 // below this a row-for-row pair gets no highlight
	pairWindow    = 20  // how far down the added lines a deleted one looks
)

// pairer walks two sibling lists and writes the aligned rows.
type pairer struct {
	out                                  []DiffLine
	approx                               bool
	added, removed, changed, moved, same int
}

func (p *pairer) row(l DiffLine) {
	switch {
	case l.Noise:
	case l.Kind == "+":
		p.added++
	case l.Kind == "-":
		p.removed++
	case l.Kind == "~":
		p.changed++
		if l.Moved {
			p.moved++
		}
	case l.Moved:
		p.moved++
	default:
		p.same++
	}
	p.out = append(p.out, l)
}

// noiseRows writes the left-out lines that followed a line, dimmed.
func (p *pairer) noiseRows(l *dline, side byte) {
	for _, n := range l.noise {
		r := DiffLine{Kind: "=", Noise: true, Depth: n.depth}
		if side == 'a' {
			r.A, r.LineA = n.text, n.idx+1
		} else {
			r.B, r.LineB = n.text, n.idx+1
		}
		p.row(r)
	}
}

// one writes a block or line that is only on one side.
func (p *pairer) one(n *node, side byte) {
	kind := "-"
	if side == 'b' {
		kind = "+"
	}
	r := DiffLine{Kind: kind, Depth: n.line.depth, Head: n.head}
	if side == 'a' {
		r.A, r.LineA = n.line.text, n.line.idx+1
	} else {
		r.B, r.LineB = n.line.text, n.line.idx+1
	}
	p.row(r)
	p.noiseRows(&n.line, side)
	for _, k := range n.kids {
		p.one(k, side)
	}
	for i := range n.trailer {
		t := &n.trailer[i]
		r := DiffLine{Kind: kind, Depth: t.depth}
		if side == 'a' {
			r.A, r.LineA = t.text, t.idx+1
		} else {
			r.B, r.LineB = t.text, t.idx+1
		}
		p.row(r)
		p.noiseRows(t, side)
	}
}

// pairLine writes two lines that stand for each other: the same line, or a
// changed one with its value lit.
func (p *pairer) pairLine(a, b *dline, head, moved, highlight bool) {
	r := DiffLine{Kind: "=", A: a.text, B: b.text, LineA: a.idx + 1, LineB: b.idx + 1, Depth: b.depth, Head: head, Moved: moved}
	if a.key != b.key {
		r.Kind = "~"
		if highlight {
			r.SegA, r.SegB = inlineDiff(a.text, b.text)
		}
	}
	p.row(r)
	// The left-out lines under a pair are shown side by side as far as
	// both sides have them (a certificate on both sides is one dimmed
	// block, not two).
	n := min(len(a.noise), len(b.noise))
	for i := 0; i < n; i++ {
		x, y := a.noise[i], b.noise[i]
		p.row(DiffLine{Kind: "=", Noise: true, A: x.text, B: y.text, LineA: x.idx + 1, LineB: y.idx + 1, Depth: y.depth})
	}
	p.noiseRows(&dline{noise: a.noise[n:]}, 'a')
	p.noiseRows(&dline{noise: b.noise[n:]}, 'b')
}

// pair writes two nodes that stand for each other and, for blocks, pairs
// what is under them. A flat line and a block with the same heading pair
// too (an interface that gained its first setting).
func (p *pairer) pair(a, b *node, moved, hl bool) {
	p.pairLine(&a.line, &b.line, a.head || b.head, moved, hl)
	if a.head || b.head {
		p.kids(a.kids, b.kids, b.line.key)
	}
	n := min(len(a.trailer), len(b.trailer))
	for i := 0; i < n; i++ {
		p.pairLine(&a.trailer[i], &b.trailer[i], false, false, true)
	}
	for i := n; i < len(a.trailer); i++ {
		t := &a.trailer[i]
		p.row(DiffLine{Kind: "-", A: t.text, LineA: t.idx + 1, Depth: t.depth})
		p.noiseRows(t, 'a')
	}
	for i := n; i < len(b.trailer); i++ {
		t := &b.trailer[i]
		p.row(DiffLine{Kind: "+", B: t.text, LineB: t.idx + 1, Depth: t.depth})
		p.noiseRows(t, 'b')
	}
}

// kids pairs two sibling lists. parentKey is the heading over them ("" at
// the top).
func (p *pairer) kids(a, b []*node, parentKey string) {
	ka, kb := make([]string, len(a)), make([]string, len(b))
	for i, n := range a {
		ka[i] = pairKey(n)
	}
	for i, n := range b {
		kb[i] = pairKey(n)
	}
	ops, approx := myers(ka, kb)
	p.approx = p.approx || approx
	// pa[i] / pb[j]: the partner's index, -1 when unpaired. moved[i] says
	// the pair stands at a different place on each side.
	pa, pb := fill(len(a), -1), fill(len(b), -1)
	moved := make([]bool, len(a))
	hl := make([]bool, len(a))
	link := func(i, j int, mv, highlight bool) { pa[i], pb[j], moved[i], hl[i] = j, i, mv, highlight }
	for _, op := range ops {
		if op.kind == '=' {
			link(op.i, op.j, false, true)
		}
	}
	// Blocks that are on both sides but not where the script matched them
	// moved; blocks that match by their second key were renamed.
	byKey := map[string][]int{}
	byKey2 := map[string][]int{}
	for i, n := range a {
		if pa[i] >= 0 || !n.head {
			continue
		}
		byKey[n.line.key] = append(byKey[n.line.key], i)
		if k2 := headKey2(n.line.key); k2 != "" {
			byKey2[k2] = append(byKey2[k2], i)
		}
	}
	take := func(m map[string][]int, k string, ok func(i int) bool) int {
		if k == "" {
			return -1
		}
		rest := m[k][:0:0]
		found := -1
		for _, i := range m[k] {
			if pa[i] >= 0 {
				continue
			}
			if found < 0 && ok(i) {
				found = i
				continue
			}
			rest = append(rest, i)
		}
		m[k] = rest
		return found
	}
	for j, n := range b {
		if pb[j] >= 0 || !n.head {
			continue
		}
		if i := take(byKey, n.line.key, func(int) bool { return true }); i >= 0 {
			link(i, j, true, true)
		}
	}
	for j, n := range b {
		if pb[j] >= 0 || !n.head {
			continue
		}
		if i := take(byKey2, headKey2(n.line.key), func(i int) bool { return headPairOK(a[i].line.key, n.line.key) }); i >= 0 {
			link(i, j, false, true)
		}
	}
	// Lines that are on both sides at different places moved (an ACL entry
	// put elsewhere).
	byLine := map[string][]int{}
	for i, n := range a {
		if pa[i] < 0 && !n.head {
			byLine[n.line.key] = append(byLine[n.line.key], i)
		}
	}
	for j, n := range b {
		if pb[j] < 0 && !n.head {
			if i := take(byLine, n.line.key, func(int) bool { return true }); i >= 0 {
				link(i, j, true, true)
			}
		}
	}
	// Within each run of edits, a deleted line and an added one that make
	// the same setting (or say mostly the same) are one changed line.
	for s := 0; s < len(ops); {
		if ops[s].kind == '=' {
			s++
			continue
		}
		e := s
		var ra, rb, ha, hb []int
		for ; e < len(ops) && ops[e].kind != '='; e++ {
			switch {
			case ops[e].kind == '-' && pa[ops[e].i] < 0:
				if a[ops[e].i].head {
					ha = append(ha, ops[e].i)
				} else {
					ra = append(ra, ops[e].i)
				}
			case ops[e].kind == '+' && pb[ops[e].j] < 0:
				if b[ops[e].j].head {
					hb = append(hb, ops[e].j)
				} else {
					rb = append(rb, ops[e].j)
				}
			}
		}
		pairRun(a, b, ha, hb, pa, link, false)
		pairRun(a, b, ra, rb, pa, link, true)
		s = e
	}
	// Written in the order of the script: a line only in A where A had it,
	// everything else where B has it.
	for _, op := range ops {
		switch op.kind {
		case '=':
			p.pair(a[op.i], b[op.j], false, true)
		case '-':
			if pa[op.i] < 0 {
				p.one(a[op.i], 'a')
			}
		case '+':
			if i := pb[op.j]; i >= 0 {
				p.pair(a[i], b[op.j], moved[i], hl[i])
			} else {
				p.one(b[op.j], 'b')
			}
		}
	}
}

// pairKey: a heading and a plain line with the same text pair (a block
// that gained its first indented line is still the same block).
func pairKey(n *node) string { return n.line.key }

func fill(n, v int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = v
	}
	return s
}

// pairRun pairs the unpaired deleted lines ra with the added lines rb of
// one run: by the setting's name first, then by how alike they are, then
// — for plain lines, when as many are left on each side — row for row.
// Pairs keep their order (a line pairs only below the previous pair).
func pairRun(a, b []*node, ra, rb []int, pa []int, link func(i, j int, mv, hl bool), rowForRow bool) {
	usedB := map[int]bool{}
	pairBy := func(ok func(i, j int) bool) {
		last := -1
		for _, i := range ra {
			if pa[i] >= 0 || a[i].line.key == "" {
				continue
			}
			for pos, j := range rb {
				if usedB[j] || pos <= last {
					continue
				}
				if pos-last > pairWindow {
					break
				}
				if ok(i, j) {
					usedB[j] = true
					last = pos
					link(i, j, false, true)
					break
				}
			}
		}
	}
	// pass 1: the same setting (or heading) with another value
	key2 := leafKey2
	if !rowForRow {
		key2 = headKey2
	}
	pairBy(func(i, j int) bool {
		x := key2(a[i].line.key)
		return x != "" && x == key2(b[j].line.key) && (rowForRow || headPairOK(a[i].line.key, b[j].line.key))
	})
	// pass 2: mostly the same words
	pairBy(func(i, j int) bool { return similarity(a[i].line.key, b[j].line.key) >= pairSimilar })
	// pass 3: as many left on each side — row for row
	if !rowForRow {
		return
	}
	var la, lb []int
	for _, i := range ra {
		if pa[i] < 0 {
			la = append(la, i)
		}
	}
	for _, j := range rb {
		if !usedB[j] {
			lb = append(lb, j)
		}
	}
	if len(la) == len(lb) {
		for k := range la {
			link(la[k], lb[k], false, similarity(a[la[k]].line.key, b[lb[k]].line.key) >= pairHighlight)
		}
	}
}
