package main

import (
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// Trust-on-first-use is only worth anything if the pin outlives the process:
// a fingerprint that is forgotten on exit means every run is a first run, and
// a swapped host key is never noticed. The runner's side of this is covered
// against real hardware; this covers the half that has to reach the vault.
func TestHostKeyPinSurvivesAVaultReopen(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatalf("create vault: %v", err)
	}

	const (
		addr = "192.0.2.201:22"
		fp   = "ssh-rsa SHA256:j2Xs/U48fUBLWM0VTQQu9MxggjdhoLTEIXog59g60s0"
	)
	if got := a.GetHostKey(addr); got != "" {
		t.Fatalf("a fresh vault already pins %q", got)
	}
	// The pin is saved a moment later (persistSoon), batched with others
	// pinned meanwhile — not inside the SSH handshake.
	a.SetHostKey(addr, fp)
	time.Sleep(1500 * time.Millisecond)
	reopen := func() *App {
		a2 := NewApp()
		a2.vaultPath = a.vaultPath
		if err := a2.Unlock("master"); err != nil {
			t.Fatalf("unlock: %v", err)
		}
		return a2
	}
	if got := reopen().GetHostKey(addr); got != fp {
		t.Fatalf("pin after reopen = %q, want %q", got, fp)
	}

	// One still pending when the app exits (or locks) is written then.
	const addr2 = "192.0.2.202:22"
	a.SetHostKey(addr2, fp)
	a.shutdown(nil)
	if got := reopen().GetHostKey(addr2); got != fp {
		t.Fatalf("pin pending at exit = %q, want %q", got, fp)
	}
}

// Clearing a pin is how a legitimately replaced device is accepted again, so
// it must reach the vault too — otherwise the warning comes back on restart
// and the device is unreachable until the vault is deleted. The jump host's
// pin is not the device's and stays: other devices share that hop.
func TestClearHostKeysReachesTheVault(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatalf("create vault: %v", err)
	}

	dev := model.Device{
		Name: "IX-A", Host: "192.0.2.201", Port: 22, Conn: model.ConnSSH,
		OSType: "nec-ix", Username: "u", Password: "p", Enabled: true,
		Bastions: []model.Bastion{{
			Host: "192.0.2.202", Port: 22, Method: model.BastionSSH, Username: "u",
		}},
	}
	if err := a.SaveDevice(dev); err != nil {
		t.Fatalf("save device: %v", err)
	}
	a.SetHostKey("192.0.2.201:22", "ssh-rsa SHA256:device")
	a.SetHostKey("192.0.2.202:22", "ssh-rsa SHA256:hop")

	if err := a.ClearHostKeys("IX-A"); err != nil {
		t.Fatalf("clear host keys: %v", err)
	}

	a2 := NewApp()
	a2.vaultPath = a.vaultPath
	if err := a2.Unlock("master"); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if got := a2.GetHostKey("192.0.2.201:22"); got != "" {
		t.Errorf("device still pinned after clear + reopen: %q", got)
	}
	if got := a2.GetHostKey("192.0.2.202:22"); got != "ssh-rsa SHA256:hop" {
		t.Errorf("jump host pin = %q, want it kept", got)
	}
}

// A batch that pins a key while the vault is locked (ロック mid-run) keeps
// it, and the vault gets it at the next unlock.
func TestPinWhileLockedIsSavedAtUnlock(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	keys := &runHostKeys{mem: newMemHostKeys(nil), app: a}
	a.Lock()
	keys.SetHostKey("192.0.2.9:22", "ssh-ed25519 SHA256:x")
	if keys.GetHostKey("192.0.2.9:22") == "" {
		t.Fatal("the run's own copy lost the pin")
	}
	if err := a.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	if got := a.GetHostKey("192.0.2.9:22"); got != "ssh-ed25519 SHA256:x" {
		t.Errorf("pin after unlock = %q", got)
	}
}

