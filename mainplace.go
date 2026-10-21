package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"unsafe"
)

// The main window opens inside the work area of its monitor (the screen
// minus the taskbar) and where it was last closed, maximised if it was.
// On a laptop at 150 % scaling (1280×720 logical, 672 high above the
// taskbar) the old 1280×800 default — and even its 680-high minimum — ran
// under the taskbar, and the window fitted only maximised; it then opened
// unmaximised again at every start (2026-10-21, field request).
//
// Everything here is in screen coordinates. WINDOWPLACEMENT's
// rcNormalPosition is in workspace coordinates (offset by the taskbar when
// it sits on the left or the top), so it is only read for a maximised
// window, converted, and never written back: mixing the two moved the
// window by the taskbar's width at every start.

var pGetWindowPlacement = user32.NewProc("GetWindowPlacement")

type point struct{ X, Y int32 }

// windowPlacement is WINDOWPLACEMENT.
type windowPlacement struct {
	Length  uint32
	Flags   uint32
	ShowCmd uint32
	MinPos  point
	MaxPos  point
	Normal  rect // workspace coordinates
}

const (
	swShowMinimized     = 2
	swShowMaximized     = 3 // also SW_MAXIMIZE for ShowWindow
	wpfRestoreToMaximum = 2
)

// mainWinState is the last placement (%LOCALAPPDATA%\PalaTerm\main-window.json):
// the restored window's rectangle in screen coordinates (physical pixels).
type mainWinState struct {
	L, T, R, B int32
	Max        bool `json:"max,omitempty"`
}

func mainWinStateFile() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "PalaTerm", "main-window.json")
}

// clampInto keeps r inside work: shrunk to it when larger, then moved in.
// Pure, for the tests.
func clampInto(r, work rect) rect {
	w, h := r.R-r.L, r.B-r.T
	if w > work.R-work.L {
		w = work.R - work.L
	}
	if h > work.B-work.T {
		h = work.B - work.T
	}
	x, y := r.L, r.T
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
	return rect{x, y, x + w, y + h}
}

// firstPlace is the window for a first start: the default size (w×h,
// physical pixels) cut to the work area, centred in it. Pure.
func firstPlace(work rect, w, h int32) rect {
	if w > work.R-work.L {
		w = work.R - work.L
	}
	if h > work.B-work.T {
		h = work.B - work.T
	}
	x := work.L + (work.R-work.L-w)/2
	y := work.T + (work.B-work.T-h)/2
	return rect{x, y, x + w, y + h}
}

// workspaceToScreen converts a workspace rectangle on the monitor mi to
// screen coordinates: workspace coordinates start at the work area's
// corner. Pure, for the tests.
func workspaceToScreen(r rect, mi monitorInfo) rect {
	dx, dy := mi.Work.L-mi.Monitor.L, mi.Work.T-mi.Monitor.T
	return rect{r.L + dx, r.T + dy, r.R + dx, r.B + dy}
}

// expandBy is work grown by the window's invisible frame border (win is the
// window, ext its visible part): a window flush with a screen edge has its
// border outside the work area, and clamping it to the bare work area moved
// it inward at every start. Pure, for the tests.
func expandBy(work, win, ext rect) rect {
	if ext.R <= ext.L || ext.B <= ext.T {
		return work
	}
	return rect{work.L - (ext.L - win.L), work.T - (ext.T - win.T), work.R + (win.R - ext.R), work.B + (win.B - ext.B)}
}

// placeMainWindow puts the main window where it belongs (see above). Runs
// in the background from startup, after the synchronous fit that keeps it
// on screen even if this never finds the window.
func placeMainWindow() {
	go func() {
		hwnd := waitOwnWindow()
		if hwnd == 0 {
			return
		}
		// The last place, on a monitor that is still there.
		if b, err := os.ReadFile(mainWinStateFile()); err == nil {
			var st mainWinState
			_ = json.Unmarshal(b, &st)
			if r := (rect{st.L, st.T, st.R, st.B}); r.R-r.L >= 200 && r.B-r.T >= 150 {
				const monitorDefaultToNull = 0
				mon, _, _ := pMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), monitorDefaultToNull)
				if mi, ok := monitorWork(mon); ok {
					placeWindow(hwnd, func(win, ext rect, _ int32) rect { return clampInto(r, expandBy(mi.Work, win, ext)) })
					if st.Max {
						// Maximised on the monitor it now sits on; restoring
						// returns to the rectangle just set.
						pShowWindow.Call(hwnd, swShowMaximized)
					}
					return
				}
			}
		}
		mi, ok := windowMonitor(hwnd)
		if !ok {
			return
		}
		placeWindow(hwnd, func(win, ext rect, dpi int32) rect {
			return frameFit(firstPlace(mi.Work, int32(mainWin.W)*dpi/96, int32(mainWin.H)*dpi/96), win, ext)
		})
	}()
}

// saveMainPlacement records where the main window is now (its restored
// rectangle, and whether it is maximised) for the next start. Called on
// every close request: × may only hide it to the tray, and the process can
// still end later without another chance.
func saveMainPlacement() {
	hwnd := ownTopLevelWindow()
	if hwnd == 0 {
		return
	}
	wp := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{}))}
	if ok, _, _ := pGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp))); ok == 0 {
		return
	}
	max := wp.ShowCmd == swShowMaximized || (wp.ShowCmd == swShowMinimized && wp.Flags&wpfRestoreToMaximum != 0)
	var r rect
	if wp.ShowCmd == swShowMaximized || wp.ShowCmd == swShowMinimized {
		// Not on screen as itself: the restored rectangle, converted.
		mi, ok := windowMonitor(hwnd)
		if !ok {
			return
		}
		r = workspaceToScreen(wp.Normal, mi)
	} else if ok, _, _ := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	if r.R-r.L < 200 || r.B-r.T < 150 {
		return
	}
	b, _ := json.Marshal(mainWinState{L: r.L, T: r.T, R: r.R, B: r.B, Max: max})
	_ = os.MkdirAll(filepath.Dir(mainWinStateFile()), 0o755)
	_ = os.WriteFile(mainWinStateFile(), b, 0o644)
}
