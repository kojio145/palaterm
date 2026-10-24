package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// A negotiation that arrives split across two reads — a banner longer than
// the read buffer pushing its trailing IAC WILL ECHO over the boundary, or a
// tunneled channel handing the bytes over in pieces — is still answered, and
// none of it reaches the application data. The three shapes that can be cut:
// a lone IAC, IAC WILL without its option, and a subnegotiation before its
// IAC SE; plus an escaped IAC IAC, which stays one literal 0xFF.
func TestTelnetNegotiationSplitAcrossReads(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	replies := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		chunks := [][]byte{
			{'a', 'b', 'c', iac},
			{will, optEcho, 'd', iac, will},
			{optSuppressGoAhead, 'e', iac, sb, 24, 1},
			{iac, se, 'f', iac},
			{iac, 'g'},
		}
		for _, c := range chunks {
			conn.Write(c)
			time.Sleep(80 * time.Millisecond)
		}
		replies <- drainFor(conn, 300*time.Millisecond)
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	s := &telnetSession{conn: conn}
	defer s.Close()
	var got []byte
	buf := make([]byte, 64)
	deadline := time.Now().Add(3 * time.Second)
	for len(got) < 8 && time.Now().Before(deadline) {
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := s.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			break
		}
	}
	if string(got) != "abcdef\xffg" {
		t.Errorf("data = %q, want %q", got, "abcdef\xffg")
	}
	r := string(<-replies)
	if !strings.Contains(r, string([]byte{iac, do, optEcho})) || !strings.Contains(r, string([]byte{iac, do, optSuppressGoAhead})) {
		t.Errorf("split WILLs not answered; replies = % x", r)
	}
}

// The keepalive sender stops as soon as the client is closed from this side
// (net.ErrClosed, not io.EOF): it used to live on for three more misses with
// its ticker — 90 s per device after every batch.
func TestKeepaliveStopsWhenClientClosed(t *testing.T) {
	old := keepaliveEvery
	keepaliveEvery = 50 * time.Millisecond
	defer func() { keepaliveEvery = old }()

	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(hostPriv)
	cfg := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			return
		}
		defer sc.Close()
		go ssh.DiscardRequests(reqs)
		for nc := range chans {
			nc.Reject(ssh.Prohibited, "")
		}
	}()
	ccfg := &ssh.ClientConfig{User: "u", Auth: []ssh.AuthMethod{ssh.Password("p")}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second}
	client, _, err := dialSSHClient(ln.Addr().String(), ccfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	stopped := startKeepalive(client)
	time.Sleep(120 * time.Millisecond) // a couple of keepalives go out
	client.Close()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("keepalive goroutine still running after Close")
	}
}

// The レガシー暗号 hint is offered only where the toggle can help: a key
// exchange or cipher the device insists on. An old MAC or host-key
// algorithm fails the same way with it on, so that gets a different word.
func TestAlgorithmHint(t *testing.T) {
	mk := func(what string) error {
		return fmt.Errorf("ssh: handshake failed: ssh: no common algorithm for %s; we offered: [\"a\"], peer offered: [\"b\"]", what)
	}
	legacy := "レガシー暗号を許可」を有効"
	other := "この不一致には効きません"
	cases := []struct {
		what   string
		legacy bool
		want   string // substring; "" = no hint
	}{
		{"key exchange", false, legacy},
		{"client to server cipher", false, legacy},
		{"server to client cipher", false, legacy},
		{"key exchange", true, ""},
		{"client to server MAC", false, other},
		{"host key", false, other},
	}
	for _, c := range cases {
		got := algorithmHint(mk(c.what), DialOpts{Legacy: c.legacy})
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s (legacy=%v): hint %q, want %q", c.what, c.legacy, got, c.want)
		}
	}
	if algorithmHint(errors.New("ssh: unable to authenticate"), DialOpts{}) != "" {
		t.Error("hint on an unrelated error")
	}
}

// The host-key refusal says which endpoint it was about, as a typed error
// and in the text alone (a batch result carries only the text).
func TestHostKeyMismatchNamesEndpoint(t *testing.T) {
	store := &mapStore{m: map[string]string{}}
	cb := VerifyHostKey(store)
	name := "192.0.2.1:22>192.0.2.10:22"
	if err := cb(name, nil, genKey(t)); err != nil {
		t.Fatal(err)
	}
	err := cb(name, nil, genKey(t))
	var m *HostKeyMismatchError
	if !errors.As(err, &m) || m.Name != name {
		t.Fatalf("err = %#v", err)
	}
	wrapped := fmt.Errorf("ssh dial x: %w", err)
	if got := MismatchedHostKey(wrapped); got != name {
		t.Errorf("MismatchedHostKey(wrapped) = %q", got)
	}
	if got := MismatchedHostKeyIn("機器 sw1: " + err.Error()); got != name {
		t.Errorf("MismatchedHostKeyIn(text) = %q", got)
	}
	if got := MismatchedHostKey(errors.New("timeout")); got != "" {
		t.Errorf("other error named %q", got)
	}
	if got := MismatchedHostKey(nil); got != "" {
		t.Errorf("nil named %q", got)
	}
}

// A device that cannot be reached by tunnel (a console cable) is refused
// before any hop is dialed: nothing connects to the hop, so no stray login
// shows up in its log.
func TestDialTunnelRefusesUnsupportedConnBeforeDialing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- struct{}{}
			c.Close()
		}
	}()
	host, port := splitAddr(t, ln.Addr().String())
	hops := []model.Bastion{{Host: host, Port: port, Method: model.BastionSSH, Username: "ops", Password: "pw"}}
	dev := &model.Device{Name: "con1", Conn: model.ConnSerial}
	_, err = DialTunnel(hops, dev, time.Second, DialOpts{})
	if err == nil || !strings.Contains(err.Error(), "SSH トンネルで接続できるのは") {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-accepted:
		t.Error("the hop was dialed for a device the tunnel cannot reach")
	case <-time.After(200 * time.Millisecond):
	}
}
