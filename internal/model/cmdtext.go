package model

import (
	"fmt"
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
	for _, steps := range [][]profile.Step{p.Login, p.Pager, p.Disconnect, p.Answers} {
		for _, st := range steps {
			if err := CheckCommandText(st.Send); err != nil {
				return fmt.Errorf("プロファイル「%s」の送信文字に%w", p.Name, err)
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
