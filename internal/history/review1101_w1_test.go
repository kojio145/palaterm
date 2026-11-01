package history

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/logstore"
)

// Work cycle (design 2026-11-01): the terminal windows of one group share
// one work folder, read from the disk alone.

func w1Session(name, startedAt string) DeviceSummary {
	return DeviceSummary{Name: name, Host: "192.0.2.1", Success: true, LogFile: name + "_" + startedAt[11:13] + ".txt", StartedAt: startedAt}
}

func w1Write(t *testing.T, root, name string, s Summary) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, s); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReview1101W1FindWorkDir(t *testing.T) {
	root := t.TempDir()
	tok := logstore.DefaultStageTokens
	now := time.Date(2026, 11, 1, 22, 0, 0, 0, time.Local)
	at := func(h, m int) string { return time.Date(2026, 11, 1, h, m, 0, 0, time.Local).Format(time.RFC3339) }
	join := 12 * time.Hour

	// Nothing there yet.
	if _, _, ok := FindWorkDir(root, tok, "lab", now, join, time.Time{}); ok {
		t.Fatal("found a folder in an empty root")
	}
	// A work folder of the group, two sessions: joinable.
	work := w1Write(t, root, "20261101_200000_lab_work", Summary{StartedAt: at(20, 0), Group: "lab", Stage: "work", Interactive: true, Sessions: 2,
		Devices: []DeviceSummary{w1Session("lab-sw1", at(20, 0)), w1Session("lab-sw2", at(20, 30))}})
	dir, wc, ok := FindWorkDir(root, tok, "lab", now, join, time.Time{})
	if !ok || dir != work {
		t.Fatalf("dir = %q ok=%v, want %q", dir, ok, work)
	}
	if wc.Dir != "20261101_200000_lab_work" || wc.Sessions != 2 || wc.Devices != 2 || wc.Since != at(20, 0) {
		t.Fatalf("cycle = %+v", wc)
	}
	// Another group's folder, a 予約 folder and a during batch do not close it.
	w1Write(t, root, "20261101_210000_other_work", Summary{StartedAt: at(21, 0), Group: "other", Stage: "work", Interactive: true, Devices: []DeviceSummary{w1Session("x1", at(21, 0))}})
	w1Write(t, root, "20261101_210500_lab_reserve", Summary{StartedAt: at(21, 5), Group: "lab", Stage: "after", Schedule: "nightly", Devices: []DeviceSummary{w1Session("lab-sw1", at(21, 5))}})
	w1Write(t, root, "20261101_211000_lab_during", Summary{StartedAt: at(21, 10), Group: "lab", Stage: "during", Devices: []DeviceSummary{w1Session("lab-sw1", at(21, 10))}})
	if dir, _, ok := FindWorkDir(root, tok, "lab", now, join, time.Time{}); !ok || dir != work {
		t.Fatalf("after other folders: dir = %q ok=%v", dir, ok)
	}
	// Joining turned off.
	if _, _, ok := FindWorkDir(root, tok, "lab", now, 0, time.Time{}); ok {
		t.Fatal("joined with join = 0")
	}
	// The latest session is older than the join window.
	if _, _, ok := FindWorkDir(root, tok, "lab", now.Add(12*time.Hour), join, time.Time{}); ok {
		t.Fatal("joined a folder whose latest session is 12 h old")
	}
	if dir, _, ok := FindWorkDir(root, tok, "lab", at2(20, 30).Add(join-time.Minute), join, time.Time{}); !ok || dir != work {
		t.Fatal("did not join a folder whose latest session is just inside the window")
	}
	// A manual cut newer than the folder's first session closes it.
	if _, _, ok := FindWorkDir(root, tok, "lab", now, join, at2(20, 1)); ok {
		t.Fatal("joined across a manual cut")
	}
	if dir, _, ok := FindWorkDir(root, tok, "lab", now, join, at2(19, 59)); !ok || dir != work {
		t.Fatal("an older cut closed the folder")
	}
	// A 作業後 batch of the group after the first session closes it.
	after := w1Write(t, root, "20261101_213000_lab_after", Summary{StartedAt: at(21, 30), Group: "lab", Stage: "after", Devices: []DeviceSummary{w1Session("lab-sw1", at(21, 30))}})
	if _, _, ok := FindWorkDir(root, tok, "lab", now, join, time.Time{}); ok {
		t.Fatal("joined across a 作業後 batch")
	}
	if err := os.RemoveAll(after); err != nil {
		t.Fatal(err)
	}
	// So does a 作業前 batch (it opens the next cycle); a dry run does not.
	w1Write(t, root, "20261101_214000_lab_dry", Summary{StartedAt: at(21, 40), Group: "lab", Stage: "before", DryRun: true, Devices: []DeviceSummary{}})
	if dir, _, ok := FindWorkDir(root, tok, "lab", now, join, time.Time{}); !ok || dir != work {
		t.Fatal("a dry run closed the folder")
	}
	w1Write(t, root, "20261101_214500_lab_before", Summary{StartedAt: at(21, 45), Group: "lab", Stage: "before", Devices: []DeviceSummary{w1Session("lab-sw1", at(21, 45))}})
	if _, _, ok := FindWorkDir(root, tok, "lab", now, join, time.Time{}); ok {
		t.Fatal("joined across a 作業前 batch")
	}
	// The next window's folder, after that batch, is the one to join; a
	// folder without a summary (a crashed window) is never joined.
	if err := os.MkdirAll(filepath.Join(root, "20261101_215500_lab_work"), 0o755); err != nil {
		t.Fatal(err)
	}
	work2 := w1Write(t, root, "20261101_215000_lab_work", Summary{StartedAt: at(21, 50), Group: "lab", Stage: "work", Interactive: true, Devices: []DeviceSummary{w1Session("lab-sw1", at(21, 50))}})
	if dir, wc, ok := FindWorkDir(root, tok, "lab", now, join, time.Time{}); !ok || dir != work2 || wc.Sessions != 1 {
		t.Fatalf("new cycle: dir = %q ok=%v wc=%+v", dir, ok, wc)
	}
	// Two folders of the same second ("_2"): the first name wins.
	w1Write(t, root, "20261101_215000_lab_work_2", Summary{StartedAt: at(21, 50), Group: "lab", Stage: "work", Interactive: true, Devices: []DeviceSummary{w1Session("lab-sw2", at(21, 50))}})
	if dir, _, ok := FindWorkDir(root, tok, "lab", now, join, time.Time{}); !ok || dir != work2 {
		t.Fatalf("same second: dir = %q", dir)
	}
	// A renamed stage word is read the same way.
	tok2 := logstore.StageTokens{Work: "mid"}.Filled()
	w1Write(t, root, "20261101_215800_lab_mid", Summary{StartedAt: at(21, 58), Group: "lab", Stage: "mid", Interactive: true, Devices: []DeviceSummary{w1Session("lab-sw3", at(21, 58))}})
	if dir, _, ok := FindWorkDir(root, tok2, "lab", now, join, time.Time{}); !ok || filepath.Base(dir) != "20261101_215800_lab_mid" {
		t.Fatalf("renamed stage word: dir = %q", dir)
	}
}

