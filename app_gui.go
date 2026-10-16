package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/kojio145/palaterm/internal/model"
)

// GUI を開く: the device's web GUI (FortiGate & co.) in a window of
// PalaTerm's own ("--gui", gui_window_windows.go), logged in with the
// device's credentials. Only devices with GUI を使う on, always opened
// directly at the device's address (no jump host). The windows close on
// lock and on exit.

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
	Lang   string `json:"lang"`
}

// guiEventCert is the line a GUI window writes when it records a
// certificate: "GUICERT <sha256>" (or "GUICERT ca": the browser trusted it).
// The main window stores it under the key it handed that window.
const guiEventCert = "GUICERT"

var (
	guiWinMu sync.Mutex
	guiWins  = map[*exec.Cmd]io.WriteCloser{}
)

// GUIInfo describes an opened GUI for the UI.
type GUIInfo struct {
	ID     string `json:"id"`
	Device string `json:"device"`
	URL    string `json:"url"`
}

// OpenGUI opens the device's web GUI in a GUI window, logged in with the
// device's credentials (its group's when it uses them).
func (a *App) OpenGUI(id string) (*GUIInfo, error) {
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
	d := a.inv.ResolveCredentials(a.inv.Devices[i])
	// Per device: two sites' devices often share a private address.
	key := "gui " + d.ID + " " + model.HopAddr(d.Host, d.EffectiveGUIPort())
	pin := a.inv.KnownGUICerts[key] // read under the lock: setGUICert writes the map
	lang := a.lang
	a.mu.Unlock()
	if !d.GUIOn {
		return nil, fmt.Errorf("この機器は GUI を使わない設定です（機器の編集で「GUI を使う」をオンにしてください）")
	}
	if d.Host == "" {
		return nil, fmt.Errorf("IPアドレスが入っていません")
	}
	info := GUIInfo{ID: d.ID, Device: d.Name, URL: d.GUIURL()}
	if err := a.spawnGUIWindow(guiLaunch{URL: info.URL, Title: d.Name, User: d.Username, Password: d.Password,
		PinKey: key, Pin: pin, Lang: lang}); err != nil {
		return nil, err
	}
	return &info, nil
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
			if len(f) == 2 && f[0] == guiEventCert && validPin(f[1]) {
				a.setGUICert(l.PinKey, f[1])
			}
		}
		_ = cmd.Wait()
		guiWinMu.Lock()
		delete(guiWins, cmd)
		guiWinMu.Unlock()
	}()
	return nil
}

// validPin is a certificate fingerprint (SHA-256 hex) or "ca".
func validPin(s string) bool {
	if s == "ca" {
		return true
	}
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
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
