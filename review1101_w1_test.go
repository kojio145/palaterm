package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/history"
	"github.com/kojio145/palaterm/internal/logstore"
	"github.com/kojio145/palaterm/internal/model"
	runpkg "github.com/kojio145/palaterm/internal/runner"
)

// w1App is an unlocked App whose log root is a temp folder.
func w1App(t *testing.T) (*App, string) {
	t.Helper()
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	a.mu.Lock()
	a.inv.Settings.LogDir = root
	a.mu.Unlock()
	return a, root
}

func w1Batch(t *testing.T, root, name string, s history.Summary) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := history.Write(dir, s); err != nil {
		t.Fatal(err)
	}
	return dir
}

// 失敗のみ再実行 writes into the original folder only when that folder is a
// batch of the same group and stage directly under the log root.
func TestReview1101W1RetryDir(t *testing.T) {
	a, root := w1App(t)
	before := w1Batch(t, root, "20261101_200000_lab_before", history.Summary{StartedAt: "2026-11-01T20:00:00+09:00", Group: "lab", Stage: "before",
		Devices: []history.DeviceSummary{{Name: "lab-sw1", Success: true}, {Name: "lab-sw2", Error: "timeout"}}})
	req := RunRequest{Names: []string{"lab-sw2"}, Group: "lab", Stage: "before", RetryDir: before}
	if dir, why := a.retryDir(req); dir != before || why != "" {
		t.Fatalf("good folder: dir=%q why=%q", dir, why)
	}
	// Relative spelling and a trailing separator resolve to the same folder.
	if dir, _ := a.retryDir(RunRequest{Group: "lab", Stage: "before", RetryDir: before + string(filepath.Separator)}); dir != before {
		t.Fatalf("trailing separator: %q", dir)
	}
	bad := []struct {
		name string
		req  RunRequest
		why  string
	}{
		{"no retry", RunRequest{Group: "lab", Stage: "before"}, ""},
		{"dry run", RunRequest{Group: "lab", Stage: "before", DryRun: true, RetryDir: before}, ""},
		{"other group", RunRequest{Group: "other", Stage: "before", RetryDir: before}, "グループまたは作業タイミング"},
		{"other stage", RunRequest{Group: "lab", Stage: "after", RetryDir: before}, "グループまたは作業タイミング"},
		{"missing", RunRequest{Group: "lab", Stage: "before", RetryDir: filepath.Join(root, "20261101_000000_lab_before")}, "見つからない"},
		{"outside root", RunRequest{Group: "lab", Stage: "before", RetryDir: t.TempDir()}, "直下にない"},
		{"root itself", RunRequest{Group: "lab", Stage: "before", RetryDir: root}, "直下にない"},
		{"parent of root", RunRequest{Group: "lab", Stage: "before", RetryDir: filepath.Join(root, "..")}, "直下にない"},
		{"nested", RunRequest{Group: "lab", Stage: "before", RetryDir: filepath.Join(before, "sub")}, "直下にない"},
	}
	for _, c := range bad {
		dir, why := a.retryDir(c.req)
		if dir != "" || !strings.Contains(why, c.why) {
			t.Errorf("%s: dir=%q why=%q", c.name, dir, why)
		}
	}
	// Folders that are not a plain batch of the group.
	nosum := filepath.Join(root, "20261101_201000_lab_before")
	if err := os.MkdirAll(nosum, 0o755); err != nil {
		t.Fatal(err)
	}
	if dir, why := a.retryDir(RunRequest{Group: "lab", Stage: "before", RetryDir: nosum}); dir != "" || !strings.Contains(why, "実行記録がない") {
		t.Errorf("no summary: dir=%q why=%q", dir, why)
	}
	work := w1Batch(t, root, "20261101_202000_lab_work", history.Summary{Group: "lab", Stage: "work", Interactive: true, Devices: []history.DeviceSummary{{Name: "lab-sw1", Success: true}}})
	if dir, why := a.retryDir(RunRequest{Group: "lab", Stage: "work", RetryDir: work}); dir != "" || !strings.Contains(why, "対話接続") {
		t.Errorf("interactive: dir=%q why=%q", dir, why)
	}
	sched := w1Batch(t, root, "20261101_203000_lab_reserve", history.Summary{Group: "lab", Stage: "", Schedule: "nightly", Devices: []history.DeviceSummary{{Name: "lab-sw1", Success: true}}})
	if dir, why := a.retryDir(RunRequest{Group: "lab", Stage: "", RetryDir: sched}); dir != "" || !strings.Contains(why, "予約") {
		t.Errorf("schedule: dir=%q why=%q", dir, why)
	}
	dry := w1Batch(t, root, "20261101_204000_lab_before", history.Summary{Group: "lab", Stage: "before", DryRun: true, Devices: []history.DeviceSummary{}})
	if dir, why := a.retryDir(RunRequest{Group: "lab", Stage: "before", RetryDir: dry}); dir != "" || !strings.Contains(why, "ドライラン") {
		t.Errorf("dry-run record: dir=%q why=%q", dir, why)
	}
}

