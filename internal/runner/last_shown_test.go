package runner

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

func TestLastLineStripsEscapes(t *testing.T) {
	out := "Last login: x\r\n\x1b]0;ops@jump1: ~\x07\x1b[01;32mops@jump1\x1b[00m:\x1b[01;34m~\x1b[00m$ "
	if got := lastLine(out); got != "ops@jump1:~$" {
		t.Errorf("lastLine = %q", got)
	}
}

func TestLastShown(t *testing.T) {
	dev := &model.Device{Name: "fw1", Password: "s3cret"}
	e := &expecter{}
	e.transcript.WriteString("banner\r\nops@jump1:~$ ssh admin@192.0.2.10\r\nadmin@192.0.2.10's password: s3cret")
	got := lastShown(context.Background(), e, dev, "login: timeout")
	if !strings.Contains(got, "機器の最後の表示") || !strings.Contains(got, "password:") || strings.Contains(got, "s3cret") {
		t.Errorf("lastShown = %q (want the last line, password masked)", got)
	}
	if lastShown(context.Background(), e, dev, "x（機器の最後の表示: \"a\"）") != "" {
		t.Error("added twice")
	}
	if lastShown(context.Background(), nil, dev, "dial") != "" {
		t.Error("no session, yet something added")
	}
	if lastShown(context.Background(), &expecter{}, dev, "dial") != "" {
		t.Error("nothing received, yet something added")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if lastShown(ctx, e, dev, "canceled") != "" {
		t.Error("a stopped run got the last line")
	}
}

// What the tee already put on screen goes to the log only; what comes
// after goes to the screen (and, through it, the log).
func TestStartRawShown(t *testing.T) {
	e := &expecter{}
	e.transcript.WriteString("login: ")
	e.buf.WriteString("login: ")
	var mu sync.Mutex
	var screen, logged []string
	e.StartRawShown(func(b []byte) { mu.Lock(); screen = append(screen, string(b)); mu.Unlock() },
		func(b []byte) { logged = append(logged, string(b)) })
	if len(screen) != 0 || len(logged) != 1 || logged[0] != "login: " {
		t.Errorf("screen=%q logged=%q", screen, logged)
	}
	if e.Transcript() != "" || e.rawSink == nil {
		t.Error("not switched to raw")
	}
}
