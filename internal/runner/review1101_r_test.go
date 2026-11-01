package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// Regressions for the 2026-11-01 review (B / J / H), internal/runner.
// Every value is a lab one (192.0.2.x, ops, lab-sw1, jump1).

// B S-2: a login banner that says "password:" in a sentence is not the
// password prompt. The user name goes first, once "Username:" is the line
// the device is on.
func TestBannerWordsAreNotTheCredentialPrompt(t *testing.T) {
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
	dev.say("Enter your corporate password: do not share it.\r\nAuthorized use only.\r\n")
	time.Sleep(time.Second)
	dev.say("Username: ")
	if got := dev.next(t); got != "ops\r\n" {
		t.Fatalf("first send %q, want the user name", got)
	}
	dev.say("\r\nPassword: ")
	if got := dev.next(t); got != "pw\r\n" {
		t.Fatalf("then %q, want the password", got)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runSteps did not return")
	}
}

// B A-1: a "####" banner after the login is not the operational prompt:
// "enable" is sent once ">" shows.
func TestBannerHashIsNotTheOperationalPrompt(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	steps := []profile.Step{{Expect: ">", Send: "enable"}}
	done := make(chan error, 1)
	go func() {
		done <- New(profile.NewRegistry()).runSteps(context.Background(), exp, steps, profile.Vars{}, 15*time.Second, false, "#")
	}()
	dev.say("####\r\n# welcome #\r\n####\r\n")
	time.Sleep(500 * time.Millisecond)
	dev.say("lab-sw1>")
	if got := dev.next(t); got != "enable\r\n" {
		t.Fatalf("sent %q, want enable", got)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runSteps did not return")
	}
}

// A device already at "#" still skips the step quickly, and a prompt that
// is still being printed does not count until it has settled.
func TestExpectOrReadyWaitsForTheTailToSettle(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	dev.say("lab-sw1#")
	waitSeen(context.Background(), exp, `#`, time.Second)
	start := time.Now()
	ready, err := New(profile.NewRegistry()).expectOrReady(context.Background(), exp, ">", "#", 10*time.Second)
	if err != nil || !ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("the skip took %v", time.Since(start))
	}
}

// B A-2: the first jump host's prompt is read after its MOTD, not at the
// first MOTD line that happens to end in "%" or "#".
func TestFirstHopPromptIsReadAfterTheMOTD(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	first := model.Bastion{Host: "192.0.2.10", Method: model.BastionSSH, Username: "ops", Password: "pw"}
	target := &model.Device{Name: "lab-sw1", Host: "192.0.2.21", Conn: model.ConnTelnet, Username: "ops"}
	type res struct {
		to  *landing
		err error
	}
	done := make(chan res, 1)
	go func() {
		to, err := (&Runner{}).traverseBastions(context.Background(), exp, []model.Bastion{first}, target, 10*time.Second)
		done <- res{to, err}
	}()
	for _, line := range []string{"Welcome to jump1\r\n", "  Memory usage: 23%\r\n", "  # Authorized use only #\r\n", "\r\n"} {
		dev.say(line)
		time.Sleep(200 * time.Millisecond)
	}
	dev.say("[ops@jump1 ~]$ ")
	if got := dev.next(t); !strings.HasPrefix(got, "telnet 192.0.2.21") {
		t.Fatalf("sent %q, want the jump command", got)
	}
	dev.say("Trying 192.0.2.21...\r\nConnected to 192.0.2.21.\r\n\r\nlogin: ")
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.to.prompt != "[ops@jump1 ~]$" {
			t.Errorf("the hop's prompt was read as %q", r.to.prompt)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("traverseBastions did not return")
	}
}

// reShellDone is the tail of the output, like every other prompt.
func TestShellDoneIsTheTail(t *testing.T) {
	for text, want := range map[string]bool{
		"  Memory usage: 23%\r\n":     false,
		"# Authorized use only #\r\n": false,
		"[ops@jump1 ~]$ ":             true,
		"jump1#":                      true,
		"lab-rt1> ":                   true,
	} {
		e := &expecter{}
		e.buf.WriteString(text)
		if got := e.Seen(reShellDone); got != want {
			t.Errorf("%q: shell prompt = %v, want %v", text, got, want)
		}
	}
}

