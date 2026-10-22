package session

import (
	"runtime"
	"strings"
	"testing"
)

// A COM port that is not there is said in plain words, naming the port and
// what to check — not the library's "Serial port not found" (review
// 2026-10-22 E11).
func TestSerialNotFoundIsExplained(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("COM port names are Windows'")
	}
	_, err := dialSerial("COM250", 9600)
	if err == nil {
		t.Skip("COM250 exists on this machine")
	}
	if !strings.HasPrefix(err.Error(), "COM250 が見つかりません") {
		t.Fatalf("err = %v", err)
	}
}
