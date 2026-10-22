package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// Regressions for the 2026-10-22 review, connection handling (E1 E2 E4 E8 E9).

// atHop starts a scripted hop sitting at its prompt, the way a jump host's
// shell is left before the next jump command.
func atHop(t *testing.T, prompt string) (*scriptedDevice, *expecter) {
	t.Helper()
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	dev.say("\r\n" + prompt)
	if !waitSeen(context.Background(), exp, `\S\s*$`, 2*time.Second) {
		t.Fatal("hop prompt not read")
	}
	return dev, exp
}

// E1: a hop answering the jump with a refusal is an error naming the cause,
// not a jump that went through.
func TestJumpRefusedIsAnError(t *testing.T) {
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
		var je *jumpError
		if !errors.As(err, &je) || !strings.Contains(err.Error(), "Connection refused") || !strings.Contains(err.Error(), "ポート番号") {
			t.Fatalf("want a jump error naming the refusal and the port, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("jump did not return")
	}
}

// E1: the hop's own prompt back, with no words about it, is a failed jump.
func TestJumpBackAtHopPromptIsAnError(t *testing.T) {
	dev, exp := atHop(t, "[ops@jump1 ~]$ ")
	defer exp.Close()
	done := make(chan error, 1)
	go func() {
		done <- jumpTo(context.Background(), exp, "", model.BastionSSH, "192.0.2.22", "ops", 0, false, 5*time.Second)
	}()
	dev.next(t)
	dev.say("\r\n[ops@jump1 ~]$ ")
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "踏み台のプロンプト") {
			t.Fatalf("want an error about falling back to the hop, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("jump did not return")
	}
}

// E1: the words ssh, telnet and router CLIs use for a failed connection —
// and none of the ordinary lines around a jump that works.
func TestJumpFailLine(t *testing.T) {
	fail := []string{
		"ssh: connect to host 192.0.2.1 port 22: Connection refused",
		"ssh: connect to host 192.0.2.1 port 22: Connection timed out",
		"ssh: connect to host 192.0.2.1 port 22: No route to host",
		"ssh: Could not resolve hostname sw-x: Name or service not known",
		"kex_exchange_identification: Connection closed by remote host",
		"Connection closed by 192.0.2.1 port 22",
		"Host key verification failed.",
		"ops@192.0.2.1: Permission denied (publickey).",
		"telnet: Unable to connect to remote host: Connection refused",
		"telnet: could not resolve sw-x/telnet: Name or service not known",
		"Connection closed by foreign host.",
		"% Connection refused by remote host",
		"% Connection timed out; remote host not responding",
		"% Destination unreachable; gateway or host down",
		"% Unknown command or computer name, or unable to find computer address",
		"% Bad IP address or host name",
		"[Connection to 192.0.2.1 closed by foreign host]",
		"bash: ssh: command not found",
	}
	for _, l := range fail {
		if got := jumpFailLine("ssh ops@192.0.2.1\r\n" + l + "\r\njump1#"); got != strings.TrimSpace(l) {
			t.Errorf("%q: got %q", l, got)
		}
	}
	ok := []string{
		"ssh ops@192.0.2.1",
		"Warning: Permanently added '192.0.2.1' (ECDSA) to the list of known hosts.",
		"Trying 192.0.2.1...\r\nConnected to 192.0.2.1.\r\nEscape character is '^]'.",
		"Last login: Wed Oct 14 11:51:32 2026 from 192.0.2.9",
		"User Access Verification\r\n\r\nUsername: ",
		"ops@192.0.2.1's password: ",
	}
	for _, l := range ok {
		if got := jumpFailLine(l); got != "" {
			t.Errorf("%q taken for a failure: %q", l, got)
		}
	}
}

// E1: a prompt identical to the hop's after the login is refused; another
// prompt, or a hop prompt that names nothing, is not.
func TestLandingCheck(t *testing.T) {
	for _, c := range []struct {
		hop, after string
		fail       bool
	}{
		{"jump1#", "telnet 192.0.2.30\r\n% Connection timed out; remote host not responding\r\n\r\njump1#", true},
		{"jump1#", "telnet 192.0.2.30\r\nUsername: ops\r\nPassword:\r\nsw30#", false},
		{"$", "ssh 192.0.2.30\r\n$ ", false},
	} {
		dev, exp := atHop(t, c.hop)
		l := &landing{target: "192.0.2.30", prompt: currentLine(exp.Transcript()), mark: exp.Mark()}
		dev.say(c.after)
		waitSeen(context.Background(), exp, `[#$]\s*$`, 2*time.Second)
		time.Sleep(50 * time.Millisecond)
		err := l.check(exp)
		if (err != nil) != c.fail {
			t.Errorf("hop %q then %q: err %v", c.hop, c.after, err)
		}
		if c.fail && !strings.Contains(err.Error(), "Connection timed out") {
			t.Errorf("the hop's words are missing: %v", err)
		}
		exp.Close()
	}
}

// E1 (several hops): a hop that fails late, while its login prompts are
// awaited, ends the wait with the reason — not a login into the old hop.
func TestLoginHopFromLateFailure(t *testing.T) {
	dev, exp := atHop(t, "jump1#")
	defer exp.Close()
	from := landing{target: "192.0.2.31", prompt: currentLine(exp.Transcript()), mark: exp.Mark()}
	go func() {
		time.Sleep(300 * time.Millisecond)
		dev.say("\r\n% Connection timed out; remote host not responding\r\n\r\njump1#")
	}()
	b := model.Bastion{Host: "192.0.2.31", Username: "ops", Password: "pw"}
	start := time.Now()
	err := loginHopFrom(context.Background(), exp, b, from, 10*time.Second)
	var je *jumpError
	if !errors.As(err, &je) {
		t.Fatalf("want a jump error, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v: the failure should end the wait at once", time.Since(start))
	}
}

// E1 end to end: a jump host whose prompt the device's OS type accepts
// ("jump1#" for "#") and a device that does not answer. The hop gives up
// after the jump counted as through; the run must stop, and the command set
// must never be typed on the jump host.
func TestBastionLateJumpFailureStopsTheRun(t *testing.T) {
	cmds := make(chan string, 32)
	bAddr, bStop := fakeBastionWith(t, "jump1#", cmds)
	defer bStop()
	bHost, bPortS, _ := net.SplitHostPort(bAddr)
	var bPort int
	fmt.Sscanf(bPortS, "%d", &bPort)

	for _, target := range []string{"198.51.100.7", closedPort(t)} {
		dev := &model.Device{
			Name: "dev1", Host: target, Conn: model.ConnTelnet, OSType: "generic",
			Username: "ops", Password: "pw", CommandSet: "chk", Enabled: true,
			Bastions: []model.Bastion{{Method: model.BastionSSH, Host: bHost, Port: bPort, Username: "j", Password: "jp"}},
		}
		set := &model.CommandSet{Name: "chk", Commands: []model.Command{{Text: "show version"}}}
		settings := model.DefaultSettings()
		settings.LogDir = t.TempDir()
		settings.ConnectTimeout = 5
		settings.CommandTimeout = 8

		res := New(testRegistry()).RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(), nil)
		if res.Success {
			t.Fatalf("%s: the run succeeded on the jump host:\n%s", target, res.Transcript)
		}
		t.Logf("%s: %s", target, res.Error)
		if !strings.Contains(res.Error, "接続できません") {
			t.Errorf("%s: error does not say the jump failed: %s", target, res.Error)
		}
	drain:
		for {
			select {
			case c := <-cmds:
				if c == "show version" {
					t.Errorf("%s: the command set was typed on the jump host", target)
				}
			default:
				break drain
			}
		}
	}
}

