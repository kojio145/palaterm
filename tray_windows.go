package main

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// The notification-area (tray) icon the main window hides into when it is
// closed while 予約 are pending (see App.beforeClose). Plain Win32: a hidden
// window of our own, on a goroutine locked to its OS thread, owns the icon
// and runs the message loop that receives its clicks — Wails has no tray of
// its own, and a library would bring a second GUI toolkit for one icon.

var (
	shell32                 = syscall.NewLazyDLL("shell32.dll")
	pShellNotifyIcon        = shell32.NewProc("Shell_NotifyIconW")
	pRegisterClassEx        = user32.NewProc("RegisterClassExW")
	pCreateWindowEx         = user32.NewProc("CreateWindowExW")
	pDestroyWindow          = user32.NewProc("DestroyWindow")
	pDefWindowProc          = user32.NewProc("DefWindowProcW")
	pGetMessage             = user32.NewProc("GetMessageW")
	pTranslateMessage       = user32.NewProc("TranslateMessage")
	pDispatchMessage        = user32.NewProc("DispatchMessageW")
	pPostMessage            = user32.NewProc("PostMessageW")
	pPostQuitMessage        = user32.NewProc("PostQuitMessage")
	pCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	pAppendMenu             = user32.NewProc("AppendMenuW")
	pTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	pDestroyMenu            = user32.NewProc("DestroyMenu")
	pGetCursorPos           = user32.NewProc("GetCursorPos")
	pLoadImage              = user32.NewProc("LoadImageW")
	pRegisterWindowMessage  = user32.NewProc("RegisterWindowMessageW")
	pGetModuleHandle        = kernel32.NewProc("GetModuleHandleW")
	pGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	pSetForegroundWindowRaw = pSetForegroundWindow
)

const (
	nimAdd      = 0
	nimModify   = 1
	nimDelete   = 2
	nifMessage  = 0x1
	nifIcon     = 0x2
	nifTip      = 0x4
	nifInfo     = 0x10
	niifInfo    = 0x1
	wmApp       = 0x8000
	wmTray      = wmApp + 1
	wmTrayClose = wmApp + 2
	wmClose     = 0x0010
	wmDestroy   = 0x0002
	wmNull      = 0x0000
	wmLButtonUp = 0x0202
	wmLButtonDb = 0x0203
	wmRButtonUp = 0x0205
	mfString    = 0x0
	mfGrayed    = 0x1
	mfSeparator = 0x800
	tpmReturnCm = 0x0100
	tpmRightBtn = 0x0002
	imageIcon   = 1
	smCxSmIcon  = 49
	smCySmIcon  = 50
	trayCmdOpen = 1
	trayCmdQuit = 2
)

// notifyIconData is NOTIFYICONDATAW (Vista+ layout).
type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type winMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// tray is the one tray icon of the process.
var tray struct {
	mu        sync.Mutex
	hwnd      uintptr
	icon      uintptr
	created   uint32 // "TaskbarCreated": Explorer restarted, add the icon again
	onOpen    func()
	onQuit    func()
	shown     chan struct{}
	wndProcCB uintptr
	// tip is what the icon says on hover and at the top of its menu: the
	// schedule running now, or the next one and when ("waiting" alone told
	// nothing about what the tray was waiting for).
	tip string
}

