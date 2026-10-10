package main

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	pSetFocus               = user32.NewProc("SetFocus")
	pShowWindow             = user32.NewProc("ShowWindow")
	pEnumWindows            = user32.NewProc("EnumWindows")
	pEnumChildWindows       = user32.NewProc("EnumChildWindows")
	pGetClassName           = user32.NewProc("GetClassNameW")
	pGetWindowThreadProcess = user32.NewProc("GetWindowThreadProcessId")
	pIsWindowVisible        = user32.NewProc("IsWindowVisible")
	pGetWindow              = user32.NewProc("GetWindow")
	pAttachThreadInput      = user32.NewProc("AttachThreadInput")
	pGetCurrentThreadId     = kernel32.NewProc("GetCurrentThreadId")
	pGetCurrentProcessId    = kernel32.NewProc("GetCurrentProcessId")
	pSetActiveWindow        = user32.NewProc("SetActiveWindow")
	pBringWindowToTop       = user32.NewProc("BringWindowToTop")
)

// focusMu serialises the focus attempts of one process: the page asks at
// DomReady and again once connected, and two AttachThreadInput dances at
// once would fight each other.
var focusMu sync.Mutex

// focusOwnWindow brings this process's main window to the foreground and
// puts the keyboard focus on the WebView2 inside it, so typing goes to the
// page at once. Every PalaTerm child window (terminal, paste, diff, log
// viewer) is its own process, so "the window of this process" is exact —
// looking the window up by title found the wrong one when two sessions to
// the same device were open (same title). Used when one window hands over
// to another (terminal → paste confirmation → terminal) and when a window
// first appears: the window manager did not reliably activate it, and even
// when it did, the WebView2 child did not get the keyboard. Retries for a
// moment because the window may still be appearing. Returns whether it
// succeeded.
//
// SetForegroundWindow is granted because the calling process is, or was
// started by, the foreground process — the relationship these windows have.
// SetFocus only works from the thread that owns the window, so this thread
// attaches its input queue to that thread for the duration of the call.
func focusOwnWindow() bool {
	focusMu.Lock()
	defer focusMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for i := 0; i < 20; i++ {
		hwnd := ownTopLevelWindow()
		if hwnd == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		const swShow = 5
		pShowWindow.Call(hwnd, swShow)
		tid, _, _ := pGetWindowThreadProcess.Call(hwnd, 0)
		cur, _, _ := pGetCurrentThreadId.Call()
		attached := false
		if tid != 0 && tid != cur {
			r, _, _ := pAttachThreadInput.Call(cur, tid, 1)
			attached = r != 0
		}
		pBringWindowToTop.Call(hwnd)
		ok, _, _ := pSetForegroundWindow.Call(hwnd)
		pSetActiveWindow.Call(hwnd)
		// The keyboard must be given to the WebView2's own widget window
		// (class Chrome_WidgetWin_1, or _0); focusing the frame or the
		// Chrome_RenderWidgetHostHWND child leaves keys undelivered — measured
		// with keybd_event against each child HWND.
		target := webviewInputChild(hwnd)
		if target == 0 {
			target = hwnd
		}
		pSetFocus.Call(target)
		if attached {
			pAttachThreadInput.Call(cur, tid, 0)
		}
		return ok != 0
	}
	return false
}

// ownTopLevelWindow finds the visible, unowned top-level window that belongs
// to this process (the Wails frame), or 0 while it has not appeared yet.
func ownTopLevelWindow() uintptr {
	pid, _, _ := pGetCurrentProcessId.Call()
	var found uintptr
	const gwOwner = 4
	cb := syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
		var wpid uint32
		pGetWindowThreadProcess.Call(h, uintptr(unsafe.Pointer(&wpid)))
		if uintptr(wpid) != pid {
			return 1
		}
		if v, _, _ := pIsWindowVisible.Call(h); v == 0 {
			return 1
		}
		if owner, _, _ := pGetWindow.Call(h, gwOwner); owner != 0 {
			return 1 // a tooltip or dialog owned by the frame
		}
		found = h
		return 0
	})
	pEnumWindows.Call(cb, 0)
	return found
}

// webviewInputChild finds the WebView2 child HWND that receives keyboard
// input, or 0.
func webviewInputChild(parent uintptr) uintptr {
	var win1, win0 uintptr
	cb := syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
		buf := make([]uint16, 128)
		n, _, _ := pGetClassName.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		switch syscall.UTF16ToString(buf[:n]) {
		case "Chrome_WidgetWin_1":
			win1 = h
		case "Chrome_WidgetWin_0":
			win0 = h
		}
		return 1
	})
	pEnumChildWindows.Call(parent, cb, 0)
	if win1 != 0 {
		return win1
	}
	return win0
}
