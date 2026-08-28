// Package profile holds the per-OS login automation profiles.
//
// A profile is a declarative sequence of expect/send steps that drives a
// device from the moment the transport connects to a ready, paging-disabled
// operational prompt — plus the sequence to log out cleanly.
//
// Profiles are data, not code, so users can add their own via YAML without
// recompiling. The built-in set mirrors the 14 OS types of the legacy tool.
package profile

import (
	"regexp"
	"strings"
)

// Step is one expect/send action.
//
// Expect is authored as a plain string (quoted to a literal regexp via Quote
// on registration; matched against the accumulated output tail) and is
// awaited first, if non-empty. Then Send is written followed by a newline, or
// SendRaw is written verbatim (use for control characters such as
// "\x03" = Ctrl-C). DelayMs pauses after sending.
//
// There is no per-step optional flag: the runner classifies steps itself.
// Credential steps (sending {user}/{password}) are skipped outright over a
// directly-authenticated SSH session, and auth steps (credential or {enable})
// are skipped without error when their prompt never appears — that is how one
// profile serves SSH, Telnet, and serial alike.
type Step struct {
	Expect  string `json:"expect,omitempty"`
	Send    string `json:"send,omitempty"`
	SendRaw string `json:"sendRaw,omitempty"`
	// Enter sends a bare newline when Send and SendRaw are both empty. Without
	// it, a step that only sets Expect is expect-only and sends nothing.
	Enter   bool `json:"enter,omitempty"`
	DelayMs int  `json:"delayMs,omitempty"`
}

// Sends reports whether the step transmits anything after its Expect.
// A step with only Expect set waits without sending.
func (s Step) Sends() bool {
	return s.Send != "" || s.SendRaw != "" || s.Enter
}

// Credential reports whether the step sends the login user or password —
// prompts the device never shows when the transport itself authenticated
// (direct SSH), and that some devices skip (password-only Telnet logins).
func (s Step) Credential() bool {
	return strings.Contains(s.Send, "{user}") || strings.Contains(s.Send, "{password}")
}

// Auth reports whether the step belongs to authentication (credentials or the
// enable/昇格 password). An auth step whose prompt never appears is skipped
// after a short wait instead of failing the login.
func (s Step) Auth() bool {
	return s.Credential() || strings.Contains(s.Send, "{enable}")
}

// Profile describes how to log into and operate one OS family.
type Profile struct {
	Key  string `json:"key"`
	Name string `json:"name"`

	// Prompt matches the operational (post-login) prompt. The runner waits for
	// it between commands.
	Prompt string `json:"prompt"`

	// MorePrompt matches a pager prompt (e.g. "--More--"). When seen mid-output
	// the runner sends a space to advance. Empty disables handling.
	MorePrompt string `json:"morePrompt,omitempty"`

	// Login drives from connect to operational prompt (auth + enable).
	Login []Step `json:"login"`

	// Pager disables output paging once at the operational prompt.
	Pager []Step `json:"pager,omitempty"`

	// Disconnect logs out cleanly.
	Disconnect []Step `json:"disconnect,omitempty"`
}

// Quote returns a copy of p with every prompt/expect treated as a literal
// string (QuoteMeta). Profiles are authored as plain text — the same wait
// strings the legacy TTL macro used (e.g. "#", "assword:") — while the
// matching engine works on regexps, so registration quotes them.
func Quote(p Profile) Profile {
	q := p
	q.Prompt = regexp.QuoteMeta(p.Prompt)
	q.MorePrompt = regexp.QuoteMeta(p.MorePrompt)
	quoteSteps := func(steps []Step) []Step {
		if len(steps) == 0 {
			return steps
		}
		out := make([]Step, len(steps))
		for i, st := range steps {
			st.Expect = regexp.QuoteMeta(st.Expect)
			out[i] = st
		}
		return out
	}
	q.Login = quoteSteps(p.Login)
	q.Pager = quoteSteps(p.Pager)
	q.Disconnect = quoteSteps(p.Disconnect)
	return q
}

// Vars are substituted into Step.Send/SendRaw via {name} placeholders.
type Vars struct {
	User     string
	Password string
	Enable   string
}

// Expand replaces {user}, {password}, {enable} placeholders.
func Expand(s string, v Vars) string {
	r := strings.NewReplacer(
		"{user}", v.User,
		"{password}", v.Password,
		"{enable}", v.Enable,
	)
	return r.Replace(s)
}
