// PalaTerm — multi-device SSH/Telnet/Serial batch login tool.
//
// A single portable Windows executable (no installation) that logs into many
// network devices at once to capture logs and push configuration, with an
// encrypted credential store.
package main

import (
	"embed"
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
	// "PalaTerm.exe --connect <device ID> [--manual] [--label <name>]".
	// --manual opens the line without the automatic login (initial setup
	// over serial, a device with no password yet, an unexpected screen);
	// --label is the name the window shows (the ID means nothing to a person).
	if len(os.Args) >= 3 && os.Args[1] == "--connect" {
		manual, label := false, ""
		for i := 3; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--manual":
				manual = true
			case "--label":
				if i+1 < len(os.Args) {
					label = os.Args[i+1]
					i++
				}
			}
		}
		runTerminalWindow(os.Args[2], label, manual)
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

func runTerminalWindow(device, label string, manual bool) {
	t := NewTerm(device, label, manual)
	title := "PalaTerm - " + t.label
	if manual {
		title += " - " + winWord("手動接続", "manual")
	}
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
