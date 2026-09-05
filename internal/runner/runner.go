// Package runner orchestrates connecting to devices, running their login
// automation and command sets, and saving logs — one device at a time or many
// in parallel.
package runner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kojio145/palaterm/internal/logstore"
	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
	"github.com/kojio145/palaterm/internal/session"
)

// Phase identifies where a device run currently is.
type Phase string

const (
	PhaseQueued     Phase = "queued"
	PhaseConnecting Phase = "connecting"
	PhaseLogin      Phase = "login"
	PhaseRunning    Phase = "running"
	PhaseSaving     Phase = "saving"
	PhaseDone       Phase = "done"
	PhaseError      Phase = "error"
)

// Event is a progress update emitted during a run. During PhaseRunning, Done
// counts commands completed so far out of Total (Total is 0 when the device
// has no command set).
type Event struct {
	Device  string `json:"device"`
	Phase   Phase  `json:"phase"`
	Message string `json:"message"`
	Done    int    `json:"done,omitempty"`
	Total   int    `json:"total,omitempty"`
	// WaitSec counts down the configured pause after a command, so a run that
	// looks frozen on a 10-second 待機 says how much of it is left.
	WaitSec int `json:"waitSec,omitempty"`
}

// DeviceResult is the outcome of one device run.
type DeviceResult struct {
	Device     string        `json:"device"`
	Success    bool          `json:"success"`
	Error      string        `json:"error,omitempty"`
	LogPath    string        `json:"logPath,omitempty"`
	Elapsed    time.Duration `json:"elapsed"`
	Transcript string        `json:"-"`
}

// EmitFunc receives progress events; may be nil.
type EmitFunc func(Event)

// Runner executes device workflows.
type Runner struct {
	profiles *profile.Registry

	// HostKeys pins SSH host keys (TOFU). nil accepts any key (tests only).
	HostKeys session.HostKeyStore
}

// New returns a Runner using the given profile registry.
func New(profiles *profile.Registry) *Runner {
	return &Runner{profiles: profiles}
}

// dialOpts builds the per-device security options for a dial.
func (r *Runner) dialOpts(dev *model.Device) session.DialOpts {
	return session.DialOpts{
		HostKey: session.VerifyHostKey(r.HostKeys),
		Legacy:  dev.LegacyAlgos,
	}
}

func (r *Runner) emit(emit EmitFunc, dev string, phase Phase, msg string) {
	if emit != nil {
		emit(Event{Device: dev, Phase: phase, Message: msg})
	}
}

