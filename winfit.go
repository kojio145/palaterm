package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// winSize is a window's default size and the floor it may shrink to; one per
// window kind, shared by the wails.Run options and the startup fit below so
// the two never disagree.
type winSize struct{ W, H, MinW, MinH int }

var (
	// One notch below the earlier 1440×960: that overflowed a laptop screen
	// at 125 % scaling (1536×864 logical), the common case in the office.
	// Still above MinWidth so nothing in the tables wraps.
	mainWin   = winSize{W: 1280, H: 800, MinW: 1100, MinH: 680}
	termWin   = winSize{W: 1080, H: 680, MinW: 640, MinH: 400}
	pasteWin  = winSize{W: 980, H: 640, MinW: 480, MinH: 320}
	viewerWin = winSize{W: 1080, H: 700, MinW: 480, MinH: 320}
	diffWin   = winSize{W: 1320, H: 760, MinW: 720, MinH: 400}
)

// Room left for the taskbar and the window frame: Wails reports the whole
// screen, not the work area, and a window that exactly fills it still hides
// its bottom edge behind the taskbar.
const (
	screenMarginW = 24
	screenMarginH = 96
)

// fitSize is the pure part of fit: the size a window of want should open at
// on a screen of sw×sh logical pixels. Smaller screens get the default
// shrunk to what fits (never below the minimum); larger ones the default.
func fitSize(sw, sh int, want winSize) (int, int) {
	w, h := want.W, want.H
	if sw > 0 && w > sw-screenMarginW {
		w = sw - screenMarginW
	}
	if sh > 0 && h > sh-screenMarginH {
		h = sh - screenMarginH
	}
	if w < want.MinW {
		w = want.MinW
	}
	if h < want.MinH {
		h = want.MinH
	}
	return w, h
}

// fit shrinks the window to the screen it opened on when the default does
// not fit there, then recentres it. Called from each window's startup. The
// screen is the one the window is on (IsCurrent), else the primary one; on
// any failure to read the screens the default stays.
func (s winSize) fit(ctx context.Context) {
	screens, err := runtime.ScreenGetAll(ctx)
	if err != nil || len(screens) == 0 {
		return
	}
	pick := -1
	for i, sc := range screens {
		if sc.IsCurrent {
			pick = i
			break
		}
	}
	if pick < 0 {
		for i, sc := range screens {
			if sc.IsPrimary {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		pick = 0
	}
	sw, sh := screens[pick].Size.Width, screens[pick].Size.Height
	w, h := fitSize(sw, sh, s)
	if w == s.W && h == s.H {
		return
	}
	runtime.WindowSetSize(ctx, w, h)
	runtime.WindowCenter(ctx)
}
