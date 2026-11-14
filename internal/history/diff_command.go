package history

import (
	"regexp"
	"strings"
)

// Comparing one command's output (DiffOpts.Command, v1.9.36): the 範囲 list
// of the diff window offers, after 設定だけ and ログ全体, every command typed
// in A or B that printed something — "show version", "show interfaces
// GigabitEthernet0/1" — and picking one compares that output alone. A
// command typed more than once in a log is taken the last time it was
// typed, as configSectionIdx takes the last listing.
//
// Contexts (v1.9.37): "display this" typed in two interface views, "show"
// typed in two of FortiOS's tables, are the same words asking for
// different things, and one entry for both (the last typed) compared one
// interface with another. When a log typed such a command with output in
// two or more contexts, the command is listed once per context — "display
// this (GigabitEthernet1/0/1)" — in both logs. Every other command is one
// entry, as before.

// cmdOutput is what one log holds of one typed command: its name as the
// list shows it (as first typed, whitespace collapsed, and the context in
// brackets after it when it is one of several), the command and the
// context apart, the line indices of its output the last time it was
// typed (the prompt line itself left out), and whether any time it was
// typed printed a line that is not blank or a pager prompt.
type cmdOutput struct {
	name     string
	cmd, ctx string
	lines    []int
	output   bool
}

// navCommand: the commands whose output is nothing to compare — entering
// and leaving modes, saving, pager settings. A setting typed at a config
// prompt has no output and drops out by itself; one that printed an error
// stays listed, which is what the user wants to see.
var navCommand = regexp.MustCompile(`(?i)^(do\s+)?(configure|conf|config|configuration|exit|end|quit|q|logout|return|back|top|up|edit|next|terminal|term|screen-length|enable|disable|commit|write|wr|copy|save|system-view|cli|admin|undo\s+terminal|no\s+page|set\s+cli)(\s|$)`)

// navOutput: the same first words when they run something whose output is
// compared — "admin show platform" (IOS-XR), "admin display-config" (SR
// OS), "top show interfaces" (Junos) and "top -bn1" (a shell), "write
// terminal" / "wr t" (a configuration listing), "commit check".
var navOutput = regexp.MustCompile(`(?i)^(do\s+)?(admin\s+\S|top\s+\S|wr(ite)?\s+t[a-z]*\s*$|commit\s+check(\s|$))`)

// navigation says whether a typed command is left out of the list.
func navigation(cmd string) bool {
	return navCommand.MatchString(cmd) && !navOutput.MatchString(cmd)
}

// typedWord: a command line as someone types it — a lower-case word first
// ("show", "display", "terminal"). The lines of an output that read as a
// prompt do not start that way: "Switch#   Role    Mac Address" (the
// heading of show switch), "r>i198.51.100.0/24" (a BGP table's status
// letters), "C>* 192.0.2.0/24" (a routing table's).
var typedWord = regexp.MustCompile(`^[a-z][a-z\-]*(\s|\||$)`)

// promptName is the device's part of a prompt: "sw-lab-01" of
// "sw-lab-01(config-if)#", of "!sw-lab-01#" (AlaxalA, not saved) and of
// "sw-lab-01 (global) #" (FortiOS). The bracketed forms and SR OS's keep
// their context ("<sw-lab-01", "[sw-lab-01-vlan10]", "A:sw-lab-01>config");
// Yamaha's "#" / ">" has no name.
func promptName(prompt string) string {
	p := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(prompt), "#>$%"))
	for strings.HasSuffix(p, ")") {
		i := strings.LastIndex(p, "(")
		if i < 0 {
			break
		}
		p = strings.TrimSpace(p[:i])
	}
	return strings.TrimPrefix(p, "!")
}

// logPrompts tells the prompts of a log from the output lines that only
// read as one (promptParts takes "r>i198.51.100.0/24" for the prompt "r>"
// and a command, which cut the output of show ip bgp in two and listed
// the rest of the line as a command). typed holds the names of the
// prompts a typed command follows (typedWord), bare those of the lines
// that are nothing but a prompt. A line is a prompt when its name is in
// either; a log that shows neither kind keeps every prompt-like line, as
// promptParts reads it.
type logPrompts struct {
	typed, bare map[string]bool
}

