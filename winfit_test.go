package main

import "testing"

func TestFitSize(t *testing.T) {
	cases := []struct {
		name   string
		sw, sh int
		want   winSize
		w, h   int
	}{
		{"広い画面はそのまま", 1920, 1080, mainWin, 1280, 800},
		{"125% のノート（1536×864）は高さだけ縮む", 1536, 864, mainWin, 1280, 768},
		{"1366×768 は高さだけ縮む", 1366, 768, mainWin, 1280, 672},
		{"1280×720（150%）も画面に収まる", 1280, 720, mainWin, 1256, 624},
		{"差分窓も同じ規則", 1366, 768, diffWin, 1320, 672},
		{"画面不明（0）は既定", 0, 0, termWin, 1080, 680},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := fitSize(c.sw, c.sh, c.want)
			if w != c.w || h != c.h {
				t.Fatalf("fitSize(%d, %d, %+v) = %d×%d, want %d×%d", c.sw, c.sh, c.want, w, h, c.w, c.h)
			}
		})
	}
}

// The main window's place is kept inside the work area: a laptop's taskbar
// no longer hides its bottom edge (2026-10-21).
func TestMainPlacement(t *testing.T) {
	work := rect{0, 0, 1920, 1032} // 1920×1080 with a 48-px taskbar
	if r := firstPlace(work, 1920, 1200); r != (rect{0, 0, 1920, 1032}) {
		t.Errorf("150%% の既定サイズは作業領域いっぱい: %+v", r)
	}
	if r := firstPlace(work, 1280, 800); r != (rect{320, 116, 1600, 916}) {
		t.Errorf("収まる既定サイズは中央: %+v", r)
	}
	if r := clampInto(rect{1000, 600, 2000, 1400}, work); r != (rect{920, 232, 1920, 1032}) {
		t.Errorf("はみ出した位置は作業領域へ戻す: %+v", r)
	}
	if r := clampInto(rect{-50, -20, 2100, 1300}, work); r != work {
		t.Errorf("大きすぎる窓は作業領域に縮める: %+v", r)
	}
	second := rect{1920, 0, 3840, 1040}
	if r := clampInto(rect{2000, 100, 3000, 900}, second); r != (rect{2000, 100, 3000, 900}) {
		t.Errorf("2 台目のモニターの位置はそのまま: %+v", r)
	}
}

// The restored rectangle of a maximised window comes in workspace
// coordinates: with the taskbar on the left or the top they are offset from
// the screen's, and must be converted before the screen rectangle is saved
// (mixing them moved the window by the taskbar's width at every start).
func TestWorkspaceToScreen(t *testing.T) {
	left := monitorInfo{Monitor: rect{0, 0, 1920, 1080}, Work: rect{62, 0, 1920, 1080}}
	if r := workspaceToScreen(rect{0, 100, 1000, 700}, left); r != (rect{62, 100, 1062, 700}) {
		t.Errorf("左のタスクバー: %+v", r)
	}
	top := monitorInfo{Monitor: rect{1920, 0, 3840, 1080}, Work: rect{1920, 48, 3840, 1080}}
	if r := workspaceToScreen(rect{1920, 0, 2920, 600}, top); r != (rect{1920, 48, 2920, 648}) {
		t.Errorf("上のタスクバー（2 台目）: %+v", r)
	}
	bottom := monitorInfo{Monitor: rect{0, 0, 1920, 1080}, Work: rect{0, 0, 1920, 1032}}
	if r := workspaceToScreen(rect{10, 20, 30, 40}, bottom); r != (rect{10, 20, 30, 40}) {
		t.Errorf("下のタスクバーはそのまま: %+v", r)
	}
	// Saved in screen coordinates and restored with clampInto on the same
	// work area: the window stays where it was, start after start.
	r := rect{62, 100, 1062, 700}
	for i := 0; i < 3; i++ {
		r = clampInto(r, left.Work)
	}
	if r != (rect{62, 100, 1062, 700}) {
		t.Errorf("起動を繰り返すとずれる: %+v", r)
	}
}

// A window flush with the screen edge has its invisible border outside the
// work area: restoring it keeps it there instead of moving it inward.
func TestExpandByBorder(t *testing.T) {
	work := rect{0, 0, 1920, 1032}
	win := rect{-7, 0, 967, 1039} // flush left and bottom, 7-px borders
	ext := rect{0, 0, 960, 1032}
	if r := clampInto(win, expandBy(work, win, ext)); r != win {
		t.Errorf("端に付けた窓が内側へずれる: %+v", r)
	}
	if r := expandBy(work, win, rect{}); r != work {
		t.Errorf("DWM が読めないときは作業領域そのまま: %+v", r)
	}
}