// trayTip sets the icon's hover text (no-op without an icon, or unchanged).
func trayTip(text string) {
	tray.mu.Lock()
	hwnd := tray.hwnd
	same := tray.tip == text
	tray.tip = text
	tray.mu.Unlock()
	if hwnd == 0 || same {
		return
	}
	nid := trayData(hwnd)
	nid.UFlags = nifTip
	utf16Fill(nid.SzTip[:], text)
	pShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func trayTipText() string {
	tray.mu.Lock()
	defer tray.mu.Unlock()
	if tray.tip != "" {
		return tray.tip
	}
	return "PalaTerm — " + winWord("予約を待機中", "waiting for schedules")
}

func utf16Fill(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	if len(u) > len(dst) {
		u = append(u[:len(dst)-1], 0)
	}
	copy(dst, u)
}

func trayData(hwnd uintptr) notifyIconData {
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = 1
	return nid
}

func trayAdd() {
	nid := trayData(tray.hwnd)
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = wmTray
	nid.HIcon = tray.icon
	utf16Fill(nid.SzTip[:], trayTipText())
	pShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
}

func trayWndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch {
	case msg == wmTray:
		switch uint32(lparam & 0xffff) {
		case wmLButtonUp, wmLButtonDb:
			// A single click opens too: a double-click-only icon read as dead.
			if tray.onOpen != nil {
				go tray.onOpen()
			}
		case wmRButtonUp:
			trayMenu(hwnd)
		}
		return 0
	case msg == tray.created && msg != 0:
		trayAdd()
		return 0
	case msg == wmTrayClose:
		nid := trayData(hwnd)
		pShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
		pDestroyWindow.Call(hwnd)
		return 0
	case msg == wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}

func trayMenu(hwnd uintptr) {
	menu, _, _ := pCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer pDestroyMenu.Call(menu)
	open, _ := syscall.UTF16PtrFromString(winWord("PalaTerm を開く", "Open PalaTerm"))
	quit, _ := syscall.UTF16PtrFromString(winWord("終了（予約は実行されなくなります）", "Quit (schedules stop running)"))
	// The status line first (greyed, not a command): what is running or next.
	info, _ := syscall.UTF16PtrFromString(trayTipText())
	pAppendMenu.Call(menu, mfString|mfGrayed, 0, uintptr(unsafe.Pointer(info)))
	pAppendMenu.Call(menu, mfSeparator, 0, 0)
	pAppendMenu.Call(menu, mfString, trayCmdOpen, uintptr(unsafe.Pointer(open)))
	pAppendMenu.Call(menu, mfSeparator, 0, 0)
	pAppendMenu.Call(menu, mfString, trayCmdQuit, uintptr(unsafe.Pointer(quit)))
	var pt struct{ X, Y int32 }
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Without the foreground the menu would not close on a click elsewhere
	// (documented TrackPopupMenu behaviour).
	pSetForegroundWindowRaw.Call(hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(menu, tpmReturnCm|tpmRightBtn, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	pPostMessage.Call(hwnd, wmNull, 0, 0)
	switch cmd {
	case trayCmdOpen:
		if tray.onOpen != nil {
			go tray.onOpen()
		}
	case trayCmdQuit:
		if tray.onQuit != nil {
			go tray.onQuit()
		}
	}
}

// trayShow puts the icon in the notification area (once; later calls only
// show the balloon). onOpen / onQuit run on their own goroutine.
func trayShow(onOpen, onQuit func(), balloonTitle, balloon string) {
	tray.mu.Lock()
	if tray.hwnd == 0 {
		tray.onOpen, tray.onQuit = onOpen, onQuit
		tray.shown = make(chan struct{})
		go trayLoop()
		ch := tray.shown
		tray.mu.Unlock()
		<-ch
		tray.mu.Lock()
	}
	hwnd := tray.hwnd
	tray.mu.Unlock()
	if hwnd != 0 && balloon != "" {
		trayBalloon(balloonTitle, balloon)
	}
}

// trayBalloon shows a notification from the tray icon (no-op without one).
func trayBalloon(title, text string) {
	tray.mu.Lock()
	hwnd := tray.hwnd
	tray.mu.Unlock()
	if hwnd == 0 {
		return
	}
	nid := trayData(hwnd)
	nid.UFlags = nifInfo
	nid.DwInfoFlags = niifInfo
	utf16Fill(nid.SzInfoTitle[:], title)
	utf16Fill(nid.SzInfo[:], text)
	pShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// trayHide removes the icon (on exit).
func trayHide() {
	tray.mu.Lock()
	hwnd := tray.hwnd
	tray.hwnd = 0
	tray.mu.Unlock()
	if hwnd != 0 {
		pPostMessage.Call(hwnd, wmTrayClose, 0, 0)
	}
}

func trayLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hinst, _, _ := pGetModuleHandle.Call(0)
	if tray.wndProcCB == 0 {
		tray.wndProcCB = syscall.NewCallback(trayWndProc)
	}
	cls, _ := syscall.UTF16PtrFromString("PalaTermTray")
	wc := wndClassEx{LpfnWndProc: tray.wndProcCB, HInstance: hinst, LpszClassName: cls}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, 0, 0, hinst, 0)
	tb, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	created, _, _ := pRegisterWindowMessage.Call(uintptr(unsafe.Pointer(tb)))
	cx, _, _ := pGetSystemMetrics.Call(smCxSmIcon)
	cy, _, _ := pGetSystemMetrics.Call(smCySmIcon)
	// The app icon is resource #1 (build/windows/winres.json).
	icon, _, _ := pLoadImage.Call(hinst, 1, imageIcon, cx, cy, 0)

	tray.mu.Lock()
	tray.hwnd, tray.icon, tray.created = hwnd, icon, uint32(created)
	tray.mu.Unlock()
	// The icon is in the notification area before trayShow returns (its
	// balloon would otherwise go to an icon that does not exist yet).
	if hwnd != 0 {
		trayAdd()
	}
	close(tray.shown)
	if hwnd == 0 {
		return
	}
	var m winMsg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}
