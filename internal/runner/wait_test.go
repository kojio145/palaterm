package runner

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// setRun runs set against the fakeAsking router.
func setRun(t *testing.T, set *model.CommandSet) (DeviceResult, []string, time.Duration) {
	t.Helper()
	addr, got, stop := fakeAsking(t)
	defer stop()
	host, portS, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portS, "%d", &port)
	dev := &model.Device{Name: "r1", Host: host, Port: port, Conn: model.ConnTelnet,
		OSType: "cisco-ios", Username: "u", Password: "p", CommandSet: set.Name, Enabled: true}
	s := model.DefaultSettings()
	s.LogDir = t.TempDir()
	s.ConnectTimeout = 5
	s.CommandTimeout = 2
	start := time.Now()
	res := New(testRegistry()).RunDevice(context.Background(), dev, set, s, s.LogDir, time.Now(), nil)
	return res, got(), time.Since(start)
}

// A command's 待つ文字列 replaces the prompt: "boot" ends on "READY> ",
// which the Cisco prompt never matches, and the run goes on.
func TestCommandWaitText(t *testing.T) {
	res, got, _ := setRun(t, &model.CommandSet{Name: "s", Commands: []model.Command{
		{Text: "boot", Wait: "READY>"},
		{Text: "show clock"},
	}})
	if !res.Success {
		t.Fatalf("run failed: %s", res.Error)
	}
	// "show clock" follows "boot" directly: READY> ended the wait, not the
	// empty-Enter nudge a silent device gets after a second.
	if !strings.Contains(strings.Join(got, "|"), "boot|show clock") {
		t.Errorf("commands seen by the device: %v", got)
	}
	// A wait text that never shows is named in the error.
	res, _, _ = setRun(t, &model.CommandSet{Name: "s", Commands: []model.Command{{Text: "show clock", Wait: "(config)#"}}})
	if res.Success || !strings.Contains(res.Error, `待つ文字列 "(config)#" が出ませんでした`) {
		t.Fatalf("missing wait text: %q", res.Error)
	}
}

// waitTail matches the text on the device's current line only, literally.
func TestWaitTail(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"\r\nR1(config)#", true},
		{"\r\nR1(config)# ", true},
		{"\r\nEnter configuration commands\r\nR1(config-if)#", false},
		{"\r\nR1(config)#\r\nbuilding", false},
		{"\r\nR1(configX#", false},
		{"\r\nR1(config)# show run", false}, // not at the end of the line
	}
	for _, c := range cases {
		e := &expecter{}
		e.buf.WriteString(c.out)
		if got := e.Seen(waitTail("(config)#")); got != c.want {
			t.Errorf("%q: got %v, want %v", c.out, got, c.want)
		}
	}
	// "#" as the wait text: a column header still being printed is not the
	// prompt (NEC IX "show processes").
	for out, want := range map[string]bool{"\r\nPDEV#  Name": false, "\r\nIX-A(config)#": true, "\r\nIX-A(config)# ": true} {
		e := &expecter{}
		e.buf.WriteString(out)
		if got := e.Seen(waitTail("#")); got != want {
			t.Errorf("# on %q: got %v, want %v", out, got, want)
		}
	}
}

// 待機秒を使う off: the commands' pauses are skipped (and kept).
func TestPauseOff(t *testing.T) {
	set := &model.CommandSet{Name: "s", PauseOff: true, Commands: []model.Command{
		{Text: "show clock", PauseSec: 3, SerialSec: 3},
		{Text: "show clock", PauseSec: 3, SerialSec: 3},
	}}
	res, _, took := setRun(t, set)
	if !res.Success {
		t.Fatalf("run failed: %s", res.Error)
	}
	if took > 4*time.Second {
		t.Errorf("pauses were not skipped: %v", took)
	}
	if set.Commands[0].PauseSec != 3 {
		t.Errorf("pause value changed")
	}
}
