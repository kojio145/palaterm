package session

import (
	"errors"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

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

// "ssh: unexpected message type 51 (expected 60)" is a refused login (51 is
// SSH_MSG_USERAUTH_FAILURE), said in words that point at the credentials
// (review 2026-10-22 D-2).
func TestAuthFailureHintMessage51(t *testing.T) {
	err := errors.New("ssh: handshake failed: ssh: unexpected message type 51 (expected 60)")
	got := authFailureHint(err, "ops", model.AuthPassword)
	if !strings.HasPrefix(got, "ユーザー名またはパスワードが違います") || !strings.Contains(got, "認証方式") {
		t.Fatalf("hint = %q", got)
	}
}
