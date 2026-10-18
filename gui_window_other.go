//go:build !windows

package main

// runGUIWindow: the GUI window is Windows-only (WebView2).
func runGUIWindow() {}

// cleanStaleGUIProfiles: no GUI windows, nothing left behind.
func cleanStaleGUIProfiles() {}
