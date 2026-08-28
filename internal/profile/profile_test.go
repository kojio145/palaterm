package profile

import (
	"regexp"
	"strings"
	"testing"
)

// Every default profile, once quoted for registration, must compile.
func TestDefaultsQuoteAndCompile(t *testing.T) {
	for _, d := range Defaults() {
		p := Quote(d)
		if p.Prompt != "" {
			if _, err := regexp.Compile(p.Prompt); err != nil {
				t.Errorf("%s: bad quoted prompt %q: %v", p.Key, p.Prompt, err)
			}
		}
		if p.MorePrompt != "" {
			if _, err := regexp.Compile(p.MorePrompt); err != nil {
				t.Errorf("%s: bad quoted more %q: %v", p.Key, p.MorePrompt, err)
			}
		}
		checkSteps(t, p.Key, "login", p.Login)
		checkSteps(t, p.Key, "pager", p.Pager)
		checkSteps(t, p.Key, "disconnect", p.Disconnect)
	}
}

func checkSteps(t *testing.T, key, phase string, steps []Step) {
	t.Helper()
	for i, s := range steps {
		if s.Expect == "" {
			continue
		}
		if _, err := regexp.Compile(s.Expect); err != nil {
			t.Errorf("%s.%s[%d]: bad quoted expect %q: %v", key, phase, i, s.Expect, err)
		}
	}
}

// Defaults are authored as plain wait strings, TTL-style: no regex
// metacharacter escapes and no case-insensitivity flags anywhere.
func TestDefaultsArePlainText(t *testing.T) {
	for _, p := range Defaults() {
		all := []string{p.Prompt, p.MorePrompt}
		for _, s := range p.Login {
			all = append(all, s.Expect)
		}
		for _, s := range p.Disconnect {
			all = append(all, s.Expect)
		}
		for _, pat := range all {
			if strings.Contains(pat, `\`) || strings.Contains(pat, "(?i)") || strings.Contains(pat, "$") {
				t.Errorf("%s: %q looks like a regexp, defaults must be plain text", p.Key, pat)
			}
		}
	}
}

func TestExpectedDefaultCount(t *testing.T) {
	// 13 concrete OS families + 1 generic fallback.
	if got := len(Defaults()); got != 14 {
		t.Fatalf("expected 14 default profiles, got %d", got)
	}
}

// Defaults() returns deep copies: mutating a returned profile's steps must
// not leak into a later call's result.
func TestDefaultsAreDeepCopies(t *testing.T) {
	a := Defaults()
	a[0].Login[0].Send = "mutated"
	a[0].Name = "mutated"
	b := Defaults()
	if b[0].Name == "mutated" || b[0].Login[0].Send == "mutated" {
		t.Fatal("Defaults() shares state between calls")
	}
}

func TestExpand(t *testing.T) {
	got := Expand("{user}/{password}/{enable}", Vars{User: "u", Password: "p", Enable: "e"})
	if got != "u/p/e" {
		t.Fatalf("expand: %q", got)
	}
}

// A registry with profiles loaded falls back to its "generic" entry; an empty
// registry (or one whose generic was deleted) falls back to the hardcoded
// generic so a run never gets a zero profile.
func TestGetFallsBackToGeneric(t *testing.T) {
	reg := NewRegistry()
	for _, d := range Defaults() {
		reg.Add(Quote(d))
	}
	if reg.Get("nonexistent").Key != "generic" {
		t.Fatal("unknown OS type should fall back to registered generic")
	}
	empty := NewRegistry()
	p := empty.Get("nonexistent")
	if p.Key != "generic" || p.Prompt == "" {
		t.Fatalf("empty registry should fall back to hardcoded generic, got %+v", p)
	}
}

// Quote escapes every pattern position, including pager/disconnect expects.
func TestQuoteEscapesAllPositions(t *testing.T) {
	p := Quote(Profile{
		Prompt:     "a#",
		MorePrompt: "---(more",
		Login:      []Step{{Expect: "[y"}},
		Pager:      []Step{{Expect: "(x)"}},
		Disconnect: []Step{{Expect: "[y/n]"}},
	})
	for pos, got := range map[string]string{
		"prompt":     p.Prompt,
		"morePrompt": p.MorePrompt,
		"login":      p.Login[0].Expect,
		"pager":      p.Pager[0].Expect,
		"disconnect": p.Disconnect[0].Expect,
	} {
		if _, err := regexp.Compile(got); err != nil {
			t.Errorf("%s: %q does not compile after Quote: %v", pos, got, err)
		}
	}
	if p.MorePrompt == "---(more" {
		t.Error("morePrompt was not quoted")
	}
	if p.Disconnect[0].Expect == "[y/n]" {
		t.Error("disconnect expect was not quoted")
	}
}

// Step classification drives the runner's automatic skipping: credential
// steps vanish over direct SSH, auth steps skip when their prompt is absent.
func TestStepClassification(t *testing.T) {
	cases := []struct {
		send             string
		credential, auth bool
	}{
		{"{user}", true, true},
		{"{password}", true, true},
		{"{enable}", false, true},
		{"enable", false, false},
		{"terminal length 0", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		s := Step{Send: c.send}
		if s.Credential() != c.credential || s.Auth() != c.auth {
			t.Errorf("Send=%q: Credential=%v Auth=%v, want %v/%v",
				c.send, s.Credential(), s.Auth(), c.credential, c.auth)
		}
	}
}
