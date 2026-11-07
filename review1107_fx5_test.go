package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// 2026-11-07 review E (terminal, log, grep and screen windows). Lab values
// only.

// E A-3: window-changes stop after the pager step only on a device that
// sizes its paging from the terminal; a shell's PAGER setting (Linux) is
// not one, and vim / less there need the window-changes. A command not
// recognised is still frozen, as every pager step was before.
func TestPagerFreezesWindow(t *testing.T) {
	cases := []struct {
		name  string
		steps []profile.Step
		want  bool
	}{
		{"none", nil, false},
		{"Cisco IOS", []profile.Step{{Send: "terminal length 0"}}, true},
		{"Cisco ASA", []profile.Step{{Send: "terminal pager 0"}}, true},
		{"Junos", []profile.Step{{Send: "set cli screen-length 0"}}, true},
		{"NetScreen", []profile.Step{{Send: "set console page 0"}}, true},
		{"ProCurve", []profile.Step{{Send: "no page"}}, true},
		{"Yamaha", []profile.Step{{Send: "console lines infinity"}}, true},
		{"Alaxala", []profile.Step{{Send: "set terminal pager disable"}}, true},
		{"PAN-OS", []profile.Step{{Send: "set cli pager off"}}, true},
		{"FortiOS", []profile.Step{{Send: "config system console"}, {Send: "set output standard"}, {Send: "end"}}, true},
		{"NEC IX", []profile.Step{{Send: "svintr-config"}, {Send: "terminal length 0"}}, true},
		{"unknown device command stays frozen", []profile.Step{{Send: "some-other pager-command"}}, true},
		{"Linux export", []profile.Step{{Send: "export PAGER=cat SYSTEMD_PAGER=cat GIT_PAGER=cat"}}, false},
		{"plain variable", []profile.Step{{Send: "PAGER=cat"}}, false},
		{"unset", []profile.Step{{Send: "unset PAGER"}}, false},
		{"stty", []profile.Step{{Send: "stty -ixon"}}, false},
		{"SendRaw export", []profile.Step{{SendRaw: "export PAGER=cat\n"}}, false},
		{"shell then device", []profile.Step{{Send: "export PAGER=cat"}, {Send: "terminal length 0"}}, true},
		{"empty steps", []profile.Step{{Send: ""}, {Send: "  "}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pagerFreezesWindow(c.steps); got != c.want {
				t.Fatalf("pagerFreezesWindow(%v) = %v, want %v", c.steps, got, c.want)
			}
		})
	}
	// The built-in profiles: every network device freezes, Linux does not.
	for _, p := range profile.NewRegistry().List() {
		linux := strings.HasPrefix(p.Key, "linux")
		if len(p.Pager) == 0 {
			continue
		}
		if got := pagerFreezesWindow(p.Pager); got == linux {
			t.Errorf("profile %s: pagerFreezesWindow = %v", p.Key, got)
		}
	}
}

// E A-6: the size the terminal window opens at (the remembered one) is what
// fit compares with the screen — the default alone let a remembered size
// larger than the screen stand whenever the default fitted.
func TestOpenSizeIsFitted(t *testing.T) {
	ws := openSize(termWin, 1900, 1000)
	if ws.W != 1900 || ws.H != 1000 || ws.MinW != termWin.MinW || ws.MinH != termWin.MinH {
		t.Fatalf("openSize = %+v", ws)
	}
	// A 125 % laptop (1536×864 logical): the default fits, the remembered
	// size does not and is shrunk to the screen.
	if w, h := fitSize(1536, 864, termWin); w != termWin.W || h != termWin.H {
		t.Fatalf("the default on 1536×864: %d×%d", w, h)
	}
	if w, h := fitSize(1536, 864, ws); w != 1536-screenMarginW || h != 864-screenMarginH {
		t.Fatalf("the remembered size on 1536×864: %d×%d", w, h)
	}
	// A large screen keeps the remembered size.
	if w, h := fitSize(2560, 1440, ws); w != 1900 || h != 1000 {
		t.Fatalf("the remembered size on 2560×1440: %d×%d", w, h)
	}
	// Nothing remembered: the default.
	if ws := openSize(termWin, 0, 0); ws != termWin {
		t.Fatalf("openSize(0, 0) = %+v", ws)
	}
	// A maximised or minimised window's size is not kept for the next one;
	// neither is a grid cell's.
	if !sizeWorthKeeping(false, false, false) {
		t.Fatal("a plain window's size is kept")
	}
	for _, c := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if sizeWorthKeeping(c[0], c[1], c[2]) {
			t.Fatalf("kept: tiled=%v maximised=%v minimised=%v", c[0], c[1], c[2])
		}
	}
}

