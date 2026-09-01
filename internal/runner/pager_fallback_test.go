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

// fakePagingDevice answers one command with output split by a "--More--"
// pager that only advances when a bare space arrives — the behaviour of a
// device whose pager-disable command did not take effect.
func fakePagingDevice(t *testing.T) (addr string, stop func()) {
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
			go func(c net.Conn) {
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
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
					go pagingShell(ch)
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func pagingShell(ch ssh.Channel) {
	defer ch.Close()
	io.WriteString(ch, "\r\nR1#")

	buf := make([]byte, 1024)
	var line strings.Builder
	paused := false
	for {
		n, err := ch.Read(buf)
		for _, b := range buf[:n] {
			switch {
			case paused && b == ' ':
				// The pager advances only on a bare space, never on a newline.
				paused = false
				io.WriteString(ch, "\r\nPAGE-TWO-CONTENT\r\nR1#")
			case b == '\r':
			case b == '\n':
				cmd := strings.TrimSpace(line.String())
				line.Reset()
				if cmd == "show interfaces" {
					paused = true
					io.WriteString(ch, "\r\nPAGE-ONE-CONTENT\r\n--More--        ")
				} else {
					io.WriteString(ch, "\r\nR1#")
				}
			default:
				if !paused {
					line.WriteByte(b)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// TestGenericPagerFallback covers a profile that declares no MorePrompt (here
// cisco-ios) talking to a device that is still paging — the situation created
// by a pager-disable command the OS does not accept in that mode. The runner
// must recognise the marker anyway and space through it rather than hanging
// until the command timeout.
func TestGenericPagerFallback(t *testing.T) {
	addr, stop := fakePagingDevice(t)
	defer stop()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	dev := &model.Device{
		Name: "R1", Host: host, Conn: model.ConnSSH, Port: port,
		OSType: "cisco-ios", Username: "admin", Password: "x", EnablePassword: "en",
		CommandSet: "show", Enabled: true,
	}
	set := &model.CommandSet{Name: "show", Commands: []model.Command{{Text: "show interfaces"}}}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.CommandTimeout = 20
	settings.ConnectTimeout = 5

	start := time.Now()
	r := New(testRegistry())
	res := r.RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(), nil)
	elapsed := time.Since(start)

	if !res.Success {
		t.Fatalf("run failed against a paging device: %s\n---transcript---\n%s", res.Error, res.Transcript)
	}
	// Everything past the pager must be captured, not just the first page.
	if !strings.Contains(res.Transcript, "PAGE-TWO-CONTENT") {
		t.Fatalf("output after the pager was lost\n---transcript---\n%s", res.Transcript)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("took %s — the pager marker is not being answered", elapsed)
	}
}

// fakePromptInOutputDevice prints a "#" in the middle of a command's own
// output — NEC IX's "show processes" heads its table with a "PDEV#" column —
// and pages the rest. A prompt matched anywhere in the stream ends the wait
// there, so the next command is typed while the device is still at "--More--"
// and the pager eats its first character.
func fakePromptInOutputDevice(t *testing.T) (addr string, stop func(), got func() []string) {
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
	var mu sync.Mutex
	var seen []string
	record := func(c string) { mu.Lock(); seen = append(seen, c); mu.Unlock() }

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
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
					go promptInOutputShell(ch, record)
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func promptInOutputShell(ch ssh.Channel, record func(string)) {
	defer ch.Close()
	io.WriteString(ch, "\r\nR1#")

	buf := make([]byte, 1024)
	var line strings.Builder
	paused := false
	for {
		n, err := ch.Read(buf)
		for _, b := range buf[:n] {
			switch {
			case paused:
				// The pager swallows whatever key arrives, exactly as a real
				// one does — this is how the lost character happens.
				paused = false
				io.WriteString(ch, "\r\nTAIL-OF-PROCESSES\r\nR1#")
			case b == '\r':
			case b == '\n':
				cmd := strings.TrimSpace(line.String())
				line.Reset()
				record(cmd)
				if cmd == "show processes" {
					paused = true
					// The header lands first and carries a "#" (the PDEV column).
					io.WriteString(ch, "\r\nPID Status PDEV# TTY Process\r\n")
					// Real output streams: the rest, and the pager marker with
					// it, arrive later. That gap is where a prompt matched
					// mid-stream ends the wait too early.
					time.Sleep(600 * time.Millisecond)
					io.WriteString(ch, "  1 Sw Local Console\r\n--More--        ")
				} else {
					io.WriteString(ch, "\r\nR1#")
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

// TestPromptInsideOutputDoesNotEndCommand pins the fix: the command after a
// paged one must arrive intact, not with its first character eaten.
func TestPromptInsideOutputDoesNotEndCommand(t *testing.T) {
	addr, stop, got := fakePromptInOutputDevice(t)
	defer stop()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	dev := &model.Device{
		Name: "R1", Host: host, Conn: model.ConnSSH, Port: port,
		OSType: "cisco-ios", Username: "admin", Password: "x", EnablePassword: "en",
		CommandSet: "show", Enabled: true,
	}
	set := &model.CommandSet{Name: "show", Commands: []model.Command{
		{Text: "show processes"},
		{Text: "show logging"},
	}}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.CommandTimeout = 20
	settings.ConnectTimeout = 5

	r := New(testRegistry())
	res := r.RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(), nil)
	if !res.Success {
		t.Fatalf("run failed: %s\n---transcript---\n%s", res.Error, res.Transcript)
	}
	for _, want := range []string{"show processes", "show logging"} {
		found := false
		for _, c := range got() {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("device never received %q intact; it got %q", want, got())
		}
	}
}

// TestCountdownReportsSecondsLeft pins the per-command 待機 countdown: it must
// report every second, starting at the full pause so the UI never shows a
// stale figure, and stop when the context ends.
func TestCountdownReportsSecondsLeft(t *testing.T) {
	var got []int
	if !countdown(context.Background(), 3, func(left int) { got = append(got, left) }) {
		t.Fatal("countdown reported cancellation with a live context")
	}
	want := []int{3, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("reported %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reported %v, want %v", got, want)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if countdown(ctx, 5, func(int) {}) {
		t.Fatal("a cancelled context must abort the wait")
	}
}

// TestWrongPortHint covers the Telnet-at-port-22 mistake: switching a device's
// method without moving its port leaves a Telnet client reading the SSH
// identification string, which used to surface only as an expect timeout.
func TestWrongPortHint(t *testing.T) {
	telnetOn22 := &model.Device{Name: "d", Conn: model.ConnTelnet, Port: 22}
	sshBanner := "SSH-2.0-NEC-IX2105-ms-10.2.16\r\n"

	if h := wrongPortHint(telnetOn22, sshBanner); h == "" || !strings.Contains(h, "22") {
		t.Fatalf("expected a hint naming port 22, got %q", h)
	}
	// A real Telnet login must not be second-guessed.
	if h := wrongPortHint(&model.Device{Name: "d", Conn: model.ConnTelnet, Port: 23}, "\r\nlogin: "); h != "" {
		t.Fatalf("unexpected hint on a genuine telnet login: %q", h)
	}
	// SSH devices legitimately see that banner; saying anything there is noise.
	if h := wrongPortHint(&model.Device{Name: "d", Conn: model.ConnSSH, Port: 22}, sshBanner); h != "" {
		t.Fatalf("unexpected hint on an SSH device: %q", h)
	}
}