func at2(h, m int) time.Time { return time.Date(2026, 11, 1, h, m, 0, 0, time.Local) }

func TestReview1101W1AddDropSession(t *testing.T) {
	at := func(h, m int) string { return time.Date(2026, 11, 1, h, m, 0, 0, time.Local).Format(time.RFC3339) }
	base := Summary{App: "PalaTerm t", StartedAt: at(20, 0), FinishedAt: at(20, 0), Group: "lab", Stage: "work", Interactive: true}
	first := base
	first.Devices = []DeviceSummary{w1Session("lab-sw1", at(20, 0))}
	// A second device's session, recorded at connect time.
	cur := base
	cur.StartedAt, cur.FinishedAt = at(20, 30), at(20, 30)
	cur.Devices = []DeviceSummary{w1Session("lab-sw2", at(20, 30))}
	s := AddSession(first, cur)
	if s.StartedAt != at(20, 0) || s.FinishedAt != at(20, 30) || s.Sessions != 2 || len(s.Devices) != 2 || s.Devices[1].Name != "lab-sw2" {
		t.Fatalf("after second session: %+v", s)
	}
	// The same session ending: its entry is completed, not duplicated.
	done := w1Session("lab-sw2", at(20, 30))
	done.EndedAt, done.ElapsedSec = at(20, 40), 600
	cur.Devices = []DeviceSummary{done}
	cur.FinishedAt = at(20, 40)
	s = AddSession(s, cur)
	if s.Sessions != 2 || len(s.Devices) != 2 || s.Devices[1].EndedAt != at(20, 40) || s.FinishedAt != at(20, 40) {
		t.Fatalf("after the session ended: %+v", s)
	}
	// The same device again, later: a second entry, oldest first.
	cur.Devices = []DeviceSummary{w1Session("lab-sw1", at(21, 0))}
	s = AddSession(s, cur)
	if s.Sessions != 3 || len(s.Devices) != 3 || s.Devices[2].Name != "lab-sw1" || s.Devices[0].StartedAt != at(20, 0) {
		t.Fatalf("after the same device again: %+v", s)
	}
	// Dropping the second session (nothing logged) leaves the others.
	s = DropSession(s, done)
	if s.Sessions != 2 || len(s.Devices) != 2 || s.Devices[0].Name != "lab-sw1" || s.Devices[1].StartedAt != at(21, 0) {
		t.Fatalf("after drop: %+v", s)
	}
	// Dropping what is not there changes nothing.
	if s2 := DropSession(s, w1Session("lab-sw9", at(22, 0))); len(s2.Devices) != 2 {
		t.Fatal("drop of an unknown session changed the list")
	}
}

