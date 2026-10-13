//go:build !windows

package main

// keepSystemAwake is a no-op off Windows (see keepawake_windows.go).
func keepSystemAwake() (release func()) { return func() {} }
