package main

import (
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// v1.9.38: the spare paste window (one in all, with the terminal window
// last in use) and its first line.

// A terminal window that became the active one: the main window tells every
// other window to drop its spare, not the one that said so.
func TestFocusDropsOtherSpares(t *testing.T) {
	a := NewApp()
	from, fromBuf := &exec.Cmd{}, &nopBuf{}
	a.terms[from] = newTermChild(fromBuf, 0)
	other, otherBuf := &exec.Cmd{}, &nopBuf{}
	a.terms[other] = newTermChild(otherBuf, 1)
	a.termLine(from, termEventFocus)
	if !waitFor(func() bool { return otherBuf.String() == termLineSpareOff+"\n" }) {
		t.Errorf("ほかの窓に SPAREOFF が届かない: %q", otherBuf.String())
	}
	time.Sleep(50 * time.Millisecond)
	if fromBuf.String() != "" {
		t.Errorf("前面になった窓自身には送らない: %q", fromBuf.String())
	}
}

// fakeSpares replaces spawnPasteSpare: each spare's stdout is a pipe the
// test writes its first line to; release holds the start back.
type fakeSpares struct {
	mu      sync.Mutex
	started int
	outs    []*io.PipeWriter
	ins     []*nopBuf
	release chan struct{}
}

func (f *fakeSpares) spawn(string) (*pasteProc, error) {
	if f.release != nil {
		<-f.release
	}
	r, w := io.Pipe()
	in := &nopBuf{}
	f.mu.Lock()
	f.started++
	f.outs = append(f.outs, w)
	f.ins = append(f.ins, in)
	f.mu.Unlock()
	return newPasteProc(&exec.Cmd{}, in, r, true), nil
}

func (f *fakeSpares) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

func withFakeSpares(t *testing.T, f *fakeSpares) {
	old, on := spawnPasteSpare, pasteSpareOn
	spawnPasteSpare, pasteSpareOn = f.spawn, true
	t.Cleanup(func() {
		spawnPasteSpare, pasteSpareOn = old, on
		f.mu.Lock()
		for _, w := range f.outs {
			_ = w.Close()
		}
		f.mu.Unlock()
	})
}

// As built, no spare is kept (pasteSpareOn): a terminal window that became
// the active one starts none.
func TestNoSpareAsBuilt(t *testing.T) {
	if pasteSpareOn {
		t.Fatal("pasteSpareOn は false のはず")
	}
	f := &fakeSpares{}
	old := spawnPasteSpare
	spawnPasteSpare = f.spawn
	t.Cleanup(func() { spawnPasteSpare = old })
	tm := &Term{label: "lab1"}
	tm.respare()
	time.Sleep(80 * time.Millisecond)
	if f.count() != 0 || spareOf(tm) != nil {
		t.Errorf("待機窓を起動した: %d", f.count())
	}
}

func spareOf(tm *Term) *pasteProc {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.pasteSpare
}

// One spare per window, none while locked, closing or a paste window is
// open; a drop ends it, and one that was starting through the drop is not
// kept.
func TestRespare(t *testing.T) {
	f := &fakeSpares{}
	withFakeSpares(t, f)

	for _, tm := range []*Term{{locked: true}, {quitting: true}, {pasteOpen: true}} {
		tm.respare()
	}
	time.Sleep(50 * time.Millisecond)
	if f.count() != 0 {
		t.Fatalf("ロック中・終了中・確認窓が開いている間は作らない: %d", f.count())
	}

	tm := &Term{}
	tm.respare()
	tm.respare()
	if !waitFor(func() bool { return spareOf(tm) != nil }) {
		t.Fatal("待機窓ができない")
	}
	tm.respare()
	time.Sleep(50 * time.Millisecond)
	if f.count() != 1 {
		t.Errorf("待機窓は 1 つだけ: %d", f.count())
	}
	tm.dropPasteSpare()
	if spareOf(tm) != nil {
		t.Error("drop で待機窓が外れない")
	}

	// Dropped (another window in use) while its process was starting: not kept.
	f.release = make(chan struct{})
	tm.respare()
	time.Sleep(20 * time.Millisecond)
	tm.dropPasteSpare()
	close(f.release)
	time.Sleep(80 * time.Millisecond)
	if spareOf(tm) != nil {
		t.Error("起動中に drop された待機窓が残った")
	}
	// Locked while it was starting: not kept either.
	f.release = make(chan struct{})
	tm.respare()
	time.Sleep(20 * time.Millisecond)
	tm.mu.Lock()
	tm.locked = true
	tm.mu.Unlock()
	close(f.release)
	time.Sleep(80 * time.Millisecond)
	if spareOf(tm) != nil {
		t.Error("起動中にロックされた待機窓が残った")
	}
}

// A spare is ready only once its page has said so (its first line); a
// paste window that is not a spare is ready at once.
func TestPasteProcReady(t *testing.T) {
	r, w := io.Pipe()
	p := newPasteProc(&exec.Cmd{}, &nopBuf{}, r, true)
	select {
	case <-p.ready:
		t.Fatal("最初の行の前に準備完了になった")
	default:
	}
	go func() { _, _ = io.WriteString(w, pasteLineReady+"\n"+`{"ok":true,"text":"x"}`+"\n") }()
	select {
	case <-p.ready:
	case <-time.After(time.Second):
		t.Fatal("READY で準備完了にならない")
	}
	<-p.readDone
	// The answer after it is still there to read.
	line, err := p.rd.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != `{"ok":true,"text":"x"}` {
		t.Errorf("READY の後の答え: %q %v", line, err)
	}

	// A spare that ends before its page came up never becomes ready.
	r2, w2 := io.Pipe()
	p2 := newPasteProc(&exec.Cmd{}, &nopBuf{}, r2, true)
	_ = w2.CloseWithError(errors.New("gone"))
	<-p2.readDone
	select {
	case <-p2.ready:
		t.Error("終わった待機窓が準備完了になった")
	default:
	}

	p3 := newPasteProc(&exec.Cmd{}, &nopBuf{}, io.NopCloser(strings.NewReader("")), false)
	select {
	case <-p3.ready:
	default:
		t.Error("待機窓でない確認窓はすぐ使える")
	}
}
