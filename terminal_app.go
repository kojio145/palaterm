package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/kojio145/palaterm/internal/history"
	"github.com/kojio145/palaterm/internal/logstore"
	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
	runpkg "github.com/kojio145/palaterm/internal/runner"
	"github.com/kojio145/palaterm/internal/session"
	"github.com/kojio145/palaterm/internal/vault"
)

// Term is the backend for a standalone single-device terminal window, launched
// as "PalaTerm.exe --connect <device>". Each launch is its own OS window (its own
// taskbar entry, freely resizable), so several can run at once. The master
// password arrives on stdin from the parent; if absent, the window prompts.
type Term struct {
	ctx       context.Context
	deviceArg string // device ID to connect to (a name from older callers)
	label     string // what the window shows for the device: its name
	// manual opens the transport and hands it straight to the keyboard: no
	// profile login, no pager step, no jump-host traversal — the console the
	// way Tera Term opens one. For initial setup over serial, a device with
	// no password yet, or any screen the OS profile does not anticipate.
	manual bool
	// direct leaves the device's jump hosts out (踏み台を使わずに接続).
	direct    bool
	vaultPath string
	profiles  *profile.Registry
	runner    *runpkg.Runner
	pwCh      chan string

	mu        sync.Mutex
	send      func(string) error
	resizeFn  func(cols, rows int) error
	closeFn   func() error
	logDir    string // Settings.LogDir once the vault is open ("" => logs/)
	slog      *logstore.Stream
	lastLog   string        // the last session's log file (its folder is what ログフォルダ opens)
	pasteOpen bool          // a paste-confirmation window is showing
	pw        string        // master password, kept for a reconnect
	dev       *model.Device // the device once the vault is open
	hostKeys  *memHostKeys  // this window's pins (seeded from the vault)
	// pending is a session whose automatic login failed but whose line is
	// still open, waiting for the user to take it over (Takeover) or drop it
	// (Discard). The transcript shown with the error is what they decide on.
	pending    *runpkg.Expecter
	pendingSet model.Settings
	pendingDev *model.Device
	// cancelLogin stops an automatic login in progress (SwitchToManual);
	// switched records that it was asked for, so connect hands the line over
	// instead of reporting the cancellation as a failure. connecting is true
	// while connect runs: a switch asked for outside that (right after the
	// login finished) is not kept for the next Reconnect.
	cancelLogin context.CancelFunc
	switched    bool
	connecting  bool
	// closeDone is closed when the live session's onClose has run (log
	// closed, summary written): what closeSession waits for.
	closeDone chan struct{}
	// locked covers the window until the master password is typed (the main
	// window's ロック, or this window's own idle time); keys are not sent
	// meanwhile. autoLockMin is the vault's idle setting (0 = off).
	locked      bool
	autoLockMin int
	rightClick  string // Settings.RightClickPaste() once the vault is open
	lang        string
	exp         *runpkg.Expecter // the live session's line (SendBreak)
	pasteProc   *os.Process      // the open paste-confirmation window (ended on Lock)
	unlockFails int              // wrong passwords in a row at the lock screen
	quitting    bool             // quitForParent: close without asking
	bc          bcastState       // 同時入力 (term_bcast.go)
	inQ         *lineQueue       // INPUT waiting to be sent (bcastSend)
	placeQ      *lineQueue       // grid places waiting to be made (doPlace)
	placed      bool             // a PLACE came before startup: it wins over the start place
	sendMu      sync.Mutex       // one send at a time: here and the relay in the same order
	tiled       bool             // placed in a grid: its size is not remembered
	unlockMu    sync.Mutex       // serialises Unlock attempts
	// replacedKeys are the stale pins dropped by AllowHostKeyChange, by
	// address. The main window removes them from the vault a moment later,
	// but connect reads the vault again right away; until the vault stops
	// holding these exact fingerprints, connect leaves them out again.
	replacedKeys map[string]string
	// hkMismatch names the endpoint whose host key the last connection
	// attempt refused ("" when it failed for another reason): what
	// AllowHostKeyChange is allowed to drop depends on it.
	hkMismatch string
}

// noteMismatch records which host key, if any, err refused (see hkMismatch).
func (t *Term) noteMismatch(err error) {
	t.mu.Lock()
	t.hkMismatch = session.MismatchedHostKey(err)
	t.mu.Unlock()
}

// errLocked refuses what a locked window must not do.
var errLocked = fmt.Errorf("ロック中です。マスターパスワードでロックを解除してください")

// isLocked reports the lock (see Lock).
func (t *Term) isLocked() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.locked
}

