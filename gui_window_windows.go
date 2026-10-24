//go:build windows

package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// The GUI window ("PalaTerm.exe --gui"): the device's web GUI in a window of
// its own, logged in with the device's credentials. The main window sends
// one JSON line on stdin (guiLaunch) and keeps the pipe open; the window
// closes when the pipe does (ロック, exit). A certificate seen for the first
// time is reported on stdout (guiEventCert) for the vault.

// The window, message-loop and menu procedures (RegisterClassEx, CreateWindowEx,
// GetMessage, PostMessage, AppendMenu …) are the p* ones of tray_windows.go,
// winfocus.go and winplace.go; only what no other window needs is here.
var (
	procUpdateWindow                  = user32.NewProc("UpdateWindow")
	procCreateMenu                    = user32.NewProc("CreateMenu")
	procGetMenu                       = user32.NewProc("GetMenu")
	procDeleteMenu                    = user32.NewProc("DeleteMenu")
	procDrawMenuBar                   = user32.NewProc("DrawMenuBar")
	procSetWindowTextW                = user32.NewProc("SetWindowTextW")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procLoadIconW                     = user32.NewProc("LoadIconW")
	procMessageBoxW                   = user32.NewProc("MessageBoxW")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
)

const (
	wmSize     = 0x0005
	wmSetFocus = 0x0007
	wmCommand  = 0x0111

	wsOverlappedWindow = 0x00CF0000
	cwUseDefault       = 0x80000000
	swShow             = 5
	idcArrow           = 32512

	menuBrowser = 1
	menuReload  = 2
	menuRelogin = 3
	menuAllow   = 4
	menuBack    = 5
	menuForward = 6
	menuCopyURL = 7

	wmGUINavigate = 0x8000 + 20

	// ICoreWebView2 history: a same-origin pop-up (FortiGate's CLI console,
	// a help page) replaces the page in this window, and the menu is the
	// only way back a user can see (Alt+← works but is written nowhere).
	slotGetCanGoBack    = 38 // ICoreWebView2::get_CanGoBack
	slotGetCanGoForward = 39
	slotGoBack          = 40
	slotGoForward       = 41

	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procGetDpiForWindow  = user32.NewProc("GetDpiForWindow")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
)

// setClipboardText puts s on the clipboard (the menu's URL をコピー; the
// page's navigator.clipboard needs a secure context an http GUI lacks).
func setClipboardText(hwnd windows.HWND, s string) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}
	if r, _, _ := procOpenClipboard.Call(uintptr(hwnd)); r == 0 {
		return
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	n := uintptr(len(u) * 2)
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, n)
	if h == 0 {
		return
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return
	}
	// p is memory the system handed out, reinterpreted in place (not a
	// converted Go pointer), as comCall does.
	dst := *(*unsafe.Pointer)(unsafe.Pointer(&p))
	copy(unsafe.Slice((*uint16)(dst), len(u)), u)
	procGlobalUnlock.Call(h)
	procSetClipboardData.Call(cfUnicodeText, h) // the system owns h from here
}

func u16(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

// guiProbeArgs is set only by the GUI-test build (gui_probe_hook.go).
var guiProbeArgs string

// guiWin is the one GUI window of this process.
type guiWin struct {
	cfg      guiLaunch
	hwnd     windows.HWND
	chromium *edge.Chromium
	base     *url.URL // where the window was opened (scheme://host:port)

	mu sync.Mutex
	// autoSent: the login was sent automatically — once per window, until
	// the menu's ログインし直す (a wrong password must not be retried into
	// a lockout).
	autoSent bool
	// formSeen: a login form was found and filled in (sent or not) since the
	// window opened or the last ログインし直す.
	formSeen bool
	// loggedIn: a page of the device without a login form came after the
	// login form: from then on nothing is filled in or clicked (a password
	// field on a settings page is not a login). Only after a form was seen:
	// a slow login page that is still empty when the script looks is not a
	// logged-in session.
	loggedIn bool
	// tofuNow: the certificate was seen for the first time in this window
	// and recorded: the login is filled in but not sent. swappedNow: the
	// pinned self-signed certificate gave way to one the browser trusts;
	// filled in, not sent, either.
	tofuNow    bool
	swappedNow bool
	// certErr: the device's address had a certificate error in this window
	// (none on https = the browser trusted it).
	certErr bool
	// badCert is the fingerprint refused as different from the pin; the
	// menu's 証明書の変更を許可 accepts it. badShown: its page is up.
	badCert  string
	badShown bool
	// noCert: the device's certificate could not be read (no fingerprint to
	// check), so the page was refused; said so instead of a blank window.
	noCert    bool
	allowMenu bool    // 証明書の変更を許可 is on the menu
	c14       uintptr // ICoreWebView2_14 (certificate errors), 0 if the runtime is older
	ready     bool
	dataDir   string // this window's own WebView2 profile, removed on exit
	// activeSent: when the last ACTIVE line went to the main window.
	activeSent time.Time
}

func runGUIWindow() {
	runtime.LockOSThread()
	in := bufio.NewReader(os.Stdin)
	line, err := in.ReadString('\n')
	var cfg guiLaunch
	if err != nil || json.Unmarshal([]byte(line), &cfg) != nil || cfg.URL == "" {
		return
	}
	base, err := url.Parse(cfg.URL)
	if err != nil {
		return
	}
	w := &guiWin{cfg: cfg, base: base}
	if cfg.Lang == "en" {
		os.Setenv("PALATERM_LANG", "en")
	}
	_ = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	// PER_MONITOR_AWARE_V2 (-4); the export is Windows 10 1703+, and calling
	// a missing one panics.
	if procSetProcessDpiAwarenessContext.Find() == nil {
		procSetProcessDpiAwarenessContext.Call(^uintptr(3))
	}
	if err := w.create(); err != nil {
		w.alert(err.Error())
		return
	}
	// The main window closing the pipe (ロック, exit) closes this window.
	go func() {
		_, _ = in.ReadString('\n')
		pPostMessage.Call(uintptr(w.hwnd), wmClose, 0, 0)
	}()
	var m winMsg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	w.removeProfile()
}

// guiProfileRoot holds the GUI windows' WebView2 profiles: one per window,
// removed when it closes, so no device session (cookies, cache, form data)
// outlives the window — a later window, or anyone with a shell on this PC
// while PalaTerm is locked, finds nothing to reuse.
func guiProfileRoot() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "PalaTerm", "GUI")
}

