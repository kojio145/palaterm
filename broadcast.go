package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// 並べて接続 and 同時入力 (2026-10-21, field request after Tera Term's
// ［ウィンドウ］-［左右に並べて表示］ and ［コントロール］-［ブロードキャストコマンド］
// in real-time mode).
//
// 並べて接続 opens the selected devices' terminal windows at once, laid out
// as a grid over the main window's monitor (with 同時入力 on if the 設定 tab
// says so; off by default) — or, with the 設定 tab's メイン窓の場所を空ける
// (Settings.TileKeepMain), over another monitor or beside a strip kept for
// the main window (tilePlan). 同時入力 sends what is typed in
// one terminal window to every other window that has it on. Each terminal
// window is its own process; the main window already holds a pipe to each
// (stdin down, stdout up) and relays between them:
//
//	child → main  "PALATERM bcast <base64>"        keys typed with 同時入力 on
//	child → main  "PALATERM bcstate <join> <ready>" 同時入力 on/off, and whether
//	                                                its session takes keys now
//	child → main  "PALATERM tile <l,t,r,b>"         並べ直す, with its monitor's work area
//	main → child  "INPUT <base64>"                  keys typed in another window
//	main → child  "BCNAMES <json array>"            which windows take part (device names,
//	                                                oldest first), sent just before each BCN
//	main → child  "BCN <joined> <ready>"            how many windows take part
//	main → child  "BCOFF"                           turn 同時入力 off (LeaveAllBroadcast)
//	main → child  "PLACE <l,t,r,b>"                 where to sit (physical pixels)
//
// A window takes INPUT only while 同時入力 is on there and its session is
// connected and unlocked; one still logging in, disconnected or locked
// receives nothing, and the counts say so ("3 / 4 台").

const (
	termEventBcast   = "PALATERM bcast"
	termEventBcState = "PALATERM bcstate"
	termEventTile    = "PALATERM tile"
	termLineInput    = "INPUT"
	termLineBcN      = "BCN"
	termLineBcNames  = "BCNAMES"
	termLineBcOff    = "BCOFF"
	termLinePlace    = "PLACE"

	// tileEnv / bcastEnv start a terminal window at its grid cell, and with
	// 同時入力 on (並べて接続; bcastEnv only with Settings.TiledBroadcast).
	tileEnv  = "PALATERM_TILE"
	bcastEnv = "PALATERM_BCAST"

	// maxTiled caps 並べて接続: beyond 16 a window on a laptop screen is too
	// small to read.
	maxTiled = 16
	// bcastChunk keeps one relayed line well under the pipes' line limit (a
	// pasted config can be large).
	bcastChunk = 24 * 1024
)

// termChild is an open terminal window as the main window sees it. Lines
// to it go through out, never straight to the pipe: a window whose device
// is slow (a 9600-baud console taking a pasted config) stops reading for a
// while, and a direct write would hold up the main window — and, under
// termsMu, every other window — until it read again.
type termChild struct {
	out   *lineQueue
	seq   int    // the order it was opened in (並べ直す keeps it)
	name  string // its device's name (the BCN list, JoinedTerminals)
	join  bool   // 同時入力 on
	ready bool   // connected and unlocked: INPUT reaches its device
}

func newTermChild(in io.WriteCloser, seq int) *termChild {
	return &termChild{seq: seq, out: newLineQueue(func(s string) { _, _ = io.WriteString(in, s+"\n") })}
}

// put queues one line for the window (it never blocks).
func (c *termChild) put(line string) { c.out.put(line) }

// lineQueue hands lines, in order, to consume on a goroutine of its own:
// put never waits for the consumer. Unbounded, as what is queued is what
// the user typed or pasted.
type lineQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	q      []string
	closed bool
}

func newLineQueue(consume func(string)) *lineQueue {
	l := &lineQueue{}
	l.cond = sync.NewCond(&l.mu)
	go func() {
		for {
			l.mu.Lock()
			for len(l.q) == 0 && !l.closed {
				l.cond.Wait()
			}
			if len(l.q) == 0 {
				l.mu.Unlock()
				return
			}
			s := l.q[0]
			l.q[0] = ""
			l.q = l.q[1:]
			l.mu.Unlock()
			// A panic in one line's consumer is logged and the queue goes
			// on with the next (the process, and every window, lives).
			func() {
				defer recoverGo("lineQueue")
				consume(s)
			}()
		}
	}()
	return l
}

