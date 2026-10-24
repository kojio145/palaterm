package session

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func writeKeyFile(t *testing.T, block *pem.Block) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPrivateKeyPlain(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path := writeKeyFile(t, block)

	if _, err := loadPrivateKey(path, ""); err != nil {
		t.Fatalf("plain key should load: %v", err)
	}
	// A passphrase left in the device's field from the protected key this
	// file replaced must not stop an unprotected key from loading: the ssh
	// package answers "key is not password protected", and the connection
	// failed on it.
	if _, err := loadPrivateKey(path, "unneeded"); err != nil {
		t.Fatalf("stray passphrase on an unprotected OpenSSH key: %v", err)
	}
}

// The same for the classic PEM shape ("EC PRIVATE KEY", as older tooling
// wrote), where the package's answer is "not an encrypted key".
func TestLoadPrivateKeyPlainPEMWithStrayPassphrase(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	path := writeKeyFile(t, &pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if _, err := loadPrivateKey(path, "unneeded"); err != nil {
		t.Fatalf("stray passphrase on an unprotected PEM key: %v", err)
	}
	if _, err := loadPrivateKey(path, ""); err != nil {
		t.Fatalf("plain PEM key should load: %v", err)
	}
}

func TestLoadPrivateKeyPassphrase(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("topsecret"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeKeyFile(t, block)

	if _, err := loadPrivateKey(path, "topsecret"); err != nil {
		t.Fatalf("correct passphrase should load: %v", err)
	}
	if _, err := loadPrivateKey(path, ""); err == nil ||
		!strings.Contains(err.Error(), "パスフレーズで保護") {
		t.Fatalf("missing passphrase should give guidance, got: %v", err)
	}
	if _, err := loadPrivateKey(path, "wrong"); err == nil ||
		!strings.Contains(err.Error(), "パスフレーズが違います") {
		t.Fatalf("wrong passphrase should be reported clearly, got: %v", err)
	}
}
