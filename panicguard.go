package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// A panic anywhere in the main process ends it, and with it every terminal
// window (they live in its job object, see winjob.go) — sessions cut off,
// a batch half done. Wails v2 does not recover panics in bound methods or
// in our goroutines, so the places that run work recover themselves: the
// panic is written to data\error.log (with the stack, for a bug report) and
// turned into an error the user sees, and the process goes on.

// errorLogMu serialises writes to the error log.
var errorLogMu sync.Mutex

// errorLogPath is data\error.log next to the exe (a variable for tests).
var errorLogPath = func() string { return filepath.Join(exeDir(), "data", "error.log") }

// logPanic records a recovered panic and returns the message for the user.
// Only the panic value and the stack are written: no device data, no
// passwords (the stack holds raw argument words, not their strings).
func logPanic(where string, r any) string {
	errorLogMu.Lock()
	defer errorLogMu.Unlock()
	p := errorLogPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	// Kept small: a log that grows forever on a bug in a loop is a bug too.
	if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
		_ = os.Rename(p, p+".old")
	}
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		fmt.Fprintf(f, "%s PalaTerm %s panic in %s: %v\n%s\n", time.Now().Format(time.RFC3339), appVersion, where, r, debug.Stack())
		f.Close()
	}
	return fmt.Sprintf("内部エラーが発生しました（%s: %v）。data\\error.log に記録しました", where, r)
}

// recoverGo is deferred first thing in a background goroutine: the panic is
// logged and the goroutine ends, the process lives on.
func recoverGo(where string) {
	if r := recover(); r != nil {
		logPanic(where, r)
	}
}

// recoverBound is deferred first thing in a bound method: the panic is
// logged and the page told (app:internal-error, shown as an error toast),
// since the method's own result is lost. ctx is the app's Wails context.
func recoverBound(ctx context.Context, where string) {
	if r := recover(); r != nil {
		msg := logPanic(where, r)
		if ctx != nil {
			runtime.EventsEmit(ctx, "app:internal-error", msg)
		}
	}
}
