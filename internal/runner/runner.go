// Package runner orchestrates connecting to devices, running their login
// automation and command sets, and saving logs — one device at a time or many
// in parallel.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	// PhaseCanceled: the run was stopped by the user before this device
	// finished — either it never started, or it was cut off mid-way. Kept
	// apart from PhaseError so the UI can offer "re-run what was cut off"
	// without lumping it in with devices that actually failed.
	PhaseCanceled Phase = "canceled"
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
	// LogPath is the device's log file, written as the output arrives: on
	// every event from the start of the connection (ログ表示 works while the
	// device runs), and on the last one the file kept ("" = none: nothing
	// was read).
	LogPath string `json:"logPath,omitempty"`
}

// DeviceResult is the outcome of one device run.
type DeviceResult struct {
	Device     string        `json:"device"`
	Success    bool          `json:"success"`
	Error      string        `json:"error,omitempty"`
	LogPath    string        `json:"logPath,omitempty"`
	Elapsed    time.Duration `json:"elapsed"`
	Transcript string        `json:"-"`
	// Output is what the device showed, for a 接続確認のみ that failed: a
	// dry run writes no log, so the row's "出力" is the only way to see the
	// hop's banner or the prompt the login stopped at. Masked, trimmed to
	// the last few thousand characters, and never written to disk.
	Output string `json:"output,omitempty"`
	// Canceled is set when the batch was stopped (中止) before this device
	// completed. Success is false; Error says whether it never ran or was
	// interrupted.
	Canceled bool `json:"canceled,omitempty"`
	// Warnings name the commands the device refused ("show foo → % Invalid
	// input detected at '^' marker."); the run itself still succeeded.
	Warnings []string `json:"warnings,omitempty"`
}

// CanceledMsg / CanceledNotRunMsg are the messages a stopped run leaves on
// its devices (the UI translates them).
const (
	CanceledMsg       = "中止しました"
	CanceledNotRunMsg = "中止しました（未実行）"
	// CanceledDeviceMsg: this device alone was stopped (CancelDevice), the
	// rest of the batch went on.
	CanceledDeviceMsg = "この機器だけ中止しました"
)

// finishPhase picks the terminal phase for a failed device: canceled when the
// run was stopped, error otherwise.
func finishPhase(ctx context.Context, res *DeviceResult) Phase {
	if ctx.Err() != nil {
		res.Canceled = true
		res.Error = CanceledMsg
		return PhaseCanceled
	}
	return PhaseError
}

// EmitFunc receives progress events; may be nil.
type EmitFunc func(Event)

// Runner executes device workflows.
type Runner struct {
	profiles *profile.Registry

	// HostKeys pins SSH host keys (TOFU). nil accepts any key (tests only).
	HostKeys session.HostKeyStore

	// live: the logs of the devices running now, by path (see FlushLive).
	live sync.Map

	// Stopping one device of a batch (CancelDevice): the running ones by
	// name, and the ones asked to stop before their turn came. devStop is
	// nil outside a batch.
	devMu     sync.Mutex
	devCancel map[string]context.CancelFunc
	devStop   map[string]bool
}

// CancelDevice stops one device of the running batch and lets the others go
// on: a device in progress is cut off like 中止 cuts off the whole batch, a
// device still waiting for its turn is not started. False when no batch is
// running.
func (r *Runner) CancelDevice(name string) bool {
	r.devMu.Lock()
	defer r.devMu.Unlock()
	if c, ok := r.devCancel[name]; ok {
		c()
		return true
	}
	if r.devStop == nil {
		return false
	}
	r.devStop[name] = true
	return true
}

// claimDevice gives a batch device its own context (CancelDevice cancels
// it), unless it was already asked to stop. release ends the claim.
func (r *Runner) claimDevice(ctx context.Context, name string) (dctx context.Context, release func(), stopped bool) {
	r.devMu.Lock()
	defer r.devMu.Unlock()
	if r.devStop[name] {
		return ctx, func() {}, true
	}
	dctx, cancel := context.WithCancel(ctx)
	if r.devCancel == nil {
		r.devCancel = map[string]context.CancelFunc{}
	}
	r.devCancel[name] = cancel
	return dctx, func() {
		r.devMu.Lock()
		delete(r.devCancel, name)
		r.devMu.Unlock()
		cancel()
	}, false
}

