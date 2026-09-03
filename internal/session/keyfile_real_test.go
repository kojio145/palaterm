package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The existing key tests generate keys with x/crypto's own marshaller, which
// only proves the library round-trips its own output. Users arrive with files
// ssh-keygen wrote, in whichever format their machine defaults to, so these
// load the real thing.
func TestLoadPrivateKeyAcceptsSSHKeygenOutput(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not available")
	}

	cases := []struct {
		name       string
		args       []string // beyond -f and -N
		passphrase string
	}{
		{"ed25519", []string{"-t", "ed25519"}, ""},
		{"ed25519 + パスフレーズ", []string{"-t", "ed25519"}, "s3cret phrase"},
		{"rsa", []string{"-t", "rsa", "-b", "2048"}, ""},
		{"rsa + パスフレーズ", []string{"-t", "rsa", "-b", "2048"}, "s3cret phrase"},
		{"ecdsa", []string{"-t", "ecdsa"}, ""},
		// -m PEM is the classic format still handed out by older tooling.
		{"rsa PEM形式", []string{"-t", "rsa", "-b", "2048", "-m", "PEM"}, ""},
		{"rsa PEM形式 + パスフレーズ", []string{"-t", "rsa", "-b", "2048", "-m", "PEM"}, "s3cret phrase"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "id_key")
			args := append([]string{"-q", "-f", path, "-N", c.passphrase, "-C", "palaterm-test"}, c.args...)
			out, err := exec.Command(keygen, args...).CombinedOutput()
			if err != nil {
				t.Skipf("ssh-keygen %v failed: %v (%s)", c.args, err, out)
			}

			if _, err := loadPrivateKey(path, c.passphrase); err != nil {
				t.Fatalf("loadPrivateKey: %v", err)
			}
		})
	}
}

// Getting the passphrase wrong, or leaving it out, must say which of the two
// happened: both otherwise surface as an opaque parse failure, and the user
// cannot tell a typo from a field they never filled in.
func TestLoadPrivateKeyPassphraseErrorsAreSpecific(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not available")
	}
	path := filepath.Join(t.TempDir(), "id_key")
	out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-f", path,
		"-N", "right phrase", "-C", "palaterm-test").CombinedOutput()
	if err != nil {
		t.Skipf("ssh-keygen failed: %v (%s)", err, out)
	}

	_, err = loadPrivateKey(path, "")
	if err == nil {
		t.Fatal("a protected key loaded with no passphrase")
	}
	if !strings.Contains(err.Error(), "パスフレーズ") {
		t.Errorf("missing-passphrase error %q does not mention the passphrase field", err)
	}

	_, err = loadPrivateKey(path, "wrong phrase")
	if err == nil {
		t.Fatal("a protected key loaded with the wrong passphrase")
	}
	if !strings.Contains(err.Error(), "パスフレーズ") {
		t.Errorf("wrong-passphrase error %q does not mention the passphrase", err)
	}
}

// A PuTTY .ppk is not an OpenSSH key, and Windows network engineers have
// plenty of them. Refusing one is fine; refusing it with a parse error that
// says nothing about the format is not.
func TestLoadPrivateKeyRejectsPuTTYKeysClearly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.ppk")
	ppk := strings.Join([]string{
		"PuTTY-User-Key-File-3: ssh-ed25519",
		"Encryption: none",
		"Comment: palaterm-test",
		"Public-Lines: 2",
		"AAAAC3NzaC1lZDI1NTE5AAAAIGm2fake0000000000000000000000000000000",
		"AAAA",
		"Private-Lines: 1",
		"AAAAIGm2fake0000000000000000000000000000000000000000000000000000",
		"Private-MAC: 00",
	}, "\n")
	if err := os.WriteFile(path, []byte(ppk), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadPrivateKey(path, "")
	if err == nil {
		t.Fatal("a PuTTY key was accepted as an OpenSSH key")
	}
	if !strings.Contains(err.Error(), "PuTTY") {
		t.Errorf("error %q does not say the file is a PuTTY key", err)
	}
	if !strings.Contains(err.Error(), "PuTTYgen") {
		t.Errorf("error %q does not say how to convert it", err)
	}
}
