package history

import (
	"regexp"
	"strings"
)

// DiffLine is one row of a side-by-side comparison.
type DiffLine struct {
	Kind  string `json:"kind"` // "=" same, "-" only in A, "+" only in B
	A     string `json:"a,omitempty"`
	B     string `json:"b,omitempty"`
	LineA int    `json:"lineA,omitempty"` // 1-based line number in A (0 when absent)
	LineB int    `json:"lineB,omitempty"`
}

// DiffResult is what the UI renders: the aligned lines plus counts.
type DiffResult struct {
	Lines   []DiffLine `json:"lines"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Same    int        `json:"same"`
	// Approx is set when the two sides were too large or too different for
	// the shortest edit script (see maxDiffLines / maxEdits): the part of
	// them that is neither common head nor common tail was then compared
	// line for line, so the counts are an upper bound and a moved block
	// shows as changed lines.
	Approx bool `json:"approx,omitempty"`
}

// noise matches the parts of a line that differ between any two captures of
// the same device without meaning anything: clock readings (see
// blankClock), uptime and traffic counters. They are blanked before
// comparing so a before/after diff shows what changed in the configuration
// and state, not that time passed. The original text is still what the UI
// shows.
var noise = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(uptime is|up for)\b.*$`),                       // uptime lines
	regexp.MustCompile(`(?i)\b\d+ (weeks?|days?|hours?|minutes?|seconds?)\b`), // 3 days, 2 hours
	regexp.MustCompile(`(?i)\b(packets (input|output)|bytes|bits/sec|packets/sec|input rate|output rate|CPU utilization|Memory|free|used)\b.*$`),
	regexp.MustCompile(`\b\d{4}[-/]\d{2}[-/]\d{2}\b`), // dates
	// Compact ages such as a route's "37d20h54m31s" or "2w3d", and
	// "00:12:34"-less uptimes like "5h32m".
	regexp.MustCompile(`\b(?:\d+[wdhms]){2,}\b`),
}

// configStamps match the whole lines every configuration listing carries
// and that change with each save or by themselves: Cisco's size and
// last-change comments and the clock-period it tunes, FortiGate's file
// version, Junos's last commit, AlaxalA's last modification. Without these,
// two listings of the same configuration differed. Unlike noise they never
// cut into a configuration line, so the work verdict uses them alone.
var configStamps = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*current configuration\s*:.*$`),
	regexp.MustCompile(`(?i)^\s*!\s*(last configuration change|nvram config last updated|no configuration change since).*$`),
	regexp.MustCompile(`(?i)^\s*ntp clock-period\s+\d+\s*$`),
	regexp.MustCompile(`(?i)^\s*#(conf_file_ver|config-version)=.*$`),
	regexp.MustCompile(`(?i)^\s*## last (commit|changed):.*$`),
	regexp.MustCompile(`(?i)^\s*#\s*last modified by\b.*$`),
}

// stripStamps blanks a line that is one of the configStamps.
func stripStamps(line string) string {
	for _, re := range configStamps {
		if re.MatchString(line) {
			return ""
		}
	}
	return line
}

// colonRun matches a whole run of colon-joined hex groups: a clock reading
// (12:34:56, 12:34:56.789), but also a MAC address (00:11:22:33:44:55) and
// an IPv6 address (2001:db8::12:34:56). clockRun is the shape of a clock.
var (
	colonRun = regexp.MustCompile(`[0-9A-Fa-f]*(?::[0-9A-Fa-f]+)+(?:\.\d+)?`)
	clockRun = regexp.MustCompile(`^\d{1,2}:\d{2}:\d{2}(?:\.\d+)?$`)
)

// blankClock takes the clock readings out of a line and nothing else. A
// bare hh:mm:ss pattern also matched the middle of a MAC or IPv6 address,
// and a changed MAC entry or IPv6 next hop was then shown as identical;
// taking the whole colon run first keeps those apart from a clock.
func blankClock(line string) string {
	if !strings.Contains(line, ":") {
		return line
	}
	return colonRun.ReplaceAllStringFunc(line, func(run string) string {
		if clockRun.MatchString(run) {
			return ""
		}
		return run
	})
}

func normalize(line string) string {
	line = strings.TrimRight(line, " \t\r")
	// AlaxalA's "!" before the prompt only says a change is not saved yet.
	if strings.HasPrefix(line, "!") && promptLine.MatchString(line) {
		line = line[1:]
	}
	line = stripStamps(line)
	line = blankClock(line)
	for _, re := range noise {
		line = re.ReplaceAllString(line, "")
	}
	return strings.TrimSpace(line)
}

// barePrompt matches a line that is nothing but a prompt ("R1#", "IX-A(config)# ",
// "user@host:~$"): the device reprints it for every Enter and for timing
// reasons, so two captures differ in how many of these they hold. AlaxalA
// puts "!" before the prompt while a change is not saved.
var barePrompt = regexp.MustCompile(`^!?[\w.@~:/()\-]*[#>$%]\s*$`)

