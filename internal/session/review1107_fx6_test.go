package session

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// F-A-4 (review 2026-11-07): a failed dial reads "理由（ssh dial addr:
// raw）" — the reason first, the raw error kept and still wrapped; one with
// no reason of its own stays "ssh dial addr: raw".
func TestSSHDialErrorForm(t *testing.T) {
	addr := "192.0.2.11:22"
	err := sshDialError(addr, errors.New("ssh: handshake failed: EOF"), "ops", model.AuthPassword, DialOpts{})
	if s := err.Error(); !strings.HasPrefix(s, HintHandshakeDropped+"（ssh dial 192.0.2.11:22: ssh: handshake failed: EOF）") {
		t.Errorf("EOF: %q", s)
	}
	err = sshDialError(addr, errors.New("ssh: handshake failed: read tcp 192.0.2.9:5000->192.0.2.11:22: i/o timeout"), "ops", model.AuthPassword, DialOpts{})
	if s := err.Error(); !strings.HasPrefix(s, HintHandshakeTimedOut+"（ssh dial ") {
		t.Errorf("handshake timeout: %q", s)
	}
	err = sshDialError(addr, errors.New("ssh: handshake failed: read tcp 192.0.2.9:5000->192.0.2.11:22: wsarecv: An existing connection was forcibly closed by the remote host."), "ops", model.AuthPassword, DialOpts{})
	if s := err.Error(); !strings.HasPrefix(s, HintHandshakeDropped) {
		t.Errorf("forcibly closed: %q", s)
	}
	// The old-algorithm hint, in the same form.
	err = sshDialError(addr, errors.New("ssh: handshake failed: ssh: no common algorithm for key exchange; client offered: [a], server offered: [b]"), "ops", model.AuthPassword, DialOpts{})
	if s := err.Error(); !strings.HasPrefix(s, "機器が新しい暗号方式") || !strings.Contains(s, "（ssh dial 192.0.2.11:22: ssh: handshake failed: ssh: no common algorithm") {
		t.Errorf("algorithm: %q", s)
	}
	// No reason: the raw form, for the runner's dialHint to read.
	err = sshDialError(addr, errors.New("dial tcp 192.0.2.11:22: i/o timeout"), "ops", model.AuthPassword, DialOpts{})
	if s := err.Error(); s != "ssh dial 192.0.2.11:22: dial tcp 192.0.2.11:22: i/o timeout" {
		t.Errorf("plain: %q", s)
	}
	// Wrapped: a host-key mismatch is still found through it.
	store := &mapStore{m: map[string]string{}}
	cb := VerifyHostKey(store)
	if err := cb(addr, nil, genKey(t)); err != nil {
		t.Fatal(err)
	}
	mm := cb(addr, nil, genKey(t))
	wrapped := sshDialError(addr, fmt.Errorf("ssh: handshake failed: %w", mm), "ops", model.AuthPassword, DialOpts{})
	if got := MismatchedHostKey(wrapped); got != addr {
		t.Errorf("MismatchedHostKey through sshDialError = %q", got)
	}
}
