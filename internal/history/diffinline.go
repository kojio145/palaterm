package history

import (
	"strings"
	"unicode"
)

// Inline (within-line) differences and the pairing of changed lines.
//
// A changed setting is one line in A and one in B that say the same thing
// with a different value: the window highlights the value, not the line.
// Which deleted line goes with which added one is decided in pairLeaves
// (by the setting's name, then by how many words they share), and the
// segments of a pair come from inlineDiff: a token-level script first, the
// replaced tokens split again by character, prefixes and suffixes snapped
// to a delimiter so "100,200" against "100,250" lights "200" / "250" and
// not "00" / "50".

// Seg is one stretch of a changed line: the same on both sides, only in A
// or only in B.
type Seg struct {
	Kind string `json:"k"` // "=" / "-" / "+"
	Text string `json:"t"`
}

// Inline limits.
const (
	inlineMaxTokens = 200     // token script above this many tokens on a side
	inlineMaxChars  = 200_000 // character script above this product of lengths
	// inlineMaxChanged: a line changed in more than this share of its
	// characters is shown as a whole changed line — a highlight cut into
	// slivers reads worse than none.
	inlineMaxChanged = 0.8
)

// inlineDiff returns the segments of a and b, or nil, nil when the two have
// too little in common for a highlight to help.
func inlineDiff(a, b string) ([]Seg, []Seg) {
	if a == b {
		return []Seg{{"=", a}}, []Seg{{"=", b}}
	}
	ta, tb := tokenize(a), tokenize(b)
	var sa, sb []Seg
	if len(ta) >= 2 && len(tb) >= 2 && len(ta) <= inlineMaxTokens && len(tb) <= inlineMaxTokens {
		sa, sb = tokenDiff(ta, tb)
	} else {
		sa, sb = charDiff([]rune(a), []rune(b))
	}
	sa, sb = mergeSegs(sa), mergeSegs(sb)
	if len(sa) == 0 || len(sb) == 0 || tooChanged(sa, a) || tooChanged(sb, b) {
		return nil, nil
	}
	return sa, sb
}

// tooChanged: the changed share of the line is above inlineMaxChanged.
func tooChanged(segs []Seg, whole string) bool {
	changed := 0
	for _, s := range segs {
		if s.Kind != "=" {
			changed += len([]rune(s.Text))
		}
	}
	n := len([]rune(whole))
	return n > 0 && float64(changed)/float64(n) >= inlineMaxChanged
}

// tokenize splits a line into runs of non-blank and blank characters, in
// turn, so that joining the tokens gives the line back.
func tokenize(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	for i := 1; i <= len(rs); i++ {
		if i == len(rs) || unicode.IsSpace(rs[i]) != unicode.IsSpace(rs[start]) {
			out = append(out, string(rs[start:i]))
			start = i
		}
	}
	return out
}

// tokenDiff: the token script of a and b, replaced tokens split by
// character.
func tokenDiff(ta, tb []string) ([]Seg, []Seg) {
	ops, _ := myers(ta, tb)
	var sa, sb []Seg
	for i := 0; i < len(ops); {
		if ops[i].kind == '=' {
			sa = append(sa, Seg{"=", ta[ops[i].i]})
			sb = append(sb, Seg{"=", tb[ops[i].j]})
			i++
			continue
		}
		var del, ins []string
		for ; i < len(ops) && ops[i].kind != '='; i++ {
			if ops[i].kind == '-' {
				del = append(del, ta[ops[i].i])
			} else {
				ins = append(ins, tb[ops[i].j])
			}
		}
		k := 0
		for ; k < len(del) && k < len(ins); k++ {
			ca, cb := charDiff([]rune(del[k]), []rune(ins[k]))
			sa = append(sa, ca...)
			sb = append(sb, cb...)
		}
		for ; k < len(del); k++ {
			sa = append(sa, Seg{"-", del[k]})
		}
		for k2 := len(del); k2 < len(ins); k2++ {
			sb = append(sb, Seg{"+", ins[k2]})
		}
	}
	return sa, sb
}

// isDelim: the characters a highlight should not start or end in the
// middle of a run between.
func isDelim(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(",/:;-.=", r)
}

// charDiff: the character script of a and b. The common prefix and suffix
// are taken first and pulled back to a delimiter, the middle is the
// shortest script when the lengths allow it, else one replaced stretch.
func charDiff(a, b []rune) ([]Seg, []Seg) {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	for p > 0 && p < len(a) && p < len(b) && !isDelim(a[p-1]) {
		p--
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	for s > 0 && !isDelim(a[len(a)-s]) {
		s--
	}
	ma, mb := a[p:len(a)-s], b[p:len(b)-s]
	var sa, sb []Seg
	if p > 0 {
		sa = append(sa, Seg{"=", string(a[:p])})
		sb = append(sb, Seg{"=", string(b[:p])})
	}
	switch {
	case len(ma) == 0 && len(mb) == 0:
	case len(ma) == 0:
		sb = append(sb, Seg{"+", string(mb)})
	case len(mb) == 0:
		sa = append(sa, Seg{"-", string(ma)})
	case len(ma)*len(mb) > inlineMaxChars || (!hasDelim(ma) && !hasDelim(mb)):
		// One word against one word ("10" / "11", "20" / "25"): the whole
		// word is what changed, not the characters they happen to share.
		sa = append(sa, Seg{"-", string(ma)})
		sb = append(sb, Seg{"+", string(mb)})
	default:
		xa, xb := make([]string, len(ma)), make([]string, len(mb))
		for i, r := range ma {
			xa[i] = string(r)
		}
		for i, r := range mb {
			xb[i] = string(r)
		}
		ops, _ := myers(xa, xb)
		for _, op := range ops {
			switch op.kind {
			case '=':
				sa = append(sa, Seg{"=", xa[op.i]})
				sb = append(sb, Seg{"=", xb[op.j]})
			case '-':
				sa = append(sa, Seg{"-", xa[op.i]})
			case '+':
				sb = append(sb, Seg{"+", xb[op.j]})
			}
		}
	}
	if s > 0 {
		sa = append(sa, Seg{"=", string(a[len(a)-s:])})
		sb = append(sb, Seg{"=", string(b[len(b)-s:])})
	}
	return sa, sb
}

func hasDelim(rs []rune) bool {
	for _, r := range rs {
		if isDelim(r) {
			return true
		}
	}
	return false
}

// mergeSegs joins neighbouring segments of one kind.
func mergeSegs(segs []Seg) []Seg {
	var out []Seg
	for _, s := range segs {
		if s.Text == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Kind == s.Kind {
			out[n-1].Text += s.Text
			continue
		}
		out = append(out, s)
	}
	return out
}

// similarity is the Jaccard index of the two lines' word sets: how much of
// what they say is the same.
func similarity(a, b string) float64 {
	wa, wb := strings.Fields(a), strings.Fields(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, w := range wa {
		set[w] = true
	}
	both := 0
	seen := map[string]bool{}
	for _, w := range wb {
		if set[w] && !seen[w] {
			both++
			seen[w] = true
		}
	}
	union := len(set)
	for _, w := range wb {
		if !set[w] && !seen[w] {
			union++
			seen[w] = true
		}
	}
	return float64(both) / float64(union)
}
