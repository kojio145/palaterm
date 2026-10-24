package runner

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// Regressions for the 2026-10-24 review, internal/runner. Every value is a
// lab one (192.0.2.x, ops, jump1).

// twoHops is a jump chain of two SSH hops (the first already authenticated
// at the transport, as DialBastionHead leaves it) in front of a Telnet
// device, driven through traverseBastions by a scripted line.
func twoHops(t *testing.T, second model.Bastion) (*scriptedDevice, *expecter, chan error) {
	t.Helper()
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	first := model.Bastion{Host: "192.0.2.10", Method: model.BastionSSH, Username: "ops", Password: "pw"}
	target := &model.Device{Name: "lab-sw1", Host: "192.0.2.21", Conn: model.ConnTelnet, Username: "ops"}
	done := make(chan error, 1)
	go func() {
		_, err := (&Runner{}).traverseBastions(context.Background(), exp, []model.Bastion{first, second}, target, 10*time.Second)
		done <- err
	}()
	dev.say("jump1#")
	return dev, exp, done
}

func hopResult(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("traverseBastions did not return")
		return nil
	}
}

// A jump to the second hop that fails late — after the moments jumpSeen
// watches, while the hop's login prompt is awaited — leaves the line on the
// first hop's shell: AtJumpHost, not AtJumpLogin (the login wrapper took it
// for a stop at the hop's login prompt).
func TestLateJumpFailureBetweenHopsIsAtJumpHost(t *testing.T) {
	dev, exp, done := twoHops(t, model.Bastion{Host: "192.0.2.11", Method: model.BastionSSH, Username: "ops", Password: "pw"})
	defer exp.Close()
	if got := dev.next(t); !strings.HasPrefix(got, "ssh ops@192.0.2.11") {
		t.Fatalf("sent %q, want the jump command", got)
	}
	time.Sleep(3500 * time.Millisecond) // past jumpSeen's window
	dev.say("\r\nssh: connect to host 192.0.2.11 port 22: Connection timed out\r\njump1#")
	err := hopResult(t, done)
	if err == nil || !strings.Contains(err.Error(), "Connection timed out") {
		t.Fatalf("want the hop's words, got %v", err)
	}
	wrapped := &hopError{err} // as connect returns it
	if !AtJumpHost(wrapped) || AtJumpLogin(wrapped) {
		t.Fatalf("a late jump failure: AtJumpHost=%v AtJumpLogin=%v (%v)", AtJumpHost(wrapped), AtJumpLogin(wrapped), err)
	}
}

// A hop entered with a key on the previous hop, whose key the hop refuses
// and whose ssh falls back to a password prompt, is stopped at that prompt:
// AtJumpLogin, and the error says what happened.
func TestKeyedHopFallingBackToPasswordIsAtJumpLogin(t *testing.T) {
	dev, exp, done := twoHops(t, model.Bastion{Host: "192.0.2.11", Method: model.BastionSSH, Username: "ops", AuthMethod: model.AuthPublicKey, KeyPassphrase: "pp"})
	defer exp.Close()
	dev.next(t) // the jump command
	dev.say("Enter passphrase for key '/home/ops/.ssh/id_ed25519': ")
	if got := dev.next(t); got != "pp\r\n" {
		t.Fatalf("sent %q, want the passphrase", got)
	}
	dev.say("\r\nops@192.0.2.11's password: ")
	start := time.Now()
	err := hopResult(t, done)
	if err == nil || !strings.Contains(err.Error(), "鍵でのログインを受け付けず") {
		t.Fatalf("want the fallback named, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v: the password prompt should end the wait", time.Since(start))
	}
	wrapped := &hopError{err}
	if !AtJumpLogin(wrapped) || AtJumpHost(wrapped) {
		t.Fatalf("a key refused: AtJumpLogin=%v AtJumpHost=%v", AtJumpLogin(wrapped), AtJumpHost(wrapped))
	}
}

// A hop that lets the previous hop's key (or agent) straight in greets with
// "Last login: …" and its shell prompt: no user name is typed into that
// shell, and the next jump follows at once.
func TestHopEnteredWithKeyNeedsNoLogin(t *testing.T) {
	dev, exp, done := twoHops(t, model.Bastion{Host: "192.0.2.11", Method: model.BastionSSH, Username: "ops", Password: "pw"})
	defer exp.Close()
	dev.next(t) // the jump command
	start := time.Now()
	dev.say("Last login: Tue Oct 20 09:00:00 2026 from 192.0.2.9\r\n[ops@jump2 ~]$ ")
	got := dev.next(t)
	if !strings.HasPrefix(got, "telnet 192.0.2.21") {
		t.Fatalf("sent %q after the hop's shell prompt, want the jump to the device", got)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("the jump to the device took %v", time.Since(start))
	}
	dev.say("Trying 192.0.2.21...\r\nConnected to 192.0.2.21.\r\n\r\nlogin: ")
	if err := hopResult(t, done); err != nil {
		t.Fatalf("traverse: %v", err)
	}
}