// RunDevice connects to one device, drives login + commands, and writes a log.
func (r *Runner) RunDevice(ctx context.Context, dev *model.Device, set *model.CommandSet, s model.Settings, runDir string, now time.Time, emit EmitFunc) DeviceResult {
	start := time.Now()
	res := DeviceResult{Device: dev.Name}

	cmdTimeout := time.Duration(s.CommandTimeout) * time.Second
	if cmdTimeout <= 0 {
		cmdTimeout = 30 * time.Second
	}

	exp, prof, err := r.Connect(ctx, dev, s, emit)
	if exp != nil {
		defer exp.Close()
	}
	if err != nil {
		res.Error = err.Error()
		if exp != nil {
			res.Transcript = exp.Transcript()
		}
		r.saveLog(&res, dev, s, runDir, now)
		res.Elapsed = time.Since(start)
		r.emit(emit, dev.Name, PhaseError, res.Error)
		return res
	}

	vars := profile.Vars{User: dev.Username, Password: dev.Password, Enable: dev.EnablePassword}

	// Run the command set, reporting per-command progress (Done/Total feeds
	// the UI's per-device progress bar and overall ETA).
	total := 0
	if set != nil {
		total = len(set.Commands)
	}
	if emit != nil {
		emit(Event{Device: dev.Name, Phase: PhaseRunning, Message: setName(set), Done: 0, Total: total})
	}
	if set != nil {
		for i, cmd := range set.Commands {
			if err := ctx.Err(); err != nil {
				res.Error = err.Error()
				break
			}
			if emit != nil {
				emit(Event{Device: dev.Name, Phase: PhaseRunning, Message: cmd.Text, Done: i, Total: total})
			}
			if err := r.sendCommand(ctx, exp, prof, profile.Expand(cmd.Text, vars), cmdTimeout); err != nil {
				res.Error = fmt.Sprintf("command %q: %v", cmd.Text, err)
				break
			}
			if p := pauseFor(cmd, dev.Conn); p > 0 {
				if !countdown(ctx, p, func(left int) {
					if emit != nil {
						emit(Event{Device: dev.Name, Phase: PhaseRunning, Message: cmd.Text,
							Done: i, Total: total, WaitSec: left})
					}
				}) {
					res.Error = ctx.Err().Error()
					break
				}
			}
			if emit != nil {
				emit(Event{Device: dev.Name, Phase: PhaseRunning, Message: cmd.Text, Done: i + 1, Total: total})
			}
		}
	}

	// Clean logout (best effort), then unwind each bastion layer.
	r.disconnect(ctx, exp, prof, vars, len(dev.ActiveBastions()))

	res.Transcript = exp.Transcript()
	res.Success = res.Error == ""
	r.saveLog(&res, dev, s, runDir, now)
	res.Elapsed = time.Since(start)

	if res.Success {
		r.emit(emit, dev.Name, PhaseDone, res.LogPath)
	} else {
		r.emit(emit, dev.Name, PhaseError, res.Error)
	}
	return res
}

// wakeSerial coaxes a console line into showing where it stands.
//
// A serial console, unlike a network session, is not opened — it is joined. It
// printed its prompt whenever it last had something to say, possibly days ago,
// and says nothing at all to a program that only listens. The login steps then
// wait out their timeout for a prompt that already came and went, and the run
// ends with an empty log and a timeout naming the wrong thing.
//
// Enter makes it repeat itself: a login prompt, or the shell prompt of a
// session someone left logged in. It is retried a few times because some
// consoles spend the first keystroke waking up and print nothing for it.
//
// The line is erased first, because it may not be empty. Someone who typed
// half a command and walked away leaves it in the input buffer, and a bare
// Enter runs it — on the bench "show ru" came back as
// "% ru -- Invalid command.", and a half-typed line in config mode would be
// worse than a wasted round trip.
//
// Backspaces do the erasing, for the one property nothing else had: they
// cannot submit. Ctrl+U, the readline way, did nothing at all on the NEC IX router we tested.
// Ctrl+C, the network-CLI way, does abandon the line at a command prompt — but
// at a login prompt the same device treats it as Enter, so each attempt to be
// careful cost an extra failed login, and the stale prompts it left behind
// made the profile's own expects match the wrong one. Backspace at a login
// prompt just erases a half-typed username, which is the same thing it means
// everywhere else.
func wakeSerial(ctx context.Context, exp *expecter, prof profile.Profile) {
	const (
		tries = 3
		wait  = time.Second
	)
	for i := 0; i < tries; i++ {
		if err := exp.SendRaw(eraseLine); err != nil {
			return
		}
		// Send, not SendRaw: the console counts CR and LF as two Enters, and
		// the transport's own line ending is what gets that right.
		if err := exp.Send(""); err != nil {
			return
		}
		// Seen rather than Expect: the reply is the login banner the profile's
		// own steps are about to match against, so it must not be consumed.
		//
		// A reply is printable text, not a bell. NEC IX answers every
		// backspace on an empty line with BEL (0x07), so the erase above
		// comes back as 120 bells before the Enter's real answer ("Password:"
		// on a console sitting at its login prompt). Counting the bells as the
		// console answering moved on before "Password:" arrived: the stale-
		// login clearing saw nothing to clear, the profile's "login:" wait
		// timed out, and the password was typed as the answer to an empty
		// username — "認証に失敗" on a console with perfectly good credentials.
		if waitSeen(ctx, exp, consoleText, wait) {
			clearStaleLogin(ctx, exp, prof)
			return
		}
	}
}