// NewTerm builds the terminal-window backend and starts reading the password
// line from stdin in the background.
func NewTerm(device, label string, manual, direct bool) *Term {
	reg := profile.NewRegistry()
	if label == "" {
		label = device
	}
	t := &Term{
		deviceArg: device,
		label:     label,
		manual:    manual,
		direct:    direct,
		vaultPath: defaultVaultPath(),
		profiles:  reg,
		runner:    runpkg.New(reg),
		pwCh:      make(chan string, 1),
	}
	// 並べて接続 may open the window with 同時入力 on (設定 tab).
	t.bc.join = os.Getenv(bcastEnv) == "1"
	t.inQ = newLineQueue(t.bcastSend)
	t.placeQ = newLineQueue(t.doPlace)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 64*1024), 1024*1024) // INPUT lines (bcastChunk)
		if sc.Scan() {
			t.pwCh <- sc.Text()
		}
		// The parent keeps the pipe open: LOCK (its ロック), PW <base64> (the
		// master password was changed), CLOSE (PalaTerm is quitting).
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == termLockLine:
				t.Lock()
			case strings.HasPrefix(line, termPWLine+" "):
				if pw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, termPWLine+" ")); err == nil && len(pw) > 0 {
					t.mu.Lock()
					t.pw, t.unlockFails = string(pw), 0
					t.mu.Unlock()
				}
			case line == termCloseLine:
				// The main window is closing and already asked: no
				// "disconnect?" question here.
				t.closeAndQuit()
			case strings.HasPrefix(line, termLineInput+" "):
				t.bcastIn(strings.TrimPrefix(line, termLineInput+" "))
			case strings.HasPrefix(line, termLineBcN+" "):
				t.bcastCounts(strings.TrimPrefix(line, termLineBcN+" "))
			case strings.HasPrefix(line, termLinePlace+" "):
				if r, ok := parseRect(strings.TrimPrefix(line, termLinePlace+" ")); ok {
					// Flagged and queued under one lock, as startup reads
					// the flag and queues its own place under it: a place
					// of startup's queued after this one would stand.
					t.mu.Lock()
					t.placed = true
					t.placeAt(r)
					t.mu.Unlock()
				}
			}
		}
	}()
	return t
}

func (t *Term) startup(ctx context.Context) {
	go keepKeyboardOnWebview(ctx)
	cell, ok := parseRect(os.Getenv(tileEnv))
	// The flag and the queue under one lock (see PLACE in NewTerm): a
	// 並べ直す that reached the window while it was starting keeps its
	// place, queued already, over the grid cell.
	t.mu.Lock()
	t.ctx = ctx
	placed := t.placed
	if !placed && ok {
		t.placeAt(cell) // 並べて接続: its grid cell
		placed = true
	}
	t.mu.Unlock()
	if !placed {
		termWin.fit(ctx)
		placeNearParent(ctx, termWin)
	}
	t.bcastReport()
}

// domReady brings this window to the front and gives the WebView2 the
// keyboard. A window spawned from the main window's "接続" button appeared
// behind it (or without keyboard focus), so the first keystrokes went to the
// main window and the user had to click into the terminal before typing
// (2026-10-10). The paste and diff windows already do the same.
func (t *Term) domReady(ctx context.Context) { go focusOwnWindow() }

// Focus is called by the page once the session is connected: a second nudge
// for the case where the window became visible only after domReady.
func (t *Term) Focus() { go focusOwnWindow() }

func (t *Term) shutdown(ctx context.Context) {
	t.mu.Lock()
	c, p := t.closeFn, t.pending
	t.pending = nil
	t.mu.Unlock()
	if c != nil {
		_ = c()
	}
	if p != nil {
		_ = p.Close()
	}
}

// TermStart is returned by Start to tell the UI whether a password is needed.
type TermStart struct {
	NeedPassword bool   `json:"needPassword"`
	Name         string `json:"name"`
	Manual       bool   `json:"manual"` // opened with --manual: no login to switch away from
}

// Start begins the session using the stdin password if it arrived; otherwise it
// asks the UI to prompt (StartWithPassword).
func (t *Term) Start() TermStart {
	select {
	case pw := <-t.pwCh:
		go t.connect(pw)
		return TermStart{NeedPassword: false, Name: t.label, Manual: t.manual}
	case <-time.After(500 * time.Millisecond):
		return TermStart{NeedPassword: true, Name: t.label, Manual: t.manual}
	}
}

// StartWithPassword connects using a password entered in the window.
func (t *Term) StartWithPassword(pw string) { go t.connect(pw) }

// termReady carries device info to the UI once connected.
type termReady struct {
	Device string `json:"device"`
	Host   string `json:"host"`
	Conn   string `json:"conn"`
	Manual bool   `json:"manual"` // opened without automatic login
	Hops   int    `json:"hops"`   // jump hosts on the way (0 = direct)
}

