package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readPowerInfo answers on this machine (values depend on the PC; this
// checks the calls work and the setting reads as one of its known values).
func TestReadPowerInfo(t *testing.T) {
	pi := readPowerInfo()
	t.Logf("%+v", pi)
	for _, v := range []int{pi.WakeTimersAC, pi.WakeTimersDC} {
		if v < 0 || v > 2 {
			t.Errorf("wake timer setting not read: %+v", pi)
		}
	}
}

// A time the timer failed to take is tried again at the next call; one it
// holds is not re-set.
func TestArmWakeRetriesAfterFailure(t *testing.T) {
	defer armWake(time.Time{})
	at := time.Now().Add(time.Hour).Round(0)
	first := armWake(at)
	if first != "ok" && first != "unsupported" {
		t.Skipf("no waitable timer here: %s", first)
	}
	// Held: the status is answered without a call.
	wakeTimer.mu.Lock()
	wakeTimer.status = "held"
	wakeTimer.mu.Unlock()
	if got := armWake(at); got != "held" {
		t.Errorf("re-set a time the timer holds: %q", got)
	}
	// Failed last time: set again.
	wakeTimer.mu.Lock()
	wakeTimer.armed, wakeTimer.status = false, "SetWaitableTimer: made-up failure"
	wakeTimer.mu.Unlock()
	if got := armWake(at); got != first {
		t.Errorf("after a failure: %q, want %q (tried again)", got, first)
	}
	if got := armWake(time.Time{}); got != "none" {
		t.Errorf("cancel: %q", got)
	}
}

// The main window of another folder's PalaTerm is not this folder's; the
// power info is answered from the cache within its TTL.
func TestMainWindowOfFolder(t *testing.T) {
	if w := mainWindowOfFolder(t.TempDir()); w != 0 {
		t.Errorf("a folder with no PalaTerm has a main window: %#x", w)
	}
	if p := processImage(uint32(os.Getpid())); !strings.EqualFold(filepath.Dir(p), exeDir()) {
		t.Errorf("own image %q is not under %q", p, exeDir())
	}
	if a, b := cachedPowerInfo(), cachedPowerInfo(); a != b {
		t.Errorf("cached power info differs: %+v / %+v", a, b)
	}
}
