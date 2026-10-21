package main

import (
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// 並べて接続 and 同時入力 (2026-10-21, field request after Tera Term's
// ［ウィンドウ］-［左右に並べて表示］ and ［コントロール］-［ブロードキャストコマンド］
// in real-time mode).
//
// 並べて接続 opens the selected devices' terminal windows at once, laid out
// as a grid over the main window's monitor (with 同時入力 on if the 設定 tab
// says so; off by default). 同時入力 sends what is typed in
// one terminal window to every other window that has it on. Each terminal
// window is its own process; the main window already holds a pipe to each
// (stdin down, stdout up) and relays between them:
//
//	child → main  "PALATERM bcast <base64>"        keys typed with 同時入力 on
//	child → main  "PALATERM bcstate <join> <ready>" 同時入力 on/off, and whether
//	                                                its session takes keys now
//	child → main  "PALATERM tile <l,t,r,b>"         並べ直す, with its monitor's work area
//	main → child  "INPUT <base64>"                  keys typed in another window
//	main → child  "BCN <joined> <ready>"            how many windows take part
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
	seq   int  // the order it was opened in (並べ直す keeps it)
	join  bool // 同時入力 on
	ready bool // connected and unlocked: INPUT reaches its device
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
			consume(s)
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

// sendBcastCounts tells every window the current counts.
func (a *App) sendBcastCounts() {
	a.termsMu.Lock()
	defer a.termsMu.Unlock()
	j, r := bcastCounts(a.terms)
	line := termLineBcN + " " + strconv.Itoa(j) + " " + strconv.Itoa(r)
	for _, c := range a.terms {
		c.put(line)
	}
}

// retile is 並べ直す: the open terminal windows (up to maxTiled), in the
// order they were opened, laid out over work (the monitor of the window
// that asked).
func (a *App) retile(work rect) {
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
