package main

import "testing"

// A scheduled run asks for the window after every round: the lookups must
// not use up Windows callbacks (about 2000 per process, never freed), or a
// 10-second repeat killed the process after a few hours.
func TestWindowLookupDoesNotExhaustCallbacks(t *testing.T) {
	for i := 0; i < 3000; i++ {
		ownTopLevelWindow()
		webviewInputChild(0)
	}
}
