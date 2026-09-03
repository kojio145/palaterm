package runner

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// Regressions found on the NEC IX series lab bench (2026-09-02). Each one is driven
// through an in-memory device rather than a real transport, so the behaviour
// under test is the runner's, not a server fake's.

// scriptedDevice is a device the test speaks for: sent carries what the runner
// wrote, and say() pushes bytes back at it.
type scriptedDevice struct {
	sent chan string
	w    *io.PipeWriter
	r    *io.PipeReader
}

func newScriptedDevice() (*scriptedDevice, *scriptedDevice) {
	r, w := io.Pipe()
	d := &scriptedDevice{sent: make(chan string, 32), w: w, r: r}
	return d, d
}

func (d *scriptedDevice) Read(p []byte) (int, error) { return d.r.Read(p) }

func (d *scriptedDevice) Write(p []byte) (int, error) {
	d.sent <- string(p)
	return len(p), nil
}

func (d *scriptedDevice) Close() error {
	d.w.Close()
	return d.r.Close()
}

func (d *scriptedDevice) say(s string) { _, _ = d.w.Write([]byte(s)) }

// next returns the next thing the runner sent, failing the test if nothing
// arrives.
func (d *scriptedDevice) next(t *testing.T) string {
	t.Helper()
	select {
	case s := <-d.sent:
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("runner sent nothing")
		return ""
	}
}

// takeNudge consumes one wake-up from inside a script goroutine: the
// backspaces that erase whatever was typed on the line, then the Enter that
// asks the console to speak.
func (d *scriptedDevice) takeNudge() {
	<-d.sent
	<-d.sent
}

// A second "exit" fired while the device is still leaving config mode loses
// characters: NEC IX read it as "e" + prompt + "t" and answered
// "% t -- Ambiguous command.", staying logged in with the jump host's inner
// session dangling.
func TestDisconnectWaitsForEachStepToLand(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	r := New(profile.NewRegistry())
	prof := profile.Profile{
		Prompt:     "#",
		Disconnect: []profile.Step{{Send: "exit"}, {Send: "exit"}},
	}

	done := make(chan struct{})
	go func() {
		r.disconnect(context.Background(), exp, prof, profile.Vars{}, 0)
		close(done)
	}()

	if got := dev.next(t); got != "exit\r\n" {
		t.Fatalf("first logout = %q, want %q", got, "exit\r\n")
	}
	select {
	case extra := <-dev.sent:
		t.Fatalf("second logout %q was sent before the device answered the first", extra)
	case <-time.After(300 * time.Millisecond):
	}

	dev.say("\r\nIX-B# ") // config mode left; prompt is back
	if got := dev.next(t); got != "exit\r\n" {
		t.Fatalf("second logout = %q, want %q", got, "exit\r\n")
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not return")
	}
}

// The final logout is answered by a closed connection, never a prompt, so it
// must not be preceded by a settle that can only time out.
func TestDisconnectDoesNotWaitAfterTheLastStep(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	r := New(profile.NewRegistry())
	prof := profile.Profile{Prompt: "#", Disconnect: []profile.Step{{Send: "exit"}}}

	start := time.Now()
	r.disconnect(context.Background(), exp, prof, profile.Vars{}, 0)
	if elapsed := time.Since(start); elapsed > disconnectSettle {
		t.Fatalf("single logout took %s, want well under %s", elapsed, disconnectSettle)
	}
	if got := dev.next(t); got != "exit\r\n" {
		t.Fatalf("logout = %q", got)
	}
}

// One "exit" per jump host unwinds the chain, and those are settled between
// too — the hop reprints its prompt as the inner session closes.
func TestDisconnectUnwindsOneExitPerHop(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	r := New(profile.NewRegistry())
	prof := profile.Profile{Prompt: "#"} // no profile logout steps

	done := make(chan struct{})
	go func() {
		r.disconnect(context.Background(), exp, prof, profile.Vars{}, 2)
		close(done)
	}()

	for i := 0; i < 2; i++ {
		if got := dev.next(t); got != "exit\r\n" {
			t.Fatalf("hop %d logout = %q", i+1, got)
		}
		dev.say("\r\nIX-A# ")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not return")
	}
}