// termWinState names the terminal window's remembered size
// (%LOCALAPPDATA%\PalaTerm\term-window.json, see loadWinSize).
const termWinState = "term-window"

// loadTermWinSize is the size the last terminal window was closed at
// (logical pixels), within sane bounds.
func loadTermWinSize() (int, int, bool) { return loadWinSize(termWinState, termWin) }

// rememberSize stores the window's current size for the next one.
func (t *Term) rememberSize() {
	if t.ctx == nil {
		return
	}
	t.mu.Lock()
	tiled := t.tiled
	t.mu.Unlock()
	if tiled {
		return // a grid cell's size is no size for the next window
	}
	w, h := runtime.WindowGetSize(t.ctx)
	saveWinSize(termWinState, termWin, w, h)
}

func (t *Term) connect(pw string) {
	t.mu.Lock()
	t.connecting = true
	t.mu.Unlock()
	// A panic while connecting closes this window's session with the
	// reason, instead of killing the window outright (logged in
	// data\error.log). Whatever way it ends, no switch to manual is left
	// over for the next connect.
	defer func() {
		t.mu.Lock()
		t.connecting, t.switched, t.cancelLogin = false, false, nil
		t.mu.Unlock()
		if r := recover(); r != nil {
			msg := logPanic("terminal connect", r)
			runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.label, Error: msg})
		}
	}()
	inv, err := vault.Load(t.vaultPath, pw)
	if err != nil {
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.label, Error: "マスターパスワードが違うか、vaultを開けません"})
		return
	}
	inv.NormalizeGroups()
	registerProfiles(t.profiles, inv)
	// Host key pinning: start from the vault's recorded fingerprints. This
	// child process never writes the vault (the main window owns it), so a
	// key first seen here is only held for this window's lifetime; it gets
	// pinned permanently the first time the device runs in the main window.
	t.mu.Lock()
	hk := seedHostKeys(inv.KnownHostKeys, t.replacedKeys)
	t.mu.Unlock()
	t.runner.HostKeys = hk
	var dev *model.Device
	if i := inv.DeviceIndex(t.deviceArg); i >= 0 {
		d := inv.Devices[i]
		if t.direct {
			d = d.WithoutBastions()
		}
		d = inv.ResolveCredentials(d)
		dev = &d
	}
	t.mu.Lock()
	t.pw, t.dev, t.hostKeys = pw, dev, hk
	t.autoLockMin = inv.Settings.AutoLockMin
	if t.autoLockMin <= 0 {
		t.autoLockMin = model.DefaultAutoLockMin
	}
	if inv.Settings.AutoLockOff {
		t.autoLockMin = 0
	}
	t.rightClick = inv.Settings.RightClickPaste()
	t.mu.Unlock()
	if dev == nil {
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.label, Error: "機器が見つかりません"})
		return
	}
	// The device's 端末の色, shown from the start of the login.
	runtime.EventsEmit(t.ctx, "term:device", map[string]string{"tint": dev.TermColor})

	// Tick elapsed/total seconds to the UI while connecting, so a slow or
	// failing connection shows progress instead of a silent wait.
	//
	// Connect() covers the dial AND the login, so the bound is the sum of the
	// two timeouts. With only the connect timeout as the total, a console that
	// opened instantly and then never answered counted up to "39/10秒" — past
	// the end of its own gauge — before the login timeout finally fired.
	total := inv.Settings.ConnectTimeout
	if total <= 0 {
		total = 20
	}
	if ct := inv.Settings.CommandTimeout; ct > 0 {
		total += ct
	} else {
		total += 30
	}
	progressDone := make(chan struct{})
	var progressOnce sync.Once
	stopProgress := func() { progressOnce.Do(func() { close(progressDone) }) }
	defer stopProgress() // a panic below must not leave the ticker running
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		start := time.Now()
		for {
			select {
			case <-progressDone:
				return
			case <-tick.C:
				runtime.EventsEmit(t.ctx, "term:progress", map[string]int{
					"elapsed": int(time.Since(start).Seconds()), "total": total,
				})
			}
		}
	}()

	if t.manual {
		exp, err := t.runner.Open(context.Background(), dev, inv.Settings)
		stopProgress()
		t.noteMismatch(err)
		if err != nil {
			runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.label, Error: err.Error()})
			return
		}
		t.attach(exp, dev, inv.Settings, false)
		return
	}

	// The automatic login can be abandoned from the window (SwitchToManual):
	// cancelling this context stops the login steps where they are and
	// Connect returns the still-open session, which then goes to the keyboard.
	ctx, cancel := context.WithCancel(context.Background())
	t.mu.Lock()
	t.cancelLogin = cancel
	// 「手動に切り替え」 pressed before this point (while the vault was
	// opening) counts too: cancel right away so Connect gives the line
	// back as soon as it is up.
	if t.switched {
		cancel()
	}
	t.mu.Unlock()
	// The device's output is shown as it arrives, login included (view
	// only: keys go nowhere until attach), so a login that stalls shows
	// where it is waiting — a jump host's prompt, "Password:", a banner —
	// without switching to manual first.
	tee := func(b []byte) {
		runtime.EventsEmit(t.ctx, "term:data", termMsg{Device: t.label,
			Data: base64.StdEncoding.EncodeToString([]byte(runpkg.RedactSecrets(string(b), dev)))})
	}
	exp, _, err := t.runner.ConnectTee(ctx, dev, inv.Settings, tee)
	stopProgress()
	t.noteMismatch(err)
	t.mu.Lock()
	t.cancelLogin = nil
	switched := t.switched
	t.switched = false
	t.mu.Unlock()
	cancel()
	if switched {
		// Cancelled mid-dial (before any session existed): open the line
		// afresh, with no login, which is what was asked for. If the line
		// itself cannot be opened, say so plainly — "manual" cannot help
		// there: SSH authenticates while opening the connection, so wrong
		// credentials or an unreachable host fail before any screen exists.
		shown := exp != nil // the login so far is on screen (tee)
		if exp == nil {
			if err != nil && !errors.Is(err, context.Canceled) {
				runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.label, Error: manualUnavailable(dev, err)})
				return
			}
			exp, err = t.runner.Open(context.Background(), dev, inv.Settings)
			t.noteMismatch(err)
			if err != nil {
				runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.label, Error: manualUnavailable(dev, err)})
				return
			}
		}
		t.manual = true
		t.attach(exp, dev, inv.Settings, shown)
		return
	}
	if err != nil {
		if exp == nil {
			runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.label, Error: err.Error()})
			return
		}
		// What the device said is already on screen above the error line
		// (the tee): the reader sees which prompt it was really at.
		// The line itself is still open: the device is sitting at whatever
		// screen the profile could not get past — an initial-setup dialog, a
		// forced password change, a prompt nobody wrote a profile for. The
		// user can see it now, and is the one who knows what to type. Keep
		// the session and offer the keyboard instead of hanging up.
		t.mu.Lock()
		if t.pending != nil {
			t.pending.Close()
		}
		t.pending, t.pendingDev, t.pendingSet = exp, dev, inv.Settings
		t.mu.Unlock()
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.label, Error: err.Error(), Takeover: true, AtJump: runpkg.AtJumpHost(err), AtJumpLogin: runpkg.AtJumpLogin(err)})
		return
	}
	t.attach(exp, dev, inv.Settings, true)
}

