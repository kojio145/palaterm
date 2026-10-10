package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/kojio145/palaterm/internal/vault"
)

// Term is the backend for a standalone single-device terminal window, launched
// as "PalaTerm.exe --connect <device>". Each launch is its own OS window (its own
// taskbar entry, freely resizable), so several can run at once. The master
// password arrives on stdin from the parent; if absent, the window prompts.
type Term struct {
	ctx       context.Context
	deviceArg string
	vaultPath string
	profiles  *profile.Registry
	runner    *runpkg.Runner
	pwCh      chan string

	mu        sync.Mutex
	send      func(string) error
	resizeFn  func(cols, rows int) error
	closeFn   func() error
	logDir    string // Settings.LogDir once the vault is open ("" => logs/)
	slog      *streamLog
	pasteOpen bool          // a paste-confirmation window is showing
	pw        string        // master password, kept for a reconnect
	dev       *model.Device // the device once the vault is open
	hostKeys  *memHostKeys  // this window's pins (seeded from the vault)
}

// NewTerm builds the terminal-window backend and starts reading the password
// line from stdin in the background.
func NewTerm(device string) *Term {
	reg := profile.NewRegistry()
	t := &Term{
		deviceArg: device,
		vaultPath: defaultVaultPath(),
		profiles:  reg,
		runner:    runpkg.New(reg),
		pwCh:      make(chan string, 1),
	}
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			t.pwCh <- sc.Text()
		}
	}()
	return t
}

func (t *Term) startup(ctx context.Context) { t.ctx = ctx }

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
	c := t.closeFn
	t.mu.Unlock()
	if c != nil {
		_ = c()
	}
}

// TermStart is returned by Start to tell the UI whether a password is needed.
type TermStart struct {
	NeedPassword bool   `json:"needPassword"`
	Name         string `json:"name"`
}

// Start begins the session using the stdin password if it arrived; otherwise it
// asks the UI to prompt (StartWithPassword).
func (t *Term) Start() TermStart {
	select {
	case pw := <-t.pwCh:
		go t.connect(pw)
		return TermStart{NeedPassword: false, Name: t.deviceArg}
	case <-time.After(500 * time.Millisecond):
		return TermStart{NeedPassword: true, Name: t.deviceArg}
	}
}

// StartWithPassword connects using a password entered in the window.
func (t *Term) StartWithPassword(pw string) { go t.connect(pw) }

// termReady carries device info to the UI once connected.
type termReady struct {
	Device string `json:"device"`
	Host   string `json:"host"`
	Conn   string `json:"conn"`
}

func (t *Term) connect(pw string) {
	inv, err := vault.Load(t.vaultPath, pw)
	if err != nil {
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.deviceArg, Error: "マスターパスワードが違うか、vaultを開けません"})
		return
	}
	inv.NormalizeGroups()
	registerProfiles(t.profiles, inv)
	// Host key pinning: start from the vault's recorded fingerprints. This
	// child process never writes the vault (the main window owns it), so a
	// key first seen here is only held for this window's lifetime; it gets
	// pinned permanently the first time the device runs in the main window.
	hk := newMemHostKeys(inv.KnownHostKeys)
	t.runner.HostKeys = hk
	var dev *model.Device
	for i := range inv.Devices {
		if inv.Devices[i].Name == t.deviceArg {
			d := inv.ResolveCredentials(inv.Devices[i])
			dev = &d
			break
		}
	}
	t.mu.Lock()
	t.pw, t.dev, t.hostKeys = pw, dev, hk
	t.mu.Unlock()
	if dev == nil {
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.deviceArg, Error: "機器が見つかりません"})
		return
	}

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

	exp, _, err := t.runner.Connect(context.Background(), dev, inv.Settings, nil)
	close(progressDone)
	if err != nil {
		if exp != nil {
			// Show what the device said before the error line, the way a
			// successful connect replays the login. An error alone ("認証に
			// 失敗しました") leaves the reader guessing which prompt the
			// device was really at; the transcript is the evidence.
			if tr := exp.Transcript(); tr != "" {
				runtime.EventsEmit(t.ctx, "term:data", termMsg{Device: t.deviceArg,
					Data: base64.StdEncoding.EncodeToString([]byte(runpkg.RedactSecrets(tr, dev)))})
			}
			exp.Close()
		}
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.deviceArg, Error: err.Error()})
		return
	}

	settings := inv.Settings
	d := *dev
	// The transcript goes to one file, named at connect time by the same
	// template as a batch log with the stage "work", in its own run folder
	// (log_<time>_work) so the session shows in 実行履歴 like a batch. It is
	// written as it arrives (Tera Term style) with the passwords masked —
	// see streamLog.
	started := time.Now()
	runDir := logstore.RunDir(settings.LogDir, settings.LogDirTemplate, d.Group, logstore.StageWork, settings.StageTokens(), started)
	logPath := filepath.Join(runDir, filepath.Base(logstore.Path(settings.LogDir, settings.LogNameTemplate,
		logstore.Fields{Host: d.Name, IP: d.Host, OS: d.OSType, Group: d.Group, Site: d.Site, Stage: logstore.StageWork, Tokens: settings.StageTokens()}, started)))
	var secrets []string
	if settings.MaskLogSecrets {
		secrets = runpkg.SecretsOf(&d)
	}
	slog := newStreamLog(logPath, secrets)
	sink := func(b []byte) {
		slog.Write(b)
		runtime.EventsEmit(t.ctx, "term:data", termMsg{Device: t.deviceArg, Data: base64.StdEncoding.EncodeToString(b)})
	}
	t.mu.Lock()
	t.logDir = settings.LogDir
	t.slog = slog
	t.mu.Unlock()
	onClose := func() {
		t.mu.Lock()
		t.slog = nil
		t.mu.Unlock()
		p, _ := slog.Close()
		if p != "" {
			_ = history.Write(runDir, history.Summary{
				App: "PalaTerm " + appVersion, StartedAt: started.Format(time.RFC3339), FinishedAt: time.Now().Format(time.RFC3339),
				Group: d.Group, Stage: logstore.StageWork, Interactive: true,
				Devices: []history.DeviceSummary{{Name: d.Name, Host: d.Host, Site: d.Site, Success: true,
					LogFile: filepath.Base(p), ElapsedSec: time.Since(started).Seconds()}},
			})
		}
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.deviceArg, LogPath: p})
	}
	send, resize, closeFn := t.runner.StartInteractive(exp, sink, onClose)
	t.mu.Lock()
	t.send, t.resizeFn, t.closeFn = send, resize, closeFn
	t.mu.Unlock()
	runtime.EventsEmit(t.ctx, "term:ready", termReady{Device: t.deviceArg, Host: d.Host, Conn: string(d.Conn)})
}