// eraseLine backs over anything typed on the current line. The width is a
// guess at "longer than any half-typed command", and overshooting is free:
// backspace at the start of an empty line does nothing at all.
var eraseLine = strings.Repeat("\b", 120)

// consoleText is what counts as the console having answered: a printable
// character. Whitespace is the echo of our own Enter, and BEL (0x07) is what
// NEC IX sends for every backspace that has nothing to erase.
const consoleText = `[^\s\x07]`

// clearStaleLogin abandons a half-finished login that was already on the
// console when we joined.
//
// A console is shared and has no session: whoever used it last may have typed
// a username and walked away, leaving a password prompt that belongs to an
// attempt we know nothing about. Answering it with our password fails, and the
// run then reports bad credentials for credentials that are perfectly good.
// An empty Enter lets that stale attempt fail on its own so the device returns
// to its login prompt, which the profile can drive from the start.
//
// This only applies when the profile supplies a username: a device whose
// console asks for a password and nothing else is at its real prompt, not a
// stale one, and must not have it thrown away.
func clearStaleLogin(ctx context.Context, exp *expecter, prof profile.Profile) {
	sendsUsername := false
	for _, st := range prof.Login {
		if strings.Contains(st.Send, "{user}") {
			sendsUsername = true
			break
		}
	}
	if !sendsUsername || !exp.Seen(rePassword) || exp.Seen(reLogin) {
		return
	}
	if err := exp.Send(""); err != nil {
		return
	}
	waitSeen(ctx, exp, reLogin, 3*time.Second)
}

// waitSeen polls until pattern is present in the unconsumed output, without
// consuming it, and reports whether it turned up before the deadline.
func waitSeen(ctx context.Context, exp *expecter, pattern string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if exp.Seen(pattern) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return false
}

// disconnectSettle bounds the wait for a hop to finish answering one logout
// step. Teardown is best effort, so this stays short.
const disconnectSettle = 2 * time.Second

// disconnect logs out and unwinds the jump-host chain, letting each step land
// before sending the next.
//
// Logout steps are pure sends with no prompt of their own to key off, and a
// device changing mode reprints its prompt while it works. Fired back to back,
// the second "exit" lands inside that reprint and loses characters: NEC IX
// received it as "e" + prompt + "t" and answered "% t -- Ambiguous command.",
// leaving the session logged in and the jump host's inner session dangling.
//
// The settle happens BEFORE each send rather than after, so the final logout —
// which is answered by a closed connection, never a prompt — costs nothing.
func (r *Runner) disconnect(ctx context.Context, exp *expecter, prof profile.Profile, vars profile.Vars, hops int) {
	sends := make([]string, 0, len(prof.Disconnect)+hops)
	for _, st := range prof.Disconnect {
		if !st.Sends() {
			continue
		}
		if st.SendRaw != "" {
			sends = append(sends, profile.Expand(st.SendRaw, vars))
			continue
		}
		sends = append(sends, profile.Expand(st.Send, vars))
	}
	// One "exit" per jump host, to leave the shell we typed the jump command on.
	for i := 0; i < hops; i++ {
		sends = append(sends, "exit")
	}

	for i, s := range sends {
		if i > 0 {
			_ = r.waitPrompt(ctx, exp, prof, disconnectSettle)
		}
		if err := exp.Send(s); err != nil {
			return // the far end is already gone; nothing left to unwind
		}
	}
}

