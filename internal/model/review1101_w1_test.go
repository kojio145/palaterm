package model

import (
	"testing"
	"time"
)

// Settings.WorkJoinHours: 0 = the default, -1 = never, otherwise hours.
func TestReview1101W1WorkJoin(t *testing.T) {
	cases := []struct {
		hours int
		want  time.Duration
		valid bool
	}{
		{0, DefaultWorkJoinHours * time.Hour, true},
		{WorkJoinNever, 0, true},
		{1, time.Hour, true},
		{36, 36 * time.Hour, true},
		{MaxWorkJoinHours, MaxWorkJoinHours * time.Hour, true},
		{MaxWorkJoinHours + 1, MaxWorkJoinHours * time.Hour, false},
		{-2, 0, false},
	}
	for _, c := range cases {
		s := Settings{WorkJoinHours: c.hours}
		if got := s.WorkJoin(); got != c.want {
			t.Errorf("WorkJoinHours %d: WorkJoin = %v, want %v", c.hours, got, c.want)
		}
		if err := s.ValidateWorkJoinHours(); (err == nil) != c.valid {
			t.Errorf("WorkJoinHours %d: validate = %v, want valid=%v", c.hours, err, c.valid)
		}
	}
	// A vault from before the setting (zero value) joins with the default.
	if DefaultSettings().WorkJoin() != DefaultWorkJoinHours*time.Hour {
		t.Fatal("default settings do not join with the default window")
	}
}
