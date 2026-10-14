package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// A jump host case: hop → ssh admin@192.0.2.209 asks to trust
// the host key, then for the passphrase of the key kept on the hop
// (/home/…/.ssh/id_rsa). The jump answers "yes", then the passphrase from
// the device's 秘密鍵のパスフレーズ, and reports the key login.
func TestJumpAnswersHopKeyPassphrase(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	type res struct {
		keyed bool
		err   error
	}
	done := make(chan res, 1)
	go func() {
		k, err := jumpToKey(context.Background(), exp, "", model.BastionSSH, "192.0.2.209", "admin", 0, "kp-secret", 5*time.Second)
		done <- res{k, err}
	}()
	if got := strings.TrimSpace(dev.next(t)); got != "ssh admin@192.0.2.209" {
		t.Fatalf("jump command %q", got)
	}
	dev.say("The authenticity of host '192.0.2.209' can't be established.\r\nAre you sure you want to continue connecting (yes/no/[fingerprint])? ")
	if got := strings.TrimSpace(dev.next(t)); got != "yes" {
		t.Fatalf("host key answer %q", got)
	}
	dev.say("\r\nWarning: Permanently added '192.0.2.209' (RSA) to the list of known hosts.\r\nEnter passphrase for key '/home/ops/.ssh/id_rsa': ")
	if got := strings.TrimSpace(dev.next(t)); got != "kp-secret" {
		t.Fatalf("passphrase answer %q", got)
	}
	dev.say("\r\nLast login: Wed Oct 14 11:51:32 2026\r\n[admin@srv1 ~]$ ")
	select {
	case r := <-done:
		if r.err != nil || !r.keyed {
			t.Fatalf("keyed=%v err=%v", r.keyed, r.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("jump did not return")
	}
}

// A wrong passphrase (asked again) and an empty 秘密鍵のパスフレーズ end the
// jump with a message that says which, instead of a login timeout.
func TestJumpPassphraseWrongOrMissing(t *testing.T) {
	for _, c := range []struct {
		pass, want string
	}{
		{"wrong", "パスフレーズが違う"},
		{"", "「秘密鍵のパスフレーズ」が空"},
	} {
		dev, sess := newScriptedDevice()
		exp := newExpecter(sess)
		done := make(chan error, 1)
		go func() {
			_, err := jumpToKey(context.Background(), exp, "", model.BastionSSH, "192.0.2.209", "admin", 0, c.pass, 5*time.Second)
			done <- err
		}()
		dev.next(t)
		dev.say("Enter passphrase for key '/home/u/.ssh/id_rsa': ")
		if c.pass != "" {
			dev.next(t)
			dev.say("\r\nEnter passphrase for key '/home/u/.ssh/id_rsa': ")
		}
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("pass %q: err %v", c.pass, err)
			}
		case <-time.After(8 * time.Second):
			t.Fatalf("pass %q: jump did not return", c.pass)
		}
		exp.Close()
	}
}