// removeProfile deletes this window's profile once its browser processes
// have let go of the files (they exit just after the window), and any left
// behind by a window that did not get to (killed): those still in use by
// another open window fail to delete and stay.
func (w *guiWin) removeProfile() {
	if w.dataDir == "" {
		return
	}
	for i := 0; i < 40; i++ {
		if os.RemoveAll(w.dataDir) == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	cleanStaleGUIProfiles()
}

// cleanStaleGUIProfiles deletes the profiles left behind by GUI windows
// that were killed: only those whose process is gone — another open
// window's files must not be touched. Run by a closing GUI window and by
// the main window at startup.
func cleanStaleGUIProfiles() {
	if ents, err := os.ReadDir(guiProfileRoot()); err == nil {
		for _, e := range ents {
			var pid uint32
			var ns int64
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "w-") {
				continue
			}
			if n, _ := fmt.Sscanf(e.Name(), "w-%d-%d", &pid, &ns); n != 2 || pid == uint32(os.Getpid()) || processAlive(pid) {
				continue
			}
			_ = os.RemoveAll(filepath.Join(guiProfileRoot(), e.Name()))
		}
	}
}

// processAlive reports that a process with this ID is running.
func processAlive(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// guiWinState names the GUI window's remembered size
// (%LOCALAPPDATA%\PalaTerm\gui-window.json, see loadWinSize); its default
// is guiWinSize (winfit.go), in logical (96-dpi) pixels.
const guiWinState = "gui-window"

// dpi is the window's DPI (96 = 100 %).
func (w *guiWin) dpi() int32 { return windowDPI(uintptr(w.hwnd)) }

// place sizes the window for its monitor's scaling (the remembered size,
// else the default) and centres it over the main window, on the main
// window's monitor, like the terminal windows (see winplace.go).
func (w *guiWin) place() {
	lw, lh, ok := loadWinSize(guiWinState, guiWinSize)
	if !ok {
		lw, lh = guiWinSize.W, guiWinSize.H
	}
	parent, hasParent := parseRect(os.Getenv(parentRectEnv))
	const monitorDefaultToNearest = 2
	var mon uintptr
	if hasParent {
		mon, _, _ = pMonitorFromRect.Call(uintptr(unsafe.Pointer(&parent)), monitorDefaultToNearest)
	} else {
		mon, _, _ = pMonitorFromWindw.Call(uintptr(w.hwnd), monitorDefaultToNearest)
	}
	mi, ok := monitorWork(mon)
	if !ok {
		return
	}
	// Move onto the parent's monitor first: the DPI read below is that
	// monitor's once the window is there.
	const swpNoSize, swpNoZOrder, swpNoActivate = 0x0001, 0x0004, 0x0010
	pSetWindowPos.Call(uintptr(w.hwnd), 0, uintptr(mi.Work.L), uintptr(mi.Work.T), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	scale := float64(w.dpi()) / 96
	pw, ph := int32(float64(lw)*scale), int32(float64(lh)*scale)
	if pw > mi.Work.R-mi.Work.L {
		pw = mi.Work.R - mi.Work.L
	}
	if ph > mi.Work.B-mi.Work.T {
		ph = mi.Work.B - mi.Work.T
	}
	over := parent
	if !hasParent {
		over = mi.Work
	}
	x, y := centreIn(over, mi.Work, pw, ph)
	pSetWindowPos.Call(uintptr(w.hwnd), 0, uintptr(x), uintptr(y), uintptr(pw), uintptr(ph), swpNoZOrder|swpNoActivate)
}

// rememberSize stores the window's logical size for the next GUI window —
// not while it is maximised or minimised: the frame then measures the whole
// work area (or nothing), and every later GUI window opened that large.
func (w *guiWin) rememberSize() {
	wp := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{}))}
	if ok, _, _ := pGetWindowPlacement.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&wp))); ok == 0 {
		return
	}
	if wp.ShowCmd == swShowMaximized || wp.ShowCmd == swShowMinimized {
		return
	}
	var r rect
	if ok, _, _ := pGetWindowRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	scale := float64(w.dpi()) / 96
	saveWinSize(guiWinState, guiWinSize, int(float64(r.R-r.L)/scale), int(float64(r.B-r.T)/scale))
}