// Diff compares two transcripts line by line. With ignoreNoise, lines that
// differ only in timestamps, uptimes and counters count as equal, and blank
// lines and bare prompt lines are left out of the comparison (and of the
// result) altogether — they say nothing about the device and their count is
// an accident of timing.
func Diff(a, b string, ignoreNoise bool) DiffResult {
	la := splitLines(a)
	lb := splitLines(b)
	// ia/ib map the compared lines back to their original line numbers.
	ia := make([]int, 0, len(la))
	ib := make([]int, 0, len(lb))
	var ka, kb []string
	if ignoreNoise {
		for i, l := range la {
			if k := normalize(l); k != "" && !barePrompt.MatchString(k) {
				ka = append(ka, k)
				ia = append(ia, i)
			}
		}
		for i, l := range lb {
			if k := normalize(l); k != "" && !barePrompt.MatchString(k) {
				kb = append(kb, k)
				ib = append(ib, i)
			}
		}
	} else {
		ka, kb = la, lb
		for i := range la {
			ia = append(ia, i)
		}
		for i := range lb {
			ib = append(ib, i)
		}
	}
	var out DiffResult
	ops, approx := myers(ka, kb)
	out.Approx = approx
	for _, op := range ops {
		switch op.kind {
		case '=':
			out.Lines = append(out.Lines, DiffLine{Kind: "=", A: la[ia[op.i]], B: lb[ib[op.j]], LineA: ia[op.i] + 1, LineB: ib[op.j] + 1})
			out.Same++
		case '-':
			out.Lines = append(out.Lines, DiffLine{Kind: "-", A: la[ia[op.i]], LineA: ia[op.i] + 1})
			out.Removed++
		case '+':
			out.Lines = append(out.Lines, DiffLine{Kind: "+", B: lb[ib[op.j]], LineB: ib[op.j] + 1})
			out.Added++
		}
	}
	return out
}

// splitLines breaks a capture into screen lines: "\r\n" and "\n" end a
// line, a bare "\r" is a return to column 0 inside the line, and every line
// goes through ResolveLineEdits, so what is compared, counted or shown is
// what was on the screen — not the device's line-editing bytes.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = ResolveLineEdits(l)
	}
	return lines
}

type editOp struct {
	kind byte // '=', '-', '+'
	i, j int
}

// maxDiffLines caps how many lines (both sides together, once the common
// head and tail are set aside) the shortest edit script is computed for;
// beyond it the middle is compared line for line and the result says so
// (DiffResult.Approx). Memory is linear either way; this bounds the time.
const maxDiffLines = 100_000

// maxEdits is the edit distance one middle-snake search (see middleSnake)
// explores before giving up on that stretch: two captures of one device
// differ in a few lines, so a stretch that needs thousands of edits is a
// different device, a different command, or a log the pager cut short, and
// a line-for-line comparison serves it as well as an exact script that
// would take seconds to find. Scaled with the input so that a large pair
// with many scattered changes still gets the exact answer.
func maxEdits(lines int) int {
	limit := lines / 2
	if limit < 4096 {
		limit = 4096
	}
	if limit > 10000 {
		limit = 10000
	}
	return limit
}

// myers computes a shortest edit script between a and b (Myers 1986, the
// linear-space divide-and-conquer form of section 4b). The earlier version
// kept a copy of the whole frontier for every edit-distance step, O(d·(n+m))
// ints: two unrelated 6,000-line captures took gigabytes. This one keeps
// two frontiers of n+m ints and recurses on the middle snake, so the memory
// is the script itself. The second result says whether a stretch was
// compared line for line instead (see maxDiffLines, maxEdits).
func myers(a, b []string) ([]editOp, bool) {
	n, m := len(a), len(b)
	if n+m == 0 {
		return nil, false
	}
	d := &differ{a: a, b: b, vf: make([]int, 2*(n+m)+4), vb: make([]int, 2*(n+m)+4)}
	d.diff(0, n, 0, m)
	orderEdits(d.ops)
	return d.ops, d.approx
}

// orderEdits puts, within each run of edits between two equal lines, the
// removed lines before the added ones. The UI folds "removed, then added"
// into one changed block shown row for row; which side of a split the
// middle snake lands on could otherwise put an addition first. The ops of
// one run are independent (removals index a, additions b), so their order
// does not change what they rebuild.
func orderEdits(ops []editOp) {
	for i := 0; i < len(ops); {
		if ops[i].kind == '=' {
			i++
			continue
		}
		j := i
		for j < len(ops) && ops[j].kind != '=' {
			j++
		}
		run := ops[i:j]
		var minus, plus []editOp
		for _, op := range run {
			if op.kind == '-' {
				minus = append(minus, op)
			} else {
				plus = append(plus, op)
			}
		}
		copy(run, minus)
		copy(run[len(minus):], plus)
		i = j
	}
}

type differ struct {
	a, b   []string
	ops    []editOp
	approx bool
	vf, vb []int // the forward and backward frontiers, sized for the whole
}

