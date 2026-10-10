package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
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
type Paste struct {
	ctx   context.Context
	title string
	reqCh chan pasteReq
}

// pasteReq is what the terminal sends: the clipboard text, whether Enter
// should be sent at the end (Alt+V), and the device name for the title.
type pasteReq struct {
	Device string `json:"device"`
	Text   string `json:"text"`
	CR     bool   `json:"cr"`
}

// pasteRes is the answer: OK false means cancelled.
type pasteRes struct {
	OK   bool   `json:"ok"`
	Text string `json:"text,omitempty"`
	CR   bool   `json:"cr,omitempty"`
}

func NewPaste(title string) *Paste {
	p := &Paste{title: title, reqCh: make(chan pasteReq, 1)}
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1024*1024), 64*1024*1024) // pasted configs can be big
		var r pasteReq
		if sc.Scan() && json.Unmarshal(sc.Bytes(), &r) == nil {
			p.reqCh <- r
		}
	}()
	return p
}

func (p *Paste) startup(ctx context.Context) { p.ctx = ctx }

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
		return r
	case <-time.After(2 * time.Second):
		return pasteReq{}
	}
}

// Submit returns the (possibly edited) text to the terminal and closes.
func (p *Paste) Submit(text string, cr bool) {
	_ = json.NewEncoder(os.Stdout).Encode(pasteRes{OK: true, Text: text, CR: cr})
	runtime.Quit(p.ctx)
}

// Cancel closes without pasting.
func (p *Paste) Cancel() {
	_ = json.NewEncoder(os.Stdout).Encode(pasteRes{OK: false})
	runtime.Quit(p.ctx)
}
