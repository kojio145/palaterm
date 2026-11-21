package runner

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// The device's login prompt as the line the screen is on now, or Cisco's
// "Press RETURN to get started." with nothing after it — and not a shell's
// "Last login:", a password prompt or a line that only mentions a login.
func TestLoggedOutAtLogin(t *testing.T) {
	for _, c := range []struct {
		tail string
		want bool
	}{
		{"lab-con1#exit\r\n\r\nUser Access Verification\r\n\r\nUsername: ", true},
		{"lab1 login: ", true},
		{"\x1b[0mLogin:\x1b[K", true},
		{"lab-con1#exit\r\n\r\n\r\nlab-con1 con0 is now available\r\n\r\n\r\nPress RETURN to get started.\r\n\r\n\r\n", true},
		{"Press RETURN to get started.", true},
		{"lab-con1#", false},
		{"lab-con1#enable\r\nPassword: ", false}, // enable asks for a password too
		{"Last login:", false},                   // a chunk cut after the colon
		{"Last login: lab banner\r\n[ops@jump1 ~]$ ", false},
		{"lab-con1#show run | i login\r\n login local\r\nlab-con1#", false},
		{"Username: ops\r\nPassword: \r\nlab-con1#", false},
		{"Press RETURN to get started.\r\nUsername: ops", false},
		{"", false},
	} {
		if got := loggedOutAtLogin(c.tail); got != c.want {
			t.Errorf("loggedOutAtLogin(%q) = %v", c.tail, got)
		}
	}
}

// On a session without jump hosts (a console), the window hears when the
// output comes to rest on the device's login prompt — once, and not for a
// login prompt that more output follows straight away.
func TestOnBackAtLoginScripted(t *testing.T) {
	sess, _ := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	exp.setLoginWatch()
	var calls atomic.Int32
	exp.StartRaw(func([]byte) {})
	exp.OnBackAtJump(func() { calls.Add(1) })
	if !exp.AtLoginWatch() {
		t.Fatal("AtLoginWatch is false on a session without jump hosts")
	}
	settle := loginSettle + 300*time.Millisecond

	sess.say("show log\r\nLast login:")
	time.Sleep(50 * time.Millisecond)
	sess.say(" lab banner\r\nUsername:")
	time.Sleep(50 * time.Millisecond)
	sess.say(" ops was here\r\nlab-con1#")
	time.Sleep(settle)
	if n := calls.Load(); n != 0 {
		t.Fatalf("called %d times for output that went on past a login prompt", n)
	}

	sess.say("exit\r\n\r\nlab-con1 con0 is now available\r\n\r\nPress RETURN to get started.\r\n")
	time.Sleep(settle)
	if n := calls.Load(); n != 1 {
		t.Fatalf("called %d times on Press RETURN", n)
	}
	sess.say("\r\n\r\nUsername: ")
	time.Sleep(settle)
	if n := calls.Load(); n != 1 {
		t.Fatalf("called %d times: once per return to the login prompt", n)
	}
	sess.say("ops\r\nPassword: \r\nlab-con1#")
	time.Sleep(settle)
	sess.say("exit\r\nUsername: ")
	time.Sleep(settle)
	if n := calls.Load(); n != 2 {
		t.Fatalf("called %d times after a second logout", n)
	}
}

// A session through jump hosts keeps the jump host's watch: a login prompt
// there is the device's own, not the end of the session.
func TestLoginWatchYieldsToJump(t *testing.T) {
	sess, _ := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	exp.setJumpPrompt("[ops@jump1 ~]$")
	var calls atomic.Int32
	exp.StartRaw(func([]byte) {})
	exp.OnBackAtJump(func() { calls.Add(1) })
	if exp.AtLoginWatch() {
		t.Fatal("AtLoginWatch is true through a jump host")
	}
	sess.say("lab-sw1#telnet 192.0.2.30\r\nUsername: ")
	time.Sleep(loginSettle + 300*time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("called %d times", n)
	}
}

// consoleTelnetDevice is a console server's port (lab values): it logs in,
// answers at "lab-con1#", and on "exit" or "idle" logs the user out the way
// a console does — "Press RETURN to get started." with the line still up.
func consoleTelnetDevice(t *testing.T) string {
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
							io.WriteString(c, "\r\nlab-con1#")
						case stage == 3:
							stage = 0
							io.WriteString(c, "\r\nUsername: ")
						case cmd == "exit":
							stage = 3
							io.WriteString(c, "\r\n\r\nlab-con1 con0 is now available\r\n\r\nPress RETURN to get started.\r\n")
						default:
							io.WriteString(c, "\r\nok\r\nlab-con1#")
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

// A device logged into without jump hosts — here a console server's port
// over Telnet, as a serial console behaves — tells the window when it logs
// the user out and the line stays up (user request 2026-11-22, after the
// jump host case of v1.10.6).
func TestOnBackAtLoginConsole(t *testing.T) {
	host, portS, _ := net.SplitHostPort(consoleTelnetDevice(t))
	port, _ := strconv.Atoi(portS)
	dev := &model.Device{
		Name: "lab-con1", Host: host, Port: port, Conn: model.ConnTelnet, OSType: "generic",
		Username: "ops", Password: "pw", Enabled: true,
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
	if !exp.AtLoginWatch() {
		t.Fatal("AtLoginWatch is false on a direct login")
	}
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
	settle := loginSettle + 300*time.Millisecond
	_ = exp.SendRaw("show clock\r")
	waitFor("ok")
	time.Sleep(settle)
	if n := calls.Load(); n != 0 {
		t.Fatalf("called %d times at the device's prompt", n)
	}
	_ = exp.SendRaw("exit\r")
	waitFor("Press RETURN to get started.")
	time.Sleep(settle)
	if n := calls.Load(); n != 1 {
		t.Fatalf("called %d times after the logout:\n%s", n, out.String())
	}
	if exp.Closed() {
		t.Fatal("the console line is still up")
	}
}
