package schedule

import (
	"testing"
	"time"
)

// 2026-11-01 review E, B-13: an interval is counted in elapsed time, so
// across a daylight-saving change (none in Japan) a 24-hour interval fires
// 24 real hours later — another wall-clock hour that day — rather than
// being skipped or doubled. The daily kind goes by the wall clock.
func TestIntervalCountsElapsedTimeAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no zone database:", err)
	}
	// 2026-03-08 02:00 local does not exist (spring forward): 03-07 03:30
	// EST plus 24 hours is 03-08 04:30 EDT.
	s := Slot{Kind: KindInterval, Date: "2026-03-07", Time: "03:30", EverySeconds: 24 * 3600}
	start := time.Date(2026, 3, 7, 3, 30, 0, 0, loc)
	next := s.Next(start.Add(time.Second))
	if next.IsZero() {
		t.Fatal("no next firing")
	}
	if got := next.Sub(start); got != 24*time.Hour {
		t.Errorf("elapsed = %v, want 24h", got)
	}
	if next.Hour() != 4 || next.Day() != 8 {
		t.Errorf("next = %v: the wall clock should have moved by the DST hour", next)
	}
	d := Slot{Kind: KindDaily, Time: "03:30"}
	if n := d.Next(start); n.Hour() != 3 || n.Minute() != 30 || n.Day() != 8 {
		t.Errorf("daily 03:30 after 03-07 03:30 = %v, want 03-08 03:30 by the wall clock", n)
	}
}
