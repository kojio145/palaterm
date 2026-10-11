package runner

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// fakeLinux is a Telnet "server" answering like a Linux box: "login:" /
// "Password:", a bash-style prompt ("user@srv01:~$ ", or "root@srv01:~# "
// for root) preceded by the bracketed-paste switch bash 5.1+ prints, and
// command output that itself contains "$" and "#" mid-line. It records every
// line it receives.
func fakeLinux(t *testing.T) (addr string, cmds func() []string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []string
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go linuxConn(c, func(s string) { mu.Lock(); got = append(got, s); mu.Unlock() })
		}
	}()
	return ln.Addr().String(), func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }, func() { ln.Close() }
}

func linuxConn(c net.Conn, record func(string)) {
	defer c.Close()
	io.WriteString(c, "Ubuntu 24.04 LTS\r\nsrv01 login: ")
	buf := make([]byte, 512)
	var line strings.Builder
	stage, user := 0, ""
	prompt := func() string {
		if user == "root" {
			return "\x1b[?2004hroot@srv01:~# "
		}
		return "\x1b[?2004h" + user + "@srv01:~$ "
	}
	for {
		n, err := c.Read(buf)
		for i := 0; i < n; i++ {
			b := buf[i]
			if b != '\r' && b != '\n' {
				line.WriteByte(b)
				continue
			}
			if b == '\n' && line.Len() == 0 {
				continue // the LF of a CRLF
			}
			cmd := strings.TrimSpace(line.String())
			line.Reset()
			record(cmd)
			switch stage {
			case 0:
				user, stage = cmd, 1
				io.WriteString(c, "\r\nPassword: ")
			case 1:
				stage = 2
				io.WriteString(c, "\r\nWelcome to Ubuntu 24.04 LTS (# 1 SMP $Revision$)\r\n"+prompt())
			default:
				io.WriteString(c, "\x1b[?2004l\r\n")
				switch {
				case cmd == "exit":
					io.WriteString(c, "logout\r\n")
					return
				case cmd == "uname -a":
					io.WriteString(c, "Linux srv01 6.8.0-45-generic #45-Ubuntu SMP x86_64 GNU/Linux\r\n")
				case strings.HasPrefix(cmd, "echo "):
					io.WriteString(c, strings.TrimPrefix(cmd, "echo ")+"\r\n")
				}
				io.WriteString(c, prompt())
			}
		}
		if err != nil {
			return
		}
	}
}

// The built-in Linux profiles log in over Telnet, turn the pagers off before
// the first command, run commands whose output carries "$" and "#", and log
// out — the ordinary user at "$", root at "#".
func TestLinuxProfilesEndToEnd(t *testing.T) {
	for _, tc := range []struct{ os, user string }{{"linux", "ops"}, {"linux-root", "root"}} {
		t.Run(tc.os, func(t *testing.T) {
			addr, cmds, stop := fakeLinux(t)
			defer stop()
			host, portS, _ := net.SplitHostPort(addr)
			var port int
			fmt.Sscanf(portS, "%d", &port)
			dev := &model.Device{
				Name: "srv01", Host: host, Port: port, Conn: model.ConnTelnet,
				OSType: tc.os, Username: tc.user, Password: "pw", CommandSet: "chk", Enabled: true,
			}
			set := &model.CommandSet{Name: "chk", Commands: []model.Command{
				{Text: "uname -a"}, {Text: "echo cost $5 # note"},
			}}
			settings := model.DefaultSettings()
			settings.LogDir = t.TempDir()
			settings.ConnectTimeout = 5
			settings.CommandTimeout = 6
			r := New(testRegistry())
			started := time.Now()
			res := r.RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(), nil)
			if !res.Success {
				t.Fatalf("run failed: %s\n---\n%s", res.Error, res.Transcript)
			}
			if took := time.Since(started); took > 6*time.Second {
				t.Errorf("run took %v", took)
			}
			for _, want := range []string{"6.8.0-45-generic", "cost $5 # note"} {
				if !strings.Contains(res.Transcript, want) {
					t.Errorf("output %q missing:\n%s", want, res.Transcript)
				}
			}
			got := strings.Join(cmds(), " | ")
			pager := "export PAGER=cat SYSTEMD_PAGER=cat GIT_PAGER=cat"
			for _, want := range []string{tc.user + " | pw", pager, "uname -a", "echo cost $5 # note", "exit"} {
				if !strings.Contains(got, want) {
					t.Errorf("server did not receive %q; got: %s", want, got)
				}
			}
			if strings.Index(got, pager) > strings.Index(got, "uname -a") {
				t.Errorf("pagers must be off before the first command; got: %s", got)
			}
		})
	}
}
