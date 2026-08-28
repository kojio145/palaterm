package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

type mapStore struct{ m map[string]string }

func (s *mapStore) GetHostKey(addr string) string { return s.m[addr] }
func (s *mapStore) SetHostKey(addr, fp string)    { s.m[addr] = fp }

func genKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TOFU: first key is pinned, the same key passes, a different key hard-fails.
func TestVerifyHostKeyTOFU(t *testing.T) {
	store := &mapStore{m: map[string]string{}}
	cb := VerifyHostKey(store)
	k1, k2 := genKey(t), genKey(t)

	if err := cb("10.0.0.1:22", nil, k1); err != nil {
		t.Fatalf("first sight should pin, got %v", err)
	}
	if store.m["10.0.0.1:22"] == "" {
		t.Fatal("fingerprint not stored")
	}
	if err := cb("10.0.0.1:22", nil, k1); err != nil {
		t.Fatalf("same key should pass, got %v", err)
	}
	err := cb("10.0.0.1:22", nil, k2)
	if err == nil {
		t.Fatal("changed key must be rejected")
	}
	if !strings.Contains(err.Error(), "ホストキーが前回接続時と異なります") {
		t.Fatalf("unexpected error: %v", err)
	}
	// A different host is independent.
	if err := cb("10.0.0.2:22", nil, k2); err != nil {
		t.Fatalf("other host first sight should pin, got %v", err)
	}
}

// nil store (tests/fakes) accepts anything.
func TestVerifyHostKeyNilStore(t *testing.T) {
	cb := VerifyHostKey(nil)
	if err := cb("x:22", nil, genKey(t)); err != nil {
		t.Fatal(err)
	}
}
