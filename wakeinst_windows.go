package main

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Two pieces of Win32 the 予約 need: a wake-up timer that brings the PC out
// of sleep for the next firing, and one main PalaTerm per folder.

var (
	pCreateWaitableTimer = kernel32.NewProc("CreateWaitableTimerW")
	pSetWaitableTimer    = kernel32.NewProc("SetWaitableTimer")
	pCancelWaitableTimer = kernel32.NewProc("CancelWaitableTimer")
	pCreateMutex         = kernel32.NewProc("CreateMutexW")
)

// wakeTimer is the process's waitable timer with "resume" set: when it comes
// due Windows wakes from sleep (S3 / modern standby) if the power plan allows
// wake timers. Nobody waits on it — waking the PC is all it is for; the
// scheduler's own loop then sees the time and runs the firing.
var wakeTimer struct {
	mu     sync.Mutex
	h      uintptr
	at     time.Time
	armed  bool   // the timer holds at (set, or cancelled for a zero at)
	status string // "" / "ok" / the reason it could not be set
}

// fileTimeOf is t as a Windows FILETIME count (100 ns since 1601, UTC).
func fileTimeOf(t time.Time) int64 {
	return t.UnixNano()/100 + 116444736000000000
}

// armWake sets the wake-up for at (zero: cancel it). A time the timer
// already holds is not re-set; one it failed to take is tried again at the
// next call (the scheduler asks every second), so a passing failure does not
// leave the PC unable to wake for that firing. Returns the status shown in
// the 予約 tab.
func armWake(at time.Time) string {
	wakeTimer.mu.Lock()
	defer wakeTimer.mu.Unlock()
	if wakeTimer.armed && at.Equal(wakeTimer.at) {
		return wakeTimer.status
	}
	wakeTimer.armed = false
	if wakeTimer.h == 0 {
		h, _, err := pCreateWaitableTimer.Call(0, 1, 0)
		if h == 0 {
			wakeTimer.status = "CreateWaitableTimer: " + err.Error()
			return wakeTimer.status
		}
		wakeTimer.h = h
	}
	if at.IsZero() {
		pCancelWaitableTimer.Call(wakeTimer.h)
		wakeTimer.at, wakeTimer.armed, wakeTimer.status = at, true, "none"
		return wakeTimer.status
	}
	due := fileTimeOf(at) // positive = absolute time
	const resume = 1
	r, _, err := pSetWaitableTimer.Call(wakeTimer.h, uintptr(unsafe.Pointer(&due)), 0, 0, 0, resume)
	switch {
	case r == 0:
		wakeTimer.status = "SetWaitableTimer: " + err.Error()
		return wakeTimer.status
	case err == syscall.Errno(50): // ERROR_NOT_SUPPORTED: set, but cannot wake this PC
		wakeTimer.status = "unsupported"
	default:
		wakeTimer.status = "ok"
	}
	wakeTimer.at, wakeTimer.armed = at, true
	return wakeTimer.status
}

var (
	powrprof                = syscall.NewLazyDLL("powrprof.dll")
	pCallNtPowerInformation = powrprof.NewProc("CallNtPowerInformation")
	pPowerGetActiveScheme   = powrprof.NewProc("PowerGetActiveScheme")
	pPowerReadACValueIndex  = powrprof.NewProc("PowerReadACValueIndex")
	pPowerReadDCValueIndex  = powrprof.NewProc("PowerReadDCValueIndex")
	pLocalFree              = kernel32.NewProc("LocalFree")
	pGetSystemPowerStatus   = kernel32.NewProc("GetSystemPowerStatus")
	guidSleepSubgroup       = syscall.GUID{Data1: 0x238c9fa8, Data2: 0x0aad, Data3: 0x41ed, Data4: [8]byte{0x83, 0xf4, 0x97, 0xbe, 0x24, 0x2c, 0x8f, 0x20}}
	// Power buttons and lid: what closing the lid does (0 nothing, 1 sleep,
	// 2 hibernate, 3 shut down).
	guidButtonSubgroup         = syscall.GUID{Data1: 0x4f971e89, Data2: 0xeebd, Data3: 0x4455, Data4: [8]byte{0xa8, 0xde, 0x9e, 0x59, 0x04, 0x0e, 0x73, 0x47}}
	guidLidAction              = syscall.GUID{Data1: 0x5ca83367, Data2: 0x6e45, Data3: 0x459f, Data4: [8]byte{0xa2, 0x7b, 0x47, 0x6b, 0x1d, 0x01, 0xc9, 0x36}}
	guidAllowWakeTimers        = syscall.GUID{Data1: 0xbd3b718a, Data2: 0x0680, Data3: 0x4d9d, Data4: [8]byte{0x8a, 0xb2, 0xe1, 0xd2, 0xb4, 0xac, 0x80, 0x6d}}
	systemPowerCapabilitiesLvl = uintptr(4) // SystemPowerCapabilities
)

