package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	// NoAutoLogin: an earlier window of this device sent these credentials
	// and the login form came back (refused); this window fills them in
	// but does not press the button — a wrong password sent once per
	// window opened would count towards locking the user out.
	NoAutoLogin bool `json:"noAutoLogin,omitempty"`
}

// guiEventCert is the line a GUI window writes when it records a
// certificate: "GUICERT <sha256>" (or "GUICERT ca": the browser trusted it).
// The main window stores it under the key it handed that window.
const guiEventCert = "GUICERT"

// guiEventActive is the line a GUI window writes when it is being used
// (keys, mouse), at most every few seconds. The main window's idle lock
// counts it as activity: locking closes the GUI windows, and a FortiGate
// policy edited for forty minutes used to vanish because the main window
// sat untouched meanwhile.
const guiEventActive = "ACTIVE"

// guiEventLoginFail is the line a GUI window writes when the login it sent
// automatically was refused (the login form came back). The main window
// remembers it for the device and these credentials until PalaTerm exits
// or the credentials are edited: the next window fills in, does not send.
const guiEventLoginFail = "GUILOGINFAIL"

var (
	guiWinMu sync.Mutex
	guiWins  = map[*exec.Cmd]io.WriteCloser{}
	// guiActiveAt is when a GUI window last reported activity (unix ms).
	guiActiveAt atomic.Int64
	// guiLoginFailed: per device ID, the credentials a GUI window sent and
	// had refused (under guiWinMu; for this process's life).
	guiLoginFailed = map[string]string{}
)

// guiCredKey is what guiLoginFailed remembers of the credentials: the user
// name and a digest of the password, never the password itself.
func guiCredKey(user, password string) string {
	sum := sha256.Sum256([]byte(password))
	return user + "\x00" + hex.EncodeToString(sum[:])
}

// guiLoginRefused reports that a window of device id sent these credentials
// and had them refused.
func guiLoginRefused(id, user, password string) bool {
	guiWinMu.Lock()
	defer guiWinMu.Unlock()
	return id != "" && guiLoginFailed[id] == guiCredKey(user, password)
}

// noteGUILoginRefused remembers that a window of device id had these
// credentials refused.
func noteGUILoginRefused(id, user, password string) {
	if id == "" {
		return
	}
	guiWinMu.Lock()
	guiLoginFailed[id] = guiCredKey(user, password)
	guiWinMu.Unlock()
}

// GUIActiveAgoMs is how long ago a GUI window was last used, in
// milliseconds; -1 when none is open or none has reported anything.
func (a *App) GUIActiveAgoMs() int64 {
	if openGUIWindows() == 0 {
		return -1
	}
	at := guiActiveAt.Load()
	if at == 0 {
		return -1
	}
	return time.Now().UnixMilli() - at
}

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
	key := d.GUICertKey()
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
		PinKey: key, Pin: pin, Lang: lang, NoAutoLogin: guiLoginRefused(d.ID, d.Username, d.Password)}, d.ID); err != nil {
		return nil, err
	}
	return &info, nil
}

// guiChildEnv is childEnv without the WEBVIEW2_* variables: the WebView2
// loader reads them (WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS, _USER_DATA_FOLDER,
// _BROWSER_EXECUTABLE_FOLDER) over what the window asks for, and a
// --remote-debugging-port planted that way would open the logged-in page
// to any process, the throw-away profile moved elsewhere.
func guiChildEnv() []string {
	var env []string
	for _, kv := range childEnv() {
		if strings.HasPrefix(strings.ToUpper(kv), "WEBVIEW2_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// spawnGUIWindow opens a GUI window (a child PalaTerm process, tied to this
// one like the terminals) and hands it l on stdin. deviceID is what a
// refused login is remembered under.
func (a *App) spawnGUIWindow(l guiLaunch, deviceID string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	line, err := json.Marshal(l)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--gui")
	cmd.Env = guiChildEnv()
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
	// Registered before the password goes down the pipe: a ロック or exit
	// in between (closeGUIWindows) then closes this pipe too, and the window
	// reads an end instead of a login.
	guiWinMu.Lock()
	guiWins[cmd] = stdin
	guiWinMu.Unlock()
	tieToMainWindow(cmd.Process.Pid)
	trackChild(cmd.Process)
	_, _ = stdin.Write(append(line, '\n'))
	go func() {
		// A panic here (setGUICert → persist) must not end the main process
		// and every terminal window with it.
		defer recoverGo("guiLine")
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) == 2 && f[0] == guiEventCert && validPin(f[1]) {
				a.setGUICert(l.PinKey, f[1])
			}
			if len(f) == 1 && f[0] == guiEventActive {
				guiActiveAt.Store(time.Now().UnixMilli())
			}
			if len(f) == 1 && f[0] == guiEventLoginFail {
				noteGUILoginRefused(deviceID, l.User, l.Password)
			}
		}
		// Scan stops short of EOF on a line over its buffer (ErrTooLong):
		// read on to the end regardless, or the child blocks on a full
		// pipe, Wait never returns and the window stays counted as open.
		_, _ = io.Copy(io.Discard, stdout)
		_ = cmd.Wait()
		guiWinMu.Lock()
		delete(guiWins, cmd)
		last := len(guiWins) == 0
		guiWinMu.Unlock()
		// The last window gone (closed, or killed by the exit or a crash):
		// the profiles no running window owns are deleted now, not at the
		// next start of PalaTerm.
		if last {
			go cleanStaleGUIProfiles()
		}
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
// the user allowed) in the vault — under key only while a device still
// answers to it: the window was handed its key when it opened, and the
// device may have been deleted or given another address since (the pin
// went with it, and would otherwise come back as an orphan).
func (a *App) setGUICert(key, fp string) {
	a.mu.Lock()
	if a.inv == nil || !guiCertKeyInUse(a.inv, key) {
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

// guiCertKeyInUse reports that some device of inv pins its GUI certificate
// under key.
func guiCertKeyInUse(inv *model.Inventory, key string) bool {
	for i := range inv.Devices {
		if inv.Devices[i].GUICertKey() == key {
			return true
		}
	}
	return false
}

// closeGUIWindows closes every GUI window (ロック, exit): closing its stdin
// is its signal.
func closeGUIWindows() {
	guiWinMu.Lock()
	defer guiWinMu.Unlock()
	// Closing stdin is the close request; the entry goes when the process
	// has exited (spawnGUIWindow's waiter), so openGUIWindows can tell a
	// window still deleting its profile from one that is gone.
	for _, in := range guiWins {
		_ = in.Close()
	}
}

// openGUIWindows is how many GUI window processes are still running.
func openGUIWindows() int {
	guiWinMu.Lock()
	defer guiWinMu.Unlock()
	return len(guiWins)
}
