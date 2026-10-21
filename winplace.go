package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"
)

// Child windows (terminal, paste confirmation, log viewer, diff) are their
// own processes, and Windows opened every one of them on the primary
// monitor — with the main window on a second monitor, each had to be dragged
// across. The parent now hands its own window rectangle to the child in
// PALATERM_PARENT_RECT, and the child centres itself over that rectangle,
// kept inside the work area of the monitor the parent is on.

const parentRectEnv = "PALATERM_PARENT_RECT"

// cascadeEnv is the terminal window's number among the ones open (0, 1,
// 2 …): each is offset by cascadeStep from the centre, so several sessions
// opened in a row do not sit exactly on top of each other.
const (
	cascadeEnv  = "PALATERM_CASCADE"
	cascadeStep = 32
)

// cascadeOffset is the offset for window number n (wrapping after 8, so a
// twelfth window still lands on screen).
func cascadeOffset(n int) int32 {
	if n < 0 {
		n = 0
	}
	return int32(n%8) * cascadeStep
}

var (
	pGetWindowRect    = user32.NewProc("GetWindowRect")
	pSetWindowPos     = user32.NewProc("SetWindowPos")
	pMonitorFromRect  = user32.NewProc("MonitorFromRect")
	pGetMonitorInfo   = user32.NewProc("GetMonitorInfoW")
	pMonitorFromWindw = user32.NewProc("MonitorFromWindow")
)

type rect struct{ L, T, R, B int32 }

type monitorInfo struct {
	CbSize  uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

// uiLang is the UI language the page reported (App.SetLang / Term.SetLang),
// handed to child windows so their OS titles match it.
var uiLang atomic.Value // string

// winWord picks a child window's title word for the language its parent
// passed in PALATERM_LANG (the page is not loaded yet when the title is set).
func winWord(ja, en string) string {
	if os.Getenv("PALATERM_LANG") == "en" {
		return en
	}
	// The main window itself: the language its page reported.
	if l, _ := uiLang.Load().(string); l == "en" {
		return en
	}
	return ja
}

// childEnv is the environment for a child window process: this process's,
// plus where this process's window is (when it has one on screen) and the
// UI language.
func childEnv(extra ...string) []string {
	env := os.Environ()
	lang, _ := uiLang.Load().(string)
	if lang == "" {
		lang = os.Getenv("PALATERM_LANG")
	}
	if lang != "" {
		env = append(env, "PALATERM_LANG="+lang)
	}
	if hwnd := ownTopLevelWindow(); hwnd != 0 {
		var r rect
		if ok, _, _ := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok != 0 {
			env = append(env, fmt.Sprintf("%s=%d,%d,%d,%d", parentRectEnv, r.L, r.T, r.R, r.B))
		}
	}
	return append(env, extra...)
}

// parseRect reads "l,t,r,b"; ok is false for anything else or an empty box.
func parseRect(s string) (rect, bool) {
	p := strings.Split(s, ",")
	if len(p) != 4 {
		return rect{}, false
	}
	var v [4]int32
	for i := range p {
		n, err := strconv.Atoi(strings.TrimSpace(p[i]))
		if err != nil {
			return rect{}, false
		}
		v[i] = int32(n)
	}
	r := rect{v[0], v[1], v[2], v[3]}
	return r, r.R > r.L && r.B > r.T
}

// centreIn is where a w×h window goes to sit centred over parent while
// staying inside work (the parent's monitor). Pure, for the tests.
func centreIn(parent, work rect, w, h int32) (x, y int32) {
	x = (parent.L+parent.R)/2 - w/2
	y = (parent.T+parent.B)/2 - h/2
	if x+w > work.R {
		x = work.R - w
	}
	if y+h > work.B {
		y = work.B - h
	}
	if x < work.L {
		x = work.L
	}
	if y < work.T {
		y = work.T
	}
	return x, y
}

// monitorWork reads a monitor's whole and work rectangles (screen
// coordinates).
func monitorWork(mon uintptr) (monitorInfo, bool) {
	mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	if mon == 0 {
		return mi, false
	}
	if ok, _, _ := pGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		return mi, false
	}
	return mi, mi.Work.R > mi.Work.L && mi.Work.B > mi.Work.T
}

