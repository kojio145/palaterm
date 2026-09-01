package runner

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// fakeIX is an in-process SSH server standing in for a NEC IX2105 whose
// account logs in already privileged: it lands on "IX-A#" and never shows the
// ">" that the profile's escalation step waits for.
func fakeIX(t *testing.T) (addr string, stop func()) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveIX(conn, cfg)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func serveIX(nConn net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(nConn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			return
		}
		go func(in <-chan *ssh.Request) {
			for req := range in {
				req.Reply(req.Type == "shell" || req.Type == "pty-req", nil)
			}
		}(chReqs)
		go ixShell(ch)
	}
}

func ixShell(ch ssh.Channel) {
	defer ch.Close()
	// Straight to the privileged prompt — no ">" stage at all.
	io.WriteString(ch, "\r\nNEC Portable Internetwork Core Operating System Software\r\nIX-A#")

	buf := make([]byte, 1024)
	var line strings.Builder
	for {
		n, err := ch.Read(buf)
		for _, b := range buf[:n] {
			switch b {
			case '\r':
			case '\n':
				cmd := strings.TrimSpace(line.String())
				line.Reset()
				if cmd == "show version" {
					io.WriteString(ch, "\r\nIX Series IX2105 (magellan-sec) Software, Version 10.2.16\r\nIX-A#")
				} else {
					io.WriteString(ch, "\r\nIX-A#")
				}
			default:
				line.WriteByte(b)
			}
		}
		if err != nil {
			return
		}
	}
}

// TestLoginOnAlreadyPrivilegedDevice covers a device that logs in privileged.
// The nec-ix profile (like cisco-ios and others) carries an escalation step
// that waits for ">" before sending "enable". That step sends a literal
// command rather than the {enable} password, so it is not an auth step and
// used to fail the whole login with `waiting for ">": expect timeout` once the
// command timeout elapsed. The runner must instead notice the operational
// prompt is already up, skip the step, and log in normally.
func TestLoginOnAlreadyPrivilegedDevice(t *testing.T) {
	addr, stop := fakeIX(t)
	defer stop()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	dev := &model.Device{
		Name: "IX-A", Host: host, Conn: model.ConnSSH, Port: port,
		OSType: "nec-ix", Username: "palaterm", Password: "x",
		CommandSet: "show", Enabled: true,
	}
	set := &model.CommandSet{Name: "show", Commands: []model.Command{{Text: "show version"}}}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.CommandTimeout = 20
	settings.ConnectTimeout = 5

	start := time.Now()
	r := New(testRegistry())
	res := r.RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(), nil)
	elapsed := time.Since(start)

	if !res.Success {
		t.Fatalf("login failed on already-privileged device: %s\n---transcript---\n%s", res.Error, res.Transcript)
	}
	if !strings.Contains(res.Transcript, "Version 10.2.16") {
		t.Fatalf("show version output missing\n---transcript---\n%s", res.Transcript)
	}
	// The skip must be cheap: burning the 20s command timeout on the ">" that
	// never comes is the very regression under test.
	if elapsed > 10*time.Second {
		t.Fatalf("login took %s — the escalation step is not being skipped early", elapsed)
	}
}