// FlushLive writes out what a running device's log still holds back and
// reports whether path is such a log (it is still growing: a viewer opened
// on it should follow it).
func (r *Runner) FlushLive(path string) bool {
	v, ok := r.live.Load(path)
	if ok {
		v.(*logstore.Stream).Flush()
	}
	return ok
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

// Options tune one batch beyond the vault's Settings.
type Options struct {
	// Stage labels the run (before / during / after the maintenance work).
	// It goes into the run folder and log file names.
	Stage model.Stage
	// Group is the group the run was started for (named in the log folder).
	Group string
	// DryRun connects and logs in only: no command set is sent, nothing is
	// written to disk. The result says whether each device is reachable with
	// its credentials — a rehearsal for the real batch.
	DryRun bool
	// Dir, when set, is the folder the logs go to instead of a new run
	// folder: a 予約 keeps every round in one folder of its own.
	Dir string
	// LogStage, when set, is the word {stage} becomes in the log file names
	// instead of Stage's: a 予約 run with no stage is named "予約" rather
	// than the plain capture's 指定なし word ("Current").
	LogStage string
}

// logStage is what the log file names call the run's stage.
func (o Options) logStage() model.Stage {
	if o.LogStage != "" {
		return model.Stage(o.LogStage)
	}
	return o.Stage
}

// RunDevice connects to one device, drives login + commands, and writes a log.
func (r *Runner) RunDevice(ctx context.Context, dev *model.Device, set *model.CommandSet, s model.Settings, runDir string, now time.Time, emit EmitFunc) DeviceResult {
	return r.runDevice(ctx, dev, set, s, runDir, now, emit, Options{})
}

func (r *Runner) runDevice(ctx context.Context, dev *model.Device, set *model.CommandSet, s model.Settings, runDir string, now time.Time, emit EmitFunc, opts Options) DeviceResult {
	start := time.Now()
	res := DeviceResult{Device: dev.Name}
	if opts.DryRun {
		set = nil
	}

	cmdTimeout := time.Duration(s.CommandTimeout) * time.Second
	if cmdTimeout <= 0 {
		cmdTimeout = 30 * time.Second
	}
	// A set that types a password into a command line ("{enable}" on its
	// own, "ping {password}…") gets it echoed by the device: that log is
	// masked whatever the setting says (the setting's reason — a password
	// that doubles as a community string — does not cover a typed one).
	if len(set.SecretCommands()) > 0 {
		s.MaskLogSecrets = true
	}

	// The log is written as the output arrives, so ログ表示 shows a device
	// still running and a batch cut short (quit, power cut, sleep) leaves
	// what it had read. A file that cannot be claimed falls back to writing
	// the transcript at the end (where the error is reported).
	final := emit
	var live *liveLog
	if !opts.DryRun {
		live = r.startLog(dev, s, runDir, now, opts.logStage())
	}
	var tee func([]byte)
	if live != nil {
		tee = live.stream.Write
		emit = live.tag(emit)
	}

	exp, prof, pagerWarn, err := r.connect(ctx, dev, s, emit, tee)
	if exp != nil {
		defer exp.Close()
	}
	if pagerWarn != "" {
		res.Warnings = append(res.Warnings, pagerWarn)
	}
	if err != nil {
		res.Error = err.Error() + lastShown(ctx, exp, dev, err.Error())
		ph := finishPhase(ctx, &res)
		if exp != nil {
			res.Transcript = exp.Transcript()
		}
		if opts.DryRun {
			res.Output = dryRunOutput(exp, dev)
		} else {
			r.finishLog(&res, live, dev, s, runDir, now, opts.logStage())
		}
		res.Elapsed = time.Since(start)
		if final != nil {
			final(Event{Device: dev.Name, Phase: ph, Message: res.Error, LogPath: res.LogPath})
		}
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
			before := len(exp.Transcript())
			if err := r.sendCommand(ctx, exp, prof, profile.Expand(cmd.Text, vars), cmd.Wait, cmdTimeout); err != nil {
				res.Error = fmt.Sprintf("command %q: %v", cmd.Text, err)
				// A timeout says nothing by itself; what the device was showing
				// tells a question waiting for an answer from a long output.
				if errors.Is(err, ErrExpectTimeout) {
					if cmd.Wait != "" {
						res.Error += fmt.Sprintf("（待つ文字列 %q が出ませんでした）", cmd.Wait)
					}
					if last := lastLine(exp.Transcript()[before:]); last != "" {
						res.Error += fmt.Sprintf("（機器の最後の表示: %q。確認を求めているなら OSタイプ設定の「確認への自動応答」に登録、出力が長いだけなら設定の「コマンドタイムアウト」を延ばしてください）", RedactSecrets(last, dev))
					}
				}
				break
			}
			// The device answered, but possibly with a refusal ("% Invalid
			// input", "command not found"): the run still counts as done,
			// with a warning naming the command, so a log missing what it was
			// supposed to show is noticed now and not at the next outage.
			if line := rejectedLine(exp.Transcript()[before:]); line != "" {
				// The device's line may repeat a typed secret ("bash: Secr3t:
				// command not found" after an unanswered su), and warnings go
				// to the run table and the history file: passwords are always
				// blanked here, whatever the log mask setting.
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s → %s", cmd.Text, RedactSecrets(line, dev)))
			}
			if p := pauseFor(cmd, dev.Conn); p > 0 && !set.PauseOff {
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
	r.disconnect(ctx, exp, prof, vars, typedHops(dev))

	res.Transcript = exp.Transcript()
	res.Success = res.Error == ""
	if !opts.DryRun {
		r.finishLog(&res, live, dev, s, runDir, now, opts.logStage())
		res.Success = res.Error == ""
	}
	res.Elapsed = time.Since(start)

	if final != nil {
		if res.Success {
			msg := res.LogPath
			if opts.DryRun {
				msg = DryRunOKMsg
			}
			final(Event{Device: dev.Name, Phase: PhaseDone, Message: msg, LogPath: res.LogPath})
		} else {
			final(Event{Device: dev.Name, Phase: finishPhase(ctx, &res), Message: res.Error, LogPath: res.LogPath})
		}
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
// typedHops is how many jump hosts the session sits inside as shells, each
// left with an "exit": none through a tunnel, where nothing was typed on them.
func typedHops(dev *model.Device) int {
	if dev.Tunneled() {
		return 0
	}
	return len(dev.ActiveBastions())
}

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
	exp, prof, _, err := r.connect(ctx, dev, s, emit, nil)
	return exp, prof, err
}

// ConnectTee is Connect with tee getting every byte the device sends from
// the first one on: the interactive window shows the login as it happens,
// so a login that stalls shows where (a hop's prompt, "Password:", a
// banner) without switching to manual. Hand the session over with
// StartInteractiveShown, the output so far being on screen already.
func (r *Runner) ConnectTee(ctx context.Context, dev *model.Device, s model.Settings, tee func([]byte)) (*expecter, profile.Profile, error) {
	exp, prof, _, err := r.connect(ctx, dev, s, nil, tee)
	return exp, prof, err
}

// connect is Connect with tee getting a copy of every byte read (see
// newExpecterTee): a batch device's live log. warn names a pager-disable
// command the device refused ("" when none): the run goes on (--More-- is
// answered as it comes), but the refusal is worth a word in the result.
func (r *Runner) connect(ctx context.Context, dev *model.Device, s model.Settings, emit EmitFunc, tee func([]byte)) (exp *expecter, prof profile.Profile, warn string, err error) {
	prof = r.profiles.Get(dev.OSType)
	connTimeout := time.Duration(s.ConnectTimeout) * time.Second
	if connTimeout <= 0 {
		connTimeout = 20 * time.Second
	}
	cmdTimeout := time.Duration(s.CommandTimeout) * time.Second
	if cmdTimeout <= 0 {
		cmdTimeout = 30 * time.Second
	}

	bastions := dev.ActiveBastions()
	sess, err := r.dialTransport(ctx, dev, bastions, connTimeout, emit)
	if err != nil {
		return nil, prof, "", err
	}
	exp = newExpecterTee(sess, tee)

	if dev.Conn == model.ConnSerial {
		wakeSerial(ctx, exp, prof)
	}

	if len(bastions) > 0 && !dev.Tunneled() {
		if err := r.traverseBastions(ctx, exp, bastions, dev, cmdTimeout); err != nil {
			return exp, prof, "", err
		}
	}

	vars := profile.Vars{User: dev.Username, Password: dev.Password, Enable: dev.EnablePassword}

	r.emit(emit, dev.Name, PhaseLogin, prof.Name)
	// Over direct SSH the transport already authenticated, so the profile's
	// {user}/{password} steps are skipped outright; through a bastion (or on
	// Telnet/serial) the prompts arrive in-stream and the steps run.
	skipCreds := (len(bastions) == 0 || dev.Tunneled()) && dev.Conn == model.ConnSSH
	if err := r.runSteps(ctx, exp, prof.Login, vars, cmdTimeout, skipCreds, prof.Prompt); err != nil {
		if hint := wrongPortHint(dev, exp.Transcript()); hint != "" {
			return exp, prof, "", errors.New(hint)
		}
		if hint := silentDeviceHint(dev, exp.Transcript()); hint != "" {
			return exp, prof, "", errors.New(hint)
		}
		if backAtLogin(exp.Transcript()) {
			return exp, prof, "", errors.New(errAuthRefused)
		}
		if hint := enableRefusedHint(prof, exp.Transcript()); hint != "" {
			return exp, prof, "", errors.New(hint)
		}
		return exp, prof, "", fmt.Errorf("login: %w", err)
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
				return exp, prof, "", errors.New(hint)
			}
			if hint := silentDeviceHint(dev, exp.Transcript()); hint != "" {
				return exp, prof, "", errors.New(hint)
			}
			if backAtLogin(exp.Transcript()) {
				return exp, prof, "", errors.New(errAuthRefused)
			}
			if hint := enableRefusedHint(prof, exp.Transcript()); hint != "" {
				return exp, prof, "", errors.New(hint)
			}
			if hint := promptMismatchHint(prof, exp.Transcript()); hint != "" {
				return exp, prof, "", errors.New(hint)
			}
			return exp, prof, "", fmt.Errorf("login: waiting for prompt: %w", err)
		}
	}

	// Disable paging (non-fatal: some devices have none). A refusal used
	// to flash by as a login-phase message and vanish under the first
	// command's; it comes back as a warning on the result instead.
	for _, st := range prof.Pager {
		before := len(exp.Transcript())
		if err := r.sendCommand(ctx, exp, prof, profile.Expand(st.Send, vars), "", cmdTimeout); err != nil {
			r.emit(emit, dev.Name, PhaseLogin, "pager disable skipped: "+err.Error())
			warn = fmt.Sprintf("ページャ解除コマンド「%s」が通りませんでした（%v）。出力の途中停止は --More-- への応答で続けます", st.Send, err)
			break
		}
		if line := rejectedLine(exp.Transcript()[before:]); line != "" {
			warn = fmt.Sprintf("ページャ解除コマンド「%s」を機器が受け付けませんでした: %s", st.Send, RedactSecrets(line, dev))
			break
		}
	}
	return exp, prof, warn, nil
}

// enableRefusedHint names the commonest login failure after a wrong
// password: the enable (privilege) password missing or wrong. The profile
// sent {enable} and the device is sitting at "Password:" again, waiting
// for a better answer — which used to end as "login: waiting for prompt:
// expect timeout（機器の最後の表示: "Password:"）", a riddle.
func enableRefusedHint(prof profile.Profile, transcript string) string {
	sendsEnable := false
	for _, st := range prof.Login {
		if strings.Contains(st.Send, "{enable}") {
			sendsEnable = true
			break
		}
	}
	if !sendsEnable {
		return ""
	}
	tail := transcript
	if len(tail) > 200 {
		tail = tail[len(tail)-200:]
	}
	if !reAtPassword.MatchString(reEscape.ReplaceAllString(tail, "")) {
		return ""
	}
	return "enable（昇格）パスワードが受け付けられませんでした。機器の編集画面の enable パスワード（グループ既定を使う機器はグループの既定）を確認してください"
}

var reAtPassword = regexp.MustCompile(`(?i)password:\s*\z`)

// dialTransport opens the device's transport: the serial port, the direct
// SSH/Telnet connection, or — behind a jump-host chain — the connection to
// the first hop. Nothing is read or sent; the caller owns the session.
func (r *Runner) dialTransport(ctx context.Context, dev *model.Device, bastions []model.Bastion, connTimeout time.Duration, emit EmitFunc) (session.Session, error) {
	if err := missingHopLogin(dev, bastions); err != nil {
		return nil, err
	}
	var sess session.Session
	var err error
	if len(bastions) > 0 && dev.Tunneled() {
		r.emit(emit, dev.Name, PhaseConnecting, fmt.Sprintf("踏み台%d段経由（SSH トンネル） %s", len(bastions), dev.Host))
		sess, err = dialCtx(ctx, func() (session.Session, error) {
			return session.DialTunnel(bastions, dev, connTimeout, r.dialOpts(dev))
		})
	} else if len(bastions) > 0 {
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
			return nil, fmt.Errorf("%w — %s", err, errors.New(hint))
		}
		// Otherwise the plain reason first ("dial tcp 192.0.2.11:22: i/o
		// timeout" told a first-time user nothing), the raw error after it.
		if hint := dialHint(err, dev, bastions); hint != "" {
			return nil, fmt.Errorf("%s（%w）", hint, err)
		}
		return nil, err
	}
	return sess, nil
}

// dialHint says in plain words why a network connection could not be
// opened, and what to check. "" for a serial port, and for failures that
// already carry their own explanation (authentication, ciphers, host key).
func dialHint(err error, dev *model.Device, bastions []model.Bastion) string {
	if dev.Conn == model.ConnSerial && len(bastions) == 0 {
		return ""
	}
	target := dev.Host
	if len(bastions) > 0 && !dev.Tunneled() {
		target = fmt.Sprintf("踏み台1段目（%s）", bastions[0].Host)
	} else if len(bastions) > 0 {
		target = fmt.Sprintf("踏み台経由の %s", dev.Host)
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "i/o timeout") || strings.Contains(s, "timed out") || errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("%s に接続できません（応答がありません）。アドレスとポート、この PC からその機器へ通信できるか（VPN・踏み台の要否）を確認してください", target)
	case strings.Contains(s, "connection refused") || strings.Contains(s, "actively refused"):
		return fmt.Sprintf("%s に接続を拒否されました。ポート番号と、機器側で SSH / Telnet が有効か（アクセス制限がないか）を確認してください", target)
	case strings.Contains(s, "no such host") || strings.Contains(s, "lookup ") || strings.Contains(s, "GetAddrInfoW"):
		return fmt.Sprintf("%s のホスト名を解決できません。アドレスのつづりと DNS を確認してください", target)
	case strings.Contains(s, "unreachable") || strings.Contains(s, "no route"):
		return fmt.Sprintf("%s へのネットワーク経路がありません。この PC のネットワーク（VPN・踏み台の要否）を確認してください", target)
	}
	return ""
}

