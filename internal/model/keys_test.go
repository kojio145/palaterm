package model

import (
	"reflect"
	"testing"
)

func TestKeySegments(t *testing.T) {
	cases := []struct {
		in   string
		want []KeySeg
		ok   bool
	}{
		{"show clock", nil, false},
		{"{ctrl+c}", []KeySeg{{KeySegText, "\x03"}}, true},
		{"{CTRL+Z}", []KeySeg{{KeySegText, "\x1a"}}, true},
		{"{ctrl+^}x", []KeySeg{{KeySegText, "\x1ex"}}, true},
		{"{ctrl+[}{ctrl+\\}{ctrl+]}{ctrl+_}", []KeySeg{{KeySegText, "\x1b\x1c\x1d\x1f"}}, true},
		{"{esc}:q{enter}", []KeySeg{{KeySegText, "\x1b:q"}, {KeySegEnter, ""}}, true},
		{"{tab}", []KeySeg{{KeySegText, "\t"}}, true},
		{"{break}", []KeySeg{{KeySegBreak, ""}}, true},
		{"a{break}b{enter}", []KeySeg{{KeySegText, "a"}, {KeySegBreak, ""}, {KeySegText, "b"}, {KeySegEnter, ""}}, true},
		// Not a key: the credential variables and anything else in braces.
		{"ping {user}.lab", nil, false},
		{"{ctrl+1}", nil, false},
	}
	for _, c := range cases {
		got, ok := KeySegments(c.in)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("KeySegments(%q) = %#v, %v; want %#v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestCheckKeyTokens(t *testing.T) {
	for _, ok := range []string{"show clock", "{ctrl+c}", "{Ctrl+C}x", "{enter}", "{user}", "copy {password} x", "{}"} {
		if err := CheckKeyTokens(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"{ctrl+1}", "{ctrl-c}", "{ctrl+cc}", "{ctrl}", "{escape}", "{breaker}", "{ctrl+}"} {
		if err := CheckKeyTokens(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// The set check refuses the same thing, naming the line.
	err := CheckCommands([]Command{{Text: "show clock"}, {Text: "{ctrl+1}"}})
	if err == nil || err.Error()[:len("2 行目")] != "2 行目" {
		t.Errorf("CheckCommands: %v", err)
	}
}