func promptsOf(lines []string) logPrompts {
	p := logPrompts{typed: map[string]bool{}, bare: map[string]bool{}}
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if m := promptParts(l); m != nil {
			if typedWord.MatchString(m[2]) {
				p.typed[promptName(m[1])] = true
			}
		} else if l != "" && barePrompt.MatchString(l) {
			p.bare[promptName(l)] = true
		}
	}
	return p
}

// statusRow: a row of a routing or BGP table whose status letters read as
// a one-letter prompt — "r>i198.51.100.0/24", "s> 203.0.113.0/24", "C>*
// 192.0.2.0/24". On a device named "r" (or "s", "C") the name does not
// tell them from a typed command; what follows the letters does: an
// address after status marks, with a prefix length, or with columns after
// it. An address typed alone at the prompt stays a command.
var statusRow = regexp.MustCompile(`^[A-Za-z]>([*=ie?]{0,2})\s*(?:\d{1,3}(?:\.\d{1,3}){3}|[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,})(/\d{1,3})?(\s{2,}\S.*)?(\s.*)?$`)

func tableRow(line string) bool {
	m := statusRow.FindStringSubmatch(line)
	return m != nil && (m[1] != "" || m[2] != "" || m[3] != "")
}

// command says whether a line promptParts split (m) is a typed command.
// Two or more spaces before a capitalised word is a table's heading even
// under the device's own name (a switch still named "Switch" printing
// "Switch#   Role    Mac Address").
func (p logPrompts) command(m []string) bool {
	if gap := len(m[0]) - len(m[1]) - len(m[2]); gap >= 2 && m[2][0] >= 'A' && m[2][0] <= 'Z' {
		return false
	}
	if tableRow(m[0]) {
		return false
	}
	if len(p.typed)+len(p.bare) == 0 {
		return true
	}
	name := promptName(m[1])
	return name == "" || p.typed[name] || p.bare[name]
}

// ends says whether a line is the device's bare prompt, which ends the
// output of the command before it (an Enter with nothing typed). Only the
// prompts commands were typed at count: "#" closes a banner in a listing
// and "100%" ends a progress line, and both read as a bare prompt.
func (p logPrompts) ends(line string) bool {
	if !barePrompt.MatchString(line) {
		return false
	}
	name := promptName(line)
	return name != "" && p.typed[name]
}

// cmdKey is the key two logs share for one command: lower case, one space
// between words.
func cmdKey(cmd string) string {
	return strings.ToLower(strings.Join(strings.Fields(cmd), " "))
}

// editBanner: the line Junos (VyOS and PAN-OS too) prints above a
// configuration prompt — "[edit]", "[edit interfaces ge-0/0/0]".
var editBanner = regexp.MustCompile(`^\[(edit(?:\s[^\[\]]*)?)\]$`)

// srosPath: the context SR OS shows after the name — "config>router" of
// "*A:rt-lab-01>config>router#".
var srosPath = regexp.MustCompile(`^\*?[A-Z]:[^>#\s]+>([^#\s]+)[#$]$`)

// execCommand: an operational command run from a configuration mode ("do
// show vlan", Junos's "run show route") asks the same in every context.
var execCommand = regexp.MustCompile(`(?i)^(do|run)\s`)

// thisCommand: the commands that show the context they are typed in —
// "display this" (Comware, VRP), "info" (SR OS), a bare "show" or "get",
// "show active" (EOS), "show configuration" (OS10, IOS-XR), "show
// running-config current-context" (AOS-CX). Any other command typed in a
// view or a config mode ("display vlan", "show vlan") asks the same
// wherever it is typed and stays one entry.
var thisCommand = regexp.MustCompile(`(?i)^(display\s+this(\s|\||$)|info(\s|\||$)|get\s*$|show(\s+(active|this|config|configuration|full-configuration|running-config\s+current-context)(\s+[a-z\-]+)?)?\s*(\|.*)?$)`)

// viewName is the name inside a bracketed prompt: "sw-lab-01-vlan10" of
// "[sw-lab-01-vlan10]" and of "[~sw-lab-01-vlan10]" (VRP, not committed),
// "sw-lab-01" of "<sw-lab-01>".
func viewName(prompt string) string {
	return strings.TrimLeft(strings.Trim(strings.TrimSpace(prompt), "[]<>"), "~*")
}

