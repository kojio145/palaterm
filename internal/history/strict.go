package history

import (
	"regexp"
	"strings"
)

// Strict verification: what was typed during the work, and whether each of
// those lines is in the device's state afterwards.
//
// The work log (a batch tagged 作業中, or an interactive session) is a device
// transcript: every command appears echoed after a prompt. The lines that
// configure something are picked out — navigation, show commands and saves
// are not settings — and each is looked for, as a whole line, in the after
// log (which normally holds a show running-config). A "no …" line is
// reflected when the thing it removes is gone from the after log (or at
// least rarer than in the before log).

// CommandCheck is one configuration line from the work log and its fate.
type CommandCheck struct {
	Line   string `json:"line"`
	Status string `json:"status"` // reflected | already | missing
	// Already: the line was in the before log too (it was reflected, but
	// not by this work — or the work re-applied an existing setting).
}

// StrictResult is the outcome for one device.
type StrictResult struct {
	Commands  []CommandCheck `json:"commands"`
	Reflected int            `json:"reflected"` // reflected + already
	Missing   int            `json:"missing"`
	Typed     int            `json:"typed"` // command lines seen in the work log, settings or not
}

// promptLine matches a transcript line that is a prompt followed by what was
// typed: "R1#show run", "IX-A(config)# hostname x", "FG (port1) # set ip …",
// "user@host> show". Output lines ("% Invalid input") do not start that way.
var promptLine = regexp.MustCompile(`^([A-Za-z0-9][\w.\-@~:/]*(?:\([^)]*\))?(?:\s\([^)]*\))?\s?[#>])\s*(\S.*)$`)

// notSetting lists command words that read, navigate, save or run things
// rather than configure them. Vendor-mixed on purpose.
var notSetting = regexp.MustCompile(`(?i)^(show|sh|get|display|dir|more|ping|traceroute|tracert|telnet|ssh|exit|end|quit|logout|configure|conf|config|svintr-config|terminal|term|write|wr|copy|commit|save|rollback|execute|exec|diagnose|clear|debug|undebug|reload|reboot|enable|disable|do|run|cls|help|\?|history|where|top|up|edit|next|abort|discard|load|compare|monitor|test|verify|y|yes|n|no\s*$)(\s|$)`)

var ws = regexp.MustCompile(`\s+`)

// oscSeq matches an OSC sequence (window title etc.): ESC ] … BEL / ESC \.
var oscSeq = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// ResolveLineEdits turns one raw transcript line into what was on the
// screen when the line was finished. An interactive session log is a byte
// capture: tab completion and corrections arrive as the device's own line
// editing — backspace-space-backspace on NEC IX / Cisco ("sho\b \b\b \b\b
// \bshow "), backspace plus erase-to-end-of-line (ESC[K) on readline-style
// gear (Linux, Juniper, Arista), cursor moves (ESC[nD / ESC[nC / ESC[nG),
// deletes (ESC[nP) — a bare carriage return rewrites the line from column
// 0 (a pager's "--More--\r        \r"), and colour escapes may be
// interleaved. Comparing the raw bytes against a running-config made every
// completed command look unreflected (2026-10-10, NEC IX log). Editing
// sequences are applied, everything else in ESC sequences is dropped, and a
// line with no control characters at all is returned as it is.
func ResolveLineEdits(line string) string {
	if !strings.ContainsAny(line, "\b\x7f\r\x1b\x07\x00") {
		return line
	}
	line = oscSeq.ReplaceAllString(line, "")
	rs := []rune(line)
	buf := make([]rune, 0, len(rs))
	cur := 0 // cursor column in buf
	put := func(r rune) {
		for cur > len(buf) {
			buf = append(buf, ' ')
		}
		if cur < len(buf) {
			buf[cur] = r
		} else {
			buf = append(buf, r)
		}
		cur++
	}
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; r {
		case '\b', 0x7f:
			if cur > 0 {
				cur--
			}
		case '\r':
			cur = 0
		case 0x07, 0x00:
			// bell, NUL: nothing on screen
		case 0x1b:
			if i+1 >= len(rs) {
				break
			}
			if rs[i+1] != '[' {
				i++ // two-character ESC sequence (ESC 7, ESC =, …)
				break
			}
			// CSI: parameter bytes, then one final byte in 0x40–0x7e.
			j := i + 2
			for j < len(rs) && (rs[j] < 0x40 || rs[j] > 0x7e) {
				j++
			}
			if j >= len(rs) {
				i = len(rs)
				break
			}
			n, hasN := csiParam(rs[i+2 : j])
			i = j
			switch rs[j] {
			case 'K': // erase in line
				switch {
				case !hasN || n == 0:
					if cur < len(buf) {
						buf = buf[:cur]
					}
				case n == 1:
					for k := 0; k < cur && k < len(buf); k++ {
						buf[k] = ' '
					}
				default:
					buf = buf[:0]
				}
			case 'D': // cursor left
				cur -= max(n, 1)
				if cur < 0 {
					cur = 0
				}
			case 'C': // cursor right
				cur += max(n, 1)
			case 'G': // cursor to column (1-based)
				cur = max(n, 1) - 1
			case 'P': // delete characters at the cursor
				if cur < len(buf) {
					e := cur + max(n, 1)
					if e > len(buf) {
						e = len(buf)
					}
					buf = append(buf[:cur], buf[e:]...)
				}
			}
			// colour (m), modes (h/l), clears of the whole screen (J) and
			// the rest leave the line's text alone
		default:
			put(r)
		}
	}
	return string(buf)
}

