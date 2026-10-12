package main

import "testing"

func TestParseRect(t *testing.T) {
	if r, ok := parseRect("-1920,0,-640,800"); !ok || r != (rect{-1920, 0, -640, 800}) {
		t.Errorf("left monitor rect = %+v, %v", r, ok)
	}
	for _, bad := range []string{"", "1,2,3", "a,b,c,d", "10,10,10,20", "0,0,5,-5"} {
		if _, ok := parseRect(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A child centres over its parent and never leaves the parent's monitor.
func TestCentreIn(t *testing.T) {
	work := rect{1920, 0, 3840, 1040} // second monitor, right of the primary
	for _, tc := range []struct {
		parent       rect
		w, h         int32
		wantX, wantY int32
	}{
		{rect{2200, 100, 3480, 900}, 980, 640, 2350, 180},  // centred over the main window
		{rect{3300, 600, 3840, 1040}, 980, 640, 2860, 400}, // parent near the corner: pulled inside
		{rect{1800, -50, 2400, 300}, 980, 640, 1920, 0},    // parent straddling the edge
		{rect{2000, 0, 3000, 1000}, 2400, 1200, 1920, 0},   // larger than the screen: top-left
	} {
		x, y := centreIn(tc.parent, work, tc.w, tc.h)
		if x != tc.wantX || y != tc.wantY {
			t.Errorf("parent %+v %dx%d: got (%d,%d), want (%d,%d)", tc.parent, tc.w, tc.h, x, y, tc.wantX, tc.wantY)
		}
	}
}
