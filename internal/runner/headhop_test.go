package runner

import (
	"errors"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/session"
)

// A host-key refusal of the first jump host (dialed by this PC, commands
// typed on it) is named as the hop's, like a tunnel's hops are — the run tab
// then offers to forget the hop's key, not the device's (which 「鍵を許可して
// 再実行」 dropped, uselessly, as often as it was pressed).
func TestHeadHopErrorNamesTheJumpHost(t *testing.T) {
	mismatch := &session.HostKeyMismatchError{Name: "192.0.2.10:22", Got: "ssh-ed25519 SHA256:a", Stored: "ecdsa-sha2-nistp256 SHA256:b"}
	hop := model.Bastion{Host: "192.0.2.10", Method: model.BastionSSH, Username: "ops"}
	dev := &model.Device{Name: "sw1", Host: "192.0.2.21", Conn: model.ConnSSH, Bastions: []model.Bastion{hop}}

	err := headHopError(mismatch, dev, dev.ActiveBastions())
	if !strings.HasPrefix(err.Error(), "踏み台1段目（192.0.2.10）: ホストキーが前回接続時と異なります（192.0.2.10:22") {
		t.Fatalf("not named as the hop's: %v", err)
	}
	if session.MismatchedHostKey(err) != "192.0.2.10:22" {
		t.Fatalf("the refused name must still be readable: %v", err)
	}
	var m *session.HostKeyMismatchError
	if !errors.As(err, &m) {
		t.Fatal("the refusal must stay wrapped")
	}

	// Through a tunnel the hop is already named by the tunnel: untouched.
	tun := *dev
	tun.BastionMode = model.BastionViaTunnel
	if got := headHopError(mismatch, &tun, tun.ActiveBastions()); got != mismatch {
		t.Errorf("tunnel: %v", got)
	}
	// Direct: the device's own key, the device's button.
	direct := &model.Device{Name: "sw2", Host: "192.0.2.22", Conn: model.ConnSSH}
	if got := headHopError(mismatch, direct, nil); got != mismatch {
		t.Errorf("direct: %v", got)
	}
	// Any other failure of the hop is left to dialHint and the raw error.
	other := errors.New("dial tcp 192.0.2.10:22: connection refused")
	if got := headHopError(other, dev, dev.ActiveBastions()); got != other {
		t.Errorf("other error: %v", got)
	}
}
