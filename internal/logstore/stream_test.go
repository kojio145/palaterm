package logstore

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
		l := NewStream(path, []string{secret, "", "ab"})
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
	l := NewStream(path, []string{"longpassword"})
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
	l := NewStream(path, nil)
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
	l := NewStream(path, nil)
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
	l := NewStream(path, []string{"secretpw"})
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
	l2 := NewStream(path2, nil)
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

// Flush (the log viewer opening, the idle timer) must not write the start
// of a secret that has only half arrived: the file would hold it in clear,
// with its other half appended later, unmasked.
func TestStreamFlushHoldsSecretStart(t *testing.T) {
	const secret = "s3cretPW"
	path := filepath.Join(t.TempDir(), "x.txt")
	l := NewStream(path, []string{secret, "otherlongpw"})
	l.Write([]byte("Password: s3cr"))
	if p, err := l.Flush(); err != nil || p != path {
		t.Fatalf("flush: %q %v", p, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "Password: " {
		t.Fatalf("flush wrote a possible secret start: %q", got)
	}
	l.Write([]byte("etPW\r\nR1#"))
	l.Flush()
	got, _ = os.ReadFile(path)
	// "R1#" starts no secret, so Flush writes it all.
	if string(got) != "Password: ****\r\nR1#" {
		t.Fatalf("after the rest arrived: %q", got)
	}
	l.Close()

	// Flushing after every chunk, whatever the chunking, never leaks and
	// loses nothing.
	transcript := "login: ops\r\nPassword: \r\nR1#show run\r\nusername ops password s3cretPW\r\nenable secret otherlongpw\r\nR1#"
	want := strings.ReplaceAll(strings.ReplaceAll(transcript, secret, "****"), "otherlongpw", "****")
	for size := 1; size <= len(transcript); size++ {
		path := filepath.Join(t.TempDir(), "x.txt")
		l := NewStream(path, []string{secret, "otherlongpw"})
		for i := 0; i < len(transcript); i += size {
			end := min(i+size, len(transcript))
			l.Write([]byte(transcript[i:end]))
			l.Flush()
			if got, _ := os.ReadFile(path); bytes.Contains(got, []byte(secret)) || bytes.Contains(got, []byte("otherlongpw")) {
				t.Fatalf("chunk %d: secret leaked by Flush: %q", size, got)
			}
		}
		l.Close()
		if got, _ := os.ReadFile(path); string(got) != want {
			t.Fatalf("chunk %d: file = %q, want %q", size, got, want)
		}
	}
}

// A closed log stays closed: a pause after Close must not append a marker
// and reopen the file (the handle leaked).
func TestStreamSetPausedAfterCloseIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	l := NewStream(path, nil)
	l.Write([]byte("hello\r\n"))
	l.Close()
	if p, err := l.SetPaused(true); err != nil || p != path {
		t.Fatalf("pause after close: %q %v", p, err)
	}
	if l.f != nil || l.paused {
		t.Fatalf("pause after close reopened the file or took effect")
	}
	if got, _ := os.ReadFile(path); string(got) != "hello\r\n" {
		t.Fatalf("file changed after close: %q", got)
	}
}

// Once a write has failed, what arrives afterwards is not held in memory
// for the rest of the session.
func TestStreamDropsBufferAfterWriteError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The log's folder is a regular file: it cannot be created.
	l := NewStream(filepath.Join(blocker, "x.txt"), nil)
	l.Write([]byte("first\r\n"))
	if _, err := l.Flush(); err == nil {
		t.Fatal("write into a file-as-folder succeeded")
	}
	for i := 0; i < 100; i++ {
		l.Write([]byte("more output that must not pile up\r\n"))
	}
	if len(l.buf) != 0 {
		t.Fatalf("buffer kept %d bytes after the write error", len(l.buf))
	}
	l.Close()
}

// With no secrets (masking off, the default since v1.5.3) the capture is
// written byte for byte.
func TestStreamLogNoSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "raw.txt")
	l := NewStream(p, nil)
	l.Write([]byte("login: admin\r\nusername admin password secret1\r\n"))
	l.Close()
	b, _ := os.ReadFile(p)
	if string(b) != "login: admin\r\nusername admin password secret1\r\n" {
		t.Fatalf("raw log altered: %q", b)
	}
}
