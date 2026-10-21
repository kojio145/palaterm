package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The terminal window's side of 並べて接続 and 同時入力 (see broadcast.go
// for the main window's side and the lines exchanged).

var (
	dwmapi                 = syscall.NewLazyDLL("dwmapi.dll")
	pDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
	childOutMu             sync.Mutex
)

// A tiled window's minimum size (logical pixels): low enough for the
// toolbar to stay usable, and never above its grid cell (tiledMin), so 16
// windows on a laptop at 150 % do not overlap.
const (
	tiledMinW, tiledMinH = 360, 220
	tiledFloorW          = 160
	tiledFloorH          = 110
)

// tiledMin is the minimum size for a window in a cell of cw×ch physical
// pixels at dpi: the usual tiled minimum, brought down to the cell (with a
// floor). Pure, for the tests.
func tiledMin(cw, ch, dpi int32) (int, int) {
	if dpi <= 0 {
		dpi = 96
	}
	w, h := int(cw*96/dpi), int(ch*96/dpi)
	w, h = min(w, tiledMinW), min(h, tiledMinH)
	return max(w, tiledFloorW), max(h, tiledFloorH)
}

// childOut is where childLine writes: the pipe to the main window (the
// tests swap it).
var childOut io.Writer = os.Stdout

// childLine prints one line to the main window (stdout), whole.
func childLine(s string) {
	childOutMu.Lock()
	defer childOutMu.Unlock()
	fmt.Fprintln(childOut, s)
}

// bcastState is what the window last told the main window.
type bcastState struct {
	join, ready    bool
	joined, readyN int // the counts from the main window (BCN)
	reported       string
}

// bcastEvent is term:bcast for the page.
type bcastEvent struct {
	Join   bool `json:"join"`
	Ready  bool `json:"ready"`
	Joined int  `json:"joined"` // windows with 同時入力 on (this one included)
	ReadyN int  `json:"readyN"` // of those, the ones that take keys now
}

// bcastReport tells the main window (and the page) this window's state when
// it changed: 同時入力 on/off, and whether its session takes keys (connected,
// unlocked). Called wherever one of those changes: startup, attach and its
// close, Lock, Unlock and SetBroadcast. Not with t.mu held.
func (t *Term) bcastReport() {
	t.mu.Lock()
	t.bc.ready = t.send != nil && !t.locked
	st := "0"
	if t.bc.join {
		st = "1"
	}
	if t.bc.ready {
		st += " 1"
	} else {
		st += " 0"
	}
	changed := st != t.bc.reported
	t.bc.reported = st
	ev := t.bcEventLocked()
	t.mu.Unlock()
	if changed {
		childLine(termEventBcState + " " + st)
		if t.ctx != nil {
			runtime.EventsEmit(t.ctx, "term:bcast", ev)
		}
	}
}

func (t *Term) bcEventLocked() bcastEvent {
	return bcastEvent{Join: t.bc.join, Ready: t.bc.ready, Joined: t.bc.joined, ReadyN: t.bc.readyN}
}

// SetBroadcast turns 同時入力 on or off for this window.
func (t *Term) SetBroadcast(on bool) bcastEvent {
	t.mu.Lock()
	t.bc.join = on
	t.mu.Unlock()
	t.bcastReport()
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.bcEventLocked()
}

// BroadcastState is the current state, for the page at start.
func (t *Term) BroadcastState() bcastEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.bcEventLocked()
}

// bcastOut hands text typed here to the main window for the other windows,
// when 同時入力 is on.
func (t *Term) bcastOut(s string) {
	t.mu.Lock()
	on := t.bc.join
	t.mu.Unlock()
	if !on {
		return
	}
	for len(s) > 0 {
		n := len(s)
		if n > bcastChunk {
			n = bcastChunk
		}
		childLine(termEventBcast + " " + base64.StdEncoding.EncodeToString([]byte(s[:n])))
		s = s[n:]
	}
}

// bcastIn is INPUT from the main window: keys typed in another window. It
// only queues them (inQ, see bcastSend): sending can take long on a slow
// line, and the stdin reader must stay free for LOCK, CLOSE and the rest.
func (t *Term) bcastIn(data string) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) == 0 {
		return
	}
	t.inQ.put(string(raw))
}

