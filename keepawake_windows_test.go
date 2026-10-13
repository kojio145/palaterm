package main

import (
	"sync"
	"testing"
)

func TestKeepSystemAwakeSetsAndClearsOnce(t *testing.T) {
	var mu sync.Mutex
	var calls []uint32
	orig := setExecState
	setExecState = func(flags uint32) uint32 {
		mu.Lock()
		calls = append(calls, flags)
		mu.Unlock()
		return 0
	}
	defer func() { setExecState = orig }()

	release := keepSystemAwake()
	release()
	release() // a second release must neither clear twice nor panic

	mu.Lock()
	defer mu.Unlock()
	want := []uint32{esContinuous | esSystemRequired, esContinuous}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#x, want %#x", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %#x, want %#x", calls, want)
		}
	}
}

// The real API (harmless): SetThreadExecutionState returns the thread's
// previous state, so the clearing call must see the flags the setting call
// left — proof both ran on the same thread and the state held meanwhile.
func TestKeepSystemAwakeRealAPI(t *testing.T) {
	var mu sync.Mutex
	var prev []uint32
	orig := setExecState
	setExecState = func(flags uint32) uint32 {
		r := orig(flags)
		mu.Lock()
		prev = append(prev, r)
		mu.Unlock()
		return r
	}
	defer func() { setExecState = orig }()

	release := keepSystemAwake()
	release()

	mu.Lock()
	defer mu.Unlock()
	if len(prev) != 2 || prev[0] == 0 {
		t.Fatalf("SetThreadExecutionState returns = %#x (0 = failed)", prev)
	}
	if prev[1] != esContinuous|esSystemRequired {
		t.Fatalf("state before clearing = %#x, want %#x", prev[1], esContinuous|esSystemRequired)
	}
}