func TestReview1101W1AddRetry(t *testing.T) {
	prev := Summary{App: "PalaTerm old", StartedAt: "2026-11-01T20:00:00+09:00", FinishedAt: "2026-11-01T20:05:00+09:00", Group: "lab", Stage: "before",
		Devices: []DeviceSummary{
			{Name: "lab-sw1", Success: true, LogFile: "lab-sw1_before_1.txt"},
			{Name: "lab-sw2", Success: false, Error: "timeout", LogFile: "lab-sw2_before_1.txt"},
			{Name: "lab-sw3", Success: false, Error: "refused"},
		}}
	cur := Summary{App: "PalaTerm new", StartedAt: "2026-11-01T20:30:00+09:00", FinishedAt: "2026-11-01T20:31:00+09:00", Group: "lab", Stage: "before",
		Devices: []DeviceSummary{
			{Name: "lab-sw2", Success: true, LogFile: "lab-sw2_before_2.txt"},
			{Name: "lab-sw3", Success: false, Error: "still refused"},
			{Name: "lab-sw4", Success: true, LogFile: "lab-sw4_before_2.txt"},
		}}
	s := AddRetry(prev, cur)
	if s.StartedAt != prev.StartedAt || s.FinishedAt != cur.FinishedAt || s.Retries != 1 || s.App != "PalaTerm new" {
		t.Fatalf("header: %+v", s)
	}
	if len(s.Devices) != 4 || s.Devices[0].Name != "lab-sw1" || !s.Devices[0].Success {
		t.Fatalf("devices: %+v", s.Devices)
	}
	if d := s.Devices[1]; d.Name != "lab-sw2" || !d.Success || d.LogFile != "lab-sw2_before_2.txt" {
		t.Fatalf("retried device not replaced: %+v", d)
	}
	if d := s.Devices[2]; d.Name != "lab-sw3" || d.Error != "still refused" {
		t.Fatalf("retried device not replaced: %+v", d)
	}
	if d := s.Devices[3]; d.Name != "lab-sw4" || !d.Success {
		t.Fatalf("new device not appended: %+v", d)
	}
	if s2 := AddRetry(s, cur); s2.Retries != 2 || len(s2.Devices) != 4 {
		t.Fatalf("second retry: %+v", s2)
	}
}

