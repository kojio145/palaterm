//go:build windows

package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// The GUI window ("PalaTerm.exe --gui"): the device's web GUI in a window of
// its own, logged in with the device's credentials. The main window sends
// one JSON line on stdin (guiLaunch) and keeps the pipe open; the window
// closes when the pipe does (ロック, exit). A certificate seen for the first
// time is reported on stdout (guiEventCert) for the vault.

var (
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procUpdateWindow                  = user32.NewProc("UpdateWindow")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procCreateMenu                    = user32.NewProc("CreateMenu")
	procAppendMenuW                   = user32.NewProc("AppendMenuW")
	procSetMenu                       = user32.NewProc("SetMenu")
	procSetWindowTextW                = user32.NewProc("SetWindowTextW")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procLoadIconW                     = user32.NewProc("LoadIconW")
	procMessageBoxW                   = user32.NewProc("MessageBoxW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
)

const (
	wmSize        = 0x0005
	wmSetFocus    = 0x0007
	wmCommand     = 0x0111
	wmGUINavigate = 0x8000 + 20

	wsOverlappedWindow = 0x00CF0000
	cwUseDefault       = 0x80000000
	swShow             = 5
	idcArrow           = 32512

	menuBrowser = 1
	menuReload  = 2
	menuRelogin = 3
	menuAllow   = 4
)

type guiWndClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type guiMsg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
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
	// submitted: the login was sent automatically once and has not been
	// seen to succeed since; a login form showing again is then only
	// filled in (a wrong password must not be retried into a lockout).
	submitted bool
	// badCert is the fingerprint refused as different from the pin; the
	// menu's 証明書の変更を許可 accepts it.
	badCert   string
	allowMenu bool    // 証明書の変更を許可 is on the menu
	c14       uintptr // ICoreWebView2_14 (certificate errors), 0 if the runtime is older
	ready     bool
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
	procSetProcessDpiAwarenessContext.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4)
	if err := w.create(); err != nil {
		w.alert(err.Error())
		return
	}
	// The main window closing the pipe (ロック, exit) closes this window.
	go func() {
		_, _ = in.ReadString('\n')
		procPostMessageW.Call(uintptr(w.hwnd), wmClose, 0, 0)
	}()
	var m guiMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
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
	wc := guiWndClass{
		WndProc:   windows.NewCallback(w.wndProc),
		Instance:  inst,
		Cursor:    windows.Handle(cur),
		Icon:      windows.Handle(icon),
		ClassName: cls,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassEx: %v", err)
	}
	menu, _, _ := procCreateMenu.Call()
	add := func(id int, ja, en string) {
		procAppendMenuW.Call(menu, mfString, uintptr(id), uintptr(unsafe.Pointer(u16(winWord(ja, en)))))
	}
	add(menuBrowser, "既定のブラウザで開く", "Open in default browser")
	add(menuReload, "再読み込み", "Reload")
	add(menuRelogin, "ログインし直す", "Log in again")
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("PalaTerm"))),
		wsOverlappedWindow, cwUseDefault, cwUseDefault, 1280, 860, 0, menu, uintptr(inst), 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowEx: %v", err)
	}
	w.hwnd = windows.HWND(hwnd)
	w.title("")
	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)

	c := edge.NewChromium()
	c.DataPath = filepath.Join(os.Getenv("LOCALAPPDATA"), "PalaTerm", "GUI")
	if guiProbeArgs != "" {
		c.AdditionalBrowserArgs = []string{guiProbeArgs}
	}
	c.SetErrorCallback(func(err error) { w.alert("WebView2: " + err.Error()); os.Exit(1) })
	c.MessageCallback = w.onMessage
	c.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) { w.onNavigated() }
	w.chromium = c
	c.Embed(hwnd)
	c.Resize()
	w.hook()
	c.Init(guiPageScript)
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
	slotAddNavigationStarting      = 7   // ICoreWebView2::add_NavigationStarting
	slotAddServerCertErrorDetected = 106 // ICoreWebView2_14: 3 + 58 (ICoreWebView2) + 45 (_2…_13)
	slotClearServerCertErrorActs   = 108 // ICoreWebView2_14::ClearServerCertificateErrorActions
	slotCertArgsGetCert            = 5   // …ServerCertificateErrorDetectedEventArgs::get_ServerCertificate
	slotCertArgsPutAction          = 7   // …::put_Action
	slotCertToPem                  = 9   // ICoreWebView2Certificate::ToPemEncoding
	slotNavArgsGetUri              = 3   // ICoreWebView2NavigationStartingEventArgs::get_Uri
	slotNavArgsPutCancel           = 8   // …::put_Cancel
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
// Invoke(sender, args).
type comHandler struct {
	vtbl   *[4]uintptr
	invoke func(sender, args uintptr)
}

