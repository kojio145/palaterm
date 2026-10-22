package main

import "testing"

// 許可して再接続 on a replaced device: the reconnect reads the vault again
// before the main window has dropped the stale pin from it. The window must
// leave that pin out anyway, or the first 許可 does nothing.
func TestSeedHostKeysLeavesOutAReplacedPin(t *testing.T) {
	const (
		dev   = "192.0.2.10:22"
		other = "192.0.2.11:22"
		stale = "ssh-ed25519 SHA256:staleStaleStaleStaleStaleStaleStaleStaleSta"
		fresh = "ssh-ed25519 SHA256:freshFreshFreshFreshFreshFreshFreshFreshFre"
		kept  = "ssh-ed25519 SHA256:keptKeptKeptKeptKeptKeptKeptKeptKeptKeptKep"
	)
	replaced := map[string]string{dev: stale}

	// The vault still holds the stale pin (the main window has not saved yet).
	hk := seedHostKeys(map[string]string{dev: stale, other: kept}, replaced)
	if got := hk.GetHostKey(dev); got != "" {
		t.Fatalf("stale pin came back from the vault: %q", got)
	}
	if got := hk.GetHostKey(other); got != kept {
		t.Fatalf("an unrelated pin was dropped: %q", got)
	}
	if replaced[dev] != stale {
		t.Fatalf("the replaced pin was forgotten while the vault still holds it")
	}

	// The main window has dropped it and the new key was pinned meanwhile:
	// the new pin is used, and the window stops leaving the address out, so
	// a later change of key is caught again.
	hk = seedHostKeys(map[string]string{dev: fresh}, replaced)
	if got := hk.GetHostKey(dev); got != fresh {
		t.Fatalf("pin = %q, want the new key", got)
	}
	if _, ok := replaced[dev]; ok {
		t.Fatalf("replaced still lists %s after the vault moved on", dev)
	}

	// Nothing replaced: the vault's pins as they are.
	hk = seedHostKeys(map[string]string{dev: stale}, nil)
	if got := hk.GetHostKey(dev); got != stale {
		t.Fatalf("pin = %q with nothing replaced", got)
	}
}
