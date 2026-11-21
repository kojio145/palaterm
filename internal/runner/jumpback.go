package runner

import (
	"regexp"
	"strings"
	"time"
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
// an interactive session comes back to the jump host's shell (backAtJump),
// or, on a session reached without jump hosts, rests on the device's login
// prompt (loggedOutAtLogin; AtLoginWatch tells which).
func (e *expecter) OnBackAtJump(fn func()) {
	e.mu.Lock()
	e.jumpFn = fn
	e.mu.Unlock()
}

// A serial console holds no session either: when the device logs the user
// out (its exec timeout, exit), the port stays open and the console just
// shows the login prompt again, so the window went on reading 接続済み
// with nothing to press, as through jump hosts. The same goes for a Telnet
// or SSH line to a console server's port. On a session reached without
// jump hosts the window watches for the device's login prompt instead:
//
//   - the line the screen is on now asking for the login name ("Username:",
//     "lab1 login:"), not a "Last login:" line;
//   - or the last line Cisco's "Press RETURN to get started." with nothing
//     after it.
//
// A password prompt alone does not count ("enable" asks for one too).
// Because a line may be cut anywhere between two chunks of output, the
// window is told only once the output has paused (loginSettle) on it.

// reLoginLine is a login prompt as the current line, spaces trimmed.
var reLoginLine = regexp.MustCompile(`(?i)(?:^|\s)(?:login|username|user name)\s*:$`)

// reLastLogin is a shell's "Last login: …", cut after its colon.
var reLastLogin = regexp.MustCompile(`(?i)\blast\s+login\s*:$`)

// rePressReturn is Cisco's console line after a session ended.
var rePressReturn = regexp.MustCompile(`(?i)^press return to get started\.?$`)

// loginSettle is how long the output must stay on a login prompt before the
// window is told.
var loginSettle = 400 * time.Millisecond

// loggedOutAtLogin reports that tail, the latest output, ends on the device's
// login prompt.
func loggedOutAtLogin(tail string) bool {
	text := reEscape.ReplaceAllString(tail, "")
	line := currentLine(text)
	if line != "" {
		return (reLoginLine.MatchString(line) && !reLastLogin.MatchString(line)) || rePressReturn.MatchString(line)
	}
	last := ""
	for _, l := range strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' }) {
		if l = strings.TrimSpace(l); l != "" {
			last = l
		}
	}
	return rePressReturn.MatchString(last)
}

// setLoginWatch has a terminal window watch for the device's login prompt
// coming back (a login without jump hosts by commands typed on them).
func (e *expecter) setLoginWatch() {
	e.mu.Lock()
	e.loginWatch = true
	e.mu.Unlock()
}

// AtLoginWatch reports that the watch is for the device's login prompt
// (loggedOutAtLogin) rather than a jump host's shell.
func (e *expecter) AtLoginWatch() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.jumpPrompt == "" && e.loginWatch
}

// watchJump is the read loop's step for a chunk of interactive output.
func (e *expecter) watchJump(chunk []byte) {
	e.mu.Lock()
	if e.jumpFn == nil || (e.jumpPrompt == "" && !e.loginWatch) {
		e.mu.Unlock()
		return
	}
	e.jumpTail += string(chunk)
	if len(e.jumpTail) > jumpTailMax {
		e.jumpTail = e.jumpTail[len(e.jumpTail)-jumpTailMax:]
	}
	if e.jumpPrompt == "" {
		e.watchLoginLocked()
		return
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

// watchLoginLocked is watchJump's step on a session without jump hosts,
// with e.mu held (it unlocks): the window is told when the output comes to
// rest on the device's login prompt, once each time it does.
func (e *expecter) watchLoginLocked() {
	e.loginSeq++
	seq := e.loginSeq
	back := loggedOutAtLogin(e.jumpTail)
	if !back {
		e.jumpBack = false
	}
	arm := back && !e.jumpBack
	e.mu.Unlock()
	if !arm {
		return
	}
	time.AfterFunc(loginSettle, func() {
		e.mu.Lock()
		fire := e.loginSeq == seq && !e.jumpBack && e.jumpFn != nil && !e.closed
		if fire {
			e.jumpBack = true
		}
		fn := e.jumpFn
		e.mu.Unlock()
		if fire {
			fn()
		}
	})
}