var comHandlers []*comHandler // kept reachable: WebView2 holds raw pointers

func newComHandler(invoke func(sender, args uintptr)) *comHandler {
	h := &comHandler{invoke: invoke}
	h.vtbl = &[4]uintptr{
		windows.NewCallback(func(this, riid, out uintptr) uintptr { return 0x80004002 }), // E_NOINTERFACE
		windows.NewCallback(func(this uintptr) uintptr { return 1 }),
		windows.NewCallback(func(this uintptr) uintptr { return 1 }),
		windows.NewCallback(func(this, sender, args uintptr) uintptr { h.invoke(sender, args); return 0 }),
	}
	comHandlers = append(comHandlers, h)
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
// recorded certificate) and navigations (a redirect to the device's own
// address is brought back through the forward).
func (w *guiWin) hook() {
	core := w.core()
	if core == 0 {
		return
	}
	var token int64
	nav := newComHandler(w.navigationStarting)
	comCall(core, slotAddNavigationStarting, uintptr(unsafe.Pointer(nav)), uintptr(unsafe.Pointer(&token)))
	var c14 uintptr
	if hr := comCall(core, 0, uintptr(unsafe.Pointer(&iidICoreWebView2_14)), uintptr(unsafe.Pointer(&c14))); int32(hr) >= 0 && c14 != 0 {
		cert := newComHandler(w.certificateError)
		comCall(c14, slotAddServerCertErrorDetected, uintptr(unsafe.Pointer(cert)), uintptr(unsafe.Pointer(&token)))
		w.c14 = c14
	}
}

// open navigates to the GUI. WebView2 remembers an allowed certificate
// error for as long as its browser process runs (shared by every GUI
// window), so the decisions are cleared first: each window checks the
// certificate against the pin again.
func (w *guiWin) open() {
	if w.c14 == 0 {
		w.chromium.Navigate(w.cfg.URL)
		return
	}
	done := newComHandler(func(_, _ uintptr) { w.chromium.Navigate(w.cfg.URL) })
	if hr := comCall(w.c14, slotClearServerCertErrorActs, uintptr(unsafe.Pointer(done))); int32(hr) < 0 {
		w.chromium.Navigate(w.cfg.URL)
	}
}

func (w *guiWin) certificateError(_, args uintptr) {
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
	pin := w.cfg.Pin
	w.mu.Unlock()
	action := uintptr(certActionCancel)
	switch {
	case fp == "":
	case pin == "" || pin == fp:
		if pin == "" {
			w.mu.Lock()
			w.cfg.Pin = fp
			w.mu.Unlock()
			fmt.Printf("%s %s %s\n", guiEventCert, w.cfg.PinKey, fp)
		}
		action = certActionAllow
	default:
		w.mu.Lock()
		w.badCert = fp
		w.mu.Unlock()
	}
	comCall(args, slotCertArgsPutAction, action)
}

func (w *guiWin) navigationStarting(_, args uintptr) {
	if w.cfg.Via == 0 || w.cfg.DeviceHost == "" {
		return
	}
	u, err := url.Parse(comString(args, slotNavArgsGetUri))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Hostname(), w.cfg.DeviceHost) ||
		strings.EqualFold(u.Host, w.base.Host) {
		return
	}
	// The device sent the window to its own address, which this PC cannot
	// reach: the same page through the forward instead.
	u.Scheme, u.Host = w.base.Scheme, w.base.Host
	comCall(args, slotNavArgsPutCancel, 1)
	guiNavigate.Store(u.String())
	procPostMessageW.Call(uintptr(w.hwnd), wmGUINavigate, 0, 0)
}

var guiNavigate syncString

type syncString struct {
	mu sync.Mutex
	s  string
}

func (s *syncString) Store(v string) { s.mu.Lock(); s.s = v; s.mu.Unlock() }
func (s *syncString) Take() string   { s.mu.Lock(); defer s.mu.Unlock(); v := s.s; s.s = ""; return v }

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
		case menuBrowser:
			src := w.cfg.URL
			if s := w.source(); strings.HasPrefix(s, "http") {
				src = s
			}
			_ = windows.ShellExecute(0, u16("open"), u16(src), nil, nil, 1)
		case menuReload:
			w.chromium.Eval("location.reload()")
		case menuRelogin:
			w.mu.Lock()
			w.submitted = false
			w.mu.Unlock()
			w.title("")
			w.chromium.Navigate(w.cfg.URL)
		case menuAllow:
			w.mu.Lock()
			fp := w.badCert
			w.badCert = ""
			if fp != "" {
				w.cfg.Pin = fp
			}
			w.mu.Unlock()
			if fp != "" {
				fmt.Printf("%s %s %s\n", guiEventCert, w.cfg.PinKey, fp)
				w.title("")
				w.open() // the refusal is remembered too: cleared first
			}
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

