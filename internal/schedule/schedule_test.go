package schedule

import (
	"testing"
	"time"
)

var jst = time.FixedZone("JST", 9*3600)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, jst)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSlotNext(t *testing.T) {
	cases := []struct {
		name  string
		slot  Slot
		after string
		want  string // "" = never
	}{
		{"once ahead", Slot{Kind: KindOnce, Date: "2026-10-14", Time: "03:00:00"}, "2026-10-13 20:00:00", "2026-10-14 03:00:00"},
		{"once exactly now is past", Slot{Kind: KindOnce, Date: "2026-10-14", Time: "03:00:00"}, "2026-10-14 03:00:00", ""},
		{"once past", Slot{Kind: KindOnce, Date: "2026-10-12", Time: "03:00"}, "2026-10-13 20:00:00", ""},
		{"daily later today", Slot{Kind: KindDaily, Time: "21:30:15"}, "2026-10-13 20:00:00", "2026-10-13 21:30:15"},
		{"daily tomorrow", Slot{Kind: KindDaily, Time: "03:00"}, "2026-10-13 20:00:00", "2026-10-14 03:00:00"},
		{"daily same second is next day", Slot{Kind: KindDaily, Time: "20:00:00"}, "2026-10-13 20:00:00", "2026-10-14 20:00:00"},
		// 2026-10-13 is a Tuesday.
		{"weekly sun/wed", Slot{Kind: KindWeekly, Time: "12:00:00", Weekdays: []int{0, 3}}, "2026-10-13 20:00:00", "2026-10-14 12:00:00"},
		{"weekly only tue, later today", Slot{Kind: KindWeekly, Time: "23:00:00", Weekdays: []int{2}}, "2026-10-13 20:00:00", "2026-10-13 23:00:00"},
		{"weekly only tue, passed today", Slot{Kind: KindWeekly, Time: "08:00:00", Weekdays: []int{2}}, "2026-10-13 20:00:00", "2026-10-20 08:00:00"},
		{"weekly no days", Slot{Kind: KindWeekly, Time: "08:00:00"}, "2026-10-13 20:00:00", ""},
		{"interval before start", Slot{Kind: KindInterval, Date: "2026-10-14", Time: "00:00:00", EveryHours: 6}, "2026-10-13 20:00:00", "2026-10-14 00:00:00"},
		{"interval running", Slot{Kind: KindInterval, Date: "2026-10-13", Time: "01:00:00", EveryHours: 6}, "2026-10-13 20:00:00", "2026-10-14 01:00:00"},
		{"interval on boundary", Slot{Kind: KindInterval, Date: "2026-10-13", Time: "02:00:00", EveryHours: 6}, "2026-10-13 20:00:00", "2026-10-14 02:00:00"},
		{"bad time", Slot{Kind: KindDaily, Time: "25:00"}, "2026-10-13 20:00:00", ""},
	}
	for _, c := range cases {
		got := c.slot.Next(at(c.after))
		if c.want == "" {
			if !got.IsZero() {
				t.Errorf("%s: got %v, want never", c.name, got)
			}
			continue
		}
		if !got.Equal(at(c.want)) {
			t.Errorf("%s: got %v, want %s", c.name, got, c.want)
		}
	}
}

func TestScheduleNextPicksEarliestSlot(t *testing.T) {
	sc := Schedule{Enabled: true, Slots: []Slot{
		{Kind: KindDaily, Time: "03:00:00"},
		{Kind: KindOnce, Date: "2026-10-13", Time: "22:00:00"},
	}}
	if got := sc.Next(at("2026-10-13 20:00:00")); !got.Equal(at("2026-10-13 22:00:00")) {
		t.Errorf("got %v", got)
	}
	sc.Enabled = false
	if !sc.Next(at("2026-10-13 20:00:00")).IsZero() {
		t.Error("a disabled schedule must not fire")
	}
}

