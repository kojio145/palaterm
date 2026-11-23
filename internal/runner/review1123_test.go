package runner

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/profile"
)

// "Password:" and a blank after it in reads of their own (a slow serial
// line) is still the password prompt: the password goes (review
// 2026-11-23). It used to wait the whole step out and send nothing.
func TestSplitPasswordPrompt(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()
	r := New(profile.NewRegistry())
	steps := profile.Quote(profile.Profile{Login: []profile.Step{{Expect: "assword:", Send: "{password}"}}}).Login
	go func() {
		dev.say("lab login: ops\r\nPassword:")
		time.Sleep(20 * time.Millisecond)
		dev.say(" ")
	}()
	done := make(chan error, 1)
	go func() {
		done <- r.runSteps(context.Background(), exp, steps, profile.Vars{Password: "pw"}, 5*time.Second, false, "#")
	}()
	select {
	case s := <-dev.sent:
		if s != "pw\r" && s != "pw\r\n" && s != "pw" {
			t.Fatalf("sent %q", s)
		}
	case err := <-done:
		t.Fatalf("runSteps returned %v without sending the password", err)
	case <-time.After(3 * time.Second):
		t.Fatal("the password was not sent within 3 s")
	}
}

// "more" as the start of a longer word is not the pager (review 2026-11-23);
// the markers vendors print still are.
func TestGenericMoreWholeWord(t *testing.T) {
	re := regexp.MustCompile(genericMore)
	for _, s := range []string{"Gi0/1  up  up  --more-uplinks\r\n", "x --moreover"} {
		if re.MatchString(s) {
			t.Errorf("matched %q", s)
		}
	}
	for _, s := range []string{"--More--", "---(more 25%)---", "-- MORE --, next page: Space", "--More--\r\n", "---- More ----", "\x1b[7m--More--\x1b[m"} {
		if !re.MatchString(s) {
			t.Errorf("marker not matched: %q", s)
		}
	}
}
