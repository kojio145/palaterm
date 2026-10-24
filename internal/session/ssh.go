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
	// hops are the jump hosts a tunneled session runs through (outermost
	// first), closed after the device's own connection.
	hops []*ssh.Client
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

// LineEnding is a bare CR, as a terminal's Enter key sends. The pty on the
// far side turns it into one newline; CR LF would be two Enters there — an
// empty command and a second prompt after every line, visible as doubled
// prompts on a Unix-style CLI, and through a jump host that is what every
// device behind it receives.
func (s *sshSession) LineEnding() string { return "\r" }

func (s *sshSession) Close() error {
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.sess != nil {
		_ = s.sess.Close()
	}
	var err error
	if s.client != nil {
		err = s.client.Close()
	}
	closeHops(s.hops)
	if err != nil {
		return err
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
			return &HostKeyMismatchError{Name: hostname, Got: fp, Stored: prev}
		}
		return nil
	}
}

// HostKeyMismatchError is VerifyHostKey's refusal: the endpoint pinned as
// Name (the device's own address, or a jump host's — see model.HostKeyNames)
// presented Got where Stored was recorded.
type HostKeyMismatchError struct {
	Name, Got, Stored string
}

// mismatchPrefix opens the message; MismatchedHostKeyIn reads the name back
// from it, and the UI recognises the message by it.
const mismatchPrefix = "ホストキーが前回接続時と異なります（"

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("%s%s、今回: %s / 記録: %s）。中間者攻撃または機器交換の可能性があります。機器を交換した場合は、接続時の確認で「機器を交換したので許可して再接続」を選んでください", mismatchPrefix, e.Name, e.Got, e.Stored)
}

// MismatchedHostKey names the endpoint whose host key err refused, or "" when
// err is not (or does not wrap) that refusal. An error that was flattened to
// text on the way — a batch result's message — still yields the name.
func MismatchedHostKey(err error) string {
	if err == nil {
		return ""
	}
	var m *HostKeyMismatchError
	if errors.As(err, &m) {
		return m.Name
	}
	return MismatchedHostKeyIn(err.Error())
}

// MismatchedHostKeyIn is MismatchedHostKey for the error's text.
func MismatchedHostKeyIn(msg string) string {
	_, rest, ok := strings.Cut(msg, mismatchPrefix)
	if !ok {
		return ""
	}
	name, _, ok := strings.Cut(rest, "、今回: ")
	if !ok {
		return ""
	}
	return name
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
// The deadline stays on through opening the session, the pty and the shell
// (a server that completes the handshake and then never answers a channel
// request held the worker forever); the caller clears it once the shell is
// up — from there the session is long-lived and must not expire mid-command.
func dialSSHClient(addr string, cfg *ssh.ClientConfig, timeout time.Duration) (*ssh.Client, net.Conn, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return ssh.NewClient(c, chans, reqs), conn, nil
}

// keepaliveEvery is how often an idle SSH session is poked. Firewalls and
// NAT routers drop a TCP flow that is silent for a few minutes; a terminal
// window left open while reading something else came back dead.
var keepaliveEvery = 30 * time.Second

// startKeepalive sends OpenSSH's keepalive request until the client closes,
// and closes it after three unanswered in a row (the line is gone — better
// to say so than to sit there). The returned channel closes when the sender
// has stopped (tests).
//
// The connection's own end (c.Wait) is what stops the sender: a client we
// closed answers SendRequest with net.ErrClosed, not io.EOF, and judging by
// the error alone kept the goroutine and its ticker alive for three more
// misses — 90 s per device after every batch.
func startKeepalive(c *ssh.Client) <-chan struct{} {
	closed := make(chan struct{})
	go func() {
		_ = c.Wait()
		close(closed)
	}()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(keepaliveEvery)
		defer t.Stop()
		misses := 0
		for {
			select {
			case <-closed:
				return
			case <-t.C:
			}
			done := make(chan error, 1)
			go func() {
				_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
				done <- err
			}()
			select {
			case <-closed:
				return
			case err := <-done:
				if err != nil && (errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)) {
					return // closed
				}
				if err != nil {
					misses++
				} else {
					misses = 0
				}
			case <-time.After(keepaliveEvery):
				misses++
			}
			if misses >= 3 {
				c.Close()
				return
			}
		}
	}()
	return stopped
}

