package history

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Commands from a comparison: what to type to take the device from its
// after state back to its before state (rollback), or from before to after
// (forward — the same thing with the two sides swapped). The lines come
// from the structured comparison (DiffWith, configuration only) and a
// small set of rules per family of devices; a line the rules cannot turn
// into a command is kept as a "check by hand" note rather than dropped.

// CmdLine is one line of the generated commands.
type CmdLine struct {
	Text    string `json:"text"`
	Context bool   `json:"context,omitempty"` // a heading, exit, configure terminal …
	Note    string `json:"note,omitempty"`    // why this needs a look by hand
	Row     int    `json:"row,omitempty"`     // the comparison row it came from (rollback only; -1 none)
}

// CmdGen is the result.
type CmdGen struct {
	Family    string    `json:"family"`   // cisco / fortigate / junos / yamaha / generic
	FamilyBy  string    `json:"familyBy"` // "osType" / "content"
	Direction string    `json:"direction"`
	Lines     []CmdLine `json:"lines"`
	Manual    int       `json:"manual"`    // lines to check by hand
	Skipped   int       `json:"skipped"`   // changed rows left out (secrets)
	Reordered bool      `json:"reordered"` // the phases changed the order
	Empty     bool      `json:"empty"`     // differences, but nothing the rules can turn into commands
	NoConfig  bool      `json:"noConfig"`  // a side has no configuration listing
	Warnings  []string  `json:"warnings,omitempty"`
}

// GenerateCommands makes the rollback (after → before) or forward
// (before → after) commands for the two captures. osType is the device's
// profile key when known ("" for an external folder).
func GenerateCommands(a, b, direction, osType string) CmdGen {
	g := CmdGen{Direction: direction}
	if direction == "forward" {
		a, b = b, a
	}
	r := DiffWith(a, b, DiffOpts{IgnoreNoise: true, IgnoreSecrets: true, ConfigOnly: true})
	if r.Mode != "config" {
		g.NoConfig = true
		g.Empty = true
		return g
	}
	la := splitLines(a)
	idx, _ := configSectionIdx(la)
	var cfg []string
	for _, k := range idx {
		if k >= 0 {
			cfg = append(cfg, la[k])
		}
	}
	g.Family, g.FamilyBy = detectFamily(cfg, osType)
	w := &cmdWriter{res: r, rows: direction == "rollback"}
	switch g.Family {
	case "cisco":
		w.cisco()
	case "fortigate":
		w.fortigate()
	case "junos":
		w.junos(a, b)
	case "yamaha":
		w.yamaha()
	default:
		w.generic()
	}
	g.Lines, g.Manual, g.Skipped, g.Reordered, g.Warnings = w.out, w.manual, w.skipped, w.reordered, w.warnings
	if len(g.Lines) == 0 {
		g.Empty = true
	}
	return g
}

// detectFamily tells the family of device from the configuration text
// (or from the profile key when one is known).
func detectFamily(cfg []string, osType string) (string, string) {
	lo := strings.ToLower(osType)
	switch {
	case lo == "":
	case strings.Contains(lo, "forti"):
		return "fortigate", "osType"
	case strings.Contains(lo, "junos") || strings.Contains(lo, "juniper"):
		return "junos", "osType"
	case strings.Contains(lo, "yamaha") || strings.Contains(lo, "rtx"):
		return "yamaha", "osType"
	case strings.Contains(lo, "cisco") || strings.Contains(lo, "ios") || strings.Contains(lo, "alaxala") || strings.Contains(lo, "aruba") || strings.Contains(lo, "allied") || strings.Contains(lo, "nec-ix") || strings.Contains(lo, "nxos"):
		return "cisco", "osType"
	}
	var fgConfig, fgEnd, edits, setLines, braces, yam, bang, iface, host, nonEmpty int
	for _, l := range cfg {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		nonEmpty++
		lt := strings.ToLower(t)
		switch {
		case strings.HasPrefix(lt, "config ") && !strings.HasPrefix(l, " "):
			fgConfig++
		case lt == "end":
			fgEnd++
		}
		if strings.HasPrefix(lt, "set ") {
			setLines++
		}
		if strings.HasPrefix(lt, "edit ") {
			edits++
		}
		if strings.HasSuffix(lt, " {") {
			braces++
		}
		for _, p := range []string{"ip route ", "ip filter ", "pp select ", "tunnel select ", "administrator "} {
			if strings.HasPrefix(lt, p) {
				yam++
				break
			}
		}
		switch {
		case t == "!":
			bang++
		case strings.HasPrefix(lt, "interface "):
			iface++
		case strings.HasPrefix(lt, "hostname "):
			host++
		}
	}
	switch {
	case fgConfig >= 1 && fgEnd > 0 && (setLines > 0 || edits > 0):
		return "fortigate", "content"
	case nonEmpty > 0 && (setLines*2 >= nonEmpty || braces >= 2):
		return "junos", "content"
	case yam >= 3 && bang == 0:
		return "yamaha", "content"
	case bang > 0 || iface > 0 || host > 0:
		return "cisco", "content"
	}
	return "generic", "content"
}

// cnode is a block (heading + what is under it) or a single row of the
// comparison, rebuilt from the flat result the way difftree builds it.
type cnode struct {
	row     int
	head    bool
	kids    []*cnode
	trailer []int
}