func (w *guiWin) alert(msg string) {
	procMessageBoxW.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(u16(msg))), uintptr(unsafe.Pointer(u16("PalaTerm"))), 0x10)
}

func (w *guiWin) title(extra string) {
	t := "PalaTerm - GUI - " + w.cfg.Title
	if extra != "" {
		t += " - " + extra
	}
	procSetWindowTextW.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(u16(t))))
}

func (w *guiWin) create() error {
	var inst windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &inst)
	cls := u16("PalaTermGUI")
	cur, _, _ := procLoadCursorW.Call(0, idcArrow)
	icon, _, _ := procLoadIconW.Call(uintptr(inst), 1) // the exe's icon (winres RT_GROUP_ICON #1)
	wc := wndClassEx{
		LpfnWndProc:   windows.NewCallback(w.wndProc),
		HInstance:     uintptr(inst),
		HCursor:       cur,
		HIcon:         icon,
		LpszClassName: cls,
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassEx: %v", err)
	}
	menu, _, _ := procCreateMenu.Call()
	add := func(id int, ja, en string) {
		pAppendMenu.Call(menu, mfString, uintptr(id), uintptr(unsafe.Pointer(u16(winWord(ja, en)))))
	}
	add(menuBack, "← 戻る", "← Back")
	add(menuForward, "進む →", "Forward →")
	add(menuReload, "再読み込み", "Reload")
	add(menuRelogin, "ログインし直す", "Log in again")
	add(menuCopyURL, "URL をコピー", "Copy URL")
	add(menuBrowser, "既定のブラウザで開く（自動ログインなし）", "Open in default browser (no auto login)")
	// Created hidden, then sized for the monitor's scaling and placed over
	// the main window (the other child windows do the same; this one used
	// to open at Windows' default spot on the primary monitor, 1280×860
	// physical pixels — a 640×430 window on a 4K screen at 200 %).
	hwnd, _, err := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("PalaTerm"))),
		wsOverlappedWindow, cwUseDefault, cwUseDefault, uintptr(guiWinSize.W), uintptr(guiWinSize.H), 0, menu, uintptr(inst), 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowEx: %v", err)
	}
	w.hwnd = windows.HWND(hwnd)
	w.title("")
	w.place()
	pShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)

	c := edge.NewChromium()
	w.dataDir = filepath.Join(guiProfileRoot(), fmt.Sprintf("w-%d-%d", os.Getpid(), time.Now().UnixNano()))
	c.DataPath = w.dataDir
	if guiProbeArgs != "" {
		c.AdditionalBrowserArgs = []string{guiProbeArgs}
	}
	c.SetErrorCallback(func(err error) { w.alert("WebView2: " + err.Error()); os.Exit(1) })
	c.MessageCallback = w.onMessage
	c.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) { w.onNavigated() }
	w.chromium = c
	c.Embed(hwnd)
	c.Resize()
	// No DevTools (a typed password would be one click away), no autofill or
	// saved passwords of the browser's own.
	if st, err := c.GetSettings(); err == nil && st != nil {
		_ = st.PutAreDevToolsEnabled(false)
		if s4 := st.GetICoreWebView2Settings4(); s4 != nil {
			_ = s4.PutIsGeneralAutofillEnabled(false)
			_ = s4.PutIsPasswordAutosaveEnabled(false)
		}
	}
	w.hook()
	c.Init(guiPageScript(jsOrigin(w.base)))
	w.mu.Lock()
	w.ready = true
	w.mu.Unlock()
	w.open()
	c.Focus()
	return nil
}

// The WebView2 events the edge wrapper does not offer, called through the
// COM vtables directly. (go-webview2's pkg/webview2 cannot be used: its
// package init panics on Go 1.26 — a callback signature NewCallback no
// longer takes — and its ICoreWebView2_14 vtable leaves out the inherited
// methods.) Slot numbers count IUnknown's three first.
const (
	slotGetSource                  = 4   // ICoreWebView2::get_Source
	slotAddNewWindowRequested      = 44  // ICoreWebView2::add_NewWindowRequested
	slotNewWinArgsGetUri           = 3   // ICoreWebView2NewWindowRequestedEventArgs::get_Uri
	slotNewWinArgsPutHandled       = 6   // …::put_Handled
	slotCertArgsGetRequestUri      = 4   // …ServerCertificateErrorDetectedEventArgs::get_RequestUri
	slotAddServerCertErrorDetected = 106 // ICoreWebView2_14: 3 + 58 (ICoreWebView2) + 45 (_2…_13)
	slotClearServerCertErrorActs   = 108 // ICoreWebView2_14::ClearServerCertificateErrorActions
	slotCertArgsGetCert            = 5   // …ServerCertificateErrorDetectedEventArgs::get_ServerCertificate
	slotCertArgsPutAction          = 7   // …::put_Action
	slotCertToPem                  = 9   // ICoreWebView2Certificate::ToPemEncoding
	certActionAllow                = 0   // COREWEBVIEW2_SERVER_CERTIFICATE_ERROR_ACTION_ALWAYS_ALLOW
	certActionCancel               = 1
)