func dialSSH(host string, port int, user, password string, auth model.AuthMethod, keyFile, keyPassphrase string, timeout time.Duration, opts DialOpts) (Session, error) {
	cfg, err := sshConfig(user, password, auth, keyFile, keyPassphrase, timeout, opts)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	client, conn, err := dialSSHClient(addr, cfg, timeout)
	if err != nil {
		return nil, sshDialError(addr, err, user, auth, opts)
	}
	s, err := startShell(client)
	if err != nil {
		client.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		s.Close()
		return nil, err
	}
	startKeepalive(client)
	return s, nil
}

// sshConfig builds the client config for one SSH endpoint: its credentials,
// the algorithms it is offered and the host-key check.
func sshConfig(user, password string, auth model.AuthMethod, keyFile, keyPassphrase string, timeout time.Duration, opts DialOpts) (*ssh.ClientConfig, error) {
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
	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKey,
		Timeout:         timeout,
		Config: ssh.Config{
			KeyExchanges: kex,
			Ciphers:      ciphers,
		},
	}, nil
}

// sshDialError adds what the user can do about a failed handshake: allow
// the old algorithms, or fix the credentials.
func sshDialError(addr string, err error, user string, auth model.AuthMethod, opts DialOpts) error {
	if hint := algorithmHint(err, opts); hint != "" {
		return fmt.Errorf("ssh dial %s: %w — %s", addr, err, hint)
	}
	if hint := authFailureHint(err, user, auth); hint != "" {
		return fmt.Errorf("ssh dial %s: %w — %s", addr, err, hint)
	}
	return fmt.Errorf("ssh dial %s: %w", addr, err)
}

// algorithmHint says what to do about x/crypto's "no common algorithm for
// <what>; ...". The レガシー暗号 toggle adds old key-exchange and cipher
// algorithms and nothing else, so it is offered for those two alone: a device
// that insists on an old MAC or host-key algorithm would fail the same way
// with it on, and the hint would send the user round in a circle.
func algorithmHint(err error, opts DialOpts) string {
	msg := err.Error()
	i := strings.Index(msg, "no common algorithm for ")
	if i < 0 {
		return ""
	}
	what, _, _ := strings.Cut(msg[i+len("no common algorithm for "):], ";")
	if strings.Contains(what, "key exchange") || strings.Contains(what, "cipher") {
		if opts.Legacy {
			return ""
		}
		return "機器が新しい暗号方式に対応していない可能性があります。機器の編集画面で「レガシー暗号を許可」を有効にしてください"
	}
	return "機器と共通のアルゴリズムがありません（「レガシー暗号を許可」が足すのは鍵交換と暗号だけで、この不一致には効きません）"
}

// startShell opens the interactive shell on an authenticated client. The
// caller still owns client when it fails.
func startShell(client *ssh.Client) (*sshSession, error) {
	sess, err := client.NewSession()
	if err != nil {
		return nil, err
	}

	// ECHO stays on. Network gear ignores these modes, but a Unix-style far
	// end honours them, and an OpenSSH client started on such a host — the
	// jump-host case — copies its own tty's modes into the pty it requests
	// from the next hop. With ECHO off here, nothing the user typed showed
	// on an ALAXALA reached through a jump server (its CLI is a real shell).
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm", 40, 120, modes); err != nil {
		sess.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	sess.Stderr = io.Discard
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, err
	}
	return &sshSession{client: client, sess: sess, stdin: stdin, stdout: stdout}, nil
}

