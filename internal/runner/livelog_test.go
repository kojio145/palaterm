package runner

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// A batch device's log is written while it runs: the path is on its events
// from the start, the file already holds the output during a command's
// pause, and when the device ends the file is exactly what the old
// end-of-run write produced. A second device of the same name in the same
// folder gets "_2"; a device that never answered leaves no file.
func TestRunDeviceWritesLogWhileRunning(t *testing.T) {
	addr, stop := fakeCisco(t)
	defer stop()
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	dev := &model.Device{
		Name: "router1", Host: host, Conn: model.ConnSSH, Port: port,
		OSType: "cisco-ios", Username: "admin", Password: "x", EnablePassword: "en",
		CommandSet: "backup", Enabled: true,
	}
	set := &model.CommandSet{Name: "backup", Commands: []model.Command{{Text: "show version", PauseSec: 2}, {Text: "terminal length 0"}}}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.CommandTimeout = 5
	settings.ConnectTimeout = 5
	r := New(testRegistry())
	now := time.Now()

	var mu sync.Mutex
	var paths []string
	midRun := ""
	emit := func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, string(ev.Phase)+"="+ev.LogPath)
		// During show version's pause: the log must already hold its output.
		if ev.Phase == PhaseRunning && ev.WaitSec == 1 && midRun == "" {
			if !r.FlushLive(ev.LogPath) {
				midRun = "not live"
				return
			}
			b, _ := os.ReadFile(ev.LogPath)
			midRun = string(b)
		}
	}
	res := r.RunDevice(context.Background(), dev, set, settings, settings.LogDir, now, emit)
	if !res.Success || res.LogPath == "" {
		t.Fatalf("run failed: %+v", res.Error)
	}
	if !strings.Contains(midRun, "Cisco IOS Software, Version 15.7") {
		t.Fatalf("log during the pause = %q", midRun)
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, "="+res.LogPath) {
			t.Fatalf("event without the log path: %q (all: %v)", p, paths)
		}
	}
	if r.FlushLive(res.LogPath) {
		t.Fatal("finished log still counted as live")
	}
	got, _ := os.ReadFile(res.LogPath)
	if string(got) != res.Transcript {
		t.Fatalf("log differs from the transcript:\n%q\n%q", got, res.Transcript)
	}

	// Same name, same folder: the second log is "_2", the first untouched.
	res2 := r.RunDevice(context.Background(), dev, set, settings, settings.LogDir, now, nil)
	if !res2.Success || !strings.HasSuffix(strings.TrimSuffix(res2.LogPath, ".log"), "_2") && !strings.Contains(res2.LogPath, "_2.") {
		t.Fatalf("second log path %q (first %q)", res2.LogPath, res.LogPath)
	}
	if again, _ := os.ReadFile(res.LogPath); string(again) != string(got) {
		t.Fatal("first log changed by the second run")
	}

	// Nobody answers: the claimed file is removed again.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := l.Addr().(*net.TCPAddr).Port
	l.Close()
	dead := *dev
	dead.Name, dead.Port = "dead1", deadPort
	res3 := r.RunDevice(context.Background(), &dead, set, settings, settings.LogDir, now, nil)
	if res3.Success || res3.LogPath != "" {
		t.Fatalf("dead device: %+v", res3)
	}
	entries, _ := os.ReadDir(filepath.Dir(res.LogPath))
	for _, e := range entries {
		if strings.Contains(e.Name(), "dead1") {
			t.Fatalf("empty log left for a device that never answered: %s", e.Name())
		}
	}
}

// 中止 on one row stops that device only: the one in progress is cut off
// (its log so far kept), one still waiting is never started, and the rest
// of the batch runs to the end.
func TestCancelDeviceStopsOnlyThatDevice(t *testing.T) {
	addr, stop := fakeCisco(t)
	defer stop()
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mk := func(name string) model.Device {
		return model.Device{Name: name, Host: host, Conn: model.ConnSSH, Port: port, OSType: "cisco-ios",
			Username: "admin", Password: "x", EnablePassword: "en", CommandSet: "slow", Enabled: true}
	}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.CommandTimeout = 5
	settings.ConnectTimeout = 5
	settings.MaxParallel = 2
	inv := &model.Inventory{
		Devices:     []model.Device{mk("a1"), mk("b1"), mk("c1")},
		CommandSets: []model.CommandSet{{Name: "slow", Commands: []model.Command{{Text: "show version", PauseSec: 3}, {Text: "terminal length 0"}}}},
		Settings:    settings,
	}
	r := New(testRegistry())
	if r.CancelDevice("a1") {
		t.Fatal("CancelDevice outside a batch reported true")
	}
	var once sync.Once
	emit := func(ev Event) {
		if ev.Device == "a1" && ev.Phase == PhaseRunning && ev.WaitSec > 0 {
			once.Do(func() {
				go func() {
					if !r.CancelDevice("c1") || !r.CancelDevice("a1") {
						t.Error("CancelDevice during the batch reported false")
					}
				}()
			})
		}
	}
	results, _ := r.RunBatchOpts(context.Background(), inv, Options{}, emit)
	got := map[string]DeviceResult{}
	for _, res := range results {
		got[res.Device] = res
	}
	if a := got["a1"]; !a.Canceled || a.Error != CanceledDeviceMsg || a.LogPath == "" {
		t.Fatalf("a1: %+v", a)
	}
	if b := got["b1"]; !b.Success {
		t.Fatalf("b1 should have run to the end: %+v", b.Error)
	}
	if c := got["c1"]; !c.Canceled || c.Error != CanceledNotRunMsg || c.LogPath != "" {
		t.Fatalf("c1: %+v", c)
	}
	if log, _ := os.ReadFile(got["a1"].LogPath); !strings.Contains(string(log), "Cisco IOS Software") {
		t.Fatalf("a1's log so far was not kept: %q", log)
	}
	if r.CancelDevice("b1") {
		t.Fatal("CancelDevice after the batch reported true")
	}
}
