package runner

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// The jump host's shell coming back: its own prompt line, when that prompt
// names something; behind a bare prompt, only with its words for the
// session that ended.
func TestBackAtJump(t *testing.T) {
	for _, c := range []struct {
		tail, prompt string
		want         bool
	}{
		{"lab-sw1#exit\r\nConnection to 192.0.2.10 closed.\r\n[ops@jump1 ~]$ ", "[ops@jump1 ~]$", true},
		{"lab-sw1#\r\n[ops@jump1 ~]$ ", "[ops@jump1 ~]$", true}, // no closing words: the named prompt is enough
		{"\x1b[0m[ops@jump1 ~]$ \x1b[K", "[ops@jump1 ~]$", true},
		{"[ops@jump1 ~]$ ls", "[ops@jump1 ~]$", false}, // typing on it: not the prompt line any more
		{"lab-sw1#show run\r\n hostname [ops@jump1 ~]$\r\nlab-sw1#", "[ops@jump1 ~]$", false},
		{"lab-sw1#", "[ops@jump1 ~]$", false},
		{"Connection closed by foreign host.\r\n$ ", "$", true},
		{"[Connection to 192.0.2.10 closed by foreign host]\r\njump1#", "jump1#", true},
		{"lab-sw1$ exit\r\n$ ", "$", false}, // a bare prompt alone could be anyone's
		{"x\r\nConnection to 192.0.2.10 closed.\r\n$ ", "", false},
		{"Unauthorized connections will be closed.\r\n$ ", "$", false},
	} {
		if got := backAtJump(c.tail, c.prompt); got != c.want {
			t.Errorf("backAtJump(%q, %q) = %v", c.tail, c.prompt, got)
		}
	}
}

// exitTelnetDevice is a Telnet device (lab values) that logs in, answers at
// "lab-sw1#" and ends the session on "exit", as one whose idle timeout
// ran out does.
func exitTelnetDevice(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.WriteString(c, "Username: ")
				buf := make([]byte, 512)
				var line strings.Builder
				stage := 0
				for {
					n, err := c.Read(buf)
					for i := 0; i < n; i++ {
						b := buf[i]
						if b == '\n' && line.Len() == 0 {
							continue
						}
						if b != '\r' && b != '\n' {
							line.WriteByte(b)
							continue
						}
						cmd := strings.TrimSpace(line.String())
						line.Reset()
						switch {
						case stage == 0:
							stage = 1
							io.WriteString(c, "\r\nPassword: ")
						case stage == 1:
							stage = 2
							io.WriteString(c, "\r\nlab-sw1#")
						case cmd == "exit":
							return
						default:
							io.WriteString(c, "\r\nok\r\nlab-sw1#")
						}
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

// A window logged in through a jump host hears when the device ends its
// session and the jump host's shell is back (user report 2026-11-21: no
// 再接続 button there) — and not while the device's own prompt is up.
func TestOnBackAtJumpThroughBastion(t *testing.T) {
	bAddr, bStop := fakeBastionWith(t, "[ops@jump1 ~]$ ", nil)
	defer bStop()
	dAddr := exitTelnetDevice(t)
	bHost, bPortS, _ := net.SplitHostPort(bAddr)
	var bPort int
	fmt.Sscanf(bPortS, "%d", &bPort)
	dev := &model.Device{
		Name: "lab-sw1", Host: dAddr, Conn: model.ConnTelnet, OSType: "generic",
		Username: "ops", Password: "pw", Enabled: true,
		Bastions: []model.Bastion{{Method: model.BastionSSH, Host: bHost, Port: bPort, Username: "ops", Password: "pw"}},
	}
	settings := model.DefaultSettings()
	settings.ConnectTimeout = 5
	settings.CommandTimeout = 6
	r := New(testRegistry())
	exp, _, err := r.Connect(context.Background(), dev, settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer exp.Close()
	var calls atomic.Int32
	var out strings.Builder
	got := make(chan string, 64)
	exp.StartRaw(func(b []byte) { got <- string(b) })
	exp.OnBackAtJump(func() { calls.Add(1) })
	waitFor := func(s string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !strings.Contains(out.String(), s) {
			select {
			case b := <-got:
				out.WriteString(b)
			case <-deadline:
				t.Fatalf("no %q in:\n%s", s, out.String())
			}
		}
	}
	_ = exp.SendRaw("show clock\r")
	waitFor("ok")
	waitFor("lab-sw1#")
	time.Sleep(200 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("called %d times at the device's prompt", n)
	}
	out.Reset()
	_ = exp.SendRaw("exit\r")
	waitFor("[ops@jump1 ~]$")
	time.Sleep(200 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("called %d times back on the jump host:\n%s", n, out.String())
	}
	if exp.Closed() {
		t.Fatal("the line to the jump host is still up")
	}
}

// A device reached directly is never watched.
func TestOnBackAtJumpNotDirect(t *testing.T) {
	sess, dev := newScriptedDevice()
	_ = dev
	exp := newExpecter(sess)
	defer exp.Close()
	var calls atomic.Int32
	exp.StartRaw(func([]byte) {})
	exp.OnBackAtJump(func() { calls.Add(1) })
	sess.say("Connection to 192.0.2.10 closed.\r\n$ ")
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("a session with no jump host was taken as back on one")
	}
}
