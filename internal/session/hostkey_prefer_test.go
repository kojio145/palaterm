package session

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// A server with two host keys, as OpenSSH has: which one it presents is up
// to the order the client offers. Returns the server address and the
// fingerprints VerifyHostKey would pin for each key.
func twoKeyServer(t *testing.T) (host string, port int, ecdsaFP, ed25519FP string) {
	t.Helper()
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecSigner, _ := ssh.NewSignerFromKey(ecPriv)
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	edSigner, _ := ssh.NewSignerFromKey(edPriv)
	cfg := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	cfg.AddHostKey(ecSigner)
	cfg.AddHostKey(edSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer sc.Close()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					ch, chReqs, err := nc.Accept()
					if err != nil {
						return
					}
					go func() {
						for req := range chReqs {
							req.Reply(req.Type == "shell" || req.Type == "pty-req", nil)
						}
					}()
					go func() { _, _ = io_copyDiscard(ch) }()
				}
			}()
		}
	}()
	host, port = splitAddr(t, ln.Addr().String())
	fp := func(s ssh.Signer) string { return s.PublicKey().Type() + " " + ssh.FingerprintSHA256(s.PublicKey()) }
	return host, port, fp(ecSigner), fp(edSigner)
}

// An endpoint pinned under one key type (by a version that preferred it)
// keeps connecting after the preferred order changed: the pinned type is
// offered first, so the server presents that key and the pin holds.
func TestPinnedHostKeyTypeOfferedFirst(t *testing.T) {
	host, port, ecdsaFP, ed25519FP := twoKeyServer(t)
	addr := model.HopAddr(host, port)

	// Pinned as ECDSA (what v1.9.25 and earlier recorded for such a server).
	store := &mapStore{m: map[string]string{addr: ecdsaFP}}
	opts := DialOpts{HostKey: VerifyHostKey(store), HostKeys: store}
	sess, err := dialSSH(host, port, "ops", "pw", model.AuthPassword, "", "", 5*time.Second, opts)
	if err != nil {
		t.Fatalf("pinned ECDSA must still connect: %v", err)
	}
	sess.Close()
	if store.m[addr] != ecdsaFP {
		t.Fatalf("pin changed: %q", store.m[addr])
	}

	// Pinned as ED25519 (what v1.9.26–28 recorded): the same.
	store = &mapStore{m: map[string]string{addr: ed25519FP}}
	opts = DialOpts{HostKey: VerifyHostKey(store), HostKeys: store}
	sess, err = dialSSH(host, port, "ops", "pw", model.AuthPassword, "", "", 5*time.Second, opts)
	if err != nil {
		t.Fatalf("pinned ED25519 must still connect: %v", err)
	}
	sess.Close()
	if store.m[addr] != ed25519FP {
		t.Fatalf("pin changed: %q", store.m[addr])
	}

	// No pin yet: the first key type of the list is pinned (ED25519), and
	// it holds on the next connection.
	store = &mapStore{m: map[string]string{}}
	opts = DialOpts{HostKey: VerifyHostKey(store), HostKeys: store}
	for i := 0; i < 2; i++ {
		sess, err = dialSSH(host, port, "ops", "pw", model.AuthPassword, "", "", 5*time.Second, opts)
		if err != nil {
			t.Fatalf("connection %d: %v", i+1, err)
		}
		sess.Close()
	}
	if store.m[addr] != ed25519FP {
		t.Fatalf("first sight should pin ED25519, got %q", store.m[addr])
	}

	// Without the store on the options (the v1.9.26–28 behaviour) an ECDSA
	// pin is refused — the very symptom; the test documents it.
	store = &mapStore{m: map[string]string{addr: ecdsaFP}}
	_, err = dialSSH(host, port, "ops", "pw", model.AuthPassword, "", "", 5*time.Second, DialOpts{HostKey: VerifyHostKey(store)})
	if err == nil || !strings.Contains(err.Error(), "ホストキーが前回接続時と異なります") {
		t.Fatalf("without the store the ECDSA pin should be refused, got %v", err)
	}
}

// pinnedFirst moves the pinned key's algorithms to the front, in their
// order; a pin of an unknown type, or none, leaves the list alone.
func TestPinnedFirst(t *testing.T) {
	algos := []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.CertAlgoRSASHA256v01}
	cases := []struct {
		pinned string
		want   []string
	}{
		{"", algos},
		{"ssh-ed25519 SHA256:x", algos},
		{"ecdsa-sha2-nistp256 SHA256:x", []string{ssh.KeyAlgoECDSA256, ssh.KeyAlgoED25519, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.CertAlgoRSASHA256v01}},
		{"ssh-rsa SHA256:x", []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.CertAlgoRSASHA256v01}},
		{"ssh-rsa-cert-v01@openssh.com SHA256:x", []string{ssh.CertAlgoRSASHA256v01, ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}},
		{"ssh-dss SHA256:x", algos}, // not offered without レガシー暗号: nothing to move
	}
	for _, c := range cases {
		got := pinnedFirst(algos, c.pinned)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q: got %v, want %v", c.pinned, got, c.want)
		}
	}
	// A legacy list keeps ssh-rsa with the other RSA algorithms.
	legacy := []string{ssh.KeyAlgoED25519, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA, ssh.InsecureKeyAlgoDSA}
	got := pinnedFirst(legacy, "ssh-rsa SHA256:x")
	if want := []string{ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA, ssh.KeyAlgoED25519, ssh.InsecureKeyAlgoDSA}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("legacy: got %v, want %v", got, want)
	}
	if got := pinnedFirst(legacy, "ssh-dss SHA256:x"); got[0] != ssh.InsecureKeyAlgoDSA {
		t.Errorf("legacy DSA pin: got %v", got)
	}
}
