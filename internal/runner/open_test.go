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

// A factory-fresh device on its console is at a screen no OS profile knows —
// here Cisco's initial configuration dialog — and asks for no password at
// all. Open must put the user in front of that screen without typing a
// single byte on their behalf: no wake-up Enter, no credentials, no
// "enable", no pager command. That is what makes initial setup possible.
func TestOpenTypesNothingAndShowsTheDeviceAsIs(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var received []byte
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.WriteString(c, "\r\n--- System Configuration Dialog ---\r\n\r\nWould you like to enter the initial configuration dialog? [yes/no]: ")
		buf := make([]byte, 256)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				mu.Lock()
				received = append(received, buf[:n]...)
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	host, portS, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	fmt.Sscanf(portS, "%d", &port)

	dev := &model.Device{
		Name: "fresh", Host: host, Port: port, Conn: model.ConnTelnet,
		OSType: "cisco-ios", Username: "", Password: "", Enabled: true,
	}
	settings := model.DefaultSettings()
	settings.ConnectTimeout = 5

	r := New(testRegistry())
	started := time.Now()
	exp, err := r.Open(context.Background(), dev, settings)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer exp.Close()
	// Connect would have waited out the credential prompts and the ">" wait;
	// Open returns as soon as the line is up.
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("Open took %v; it must not wait for any prompt", took)
	}

	var screen strings.Builder
	var smu sync.Mutex
	exp.StartRaw(func(b []byte) { smu.Lock(); screen.Write(b); smu.Unlock() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		smu.Lock()
		got := screen.String()
		smu.Unlock()
		if strings.Contains(got, "[yes/no]:") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	smu.Lock()
	got := screen.String()
	smu.Unlock()
	if !strings.Contains(got, "initial configuration dialog? [yes/no]:") {
		t.Fatalf("the device's own screen must reach the terminal; got %q", got)
	}

	// Typing goes through as-is — the user answers the dialog themselves.
	if err := exp.SendRaw("no\r"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	typed := string(received)
	mu.Unlock()
	if typed != "no\r" {
		t.Fatalf("the device must receive only what the user typed; got %q", typed)
	}
}

// TakeTranscript hands back everything captured so far and forgets it, so a
// failed login that was already shown with its error is not replayed on the
// screen by StartRaw — while the caller can still put it in the log.
func TestTakeTranscriptClearsWhatStartRawWouldReplay(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	exp := newExpecter(b) // a net.Conn is a session.Session
	defer exp.Close()
	go io.WriteString(a, "Username: ")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !exp.Seen("Username: ") {
		time.Sleep(10 * time.Millisecond)
	}
	if got := exp.TakeTranscript(); got != "Username: " {
		t.Fatalf("TakeTranscript = %q", got)
	}
	if got := exp.Transcript(); got != "" {
		t.Fatalf("transcript should be empty after Take; got %q", got)
	}
	var replayed strings.Builder
	exp.StartRaw(func(b []byte) { replayed.Write(b) })
	if replayed.Len() != 0 {
		t.Fatalf("StartRaw replayed %q after the transcript was taken", replayed.String())
	}
}
