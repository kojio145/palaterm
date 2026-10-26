// PalaTerm — multi-device SSH/Telnet/Serial batch login tool.
//
// A single portable Windows executable (no installation) that logs into many
// network devices at once to capture logs and push configuration, with an
// encrypted credential store.
package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Standalone terminal window mode:
	// "PalaTerm.exe --connect <device ID> [--manual] [--direct] [--label <name>]".
	// --manual opens the line without the automatic login (initial setup
	// over serial, a device with no password yet, an unexpected screen);
	// --label is the name the window shows (the ID means nothing to a person).
	if len(os.Args) >= 3 && os.Args[1] == "--connect" {
		manual, direct, label := false, false, ""
		for i := 3; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--manual":
				manual = true
			case "--direct":
				direct = true
			case "--label":
				if i+1 < len(os.Args) {
					label = os.Args[i+1]
					i++
				}
			}
		}
		runTerminalWindow(os.Args[2], label, manual, direct)
		return
	}
	// GUI window: "--gui" (a device's web GUI, logged in; what to open
	// arrives on stdin — see guiLaunch).
	if len(os.Args) >= 2 && os.Args[1] == "--gui" {
		runGUIWindow()
		return
	}
	// Log-viewer window: "--view <file>" (opened from the main window's and the
	// terminal's ログ表示 buttons).
	if len(os.Args) >= 3 && os.Args[1] == "--view" {
		runViewerWindow(os.Args[2])
		return
	}
	// Screen window: "--watch <file>" (a batch device's log replayed into a
	// read-only terminal as it grows; the run table's 画面).
	if len(os.Args) >= 3 && os.Args[1] == "--watch" {
		runWatchWindow(os.Args[2])
		return
	}
	// Diff window: "--diff <A> <B> [label]".
	if len(os.Args) >= 4 && os.Args[1] == "--diff" {
		label := ""
		if len(os.Args) >= 5 {
			label = os.Args[4]
		}
		runDiffWindow(os.Args[2], os.Args[3], label)
		return
	}
	// Command-set window: "--cmdset" (one set, read-only; the set arrives on
	// stdin from the main window — see cmdsetMsg).
	if len(os.Args) >= 2 && os.Args[1] == "--cmdset" {
		runCmdsetWindow()
		return
	}
	// Paste-confirmation window opened by a terminal window: "--paste <device>".
	if len(os.Args) >= 2 && os.Args[1] == "--paste" {
		dev := ""
		if len(os.Args) >= 3 {
			dev = os.Args[2]
		}
		runPasteWindow(dev)
		return
	}
	runMainApp()
}

// childWindow is what differs between the child windows (log viewer,
// screen, diff, command set, paste); runChildWindow runs one with what they
// share.
type childWindow struct {
	title    string
	size     winSize
	bg       options.RGBA
	bind     interface{}
	startup  func(context.Context)
	domReady func(context.Context)
	// beforeClose runs when the window is closed from its frame (nil: just
	// close); true keeps it open.
	beforeClose func(context.Context) bool
	hidden      bool // StartHidden: the spare command-set window
	onTop    bool // AlwaysOnTop: the paste window
}

// Background colours: the panel windows (viewer, diff, command set), the
// terminal-like screen window, and the paste window.
var (
	panelBG  = options.RGBA{R: 17, G: 21, B: 28, A: 1}
	screenBG = options.RGBA{R: 0, G: 0, B: 0, A: 1}
	pasteBG  = options.RGBA{R: 26, G: 33, B: 43, A: 1}
)

