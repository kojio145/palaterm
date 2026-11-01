package history

import (
	"regexp"
	"strings"
)

// DiffWith is the structured comparison of two captures (see difftree.go);
// Diff is the plain line comparison it grew from and is kept as it was.

// DiffOpts selects what is compared and what is left out.
type DiffOpts struct {
	// IgnoreNoise: clock readings, uptimes and counters count as equal, and
	// blank lines and bare prompts are left out (as in Diff).
	IgnoreNoise bool `json:"ignoreNoise"`
	// IgnoreSecrets: certificates, encrypted values and UUIDs are left out
	// of the comparison and shown dimmed (see secretMask).
	IgnoreSecrets bool `json:"ignoreSecrets"`
	// ConfigOnly: only the configuration listing of each log (show
	// running-config and the like, see configSection) is compared. When
	// either log has none the whole logs are compared and the result says
	// so (DiffResult.Mode, ConfigA, ConfigB).
	ConfigOnly bool `json:"configOnly"`
}

// Jump is a place in the result the window can go to: a section heading,
// or a command typed in the log.
type Jump struct {
	Name  string `json:"name"`
	Group string `json:"group"`
	Row   int    `json:"row"` // index into DiffResult.Lines
}

// Counts is the tally of one comparison.
type Counts struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
	Moved   int `json:"moved"`
}

// Any says whether anything differs.
func (c Counts) Any() bool { return c.Added+c.Removed+c.Changed+c.Moved > 0 }

// DiffSummary is what the comparison tab shows per device: the counts of
// the whole logs and of their configuration listings.
type DiffSummary struct {
	Whole   Counts `json:"whole"`
	Config  Counts `json:"config"` // meaningful when ConfigA && ConfigB
	ConfigA bool   `json:"configA"`
	ConfigB bool   `json:"configB"`
	Approx  bool   `json:"approx,omitempty"`
	// Impact of the configuration changes (when both sides have a listing).
	Impact string `json:"impact,omitempty"` // high / mid / low / none
	High   int    `json:"high,omitempty"`
	Mid    int    `json:"mid,omitempty"`
	Score  int    `json:"score,omitempty"`
}

// Limits of the jump lists.
const maxJumps = 3000

// morePrompt is the pager marker the vendors print, as the runner's
// genericMore (internal/runner/runner.go) knows it: "--More--", "---- More
// ----", "-- MORE --, next page: …" (ArubaOS), "---(more 25%)---" (Junos).
// The two are kept the same text: a pager the run could not turn off
// leaves exactly these lines in the log.
const morePrompt = `-{2,}\s*\(?\s*more`

// pagerLine: a line that is nothing but the pager's prompt (morePrompt
// with whatever follows it on the line, "More: …", "Press any key to
// continue", EXOS's "Press <SPACE> to continue or <Q> to quit:", "<space>
// …"); pagerText is the pager's marker left inside a line.
var (
	pagerLine = regexp.MustCompile(`(?i)^\s*(` + morePrompt + `\b.*|more:\s.*|press\s+(any key|<space>|<enter>|q\b).*|<space>.*)\s*$`)
	pagerText = regexp.MustCompile(`(?i)` + morePrompt + `(?:\s+\d+%)?\s*\)?\s*-*`)
)

// DiffWith compares two captures with the structured comparison.
func DiffWith(a, b string, o DiffOpts) DiffResult {
	la, lb := splitLines(a), splitLines(b)
	selA, kindA := configSectionIdx(la)
	selB, kindB := configSectionIdx(lb)
	res := DiffResult{Mode: "whole", ConfigA: kindA != noListing, ConfigB: kindB != noListing}
	// The listing's lines get the listing normaliser (normalizeConfig):
	// every compared line when the listings alone are compared, the lines
	// of a listing found among the show commands when the whole logs are.
	var cfgA, cfgB []bool
	if o.ConfigOnly && res.ConfigA && res.ConfigB {
		res.Mode = "config"
		cfgA, cfgB = listingSet(len(la), selA, oneListing), listingSet(len(lb), selB, oneListing)
	} else {
		cfgA, cfgB = listingSet(len(la), selA, kindA), listingSet(len(lb), selB, kindB)
		selA, selB = allIdx(len(la)), allIdx(len(lb))
	}
	da, leadA, na, sa := prepLines(la, selA, cfgA, o)
	db, leadB, nb, sb := prepLines(lb, selB, cfgB, o)
	res.NoiseLines, res.SecretLines = na+nb, sa+sb
	p := &pairer{}
	for i := range leadA {
		p.noiseRows(&leadA[i], 'a')
	}
	for i := range leadB {
		p.noiseRows(&leadB[i], 'b')
	}
	p.kids(buildTree(da), buildTree(db), "")
	res.Lines = p.out
	if res.Lines == nil {
		res.Lines = []DiffLine{}
	}
	res.Added, res.Removed, res.Changed, res.Moved, res.Same, res.Approx = p.added, p.removed, p.changed, p.moved, p.same, p.approx
	res.Sections, res.Commands = jumps(res.Lines, res.Mode == "whole")
	if res.Mode == "config" {
		imp := Impact(res)
		res.Impact = &imp
	}
	return res
}

