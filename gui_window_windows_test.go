package main

import (
	"net/url"
	"testing"
	"unsafe"
)

// The device's address and the browser's spelling of it are one origin:
// an IPv6 literal registered uncompressed, a default port written out, a
// host name in capitals (2026-10-24 review: the raw strings differed and
// the certificate was refused, the login never filled in).
func TestGUIOriginNormalised(t *testing.T) {
	base, _ := url.Parse("https://[2001:db8:0:0:0:0:0:1]/")
	w := &guiWin{base: base}
	for raw, want := range map[string]bool{
		"https://[2001:db8::1]/login":  true,
		"https://[2001:DB8::1]:443/x":  true,
		"https://[2001:db8::1]:8443/":  false,
		"http://[2001:db8::1]/":        false,
		"https://[2001:db8::2]/":       false,
		"https://192.0.2.10/":          false,
		"":                             false,
		"about:blank":                  false,
		"https://[2001:db8::1]@evil/x": false,
	} {
		if got := w.sameOrigin(raw); got != want {
			t.Errorf("sameOrigin(%q) = %v, want %v", raw, got, want)
		}
	}
	if o := jsOrigin(base); o != "https://[2001:db8::1]" {
		t.Errorf("jsOrigin(v6) = %q", o)
	}

	v4, _ := url.Parse("http://192.0.2.10:80/")
	w = &guiWin{base: v4}
	if !w.sameOrigin("http://192.0.2.10/a") || !w.sameOrigin("HTTP://192.0.2.10:80/") {
		t.Error("the default port spelled out and left out differ")
	}
	if w.sameOrigin("https://192.0.2.10/") || w.sameOrigin("http://192.0.2.10:8080/") {
		t.Error("another scheme or port passes as the device")
	}
	if o := jsOrigin(v4); o != "http://192.0.2.10" {
		t.Errorf("jsOrigin(v4) = %q", o)
	}

	named, _ := url.Parse("https://Lab-FW1.example:8443/")
	w = &guiWin{base: named}
	if !w.sameOrigin("https://lab-fw1.example:8443/login") {
		t.Error("host name case")
	}
	if o := jsOrigin(named); o != "https://lab-fw1.example:8443" {
		t.Errorf("jsOrigin(named) = %q", o)
	}
}

// The login is pressed once, with both a username and a password: an empty
// one sent is a failed login the device counts towards a lockout.
func TestGUILoginSubmit(t *testing.T) {
	w := &guiWin{cfg: guiLaunch{User: "ops", Password: "pw"}}
	if !w.loginSubmit(false) {
		t.Fatal("first login with credentials not sent")
	}
	w.autoSent = true
	if w.loginSubmit(false) {
		t.Error("sent twice")
	}
	w.autoSent, w.tofuNow = false, true
	if w.loginSubmit(false) {
		t.Error("sent on a certificate seen for the first time")
	}
	w.tofuNow = false
	if w.loginSubmit(true) {
		t.Error("sent on a certificate swapped to a trusted one")
	}
	for _, cfg := range []guiLaunch{{User: "ops"}, {Password: "pw"}, {}} {
		w := &guiWin{cfg: cfg}
		if w.loginSubmit(false) {
			t.Errorf("sent without credentials: %+v", cfg)
		}
	}
}

// Every COM handler shares one vtable (four callbacks for the process, not
// four per handler), and Invoke finds the handler by its address.
func TestComHandlersShareVtable(t *testing.T) {
	var got []int
	a := newComHandler(func(sender, args uintptr) { got = append(got, 1) })
	b := newComHandler(func(sender, args uintptr) { got = append(got, 2) })
	if a.vtbl != b.vtbl {
		t.Error("two vtables")
	}
	comInvoke(uintptr(unsafe.Pointer(b)), 0, 0)
	comInvoke(uintptr(unsafe.Pointer(a)), 0, 0)
	comInvoke(0x1000, 0, 0) // an object that is not ours: ignored
	if len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Errorf("invoked = %v", got)
	}
	// The object's first word is the vtable pointer, as COM reads it.
	if *(*uintptr)(unsafe.Pointer(a)) != uintptr(unsafe.Pointer(&comHandlerVtbl)) {
		t.Error("vtable pointer not first")
	}
}