// A retry's summary replaces the retried devices' rows and keeps the rest.
func TestReview1101W1WriteRunSummaryRetry(t *testing.T) {
	root := t.TempDir()
	dir := w1Batch(t, root, "20261101_200000_lab_before", history.Summary{App: "PalaTerm old", StartedAt: "2026-11-01T20:00:00+09:00", FinishedAt: "2026-11-01T20:05:00+09:00", Group: "lab", Stage: "before",
		Devices: []history.DeviceSummary{{Name: "lab-sw1", Success: true, LogFile: "lab-sw1_before_200000.txt"}, {Name: "lab-sw2", Error: "timeout"}}})
	inv := &model.Inventory{Devices: []model.Device{{Name: "lab-sw2", Host: "192.0.2.2", Site: "dc1"}}}
	req := RunRequest{Names: []string{"lab-sw2"}, Group: "lab", Stage: "before", RetryDir: dir}
	results := []runpkg.DeviceResult{{Device: "lab-sw2", Success: true, LogPath: filepath.Join(dir, "lab-sw2_before_203000.txt"), Elapsed: 3 * time.Second}}
	writeRunSummary(dir, inv, req, "", summaryRetry, time.Now(), results)
	s, ok := history.Read(dir)
	if !ok || s.Retries != 1 || s.StartedAt != "2026-11-01T20:00:00+09:00" || len(s.Devices) != 2 {
		t.Fatalf("summary = %+v ok=%v", s, ok)
	}
	if d := s.Devices[0]; d.Name != "lab-sw1" || !d.Success || d.LogFile != "lab-sw1_before_200000.txt" {
		t.Fatalf("kept device changed: %+v", d)
	}
	if d := s.Devices[1]; d.Name != "lab-sw2" || !d.Success || d.Error != "" || d.LogFile != "lab-sw2_before_203000.txt" || d.Host != "192.0.2.2" {
		t.Fatalf("retried device not replaced: %+v", d)
	}
	// summaryNew still replaces the whole summary (a plain run).
	writeRunSummary(dir, inv, RunRequest{Group: "lab", Stage: "before"}, "", summaryNew, time.Now(), results)
	if s, _ := history.Read(dir); len(s.Devices) != 1 || s.Retries != 0 {
		t.Fatalf("plain write: %+v", s)
	}
}

// The terminal window's folder: the group's current work folder when one
// is open, a new one otherwise; a manual cut and WorkJoinHours = -1 give
// a new one; nothing is ever made outside the log root.
func TestReview1101W1WorkRunDir(t *testing.T) {
	a, root := w1App(t)
	group := "lab-w1-" + time.Now().Format("150405.000")
	s := model.Settings{LogDir: root, LogDirTemplate: logstore.DefaultDirTemplate}
	now := time.Now()
	d1 := workRunDir(s, group, now)
	if filepath.Dir(d1) != filepath.Clean(root) {
		t.Fatalf("first folder %q not directly under the root", d1)
	}
	if fi, err := os.Stat(d1); err != nil || !fi.IsDir() {
		t.Fatalf("first folder not created: %v", err)
	}
	// Nothing to join yet (no summary): the next window takes its own.
	if d := workRunDir(s, group, now); d == d1 {
		t.Fatal("joined a folder without a summary")
	}
	// The first window recorded its session: the next window joins.
	sess := history.Summary{StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339), Group: group, Stage: logstore.StageWork, Interactive: true,
		Devices: []history.DeviceSummary{{Name: "lab-sw1", Success: true, LogFile: "lab-sw1_work.txt", StartedAt: now.Format(time.RFC3339)}}}
	if err := history.Update(d1, func(prev history.Summary, ok bool) (history.Summary, bool) { return sess, true }); err != nil {
		t.Fatal(err)
	}
	if d := workRunDir(s, group, now.Add(time.Hour)); d != d1 {
		t.Fatalf("second window got %q, want %q", d, d1)
	}
	wc := a.WorkCycleInfo(group)
	if wc.Dir != filepath.Base(d1) || wc.Sessions != 1 || wc.Devices != 1 {
		t.Fatalf("WorkCycleInfo = %+v", wc)
	}
	// Joining off: a new folder every time.
	s.WorkJoinHours = model.WorkJoinNever
	if d := workRunDir(s, group, now.Add(2*time.Hour)); d == d1 {
		t.Fatal("joined with WorkJoinHours = -1")
	}
	s.WorkJoinHours = 0
	// Past the join window: a new folder.
	if d := workRunDir(s, group, now.Add(13*time.Hour)); d == d1 {
		t.Fatal("joined 13 h after the latest session")
	}
	// A manual cut closes the folder for the next window.
	time.Sleep(1100 * time.Millisecond) // the cut is recorded to the second, after the session's start
	if err := a.CutWorkCycle(group); err != nil {
		t.Fatal(err)
	}
	if d := workRunDir(s, group, time.Now()); d == d1 {
		t.Fatal("joined across a manual cut")
	}
	if wc := a.WorkCycleInfo(group); wc.Dir != "" {
		t.Fatalf("WorkCycleInfo after the cut = %+v", wc)
	}
	// Locked: the cut is refused.
	a.mu.Lock()
	inv := a.inv
	a.inv = nil
	a.mu.Unlock()
	if err := a.CutWorkCycle(group); err == nil {
		t.Fatal("cut while locked")
	}
	a.mu.Lock()
	a.inv = inv
	a.mu.Unlock()
}