// Connect dials the device (through any jump-host chain), runs the OS profile
// login and pager-disable, and returns an OPEN expecter positioned at the
// operational prompt plus the resolved profile. The caller owns exp and must
// Close it. On failure exp may be non-nil (holding the partial transcript).
//
// RunDevice uses this before executing a command set; the interactive terminal
// uses it and then hands the session to raw pass-through.
func (r *Runner) Connect(ctx context.Context, dev *model.Device, s model.Settings, emit EmitFunc) (*expecter, profile.Profile, error) {
	prof := r.profiles.Get(dev.OSType)
	connTimeout := time.Duration(s.ConnectTimeout) * time.Second
	if connTimeout <= 0 {
		connTimeout = 20 * time.Second
	}
	cmdTimeout := time.Duration(s.CommandTimeout) * time.Second
	if cmdTimeout <= 0 {
		cmdTimeout = 30 * time.Second
	}

	bastions := dev.ActiveBastions()
	var sess session.Session
	var err error
	if len(bastions) > 0 {
		r.emit(emit, dev.Name, PhaseConnecting, fmt.Sprintf("踏み台%d段経由 %s", len(bastions), dev.Host))
		sess, err = dialCtx(ctx, func() (session.Session, error) {
			return session.DialBastionHead(&bastions[0], connTimeout, r.dialOpts(dev))
		})
	} else {
		r.emit(emit, dev.Name, PhaseConnecting, fmt.Sprintf("%s %s", dev.Conn, dev.Host))
		sess, err = dialCtx(ctx, func() (session.Session, error) { return session.DialDirect(dev, connTimeout, r.dialOpts(dev)) })
	}
	if err != nil {
		// A dial that fails on a method/port pairing that cannot work is worth
		// saying so about: the transport error alone reads as an unreachable
		// device. Nothing has been received yet, so the port is the evidence.
		if hint := wrongPortHint(dev, ""); hint != "" && len(bastions) == 0 {
			return nil, prof, fmt.Errorf("%w — %s", err, errors.New(hint))
		}
		return nil, prof, err
	}
	exp := newExpecter(sess)

	if dev.Conn == model.ConnSerial {
		wakeSerial(ctx, exp, prof)
	}

	if len(bastions) > 0 {
		if err := r.traverseBastions(ctx, exp, bastions, dev, cmdTimeout); err != nil {
			return exp, prof, err
		}
	}

	vars := profile.Vars{User: dev.Username, Password: dev.Password, Enable: dev.EnablePassword}

	r.emit(emit, dev.Name, PhaseLogin, prof.Name)
	// Over direct SSH the transport already authenticated, so the profile's
	// {user}/{password} steps are skipped outright; through a bastion (or on
	// Telnet/serial) the prompts arrive in-stream and the steps run.
	skipCreds := len(bastions) == 0 && dev.Conn == model.ConnSSH
	if err := r.runSteps(ctx, exp, prof.Login, vars, cmdTimeout, skipCreds, prof.Prompt); err != nil {
		if hint := wrongPortHint(dev, exp.Transcript()); hint != "" {
			return exp, prof, errors.New(hint)
		}
		if hint := silentDeviceHint(dev, exp.Transcript()); hint != "" {
			return exp, prof, errors.New(hint)
		}
		if backAtLogin(exp.Transcript()) {
			return exp, prof, errors.New(errAuthRefused)
		}
		return exp, prof, fmt.Errorf("login: %w", err)
	}
	// Login lists need no trailing expect-only prompt row: when the last step
	// sent something (or the list is empty), wait for the operational prompt
	// here. A profile that still ends with its own expect row keeps the old
	// behavior — no second wait that would hang.
	if n := len(prof.Login); n == 0 || prof.Login[n-1].Sends() {
		if err := r.waitPrompt(ctx, exp, prof, cmdTimeout); err != nil {
			// The same three explanations as above, and for the same reason:
			// which of the login steps happens to be the one that times out
			// depends on the profile, so a diagnosis attached to only one of
			// them disappears the moment a profile changes shape.
			if hint := wrongPortHint(dev, exp.Transcript()); hint != "" {
				return exp, prof, errors.New(hint)
			}
			if hint := silentDeviceHint(dev, exp.Transcript()); hint != "" {
				return exp, prof, errors.New(hint)
			}
			if backAtLogin(exp.Transcript()) {
				return exp, prof, errors.New(errAuthRefused)
			}
			if hint := promptMismatchHint(prof, exp.Transcript()); hint != "" {
				return exp, prof, errors.New(hint)
			}
			return exp, prof, fmt.Errorf("login: waiting for prompt: %w", err)
		}
	}

	// Disable paging (non-fatal: some devices have none).
	for _, st := range prof.Pager {
		if err := r.sendCommand(ctx, exp, prof, profile.Expand(st.Send, vars), cmdTimeout); err != nil {
			r.emit(emit, dev.Name, PhaseLogin, "pager disable skipped: "+err.Error())
			break
		}
	}
	return exp, prof, nil
}