func (l *lineQueue) put(s string) {
	l.mu.Lock()
	if !l.closed {
		l.q = append(l.q, s)
		l.cond.Signal()
	}
	l.mu.Unlock()
}

// close lets the goroutine end once what is queued has been consumed.
func (l *lineQueue) close() {
	l.mu.Lock()
	l.closed = true
	l.cond.Signal()
	l.mu.Unlock()
}

// tileRects lays n windows out over work: the column count that gives each
// window the most room for a terminal (wider than high), rows filled left
// to right, and a short last row stretched to the full width. Pure, for the
// tests.
func tileRects(work rect, n int) []rect {
	if n <= 0 {
		return nil
	}
	W, H := work.R-work.L, work.B-work.T
	const aspect = 1.5 // a terminal's useful width : height
	cols, best := 1, -1.0
	for c := 1; c <= n; c++ {
		r := (n + c - 1) / c
		cw, ch := float64(W)/float64(c), float64(H)/float64(r)
		score := ch
		if cw/aspect < score {
			score = cw / aspect
		}
		if score > best+0.5 {
			cols, best = c, score
		}
	}
	rows := (n + cols - 1) / cols
	out := make([]rect, 0, n)
	for i := 0; i < n; i++ {
		row, col := i/cols, i%cols
		inRow := cols
		if row == rows-1 {
			inRow = n - row*cols
		}
		l := work.L + W*int32(col)/int32(inRow)
		r := work.L + W*int32(col+1)/int32(inRow)
		t := work.T + H*int32(row)/int32(rows)
		b := work.T + H*int32(row+1)/int32(rows)
		out = append(out, rect{l, t, r, b})
	}
	return out
}

func rectArg(r rect) string { return fmt.Sprintf("%d,%d,%d,%d", r.L, r.T, r.R, r.B) }

// mainWorkArea is the work area of the monitor the main window is on.
func mainWorkArea() (rect, bool) {
	mi, ok := windowMonitor(ownTopLevelWindow())
	return mi.Work, ok
}

// tilePlan is where the grid goes with メイン窓の場所を空ける on (M-12,
// 2026-11-01: on a laptop screen three tiled windows covered the main
// window, and the run tab had to be found with Alt+Tab). work is the
// monitor the grid was asked for, mainWin the main window's rectangle,
// monitors the work areas of every monitor, minMain the least width to keep
// for the main window (physical pixels: its minimum width at the screen's
// scaling). The main window not on work (並べ直す pressed on another
// screen): nothing to keep, the grid fills work. Another monitor: the
// grid goes to the largest one, and the main window stays. One screen: a
// strip on the right, the main window's width (at least minMain, at most
// half the screen), is left and the main window is moved into it
// (moveMain, mainTo). Pure, for the tests.
func tilePlan(work, mainWin rect, monitors []rect, minMain int32) (tile rect, mainTo rect, moveMain bool) {
	cx, cy := (mainWin.L+mainWin.R)/2, (mainWin.T+mainWin.B)/2
	if mainWin.R <= mainWin.L || cx < work.L || cx >= work.R || cy < work.T || cy >= work.B {
		return work, rect{}, false
	}
	var other rect
	var best int64
	for _, m := range monitors {
		if m == work || m.R <= m.L || m.B <= m.T {
			continue
		}
		if area := int64(m.R-m.L) * int64(m.B-m.T); area > best {
			other, best = m, area
		}
	}
	if best > 0 {
		return other, rect{}, false
	}
	strip := mainWin.R - mainWin.L
	if strip < minMain {
		strip = minMain
	}
	if half := (work.R - work.L) / 2; strip > half {
		strip = half
	}
	tile = rect{work.L, work.T, work.R - strip, work.B}
	mainTo = rect{work.R - strip, work.T, work.R, work.B}
	return tile, mainTo, true
}

// tileKeepMain is the 設定 tab's メイン窓の場所を空ける (false while locked).
func (a *App) tileKeepMain() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inv != nil && a.inv.Settings.TileKeepMain
}

