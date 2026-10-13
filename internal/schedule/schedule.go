// Package schedule holds 予約 (scheduled batch runs): what a schedule runs,
// when it fires, and the pure time arithmetic the scheduler in package main
// builds on — next firing time, the firings a sleeping PC slept through, and
// whether a late firing is still worth running. All times are local wall
// clock (the times the user typed), to the second.
package schedule

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Kinds of slot (枠).
const (
	KindOnce     = "once"     // 1 回: Date + Time
	KindDaily    = "daily"    // 毎日: Time
	KindWeekly   = "weekly"   // 毎週: Weekdays + Time
	KindInterval = "interval" // n 秒・分・時間おき: from Date + Time, every Period, Count times
)

// MaxLate is how late a firing may still start: one that had to wait longer
// (another batch running, the PC waking late) is skipped and recorded, not
// run at a time nobody planned for.
const MaxLate = 30 * time.Minute

// Slot is one firing rule of a schedule. A schedule may have several (毎日
// 03:00 and 毎週日曜 12:00, say).
type Slot struct {
	Kind string `json:"kind"`
	// Date is "YYYY-MM-DD": the day of a once slot, the first day of an
	// interval slot. Unused otherwise.
	Date string `json:"date,omitempty"`
	// Time is "HH:MM:SS" (or "HH:MM") local time.
	Time string `json:"time"`
	// Weekdays of a weekly slot, 0 = Sunday … 6 = Saturday.
	Weekdays []int `json:"weekdays,omitempty"`
	// EverySeconds is an interval slot's period. EveryHours is the older
	// form (v1.6.0–v1.6.1), read when EverySeconds is 0.
	EverySeconds int `json:"everySeconds,omitempty"`
	EveryHours   int `json:"everyHours,omitempty"`
	// Count limits an interval slot to that many firings, the first at
	// Date + Time (0 = no limit).
	Count int `json:"count,omitempty"`
	// Until ends an interval slot: no firing after this local date and time
	// ("YYYY-MM-DD HH:MM:SS"; empty = no end time).
	Until string `json:"until,omitempty"`
}