// viewNames collects the bracketed prompts of a log (Comware, VRP): every
// "<name>" and every "[name]" a typed command follows. The shortest of
// them that starts a view's prompt is the device's name there, and the
// rest of that prompt is the view.
func viewNames(lines []string) map[string]bool {
	v := map[string]bool{}
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" || (l[0] != '<' && l[0] != '[') {
			continue
		}
		if m := promptParts(l); m != nil {
			if l[0] == '<' || typedWord.MatchString(m[2]) {
				v[viewName(m[1])] = true
			}
		} else if l[0] == '<' && barePrompt.MatchString(l) {
			v[viewName(l)] = true
		}
	}
	return v
}

// promptContext is the mode part of a prompt, the device's name left out:
// "GigabitEthernet1/0/1" of "[sw-lab-01-GigabitEthernet1/0/1]", "port1" of
// "fw-lab-01 (port1) #", "config-if" of "sw-lab-01(config-if)#",
// "config>router" of "*A:rt-lab-01>config>router#", and "edit interfaces"
// when the line above the prompt is Junos's "[edit interfaces]". "#" and
// ">" alone are no context. every says that every command typed there
// depends on it (Junos's configuration mode, FortiOS's tables and VDOMs);
// elsewhere only the commands of thisCommand do.
func promptContext(prompt, above string, views map[string]bool) (ctx string, every bool) {
	if m := editBanner.FindStringSubmatch(strings.TrimSpace(above)); m != nil {
		return strings.Join(strings.Fields(m[1]), " "), true
	}
	p := strings.TrimSpace(prompt)
	switch {
	case strings.HasPrefix(p, "<"):
		return "", false
	case strings.HasPrefix(p, "["):
		in := viewName(p)
		base := in
		for v := range views {
			if len(v) < len(base) && strings.HasPrefix(in, v+"-") {
				base = v
			}
		}
		if base == in {
			return "", false
		}
		return in[len(base)+1:], false
	}
	if m := srosPath.FindStringSubmatch(p); m != nil {
		return m[1], false
	}
	p = strings.TrimRight(p, "#>$% ")
	var groups []string
	for strings.HasSuffix(p, ")") {
		i := strings.LastIndex(p, "(")
		if i < 0 {
			break
		}
		if g := strings.TrimSpace(p[i+1 : len(p)-1]); g != "" {
			groups = append([]string{g}, groups...)
		}
		every = every || (i > 0 && p[i-1] == ' ')
		p = strings.TrimSpace(p[:i])
	}
	return strings.Join(groups, " "), every
}

// typedLog is the typed commands of one log: one cmdOutput per command and
// context in the order first typed (first), and one per command whatever
// the context (last, by cmdKey) — the last time it was typed anywhere,
// which is what the list shows for a command that is one entry.
type typedLog struct {
	first []*cmdOutput
	last  map[string]*cmdOutput
}

// typedLogOf walks a log's lines.
func typedLogOf(lines []string) *typedLog {
	tl := &typedLog{last: map[string]*cmdOutput{}}
	prompts := promptsOf(lines)
	views := viewNames(lines)
	byCtx := map[string]*cmdOutput{}
	var cur, curCtx *cmdOutput
	for li, l := range lines {
		t := strings.TrimRight(l, " \t")
		if m := promptParts(t); m != nil && prompts.command(m) {
			cur, curCtx = nil, nil
			cmd := strings.Join(strings.Fields(m[2]), " ")
			if cmd == "" {
				continue
			}
			k := cmdKey(cmd)
			c := tl.last[k]
			if c == nil {
				c = &cmdOutput{name: cmd, cmd: cmd}
				tl.last[k] = c
			}
			c.lines = c.lines[:0] // the last time it was typed
			above := ""
			if li > 0 {
				above = lines[li-1]
			}
			ctx, every := promptContext(m[1], above, views)
			if ctx != "" && (execCommand.MatchString(cmd) || !(every || thisCommand.MatchString(cmd))) {
				ctx = ""
			}
			ck := k + "\x00" + strings.ToLower(ctx)
			x := byCtx[ck]
			if x == nil {
				x = &cmdOutput{name: cmd, cmd: cmd, ctx: ctx}
				if ctx != "" {
					x.name = cmd + " (" + ctx + ")"
				}
				byCtx[ck] = x
				tl.first = append(tl.first, x)
			}
			x.lines = x.lines[:0]
			cur, curCtx = c, x
			continue
		}
		if cur == nil {
			continue
		}
		if prompts.ends(t) {
			cur, curCtx = nil, nil
			continue
		}
		cur.lines = append(cur.lines, li)
		curCtx.lines = append(curCtx.lines, li)
		if strings.TrimSpace(l) != "" && !pagerLine.MatchString(l) && !barePrompt.MatchString(t) {
			cur.output, curCtx.output = true, true
		}
	}
	// A bare prompt left at the end (Yamaha's "#") is not output.
	trim := func(c *cmdOutput) {
		for n := len(c.lines); n > 0 && barePrompt.MatchString(strings.TrimRight(lines[c.lines[n-1]], " \t")); n-- {
			c.lines = c.lines[:n-1]
		}
	}
	for _, x := range tl.first {
		trim(x)
	}
	for _, c := range tl.last {
		trim(c)
	}
	return tl
}

