package main

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The terminal, paste, log and diff windows are child processes. Left to
// themselves they outlived the main window — a logged-in terminal (holding
// the master password in memory) stayed open after PalaTerm was "closed".
// The main process now puts every child into a job object that ends them all
// when the main process ends, however it ends. Windows a child opens itself
// (a terminal's paste confirmation) are in the job too: a job's processes
// pass it on to theirs.

var (
	jobOnce   sync.Once
	jobHandle windows.Handle
)

// childJob returns the kill-on-close job, creating it on first use (0 if
// Windows refused; the children then simply are not tied to the main
// window, as before).
func childJob() windows.Handle {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		// BREAKAWAY_OK: an Explorer window a child opens (ログフォルダ) is
		// started outside the job (startExplorer) and outlives PalaTerm.
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(h)
			return
		}
		jobHandle = h // never closed: closing it is what ends the children
	})
	return jobHandle
}

// tieToMainWindow puts a just-started child process into the job. Only the
// main window does this; a child's own children inherit the job.
func tieToMainWindow(pid int) {
	if isChildProcess() {
		return
	}
	job := childJob()
	if job == 0 {
		return
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(p)
	_ = windows.AssignProcessToJobObject(job, p)
}

// children are the windows this (main) process started. The job ends them
// whatever happens to the main process; endChildren ends them on a normal
// exit too, for the case the job could not be set up or joined (Windows
// refused: PalaTerm itself started inside a job that forbids nesting).
var (
	childMu  sync.Mutex
	children []*os.Process
)

func trackChild(p *os.Process) {
	if isChildProcess() || p == nil {
		return
	}
	childMu.Lock()
	children = append(children, p)
	childMu.Unlock()
}

// endChildren ends every child window still running (main window exit).
func endChildren() {
	childMu.Lock()
	defer childMu.Unlock()
	for _, p := range children {
		_ = p.Kill() // an already-exited one just returns an error
	}
	children = nil
}

// startExplorer opens Explorer outside the job, so a folder window opened
// from PalaTerm stays when PalaTerm closes; if Windows refuses the breakaway
// it is started the ordinary way.
func startExplorer(args ...string) error {
	const createBreakawayFromJob = 0x01000000
	cmd := exec.Command("explorer", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createBreakawayFromJob}
	if err := cmd.Start(); err == nil {
		return nil
	}
	return exec.Command("explorer", args...).Start()
}

// isChildProcess reports whether this process is one of the child windows
// (started with --connect / --view / --diff / --paste), not the main one.
func isChildProcess() bool {
	return len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "--")
}
