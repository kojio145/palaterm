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
	// Standalone terminal window mode: "PalaTerm.exe --connect <device>".
	if len(os.Args) >= 3 && os.Args[1] == "--connect" {
		runTerminalWindow(os.Args[2])
		return
	}
	// Log-viewer window: "--view <file>" (opened from the main window's and the
	// terminal's ログ表示 buttons).
	if len(os.Args) >= 3 && os.Args[1] == "--view" {
		runViewerWindow(os.Args[2])
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
	title := "PalaTerm - ログ - " + filepath.Base(path)
	v := NewViewer(path, title)
	err := wails.Run(&options.App{
		Title:     title,
		Width:     1200,
		Height:    800,
		MinWidth:  480,
		MinHeight: 320,
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

func runDiffWindow(a, b, label string) {
	title := "PalaTerm - 差分"
	if label != "" {
		title += " - " + label
	}
	d := NewDiffWin(a, b, label, title)
	err := wails.Run(&options.App{
		Title:     title,
		Width:     1500,
		Height:    860,
		MinWidth:  720,
		MinHeight: 400,
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
	title := "PalaTerm - 貼り付けの確認"
	if device != "" {
		title += " - " + device
	}
	p := NewPaste(title)
	err := wails.Run(&options.App{
		Title:     title,
		Width:     1100,
		Height:    720,
		MinWidth:  480,
		MinHeight: 320,
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
	app := NewApp()
	err := wails.Run(&options.App{
		Title:     "PalaTerm",
		Width:     1440,
		Height:    960,
		MinWidth:  1100,
		MinHeight: 680,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 21, B: 28, A: 1},
		OnStartup:        app.startup,
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

func runTerminalWindow(device string) {
	t := NewTerm(device)
	err := wails.Run(&options.App{
		Title:     "PalaTerm - " + device,
		Width:     1200,
		Height:    760,
		MinWidth:  640,
		MinHeight: 400,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 1},
		OnStartup:        t.startup,
		OnDomReady:       t.domReady,
		OnShutdown:       t.shutdown,
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