// authFailureHint turns x/crypto's "unable to authenticate, attempted methods
// [...]" into something the user can act on. The attempted list is the tell:
//
//   - only "none" tried: the server never offered password at all (a
//     public-key-only jump server, say) — pick 公開鍵 and point at the key;
//   - password / keyboard-interactive tried and refused: wrong credentials;
//   - publickey tried and refused: the key is not registered for that user.
//
// Without this, a password-only setup against a key-only host reads as a
// generic handshake failure and nothing says which field to change.
func authFailureHint(err error, user string, auth model.AuthMethod) string {
	msg := err.Error()
	// Some servers answer a refused keyboard-interactive (or public-key)
	// attempt with a bare failure where x/crypto expects the next step, and
	// the handshake ends as "ssh: unexpected message type 51 (expected 60)"
	// — 51 being SSH_MSG_USERAUTH_FAILURE: the login was refused.
	if strings.Contains(msg, "unexpected message type 51") {
		return "ユーザー名またはパスワードが違います（またはサーバが受け付ける認証方式と合いません）"
	}
	if !strings.Contains(msg, "unable to authenticate") {
		return ""
	}
	const marker = "attempted methods ["
	i := strings.Index(msg, marker)
	if i < 0 {
		return ""
	}
	rest := msg[i+len(marker):]
	j := strings.Index(rest, "]")
	if j < 0 {
		return ""
	}
	var triedPassword, triedKey bool
	for _, m := range strings.Fields(rest[:j]) {
		switch m {
		case "password", "keyboard-interactive":
			triedPassword = true
		case "publickey":
			triedKey = true
		}
	}
	switch {
	case triedKey:
		return fmt.Sprintf("公開鍵が受け付けられませんでした。この秘密鍵に対応する公開鍵がサーバのユーザー「%s」に登録されているか、ユーザー名が合っているかを確認してください", user)
	case triedPassword:
		return "ユーザー名またはパスワードが違います"
	case auth != model.AuthPublicKey:
		return "サーバがパスワード認証を受け付けていません（公開鍵認証のみ）。編集画面の SSH認証を「公開鍵」にして、秘密鍵ファイル（Tera Term などで使っていた鍵）を指定してください"
	}
	return ""
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
	// not) and the classic PEM "DSA PRIVATE KEY" that older tooling (Tera
	// Term, PuTTYgen exports, ssh-keygen before 9.x) wrote as id_dsa. DSA is
	// weak and modern servers refuse ssh-dss, but the jump servers people
	// still carry such keys for are exactly the ones that only accept it;
	// the library signs ssh-dss when the server offers nothing else.
	if passphrase != "" {
		signer, err := parseKey(data, passphrase)
		if err != nil && isUnencryptedKeyError(err) {
			// The passphrase is left over from the protected key this file
			// replaced (the device's field kept it): the file needs none.
			signer, err = parseKey(data, "")
		}
		if err != nil {
			if errors.Is(err, x509.IncorrectPasswordError) || strings.Contains(err.Error(), "incorrect passphrase") || strings.Contains(err.Error(), "decryption password incorrect") {
				return nil, fmt.Errorf("秘密鍵のパスフレーズが違います: %s", keyFile)
			}
			return nil, fmt.Errorf("parse private key (Ed25519/RSA/ECDSA/DSA expected): %w", err)
		}
		return signer, nil
	}
	signer, err := parseKey(data, "")
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, fmt.Errorf("この秘密鍵はパスフレーズで保護されています。編集画面の「秘密鍵のパスフレーズ」を入力してください: %s", keyFile)
		}
		return nil, fmt.Errorf("parse private key (Ed25519/RSA/ECDSA/DSA expected): %w", err)
	}
	return signer, nil
}

// isUnencryptedKeyError recognises x/crypto's two refusals of a passphrase
// for a key that has none: PEM ("not an encrypted key") and OpenSSH
// container ("key is not password protected").
func isUnencryptedKeyError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not an encrypted key") || strings.Contains(msg, "not password protected")
}

// parseKey hands the file to x/crypto and, for the one shape it declines —
// a DSA key in OpenSSH's own container format — to parseOpenSSHDSA.
func parseKey(data []byte, passphrase string) (ssh.Signer, error) {
	var signer ssh.Signer
	var err error
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(data)
	}
	if isOpenSSHDSAUnhandled(err) {
		return parseOpenSSHDSA(data, passphrase)
	}
	return signer, err
}
