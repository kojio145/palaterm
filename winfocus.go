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
	pGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	pSetFocus              = user32.NewProc("SetFocus")
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
	pIsIconic               = user32.NewProc("IsIconic")
	pAllowSetForeground     = user32.NewProc("AllowSetForegroundWindow")
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

// Windows callbacks are made once: Go never frees one, and a process may
// make at most about 2000 (syscall.NewCallback). ownTopLevelWindow runs
// after every scheduled run, so a fresh closure per call ran out after a few
// hours of a 10-second repeat and the process died. The callbacks take their
// state from these variables, held by enumMu for the length of one walk.
var (
	enumMu      sync.Mutex
	enumPID     uintptr
	enumFound   uintptr
	enumWin1    uintptr
	enumWin0    uintptr
	enumTopCB   = syscall.NewCallback(enumTopProc)
	enumChildCB = syscall.NewCallback(enumChildProc)
	enumFrameCB = syscall.NewCallback(enumFrameProc)
)

// enumFrameProc finds this process's Wails frame by its class, shown or not.
func enumFrameProc(h uintptr, _ uintptr) uintptr {
	var wpid uint32
	pGetWindowThreadProcess.Call(h, uintptr(unsafe.Pointer(&wpid)))
	if uintptr(wpid) != enumPID {
		return 1
	}
	buf := make([]uint16, 64)
	n, _, _ := pGetClassName.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if syscall.UTF16ToString(buf[:n]) != "wailsWindow" {
		return 1
	}
	enumFound = h
	return 0
}

// foregroundWindow is the window the user is in (any process), or 0.
func foregroundWindow() uintptr {
	h, _, _ := pGetForegroundWindow.Call()
	return h
}

// keepForegroundWhileHidden hands the foreground back to prev for as long as
// hidden() says this window has not been shown (up to 10 s): Windows lets a
// process started by the foreground one take the foreground, and a frame
// created hidden did — the keyboard went to an invisible window. Being the
// foreground process then, it may give it back.
func keepForegroundWhileHidden(prev uintptr, hidden func() bool) {
	if prev == 0 {
		return
	}
	for i := 0; i < 200 && hidden(); i++ {
		if own := ownFrame(); own != 0 && foregroundWindow() == own {
			pSetForegroundWindow.Call(prev)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ownFrame is this process's Wails frame even while it is hidden (a window
// started hidden is placed before it appears), or 0.
func ownFrame() uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumPID, _, _ = pGetCurrentProcessId.Call()
	enumFound = 0
	pEnumWindows.Call(enumFrameCB, 0)
	return enumFound
}

func enumTopProc(h uintptr, _ uintptr) uintptr {
	const gwOwner = 4
	var wpid uint32
	pGetWindowThreadProcess.Call(h, uintptr(unsafe.Pointer(&wpid)))
	if uintptr(wpid) != enumPID {
		return 1
	}
	if v, _, _ := pIsWindowVisible.Call(h); v == 0 {
		return 1
	}
	if owner, _, _ := pGetWindow.Call(h, gwOwner); owner != 0 {
		return 1 // a tooltip or dialog owned by the frame
	}
	enumFound = h
	return 0
}

func enumChildProc(h uintptr, _ uintptr) uintptr {
	buf := make([]uint16, 128)
	n, _, _ := pGetClassName.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	switch syscall.UTF16ToString(buf[:n]) {
	case "Chrome_WidgetWin_1":
		enumWin1 = h
	case "Chrome_WidgetWin_0":
		enumWin0 = h
	}
	return 1
}

// ownTopLevelWindow finds the visible, unowned top-level window that belongs
// to this process (the Wails frame), or 0 while it has not appeared yet.
func ownTopLevelWindow() uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumPID, _, _ = pGetCurrentProcessId.Call()
	enumFound = 0
	pEnumWindows.Call(enumTopCB, 0)
	return enumFound
}

// topLevelWindowOf is ownTopLevelWindow for another process (a child
// window), or 0.
func topLevelWindowOf(pid int) uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumPID = uintptr(pid)
	enumFound = 0
	pEnumWindows.Call(enumTopCB, 0)
	return enumFound
}

// bringProcessToFront raises a child window process's window (restored if
// minimised) from this, the foreground process — the one the user just
// clicked in — and lets it take the foreground itself (it then puts the
// keyboard on its page: focusOwnWindow).
func bringProcessToFront(pid int) {
	if pid <= 0 {
		return
	}
	pAllowSetForeground.Call(uintptr(pid))
	hwnd := topLevelWindowOf(pid)
	if hwnd == 0 {
		return
	}
	const swRestore = 9
	if ic, _, _ := pIsIconic.Call(hwnd); ic != 0 {
		pShowWindow.Call(hwnd, swRestore)
	}
	pBringWindowToTop.Call(hwnd)
	pSetForegroundWindow.Call(hwnd)
}

// webviewInputChild finds the WebView2 child HWND that receives keyboard
// input, or 0.
func webviewInputChild(parent uintptr) uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumWin1, enumWin0 = 0, 0
	pEnumChildWindows.Call(parent, enumChildCB, 0)
	if enumWin1 != 0 {
		return enumWin1
	}
	return enumWin0
}
