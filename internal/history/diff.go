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
}

// noise matches the parts of a line that differ between any two captures of
// the same device without meaning anything: clock readings, uptime and
// traffic counters. They are blanked before comparing so a before/after diff
// shows what changed in the configuration and state, not that time passed.
// The original text is still what the UI shows.
var noise = []*regexp.Regexp{
	regexp.MustCompile(`\d{1,2}:\d{2}:\d{2}(\.\d+)?`),                         // hh:mm:ss
	regexp.MustCompile(`(?i)\b(uptime is|up for)\b.*$`),                       // uptime lines
	regexp.MustCompile(`(?i)\b\d+ (weeks?|days?|hours?|minutes?|seconds?)\b`), // 3 days, 2 hours
	regexp.MustCompile(`(?i)\b(packets (input|output)|bytes|bits/sec|packets/sec|input rate|output rate|CPU utilization|Memory|free|used)\b.*$`),
	regexp.MustCompile(`\b\d{4}[-/]\d{2}[-/]\d{2}\b`), // dates
	// Compact ages such as a route's "37d20h54m31s" or "2w3d", and
	// "00:12:34"-less uptimes like "5h32m".
	regexp.MustCompile(`\b(?:\d+[wdhms]){2,}\b`),
}

func normalize(line string) string {
	line = strings.TrimRight(line, " \t\r")
	for _, re := range noise {
		line = re.ReplaceAllString(line, "")
	}
	return strings.TrimSpace(line)
}

// barePrompt matches a line that is nothing but a prompt ("R1#", "IX-A(config)# ",
// "user@host:~$"): the device reprints it for every Enter and for timing
// reasons, so two captures differ in how many of these they hold.
var barePrompt = regexp.MustCompile(`^[\w.@~:/()\-]*[#>$%]\s*$`)

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
	for _, op := range myers(ka, kb) {
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

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

type editOp struct {
	kind byte // '=', '-', '+'
	i, j int
}

// myers computes a shortest edit script between a and b (Myers 1986, the
// linear-space variant is unnecessary at log sizes: a few thousand lines).
func myers(a, b []string) []editOp {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}
	// v[k] = furthest x on diagonal k; stored with offset.
	v := make([]int, 2*max+2)
	var trace [][]int
	off := max
	var x, y int
	found := false
	for d := 0; d <= max && !found; d++ {
		vc := make([]int, len(v))
		copy(vc, v)
		trace = append(trace, vc)
		for k := -d; k <= d; k += 2 {
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y = x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	// Backtrack.
	var ops []editOp
	x, y = n, m
	for d := len(trace) - 1; d >= 0; d-- {
		vv := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vv[off+k-1] < vv[off+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vv[off+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			ops = append(ops, editOp{'=', x, y})
		}
		if d > 0 {
			if x == prevX {
				y--
				ops = append(ops, editOp{'+', x, y})
			} else {
				x--
				ops = append(ops, editOp{'-', x, y})
			}
		}
	}
	// Reverse.
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
