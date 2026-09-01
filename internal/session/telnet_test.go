package session

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// drainFor accumulates everything the client sends within d. The client writes
// each negotiation reply as its own packet, so a single Read would catch only
// the first one and the test would fail for reasons that have nothing to do
// with the policy under test.
func drainFor(conn net.Conn, d time.Duration) []byte {
	var acc []byte
	buf := make([]byte, 256)
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
		n, err := conn.Read(buf)
		acc = append(acc, buf[:n]...)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			break
		}
	}
	conn.SetReadDeadline(time.Time{})
	return acc
}

// fakeIXTelnet stands in for a NEC IX2105 telnet server. It opens with
// "IAC WILL ECHO, IAC WILL SUPPRESS-GO-AHEAD" and — like the real device —
// hangs up on a client that refuses both, instead of falling back to line
// mode. Whatever the client replied is reported back to the test.
func fakeIXTelnet(t *testing.T) (addr string, replies func() []byte, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write([]byte{iac, will, optEcho, iac, will, optSuppressGoAhead})

		answer := drainFor(conn, 400*time.Millisecond)
		got <- answer

		// Accepting both is what earns the login prompt.
		if strings.Contains(string(answer), string([]byte{iac, do, optEcho})) &&
			strings.Contains(string(answer), string([]byte{iac, do, optSuppressGoAhead})) {
			conn.Write([]byte("login: "))
			time.Sleep(300 * time.Millisecond)
			return
		}
		// Refused: the device drops the session without a word.
	}()
	return ln.Addr().String(), func() []byte {
		select {
		case a := <-got:
			return a
		case <-time.After(3 * time.Second):
			t.Fatal("server never saw a negotiation reply")
			return nil
		}
	}, func() { ln.Close() }
}

// TestTelnetAcceptsEchoAndSuppressGoAhead pins the negotiation policy. The
// client used to refuse every option, which a NEC IX2105 answers by closing
// the connection as soon as a username arrives — the login then failed with
// nothing logged but the device's own "login: " prompt.
func TestTelnetAcceptsEchoAndSuppressGoAhead(t *testing.T) {
	addr, replies, stop := fakeIXTelnet(t)
	defer stop()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	sess, err := dialTelnet(host, port, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	buf := make([]byte, 64)
	n, _ := sess.Read(buf) // drives the negotiation and returns the prompt

	answer := replies()
	want := map[string][]byte{
		"DO ECHO":              {iac, do, optEcho},
		"DO SUPPRESS-GO-AHEAD": {iac, do, optSuppressGoAhead},
	}
	for name, seq := range want {
		if !strings.Contains(string(answer), string(seq)) {
			t.Errorf("client did not reply %s; sent % x", name, answer)
		}
	}
	if got := string(buf[:n]); !strings.Contains(got, "login:") {
		t.Errorf("prompt not delivered to the caller, got %q", got)
	}
}

// TestTelnetRefusesOtherOptions keeps the policy narrow: only the two options
// that matter are accepted, so nothing pulls the client into subnegotiation it
// does not implement.
func TestTelnetRefusesOtherOptions(t *testing.T) {
	const optTerminalType = 24
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write([]byte{iac, do, optTerminalType, iac, will, optTerminalType})
		got <- drainFor(conn, 400*time.Millisecond)
		conn.Write([]byte("login: "))
		time.Sleep(200 * time.Millisecond)
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	sess, err := dialTelnet(host, port, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	buf := make([]byte, 64)
	sess.Read(buf)

	answer := <-got
	for name, seq := range map[string][]byte{
		"WONT TERMINAL-TYPE": {iac, wont, optTerminalType},
		"DONT TERMINAL-TYPE": {iac, dont, optTerminalType},
	} {
		if !strings.Contains(string(answer), string(seq)) {
			t.Errorf("client did not reply %s; sent % x", name, answer)
		}
	}
}