// cmdTree rebuilds the blocks of a comparison result.
func cmdTree(lines []DiffLine) []*cnode {
	root := &cnode{row: -1}
	var stack []*cnode
	depthOf := func(i int) int { return lines[i].Depth }
	for i := range lines {
		l := lines[i]
		if l.Noise {
			continue
		}
		key := strings.TrimSpace(rowText(l))
		if isCloser(key) {
			closed := false
			for k := len(stack) - 1; k >= 0; k-- {
				if depthOf(stack[k].row) < l.Depth {
					break
				}
				if depthOf(stack[k].row) == l.Depth {
					stack[k].trailer = append(stack[k].trailer, i)
					stack = stack[:k]
					closed = true
					break
				}
			}
			if closed {
				continue
			}
			for len(stack) > 0 && depthOf(stack[len(stack)-1].row) >= l.Depth {
				stack = stack[:len(stack)-1]
			}
			parent := root
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			if k := len(parent.kids); k > 0 {
				parent.kids[k-1].trailer = append(parent.kids[k-1].trailer, i)
				continue
			}
		}
		for len(stack) > 0 && depthOf(stack[len(stack)-1].row) >= l.Depth {
			stack = stack[:len(stack)-1]
		}
		n := &cnode{row: i, head: l.Head}
		parent := root
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		parent.kids = append(parent.kids, n)
		if n.head {
			stack = append(stack, n)
		}
	}
	return root.kids
}

// rowText is the line a row stands for: B's (the current state) unless
// the row is only in A.
func rowText(l DiffLine) string {
	if l.Kind == "-" {
		return l.A
	}
	return l.B
}

func (n *cnode) all(lines []DiffLine, kind string) bool {
	if lines[n.row].Kind != kind {
		return false
	}
	for _, k := range n.kids {
		if !k.all(lines, kind) {
			return false
		}
	}
	return true
}

func (n *cnode) changed(lines []DiffLine) bool {
	if lines[n.row].Kind != "=" {
		return true
	}
	for _, k := range n.kids {
		if k.changed(lines) {
			return true
		}
	}
	return false
}

// chunk is a run of commands that moves as one when the phases are
// sorted: a block with its heading and exit, or a single global command.
type chunk struct {
	lines []CmdLine
	phase int
	sub   int
	order int
}

type cmdWriter struct {
	res       DiffResult
	rows      bool // keep row references (rollback)
	out       []CmdLine
	manual    int
	skipped   int
	reordered bool
	warnings  []string
	chunks    []chunk
}

func (w *cmdWriter) line(text string, row int) CmdLine {
	if !w.rows {
		row = -1
	}
	return CmdLine{Text: text, Row: row}
}

func (w *cmdWriter) ctx(text string, row int) CmdLine {
	l := w.line(text, row)
	l.Context = true
	return l
}

func (w *cmdWriter) note(text, why string, row int) CmdLine {
	w.manual++
	l := w.line(text, row)
	l.Note = why
	return l
}

func (w *cmdWriter) add(c chunk) {
	c.order = len(w.chunks)
	w.chunks = append(w.chunks, c)
}

// flush sorts the chunks by phase (stable) and writes them out.
func (w *cmdWriter) flush() {
	cs := append([]chunk(nil), w.chunks...)
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].phase != cs[j].phase {
			return cs[i].phase < cs[j].phase
		}
		return cs[i].sub < cs[j].sub
	})
	for i, c := range cs {
		if c.order != w.chunks[i].order {
			w.reordered = true
		}
		w.out = append(w.out, c.lines...)
	}
}

// Phases (spec §1.4): a chunk's place in the whole.
const (
	phPre    = 0 // vlan definitions
	phRefDel = 2 // references removed
	phObjDel = 3 // objects removed
	phObjAdd = 4 // objects (re)created
	phRefAdd = 5 // references restored
	phOther  = 6
	phFinal  = 8
)

// noToggle puts "no " in front of a command or takes it off.
func noToggle(cmd string) string {
	t := strings.TrimSpace(cmd)
	lead := cmd[:len(cmd)-len(strings.TrimLeft(cmd, " \t"))]
	if strings.HasPrefix(strings.ToLower(t), "no ") {
		return lead + strings.TrimSpace(t[3:])
	}
	return lead + "no " + t
}

