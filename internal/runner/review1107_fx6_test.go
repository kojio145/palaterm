package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// F-A-1 (review 2026-11-07): the command timeout is how long the device may
// stay silent, not the command's whole time — output that keeps arriving
// (a long "show tech", a 9600 bps console) keeps the wait alive.
func TestCommandWaitIsSilenceNotTotalTime(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	r := New(profile.NewRegistry())
	prof := profile.Profile{Prompt: "#"}
	tail := "(?:#)" + tailPad

	// Output for 800 ms, then the prompt: a 300 ms timeout must hold.
	go func() {
		for i := 0; i < 8; i++ {
			dev.say(fmt.Sprintf("line %d\r\n", i))
			time.Sleep(100 * time.Millisecond)
		}
		dev.say("sw1#")
	}()
	if err := r.waitTailAnswering(context.Background(), exp, prof, tail, 300*time.Millisecond, nil); err != nil {
		t.Fatalf("flowing output timed out: %v", err)
	}

	// A silent device still times out after the timeout (about).
	exp.Drain()
	started := time.Now()
	err := r.waitTailAnswering(context.Background(), exp, prof, tail, 300*time.Millisecond, nil)
	if !errors.Is(err, ErrExpectTimeout) {
		t.Fatalf("silent device: %v", err)
	}
	if took := time.Since(started); took > 1500*time.Millisecond {
		t.Errorf("silent device took %v for a 300 ms timeout", took)
	}
}

// F-A-1: an output that never ends is cut at the hard limit even though
// it keeps arriving.
func TestCommandWaitHardLimit(t *testing.T) {
	old := cmdHardLimit
	cmdHardLimit = 600 * time.Millisecond
	defer func() { cmdHardLimit = old }()

	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	r := New(profile.NewRegistry())
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				dev.say(fmt.Sprintf("line %d\r\n", i))
			}
		}
	}()
	started := time.Now()
	err := r.waitTailAnswering(context.Background(), exp, profile.Profile{Prompt: "#"}, "(?:#)"+tailPad, 200*time.Millisecond, nil)
	if !errors.Is(err, ErrExpectTimeout) {
		t.Fatalf("endless output: %v", err)
	}
	if took := time.Since(started); took < 500*time.Millisecond || took > 2*time.Second {
		t.Errorf("hard limit of 600 ms ended the wait after %v", took)
	}
}

// F-A-2: a prompt followed by escape sequences (ArubaOS-Switch) is the
// prompt; so is a 待つ文字列 with them after it.
func TestPromptWithTrailingEscapes(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	r := New(profile.NewRegistry())
	prof := profile.Profile{Prompt: regexp.QuoteMeta("#")}
	dev.say("sw1# show version\r\nsome output\r\nsw1#\x1b[K\x1b[?25h ")
	if err := r.waitPromptAnswering(context.Background(), exp, prof, 2*time.Second, nil); err != nil {
		t.Fatalf("prompt with escapes after it: %v", err)
	}
	exp.Drain()
	dev.say("copy done\x1b[0m")
	if err := r.waitTailAnswering(context.Background(), exp, prof, waitTail("done"), 2*time.Second, nil); err != nil {
		t.Fatalf("wait text with escapes after it: %v", err)
	}
	// The pattern is still the tail: the prompt inside the output is not it.
	exp.Drain()
	dev.say("PDEV# column\r\nmore rows\r\n")
	if err := r.waitPromptAnswering(context.Background(), exp, prof, 300*time.Millisecond, nil); !errors.Is(err, ErrExpectTimeout) {
		t.Fatalf("a '#' mid-output taken for the prompt: %v", err)
	}
}

// F-A-3: a login banner's sentence is not a failed jump; the programs'
// own failure lines still are.
func TestJumpFailLineIgnoresBannerSentences(t *testing.T) {
	for _, ok := range []string{
		"ssh ops@192.0.2.1\r\nUnauthorized users will be disconnected from the network.\r\nPassword: ",
		"ssh ops@192.0.2.1\r\nAll sessions may be disconnected from the network without notice.\r\nlogin: ",
		"NOTICE: unauthorized access is prohibited; connection closed by the administrator otherwise\r\nPassword: ",
	} {
		if got := jumpFailLine(ok); got != "" {
			t.Errorf("%q taken for a failure: %q", ok, got)
		}
	}
	for _, l := range []string{
		"ssh: connect to host 192.0.2.5 port 22: Connection refused",
		"telnet: connect to address 192.0.2.5: Connection refused",
		"ssh_exchange_identification: read: Connection reset by peer",
		"ops@192.0.2.5: Permission denied (publickey,password).",
		"Connection closed by foreign host.",
		"% Connection timed out; remote host not responding",
		"bash: ssh: command not found",
	} {
		if got := jumpFailLine("ssh ops@192.0.2.5\r\n" + l + "\r\njump1#"); got != l {
			t.Errorf("%q: got %q", l, got)
		}
	}
}

