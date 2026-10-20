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
// are not settings — and each is looked for in the after log (which
// normally holds a show running-config), inside the block it was typed in
// when that is known. Devices store a setting the way they print it, not
// the way it was typed, so the lookup accepts abbreviations, encrypted
// secrets, merged VLAN lists, quotes and Junos's brace format (see
// strict_match.go). A "no …" line is reflected when the thing it removes
// is gone from the after log (or at least rarer than in the before log).

// CommandCheck is one configuration line from the work log and its fate.
type CommandCheck struct {
	Line string `json:"line"`
	// reflected | already | superseded | missing | unverified
	// Already: the line was in the before log too (it was reflected, but
	// not by this work — or the work re-applied an existing setting).
	// Superseded: a later line of the same work undid or replaced it
	// ("shutdown" then "no shutdown", an address typed again corrected).
	// Unverified: the logs cannot tell (see Note); not counted as missing.
	Status string `json:"status"`
	Parent string `json:"parent,omitempty"` // the block it was typed in
	Match  string `json:"match,omitempty"`  // the after-log line it matched, when not the typed text
	// Note says how it was matched or why it could not be: abbrev, similar,
	// secret, vlan, otherBlock, noConfig, notStored, default.
	Note string `json:"note,omitempty"`
}

// StrictResult is the outcome for one device.
type StrictResult struct {
	Commands   []CommandCheck `json:"commands"`
	Reflected  int            `json:"reflected"` // reflected + already + superseded
	Missing    int            `json:"missing"`
	Unverified int            `json:"unverified"`
	// NoConfig: the after log shows no configuration (no show
	// running-config or the like), so nothing could be looked up.
	NoConfig bool `json:"noConfig,omitempty"`
	Typed    int  `json:"typed"` // command lines seen in the work log, settings or not
	// Unexpected: configuration lines that changed between the before and
	// after logs although no typed setting accounts for them (the first
	// maxUnexpected of UnexpectedCount).
	Unexpected      []ChangedLine `json:"unexpected"`
	UnexpectedCount int           `json:"unexpectedCount"`
}

// ChangedLine is one configuration line added (+) or removed (-) between the
// before and after logs, with the block it is in.
type ChangedLine struct {
	Kind  string `json:"kind"`
	Line  string `json:"line"`
	Block string `json:"block,omitempty"`
}

// promptLine matches a transcript line that is a prompt followed by what was
// typed: "R1#show run", "IX-A(config)# hostname x", "FG (port1) # set ip …",
// "user@host> show". Output lines ("% Invalid input") do not start that way.
// AlaxalA marks a configuration not saved yet with "!" before the prompt
// ("!sw(config)# interface vlan 10"): every line typed after the first
// change has it, and missing it left those lines out (user report
// 2026-10-20).
var promptLine = regexp.MustCompile(`^(!?[A-Za-z0-9][\w.\-@~:/]*(?:\([^)]*\))?(?:\s\([^)]*\))?\s?[#>])\s*(\S.*)$`)

// notSetting lists command words that read, navigate, save or run things
// rather than configure them. Vendor-mixed on purpose.
var notSetting = regexp.MustCompile(`(?i)^(show|sh|get|display|dir|more|ping|traceroute|tracert|telnet|ssh|exit|end|quit|logout|configure|conf|config|svintr-config|terminal|term|write|wr|copy|commit|save|rollback|execute|exec|diagnose|clear|debug|undebug|reload|reboot|enable\s*$|disable\s*$|do|run|cls|help|\?|history|where|top|up|edit|next|abort|discard|load|compare|monitor|test|verify|y|yes|n|no\s*$)(\s|$)`)

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

// refusedMark matches what devices print right after a command they did
// not take: "% Invalid input detected at '^' marker.", "% Incomplete
// command.", "% Ambiguous command", NEC IX "% x -- Invalid command.",
// "Unrecognized command", "syntax error", bash "command not found", and the
// caret line that points at the error.
var refusedMark = regexp.MustCompile(`(?i)(^\s*\^\s*$|%\s*(invalid|incomplete|ambiguous|unknown|unrecognized)|invalid (input|command)|unrecognized command|unknown command|syntax error|command not found|command fail|error: )`)

// refusedWithin is how many output lines after a command are looked at for
// a refusal (the device answers at once; later lines are output).
const refusedWithin = 3

// ExtractConfigCommands returns the setting lines typed in a work transcript,
// in order, deduplicated, and the number of command lines of any kind.
//
// A prompt line is not always a command that ran. Two kinds are left out,
// because counting them made a mistyped "show" or a half-typed word look
// like a setting the device had not taken (user report 2026-10-13):
//
//   - refused: the device answered with an error ("% Invalid input …",
//     the "^" marker, "command not found") — a typo, or "de" + Enter;
//   - redrawn: "?" help or an ambiguous Tab listed candidates and the device
//     printed the prompt again with what was typed so far ("…#de" → list →
//     "…#description x"). The earlier line is the same prompt, the later
//     one continues it, and output came in between. (Two real commands in a
//     row show no output between them, so a setting that extends the one
//     before it is still counted.)
func ExtractConfigCommands(workLog string) ([]string, int) {
	cmds, typed := extractTyped(workLog)
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.line
	}
	return out, typed
}

// typedCmd is one setting line of the work log: as typed, the key it is
// looked up by (a Juniper "set" under "edit x" gets the x prefix), and the
// block it was typed in ("interface Gi0/1" for a line at a (config-if)
// prompt; "" for a global line or when that is not known).
type typedCmd struct {
	line, key, parent string
	last              int // position of its last occurrence in the work log
}