// Summarize is DiffWith's counts for the whole logs and, when both have a
// configuration listing, for the listings alone.
func Summarize(a, b string, o DiffOpts) DiffSummary {
	o.ConfigOnly = false
	w := DiffWith(a, b, o)
	s := DiffSummary{Whole: counts(w), ConfigA: w.ConfigA, ConfigB: w.ConfigB, Approx: w.Approx}
	if w.ConfigA && w.ConfigB {
		o.ConfigOnly = true
		c := DiffWith(a, b, o)
		s.Config = counts(c)
		s.Approx = s.Approx || c.Approx
		if c.Impact != nil {
			s.Impact, s.High, s.Mid, s.Score = c.Impact.Level, c.Impact.High, c.Impact.Mid, c.Impact.Score
		}
	}
	return s
}

func counts(r DiffResult) Counts {
	return Counts{Added: r.Added, Removed: r.Removed, Changed: r.Changed, Moved: r.Moved}
}

func allIdx(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

// listingSet marks, for a capture of n lines, the lines that belong to its
// configuration listing (sel and kind from configSectionIdx); none unless
// the capture holds a listing among its show commands (a file with no
// prompts at all may be a saved listing or a saved show output: compared
// whole, it keeps the noise rules).
func listingSet(n int, sel []int, kind int) []bool {
	cfg := make([]bool, n)
	if kind != oneListing {
		return cfg
	}
	for _, k := range sel {
		if k >= 0 {
			cfg[k] = true
		}
	}
	return cfg
}

// prepLines turns the selected lines of a capture into compared lines:
// each with its key and depth, the noise left out (and counted), the
// secret lines hung on the line before them. lead holds the secret lines
// before the first compared line. cfg says which lines are the
// configuration listing's: those lose only the listing's stamps, the rest
// the clock and counter noise too (see normalizeConfig).
func prepLines(lines []string, sel []int, cfg []bool, o DiffOpts) (out []dline, lead []dline, noise, secret int) {
	raw := make([]string, 0, len(sel))
	for _, k := range sel {
		if k < 0 {
			raw = append(raw, "")
		} else {
			raw = append(raw, lines[k])
		}
	}
	mask := secretMask(raw, o.IgnoreSecrets)
	last := -1 // index into out of the line secrets hang on
	// bannerDelim: the delimiter of a Cisco banner that is open, so its
	// closing line ("#", "$" — which reads as a bare prompt) is kept and
	// buildTree sees where the banner ends (C S-9, with H2's bannerBlock).
	bannerDelim := ""
	for n, k := range sel {
		if k < 0 {
			out = append(out, dline{brk: true})
			last = -1
			bannerDelim = ""
			continue
		}
		l := lines[k]
		depth := len(l) - len(strings.TrimLeft(l, " \t"))
		if mask[n] {
			secret++
			d := dline{text: l, idx: k, depth: depth}
			if last < 0 {
				lead = append(lead, d)
			} else {
				out[last].noise = append(out[last].noise, d)
			}
			continue
		}
		key := strings.TrimRight(l, " \t\r")
		if o.IgnoreNoise {
			if pagerLine.MatchString(key) {
				noise++
				continue
			}
			key = pagerText.ReplaceAllString(key, "")
			if cfg[k] {
				key = normalizeConfig(key)
			} else {
				key = normalize(key)
			}
			if bannerDelim != "" && strings.Contains(strings.TrimSpace(l), bannerDelim) {
				bannerDelim = "" // the banner's closing delimiter: kept
			} else if key == "" || barePrompt.MatchString(key) {
				noise++
				continue
			}
			if _, d, ok := bannerBlock(key); ok {
				bannerDelim = d
			}
		}
		out = append(out, dline{text: l, key: key, idx: k, depth: depth})
		last = len(out) - 1
	}
	return out, lead, noise, secret
}

// jumps collects the section headings of the result and, for whole logs,
// the commands typed in them.
func jumps(lines []DiffLine, commands bool) (sections, cmds []Jump) {
	seenS, seenC := map[string]bool{}, map[string]bool{}
	type level struct {
		depth int
		key   string
	}
	var parents []level // the open headings
	for i, l := range lines {
		if l.Noise {
			continue
		}
		text := l.B
		if l.Kind == "-" {
			text = l.A
		}
		key := strings.Join(strings.Fields(text), " ")
		if l.Head {
			for len(parents) > 0 && parents[len(parents)-1].depth >= l.Depth {
				parents = parents[:len(parents)-1]
			}
			parent := ""
			if len(parents) > 0 {
				parent = parents[len(parents)-1].key
			}
			parents = append(parents, level{l.Depth, key})
			lk := strings.ToLower(key)
			group := ""
			switch {
			case parent == "":
				if f := strings.Fields(lk); len(f) > 0 {
					group = f[0]
				}
			case strings.HasPrefix(lk, "edit ") && strings.HasPrefix(strings.ToLower(parent), "config "):
				group = parent
			default:
				continue
			}
			if !seenS[key] && len(sections) < maxJumps {
				seenS[key] = true
				sections = append(sections, Jump{Name: key, Group: group, Row: i})
			}
			continue
		}
		if !commands {
			continue
		}
		m := promptParts(strings.TrimRight(text, " \t"))
		if m == nil {
			continue
		}
		cmd := strings.Join(strings.Fields(m[2]), " ")
		if cmd == "" || seenC[cmd] || len(cmds) >= maxJumps {
			continue
		}
		seenC[cmd] = true
		group := strings.ToLower(strings.Fields(cmd)[0])
		cmds = append(cmds, Jump{Name: cmd, Group: group, Row: i})
	}
	return sections, cmds
}
