package main

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	pFindWindow             = user32.NewProc("FindWindowW")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	pSetFocus               = user32.NewProc("SetFocus")
	pShowWindow             = user32.NewProc("ShowWindow")
	pEnumChildWindows       = user32.NewProc("EnumChildWindows")
	pGetClassName           = user32.NewProc("GetClassNameW")
	pGetWindowThreadProcess = user32.NewProc("GetWindowThreadProcessId")
	pAttachThreadInput      = user32.NewProc("AttachThreadInput")
	pGetCurrentThreadId     = kernel32.NewProc("GetCurrentThreadId")
	pSetActiveWindow        = user32.NewProc("SetActiveWindow")
	pBringWindowToTop       = user32.NewProc("BringWindowToTop")
)

// focusWindowByTitle brings the top-level window with exactly that title to
// the foreground and puts the keyboard focus on the WebView2 inside it, so
// typing goes to the page at once. Used when one PalaTerm window hands over
// to another (terminal → paste confirmation → terminal): the window manager
// did not reliably activate the next window, and even when it did, the
// WebView2 child did not get the keyboard, which left the user clicking into
// the window before typing. Retries for a moment because the window may
// still be appearing. Returns whether it succeeded.
//
// SetForegroundWindow is granted because the calling process is, or was
// started by, the foreground process — the relationship these windows have.
// SetFocus only works from the thread that owns the window, so this thread
// attaches its input queue to that thread for the duration of the call.
func focusWindowByTitle(title string) bool {
	ptr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return false
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for i := 0; i < 20; i++ {
		hwnd, _, _ := pFindWindow.Call(0, uintptr(unsafe.Pointer(ptr)))
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
