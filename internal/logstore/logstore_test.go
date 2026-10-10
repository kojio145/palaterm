package logstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStageInNames(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 34, 56, 0, time.Local)
	f := Fields{Host: "r1", Stage: "before"}
	// No {stage} in the template: appended before the extension.
	if got := expandName("{host}_Config_{date}_{time}.txt", f, now); got != "r1_Config_20261009_123456_before.txt" {
		t.Errorf("appended stage: %q", got)
	}
	// {stage} placeholder honoured where it is.
	if got := expandName("{stage}_{host}.log", f, now); got != "before_r1.log" {
		t.Errorf("placeholder: %q", got)
	}
	// No stage: unchanged names, no stray underscore.
	if got := expandName("{host}_Config_{date}_{time}.txt", Fields{Host: "r1"}, now); got != "r1_Config_20261009_123456.txt" {
		t.Errorf("no stage: %q", got)
	}
	if got := expandName("{host}_{stage}.txt", Fields{Host: "r1"}, now); got != "r1_Config.txt" {
		t.Errorf("no-stage placeholder should read Config: %q", got)
	}
	root := t.TempDir()
	d := RunDir(root, "", "A社", "after", StageTokens{}, now)
	if filepath.Base(d) != "log_20261009_123456_after" {
		t.Errorf("run dir: %q", d)
	}
	if filepath.Base(RunDir(root, "", "", "during", StageTokens{}, now)) != "log_20261009_123456_work" {
		t.Errorf("during run dir should say work: %q", RunDir(root, "", "", "during", StageTokens{}, now))
	}
	if got := filepath.Base(RunDir(root, "{date}_{group}_{stage}", "A社", "before", StageTokens{}, now)); got != "20261009_A社_before" {
		t.Errorf("custom dir template: %q", got)
	}
	if got := filepath.Base(RunDir(root, "{date}_{group}_{stage}", "A社", "", StageTokens{}, now)); got != "20261009_A社" {
		t.Errorf("custom dir template, no stage: %q", got)
	}
	// An existing folder of the same name gets a numeric suffix.
	os.MkdirAll(filepath.Join(root, "20261009_A社"), 0o755)
	if got := filepath.Base(RunDir(root, "{date}_{group}_{stage}", "A社", "", StageTokens{}, now)); got != "20261009_A社_2" {
		t.Errorf("unique suffix: %q", got)
	}
	// The default template: site and stage tokens, Config when no stage,
	// work for 作業中 / interactive, no doubled underscores for a blank site.
	if got := expandName(DefaultTemplate, Fields{Host: "r1", Site: "本社"}, now); got != "r1_本社_Config_20261009_123456.txt" {
		t.Errorf("default no stage: %q", got)
	}
	if got := expandName(DefaultTemplate, Fields{Host: "r1", Site: "本社", Stage: "during"}, now); got != "r1_本社_work_20261009_123456.txt" {
		t.Errorf("default during: %q", got)
	}
	if got := expandName(DefaultTemplate, Fields{Host: "r1", Stage: StageWork}, now); got != "r1_work_20261009_123456.txt" {
		t.Errorf("default work, no site: %q", got)
	}
	if got := expandName(DefaultTemplate, Fields{Host: "r1", Stage: "before"}, now); got != "r1_before_20261009_123456.txt" {
		t.Errorf("default before, no site: %q", got)
	}
	// The default carries {role} between site and stage (v1.5.8).
	if got := expandName(DefaultTemplate, Fields{Host: "r1", Site: "本社", Role: "コア", Stage: "before"}, now); got != "r1_本社_コア_before_20261009_123456.txt" {
		t.Errorf("default with role: %q", got)
	}
	for _, old := range OldDefaultTemplates {
		if !IsOldDefaultTemplate(old) {
			t.Errorf("%q should count as an old default", old)
		}
	}
	if IsOldDefaultTemplate(DefaultTemplate) || IsOldDefaultTemplate("{host}_{date}.txt") {
		t.Error("current default / user template must not count as old")
	}
	// {role} is a label like {site}: filled when set, squeezed out when blank.
	if got := expandName("{host}_{site}_{role}_{date}.txt", Fields{Host: "r1", Site: "本社", Role: "コア"}, now); got != "r1_本社_コア_20261009.txt" {
		t.Errorf("role: %q", got)
	}
	if got := expandName("{host}_{site}_{role}_{date}.txt", Fields{Host: "r1"}, now); got != "r1_20261009.txt" {
		t.Errorf("blank site and role: %q", got)
	}
	// An empty optional placeholder takes exactly one neighbouring separator
	// with it, whatever the separator is, and whichever side it is on.
	cases := []struct{ tmpl, want string }{
		{"{host}-{site}-{role}-{date}.txt", "r1-20261009.txt"},
		{"{site}_{host}_{date}.txt", "r1_20261009.txt"},
		{"{site}-{role}-{host}.txt", "r1.txt"},
		{"{host} {site} {date}.txt", "r1 20261009.txt"},
		{"{host}.{site}.txt", "r1.txt"},
		{"{host}__{site}__{date}.txt", "r1___20261009.txt"}, // exactly one separator goes; the rest is what the user typed
		{"{host}({site}).txt", "r1().txt"},                 // only separators are dropped
	}
	for _, c := range cases {
		if got := expandName(c.tmpl, Fields{Host: "r1"}, now); got != c.want {
			t.Errorf("%s: got %q, want %q", c.tmpl, got, c.want)
		}
	}
	// A filled placeholder is never touched by that rule.
	if got := expandName("{site}-{host}", Fields{Host: "r1", Site: "HQ"}, now); got != "HQ-r1" {
		t.Errorf("filled site: %q", got)
	}
	// Folder names: an empty {group} and {stage} drop out the same way.
	if got := filepath.Base(RunDir(root, "{date}-{group}-{stage}", "", "", StageTokens{}, now)); got != "20261009" {
		t.Errorf("dir with blank group and stage: %q", got)
	}
	if got := filepath.Base(RunDir(root, "{group}_{date}_{stage}", "", "after", StageTokens{}, now)); got != "20261009_after" {
		t.Errorf("dir with leading blank group: %q", got)
	}
	if !strings.HasSuffix(filepath.Base(RunDir(root, "", "", "", StageTokens{}, now)), "123456") {
		t.Errorf("run dir without stage: %q", RunDir(root, "", "", "", StageTokens{}, now))
	}
}

