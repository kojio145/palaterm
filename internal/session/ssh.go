package session

import (
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// sshSession wraps an SSH channel's stdin/stdout as one ReadWriteCloser.
type sshSession struct {
	client *ssh.Client
	sess   *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
}

func (s *sshSession) Read(p []byte) (int, error)  { return s.stdout.Read(p) }
func (s *sshSession) Write(p []byte) (int, error) { return s.stdin.Write(p) }

// Resize changes the remote PTY size so output reflows to the new width.
func (s *sshSession) Resize(cols, rows int) error {
	if s.sess == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	return s.sess.WindowChange(rows, cols)
}

func (s *sshSession) Close() error {
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.sess != nil {
		_ = s.sess.Close()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// HostKeyStore persists first-seen SSH host key fingerprints (TOFU:
// trust-on-first-use, like OpenSSH's accept-new). Implementations must be
// safe for concurrent use — parallel batch runs dial many hosts at once.
type HostKeyStore interface {
	// GetHostKey returns the stored fingerprint for addr ("host:port"), or "".
	GetHostKey(addr string) string
	// SetHostKey remembers addr's fingerprint (first connection).
	SetHostKey(addr, fingerprint string)
}

// VerifyHostKey returns a HostKeyCallback that pins the first-seen key and
// hard-fails when a later connection presents a different one (MITM or a
// replaced device). A nil store accepts any key (tests only).
func VerifyHostKey(store HostKeyStore) ssh.HostKeyCallback {
	if store == nil {
		return ssh.InsecureIgnoreHostKey()
	}
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		fp := key.Type() + " " + ssh.FingerprintSHA256(key)
		prev := store.GetHostKey(hostname)
		if prev == "" {
			store.SetHostKey(hostname, fp)
			return nil
		}
		if prev != fp {
			return fmt.Errorf("ホストキーが前回接続時と異なります（%s、今回: %s / 記録: %s）。中間者攻撃または機器交換の可能性があります。機器を交換した場合は機器の編集画面で「ホストキー記録を削除」してから接続し直してください", hostname, fp, prev)
		}
		return nil
	}
}

// DialOpts carries the security-relevant connection options.
type DialOpts struct {
	// HostKey verifies the server host key; nil accepts any key (tests only).
	HostKey ssh.HostKeyCallback
	// Legacy additionally offers old KEX/cipher algorithms (SHA-1 DH groups,
	// CBC, 3DES) for network gear that supports nothing newer. Off by
	// default, mirroring modern clients that disabled SHA-1 by default.
	Legacy bool
}

// dialSSHClient connects and completes the SSH handshake, with the connect
// timeout covering both.
//
// ssh.Dial would apply cfg.Timeout to the TCP connect alone and then let the
// handshake run unbounded. Pointing SSH at a Telnet port is enough to expose
// that: the TCP connect succeeds instantly, the far end never speaks SSH, and
// the dial sat there for two minutes — until the *device* gave up — no matter
// what connect timeout the user had set. In a batch that is one worker stuck
// on a typo.
//
// The deadline is cleared once the handshake is through: from there the
// session is long-lived and must not expire mid-command.
func dialSSHClient(addr string, cfg *ssh.ClientConfig, timeout time.Duration) (*ssh.Client, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		c.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func dialSSH(host string, port int, user, password string, auth model.AuthMethod, keyFile, keyPassphrase string, timeout time.Duration, opts DialOpts) (Session, error) {
	var authMethods []ssh.AuthMethod

	if auth == model.AuthPublicKey {
		signer, err := loadPrivateKey(keyFile, keyPassphrase)
		if err != nil {
			return nil, err
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	} else {
		authMethods = append(authMethods, ssh.Password(password))
		// Some devices use keyboard-interactive for password auth.
		authMethods = append(authMethods, ssh.KeyboardInteractive(
			func(_, _ string, questions []string, _ []bool) ([]string, error) {
				ans := make([]string, len(questions))
				for i := range questions {
					ans[i] = password
				}
				return ans, nil
			}))
	}

	// Strong algorithms only by default; the per-device レガシー暗号 toggle
	// additionally offers the old ones some network gear still requires.
	kex := []string{
		"curve25519-sha256", "curve25519-sha256@libssh.org",
		"ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
		"diffie-hellman-group14-sha256", "diffie-hellman-group-exchange-sha256",
	}
	ciphers := []string{
		"aes128-gcm@openssh.com", "aes256-gcm@openssh.com",
		"aes128-ctr", "aes192-ctr", "aes256-ctr",
	}
	if opts.Legacy {
		kex = append(kex, "diffie-hellman-group14-sha1", "diffie-hellman-group1-sha1")
		ciphers = append(ciphers, "aes128-cbc", "3des-cbc")
	}
	hostKey := opts.HostKey
	if hostKey == nil {
		hostKey = ssh.InsecureIgnoreHostKey() // tests / fakes only
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKey,
		Timeout:         timeout,
		Config: ssh.Config{
			KeyExchanges: kex,
			Ciphers:      ciphers,
		},
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	client, err := dialSSHClient(addr, cfg, timeout)
	if err != nil {
		if !opts.Legacy && strings.Contains(err.Error(), "no common algorithm") {
			return nil, fmt.Errorf("ssh dial %s: %w — 機器が新しい暗号方式に対応していない可能性があります。機器の編集画面で「レガシー暗号を許可」を有効にしてください", addr, err)
		}
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}

	modes := ssh.TerminalModes{ssh.ECHO: 0, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm", 40, 120, modes); err != nil {
		sess.Close()
		client.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		client.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		client.Close()
		return nil, err
	}
	sess.Stderr = io.Discard
	if err := sess.Shell(); err != nil {
		sess.Close()
		client.Close()
		return nil, err
	}

	return &sshSession{client: client, sess: sess, stdin: stdin, stdout: stdout}, nil
}

func loadPrivateKey(keyFile, passphrase string) (ssh.Signer, error) {
	if keyFile == "" {
		return nil, fmt.Errorf("public-key auth selected but no key file given")
	}
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	// A PuTTY .ppk is not an OpenSSH key, and on Windows plenty of people have
	// one because PuTTY and Tera Term are what they already use. Left to the
	// parser it comes back as "ssh: no key found", which says nothing about
	// the format or what to do next.
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("PuTTY-User-Key-File")) {
		return nil, fmt.Errorf("この鍵はPuTTY形式（.ppk）です。PuTTYgenで開き「Conversions → Export OpenSSH key」で変換したファイルを指定してください: %s", keyFile)
	}
	// Supports Ed25519, RSA, ECDSA (PEM/OpenSSH keys, passphrase-protected or
	// not). DSA is not supported by design.
	if passphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		if err != nil {
			if errors.Is(err, x509.IncorrectPasswordError) || strings.Contains(err.Error(), "incorrect passphrase") || strings.Contains(err.Error(), "decryption password incorrect") {
				return nil, fmt.Errorf("秘密鍵のパスフレーズが違います: %s", keyFile)
			}
			return nil, fmt.Errorf("parse private key (Ed25519/RSA/ECDSA expected): %w", err)
		}
		return signer, nil
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, fmt.Errorf("この秘密鍵はパスフレーズで保護されています。編集画面の「秘密鍵のパスフレーズ」を入力してください: %s", keyFile)
		}
		return nil, fmt.Errorf("parse private key (Ed25519/RSA/ECDSA expected): %w", err)
	}
	return signer, nil
}