// SwitchToManual abandons the automatic login that is running and hands the
// line to the keyboard as it is — the 「手動に切り替え」 button shown while
// connecting. The user can see the device is at a screen no profile will get
// past; this spares them the timeout.
func (t *Term) SwitchToManual() error {
	if t.isLocked() {
		return errLocked
	}
	t.mu.Lock()
	if !t.connecting {
		// Nothing to switch away from (the login just finished, or there is
		// no connect): a flag kept now would open the next Reconnect in
		// manual mode unasked.
		t.mu.Unlock()
		return nil
	}
	t.switched = true
	cancel := t.cancelLogin
	t.mu.Unlock()
	// No login running yet (the vault is still opening): the request is
	// remembered and honoured the moment connect reaches the login.
	if cancel != nil {
		cancel()
	}
	return nil
}

// manualUnavailable explains a 「手動に切り替え」 that could not be honoured
// because the line itself never opened. The raw dial error alone reads as
// if the switch had failed; the point is that there is no screen to hand
// over, and for SSH that the credentials are checked while opening.
func manualUnavailable(dev *model.Device, err error) string {
	why := "回線がつながっていないため、手動に切り替えられません"
	if dev.Conn == model.ConnSSH || len(dev.ActiveBastions()) > 0 {
		why += "（SSH はユーザー名・パスワード・鍵の確認を回線を開くときに行うため、合っていないと手動でも入れません）"
	}
	return why + ": " + err.Error()
}

// Takeover hands a session whose automatic login failed to the keyboard, as
// it is. The answer to the 「手動で続ける」 button.
func (t *Term) Takeover() error {
	if t.isLocked() {
		return errLocked
	}
	t.mu.Lock()
	exp, dev, settings := t.pending, t.pendingDev, t.pendingSet
	t.pending, t.pendingDev = nil, nil
	t.mu.Unlock()
	if exp == nil {
		return fmt.Errorf("引き継げる接続がありません")
	}
	t.attach(exp, dev, settings, true)
	return nil
}

