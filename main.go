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

func runViewerWindow(path string) {
	title := "PalaTerm - " + winWord("ログ", "Log") + " - " + filepath.Base(path)
	v := NewViewer(path, title)
	err := wails.Run(&options.App{
		Title:     title,
		Width:     viewerWin.W,
		Height:    viewerWin.H,
		MinWidth:  viewerWin.MinW,
		MinHeight: viewerWin.MinH,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 21, B: 28, A: 1},
		OnStartup:        v.startup,
		OnDomReady:       v.domReady,
		Bind:             []interface{}{v},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("error:", err.Error())
	}
}

func runWatchWindow(path string) {
	title := "PalaTerm - " + winWord("画面", "Screen") + " - " + filepath.Base(path)
	w := NewWatcher(path, title)
	err := wails.Run(&options.App{
		Title:     title,
		Width:     termWin.W,
		Height:    termWin.H,
		MinWidth:  termWin.MinW,
		MinHeight: termWin.MinH,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 1},
		OnStartup:        w.startup,
		OnDomReady:       w.domReady,
		Bind:             []interface{}{w},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("error:", err.Error())
	}
}

func runDiffWindow(a, b, label string) {
	title := "PalaTerm - " + winWord("差分", "Diff")
	if label != "" {
		title += " - " + label
	}
	d := NewDiffWin(a, b, label, title)
	err := wails.Run(&options.App{
		Title:     title,
		Width:     diffWin.W,
		Height:    diffWin.H,
		MinWidth:  diffWin.MinW,
		MinHeight: diffWin.MinH,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 21, B: 28, A: 1},
		OnStartup:        d.startup,
		OnDomReady:       d.domReady,
		Bind:             []interface{}{d},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("error:", err.Error())
	}
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
	err := wails.Run(&options.App{
		StartHidden: spare,
		Title:       title,
		Width:       cmdsetWinSize.W,
		Height:      cmdsetWinSize.H,
		MinWidth:    cmdsetWinSize.MinW,
		MinHeight:   cmdsetWinSize.MinH,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 21, B: 28, A: 1},
		OnStartup: func(ctx context.Context) {
			c.startup(ctx)
			close(ready)
		},
		OnDomReady: c.domReady,
		Bind:       []interface{}{c},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("error:", err.Error())
	}
}

func runPasteWindow(device string) {
	title := "PalaTerm - " + winWord("貼り付けの確認", "Paste")
	if device != "" {
		title += " - " + device
	}
	p := NewPaste(title)
	err := wails.Run(&options.App{
		Title:     title,
		Width:     pasteWin.W,
		Height:    pasteWin.H,
		MinWidth:  pasteWin.MinW,
		MinHeight: pasteWin.MinH,
		// Stays above the terminal it belongs to, like Tera Term's dialog;
		// it can still be moved aside to read the terminal underneath.
		AlwaysOnTop: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 26, G: 33, B: 43, A: 1},
		OnStartup:        p.startup,
		OnDomReady:       p.domReady,
		Bind:             []interface{}{p},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("error:", err.Error())
	}
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