func runChildWindow(w childWindow) {
	bg := w.bg
	err := wails.Run(&options.App{
		StartHidden: w.hidden,
		AlwaysOnTop: w.onTop,
		Title:       w.title,
		Width:       w.size.W,
		Height:      w.size.H,
		MinWidth:    w.size.MinW,
		MinHeight:   w.size.MinH,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &bg,
		OnStartup:        w.startup,
		OnDomReady:       w.domReady,
		OnBeforeClose:    w.beforeClose,
		Bind:             []interface{}{w.bind},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("error:", err.Error())
	}
}

func runViewerWindow(path string) {
	title := "PalaTerm - " + winWord("ログ", "Log") + " - " + filepath.Base(path)
	v := NewViewer(path, title)
	runChildWindow(childWindow{title: title, size: viewerWin, bg: panelBG, bind: v, startup: v.startup, domReady: v.domReady})
}

func runWatchWindow(path string) {
	title := "PalaTerm - " + winWord("画面", "Screen") + " - " + filepath.Base(path)
	w := NewWatcher(path, title)
	runChildWindow(childWindow{title: title, size: termWin, bg: screenBG, bind: w, startup: w.startup, domReady: w.domReady})
}

func runDiffWindow(a, b, label string) {
	title := "PalaTerm - " + winWord("差分", "Diff")
	if label != "" {
		title += " - " + label
	}
	d := NewDiffWin(a, b, label, title)
	runChildWindow(childWindow{title: title, size: diffWin, bg: panelBG, bind: d, startup: d.startup, domReady: d.domReady})
}

func runCmdsetWindow() {
	in := bufio.NewReader(os.Stdin)
	c := &CmdsetWin{}
	title := cmdsetTitle("")
	// A spare starts hidden with nothing in it, ahead of the click; its
	// first set shows it (see CmdsetWin.follow). Any other window starts
	// with its set.
	spare := len(os.Args) >= 3 && os.Args[2] == "--spare"
	if spare {
		c.hidden = true
		// The window the user is in now gets the foreground back if the
		// hidden frame takes it.
		go keepForegroundWhileHidden(foregroundWindow(), func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.hidden
		})
	} else {
		line, err := in.ReadBytes('\n')
		var m cmdsetMsg
		if err != nil || json.Unmarshal(line, &m) != nil || m.Kind != "show" || m.Doc == nil {
			return
		}
		c.apply(m)
		title = m.Title
	}
	ready := make(chan struct{})
	go c.follow(in, ready)
	runChildWindow(childWindow{title: title, size: cmdsetWinSize, bg: panelBG, bind: c, hidden: spare,
		startup: func(ctx context.Context) {
			c.startup(ctx)
			close(ready)
		},
		domReady: c.domReady,
	})
}

func runPasteWindow(device string) {
	title := "PalaTerm - " + winWord("貼り付けの確認", "Paste")
	if device != "" {
		title += " - " + device
	}
	p := NewPaste(title)
	// Stays above the terminal it belongs to, like Tera Term's dialog; it
	// can still be moved aside to read the terminal underneath.
	runChildWindow(childWindow{title: title, size: pasteWin, bg: pasteBG, bind: p, onTop: true, startup: p.startup, domReady: p.domReady, beforeClose: p.beforeClose})
}

func runMainApp() {
	// One main window per folder: a second start shows the first (it may be
	// in the tray running 予約) and ends.
	if !claimSingleInstance() {
		return
	}
	// …and one per folder however it is reached (a share path, another PC,
	// another user's session): see datalock_windows.go.
	if !claimDataFolder() {
		return
	}
	app := NewApp()
	err := wails.Run(&options.App{
		Title:     "PalaTerm",
		Width:     mainWin.W,
		Height:    mainWin.H,
		MinWidth:  mainWin.MinW,
		MinHeight: mainWin.MinH,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 21, B: 28, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    app.beforeClose,
		Bind:             []interface{}{app},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("error:", err.Error())
	}
}

func runTerminalWindow(device, label string, manual, direct bool) {
	t := NewTerm(device, label, manual, direct)
	title := "PalaTerm - " + t.label
	if manual {
		title += " - " + winWord("手動接続", "manual")
	}
	if direct {
		title += " - " + winWord("踏み台なし", "no jump host")
	}
	// The size the last terminal window was closed at (see rememberSize).
	w, h := termWin.W, termWin.H
	if sw, sh, ok := loadTermWinSize(); ok {
		w, h = sw, sh
	}
	err := wails.Run(&options.App{
		Title:     title,
		Width:     w,
		Height:    h,
		MinWidth:  termWin.MinW,
		MinHeight: termWin.MinH,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 1},
		OnStartup:        t.startup,
		OnDomReady:       t.domReady,
		OnShutdown:       t.shutdown,
		OnBeforeClose:    t.beforeClose,
		Bind:             []interface{}{t},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("error:", err.Error())
	}
}
