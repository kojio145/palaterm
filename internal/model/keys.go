package model

import (
	"fmt"
	"regexp"
	"strings"
)

// Key tokens let a command line send a control key, which CheckCommandText
// keeps out of the text itself: {ctrl+c} abandons a running ping, {ctrl+z}
// leaves config mode, {ctrl+^}x is Cisco's escape out of a ping or
// traceroute, {break} is a serial break. Written like the {user} variables,
// case does not matter. A line holding any token goes out without the line
// ending — the token is the key; {enter} at the end adds one.
//
// The token names are what the user types; KeySegments turns a line into
// what the transport sends.
var keyTokenRe = regexp.MustCompile(`(?i)\{(ctrl\+[a-z\[\\\]^_]|esc|tab|enter|break)\}`)

// ctrlTokenRe matches anything that looks like a key token ({ctrl+…}, or
// one of the named keys with trailing text) so a typo is refused at save
// time rather than typed to the device as text.
var ctrlTokenRe = regexp.MustCompile(`(?i)\{(ctrl[^}]*|esc\w+|tab\w+|enter\w+|break\w+)\}`)

// KeyTokenHelp lists the tokens a command may use, for error messages.
const KeyTokenHelp = "{ctrl+a}〜{ctrl+z} {ctrl+[} {ctrl+\\} {ctrl+]} {ctrl+^} {ctrl+_} {esc} {tab} {enter} {break}"

// KeySegKind says what a segment of a key-token line is.
type KeySegKind int

const (
	KeySegText  KeySegKind = iota // bytes to write as they are
	KeySegEnter                   // the transport's line ending
	KeySegBreak                   // a serial break signal
)

// KeySeg is one piece of a command line that holds key tokens.
type KeySeg struct {
	Kind KeySegKind
	Text string // KeySegText: the bytes, control characters included
}

// KeySegments splits text into what to send when it holds key tokens. ok
// is false when the line has none: it is an ordinary command, sent with
// its line ending.
func KeySegments(text string) (segs []KeySeg, ok bool) {
	locs := keyTokenRe.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		return nil, false
	}
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			segs = append(segs, KeySeg{Kind: KeySegText, Text: cur.String()})
			cur.Reset()
		}
	}
	last := 0
	for _, l := range locs {
		cur.WriteString(text[last:l[0]])
		name := strings.ToLower(text[l[2]:l[3]])
		switch {
		case name == "enter":
			flush()
			segs = append(segs, KeySeg{Kind: KeySegEnter})
		case name == "break":
			flush()
			segs = append(segs, KeySeg{Kind: KeySegBreak})
		case name == "esc":
			cur.WriteByte(0x1b)
		case name == "tab":
			cur.WriteByte('\t')
		default: // ctrl+X: the letter's position, or the four keys after Z
			c := name[len(name)-1]
			if c >= 'a' && c <= 'z' {
				cur.WriteByte(c - 'a' + 1)
			} else {
				cur.WriteByte(c - '[' + 0x1b) // [ \ ] ^ _ → 0x1b..0x1f
			}
		}
		last = l[1]
	}
	cur.WriteString(text[last:])
	flush()
	return segs, true
}

// HasKeyTokens reports whether a command line sends a key token.
func HasKeyTokens(text string) bool { return keyTokenRe.MatchString(text) }

// CheckKeyTokens refuses a token that looks like a key but is not one
// ({ctrl+1}, {ctrl-c}, {escape}): sent as text it would run on the device
// as a mistyped command.
func CheckKeyTokens(text string) error {
	for _, m := range ctrlTokenRe.FindAllString(text, -1) {
		if !keyTokenRe.MatchString(m) {
			return fmt.Errorf("使えないキー札があります: %s（使えるのは %s）", m, KeyTokenHelp)
		}
	}
	return nil
}
