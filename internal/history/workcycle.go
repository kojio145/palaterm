package history

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kojio145/palaterm/internal/logstore"
)

// A work cycle (design 2026-11-01): the usual job is a 作業前 batch, then
// the devices one at a time in terminal windows, then a 作業後 batch and
// the comparison. The windows of one group share one work folder — one
// log file per session, one summary that accumulates them — instead of a
// folder per window. Which folder a window joins is read from the disk
// alone (FindWorkDir), so the terminal processes need no shared state.

// WorkCycle describes the work folder the next window of a group joins.
type WorkCycle struct {
	Dir      string `json:"dir"`      // folder name only ("" = none: the next window makes a new one)
	Sessions int    `json:"sessions"` // sessions logged in it so far
	Devices  int    `json:"devices"`  // distinct devices among them
	Since    string `json:"since"`    // RFC3339: when its first session started
}

// FindWorkDir returns the work folder a terminal window of group joins:
// the newest folder directly under root with a summary of that group,
// stage work and Interactive set, provided that (a) its latest session
// started less than join ago, (b) no 作業前 or 作業後 batch of the group
// (not a 予約, not a dry run) started after the folder's first session —
// a 作業前 batch opens the next cycle, a 作業後 batch closes this one —
// and (c) no manual cut (cut, zero = none) is newer than that first
// session. ok is false when there is no such folder, or join is 0 (joining
// turned off). Only the summaries are read: the log files are not touched.
func FindWorkDir(root string, tok logstore.StageTokens, group string, now time.Time, join time.Duration, cut time.Time) (dir string, wc WorkCycle, ok bool) {
	if join <= 0 {
		return "", WorkCycle{}, false
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", WorkCycle{}, false
	}
	type cand struct {
		dir   string
		s     Summary
		first time.Time
	}
	var best *cand
	var closers []time.Time // starts of the group's before / after batches
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(root, e.Name())
		s, has := Read(d)
		if !has || s.Group != group || s.Schedule != "" || s.DryRun {
			continue
		}
		first, err := time.Parse(time.RFC3339, s.StartedAt)
		if err != nil {
			continue
		}
		if s.Interactive && tok.Canonical(s.Stage) == logstore.StageWork {
			// The newest first session wins; the same second (two windows
			// claiming at once, the second one "_2") goes to the first name.
			if best == nil || first.After(best.first) || (first.Equal(best.first) && e.Name() < filepath.Base(best.dir)) {
				best = &cand{dir: d, s: s, first: first}
			}
			continue
		}
		if !s.Interactive {
			switch tok.Canonical(s.Stage) {
			case "before", "after":
				closers = append(closers, first)
			}
		}
	}
	if best == nil {
		return "", WorkCycle{}, false
	}
	if !cut.IsZero() && cut.After(best.first) {
		return "", WorkCycle{}, false
	}
	for _, c := range closers {
		if c.After(best.first) {
			return "", WorkCycle{}, false
		}
	}
	latest := latestSession(best.s, best.first)
	if !now.Before(latest.Add(join)) {
		return "", WorkCycle{}, false
	}
	return best.dir, cycleOf(best.dir, best.s), true
}

// cycleOf is the WorkCycle a joinable folder shows as.
func cycleOf(dir string, s Summary) WorkCycle {
	names := map[string]bool{}
	for _, d := range s.Devices {
		names[d.Name] = true
	}
	n := s.Sessions
	if n == 0 {
		n = len(s.Devices)
	}
	return WorkCycle{Dir: filepath.Base(dir), Sessions: n, Devices: len(names), Since: s.StartedAt}
}

// latestSession is when the folder's newest session started (first when
// no entry says).
func latestSession(s Summary, first time.Time) time.Time {
	latest := first
	for _, d := range s.Devices {
		if t, err := time.Parse(time.RFC3339, d.StartedAt); err == nil && t.After(latest) {
			latest = t
		}
	}
	return latest
}

