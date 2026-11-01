package listimport

import (
	"reflect"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// A secret field's column cannot be checked by eye (masked), so "Enabled"
// is not the enable password, "電源(PWR)" not the password, and
// "Description" is no IP address: those words only count as whole words.
func TestSuggestSecretFieldsWholeWords(t *testing.T) {
	s := &sheet.Sheet{Rows: [][]string{
		{"Hostname", "Description", "IP Address", "Enabled", "電源(PWR)", "Login Password", "Enable Password", "サポート期限"},
		{"lab-sw1", "uplink", "192.0.2.10", "TRUE", "on", "x9", "x9", "2030/01/01"},
	}}
	got := sugMap(Suggest(s, model.ImportRule{HeaderRow: 1}).Fields)
	want := map[string]string{"name": "Hostname", "host": "IP Address", "password": "Login Password", "enablePassword": "Enable Password"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all %v)", k, got[k], v, got)
		}
	}
	for _, col := range []string{"Description", "Enabled", "電源(PWR)"} {
		for k, v := range got {
			if v == col {
				t.Errorf("%s guessed from %q", k, col)
			}
		}
	}
	// The usual spellings still match: a whole word, camel case, a
	// Japanese compound, the word alone.
	s = &sheet.Sheet{Rows: [][]string{
		{"ホスト名", "IP", "enablePassword", "機器パスワード", "pw"},
		{"lab-sw1", "192.0.2.10", "x9", "x9", "x9"},
	}}
	got = sugMap(Suggest(s, model.ImportRule{HeaderRow: 1}).Fields)
	if got["host"] != "IP" || got["enablePassword"] != "enablePassword" || got["password"] != "機器パスワード" {
		t.Errorf("fields = %v", got)
	}
	s = &sheet.Sheet{Rows: [][]string{{"ホスト名", "管理IP", "特権PW", "PW"}, {"lab-sw1", "192.0.2.10", "x9", "x9"}}}
	got = sugMap(Suggest(s, model.ImportRule{HeaderRow: 1}).Fields)
	if got["host"] != "管理IP" || got["enablePassword"] != "特権PW" || got["password"] != "PW" {
		t.Errorf("fields = %v", got)
	}
}

func TestHeaderTokens(t *testing.T) {
	cases := map[string][]string{
		"IP Address":     {"ip", "address"},
		"管理IP":           {"管理", "ip"},
		"enablePassword": {"enable", "password"},
		"電源(PWR)":        {"電源", "pwr"},
		"enable_pw":      {"enable", "pw"},
		"ＩＰアドレス":         {"ip", "アドレス"},
		"Enabled":        {"enabled"},
	}
	for in, want := range cases {
		if got := headerTokens(in); !reflect.DeepEqual(got, want) {
			t.Errorf("headerTokens(%q) = %q, want %q", in, got, want)
		}
	}
}

// "Telnet（SSH不可）" names SSH only to rule it out: left as it is (the
// row then fails the check and gets looked at), not read as SSH.
func TestNormalizeChoiceNegatedConn(t *testing.T) {
	cases := map[string]string{
		"Telnet（SSH不可）":   "Telnet（SSH不可）",
		"telnet (no ssh)": "telnet (no ssh)",
		"SSH (telnet なし)": "SSH (telnet なし)",
		"SSH/Telnet":      "ssh",
		"Telnet":          "telnet",
		"SSH":             "ssh",
		"シリアル":            "serial",
	}
	for in, want := range cases {
		if got := normalizeChoice("conn", in); got != want {
			t.Errorf("normalizeChoice(conn, %q) = %q, want %q", in, got, want)
		}
	}
}
