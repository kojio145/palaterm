package main

import (
	"strings"
	"testing"
)

// M-12 メイン窓の場所を空ける: where the grid goes (tilePlan).
func TestReview1101U2TilePlan(t *testing.T) {
	work := rect{0, 0, 1920, 1040}
	main := rect{300, 200, 1100, 900} // 800 wide, on work
	const minMain = 480

	// One screen: a strip on the right, the main window's width, the grid
	// beside it and the main window moved into the strip at full height.
	tile, to, move := tilePlan(work, main, []rect{work}, minMain)
	if !move {
		t.Fatal("1 画面: メイン窓を動かさない")
	}
	if tile != (rect{0, 0, 1120, 1040}) || to != (rect{1120, 0, 1920, 1040}) {
		t.Errorf("1 画面: tile=%+v to=%+v", tile, to)
	}
	// A narrow main window still gets its minimum width.
	tile, to, _ = tilePlan(work, rect{0, 0, 300, 400}, []rect{work}, minMain)
	if to.R-to.L != minMain || tile.R != to.L {
		t.Errorf("最小幅: tile=%+v to=%+v", tile, to)
	}
	// A very wide one is cut to half the screen so the grid keeps room.
	tile, to, _ = tilePlan(work, rect{0, 0, 1800, 400}, []rect{work}, minMain)
	if to.R-to.L != 960 || tile.R-tile.L != 960 {
		t.Errorf("半分まで: tile=%+v to=%+v", tile, to)
	}
	// No monitor list at all (enumeration failed) behaves as one screen.
	if _, _, move := tilePlan(work, main, nil, minMain); !move {
		t.Error("モニター不明: 1 画面として扱う")
	}

	// Two monitors: the grid goes to the other one, the largest of them,
	// and the main window stays.
	left := rect{-1600, 0, 0, 860}
	right := rect{1920, 0, 4480, 1400}
	tile, _, move = tilePlan(work, main, []rect{left, work, right}, minMain)
	if move || tile != right {
		t.Errorf("2 画面: tile=%+v move=%v", tile, move)
	}
	tile, _, move = tilePlan(work, main, []rect{work, left}, minMain)
	if move || tile != left {
		t.Errorf("左の画面: tile=%+v move=%v", tile, move)
	}

	// 並べ直す pressed on a screen the main window is not on: nothing to
	// keep, the grid fills that screen.
	tile, _, move = tilePlan(left, main, []rect{left, work}, minMain)
	if move || tile != left {
		t.Errorf("別画面の並べ直す: tile=%+v move=%v", tile, move)
	}
	// An unknown main window rectangle: as before.
	tile, _, move = tilePlan(work, rect{}, []rect{work, left}, minMain)
	if move || tile != work {
		t.Errorf("メイン窓不明: tile=%+v move=%v", tile, move)
	}
	// The strip still fits a grid: the cells cover the rest exactly.
	tile, _, _ = tilePlan(work, main, []rect{work}, minMain)
	var area int64
	for _, c := range tileRects(tile, 3) {
		if c.R > tile.R {
			t.Errorf("セル %+v が帯にかかる", c)
		}
		area += int64(c.R-c.L) * int64(c.B-c.T)
	}
	if area != int64(tile.R-tile.L)*int64(tile.B-tile.T) {
		t.Error("セルが帯の左を埋めない")
	}
}

// M-12: the setting is off while locked, and off by default.
func TestReview1101U2TileKeepMainLocked(t *testing.T) {
	a := &App{}
	if a.tileKeepMain() {
		t.Error("ロック中に設定が読めた")
	}
	if a.tileArea(rect{0, 0, 100, 100}) != (rect{0, 0, 100, 100}) {
		t.Error("設定オフで作業領域が変わった")
	}
}

// M-13: what a paste window ended by Lock held is kept for after the
// unlock, and dropped by 破棄 / the window closing.
func TestReview1101U2PasteDraft(t *testing.T) {
	sent := pasteReq{Text: "conf t\nhostname lab-sw1\nend\n", CR: true, Single: false}

	// The window answered with what it held (edited): that is kept.
	tm := &Term{locked: true, pasteSent: sent}
	tm.keepDraft(pasteRes{Draft: true, Text: "conf t\r\nhostname lab-sw1\r\nno shut\r\nend", CR: false}, true)
	if n := tm.PasteDraftLines(); n != 4 {
		t.Errorf("編集後の行数 %d, want 4", n)
	}
	if tm.pasteDraft == nil || tm.pasteDraft.cr {
		t.Error("編集後の Enter の有無が残らない")
	}
	if tm.pasteSent.Text != "" {
		t.Error("渡した本文が残っている")
	}

	// Killed before it could answer: the text it was handed is kept.
	tm = &Term{locked: true, pasteSent: sent}
	tm.keepDraft(pasteRes{}, false)
	if tm.pasteDraft == nil || tm.pasteDraft.text != sent.Text || !tm.pasteDraft.cr {
		t.Errorf("渡した本文が残らない: %+v", tm.pasteDraft)
	}
	if n := tm.PasteDraftLines(); n != 4 { // three lines and the final newline
		t.Errorf("行数 %d, want 4", n)
	}

	// Confirmed in the same instant as the lock: kept, not sent.
	tm = &Term{locked: true, pasteSent: sent}
	tm.keepDraft(pasteRes{OK: true, Text: "show run", CR: true}, true)
	if tm.pasteDraft == nil || tm.pasteDraft.text != "show run" {
		t.Error("ロックと同時の貼り付けが残らない")
	}

	// Cancelled before the lock: nothing to keep.
	tm = &Term{locked: true, pasteSent: sent}
	tm.keepDraft(pasteRes{OK: false}, true)
	if tm.pasteDraft != nil || tm.PasteDraftLines() != 0 {
		t.Error("キャンセルが残った")
	}

	// 破棄, and the window closing, forget it.
	tm = &Term{pasteDraft: &pasteDraft{text: "x"}}
	tm.DropPasteDraft()
	if tm.pasteDraft != nil {
		t.Error("破棄で消えない")
	}
	tm = &Term{pasteDraft: &pasteDraft{text: "x"}}
	tm.shutdown(nil)
	if tm.pasteDraft != nil {
		t.Error("終了で消えない")
	}

	// 再表示 with nothing kept says so; while locked it refuses and keeps
	// the draft for after the unlock.
	tm = &Term{}
	if err := tm.ReopenPasteDraft(); err == nil || !strings.Contains(err.Error(), "もうありません") {
		t.Errorf("空の再表示: %v", err)
	}
	tm = &Term{locked: true, pasteDraft: &pasteDraft{text: "x"}}
	if err := tm.ReopenPasteDraft(); err != errLocked {
		t.Errorf("ロック中の再表示: %v", err)
	}
	if tm.pasteDraft == nil {
		t.Error("ロック中の再表示で内容が消えた")
	}

	// A Draft answer is never a paste.
	if _, ok := pasteText(pasteRes{Draft: true, Text: "show run"}); ok {
		t.Error("Draft が機器へ送られる")
	}
}

// M-13: Lock with no paste window open neither waits nor panics (no ctx,
// no process).
func TestReview1101U2LockWithoutPaste(t *testing.T) {
	tm := &Term{}
	tm.Lock()
	if !tm.isLocked() {
		t.Error("ロックされない")
	}
	// pasteDone with nothing open is harmless, and closes the channel once.
	tm.pasteGone = make(chan struct{})
	tm.pasteDone()
	tm.pasteDone()
	if tm.pasteGone != nil || tm.pasteOpen {
		t.Error("pasteDone の後始末")
	}
}
