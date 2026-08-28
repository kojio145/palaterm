// Package runner orchestrates connecting to devices, running their login
// automation and command sets, and saving logs — one device at a time or many
// in parallel.
package runner

import (
	"context"
	"fmt"
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
				if !sleepCtx(ctx, time.Duration(p)*time.Second) {
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
	_ = r.runSteps(ctx, exp, prof.Disconnect, vars, 3*time.Second, false)
	for range dev.ActiveBastions() {
		_ = exp.Send("exit")
		time.Sleep(150 * time.Millisecond)
	}

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
		return nil, prof, err
	}
	exp := newExpecter(sess)

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
	if err := r.runSteps(ctx, exp, prof.Login, vars, cmdTimeout, skipCreds); err != nil {
		return exp, prof, fmt.Errorf("login: %w", err)
	}
	// Login lists need no trailing expect-only prompt row: when the last step
	// sent something (or the list is empty), wait for the operational prompt
	// here. A profile that still ends with its own expect row keeps the old
	// behavior — no second wait that would hang.
	if n := len(prof.Login); n == 0 || prof.Login[n-1].Sends() {
		if err := r.waitPrompt(ctx, exp, prof, cmdTimeout); err != nil {
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

// waitPrompt waits for the operational prompt, advancing through pager prompts.
func (r *Runner) waitPrompt(ctx context.Context, exp *expecter, prof profile.Profile, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ErrExpectTimeout
		}
		if prof.MorePrompt != "" {
			// Race the prompt against the pager marker by polling briefly.
			if err := exp.Expect(ctx, prof.Prompt, min(remaining, 800*time.Millisecond)); err == nil {
				return nil
			}
			// Not the prompt yet; if a pager is showing, advance it.
			if err := exp.Expect(ctx, prof.MorePrompt, 200*time.Millisecond); err == nil {
				_ = exp.SendRaw(" ")
				continue
			}
			continue
		}
		return exp.Expect(ctx, prof.Prompt, remaining)
	}
}

// runSteps executes a step list (login / pager / disconnect).
//
// skipCreds is true when the transport itself already authenticated (direct
// SSH): steps sending {user}/{password} are then skipped outright. On every
// transport, auth steps ({user}/{password}/{enable}) whose prompt does not
// appear within a short wait are skipped without error — that is how one
// profile serves devices that ask for none, some, or all of the credentials.
func (r *Runner) runSteps(ctx context.Context, exp *expecter, steps []profile.Step, vars profile.Vars, timeout time.Duration, skipCreds bool) error {
	for _, st := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if skipCreds && st.Credential() {
			continue // transport-level auth already done (direct SSH)
		}
		if st.Expect != "" {
			stepTimeout := timeout
			if st.Auth() {
				stepTimeout = min(timeout, 3*time.Second)
			}
			err := exp.Expect(ctx, st.Expect, stepTimeout)
			if err != nil {
				if st.Auth() {
					continue // prompt absent: device skips this question
				}
				return fmt.Errorf("waiting for %q: %w", st.Expect, err)
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