// Several windows ending their sessions into one folder at once: every
// session is in the summary afterwards.
func TestReview1101W1UpdateConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			at := time.Date(2026, 11, 1, 20, i, 0, 0, time.Local).Format(time.RFC3339)
			cur := Summary{StartedAt: at, FinishedAt: at, Group: "lab", Stage: "work", Interactive: true, Devices: []DeviceSummary{w1Session("lab-sw"+string(rune('a'+i)), at)}}
			err := Update(dir, func(prev Summary, ok bool) (Summary, bool) {
				if !ok {
					return cur, true
				}
				return AddSession(prev, cur), true
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	s, ok := Read(dir)
	if !ok || s.Sessions != n || len(s.Devices) != n {
		t.Fatalf("sessions = %d, devices = %d, ok=%v", s.Sessions, len(s.Devices), ok)
	}
	for i := 1; i < n; i++ {
		if s.Devices[i-1].StartedAt > s.Devices[i].StartedAt {
			t.Fatalf("not oldest first: %+v", s.Devices)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, lockName)); err == nil {
		t.Fatal("lock file left behind")
	}
	// A stale lock (a process that died) does not block for ever.
	if err := os.WriteFile(filepath.Join(dir, lockName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filepath.Join(dir, lockName), old, old); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := Update(dir, func(prev Summary, ok bool) (Summary, bool) { return prev, false }); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("a stale lock held the update")
	}
}

// The history row of a work folder: sessions, distinct devices, each
// entry's start, oldest first.
func TestReview1101W1ListWorkFolder(t *testing.T) {
	root := t.TempDir()
	at := func(h, m int) string { return time.Date(2026, 11, 1, h, m, 0, 0, time.Local).Format(time.RFC3339) }
	dir := filepath.Join(root, "20261101_200000_lab_work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s1 := w1Session("lab-sw1", at(20, 0))
	s2 := w1Session("lab-sw2", at(20, 30))
	s3 := w1Session("lab-sw1", at(21, 0))
	s3.EndedAt = at(21, 10)
	for _, f := range []string{s1.LogFile, s2.LogFile, s3.LogFile} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("show version\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Write(dir, Summary{StartedAt: at(20, 0), FinishedAt: at(21, 10), Group: "lab", Stage: "work", Interactive: true, Sessions: 3, Devices: []DeviceSummary{s1, s2, s3}}); err != nil {
		t.Fatal(err)
	}
	runs, err := List(root, logstore.DefaultStageTokens)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	r := runs[0]
	if r.Sessions != 3 || r.DeviceCount != 2 || len(r.Devices) != 3 || r.OK != 3 || !r.Interactive {
		t.Fatalf("row = %+v", r)
	}
	if r.Devices[0].StartedAt != at(20, 0) || r.Devices[2].Name != "lab-sw1" || r.Devices[2].StartedAt != at(21, 0) || r.Devices[2].EndedAt != at(21, 10) {
		t.Fatalf("devices = %+v", r.Devices)
	}
	if r.Devices[2].LogPath != filepath.Join(dir, s3.LogFile) {
		t.Fatalf("log path = %q", r.Devices[2].LogPath)
	}
	// A batch row counts its devices once each and has no sessions.
	bdir := filepath.Join(root, "20261101_190000_lab_before")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(bdir, Summary{StartedAt: at(19, 0), Group: "lab", Stage: "before", Devices: []DeviceSummary{{Name: "lab-sw1", Success: true}, {Name: "lab-sw2", Success: true}}}); err != nil {
		t.Fatal(err)
	}
	runs, _ = List(root, logstore.DefaultStageTokens)
	if len(runs) != 2 || runs[1].DeviceCount != 2 || runs[1].Sessions != 0 || runs[1].Devices[0].StartedAt != "" {
		t.Fatalf("batch row = %+v", runs[1])
	}
}
