package main

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// 一括変更 → 踏み台を設定: a first hop put on the selected devices at once
// (I M-2). Lab values only.
func u1App(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []model.Device{
		// r1 already has a chain of two hops.
		{Name: "lab-rt1", Group: "lab", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Password: "pw", Enabled: true,
			Bastions: []model.Bastion{
				{Host: "198.51.100.9", Method: model.BastionSSH, Username: "old", Password: "oldpw"},
				{Host: "198.51.100.10", Method: model.BastionSSH, Username: "hop2", Password: "hop2pw"},
			}},
		// r2 has none.
		{Name: "lab-sw1", Group: "lab", Host: "192.0.2.2", Conn: model.ConnSSH, OSType: "cisco-ios", Username: "ops", Password: "pw", Enabled: true},
	} {
		if err := a.SaveDevice(d); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func u1Hop() *model.Bastion {
	return &model.Bastion{Host: "jump1.example.net", Method: model.BastionSSH, Port: 2222, Username: "ops", Password: "jpw", AuthMethod: model.AuthPassword}
}

func TestReview1101U1_Bastion1Replace(t *testing.T) {
	a := u1App(t)
	ids := []string{a.inv.Devices[0].ID, a.inv.Devices[1].ID}
	rep, err := a.UpdateDevices(ids, DevicePatch{Bastion1: u1Hop(), Bastion1Mode: "replace"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Hop1Set != 2 || rep.Hop1Kept != 0 || rep.Devices != 2 {
		t.Errorf("report = %+v, want Hop1Set 2 / Hop1Kept 0", rep)
	}
	r1, r2 := a.inv.Devices[0], a.inv.Devices[1]
	if len(r1.Bastions) != 2 || r1.Bastions[0].Host != "jump1.example.net" || r1.Bastions[0].Port != 2222 || r1.Bastions[0].Password != "jpw" {
		t.Errorf("r1 hop 1 not replaced: %+v", r1.Bastions)
	}
	if r1.Bastions[1].Host != "198.51.100.10" || r1.Bastions[1].Password != "hop2pw" {
		t.Errorf("r1 hop 2 must stay: %+v", r1.Bastions)
	}
	if len(r2.Bastions) != 1 || r2.Bastions[0].Host != "jump1.example.net" || r2.Bastions[0].Username != "ops" {
		t.Errorf("r2 got no hop 1: %+v", r2.Bastions)
	}
}

func TestReview1101U1_Bastion1Fill(t *testing.T) {
	a := u1App(t)
	ids := []string{a.inv.Devices[0].ID, a.inv.Devices[1].ID}
	tunnel := "tunnel"
	rep, err := a.UpdateDevices(ids, DevicePatch{Bastion1: u1Hop(), Bastion1Mode: "fill", BastionMode: &tunnel})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Hop1Set != 1 || rep.Hop1Kept != 1 {
		t.Errorf("report = %+v, want Hop1Set 1 / Hop1Kept 1", rep)
	}
	r1, r2 := a.inv.Devices[0], a.inv.Devices[1]
	if r1.Bastions[0].Host != "198.51.100.9" || r1.Bastions[0].Password != "oldpw" {
		t.Errorf("fill must keep r1's own hop 1: %+v", r1.Bastions)
	}
	if len(r2.Bastions) != 1 || r2.Bastions[0].Host != "jump1.example.net" {
		t.Errorf("fill must give r2 the hop: %+v", r2.Bastions)
	}
	if r1.BastionMode != model.BastionViaTunnel || r2.BastionMode != model.BastionViaTunnel {
		t.Errorf("通り方 not applied: r1=%q r2=%q", r1.BastionMode, r2.BastionMode)
	}
}

// A hop with its credentials in the same patch: the hop goes in first, the
// hop-credential edit (the existing part of the patch) then applies to it.
func TestReview1101U1_Bastion1WithHopCredentials(t *testing.T) {
	a := u1App(t)
	ids := []string{a.inv.Devices[1].ID}
	hop, bpw := 1, "rotated"
	rep, err := a.UpdateDevices(ids, DevicePatch{Bastion1: u1Hop(), Bastion1Mode: "replace", BastionHop: &hop, BastionPassword: &bpw})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Hop1Set != 1 || rep.HopApplied != 1 || rep.HopMissing != 0 {
		t.Errorf("report = %+v", rep)
	}
	if got := a.inv.Devices[1].Bastions[0].Password; got != "rotated" {
		t.Errorf("hop password = %q, want rotated", got)
	}
}

// Refused whole, nothing changed: a bad mode, a hop without a host, an SSH
// hop without a user name, a Telnet hop behind an SSH tunnel, and a serial
// device (no jump host for a console cable) among the selected ones.
func TestReview1101U1_Bastion1Refused(t *testing.T) {
	a := u1App(t)
	if err := a.SaveDevice(model.Device{Name: "lab-con1", Group: "lab", Conn: model.ConnSerial, OSType: "cisco-ios", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	all := []string{a.inv.Devices[0].ID, a.inv.Devices[1].ID, a.inv.Devices[2].ID}
	two := all[:2]
	noHost, noUser, telnet := u1Hop(), u1Hop(), u1Hop()
	noHost.Host = " "
	noUser.Username = ""
	telnet.Method = model.BastionTelnet
	tunnel := "tunnel"
	cases := []struct {
		name string
		ids  []string
		p    DevicePatch
		want string
	}{
		{"mode", two, DevicePatch{Bastion1: u1Hop(), Bastion1Mode: "all"}, "入れ方"},
		{"host", two, DevicePatch{Bastion1: noHost, Bastion1Mode: "replace"}, "ホスト"},
		{"user", two, DevicePatch{Bastion1: noUser, Bastion1Mode: "replace"}, "ユーザー名"},
		{"tunnel+telnet", two, DevicePatch{Bastion1: telnet, Bastion1Mode: "replace", BastionMode: &tunnel}, "SSH トンネル"},
		{"serial", all, DevicePatch{Bastion1: u1Hop(), Bastion1Mode: "replace"}, "シリアル"},
		{"bad mode word", two, DevicePatch{Bastion1: u1Hop(), Bastion1Mode: "replace", BastionMode: u1Str("vpn")}, "通り方"},
	}
	for _, c := range cases {
		_, err := a.UpdateDevices(c.ids, c.p)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
	r1, r2 := a.inv.Devices[0], a.inv.Devices[1]
	if r1.Bastions[0].Host != "198.51.100.9" || len(r2.Bastions) != 0 || r1.BastionMode != model.BastionViaShell {
		t.Errorf("a refused patch changed a device: r1=%+v r2=%+v", r1.Bastions, r2.Bastions)
	}
}

// A patch without Bastion1 is untouched by the new part: the hop-credential
// edit still finds the hop it had, and a device without one is left alone.
func TestReview1101U1_Bastion1AbsentKeepsHopPatch(t *testing.T) {
	a := u1App(t)
	ids := []string{a.inv.Devices[0].ID, a.inv.Devices[1].ID}
	hop, bu := 1, "newuser"
	rep, err := a.UpdateDevices(ids, DevicePatch{BastionHop: &hop, BastionUsername: &bu})
	if err != nil {
		t.Fatal(err)
	}
	if rep.HopApplied != 1 || rep.HopMissing != 1 || rep.Hop1Set != 0 || rep.Hop1Kept != 0 {
		t.Errorf("report = %+v", rep)
	}
	if a.inv.Devices[0].Bastions[0].Username != "newuser" || len(a.inv.Devices[1].Bastions) != 0 {
		t.Errorf("hop credential patch broken: %+v / %+v", a.inv.Devices[0].Bastions, a.inv.Devices[1].Bastions)
	}
}

func u1Str(s string) *string { return &s }