func firstWords(s string, n int) string {
	f := strings.Fields(strings.ToLower(s))
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

// ---- cisco ----

var (
	physIface   = regexp.MustCompile(`(?i)^interface\s+((Gigabit|FastEthernet|TenGigabit|TwentyFiveGig|FortyGigabit|HundredGig|Ethernet|Serial|mgmt)\S*)$`)
	numberedACL = regexp.MustCompile(`(?i)^access-list\s+(\d+)\s`)
	bannerHead  = regexp.MustCompile(`(?i)^banner\s+(motd|login|exec|incoming)\s+(\S)`)
	vlanListRe  = regexp.MustCompile(`(?i)^(\s*)(switchport trunk allowed vlan|switchport trunk pruning vlan|spanning-tree vlan)\s+([\d,\-]+)(\s+.*)?$`)
	addrFamily  = regexp.MustCompile(`(?i)^\s*address-family\b`)
)

func (w *cmdWriter) cisco() {
	lines := w.res.Lines
	tree := cmdTree(lines)
	w.skipped = w.res.SecretLines
	consumed := map[int]bool{}
	// Numbered ACLs and banners are groups of top-level rows.
	for i, n := range tree {
		if n.head || consumed[n.row] {
			continue
		}
		text := strings.TrimSpace(rowText(lines[n.row]))
		if m := numberedACL.FindStringSubmatch(text); m != nil {
			num := m[1]
			var group []int
			changed := false
			for _, x := range tree[i:] {
				t := strings.TrimSpace(rowText(lines[x.row]))
				if mm := numberedACL.FindStringSubmatch(t); mm == nil || mm[1] != num {
					continue
				}
				group = append(group, x.row)
				if lines[x.row].Kind != "=" {
					changed = true
				}
			}
			for _, r := range group {
				consumed[r] = true
			}
			if changed {
				c := chunk{phase: phObjAdd, sub: 4}
				c.lines = append(c.lines, w.line("no access-list "+num, group[0]))
				for _, r := range group {
					if lines[r].Kind != "+" {
						c.lines = append(c.lines, w.line(strings.TrimSpace(lines[r].A), r))
					}
				}
				w.add(c)
			}
			continue
		}
		if m := bannerHead.FindStringSubmatch(text); m != nil {
			delim := m[2]
			group := []int{n.row}
			changed := lines[n.row].Kind != "="
			for _, x := range tree[i+1:] {
				t := rowText(lines[x.row])
				group = append(group, x.row)
				if lines[x.row].Kind != "=" {
					changed = true
				}
				if strings.Contains(t, delim) {
					break
				}
			}
			for _, r := range group {
				consumed[r] = true
			}
			if changed {
				c := chunk{phase: phOther}
				c.lines = append(c.lines, w.line("no banner "+strings.ToLower(m[1]), group[0]))
				for _, r := range group {
					if lines[r].Kind != "+" {
						c.lines = append(c.lines, w.line(lines[r].A, r))
					}
				}
				w.add(c)
			}
		}
	}
	for _, n := range tree {
		if consumed[n.row] || !n.changed(lines) {
			continue
		}
		if !n.head {
			if cl, ok := w.ciscoLeaf(n.row, false); ok {
				ph, sub := ciscoPhase(cl[0].Text, false)
				w.add(chunk{lines: cl, phase: ph, sub: sub})
			}
			continue
		}
		w.ciscoBlock(n, 0)
	}
	if len(w.chunks) == 0 {
		return
	}
	w.out = append(w.out, w.ctx("configure terminal", -1))
	w.flush()
	w.out = append(w.out, w.ctx("end", -1))
}

// ciscoBlock writes a block with differences as one chunk (or one line
// when the whole block is new or gone).
func (w *cmdWriter) ciscoBlock(n *cnode, depth int) {
	lines := w.res.Lines
	head := strings.TrimSpace(rowText(lines[n.row]))
	switch {
	case n.all(lines, "+"):
		if m := physIface.FindStringSubmatch(head); m != nil {
			w.add(chunk{lines: []CmdLine{w.line("default interface "+m[1], n.row)}, phase: phOther, sub: 9})
			return
		}
		ph, sub := ciscoPhase("no "+head, false)
		w.add(chunk{lines: []CmdLine{w.line("no "+head, n.row)}, phase: ph, sub: sub})
		return
	case n.all(lines, "-"):
		c := chunk{}
		c.phase, c.sub = ciscoPhase(head, false)
		c.lines = append(c.lines, w.ctx(head, n.row))
		w.wholeBlock(n, &c)
		c.lines = append(c.lines, w.ctx(exitFor(head, lines[n.row].Depth), -1))
		w.add(c)
		return
	case lines[n.row].Kind == "~":
		// A renamed block ("ip access-list extended OLD" → "… NEW"): the
		// new one goes, the old one comes back whole.
		ph, sub := ciscoPhase("no "+head, false)
		w.add(chunk{lines: []CmdLine{w.line("no "+head, n.row)}, phase: ph, sub: sub})
		old := strings.TrimSpace(lines[n.row].A)
		c := chunk{}
		c.phase, c.sub = ciscoPhase(old, false)
		c.lines = append(c.lines, w.ctx(old, n.row))
		w.wholeBlock(n, &c)
		c.lines = append(c.lines, w.ctx(exitFor(old, lines[n.row].Depth), -1))
		w.add(c)
		return
	}
	if m := physIface.FindStringSubmatch(head); m != nil && lines[n.row].Kind == "=" && len(n.kids) > 0 {
		// A physical interface that had no settings and now has some:
		// back to its defaults in one line.
		allNew := true
		for _, k := range n.kids {
			if !k.all(lines, "+") {
				allNew = false
			}
		}
		if allNew {
			w.add(chunk{lines: []CmdLine{w.line("default interface "+m[1], n.row)}, phase: phOther, sub: 9})
			return
		}
	}
	c := chunk{phase: phOther, sub: 9}
	c.lines = append(c.lines, w.ctx(head, n.row))
	var nos, rest []CmdLine
	for _, k := range n.kids {
		if !k.changed(lines) {
			continue
		}
		if k.head {
			sub := &cmdWriter{res: w.res, rows: w.rows}
			sub.ciscoBlock(k, depth+1)
			for _, sc := range sub.chunks {
				rest = append(rest, sc.lines...)
			}
			w.manual += sub.manual
			continue
		}
		cl, ok := w.ciscoLeaf(k.row, true)
		if !ok {
			continue
		}
		for _, l := range cl {
			if strings.HasPrefix(strings.TrimSpace(l.Text), "no ") {
				nos = append(nos, l)
			} else {
				rest = append(rest, l)
			}
		}
	}
	body := append(nos, rest...)
	for _, l := range body {
		if ph, sub := ciscoPhase(l.Text, true); ph < c.phase || (ph == c.phase && sub < c.sub) {
			c.phase, c.sub = ph, sub
		}
	}
	if ph, sub := ciscoPhase(head, false); ph == phPre || ph == phObjAdd {
		// vlan / interface Vlan / Port-channel headings place the block.
		c.phase, c.sub = ph, sub
	}
	c.lines = append(c.lines, body...)
	c.lines = append(c.lines, w.ctx(exitFor(head, lines[n.row].Depth), -1))
	w.add(c)
}

// wholeBlock writes every line of a block as it was in A.
func (w *cmdWriter) wholeBlock(n *cnode, c *chunk) {
	lines := w.res.Lines
	for _, k := range n.kids {
		if lines[k.row].Kind == "+" {
			continue
		}
		c.lines = append(c.lines, w.line(lines[k.row].A, k.row))
		if k.head {
			w.wholeBlock(k, c)
			t := strings.TrimSpace(lines[k.row].A)
			c.lines = append(c.lines, w.ctx(exitFor(t, lines[k.row].Depth), -1))
		}
	}
}

func exitFor(head string, depth int) string {
	ind := strings.Repeat(" ", depth)
	if addrFamily.MatchString(head) {
		return ind + "exit-address-family"
	}
	return ind + "exit"
}

// ciscoLeaf turns one changed row into its command(s).
func (w *cmdWriter) ciscoLeaf(row int, sub bool) ([]CmdLine, bool) {
	l := w.res.Lines[row]
	a, b := l.A, l.B
	ta, tb := strings.TrimSpace(a), strings.TrimSpace(b)
	switch l.Kind {
	case "+":
		return []CmdLine{w.line(noToggle(b), row)}, true
	case "-":
		return []CmdLine{w.line(a, row)}, true
	case "~":
		if ma, mb := vlanListRe.FindStringSubmatch(a), vlanListRe.FindStringSubmatch(b); ma != nil && mb != nil && strings.EqualFold(ma[2], mb[2]) && ma[4] == mb[4] {
			setA, setB := vlanSet(ma[3]), vlanSet(mb[3])
			var out []CmdLine
			lead, key := ma[1], ma[2]
			allowed := strings.EqualFold(key, "switchport trunk allowed vlan")
			if extra := vlanDiff(setB, setA); extra != "" {
				if allowed {
					out = append(out, w.line(lead+key+" remove "+extra, row))
				} else {
					out = append(out, w.line(lead+"no "+key+" "+extra+ma[4], row))
				}
			}
			if gone := vlanDiff(setA, setB); gone != "" {
				if allowed {
					out = append(out, w.line(lead+key+" add "+gone, row))
				} else {
					out = append(out, w.line(lead+key+" "+gone+ma[4], row))
				}
			}
			return out, len(out) > 0
		}
		if sub && firstWords(ta, 2) != firstWords(tb, 2) {
			return []CmdLine{w.line(noToggle(b), row), w.line(a, row)}, true
		}
		if ph, _ := ciscoPhase(tb, true); sub && ph == phRefAdd {
			// A reference to another object ("ip access-group NEW in"):
			// the new reference is taken off before the old one goes back.
			return []CmdLine{w.line(noToggle(b), row), w.line(a, row)}, true
		}
		return []CmdLine{w.line(a, row)}, true
	}
	return nil, false
}

// vlanSet expands "10,20-22" into its numbers.
func vlanSet(s string) map[int]bool {
	out := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		if i := strings.Index(part, "-"); i > 0 {
			lo, e1 := strconv.Atoi(part[:i])
			hi, e2 := strconv.Atoi(part[i+1:])
			if e1 == nil && e2 == nil && hi >= lo && hi-lo < 4096 {
				for v := lo; v <= hi; v++ {
					out[v] = true
				}
			}
			continue
		}
		if v, err := strconv.Atoi(part); err == nil {
			out[v] = true
		}
	}
	return out
}

