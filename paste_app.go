package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Paste is the backend of the paste-confirmation window, a separate process
// ("PalaTerm.exe --paste <device>") the interactive terminal opens the way
// Tera Term does: it can be moved, resized and compared against the terminal
// side by side instead of covering it. The text to confirm arrives as one
// JSON line on stdin; the answer goes back as one JSON line on stdout and the
// window closes. Closing the window without answering (the X) ends the
// process, and the terminal takes the EOF as a cancel. No vault, no
// password: this process only edits text.
//
// The terminal keeps the stdin pipe open: a pasteLineDump line on it (its
// Lock) asks for the text as it is now, answered with a Draft line and the
// window closing — the terminal keeps it for after the unlock (M-13).
type Paste struct {
	ctx   context.Context
	title string
	reqCh chan pasteReq

	mu       sync.Mutex
	req      pasteReq // what arrived on stdin
	loaded   bool     // the page has it (Load)
	answered bool     // a line has gone back on stdout
}

// pasteLineDump is what the terminal writes to the paste window's stdin
// to ask for the text it holds now.
const pasteLineDump = "DUMP"

// pasteDumpPageWait is how long the page gets to answer paste:dump before
// the text as handed over is sent back instead.
const pasteDumpPageWait = 700 * time.Millisecond

// pasteReq is what the terminal sends: the clipboard text, whether Enter
// should be sent at the end (Alt+V), and the device name for the title.
type pasteReq struct {
	Device string `json:"device"`
	Text   string `json:"text"`
	CR     bool   `json:"cr"`
	// Bcast is how many other windows get the text too (同時入力), 0 when off.
	Bcast int `json:"bcast,omitempty"`
	// Single: a one-line paste, confirmed only because of 同時入力 (no
	// Enter unless CR, whatever the remembered checkbox says).
	Single bool `json:"single,omitempty"`
}

// pasteRes is the answer: OK false means cancelled.
type pasteRes struct {
	OK   bool   `json:"ok"`
	Text string `json:"text,omitempty"`
	CR   bool   `json:"cr,omitempty"`
	// Draft: the text as held when the terminal's Lock asked for it
	// (pasteLineDump); not a confirmation, nothing is sent.
	Draft bool `json:"draft,omitempty"`
}

func NewPaste(title string) *Paste {
	p := &Paste{title: title, reqCh: make(chan pasteReq, 1)}
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1024*1024), 64*1024*1024) // pasted configs can be big
		var r pasteReq
		if sc.Scan() && json.Unmarshal(sc.Bytes(), &r) == nil {
			p.mu.Lock()
			p.req = r
			p.mu.Unlock()
			p.reqCh <- r
		}
		for sc.Scan() {
			if sc.Text() == pasteLineDump {
				p.dump()
			}
		}
	}()
	return p
}

// dump answers pasteLineDump: the page is asked for the text as edited
// (paste:dump → Draft), or, before it has the text, the text as handed over
// goes back at once.
func (p *Paste) dump() {
	p.mu.Lock()
	ctx, loaded, req := p.ctx, p.loaded, p.req
	p.mu.Unlock()
	if ctx == nil || !loaded {
		p.Draft(req.Text, req.CR)
		return
	}
	runtime.EventsEmit(ctx, "paste:dump")
	time.AfterFunc(pasteDumpPageWait, func() { p.Draft(req.Text, req.CR) })
}

// answer writes one line back and closes; false when one already went
// (a Draft after a Submit, or the other way round).
func (p *Paste) answer(res pasteRes) bool {
	p.mu.Lock()
	if p.answered {
		p.mu.Unlock()
		return false
	}
	p.answered = true
	ctx := p.ctx
	p.mu.Unlock()
	_ = json.NewEncoder(os.Stdout).Encode(res)
	handForegroundToParent()
	if ctx != nil {
		runtime.Quit(ctx)
	} else {
		os.Exit(0)
	}
	return true
}

// Draft is the page's answer to paste:dump: the text as it is now, not
// confirmed (the terminal keeps it for after its unlock).
func (p *Paste) Draft(text string, cr bool) {
	p.answer(pasteRes{Draft: true, Text: text, CR: cr})
}

func (p *Paste) startup(ctx context.Context) {
	p.mu.Lock()
	p.ctx = ctx
	p.mu.Unlock()
	pasteWin.fit(ctx)
	placeNearParent(ctx, pasteWin)
	go keepKeyboardOnWebview(ctx)
}

// domReady brings the new window to the front with keyboard focus, so Enter
// pastes the moment it appears instead of after a click into it.
func (p *Paste) domReady(ctx context.Context) {
	go focusOwnWindow()
}

// Focus is called by the page once its textarea is ready (a second nudge,
// in case the window was found before it could take focus).
func (p *Paste) Focus() { go focusOwnWindow() }

// Load hands the UI the text to show (empty if nothing arrived on stdin).
func (p *Paste) Load() pasteReq {
	select {
	case r := <-p.reqCh:
		p.mu.Lock()
		p.loaded = true
		p.mu.Unlock()
		return r
	case <-time.After(2 * time.Second):
		return pasteReq{}
	}
}

// Submit returns the (possibly edited) text to the terminal and closes.
// The foreground goes back to the terminal first (handForegroundToParent),
// while this window still holds it.
func (p *Paste) Submit(text string, cr bool) {
	p.answer(pasteRes{OK: true, Text: text, CR: cr})
}

// Cancel closes without pasting.
func (p *Paste) Cancel() {
	p.answer(pasteRes{OK: false})
}

// beforeClose: the X (or Alt+F4) closes the window with no answer — the
// terminal takes the EOF as a cancel — and hands the foreground back the
// same way. false lets the close go on.
func (p *Paste) beforeClose(context.Context) bool {
	handForegroundToParent()
	return false
}
