package runner

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A set that fills in values (変数) goes out as the device's filled-in
// copy (Options.DeviceSets); a device with none is not connected to, so a
// {{name}} line never reaches a device as it is.
func TestBatchSendsFilledSetAndSkipsUnfilled(t *testing.T) {
	addr, cmds, stop := fakeLinux(t)
	defer stop()
	host, portS, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portS, "%d", &port)
	set := model.CommandSet{Name: "fill", Commands: []model.Command{{Text: "echo hop {{nexthop}}"}}}
	dev := func(name string) model.Device {
		return model.Device{Name: name, Host: host, Port: port, Conn: model.ConnTelnet, OSType: "linux",
			Username: "ops", Password: "pw", CommandSet: "fill", Enabled: true}
	}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.ConnectTimeout = 5
	settings.CommandTimeout = 6
	inv := &model.Inventory{Settings: settings, CommandSets: []model.CommandSet{set},
		Devices: []model.Device{dev("lab-rt1"), dev("lab-rt2")}}
	filled := set
	filled.Commands = []model.Command{{Text: "echo hop 192.0.2.1"}}
	res, _ := New(testRegistry()).RunBatchOpts(context.Background(), inv,
		Options{DeviceSets: map[string]*model.CommandSet{"lab-rt1": &filled}}, nil)
	by := map[string]DeviceResult{}
	for _, r := range res {
		by[r.Device] = r
	}
	if !by["lab-rt1"].Success {
		t.Fatalf("lab-rt1: %s", by["lab-rt1"].Error)
	}
	if r := by["lab-rt2"]; r.Success || r.Error != FillMissingMsg || r.LogPath != "" {
		t.Fatalf("lab-rt2 must be left out unconnected: %+v", r)
	}
	got := strings.Join(cmds(), " | ")
	if !strings.Contains(got, "echo hop 192.0.2.1") || strings.Contains(got, "{{") {
		t.Fatalf("sent: %s", got)
	}
	if n := strings.Count(got, "ops | pw"); n != 1 {
		t.Fatalf("one login expected (lab-rt1 only), got %d: %s", n, got)
	}
}
