package runner

import (
	"context"
	"fmt"
	"regexp"
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

// OpenSSH on a jump host refuses an old device's algorithms by default and
// says so in one line, naming what the device offered:
//
//	Unable to negotiate with 10.0.0.1 port 22: no matching host key type found. Their offer: ssh-dss
//
// That happened in the field on an ALAXALA reached through a Linux jump
// host (2026-10-11): the switch has a DSA host key only, the hop's OpenSSH
// dropped ssh-dss years ago, the ssh command died at once and the run sat
// waiting for a login prompt that was never coming. The offer is exactly
// the information needed to retry, so jumpTo does: it re-issues the ssh
// command with the offered algorithms allowed for that one category
// (-o HostKeyAlgorithms=+ssh-dss, KexAlgorithms, Ciphers, MACs) and keeps
// going until the negotiation passes or nothing new is learned.
const reNegotiate = `Unable to negotiate`

var reNegotiateDetail = regexp.MustCompile(`no matching (host key type|key exchange method|cipher|MAC) found\. Their offer: ([^\s]+)`)

var negotiateOption = map[string]string{
	"host key type":       "HostKeyAlgorithms",
	"key exchange method": "KexAlgorithms",
	"cipher":              "Ciphers",
	"MAC":                 "MACs",
}

// maxNegotiateRetries bounds the retries: one per category at most.
const maxNegotiateRetries = 4

// jumpTo issues an ssh/telnet command toward host and clears an SSH host-key
// confirmation if the far end asks for one. A non-empty template overrides the
// standard command; {user} {host} {port} are substituted (port 0 => "").
// An OpenSSH "Unable to negotiate" refusal is answered by retrying with the
// device's offered algorithms allowed (see reNegotiate).
func jumpTo(ctx context.Context, exp *expecter, template string, method model.BastionMethod, host, user string, port int, timeout time.Duration) error {
	cmd := buildJumpCommand(template, method, host, user, port)
	var extra []string
	for attempt := 0; ; attempt++ {
		exp.Drain()
		if err := exp.Send(withSSHOptions(cmd, extra)); err != nil {
			return err
		}
		// A first-connection host-key prompt, or OpenSSH refusing the far
		// end's algorithms, both show up within moments of the command. The
		// far end's own login prompt showing up instead means the jump is
		// through: return at once and leave that prompt unconsumed for the
		// OS profile (or loginHop) to answer.
		seen := ""
		deadline := time.Now().Add(3 * time.Second)
		for seen == "" && time.Now().Before(deadline) {
			switch {
			case exp.Seen(reNegotiate):
				seen = "negotiate"
			case exp.Seen(reYesNo):
				seen = "yesno"
			case exp.Seen(reLogin) || exp.Seen(rePassword):
				seen = "login"
			default:
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(50 * time.Millisecond):
				}
			}
		}
		if seen == "" || seen == "login" {
			return nil
		}
		if seen == "yesno" {
			_ = exp.Expect(ctx, reYesNo, time.Second)
			_ = exp.Send("yes")
			return nil
		}
		// Let the line finish so the offer is readable, then land back on the
		// hop's shell before typing again.
		_ = exp.Expect(ctx, `Their offer: [^\s]+`, 2*time.Second)
		opt := negotiateFix(exp.Transcript())
		_ = exp.Expect(ctx, reShellDone, 3*time.Second)
		if opt == "" || attempt >= maxNegotiateRetries || containsStr(extra, opt) {
			return fmt.Errorf("%s の ssh が %s と暗号方式を合意できません（%s）。機器が古い方式しか持たない場合は踏み台のジャンプコマンド欄で -o オプションを指定してください", shellName(exp), host, lastNegotiateLine(exp.Transcript()))
		}
		extra = append(extra, opt)
	}
}

// negotiateFix turns the newest "Unable to negotiate" line of a transcript
// into the ssh -o option that allows what the device offered ("" if the
// line is not one OpenSSH wrote).
func negotiateFix(transcript string) string {
	m := reNegotiateDetail.FindAllStringSubmatch(transcript, -1)
	if len(m) == 0 {
		return ""
	}
	last := m[len(m)-1]
	return negotiateOption[last[1]] + "=+" + last[2]
}

// withSSHOptions puts "-o k=v" options right after the ssh word of cmd, so
// both the standard "ssh user@host" and a template like "ssh -p 2222 …" keep
// working. A command that is not ssh at all is left alone.
func withSSHOptions(cmd string, opts []string) string {
	if len(opts) == 0 || !strings.HasPrefix(cmd, "ssh ") {
		return cmd
	}
	var b strings.Builder
	b.WriteString("ssh")
	for _, o := range opts {
		b.WriteString(" -o ")
		b.WriteString(o)
	}
	b.WriteString(cmd[len("ssh"):])
	return b.String()
}

func lastNegotiateLine(transcript string) string {
	i := strings.LastIndex(transcript, "Unable to negotiate")
	if i < 0 {
		return ""
	}
	line := transcript[i:]
	if j := strings.IndexAny(line, "\r\n"); j >= 0 {
		line = line[:j]
	}
	return strings.TrimSpace(line)
}

// shellName names the hop in an error: the last "user@host" prompt seen, or
// just 踏み台 when the prompt gives nothing away.
func shellName(exp *expecter) string {
	tr := exp.Transcript()
	if m := regexp.MustCompile(`\[?([A-Za-z0-9._-]+@[A-Za-z0-9._-]+)`).FindAllStringSubmatch(tr, -1); len(m) > 0 {
		return "踏み台 " + m[len(m)-1][1]
	}
	return "踏み台"
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
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