// dryRunOutput is what a failed 接続確認のみ shows for the device: the end
// of the transcript, escape sequences out, passwords masked.
func dryRunOutput(exp *expecter, dev *model.Device) string {
	if exp == nil {
		return ""
	}
	out := reEscape.ReplaceAllString(exp.Transcript(), "")
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, "\r", "\n")
	if r := []rune(out); len(r) > 6000 {
		out = "…" + string(r[len(r)-6000:])
	}
	return RedactSecrets(out, dev)
}

// missingHopLogin refuses an SSH hop this PC logs into itself (the first
// one, or every one through a tunnel) that has no username — what a device
// list shared without jump-host logins looks like before the user has
// registered their own (自分の踏み台認証). An SSH login as "" would only
// fail later as a bare authentication error.
func missingHopLogin(dev *model.Device, bastions []model.Bastion) error {
	// Hops are numbered as their editors show them: the own jump hosts in
	// Settings, the device's own from 1 after them.
	mine := 0
	for _, b := range bastions {
		if b.Mine {
			mine++
		}
	}
	for i, b := range bastions {
		if i > 0 && !dev.Tunneled() {
			break
		}
		if b.Mine && b.Host == "" {
			return errors.New("「自分の踏み台を使う」機器ですが、自分の踏み台が登録されていません。設定タブの「自分の踏み台」に登録してください")
		}
		if b.Mine && b.Method == model.BastionSSH && b.Username == "" {
			return fmt.Errorf("自分の踏み台%d段目のユーザー名がありません。設定タブの「自分の踏み台」に入力してください", i+1)
		}
		if b.Method == model.BastionSSH && b.Username == "" {
			return fmt.Errorf("踏み台%d段目（%s）のユーザー名がありません。機器の踏み台の欄に入力してください", i+1-mine, b.Host)
		}
	}
	return nil
}