// PowerInfo is what the 予約 tab needs to know about this PC's sleep: a
// schedule can only run during sleep if the PC can be woken for it.
type PowerInfo struct {
	// ModernStandby (S0 low-power idle): Windows suspends desktop apps while
	// in standby and may not honour an app's wake timer.
	ModernStandby bool `json:"modernStandby"`
	S3            bool `json:"s3"`
	// WakeTimersAC / DC: the power plan's "Allow wake timers" (0 off,
	// 1 on, 2 important only, -1 unknown) on mains / battery.
	WakeTimersAC int  `json:"wakeTimersAC"`
	WakeTimersDC int  `json:"wakeTimersDC"`
	OnAC         bool `json:"onAC"`
	// LidPresent: the PC has a lid (a laptop). LidActionAC / DC: what
	// closing it does on mains / battery (0 nothing, 1 sleep, 2 hibernate,
	// 3 shut down, -1 unknown) — anything but 0 stops a running batch.
	LidPresent  bool `json:"lidPresent"`
	LidActionAC int  `json:"lidActionAC"`
	LidActionDC int  `json:"lidActionDC"`
}

// readPowerInfo asks Windows about sleep states and the wake-timer setting
// (no administrator rights needed).
func readPowerInfo() PowerInfo {
	pi := PowerInfo{WakeTimersAC: -1, WakeTimersDC: -1, OnAC: true, LidActionAC: -1, LidActionDC: -1}
	// SYSTEM_POWER_CAPABILITIES: 15 BOOLEANs from PowerButtonPresent to
	// ProcessorThrottle (SystemS3 is the 6th), then two BYTEs and
	// FastSystemS4, Hiberboot, WakeAlarmPresent, AoAc (offset 20).
	var caps [128]byte
	if r, _, _ := pCallNtPowerInformation.Call(systemPowerCapabilitiesLvl, 0, 0, uintptr(unsafe.Pointer(&caps[0])), uintptr(len(caps))); r == 0 {
		pi.LidPresent = caps[2] != 0
		pi.S3 = caps[5] != 0
		pi.ModernStandby = caps[20] != 0
	}
	var scheme *syscall.GUID
	if r, _, _ := pPowerGetActiveScheme.Call(0, uintptr(unsafe.Pointer(&scheme))); r == 0 && scheme != nil {
		var v uint32
		if r, _, _ := pPowerReadACValueIndex.Call(0, uintptr(unsafe.Pointer(scheme)), uintptr(unsafe.Pointer(&guidSleepSubgroup)), uintptr(unsafe.Pointer(&guidAllowWakeTimers)), uintptr(unsafe.Pointer(&v))); r == 0 {
			pi.WakeTimersAC = int(v)
		}
		if r, _, _ := pPowerReadDCValueIndex.Call(0, uintptr(unsafe.Pointer(scheme)), uintptr(unsafe.Pointer(&guidSleepSubgroup)), uintptr(unsafe.Pointer(&guidAllowWakeTimers)), uintptr(unsafe.Pointer(&v))); r == 0 {
			pi.WakeTimersDC = int(v)
		}
		if r, _, _ := pPowerReadACValueIndex.Call(0, uintptr(unsafe.Pointer(scheme)), uintptr(unsafe.Pointer(&guidButtonSubgroup)), uintptr(unsafe.Pointer(&guidLidAction)), uintptr(unsafe.Pointer(&v))); r == 0 {
			pi.LidActionAC = int(v)
		}
		if r, _, _ := pPowerReadDCValueIndex.Call(0, uintptr(unsafe.Pointer(scheme)), uintptr(unsafe.Pointer(&guidButtonSubgroup)), uintptr(unsafe.Pointer(&guidLidAction)), uintptr(unsafe.Pointer(&v))); r == 0 {
			pi.LidActionDC = int(v)
		}
		pLocalFree.Call(uintptr(unsafe.Pointer(scheme)))
	}
	// SYSTEM_POWER_STATUS: ACLineStatus is the first byte (0 off, 1 on).
	var ps [12]byte
	if r, _, _ := pGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&ps[0]))); r != 0 && ps[0] == 0 {
		pi.OnAC = false
	}
	return pi
}

