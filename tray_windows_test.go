package main

import (
	"testing"
	"time"
	"unsafe"
)

// The tray icon is really registered with the shell (NIM_MODIFY succeeds
// only for an existing icon), and gone after trayHide.
func TestTrayIconRegisters(t *testing.T) {
	trayShow(func() {}, func() {}, "", "")
	tray.mu.Lock()
	hwnd := tray.hwnd
	tray.mu.Unlock()
	if hwnd == 0 {
		t.Fatal("no tray window")
	}
	if tray.icon == 0 {
		t.Error("app icon (resource #1) not loaded")
	}
	modify := func() bool {
		nid := trayData(hwnd)
		nid.UFlags = nifTip
		utf16Fill(nid.SzTip[:], "PalaTerm test")
		r, _, _ := pShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
		return r != 0
	}
	if !modify() {
		t.Fatal("icon not registered with the shell")
	}
	trayHide()
	time.Sleep(300 * time.Millisecond)
	if modify() {
		t.Error("icon still registered after trayHide")
	}
}