func TestPlan(t *testing.T) {
	sc := Schedule{Enabled: true, Slots: []Slot{{Kind: KindInterval, Date: "2026-10-13", Time: "00:00:00", EveryHours: 1}}}
	// Normal tick: the 21:00 firing, seen 5 seconds late, runs.
	run, missed := sc.Plan(at("2026-10-13 20:59:58"), at("2026-10-13 21:00:05"))
	if !run.Equal(at("2026-10-13 21:00:00")) || len(missed) != 0 {
		t.Errorf("normal: run=%v missed=%v", run, missed)
	}
	// Slept 20:59 → 23:10: 21:00 and 22:00 were missed, 23:00 (10 min late) runs.
	run, missed = sc.Plan(at("2026-10-13 20:59:00"), at("2026-10-13 23:10:00"))
	if !run.Equal(at("2026-10-13 23:00:00")) || len(missed) != 2 || !missed[0].Equal(at("2026-10-13 21:00:00")) {
		t.Errorf("after sleep: run=%v missed=%v", run, missed)
	}
	// Woke 40 min after the last firing: nothing runs, all recorded as missed.
	run, missed = sc.Plan(at("2026-10-13 20:59:00"), at("2026-10-13 23:40:00"))
	if !run.IsZero() || len(missed) != 3 {
		t.Errorf("too late: run=%v missed=%v", run, missed)
	}
	// Nothing due.
	run, missed = sc.Plan(at("2026-10-13 21:00:01"), at("2026-10-13 21:00:06"))
	if !run.IsZero() || missed != nil {
		t.Errorf("idle: run=%v missed=%v", run, missed)
	}
}

func TestValidate(t *testing.T) {
	now := at("2026-10-13 20:00:00")
	ok := Schedule{Name: "夜間", Group: "A社", Enabled: true, Slots: []Slot{{Kind: KindDaily, Time: "03:00"}}}
	if err := ok.Validate(now); err != nil {
		t.Fatalf("valid schedule refused: %v", err)
	}
	bad := []Schedule{
		{Group: "A社", Slots: ok.Slots},
		{Name: "x", Slots: ok.Slots},
		{Name: "x", Group: "A社"},
		{Name: "x", Group: "A社", Slots: []Slot{{Kind: KindOnce, Date: "2026-10-12", Time: "03:00"}}},
		{Name: "x", Group: "A社", Slots: []Slot{{Kind: KindInterval, Date: "2026-10-12", Time: "03:00", EveryHours: 0}}},
		{Name: "x", Group: "A社", Slots: []Slot{{Kind: KindWeekly, Time: "03:00"}}},
		{Name: "x", Group: "A社", Slots: []Slot{{Kind: "monthly", Time: "03:00"}}},
		{Name: "x", Group: "A社", Slots: []Slot{{Kind: KindDaily, Time: "3時"}}},
	}
	for i, sc := range bad {
		if err := sc.Validate(now); err == nil {
			t.Errorf("case %d accepted: %+v", i, sc)
		}
	}
}

func TestNextOf(t *testing.T) {
	list := []Schedule{
		{Enabled: true, Slots: []Slot{{Kind: KindDaily, Time: "03:00"}}},
		{Enabled: false, Slots: []Slot{{Kind: KindDaily, Time: "21:00"}}},
		{Enabled: true, Slots: []Slot{{Kind: KindDaily, Time: "22:00"}}},
	}
	if got := NextOf(list, at("2026-10-13 20:00:00")); !got.Equal(at("2026-10-13 22:00:00")) {
		t.Errorf("got %v", got)
	}
}

