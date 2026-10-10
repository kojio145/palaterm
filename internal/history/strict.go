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

// ansiSeq matches the terminal escape sequences a device or the local
// terminal may leave in an interactive transcript: CSI (colour, cursor
// movement, erase), OSC (window titles) and single-character ESC sequences.
var ansiSeq = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// ResolveLineEdits turns one raw transcript line into what was on the screen
// when the line was finished. An interactive session log is a byte capture:
// tab completion and corrections arrive as backspace-space-backspace (the
// device erases the partial word and echoes the full one, so "sho<TAB>" is
// recorded as "sho\b \b\b \b\b \bshow "), a bare carriage return rewrites
// the line from column 0, and colour or cursor escapes may be interleaved.
// Comparing the raw bytes against a running-config made every completed
// command look unreflected (2026-10-10, NEC IX log: "sho▯ ▯▯ ▯show run…").
func ResolveLineEdits(line string) string {
	line = ansiSeq.ReplaceAllString(line, "")
	buf := make([]rune, 0, len(line))
	cur := 0 // cursor column in buf
	for _, r := range line {
		switch r {
		case '\b', 0x7f:
			if cur > 0 {
				cur--
			}
		case '\r':
			cur = 0
		case 0x07, 0x00:
			// bell, NUL: nothing on screen
		default:
			if cur < len(buf) {
				buf[cur] = r
			} else {
				buf = append(buf, r)
			}
			cur++
		}
	}
	// Text right of the final cursor stays: a device erases only with
	// "\b \b", so a bare backspace leaves the character visible.
	return string(buf)
}

// CleanTranscript applies ResolveLineEdits to every line of a transcript.
func CleanTranscript(log string) string {
	lines := splitLines(log)
	for i, l := range lines {
		lines[i] = ResolveLineEdits(l)
	}
	return strings.Join(lines, "\n")
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
		raw = ResolveLineEdits(raw)
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
		if k := normCmd(ResolveLineEdits(l)); k != "" {
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