var iidICoreWebView2_14 = windows.GUID{Data1: 0x6daa4f10, Data2: 0x4a90, Data3: 0x4753, Data4: [8]byte{0x88, 0x98, 0x77, 0xc5, 0xdf, 0x53, 0x41, 0x65}}

// comCall calls method slot of the COM object at this.
func comCall(this uintptr, slot int, args ...uintptr) uintptr {
	// this is a COM pointer from outside Go's heap: reinterpreted in place,
	// not converted, so vet's uintptr→Pointer rule does not apply.
	obj := *(*unsafe.Pointer)(unsafe.Pointer(&this))
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{this}, args...)...)
	return r
}

// comString calls a getter that returns a CoTaskMem-allocated string.
func comString(this uintptr, slot int) string {
	var p *uint16
	if hr := comCall(this, slot, uintptr(unsafe.Pointer(&p))); int32(hr) < 0 || p == nil {
		return ""
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(p))
	return windows.UTF16PtrToString(p)
}

// comHandler is an event handler object WebView2 can hold: IUnknown plus
// Invoke(sender, args). Every handler shares one vtable: Windows callbacks
// are never freed and a process may make about 2000 (see winfocus.go), so
// the four are made once, and Invoke finds its handler by the object's
// address.
type comHandler struct {
	vtbl   *[4]uintptr
	invoke func(sender, args uintptr)
}

var (
	comHandlerMu   sync.Mutex
	comHandlers    = map[uintptr]*comHandler{} // this → handler; kept reachable: WebView2 holds raw pointers
	comHandlerVtbl [4]uintptr
	comHandlerOnce sync.Once
)

// comHandlerInit makes the shared vtable (in the GUI window process only:
// the main window never needs these callback slots).
func comHandlerInit() {
	comHandlerVtbl = [4]uintptr{
		windows.NewCallback(comQueryInterface),
		windows.NewCallback(comAddRef),
		windows.NewCallback(comRelease),
		windows.NewCallback(comInvoke),
	}
}

// comQueryInterface: the object is what WebView2 asks for (it only asks for
// IUnknown or the handler interface it registered).
func comQueryInterface(this, riid, out uintptr) uintptr {
	if out == 0 {
		return 0x80004003 // E_POINTER
	}
	dst := *(*unsafe.Pointer)(unsafe.Pointer(&out))
	*(*uintptr)(dst) = this
	return 0
}

func comAddRef(this uintptr) uintptr  { return 1 }
func comRelease(this uintptr) uintptr { return 1 }

func comInvoke(this, sender, args uintptr) uintptr {
	comHandlerMu.Lock()
	h := comHandlers[this]
	comHandlerMu.Unlock()
	if h != nil {
		h.invoke(sender, args)
	}
	return 0
}

func newComHandler(invoke func(sender, args uintptr)) *comHandler {
	comHandlerOnce.Do(comHandlerInit)
	h := &comHandler{vtbl: &comHandlerVtbl, invoke: invoke}
	comHandlerMu.Lock()
	comHandlers[uintptr(unsafe.Pointer(h))] = h
	comHandlerMu.Unlock()
	return h
}

// core is the WebView2 core's interface pointer (the _3 one the wrapper
// hands out; every later interface extends the same vtable).
func (w *guiWin) core() uintptr {
	return uintptr(unsafe.Pointer(w.chromium.GetICoreWebView2_3()))
}

// source is the address the window shows.
func (w *guiWin) source() string {
	if c := w.core(); c != 0 {
		return comString(c, slotGetSource)
	}
	return ""
}

// hook adds the handlers the edge wrapper does not: certificate errors
// (self-signed GUIs open without the browser's warning page, against the
// recorded certificate) and new windows (kept in this window, where the
// certificate check and the page script run, or sent to the browser).
func (w *guiWin) hook() {
	core := w.core()
	if core == 0 {
		return
	}
	var token int64
	nw := newComHandler(w.newWindow)
	comCall(core, slotAddNewWindowRequested, uintptr(unsafe.Pointer(nw)), uintptr(unsafe.Pointer(&token)))
	var c14 uintptr
	if hr := comCall(core, 0, uintptr(unsafe.Pointer(&iidICoreWebView2_14)), uintptr(unsafe.Pointer(&c14))); int32(hr) >= 0 && c14 != 0 {
		cert := newComHandler(w.certificateError)
		comCall(c14, slotAddServerCertErrorDetected, uintptr(unsafe.Pointer(cert)), uintptr(unsafe.Pointer(&token)))
		w.c14 = c14
	}
}

