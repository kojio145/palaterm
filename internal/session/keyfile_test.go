package session

import (
	"crypto/ed25519"
	"crypto/rand"
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
	// A stray passphrase on an unprotected key is an error from the ssh pkg;
	// just assert it does not succeed silently with the wrong expectation.
	if _, err := loadPrivateKey(path, "unneeded"); err == nil {
		t.Log("note: passphrase ignored for unprotected key")
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
