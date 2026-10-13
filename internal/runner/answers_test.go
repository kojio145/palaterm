package runner

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// fakeAsking is a Telnet Cisco-like router whose commands ask questions or
// refuse: "copy run start" asks for the destination file name, "clear
// counters" wants [confirm], "show foo" is refused, "hang" never answers
// until Ctrl+C. It records every line (and ^C) it receives.
func fakeAsking(t *testing.T) (addr string, got func() []string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var lines []string
	rec := func(s string) { mu.Lock(); lines = append(lines, s); mu.Unlock() }
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go askingConn(c, rec)
		}
	}()
	return ln.Addr().String(), func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), lines...) }, func() { ln.Close() }
}

func askingConn(c net.Conn, rec func(string)) {
	defer c.Close()
	io.WriteString(c, "\r\nUsername: ")
	buf := make([]byte, 512)
	var line strings.Builder
	stage, asking := 0, ""
	prompt := "\r\nR1#"
	for {
		n, err := c.Read(buf)
		for i := 0; i < n; i++ {
			b := buf[i]
			if b == 0x03 {
				rec("^C")
				line.Reset()
				asking = ""
				io.WriteString(c, "^C"+prompt)
				continue
			}
			if b != '\r' && b != '\n' {
				line.WriteByte(b)
				continue
			}
			if b == '\n' && line.Len() == 0 {
				continue
			}
			cmd := strings.TrimSpace(line.String())
			line.Reset()
			rec(cmd)
			switch {
			case stage == 0:
				stage = 1
				io.WriteString(c, "\r\nPassword: ")
			case stage == 1:
				stage = 2
				io.WriteString(c, prompt)
			case asking == "hang":
				// still busy: input is ignored until ^C
			case asking == "copy":
				asking = ""
				io.WriteString(c, "\r\nBuilding configuration...\r\n[OK]"+prompt)
			case asking == "clear":
				asking = ""
				io.WriteString(c, prompt)
			case cmd == "copy run start":
				asking = "copy"
				io.WriteString(c, "\r\nDestination filename [startup-config]? ")
			case cmd == "clear counters":
				asking = "clear"
				io.WriteString(c, "\r\nClear \"show interface\" counters on all interfaces [confirm]")
			case cmd == "copy tftp: flash:":
				asking = "hang" // waits for an address; an Enter would abort it
				io.WriteString(c, "\r\nAddress or name of remote host []? ")
			case cmd == "show foo":
				io.WriteString(c, "\r\n        ^\r\n% Invalid input detected at '^' marker.\r\n"+prompt)
			case cmd == "hang":
				asking = "hang" // says nothing until ^C
			case cmd == "boot":
				// ends on a line the Cisco prompt does not match (待つ文字列 test)
				io.WriteString(c, "\r\nbooting...\r\nREADY> ")
			case cmd == "show clock":
				io.WriteString(c, "\r\n*10:00:00.000 JST Mon Oct 12 2026"+prompt)
			default:
				io.WriteString(c, prompt)
			}
		}
		if err != nil {
			return
		}
	}
}

func askingRun(t *testing.T, cmds []string, mod func(*model.Settings), runDir string) (DeviceResult, []string) {
	t.Helper()
	addr, got, stop := fakeAsking(t)
	defer stop()
	host, portS, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portS, "%d", &port)
	dev := &model.Device{Name: "r1", Host: host, Port: port, Conn: model.ConnTelnet,
		OSType: "cisco-ios", Username: "u", Password: "p", CommandSet: "s", Enabled: true}
	set := &model.CommandSet{Name: "s"}
	for _, c := range cmds {
		set.Commands = append(set.Commands, model.Command{Text: c})
	}
	s := model.DefaultSettings()
	s.LogDir = t.TempDir()
	s.ConnectTimeout = 5
	s.CommandTimeout = 2
	if mod != nil {
		mod(&s)
	}
	if runDir == "" {
		runDir = s.LogDir
	}
	res := New(testRegistry()).RunDevice(context.Background(), dev, set, s, runDir, time.Now(), nil)
	return res, got()
}

