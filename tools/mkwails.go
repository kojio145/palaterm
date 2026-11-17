//go:build ignore

// mkwails makes what a release build is built with in place of Wails as it
// comes: a copy of the Wails module with one function changed, and a go.mod
// that names the copy.
//
//	go run tools/mkwails.go                      (from the repository root)
//	go build -modfile build/release.mod ...
//
// It writes build/wails (the copy), build/release.mod (go.mod plus
// "replace github.com/wailsapp/wails/v2 => ./build/wails") and
// build/release.sum. Nothing else is changed: not the module cache, not
// go.mod. -o DIR writes them somewhere else (the replace then names DIR by
// its full path, which a build that is to come out the same everywhere
// must not have).
//
// What is changed is Center in winc/form.go. Wails centres every new frame
// with a SetWindowPos that also makes it the active window — shown or not.
// A paste window started hidden ahead of the paste (the spare, see
// Term.respare) took the keyboard from the terminal for some tens of
// milliseconds that way, and letters typed just then were lost. The
// changed Center leaves a frame that is still hidden where it is, and only
// in a process that asks for it by its own id (hiddenStartEnv, set for a
// window started hidden): every other window is centred exactly as Wails
// does it. The changed file also says that it is there (wailsPatchEnv), and
// the spare is kept only in a build that has it — an ordinary go build has
// neither the change nor the spare.
//
// The Wails file must be the one this was tried with (formSHA): after a
// Wails upgrade this stops the build until the change has been looked at
// again, tried and the hash updated.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	wailsModule = "github.com/wailsapp/wails/v2"
	formRel     = "internal/frontend/desktop/windows/winc/form.go"
	// formSHA is the SHA-256 of form.go in Wails v2.9.2.
	formSHA = "4f1b2c7c1b3ed8cbbbcf29d876b1502d6c87d2d39e640a4bcd52e57eb9f59944"
)

type edit struct{ old, new string }

var formEdits = []edit{
	{
		"import (\n\t\"unsafe\"\n",
		"import (\n\t\"os\"\n\t\"strconv\"\n\t\"unsafe\"\n",
	},
	{
		"// A Form is main window of the application.\n",
		"// PalaTerm (tools/mkwails.go): says that this build has the Center below.\n" +
			"func init() { os.Setenv(\"PALATERM_WAILS_PATCH\", strconv.Itoa(os.Getpid())) }\n" +
			"\n" +
			"// A Form is main window of the application.\n",
	},
	{
		"\tw32.SetWindowPos(fm.hwnd, w32.HWND_TOP, int(windowX), int(windowY), int(winWidth), int(winHeight), w32.SWP_NOSIZE)\n",
		"\t// PalaTerm (tools/mkwails.go): a frame that is still hidden is not made\n" +
			"\t// the active window by being centred, in a process that asks for it.\n" +
			"\tflags := uint(w32.SWP_NOSIZE)\n" +
			"\tif !w32.IsWindowVisible(fm.hwnd) && os.Getenv(\"PALATERM_HIDDEN_START\") == strconv.Itoa(os.Getpid()) {\n" +
			"\t\tflags |= w32.SWP_NOACTIVATE | w32.SWP_NOZORDER\n" +
			"\t}\n" +
			"\tw32.SetWindowPos(fm.hwnd, w32.HWND_TOP, int(windowX), int(windowY), int(winWidth), int(winHeight), flags)\n",
	},
}

func main() {
	out := flag.String("o", "", "where to write (default: build, named in release.mod as ./build/wails)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: go run tools/mkwails.go [-o DIR]   (from the repository root)")
	}
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "mkwails:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	// The copy is named in release.mod as it will be read: from the
	// repository root for the default place, so that the same source gives
	// the same exe wherever it is built.
	replace := "./build/wails"
	if out == "" {
		out = "build"
	} else {
		abs, err := filepath.Abs(out)
		if err != nil {
			return err
		}
		out, replace = abs, filepath.Join(abs, "wails")
	}
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		return fmt.Errorf("%w (run from the repository root)", err)
	}
	sum, err := os.ReadFile("go.sum")
	if err != nil {
		return err
	}
	// Fetched (and checked against go.sum) if it is not in the module cache yet.
	b, err := exec.Command("go", "mod", "download", "-json", wailsModule).Output()
	var got struct{ Dir, Error string }
	if jerr := json.Unmarshal(b, &got); err != nil || jerr != nil || got.Dir == "" {
		return fmt.Errorf("go mod download %s: %v %s", wailsModule, err, strings.TrimSpace(got.Error+" "+string(b)))
	}
	src := got.Dir

	form, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(formRel)))
	if err != nil {
		return err
	}
	if s := sha256.Sum256(form); hex.EncodeToString(s[:]) != formSHA {
		return fmt.Errorf("%s in %s is not the file this change was tried with (SHA-256 %s, want %s): Wails was upgraded? Look at Center again, try the build, then update formSHA",
			formRel, src, hex.EncodeToString(s[:]), formSHA)
	}
	for _, e := range formEdits {
		if n := bytes.Count(form, []byte(e.old)); n != 1 {
			return fmt.Errorf("%s: expected one place to change, found %d: %q", formRel, n, e.old)
		}
		form = bytes.Replace(form, []byte(e.old), []byte(e.new), 1)
	}

	dst := filepath.Join(out, "wails")
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := copyTree(src, dst); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dst, filepath.FromSlash(formRel)), form, 0o644); err != nil {
		return err
	}

	modFile := filepath.Join(out, "release.mod")
	if err := os.WriteFile(modFile, mod, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "release.sum"), sum, 0o644); err != nil {
		return err
	}
	if b, err := exec.Command("go", "mod", "edit", "-replace="+wailsModule+"="+filepath.ToSlash(replace), modFile).CombinedOutput(); err != nil {
		return fmt.Errorf("go mod edit: %w\n%s", err, b)
	}
	return nil
}

// copyTree copies the files under src to dst, writable (the module cache
// is read-only).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
}