// open navigates to the GUI. WebView2 remembers certificate decisions for
// as long as its browser process runs, so they are cleared first: each
// window checks the certificate against the pin again. A runtime too old
// to report certificate errors gets no https GUI at all (no pin check).
func (w *guiWin) open() {
	w.mu.Lock()
	w.badShown, w.noCert = false, false
	w.mu.Unlock()
	if w.c14 == 0 {
		if w.base.Scheme == "https" {
			w.chromium.NavigateToString(guiNotice(winWord(
				"この PC の WebView2 ランタイムが古いため、証明書を確かめられず https の GUI を開けません。Windows Update で Microsoft Edge WebView2 Runtime を更新するか、メニューの「既定のブラウザで開く」を使ってください。",
				"This PC's WebView2 runtime is too old to check certificates, so the https GUI is not opened. Update the Microsoft Edge WebView2 Runtime, or use \"Open in default browser\" in the menu.")))
			return
		}
		w.chromium.Navigate(w.cfg.URL)
		return
	}
	done := newComHandler(func(_, _ uintptr) { w.chromium.Navigate(w.cfg.URL) })
	if hr := comCall(w.c14, slotClearServerCertErrorActs, uintptr(unsafe.Pointer(done))); int32(hr) < 0 {
		w.chromium.Navigate(w.cfg.URL)
	}
}

// sameOrigin reports that raw is the device's GUI (scheme, host and port
// the window was opened at).
func (w *guiWin) sameOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && originKey(u) == originKey(w.base)
}

// originKey is u's origin in one spelling — "scheme://host:port", the host
// lower-cased and an IP literal in its canonical form, the port written
// out even when it is the scheme's default — so the device's address
// compares equal to the browser's spelling of it. A device registered as
// 2001:db8:0:0:0:0:0:1 is navigated as [2001:db8::1] by the browser;
// comparing the raw strings refused its certificate and never logged in.
func originKey(u *url.URL) string {
	if u == nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host, port := normalHost(u.Hostname()), u.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return scheme + "://" + host + ":" + port
}

// jsOrigin is u's origin as the page's location.origin spells it: scheme and
// host (an IPv6 literal in brackets, canonical), the port only when it is
// not the scheme's default.
func jsOrigin(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host, port := normalHost(u.Hostname()), u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		return scheme + "://" + host + ":" + port
	}
	return scheme + "://" + host
}

// normalHost is a host name lower-cased, or an IP literal in its canonical
// form (IPv6 compressed, in brackets).
func normalHost(h string) string {
	if ip := net.ParseIP(h); ip != nil {
		if ip.To4() != nil {
			return ip.String()
		}
		return "[" + ip.String() + "]"
	}
	return strings.ToLower(h)
}

// certificateError decides a certificate error against the device's pin —
// only for the device's own address: another host's error (a resource the
// page loads from elsewhere) is refused and never touches the pin.
func (w *guiWin) certificateError(_, args uintptr) {
	if !w.sameOrigin(comString(args, slotCertArgsGetRequestUri)) {
		comCall(args, slotCertArgsPutAction, certActionCancel)
		return
	}
	fp := ""
	var cert uintptr
	if hr := comCall(args, slotCertArgsGetCert, uintptr(unsafe.Pointer(&cert))); int32(hr) >= 0 && cert != 0 {
		if p := comString(cert, slotCertToPem); p != "" {
			if b, _ := pem.Decode([]byte(p)); b != nil {
				sum := sha256.Sum256(b.Bytes)
				fp = hex.EncodeToString(sum[:])
			}
		}
		comCall(cert, 2) // Release
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.certErr = true
	action := uintptr(certActionCancel)
	switch {
	case fp == "":
		// No certificate to check against the pin: refused, and said so
		// (onNavigated) rather than a blank window.
		w.noCert = true
	case w.cfg.Pin == "":
		// First sight (trust on first use): recorded, opened — but nothing
		// is sent automatically on a certificate nobody has seen before.
		w.cfg.Pin, w.tofuNow = fp, true
		fmt.Printf("%s %s\n", guiEventCert, fp)
		action = certActionAllow
	case w.cfg.Pin == fp:
		action = certActionAllow
	default:
		// Different from the pin — or the device once had a certificate the
		// browser trusted ("ca") and now presents one it does not.
		w.badCert = fp
	}
	comCall(args, slotCertArgsPutAction, action)
}

// newWindow keeps a page's new window (target=_blank, window.open) in this
// window when it is the device's GUI, and sends anything else to the
// default browser: a WebView2 window of its own would have neither the
// certificate check nor the page script.
func (w *guiWin) newWindow(_, args uintptr) {
	uri := comString(args, slotNewWinArgsGetUri)
	// A window opened empty (about:blank) and written into by the page's
	// script has no address to judge; handling it would make it vanish, so
	// WebView2 opens it as it would on its own.
	if uri == "" || strings.HasPrefix(strings.ToLower(uri), "about:blank") {
		return
	}
	comCall(args, slotNewWinArgsPutHandled, 1)
	if w.sameOrigin(uri) {
		guiNavigate.Store(uri)
		pPostMessage.Call(uintptr(w.hwnd), wmGUINavigate, 0, 0)
		return
	}
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		_ = windows.ShellExecute(0, u16("open"), u16(uri), nil, nil, 1)
	}
}

