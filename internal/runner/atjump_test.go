package runner

import (
	"context"
	"errors"
	"fmt"
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
		{"a hop's own login failed", &hopError{fmt.Errorf("踏み台1段目のログインに失敗: %w", errors.New("timeout"))}, true},
		{"device login failed", fmt.Errorf("login: %w", errors.New("timeout")), false},
		{"plain error", errors.New(errAuthRefused), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := AtJumpHost(c.err); got != c.want {
			t.Errorf("%s: AtJumpHost = %v, want %v", c.name, got, c.want)
		}
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
