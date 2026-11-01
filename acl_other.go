//go:build !windows

package main

import "fmt"

// protectFolder: the folder ACL is a Windows matter (acl_windows.go); on
// other systems the 0700 / 0600 modes the files are made with do the job.
func protectFolder(dir string) {}

// openWithDefaultApp: opening a file in its program is Windows-only here.
func openWithDefaultApp(path string) error {
	return fmt.Errorf("この OS ではファイルを開けません")
}
