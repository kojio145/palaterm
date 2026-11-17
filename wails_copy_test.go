package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// tools/mkwails.go against the Wails this module names: the copy a release
// build is made with comes out as meant (a Wails upgrade that changes
// form.go stops here, before a build), names the same variables as this
// side, and the changed package compiles.
func TestWailsCopy(t *testing.T) {
	if testing.Short() {
		t.Skip("go run / go build を呼ぶ")
	}
	out := t.TempDir()
	if b, err := exec.Command("go", "run", "tools/mkwails.go", "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("mkwails: %v\n%s", err, b)
	}
	form, err := os.ReadFile(filepath.Join(out, "wails", "internal", "frontend", "desktop", "windows", "winc", "form.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`func init() { os.Setenv("` + wailsPatchEnv + `", strconv.Itoa(os.Getpid())) }`,
		`os.Getenv("` + hiddenStartEnv + `") == strconv.Itoa(os.Getpid())`,
		"!w32.IsWindowVisible(fm.hwnd)",
		"flags |= w32.SWP_NOACTIVATE | w32.SWP_NOZORDER",
	} {
		if !bytes.Contains(form, []byte(want)) {
			t.Errorf("form.go に無い: %s", want)
		}
	}
	if bytes.Contains(form, []byte("int(winHeight), w32.SWP_NOSIZE)")) {
		t.Error("form.go に元の中央寄せが残っている")
	}
	if bytes.Contains(form, []byte("\r\n")) {
		t.Error("form.go の改行が変わった（同じソースから同じ exe にならない）")
	}
	mod, _ := os.ReadFile(filepath.Join(out, "release.mod"))
	if !bytes.Contains(mod, []byte("replace github.com/wailsapp/wails/v2 => ")) {
		t.Errorf("release.mod に replace が無い:\n%s", mod)
	}
	if _, err := os.Stat(filepath.Join(out, "release.sum")); err != nil {
		t.Error(err)
	}

	if runtime.GOOS != "windows" {
		return
	}
	cmd := exec.Command("go", "build", "-modfile", filepath.Join(out, "release.mod"),
		"github.com/wailsapp/wails/v2/internal/frontend/desktop/windows/winc")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("直した winc がコンパイルできない: %v\n%s", err, b)
	}
}

// This test binary is built with Wails as it comes: it does not say that
// the changed Center is there, so no spare paste window is kept.
func TestWailsAsItComes(t *testing.T) {
	if wailsLeavesHiddenFrames() || pasteSpareOn {
		t.Errorf("直していない Wails なのに待機窓がオン: %v %v", wailsLeavesHiddenFrames(), pasteSpareOn)
	}
}
