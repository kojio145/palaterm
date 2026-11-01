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
	Family    string    `json:"family"`   // cisco / comware / fortigate / junos / yamaha / generic
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
	w := &cmdWriter{res: r, rows: direction == "rollback", os: strings.ToLower(osType)}
	switch g.Family {
	case "cisco":
		w.cisco()
	case "comware":
		w.comware()
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
	case strings.Contains(lo, "comware") || strings.Contains(lo, "h3c") || strings.Contains(lo, "huawei") || strings.Contains(lo, "vrp") || strings.Contains(lo, "nec-qx"):
		return "comware", "osType"
	case strings.Contains(lo, "cisco") || strings.Contains(lo, "ios") || strings.Contains(lo, "alaxala") || strings.Contains(lo, "aruba") || strings.Contains(lo, "allied") || strings.Contains(lo, "nec-ix") || strings.Contains(lo, "nxos"):
		return "cisco", "osType"
	}
	var fgConfig, fgEnd, edits, setLines, braces, yam, bang, iface, host, nonEmpty, hash, cw, ciscoish int
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
		case t == "#":
			hash++
		case strings.HasPrefix(lt, "interface "):
			iface++
		case strings.HasPrefix(lt, "hostname "):
			host++
		}
		for _, p := range []string{"sysname ", "undo ", "port link-type ", "port link-mode ", "ip route-static ", "local-user ", "ntp-service ", "snmp-agent "} {
			if strings.HasPrefix(lt, p) {
				cw++
				break
			}
		}
		for _, p := range []string{"switchport", "ip address ", "spanning-tree ", "line vty", "line con", "feature ", "shutdown", "no shutdown", "ip route ", "ntp server ", "logging ", "version ", "service ", "enable ", "vlan "} {
			if strings.HasPrefix(lt, p) {
				ciscoish++
				break
			}
		}
	}
	switch {
	case fgConfig >= 1 && fgEnd > 0 && (setLines > 0 || edits > 0):
		return "fortigate", "content"
	case nonEmpty > 0 && (setLines*2 >= nonEmpty || braces >= 2):
		return "junos", "content"
	case yam >= 3 && bang == 0:
		return "yamaha", "content"
	case hash >= 2 && cw > 0 && bang == 0:
		// Comware / VRP: "#" between sections, sysname, undo.
		return "comware", "content"
	case bang > 0 && (iface > 0 || host > 0), iface > 0 && ciscoish >= 2:
		// Cisco-style: "!" between sections with interfaces or a hostname,
		// or (NX-OS, no "!") interfaces with Cisco-style lines under them.
		return "cisco", "content"
	}
	return "generic", "content"
}

