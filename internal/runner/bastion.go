package runner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/kojio145/palaterm/internal/model"
)

// Prompts commonly seen while logging into / hopping through jump hosts.
const (
	reLogin    = `(?i)login:|username:`
	rePassword = `(?i)password:`
	reYesNo    = `(?i)\(yes/no`
	// OpenSSH on a hop asking for the passphrase of a key kept on that hop
	// ("Enter passphrase for key '/home/u/.ssh/id_rsa':"): the next hop or
	// the device takes the key that lives on the jump host, not one on this PC.
	rePassphrase = `(?i)passphrase for key`
	reShellDone  = `[>#$%\]]\s*$` // a generic shell prompt after landing on a hop
)

// traverseBastions walks the jump-host chain and then jumps to the device,
// leaving the stream sitting at the device's pre-login banner so the device's
// OS profile can drive the final login.
//
// hop[0] transport is already open (SSH authed at transport, or raw Telnet).
// For each subsequent hop, and finally the device, it issues an ssh/telnet
// command from the current shell and answers the intermediate login prompts.
func (r *Runner) traverseBastions(ctx context.Context, exp *expecter, bastions []model.Bastion, dev *model.Device, timeout time.Duration) (*landing, error) {
	// Log into the first hop if it was reached by Telnet (SSH already authed).
	if bastions[0].Method == model.BastionTelnet {
		if err := loginHop(ctx, exp, bastions[0], timeout); err != nil {
			return nil, &hopLoginError{fmt.Errorf("踏み台1段目のログインに失敗: %w", err)}
		}
	} else {
		// Wait until the first SSH bastion's shell prompt appears.
		_ = exp.Expect(ctx, reShellDone, timeout)
	}

	// Hop from each bastion to the next. The jump command is typed on the
	// PREVIOUS hop's shell, so that hop's JumpCommand template applies.
	for i := 1; i < len(bastions); i++ {
		next := bastions[i]
		from := landing{target: next.Host, prompt: currentLine(exp.Transcript()), mark: exp.Mark()}
		keyed, err := jumpToKey(ctx, exp, bastions[i-1].JumpCommand, next.Method, next.Host, next.Username, next.Port, next.AuthMethod, next.KeyPassphrase, next.LegacyAlgos, timeout)
		if err != nil {
			return nil, fmt.Errorf("踏み台%d段目へのジャンプに失敗: %w", i+1, err)
		}
		if keyed {
			// In with the hop's key: no login or password prompt is coming.
			if err := exp.Expect(ctx, reShellDone, timeout); err != nil {
				return nil, fmt.Errorf("踏み台%d段目（%s）のログイン後にプロンプトが出ませんでした: %w", i+1, next.Host, err)
			}
		} else if err := loginHopFrom(ctx, exp, next, from, timeout); err != nil {
			return nil, &hopLoginError{fmt.Errorf("踏み台%d段目のログインに失敗: %w", i+1, err)}
		}
		// The shell reached must not be the one the jump was typed on.
		if err := from.check(exp); err != nil {
			return nil, fmt.Errorf("踏み台%d段目へのジャンプに失敗: %w", i+1, err)
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
	to := &landing{target: dev.Host, prompt: currentLine(exp.Transcript()), mark: exp.Mark()}
	if _, err := jumpToKey(ctx, exp, last.JumpCommand, method, dev.Host, dev.Username, dev.Port, dev.AuthMethod, dev.KeyPassphrase, dev.LegacyAlgos, timeout); err != nil {
		return nil, fmt.Errorf("機器へのジャンプに失敗: %w", err)
	}
	return to, nil
}

// A jump that fails does not always say so at once. A refused port answers
// within moments, but a device that does not answer at all leaves the hop's
// ssh or telnet waiting 20–75 seconds before it prints "Connection timed
// out" and drops back to the hop's own prompt — long after the jump counted
// as through. The device's login steps then ran against the hop, and a hop
// whose prompt ends in "#" or ">" (a router used as the jump host) passed
// for the device: the command set ran on the jump host (review 2026-10-22).
//
// Three checks close that, each on its own evidence:
//
//   - reJumpFailed: the words ssh, telnet and router CLIs use for a
//     connection that did not happen (OpenSSH, Linux telnet, Cisco IOS);
//   - the hop's prompt line, as it stood before the jump command, coming
//     back at the tail before anything was logged into;
//   - after the login, a prompt identical to that hop's (see landing.check).
var reJumpFailed = regexp.MustCompile(`(?im)(connection refused|connection timed out|operation timed out|no route to host|network is unreachable|host is unreachable|could not resolve|name or service not known|unknown host|connection closed by|closed by foreign host|connection reset by|unable to connect to remote host|host key verification failed|kex_exchange_identification|permission denied \(|command not found|^[ \t]*%[ \t]*(connection|destination unreachable|unknown command or computer name|bad ip address|host|invalid input))`)

// jumpFailLine is the hop's line saying the jump failed ("" for none), cut
// to a readable length, escape sequences out.
func jumpFailLine(text string) string {
	text = reEscape.ReplaceAllString(text, "")
	loc := reJumpFailed.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	start := strings.LastIndexAny(text[:loc[0]], "\r\n") + 1
	end := len(text)
	if j := strings.IndexAny(text[loc[0]:], "\r\n"); j >= 0 {
		end = loc[0] + j
	}
	return lastLine(text[start:end])
}

// jumpAdvice is what to check for the failure a hop reported.
func jumpAdvice(line string) string {
	l := strings.ToLower(line)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(l, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("refused"):
		return "宛先のポート番号と、機器側で SSH / Telnet が有効か（アクセス制限がないか）を確認してください"
	case has("could not resolve", "name or service", "unknown host", "computer name", "bad ip"):
		return "アドレス（ホスト名）のつづりと、踏み台での名前解決を確認してください"
	case has("host key verification"):
		return "踏み台に記録されたこの機器のホストキーが、今の機器と違います（機器の交換など）。踏み台の ~/.ssh/known_hosts から該当する行を消してから実行してください"
	case has("permission denied"):
		return "踏み台の ssh が認証で拒否されました。ユーザー名と、その機器の SSH 認証方式（パスワード／公開鍵）を確認してください"
	case has("command not found", "invalid input"):
		return "踏み台がジャンプコマンドを受け付けません。踏み台の「ジャンプコマンド」欄（ssh / telnet の書き方）を確認してください"
	case has("closed", "reset", "kex_exchange"):
		return "機器側が接続を切りました。機器のアクセス制限（許可する接続元）や同時ログイン数の上限を確認してください"
	}
	return "アドレスとポート、踏み台からその機器へ通信できるか（経路・アクセス制限）を確認してください"
}

// jumpError is a jump the hop reported (or showed) as failed: the run stops
// there and says why, instead of logging into the hop as if it were the far
// end.
type jumpError struct{ msg string }

func (e *jumpError) Error() string { return e.msg }

// hopError is any failure on the way through the jump hosts (a hop's login,
// a jump between hops): the line, if still open, is on a jump host.
type hopError struct{ err error }

func (e *hopError) Error() string { return e.err.Error() }
func (e *hopError) Unwrap() error { return e.err }

// hopLoginError is a jump host's own login that failed: the line is at that
// hop's login or password prompt, not at a shell (review 2026-10-23: the
// terminal window said "back on the jump host's shell" there, and an ssh
// command typed on that advice went into a password prompt).
type hopLoginError struct{ err error }

func (e *hopLoginError) Error() string { return e.err.Error() }
func (e *hopLoginError) Unwrap() error { return e.err }

// AtJumpHost reports whether a failed login left the line on a jump host's
// shell rather than the device: the terminal window must not offer that
// shell as the device's screen. A hop's failed login is AtJumpLogin instead.
func AtJumpHost(err error) bool {
	var j *jumpError
	var h *hopError
	return (errors.As(err, &j) || errors.As(err, &h)) && !AtJumpLogin(err)
}

// AtJumpLogin reports whether a failed login left the line at a jump host's
// own login or password prompt.
func AtJumpLogin(err error) bool {
	var l *hopLoginError
	return errors.As(err, &l)
}

func failedJump(target, line string) error {
	return &jumpError{fmt.Sprintf("踏み台から %s へ接続できませんでした（踏み台の表示: %q）。%s", target, line, jumpAdvice(line))}
}

// jumpFailure inspects what arrived since the jump command (text) for a
// failed jump: the hop's failure words, or its own prompt (from) back at
// the tail. nil when neither is there.
func jumpFailure(text, target, from string) error {
	if line := jumpFailLine(text); line != "" {
		return failedJump(target, line)
	}
	if atPrompt(text, from) {
		return &jumpError{fmt.Sprintf("踏み台から %s へ接続できず、踏み台のプロンプト（%q）に戻りました。アドレスとポート、踏み台からその機器へ通信できるか（経路・アクセス制限）を確認してください", target, from)}
	}
	return nil
}

// currentLine is the line output is on now — after the last line break,
// escape sequences out, trimmed. At a shell that is its prompt.
func currentLine(text string) string {
	text = reEscape.ReplaceAllString(text, "")
	if i := strings.LastIndexAny(text, "\r\n"); i >= 0 {
		text = text[i+1:]
	}
	return strings.TrimSpace(text)
}

// atPrompt reports that text ends on prompt, a hop's prompt line. Only a
// prompt that names something counts ("jump1#", "[ops@jump1 ~]$"): a bare
// "$" or "#" is what any host may show.
func atPrompt(text, prompt string) bool {
	if !distinctPrompt(prompt) {
		return false
	}
	return currentLine(text) == prompt
}

func distinctPrompt(p string) bool {
	for _, r := range p {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// landing is a jump about to be made: where to (target), the prompt of the
// hop it is typed on, and where in the transcript it starts.
type landing struct {
	target string
	prompt string
	mark   int
}

// check refuses a login that ended on the hop the jump was typed on: the
// prompt reached is that hop's own. A login that worked would show the far
// end's prompt; the same line means the hop's ssh or telnet gave up (late,
// after the login steps had started) and the hop's shell took the login's
// place. Run there, the command set would run on the jump host.
func (l *landing) check(exp *expecter) error {
	if l == nil || !atPrompt(exp.Transcript(), l.prompt) {
		return nil
	}
	since := exp.Since(l.mark)
	if line := jumpFailLine(since); line != "" {
		return failedJump(l.target, line)
	}
	return &jumpError{fmt.Sprintf("ログイン後のプロンプト（%q）が踏み台のプロンプトと同じため、中止しました。踏み台から %s への接続に失敗して、踏み台に戻っている可能性があります。機器と踏み台のホスト名が同じ場合は、どちらかのホスト名を変えてください", l.prompt, l.target)}
}

// failed explains a login through the jump that did not complete: the hop
// reported the jump as failed before any login prompt arrived. nil when
// nothing says so (the login failed for its own reasons).
func (l *landing) failed(exp *expecter) error {
	if l == nil {
		return nil
	}
	since := exp.Since(l.mark)
	// Only what came before the far end's first login prompt: after it, the
	// text is the far end's (a banner may well say "connection closed").
	if loc := reLoginOrPassword.FindStringIndex(since); loc != nil && !atPrompt(since, l.prompt) {
		since = since[:loc[0]]
	}
	return jumpFailure(since, l.target, l.prompt)
}

var reLoginOrPassword = regexp.MustCompile(reLogin + `|` + rePassword)

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
func jumpTo(ctx context.Context, exp *expecter, template string, method model.BastionMethod, host, user string, port int, legacy bool, timeout time.Duration) error {
	_, err := jumpToKey(ctx, exp, template, method, host, user, port, model.AuthPassword, "", legacy, timeout)
	return err
}

// jumpToKey is jumpTo that also answers the hop's ssh asking for the
// passphrase of a key kept on the hop (see rePassphrase) with passphrase —
// the 秘密鍵のパスフレーズ of the device or bastion being reached. keyed
// reports that it did: the far end then logs in with the key, and no login
// or password prompt follows.
//
// auth is the far end's SSH authentication method: the passphrase goes only
// when it is 公開鍵 — a password-authenticated far end that asks for one is
// not the hop's ssh talking but the far end's banner, and the passphrase of
// a key kept on the hop is nothing it should learn. legacy is the far end's
// レガシー暗号を許可: only then is a refused negotiation retried with the
// offered (old) algorithms allowed, as a direct connection would only offer
// them with it set.
func jumpToKey(ctx context.Context, exp *expecter, template string, method model.BastionMethod, host, user string, port int, auth model.AuthMethod, passphrase string, legacy bool, timeout time.Duration) (keyed bool, err error) {
	// The user name is typed into the hop's shell. Checked here, on the value
	// actually used — a device on its group's default credentials sends the
	// group's name, which the device's own checks never see.
	if !model.ShellSafeWord(user) {
		return false, fmt.Errorf("踏み台で使うユーザー名に使えない文字（空白・記号 ; | & $ など）が含まれています: %q", user)
	}
	cmd := buildJumpCommand(template, method, host, user, port)
	// The hop's prompt as it stands now: seen again before anything else, the
	// jump fell straight back to the hop (see reJumpFailed).
	from := currentLine(exp.Transcript())
	var extra []string
	for attempt := 0; ; attempt++ {
		exp.Drain()
		if err := exp.Send(withSSHOptions(cmd, extra)); err != nil {
			return false, err
		}
		// A first-connection host-key prompt, or OpenSSH refusing the far
		// end's algorithms, both show up within moments of the command. The
		// far end's own login prompt showing up instead means the jump is
		// through: return at once and leave that prompt unconsumed for the
		// OS profile (or loginHop) to answer.
		seen, err := jumpSeen(ctx, exp, host, from)
		if err != nil {
			return false, err
		}
		if seen == "yesno" {
			_ = exp.Expect(ctx, reYesNo, time.Second)
			exp.Drain()
			_ = exp.Send("yes")
			// The key's passphrase is asked after the host key is accepted.
			if seen, err = jumpSeen(ctx, exp, host, from); err != nil {
				return false, err
			}
		}
		if seen == "passphrase" {
			if auth != model.AuthPublicKey {
				return false, fmt.Errorf("%s で %s へ入るときに鍵のパスフレーズを聞かれましたが、この接続先の SSH 認証方式は「公開鍵」ではないため送りませんでした。踏み台の上にある鍵で入るなら、その機器（踏み台なら踏み台）の認証方式を「公開鍵」にしてパスフレーズを「秘密鍵のパスフレーズ」欄に入れてください", shellName(exp), host)
			}
			return true, answerPassphrase(ctx, exp, host, passphrase)
		}
		if seen != "negotiate" {
			return false, nil
		}
		// Let the line finish so the offer is readable, then land back on the
		// hop's shell before typing again.
		_ = exp.Expect(ctx, `Their offer: [^\s]+`, 2*time.Second)
		opt := negotiateFix(exp.Transcript())
		_ = exp.Expect(ctx, reShellDone, 3*time.Second)
		if !legacy {
			return false, fmt.Errorf("%s の ssh が %s と暗号方式を合意できません（%s）。機器が古い方式しか持たない場合は、その機器（踏み台なら踏み台）の「レガシー暗号を許可」をオンにするか、踏み台のジャンプコマンド欄で -o オプションを指定してください", shellName(exp), host, lastNegotiateLine(exp.Transcript()))
		}
		if opt == "" || attempt >= maxNegotiateRetries || containsStr(extra, opt) {
			return false, fmt.Errorf("%s の ssh が %s と暗号方式を合意できません（%s）。機器が古い方式しか持たない場合は踏み台のジャンプコマンド欄で -o オプションを指定してください", shellName(exp), host, lastNegotiateLine(exp.Transcript()))
		}
		extra = append(extra, opt)
	}
}

// jumpSeen watches the moments after a jump command (or the "yes" to a host
// key) for what the hop's ssh says: "negotiate", "yesno", "passphrase",
// "login" (the far end's own login or password prompt), or "" (nothing yet:
// the far end is on its way). A prompt is left unconsumed for whoever
// answers it. A jump the hop reports as failed — its failure words, or its
// own prompt (from) back — is an error naming target (see reJumpFailed).
func jumpSeen(ctx context.Context, exp *expecter, target, from string) (string, error) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		switch {
		case exp.Seen(reNegotiate):
			return "negotiate", nil
		case exp.Seen(reYesNo):
			return "yesno", nil
		case exp.Seen(rePassphrase):
			return "passphrase", nil
		case exp.Seen(reLogin) || exp.Seen(rePassword):
			return "login", nil
		}
		if err := jumpFailure(exp.Pending(), target, from); err != nil {
			return "", err
		}
		if exp.Closed() {
			return "", ErrClosed
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return "", nil
}

// answerPassphrase sends the key passphrase the hop's ssh asked for. Asked
// again, the passphrase was wrong: said so, instead of waiting out a login
// that will not come (OpenSSH asks three times, then falls back or gives up).
func answerPassphrase(ctx context.Context, exp *expecter, host, passphrase string) error {
	if passphrase == "" {
		return fmt.Errorf("%s で %s へ入る鍵のパスフレーズを聞かれましたが、「秘密鍵のパスフレーズ」が空です。その機器（踏み台なら踏み台）の SSH 認証方式を「公開鍵」にし、踏み台の上にある鍵のパスフレーズを「秘密鍵のパスフレーズ」欄に入れてください（踏み台経由では秘密鍵ファイル欄は使いません）", shellName(exp), host)
	}
	_ = exp.Expect(ctx, rePassphrase+`[^\r\n]*:`, time.Second)
	exp.Drain()
	if err := exp.Send(passphrase); err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if exp.Seen(rePassphrase) {
			return fmt.Errorf("%s で %s へ入る鍵のパスフレーズが違うため、入れませんでした（「秘密鍵のパスフレーズ」欄を確認してください）", shellName(exp), host)
		}
		// The key refused ("Permission denied (publickey)."), the hop's shell
		// prompt follows: that prompt is no sign of having arrived.
		if line := jumpFailLine(exp.Pending()); line != "" {
			return failedJump(host, line)
		}
		if exp.Seen(reShellDone) || exp.Seen(reLogin) || exp.Seen(rePassword) {
			return nil
		}
		if exp.Closed() {
			return ErrClosed
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
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
	// The offer is the far end's own words, typed back into the hop's shell:
	// only algorithm names get through (a ";", "$(" or "|" in a forged offer
	// would otherwise run on the jump host). Anything else ends the retry.
	if !reAlgoList.MatchString(last[2]) {
		return ""
	}
	return negotiateOption[last[1]] + "=+" + last[2]
}

// reAlgoList is a comma-separated list of SSH algorithm names
// ("ssh-dss", "diffie-hellman-group1-sha1", "aes128-cbc,3des-cbc",
// "hmac-sha1-96@openssh.com").
var reAlgoList = regexp.MustCompile(`^[A-Za-z0-9@._+-]+(,[A-Za-z0-9@._+-]+)*$`)

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
// (port 0 => ""); otherwise the standard ssh/telnet form for method is used,
// with the port when it is not the method's own (a device may well run its
// SSH on 2222: typed without it, the hop reached port 22 instead).
func buildJumpCommand(template string, method model.BastionMethod, host, user string, port int) string {
	if template != "" {
		p := ""
		if port > 0 {
			p = fmt.Sprintf("%d", port)
		}
		return strings.NewReplacer("{user}", user, "{host}", host, "{port}", p).Replace(template)
	}
	if method == model.BastionSSH {
		opt := ""
		if port > 0 && port != 22 {
			opt = fmt.Sprintf("-p %d ", port)
		}
		if user != "" {
			return fmt.Sprintf("ssh %s%s@%s", opt, user, host)
		}
		return fmt.Sprintf("ssh %s%s", opt, host)
	}
	if port > 0 && port != 23 {
		return fmt.Sprintf("telnet %s %d", host, port)
	}
	return fmt.Sprintf("telnet %s", host)
}

// expectHop is Expect for a login prompt of a hop just jumped to. It gives
// up early, with ErrExpectTimeout, once skip is showing (the prompt is not
// coming), and with a jumpError once what arrived since the jump says the
// jump failed (the hop's failure words, or its prompt back).
func expectHop(ctx context.Context, exp *expecter, pattern, skip string, from landing, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ErrExpectTimeout
		}
		err := exp.Expect(ctx, pattern, min(remaining, 100*time.Millisecond))
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrExpectTimeout) {
			return err // canceled, or the session closed
		}
		// (No target: the first hop, reached without a jump.)
		if from.target != "" {
			if ferr := jumpFailure(exp.Pending(), from.target, from.prompt); ferr != nil {
				return ferr
			}
		}
		if skip != "" && exp.Seen(skip) {
			return ErrExpectTimeout
		}
	}
}

// loginHop answers the login/password prompts for a bastion we just reached.
//
// Reaching the hop's shell prompt afterwards is required, not optional: a hop
// that rejects the credentials prints its login prompt again, and continuing
// regardless types the jump command in as a username. The run then fails much
// later, on the device's prompt, with a timeout that says nothing about the
// hop whose password was actually wrong.
func loginHop(ctx context.Context, exp *expecter, b model.Bastion, timeout time.Duration) error {
	return loginHopFrom(ctx, exp, b, landing{}, timeout)
}

// loginHopFrom is loginHop for a hop just jumped to from another (from):
// while its login prompts are awaited, the jump failing — late, as a timed
// out connection does — ends the wait with that reason (see reJumpFailed).
func loginHopFrom(ctx context.Context, exp *expecter, b model.Bastion, from landing, timeout time.Duration) error {
	if b.Username != "" {
		// An ssh hop asks for the password only: once that is showing, there
		// is no login prompt to wait for.
		err := expectHop(ctx, exp, reLogin, rePassword, from, timeout)
		var je *jumpError
		if errors.As(err, &je) {
			return err
		}
		if err == nil {
			_ = exp.Send(b.Username)
		}
	}
	err := expectHop(ctx, exp, rePassword, "", from, timeout)
	var je *jumpError
	if errors.As(err, &je) {
		return err
	}
	if err == nil {
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