// J #1: a jump host whose prompt names nothing ("# ") and a device that
// does not answer. The hop's "Connection timed out", late, is read whatever
// the prompt looks like; and an SSH device on a password that never asked
// for anything was never logged into.
func TestLandingCheckBehindABarePrompt(t *testing.T) {
	for _, c := range []struct {
		name      string
		after     string
		needLogin bool
		keyed     bool
		want      string // substring of the error; "" = no error
	}{
		{"timed out late", "ssh ops@192.0.2.10\r\nssh: connect to host 192.0.2.10 port 22: Connection timed out\r\n# ", true, false, "Connection timed out"},
		{"back at the same bare prompt", "ssh ops@192.0.2.10\r\n# ", true, false, "踏み台のプロンプトと同じ"},
		{"a prompt with no login asked", "ssh ops@192.0.2.10\r\nlab-sw1# ", true, false, "入力待ちが一度も出ない"},
		{"logged in", "ssh ops@192.0.2.10\r\nops@192.0.2.10's password: \r\nlab-sw1# ", true, false, ""},
		{"in with a key on the hop", "ssh ops@192.0.2.10\r\nLast login: Tue Oct 20 09:00:00 2026\r\nlab-sw1# ", true, true, ""},
		{"a Telnet device", "telnet 192.0.2.10\r\nlogin: ops\r\nPassword: \r\nlab-sw1# ", false, false, ""},
		{"a banner saying closed, after the login prompt", "ssh ops@192.0.2.10\r\nPassword: \r\nThis session is logged; connection closed by admin on abuse.\r\nlab-sw1# ", true, false, ""},
	} {
		dev, exp := atHop(t, "# ")
		l := &landing{target: "192.0.2.10", prompt: currentLine(exp.Transcript()), mark: exp.Mark(), needLogin: c.needLogin, keyed: c.keyed}
		dev.say(c.after)
		waitSeen(context.Background(), exp, `#\s*$`, 2*time.Second)
		time.Sleep(50 * time.Millisecond)
		err := l.check(exp)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err %v, want %q", c.name, err, c.want)
		}
		if err != nil && !AtJumpHost(fmt.Errorf("機器へのジャンプに失敗: %w", err)) {
			t.Errorf("%s: not AtJumpHost: %v", c.name, err)
		}
		exp.Close()
	}
}

// J #1 end to end: a jump host on a bare "# " and a device that does not
// answer; the command set must not be typed on the jump host.
func TestBareHopPromptLateJumpFailureStopsTheRun(t *testing.T) {
	cmds := make(chan string, 32)
	bAddr, bStop := fakeBastionWith(t, "# ", cmds)
	defer bStop()
	bHost, bPortS, _ := net.SplitHostPort(bAddr)
	var bPort int
	fmt.Sscanf(bPortS, "%d", &bPort)
	dev := &model.Device{
		Name: "lab-sw1", Host: "198.51.100.7", Conn: model.ConnTelnet, OSType: "linux-root",
		Username: "ops", Password: "pw", CommandSet: "chk", Enabled: true,
		Bastions: []model.Bastion{{Method: model.BastionSSH, Host: bHost, Port: bPort, Username: "ops", Password: "pw"}},
	}
	set := &model.CommandSet{Name: "chk", Commands: []model.Command{{Text: "show version"}}}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.ConnectTimeout = 5
	settings.CommandTimeout = 8
	res := New(testRegistry()).RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(), nil)
	if res.Success {
		t.Fatalf("the run succeeded on the jump host:\n%s", res.Transcript)
	}
	if !strings.Contains(res.Error, "接続できません") && !strings.Contains(res.Error, "中止しました") {
		t.Errorf("error does not say the jump failed: %s", res.Error)
	}
	for {
		select {
		case c := <-cmds:
			if c == "show version" {
				t.Fatal("the command set was typed on the jump host")
			}
			continue
		default:
		}
		break
	}
}

// B B-3: OpenSSH's other ways of saying the jump did not happen.
func TestJumpFailLineKnowsMoreOfOpenSSH(t *testing.T) {
	for _, l := range []string{
		"Received disconnect from 192.0.2.9 port 22:2: Too many authentication failures",
		"Disconnected from 192.0.2.9 port 22",
		"Connection to 192.0.2.9 closed.",
		"Bad port 'x'",
	} {
		if got := jumpFailLine("ssh ops@192.0.2.9\r\n" + l + "\r\njump1#"); got != l {
			t.Errorf("%q: got %q", l, got)
		}
	}
}

// B B-1: the confirmation forms of Junos, PAN-OS, NX-OS and mkfs are
// questions: no nudge Enter at them.
func TestQuestionTailForms(t *testing.T) {
	for text, want := range map[string]bool{
		"Delete everything under this level? [yes,no] (no) ": true,
		"Are you sure you want to continue? (y or n)":        true,
		"Do you want to continue (y/n)?  [n] ":               true,
		"Do you want to continue (y/n)  [n] ":                true,
		"Proceed anyway? (y,N) ":                             true,
		"Reload? [confirm]":                                  true,
		"Delete filename [x.bin]? ":                          true,
		"lab-sw1#":                                           false,
		"lab-sw1> ":                                          false,
		"Building configuration...\r\n":                      false,
	} {
		e := &expecter{}
		e.buf.WriteString(text)
		if got := e.Seen(reQuestionTail); got != want {
			t.Errorf("%q: question = %v, want %v", text, got, want)
		}
	}
}

