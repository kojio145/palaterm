package logstore

import (
	"strings"
	"testing"
)

// F-C-7 (review 2026-11-07): a device named like a Windows reserved name
// gets a file name Windows accepts.
func TestSanitizeReservedNames(t *testing.T) {
	for in, want := range map[string]string{"CON": "CON_", "com1": "com1_", "LPT9": "LPT9_", "lab-sw1": "lab-sw1", "CONSOLE": "CONSOLE", "a:b": "a_b", "nul.txt": "nul.txt_"} {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

// B-B6: the name templates are checked when saved: a character Windows
// refuses, a reserved name, "..", a control character; the defaults and a
// nested folder template pass.
func TestCheckTemplates(t *testing.T) {
	for _, ok := range [][2]string{
		{"", ""},
		{DefaultTemplate, DefaultDirTemplate},
		{"{host}_{date}.txt", `{date}\{group}`},
		{"{host}.log", "{group}/{stage}_{hhmm}"},
	} {
		if err := CheckTemplates(ok[0], ok[1]); err != nil {
			t.Errorf("CheckTemplates(%q, %q) = %v", ok[0], ok[1], err)
		}
	}
	for _, bad := range []struct{ file, dir, want string }{
		{"{host}:{date}.txt", "", "ログファイル名の書式に Windows のファイル名に使えない文字"},
		{"CON.txt", "", "予約名"},
		{"{host}\t{date}.txt", "", "制御文字"},
		{"", `{group}\..\x`, "ログフォルダ名の書式に「..」"},
		{"", "{group}|{date}", "ログフォルダ名の書式に Windows"},
		{"", "aux", "予約名"},
	} {
		err := CheckTemplates(bad.file, bad.dir)
		if err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("CheckTemplates(%q, %q) = %v, want %q", bad.file, bad.dir, err, bad.want)
		}
	}
}
