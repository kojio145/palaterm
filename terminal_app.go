package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

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

	mu       sync.Mutex
	send     func(string) error
	resizeFn func(cols, rows int) error
	closeFn  func() error
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
	t.runner.HostKeys = newMemHostKeys(inv.KnownHostKeys)
	var dev *model.Device
	for i := range inv.Devices {
		if inv.Devices[i].Name == t.deviceArg {
			d := inv.Devices[i]
			dev = &d
			break
		}
	}
	if dev == nil {
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.deviceArg, Error: "機器が見つかりません"})
		return
	}

	// Tick elapsed/total seconds to the UI while connecting, so a slow or
	// failing connection shows progress instead of a silent wait.
	total := inv.Settings.ConnectTimeout
	if total <= 0 {
		total = 20
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
			exp.Close()
		}
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.deviceArg, Error: err.Error()})
		return
	}

	var logMu sync.Mutex
	var logBuf []byte
	settings := inv.Settings
	d := *dev
	sink := func(b []byte) {
		logMu.Lock()
		logBuf = append(logBuf, b...)
		logMu.Unlock()
		runtime.EventsEmit(t.ctx, "term:data", termMsg{Device: t.deviceArg, Data: base64.StdEncoding.EncodeToString(b)})
	}
	onClose := func() {
		logMu.Lock()
		data := append([]byte(nil), logBuf...)
		logMu.Unlock()
		logPath := ""
		if len(data) > 0 {
			if p, e := logstore.Write(settings.LogDir, "{host}_interactive_{date}_{time}.txt",
				logstore.Fields{Host: d.Name, IP: d.Host, OS: d.OSType, Group: d.Group, Site: d.Site},
				runpkg.RedactSecrets(string(data), &d), time.Now()); e == nil {
				logPath = p
			}
		}
		runtime.EventsEmit(t.ctx, "term:closed", termClosed{Device: t.deviceArg, LogPath: logPath})
	}
	send, resize, closeFn := t.runner.StartInteractive(exp, sink, onClose)
	t.mu.Lock()
	t.send, t.resizeFn, t.closeFn = send, resize, closeFn
	t.mu.Unlock()
	runtime.EventsEmit(t.ctx, "term:ready", termReady{Device: t.deviceArg, Host: d.Host, Conn: string(d.Conn)})
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

// Close ends the session.
func (t *Term) Close() {
	t.mu.Lock()
	c := t.closeFn
	t.mu.Unlock()
	if c != nil {
		_ = c()
	}
}