// vlanDiff lists the numbers in x but not in y, ranges compressed.
func vlanDiff(x, y map[int]bool) string {
	var vs []int
	for v := range x {
		if !y[v] {
			vs = append(vs, v)
		}
	}
	sort.Ints(vs)
	var parts []string
	for i := 0; i < len(vs); {
		j := i
		for j+1 < len(vs) && vs[j+1] == vs[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, strconv.Itoa(vs[i])+"-"+strconv.Itoa(vs[j]))
		} else {
			parts = append(parts, strconv.Itoa(vs[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// ciscoPhase classifies one command (spec §1.4).
func ciscoPhase(cmd string, sub bool) (int, int) {
	t := strings.ToLower(strings.TrimSpace(cmd))
	has := func(p ...string) bool {
		for _, x := range p {
			if strings.HasPrefix(t, x) {
				return true
			}
		}
		return false
	}
	if sub {
		switch {
		case has("no service-policy", "no crypto map", "no standby") && strings.Contains(t, "track") || has("no service-policy", "no crypto map"):
			return phRefDel, 0
		case has("no ip access-group", "no ipv6 traffic-filter") || (has("no ") && strings.Contains(t, "route-map")):
			return phRefDel, 1
		case has("no channel-group"):
			return phRefDel, 2
		case has("channel-group"):
			return phRefAdd, 0
		case has("ip access-group", "ipv6 traffic-filter") || strings.Contains(t, " route-map "):
			return phRefAdd, 1
		case has("service-policy", "crypto map") || (has("standby") && strings.Contains(t, "track")):
			return phRefAdd, 2
		}
		return phOther, 5
	}
	switch {
	case has("no policy-map"):
		return phObjDel, 0
	case has("no class-map", "no access-list", "no ip access-list", "no ipv6 access-list", "no route-map", "no ip prefix-list", "no crypto map"):
		return phObjDel, 1
	case has("no track"):
		return phObjDel, 2
	case has("no interface vlan"):
		return phObjDel, 3
	case has("no interface port-channel"):
		return phObjDel, 4
	case has("no vlan"):
		return phObjDel, 5
	case has("vlan ") && !has("vlan access-map"):
		return phPre, 0
	case has("interface port-channel"):
		return phObjAdd, 1
	case has("interface vlan"):
		return phObjAdd, 2
	case has("track"):
		return phObjAdd, 3
	case has("class-map", "access-list", "ip access-list", "ipv6 access-list", "route-map", "ip prefix-list", "crypto map"):
		return phObjAdd, 4
	case has("policy-map"):
		return phObjAdd, 5
	}
	return phOther, 5
}

// ---- fortigate ----

var (
	fgEdit           = regexp.MustCompile(`(?i)^edit\s+(.+)$`)
	fgSet            = regexp.MustCompile(`(?i)^set\s+(\S+)`)
	fgPolicySections = []string{"firewall policy", "firewall policy6", "firewall proxy-policy", "firewall local-in-policy", "firewall DoS-policy", "firewall DoS-policy6", "firewall shaping-policy", "firewall security-policy"}
)

type fgPath struct {
	heads []string // "config X" / "edit Y" from the outside in
}

func (w *cmdWriter) fortigate() {
	lines := w.res.Lines
	tree := cmdTree(lines)
	w.skipped = w.res.SecretLines
	for _, n := range tree {
		w.fgWalk(n, nil)
	}
	w.fgMoves(tree)
	w.flush()
}

// fgWalk writes the chunks of a config / edit block and what is under it.
func (w *cmdWriter) fgWalk(n *cnode, path []string) {
	lines := w.res.Lines
	if !n.changed(lines) {
		return
	}
	head := strings.TrimSpace(rowText(lines[n.row]))
	lh := strings.ToLower(head)
	if !n.head {
		// A line outside any block (rare: a stray set): keep for a look.
		w.add(chunk{lines: []CmdLine{w.note(head, "ブロックの外の行", n.row)}, phase: phOther})
		return
	}
	isEdit := strings.HasPrefix(lh, "edit ")
	if isEdit && n.all(lines, "+") {
		if m := fgEdit.FindStringSubmatch(head); m != nil {
			w.fgChunk(path, []CmdLine{w.line("delete "+m[1], n.row)}, true)
		}
		return
	}
	if isEdit && n.all(lines, "-") {
		var body []CmdLine
		body = append(body, w.ctx(head, n.row))
		w.fgWhole(n, &body, 1)
		body = append(body, w.ctx("next", -1))
		w.fgChunk(path, body, false)
		return
	}
	cur := append(append([]string(nil), path...), head)
	var dels, unsets, rest []CmdLine
	for _, k := range n.kids {
		if !k.changed(lines) {
			continue
		}
		if k.head {
			continue
		}
		l := lines[k.row]
		t := strings.TrimSpace(rowText(l))
		lt := strings.ToLower(t)
		switch l.Kind {
		case "+":
			if m := fgSet.FindStringSubmatch(t); m != nil {
				unsets = append(unsets, w.line("unset "+m[1], k.row))
			} else if strings.HasPrefix(lt, "unset ") {
				rest = append(rest, w.note(t, "unset の取り消しは元の値が分からない", k.row))
			} else {
				rest = append(rest, w.note(t, "逆の操作が分からない", k.row))
			}
		case "-":
			rest = append(rest, w.line(strings.TrimSpace(l.A), k.row))
		case "~":
			rest = append(rest, w.line(strings.TrimSpace(l.A), k.row))
		}
	}
	if len(dels)+len(unsets)+len(rest) > 0 {
		w.fgChunk(cur, append(append(dels, unsets...), rest...), false)
	}
	for _, k := range n.kids {
		if k.head {
			w.fgWalk(k, cur)
		}
	}
}

// fgWhole writes the lines under a block as they were in A.
func (w *cmdWriter) fgWhole(n *cnode, body *[]CmdLine, depth int) {
	lines := w.res.Lines
	ind := strings.Repeat("    ", depth)
	for _, k := range n.kids {
		t := strings.TrimSpace(lines[k.row].A)
		if k.head {
			*body = append(*body, w.ctx(ind+t, k.row))
			w.fgWhole(k, body, depth+1)
			if strings.HasPrefix(strings.ToLower(t), "edit ") {
				*body = append(*body, w.ctx(ind+"next", -1))
			} else {
				*body = append(*body, w.ctx(ind+"end", -1))
			}
			continue
		}
		*body = append(*body, w.line(ind+t, k.row))
	}
}

// fgChunk wraps a body in its path (config … / edit …) and closes it.
func (w *cmdWriter) fgChunk(path []string, body []CmdLine, deletes bool) {
	var c chunk
	for i, h := range path {
		c.lines = append(c.lines, w.ctx(strings.Repeat("    ", i)+h, -1))
	}
	ind := strings.Repeat("    ", len(path))
	for _, l := range body {
		l.Text = ind + l.Text
		c.lines = append(c.lines, l)
	}
	for i := len(path) - 1; i >= 0; i-- {
		close := "end"
		if strings.HasPrefix(strings.ToLower(path[i]), "edit ") {
			close = "next"
		}
		c.lines = append(c.lines, w.ctx(strings.Repeat("    ", i)+close, -1))
	}
	section := ""
	if len(path) > 0 {
		section = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(path[0]), "config ")))
	}
	hasDel := deletes
	for _, l := range body {
		if strings.HasPrefix(strings.TrimSpace(l.Text), "delete ") {
			hasDel = true
		}
	}
	c.phase, c.sub = fgPhase(section, hasDel)
	w.add(c)
}

// fgPhase: the spec's table of FortiGate sections (deleting direction /
// creating direction).
func fgPhase(section string, deleting bool) (int, int) {
	kind := "other"
	switch {
	case strings.HasPrefix(section, "system interface"):
		kind = "interface"
	case strings.HasPrefix(section, "system zone"):
		kind = "zone"
	case strings.HasPrefix(section, "vpn ipsec phase1"):
		kind = "phase1"
	case strings.HasPrefix(section, "vpn ipsec phase2"):
		kind = "phase2"
	case strings.Contains(section, "addrgrp") || strings.Contains(section, "service group") || strings.Contains(section, "vipgrp") || strings.Contains(section, "profile-group") || strings.Contains(section, "internet-service-group"):
		kind = "group"
	case strings.HasPrefix(section, "firewall address") || strings.HasPrefix(section, "firewall service custom") || strings.HasPrefix(section, "firewall vip") || strings.HasPrefix(section, "firewall ippool") || strings.HasPrefix(section, "firewall schedule") || strings.HasPrefix(section, "antivirus") || strings.HasPrefix(section, "webfilter") || strings.HasPrefix(section, "ips sensor") || strings.HasPrefix(section, "application list") || strings.HasPrefix(section, "dnsfilter") || strings.HasPrefix(section, "dlp") || strings.HasPrefix(section, "ssl-ssh-profile") || strings.HasPrefix(section, "firewall ssl-ssh-profile"):
		kind = "object"
	case strings.HasPrefix(section, "router ") || strings.HasPrefix(section, "system sdwan") || strings.HasPrefix(section, "system virtual-wan-link") || strings.HasPrefix(section, "system dhcp") || strings.HasPrefix(section, "system dns") || strings.HasPrefix(section, "system link-monitor") || strings.HasPrefix(section, "vpn ssl") || strings.HasPrefix(section, "log ") || strings.HasPrefix(section, "system ha"):
		kind = "infra"
	}
	for _, p := range fgPolicySections {
		if strings.EqualFold(section, p) {
			kind = "policy"
		}
	}
	del := map[string]int{"policy": 0, "phase2": 1, "infra": 2, "group": 3, "object": 4, "phase1": 5, "zone": 6, "interface": 7, "other": 8}
	add := map[string]int{"interface": 0, "zone": 1, "phase1": 2, "object": 3, "group": 4, "phase2": 5, "infra": 5, "policy": 7, "other": 6}
	if deleting {
		return phObjDel, del[kind]
	}
	return phObjAdd, add[kind]
}

// fgMoves: policy sections whose edit IDs are in another order in A
// than in B get move commands, after everything else.
func (w *cmdWriter) fgMoves(tree []*cnode) {
	lines := w.res.Lines
	var walk func(n *cnode, path []string)
	walk = func(n *cnode, path []string) {
		if !n.head {
			return
		}
		head := strings.TrimSpace(rowText(lines[n.row]))
		section := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(head), "config "))
		isPolicy := false
		for _, p := range fgPolicySections {
			if strings.EqualFold(section, p) {
				isPolicy = true
			}
		}
		if isPolicy {
			var idsA, idsB []string
			for _, k := range n.kids {
				if !k.head {
					continue
				}
				l := lines[k.row]
				if l.Kind != "+" {
					if m := fgEdit.FindStringSubmatch(strings.TrimSpace(l.A)); m != nil {
						idsA = append(idsA, m[1])
					}
				}
				if l.Kind != "-" {
					if m := fgEdit.FindStringSubmatch(strings.TrimSpace(l.B)); m != nil {
						idsB = append(idsB, m[1])
					}
				}
			}
			// Rows of B are written in B's order; A's order is the target.
			// Moved rows sit at B's position in the result, so idsA here
			// is A's set in B's order: take A's order from the line numbers.
			sort.SliceStable(idsA, func(i, j int) bool { return fgLineA(lines, n, idsA[i]) < fgLineA(lines, n, idsA[j]) })
			if moves := moveSteps(idsB, idsA); len(moves) > 0 {
				var body []CmdLine
				for _, m := range moves {
					body = append(body, w.line(m, -1))
				}
				c := chunk{phase: phFinal}
				wrap := append([]string(nil), path...)
				wrap = append(wrap, head)
				for i, h := range wrap {
					c.lines = append(c.lines, w.ctx(strings.Repeat("    ", i)+h, -1))
				}
				ind := strings.Repeat("    ", len(wrap))
				for _, l := range body {
					l.Text = ind + l.Text
					c.lines = append(c.lines, l)
				}
				for i := len(wrap) - 1; i >= 0; i-- {
					close := "end"
					if strings.HasPrefix(strings.ToLower(wrap[i]), "edit ") {
						close = "next"
					}
					c.lines = append(c.lines, w.ctx(strings.Repeat("    ", i)+close, -1))
				}
				w.add(c)
			}
			return
		}
		cur := append(append([]string(nil), path...), head)
		for _, k := range n.kids {
			walk(k, cur)
		}
	}
	for _, n := range tree {
		walk(n, nil)
	}
}