// bcastSend sends one queued INPUT to this device, only while 同時入力 is on
// here and the session takes keys (checked when it is sent, not queued).
// It counts as use of this window: its own idle lock would otherwise lock
// every window but the one typed in, and they would drop out of 同時入力
// in the middle of the work.
func (t *Term) bcastSend(s string) {
	t.mu.Lock()
	send, ok, ctx := t.send, t.bc.join && !t.locked, t.ctx
	t.mu.Unlock()
	if ok && send != nil {
		_ = send(s)
		if ctx != nil {
			runtime.EventsEmit(ctx, "term:remote-input")
		}
	}
}

// bcastCounts is BCN from the main window.
func (t *Term) bcastCounts(arg string) {
	f := strings.Fields(arg)
	if len(f) != 2 {
		return
	}
	j, _ := strconv.Atoi(f[0])
	r, _ := strconv.Atoi(f[1])
	t.mu.Lock()
	t.bc.joined, t.bc.readyN = j, r
	ev := t.bcEventLocked()
	t.mu.Unlock()
	if t.ctx != nil {
		runtime.EventsEmit(t.ctx, "term:bcast", ev)
	}
}

// Tile is 並べ直す: every open terminal window laid out over this window's
// monitor (the main window does the layout).
func (t *Term) Tile() error {
	hwnd := ownTopLevelWindow()
	if hwnd == 0 {
		return fmt.Errorf("ウィンドウが見つかりません")
	}
	mi, ok := windowMonitor(hwnd)
	if !ok {
		return fmt.Errorf("画面の大きさを読めません")
	}
	childLine(termEventTile + " " + rectArg(mi.Work))
	return nil
}

// frameFit is the window rectangle that makes the visible frame cover cell:
// Windows 10/11 frames carry an invisible resize border (ext is the visible
// part, win the whole window), which left gaps between windows laid side by
// side. Pure, for the tests.
func frameFit(cell, win, ext rect) rect {
	if ext.R <= ext.L || ext.B <= ext.T {
		return cell
	}
	return rect{
		L: cell.L - (ext.L - win.L),
		T: cell.T - (ext.T - win.T),
		R: cell.R + (win.R - ext.R),
		B: cell.B + (win.B - ext.B),
	}
}

// placeAt moves this window onto cell (physical pixels): its grid place
// (並べて接続 / 並べ直す). Queued: places are made one at a time, in the
// order they came (a 並べ直す can arrive while the window is still placing
// itself), each once the window and its ctx exist.
func (t *Term) placeAt(cell rect) { t.placeQ.put(rectArg(cell)) }

// doPlace is one queued placeAt. The minimum size comes down so the cell
// fits, and the size is no longer remembered for the next window.
func (t *Term) doPlace(arg string) {
	cell, ok := parseRect(arg)
	if !ok {
		return
	}
	var ctx context.Context
	for i := 0; i < 250; i++ { // startup sets ctx; wait for it (10 s at most)
		t.mu.Lock()
		ctx = t.ctx
		t.mu.Unlock()
		if ctx != nil {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if ctx == nil {
		return
	}
	t.mu.Lock()
	t.tiled = true
	t.mu.Unlock()
	if runtime.WindowIsMaximised(ctx) || runtime.WindowIsMinimised(ctx) {
		runtime.WindowUnmaximise(ctx)
		runtime.WindowUnminimise(ctx)
	}
	hwnd := waitOwnWindow()
	if hwnd == 0 {
		return
	}
	placeWindow(hwnd, func(win, ext rect, dpi int32) rect {
		// The minimum for the scaling of the monitor the cell is on (known
		// once the window is there), set before each move.
		mw, mh := tiledMin(cell.R-cell.L, cell.B-cell.T, dpi)
		runtime.WindowSetMinSize(ctx, mw, mh)
		return frameFit(cell, win, ext)
	})
}

// bcastPeers is how many other windows take what is typed here now (0 when
// 同時入力 is off here).
func (t *Term) bcastPeers() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.bc.join {
		return 0
	}
	n := t.bc.readyN
	if t.bc.ready {
		n--
	}
	if n < 0 {
		n = 0
	}
	return n
}
