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

// fakeBastion is an SSH server that emulates a jump host: it presents a shell
// prompt and, when it receives `telnet <host>`, dials that TCP address and
// bridges the two streams (so the device behind it becomes reachable).
func fakeBastion(t *testing.T) (addr string, stop func()) {
	t.Helper()
	signer := testSigner(t)
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
			go serveBastion(conn, cfg)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func testSigner(t *testing.T) ssh.Signer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func serveBastion(nConn net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(nConn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "")
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
		go bastionShell(ch)
	}
}

func bastionShell(ch ssh.Channel) {
	defer ch.Close()
	io.WriteString(ch, "\r\njump-host$ ")
	buf := make([]byte, 1024)
	var line strings.Builder
	for {
		n, err := ch.Read(buf)
		for i := 0; i < n; i++ {
			b := buf[i]
			if b == '\n' {
				cmd := strings.TrimSpace(line.String())
				line.Reset()
				if strings.HasPrefix(cmd, "telnet ") {
					target := strings.TrimSpace(strings.TrimPrefix(cmd, "telnet "))
					bridgeTelnet(ch, target)
					io.WriteString(ch, "\r\njump-host$ ")
				} else {
					io.WriteString(ch, "\r\njump-host$ ")
				}
			} else if b != '\r' {
				line.WriteByte(b)
			}
		}
		if err != nil {
			return
		}
	}
}

// bridgeTelnet connects to target and pipes bytes both ways until either side
// closes, so the device behind the jump host is reachable through this shell.
func bridgeTelnet(ch ssh.Channel, target string) {
	conn, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		io.WriteString(ch, "\r\ntelnet: unable to connect\r\n")
		return
	}
	defer conn.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(conn, ch); done <- struct{}{} }()
	go func() { io.Copy(ch, conn); done <- struct{}{} }()
	<-done
}

// fakeTelnetDevice is a minimal Telnet "device" that asks for login/password,
// then answers a couple of commands at a "#" prompt.
func fakeTelnetDevice(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go telnetDeviceConn(c)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func telnetDeviceConn(c net.Conn) {
	defer c.Close()
	io.WriteString(c, "Username: ")
	buf := make([]byte, 512)
	var line strings.Builder
	stage := 0 // 0=await user, 1=await pass, 2=logged in
	for {
		n, err := c.Read(buf)
		for i := 0; i < n; i++ {
			b := buf[i]
			if b == '\n' {
				line.Reset()
				switch stage {
				case 0:
					stage = 1
					io.WriteString(c, "\r\nPassword: ")
				case 1:
					stage = 2
					io.WriteString(c, "\r\ndev1#")
				default:
					io.WriteString(c, "\r\nok\r\ndev1#")
				}
			} else if b != '\r' {
				line.WriteByte(b)
			}
		}
		if err != nil {
			return
		}
	}
}

// TestBastionChainTwoHops runs a device reached through one SSH jump host.
func TestBastionChainOneHop(t *testing.T) {
	bAddr, bStop := fakeBastion(t)
	defer bStop()
	dAddr, dStop := fakeTelnetDevice(t)
	defer dStop()

	bHost, bPortS, _ := net.SplitHostPort(bAddr)
	var bPort int
	fmt.Sscanf(bPortS, "%d", &bPort)

	dev := &model.Device{
		Name: "dev1", Host: dAddr, Conn: model.ConnTelnet, // reached via bastion → telnet to dAddr
		OSType:   "generic",
		Username: "admin", Password: "pw",
		CommandSet: "chk", Enabled: true,
		Bastions: []model.Bastion{
			{Method: model.BastionSSH, Host: bHost, Port: bPort, Username: "j", Password: "jp"},
		},
	}
	// dev.Host must be the full host:port telnet target for the bastion's
	// `telnet <host>` bridge; encode it as host and let EffectivePort be unused
	// by passing the address through Host (the bastion bridges to it verbatim).
	dev.Host = dAddr

	set := &model.CommandSet{Name: "chk", Commands: []model.Command{{Text: "show version"}}}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.ConnectTimeout = 5
	settings.CommandTimeout = 6

	r := New(testRegistry())
	res := r.RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(), nil)

	if !res.Success {
		t.Fatalf("bastion run failed: %s\n---\n%s", res.Error, res.Transcript)
	}
	if !strings.Contains(res.Transcript, "dev1#") {
		t.Fatalf("did not reach device prompt through bastion:\n%s", res.Transcript)
	}
}
