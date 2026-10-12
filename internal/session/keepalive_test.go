package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"golang.org/x/crypto/ssh"
)

// An idle SSH session keeps sending keepalive requests (so a firewall does
// not drop the flow), and the deadline used while connecting is gone once the
// shell is open: the session outlives the connect timeout.
func TestSSHKeepaliveAndNoDeadlineAfterShell(t *testing.T) {
	old := keepaliveEvery
	keepaliveEvery = 100 * time.Millisecond
	defer func() { keepaliveEvery = old }()

	var keepalives atomic.Int32
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
		go func() {
			for r := range reqs {
				if r.Type == "keepalive@openssh.com" {
					keepalives.Add(1)
				}
				if r.WantReply {
					r.Reply(false, nil) // what network gear answers to an unknown request
				}
			}
		}()
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
	host, port := splitAddr(t, ln.Addr().String())
	sess, err := dialSSH(host, port, "u", "p", model.AuthPassword, "", "", 300*time.Millisecond, DialOpts{})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	time.Sleep(700 * time.Millisecond) // past the 300 ms connect timeout
	if n := keepalives.Load(); n < 3 {
		t.Errorf("keepalives sent = %d, want several", n)
	}
	if _, err := sess.Write([]byte("show clock\r")); err != nil {
		t.Errorf("session died after the connect timeout: %v", err)
	}
}
