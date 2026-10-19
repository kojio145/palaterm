package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/logstore"
)

// Folders under the log root that PalaTerm did not write — an earlier job's
// logs dropped in by hand, flat or as job\before, job\after — are listed as
// external runs: devices from the file names, the stage from the folder name.
func TestListExternalFolders(t *testing.T) {
	root := t.TempDir()
	mk := func(parts ...string) string {
		p := filepath.Join(append([]string{root}, parts...)...)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("show run\n"), 0o644)
		return p
	}
	// Flat folders from another tool.
	mk("maint_before", "sw01_before_20260901_143000.txt")
	mk("maint_before", "sw02-before.log")
	mk("maint_before", "notes.xlsx") // not a device
	mk("maint_after", "sw01_after_20260902.txt")
	mk("maint_after", "sw02-after.log")
	// A job folder holding the stage folders one level down.
	mk("案件X", "作業前", "core1.txt")
	mk("案件X", "作業中", "core1.txt")
	mk("案件X", "作業後", "core1.txt")
	// A folder with neither log files nor log sub-folders: not listed.
	os.MkdirAll(filepath.Join(root, "pictures"), 0o755)
	os.WriteFile(filepath.Join(root, "pictures", "a.png"), []byte("x"), 0o644)
	// An app run still lists the old way.
	d := filepath.Join(root, "20261001_030000_lab_before")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "sw01_HQ_core_before_20261001_030000.txt"), []byte("x"), 0o644)

	runs, err := List(root, logstore.StageTokens{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Run{}
	for _, r := range runs {
		byName[r.Name] = r
	}
	if len(runs) != 6 {
		t.Fatalf("want 6 runs, got %d: %v", len(runs), names(runs))
	}
	b := byName["maint_before"]
	if !b.External || b.Stage != "before" || len(b.Devices) != 2 || b.Devices[0].Name != "sw01" || b.Devices[1].Name != "sw02" || b.StartedAt == "" {
		t.Errorf("maint_before: %+v", b)
	}
	a := byName["maint_after"]
	if a.Stage != "after" || len(a.Devices) != 2 || a.Devices[0].Name != "sw01" || a.Devices[1].Name != "sw02" {
		t.Errorf("maint_after: %+v", a)
	}
	for sub, st := range map[string]string{"作業前": "before", "作業中": "during", "作業後": "after"} {
		r, ok := byName[filepath.Join("案件X", sub)]
		if !ok || !r.External || r.Stage != st || len(r.Devices) != 1 || r.Devices[0].Name != "core1" || r.Devices[0].Status != "unknown" {
			t.Errorf("案件X\\%s: ok=%v %+v", sub, ok, r)
		}
	}
	if _, ok := byName["案件X"]; ok {
		t.Error("the job folder itself (no log files) is listed")
	}
	if _, ok := byName["pictures"]; ok {
		t.Error("a folder without logs is listed")
	}
	app := byName["20261001_030000_lab_before"]
	if app.External || app.Devices[0].Name != "sw01" {
		t.Errorf("app folder changed: %+v", app)
	}
	// The cache follows a file added to an external folder.
	time.Sleep(20 * time.Millisecond)
	mk("maint_after", "sw03_after.txt")
	os.Chtimes(filepath.Join(root, "maint_after"), time.Now(), time.Now())
	runs, _ = List(root, logstore.StageTokens{})
	for _, r := range runs {
		if r.Name == "maint_after" && len(r.Devices) != 3 {
			t.Errorf("new file not seen: %+v", r.Devices)
		}
	}
}

func TestExternalDeviceKeyAndStage(t *testing.T) {
	tok := logstore.StageTokens{Before: "mae", After: "ato"}
	for in, want := range map[string]string{
		"sw01_before_20260901_143000.txt": "sw01",
		"core-sw1 (after).log":            "core_sw1",
		"fw1_mae.txt":                     "fw1",
		"fw1.txt":                         "fw1",
		"before.txt":                      "before", // nothing else to name it by
		"R1_HQ_core_after_20261001.txt":   "R1_HQ_core",
		"sw2_2026-09-01_14h30.log":        "sw2",
	} {
		if got := externalDeviceKey(in, tok); got != want {
			t.Errorf("externalDeviceKey(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"maint_before": "before", "post-change": "after", "案件X\\作業中ログ": "during", "ato_logs": "after",
		"before_after": "", "plain": "", "pre": "before",
	} {
		if got := stageFromName(in, tok); got != want {
			t.Errorf("stageFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func names(runs []Run) []string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = r.Name
	}
	return out
}
