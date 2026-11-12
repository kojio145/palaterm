package logstore

import (
	"strings"
	"testing"
	"time"
)

// v1.9.35: a stage word typed in the run tab (作業タイミング: 今回だけ入力).
func TestCustomStage(t *testing.T) {
	tok := StageTokens{Before: "pre", After: "post"}
	cases := []struct{ in, want, errPart string }{
		{"切替前", "切替前", ""},
		{"rollback", "rollback", ""},
		{"  rollback  ", "rollback", ""},
		{"pre", "before", ""},    // the configured 作業前 word runs as 作業前
		{"BEFORE", "before", ""}, // and so does the default, any case
		{"post", "after", ""},
		{"work", "during", ""},
		{"during", "during", ""},
		{"", "", "入力してください"},
		{"reserve", "", "予約"},
		{"Current", "", "指定なし"},
		{"config", "", "指定なし"},
		{"a_b", "", "アンダースコア"},
		{"a b", "", "使えない文字"},
		{"x/y", "", "使えない文字"},
		{"end.", "", "ドット"},
		{"CON", "", "CON"}, // sanitize rewrites a reserved name first
		{strings.Repeat("あ", 33), "", "長すぎます"},
	}
	for _, c := range cases {
		got, err := tok.CustomStage(c.in)
		if c.errPart != "" {
			if err == nil || !strings.Contains(err.Error(), c.errPart) {
				t.Errorf("CustomStage(%q): err = %v, want containing %q", c.in, err, c.errPart)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("CustomStage(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	// The word goes into the names as it is, in the folder and the file.
	now := time.Date(2026, 11, 12, 1, 2, 3, 0, time.Local)
	f := Fields{Host: "lab-sw1", IP: "192.0.2.1", OS: "generic", Group: "lab", Stage: "切替前", Tokens: DefaultStageTokens}
	name := expandName(DefaultTemplate, f, now)
	if !strings.Contains(name, "_切替前_") {
		t.Errorf("file name %q lacks the typed stage", name)
	}
	dir := RunDir(t.TempDir(), DefaultDirTemplate, "lab", "切替前", DefaultStageTokens, now)
	if !strings.HasSuffix(dir, "_lab_切替前") {
		t.Errorf("run dir %q lacks the typed stage", dir)
	}
}
