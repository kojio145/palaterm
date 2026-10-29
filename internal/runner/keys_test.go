package runner

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// A {ctrl+c} line sends the control character itself, with no line ending,
// and the set goes on once the prompt is back: "boot" leaves the fake on a
// line the Cisco prompt does not match (its 待つ文字列 is READY>), ^C brings
// the prompt back, and "show clock" runs after it. {break} on a Telnet
// session is an error that stops the device, not text typed to it.
func TestKeyTokenLines(t *testing.T) {
	run := func(cmds []model.Command) (DeviceResult, []string) {
		addr, got, stop := fakeAsking(t)
		defer stop()
		host, portS, _ := net.SplitHostPort(addr)
		var port int
		fmt.Sscanf(portS, "%d", &port)
		dev := &model.Device{Name: "r1", Host: host, Port: port, Conn: model.ConnTelnet,
			OSType: "cisco-ios", Username: "u", Password: "p", CommandSet: "s", Enabled: true}
		set := &model.CommandSet{Name: "s", Commands: cmds, PauseOff: true}
		s := model.DefaultSettings()
		s.LogDir = t.TempDir()
		s.ConnectTimeout = 5
		s.CommandTimeout = 2
		res := New(testRegistry()).RunDevice(context.Background(), dev, set, s, s.LogDir, time.Now(), nil)
		return res, got()
	}
	res, got := run([]model.Command{{Text: "boot", Wait: "READY>"}, {Text: "{Ctrl+C}"}, {Text: "show clock"}})
	if !res.Success {
		t.Fatalf("run failed: %s %v", res.Error, got)
	}
	// In order, among the profile's own lines (terminal length 0, exit).
	want := []string{"boot", "^C", "show clock"}
	for _, l := range got {
		if len(want) > 0 && l == want[0] {
			want = want[1:]
		}
	}
	if len(want) > 0 {
		t.Errorf("device received %v, missing %v in order", got, want)
	}

	res, got = run([]model.Command{{Text: "{break}"}})
	if res.Success || res.Error == "" {
		t.Errorf("a serial break over Telnet should fail the device: %+v %v", res, got)
	}
	for _, l := range got {
		if l == "{break}" {
			t.Errorf("the token was typed as text: %v", got)
		}
	}
}
