package main

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// What a paste window's answer sends to the device: line endings as CR, a
// final Enter when asked. A cancel, or an empty text (the window's Load
// timed out and Enter was pressed in an empty box), sends nothing — not
// even the Enter.
func TestPasteText(t *testing.T) {
	for _, c := range []struct {
		res  pasteRes
		want string
		ok   bool
	}{
		{pasteRes{OK: false, Text: "show clock", CR: true}, "", false},
		{pasteRes{OK: true, Text: "", CR: true}, "", false},
		{pasteRes{OK: true, Text: "show clock\r\nshow version\n", CR: true}, "show clock\rshow version\r", true},
		{pasteRes{OK: true, Text: "show clock", CR: true}, "show clock\r", true},
		{pasteRes{OK: true, Text: "show clock", CR: false}, "show clock", true},
	} {
		if got, ok := pasteText(c.res); got != c.want || ok != c.ok {
			t.Errorf("pasteText(%+v) = %q, %v; want %q, %v", c.res, got, ok, c.want, c.ok)
		}
	}
}

// Quitting waits for the session's onClose (the log flushed, the summary
// written) rather than a fixed pause — and not past the bound when it never
// comes. With no session ever attached it returns at once.
func TestCloseSessionWaitsForOnClose(t *testing.T) {
	tm := &Term{}
	start := time.Now()
	tm.closeSession(time.Second)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("waited with no session attached")
	}
	done := make(chan struct{})
	var ran atomic.Bool
	tm.closeDone = done
	tm.closeFn = func() error {
		go func() {
			time.Sleep(30 * time.Millisecond)
			ran.Store(true)
			close(done)
		}()
		return nil
	}
	start = time.Now()
	tm.closeSession(2 * time.Second)
	if !ran.Load() {
		t.Fatal("returned before onClose ran")
	}
	if time.Since(start) > time.Second {
		t.Fatal("waited for the bound instead of onClose")
	}
	tm.closeDone = make(chan struct{})
	tm.closeFn = func() error { return nil }
	start = time.Now()
	tm.closeSession(50 * time.Millisecond)
	if d := time.Since(start); d < 50*time.Millisecond || d > time.Second {
		t.Fatalf("an onClose that never comes: waited %v, want the bound", d)
	}
}

// 「手動に切り替え」 counts only while a connect is running: pressed while
// the vault opens it is kept for the login; pressed after the login finished
// it must not leave a flag that opens the next Reconnect in manual mode.
func TestSwitchToManualOnlyWhileConnecting(t *testing.T) {
	tm := &Term{}
	if err := tm.SwitchToManual(); err != nil || tm.switched {
		t.Fatalf("no connect running: err=%v switched=%v", err, tm.switched)
	}
	tm.connecting = true
	if err := tm.SwitchToManual(); err != nil || !tm.switched {
		t.Fatalf("pressed while the vault opens must be kept: err=%v switched=%v", err, tm.switched)
	}
	tm.switched = false
	cancelled := false
	tm.cancelLogin = func() { cancelled = true }
	if err := tm.SwitchToManual(); err != nil || !tm.switched || !cancelled {
		t.Fatalf("pressed during the login: err=%v switched=%v cancelled=%v", err, tm.switched, cancelled)
	}
	tm.locked = true
	if err := tm.SwitchToManual(); err == nil {
		t.Fatal("allowed while locked")
	}
}

// A window opened by a terminal window (its paste window, its log viewer)
// does not inherit the terminal's own place in the grid or the cascade, nor
// its 同時入力: it is placed over the terminal's centre like any child.
func TestWindowEnvDropsTerminalPlacement(t *testing.T) {
	t.Setenv(tileEnv, "0,0,800,600")
	t.Setenv(cascadeEnv, "3")
	t.Setenv(bcastEnv, "1")
	t.Setenv("PALATERM_LANG", "en")
	env := windowEnv("PALATERM_VIEW_LIVE=1")
	var live, lang bool
	for _, kv := range env {
		for _, k := range []string{tileEnv, cascadeEnv, bcastEnv} {
			if strings.HasPrefix(kv, k+"=") {
				t.Errorf("%s inherited by the child window", kv)
			}
		}
		live = live || kv == "PALATERM_VIEW_LIVE=1"
		lang = lang || strings.HasPrefix(kv, "PALATERM_LANG=")
	}
	if !live || !lang {
		t.Fatalf("the extra line or the language is missing: live=%v lang=%v", live, lang)
	}
}