// closedPort is a local address nothing listens on (connection refused).
func closedPort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// E2: a session that ends is reported at once as a lost connection, not
// waited out and reported as a timeout.
func TestExpectOnClosedSession(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	dev.say("R1#")
	dev.Close()
	start := time.Now()
	if err := exp.Expect(context.Background(), "never", 5*time.Second); !errors.Is(err, ErrClosed) {
		t.Fatalf("Expect = %v, want ErrClosed", err)
	}
	r := New(testRegistry())
	err := r.waitPromptAnswering(context.Background(), exp, testRegistry().Get("generic"), 5*time.Second, nil)
	if err != nil {
		t.Fatalf("the prompt that arrived before the close still counts: %v", err)
	}
	exp.Drain()
	if err := r.waitPrompt(context.Background(), exp, testRegistry().Get("generic"), 5*time.Second); !errors.Is(err, ErrClosed) {
		t.Fatalf("waitPrompt = %v, want ErrClosed", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v: a closed session should end the waits at once", d)
	}
}

// E4: a bug while driving one device fails that device — the program (and
// the batch's other devices) carry on.
func TestPanicFailsOnlyThatDevice(t *testing.T) {
	inv := &model.Inventory{Settings: model.DefaultSettings()}
	inv.Settings.LogDir = t.TempDir()
	inv.Settings.ConnectTimeout = 2
	inv.Devices = []model.Device{
		{Name: "boom", Host: "192.0.2.40", Conn: model.ConnTelnet, OSType: "generic", Enabled: true},
	}
	emit := func(ev Event) {
		if ev.Phase == PhaseConnecting {
			panic("test panic")
		}
	}
	res := New(testRegistry()).RunBatch(context.Background(), inv, emit)
	if len(res) != 1 || res[0].Success || !strings.HasPrefix(res[0].Error, internalErrMsg) || !strings.Contains(res[0].Error, "test panic") {
		t.Fatalf("results %+v", res)
	}
}

// E4: a panic in the read loop (here, in the live log's tee) closes that
// session instead of ending the program.
func TestReadLoopPanicClosesTheSession(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecterTee(sess, func([]byte) { panic("tee") })
	defer exp.Close()
	go dev.say("R1#")
	if err := exp.Expect(context.Background(), "never", 3*time.Second); !errors.Is(err, ErrClosed) {
		t.Fatalf("Expect = %v, want ErrClosed", err)
	}
}

// E8: with KeepTail the transcript stays bounded, while a command's output
// still shows its start (for rejectedLine) and its end (for lastLine).
func TestKeepTailBoundsTheTranscript(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	exp.KeepTail(1024)
	mark := exp.Mark()
	dev.say("show tech\r\n% Invalid input detected at '^' marker.\r\n")
	for i := 0; i < 200; i++ {
		dev.say(fmt.Sprintf("line %03d %s\r\n", i, strings.Repeat("x", 60)))
	}
	dev.say("R1#")
	if !waitSeen(context.Background(), exp, `R1#$`, 3*time.Second) {
		t.Fatal("output not read")
	}
	if n := len(exp.Transcript()); n > 2*1024 {
		t.Errorf("transcript kept %d bytes", n)
	}
	out := exp.Since(mark)
	if rejectedLine(out) == "" {
		t.Errorf("the start of the output is gone: %q", out[:80])
	}
	if lastLine(out) != "R1#" {
		t.Errorf("the end of the output is gone: %q", lastLine(out))
	}
}

// E8: a device whose live log holds its output keeps only the tail in its
// result; without a live log (saveLog's case) the result keeps it all.
func TestResultTranscriptTail(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	big := strings.Repeat("y", 3*resultKeep) + "END"
	dev.say(big)
	waitSeen(context.Background(), exp, `END$`, 3*time.Second)
	if got := resultTranscript(exp, nil); got != big {
		t.Errorf("without a live log the transcript was cut to %d", len(got))
	}
	got := resultTranscript(exp, &liveLog{})
	if len(got) != resultKeep || !strings.HasSuffix(got, "END") {
		t.Errorf("with a live log: %d bytes", len(got))
	}
}

// E9: a log folder that cannot be written stops the batch before any device
// is connected to, and says why.
func TestUnwritableLogFolderStopsTheBatch(t *testing.T) {
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	inv := &model.Inventory{Settings: model.DefaultSettings()}
	inv.Settings.LogDir = notDir
	// A device that would take the connect timeout to fail, were it dialed.
	inv.Settings.ConnectTimeout = 20
	inv.Devices = []model.Device{
		{Name: "a", Host: "192.0.2.41", Conn: model.ConnTelnet, OSType: "generic", Enabled: true},
		{Name: "b", Host: "192.0.2.42", Conn: model.ConnTelnet, OSType: "generic", Enabled: true},
	}
	var connecting []string
	emit := func(ev Event) {
		if ev.Phase == PhaseConnecting {
			connecting = append(connecting, ev.Device)
		}
	}
	start := time.Now()
	res, dir := New(testRegistry()).RunBatchOpts(context.Background(), inv, Options{}, emit)
	if time.Since(start) > 3*time.Second || len(connecting) > 0 {
		t.Fatalf("devices were connected to: %v", connecting)
	}
	if dir != "" || len(res) != 2 {
		t.Fatalf("dir %q, %d results", dir, len(res))
	}
	for _, r := range res {
		if r.Success || !strings.HasPrefix(r.Error, logUnwritableMsg) {
			t.Errorf("%s: %q", r.Device, r.Error)
		}
	}
}
