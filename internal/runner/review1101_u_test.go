package runner

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A chain of jump hosts whose prompts name nothing ("$ "), entered with a
// key: the far end's prompt is the same bare "$ " and no login prompt is
// due, so the landing is accepted (owner's decision 2026-11-01) — unless
// the hop said the jump failed.
func TestBarePromptKeyedLandingIsAccepted(t *testing.T) {
	for _, c := range []struct {
		after string
		keyed bool
		want  string // substring of the error; "" = no error
	}{
		{"ssh 192.0.2.30\r\n$ ", true, ""},
		{"ssh 192.0.2.30\r\n$ ", false, "ホスト名が無い"},
		{"ssh 192.0.2.30\r\nssh: connect to host 192.0.2.30 port 22: Connection timed out\r\n$ ", true, "Connection timed out"},
	} {
		dev, exp := atHop(t, "$")
		l := &landing{target: "192.0.2.30", prompt: currentLine(exp.Transcript()), mark: exp.Mark(), keyed: c.keyed}
		dev.say(c.after)
		waitSeen(context.Background(), exp, `[#$]\s*$`, 2*time.Second)
		time.Sleep(50 * time.Millisecond)
		err := l.check(exp)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("keyed=%v %q: unexpected %v", c.keyed, c.after, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("keyed=%v %q: err %v, want %q", c.keyed, c.after, err, c.want)
		}
		exp.Close()
	}
}