// A jump host that rejects the credentials prints its login prompt again.
// Continuing regardless typed the jump command in as a username, and the run
// failed a minute later on the device's prompt with a timeout that said
// nothing about which hop actually refused us.
func TestLoginHopReportsRejectedCredentials(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	go func() {
		dev.say("login: ")
		<-dev.sent // username
		dev.say("\r\nPassword: ")
		<-dev.sent // password
		dev.say("\r\nLogin attempt failed.\r\nlogin: ")
	}()

	b := model.Bastion{Host: "192.0.2.201", Username: "u", Password: "wrong"}
	err := loginHop(context.Background(), exp, b, 500*time.Millisecond)
	if err == nil {
		t.Fatal("loginHop accepted a rejected login")
	}
	if !strings.Contains(err.Error(), "192.0.2.201") {
		t.Errorf("error %q does not name the hop that refused", err)
	}
	if !strings.Contains(err.Error(), "パスワード") {
		t.Errorf("error %q does not point at the credentials", err)
	}
}

func TestLoginHopAcceptsAShellPrompt(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	go func() {
		dev.say("login: ")
		<-dev.sent // username
		dev.say("\r\nPassword: ")
		<-dev.sent // password
		dev.say("\r\nWelcome\r\nIX-A# ")
	}()

	b := model.Bastion{Host: "192.0.2.201", Username: "u", Password: "right"}
	if err := loginHop(context.Background(), exp, b, 2*time.Second); err != nil {
		t.Fatalf("loginHop rejected a good login: %v", err)
	}
}

// A serial console says nothing to a program that only listens: its prompt was
// printed whenever it last had something to say. Without a nudge the login
// steps wait out their timeout and the run ends with an empty log.
func TestWakeSerialRetriesUntilTheConsoleAnswers(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	go func() {
		dev.takeNudge() // this console wakes on the first one and says nothing
		dev.takeNudge()
		dev.say("\r\nlogin: ")
	}()

	wakeSerial(context.Background(), exp, profile.Profile{})

	// The banner must survive: the profile's own login steps match against it.
	if !exp.Seen("ogin:") {
		t.Fatal("wakeSerial returned without the console banner intact")
	}
}

func TestWakeSerialStopsOnceTheConsoleReplies(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	go func() {
		dev.takeNudge()
		dev.say("\r\nIX-B# ")
	}()

	wakeSerial(context.Background(), exp, profile.Profile{})

	select {
	case extra := <-dev.sent:
		t.Fatalf("wakeSerial kept nudging after the console answered: %q", extra)
	default:
	}
}

// A refused password is the commonest way a run fails, and it used to surface
// as the profile's next step timing out — a message naming the step we
// happened to be on rather than the reason.
func TestBackAtLoginDetectsARefusedPassword(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
		want       bool
	}{
		{"拒否されて再びログインプロンプト", "login: admin\r\nPassword: \r\nLogin attempt failed.\r\nlogin: ", true},
		{"username表記の機器", "Username: admin\r\nPassword: \r\n% Login invalid\r\nUsername: ", true},
		{"ログイン成功後のプロンプト", "login: admin\r\nPassword: \r\nIX-B# ", false},
		// Only reached after the login steps have run, so a transcript still
		// ending at the login prompt means the credentials went nowhere.
		{"ログインプロンプトのまま進んでいない", "login: ", true},
		{"出力の途中にloginの語がある", "IX-B# show users\r\nlogin  tty  admin\r\nIX-B# ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := backAtLogin(c.transcript); got != c.want {
				t.Errorf("backAtLogin(%q) = %v, want %v", c.transcript, got, c.want)
			}
		})
	}
}

// consoleDevice is a scriptedDevice that ends lines the way a serial console
// does.
type consoleDevice struct{ *scriptedDevice }

func (c *consoleDevice) LineEnding() string { return "\r" }

