package bundle

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/csvio"
	"github.com/kojio145/palaterm/internal/model"
)

// A row that keeps the existing device's password but points the device at
// another address is listed (review 2026-11-23); the same address (port
// written out or left to the default) is not.
func TestApplyCredsNewHost(t *testing.T) {
	inv := sampleInventory()
	c := Contents{Devices: []model.Device{
		{Name: "a-rt1", Host: "192.0.2.66", Conn: model.ConnSSH, Group: "A社", Enabled: true},
		{Name: "b-sw1", Host: "192.0.2.3", Port: 23, Conn: model.ConnTelnet, Group: "B社", Enabled: true},
	}}
	inv.Devices[2].Password = "p3"
	s := Preview(inv, c, Options{})
	if strings.Join(s.CredsKept, ",") != "A社 / a-rt1,B社 / b-sw1" {
		t.Fatalf("kept=%v", s.CredsKept)
	}
	if len(s.CredsNewHost) != 1 || s.CredsNewHost[0] != "A社 / a-rt1: ssh 192.0.2.1 → ssh 192.0.2.66" {
		t.Fatalf("newHost=%q", s.CredsNewHost)
	}
	// Credentials are still taken over exactly as before.
	Apply(inv, c, Options{})
	if inv.Devices[0].Password != "p" || inv.Devices[0].Host != "192.0.2.66" {
		t.Fatalf("device: %+v", inv.Devices[0])
	}
}

// A command starting with "#" and the set's 全行の待ち方 survive the file
// format, and the "#" command's wait stays on it (review 2026-11-23).
func TestCommandSetHashRoundTrip(t *testing.T) {
	in := model.CommandSet{
		Default: &model.WaitDefault{Wait: "#", PauseSec: 2, SerialSec: 3},
		Commands: []model.Command{
			{Text: "#show run", PauseSec: 1, SerialSec: 1, Wait: "sw-a#"},
			{Text: "show ver", PauseSec: 4, SerialSec: 5},
		},
	}
	out, err := ParseCommandSet(FormatCommandSet(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Commands) != 2 || out.Commands[0].Text != "#show run" || out.Commands[0].Wait != "sw-a#" ||
		out.Commands[1].Text != "show ver" || out.Commands[1].Wait != "" || out.Commands[1].PauseSec != 4 {
		t.Fatalf("commands = %+v", out.Commands)
	}
	if out.Default == nil || *out.Default != *in.Default {
		t.Fatalf("default = %+v", out.Default)
	}
	// An ordinary set is written exactly as before.
	plain := FormatCommandSet(model.CommandSet{Commands: []model.Command{{Text: "show ver", PauseSec: 1, SerialSec: 1}}})
	if plain != csvio.BOM+"show ver,1,1\r\n" {
		t.Fatalf("plain = %q", plain)
	}
}
