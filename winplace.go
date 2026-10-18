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
		var hwnd uintptr
		for i := 0; i < 50 && hwnd == 0; i++ {
			if hwnd = ownTopLevelWindow(); hwnd == 0 {
				time.Sleep(40 * time.Millisecond)
			}
		}
		if hwnd == 0 {
			return
		}
		const monitorDefaultToNearest = 2
		mon, _, _ := pMonitorFromRect.Call(uintptr(unsafe.Pointer(&parent)), monitorDefaultToNearest)
		mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
		if ok, _, _ := pGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
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