// cnode is a block (heading + what is under it) or a single row of the
// comparison, rebuilt from the flat result the way difftree builds it.
type cnode struct {
	row     int
	head    bool
	banner  bool // a multi-line banner: the rows under it are its text
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
		if top := len(stack) - 1; top >= 0 && stack[top].banner && l.Depth > depthOf(stack[top].row) {
			// Banner text: never a closer, never a heading.
			stack[top].kids = append(stack[top].kids, &cnode{row: i})
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
		if l.Head {
			_, _, n.banner = bannerBlock(strings.TrimSpace(l.A))
			if !n.banner {
				_, _, n.banner = bannerBlock(strings.TrimSpace(l.B))
			}
		}
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
	if lines[n.row].Kind != "=" || lines[n.row].Moved {
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
	rows      bool   // keep row references (rollback)
	os        string // the profile key, lower-cased ("" when unknown)
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
	// physIface: a physical port (Cisco, NX-OS, AlaxalA "gigabitethernet
	// 0/5", NEC IX "GigaEthernet0.0", Comware "GigabitEthernet1/0/1",
	// Huawei "GE0/0/1"): never "no interface", which a device rejects or
	// takes as "wipe the port".
	physIface = regexp.MustCompile(`(?i)^interface\s+((?:(?:Two|Five|TwentyFive|Forty|Hundred|Four|Ten|App|X|M)?Gigabit(?:Ethernet)?|(?:Giga|Fast|M-?|X)?Ethernet|(?:TwentyFive|Hundred|Forty|Ten|X)?GigE|Ten-GigabitEthernet|Twenty-FiveGigE|Forty-GigE|Hundred-GigE|(?:25|40|100|10)?GE|XGE|MEth|Eth|Fa|Gi|Te|Twe|Tw|Fi|Fo|Hu|Serial|Se|mgmt|Management|MgmtEth|Cellular|Dot11Radio|Wlan-GigabitEthernet|Bluetooth|Async)\s*\d\S*)$`)
	// logicalIface: an interface "no interface" removes cleanly.
	logicalIface = regexp.MustCompile(`(?i)^interface\s+(vlan|loopback|port-channel|tunnel|bvi|nve|dialer|virtual-template|null|bdi|bundle-ether|lo|vl|po|eth-trunk|vlanif|bridge-aggregation|route-aggregation|vlan-interface|multilink|group-async|pseudowire)\S*$`)
	numberedACL  = regexp.MustCompile(`(?i)^access-list\s+(\d+)\s`)
	prefixListRe = regexp.MustCompile(`(?i)^((?:ip|ipv6) prefix-list\s+\S+)\s+(?:seq\s+\d+\s+)?(?:permit|deny|description)\b`)
	// orderedBlock: a block whose entries are evaluated in order (or whose
	// device rejects a partial edit): any change inside it means the whole
	// block is removed and re-entered as it was.
	orderedBlock = regexp.MustCompile(`(?i)^((?:ip|ipv6|mac|arp) access-list\b|route-map\b)`)
	vlanListRe   = regexp.MustCompile(`(?i)^(\s*)(switchport trunk allowed vlan|switchport trunk pruning vlan|spanning-tree vlan)\s+([\d,\-]+)(\s+.*)?$`)
	// allowedVlanRe: one line of a trunk's VLAN list, with its mode (none
	// / "" = the list, add / remove / except / none / all).
	allowedVlanRe = regexp.MustCompile(`(?i)^\s*switchport trunk allowed vlan(?:\s+(add|remove|except|none|all))?(?:\s+([\d,\-]+))?\s*$`)
	// vrfSwitch: a command that wipes the interface's addresses (and more)
	// as a side effect: the addresses of A are put back after it.
	vrfSwitch  = regexp.MustCompile(`(?i)^\s*(?:no )?(?:(?:ip )?vrf forwarding\b|vrf member\b|switchport$)`)
	addrLine   = regexp.MustCompile(`(?i)^\s*(?:ip|ipv6) address\b`)
	addrFamily = regexp.MustCompile(`(?i)^\s*address-family\b`)
)

// overwriteKeys are settings a device keeps one of: typing the new value
// replaces the old one, so a changed line is put back by its A line alone.
// Every other changed line is a list entry until proven otherwise
// (ip route, ntp server, logging host, snmp-server community, ip
// helper-address …): B's line is removed first, or B's value would stay in
// force next to A's. The verdict's singleValued (strict_extra.go) is the
// same notion and is read too; this list adds the settings that only
// matter when commands are generated (an interface's port settings, which
// a "no" first would flap).
var overwriteKeys = []string{
	"ip domain-name", "ip domain name", "ip domain lookup",
	"ip mtu", "bandwidth", "delay",
	"clock summer-time", "password", "login", "transport input", "transport output", "transport preferred",
	"logging buffered", "logging console", "logging monitor", "logging trap", "logging facility", "logging source-interface", "logging origin-id",
	"ntp source", "snmp-server location", "snmp-server contact",
	"spanning-tree mode", "spanning-tree portfast", "spanning-tree cost", "spanning-tree port-priority", "spanning-tree guard", "spanning-tree link-type", "spanning-tree bpduguard", "spanning-tree bpdufilter",
	"switchport mode", "switchport access vlan", "switchport trunk native vlan", "switchport trunk encapsulation", "switchport trunk allowed vlan", "switchport voice vlan",
	"switchport port-security maximum", "switchport port-security violation", "switchport port-security aging",
	"channel-group", "encapsulation", "ip ospf cost", "ip ospf priority", "ip ospf hello-interval", "ip ospf dead-interval", "ip ospf network",
	"vrf forwarding", "ip vrf forwarding", "vrf member", "storm-control", "ip tcp adjust-mss", "ip igmp version",
	"vtp domain", "vtp mode", "vtp version", "ip ssh version", "ip ssh time-out", "ip ssh authentication-retries",
	"errdisable recovery interval", "mac address-table aging-time", "port-channel load-balance", "lldp timer", "cdp timer",
	"username",
	// Comware / VRP
	"port link-type", "port link-mode", "port access vlan", "port trunk pvid vlan", "port hybrid pvid vlan", "header", "jumboframe enable", "stp edged-port", "link-aggregation mode",
}

// keyPhrase: the leading keywords of a setting ("ip address 192.0.2.1
// 255.255.255.0" → "ip address"), lower-cased.
func keyPhrase(line string) string {
	f := strings.Fields(line)
	n := 0
	for n < len(f) && isWordToken(f[n]) {
		n++
	}
	return strings.ToLower(strings.Join(f[:n], " "))
}

// overwriteSetting says whether typing a (A's line) alone replaces b (B's
// line of the same setting) on the device.
func overwriteSetting(a, b string) bool {
	ka, kb := keyPhrase(a), keyPhrase(b)
	if ka == "" || kb == "" {
		return false
	}
	fa, fb := strings.Fields(strings.ToLower(a)), strings.Fields(strings.ToLower(b))
	if fa[0] == "username" || fb[0] == "username" {
		// "username ops …" replaces the same user's line; another user's
		// line would stay.
		return fa[0] == fb[0] && len(fa) > 1 && len(fb) > 1 && fa[1] == fb[1]
	}
	if fa[0] == "set" && (len(fa) > 4 || len(fb) > 4) {
		return false // a Junos path: a leaf may hold a list
	}
	for _, f := range [][]string{fa, fb} {
		for _, w := range f[1:] {
			if w == "secondary" || w == "add" || w == "remove" {
				return false
			}
		}
	}
	both := func(p string) bool {
		return (ka == p || strings.HasPrefix(ka, p+" ")) && (kb == p || strings.HasPrefix(kb, p+" "))
	}
	for _, p := range singleValued {
		if both(strings.Join(p, " ")) {
			return true
		}
	}
	for _, p := range overwriteKeys {
		if both(p) {
			return true
		}
	}
	return false
}

// defaultIfaceOK: the device takes "default interface X" (IOS, IOS-XE,
// NX-OS). AlaxalA, NEC IX, Aruba, Allied and ASA do not; a physical port
// of theirs that gained settings is left for a look by hand.
func (w *cmdWriter) defaultIfaceOK() bool {
	switch {
	case w.os == "":
		return true
	case strings.Contains(w.os, "asa"):
		return false
	}
	return strings.Contains(w.os, "cisco") || strings.Contains(w.os, "nxos") || strings.Contains(w.os, "ios")
}

// resetIface: the chunk that takes a physical port back to its defaults.
func (w *cmdWriter) resetIface(name string, row int) chunk {
	if w.defaultIfaceOK() {
		return chunk{lines: []CmdLine{w.line("default interface "+name, row)}, phase: phOther, sub: 9}
	}
	return chunk{lines: []CmdLine{w.note("interface "+name, "物理ポートの設定を手で既定に戻す（この機種に default interface は無い）", row)}, phase: phOther, sub: 9}
}

func (w *cmdWriter) cisco() {
	lines := w.res.Lines
	tree := cmdTree(lines)
	w.skipped = w.res.SecretLines
	consumed := map[int]bool{}
	// Numbered ACLs and prefix lists are groups of top-level rows that are
	// evaluated in order: any change (a moved entry too) and the whole
	// group is removed and re-entered as it was in A.
	for i, n := range tree {
		if n.head || consumed[n.row] {
			continue
		}
		key := flatGroupKey(strings.TrimSpace(rowText(lines[n.row])))
		if key == "" {
			continue
		}
		var group []int
		changed := false
		for _, x := range tree[i:] {
			if x.head || !strings.EqualFold(flatGroupKey(strings.TrimSpace(rowText(lines[x.row]))), key) {
				continue
			}
			group = append(group, x.row)
			if lines[x.row].Kind != "=" || lines[x.row].Moved {
				changed = true
			}
		}
		for _, r := range group {
			consumed[r] = true
		}
		if changed {
			c := chunk{phase: phObjAdd, sub: 4}
			c.lines = append(c.lines, w.line("no "+key, group[0]))
			for _, r := range group {
				if lines[r].Kind != "+" {
					c.lines = append(c.lines, w.line(strings.TrimSpace(lines[r].A), r))
				}
			}
			w.add(c)
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
		if n.banner {
			w.ciscoBanner(n)
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

// flatGroupKey names the group a top-level list line belongs to
// ("access-list 10", "ip prefix-list LAB"); "" for any other line.
func flatGroupKey(text string) string {
	if m := numberedACL.FindStringSubmatch(text); m != nil {
		return "access-list " + m[1]
	}
	if m := prefixListRe.FindStringSubmatch(text); m != nil {
		return strings.Join(strings.Fields(m[1]), " ")
	}
	return ""
}

// ciscoBanner writes a multi-line banner back as it was in A: the
// current one removed, A's lines, and the closing delimiter — always, or
// everything pasted after it would become banner text.
func (w *cmdWriter) ciscoBanner(n *cnode) {
	lines := w.res.Lines
	l := lines[n.row]
	headA, headB := strings.TrimSpace(l.A), strings.TrimSpace(l.B)
	c := chunk{phase: phOther, sub: 5}
	if l.Kind != "-" {
		kind, _, _ := bannerBlock(headB)
		if kind == "" {
			kind, _, _ = bannerBlock(headA)
		}
		c.lines = append(c.lines, w.line("no banner "+kind, n.row))
	}
	if l.Kind != "+" {
		_, delim, _ := bannerBlock(headA)
		c.lines = append(c.lines, w.line(headA, n.row))
		last := headA
		for _, k := range n.kids {
			if lines[k.row].Kind == "+" {
				continue
			}
			last = lines[k.row].A
			c.lines = append(c.lines, w.line(last, k.row))
		}
		if delim != "" && (last == headA || !strings.Contains(last, delim)) {
			c.lines = append(c.lines, w.line(delim, -1))
		}
	}
	w.add(c)
}

// removeBlock: the chunk that removes a whole block that is only in B: a
// physical port goes back to its defaults, anything else is "no <head>".
func (w *cmdWriter) removeBlock(head string, row int) chunk {
	if m := physIface.FindStringSubmatch(head); m != nil {
		return w.resetIface(m[1], row)
	}
	if strings.HasPrefix(strings.ToLower(head), "interface ") && !logicalIface.MatchString(head) {
		// An interface of a kind not known here: "no interface" is wrong
		// for a port and only right for a logical one.
		if w.defaultIfaceOK() {
			return chunk{lines: []CmdLine{w.line("default "+head, row)}, phase: phOther, sub: 9}
		}
		return chunk{lines: []CmdLine{w.note(head, "種類の分からないインタフェース: 消すか既定に戻すかを手で確認", row)}, phase: phOther, sub: 9}
	}
	ph, sub := ciscoPhase("no "+head, false)
	return chunk{lines: []CmdLine{w.line("no "+head, row)}, phase: ph, sub: sub}
}

// ciscoBlock writes a block with differences as one chunk (or one line
// when the whole block is new or gone).
func (w *cmdWriter) ciscoBlock(n *cnode, depth int) {
	lines := w.res.Lines
	head := strings.TrimSpace(rowText(lines[n.row]))
	switch {
	case n.all(lines, "+"):
		w.add(w.removeBlock(head, n.row))
		return
	case n.all(lines, "-"):
		c := chunk{}
		c.phase, c.sub = ciscoPhase(head, false)
		c.lines = append(c.lines, w.ctx(head, n.row))
		w.wholeBlock(n, &c)
		c.lines = append(c.lines, w.ctx(exitFor(head, lines[n.row].Depth), -1))
		w.add(c)
		return
	case lines[n.row].Kind == "~", orderedBlock.MatchString(head):
		// A renamed block ("ip access-list extended OLD" → "… NEW"), or an
		// ordered one (a named ACL, a route-map) with any change inside:
		// the current one goes, the old one comes back whole.
		w.add(w.removeBlock(head, n.row))
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
			w.add(w.resetIface(m[1], n.row))
			return
		}
	}
	c := chunk{phase: phOther, sub: 9}
	c.lines = append(c.lines, w.ctx(head, n.row))
	var nos, rest []CmdLine
	vlanRows := w.allowedVlanRows(n)
	for _, k := range n.kids {
		if !k.changed(lines) || vlanRows[k.row] {
			continue
		}
		if k.head {
			sub := &cmdWriter{res: w.res, rows: w.rows, os: w.os}
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
	rest = append(rest, w.allowedVlanLines(n, vlanRows)...)
	body := append(nos, rest...)
	// "no vrf forwarding", "vrf forwarding X", "switchport" / "no
	// switchport" wipe the interface's addresses: A's come back after.
	wipes := false
	for _, l := range body {
		if vrfSwitch.MatchString(l.Text) {
			wipes = true
		}
	}
	if wipes {
		have := map[string]bool{}
		for _, l := range body {
			have[strings.TrimSpace(l.Text)] = true
		}
		for _, k := range n.kids {
			if k.head || lines[k.row].Kind == "+" || !addrLine.MatchString(lines[k.row].A) || have[strings.TrimSpace(lines[k.row].A)] {
				continue
			}
			body = append(body, w.note(strings.TrimSpace(lines[k.row].A), "vrf / switchport の切替で消えるアドレスを入れ直す", k.row))
		}
	}
	if len(body) == 0 {
		return
	}
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

// allowedVlanRows: the rows under a block that make up its trunk VLAN
// list ("switchport trunk allowed vlan 1-100" and the "add …" lines
// after it) when both sides have one; they are handled as one set by
// allowedVlanLines. Empty when a side has no list (the rows then go the
// usual way, line by line).
func (w *cmdWriter) allowedVlanRows(n *cnode) map[int]bool {
	lines := w.res.Lines
	rows := map[int]bool{}
	hasA, hasB := false, false
	for _, k := range n.kids {
		if k.head {
			continue
		}
		l := lines[k.row]
		if l.Kind != "+" && allowedVlanRe.MatchString(l.A) {
			rows[k.row], hasA = true, true
		}
		if l.Kind != "-" && allowedVlanRe.MatchString(l.B) {
			rows[k.row], hasB = true, true
		}
	}
	if !hasA || !hasB {
		return map[int]bool{}
	}
	return rows
}

// allowedVlanLines turns the VLAN-list rows of a block into the add /
// remove lines that take B's set to A's. A list that says none / all /
// except is left for a look by hand.
func (w *cmdWriter) allowedVlanLines(n *cnode, rows map[int]bool) []CmdLine {
	if len(rows) == 0 {
		return nil
	}
	lines := w.res.Lines
	type ent struct {
		row  int
		line int
		text string
	}
	var ea, eb []ent
	changed := false
	first := -1
	for _, k := range n.kids {
		if !rows[k.row] {
			continue
		}
		l := lines[k.row]
		if l.Kind != "=" || l.Moved {
			changed = true
			if first < 0 {
				first = k.row
			}
		}
		if l.Kind != "+" {
			ea = append(ea, ent{k.row, l.LineA, l.A})
		}
		if l.Kind != "-" {
			eb = append(eb, ent{k.row, l.LineB, l.B})
		}
	}
	if !changed {
		return nil
	}
	sort.SliceStable(ea, func(i, j int) bool { return ea[i].line < ea[j].line })
	sort.SliceStable(eb, func(i, j int) bool { return eb[i].line < eb[j].line })
	build := func(es []ent) (map[int]bool, []ent) {
		set := map[int]bool{}
		var manual []ent
		for _, e := range es {
			m := allowedVlanRe.FindStringSubmatch(e.text)
			if m == nil {
				manual = append(manual, e)
				continue
			}
			switch strings.ToLower(m[1]) {
			case "":
				set = vlanSet(m[2])
			case "add":
				for v := range vlanSet(m[2]) {
					set[v] = true
				}
			case "remove":
				for v := range vlanSet(m[2]) {
					delete(set, v)
				}
			default:
				manual = append(manual, e)
			}
		}
		return set, manual
	}
	setA, manA := build(ea)
	setB, manB := build(eb)
	if len(manA)+len(manB) > 0 {
		var out []CmdLine
		for _, e := range append(manA, manB...) {
			if l := lines[e.row]; l.Kind != "=" || l.Moved {
				out = append(out, w.note(strings.TrimSpace(e.text), "VLAN 一覧の none / all / except は手で確認", e.row))
			}
		}
		return out
	}
	var out []CmdLine
	if extra := vlanDiff(setB, setA); extra != "" {
		out = append(out, w.line(" switchport trunk allowed vlan remove "+extra, first))
	}
	if gone := vlanDiff(setA, setB); gone != "" {
		out = append(out, w.line(" switchport trunk allowed vlan add "+gone, first))
	}
	return out
}

// wholeBlock writes every line of a block as it was in A, in A's order
// (a moved row sits at B's place in the result).
func (w *cmdWriter) wholeBlock(n *cnode, c *chunk) {
	lines := w.res.Lines
	kids := make([]*cnode, 0, len(n.kids))
	for _, k := range n.kids {
		if lines[k.row].Kind != "+" {
			kids = append(kids, k)
		}
	}
	sort.SliceStable(kids, func(i, j int) bool { return lines[kids[i].row].LineA < lines[kids[j].row].LineA })
	for _, k := range kids {
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
		if overwriteSetting(ta, tb) || strings.EqualFold(strings.TrimSpace(noToggle(tb)), ta) {
			// One value per device (description, hostname, speed …): A's
			// line replaces B's. "ip address" too — removing it first
			// would cut the session off.
			return []CmdLine{w.line(a, row)}, true
		}
		// A list entry (ip route, ntp server, logging host, snmp-server
		// community, ip helper-address …) or a reference ("ip access-group
		// NEW in"): B's goes first, then A's comes back.
		if strings.HasPrefix(strings.ToLower(tb), "username ") {
			return []CmdLine{w.note(noToggle(b), "username の削除は確認プロンプトが出る機種がある", row), w.line(a, row)}, true
		}
		return []CmdLine{w.line(noToggle(b), row), w.line(a, row)}, true
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
			// "set x …" replaces x's value; a pair of different keys
			// (a removed line and an added one that happened to sit
			// together) means B's key is unset before A's line.
			ta := strings.TrimSpace(l.A)
			ma, mb := fgSet.FindStringSubmatch(ta), fgSet.FindStringSubmatch(t)
			if mb != nil && (ma == nil || !strings.EqualFold(ma[1], mb[1])) {
				unsets = append(unsets, w.line("unset "+mb[1], k.row))
			} else if mb == nil && !strings.HasPrefix(lt, "unset ") {
				rest = append(rest, w.note(t, "逆の操作が分からない", k.row))
			}
			rest = append(rest, w.line(ta, k.row))
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
			if l.Kind == "~" {
				// The B line of a pair is a leaf of its own (a list
				// value, or an unrelated line that sat next to A's):
				// it goes before A's comes back. The commit applies
				// both at once, so a single-valued leaf sees no gap.
				tb := strings.TrimSpace(l.B)
				if f := strings.Fields(tb); len(f) >= 2 && strings.EqualFold(f[0], "set") {
					dels = append(dels, w.line("delete "+strings.TrimSpace(tb[len(f[0]):]), -1))
				}
			}
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
			if len(c.lines) > 1 {
				w.add(c)
			}
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

// yamahaFilterRef: a filter applied to an interface ("ip lan1 secure
// filter in 100").
var yamahaFilterRef = regexp.MustCompile(`\bfilter\s+(in|out)\b`)

func yamahaPhase(cmd string) (int, int) {
	t := strings.ToLower(strings.TrimSpace(cmd))
	filterRef := yamahaFilterRef
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

// ---- comware / VRP ----

// undoToggle puts "undo " in front of a command or takes it off.
func undoToggle(cmd string) string {
	t := strings.TrimSpace(cmd)
	lead := cmd[:len(cmd)-len(strings.TrimLeft(cmd, " \t"))]
	if strings.HasPrefix(strings.ToLower(t), "undo ") {
		return lead + strings.TrimSpace(t[5:])
	}
	return lead + "undo " + t
}

// comware writes the commands of a Comware / VRP device: "undo" as the
// negation, blocks closed with "quit", the whole inside system-view …
// return. A physical port that is only in B gets its lines undone one by
// one (there is no "default interface").
func (w *cmdWriter) comware() {
	lines := w.res.Lines
	tree := cmdTree(lines)
	w.skipped = w.res.SecretLines
	for _, n := range tree {
		if !n.changed(lines) {
			continue
		}
		if !n.head {
			if cl, ok := w.comwareLeaf(n.row); ok {
				w.add(chunk{lines: cl, phase: phOther, sub: 5})
			}
			continue
		}
		w.comwareBlock(n)
	}
	if len(w.chunks) == 0 {
		return
	}
	w.warnings = append(w.warnings, "Comware/VRP 系: undo に引数が要るかどうかは機種で違います。投入前に 1 行ずつ確認してください")
	w.out = append(w.out, w.ctx("system-view", -1))
	w.flush()
	w.out = append(w.out, w.ctx("return", -1))
}

func (w *cmdWriter) comwareBlock(n *cnode) {
	lines := w.res.Lines
	head := strings.TrimSpace(rowText(lines[n.row]))
	old := strings.TrimSpace(lines[n.row].A)
	phys := physIface.MatchString(head)
	whole := func(h string) {
		c := chunk{phase: phOther, sub: 5}
		c.lines = append(c.lines, w.ctx(h, n.row))
		w.wholeBlock(n, &c)
		c.lines = append(c.lines, w.ctx("quit", -1))
		w.add(c)
	}
	switch {
	case n.all(lines, "+") && !phys:
		w.add(chunk{lines: []CmdLine{w.line("undo "+head, n.row)}, phase: phOther, sub: 4})
		return
	case n.all(lines, "-"):
		whole(head)
		return
	case lines[n.row].Kind == "~":
		if phys {
			w.add(chunk{lines: []CmdLine{w.note(head, "物理ポートの設定を手で既定に戻す", n.row)}, phase: phOther, sub: 4})
		} else {
			w.add(chunk{lines: []CmdLine{w.line("undo "+head, n.row)}, phase: phOther, sub: 4})
		}
		whole(old)
		return
	}
	c := chunk{phase: phOther, sub: 5}
	c.lines = append(c.lines, w.ctx(head, n.row))
	var undos, rest []CmdLine
	for _, k := range n.kids {
		if !k.changed(lines) {
			continue
		}
		if k.head {
			sub := &cmdWriter{res: w.res, rows: w.rows, os: w.os}
			sub.comwareBlock(k)
			for _, sc := range sub.chunks {
				rest = append(rest, sc.lines...)
			}
			w.manual += sub.manual
			continue
		}
		cl, ok := w.comwareLeaf(k.row)
		if !ok {
			continue
		}
		for _, l := range cl {
			if strings.HasPrefix(strings.TrimSpace(l.Text), "undo ") {
				undos = append(undos, l)
			} else {
				rest = append(rest, l)
			}
		}
	}
	body := append(undos, rest...)
	if len(body) == 0 {
		return
	}
	c.lines = append(c.lines, body...)
	c.lines = append(c.lines, w.ctx("quit", -1))
	w.add(c)
}

func (w *cmdWriter) comwareLeaf(row int) ([]CmdLine, bool) {
	l := w.res.Lines[row]
	a, b := l.A, l.B
	ta, tb := strings.TrimSpace(a), strings.TrimSpace(b)
	switch l.Kind {
	case "+":
		return []CmdLine{w.line(undoToggle(b), row)}, true
	case "-":
		return []CmdLine{w.line(a, row)}, true
	case "~":
		if overwriteSetting(ta, tb) || strings.EqualFold(strings.TrimSpace(undoToggle(tb)), ta) {
			return []CmdLine{w.line(a, row)}, true
		}
		return []CmdLine{w.line(undoToggle(b), row), w.line(a, row)}, true
	}
	return nil, false
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
		k := normalizeConfig(l)
		if k == "" || barePrompt.MatchString(k) {
			continue
		}
		h.Write([]byte(k))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
