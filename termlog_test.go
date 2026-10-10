package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every way of slicing a transcript into chunks must leave the file equal to
// the whole transcript with its secrets masked — never a secret in clear,
// never a byte lost or duplicated.
func TestStreamLogMasksAcrossChunkBoundaries(t *testing.T) {
	const secret = "s3cretPW"
	transcript := "login: admin\r\nPassword: \r\nR1#show run\r\nusername admin password s3cretPW\r\nenable secret s3cretPW\r\nR1#"
	want := strings.ReplaceAll(transcript, secret, "****")
	for size := 1; size <= len(transcript); size++ {
		path := filepath.Join(t.TempDir(), "x.txt")
		l := newStreamLog(path, []string{secret, "", "ab"})
		for i := 0; i < len(transcript); i += size {
			end := i + size
			if end > len(transcript) {
				end = len(transcript)
			}
			l.Write([]byte(transcript[i:end]))
			// Nothing written so far may contain the secret.
			if got, _ := os.ReadFile(path); bytes.Contains(got, []byte(secret)) {
				t.Fatalf("chunk %d: secret leaked mid-stream: %q", size, got)
			}
		}
		p, err := l.Close()
		if err != nil || p != path {
			t.Fatalf("chunk %d: close %q %v", size, p, err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != want {
			t.Fatalf("chunk %d: file = %q, want %q", size, got, want)
		}
	}
}

func TestStreamLogWritesPromptlyAndIdleFlushesTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	l := newStreamLog(path, []string{"longpassword"})
	l.Write([]byte("R1#show version\r\nIOS 15.2\r\nR1#"))
	got, _ := os.ReadFile(path)
	// Written immediately, except the held tail (len("longpassword")-1 = 11 bytes).
	if !strings.HasPrefix(string(got), "R1#show version\r\n") || len(got) != len("R1#show version\r\nIOS 15.2\r\nR1#")-11 {
		t.Fatalf("immediate write wrong: %q", got)
	}
	time.Sleep(idleFlush + 300*time.Millisecond)
	got, _ = os.ReadFile(path)
	if string(got) != "R1#show version\r\nIOS 15.2\r\nR1#" {
		t.Fatalf("idle flush did not write the tail: %q", got)
	}
	// More data after the idle flush appends in order.
	l.Write([]byte("exit\r\n"))
	p, _ := l.Flush()
	got, _ = os.ReadFile(path)
	if p != path || string(got) != "R1#show version\r\nIOS 15.2\r\nR1#exit\r\n" {
		t.Fatalf("after flush: %q", got)
	}
	l.Close()
}

func TestStreamLogNoFileWithoutData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	l := newStreamLog(path, nil)
	if p, _ := l.Flush(); p != "" {
		t.Fatalf("flush with no data returned %q", p)
	}
	if p, _ := l.Close(); p != "" {
		t.Fatalf("close with no data returned %q", p)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file created without data")
	}
}

// The file stays readable by another process while the session holds it
// open (a user tailing the log in an editor).
func TestStreamLogReadableWhileOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	l := newStreamLog(path, nil)
	l.Write([]byte("hello\r\n"))
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("read while open: %v", err)
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open while open: %v", err)
	}
	f.Close()
	l.Close()
}

func TestStreamLogPauseResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	l := newStreamLog(path, []string{"secretpw"})
	l.Write([]byte("before\r\n"))
	if p, err := l.SetPaused(true); err != nil || p != path {
		t.Fatalf("pause: %q %v", p, err)
	}
	l.Write([]byte("hidden secretpw\r\n"))
	if _, err := l.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	l.Write([]byte("after secretpw\r\n"))
	l.Close()
	got, _ := os.ReadFile(path)
	s := string(got)
	if !strings.HasPrefix(s, "before\r\n") || strings.Contains(s, "hidden") || !strings.Contains(s, "[PalaTerm: log paused") ||
		!strings.Contains(s, "[PalaTerm: log resumed") || !strings.HasSuffix(s, "after ****\r\n") {
		t.Fatalf("pause/resume file wrong: %q", s)
	}
	// Pausing before anything was written leaves no file and no marker.
	path2 := filepath.Join(t.TempDir(), "y.txt")
	l2 := newStreamLog(path2, nil)
	if p, _ := l2.SetPaused(true); p != "" {
		t.Fatalf("pause on empty log returned %q", p)
	}
	l2.SetPaused(false)
	l2.Write([]byte("x"))
	l2.Close()
	if got, _ := os.ReadFile(path2); string(got) != "x" {
		t.Fatalf("empty-then-resume file: %q", got)
	}
}

// With no secrets (masking off, the default since v1.5.3) the capture is
// written byte for byte.
func TestStreamLogNoSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "raw.txt")
	l := newStreamLog(p, nil)
	l.Write([]byte("login: admin\r\nusername admin password secret1\r\n"))
	l.Close()
	b, _ := os.ReadFile(p)
	if string(b) != "login: admin\r\nusername admin password secret1\r\n" {
		t.Fatalf("raw log altered: %q", b)
	}
}