var guiNavigate syncString

type syncString struct {
	mu sync.Mutex
	s  string
}

func (s *syncString) Store(v string) { s.mu.Lock(); s.s = v; s.mu.Unlock() }
func (s *syncString) Take() string   { s.mu.Lock(); defer s.mu.Unlock(); v := s.s; s.s = ""; return v }

// guiNotice is a page of PalaTerm's own in the window.
func guiNotice(html string) string {
	return `<html><body style="font-family:sans-serif;padding:24px">` + html + `</body></html>`
}

func (w *guiWin) wndProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case wmSize:
		w.mu.Lock()
		ok := w.ready
		w.mu.Unlock()
		if ok {
			w.chromium.Resize()
		}
		return 0
	case wmSetFocus:
		w.mu.Lock()
		ok := w.ready
		w.mu.Unlock()
		if ok {
			w.chromium.Focus()
		}
		return 0
	case wmGUINavigate:
		if t := guiNavigate.Take(); t != "" {
			w.chromium.Navigate(t)
		}
		return 0
	case wmCommand:
		switch int(wp & 0xffff) {
		case menuBack:
			if c := w.core(); c != 0 {
				comCall(c, slotGoBack)
			}
		case menuForward:
			if c := w.core(); c != 0 {
				comCall(c, slotGoForward)
			}
		case menuCopyURL:
			if s := w.source(); s != "" {
				setClipboardText(w.hwnd, s)
				w.title(winWord("URL をコピーしました", "URL copied"))
			}
		case menuBrowser:
			src := w.cfg.URL
			if s := w.source(); strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
				src = s
			}
			_ = windows.ShellExecute(0, u16("open"), u16(src), nil, nil, 1)
		case menuReload:
			w.chromium.Eval("location.reload()")
		case menuRelogin:
			// A fresh start at the login page: the login may be sent once
			// more, and a certificate refused before is judged again (its
			// page comes back if it is still refused; open clears the
			// remembered refusal, as 証明書の変更を許可 does).
			w.mu.Lock()
			w.autoSent, w.formSeen, w.loggedIn = false, false, false
			w.badCert, w.badShown = "", false
			w.mu.Unlock()
			w.title("")
			w.open()
		case menuAllow:
			w.mu.Lock()
			fp := w.badCert
			w.badCert = ""
			if fp != "" {
				w.cfg.Pin = fp
			}
			w.mu.Unlock()
			if fp != "" {
				fmt.Printf("%s %s\n", guiEventCert, fp)
				w.title("")
				if menu, _, _ := procGetMenu.Call(uintptr(w.hwnd)); menu != 0 && w.allowMenu {
					procDeleteMenu.Call(menu, menuAllow, 0) // MF_BYCOMMAND
					procDrawMenuBar.Call(uintptr(w.hwnd))
					w.allowMenu = false
				}
				w.open() // the refusal is remembered too: cleared first
			}
		}
		return 0
	case wmClose:
		w.rememberSize()
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

// onNavigated shows why a page was refused for its certificate (once: the
// notice page completes a navigation of its own).
func (w *guiWin) onNavigated() {
	w.mu.Lock()
	bad, shown, nocert := w.badCert, w.badShown, w.noCert
	if bad != "" || nocert {
		w.badShown = true
	}
	w.mu.Unlock()
	if shown {
		return
	}
	if nocert {
		w.title(winWord("証明書を読み取れませんでした", "certificate could not be read"))
		w.chromium.NavigateToString(guiNotice(winWord(
			`<h2 style="color:#c00">証明書を読み取れませんでした</h2><p>この機器の GUI の証明書を読み取れなかったため、記録と照合できず、開くのを止めました。メニューの「ログインし直す」でもう一度試すか、「既定のブラウザで開く」を使ってください。</p>`,
			`<h2 style="color:#c00">The certificate could not be read</h2><p>This device's GUI certificate could not be read, so it could not be checked against the record and the page was not opened. Try "Log in again" in the menu, or use "Open in default browser".</p>`)))
		return
	}
	// "ca": the page loaded fine with a trusted certificate; the notice is
	// in the title and the menu, the page stays.
	if bad == "" || bad == "ca" {
		return
	}
	w.title(winWord("証明書が前回と異なります", "certificate changed"))
	html := winWord(
		`<h2 style="color:#c00">証明書が前回と異なります</h2><p>この機器の GUI が、前回記録した証明書と違う証明書を出しました。機器の入れ替えや証明書の更新なら問題ありませんが、通信の途中で別のものに差し替えられている可能性もあるため、開くのを止めました。</p><p>心当たりがある場合だけ、メニューの「証明書の変更を許可」を押してください。</p>`,
		`<h2 style="color:#c00">The certificate has changed</h2><p>This device's GUI presented a different certificate from the one recorded last time. A replaced device or a renewed certificate is fine, but it could also be something in between, so the page was not opened.</p><p>Only if you expect the change, choose "Allow the new certificate" in the menu.</p>`)
	w.chromium.NavigateToString(guiNotice(html + `<p style="color:#666;font-size:12px">SHA-256: ` + bad + `</p>`))
	w.showAllowMenu()
}

