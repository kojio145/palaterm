package session

import "testing"

// SSH lines end in a bare CR, like a terminal's Enter key: a pty makes one
// newline of it, where CR LF would be two Enters (doubled prompts, and an
// empty command after every real one on a Unix-style CLI).
func TestSSHSessionLineEndingIsCR(t *testing.T) {
	var s Session = &sshSession{}
	le, ok := s.(LineEnder)
	if !ok {
		t.Fatal("sshSession should declare its line ending")
	}
	if got := le.LineEnding(); got != "\r" {
		t.Fatalf("LineEnding = %q, want CR", got)
	}
}
