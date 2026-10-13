package model

import "testing"

// The interactive window's right-click: Tera Term's two options, with
// "disable" winning over "confirm" and both off pasting at once.
func TestRightClickPaste(t *testing.T) {
	cases := []struct {
		off, confirm bool
		want         string
	}{
		{false, false, "paste"},
		{false, true, "menu"},
		{true, false, "off"},
		{true, true, "off"},
	}
	for _, c := range cases {
		s := Settings{RightClickPasteOff: c.off, RightClickPasteConfirm: c.confirm}
		if got := s.RightClickPaste(); got != c.want {
			t.Errorf("off=%v confirm=%v: got %q, want %q", c.off, c.confirm, got, c.want)
		}
	}
}
