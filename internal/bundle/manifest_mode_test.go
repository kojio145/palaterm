package bundle

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The manifest (the groups' default credentials in it) is written as
// private as devices.csv. Windows has no such mode bits: checked where
// they exist.
func TestManifestWrittenPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not kept on Windows")
	}
	dir := t.TempDir()
	mp, err := Write(dir, Select(sampleInventory(), "A社", false), "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{mp, filepath.Join(dir, "devices.csv")} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(p), st.Mode().Perm())
		}
	}
}
