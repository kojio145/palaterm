package session

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// Pointing SSH at a port that speaks something else (a Telnet port, say) used
// to ignore the connect timeout entirely: ssh.Dial applies it to the TCP
// connect, which succeeds immediately, and then lets the handshake run
// unbounded. On the lab bench that left one device of a batch stuck for two
// minutes — until the far end gave up — no matter what the user had configured.
func TestDialSSHHonoursTheConnectTimeoutDuringTheHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// A server that accepts and then says nothing at all.
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	const timeout = 700 * time.Millisecond
	start := time.Now()
	_, err = dialSSH(host, port, "u", "p", model.AuthPassword, "", "", timeout, DialOpts{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("dialSSH succeeded against a server that never spoke SSH")
	}
	// Generous ceiling: the point is that it returns on its own, not that it
	// returns to the millisecond.
	if elapsed > 5*timeout {
		t.Errorf("dialSSH took %s with a %s timeout — the handshake is unbounded", elapsed, timeout)
	}

	select {
	case c := <-accepted:
		c.Close()
	default:
	}
}