// A device through the own jump host is pinned under the chain as connected
// (the own hop in front): ClearHostKeys drops that name too — and leaves the
// own jump host's pin, which every device behind it relies on.
func TestClearHostKeysThroughMyBastion(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatalf("create vault: %v", err)
	}
	if err := a.SaveMyBastions([]model.Bastion{{Host: "jump1.example", Method: model.BastionSSH, Username: "ops"}}); err != nil {
		t.Fatal(err)
	}
	dev := model.Device{Name: "fw1", Host: "192.0.2.10", Port: 22, Conn: model.ConnSSH, OSType: "nec-ix",
		Username: "u", Password: "p", Enabled: true, UseMyBastion: true, BastionMode: model.BastionViaTunnel}
	if err := a.SaveDevice(dev); err != nil {
		t.Fatalf("save device: %v", err)
	}
	a.SetHostKey("jump1.example:22", "ssh-ed25519 SHA256:hop")
	a.SetHostKey("jump1.example:22>192.0.2.10:22", "ssh-ed25519 SHA256:dev")
	if err := a.ClearHostKeys("fw1"); err != nil {
		t.Fatalf("clear host keys: %v", err)
	}
	if k := a.GetHostKey("jump1.example:22>192.0.2.10:22"); k != "" {
		t.Errorf("device key through the own jump host still pinned: %q", k)
	}
	if k := a.GetHostKey("jump1.example:22"); k != "ssh-ed25519 SHA256:hop" {
		t.Errorf("own jump host pin = %q, want it kept", k)
	}
}

// 踏み台を使わない (direct) pins the device under its bare address; a
// device that normally tunnels through its own jump hosts loses that pin
// too when its keys are cleared.
func TestClearHostKeysDropsDirectPin(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatalf("create vault: %v", err)
	}
	dev := model.Device{Name: "fw1", Host: "192.0.2.10", Port: 22, Conn: model.ConnSSH, OSType: "nec-ix",
		Username: "u", Password: "p", Enabled: true, BastionMode: model.BastionViaTunnel,
		Bastions: []model.Bastion{{Host: "192.0.2.1", Port: 22, Method: model.BastionSSH, Username: "j"}}}
	if err := a.SaveDevice(dev); err != nil {
		t.Fatalf("save device: %v", err)
	}
	a.SetHostKey("192.0.2.1:22>192.0.2.10:22", "ssh-ed25519 SHA256:viahop")
	a.SetHostKey("192.0.2.10:22", "ssh-ed25519 SHA256:direct")
	if err := a.ClearHostKeys("fw1"); err != nil {
		t.Fatalf("clear host keys: %v", err)
	}
	for _, k := range []string{"192.0.2.1:22>192.0.2.10:22", "192.0.2.10:22"} {
		if v := a.GetHostKey(k); v != "" {
			t.Errorf("%s still pinned: %q", k, v)
		}
	}
}

// Deleting a device takes its GUI certificate pin with it.
func TestDeleteDeviceDropsGUICert(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatalf("create vault: %v", err)
	}
	for _, n := range []string{"g1", "g2"} {
		if err := a.SaveDevice(model.Device{Name: n, Host: "192.0.2.5", Conn: model.ConnSSH, OSType: "nec-ix", GUIOn: true, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	inv := a.GetInventory()
	for _, d := range inv.Devices {
		a.setGUICert(d.GUICertKey(), "ab")
	}
	g1 := inv.Devices[0]
	if err := a.DeleteDevice(g1.ID); err != nil {
		t.Fatal(err)
	}
	after := a.GetInventory()
	if _, ok := after.KnownGUICerts[g1.GUICertKey()]; ok {
		t.Error("deleted device's GUI pin still there")
	}
	if _, ok := after.KnownGUICerts[inv.Devices[1].GUICertKey()]; !ok {
		t.Error("the other device's pin (same address, other ID) was dropped")
	}
}