func TestStageTokens(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 34, 56, 0, time.Local)
	tok := StageTokens{Before: "pre", Work: "mid", After: "post"}
	if got := expandName(DefaultTemplate, Fields{Host: "r1", Stage: "before", Tokens: tok}, now); got != "r1_pre_20261009_123456.txt" {
		t.Errorf("custom before: %q", got)
	}
	if got := expandName(DefaultTemplate, Fields{Host: "r1", Stage: "during", Tokens: tok}, now); got != "r1_mid_20261009_123456.txt" {
		t.Errorf("custom during: %q", got)
	}
	if got := expandName(DefaultTemplate, Fields{Host: "r1", Stage: StageWork, Tokens: tok}, now); got != "r1_mid_20261009_123456.txt" {
		t.Errorf("custom work: %q", got)
	}
	if got := expandName(DefaultTemplate, Fields{Host: "r1", Stage: "", Tokens: tok}, now); got != "r1_Config_20261009_123456.txt" {
		t.Errorf("no stage keeps Config: %q", got)
	}
	// Partly set: the empty ones keep their default.
	half := StageTokens{After: "post"}
	if got := expandName(DefaultTemplate, Fields{Host: "r1", Stage: "before", Tokens: half}, now); got != "r1_before_20261009_123456.txt" {
		t.Errorf("default before: %q", got)
	}
	root := t.TempDir()
	if got := filepath.Base(RunDir(root, "", "", "after", tok, now)); got != "log_20261009_123456_post" {
		t.Errorf("custom run dir: %q", got)
	}
	// Canonical reads both the custom and the default words.
	for in, want := range map[string]string{"pre": "before", "PRE": "before", "before": "before", "mid": "work", "work": "work", "during": "work", "post": "after", "after": "after", "x": "x"} {
		if got := tok.Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
	// Validation.
	for _, bad := range []StageTokens{
		{Before: "a b"},                 // space
		{Before: "x/y"},                 // separator
		{Before: "same", After: "same"}, // duplicate
		{Before: "After"},               // another stage's default word
		{Work: "config"},                // the no-stage word
		{Before: strings.Repeat("あ", 33)},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
	for _, ok := range []StageTokens{{}, tok, {Before: "事前", Work: "作業", After: "事後"}, {Before: "before", Work: "work", After: "after"}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v rejected: %v", ok, err)
		}
	}
}

// Japanese stage words go through file and folder names, and back out of a
// directory listing, unchanged (Go strings are UTF-8 and the Windows file
// APIs are called with UTF-16, so nothing is transcoded by code page).
func TestStageTokensJapanese(t *testing.T) {
	now := time.Date(2026, 10, 10, 17, 30, 0, 0, time.Local)
	tok := StageTokens{Before: "事前", Work: "作業", After: "事後"}
	if err := tok.Validate(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := RunDir(root, "", "A社", "before", tok, now)
	if filepath.Base(dir) != "log_20261010_173000_事前" {
		t.Fatalf("run dir: %q", dir)
	}
	p, err := Write(dir, DefaultTemplate, Fields{Host: "r1", Site: "本社", Stage: "before", Tokens: tok}, "x", now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "r1_本社_事前_20261010_173000.txt" {
		t.Fatalf("file: %q", p)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "r1_本社_事前_20261010_173000.txt" {
		t.Fatalf("listed back: %v", entries)
	}
	if tok.Canonical("事後") != "after" || tok.Canonical("作業") != StageWork {
		t.Fatalf("canonical of Japanese words")
	}
}

func TestStageTokensValidateFileNames(t *testing.T) {
	for _, bad := range []StageTokens{
		{Before: "a\tb"},    // control character
		{Work: "post."},     // trailing dot
		{After: "CON"},      // reserved
		{After: "nul.txt"},  // reserved with extension
		{Before: "com1"},    // reserved serial name
		{Before: "\x7fabc"}, // DEL
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
	for _, ok := range []StageTokens{{Before: "com"}, {Work: "console"}, {After: "v1.2"}, {Before: "事前"}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v rejected: %v", ok, err)
		}
	}
}