// tileArea is the area 並べて接続 / 並べ直す lays the grid over: work as
// asked, or with メイン窓の場所を空ける what tilePlan picks — moving the main
// window into its strip (in the background: placing takes a moment) when
// that is the plan.
func (a *App) tileArea(work rect) rect {
	if !a.tileKeepMain() {
		return work
	}
	hwnd := ownTopLevelWindow()
	if hwnd == 0 {
		return work
	}
	var mw rect
	if ok, _, _ := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&mw))); ok == 0 {
		return work
	}
	tile, mainTo, move := tilePlan(work, mw, allMonitorsWork(), int32(mainWin.MinW)*windowDPI(hwnd)/96)
	if move {
		go a.placeMain(hwnd, mainTo)
	}
	return tile
}

// placeMain moves the main window onto cell (its strip beside the grid),
// restored first if it was maximised: a maximised window ignores a move.
func (a *App) placeMain(hwnd uintptr, cell rect) {
	defer recoverGo("placeMain")
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil && (runtime.WindowIsMaximised(ctx) || runtime.WindowIsMinimised(ctx)) {
		runtime.WindowUnmaximise(ctx)
		runtime.WindowUnminimise(ctx)
	}
	placeWindow(hwnd, func(win, ext rect, _ int32) rect { return frameFit(cell, win, ext) })
}

// TiledReport says what 並べて接続 opened, and which devices it could not.
type TiledReport struct {
	Opened  int      `json:"opened"`
	Skipped []string `json:"skipped,omitempty"` // "name: reason"
}

// SpawnTiled is 並べて接続: the devices' terminal windows, opened at once in
// a grid over the main window's monitor, with 同時入力 on only when the 設定
// tab says so (Settings.TiledBroadcast, off by default). Devices that
// cannot open a terminal (GUI only, unknown) are left out of the grid and
// named in the report. direct leaves the jump hosts out (the run tab's
// 踏み台: 使わない), as its 接続 buttons do.
func (a *App) SpawnTiled(ids []string, direct bool) (TiledReport, error) {
	var rep TiledReport
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return rep, fmt.Errorf("vault is locked")
	}
	bcastOn := a.inv.Settings.TiledBroadcast
	var ok []string
	for _, id := range ids {
		i := a.inv.DeviceIndex(id)
		switch {
		case i < 0:
			rep.Skipped = append(rep.Skipped, "（削除された機器）: 機器が見つかりません")
		case a.inv.Devices[i].CLIOff:
			rep.Skipped = append(rep.Skipped, a.inv.Devices[i].Name+": CLI を使わない機器です")
		default:
			ok = append(ok, id)
		}
	}
	a.mu.Unlock()
	if len(ok) == 0 {
		return rep, fmt.Errorf("接続できる機器がありません（GUI だけの機器は対象外です）")
	}
	if len(ok) > maxTiled {
		return rep, fmt.Errorf("並べて接続できるのは %d 台までです（%d 台選択中）", maxTiled, len(ok))
	}
	work, have := mainWorkArea()
	var cells []rect
	if have {
		work = a.tileArea(work)
		cells = tileRects(work, len(ok))
	}
	var opened []*termChild
	for i, id := range ok {
		var extra []string
		if cells != nil {
			extra = append(extra, tileEnv+"="+rectArg(cells[i]))
		}
		if bcastOn {
			extra = append(extra, bcastEnv+"=1")
		}
		c, err := a.spawnTerminal(id, false, direct, extra)
		if err != nil {
			rep.Skipped = append(rep.Skipped, a.deviceName(id)+": "+err.Error())
			continue
		}
		opened = append(opened, c)
		rep.Opened++
	}
	// Some could not open: lay out the ones that did, without holes.
	if have && len(opened) > 0 && len(opened) < len(ok) {
		for i, r := range tileRects(work, len(opened)) {
			opened[i].put(termLinePlace + " " + rectArg(r))
		}
	}
	return rep, nil
}

// MaxTiled is how many devices 並べて接続 opens at most (the page checks it
// before asking).
func (a *App) MaxTiled() int { return maxTiled }

func (a *App) deviceName(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv != nil {
		if i := a.inv.DeviceIndex(id); i >= 0 {
			return a.inv.Devices[i].Name
		}
	}
	return id
}

