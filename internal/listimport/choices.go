package listimport

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// normalizeChoice turns a value another system wrote for a field with
// fixed choices into PalaTerm's word for it: case, full-width letters and
// spaces do not matter, and the usual ways of writing one are understood
// (SSH2, SSHv2, ＳＳＨ → ssh; TELNET(23) → telnet; CONSOLE, コンソール,
// COM3 → serial; YES, 有, ○ → true). A value it cannot place is passed on
// as written, folded only, so the preview names the cell.
func normalizeChoice(key, v string) string {
	kind := gridByKey[key].Kind
	switch kind {
	case "conn", "hopMethod", "auth", "bool", "scheme", "mode", "tint", "int":
	default:
		return v
	}
	folded := strings.TrimSpace(norm.NFKC.String(v))
	w := strings.ToLower(strings.Join(strings.Fields(folded), ""))
	if w == "" {
		return ""
	}
	has := func(words ...string) bool {
		for _, s := range words {
			if strings.Contains(w, s) {
				return true
			}
		}
		return false
	}
	switch kind {
	case "conn", "hopMethod":
		switch {
		case has("ssh"): // "SSH/Telnet" too: the secure one
			return "ssh"
		case has("telnet"):
			return "telnet"
		case kind == "conn" && (has("serial", "console", "シリアル", "コンソール") || strings.HasPrefix(w, "com")):
			return "serial"
		}
	case "auth":
		switch {
		case has("publickey", "pubkey", "公開鍵", "鍵", "key", "rsa", "ed25519", "ecdsa"):
			return "publickey"
		case has("password", "passwd", "パスワード", "pass"):
			return "password"
		}
	case "bool":
		switch w {
		case "true", "1", "yes", "y", "on", "はい", "○", "◯", "有効", "有", "あり", "する", "使う", "enable", "enabled", "〇":
			return "true"
		case "false", "0", "no", "n", "off", "いいえ", "×", "無効", "無", "なし", "しない", "使わない", "disable", "disabled", "-", "ー":
			return "false"
		}
	case "scheme":
		switch {
		case has("https"):
			return "https"
		case has("http"):
			return "http"
		}
	case "mode":
		switch {
		case has("tunnel", "トンネル"):
			return "tunnel"
		case has("shell", "コマンド"):
			return "shell"
		}
	case "int":
		return w
	case "tint":
		return w
	}
	return folded
}
