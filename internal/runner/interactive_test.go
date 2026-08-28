package runner

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// TestInteractivePassThrough verifies that after Connect logs in, the session
// can be switched to raw mode: device output reaches the sink and keystrokes
// sent back reach the device (which echoes them).
func TestInteractivePassThrough(t *testing.T) {
	addr, stop := fakeCisco(t)
	defer stop()
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	dev := &model.Device{
		Name: "router1", Host: host, Conn: model.ConnSSH, Port: port,
		OSType: "cisco-ios", Username: "admin", Password: "x", EnablePassword: "en",
		Enabled: true,
	}
	settings := model.DefaultSettings()
	settings.ConnectTimeout = 5
	settings.CommandTimeout = 6

	r := New(testRegistry())
	exp, _, err := r.Connect(context.Background(), dev, settings, nil)
	if exp != nil {
		defer exp.Close()
	}
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	var mu sync.Mutex
	var got []byte
	closed := make(chan struct{})
	sink := func(b []byte) { mu.Lock(); got = append(got, b...); mu.Unlock() }
	onClose := func() { close(closed) }

	send, _, closeFn := r.StartInteractive(exp, sink, onClose)

	// The login banner/prompt should have been flushed to the sink.
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	flushed := string(got)
	mu.Unlock()
	if !strings.Contains(flushed, "router1#") {
		t.Fatalf("expected prompt flushed to interactive sink, got: %q", flushed)
	}

	// Send a command; the fake device replies with version output.
	if err := send("show version\r\n"); err != nil {
		t.Fatalf("send: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := strings.Contains(string(got), "Cisco IOS Software")
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	final := string(got)
	mu.Unlock()
	if !strings.Contains(final, "Cisco IOS Software") {
		t.Fatalf("interactive command output not received:\n%s", final)
	}

	// Closing should fire onClose.
	_ = closeFn()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("onClose was not fired after close")
	}
}
