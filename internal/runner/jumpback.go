package runner

import (
	"regexp"
	"strings"
)

// A terminal window logged in through jump hosts (by commands typed on
// them, not a tunnel) holds the line to the first jump host. When the device
// ends its session — its idle timeout, or exit — the line stays up on the
// last jump host's shell, so nothing tells the window the device is gone:
// it went on reading 接続済み with no 再接続 button (user report
// 2026-11-21). The window watches for the jump host's shell coming back:
//
//   - the last jump host's prompt line as the line the screen is on now,
//     when that prompt names something ("[ops@jump1 ~]$"): the device's
//     prompt was checked to differ from it at the login (landing.check);
//   - for a bare prompt ("$ ", "# "), which any host may show, the jump
//     host's own words for the session that ended as well ("Connection to
//     192.0.2.10 closed.", telnet's "Connection closed by foreign host.").

// reHopClosed is a jump host's line for a session to the next host that
// ended (OpenSSH, Linux telnet, a router's "[Connection to x closed by
// foreign host]"), at the start of a line.
var reHopClosed = regexp.MustCompile(`(?im)^[ \t\[]*connection (?:to \S+ )?closed\b`)

// jumpTailMax is how much of the latest output the watch keeps: enough for
// the closing line and the prompt after it.
const jumpTailMax = 1024

// backAtJump reports that tail, the latest output, ends on the shell of the
// jump host whose prompt line is prompt.
func backAtJump(tail, prompt string) bool {
	if prompt == "" {
		return false
	}
	line := currentLine(tail)
	if line == "" {
		return false
	}
	if distinctPrompt(prompt) {
		return line == prompt
	}
	if line != prompt {
		return false
	}
	text := reEscape.ReplaceAllString(tail, "")
	if i := strings.LastIndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	return reHopClosed.MatchString(text)
}

// setJumpPrompt records the prompt line of the jump host the device was
// reached from (a login through jump hosts that worked).
func (e *expecter) setJumpPrompt(prompt string) {
	e.mu.Lock()
	e.jumpPrompt = prompt
	e.mu.Unlock()
}

// OnBackAtJump has fn called, from the read loop, each time the output of
// an interactive session comes back to the jump host's shell (backAtJump).
// Nothing happens for a session that did not go through jump hosts by
// commands typed on them.
func (e *expecter) OnBackAtJump(fn func()) {
	e.mu.Lock()
	e.jumpFn = fn
	e.mu.Unlock()
}

// watchJump is the read loop's step for a chunk of interactive output.
func (e *expecter) watchJump(chunk []byte) {
	e.mu.Lock()
	if e.jumpFn == nil || e.jumpPrompt == "" {
		e.mu.Unlock()
		return
	}
	e.jumpTail += string(chunk)
	if len(e.jumpTail) > jumpTailMax {
		e.jumpTail = e.jumpTail[len(e.jumpTail)-jumpTailMax:]
	}
	back := backAtJump(e.jumpTail, e.jumpPrompt)
	fire := back && !e.jumpBack
	e.jumpBack = back
	fn := e.jumpFn
	e.mu.Unlock()
	if fire {
		fn()
	}
}