// csiParam reads the leading number of a CSI parameter string ("3;1" → 3,
// "?25" → 25) and whether there was one.
func csiParam(p []rune) (int, bool) {
	n, has := 0, false
	for _, r := range p {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
			has = true
			continue
		}
		if has {
			break
		}
	}
	return n, has
}

// CleanTranscript returns a transcript as it was on the screen, line by
// line (see splitLines).
func CleanTranscript(log string) string {
	return strings.Join(splitLines(log), "\n")
}

func normCmd(s string) string {
	return strings.ToLower(strings.TrimSpace(ws.ReplaceAllString(s, " ")))
}

// ExtractConfigCommands returns the setting lines typed in a work transcript,
// in order, deduplicated, and the number of command lines of any kind.
func ExtractConfigCommands(workLog string) ([]string, int) {
	var out []string
	seen := map[string]bool{}
	typed := 0
	for _, raw := range splitLines(workLog) {
		m := promptLine.FindStringSubmatch(strings.TrimRight(raw, " \t"))
		if m == nil {
			continue
		}
		cmd := strings.TrimSpace(m[2])
		typed++
		if cmd == "" || notSetting.MatchString(cmd) {
			continue
		}
		k := normCmd(cmd)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, cmd)
	}
	return out, typed
}

// lineCounts indexes a log by normalised line.
func lineCounts(log string) map[string]int {
	m := map[string]int{}
	for _, l := range splitLines(log) {
		if k := normCmd(l); k != "" {
			m[k]++
		}
	}
	return m
}

// StrictVerify checks every setting line of workLog against afterLog, using
// beforeLog to tell a line the work added from one that was already there.
func StrictVerify(workLog, beforeLog, afterLog string) StrictResult {
	cmds, typed := ExtractConfigCommands(workLog)
	res := StrictResult{Commands: []CommandCheck{}, Typed: typed}
	before := lineCounts(beforeLog)
	after := lineCounts(afterLog)
	for _, c := range cmds {
		k := normCmd(c)
		cc := CommandCheck{Line: c}
		if strings.HasPrefix(k, "no ") {
			// Two conventions: Cisco-style gear drops the thing ("no shutdown"
			// makes the "shutdown" line disappear); NEC IX / Juniper-style gear
			// keeps the negation itself in the configuration as a line. Take
			// the literal line first, the removal rule otherwise.
			target := strings.TrimSpace(k[3:])
			switch {
			case after[k] > 0 && after[k] > before[k]:
				cc.Status = "reflected" // "no shutdown" is itself in the config now
			case after[k] > 0:
				cc.Status = "already" // and was already there before
			case after[target] == 0 && before[target] == 0:
				cc.Status = "already" // nothing to remove either time
			case after[target] < before[target] || after[target] == 0:
				cc.Status = "reflected" // the thing it negates is gone
			default:
				cc.Status = "missing"
			}
		} else {
			switch {
			case after[k] == 0:
				cc.Status = "missing"
			case before[k] >= after[k]:
				cc.Status = "already"
			default:
				cc.Status = "reflected"
			}
		}
		if cc.Status == "missing" {
			res.Missing++
		} else {
			res.Reflected++
		}
		res.Commands = append(res.Commands, cc)
	}
	return res
}
