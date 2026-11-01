package history

import (
	"regexp"
	"strings"
)

// Secret and control blocks: the lines of a listing that differ between
// two captures without the configuration having changed in any way a
// person cares about — a certificate re-issued, FortiGate's ENC values
// (re-encrypted on every listing), UUIDs the device assigns, the control
// envelope of a small-business switch's listing. secretMask marks them so
// the comparison leaves them out and the window shows them dimmed, and
// counts them: what is left out is always said.
//
// The decision is a state machine over the raw lines (trailing blanks
// removed), one rule per line in this order:
//
//  1. inside an ENC value: every line until a FortiGate keyword line
//  2. inside a quoted key: every line until one ending in a quote
//  3. "set <key-ish> "…" opens a quoted key (multi-line unless closed)
//  4. inside a PEM block: every line until -----END or a closing quote
//  5. a line with -----BEGIN opens a PEM block
//  6. inside a Cisco PKI certificate: every line until a bare "quit"
//  7. "certificate <word>" followed by a hex chunk opens one
//  8. set uuid / set snmp-index / set <word> ENC (opens 1)
//  9. "ENC" followed by a base64-looking run
// 10. ssd-control-start / -end: the envelope only (always)
// 11. unit-type-control-start … -end: the whole block (always)
// 12. a line that names a uuid and carries one

var (
	fgKeyword   = regexp.MustCompile(`(?i)^\s*(set|unset|next|end|config|edit|append|delete|move|purge|rename|clone)\b`)
	quotedKey   = regexp.MustCompile(`(?i)^\s*set\s+(public-key|private-key|ssh-public-key|ssh-key|image-base64|certificate|ca|cert)\s+"`)
	pkiOpen     = regexp.MustCompile(`(?i)^\s*certificate\s+\S`)
	hexChunk    = regexp.MustCompile(`^\s*[0-9A-Fa-f]{8}(\s+[0-9A-Fa-f]{8})*\s*$`)
	bareQuit    = regexp.MustCompile(`(?i)^\s*quit\s*$`)
	fgOneLine   = regexp.MustCompile(`(?i)^\s*set\s+(uuid|snmp-index)\s`)
	fgEnc       = regexp.MustCompile(`(?i)^\s*set\s+\S+\s+ENC\s`)
	encValue    = regexp.MustCompile(`\bENC\b\s*[A-Za-z0-9+/=]{20,}`)
	uuidWord    = regexp.MustCompile(`(?i)\buuid\b`)
	uuidValue   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	ssdControl  = regexp.MustCompile(`(?i)^\s*ssd-control-(start|end)\b`)
	unitTypeCtl = regexp.MustCompile(`(?i)^\s*unit-type-control-(start|end)\b`)
)

type secretState int

const (
	secNone secretState = iota
	secEnc
	secQuoted
	secPEM
	secPKI
	secUnitType
)

// secretMask returns, for each line, whether it is part of a secret or
// control block. With secrets false only the control envelopes (rules 10
// and 11) are marked: they are not configuration whatever the option.
func secretMask(lines []string, secrets bool) []bool {
	mask := make([]bool, len(lines))
	st := secNone
	for i, raw := range lines {
		l := strings.TrimRight(raw, " \t\r")
		switch st {
		case secUnitType:
			mask[i] = true
			if m := unitTypeCtl.FindStringSubmatch(l); m != nil && strings.EqualFold(m[1], "end") {
				st = secNone
			}
			continue
		case secEnc:
			if strings.TrimSpace(l) != "" && !fgKeyword.MatchString(l) {
				mask[i] = true
				continue
			}
			st = secNone
		case secQuoted:
			mask[i] = true
			if strings.HasSuffix(l, `"`) {
				st = secNone
			}
			continue
		case secPEM:
			mask[i] = true
			if strings.Contains(l, "-----END") || strings.HasSuffix(l, `"`) {
				st = secNone
			}
			continue
		case secPKI:
			mask[i] = true
			if bareQuit.MatchString(l) {
				st = secNone
			}
			continue
		}
		// Control envelopes, whatever the option.
		if ssdControl.MatchString(l) {
			mask[i] = true
			continue
		}
		if m := unitTypeCtl.FindStringSubmatch(l); m != nil {
			mask[i] = true
			if strings.EqualFold(m[1], "start") {
				st = secUnitType
			}
			continue
		}
		if !secrets {
			continue
		}
		switch {
		case quotedKey.MatchString(l):
			mask[i] = true
			if !closedQuote(l) {
				st = secQuoted
			}
		case strings.Contains(l, "-----BEGIN "):
			mask[i] = true
			if !strings.Contains(l, "-----END") {
				st = secPEM
			}
		case pkiOpen.MatchString(l) && nextIsHex(lines, i+1):
			mask[i] = true
			st = secPKI
		case fgEnc.MatchString(l):
			mask[i] = true
			st = secEnc
		case fgOneLine.MatchString(l), encValue.MatchString(l):
			mask[i] = true
		case uuidWord.MatchString(l) && uuidValue.MatchString(l):
			mask[i] = true
		}
	}
	return mask
}

// closedQuote says whether the quoted value opened on this line is also
// closed on it: a second quote after the opening one.
func closedQuote(l string) bool {
	i := strings.Index(l, `"`)
	return i >= 0 && strings.Contains(l[i+1:], `"`)
}

// nextIsHex: the next non-blank line is a run of hex words (the body of a
// Cisco PKI certificate), so "certificate <word>" opens one and is not an
// ordinary setting that happens to start with the word.
func nextIsHex(lines []string, i int) bool {
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		return hexChunk.MatchString(lines[i])
	}
	return false
}