// Interval slots in seconds, limited to Count firings.
func TestIntervalSecondsAndCount(t *testing.T) {
	s := Slot{Kind: KindInterval, Date: "2026-10-13", Time: "21:00:00", EverySeconds: 30, Count: 3}
	// Firings: 21:00:00, 21:00:30, 21:01:00 — then none.
	cases := map[string]string{
		"2026-10-13 20:59:59": "2026-10-13 21:00:00",
		"2026-10-13 21:00:00": "2026-10-13 21:00:30",
		"2026-10-13 21:00:45": "2026-10-13 21:01:00",
		"2026-10-13 21:01:00": "",
		"2026-10-14 00:00:00": "",
	}
	for after, want := range cases {
		got := s.Next(at(after))
		if want == "" {
			if !got.IsZero() {
				t.Errorf("after %s: got %v, want none (count reached)", after, got)
			}
		} else if !got.Equal(at(want)) {
			t.Errorf("after %s: got %v, want %s", after, got, want)
		}
	}
	sc := Schedule{Enabled: true, Slots: []Slot{s}}
	if n := len(sc.Due(at("2026-10-13 20:00:00"), at("2026-10-13 23:00:00"))); n != 3 {
		t.Errorf("due firings = %d, want 3", n)
	}
	// No count: unlimited. The older hours field still works.
	s.Count = 0
	if s.Next(at("2026-10-14 00:00:00")).IsZero() {
		t.Error("count 0 should not stop")
	}
	if got := (Slot{Kind: KindInterval, Date: "2026-10-13", Time: "00:00:00", EveryHours: 6}).Next(at("2026-10-13 20:00:00")); !got.Equal(at("2026-10-14 00:00:00")) {
		t.Errorf("hours form: %v", got)
	}
	// Limits.
	now := at("2026-10-13 20:00:00")
	for _, bad := range []Slot{
		{Kind: KindInterval, Date: "2026-10-13", Time: "21:00", EverySeconds: 5},
		{Kind: KindInterval, Date: "2026-10-13", Time: "21:00", EverySeconds: 8 * 24 * 3600},
		{Kind: KindInterval, Date: "2026-10-13", Time: "21:00", EverySeconds: 60, Count: -1},
	} {
		sc := Schedule{Name: "x", Group: "g", Slots: []Slot{bad}}
		if sc.Validate(now) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	ok := Schedule{Name: "x", Group: "g", Slots: []Slot{{Kind: KindInterval, Date: "2026-10-13", Time: "21:00", EverySeconds: 10, Count: 5}}}
	if err := ok.Validate(now); err != nil {
		t.Errorf("10 s × 5 refused: %v", err)
	}
}

// Interval slots ending at a time, and the firings left (shown in the list).
func TestIntervalUntilAndRemaining(t *testing.T) {
	s := Slot{Kind: KindInterval, Date: "2026-10-13", Time: "21:00:00", EverySeconds: 600, Until: "2026-10-13 21:25:00"}
	// Firings: 21:00, 21:10, 21:20 (21:30 is after Until).
	if got := s.Next(at("2026-10-13 21:15:00")); !got.Equal(at("2026-10-13 21:20:00")) {
		t.Errorf("next = %v", got)
	}
	if got := s.Next(at("2026-10-13 21:20:00")); !got.IsZero() {
		t.Errorf("after the last firing before Until: %v", got)
	}
	if r := s.Remaining(at("2026-10-13 20:00:00")); r != 3 {
		t.Errorf("remaining before start = %d, want 3", r)
	}
	if r := s.Remaining(at("2026-10-13 21:05:00")); r != 2 {
		t.Errorf("remaining = %d, want 2", r)
	}
	// Count and Until together: whichever ends first.
	s.Count = 2
	if r := s.Remaining(at("2026-10-13 20:00:00")); r != 2 {
		t.Errorf("count wins: %d", r)
	}
	// Neither: unlimited.
	if r := (Slot{Kind: KindInterval, Date: "2026-10-13", Time: "21:00:00", EverySeconds: 60}).Remaining(at("2026-10-13 22:00:00")); r != -1 {
		t.Errorf("unlimited = %d", r)
	}
	// Until before the start is refused.
	bad := Schedule{Name: "x", Group: "g", Slots: []Slot{{Kind: KindInterval, Date: "2026-10-13", Time: "21:00:00", EverySeconds: 60, Until: "2026-10-13 20:00:00"}}}
	if bad.Validate(at("2026-10-13 19:00:00")) == nil {
		t.Error("until before start accepted")
	}
}
