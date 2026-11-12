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

// cmdOutput is what one log holds of one typed command: its name as first
// typed (whitespace collapsed), the line indices of its output the last
// time it was typed (the prompt line itself left out), and whether any
// time it was typed printed a line that is not blank or a pager prompt.
type cmdOutput struct {
	name   string
	lines  []int
	output bool
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

// command says whether a line promptParts split (m) is a typed command.
// Two or more spaces before a capitalised word is a table's heading even
// under the device's own name (a switch still named "Switch" printing
// "Switch#   Role    Mac Address").
func (p logPrompts) command(m []string) bool {
	if gap := len(m[0]) - len(m[1]) - len(m[2]); gap >= 2 && m[2][0] >= 'A' && m[2][0] <= 'Z' {
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

// typedCommands walks a log's lines and gives, in the order first typed,
// the key of every command with output (see cmdOutput), and the outputs
// by key — every typed command, with or without output, so a caller can
// tell "typed but printed nothing" from "never typed".
func typedCommands(lines []string) (order []string, outs map[string]*cmdOutput) {
	outs = map[string]*cmdOutput{}
	prompts := promptsOf(lines)
	var cur *cmdOutput
	var first []string // keys in the order first typed
	for li, l := range lines {
		t := strings.TrimRight(l, " \t")
		if m := promptParts(t); m != nil && prompts.command(m) {
			cur = nil
			cmd := strings.Join(strings.Fields(m[2]), " ")
			if cmd == "" {
				continue
			}
			k := cmdKey(cmd)
			c := outs[k]
			if c == nil {
				c = &cmdOutput{name: cmd}
				outs[k] = c
				first = append(first, k)
			}
			c.lines = c.lines[:0] // the last time it was typed
			cur = c
			continue
		}
		if cur == nil {
			continue
		}
		if prompts.ends(t) {
			cur = nil
			continue
		}
		cur.lines = append(cur.lines, li)
		if strings.TrimSpace(l) != "" && !pagerLine.MatchString(l) && !barePrompt.MatchString(t) {
			cur.output = true
		}
	}
	for _, k := range first {
		c := outs[k]
		// A bare prompt left at the end (Yamaha's "#") is not output.
		for n := len(c.lines); n > 0 && barePrompt.MatchString(strings.TrimRight(lines[c.lines[n-1]], " \t")); n-- {
			c.lines = c.lines[:n-1]
		}
		if c.output && !navigation(c.name) {
			order = append(order, k)
		}
	}
	return order, outs
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
// — and failing that as a log typed it at all, or as asked.
func commandName(outsA, outsB map[string]*cmdOutput, cmd string) string {
	k := cmdKey(cmd)
	a, b := outsA[k], outsB[k]
	switch {
	case a != nil && a.output:
		return a.name
	case b != nil && b.output:
		return b.name
	case a != nil:
		return a.name
	case b != nil:
		return b.name
	}
	return cmd
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