// fgLineA is the A-side line number of the edit with this ID under n.
func fgLineA(lines []DiffLine, n *cnode, id string) int {
	for _, k := range n.kids {
		if k.head {
			if m := fgEdit.FindStringSubmatch(strings.TrimSpace(lines[k.row].A)); m != nil && m[1] == id {
				return lines[k.row].LineA
			}
		}
	}
	return 1 << 30
}

// moveSteps: the move commands that take the current order to the
// target order, one step at a time from the front (IDs only in the
// current order are dropped first, IDs only in the target are appended
// at the end, as a re-added edit would be).
func moveSteps(current, target []string) []string {
	inT := map[string]bool{}
	for _, id := range target {
		inT[id] = true
	}
	var cur []string
	seen := map[string]bool{}
	for _, id := range current {
		if inT[id] {
			cur = append(cur, id)
			seen[id] = true
		}
	}
	for _, id := range target {
		if !seen[id] {
			cur = append(cur, id)
		}
	}
	if len(cur) < 2 {
		return nil
	}
	var out []string
	for i, want := range target {
		if i >= len(cur) || cur[i] == want {
			continue
		}
		// take want out of cur and put it at i
		j := -1
		for k, id := range cur {
			if id == want {
				j = k
			}
		}
		if j < 0 {
			continue
		}
		cur = append(cur[:j], cur[j+1:]...)
		cur = append(cur[:i], append([]string{want}, cur[i:]...)...)
		if i == 0 {
			out = append(out, "move "+want+" before "+cur[1])
		} else {
			out = append(out, "move "+want+" after "+target[i-1])
		}
	}
	return out
}

