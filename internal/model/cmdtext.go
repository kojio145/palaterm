package model

import (
	"fmt"
	"regexp/syntax"
	"strings"

	"github.com/kojio145/palaterm/internal/profile"
)

// CheckCommandText refuses text with control characters. A carriage return
// in the middle of a line shows as a space on screen but ends the line at
// the device: "show clock\rreload" reads as one harmless command and runs
// two — and the change-command warning, which looks at the start of each
// command, never sees the second.
func CheckCommandText(s string) error {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("制御文字（改行・タブなど）が含まれています: %q", s)
		}
	}
	return nil
}

// CheckCommands applies CheckCommandText to every command of a set.
func CheckCommands(cmds []Command) error {
	for i, c := range cmds {
		if err := CheckCommandText(c.Text); err != nil {
			return fmt.Errorf("%d 行目のコマンドに%w", i+1, err)
		}
		if err := CheckKeyTokens(c.Text); err != nil {
			return fmt.Errorf("%d 行目のコマンドに%w", i+1, err)
		}
		// Matched on one line of the device's output, so a line break in
		// it could never match.
		if err := CheckCommandText(c.Wait); err != nil {
			return fmt.Errorf("%d 行目の待つ文字列に%w", i+1, err)
		}
	}
	return nil
}

// CheckProfileSends applies CheckCommandText to everything a profile types
// (login, pager, disconnect steps and answers), and refuses a credential
// variable in an answer. Answers go to whatever question matches,
// mid-command: a credential there would be typed to any prompt that happens
// to look alike. So the variables are not expanded in them, and refused
// rather than sent as literal text — here, so that the editor and every file
// import (a profile JSON, a bundle) refuse the same thing.
func CheckProfileSends(p profile.Profile) error {
	if err := checkProfilePattern(p.Prompt); err != nil {
		return fmt.Errorf("プロファイル「%s」のプロンプト%w", p.Name, err)
	}
	if err := checkProfilePattern(p.MorePrompt); err != nil {
		return fmt.Errorf("プロファイル「%s」のページ送りプロンプト%w", p.Name, err)
	}
	for _, steps := range [][]profile.Step{p.Login, p.Pager, p.Disconnect, p.Answers} {
		for _, st := range steps {
			if err := CheckCommandText(st.Send); err != nil {
				return fmt.Errorf("プロファイル「%s」の送信文字に%w", p.Name, err)
			}
			if err := checkProfilePattern(st.Expect); err != nil {
				return fmt.Errorf("プロファイル「%s」の待つ文字列%w", p.Name, err)
			}
		}
	}
	// A password typed without waiting for anything goes to whatever the
	// device shows at that moment — the username prompt, a banner, the
	// operational prompt (and so the command log).
	for _, l := range profileStepLists(p) {
		for i, st := range l.steps {
			if sendsSecretVar(st) && strings.TrimSpace(st.Expect) == "" {
				return fmt.Errorf("プロファイル「%s」の%s %d は {password}/{enable} を待つ文字列なしで送ります（パスワードのプロンプトを待ってから送るようにしてください）", p.Name, l.label, i+1)
			}
		}
	}
	for _, ans := range p.Answers {
		for _, v := range []string{"{user}", "{password}", "{enable}"} {
			if strings.Contains(ans.Send, v) {
				return fmt.Errorf("確認への自動応答の「送る文字」に %s は使えません（パスワード等を想定外の質問へ送らないため）", v)
			}
		}
	}
	return nil
}

// Profile patterns (prompt, morePrompt, every step's expect) are regexps
// matched dozens of times a second per device, so a huge one brought in by
// a file slows a whole run: the same caps as the list-import rules.
const (
	maxProfilePattern      = 500
	maxProfilePatternInsts = 10000
)

// checkProfilePattern refuses a pattern longer or (once compiled) larger
// than a prompt ever needs. A pattern that does not compile is left to the
// runner, which names it when the device is connected.
func checkProfilePattern(pat string) error {
	if len(pat) > maxProfilePattern {
		return fmt.Errorf("が長すぎます（%d 文字まで）", maxProfilePattern)
	}
	if re, err := syntax.Parse(pat, syntax.Perl); err == nil {
		if prog, err := syntax.Compile(re.Simplify()); err != nil || len(prog.Inst) > maxProfilePatternInsts {
			return fmt.Errorf("が複雑すぎます（短くしてください）")
		}
	}
	return nil
}

// sendsSecretVar reports whether the step types the device password or the
// enable password (Send or SendRaw).
func sendsSecretVar(st profile.Step) bool {
	for _, v := range []string{"{password}", "{enable}"} {
		if strings.Contains(st.Send, v) || strings.Contains(st.SendRaw, v) {
			return true
		}
	}
	return false
}

// SecretPromptExpect reports whether expect looks like a password or secret
// prompt ("assword:", "Password", "passwd", "secret", "passphrase",
// 「パスワード」).
func SecretPromptExpect(expect string) bool {
	w := strings.ToLower(expect)
	for _, k := range []string{"assword", "pass", "secret", "パスワード", "暗証"} {
		if strings.Contains(w, k) {
			return true
		}
	}
	return false
}

// profileStepLists names the step lists a profile types (answers apart:
// they may not carry a credential at all).
func profileStepLists(p profile.Profile) []struct {
	label string
	steps []profile.Step
} {
	return []struct {
		label string
		steps []profile.Step
	}{{"ログイン手順", p.Login}, {"ページ送り手順", p.Pager}, {"切断手順", p.Disconnect}}
}

// SecretSteps lists the steps of p that send {password} or {enable} to
// something other than a password prompt ("ログイン手順 2: 「sername:」 →
// {password}"): typed there, the password lands in the device's AAA log in
// clear text. Not refused — some devices really ask differently — but shown
// before an import, like the command-set lines that send a password.
func SecretSteps(p profile.Profile) []string {
	var out []string
	for _, l := range profileStepLists(p) {
		for i, st := range l.steps {
			if sendsSecretVar(st) && !SecretPromptExpect(st.Expect) {
				send := st.Send
				if send == "" {
					send = st.SendRaw
				}
				out = append(out, fmt.Sprintf("%s %d: 「%s」 → %s", l.label, i+1, st.Expect, send))
			}
		}
	}
	return out
}
