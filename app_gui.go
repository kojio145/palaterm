package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/session"
)

// GUI を開く: the device's web GUI (FortiGate & co.) in a window of
// PalaTerm's own ("--gui", gui_window_windows.go), logged in with the
// device's credentials. A device reached directly is opened at its own
// address; behind jump hosts a port of this PC is carried through them
// (session.Forward) and the window is sent to 127.0.0.1:<port>. The
// forwards live in the main window: listed in the sidebar, stopped on lock
// and on exit; the windows close with them.

// guiLaunch is what the main window hands a GUI window, as one JSON line on
// its stdin (never in its arguments: the password is in it).
type guiLaunch struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	User     string `json:"user"`
	Password string `json:"password"`
	// PinKey names the device's GUI certificate in the vault
	// (Inventory.KnownGUICerts); Pin is the SHA-256 recorded there ("" =
	// none yet: the first one seen is recorded, trust on first use).
	PinKey string `json:"pinKey"`
	Pin    string `json:"pin"`
	// DeviceHost is the device's own address: through a forward, a page
	// sending the window there is brought back to the local port.
	DeviceHost string `json:"deviceHost"`
	Via        int    `json:"via"`
	Lang       string `json:"lang"`
}

// guiEventCert is the line a GUI window writes when it records a
// certificate: "GUICERT <pinKey> <sha256>".
const guiEventCert = "GUICERT"

var (
	guiWinMu sync.Mutex
	guiWins  = map[*exec.Cmd]io.WriteCloser{}
)

// guiForward is one running forward.
type guiForward struct {
	f    *session.Forward
	info GUIInfo
}

var (
	guiMu sync.Mutex
	guis  = map[string]*guiForward{} // by device ID
	// guiAddrs gives each device's forward a loopback address of its own,
	// kept for the session (the browser's cookies for it stay valid).
	guiAddrs = map[string]string{}
)

// guiAddr is the loopback address device id's forward listens on.
func guiAddr(id string) string {
	guiMu.Lock()
	defer guiMu.Unlock()
	if a, ok := guiAddrs[id]; ok {
		return a
	}
	n := len(guiAddrs)
	a := fmt.Sprintf("127.0.%d.%d", n/250, n%250+2) // 127.0.0.2 … 127.0.0.251, 127.0.1.2 …
	guiAddrs[id] = a
	return a
}

// GUIInfo describes an opened GUI for the UI.
type GUIInfo struct {
	ID     string `json:"id"`
	Device string `json:"device"`
	URL    string `json:"url"`
	// Via is how many jump hosts it goes through (0 = opened directly).
	Via    int    `json:"via"`
	Target string `json:"target"` // the device's host:port
}