// Schedule is one 予約: a batch run (the run tab's choices) plus when.
type Schedule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`

	// What runs, as in the run tab. Names nil means the group's devices
	// that are checked (enabled) when it fires; a list fixes the devices.
	Group      string   `json:"group"`
	Ungrouped  bool     `json:"ungrouped,omitempty"`
	Names      []string `json:"names,omitempty"`
	CommandSet string   `json:"commandSet,omitempty"` // "" = each device's own
	// Commands typed for this schedule, one per line (the run tab's 今回だけ
	// 入力); when set they run instead of any command set.
	Commands []string `json:"commands,omitempty"`
	Stage    string   `json:"stage,omitempty"` // "" / before / during / after

	Slots []Slot `json:"slots"`
}

// NewID returns a fresh random schedule ID.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func parseClock(s string) (h, m, sec int, err error) {
	s = strings.TrimSpace(s)
	var n int
	switch strings.Count(s, ":") {
	case 1:
		n, err = fmt.Sscanf(s, "%d:%d", &h, &m)
		if n != 2 {
			err = fmt.Errorf("bad time")
		}
	case 2:
		n, err = fmt.Sscanf(s, "%d:%d:%d", &h, &m, &sec)
		if n != 3 {
			err = fmt.Errorf("bad time")
		}
	default:
		err = fmt.Errorf("bad time")
	}
	if err != nil || h < 0 || h > 23 || m < 0 || m > 59 || sec < 0 || sec > 59 {
		return 0, 0, 0, fmt.Errorf("時刻の形式が不正です: %q（例 03:00:00）", s)
	}
	return h, m, sec, nil
}

func parseDate(s string) (y int, mo time.Month, d int, err error) {
	t, e := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local)
	if e != nil {
		return 0, 0, 0, fmt.Errorf("日付の形式が不正です: %q（例 2026-10-14）", s)
	}
	return t.Year(), t.Month(), t.Day(), nil
}

// at is the slot's Date + Time in loc.
func (s Slot) at(loc *time.Location) (time.Time, error) {
	y, mo, d, err := parseDate(s.Date)
	if err != nil {
		return time.Time{}, err
	}
	h, m, sec, err := parseClock(s.Time)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(y, mo, d, h, m, sec, 0, loc), nil
}

// Interval limits: shorter than MinInterval and the devices would be
// logged into again before they finished answering.
const (
	MinInterval = 10 * time.Second
	MaxInterval = 7 * 24 * time.Hour
	MaxCount    = 100000
)

// Period is an interval slot's period (0 for other kinds or unset).
func (s Slot) Period() time.Duration {
	if s.EverySeconds > 0 {
		return time.Duration(s.EverySeconds) * time.Second
	}
	return time.Duration(s.EveryHours) * time.Hour
}

// Validate checks one slot.
func (s Slot) Validate() error {
	switch s.Kind {
	case KindOnce, KindInterval:
		if _, err := s.at(time.Local); err != nil {
			return err
		}
		if s.Kind == KindInterval {
			if p := s.Period(); p < MinInterval || p > MaxInterval {
				return fmt.Errorf("間隔は 10 秒〜7 日で指定してください")
			}
			if s.Count < 0 || s.Count > MaxCount {
				return fmt.Errorf("回数は 1〜%d 回で指定してください", MaxCount)
			}
			if s.Until != "" {
				u, err := s.until(time.Local)
				if err != nil {
					return err
				}
				if start, _ := s.at(time.Local); !u.After(start) {
					return fmt.Errorf("終わりの日時は開始より後にしてください")
				}
			}
		}
	case KindDaily, KindWeekly:
		if _, _, _, err := parseClock(s.Time); err != nil {
			return err
		}
		if s.Kind == KindWeekly {
			if len(s.Weekdays) == 0 {
				return fmt.Errorf("毎週の枠は曜日を 1 つ以上選んでください")
			}
			for _, w := range s.Weekdays {
				if w < 0 || w > 6 {
					return fmt.Errorf("曜日の値が不正です: %d", w)
				}
			}
		}
	default:
		return fmt.Errorf("枠の種類が不正です: %q", s.Kind)
	}
	return nil
}

// until parses Until.
func (s Slot) until(loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(s.Until), loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("終わりの日時の形式が不正です: %q", s.Until)
	}
	return t, nil
}

// intervalIndex is the index (0 = the start) of an interval slot's first
// firing strictly after after, ignoring Count and Until.
func (s Slot) intervalIndex(after time.Time) (start time.Time, n int64, ok bool) {
	start, err := s.at(after.Location())
	period := s.Period()
	if err != nil || period <= 0 {
		return start, 0, false
	}
	if !start.After(after) {
		n = int64(after.Sub(start)/period) + 1
	}
	return start, n, true
}

// Remaining is how many firings an interval slot still has after after:
// limited by Count and Until, or -1 when neither limits it.
func (s Slot) Remaining(after time.Time) int {
	if s.Kind != KindInterval {
		return -1
	}
	start, n, ok := s.intervalIndex(after)
	if !ok {
		return 0
	}
	left := int64(-1)
	if s.Count > 0 {
		left = max(int64(s.Count)-n, 0)
	}
	if s.Until != "" {
		if u, err := s.until(after.Location()); err == nil {
			last := int64(u.Sub(start) / s.Period()) // index of the last firing at or before Until
			byUntil := max(last-n+1, 0)
			if left < 0 || byUntil < left {
				left = byUntil
			}
		}
	}
	return int(left)
}

// Next returns the slot's first firing strictly after after, or the zero
// time when it never fires again (a once slot in the past, or an invalid
// slot).
func (s Slot) Next(after time.Time) time.Time {
	loc := after.Location()
	switch s.Kind {
	case KindOnce:
		t, err := s.at(loc)
		if err != nil || !t.After(after) {
			return time.Time{}
		}
		return t
	case KindInterval:
		start, n, ok := s.intervalIndex(after)
		if !ok || s.Remaining(after) == 0 {
			return time.Time{} // all Count firings, or Until, are behind
		}
		return start.Add(time.Duration(n) * s.Period())
	case KindDaily, KindWeekly:
		h, m, sec, err := parseClock(s.Time)
		if err != nil {
			return time.Time{}
		}
		days := map[int]bool{}
		for _, w := range s.Weekdays {
			days[w] = true
		}
		for i := 0; i <= 8; i++ {
			d := after.AddDate(0, 0, i)
			t := time.Date(d.Year(), d.Month(), d.Day(), h, m, sec, 0, loc)
			if !t.After(after) {
				continue
			}
			if s.Kind == KindWeekly && !days[int(t.Weekday())] {
				continue
			}
			return t
		}
	}
	return time.Time{}
}

// Validate checks a schedule before it is saved. now rejects a once slot
// already in the past (it would never fire).
func (sc Schedule) Validate(now time.Time) error {
	if strings.TrimSpace(sc.Name) == "" {
		return fmt.Errorf("予約名を入力してください")
	}
	if sc.Group == "" && !sc.Ungrouped {
		return fmt.Errorf("実行するグループを選んでください")
	}
	if len(sc.Slots) == 0 {
		return fmt.Errorf("実行する日時（枠）を 1 つ以上追加してください")
	}
	for i, s := range sc.Slots {
		if err := s.Validate(); err != nil {
			return fmt.Errorf("%d つ目の日時: %w", i+1, err)
		}
		if s.Kind == KindOnce && s.Next(now).IsZero() {
			return fmt.Errorf("%d つ目の日時が過ぎています（%s %s）。今より後の日時を選んでください", i+1, s.Date, s.Time)
		}
	}
	return nil
}

// Next is the schedule's first firing strictly after after over all its
// slots (zero: none, or disabled).
func (sc Schedule) Next(after time.Time) time.Time {
	if !sc.Enabled {
		return time.Time{}
	}
	var best time.Time
	for _, s := range sc.Slots {
		if t := s.Next(after); !t.IsZero() && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	return best
}

// maxDue bounds Due's walk (an hourly slot over a month-long gap is 720).
const maxDue = 2000

// Due returns the firings in (from, to], oldest first: what fell due
// between the scheduler's last look and now. Normally one or none; after a
// sleep with no wake-up, several.
func (sc Schedule) Due(from, to time.Time) []time.Time {
	var out []time.Time
	t := from
	for len(out) < maxDue {
		n := sc.Next(t)
		if n.IsZero() || n.After(to) {
			break
		}
		out = append(out, n)
		t = n
	}
	return out
}

// Plan splits the firings in (from, to] into the one to run now and the ones
// missed. Only the newest firing may run, and only when it is at most
// MaxLate old: firings slept through are recorded, never caught up on.
func (sc Schedule) Plan(from, to time.Time) (run time.Time, missed []time.Time) {
	due := sc.Due(from, to)
	if len(due) == 0 {
		return time.Time{}, nil
	}
	last := due[len(due)-1]
	missed = due[:len(due)-1]
	if to.Sub(last) > MaxLate {
		return time.Time{}, due
	}
	return last, missed
}

// NextOf is the earliest Next over schedules (zero: nothing scheduled).
func NextOf(list []Schedule, after time.Time) time.Time {
	var best time.Time
	for _, sc := range list {
		if t := sc.Next(after); !t.IsZero() && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	return best
}

// SortedWeekdays returns ws sorted without duplicates (for storage).
func SortedWeekdays(ws []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, w := range ws {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	sort.Ints(out)
	return out
}