// ---- junos ----

func (w *cmdWriter) junos(a, b string) {
	setA, setB := junosLines(a), junosLines(b)
	r := DiffWith(strings.Join(setA, "\n"), strings.Join(setB, "\n"), DiffOpts{IgnoreNoise: true})
	var dels, sets []CmdLine
	for _, l := range r.Lines {
		if l.Noise {
			continue
		}
		switch l.Kind {
		case "+":
			t := strings.TrimSpace(l.B)
			f := strings.Fields(t)
			if len(f) < 2 {
				continue
			}
			rest := strings.TrimSpace(t[len(f[0]):])
			switch strings.ToLower(f[0]) {
			case "set":
				dels = append(dels, w.line("delete "+rest, -1))
			case "delete":
				sets = append(sets, w.line("set "+rest, -1))
			case "deactivate":
				sets = append(sets, w.line("activate "+rest, -1))
			case "activate":
				dels = append(dels, w.line("deactivate "+rest, -1))
			default:
				sets = append(sets, w.note(t, "逆の操作が分からない", -1))
			}
		case "-", "~":
			t := strings.TrimSpace(l.A)
			if strings.HasPrefix(strings.ToLower(t), "delete ") {
				dels = append(dels, w.line(t, -1))
			} else {
				sets = append(sets, w.line(t, -1))
			}
		}
	}
	if len(dels)+len(sets) == 0 {
		return
	}
	sort.SliceStable(dels, func(i, j int) bool { return junosSub(dels[i].Text, true) < junosSub(dels[j].Text, true) })
	sort.SliceStable(sets, func(i, j int) bool { return junosSub(sets[i].Text, false) < junosSub(sets[j].Text, false) })
	w.out = append(w.out, dels...)
	w.out = append(w.out, sets...)
	w.out = append(w.out, w.ctx("commit", -1))
}