// StartInteractive hands a connected expecter over to raw pass-through mode:
// the login transcript is flushed to sink, and subsequent device output is
// streamed to sink. Returns a writer function for sending keystrokes and a
// closer. The caller must call the closer to release the session.
func (r *Runner) StartInteractive(exp *expecter, sink func([]byte), onClose func()) (send func(string) error, resize func(cols, rows int) error, closeFn func() error) {
	exp.mu.Lock()
	exp.onClose = onClose
	exp.mu.Unlock()
	exp.StartRaw(sink)
	return func(data string) error { return exp.SendRaw(data) },
		func(cols, rows int) error { return exp.Resize(cols, rows) },
		exp.Close
}

// dialCtx runs a blocking dial in a goroutine so cancellation (中止) takes
// effect immediately even mid-connect; the abandoned dial's session is closed
// whenever it eventually returns.
func dialCtx(ctx context.Context, dial func() (session.Session, error)) (session.Session, error) {
	type dres struct {
		sess session.Session
		err  error
	}
	ch := make(chan dres, 1)
	go func() {
		s, e := dial()
		ch <- dres{s, e}
	}()
	select {
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.sess != nil {
				_ = r.sess.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-ch:
		return r.sess, r.err
	}
}

// sleepCtx waits d, returning false immediately if ctx is canceled first.
// countdown sleeps for sec seconds, reporting the seconds still to go once a
// second (starting at sec, so the UI never shows a stale count). It returns
// false if the context ended first.
func countdown(ctx context.Context, sec int, report func(left int)) bool {
	for left := sec; left > 0; left-- {
		report(left)
		if !sleepCtx(ctx, time.Second) {
			return false
		}
	}
	return true
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// sendCommand sends one command line and waits for the prompt to return,
// advancing through any pager prompts in the output.
func (r *Runner) sendCommand(ctx context.Context, exp *expecter, prof profile.Profile, text string, timeout time.Duration) error {
	// Clear stale prompts so we wait for the prompt that follows THIS command,
	// not one left buffered by the previous step.
	exp.Drain()
	if err := exp.Send(text); err != nil {
		return fmt.Errorf("send %q: %w", text, err)
	}
	return r.waitPrompt(ctx, exp, prof, timeout)
}

// genericMore matches the pager markers vendors actually print — "--More--",
// "---- More ----", "--- more ---", "-- MORE --", "---(more 25%)---" — and
// backs up profiles that declare no MorePrompt of their own.
//
// The pager-disable command differs per OS and is easy to get wrong (NEC IX,
// for one, only accepts "terminal length 0" inside configure mode and answers
// "% terminal -- Invalid command." elsewhere). Without this net, one wrong
// command means every long output hangs until the command timeout and the
// whole device fails — a profile mistake should cost tidiness, not the run.
const genericMore = `(?i)-{2,}\s*\(?\s*more`

// promptMismatchHint names the case where the login worked and the device is
// waiting at a prompt — just not the one this OS type expects.
//
// It happens when the account has a different privilege level than the profile
// assumes (a NEC IX monitor user sits at "%" where an administrator gets "#"),
// or when the device is set to the wrong OS type entirely. Both used to end as
// "waiting for prompt: expect timeout", which describes what the code was
// doing rather than what the device was showing — and the device was showing
// the answer the whole time.
func promptMismatchHint(prof profile.Profile, transcript string) string {
	tail := transcript
	const tailLen = 200
	if len(tail) > tailLen {
		tail = tail[len(tail)-tailLen:]
	}
	m := reIdlePrompt.FindStringSubmatch(tail)
	if m == nil {
		return "" // nothing prompt-shaped at the end: the device is not idle
	}
	want, err := regexp.Compile("(?:" + prof.Prompt + `)[ \t]*\z`)
	if err != nil || want.MatchString(tail) {
		return "" // the profile's own prompt: not a mismatch, some other fault
	}
	return fmt.Sprintf(
		"ログインはできましたが、機器は「%s」を表示していて、OSタイプ「%s」が待つプロンプト「%s」になりません。機器の権限レベル（昇格が必要か）と、機器の編集画面のOSタイプ設定を確認してください",
		m[1], prof.Name, prof.Prompt)
}

// silentDeviceHint names the case where the login timed out because nothing
// at all arrived from the device.
//
// Found on the bench with a console cable that was no longer plugged into
// anything: the port opened fine, so the run reached the login phase and sat
// there for the whole command timeout before ending as "login: waiting for
// prompt: expect timeout" — the same words as a wrong OS type or a refused
// password, none of which apply when the device never said a thing. An empty
// transcript is the evidence; the wording follows the transport, since a
// silent console and a silent network session have different things to check.
func silentDeviceHint(dev *model.Device, transcript string) string {
	if strings.TrimSpace(transcript) != "" {
		return ""
	}
	if dev.Conn == model.ConnSerial {
		return "コンソールから応答がありません。ケーブルの結線・COMポート・ボーレートと、機器の電源を確認してください"
	}
	return "接続はできましたが、機器から何も受信しませんでした。ポート番号と機器の状態を確認してください"
}

// reIdlePrompt matches a device sitting at some prompt, whatever level it is.
var reIdlePrompt = regexp.MustCompile(`([>%$#\]])[ \t]*\z`)

// errAuthRefused is what a device asking for a username again actually means.
const errAuthRefused = "認証に失敗しました。ユーザー名とパスワードを確認してください"

// backAtLogin reports that the device is sitting at its login prompt again.
//
// A refused password is the commonest way a run fails, and it used to surface
// as "login: waiting for \">\": expect timeout" — the profile's next step
// timing out on a prompt that was never going to come. That names the step we
// happened to be on, not the reason, and sends the reader looking at the OS
// profile instead of at the credentials.
//
// Only the tail counts, as it does for the operational prompt: every login
// transcript contains a login prompt near the top, and matching that would
// call any failure an auth failure.
func backAtLogin(transcript string) bool {
	const tailLen = 200
	tail := transcript
	if len(tail) > tailLen {
		tail = tail[len(tail)-tailLen:]
	}
	return reBackAtLogin.MatchString(tail)
}

var reBackAtLogin = regexp.MustCompile(`(?i)(login|username):\s*\z`)

// wrongPortHint names the mistake behind a failed connection when the method
// and the port disagree, so the run view says what to go and check instead of
// leaving a raw timeout or handshake error to be interpreted.
//
// It fires only on evidence, never as a guess on any failure: a Telnet client
// aimed at port 22 reads the SSH identification string the server opens with
// and then waits out the timeout for a login prompt that is never coming, and
// SSH on port 23 cannot complete a handshake at all. Both look like the device
// is at fault. transcript is what the device actually said (empty when the
// dial itself failed).
func wrongPortHint(dev *model.Device, transcript string) string {
	port := dev.EffectivePort()
	switch {
	case dev.Conn == model.ConnTelnet &&
		(strings.HasPrefix(strings.TrimSpace(transcript), "SSH-") || port == 22):
		return fmt.Sprintf(
			"接続方式=Telnet ですが、ポート%dはSSH用です。機器の編集画面でポートを23に直すか、接続方式をSSHに変更してください",
			port)
	case dev.Conn == model.ConnSSH && port == 23:
		return fmt.Sprintf(
			"接続方式=SSH ですが、ポート%dはTelnet用です。機器の編集画面でポートを22に直すか、接続方式をTelnetに変更してください",
			port)
	}
	return ""
}

// promptSettle is how long the prompt must remain the tail of the output
// before the device counts as idle. A single read can end exactly on a prompt
// character that is really mid-line, so re-checking after a beat costs
// milliseconds and keeps the next command out of a device still printing.
const promptSettle = 120 * time.Millisecond

// waitPrompt waits for the operational prompt, advancing through pager prompts.
func (r *Runner) waitPrompt(ctx context.Context, exp *expecter, prof profile.Profile, timeout time.Duration) error {
	more := prof.MorePrompt
	if more == "" {
		more = genericMore
	}
	// The prompt counts only as the TAIL of what has arrived. Matched anywhere
	// in the stream, a prompt character inside a command's own output ends the
	// wait early: NEC IX's "show processes" prints a "PDEV#" column header, so
	// the run moved on mid-output and typed the next command into a device that
	// was still paging — which ate its first character, turning "show logging"
	// into "how logging".
	tail := "(?:" + prof.Prompt + `)[ \t]*\z`
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return ErrExpectTimeout
		}
		if exp.Seen(tail) {
			time.Sleep(promptSettle)
			if exp.Seen(tail) {
				exp.Drain() // nothing follows the tail; start the next wait clean
				return nil
			}
			continue
		}
		// Not idle. If a pager is showing, advance it; the short wait doubles
		// as this loop's poll interval. The marker and the last page can land
		// in one read, so re-check first: a space typed at a device that has
		// already returned to its prompt is echoed into the next command line.
		if err := exp.Expect(ctx, more, 150*time.Millisecond); err == nil && !exp.Seen(tail) {
			_ = exp.SendRaw(" ")
		}
	}
}

