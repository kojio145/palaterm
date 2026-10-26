package runner

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// The field case of 2026-10-26: the second hop is entered with a key kept on
// the first hop, whose ssh asks "Enter passphrase for key …". Every value is
// a lab one (192.0.2.x, ops, jump1).

// With the second hop on 公開鍵 and its 秘密鍵のパスフレーズ set, the jump
// answers the first hop's ssh with that passphrase, the hop's shell counts as
// in (no login prompt follows a key login), and the device is jumped to.
func TestSecondHopKeyPassphraseAnswered(t *testing.T) {
	dev, exp, done := twoHops(t, model.Bastion{Host: "192.0.2.11", Method: model.BastionSSH, Username: "ops", AuthMethod: model.AuthPublicKey, KeyPassphrase: "hop-key-pass"})
	defer exp.Close()
	if got := strings.TrimSpace(dev.next(t)); got != "ssh ops@192.0.2.11" {
		t.Fatalf("jump command %q", got)
	}
	dev.say("Enter passphrase for key '/home/ops/.ssh/id_rsa': ")
	if got := strings.TrimSpace(dev.next(t)); got != "hop-key-pass" {
		t.Fatalf("passphrase answer %q", got)
	}
	dev.say("\r\nLast login: Mon Oct 26 09:00:00 2026 from 192.0.2.9\r\n[ops@jump2 ~]$ ")
	if got := dev.next(t); !strings.HasPrefix(got, "telnet 192.0.2.21") {
		t.Fatalf("sent %q after the hop's shell prompt, want the jump to the device", got)
	}
	dev.say("Trying 192.0.2.21...\r\nConnected to 192.0.2.21.\r\n\r\nlogin: ")
	if err := hopResult(t, done); err != nil {
		t.Fatalf("traverse: %v", err)
	}
}

// The same hop left on パスワード (as the device form left it while it hid
// the field): the passphrase is not sent, and the error names the hop and
// the setting to change.
func TestSecondHopKeyPassphraseNeedsPublicKey(t *testing.T) {
	dev, exp, done := twoHops(t, model.Bastion{Host: "192.0.2.11", Method: model.BastionSSH, Username: "ops", Password: "pw", KeyPassphrase: "hop-key-pass"})
	defer exp.Close()
	dev.next(t)
	dev.say("Enter passphrase for key '/home/ops/.ssh/id_rsa': ")
	err := hopResult(t, done)
	if err == nil || !strings.Contains(err.Error(), "踏み台2段目へのジャンプに失敗") || !strings.Contains(err.Error(), "「公開鍵」ではないため送りませんでした") {
		t.Fatalf("err %v", err)
	}
}