// junosLines: the set lines of a capture's configuration (brace format
// converted).
func junosLines(log string) []string {
	lines := splitLines(log)
	idx, _ := configSectionIdx(lines)
	var cfg []string
	for _, k := range idx {
		if k >= 0 {
			cfg = append(cfg, lines[k])
		}
	}
	if js := junosSet(cfg); js != nil {
		return js
	}
	var out []string
	for _, l := range cfg {
		t := strings.TrimSpace(l)
		lt := strings.ToLower(t)
		if strings.HasPrefix(lt, "set ") || strings.HasPrefix(lt, "delete ") || strings.HasPrefix(lt, "deactivate ") || strings.HasPrefix(lt, "activate ") {
			out = append(out, t)
		}
	}
	return out
}

// junosSub: references before objects when deleting, objects before
// references when setting.
func junosSub(cmd string, deleting bool) int {
	t := strings.ToLower(cmd)
	ref := strings.Contains(t, " security policies") || (strings.Contains(t, " interfaces ") && strings.Contains(t, " filter "))
	obj := strings.Contains(t, " firewall ") || strings.Contains(t, " policy-options ")
	switch {
	case deleting && ref, !deleting && obj:
		return 0
	case deleting && obj, !deleting && ref:
		return 2
	}
	return 1
}