// E A-7 / B-6: the lines a parent writes to a window it opened.
func TestParseKidLine(t *testing.T) {
	for _, c := range []struct {
		in  string
		cmd string
		n   int
	}{
		{"CLOSE", kidLineClose, 0},
		{"CLOSE \r", kidLineClose, 0},
		{"LINE 12", kidLineGoto, 12},
		{"LINE 0", kidLineGoto, 0},
		{"LINE x", "", 0},
		{"LINE -3", "", 0},
		{"LINE", "", 0},
		{"", "", 0},
		{"PW abc", "", 0},
	} {
		if cmd, n := parseKidLine(c.in); cmd != c.cmd || n != c.n {
			t.Errorf("parseKidLine(%q) = %q, %d; want %q, %d", c.in, cmd, n, c.cmd, c.n)
		}
	}
}

// E A-7: closing the windows asks each (CLOSE on its pipe) rather than
// killing it outright, so a grep or screen window closes the log windows
// it opened; a window that has ended takes no more lines.
func TestKidsAskedToClose(t *testing.T) {
	r, w := io.Pipe()
	k := &viewerKid{in: w, done: make(chan struct{})}
	got := make(chan string, 1)
	go func() {
		b := make([]byte, 64)
		n, _ := r.Read(b)
		got <- string(b[:n])
	}()
	askKidsClose([]*viewerKid{k})
	select {
	case s := <-got:
		if s != kidLineClose+"\n" {
			t.Fatalf("written %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing written")
	}
	if !k.alive() {
		t.Fatal("alive before it ended")
	}
	close(k.done)
	if k.alive() {
		t.Fatal("alive after it ended")
	}
	if err := k.send(kidLineGoto + " 3"); err == nil {
		t.Fatal("a line to an ended window should fail")
	}
	// killKidsAfter with no process to kill returns after the wait.
	start := time.Now()
	killKidsAfter([]*viewerKid{k}, 10*time.Millisecond)
	if time.Since(start) > time.Second {
		t.Fatal("killKidsAfter waited too long")
	}
}

// E B-6: a hit in a file whose log window is still open goes to that window
// (LINE n), and the window is forgotten once it has ended. Paths differing
// in case are the same file on Windows.
func TestGrepReusesOpenLogWindow(t *testing.T) {
	r, w := io.Pipe()
	k := &viewerKid{in: w, done: make(chan struct{})}
	g := &GrepWin{kids: map[string]*viewerKid{kidKey(`C:\lab\logs\r1.txt`): k}}
	if got := g.kidFor(kidKey(`c:\lab\logs\R1.TXT`)); got != k {
		t.Fatal("the open window was not found by its path in another case")
	}
	if g.kidFor(kidKey(`C:\lab\logs\r2.txt`)) != nil {
		t.Fatal("a file with no window")
	}
	got := make(chan string, 1)
	go func() {
		b := make([]byte, 64)
		n, _ := r.Read(b)
		got <- string(b[:n])
	}()
	if err := k.send(kidLineGoto + " 42"); err != nil {
		t.Fatal(err)
	}
	if s := <-got; s != "LINE 42\n" {
		t.Fatalf("written %q", s)
	}
	close(k.done)
	if g.kidFor(kidKey(`C:\lab\logs\r1.txt`)) != nil {
		t.Fatal("an ended window was still found")
	}
	if _, ok := g.kids[kidKey(`C:\lab\logs\r1.txt`)]; ok {
		t.Fatal("an ended window was not forgotten")
	}
}

// E B-1: the follow timer asks only whether the file changed; the text is
// loaded when it did.
func TestViewerChanged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r1.txt")
	if err := os.WriteFile(p, []byte("lab-sw1#show clock\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := NewViewer(p, "")
	if !v.Changed() {
		t.Fatal("before the first load: changed")
	}
	if d := v.Load(); d.Err != "" {
		t.Fatal(d.Err)
	}
	if v.Changed() {
		t.Fatal("just loaded: unchanged")
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("lab-sw1#")
	f.Close()
	if !v.Changed() {
		t.Fatal("appended to: changed")
	}
	if d := v.Load(); !strings.HasSuffix(d.Text, "lab-sw1#") {
		t.Fatalf("after the append: %q", d.Text)
	}
	if v.Changed() {
		t.Fatal("loaded again: unchanged")
	}
	os.Remove(p)
	if !v.Changed() {
		t.Fatal("gone: changed (so the page shows the error)")
	}
}

// E B-4: the host-key question's 許可して再接続 is for the device's own key;
// a jump host's changed key cannot be allowed from the window, and the page
// is told why so it points at the main window instead of looping.
func TestHostKeyChangeBlockedForJumpHost(t *testing.T) {
	dev := &model.Device{Name: "lab-sw1", Host: "192.0.2.10", Conn: model.ConnSSH,
		Bastions: []model.Bastion{{Host: "192.0.2.1", Method: model.BastionSSH, Username: "ops"}}}
	tm := &Term{dev: dev}
	if why := tm.HostKeyChangeBlocked(); why != "" {
		t.Fatalf("no mismatch: %q", why)
	}
	tm.hkMismatch = dev.OwnHostKeyName()
	if why := tm.HostKeyChangeBlocked(); why != "" {
		t.Fatalf("the device's own key may be allowed: %q", why)
	}
	tm.hkMismatch = model.HopAddr("192.0.2.1", 22)
	why := tm.HostKeyChangeBlocked()
	if !strings.Contains(why, "踏み台1段目") || !strings.Contains(why, "192.0.2.1:22") {
		t.Fatalf("a jump host's key: %q", why)
	}
	if (&Term{}).HostKeyChangeBlocked() != "" {
		t.Fatal("no device: nothing to say")
	}
}

// E B-2: after the disconnect ログ表示 opens the session's log as it was
// left; with no session ever, or the file gone, it says so.
func TestOpenLogWindowAfterDisconnect(t *testing.T) {
	tm := &Term{}
	if err := tm.OpenLogWindow(); err == nil || !strings.Contains(err.Error(), "接続していません") {
		t.Fatalf("no session: %v", err)
	}
	tm.lastLog = filepath.Join(t.TempDir(), "gone.txt")
	if err := tm.OpenLogWindow(); err == nil || !strings.Contains(err.Error(), "前回のログが見つかりません") {
		t.Fatalf("log gone: %v", err)
	}
	tm.locked = true
	if err := tm.OpenLogWindow(); err != errLocked {
		t.Fatalf("locked: %v", err)
	}
}

// E C-1 / C-5 / C-9: the messages say what to do next, in Japanese.
func TestFX5Messages(t *testing.T) {
	tm := &Term{}
	if err := tm.Reconnect(); err == nil || !strings.Contains(err.Error(), "本体で解錠") {
		t.Fatalf("Reconnect without the password: %v", err)
	}
	err := saveTextErr(`C:\lab\PalaTerm\export\grep`, os.ErrPermission)
	for _, want := range []string{`C:\lab\PalaTerm\export\grep に書けません`, "書き込める場所に PalaTerm を置くか", "結果をコピー"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("saveTextErr lacks %q: %v", want, err)
		}
	}
}
