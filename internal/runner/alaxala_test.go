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

// fakeAlaxala is a Telnet "device" speaking the way the legacy TTL macro
// drove an ALAXALA AX (osType 15): "login:" / "Password:" prompts, a ">"
// user prompt, "enable" (no enable password configured) straight to "#",
// then "set terminal pager disable" and show commands at "#". It records
// every line it receives so the test can check the exact sequence.
func fakeAlaxala(t *testing.T) (addr string, cmds func() []string, stop func()) {
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
			go alaxalaConn(c, func(s string) { mu.Lock(); got = append(got, s); mu.Unlock() })
		}
	}()
	return ln.Addr().String(), func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }, func() { ln.Close() }
}

func alaxalaConn(c net.Conn, record func(string)) {
	defer c.Close()
	io.WriteString(c, "\r\nlogin: ")
	buf := make([]byte, 512)
	var line strings.Builder
	stage := 0 // 0=login, 1=password, 2=user mode ">", 3=admin mode "#"
	for {
		n, err := c.Read(buf)
		for i := 0; i < n; i++ {
			b := buf[i]
			if b != '\n' {
				if b != '\r' {
					line.WriteByte(b)
				}
				continue
			}
			cmd := strings.TrimSpace(line.String())
			line.Reset()
			record(cmd)
			switch stage {
			case 0:
				stage = 1
				io.WriteString(c, "\r\nPassword: ")
			case 1:
				stage = 2
				io.WriteString(c, "\r\nAX2530S> ")
			case 2:
				if cmd == "enable" {
					stage = 3
					// The real switch follows the first "#" with an access log
					// line of its own, so the prompt is not the last thing on
					// screen (seen on an AX2530S in the field, 2026-10-10).
					io.WriteString(c, "\r\nAX2530S# \r\n2026/10/10 21:54:20 01S E3 ACCESS 00030001 0209:000000000000 Local authentication succeeded.\r\n")
				} else if cmd == "exit" || cmd == "logout" {
					return
				} else {
					io.WriteString(c, "\r\nAX2530S> ")
				}
			default:
				switch cmd {
				case "show version":
					io.WriteString(c, "\r\nALAXALA AX2530S-24T Ver. 4.4\r\nAX2530S# ")
				case "exit":
					stage = 2
					io.WriteString(c, "\r\nAX2530S> ")
				default: // set terminal pager disable, blank, …
					io.WriteString(c, "\r\nAX2530S# ")
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// The built-in alaxala-ax profile must log in over Telnet, escalate with
// "enable" (skipping the enable-password row the device never asks for),
// get past the unsolicited log line the switch prints after that prompt,
// disable the pager and run a command — the sequence the legacy macro used.
func TestAlaxalaProfileEndToEnd(t *testing.T) {
	addr, cmds, stop := fakeAlaxala(t)
	defer stop()
	host, portS, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portS, "%d", &port)

	dev := &model.Device{
		Name: "ax1", Host: host, Port: port, Conn: model.ConnTelnet,
		OSType: "alaxala-ax", Username: "operator", Password: "pw", EnablePassword: "",
		CommandSet: "chk", Enabled: true,
	}
	set := &model.CommandSet{Name: "chk", Commands: []model.Command{{Text: "show version"}}}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.ConnectTimeout = 5
	settings.CommandTimeout = 6

	r := New(testRegistry())
	started := time.Now()
	res := r.RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(), nil)
	if !res.Success {
		t.Fatalf("alaxala run failed: %s\n---\n%s", res.Error, res.Transcript)
	}
	// The enable-password wait (the switch never asks) used to cost its full
	// three seconds; the prompt already on screen now ends it after about
	// one. Of what remains, disconnect's settle wait after the first "exit"
	// (the prompt is ">" then, not "#") is the bulk; the whole run was 6.1s.
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("run took %v; the skipped credential prompts should not cost their full wait", took)
	}
	if !strings.Contains(res.Transcript, "ALAXALA AX2530S-24T Ver. 4.4") {
		t.Fatalf("show version output missing:\n%s", res.Transcript)
	}
	got := strings.Join(cmds(), " | ")
	for _, want := range []string{"operator | pw | enable", "set terminal pager disable", "show version"} {
		if !strings.Contains(got, want) {
			t.Errorf("device did not receive %q in order; got: %s", want, got)
		}
	}
	if strings.Index(got, "set terminal pager disable") > strings.Index(got, "show version") {
		t.Errorf("pager must be disabled before the first command; got: %s", got)
	}
}