// expectOrReady waits for pattern, reporting ready=true instead if the device
// reaches its operational prompt first. Both are polled in short slices so a
// step that will never match costs a fraction of a second rather than the full
// timeout; pattern is checked first each round, so a device that really does
// show it still takes the normal path.
func (r *Runner) expectOrReady(ctx context.Context, exp *expecter, pattern, ready string, timeout time.Duration) (bool, error) {
	if ready == "" {
		return false, exp.Expect(ctx, pattern, timeout)
	}
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, ErrExpectTimeout
		}
		err := exp.Expect(ctx, pattern, min(remaining, 300*time.Millisecond))
		if err == nil {
			return false, nil
		}
		if !errors.Is(err, ErrExpectTimeout) {
			return false, err // context cancelled or closed
		}
		if exp.Seen(ready) {
			return true, nil
		}
	}
}

// runSteps executes a step list (login / pager / disconnect).
//
// skipCreds is true when the transport itself already authenticated (direct
// SSH): steps sending {user}/{password} are then skipped outright. On every
// transport, auth steps ({user}/{password}/{enable}) whose prompt does not
// appear within a short wait are skipped without error — that is how one
// profile serves devices that ask for none, some, or all of the credentials.
//
// readyPrompt is the operational prompt for a login list (empty elsewhere). A
// non-auth step is abandoned as soon as that prompt is already on screen: the
// privilege-escalation row (wait ">" / send "enable") has no ">" to wait for
// on a device whose account logs straight in privileged, and without this it
// would burn the whole timeout and then fail the login outright.
func (r *Runner) runSteps(ctx context.Context, exp *expecter, steps []profile.Step, vars profile.Vars, timeout time.Duration, skipCreds bool, readyPrompt string) error {
	for _, st := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if skipCreds && st.Credential() {
			continue // transport-level auth already done (direct SSH)
		}
		if st.Expect != "" {
			// Auth steps keep the plain short wait: their prompt can legitimately
			// arrive after a banner, and racing them against readyPrompt would let
			// a banner containing the prompt character skip a credential.
			var (
				ready bool
				err   error
			)
			if st.Auth() {
				err = exp.Expect(ctx, st.Expect, min(timeout, 3*time.Second))
			} else {
				ready, err = r.expectOrReady(ctx, exp, st.Expect, readyPrompt, timeout)
			}
			if err != nil {
				if st.Auth() {
					continue // prompt absent: device skips this question
				}
				return fmt.Errorf("waiting for %q: %w", st.Expect, err)
			}
			if ready {
				continue // already operational: this step has nothing to do
			}
		}
		if st.Sends() {
			if st.SendRaw != "" {
				if err := exp.SendRaw(profile.Expand(st.SendRaw, vars)); err != nil {
					return err
				}
			} else {
				// Send (possibly a bare newline when Enter is set).
				if err := exp.Send(profile.Expand(st.Send, vars)); err != nil {
					return err
				}
			}
		}
		if st.DelayMs > 0 {
			time.Sleep(time.Duration(st.DelayMs) * time.Millisecond)
		}
	}
	return nil
}