// An ssh hop whose password is wrong asks for it again: the login is
// reported refused as soon as the prompt shows, not as a missing shell
// prompt after the timeout.
func TestLoginHopFromRefusedPasswordSaysSo(t *testing.T) {
	dev, exp := atHop(t, "jump1#")
	defer exp.Close()
	from := landing{target: "192.0.2.12", prompt: currentLine(exp.Transcript()), mark: exp.Mark()}
	go func() {
		dev.say("ops@192.0.2.12's password: ")
		<-dev.sent // the password
		dev.say("\r\nPermission denied, please try again.\r\nops@192.0.2.12's password: ")
	}()
	start := time.Now()
	err := loginHopFrom(context.Background(), exp, model.Bastion{Host: "192.0.2.12", Username: "ops", Password: "wrong"}, from, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "ログインを拒否") {
		t.Fatalf("want the refusal, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v: the second password prompt should end the wait", time.Since(start))
	}
}

// A login or password prompt is the line the hop is on, not the words in
// a greeting or a banner.
func TestCredentialPromptIsTheTail(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"\r\nlogin: ", true},
		{"User Access Verification\r\n\r\nUsername: ", true},
		{"ops@192.0.2.12's password: ", true},
		{"Last login: Tue Oct 20 09:00:00 2026 from 192.0.2.9\r\n", false},
		{"Last login: Tue Oct 20 09:00:00 2026 from 192.0.2.9\r\n[ops@jump2 ~]$ ", false},
		{"Your password: expires in 3 days\r\n", false},
	} {
		dev, sess := newScriptedDevice()
		exp := newExpecter(sess)
		dev.say(c.text)
		waitSeen(context.Background(), exp, `\S`, time.Second)
		time.Sleep(50 * time.Millisecond)
		if got := exp.Seen(reCredPrompt); got != c.want {
			t.Errorf("%q: credential prompt = %v, want %v", c.text, got, c.want)
		}
		exp.Close()
	}
}

// A session that ended before the terminal took it over still reports its
// end: onClose is called at once, after the transcript reached the sink.
func TestStartInteractiveOnEndedSession(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	dev.say("R1#")
	waitSeen(context.Background(), exp, `R1#`, 2*time.Second)
	dev.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !exp.Closed() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !exp.Closed() {
		t.Fatal("the session did not close")
	}
	var mu sync.Mutex
	var order []string
	sink := func(b []byte) { mu.Lock(); order = append(order, "sink:"+string(b)); mu.Unlock() }
	onClose := func() { mu.Lock(); order = append(order, "close"); mu.Unlock() }
	_, _, closeFn := New(testRegistry()).StartInteractive(exp, sink, onClose)
	defer closeFn()
	mu.Lock()
	got := strings.Join(order, " ")
	mu.Unlock()
	if got != "sink:R1# close" {
		t.Fatalf("order %q, want the transcript then onClose", got)
	}
}

// A panic in the interactive sink ends the session the same way as any
// other end: the terminal window hears of it through onClose.
func TestReadLoopPanicInSinkCallsOnClose(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	closed := make(chan struct{})
	_, _, closeFn := New(testRegistry()).StartInteractive(exp, func([]byte) { panic("sink") }, func() { close(closed) })
	defer closeFn()
	go dev.say("R1#")
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("onClose was not called after the sink panicked")
	}
	if !exp.Closed() {
		t.Error("the session is not closed")
	}
}

// A Disconnect step's SendRaw reaches the device as it is, no line ending.
func TestDisconnectSendRawHasNoLineEnding(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	prof := profile.Profile{Prompt: "#", Disconnect: []profile.Step{{SendRaw: "\x04"}}}
	go New(profile.NewRegistry()).disconnect(context.Background(), exp, prof, profile.Vars{}, 0)
	if got := dev.next(t); got != "\x04" {
		t.Fatalf("sent %q, want the raw byte alone", got)
	}
}

