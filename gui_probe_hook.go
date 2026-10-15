//go:build probe

package main

import "os"

// The GUI-test build only (built with -tags probe):
// the GUI window's WebView2 opens the DevTools port given in
// PALATERM_GUI_WEBVIEW_ARGS, so a probe can read the page. The release
// build has no such hook.
func init() { guiProbeArgs = os.Getenv("PALATERM_GUI_WEBVIEW_ARGS") }