// showAllowMenu adds 証明書の変更を許可 to the menu bar (once).
func (w *guiWin) showAllowMenu() {
	menu, _, _ := procGetMenu.Call(uintptr(w.hwnd))
	if menu != 0 && !w.allowMenu {
		w.allowMenu = true
		pAppendMenu.Call(menu, mfString, menuAllow, uintptr(unsafe.Pointer(u16(winWord("証明書の変更を許可", "Allow the new certificate")))))
		procDrawMenuBar.Call(uintptr(w.hwnd))
	}
}

// onMessage answers the page script (guiPageScript). Only pages of the
// device's own address count.
func (w *guiWin) onMessage(msg string) {
	if msg == "pt:active" {
		// The user is here: told to the main window (its idle lock would
		// otherwise close this window from under them). At most every 5 s.
		w.mu.Lock()
		due := time.Since(w.activeSent) >= 5*time.Second
		if due {
			w.activeSent = time.Now()
		}
		w.mu.Unlock()
		if due {
			fmt.Println(guiEventActive)
		}
		return
	}
	if !w.onDevicePage() {
		return
	}
	w.mu.Lock()
	switch msg {
	case "pt:login":
		if w.loggedIn {
			w.mu.Unlock()
			return
		}
		// Sent on its own only once per window, and not on a certificate seen
		// for the first time just now; a plain https page without any
		// certificate error is one the browser trusted ("ca" recorded).
		if w.base.Scheme == "https" && !w.certErr && w.cfg.Pin == "" {
			w.cfg.Pin = "ca"
			fmt.Printf("%s %s\n", guiEventCert, "ca")
		}
		// The pin is a self-signed fingerprint but this page came with no
		// certificate error: the device now presents a certificate this PC
		// trusts — a renewal, or something in between with a CA of its own.
		// Filled in, not sent; the user decides (and 証明書の変更を許可
		// records the new state).
		swapped := w.base.Scheme == "https" && !w.certErr && w.cfg.Pin != "" && w.cfg.Pin != "ca"
		submit := w.loginSubmit(swapped)
		if submit {
			w.autoSent = true
		}
		w.formSeen = true
		tofu, noCred := w.tofuNow, !w.hasCredentials()
		if swapped {
			w.badCert, w.swappedNow = "ca", true
		}
		w.mu.Unlock()
		w.chromium.Eval(guiFillScript(w.cfg.User, w.cfg.Password, submit))
		switch {
		case submit:
			w.title("") // a "no login form" note from an earlier page goes
		case swapped:
			w.title(winWord("前回は自己署名の証明書でしたが、今回はこの PC が信頼する証明書です。心当たりがあればメニューの「証明書の変更を許可」を押してください", "the certificate changed from self-signed to a trusted one; allow it in the menu if expected"))
			w.showAllowMenu()
		case tofu:
			w.title(winWord("初めて開く機器です。証明書を記録しました。内容を確かめて「ログイン」を押してください", "first time: certificate recorded; check and log in"))
		case noCred:
			w.title(winWord("ユーザー名かパスワードが登録されていないため自動ログインしません。手で入力してください", "no username or password recorded: log in by hand"))
		case !submit:
			w.title(winWord("自動ログインは1回だけです。内容を確かめてログインしてください", "auto login runs once; check and log in"))
		}
		return
	case "pt:nologin":
		// A page of the device without a login form after one was filled
		// in: logged in. Nothing more is filled in or clicked. (Before any
		// form was seen it is a login page still loading, or one the script
		// does not recognise — the next pt:login must still be answered.)
		if w.formSeen {
			w.loggedIn = true
		}
		noForm := !w.formSeen
		w.mu.Unlock()
		if noForm {
			// The first page of the device, and no login form the script
			// recognises on it: said so, with the username, instead of a
			// blank login page the user cannot tell from "still looking".
			w.title(winWord(
				fmt.Sprintf("ログイン欄を見つけられませんでした。手で入力してください（ユーザー名: %s）", w.cfg.User),
				fmt.Sprintf("no login form found: log in by hand (username: %s)", w.cfg.User)))
			return
		}
		w.title("")
		return
	case "pt:accept":
		// A login disclaimer, before or right after the login only.
		ok := !w.loggedIn
		w.mu.Unlock()
		if ok {
			w.chromium.Eval(`window.__ptAcceptBtn && window.__ptAcceptBtn.click()`)
		}
		return
	}
	w.mu.Unlock()
}

// onDevicePage reports that the window shows the device's GUI, not some
// other site a link led to: the password goes nowhere else.
func (w *guiWin) onDevicePage() bool { return w.sameOrigin(w.source()) }

// hasCredentials: a username and a password to log in with.
func (w *guiWin) hasCredentials() bool { return w.cfg.User != "" && w.cfg.Password != "" }

