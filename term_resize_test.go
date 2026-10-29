package main

import "testing"

// The page's screen size reaches the device once as the pty size and then
// as window-changes — unless the profile's pager-off command has gone, after
// which a window-change would make Cisco IOS page again (terminal length =
// rows). A size the device already has is not sent twice.
func TestTermResizeGating(t *testing.T) {
	tm := &Term{sizeKnown: make(chan struct{})}
	var sent [][2]int
	// Before the connect: recorded for the pty, nothing to forward yet.
	if err := tm.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tm.sizeKnown:
	default:
		t.Fatal("the first report should release a waiting connect")
	}
	if tm.cols != 100 || tm.rows != 30 {
		t.Fatalf("recorded %dx%d", tm.cols, tm.rows)
	}
	// Connected (no pager step): the pty was opened at 100x30, so that
	// size is not forwarded, a new one is, the same one again is not.
	tm.resizeFn = func(c, r int) error { sent = append(sent, [2]int{c, r}); return nil }
	tm.sentCols, tm.sentRows = 100, 30
	_ = tm.Resize(100, 30)
	_ = tm.Resize(120, 40)
	_ = tm.Resize(120, 40)
	if len(sent) != 1 || sent[0] != [2]int{120, 40} {
		t.Fatalf("forwarded %v, want one 120x40", sent)
	}
	// Pager off: nothing more is forwarded, however the window changes.
	tm.pagerOff = true
	_ = tm.Resize(200, 50)
	_ = tm.Resize(80, 24)
	if len(sent) != 1 {
		t.Fatalf("forwarded after the pager went off: %v", sent)
	}
	if tm.cols != 80 || tm.rows != 24 {
		t.Fatalf("the size is still recorded for the next connect: %dx%d", tm.cols, tm.rows)
	}
	// Zero sizes (a hidden page) are ignored.
	tm.pagerOff = false
	_ = tm.Resize(0, 0)
	if len(sent) != 1 {
		t.Fatalf("a zero size was forwarded: %v", sent)
	}
}
