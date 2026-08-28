// PalaTerm — multi-device SSH/Telnet/Serial batch login tool.
//
// A single portable Windows executable (no installation) that logs into many
// network devices at once to capture logs and push configuration, with an
// encrypted credential store.
package main

import (
	"embed"
	"os"

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
	runMainApp()
}

func runMainApp() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:     "PalaTerm",
		Width:     1440,
		Height:    860,
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
		Title:  "PalaTerm - " + device,
		Width:  900,
		Height: 560,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 1},
		OnStartup:        t.startup,
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