// split is the commands (by cmdKey) this log typed with output in two or
// more contexts: those are listed once per context.
func (tl *typedLog) split() map[string]bool {
	n := map[string]int{}
	for _, x := range tl.first {
		if x.output && !navigation(x.cmd) {
			n[cmdKey(x.cmd)]++
		}
	}
	out := map[string]bool{}
	for k, c := range n {
		if c >= 2 {
			out[k] = true
		}
	}
	return out
}

// entries gives, in the order first typed, the key of every command with
// output (see cmdOutput), and the outputs by key — every typed command,
// with or without output, so a caller can tell "typed but printed
// nothing" from "never typed". A command in split (either log's, so that
// A and B name it alike) is an entry per context, keyed by its listed
// name; any other is one entry, the last time it was typed.
func (tl *typedLog) entries(split map[string]bool) (order []string, outs map[string]*cmdOutput) {
	outs = map[string]*cmdOutput{}
	for _, x := range tl.first {
		k, c := cmdKey(x.cmd), x
		if split[k] {
			k = cmdKey(x.name)
		} else {
			c = tl.last[k]
		}
		if outs[k] != nil {
			continue
		}
		outs[k] = c
		if c.output && !navigation(c.cmd) {
			order = append(order, k)
		}
	}
	return order, outs
}

// typedCommands is one log's list by itself: entries with the log's own
// split.
func typedCommands(lines []string) (order []string, outs map[string]*cmdOutput) {
	tl := typedLogOf(lines)
	return tl.entries(tl.split())
}

// commandList is the 範囲 list's commands: A's in order, then B's that A
// lacks, named as first typed; at most maxJumps.
func commandList(orderA, orderB []string, outsA, outsB map[string]*cmdOutput) []string {
	var out []string
	seen := map[string]bool{}
	add := func(order []string, outs map[string]*cmdOutput) {
		for _, k := range order {
			if seen[k] || len(out) >= maxJumps {
				continue
			}
			seen[k] = true
			out = append(out, outs[k].name)
		}
	}
	add(orderA, outsA)
	add(orderB, outsB)
	return out
}

// commandName is the name the result carries for DiffOpts.Command: as the
// list shows it — typed in A when A has its output, else in B when B has
// — and failing that as a log typed it at all, or as asked. typed is the
// command without the context the name may carry ("show" of "show
// (port1)").
func commandName(outsA, outsB map[string]*cmdOutput, cmd string) (name, typed string) {
	k := cmdKey(cmd)
	a, b := outsA[k], outsB[k]
	switch {
	case a != nil && a.output:
		return a.name, a.cmd
	case b != nil && b.output:
		return b.name, b.cmd
	case a != nil:
		return a.name, a.cmd
	case b != nil:
		return b.name, b.cmd
	}
	return cmd, cmd
}

// commandSelection is the lines of one log compared for DiffOpts.Command:
// the command's last output, and whether the log typed it at all.
func commandSelection(outs map[string]*cmdOutput, cmd string) (sel []int, typed bool) {
	c := outs[cmdKey(cmd)]
	if c == nil {
		return nil, false
	}
	return append([]int(nil), c.lines...), true
}
