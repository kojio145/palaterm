package main

import (
	"bytes"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// 並べて接続: the grid each window count gets on a 1920×1032 work area.
func TestTileRects(t *testing.T) {
	work := rect{0, 0, 1920, 1032}
	shape := func(rs []rect) string {
		rows := map[int32]int{}
		var order []int32
		for _, r := range rs {
			if rows[r.T] == 0 {
				order = append(order, r.T)
			}
			rows[r.T]++
		}
		var b strings.Builder
		for i, top := range order {
			if i > 0 {
				b.WriteString("+")
			}
			b.WriteString(strings.Repeat("x", rows[top]))
		}
		return b.String()
	}
	for n, want := range map[int]string{1: "x", 2: "xx", 3: "xx+x", 4: "xx+xx", 6: "xxx+xxx", 9: "xxx+xxx+xxx", 10: "xxxx+xxxx+xx", 16: "xxxx+xxxx+xxxx+xxxx"} {
		rs := tileRects(work, n)
		if len(rs) != n || shape(rs) != want {
			t.Errorf("%d 台: %s, want %s", n, shape(rs), want)
		}
		// The cells cover the work area exactly: no gap, no overlap.
		var area int64
		for _, r := range rs {
			if r.L < work.L || r.T < work.T || r.R > work.R || r.B > work.B {
				t.Errorf("%d 台: %+v は作業領域の外", n, r)
			}
			area += int64(r.R-r.L) * int64(r.B-r.T)
		}
		if area != int64(work.R-work.L)*int64(work.B-work.T) {
			t.Errorf("%d 台: 面積 %d が作業領域と一致しない", n, area)
		}
	}
	// A second monitor to the left (negative coordinates) works the same.
	if rs := tileRects(rect{-1920, 0, 0, 1040}, 2); rs[0].L != -1920 || rs[1].R != 0 {
		t.Errorf("左のモニター: %+v", rs)
	}
	if tileRects(work, 0) != nil {
		t.Error("0 台で枠ができた")
	}
}

// The invisible resize border is added back so visible frames touch.
func TestFrameFit(t *testing.T) {
	cell := rect{0, 0, 960, 516}
	win := rect{100, 100, 1114, 707}
	ext := rect{107, 100, 1107, 700}
	if r := frameFit(cell, win, ext); r != (rect{-7, 0, 967, 523}) {
		t.Errorf("frameFit = %+v", r)
	}
	if r := frameFit(cell, win, rect{}); r != cell {
		t.Errorf("DWM が読めないときはそのまま: %+v", r)
	}
}

// nopBuf is a child's stdin for the tests: written by the queue's
// goroutine, read by the test.
type nopBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (n *nopBuf) Write(p []byte) (int, error) { n.mu.Lock(); defer n.mu.Unlock(); return n.b.Write(p) }
func (*nopBuf) Close() error                  { return nil }
func (n *nopBuf) String() string              { n.mu.Lock(); defer n.mu.Unlock(); return n.b.String() }
func (n *nopBuf) Reset()                      { n.mu.Lock(); defer n.mu.Unlock(); n.b.Reset() }

// waitFor polls until cond holds (the queues deliver on their own
// goroutines) or a second has passed.
func waitFor(cond func() bool) bool {
	for i := 0; i < 100; i++ {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// 同時入力: keys go to the other windows that have it on and take keys,
// never back to the sender, and not from a window that has it off.
func TestRelayInput(t *testing.T) {
	a := NewApp()
	mk := func(join, ready bool) (*exec.Cmd, *nopBuf) {
		b := &nopBuf{}
		c := &exec.Cmd{}
		ch := newTermChild(b, 0)
		ch.join, ch.ready = join, ready
		a.terms[c] = ch
		return c, b
	}
	from, fromBuf := mk(true, true)
	_, on := mk(true, true)
	_, off := mk(false, true)
	_, loggingIn := mk(true, false)
	a.relayInput(from, "c2hvdyBjbG9jaw0=") // "show clock\r"
	if !waitFor(func() bool { return on.String() == "INPUT c2hvdyBjbG9jaw0=\n" }) {
		t.Errorf("参加中の窓に届かない: %q", on.String())
	}
	time.Sleep(50 * time.Millisecond)
	if fromBuf.String() != "" || off.String() != "" || loggingIn.String() != "" {
		t.Errorf("送るべきでない窓に届いた: from=%q off=%q login=%q", fromBuf.String(), off.String(), loggingIn.String())
	}
	on.Reset()
	quiet, _ := mk(false, true)
	a.relayInput(quiet, "eA==")
	// A line that cannot be one field is not passed on (the receiving
	// window checks the base64 itself).
	a.relayInput(from, "two fields")
	a.relayInput(from, "")
	time.Sleep(50 * time.Millisecond)
	if on.String() != "" {
		t.Errorf("中継されるべきでない行が届いた: %q", on.String())
	}
	if j, r := bcastCounts(a.terms); j != 3 || r != 2 {
		t.Errorf("counts = %d / %d, want 3 / 2", j, r)
	}
	// The counts reach every window.
	a.sendBcastCounts()
	if !waitFor(func() bool {
		return strings.Contains(off.String(), "BCN 3 2\n") && strings.Contains(fromBuf.String(), "BCN 3 2\n")
	}) {
		t.Errorf("BCN が届かない: %q / %q", off.String(), fromBuf.String())
	}
}

// stuckWriter is a window that has stopped reading (a 9600-baud console
// busy with a pasted config): writes block until released.
type stuckWriter struct{ release chan struct{} }

func (w *stuckWriter) Write(p []byte) (int, error) { <-w.release; return len(p), nil }
func (*stuckWriter) Close() error                  { return nil }

// A window that does not read never holds up the main window: relaying,
// the counts and a lock reach the other windows at once (review 2026-10-21:
// the writes were made under termsMu, straight to the pipes).
func TestSlowWindowDoesNotBlock(t *testing.T) {
	a := NewApp()
	stuck := &stuckWriter{release: make(chan struct{})}
	defer close(stuck.release)
	sc := newTermChild(stuck, 1)
	sc.join, sc.ready = true, true
	a.terms[&exec.Cmd{}] = sc
	from := &exec.Cmd{}
	fc := newTermChild(&nopBuf{}, 2)
	fc.join, fc.ready = true, true
	a.terms[from] = fc
	other := &nopBuf{}
	oc := newTermChild(other, 3)
	oc.join, oc.ready = true, true
	a.terms[&exec.Cmd{}] = oc

	done := make(chan struct{})
	go func() {
		chunk := strings.Repeat("A", bcastChunk)
		for i := 0; i < 50; i++ { // ~1.6 MB: far past any pipe buffer
			a.relayInput(from, chunk)
		}
		a.sendBcastCounts()
		a.sendToTerminals(termLockLine)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("読まない窓があると本体が止まる")
	}
	if !waitFor(func() bool { return strings.HasSuffix(other.String(), "LOCK\n") }) {
		t.Errorf("ほかの窓にロックが届かない（%d バイト）", len(other.String()))
	}
	if !waitFor(func() bool { return a.openTerminals() == 3 }) {
		t.Error("窓の一覧が使えない")
	}
}

// The queue keeps the order and hands everything over before it ends.
func TestLineQueueOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string
	q := newLineQueue(func(s string) { time.Sleep(time.Millisecond); mu.Lock(); got = append(got, s); mu.Unlock() })
	for i := 0; i < 20; i++ {
		q.put(strings.Repeat("x", i))
	}
	q.close()
	q.put("after close")
	if !waitFor(func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 20 }) {
		t.Fatalf("got %d lines", len(got))
	}
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for i, s := range got {
		if len(s) != i {
			t.Fatalf("順番が崩れた: %d 番目が %d 文字", i, len(s))
		}
	}
	if len(got) != 20 {
		t.Error("閉じた後の行が届いた")
	}
}

// 並べ直す lays the windows out in the order they were opened.
func TestRetileOrder(t *testing.T) {
	a := NewApp()
	var bufs []*nopBuf
	for i := 3; i >= 1; i-- {
		b := &nopBuf{}
		a.terms[&exec.Cmd{}] = newTermChild(b, i)
		bufs = append(bufs, b)
	}
	a.retile(rect{0, 0, 1000, 600})
	// bufs[2] is seq 1: the first cell.
	if !waitFor(func() bool { return strings.HasPrefix(bufs[2].String(), "PLACE 0,0,") }) {
		t.Errorf("最初に開いた窓が左上に来ない: %q", bufs[2].String())
	}
	for _, b := range bufs {
		if !waitFor(func() bool { return strings.HasPrefix(b.String(), "PLACE ") }) {
			t.Errorf("PLACE が届かない: %q", b.String())
		}
	}
}

// A tiled window's minimum never exceeds its cell, so up to 16 windows do
// not overlap on a laptop (review 2026-10-21: 360×220 was larger than a
// cell of 12–16 windows at 125–150 %).
func TestTiledMinFitsCell(t *testing.T) {
	for _, c := range []struct {
		name string
		work rect
		dpi  int32
		n    int
	}{
		{"1920×1080 150% 12 台", rect{0, 0, 1920, 1032}, 144, 12},
		{"1920×1080 125% 16 台", rect{0, 0, 1920, 1032}, 120, 16},
		{"1366×768 100% 16 台", rect{0, 0, 1366, 728}, 96, 16},
		{"2560×1440 100% 16 台", rect{0, 0, 2560, 1392}, 96, 16},
	} {
		for _, cell := range tileRects(c.work, c.n) {
			cw, ch := cell.R-cell.L, cell.B-cell.T
			mw, mh := tiledMin(cw, ch, c.dpi)
			if int32(mw)*c.dpi/96 > cw || int32(mh)*c.dpi/96 > ch {
				t.Errorf("%s: 最小 %d×%d（論理）が枠 %d×%d（物理）より大きい", c.name, mw, mh, cw, ch)
			}
		}
	}
	if w, h := tiledMin(1280, 700, 96); w != tiledMinW || h != tiledMinH {
		t.Errorf("広い枠では通常の最小: %d×%d", w, h)
	}
	if w, h := tiledMin(100, 80, 96); w != tiledFloorW || h != tiledFloorH {
		t.Errorf("下限: %d×%d", w, h)
	}
}

// orderLog records the relay (childLine) and the local send in one list.
type orderLog struct {
	mu  sync.Mutex
	log []string
}

func (o *orderLog) add(s string) { o.mu.Lock(); o.log = append(o.log, s); o.mu.Unlock() }
func (o *orderLog) Write(p []byte) (int, error) {
	o.add("relay:" + strings.TrimSpace(string(p)))
	return len(p), nil
}

func newSendTerm(o *orderLog, join bool) *Term {
	t := &Term{}
	t.bc.join = join
	t.send = func(s string) error { o.add("send:" + s); return nil }
	return t
}

// 同時入力: what is typed goes to the other windows first, then to this
// device (a slow line here no longer holds the others back); a send meant
// for this device alone, or made with 同時入力 off, is not relayed.
func TestSendTextRelayFirst(t *testing.T) {
	o := &orderLog{}
	old := childOut
	childOut = o
	defer func() { childOut = old }()

	tm := newSendTerm(o, true)
	if err := tm.sendText("show clock\r", true); err != nil {
		t.Fatal(err)
	}
	want := []string{"relay:" + termEventBcast + " c2hvdyBjbG9jaw0=", "send:show clock\r"}
	if strings.Join(o.log, "|") != strings.Join(want, "|") {
		t.Errorf("順番: %q", o.log)
	}
	o.log = nil
	_ = tm.sendText("local only", false) // a reply, a mouse report, a paste confirmed with 同時入力 off
	_ = newSendTerm(o, false).sendText("not joined", true)
	if strings.Join(o.log, "|") != "send:local only|send:not joined" {
		t.Errorf("中継されるべきでない送信: %q", o.log)
	}
	// Locked: nothing at all.
	o.log = nil
	tm.locked = true
	_ = tm.sendText("x", true)
	if len(o.log) != 0 {
		t.Errorf("ロック中に送られた: %q", o.log)
	}
}

// 並べ直す with more windows than fit lays out the first maxTiled opened
// and leaves the rest alone.
func TestRetileCap(t *testing.T) {
	a := NewApp()
	bufs := map[int]*nopBuf{}
	for i := 1; i <= maxTiled+3; i++ {
		b := &nopBuf{}
		a.terms[&exec.Cmd{}] = newTermChild(b, i)
		bufs[i] = b
	}
	a.retile(rect{0, 0, 1920, 1032})
	if !waitFor(func() bool { return strings.HasPrefix(bufs[maxTiled].String(), "PLACE ") }) {
		t.Fatalf("%d 台目が並ばない", maxTiled)
	}
	time.Sleep(50 * time.Millisecond)
	for i := maxTiled + 1; i <= maxTiled+3; i++ {
		if bufs[i].String() != "" {
			t.Errorf("%d 台目（上限超え）が動かされた: %q", i, bufs[i].String())
		}
	}
}