// junosEdit matches Junos's "[edit interfaces ge-0/0/0]" context banner.
var junosEdit = regexp.MustCompile(`^\[edit(?:\s+(.+?))?\]$`)

// promptMode is the mode in a prompt's last parentheses: "config",
// "config-if", "" for none.
func promptMode(prompt string) string {
	i := strings.LastIndex(prompt, "(")
	j := strings.LastIndex(prompt, ")")
	if i < 0 || j < i {
		return ""
	}
	return strings.ToLower(prompt[i+1 : j])
}

func isSubMode(m string) bool { return strings.HasPrefix(m, "config-") }

func extractTyped(workLog string) ([]typedCmd, int) {
	type entry struct {
		prompt, cmd, junos string
		after              []string // output lines up to the next prompt line
	}
	var entries []entry
	junos := ""
	for _, raw := range splitLines(workLog) {
		line := strings.TrimRight(raw, " \t")
		if m := promptLine.FindStringSubmatch(line); m != nil {
			entries = append(entries, entry{prompt: m[1], cmd: strings.TrimSpace(m[2]), junos: junos})
			continue
		}
		if m := junosEdit.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			junos = m[1]
		}
		if n := len(entries); n > 0 && strings.TrimSpace(line) != "" {
			entries[n-1].after = append(entries[n-1].after, line)
		}
	}
	// The block each line was typed in, from the prompts: a command at the
	// top (config) prompt followed by a sub-mode prompt opens a block; the
	// same kind of command at the sub-mode prompt ("interface Gi0/2" while
	// in config-if) opens the next one; a nested sub-mode (address-family)
	// (config-router → config-router-af) stays in the outer block, another
	// sub-mode reached from a sub-mode is a new block; a sub-mode line followed by the top prompt
	// was a global command (the device left the mode for it), and the top
	// prompt ends the block.
	parents := make([]string, len(entries))
	parent := ""
	for i, e := range entries {
		mode := promptMode(e.prompt)
		if !isSubMode(mode) {
			parent = ""
		}
		parents[i] = parent
		if i+1 >= len(entries) {
			break
		}
		next := promptMode(entries[i+1].prompt)
		switch {
		case !isSubMode(next):
			if isSubMode(mode) && !notSetting.MatchString(e.cmd) {
				parents[i] = ""
			}
			parent = ""
		case !isSubMode(mode):
			parent = e.cmd
		case next == mode:
			if sameKind(e.cmd, parent) {
				parents[i], parent = "", e.cmd
			}
		case !strings.HasPrefix(next, mode+"-"):
			parents[i], parent = "", e.cmd // another kind of block straight from a sub-mode
		}
	}
	// On gear with (config) prompts, a line at a prompt without a mode
	// ("R1#delete flash:x", "R1#clock set …") is an exec command, not a
	// setting.
	modal := false
	for _, e := range entries {
		if strings.HasPrefix(promptMode(e.prompt), "config") {
			modal = true
			break
		}
	}
	var out []typedCmd
	seen := map[string]int{}
	typed := 0
	for i, e := range entries {
		typed++
		cmd := e.cmd
		if cmd == "" || notSetting.MatchString(cmd) || strings.HasSuffix(cmd, "?") || strings.HasPrefix(cmd, "!") || strings.HasPrefix(cmd, "#") {
			// "!" / "#" typed at a prompt is a comment (pasted configuration)
			continue
		}
		if modal && promptMode(e.prompt) == "" {
			continue
		}
		if refused(e.after) {
			continue
		}
		if i+1 < len(entries) && len(e.after) > 0 {
			next := entries[i+1]
			if next.prompt == e.prompt && len(next.cmd) > len(cmd) && strings.HasPrefix(strings.ToLower(next.cmd), strings.ToLower(cmd)) {
				continue // redrawn after help / completion candidates
			}
		}
		key := normKey(cmd)
		if e.junos != "" {
			for _, verb := range []string{"set ", "delete "} {
				if strings.HasPrefix(key, verb) {
					key = verb + normKey(e.junos) + " " + key[len(verb):]
				}
			}
		}
		par := parents[i]
		if k, ok := seen[par+"\x00"+key]; ok {
			out[k].last = i // typed again: "shutdown, no shutdown, shutdown"
			continue
		}
		seen[par+"\x00"+key] = len(out)
		out = append(out, typedCmd{line: cmd, key: key, parent: par, last: i})
	}
	return out, typed
}

// sameKind: a line typed at a sub-mode prompt that opens another block of
// the kind the current one is ("interface Gi0/2" after "int gi0/1").
func sameKind(cmd, parent string) bool {
	a, b := strings.Fields(normKey(cmd)), strings.Fields(normKey(parent))
	if len(a) < 2 || len(b) < 2 || a[0] == "no" {
		return false
	}
	return family(a[0], b[0]) && family(a[1], b[1])
}

// family: one word is an abbreviation of the other, comparing the letters
// of an interface name ("gi" and "gigabitethernet0/2").
func family(x, y string) bool {
	if l, _ := splitIf(x); l != "" {
		x = l
	}
	if l, _ := splitIf(y); l != "" {
		y = l
	}
	return strings.HasPrefix(x, y) || strings.HasPrefix(y, x)
}

// refused reports a device error among the first lines after a command.
func refused(after []string) bool {
	for i, l := range after {
		if i >= refusedWithin {
			break
		}
		if refusedMark.MatchString(l) {
			return true
		}
	}
	return false
}
