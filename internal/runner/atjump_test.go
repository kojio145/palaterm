package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// A failed jump leaves the line on the jump host: the terminal window must
// not offer that shell as the device's screen (AtJumpHost).
func TestAtJumpHost(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"jump refused, wrapped like connect", fmt.Errorf("機器へのジャンプに失敗: %w", failedJump("192.0.2.21", "% Connection refused by remote host")), true},
		{"back at the hop's prompt after login", fmt.Errorf("機器へのジャンプに失敗: %w", &jumpError{"back at jump1#"}), true},
		// At the hop's login or password prompt, not its shell: AtJumpLogin.
		{"a hop's own login failed", &hopError{&hopLoginError{fmt.Errorf("踏み台1段目のログインに失敗: %w", errors.New("timeout"))}}, false},
		{"a later hop's login failed", &hopError{&hopLoginError{errors.New("踏み台2段目のログインに失敗")}}, false},
		{"device login failed", fmt.Errorf("login: %w", errors.New("timeout")), false},
		{"plain error", errors.New(errAuthRefused), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := AtJumpHost(c.err); got != c.want {
			t.Errorf("%s: AtJumpHost = %v, want %v", c.name, got, c.want)
		}
		var l *hopLoginError
		if got := AtJumpLogin(c.err); got != errors.As(c.err, &l) {
			t.Errorf("%s: AtJumpLogin = %v", c.name, got)
		}
	}
}

// A hop whose login fails leaves the line at its login prompt: the terminal
// window must say that, not "back on the jump host's shell".
func TestAtJumpLoginFromRejectedHop(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	go func() {
		dev.say("login: ")
		<-dev.sent // username
		dev.say("\r\nPassword: ")
		<-dev.sent // password
		dev.say("\r\nLogin incorrect\r\nlogin: ")
	}()
	bastions := []model.Bastion{{Host: "192.0.2.10", Method: model.BastionTelnet, Username: "ops", Password: "wrong"}}
	target := &model.Device{Name: "lab-sw1", Host: "192.0.2.21", Conn: model.ConnTelnet, Username: "ops"}
	_, err := (&Runner{}).traverseBastions(context.Background(), exp, bastions, target, 500*time.Millisecond)
	if err == nil {
		t.Fatal("a rejected hop login passed")
	}
	wrapped := &hopError{err} // as connect returns it
	if !AtJumpLogin(wrapped) || AtJumpHost(wrapped) {
		t.Fatalf("a hop's rejected login: AtJumpLogin=%v AtJumpHost=%v (%v)", AtJumpLogin(wrapped), AtJumpHost(wrapped), err)
	}
	if !strings.Contains(err.Error(), "踏み台1段目のログインに失敗") {
		t.Errorf("error %q does not name the hop", err)
	}
}

// The error a real refused jump returns is recognized end to end.
func TestAtJumpHostFromRefusedJump(t *testing.T) {
	dev, exp := atHop(t, "jump1#")
	defer exp.Close()
	done := make(chan error, 1)
	go func() {
		done <- jumpTo(context.Background(), exp, "", model.BastionTelnet, "192.0.2.21", "ops", 0, false, 5*time.Second)
	}()
	dev.next(t)
	dev.say("Trying 192.0.2.21 ...\r\n% Connection refused by remote host\r\n\r\njump1#")
	select {
	case err := <-done:
		if !AtJumpHost(fmt.Errorf("機器へのジャンプに失敗: %w", err)) {
			t.Fatalf("a refused jump should be at the jump host: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("jump did not return")
	}
}
