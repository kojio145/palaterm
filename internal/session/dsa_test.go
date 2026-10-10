package session

import (
	"bytes"
	"crypto/dsa" //nolint:staticcheck // the point is to accept the old keys users still have
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// A 2018-vintage Tera Term macro logs into its jump server with
// `/auth=publickey /keyfile=id_dsa`. ssh-keygen no longer even generates DSA
// keys, so the one here is made in-process and written in the classic PEM
// layout those files have (OpenSSL's "DSA PRIVATE KEY": version, P, Q, G,
// pub, priv). Parameter generation is the slow part, so it runs once.
var (
	dsaOnce sync.Once
	dsaKey  *dsa.PrivateKey
)

func testDSAKey(t *testing.T) *dsa.PrivateKey {
	t.Helper()
	dsaOnce.Do(func() {
		k := &dsa.PrivateKey{}
		if err := dsa.GenerateParameters(&k.Parameters, rand.Reader, dsa.L1024N160); err != nil {
			t.Fatal(err)
		}
		if err := dsa.GenerateKey(k, rand.Reader); err != nil {
			t.Fatal(err)
		}
		dsaKey = k
	})
	return dsaKey
}

func dsaPEM(t *testing.T, k *dsa.PrivateKey) *pem.Block {
	t.Helper()
	der, err := asn1.Marshal(struct {
		Version int
		P, Q, G *big.Int
		Pub     *big.Int
		Priv    *big.Int
	}{0, k.P, k.Q, k.G, k.Y, k.X})
	if err != nil {
		t.Fatal(err)
	}
	return &pem.Block{Type: "DSA PRIVATE KEY", Bytes: der}
}

func writeDSAFile(t *testing.T, block *pem.Block) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "id_dsa")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPrivateKeyAcceptsClassicDSA(t *testing.T) {
	k := testDSAKey(t)
	path := writeDSAFile(t, dsaPEM(t, k))
	signer, err := loadPrivateKey(path, "")
	if err != nil {
		t.Fatalf("plain DSA PEM should load: %v", err)
	}
	if got := signer.PublicKey().Type(); got != "ssh-dss" {
		t.Fatalf("key type = %q, want ssh-dss", got)
	}
}

// Tera Term passes the key's passphrase as /passwd. Such keys are encrypted
// the OpenSSL way (DEK-Info header), which is what x509.EncryptPEMBlock
// produces; its deprecation is about the scheme being weak, not about files
// in that form no longer existing.
func TestLoadPrivateKeyAcceptsEncryptedDSA(t *testing.T) {
	k := testDSAKey(t)
	plain := dsaPEM(t, k)
	enc, err := x509.EncryptPEMBlock(rand.Reader, plain.Type, plain.Bytes, []byte("tera term passwd"), x509.PEMCipher3DES) //nolint:staticcheck
	if err != nil {
		t.Fatal(err)
	}
	path := writeDSAFile(t, enc)

	if _, err := loadPrivateKey(path, "tera term passwd"); err != nil {
		t.Fatalf("correct passphrase should load: %v", err)
	}
	if _, err := loadPrivateKey(path, ""); err == nil || !strings.Contains(err.Error(), "パスフレーズで保護") {
		t.Fatalf("missing passphrase should give guidance, got: %v", err)
	}
	if _, err := loadPrivateKey(path, "wrong"); err == nil || !strings.Contains(err.Error(), "パスフレーズが違います") {
		t.Fatalf("wrong passphrase should be reported clearly, got: %v", err)
	}
}

// fakeSSHServer answers one connection with cfg and grants pty/shell requests
// on session channels, which is all dialSSH needs to return a Session. It
// reports only the dial's view of things; the shell itself is never used.
func fakeSSHServer(t *testing.T, cfg *ssh.ServerConfig) string {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AddHostKey(hostSigner)

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
					conn.Close()
					return
				}
				defer sc.Close()
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					ch, chReqs, err := newCh.Accept()
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
	return ln.Addr().String()
}

func io_copyDiscard(ch ssh.Channel) (int64, error) {
	buf := make([]byte, 4096)
	var n int64
	for {
		m, err := ch.Read(buf)
		n += int64(m)
		if err != nil {
			return n, err
		}
	}
}

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	return host, port
}

// The client must actually offer ssh-dss to a server that accepts nothing
// else — loading the key proves little if the auth step then skips it.
func TestDialSSHAuthenticatesWithDSAKey(t *testing.T) {
	k := testDSAKey(t)
	path := writeDSAFile(t, dsaPEM(t, k))
	pub, err := ssh.NewPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PublicKeyAuthAlgorithms: append(ssh.SupportedAlgorithms().PublicKeyAuths, ssh.InsecureAlgorithms().PublicKeyAuths...),
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == "koji" && bytes.Equal(key.Marshal(), pub.Marshal()) {
				return nil, nil
			}
			return nil, errorString("no")
		},
	}
	host, port := splitAddr(t, fakeSSHServer(t, cfg))

	sess, err := dialSSH(host, port, "koji", "", model.AuthPublicKey, path, "", 5*time.Second, DialOpts{})
	if err != nil {
		t.Fatalf("DSA public-key login should succeed: %v", err)
	}
	sess.Close()
}

type errorString string

func (e errorString) Error() string { return string(e) }

// The three ways an SSH login can be refused each name a different field to
// fix, and the raw library text ("attempted methods [none]") names none.
func TestDialSSHAuthFailureHints(t *testing.T) {
	k := testDSAKey(t)
	keyPath := writeDSAFile(t, dsaPEM(t, k))
	refuse := func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, errorString("no") }
	refusePW := func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, errorString("no") }

	cases := []struct {
		name string
		cfg  *ssh.ServerConfig
		auth model.AuthMethod
		want string
	}{
		{"鍵のみのサーバにパスワードで", &ssh.ServerConfig{PublicKeyCallback: refuse}, model.AuthPassword,
			"パスワード認証を受け付けていません"},
		{"パスワードが違う", &ssh.ServerConfig{PasswordCallback: refusePW}, model.AuthPassword,
			"ユーザー名またはパスワードが違います"},
		{"鍵が未登録", &ssh.ServerConfig{
			PublicKeyAuthAlgorithms: append(ssh.SupportedAlgorithms().PublicKeyAuths, ssh.InsecureAlgorithms().PublicKeyAuths...),
			PublicKeyCallback:       refuse}, model.AuthPublicKey,
			"ユーザー「koji」に登録されているか"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			host, port := splitAddr(t, fakeSSHServer(t, c.cfg))
			_, err := dialSSH(host, port, "koji", "pw", c.auth, keyPath, "", 5*time.Second, DialOpts{})
			if err == nil {
				t.Fatal("login should fail")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error should hint %q, got: %v", c.want, err)
			}
		})
	}
}
