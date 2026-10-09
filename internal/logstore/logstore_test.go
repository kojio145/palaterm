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
	d := RunDir(root, "", "A社", "after", now)
	if filepath.Base(d) != "log_20261009_123456_after" {
		t.Errorf("run dir: %q", d)
	}
	if filepath.Base(RunDir(root, "", "", "during", now)) != "log_20261009_123456_work" {
		t.Errorf("during run dir should say work: %q", RunDir(root, "", "", "during", now))
	}
	if got := filepath.Base(RunDir(root, "{date}_{group}_{stage}", "A社", "before", now)); got != "20261009_A社_before" {
		t.Errorf("custom dir template: %q", got)
	}
	if got := filepath.Base(RunDir(root, "{date}_{group}_{stage}", "A社", "", now)); got != "20261009_A社" {
		t.Errorf("custom dir template, no stage: %q", got)
	}
	// An existing folder of the same name gets a numeric suffix.
	os.MkdirAll(filepath.Join(root, "20261009_A社"), 0o755)
	if got := filepath.Base(RunDir(root, "{date}_{group}_{stage}", "A社", "", now)); got != "20261009_A社_2" {
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
	if !strings.HasSuffix(filepath.Base(RunDir(root, "", "", "", now)), "123456") {
		t.Errorf("run dir without stage: %q", RunDir(root, "", "", "", now))
	}
}