// Discard hangs up a session held for Takeover. The answer to 「閉じる」.
func (t *Term) Discard() {
	if t.isLocked() {
		return
	}
	t.mu.Lock()
	exp := t.pending
	t.pending, t.pendingDev = nil, nil
	t.mu.Unlock()
	if exp != nil {
		exp.Close()
	}
	runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.label})
}

// attach starts the session log and hands the open session to the terminal.
// shown says the transcript so far is already on screen (the login streamed
// as it ran, or a failed login being taken over): it then goes to the log
// only, not to the screen again.
func (t *Term) attach(exp *runpkg.Expecter, dev *model.Device, settings model.Settings, shown bool) {
	d := *dev
	// The transcript goes to one file, named at connect time by the same
	// template as a batch log with the stage "work", in its own run folder
	// (log_<time>_work) so the session shows in 実行履歴 like a batch. It is
	// written as it arrives (Tera Term style) with the passwords masked —
	// see logstore.Stream.
	started := time.Now()
	runDir := logstore.ClaimRunDir(settings.LogDir, settings.LogDirTemplate, d.Group, logstore.StageWork, settings.StageTokens(), started)
	logPath := filepath.Join(runDir, filepath.Base(logstore.Path(settings.LogDir, settings.LogNameTemplate,
		logstore.Fields{Host: d.Name, IP: d.Host, OS: d.OSType, Group: d.Group, Site: d.Site, Role: d.Role, Stage: logstore.StageWork, Tokens: settings.StageTokens()}, started)))
	var secrets []string
	if settings.MaskLogSecrets {
		secrets = runpkg.SecretsOf(&d)
	}
	slog := logstore.NewStream(logPath, secrets)
	sink := func(b []byte) {
		slog.Write(b)
		runtime.EventsEmit(t.ctx, "term:data", termMsg{Device: t.label, Data: base64.StdEncoding.EncodeToString(b)})
	}
	closeDone := make(chan struct{})
	t.mu.Lock()
	t.logDir = settings.LogDir
	t.slog = slog
	t.closeDone = closeDone
	t.mu.Unlock()
	onClose := func() {
		defer close(closeDone)
		t.mu.Lock()
		if p, _ := slog.Flush(); p != "" {
			t.lastLog = p // ログフォルダ opens this session's folder after the disconnect too
		}
		t.slog = nil
		// Not live any more: closing the window needs no confirmation, and
		// Reconnect may open the session again.
		t.send, t.resizeFn, t.closeFn, t.exp = nil, nil, nil, nil
		t.mu.Unlock()
		t.bcastReport() // no longer takes keys from the other windows
		p, _ := slog.Close()
		if p == "" {
			// Nothing was logged: drop the folder claimed at connect time, or
			// it shows in 実行履歴 as a run with no devices. (Remove only
			// succeeds on an empty folder.)
			_ = os.Remove(runDir)
		}
		if p != "" {
			_ = history.Write(runDir, history.Summary{
				App: "PalaTerm " + appVersion, StartedAt: started.Format(time.RFC3339), FinishedAt: time.Now().Format(time.RFC3339),
				Group: d.Group, Stage: logstore.StageWork, Interactive: true,
				Devices: []history.DeviceSummary{{Name: d.Name, Host: d.Host, Site: d.Site, Success: true,
					LogFile: filepath.Base(p), ElapsedSec: time.Since(started).Seconds()}},
			})
		}
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.label, LogPath: p})
	}
	var send func(string) error
	var resize func(cols, rows int) error
	var closeFn func() error
	if shown {
		send, resize, closeFn = t.runner.StartInteractiveShown(exp, sink, func(b []byte) { slog.Write(b) }, onClose)
	} else {
		send, resize, closeFn = t.runner.StartInteractive(exp, sink, onClose)
	}
	t.mu.Lock()
	t.send, t.resizeFn, t.closeFn, t.exp = send, resize, closeFn, exp
	t.mu.Unlock()
	t.bcastReport() // takes keys from the other windows now
	// A serial device record has no host; the title shows the port and baud
	// actually opened instead ("COM3 @ 9600"), so an auto-detected port is
	// never a mystery.
	host := d.Host
	if tr := exp.Transport(); tr != "" {
		host = tr
	}
	runtime.EventsEmit(t.ctx, "term:ready", termReady{Device: t.label, Host: host, Conn: string(d.Conn), Manual: t.manual, Hops: len(d.ActiveBastions())})
}

// seedHostKeys builds a window's host-key store from the vault's pins,
// leaving out each pin the user has already let go (replaced: address ->
// the stale fingerprint) while the vault still holds that same fingerprint.
// Entries the vault no longer holds as-is are forgotten from replaced, so a
// later change of key at that address is caught again.
func seedHostKeys(known, replaced map[string]string) *memHostKeys {
	hk := newMemHostKeys(known)
	for addr, old := range replaced {
		if known[addr] == old {
			hk.Delete(addr)
		} else {
			delete(replaced, addr)
		}
	}
	return hk
}