// loginSubmit decides (under w.mu) whether the login form found now is
// pressed: once per window, not on a certificate seen for the first time
// or one that swapped to a trusted one, and never without both a username
// and a password — an empty one sent is a failed login the device counts
// towards locking that user out, and a GUI window opened a few times would
// have done just that.
func (w *guiWin) loginSubmit(swapped bool) bool {
	return !w.autoSent && !w.tofuNow && !swapped && w.hasCredentials()
}

// guiFillScript fills the login form the page script found (and presses
// its button when submit). Values are set the way typing does, so the
// GUIs built on a framework (React, Vue, Angular) see them.
func guiFillScript(user, pass string, submit bool) string {
	ju, _ := json.Marshal(user)
	jp, _ := json.Marshal(pass)
	return fmt.Sprintf(`(function(u,p,submit){var f=window.__ptFind&&window.__ptFind();if(!f)return;
function set(el,v){if(!el||v==="")return;var d=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),"value");if(d&&d.set)d.set.call(el,v);else el.value=v;
el.dispatchEvent(new Event("input",{bubbles:true}));el.dispatchEvent(new Event("change",{bubbles:true}));}
set(f.u,u);set(f.p,p);if(!submit)return;
setTimeout(function(){var b=f.btn;if(b){b.click();return;}
if(f.form){if(f.form.requestSubmit)f.form.requestSubmit();else f.form.submit();return;}
f.p.dispatchEvent(new KeyboardEvent("keydown",{key:"Enter",code:"Enter",keyCode:13,which:13,bubbles:true}));},150);
})(%s,%s,%v)`, ju, jp, submit)
}

// guiPageScript runs in every page of the window: on the device's own
// address (origin) only, it finds a login form — a page with exactly one
// password field (two or more: a password change, not a login) — and tells
// the window; it offers a login disclaimer's Accept to the window, which
// presses it only around the login; and it reports a page without a login
// form (logged in).
func guiPageScript(origin string) string {
	jo, _ := json.Marshal(origin)
	return `(function(){
if (window.top !== window) return;
// Activity (keys, mouse) is reported to PalaTerm, which postpones its idle
// lock while this window is in use (the lock would close it).
(function(){ var last=0; function act(){ var n=Date.now(); if(n-last<3000) return; last=n; try{ window.chrome.webview.postMessage("pt:active"); }catch(e){} }
["keydown","mousedown","mousemove","wheel","touchstart"].forEach(function(ev){ document.addEventListener(ev, act, {passive:true, capture:true}); }); })();
if (location.origin !== ` + string(jo) + `) return;
function vis(e){ if(!e) return false; var r=e.getBoundingClientRect(); var s=getComputedStyle(e); return r.width>0&&r.height>0&&s.visibility!=="hidden"&&s.display!=="none"; }
function q(sel){ var l=document.querySelectorAll(sel); for(var i=0;i<l.length;i++) if(vis(l[i])) return l[i]; return null; }
window.__ptFind = function(){
  var pws = [].filter.call(document.querySelectorAll("input[type=password]"), vis);
  if (pws.length !== 1) return null;
  var p = pws[0];
  var form = p.form || null, scope = form || document;
  var u = q("#username,#user,input[name=username],input[name=user],input[name=login],input[name=userName]");
  if (!u) {
    var c = scope.querySelectorAll("input:not([type]),input[type=text],input[type=email]");
    for (var i=0;i<c.length;i++) if (vis(c[i]) && (c[i].compareDocumentPosition(p) & 4)) u = c[i];
  }
  var btn = q("#login_button,#submit,button[type=submit],input[type=submit]");
  if (!btn) {
    var bs = scope.querySelectorAll("button,input[type=button],a.btn,[role=button]");
    for (var j=0;j<bs.length;j++){ var t=(bs[j].innerText||bs[j].value||"").trim(); if (vis(bs[j]) && /^(log ?in|sign ?in|login|ログイン|サインイン)$/i.test(t)) { btn=bs[j]; break; } }
  }
  return {u:u, p:p, form:form, btn:btn};
};
var sent=false, offered=false, start=Date.now();
function tick(){
  if (Date.now()-start > 60000) return;
  var f = window.__ptFind();
  if (f && !sent) { sent=true; window.chrome.webview.postMessage("pt:login"); }
  if (!f && sent) { sent=false; }
  if (!f && !offered && Date.now()-start < 15000 && /disclaimer|免責|同意|terms|notice/i.test(document.body ? document.body.innerText.slice(0,4000) : "")) {
    var bs=document.querySelectorAll("button,input[type=button],input[type=submit],a");
    for (var i=0;i<bs.length;i++){ var t=(bs[i].innerText||bs[i].value||"").trim(); if (vis(bs[i]) && /^(accept|i accept|agree|i agree|同意|同意する|承諾|承諾する)$/i.test(t)) { offered=true; window.__ptAcceptBtn=bs[i]; window.chrome.webview.postMessage("pt:accept"); break; } }
  }
  setTimeout(tick, 500);
}
function reportNoLogin(){ if (!window.__ptFind()) window.chrome.webview.postMessage("pt:nologin"); }
if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", function(){ tick(); setTimeout(reportNoLogin, 4000); });
else { tick(); setTimeout(reportNoLogin, 4000); }
})();`
}