// diff appends the edit script of a[i0:i1] against b[j0:j1], in order.
func (d *differ) diff(i0, i1, j0, j1 int) {
	// The common head and tail cost nothing to find and keep the search to
	// the part that differs.
	for i0 < i1 && j0 < j1 && d.a[i0] == d.b[j0] {
		d.ops = append(d.ops, editOp{'=', i0, j0})
		i0++
		j0++
	}
	tail := 0
	for i0 < i1-tail && j0 < j1-tail && d.a[i1-1-tail] == d.b[j1-1-tail] {
		tail++
	}
	ti, tj := i1-tail, j1-tail
	i1, j1 = ti, tj
	switch {
	case i0 == i1:
		for j := j0; j < j1; j++ {
			d.ops = append(d.ops, editOp{'+', i0, j})
		}
	case j0 == j1:
		for i := i0; i < i1; i++ {
			d.ops = append(d.ops, editOp{'-', i, j0})
		}
	case (i1-i0)+(j1-j0) > maxDiffLines:
		d.approx = true
		d.pairwise(i0, i1, j0, j1)
	default:
		x, y, u, v, ok := d.middleSnake(i0, i1, j0, j1)
		if !ok {
			d.approx = true
			d.pairwise(i0, i1, j0, j1)
			break
		}
		d.diff(i0, x, j0, y)
		for ; x < u; x, y = x+1, y+1 {
			d.ops = append(d.ops, editOp{'=', x, y})
		}
		d.diff(u, i1, v, j1)
	}
	for k := 0; k < tail; k++ {
		d.ops = append(d.ops, editOp{'=', ti + k, tj + k})
	}
}

// pairwise compares a[i0:i1] and b[j0:j1] line for line — the fallback for
// a stretch too large or too different for the shortest script. Equal
// lines at the same offset are kept as such; what is left over on the
// longer side is added or removed.
func (d *differ) pairwise(i0, i1, j0, j1 int) {
	for i0 < i1 && j0 < j1 {
		if d.a[i0] == d.b[j0] {
			d.ops = append(d.ops, editOp{'=', i0, j0})
		} else {
			d.ops = append(d.ops, editOp{'-', i0, j0}, editOp{'+', i0, j0})
		}
		i0++
		j0++
	}
	for ; i0 < i1; i0++ {
		d.ops = append(d.ops, editOp{'-', i0, j0})
	}
	for ; j0 < j1; j0++ {
		d.ops = append(d.ops, editOp{'+', i0, j0})
	}
}

// middleSnake finds the middle snake of a shortest edit script between
// a[i0:i1] and b[j0:j1] — both non-empty, with no common head or tail — by
// running the forward search from the start and the backward search from
// the end until they meet. It returns the snake's ends (x,y)-(u,v) in whole-
// input coordinates, and false when the stretch needs more than maxEdits
// edits (the caller then compares it line for line).
func (d *differ) middleSnake(i0, i1, j0, j1 int) (x, y, u, v int, ok bool) {
	a, b := d.a[i0:i1], d.b[j0:j1]
	n, m := len(a), len(b)
	delta := n - m
	odd := delta&1 != 0
	half := (n + m + 1) / 2
	limit := min(maxEdits(n+m), half)
	// vf[off+k] is the furthest x reached forward on diagonal k = x-y;
	// vb[offb+k] the furthest-back x on diagonal k from the end. Both index
	// ranges fit the scratch arrays sized for the whole input.
	vf, vb := d.vf, d.vb
	off := half + 2
	offb := half + 2 - delta
	vf[off+1] = 0
	vb[offb+delta-1] = n
	for dd := 0; dd <= limit; dd++ {
		for k := -dd; k <= dd; k += 2 {
			var x0 int
			if k == -dd || (k != dd && vf[off+k-1] < vf[off+k+1]) {
				x0 = vf[off+k+1]
			} else {
				x0 = vf[off+k-1] + 1
			}
			y0 := x0 - k
			xx, yy := x0, y0
			for xx < n && yy < m && a[xx] == b[yy] {
				xx++
				yy++
			}
			vf[off+k] = xx
			if odd && k-delta >= -(dd-1) && k-delta <= dd-1 && vf[off+k] >= vb[offb+k] {
				return i0 + x0, j0 + y0, i0 + xx, j0 + yy, true
			}
		}
		for k := delta - dd; k <= delta+dd; k += 2 {
			var x0 int
			if k == delta+dd || (k != delta-dd && vb[offb+k-1] < vb[offb+k+1]) {
				x0 = vb[offb+k-1]
			} else {
				x0 = vb[offb+k+1] - 1
			}
			y0 := x0 - k
			xx, yy := x0, y0
			for xx > 0 && yy > 0 && a[xx-1] == b[yy-1] {
				xx--
				yy--
			}
			vb[offb+k] = xx
			if !odd && k >= -dd && k <= dd && vb[offb+k] <= vf[off+k] {
				return i0 + xx, j0 + yy, i0 + x0, j0 + y0, true
			}
		}
	}
	return 0, 0, 0, 0, false
}