// AddSession folds one interactive session (cur: one device entry with
// StartedAt) into the work folder's summary prev: the first start stays,
// every earlier entry is kept, and cur's entry replaces the one with the
// same name, start and log file (the session recorded at connect time,
// now ending) or is appended. Sessions counts the entries.
func AddSession(prev, cur Summary) Summary {
	out := cur
	if prev.StartedAt != "" {
		out.StartedAt = prev.StartedAt
	}
	if cur.FinishedAt == "" || (prev.FinishedAt != "" && prev.FinishedAt > cur.FinishedAt) {
		out.FinishedAt = prev.FinishedAt
	}
	devs := append([]DeviceSummary{}, prev.Devices...)
	for _, d := range cur.Devices {
		if i := sessionIndex(devs, d); i >= 0 {
			devs[i] = d
		} else {
			devs = append(devs, d)
		}
	}
	sort.SliceStable(devs, func(i, j int) bool { return devs[i].StartedAt < devs[j].StartedAt })
	out.Devices = devs
	out.Sessions = len(devs)
	out.Retries = prev.Retries
	return out
}

// DropSession takes a session recorded at connect time out again (the
// window closed with nothing logged). The folder's other sessions stay.
func DropSession(prev Summary, d DeviceSummary) Summary {
	out := prev
	out.Devices = append([]DeviceSummary{}, prev.Devices...)
	if i := sessionIndex(out.Devices, d); i >= 0 {
		out.Devices = append(out.Devices[:i], out.Devices[i+1:]...)
	}
	out.Sessions = len(out.Devices)
	return out
}

// sessionIndex finds d's own entry: same device, same start, same log
// file (two sessions of one device are two entries).
func sessionIndex(devs []DeviceSummary, d DeviceSummary) int {
	for i := range devs {
		if devs[i].Name == d.Name && devs[i].StartedAt == d.StartedAt && devs[i].LogFile == d.LogFile {
			return i
		}
	}
	return -1
}

// AddRetry folds a 失敗のみ再実行 batch (cur) into the summary of the batch
// it retried (prev): the retried devices' entries take the new result (the
// old log file stays on disk, the new one is the entry's), the others are
// kept as they were, the first start stays and the finish moves on.
func AddRetry(prev, cur Summary) Summary {
	out := prev
	out.App = cur.App
	out.FinishedAt = cur.FinishedAt
	out.Retries = prev.Retries + 1
	devs := append([]DeviceSummary{}, prev.Devices...)
	for _, d := range cur.Devices {
		replaced := false
		// The last entry of that name (a folder of several rounds keeps
		// one per device, so this is the one shown).
		for i := len(devs) - 1; i >= 0; i-- {
			if devs[i].Name == d.Name {
				devs[i] = d
				replaced = true
				break
			}
		}
		if !replaced {
			devs = append(devs, d)
		}
	}
	out.Devices = devs
	return out
}

// Update rewrites dir's summary through merge, which gets the summary now
// there (ok false: none, or unreadable) and returns the one to write
// (write false: leave the file as it is). Several terminal processes end
// their sessions into one folder: a lock file in the folder serialises
// the read-merge-write, and the write itself is atomic (Write). The lock
// of a process that died is taken over after lockStale.
func Update(dir string, merge func(prev Summary, ok bool) (out Summary, write bool)) error {
	unlock, err := lockDir(dir)
	if err != nil {
		return err
	}
	defer unlock()
	prev, ok := Read(dir)
	out, write := merge(prev, ok)
	if !write {
		return nil
	}
	return Write(dir, out)
}

const (
	lockName  = "." + SummaryName + ".lock"
	lockWait  = 3 * time.Second
	lockStale = 15 * time.Second
)

// lockDir takes dir's summary lock, waiting up to lockWait for another
// process to let it go.
func lockDir(dir string) (func(), error) {
	path := filepath.Join(dir, lockName)
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, serr := os.Stat(path); serr == nil && time.Since(st.ModTime()) > lockStale {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			// Better a summary written over than a session never recorded.
			_ = os.Remove(path)
			continue
		}
		time.Sleep(25 * time.Millisecond)
	}
}