// powerInfoCache keeps readPowerInfo's answer for powerInfoTTL: the 予約
// tab polls the status every few seconds, and each read is six Win32 power
// queries that answer the same thing for hours.
var powerInfoCache struct {
	mu sync.Mutex
	at time.Time
	pi PowerInfo
}

const powerInfoTTL = 10 * time.Second

// cachedPowerInfo is readPowerInfo, at most once per powerInfoTTL.
func cachedPowerInfo() PowerInfo {
	powerInfoCache.mu.Lock()
	defer powerInfoCache.mu.Unlock()
	if now := time.Now(); powerInfoCache.at.IsZero() || now.Sub(powerInfoCache.at) > powerInfoTTL {
		powerInfoCache.pi, powerInfoCache.at = readPowerInfo(), now
	}
	return powerInfoCache.pi
}

// instanceMutex is held for the life of the main window's process.
var instanceMutex uintptr

// claimSingleInstance makes sure only one main PalaTerm runs from this
// folder (one vault, one tray, one scheduler: two would both fire every
// 予約 and fight over the vault). A second start shows the first one's
// window — it may be hidden in the tray — and reports false so the caller
// exits. The mutex name carries a hash of the folder, so copies of PalaTerm
// in different folders (different vaults) still run side by side.
func claimSingleInstance() bool {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(exeDir()))))
	name, _ := syscall.UTF16PtrFromString(`Local\PalaTerm-` + hex.EncodeToString(sum[:8]))
	h, _, err := pCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h != 0 && err == syscall.Errno(183) { // ERROR_ALREADY_EXISTS
		if w := mainWindowOfFolder(exeDir()); w != 0 {
			const swShow, swRestore = 5, 9
			pShowWindow.Call(w, swShow)
			pShowWindow.Call(w, swRestore)
			pSetForegroundWindow.Call(w)
		}
		return false
	}
	instanceMutex = h
	return true
}

// mainWindowOfFolder finds the main window of the PalaTerm running from dir
// (shown or hidden in the tray), or 0: a Wails frame titled "PalaTerm" — the
// child windows say "PalaTerm - …" — whose process runs an exe in that
// folder. Looking the title up alone (FindWindow) raised another folder's
// copy, or any window that happened to be titled PalaTerm.
func mainWindowOfFolder(dir string) uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumMainDir = strings.ToLower(filepath.Clean(dir))
	enumMainSelf, _, _ = pGetCurrentProcessId.Call()
	enumFound = 0
	pEnumWindows.Call(enumMainCB, 0)
	return enumFound
}

// The callback is made once (see winfocus.go: Go never frees one); it reads
// its arguments from these, held by enumMu for the length of one walk.
var (
	enumMainDir    string
	enumMainSelf   uintptr
	enumMainCB     = syscall.NewCallback(enumMainProc)
	pGetWindowText = user32.NewProc("GetWindowTextW")
)

func enumMainProc(h uintptr, _ uintptr) uintptr {
	var wpid uint32
	pGetWindowThreadProcess.Call(h, uintptr(unsafe.Pointer(&wpid)))
	if wpid == 0 || uintptr(wpid) == enumMainSelf {
		return 1
	}
	buf := make([]uint16, 64)
	n, _, _ := pGetClassName.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if syscall.UTF16ToString(buf[:n]) != "wailsWindow" {
		return 1
	}
	n, _, _ = pGetWindowText.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if syscall.UTF16ToString(buf[:n]) != "PalaTerm" {
		return 1
	}
	if strings.ToLower(filepath.Dir(processImage(wpid))) != enumMainDir {
		return 1
	}
	enumFound = h
	return 0
}

// processImage is the full path of a process's exe, or "".
func processImage(pid uint32) string {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(p)
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	size := uint32(len(buf))
	if windows.QueryFullProcessImageName(p, 0, &buf[0], &size) != nil {
		return ""
	}
	return filepath.Clean(syscall.UTF16ToString(buf[:size]))
}
