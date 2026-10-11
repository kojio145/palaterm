package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// The field case (2026-10-11): an ALAXALA behind a Linux jump host has a DSA
// host key only, and the hop's OpenSSH refuses it. The refusal names what
// the switch offers, so the jump is retried with that allowed — and again
// for the key exchange, which the same old switch also fails on.
func TestJumpToRetriesWithTheOfferedAlgorithms(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	var got []string
	done := make(chan error, 1)
	go func() {
		done <- jumpTo(context.Background(), exp, "", model.BastionSSH, "10.239.1.18", "art-admin", 0, 5*time.Second)
	}()
	got = append(got, dev.next(t))
	dev.say("Unable to negotiate with 10.239.1.18 port 22: no matching host key type found. Their offer: ssh-dss\r\n[koji@ts21 ~]$ ")
	got = append(got, dev.next(t))
	dev.say("Unable to negotiate with 10.239.1.18 port 22: no matching key exchange method found. Their offer: diffie-hellman-group1-sha1,diffie-hellman-group14-sha1\r\n[koji@ts21 ~]$ ")
	got = append(got, dev.next(t))
	dev.say("\r\nlogin: ")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("jumpTo: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("jumpTo did not return")
	}
	if strings.TrimSpace(got[0]) != "ssh art-admin@10.239.1.18" {
		t.Errorf("first attempt = %q", got[0])
	}
	if strings.TrimSpace(got[1]) != "ssh -o HostKeyAlgorithms=+ssh-dss art-admin@10.239.1.18" {
		t.Errorf("second attempt = %q", got[1])
	}
	if strings.TrimSpace(got[2]) != "ssh -o HostKeyAlgorithms=+ssh-dss -o KexAlgorithms=+diffie-hellman-group1-sha1,diffie-hellman-group14-sha1 art-admin@10.239.1.18" {
		t.Errorf("third attempt = %q", got[2])
	}
	// The device's own login prompt must be left for the OS profile.
	if !exp.Seen("login:") {
		t.Error("the device's login prompt was consumed")
	}
}

// The same refusal twice means the option did not take (an ssh that does
// not understand it, say): give up with the hop's line in the error rather
// than loop.
func TestJumpToGivesUpWhenTheOfferRepeats(t *testing.T) {
	dev, sess := newScriptedDevice()
	exp := newExpecter(sess)
	defer exp.Close()

	done := make(chan error, 1)
	go func() {
		done <- jumpTo(context.Background(), exp, "", model.BastionSSH, "10.0.0.5", "u", 0, 5*time.Second)
	}()
	for i := 0; i < 2; i++ {
		dev.next(t)
		dev.say("Unable to negotiate with 10.0.0.5 port 22: no matching cipher found. Their offer: aes128-cbc\r\n[u@hop ~]$ ")
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "aes128-cbc") || !strings.Contains(err.Error(), "10.0.0.5") {
			t.Fatalf("want an error naming the offer and the host, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("jumpTo did not return")
	}
}

func TestWithSSHOptions(t *testing.T) {
	cases := []struct {
		cmd  string
		opts []string
		want string
	}{
		{"ssh u@h", nil, "ssh u@h"},
		{"ssh u@h", []string{"HostKeyAlgorithms=+ssh-dss"}, "ssh -o HostKeyAlgorithms=+ssh-dss u@h"},
		{"ssh -p 2222 u@h", []string{"A=+x", "B=+y"}, "ssh -o A=+x -o B=+y -p 2222 u@h"},
		{"telnet h", []string{"A=+x"}, "telnet h"},
		{"connect h", []string{"A=+x"}, "connect h"},
	}
	for _, c := range cases {
		if got := withSSHOptions(c.cmd, c.opts); got != c.want {
			t.Errorf("withSSHOptions(%q, %v) = %q, want %q", c.cmd, c.opts, got, c.want)
		}
	}
}

func TestNegotiateFixReadsTheNewestRefusal(t *testing.T) {
	tr := "x\r\nUnable to negotiate with h port 22: no matching host key type found. Their offer: ssh-dss\r\n$ ssh\r\n" +
		"Unable to negotiate with h port 22: no matching MAC found. Their offer: hmac-md5,hmac-sha1\r\n$ "
	if got := negotiateFix(tr); got != "MACs=+hmac-md5,hmac-sha1" {
		t.Errorf("negotiateFix = %q", got)
	}
	if got := negotiateFix("nothing here"); got != "" {
		t.Errorf("negotiateFix on plain text = %q", got)
	}
}
