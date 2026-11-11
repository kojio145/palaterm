package main

import (
	"reflect"
	"testing"
	"time"
)

// A paste of several lines goes out one line per send, with the pause
// between them; a lone Enter, a single key, and a one-line paste are sent
// as they are; a zero delay leaves send alone.
func TestPacedSendSplitsLines(t *testing.T) {
	var got []string
	send := func(s string) error { got = append(got, s); return nil }
	p := pacedSend(send, time.Millisecond)
	for _, in := range []string{"a", "\r", "show ver\r", "conf t\rhostname lab-rt1\rend\r", "no-eol\rtail"} {
		got = nil
		start := time.Now()
		if err := p(in); err != nil {
			t.Fatal(err)
		}
		var want []string
		switch in {
		case "conf t\rhostname lab-rt1\rend\r":
			want = []string{"conf t\r", "hostname lab-rt1\r", "end\r"}
		case "no-eol\rtail":
			want = []string{"no-eol\r", "tail"}
		default:
			want = []string{in}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: sent %q, want %q", in, got, want)
		}
		if n := len(want) - 1; n > 0 && time.Since(start) < time.Duration(n)*time.Millisecond {
			t.Errorf("%q: no pause between the lines", in)
		}
	}
	if reflect.ValueOf(pacedSend(send, 0)).Pointer() != reflect.ValueOf(send).Pointer() {
		t.Error("a zero delay must return send itself")
	}
}