// A serial console has no protocol between the keyboard and the device's line
// discipline: it takes CR and LF each as their own Enter. Sending "admin\r\n"
// therefore submitted the username on the CR and an empty line on the LF,
// which the device answered at the password prompt — so every console login
// failed with "Login attempt failed." while the same credentials worked over
// the network.
func TestSendUsesTheTransportsLineEnding(t *testing.T) {
	t.Run("既定はCRLF", func(t *testing.T) {
		dev, sess := newScriptedDevice()
		exp := newExpecter(sess)
		defer exp.Close()

		if err := exp.Send("admin"); err != nil {
			t.Fatal(err)
		}
		if got := dev.next(t); got != "admin\r\n" {
			t.Errorf("sent %q, want %q", got, "admin\r\n")
		}
	})

	t.Run("シリアルコンソールはCRのみ", func(t *testing.T) {
		base, sess := newScriptedDevice()
		console := &consoleDevice{scriptedDevice: sess}
		exp := newExpecter(console)
		defer exp.Close()

		if err := exp.Send("admin"); err != nil {
			t.Fatal(err)
		}
		if got := base.next(t); got != "admin\r" {
			t.Errorf("sent %q, want %q", got, "admin\r")
		}
	})
}

// A console is shared and has no session: whoever used it last may have typed
// a username and walked away. Answering that stale password prompt with our
// password fails, and the run then blames credentials that are fine.
func TestWakeSerialClearsAStaleLoginAttempt(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	prof := profile.Profile{
		Prompt: "#",
		Login: []profile.Step{
			{Expect: "ogin:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
		},
	}

	go func() {
		dev.takeNudge()           // the wake-up
		dev.say("\r\nPassword: ") // joined mid-login: someone else's attempt
		<-dev.sent                // the empty Enter that abandons it
		dev.say("\r\nLogin attempt failed.\r\nlogin: ")
	}()

	wakeSerial(context.Background(), exp, prof)

	if !exp.Seen("ogin:") {
		t.Fatal("wakeSerial left the console at the stale password prompt")
	}
}

// A console that really does ask for a password and nothing else is at its
// own prompt, not a stale one, and must not have it thrown away.
func TestWakeSerialKeepsAPasswordOnlyPrompt(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	prof := profile.Profile{
		Prompt: "#",
		Login:  []profile.Step{{Expect: "assword:", Send: "{password}"}}, // no username
	}

	go func() {
		dev.takeNudge()
		dev.say("\r\nPassword: ")
	}()

	wakeSerial(context.Background(), exp, prof)

	select {
	case extra := <-dev.sent:
		t.Fatalf("wakeSerial threw away a real password prompt by sending %q", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// NEC IX has no ">" privilege level: an administrator lands on "#" and a
// monitor user on "%". The login then works and every command times out on a
// prompt that will never come, which used to read as "waiting for prompt:
// expect timeout" — the code describing itself rather than the device.
func TestPromptMismatchNamesWhatTheDeviceShowed(t *testing.T) {
	prof := profile.Profile{Name: "NEC IX", Prompt: "#"}

	got := promptMismatchHint(prof, "login: mon\r\nPassword: \r\nIX-B% ")
	if !strings.Contains(got, "%") || !strings.Contains(got, "NEC IX") {
		t.Errorf("hint %q does not name the prompt seen and the OS type", got)
	}

	if got := promptMismatchHint(prof, "login: admin\r\nPassword: \r\nIX-B# "); got != "" {
		t.Errorf("the profile's own prompt was reported as a mismatch: %q", got)
	}
	if got := promptMismatchHint(prof, "login: admin\r\nPassword: \r\nsome output"); got != "" {
		t.Errorf("output that is not a prompt was reported as a mismatch: %q", got)
	}
}

// A console is joined, not opened, and the line may not be empty: someone who
// typed half a command and walked away leaves it in the input buffer, where a
// bare Enter runs it. On the bench "show ru" came back as
// "% ru -- Invalid command."; in config mode a half-typed line would be worse.
func TestWakeSerialCancelsTheLineBeforePressingEnter(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	done := make(chan struct{})
	go func() {
		wakeSerial(context.Background(), exp, profile.Profile{Prompt: "#"})
		close(done)
	}()

	if got := dev.next(t); got != eraseLine {
		t.Fatalf("first thing sent = %q, want the line erased (%q)", got, eraseLine)
	}
	if got := dev.next(t); got != "\r\n" {
		t.Fatalf("second thing sent = %q, want a bare Enter", got)
	}
	dev.say("\r\nIX-A# ")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("wakeSerial did not return once the console answered")
	}
}