// The Cisco profile answers the destination-file question and [confirm]
// with Enter, so the set runs through; a refused command is a warning on a
// run that still succeeds.
func TestAnswersAndRefusedCommands(t *testing.T) {
	started := time.Now()
	res, got := askingRun(t, []string{"copy run start", "clear counters", "show foo", "show clock"}, nil, "")
	if !res.Success {
		t.Fatalf("run failed: %s\n%s", res.Error, res.Transcript)
	}
	if took := time.Since(started); took > 6*time.Second {
		t.Errorf("took %v: the questions should be answered, not timed out", took)
	}
	if !strings.Contains(res.Transcript, "[OK]") || !strings.Contains(res.Transcript, "10:00:00") {
		t.Errorf("transcript:\n%s", res.Transcript)
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "show foo → % Invalid input") {
		t.Errorf("warnings = %q", res.Warnings)
	}
	if !strings.Contains(strings.Join(got, "|"), "show clock") {
		t.Errorf("show clock not reached: %v", got)
	}
}

// A command that never returns ends the device: the rest of the set is not
// typed into a device in an unknown state.
func TestFailedCommandStopsTheDevice(t *testing.T) {
	res, got := askingRun(t, []string{"hang", "show clock"}, nil, "")
	if res.Success || strings.Contains(strings.Join(got, "|"), "show clock") {
		t.Errorf("success=%v, sent %v", res.Success, got)
	}
}

// A log that cannot be written makes the device an error, not 完了.
func TestLogWriteFailureIsAnError(t *testing.T) {
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, _ := askingRun(t, []string{"show clock"}, nil, notDir)
	if res.Success || !strings.Contains(res.Error, "ログを保存できませんでした") {
		t.Errorf("success=%v error=%q", res.Success, res.Error)
	}
}

func TestRejectedLine(t *testing.T) {
	for out, want := range map[string]string{
		"show foo\r\n% Invalid input detected at '^' marker.\r\nR1#": "% Invalid input detected at '^' marker.",
		"cat x\r\nbash: cat x: command not found\r\n$ ":              "bash: cat x: command not found",
		"show interfaces | include error\r\n 0 input errors\r\nR1#":  "",
		"show clock\r\n*10:00\r\nR1#":                                "",
		// a long output quoting the words from history is not a refusal
		"show logging\r\nline 1\r\nline 2\r\nline 3\r\nbash: x: command not found\r\nR1#": "",
	} {
		if got := rejectedLine(out); got != want {
			t.Errorf("%q → %q, want %q", out, got, want)
		}
	}
}

// A question with no default ("[]?") gets no automatic Enter: it would only
// abort the copy. The command waits out its timeout instead.
func TestNoEnterForAQuestionWithoutDefault(t *testing.T) {
	res, got := askingRun(t, []string{"copy tftp: flash:"}, nil, "")
	if res.Success {
		t.Errorf("run succeeded: %v", got)
	}
	for i, l := range got {
		if l == "copy tftp: flash:" && i+1 < len(got) && got[i+1] == "" {
			t.Errorf("an Enter was sent to the []? question: %v", got)
		}
	}
}

// With no answer for "[confirm]" (the generic profile has none), the
// confirmation is left alone: the silence-nudge Enter must not confirm it.
func TestUnansweredConfirmIsNotNudged(t *testing.T) {
	addr, got, stop := fakeAsking(t)
	defer stop()
	host, portS, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portS, "%d", &port)
	dev := &model.Device{Name: "r1", Host: host, Port: port, Conn: model.ConnTelnet,
		OSType: "generic", Username: "u", Password: "p", CommandSet: "s", Enabled: true}
	set := &model.CommandSet{Name: "s", Commands: []model.Command{{Text: "clear counters"}}}
	s := model.DefaultSettings()
	s.LogDir = t.TempDir()
	s.ConnectTimeout = 5
	s.CommandTimeout = 3
	res := New(testRegistry()).RunDevice(context.Background(), dev, set, s, s.LogDir, time.Now(), nil)
	lines := got()
	for i, l := range lines {
		if l == "clear counters" && i+1 < len(lines) && lines[i+1] == "" {
			t.Fatalf("the [confirm] was answered with an Enter: %v", lines)
		}
	}
	if res.Success {
		t.Errorf("an unanswered confirmation should end in a timeout: %v", lines)
	}
}

// A command stopped at a question nobody answers ends with the question in
// the error and what to do about it.
func TestTimeoutErrorNamesTheQuestion(t *testing.T) {
	res, _ := askingRun(t, []string{"copy tftp: flash:"}, nil, "")
	if !strings.Contains(res.Error, "Address or name of remote host []?") || !strings.Contains(res.Error, "確認への自動応答") {
		t.Errorf("error = %q", res.Error)
	}
}