// AllowHostKeyChange is the answer to the host-key-mismatch prompt: the
// user says the device was replaced. The device's own stale pin is dropped
// here, the main window is told to drop it from the vault (see
// SpawnTerminal), and the connection is tried again. A jump host's pin is
// never dropped this way (model.ReplacedHostKeyNames says why).
func (t *Term) AllowHostKeyChange() error {
	if t.isLocked() {
		return errLocked
	}
	t.mu.Lock()
	dev, hk, pw, mismatched := t.dev, t.hostKeys, t.pw, t.hkMismatch
	t.mu.Unlock()
	if dev == nil || hk == nil {
		return fmt.Errorf("機器が見つかりません")
	}
	names, err := dev.ReplacedHostKeyNames(mismatched)
	if err != nil {
		return err
	}
	t.mu.Lock()
	for _, addr := range names {
		if old := hk.GetHostKey(addr); old != "" {
			if t.replacedKeys == nil {
				t.replacedKeys = map[string]string{}
			}
			t.replacedKeys[addr] = old
		}
		hk.Delete(addr)
	}
	t.mu.Unlock()
	childLine(termEventClearHostKey + " " + dev.ID)
	go t.connect(pw)
	return nil
}

// OpenLogWindow shows this session's log in a viewer window that follows
// the file as it grows. Whatever is still held back is written first.
func (t *Term) OpenLogWindow() error {
	if t.isLocked() {
		return errLocked
	}
	t.mu.Lock()
	l := t.slog
	t.mu.Unlock()
	if l == nil {
		return fmt.Errorf("接続していません")
	}
	p, err := l.Flush()
	if err != nil {
		return err
	}
	if p == "" {
		return fmt.Errorf("まだログに書かれた内容がありません")
	}
	return spawnWindow([]string{"PALATERM_VIEW_LIVE=1"}, "--view", p)
}

// SetLogging stops (false) or resumes (true) recording the session to its
// log file. Recording is on from the moment of connection; stopping writes
// everything so far and ignores later output until started again, in the
// same file. Returns the file path ("" while nothing has been written).
func (t *Term) SetLogging(on bool) (string, error) {
	if t.isLocked() {
		return "", errLocked
	}
	t.mu.Lock()
	l := t.slog
	t.mu.Unlock()
	if l == nil {
		return "", fmt.Errorf("接続していません")
	}
	return l.SetPaused(!on)
}

// OpenPasteWindow shows the paste-confirmation window (a separate process,
// see paste_app.go) with text; whatever comes back confirmed is sent to the
// device as if typed, line endings normalised to CR, with a final Enter
// when the window's checkbox says so. One at a time per terminal.
//
// single: a one-line paste (confirmed only because 同時入力 is on): the
// window starts without 最後に Enter を送る unless withCR (Alt+V), as a
// one-line paste sends no Enter. Whether the text goes to the other windows
// is decided now, when the window says so (or does not), not when it is
// confirmed.
func (t *Term) OpenPasteWindow(textB64 string, withCR, single bool) error {
	raw, err := base64.StdEncoding.DecodeString(textB64)
	if err != nil {
		return err
	}
	t.mu.Lock()
	if t.locked {
		t.mu.Unlock()
		return errLocked
	}
	if t.pasteOpen {
		t.mu.Unlock()
		return fmt.Errorf("貼り付けの確認ウィンドウが既に開いています")
	}
	t.pasteOpen = true
	relay := t.bc.join
	t.mu.Unlock()
	peers := 0
	if relay {
		peers = t.bcastPeers()
	}
	exe, err := os.Executable()
	if err != nil {
		t.pasteDone()
		return err
	}
	cmd := exec.Command(exe, "--paste", t.label)
	cmd.Env = windowEnv()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.pasteDone()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.pasteDone()
		return err
	}
	if err := cmd.Start(); err != nil {
		t.pasteDone()
		return err
	}
	t.mu.Lock()
	t.pasteProc = cmd.Process
	t.mu.Unlock()
	go func() {
		_ = json.NewEncoder(stdin).Encode(pasteReq{Device: t.label, Text: string(raw), CR: withCR, Bcast: peers, Single: single})
		_ = stdin.Close()
	}()
	go func() {
		defer t.pasteDone()
		var res pasteRes
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
		if sc.Scan() {
			_ = json.Unmarshal(sc.Bytes(), &res)
		}
		_ = cmd.Wait()
		// Hand the keyboard back to this window: closing the paste window
		// does not reliably re-activate the terminal underneath. The paste
		// window hands the foreground over before it closes
		// (handForegroundToParent), which also grants this attempt; a
		// second one a moment later wins against the window manager
		// activating whatever it picks once the paste window is gone.
		focusOwnWindow()
		time.AfterFunc(300*time.Millisecond, func() { focusOwnWindow() })
		s, ok := pasteText(res)
		if !ok {
			runtime.EventsEmit(t.ctx, "term:paste-done", false)
			return
		}
		t.sendText(s, relay)
		runtime.EventsEmit(t.ctx, "term:paste-done", true)
	}()
	return nil
}