// A line that drops during the pager-off command fails the connection; it
// is not a warning about the command, and no command follows.
func TestPagerOnDroppedLineFailsTheConnection(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	prof := profile.Profile{Prompt: "#", Pager: []profile.Step{{Send: "terminal length 0"}}}
	go func() {
		<-dev.sent // the pager command
		dev.Close()
	}()
	device := &model.Device{Name: "lab-sw1", Host: "192.0.2.21"}
	warn, err := New(profile.NewRegistry()).disablePager(context.Background(), exp, prof, profile.Vars{}, device, nil, 5*time.Second)
	if !errors.Is(err, ErrClosed) || warn != "" {
		t.Fatalf("warn %q, err %v; want ErrClosed and no warning", warn, err)
	}
}

// A batch with nothing to run claims no folder: an empty log_* showed in
// 実行履歴 as a run of no devices.
func TestNoTargetsLeavesNoRunFolder(t *testing.T) {
	inv := &model.Inventory{Settings: model.DefaultSettings()}
	inv.Settings.LogDir = t.TempDir()
	inv.Devices = []model.Device{
		{Name: "a", Host: "192.0.2.41", Conn: model.ConnTelnet, OSType: "generic", Enabled: false},
	}
	res, dir := New(testRegistry()).RunBatchOpts(context.Background(), inv, Options{}, nil)
	if len(res) != 0 || dir != "" {
		t.Fatalf("%d results, dir %q", len(res), dir)
	}
	entries, err := os.ReadDir(inv.Settings.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the log folder holds %d entries after a run of no devices", len(entries))
	}
}

// A device whose password prompt comes late (an AAA server answering after
// a few seconds) still gets the password: the step waits while the device
// is working, and skips only once it is idle at some other line.
func TestAuthPromptAfterSlowAAA(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	steps := []profile.Step{
		{Expect: "sername:", Send: "{user}"},
		{Expect: "assword:", Send: "{password}"},
	}
	vars := profile.Vars{User: "ops", Password: "pw"}
	done := make(chan error, 1)
	go func() {
		done <- New(profile.NewRegistry()).runSteps(context.Background(), exp, steps, vars, 15*time.Second, false, "#")
	}()
	dev.say("Username: ")
	if got := dev.next(t); got != "ops\r\n" {
		t.Fatalf("sent %q, want the user name", got)
	}
	time.Sleep(3500 * time.Millisecond) // the AAA server thinking
	dev.say("Password: ")
	if got := dev.next(t); got != "pw\r\n" {
		t.Fatalf("sent %q, want the password", got)
	}
	dev.say("\r\nlab-sw1#")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runSteps did not return")
	}
}

// A device idle at a line that is not the step's prompt still skips the
// step after the short wait (a console at a shell someone left logged in).
func TestAuthPromptSkippedAtAnIdleLine(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	steps := []profile.Step{{Expect: "sername:", Send: "{user}"}}
	dev.say("lab-sw1>")
	waitSeen(context.Background(), exp, `>`, time.Second)
	start := time.Now()
	err := New(profile.NewRegistry()).runSteps(context.Background(), exp, steps, profile.Vars{User: "ops"}, 30*time.Second, false, "#")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > authWait+2*time.Second {
		t.Fatalf("the skip took %v", d)
	}
}

// Both login paths (a step timing out, the prompt not coming after the last
// step) explain the failure the same way, the prompt mismatch included.
func TestLoginFailureNamesThePromptMismatch(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	dev.say("Welcome\r\nlab-sw1% ")
	waitSeen(context.Background(), exp, `% $`, time.Second)
	prof := profile.Profile{Name: "NEC IX", Prompt: "#"}
	device := &model.Device{Name: "lab-sw1", Host: "192.0.2.21", Conn: model.ConnTelnet}
	for _, what := range []string{"login", "login: waiting for prompt"} {
		err := loginFailure(ErrExpectTimeout, what, nil, device, prof, exp)
		if err == nil || !strings.Contains(err.Error(), "OSタイプ") {
			t.Errorf("%s: %v", what, err)
		}
	}
	dev.say("\r\nlab-sw1#")
	waitSeen(context.Background(), exp, `#$`, time.Second)
	err := loginFailure(ErrExpectTimeout, "login", nil, device, prof, exp)
	if err == nil || !strings.HasPrefix(err.Error(), "login: ") {
		t.Errorf("at the profile's own prompt: %v", err)
	}
}

// Patterns are compiled once: Expect and Seen poll on the same few.
func TestCompilePatternCaches(t *testing.T) {
	a, err := compilePattern(`lab-[a-z]+#`)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := compilePattern(`lab-[a-z]+#`)
	if a != b {
		t.Error("the same pattern was compiled twice")
	}
	if _, err := compilePattern(`(`); err == nil {
		t.Error("a bad pattern compiled")
	}
	if _, ok := reCache.Load(`(`); ok {
		t.Error("a bad pattern was cached")
	}
}
