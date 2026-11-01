package session

import (
	"slices"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// Review 2026-11-01 (J #6): host-key signature algorithms and MACs are
// pinned to the modern set; ssh-rsa (SHA-1), ssh-dss and the SHA-1 MACs
// come only with レガシー暗号を許可, like the old KEX and ciphers.
func TestReview1101AlgorithmLists(t *testing.T) {
	cfg, err := sshConfig("ops", "pw", model.AuthPassword, "", "", time.Second, DialOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"ssh-rsa", "ssh-dss"} {
		if slices.Contains(cfg.HostKeyAlgorithms, old) {
			t.Errorf("%s offered by default", old)
		}
	}
	for _, want := range []string{"ssh-ed25519", "ecdsa-sha2-nistp256", "rsa-sha2-512", "rsa-sha2-256", "ssh-ed25519-cert-v01@openssh.com", "rsa-sha2-256-cert-v01@openssh.com"} {
		if !slices.Contains(cfg.HostKeyAlgorithms, want) {
			t.Errorf("%s not offered", want)
		}
	}
	for _, old := range []string{"hmac-sha1", "hmac-sha1-96"} {
		if slices.Contains(cfg.MACs, old) {
			t.Errorf("%s offered by default", old)
		}
	}
	if !slices.Contains(cfg.MACs, "hmac-sha2-256-etm@openssh.com") || !slices.Contains(cfg.MACs, "hmac-sha2-512") {
		t.Errorf("sha2 MACs missing: %v", cfg.MACs)
	}
	// Every name is one the library implements: an unknown one is dropped
	// silently, and the list would be shorter than it reads.
	sup := ssh.SupportedAlgorithms()
	ins := ssh.InsecureAlgorithms()
	legacy, _ := sshConfig("ops", "pw", model.AuthPassword, "", "", time.Second, DialOpts{Legacy: true})
	for _, a := range legacy.HostKeyAlgorithms {
		if !slices.Contains(sup.HostKeys, a) && !slices.Contains(ins.HostKeys, a) {
			t.Errorf("host key algorithm %s is not one the library has", a)
		}
	}
	for _, m := range legacy.MACs {
		if !slices.Contains(sup.MACs, m) && !slices.Contains(ins.MACs, m) {
			t.Errorf("MAC %s is not one the library has", m)
		}
	}
	for _, old := range []string{"ssh-rsa", "ssh-dss"} {
		if !slices.Contains(legacy.HostKeyAlgorithms, old) {
			t.Errorf("legacy: %s not offered", old)
		}
	}
	if !slices.Contains(legacy.MACs, "hmac-sha1") {
		t.Errorf("legacy: hmac-sha1 not offered")
	}
}

// Review 2026-11-01 (B B-5 / J #7): keyboard-interactive answers the
// password question only — by its words, or an echo-off field — and only
// once; a one-time-code field and a second round get "".
func TestReview1101KeyboardInteractiveAnswersThePasswordOnly(t *testing.T) {
	ch := keyboardInteractive("pw")
	// An information-only round first (no questions): nothing is spent.
	if ans, _ := ch("ops", "welcome", nil, nil); len(ans) != 0 {
		t.Fatalf("answers to no questions: %v", ans)
	}
	ans, err := ch("ops", "", []string{"Verification code: ", "Password: "}, []bool{true, false})
	if err != nil {
		t.Fatal(err)
	}
	if ans[0] != "" || ans[1] != "pw" {
		t.Errorf("answers %q, want the password in the password field only", ans)
	}
	// A second round: the password is not typed again.
	ans, _ = ch("ops", "", []string{"Password: "}, []bool{false})
	if ans[0] != "" {
		t.Errorf("second round answered %q", ans)
	}
	// The words alone, with echo on, still count as the password's question.
	ch = keyboardInteractive("pw")
	ans, _ = ch("ops", "", []string{"パスワード: "}, []bool{true})
	if ans[0] != "pw" {
		t.Errorf("a localized password question got %q", ans)
	}
	// Echo-off fields with no words: the first gets it, the rest do not.
	ch = keyboardInteractive("pw")
	ans, _ = ch("ops", "", []string{"", ""}, []bool{false, false})
	if ans[0] != "pw" || ans[1] != "" {
		t.Errorf("two echo-off fields got %q", ans)
	}
}

// Review 2026-11-01: the レガシー暗号 hint now covers all four categories
// (it adds host-key and MAC algorithms too), and is silent once on.
func TestReview1101AlgorithmHintCoversHostKeyAndMAC(t *testing.T) {
	mk := func(what string) error {
		return errorString("ssh: handshake failed: ssh: no common algorithm for " + what + "; we offered: [\"a\"], peer offered: [\"b\"]")
	}
	for _, what := range []string{"key exchange", "host key", "client to server cipher", "server to client MAC"} {
		if got := algorithmHint(mk(what), DialOpts{}); got == "" {
			t.Errorf("%s: no hint", what)
		}
		if got := algorithmHint(mk(what), DialOpts{Legacy: true}); got != "" {
			t.Errorf("%s with legacy on: hint %q", what, got)
		}
	}
}
