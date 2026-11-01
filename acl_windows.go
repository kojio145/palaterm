package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// The folders next to the exe hold this user's things: the vault and its
// backups (data\), exported bundles with passwords in them (export\), the
// devices' configurations (logs\). Go's 0600 / 0700 mean nothing to NTFS —
// a file inherits its folder's ACL, and an exe placed outside the user's
// profile (C:\Tools\PalaTerm, a second drive, a share) sits in a folder
// every account on the PC can read. protectFolder gives such a folder a
// DACL of its own (nothing inherited from the parent): the user running
// PalaTerm and SYSTEM, full control, passed on to everything made under
// it. Applied once; a folder already carrying a protected DACL (ours, or
// one the user set) is left as it is.

// protectFolder limits dir to the current user and SYSTEM (see above).
// Best effort: a folder that cannot be changed (not ours, a share that
// refuses) is logged to data\error.log and left alone, never fatal.
func protectFolder(dir string) {
	if err := setOwnerOnlyACL(dir); err != nil {
		aclLog(dir, err)
	}
}

// setOwnerOnlyACL does protectFolder's work and reports the failure.
func setOwnerOnlyACL(dir string) error {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return nil // nothing to protect (the folder is made first)
	}
	// Already protected: ours from an earlier start, or the user's own —
	// not touched again (a logs folder with thousands of files would be
	// walked at every start otherwise).
	if sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION); err == nil {
		if ctl, _, err := sd.Control(); err == nil && ctl&windows.SE_DACL_PROTECTED != 0 {
			return nil
		}
	}
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	entry := func(sid *windows.SID, kind windows.TRUSTEE_TYPE) windows.EXPLICIT_ACCESS {
		return windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  kind,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		}
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		entry(user.User.Sid, windows.TRUSTEE_IS_USER),
		entry(system, windows.TRUSTEE_IS_WELL_KNOWN_GROUP),
	}, nil)
	if err != nil {
		return err
	}
	// PROTECTED: the parent folder's entries (Users: read) stop here.
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil)
}

// aclLog notes a folder that could not be protected in data\error.log (the
// panic log's file, same size cap), for the bug report: the folder's name
// and Windows' reason, nothing of what is in it.
func aclLog(dir string, err error) {
	errorLogMu.Lock()
	defer errorLogMu.Unlock()
	p := errorLogPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if st, serr := os.Stat(p); serr == nil && st.Size() > 1<<20 {
		_ = os.Rename(p, p+".old")
	}
	if f, ferr := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); ferr == nil {
		fmt.Fprintf(f, "%s PalaTerm %s acl: %s not protected: %v\n", time.Now().Format(time.RFC3339), appVersion, filepath.Base(dir), err)
		f.Close()
	}
}

// openWithDefaultApp opens a file in the program Windows associates with
// its type (an HTML report in the browser, a CSV in Excel), the way a
// double click in Explorer does.
func openWithDefaultApp(path string) error {
	return windows.ShellExecute(0, u16("open"), u16(path), nil, nil, windows.SW_SHOWNORMAL)
}
