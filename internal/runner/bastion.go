package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// Prompts commonly seen while logging into / hopping through jump hosts.
const (
	reLogin     = `(?i)login:|username:`
	rePassword  = `(?i)password:`
	reYesNo     = `(?i)\(yes/no`
	reShellDone = `[>#$%\]]\s*$` // a generic shell prompt after landing on a hop
)

// traverseBastions walks the jump-host chain and then jumps to the device,
// leaving the stream sitting at the device's pre-login banner so the device's
// OS profile can drive the final login.
//
// hop[0] transport is already open (SSH authed at transport, or raw Telnet).
// For each subsequent hop, and finally the device, it issues an ssh/telnet
// command from the current shell and answers the intermediate login prompts.
func (r *Runner) traverseBastions(ctx context.Context, exp *expecter, bastions []model.Bastion, dev *model.Device, timeout time.Duration) error {
	// Log into the first hop if it was reached by Telnet (SSH already authed).
	if bastions[0].Method == model.BastionTelnet {
		if err := loginHop(ctx, exp, bastions[0], timeout); err != nil {
			return fmt.Errorf("踏み台1段目のログインに失敗: %w", err)
		}
	} else {
		// Wait until the first SSH bastion's shell prompt appears.
		_ = exp.Expect(ctx, reShellDone, timeout)
	}

	// Hop from each bastion to the next. The jump command is typed on the
	// PREVIOUS hop's shell, so that hop's JumpCommand template applies.
	for i := 1; i < len(bastions); i++ {
		next := bastions[i]
		if err := jumpTo(ctx, exp, bastions[i-1].JumpCommand, next.Method, next.Host, next.Username, next.Port, timeout); err != nil {
			return fmt.Errorf("踏み台%d段目へのジャンプに失敗: %w", i+1, err)
		}
		if err := loginHop(ctx, exp, next, timeout); err != nil {
			return fmt.Errorf("踏み台%d段目のログインに失敗: %w", i+1, err)
		}
	}

	// Final jump from the last bastion to the device. The device's OS profile
	// handles its own login prompts, so we only issue the jump command (and
	// accept an SSH host-key prompt if it appears).
	method := model.BastionTelnet
	if dev.Conn == model.ConnSSH {
		method = model.BastionSSH
	}
	last := bastions[len(bastions)-1]
	if err := jumpTo(ctx, exp, last.JumpCommand, method, dev.Host, dev.Username, dev.Port, timeout); err != nil {
		return fmt.Errorf("機器へのジャンプに失敗: %w", err)
	}
	return nil
}

// jumpTo issues an ssh/telnet command toward host and clears an SSH host-key
// confirmation if the far end asks for one. A non-empty template overrides the
// standard command; {user} {host} {port} are substituted (port 0 => "").
func jumpTo(ctx context.Context, exp *expecter, template string, method model.BastionMethod, host, user string, port int, timeout time.Duration) error {
	exp.Drain()
	if err := exp.Send(buildJumpCommand(template, method, host, user, port)); err != nil {
		return err
	}
	// Accept a first-connection host-key prompt if it shows up quickly.
	if err := exp.Expect(ctx, reYesNo, 2*time.Second); err == nil {
		_ = exp.Send("yes")
	}
	return nil
}

// buildJumpCommand renders the command typed on a hop's shell to reach the
// next hop. A non-empty template wins, with {user} {host} {port} substituted
// (port 0 => ""); otherwise the standard ssh/telnet form for method is used.
func buildJumpCommand(template string, method model.BastionMethod, host, user string, port int) string {
	if template != "" {
		p := ""
		if port > 0 {
			p = fmt.Sprintf("%d", port)
		}
		return strings.NewReplacer("{user}", user, "{host}", host, "{port}", p).Replace(template)
	}
	if method == model.BastionSSH {
		if user != "" {
			return fmt.Sprintf("ssh %s@%s", user, host)
		}
		return fmt.Sprintf("ssh %s", host)
	}
	return fmt.Sprintf("telnet %s", host)
}

// loginHop answers the login/password prompts for a bastion we just reached.
//
// Reaching the hop's shell prompt afterwards is required, not optional: a hop
// that rejects the credentials prints its login prompt again, and continuing
// regardless types the jump command in as a username. The run then fails much
// later, on the device's prompt, with a timeout that says nothing about the
// hop whose password was actually wrong.
func loginHop(ctx context.Context, exp *expecter, b model.Bastion, timeout time.Duration) error {
	if b.Username != "" {
		if err := exp.Expect(ctx, reLogin, timeout); err == nil {
			_ = exp.Send(b.Username)
		}
	}
	if err := exp.Expect(ctx, rePassword, timeout); err == nil {
		_ = exp.Send(b.Password)
	}
	// Settle on the bastion's shell prompt before continuing. A login prompt
	// coming back instead is the hop saying no.
	if err := exp.Expect(ctx, reShellDone, timeout); err != nil {
		if exp.Seen(reLogin) {
			return fmt.Errorf("%s がログインを拒否しました（ユーザー名・パスワードを確認してください）", b.Host)
		}
		return fmt.Errorf("%s のログイン後にプロンプトが出ませんでした: %w", b.Host, err)
	}
	return nil
}