// windowMonitor is the monitor hwnd is on (the nearest one).
func windowMonitor(hwnd uintptr) (monitorInfo, bool) {
	if hwnd == 0 {
		return monitorInfo{}, false
	}
	const monitorDefaultToNearest = 2
	mon, _, _ := pMonitorFromWindw.Call(hwnd, monitorDefaultToNearest)
	return monitorWork(mon)
}

// waitOwnWindow is this process's top-level window, waited for up to ten
// seconds (called from startup, before the frame is on screen; several
// windows of 並べて接続 starting at once on a busy PC took over two); 0 if
// none.
func waitOwnWindow() uintptr {
	for i := 0; i < 250; i++ {
		if hwnd := ownTopLevelWindow(); hwnd != 0 {
			return hwnd
		}
		time.Sleep(40 * time.Millisecond)
	}
	return 0
}

// windowDPI is hwnd's DPI (96 = 100 %); 96 where GetDpiForWindow does not
// exist (before Windows 10 1607) — a missing export would panic the call.
func windowDPI(hwnd uintptr) int32 {
	if procGetDpiForWindow.Find() != nil {
		return 96
	}
	d, _, _ := procGetDpiForWindow.Call(hwnd)
	if d == 0 {
		return 96
	}
	return int32(d)
}

// frameRects is hwnd's window rectangle and the visible part of its frame
// (Windows 10/11 frames carry an invisible resize border); ext is empty
// when DWM cannot tell.
func frameRects(hwnd uintptr) (win, ext rect) {
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&win)))
	const dwmaExtendedFrameBounds = 9
	if r, _, _ := pDwmGetWindowAttribute.Call(hwnd, dwmaExtendedFrameBounds, uintptr(unsafe.Pointer(&ext)), unsafe.Sizeof(ext)); r != 0 {
		ext = rect{}
	}
	return win, ext
}

// placeWindow moves and sizes hwnd to target(win, ext, dpi) — worked out
// from its current frame and DPI — and does it again after 150 ms: landing
// on a monitor with other scaling resizes the frame (WM_DPICHANGED) and
// its invisible border, which left the main window larger or smaller at
// every start and tiled windows off their cells.
func placeWindow(hwnd uintptr, target func(win, ext rect, dpi int32) rect) {
	const swpNoZOrder, swpNoActivate = 0x0004, 0x0010
	move := func() {
		win, ext := frameRects(hwnd)
		r := target(win, ext, windowDPI(hwnd))
		if r.R > r.L && r.B > r.T {
			pSetWindowPos.Call(hwnd, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoZOrder|swpNoActivate)
		}
	}
	move()
	time.Sleep(150 * time.Millisecond)
	move()
}

// placeNearParent moves this process's window over its parent's (see
// above), then lets ws.fit size it to that monitor. Runs in the background
// from startup: the frame may not be on screen yet. Moving between monitors
// with different scaling resizes the window, so the position is worked out
// again after the first move.
func placeNearParent(ctx context.Context, ws winSize) {
	parent, ok := parseRect(os.Getenv(parentRectEnv))
	if !ok {
		return
	}
	var cascade int32
	if n, err := strconv.Atoi(os.Getenv(cascadeEnv)); err == nil {
		cascade = cascadeOffset(n)
	}
	go func() {
		hwnd := waitOwnWindow()
		if hwnd == 0 {
			return
		}
		const monitorDefaultToNearest = 2
		mon, _, _ := pMonitorFromRect.Call(uintptr(unsafe.Pointer(&parent)), monitorDefaultToNearest)
		mi, ok := monitorWork(mon)
		if !ok {
			return
		}
		const swpNoSize, swpNoZOrder, swpNoActivate = 0x0001, 0x0004, 0x0010
		move := func() {
			var r rect
			if ok, _, _ := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
				return
			}
			x, y := centreIn(parent, mi.Work, r.R-r.L, r.B-r.T)
			// The cascade offset, kept inside the work area the way the
			// centre is.
			if cascade > 0 {
				x, y = centreIn(rect{L: parent.L + cascade, T: parent.T + cascade, R: parent.R + cascade, B: parent.B + cascade}, mi.Work, r.R-r.L, r.B-r.T)
			}
			pSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
		}
		if cur, _, _ := pMonitorFromWindw.Call(hwnd, monitorDefaultToNearest); cur != mon {
			move()
			time.Sleep(150 * time.Millisecond) // a scaling change resizes the frame
			ws.fit(ctx)
		}
		move()
	}()
}