// F-A-4: a handshake the far end dropped, or that ran out of time, is
// named for what it is; an error session.sshDialError already explained
// gets no second explanation.
func TestDialHintHandshake(t *testing.T) {
	dev := &model.Device{Name: "sw1", Host: "192.0.2.11", Conn: model.ConnSSH, Port: 22}
	if h := dialHint(errors.New("ssh: handshake failed: EOF"), dev, nil); !strings.Contains(h, "接続直後に切断") || !strings.Contains(h, "192.0.2.11") {
		t.Errorf("EOF: %q", h)
	}
	if h := dialHint(errors.New("ssh: handshake failed: read tcp 192.0.2.9:50000->192.0.2.11:22: wsarecv: An existing connection was forcibly closed by the remote host."), dev, nil); !strings.Contains(h, "接続直後に切断") {
		t.Errorf("forcibly closed: %q", h)
	}
	if h := dialHint(errors.New("ssh: handshake failed: read tcp 192.0.2.9:50000->192.0.2.11:22: i/o timeout"), dev, nil); !strings.Contains(h, "時間内に終わりません") {
		t.Errorf("handshake timeout: %q", h)
	}
	if h := dialHint(errors.New("機器側が接続直後に切断しました（VTY の ACL・同時接続数の上限・SSH 鍵未生成のいずれか）（ssh dial 192.0.2.11:22: ssh: handshake failed: EOF）"), dev, nil); h != "" {
		t.Errorf("explained twice: %q", h)
	}
	// A TCP connect timeout is still the unreachable device.
	if h := dialHint(errors.New("ssh dial 192.0.2.11:22: dial tcp 192.0.2.11:22: i/o timeout"), dev, nil); !strings.Contains(h, "応答がありません") {
		t.Errorf("connect timeout: %q", h)
	}
}

// F-A-6: one lock per COM port two or more devices open (case folded);
// none for a port one device opens, none for auto-detect.
func TestSerialLocks(t *testing.T) {
	targets := []*model.Device{
		{Name: "c1", Conn: model.ConnSerial, SerialPort: "COM3"},
		{Name: "c2", Conn: model.ConnSerial, SerialPort: "com3"},
		{Name: "c3", Conn: model.ConnSerial, SerialPort: "COM4"},
		{Name: "c4", Conn: model.ConnSerial},
		{Name: "s1", Conn: model.ConnSSH, Host: "192.0.2.1"},
	}
	locks := serialLocks(targets)
	if len(locks) != 1 || locks["COM3"] == nil {
		t.Fatalf("locks = %v", locks)
	}
	if serialPortKey(targets[1]) != "COM3" || serialPortKey(targets[4]) != "" || serialPortKey(targets[3]) != "" {
		t.Errorf("serialPortKey: %q %q %q", serialPortKey(targets[1]), serialPortKey(targets[4]), serialPortKey(targets[3]))
	}
}

// F-B-3: a device naming a command set the vault no longer has logs in
// and runs nothing — and the result says so instead of passing as a run.
func TestMissingCommandSetIsWarned(t *testing.T) {
	addr, stop := fakeCisco(t)
	defer stop()
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	inv := &model.Inventory{
		Devices: []model.Device{{
			Name: "router1", Host: host, Conn: model.ConnSSH, Port: port,
			OSType: "cisco-ios", Username: "admin", Password: "x", EnablePassword: "en",
			CommandSet: "gone-set", Enabled: true,
		}},
		Settings: model.DefaultSettings(),
	}
	inv.Settings.LogDir = t.TempDir()
	inv.Settings.CommandTimeout, inv.Settings.ConnectTimeout = 5, 5
	r := New(testRegistry())
	results, _ := r.RunBatchOpts(context.Background(), inv, Options{}, nil)
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("results: %+v", results)
	}
	if len(results[0].Warnings) != 1 || !strings.Contains(results[0].Warnings[0], "コマンドセット「gone-set」が見つからないためログインのみ") {
		t.Errorf("warnings: %v", results[0].Warnings)
	}
	// A device with no set at all is a plain login: no warning.
	inv.Devices[0].CommandSet = ""
	results, _ = r.RunBatchOpts(context.Background(), inv, Options{}, nil)
	if len(results) != 1 || !results[0].Success || len(results[0].Warnings) != 0 {
		t.Errorf("no set: %+v", results)
	}
}

// F-C-1: the wait string is named as typed, not as its quoted regexp; the
// device with no set reads Japanese.
func TestUnquoteMetaAndSetName(t *testing.T) {
	for _, s := range []string{"[y/n]?", "assword:", "#", `C:\x`, "(config)#"} {
		if got := unquoteMeta(regexp.QuoteMeta(s)); got != s {
			t.Errorf("unquoteMeta(QuoteMeta(%q)) = %q", s, got)
		}
	}
	if setName(nil) != "（コマンドなし）" {
		t.Errorf("setName(nil) = %q", setName(nil))
	}
}

// F-C-5: the generic pager marker counts at the tail of the output only.
func TestGenericMoreAtTailOnly(t *testing.T) {
	re := regexp.MustCompile(genericMore)
	for _, s := range []string{"PAGE-ONE\r\n--More--        ", "x\r\n---(more 25%)---", "y\r\n -- MORE -- \x1b[K", "--More--\r\n"} {
		if !re.MatchString(s) {
			t.Errorf("marker at the tail not matched: %q", s)
		}
	}
	for _, s := range []string{"log: --More-- was shown\r\nnext line\r\nsw1#", "--More--\r\nsw1# "} {
		if re.MatchString(s) {
			t.Errorf("marker not at the tail matched: %q", s)
		}
	}
}

// F-C-3: a recovered panic reaches OnPanic (the app's error log); a nil
// hook, or one that panics itself, costs nothing.
func TestOnPanicHook(t *testing.T) {
	r := New(profile.NewRegistry())
	var got []string
	r.OnPanic = func(where string, p any) { got = append(got, fmt.Sprintf("%s: %v", where, p)) }
	r.notePanic("runDevice lab-sw1", "boom")
	if len(got) != 1 || got[0] != "runDevice lab-sw1: boom" {
		t.Errorf("OnPanic not called: %v", got)
	}
	r.OnPanic = func(string, any) { panic("hook broke") }
	r.notePanic("x", "y") // swallowed
	r.OnPanic = nil
	r.notePanic("x", "y")
	var nilRunner *Runner
	nilRunner.notePanic("x", "y")
}