// pasteText is what a paste window's answer sends to the device: the text
// with its line endings as CR and, when asked, a final Enter. A cancel, or
// an empty text, sends nothing (false): a window whose Load timed out shows
// an empty box, and Enter in it must not reach the device as a bare Enter.
func pasteText(res pasteRes) (string, bool) {
	if !res.OK || res.Text == "" {
		return "", false
	}
	s := strings.NewReplacer("\r\n", "\r", "\n", "\r").Replace(res.Text)
	if res.CR && !strings.HasSuffix(s, "\r") {
		s += "\r"
	}
	return s, true
}

func (t *Term) pasteDone() {
	t.mu.Lock()
	t.pasteOpen = false
	t.pasteProc = nil
	t.mu.Unlock()
}

// Send forwards keystrokes (base64) to the device, and with 同時入力 on to
// the other windows too.
func (t *Term) Send(dataB64 string) error {
	raw, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return err
	}
	return t.sendText(string(raw), true)
}

// SendReply forwards what is this device's alone, never relayed by 同時入力:
// the terminal's own answer to a query from the device (cursor position,
// device attributes), a mouse report, and a paste confirmed in a dialog
// opened while 同時入力 was off.
func (t *Term) SendReply(dataB64 string) error {
	raw, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return err
	}
	return t.sendText(string(raw), false)
}

// sendText sends s to the device and, when relay is allowed and 同時入力 is
// on, to the other windows first: a slow line here (a 9600-baud console)
// no longer holds the others back, and a session that drops halfway does
// not keep them from getting it. One at a time, so every device gets the
// keys in the same order.
func (t *Term) sendText(s string, relay bool) error {
	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	t.mu.Lock()
	send, locked := t.send, t.locked
	t.mu.Unlock()
	if send == nil || locked || s == "" {
		return nil
	}
	if relay {
		t.bcastOut(s)
	}
	return send(s)
}

// Resize notifies the device of a new terminal size.
func (t *Term) Resize(cols, rows int) error {
	t.mu.Lock()
	r := t.resizeFn
	t.mu.Unlock()
	if r == nil {
		return nil
	}
	return r(cols, rows)
}

// SetClipboard puts text selected in the terminal on the OS clipboard
// (Tera Term-style copy on select). Done in Go because the browser Clipboard
// API inside WebView2 is not reliably available to the page.
func (t *Term) SetClipboard(text string) error {
	return runtime.ClipboardSetText(t.ctx, text)
}

// GetClipboard returns the OS clipboard text for the paste-confirmation
// dialog (Alt+V / right-click / toolbar), read in Go for the same reason
// SetClipboard writes in Go.
func (t *Term) GetClipboard() (string, error) {
	if t.isLocked() {
		return "", errLocked
	}
	return runtime.ClipboardGetText(t.ctx)
}

// OpenLogDir opens this session's log folder in Explorer (the run folder
// its log is written to), or — before anything was logged — the log root
// the main window's ログフォルダを開く uses. The root alone left the user
// hunting for the right 日時_グループ_work folder.
func (t *Term) OpenLogDir() error {
	if t.isLocked() {
		return errLocked
	}
	t.mu.Lock()
	dir, l, last := t.logDir, t.slog, t.lastLog
	t.mu.Unlock()
	if l != nil {
		if p, _ := l.Flush(); p != "" {
			last = p
		}
	}
	if last != "" {
		if fi, err := os.Stat(filepath.Dir(last)); err == nil && fi.IsDir() {
			return startExplorer(filepath.Dir(last))
		}
	}
	if dir == "" {
		dir = "logs"
	}
	abs := logstore.ResolveRoot(dir)
	_ = os.MkdirAll(abs, 0o755)
	return startExplorer(abs)
}

// Lock covers the window (the main window's ロック, or the page's own idle
// timer). The session stays up; keys are dropped until Unlock.
func (t *Term) Lock() {
	t.mu.Lock()
	t.locked = true
	ctx, pp := t.ctx, t.pasteProc
	t.mu.Unlock()
	t.bcastReport()
	// A paste confirmation left open would otherwise still send to the device
	// from its own window, lock or no lock.
	if pp != nil {
		_ = pp.Kill()
	}
	if ctx != nil {
		runtime.EventsEmit(ctx, "term:lock")
	}
}

