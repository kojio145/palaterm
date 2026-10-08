package runner

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// A listener that accepts and then says nothing: an SSH dial to it completes
// the TCP connect and then hangs in the handshake until the context ends.
func silentListener(t *testing.T) (string, int) {
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
			go func() { time.Sleep(time.Minute); c.Close() }()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func collectEvents() (EmitFunc, func() map[string]Phase) {
	var mu sync.Mutex
	last := map[string]Phase{}
	emit := func(ev Event) {
		mu.Lock()
		last[ev.Device] = ev.Phase
		mu.Unlock()
	}
	return emit, func() map[string]Phase {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]Phase{}
		for k, v := range last {
			out[k] = v
		}
		return out
	}
}

// TestCancelMarksUnfinishedAsCanceled pins what 中止 leaves behind: the device
// cut off mid-connect and the devices that never got a turn are both reported
// as canceled (not as errors), with distinct messages, while the UI's
// 失敗のみ再実行 no longer has to lump them in with real failures.
func TestCancelMarksUnfinishedAsCanceled(t *testing.T) {
	host, port := silentListener(t)
	inv := &model.Inventory{
		Settings: model.Settings{ConnectTimeout: 30, CommandTimeout: 5, MaxParallel: 1, LogDir: t.TempDir()},
		Devices: []model.Device{
			{Name: "first", Host: host, Port: port, Conn: model.ConnSSH, OSType: "generic", Username: "u", Password: "p", Enabled: true},
			{Name: "second", Host: host, Port: port, Conn: model.ConnSSH, OSType: "generic", Username: "u", Password: "p", Enabled: true},
			{Name: "third", Host: host, Port: port, Conn: model.ConnSSH, OSType: "generic", Username: "u", Password: "p", Enabled: true},
		},
	}
	r := New(profile.NewRegistry())
	emit, phases := collectEvents()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(400 * time.Millisecond); cancel() }()
	start := time.Now()
	results := r.RunBatch(ctx, inv, emit)
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("cancel took %v; must not wait for the connect timeout", el)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d", len(results))
	}
	byName := map[string]DeviceResult{}
	for _, res := range results {
		byName[res.Device] = res
	}
	if !byName["first"].Canceled || byName["first"].Error != CanceledMsg || byName["first"].Success {
		t.Errorf("interrupted device: %+v", byName["first"])
	}
	for _, n := range []string{"second", "third"} {
		if !byName[n].Canceled || byName[n].Error != CanceledNotRunMsg {
			t.Errorf("%s should be 'not run': %+v", n, byName[n])
		}
	}
	for n, ph := range phases() {
		if ph != PhaseCanceled {
			t.Errorf("%s final phase = %s, want canceled", n, ph)
		}
	}
}

// TestPreCanceledBatchRunsNothing: a context already dead when the batch
// starts reports every device as not run and touches no network.
func TestPreCanceledBatchRunsNothing(t *testing.T) {
	inv := &model.Inventory{
		Settings: model.Settings{ConnectTimeout: 30, CommandTimeout: 5, LogDir: t.TempDir()},
		Devices: []model.Device{
			{Name: "a", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "generic", Enabled: true},
			{Name: "b", Host: "192.0.2.2", Conn: model.ConnSSH, OSType: "generic", Enabled: true},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	results := New(profile.NewRegistry()).RunBatch(ctx, inv, nil)
	if time.Since(start) > 2*time.Second {
		t.Fatal("pre-canceled batch must return immediately")
	}
	for _, res := range results {
		if !res.Canceled || res.Error != CanceledNotRunMsg {
			t.Errorf("%+v", res)
		}
	}
}
