package main

import (
	"testing"

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
	a.SetHostKey(addr, fp)

	a2 := NewApp()
	a2.vaultPath = a.vaultPath
	if err := a2.Unlock("master"); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if got := a2.GetHostKey(addr); got != fp {
		t.Fatalf("pin after reopen = %q, want %q", got, fp)
	}
}

// Clearing a pin is how a legitimately replaced device is accepted again, so
// it must reach the vault too — otherwise the warning comes back on restart
// and the device is unreachable until the vault is deleted.
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
	for _, addr := range []string{"192.0.2.201:22", "192.0.2.202:22"} {
		if got := a2.GetHostKey(addr); got != "" {
			t.Errorf("%s still pinned after clear + reopen: %q", addr, got)
		}
	}
}
