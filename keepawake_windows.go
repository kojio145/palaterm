package main

import (
	"runtime"
	"sync"
)

const (
	esSystemRequired = 0x00000001
	esContinuous     = 0x80000000
)

var pSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")

// setExecState is SetThreadExecutionState; a variable so the tests can watch
// the calls without touching the real power state.
var setExecState = func(flags uint32) uint32 {
	r, _, _ := pSetThreadExecutionState.Call(uintptr(flags))
	return uint32(r)
}

// keepSystemAwake stops Windows from idle-sleeping while a batch runs and
// returns the function that allows it again (safe to call more than once).
// The display may still turn off: only the system is kept running. Lid close
// and a manual sleep cannot be stopped this way.
//
// The execution state belongs to the calling OS thread and goroutines move
// between threads, so one goroutine locked to its thread sets the state,
// waits for the release and clears it on that same thread. It returns once
// the state is set.
func keepSystemAwake() (release func()) {
	set := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		setExecState(esContinuous | esSystemRequired)
		close(set)
		<-stop
		setExecState(esContinuous)
	}()
	<-set
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}