// B B-2: a tail-anchored Seen looks at the tail only; the unconsumed buffer
// is cut to its tail; a repeated Expect searches only what is new, and still
// finds a match that straddles the old end.
func TestExpectBufferIsBounded(t *testing.T) {
	e := &expecter{}
	e.buf.WriteString(strings.Repeat("x\n", 100<<10))
	e.buf.WriteString("lab-sw1#")
	if !e.Seen(tailOf("#")) || !e.Seen(tailOf("lab-sw1#")) {
		t.Error("the prompt at the tail of a long output was not seen")
	}
	if e.Seen(tailOf(">")) {
		t.Error("a prompt that is not there was seen")
	}
	e.trimBuf()
	if n := e.buf.Len(); n != bufKeep {
		t.Errorf("after the cut buf holds %d bytes, want %d", n, bufKeep)
	}
	if !strings.HasSuffix(e.buf.String(), "lab-sw1#") {
		t.Error("the cut lost the tail")
	}

	e = &expecter{}
	e.buf.WriteString(strings.Repeat("x", 3000) + "ab")
	if err := e.Expect(context.Background(), "abc", 20*time.Millisecond); !errors.Is(err, ErrExpectTimeout) {
		t.Fatalf("want a miss, got %v", err)
	}
	if e.missPat != "abc" || e.missLen != 3002 {
		t.Fatalf("miss not recorded: %q %d", e.missPat, e.missLen)
	}
	e.buf.WriteString("c")
	if err := e.Expect(context.Background(), "abc", 20*time.Millisecond); err != nil {
		t.Fatalf("a match straddling the old end was missed: %v", err)
	}
	if e.buf.Len() != 0 || e.missPat != "" {
		t.Errorf("after the match: buf %d bytes, missPat %q", e.buf.Len(), e.missPat)
	}
}

// J #4: an error message quotes no control characters: 8-bit CSI, DCS /
// APC strings, BEL, raw high bytes are out; tabs and non-ASCII text stay.
func TestLastLineStripsControls(t *testing.T) {
	for in, want := range map[string]string{
		"\x9b2J\x9bHhost key mismatch":          "2JHhost key mismatch",
		"\x1bP+q544e\x1b\\lab-sw1#":             "lab-sw1#",
		"\x1b_hidden\x1b\\lab-sw1#":             "lab-sw1#",
		"\x1b^pm\x1b\\lab-sw1#":                 "lab-sw1#",
		"\x1bXsos\x1b\\lab-sw1#":                "lab-sw1#",
		"\x07\x1b[31mError\x1b[0m: no\x7f":      "Error: no",
		"lab-sw1#\tあ":                           "lab-sw1#\tあ",
		"\xc2\x9b2Jlab-sw1#":                    "2Jlab-sw1#",
		"line one\r\nssh: connect: refused\r\n": "ssh: connect: refused",
	} {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", in, got, want)
		}
	}
	if got := jumpFailLine("ssh ops@192.0.2.1\r\n\x1bPx\x1b\\ssh: connect to host 192.0.2.1 port 22: Connection refused\r\njump1#"); got != "ssh: connect to host 192.0.2.1 port 22: Connection refused" {
		t.Errorf("jumpFailLine kept a DCS string: %q", got)
	}
}

// J #5: the passphrase question is the whole of the hop's current line,
// not words inside a banner the far end shows.
func TestPassphraseQuestionIsTheWholeLine(t *testing.T) {
	for text, want := range map[string]bool{
		"ssh ops@192.0.2.9\r\nEnter passphrase for key '/home/ops/.ssh/id_ed25519': ":              true,
		"Enter passphrase for key '/home/ops/.ssh/id_ed25519': ":                                   true,
		"\r\n  Enter passphrase for key '/home/ops/.ssh/id_rsa':":                                  true,
		"Banner: Enter passphrase for key '/x': is what you would see\r\n":                         false,
		"Note - Enter passphrase for key '/x': ":                                                   false,
		"ssh ops@192.0.2.9\r\nEnter passphrase for key '/home/ops/.ssh/id_ed25519': \r\nlab-sw1> ": false,
	} {
		e := &expecter{}
		e.buf.WriteString(text)
		if got := e.Seen(rePassphrase); got != want {
			t.Errorf("%q: passphrase question = %v, want %v", text, got, want)
		}
	}
}

// B B-8: PAN-OS, FortiOS and VRP refusals are recognized.
func TestRejectedLineKnowsMoreVendors(t *testing.T) {
	for _, l := range []string{
		"Invalid syntax.",
		"command parse error before 'xyz'",
		"Unknown action 0",
		"Error: Wrong parameter found at '^' position.",
		"Error: Unrecognized command found at '^' position.",
	} {
		if got := rejectedLine("show xyz\r\n" + l + "\r\nlab-sw1#"); got != l {
			t.Errorf("%q: got %q", l, got)
		}
	}
}