// AllowHostKeyChange is the answer to the host-key-mismatch prompt: the
// user says the device was replaced. The stale pins for the device (and its
// bastions) are dropped here, the main window is told to drop them from the
// vault (see SpawnTerminal), and the connection is tried again.
func (t *Term) AllowHostKeyChange() error {
	t.mu.Lock()
	dev, hk, pw := t.dev, t.hostKeys, t.pw
	t.mu.Unlock()
	if dev == nil || hk == nil {
		return fmt.Errorf("機器が見つかりません")
	}
	hk.Delete(fmt.Sprintf("%s:%d", dev.Host, dev.EffectivePort()))
	for _, b := range dev.ActiveBastions() {
		p := b.Port
		if p == 0 {
			p = 22
		}
		hk.Delete(fmt.Sprintf("%s:%d", b.Host, p))
	}
	fmt.Fprintln(os.Stdout, termEventClearHostKey+" "+dev.Name)
	go t.connect(pw)
	return nil
}

// OpenLogWindow shows this session's log in a viewer window that follows
// the file as it grows. Whatever is still held back is written first.
func (t *Term) OpenLogWindow() error {
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
func (t *Term) OpenPasteWindow(textB64 string, withCR bool) error {
	raw, err := base64.StdEncoding.DecodeString(textB64)
	if err != nil {
		return err
	}
	t.mu.Lock()
	if t.pasteOpen {
		t.mu.Unlock()
		return fmt.Errorf("貼り付けの確認ウィンドウが既に開いています")
	}
	t.pasteOpen = true
	t.mu.Unlock()
	exe, err := os.Executable()
	if err != nil {
		t.pasteDone()
		return err
	}
	cmd := exec.Command(exe, "--paste", t.deviceArg)
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
	go func() {
		_ = json.NewEncoder(stdin).Encode(pasteReq{Device: t.deviceArg, Text: string(raw), CR: withCR})
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
		// does not reliably re-activate the terminal underneath.
		focusOwnWindow()
		if !res.OK {
			runtime.EventsEmit(t.ctx, "term:paste-done", false)
			return
		}
		s := strings.NewReplacer("\r\n", "\r", "\n", "\r").Replace(res.Text)
		if res.CR && !strings.HasSuffix(s, "\r") {
			s += "\r"
		}
		t.mu.Lock()
		send := t.send
		t.mu.Unlock()
		if send != nil && s != "" {
			_ = send(s)
		}
		runtime.EventsEmit(t.ctx, "term:paste-done", true)
	}()
	return nil
}

func (t *Term) pasteDone() {
	t.mu.Lock()
	t.pasteOpen = false
	t.mu.Unlock()
}

// Send forwards keystrokes (base64) to the device.
func (t *Term) Send(dataB64 string) error {
	t.mu.Lock()
	s := t.send
	t.mu.Unlock()
	if s == nil {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return err
	}
	return s(string(raw))
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
	return runtime.ClipboardGetText(t.ctx)
}

// OpenLogDir opens the log folder in Explorer — the same folder the main
// window's ログフォルダを開く uses, so the interactive log saved on close is
// one click away from this window too.
func (t *Term) OpenLogDir() error {
	t.mu.Lock()
	dir := t.logDir
	t.mu.Unlock()
	if dir == "" {
		dir = "logs"
	}
	abs := logstore.ResolveRoot(dir)
	_ = os.MkdirAll(abs, 0o755)
	return exec.Command("explorer", abs).Start()
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