// onNavigated shows why a page was refused for its certificate.
func (w *guiWin) onNavigated() {
	w.mu.Lock()
	bad := w.badCert
	w.mu.Unlock()
	if bad == "" {
		return
	}
	w.title(winWord("証明書が前回と異なります", "certificate changed"))
	html := winWord(
		`<h2 style="color:#c00">証明書が前回と異なります</h2><p>この機器の GUI が、前回記録した証明書と違う証明書を出しました。機器の入れ替えや証明書の更新なら問題ありませんが、通信の途中で別のものに差し替えられている可能性もあるため、開くのを止めました。</p><p>心当たりがある場合だけ、メニューの「証明書の変更を許可」を押してください。</p>`,
		`<h2 style="color:#c00">The certificate has changed</h2><p>This device's GUI presented a different certificate from the one recorded last time. A replaced device or a renewed certificate is fine, but it could also be something in between, so the page was not opened.</p><p>Only if you expect the change, choose "Allow the new certificate" in the menu.</p>`)
	w.chromium.NavigateToString(`<html><body style="font-family:sans-serif;padding:24px">` + html + `<p style="color:#666;font-size:12px">SHA-256: ` + bad + `</p></body></html>`)
	menu, _, _ := user32.NewProc("GetMenu").Call(uintptr(w.hwnd))
	if menu != 0 && !w.allowMenu {
		w.allowMenu = true
		procAppendMenuW.Call(menu, mfString, menuAllow, uintptr(unsafe.Pointer(u16(winWord("証明書の変更を許可", "Allow the new certificate")))))
		user32.NewProc("DrawMenuBar").Call(uintptr(w.hwnd))
	}
}

// onMessage answers the page script (guiPageScript).
func (w *guiWin) onMessage(msg string) {
	switch msg {
	case "pt:login":
		if !w.onDevicePage() {
			return
		}
		w.mu.Lock()
		submit := !w.submitted
		w.submitted = true
		w.mu.Unlock()
		w.chromium.Eval(guiFillScript(w.cfg.User, w.cfg.Password, submit))
		if !submit {
			w.title(winWord("自動ログインは1回だけです。内容を確かめてログインしてください", "auto login runs once; check and log in"))
		}
	case "pt:nologin":
		// A page without the login form after the login was sent: it went
		// through, and a later login form (session timeout) may be sent
		// again.
		w.mu.Lock()
		w.submitted = false
		w.mu.Unlock()
		w.title("")
	}
}

// onDevicePage reports that the window shows the device's GUI, not some
// other site a link led to: the password goes nowhere else.
func (w *guiWin) onDevicePage() bool {
	u, err := url.Parse(w.source())
	return err == nil && u.Scheme == w.base.Scheme && strings.EqualFold(u.Host, w.base.Host)
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

// guiPageScript runs in every page of the window: it finds a login form
// (known GUIs first, then any visible password field) and tells the window,
// presses the Accept of a login disclaimer, and reports a page without a
// login form once the login went through.
const guiPageScript = `(function(){
if (window.top !== window) return;
function vis(e){ if(!e) return false; var r=e.getBoundingClientRect(); var s=getComputedStyle(e); return r.width>0&&r.height>0&&s.visibility!=="hidden"&&s.display!=="none"; }
function q(sel){ var l=document.querySelectorAll(sel); for(var i=0;i<l.length;i++) if(vis(l[i])) return l[i]; return null; }
window.__ptFind = function(){
  var p = q("#secretkey,#passwd,input[name=secretkey],input[name=passwd],input[type=password]");
  if (!p) return null;
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
var sent=false, accepted=false, start=Date.now();
function tick(){
  if (Date.now()-start > 60000) return;
  var f = window.__ptFind();
  if (f && !sent) { sent=true; window.chrome.webview.postMessage("pt:login"); }
  if (!f && sent) { sent=false; }
  if (!f && !accepted && Date.now()-start < 15000 && /disclaimer|免責|同意|terms|notice/i.test(document.body ? document.body.innerText.slice(0,4000) : "")) {
    var bs=document.querySelectorAll("button,input[type=button],input[type=submit],a");
    for (var i=0;i<bs.length;i++){ var t=(bs[i].innerText||bs[i].value||"").trim(); if (vis(bs[i]) && /^(accept|i accept|agree|i agree|同意|同意する|承諾|承諾する)$/i.test(t)) { accepted=true; bs[i].click(); break; } }
  }
  setTimeout(tick, 500);
}
function reportNoLogin(){ if (!window.__ptFind()) window.chrome.webview.postMessage("pt:nologin"); }
if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", function(){ tick(); setTimeout(reportNoLogin, 4000); });
else { tick(); setTimeout(reportNoLogin, 4000); }
})();`