// IsLocked lets the page show the lock screen for a lock that arrived before
// it was listening for term:lock.
func (t *Term) IsLocked() bool { return t.isLocked() }

// Unlock lifts the lock when pw is the master password.
//
// The main window's unlock goes through the vault's key derivation and is
// slow by nature; this compares against the password in memory, so wrong
// guesses are slowed down here instead: 1 s after the first, growing by a
// second each time up to 5 s.
func (t *Term) Unlock(pw string) bool {
	// One attempt at a time, its delay included: calls made in parallel from
	// the page would otherwise each be checked at once.
	t.unlockMu.Lock()
	defer t.unlockMu.Unlock()
	t.mu.Lock()
	if t.pw != "" && subtle.ConstantTimeCompare([]byte(pw), []byte(t.pw)) == 1 {
		t.locked, t.unlockFails = false, 0
		t.mu.Unlock()
		t.bcastReport()
		return true
	}
	t.unlockFails++
	wait := time.Duration(min(t.unlockFails, 5)) * time.Second
	t.mu.Unlock()
	time.Sleep(wait)
	return false
}

// AutoLockMinutes is the vault's idle-lock setting for this window (0 = off,
// also before the vault is open).
func (t *Term) AutoLockMinutes() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.autoLockMin
}

// RightClickPaste is what a right-click in the terminal does: "paste"
// (also before the vault is open), "menu" or "off". Read when the window
// opens, so a changed setting applies to windows opened after it.
func (t *Term) RightClickPaste() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rightClick == "" {
		return "paste"
	}
	return t.rightClick
}

// Reconnect opens the session again after it ended (a reloaded device, a
// dropped line), with the same device and mode — Tera Term's reconnect.
func (t *Term) Reconnect() error {
	if t.isLocked() {
		return errLocked
	}
	t.mu.Lock()
	pw, live := t.pw, t.send != nil
	t.mu.Unlock()
	if pw == "" {
		return fmt.Errorf("vault is locked")
	}
	if live {
		return nil
	}
	go t.connect(pw)
	return nil
}

// SendBreak sends a serial break (Tera Term's Alt+B): on a Cisco during
// boot, the way into ROMMON for password recovery.
func (t *Term) SendBreak() error {
	t.mu.Lock()
	e, locked := t.exp, t.locked
	t.mu.Unlock()
	if e == nil || locked {
		return fmt.Errorf("接続していません")
	}
	return e.Break()
}

// SetLang tells the backend the UI language for its own close dialog.
func (t *Term) SetLang(lang string) {
	t.mu.Lock()
	t.lang = lang
	uiLang.Store(lang)
	t.mu.Unlock()
}

// beforeClose asks before closing a window whose session is still up, the
// way Tera Term does: a stray click on × used to drop a device in the middle
// of a configuration change.
func (t *Term) beforeClose(ctx context.Context) bool {
	t.rememberSize()
	t.mu.Lock()
	live := t.send != nil && !t.quitting
	t.mu.Unlock()
	if !live {
		return false
	}
	// The question is asked in the window itself, styled like the rest of it
	// (Windows' own Yes/No box looked out of place): the close is held here,
	// and ConfirmClose finishes it if the user says so.
	runtime.EventsEmit(ctx, "term:close-request")
	return true
}

// ConfirmClose is the answer "disconnect and close" to term:close-request:
// the session ends the normal way (log closed, summary written), then the
// window closes without asking again.
func (t *Term) ConfirmClose() { t.closeAndQuit() }

// termCloseWait bounds the wait for onClose when the window is quitting: a
// log that will not flush must not keep the window on screen.
const termCloseWait = 3 * time.Second

// closeAndQuit ends the session the normal way — the log closed and its
// 実行履歴 summary written — then quits the window without the "disconnect?"
// question (ConfirmClose, and CLOSE from the main window). It waits for
// onClose to finish: a fixed pause used to cut a slow Flush or history.Write
// short and leave a run with no devices in the history.
func (t *Term) closeAndQuit() {
	t.mu.Lock()
	t.quitting = true
	ctx := t.ctx
	t.mu.Unlock()
	t.closeSession(termCloseWait)
	if ctx != nil {
		runtime.Quit(ctx)
	}
}

// closeSession is Close waited for: it returns once the session's onClose
// has run, or after wait. At once when no session was ever attached.
func (t *Term) closeSession(wait time.Duration) {
	t.mu.Lock()
	c, done := t.closeFn, t.closeDone
	t.mu.Unlock()
	if c != nil {
		_ = c()
	}
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(wait):
	}
}

// Close ends the session.
func (t *Term) Close() {
	t.mu.Lock()
	c := t.closeFn
	t.mu.Unlock()
	if c != nil {
		_ = c()
	}
}