// RedactSecrets masks the device's passwords (and its bastions') wherever
// they appear in transcript text — some devices echo input back, and a
// leaked prompt echo must not end up in a plaintext log file. Very short
// passwords are left alone: masking e.g. every "a" would destroy the log.
func RedactSecrets(text string, dev *model.Device) string {
	secrets := []string{dev.Password, dev.EnablePassword}
	for _, b := range dev.ActiveBastions() {
		secrets = append(secrets, b.Password)
	}
	for _, sec := range secrets {
		if len(sec) >= 4 {
			text = strings.ReplaceAll(text, sec, "****")
		}
	}
	return text
}

func (r *Runner) saveLog(res *DeviceResult, dev *model.Device, s model.Settings, runDir string, now time.Time) {
	if res.Transcript == "" {
		return
	}
	path, err := logstore.Write(runDir, s.LogNameTemplate, logstore.Fields{
		Host: dev.Name, IP: dev.Host, OS: dev.OSType, Group: dev.Group, Site: dev.Site,
	}, RedactSecrets(res.Transcript, dev), now)
	if err == nil {
		res.LogPath = path
	}
}

// RunBatch runs all enabled devices, bounded by Settings.MaxParallel.
func (r *Runner) RunBatch(ctx context.Context, inv *model.Inventory, emit EmitFunc) []DeviceResult {
	now := time.Now()
	runDir := logstore.RunDir(inv.Settings.LogDir, now)
	sets := indexSets(inv.CommandSets)

	var targets []*model.Device
	for i := range inv.Devices {
		if inv.Devices[i].Enabled {
			d := inv.Devices[i]
			targets = append(targets, &d)
			r.emit(emit, d.Name, PhaseQueued, "")
		}
	}

	limit := inv.Settings.MaxParallel
	if limit <= 0 {
		limit = len(targets)
	}
	if limit == 0 {
		return nil
	}
	sem := make(chan struct{}, limit)
	results := make([]DeviceResult, len(targets))
	var wg sync.WaitGroup

	for i, dev := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, d *model.Device) {
			defer wg.Done()
			defer func() { <-sem }()
			var set *model.CommandSet
			if cs, ok := sets[d.CommandSet]; ok {
				set = cs
			}
			results[idx] = r.RunDevice(ctx, d, set, inv.Settings, runDir, now, emit)
		}(i, dev)
	}
	wg.Wait()
	return results
}

func indexSets(sets []model.CommandSet) map[string]*model.CommandSet {
	m := make(map[string]*model.CommandSet, len(sets))
	for i := range sets {
		m[sets[i].Name] = &sets[i]
	}
	return m
}

func setName(s *model.CommandSet) string {
	if s == nil {
		return "(no commands)"
	}
	return s.Name
}

func pauseFor(cmd model.Command, conn model.ConnMethod) int {
	if conn == model.ConnSerial {
		return cmd.SerialSec
	}
	return cmd.PauseSec
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