// termLine handles one line a terminal window printed (see the top).
func (a *App) termLine(cmd *exec.Cmd, line string) {
	switch {
	case strings.HasPrefix(line, termEventClearHostKey+" "):
		_ = a.ClearHostKeys(strings.TrimSpace(strings.TrimPrefix(line, termEventClearHostKey+" ")))
	case strings.HasPrefix(line, termEventBcast+" "):
		data := strings.TrimSpace(strings.TrimPrefix(line, termEventBcast+" "))
		a.relayInput(cmd, data)
	case strings.HasPrefix(line, termEventBcState+" "):
		f := strings.Fields(strings.TrimPrefix(line, termEventBcState+" "))
		if len(f) != 2 {
			return
		}
		a.termsMu.Lock()
		if c := a.terms[cmd]; c != nil {
			c.join, c.ready = f[0] == "1", f[1] == "1"
		}
		a.termsMu.Unlock()
		a.sendBcastCounts()
	case strings.HasPrefix(line, termEventTile+" "):
		if work, ok := parseRect(strings.TrimPrefix(line, termEventTile+" ")); ok {
			a.retile(work)
		}
	}
}

// relayInput sends keys typed in from to every other window taking part.
// from must have 同時入力 on itself: a window that just turned it off may
// still have a line in flight. The data goes on as it came (base64, one
// field); the receiving window decodes and checks it.
func (a *App) relayInput(from *exec.Cmd, data string) {
	if data == "" || strings.ContainsAny(data, " \t\r\n") {
		return
	}
	a.termsMu.Lock()
	defer a.termsMu.Unlock()
	if c := a.terms[from]; c == nil || !c.join {
		return
	}
	for cmd, c := range a.terms {
		if cmd != from && c.join && c.ready {
			c.put(termLineInput + " " + data)
		}
	}
}

// bcastCounts is how many windows have 同時入力 on, and how many of them
// take keys now.
func bcastCounts(terms map[*exec.Cmd]*termChild) (joined, ready int) {
	for _, c := range terms {
		if c.join {
			joined++
			if c.ready {
				ready++
			}
		}
	}
	return joined, ready
}

// bcastNames lists the windows with 同時入力 on by device name, in the
// order they were opened (caller holds termsMu).
func bcastNames(terms map[*exec.Cmd]*termChild) []string {
	list := make([]*termChild, 0, len(terms))
	for _, c := range terms {
		if c.join {
			list = append(list, c)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].seq < list[j].seq })
	names := make([]string, 0, len(list))
	for _, c := range list {
		names = append(names, c.name)
	}
	return names
}

// sendBcastCounts tells every window the current counts, and which windows
// they are: the names go first (BCNAMES, a JSON array — a name may hold a
// space), kept quietly by the window, and the BCN line behind them is what
// it reports to its page, names included.
func (a *App) sendBcastCounts() {
	a.termsMu.Lock()
	defer a.termsMu.Unlock()
	j, r := bcastCounts(a.terms)
	names, _ := json.Marshal(bcastNames(a.terms))
	namesLine := termLineBcNames + " " + string(names)
	line := termLineBcN + " " + strconv.Itoa(j) + " " + strconv.Itoa(r)
	for _, c := range a.terms {
		c.put(namesLine)
		c.put(line)
	}
}

// JoinedTerminals names the terminal windows with 同時入力 on, oldest first
// (the question before 並べて接続 opens more with it on). Nothing while
// locked: device names are the vault's.
func (a *App) JoinedTerminals() []string {
	if a.locked() {
		return []string{}
	}
	a.termsMu.Lock()
	defer a.termsMu.Unlock()
	return bcastNames(a.terms)
}

// LeaveAllBroadcast turns 同時入力 off in every open terminal window (each
// reports back, and the counts follow).
func (a *App) LeaveAllBroadcast() error {
	if a.locked() {
		return errVaultLocked
	}
	a.termsMu.Lock()
	defer a.termsMu.Unlock()
	for _, c := range a.terms {
		if c.join {
			c.put(termLineBcOff)
		}
	}
	return nil
}

// retile is 並べ直す: the open terminal windows (up to maxTiled), in the
// order they were opened, laid out over work (the monitor of the window
// that asked; see tileArea).
func (a *App) retile(work rect) {
	work = a.tileArea(work)
	a.termsMu.Lock()
	defer a.termsMu.Unlock()
	list := make([]*termChild, 0, len(a.terms))
	for _, c := range a.terms {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].seq < list[j].seq })
	// The first maxTiled opened: past that the cells are smaller than a
	// window can be. The others stay where they are.
	if len(list) > maxTiled {
		list = list[:maxTiled]
	}
	for i, r := range tileRects(work, len(list)) {
		list[i].put(termLinePlace + " " + rectArg(r))
	}
}