// Open dials the device's transport and returns the expecter untouched:
// nothing is typed, no prompt is awaited, no jump host is traversed. It is
// the interactive terminal's 手動接続 — a plain console the way Tera Term
// opens one — for the situations the OS profile cannot anticipate: a
// factory-fresh device on the serial port still in its initial setup
// dialog, a device with no password yet, a first login that forces a
// password change, a prompt no profile knows. The caller owns exp and must
// Close it.
//
// Behind a jump-host chain only the first hop is connected; the user types
// the rest of the way, which is the point of the mode. Through an SSH tunnel
// there is nothing to type on the hops: the console opens on the device.
func (r *Runner) Open(ctx context.Context, dev *model.Device, s model.Settings) (*expecter, error) {
	connTimeout := time.Duration(s.ConnectTimeout) * time.Second
	if connTimeout <= 0 {
		connTimeout = 20 * time.Second
	}
	sess, err := r.dialTransport(ctx, dev, dev.ActiveBastions(), connTimeout, nil)
	if err != nil {
		return nil, err
	}
	return newExpecter(sess), nil
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

// StartInteractiveShown is StartInteractive for a session whose output so
// far is already on screen (ConnectTee, or a failed login being taken
// over): that part goes to logged only (see expecter.StartRawShown).
func (r *Runner) StartInteractiveShown(exp *expecter, sink, logged func([]byte), onClose func()) (send func(string) error, resize func(cols, rows int) error, closeFn func() error) {
	exp.mu.Lock()
	exp.onClose = onClose
	exp.mu.Unlock()
	exp.StartRawShown(sink, logged)
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
// advancing through any pager prompts in the output. A non-empty wait (the
// command's 待つ文字列) replaces the prompt: the wait ends once the line the
// device is on contains it.
func (r *Runner) sendCommand(ctx context.Context, exp *expecter, prof profile.Profile, text, wait string, timeout time.Duration) error {
	// Clear stale prompts so we wait for the prompt that follows THIS command,
	// not one left buffered by the previous step.
	exp.Drain()
	// Not the text: it is the expanded command, which may carry a password
	// ({enable} on its own line), and this error goes to the run table, the
	// history file and the failed-devices CSV. The caller names the command.
	if err := exp.Send(text); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if wait != "" {
		return r.waitTailAnswering(ctx, exp, prof, waitTail(wait), timeout, prof.Answers)
	}
	return r.waitPromptAnswering(ctx, exp, prof, timeout, prof.Answers)
}

// waitTail is the pattern for a command's 待つ文字列: the text, literally,
// at the end of the last (still open) line of the output — the same rule as
// the OS profile's prompt (see waitPromptAnswering). Anywhere on that line
// was too loose: "#" matched NEC IX's "PDEV#" column header while
// "show processes" was still printing, and the next command went too soon.
func waitTail(wait string) string {
	return regexp.QuoteMeta(wait) + `[ \t]*\z`
}

// maxAnswers bounds the questions answered for one command: a device that
// keeps asking (or an Expect that matches its own echo) must not loop.
const maxAnswers = 5

// rejectMarkers are what devices print when they refuse a command, matched
// case-insensitively on the output lines after the echoed command.
var rejectMarkers = []string{
	"% invalid", "invalid input detected", "invalid command", "% incomplete command",
	"% ambiguous command", "% unknown command", "% unrecognized command", "unknown command",
	"unrecognized command", "command not found", "command fail", "syntax error", "% error",
}

// rejectedLine returns the first line of a command's output that says the
// device refused it, or "". The first line is the echoed command itself and
// is skipped (a command may well contain the word "error").
//
// Only the first few lines after the echo count: a refusal is the device's
// immediate answer ("^" marker line, then "% Invalid input …"), while a long
// output such as show logging or journalctl can quote the same words from
// history and is not a refusal.
func rejectedLine(out string) string {
	const firstLines = 3
	seen := 0
	for i, l := range strings.Split(strings.ReplaceAll(out, "\r", "\n"), "\n") {
		if i == 0 || strings.TrimSpace(l) == "" {
			continue
		}
		if seen++; seen > firstLines {
			break
		}
		low := strings.ToLower(l)
		for _, m := range rejectMarkers {
			if strings.Contains(low, m) {
				return strings.TrimSpace(l)
			}
		}
	}
	return ""
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
	return r.waitPromptAnswering(ctx, exp, prof, timeout, nil)
}

// waitPromptAnswering is waitPrompt that also replies to the questions in
// answers (Profile.Answers) when one is the tail of the output.
func (r *Runner) waitPromptAnswering(ctx context.Context, exp *expecter, prof profile.Profile, timeout time.Duration, answers []profile.Step) error {
	// The prompt counts only as the TAIL of what has arrived. Matched anywhere
	// in the stream, a prompt character inside a command's own output ends the
	// wait early: NEC IX's "show processes" prints a "PDEV#" column header, so
	// the run moved on mid-output and typed the next command into a device that
	// was still paging — which ate its first character, turning "show logging"
	// into "how logging".
	return r.waitTailAnswering(ctx, exp, prof, "(?:"+prof.Prompt+`)[ \t]*\z`, timeout, answers)
}

// waitTailAnswering waits until tail (a pattern anchored at the end of the
// output) matches, paging and answering the way waitPromptAnswering
// describes.
func (r *Runner) waitTailAnswering(ctx context.Context, exp *expecter, prof profile.Profile, tail string, timeout time.Duration, answers []profile.Step) error {
	answered := 0
	more := prof.MorePrompt
	if more == "" {
		more = genericMore
	}
	deadline := time.Now().Add(timeout)
	// A device that prints something on its own after the prompt leaves the
	// tail on that line instead: ALAXALA AX logs "… Local authentication
	// succeeded." right after the first "#", and syslog-to-terminal does the
	// same on many others. The device is idle and ready, yet the wait here
	// would run out. So after a second of silence with no pager showing, a
	// bare Enter asks for the prompt again — twice at most, spaced apart, so
	// a device that is genuinely busy is not spammed.
	nudges := 0
	var nudgedAt time.Time
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
		// A question the profile answers ("Destination filename
		// [startup-config]?"): reply once it has stayed the tail for a beat,
		// then wait on, clean, for whatever comes next.
		if a, ok := pendingAnswer(exp, answers); ok && answered < maxAnswers {
			time.Sleep(promptSettle)
			if a2, ok2 := pendingAnswer(exp, answers); ok2 && a2 == a {
				answered++
				exp.Drain()
				if a.SendRaw != "" {
					_ = exp.SendRaw(a.SendRaw)
				} else {
					_ = exp.Send(a.Send)
				}
				continue
			}
		}
		// Not idle. If a pager is showing, advance it; the short wait doubles
		// as this loop's poll interval. The marker and the last page can land
		// in one read, so re-check first: a space typed at a device that has
		// already returned to its prompt is echoed into the next command line.
		if err := exp.Expect(ctx, more, 150*time.Millisecond); err == nil && !exp.Seen(tail) {
			_ = exp.SendRaw(" ")
			continue
		}
		// Not when the device is asking something ("…[]?", "Password:"): an
		// Enter there answers it — and aborts a copy waiting for an address.
		// Nor at a confirmation the profile leaves unanswered ("[confirm]",
		// "[yes/no]", "(y/n)"): removing [confirm] from 確認への自動応答 must
		// mean it is not confirmed — the nudge's Enter used to confirm it anyway.
		if nudges < 2 && exp.Idle() >= promptNudgeAfter && time.Since(nudgedAt) >= promptNudgeAfter && !exp.Seen(reQuestionTail) {
			nudges++
			nudgedAt = time.Now()
			_ = exp.Send("")
		}
	}
}

// pendingAnswer returns the answer whose (quoted) Expect is the tail of
// what has arrived.
//
// An answer that is just Enter means "take the default the device offers";
// a question offering none ("Address or name of remote host []?") is left
// alone — Enter there would only abort the command, so it waits for the
// command timeout like any question without an answer.
func pendingAnswer(exp *expecter, answers []profile.Step) (profile.Step, bool) {
	for _, a := range answers {
		if a.Expect == "" || !exp.Seen("(?:"+a.Expect+`)[ \t]*\z`) {
			continue
		}
		if a.Send == "" && a.SendRaw == "" && exp.Seen(`\[\]\?[ \t]*\z`) {
			continue
		}
		return a, true
	}
	return profile.Step{}, false
}

// lastShown is what a failed connection adds to its error: the line the
// device (or jump host) was showing, so a 接続確認 that stops says where —
// at a hop's shell, at "Password:", at a banner — without opening the
// device in manual. Empty when nothing was received, when the run was
// stopped, or when msg already quotes it.
func lastShown(ctx context.Context, exp *expecter, dev *model.Device, msg string) string {
	if exp == nil || ctx.Err() != nil || strings.Contains(msg, "機器の最後の表示") {
		return ""
	}
	// Masked before it is cut: a password across the cut would show its half.
	last := lastLine(RedactSecrets(exp.Transcript(), dev))
	if last == "" {
		return ""
	}
	return fmt.Sprintf("（機器の最後の表示: %q）", last)
}

// reEscape matches terminal escape sequences (colors, cursor moves, title
// and charset switches), which mean nothing in an error message.
var reEscape = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[()][0-9A-Za-z]|[=>78])`)

// lastLine is the last non-blank line of out, trimmed and cut to 100 runes
// (for an error message), escape sequences removed.
func lastLine(out string) string {
	out = reEscape.ReplaceAllString(out, "")
	lines := strings.Split(strings.ReplaceAll(out, "\r", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if r := []rune(l); len(r) > 100 {
				l = string(r[:100]) + "…"
			}
			return l
		}
	}
	return ""
}

// reQuestionTail is output ending in a question: no nudge Enter there.
const reQuestionTail = `(?i)([?:]|\[confirm\]|\[y(es)?/no?\]|\(y(es)?/no?\))[ \t]*\z`

// promptNudgeAfter is how long the far end must stay silent, with no prompt
// at the tail, before waitPrompt types a bare Enter to get the prompt back.
const promptNudgeAfter = time.Second

// expectAuth waits for a credential prompt and gives up early — the step is
// then skipped, as it is after the full wait — once the device has visibly
// moved past it:
//
//   - the next credential prompt is already on screen (SSH through a jump
//     host asks for no username, only "password:"), or
//   - for any step but the first, the operational prompt is there: at the
//     tail, or anywhere once the device has been silent for a second (ALAXALA
//     AX follows its first "#" with a log line of its own).
//
// The first step gets only the first rule: the buffer still holds the login
// banner then, and banners are full of "#". Each skipped wait was three
// seconds; a jump-host login to an ALAXALA had two of them.
func (r *Runner) expectAuth(ctx context.Context, exp *expecter, pattern, next, ready string, pastFirst bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	tail := ""
	if ready != "" {
		tail = "(?:" + ready + `)[ \t]*\z`
	}
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
			return err // context cancelled or closed
		}
		if next != "" && exp.Seen(next) {
			return ErrExpectTimeout
		}
		if !pastFirst || tail == "" {
			continue
		}
		if exp.Seen(tail) {
			time.Sleep(promptSettle)
			if exp.Seen(tail) {
				return ErrExpectTimeout
			}
			continue
		}
		if exp.Seen(ready) && exp.Idle() >= promptNudgeAfter {
			return ErrExpectTimeout
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
	for i, st := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if skipCreds && st.Credential() {
			continue // transport-level auth already done (direct SSH)
		}
		if st.Expect != "" {
			var (
				ready bool
				err   error
			)
			if st.Auth() {
				// Auth steps keep a short wait, cut shorter still once the
				// device has visibly moved past the prompt — see expectAuth.
				next := ""
				if i+1 < len(steps) && steps[i+1].Auth() {
					next = steps[i+1].Expect
				}
				err = r.expectAuth(ctx, exp, st.Expect, next, readyPrompt, i > 0, min(timeout, 3*time.Second))
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

// SecretsOf lists the passwords a transcript of dev must never show: its own,
// its enable password, and its bastions'.
func SecretsOf(dev *model.Device) []string {
	secrets := []string{dev.Password, dev.EnablePassword, dev.KeyPassphrase}
	for _, b := range dev.ActiveBastions() {
		secrets = append(secrets, b.Password, b.KeyPassphrase)
	}
	return secrets
}

// RedactSecrets masks the device's passwords (and its bastions') wherever
// they appear in transcript text — some devices echo input back, and a
// leaked prompt echo must not end up in a plaintext log file. Very short
// passwords are left alone: masking e.g. every "a" would destroy the log.
func RedactSecrets(text string, dev *model.Device) string {
	for _, sec := range SecretsOf(dev) {
		if len(sec) >= 4 {
			text = strings.ReplaceAll(text, sec, "****")
		}
	}
	return text
}

// liveLog is a running device's log: the file claimed for it and the
// stream filling it.
type liveLog struct {
	path   string
	stream *logstore.Stream
}

// tag adds the log's path to the device's progress events.
func (l *liveLog) tag(emit EmitFunc) EmitFunc {
	if emit == nil {
		return nil
	}
	return func(ev Event) {
		ev.LogPath = l.path
		emit(ev)
	}
}

// startLog claims the device's log file and starts its stream (nil when the
// file cannot be created: saveLog then tries again at the end and reports).
// Masking follows the setting, as saveLog's RedactSecrets does.
func (r *Runner) startLog(dev *model.Device, s model.Settings, runDir string, now time.Time, stage model.Stage) *liveLog {
	path, err := logstore.Claim(runDir, s.LogNameTemplate, logFields(dev, s, stage), now)
	if err != nil {
		return nil
	}
	var secrets []string
	if s.MaskLogSecrets {
		secrets = SecretsOf(dev)
	}
	l := &liveLog{path: path, stream: logstore.NewStream(path, secrets)}
	r.live.Store(path, l.stream)
	return l
}

// finishLog closes the device's live log (or, without one, writes the
// transcript the old way). A file nothing was written to is removed: a
// device that never answered leaves no empty log, as before.
func (r *Runner) finishLog(res *DeviceResult, l *liveLog, dev *model.Device, s model.Settings, runDir string, now time.Time, stage model.Stage) {
	if l == nil {
		r.saveLog(res, dev, s, runDir, now, stage)
		return
	}
	r.live.Delete(l.path)
	p, err := l.stream.Close()
	if err != nil {
		msg := "ログを保存できませんでした: " + err.Error()
		if res.Error == "" {
			res.Error = msg
		} else {
			res.Error += "／" + msg
		}
	}
	if p == "" {
		_ = os.Remove(l.path)
		return
	}
	res.LogPath = p
}

func logFields(dev *model.Device, s model.Settings, stage model.Stage) logstore.Fields {
	return logstore.Fields{
		Host: dev.Name, IP: dev.Host, OS: dev.OSType, Group: dev.Group, Site: dev.Site, Role: dev.Role, Stage: string(stage), Tokens: s.StageTokens(),
	}
}

func (r *Runner) saveLog(res *DeviceResult, dev *model.Device, s model.Settings, runDir string, now time.Time, stage model.Stage) {
	if res.Transcript == "" {
		return
	}
	text := res.Transcript
	if s.MaskLogSecrets {
		text = RedactSecrets(text, dev)
	}
	path, err := logstore.Write(runDir, s.LogNameTemplate, logFields(dev, s, stage), text, now)
	if err != nil {
		// A run whose log could not be written (disk full, a file locked by
		// another program, a template producing a bad name) did not do its
		// job: say so instead of showing 完了 with nothing behind it.
		msg := "ログを保存できませんでした: " + err.Error()
		if res.Error == "" {
			res.Error = msg
		} else {
			res.Error += "／" + msg
		}
		return
	}
	res.LogPath = path
}

// DryRunOKMsg is the completion message of a device that passed a dry run
// (the UI translates it).
const DryRunOKMsg = "接続・ログイン OK（コマンドは送っていません）"

// RunBatch runs all enabled devices, bounded by Settings.MaxParallel.
func (r *Runner) RunBatch(ctx context.Context, inv *model.Inventory, emit EmitFunc) []DeviceResult {
	res, _ := r.RunBatchOpts(ctx, inv, Options{}, emit)
	return res
}

// RunBatchOpts is RunBatch with per-run options. It also returns the run
// folder the logs went to ("" for a dry run, which writes nothing).
func (r *Runner) RunBatchOpts(ctx context.Context, inv *model.Inventory, opts Options, emit EmitFunc) ([]DeviceResult, string) {
	now := time.Now()
	// A real run claims its folder now (created, so a concurrent run or
	// terminal cannot take the same name); a dry run writes nothing.
	runDir := logstore.RunDir(inv.Settings.LogDir, inv.Settings.LogDirTemplate, opts.Group, string(opts.Stage), inv.Settings.StageTokens(), now)
	switch {
	case opts.Dir != "" && !opts.DryRun:
		runDir = opts.Dir
		_ = os.MkdirAll(runDir, 0o755) // a failure shows as each device's log error
	case !opts.DryRun:
		runDir = logstore.ClaimRunDir(inv.Settings.LogDir, inv.Settings.LogDirTemplate, opts.Group, string(opts.Stage), inv.Settings.StageTokens(), now)
	}
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
		return nil, ""
	}
	sem := make(chan struct{}, limit)
	results := make([]DeviceResult, len(targets))
	var wg sync.WaitGroup
	r.devMu.Lock()
	r.devStop = map[string]bool{}
	r.devMu.Unlock()
	defer func() {
		r.devMu.Lock()
		r.devStop = nil
		r.devMu.Unlock()
	}()

	for i, dev := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, d *model.Device) {
			defer wg.Done()
			defer func() { <-sem }()
			// A device whose turn comes after 中止 is reported as never run,
			// not as an error: nothing was attempted against it.
			if ctx.Err() != nil {
				results[idx] = DeviceResult{Device: d.Name, Canceled: true, Error: CanceledNotRunMsg}
				r.emit(emit, d.Name, PhaseCanceled, CanceledNotRunMsg)
				return
			}
			dctx, release, stopped := r.claimDevice(ctx, d.Name)
			defer release()
			if stopped {
				results[idx] = DeviceResult{Device: d.Name, Canceled: true, Error: CanceledNotRunMsg}
				r.emit(emit, d.Name, PhaseCanceled, CanceledNotRunMsg)
				return
			}
			var set *model.CommandSet
			if cs, ok := sets[d.CommandSet]; ok {
				set = cs
			}
			res := r.runDevice(dctx, d, set, inv.Settings, runDir, now, emit, opts)
			if res.Canceled && ctx.Err() == nil {
				// Stopped alone: say so, the batch's 中止 was not pressed.
				res.Error = CanceledDeviceMsg
				if emit != nil {
					emit(Event{Device: d.Name, Phase: PhaseCanceled, Message: res.Error, LogPath: res.LogPath})
				}
			}
			results[idx] = res
		}(i, dev)
	}
	wg.Wait()
	if opts.DryRun {
		return results, ""
	}
	return results, runDir
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