// ---- yamaha ----

func (w *cmdWriter) yamaha() {
	lines := w.res.Lines
	tree := cmdTree(lines)
	w.skipped = w.res.SecretLines
	var walk func(n *cnode, ctxHead string)
	walk = func(n *cnode, ctxHead string) {
		if !n.changed(lines) {
			return
		}
		l := lines[n.row]
		t := strings.TrimSpace(rowText(l))
		if n.head {
			c := chunk{phase: phOther, sub: 5}
			c.lines = append(c.lines, w.ctx(t, n.row))
			for _, k := range n.kids {
				if !k.changed(lines) {
					continue
				}
				for _, cl := range w.yamahaLeaf(k.row) {
					c.lines = append(c.lines, cl)
				}
			}
			w.add(c)
			return
		}
		cl := w.yamahaLeaf(n.row)
		if len(cl) == 0 {
			return
		}
		ph, sub := yamahaPhase(cl[0].Text)
		w.add(chunk{lines: cl, phase: ph, sub: sub})
	}
	for _, n := range tree {
		walk(n, "")
	}
	if len(w.chunks) == 0 {
		return
	}
	w.flush()
	w.out = append(w.out, w.ctx("save", -1))
}

func (w *cmdWriter) yamahaLeaf(row int) []CmdLine {
	l := w.res.Lines[row]
	a, b := strings.TrimSpace(l.A), strings.TrimSpace(l.B)
	switch l.Kind {
	case "+":
		return []CmdLine{w.line(noToggle(b), row)}
	case "-":
		return []CmdLine{w.line(a, row)}
	case "~":
		if firstWords(a, 2) != firstWords(b, 2) {
			return []CmdLine{w.line(noToggle(b), row), w.line(a, row)}
		}
		return []CmdLine{w.line(a, row)}
	}
	return nil
}

func yamahaPhase(cmd string) (int, int) {
	t := strings.ToLower(strings.TrimSpace(cmd))
	filterRef := regexp.MustCompile(`\bfilter\s+(in|out)\b`)
	switch {
	case strings.HasPrefix(t, "no ") && filterRef.MatchString(t):
		return phRefDel, 0
	case strings.HasPrefix(t, "no ip filter "), strings.HasPrefix(t, "no ipv6 filter "):
		return phObjDel, 0
	case strings.HasPrefix(t, "ip filter "), strings.HasPrefix(t, "ipv6 filter "):
		return phObjAdd, 0
	case filterRef.MatchString(t):
		return phRefAdd, 0
	}
	return phOther, 5
}

// ---- generic ----

func (w *cmdWriter) generic() {
	var added, removed, changed []CmdLine
	for i, l := range w.res.Lines {
		if l.Noise {
			continue
		}
		switch l.Kind {
		case "+":
			added = append(added, w.line(strings.TrimSpace(l.B), i))
		case "-":
			removed = append(removed, w.line(strings.TrimSpace(l.A), i))
		case "~":
			changed = append(changed, w.line(strings.TrimSpace(l.A)+"    # ← "+strings.TrimSpace(l.B), i))
		}
	}
	w.warnings = append(w.warnings, "この機器の種類ではコマンドを作れません。差分の一覧だけを出します")
	if len(added) > 0 {
		w.out = append(w.out, w.ctx("# [B で増えた行（戻すなら消す）]", -1))
		w.out = append(w.out, added...)
	}
	if len(removed) > 0 {
		w.out = append(w.out, w.ctx("# [B で消えた行（戻すなら入れ直す）]", -1))
		w.out = append(w.out, removed...)
	}
	if len(changed) > 0 {
		w.out = append(w.out, w.ctx("# [変わった行: 元の行    # ← 今の行]", -1))
		w.out = append(w.out, changed...)
	}
	w.manual = len(w.out)
}

// ---- per-device versions (spec §2) ----

// ConfigFingerprint is a short hash of a capture's configuration listing
// with the noise and secrets left out: equal fingerprints mean the
// configuration did not change between two captures. "" when the capture
// holds no listing.
func ConfigFingerprint(log string) string {
	lines := splitLines(log)
	idx, kind := configSectionIdx(lines)
	if kind == noListing {
		return ""
	}
	raw := make([]string, 0, len(idx))
	for _, k := range idx {
		if k >= 0 {
			raw = append(raw, lines[k])
		} else {
			raw = append(raw, "")
		}
	}
	mask := secretMask(raw, true)
	h := sha256.New()
	for i, l := range raw {
		if mask[i] {
			continue
		}
		k := normalize(l)
		if k == "" || barePrompt.MatchString(k) {
			continue
		}
		h.Write([]byte(k))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
