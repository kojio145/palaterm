package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// The single-instance mutex (claimSingleInstance) is per Windows session and
// per path string: the same folder opened as D:\PalaTerm and as
// \\pc\share\PalaTerm, from another PC, or from another user's session got a
// second main window, and the two overwrote each other's vault. A lock file
// in data\, held open without sharing for the life of the main window, is
// the same file however it is reached — the file server refuses the second
// open wherever it comes from. Windows drops the hold when the process ends,
// however it ends, so a crash or a power cut leaves nothing stale behind.
// The terminal and other child windows never take it.

const dataLockName = "palaterm.lock"

// errDataLocked: another main PalaTerm holds the folder's lock.
var errDataLocked = errors.New("data folder in use")

// errLockHeldElsewhere: something that is not a PalaTerm (an indexer, a
// backup or sync tool) holds the lock file open — the holder line names a
// process of this PC that no longer runs.
var errLockHeldElsewhere = errors.New("lock file held by another program")

var pCreateFileW = kernel32.NewProc("CreateFileW")

// dataLock is the open lock file, held until the process ends.
var dataLock syscall.Handle

// A sharing violation is tried again for about a second before it counts:
// an indexer or a backup tool opens the file for an instant.
const (
	lockRetries   = 5
	lockRetryWait = 200 * time.Millisecond
)

// acquireDataLock opens dir\palaterm.lock for writing with no write sharing
// and records who holds it. errDataLocked (with the holder's line, when it
// can be read) means another main window has it; any other error (a
// read-only folder, errLockHeldElsewhere) is no reason to refuse to start,
// and is returned for the caller to ignore.
func acquireDataLock(dir string) (syscall.Handle, string, error) {
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, dataLockName)
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, "", err
	}
	const (
		genericRead, genericWrite = 0x80000000, 0x40000000
		fileShareRead             = 0x1
		openAlways                = 4
		attrNormal                = 0x80
	)
	var h uintptr
	for attempt := 0; ; attempt++ {
		var callErr error
		h, _, callErr = pCreateFileW.Call(uintptr(unsafe.Pointer(p)), genericRead|genericWrite, fileShareRead, 0, openAlways, attrNormal, 0)
		if syscall.Handle(h) != syscall.InvalidHandle {
			break
		}
		if errno, ok := callErr.(syscall.Errno); !ok || (errno != 32 && errno != 33) { // sharing / lock violation
			return 0, "", callErr
		}
		if attempt < lockRetries {
			time.Sleep(lockRetryWait)
			continue
		}
		holder := readLockHolder(path)
		if lockHolderGone(holder) {
			return 0, "", errLockHeldElsewhere
		}
		return 0, holder, errDataLocked
	}
	hd := syscall.Handle(h)
	host, _ := os.Hostname()
	line := fmt.Sprintf("%s|%s|%d|%s", host, os.Getenv("USERNAME"), os.Getpid(), time.Now().Format("2006-01-02 15:04"))
	_ = syscall.SetEndOfFile(hd)
	var n uint32
	_ = syscall.WriteFile(hd, []byte(line), &n, nil)
	_ = syscall.FlushFileBuffers(hd)
	return hd, line, nil
}

// readLockHolder reads the holder's line (the holder shares reading).
func readLockHolder(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// lockHolderGone reports that the holder line names a process of this PC
// that is not running: whoever holds the file open then is not the
// PalaTerm that wrote the line (a holder writes its own line the moment it
// opens the file), and the line is last time's, not to be shown as today's
// holder. A line from another PC cannot be checked and is trusted.
func lockHolderGone(line string) bool {
	return lockHolderGoneOn(line, hostName(), processAlive)
}

// lockHolderGoneOn is lockHolderGone for host, with alive telling whether a
// process of this PC runs. Pure, for the tests.
func lockHolderGoneOn(line, host string, alive func(uint32) bool) bool {
	f := strings.Split(line, "|")
	if len(f) < 4 || host == "" || !strings.EqualFold(f[0], host) {
		return false
	}
	pid, err := strconv.ParseUint(f[2], 10, 32)
	if err != nil {
		return false
	}
	return !alive(uint32(pid))
}

func hostName() string {
	h, _ := os.Hostname()
	return h
}

// lockHolderText turns "PC|user|pid|time" into the words of the refusal.
func lockHolderText(line string) string {
	f := strings.Split(line, "|")
	if len(f) < 4 || f[0] == "" {
		return ""
	}
	return winWord(
		fmt.Sprintf("（PC: %s、ユーザー: %s、%s から）", f[0], f[1], f[3]),
		fmt.Sprintf(" (PC: %s, user: %s, since %s)", f[0], f[1], f[3]))
}

// claimDataFolder takes the data folder's lock for the main window. When
// another main window — in another session, on another PC, or through
// another path — holds it, the user is told and false is returned.
func claimDataFolder() bool {
	h, holder, err := acquireDataLock(filepath.Join(exeDir(), "data"))
	if err == nil {
		dataLock = h
		return true
	}
	if !errors.Is(err, errDataLocked) {
		return true // a folder that cannot hold the lock: start as before
	}
	msg := winWord(
		"この PalaTerm のフォルダは、別の場所で開かれている PalaTerm が使用中です"+lockHolderText(holder)+"。\n\n"+
			"同じ保管庫を 2 か所から開くと、あとから保存した側が内容を上書きします。先に開いている PalaTerm を閉じてから起動してください。",
		"This PalaTerm folder is in use by PalaTerm opened elsewhere"+lockHolderText(holder)+".\n\n"+
			"With the same vault open in two places, whichever saves last overwrites the other. Close the PalaTerm that is open first, then start it again.")
	title, _ := syscall.UTF16PtrFromString("PalaTerm")
	text, _ := syscall.UTF16PtrFromString(msg)
	const mbIconWarning = 0x30
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconWarning)
	return false
}
