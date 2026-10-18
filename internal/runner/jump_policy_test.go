package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// Without レガシー暗号を許可 on the far end, a refused negotiation is an
// error that names the fix — not a silent retry with the weak algorithms.
func TestJumpDoesNotRetryWithoutLegacy(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	done := make(chan error, 1)
	go func() {
		done <- jumpTo(context.Background(), exp, "", model.BastionSSH, "192.0.2.18", "ops", 0, false, 5*time.Second)
	}()
	dev.next(t)
	dev.say("Unable to negotiate with 192.0.2.18 port 22: no matching host key type found. Their offer: ssh-dss\r\n[ops@jump1 ~]$ ")
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "レガシー暗号を許可") || !strings.Contains(err.Error(), "ssh-dss") {
			t.Fatalf("want an error naming the setting and the offer, got %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("jumpTo did not return")
	}
	// (The retry path's message has no "レガシー暗号を許可" in it, and the
	// scripted device would still be waiting for the second ssh line.)
}

// A far end on password authentication that asks for a key passphrase is
// not the hop's ssh: the passphrase of the key kept on the hop stays put.
func TestJumpKeepsPassphraseFromPasswordTargets(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	done := make(chan error, 1)
	go func() {
		_, err := jumpToKey(context.Background(), exp, "", model.BastionSSH, "192.0.2.209", "admin", 0, model.AuthPassword, "kp-secret", false, 5*time.Second)
		done <- err
	}()
	dev.next(t)
	dev.say("\r\nEnter passphrase for key '/home/ops/.ssh/id_rsa': ")
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "公開鍵") {
			t.Fatalf("want a refusal naming the auth method, got %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("jump did not return")
	}
	if strings.Contains(exp.Transcript(), "kp-secret") {
		t.Error("the passphrase was typed")
	}
}

// A set that types a password into a command line is logged masked
// whatever the setting says.
func TestSecretCommandsForceMask(t *testing.T) {
	set := &model.CommandSet{Commands: []model.Command{{Text: "show clock"}, {Text: "{enable}"}}}
	if len(set.SecretCommands()) != 1 || set.SecretCommands()[0] != "{enable}" {
		t.Errorf("SecretCommands = %v", set.SecretCommands())
	}
	var none *model.CommandSet
	if len(none.SecretCommands()) != 0 {
		t.Error("nil set")
	}
	if model.SendsSecret("ping {user}.lab") {
		t.Error("{user} alone is no secret")
	}
}
