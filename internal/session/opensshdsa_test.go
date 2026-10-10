package session

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// A DSA key in OpenSSH's own container is what a 2018+ ssh-keygen writes for
// `-t dsa`, and what the first field report in the company trial turned out
// to be ("ssh: unhandled key type"). ssh-keygen 10 refuses to make DSA keys
// at all, so this looks for an older one — Windows ships 9.5 in System32 —
// and skips when none can produce one.
func dsaKeygen(t *testing.T) string {
	t.Helper()
	candidates := []string{`C:\Windows\System32\OpenSSH\ssh-keygen.exe`}
	if p, err := exec.LookPath("ssh-keygen"); err == nil {
		candidates = append(candidates, p)
	}
	for _, kg := range candidates {
		if _, err := os.Stat(kg); err != nil {
			continue
		}
		probe := filepath.Join(t.TempDir(), "probe")
		if out, err := exec.Command(kg, "-q", "-t", "dsa", "-N", "", "-f", probe).CombinedOutput(); err == nil {
			return kg
		} else {
			t.Logf("%s cannot make DSA keys: %v (%s)", kg, err, bytes.TrimSpace(out))
		}
	}
	t.Skip("no ssh-keygen able to generate DSA keys")
	return ""
}

func genOpenSSHDSA(t *testing.T, kg, passphrase string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "id_dsa")
	out, err := exec.Command(kg, "-q", "-t", "dsa", "-N", passphrase, "-C", "palaterm-test", "-f", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen: %v (%s)", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("-----BEGIN OPENSSH PRIVATE KEY-----")) {
		t.Skipf("ssh-keygen wrote %q, not the OpenSSH container", bytes.SplitN(data, []byte("\n"), 2)[0])
	}
	return path
}

func TestLoadPrivateKeyAcceptsOpenSSHFormatDSA(t *testing.T) {
	kg := dsaKeygen(t)

	t.Run("パスフレーズなし", func(t *testing.T) {
		path := genOpenSSHDSA(t, kg, "")
		signer, err := loadPrivateKey(path, "")
		if err != nil {
			t.Fatalf("plain OpenSSH-format DSA should load: %v", err)
		}
		if got := signer.PublicKey().Type(); got != "ssh-dss" {
			t.Fatalf("key type = %q, want ssh-dss", got)
		}
		// The embedded public key must match what the signer derives, or the
		// server would see a key it has never been told about.
		pubData, err := os.ReadFile(path + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey(pubData)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pub.Marshal(), signer.PublicKey().Marshal()) {
			t.Fatal("public key derived from the private key differs from id_dsa.pub")
		}
	})

	t.Run("パスフレーズあり", func(t *testing.T) {
		path := genOpenSSHDSA(t, kg, "2Xulq passphrase")
		if _, err := loadPrivateKey(path, "2Xulq passphrase"); err != nil {
			t.Fatalf("correct passphrase should load: %v", err)
		}
		if _, err := loadPrivateKey(path, ""); err == nil || !strings.Contains(err.Error(), "パスフレーズで保護") {
			t.Fatalf("missing passphrase should give guidance, got: %v", err)
		}
		if _, err := loadPrivateKey(path, "wrong"); err == nil || !strings.Contains(err.Error(), "パスフレーズが違います") {
			t.Fatalf("wrong passphrase should be reported clearly, got: %v", err)
		}
	})
}

// End to end against a server that only takes ssh-dss, with the key exactly
// as ssh-keygen wrote it: the field case.
func TestDialSSHAuthenticatesWithOpenSSHFormatDSA(t *testing.T) {
	kg := dsaKeygen(t)
	path := genOpenSSHDSA(t, kg, "pp")
	pubData, err := os.ReadFile(path + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(pubData)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PublicKeyAuthAlgorithms: []string{ssh.InsecureKeyAlgoDSA},
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), pub.Marshal()) {
				return nil, nil
			}
			return nil, errorString("no")
		},
	}
	host, port := splitAddr(t, fakeSSHServer(t, cfg))
	sess, err := dialSSH(host, port, "koji", "", model.AuthPublicKey, path, "pp", 5*time.Second, DialOpts{})
	if err != nil {
		t.Fatalf("OpenSSH-format DSA login should succeed: %v", err)
	}
	sess.Close()
}