// OpenGUI opens the device's web GUI in the default browser. direct leaves
// its jump hosts out (踏み台を使わずに接続). An open forward for the device
// is reused, so the browser keeps its session (same port).
func (a *App) OpenGUI(id string, direct bool) (*GUIInfo, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("vault is locked")
	}
	i := a.inv.DeviceIndex(id)
	if i < 0 {
		a.mu.Unlock()
		return nil, fmt.Errorf("機器が見つかりません")
	}
	d := a.inv.Devices[i]
	if direct {
		d = d.WithoutBastions()
	}
	d = a.inv.ResolveCredentials(d)
	settings := a.inv.Settings
	pins := a.inv.KnownGUICerts
	lang := a.lang
	a.mu.Unlock()
	launch := func(info GUIInfo, firstHop string) (*GUIInfo, error) {
		key := "gui " + info.Target
		if firstHop != "" {
			key = "gui " + firstHop + ">" + info.Target
		}
		err := a.spawnGUIWindow(guiLaunch{URL: info.URL, Title: d.Name, User: d.Username, Password: d.Password,
			PinKey: key, Pin: pins[key], DeviceHost: d.Host, Via: info.Via, Lang: lang})
		if err != nil {
			return nil, err
		}
		return &info, nil
	}

	if d.Conn == model.ConnSerial || d.Host == "" {
		return nil, fmt.Errorf("シリアル接続の機器には GUI がありません")
	}
	port := d.EffectiveGUIPort()
	target := model.HopAddr(d.Host, port)
	hops := d.ActiveBastions()
	if len(hops) == 0 {
		return launch(GUIInfo{ID: d.ID, Device: d.Name, URL: model.GUIURL(d.Host, port), Target: target}, "")
	}
	hp := hops[0].Port
	if hp == 0 {
		hp = 22
	}
	firstHop := model.HopAddr(hops[0].Host, hp)

	guiMu.Lock()
	g := guis[d.ID]
	guiMu.Unlock()
	if g != nil && g.info.Target == target && g.info.Via == len(d.ActiveBastions()) {
		select {
		case <-g.f.Done():
		default:
			return launch(g.info, firstHop)
		}
	}
	if g != nil {
		g.f.Close()
	}
	f, err := a.runner.OpenForward(&d, settings, guiAddr(d.ID))
	if err != nil {
		return nil, err
	}
	g = &guiForward{f: f, info: GUIInfo{ID: d.ID, Device: d.Name, Via: len(d.ActiveBastions()), Target: target}}
	// The browser speaks to the device as it would directly: http or https
	// by the device's GUI port, now on the local one.
	g.info.URL = model.GUIScheme(port) + "://" + f.Local + "/"
	guiMu.Lock()
	guis[d.ID] = g
	guiMu.Unlock()
	go func() {
		<-f.Done()
		guiMu.Lock()
		if guis[d.ID] == g {
			delete(guis, d.ID)
		}
		guiMu.Unlock()
		runtime.EventsEmit(a.ctx, "gui:changed")
	}()
	runtime.EventsEmit(a.ctx, "gui:changed")
	return launch(g.info, firstHop)
}

// spawnGUIWindow opens a GUI window (a child PalaTerm process, tied to this
// one like the terminals) and hands it l on stdin.
func (a *App) spawnGUIWindow(l guiLaunch) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	line, err := json.Marshal(l)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--gui")
	cmd.Env = childEnv()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	tieToMainWindow(cmd.Process.Pid)
	trackChild(cmd.Process)
	_, _ = stdin.Write(append(line, '\n'))
	guiWinMu.Lock()
	guiWins[cmd] = stdin
	guiWinMu.Unlock()
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) == 4 && f[0] == guiEventCert {
				a.setGUICert(f[1]+" "+f[2], f[3])
			}
		}
		_ = cmd.Wait()
		guiWinMu.Lock()
		delete(guiWins, cmd)
		guiWinMu.Unlock()
	}()
	return nil
}

// setGUICert records a GUI window's certificate (first seen, or a change
// the user allowed) in the vault.
func (a *App) setGUICert(key, fp string) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return
	}
	if a.inv.KnownGUICerts == nil {
		a.inv.KnownGUICerts = map[string]string{}
	}
	a.inv.KnownGUICerts[key] = fp
	a.mu.Unlock()
	_ = a.persist()
}

// closeGUIWindows closes every GUI window (ロック, exit): closing its stdin
// is its signal.
func closeGUIWindows() {
	guiWinMu.Lock()
	defer guiWinMu.Unlock()
	for cmd, in := range guiWins {
		_ = in.Close()
		delete(guiWins, cmd)
	}
}

// ListGUI lists the running forwards, by device name.
func (a *App) ListGUI() []GUIInfo {
	guiMu.Lock()
	defer guiMu.Unlock()
	out := []GUIInfo{}
	for _, g := range guis {
		out = append(out, g.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}

// StopGUI stops one device's forward ("" stops them all, and closes the
// GUI windows).
func (a *App) StopGUI(id string) {
	if id == "" {
		closeGUIWindows()
	}
	guiMu.Lock()
	var stop []*guiForward
	for k, g := range guis {
		if id == "" || k == id {
			stop = append(stop, g)
			delete(guis, k)
		}
	}
	guiMu.Unlock()
	for _, g := range stop {
		g.f.Close()
	}
	if len(stop) > 0 && a.ctx != nil {
		runtime.EventsEmit(a.ctx, "gui:changed")
	}
}
