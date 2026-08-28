package runner

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// fakeCisco is an in-process SSH server that behaves enough like a Cisco IOS
// device (enable prompt, pager command, one show command) to exercise the full
// runner pipeline: connect -> login/enable -> command -> log.
var recvMu sync.Mutex
var recvCmds []string

func recordCmd(c string) {
	recvMu.Lock()
	recvCmds = append(recvCmds, c)
	recvMu.Unlock()
}

func fakeCisco(t *testing.T) (addr string, stop func()) {
	t.Helper()
	recvMu.Lock()
	recvCmds = nil
	recvMu.Unlock()

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
			go serveCisco(conn, cfg)
		}
	}()

	return ln.Addr().String(), func() { ln.Close() }
}

func serveCisco(nConn net.Conn, cfg *ssh.ServerConfig) {
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
				// Accept pty-req/shell so the client's Shell() succeeds.
				req.Reply(req.Type == "shell" || req.Type == "pty-req", nil)
			}
		}(chReqs)
		go ciscoShell(ch)
	}
}

// ciscoShell emulates the device-side CLI.
func ciscoShell(ch ssh.Channel) {
	defer ch.Close()
	io.WriteString(ch, "\r\nrouter1>")

	reader := make([]byte, 1024)
	var line strings.Builder
	enabled := false
	awaitEnablePw := false
	for {
		n, err := ch.Read(reader)
		if n > 0 {
			for _, b := range reader[:n] {
				switch b {
				case '\r':
					// Ignore CR; act only on LF, like a real device line.
				case '\n':
					cmd := strings.TrimSpace(line.String())
					recordCmd(cmd)
					line.Reset()
					io.WriteString(ch, ciscoRespond(cmd, &enabled, &awaitEnablePw))
				default:
					line.WriteByte(b)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func ciscoRespond(cmd string, enabled, awaitEnablePw *bool) string {
	// Real IOS challenges for an enable password after "enable".
	if *awaitEnablePw {
		*awaitEnablePw = false
		*enabled = true
		return "\r\n" + promptFor(*enabled)
	}
	switch cmd {
	case "enable":
		*awaitEnablePw = true
		return "\r\nPassword: "
	case "show version":
		return "\r\nCisco IOS Software, Version 15.7\r\nrouter1 uptime is 1 day\r\n" + promptFor(*enabled)
	default: // terminal length 0, exit, blank, etc.
		return "\r\n" + promptFor(*enabled)
	}
}

func promptFor(enabled bool) string {
	if enabled {
		return "router1#"
	}
	return "router1>"
}

func TestRunDeviceEndToEnd(t *testing.T) {
	addr, stop := fakeCisco(t)
	defer stop()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	t.Logf("server at %s:%d", host, port)
	dev := &model.Device{
		Name: "router1", Host: host, Conn: model.ConnSSH, Port: port,
		OSType: "cisco-ios", Username: "admin", Password: "x", EnablePassword: "en",
		CommandSet: "backup", Enabled: true,
	}
	set := &model.CommandSet{Name: "backup", Commands: []model.Command{{Text: "show version"}}}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.CommandTimeout = 5
	settings.ConnectTimeout = 5

	r := New(testRegistry())
	res := r.RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(), nil)

	if !res.Success {
		t.Fatalf("run failed: %s\n---transcript---\n%s", res.Error, res.Transcript)
	}
	if !strings.Contains(res.Transcript, "Cisco IOS Software, Version 15.7") {
		recvMu.Lock()
		cmds := strings.Join(recvCmds, " | ")
		recvMu.Unlock()
		t.Fatalf("show version output missing. err=%q\ncmds received by device: [%s]\n---transcript---\n%s", res.Error, cmds, res.Transcript)
	}
	if res.LogPath == "" {
		t.Fatal("expected a log file path")
	}
}
