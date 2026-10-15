package session

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// startEcho is a stand-in web GUI: it answers each line with "ok <line>".
func startEcho(t *testing.T) string {
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
			go func() {
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					_, _ = c.Write([]byte("ok " + sc.Text() + "\n"))
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func ask(t *testing.T, addr, line string) string {
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
	got, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(got)
}

func TestForwardThroughTwoHops(t *testing.T) {
	h1 := startTunnelHop(t, "hop1$ ", false, nil)
	h2 := startTunnelHop(t, "hop2$ ", false, nil)
	gui := startEcho(t)
	f, err := DialForward([]model.Bastion{hopOf(t, h1), hopOf(t, h2)}, gui, "127.0.0.2", 5*time.Second, DialOpts{})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !strings.HasPrefix(f.Local, "127.0.0.2:") {
		t.Errorf("listens on %s, want loopback only", f.Local)
	}
	// Several connections, as a browser opens them.
	for _, q := range []string{"GET /", "GET /login"} {
		if got := ask(t, f.Local, q); got != "ok "+q {
			t.Errorf("%q -> %q", q, got)
		}
	}
	if n := len(h2.forwarded); n < 3 { // the check at start + two requests
		t.Errorf("hop2 forwarded %d channels: %v", n, h2.forwarded)
	}
	f.Close()
	select {
	case <-f.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed")
	}
	if c, err := net.DialTimeout("tcp", f.Local, time.Second); err == nil {
		c.Close()
		t.Error("port still open after Close")
	}
}

func TestForwardRefusedIsSaidUpFront(t *testing.T) {
	h1 := startTunnelHop(t, "hop1$ ", true, nil)
	_, err := DialForward([]model.Bastion{hopOf(t, h1)}, startEcho(t), "", 5*time.Second, DialOpts{})
	if err == nil || !strings.Contains(err.Error(), "転送を許可していません") {
		t.Fatalf("want a forwarding-refused error, got %v", err)
	}
}

func TestForwardStopsWhenHopDrops(t *testing.T) {
	h1 := startTunnelHop(t, "hop1$ ", false, nil)
	f, err := DialForward([]model.Bastion{hopOf(t, h1)}, startEcho(t), "", 5*time.Second, DialOpts{})
	if err != nil {
		t.Fatal(err)
	}
	f.clients[0].Close()
	select {
	case <-f.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("forward kept running after the hop's connection dropped")
	}
}

func TestForwardListensOnLoopbackOnly(t *testing.T) {
	h1 := startTunnelHop(t, "hop1$ ", false, nil)
	if _, err := DialForward([]model.Bastion{hopOf(t, h1)}, startEcho(t), "0.0.0.0", 5*time.Second, DialOpts{}); err == nil {
		t.Fatal("a non-loopback listen address was accepted")
	}
}
