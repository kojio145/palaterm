package main

import (
	"strings"
	"time"
)

// pacedSend returns send with a wait of delay after every line that is
// followed by more text in the same call: a multi-line paste reaches a
// 9600-baud console a line at a time, so the device's line discipline has
// processed one line before the next arrives (a whole config at once lost
// characters). Text with no line ending, a lone Enter, and the last line
// of a paste go out at once, so typing is never held; a zero delay is the
// original send.
func pacedSend(send func(string) error, delay time.Duration) func(string) error {
	if delay <= 0 || send == nil {
		return send
	}
	return func(s string) error {
		for {
			i := strings.IndexByte(s, '\r')
			if i < 0 || i+1 >= len(s) {
				return send(s)
			}
			if err := send(s[:i+1]); err != nil {
				return err
			}
			s = s[i+1:]
			time.Sleep(delay)
		}
	}
}
