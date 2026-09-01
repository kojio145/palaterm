package session

import (
	"fmt"
	"net"
	"time"
)

// Telnet command bytes (RFC 854 / 855).
const (
	iac  = 255 // Interpret As Command
	dont = 254
	do   = 253
	wont = 252
	will = 251
	sb   = 250 // subnegotiation begin
	se   = 240 // subnegotiation end
)

// Telnet options this client engages with (RFC 857 / 858).
const (
	optEcho            = 1
	optSuppressGoAhead = 3
)

// telnetSession is a TCP connection that answers option negotiation so that
// plain text flows through: it accepts remote echo and suppress-go-ahead and
// refuses everything else.
type telnetSession struct {
	conn net.Conn
	buf  []byte // leftover decoded bytes
}

func dialTelnet(host string, port int, timeout time.Duration) (Session, error) {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("telnet dial %s: %w", addr, err)
	}
	return &telnetSession{conn: conn}, nil
}

func (t *telnetSession) Write(p []byte) (int, error) {
	return t.conn.Write(p)
}

func (t *telnetSession) Close() error {
	return t.conn.Close()
}

// Read strips IAC negotiation sequences and replies to option requests,
// returning only application data. It loops (rather than recursing) over
// reads that turn out to be negotiation-only, so a chatty peer cannot grow
// the stack.
func (t *telnetSession) Read(p []byte) (int, error) {
	if len(t.buf) > 0 {
		n := copy(p, t.buf)
		t.buf = t.buf[n:]
		return n, nil
	}

	raw := make([]byte, len(p)+16)
	for {
		n, err := t.conn.Read(raw)
		if n == 0 {
			return 0, err
		}

		out := t.decode(raw[:n])
		if len(out) == 0 {
			if err != nil {
				return 0, err
			}
			continue // negotiation only; read again for real data
		}
		nn := copy(p, out)
		if nn < len(out) {
			t.buf = append(t.buf, out[nn:]...)
		}
		return nn, err
	}
}

// decode strips IAC sequences from one raw read, answering option requests.
func (t *telnetSession) decode(raw []byte) []byte {
	n := len(raw)
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		if raw[i] != iac {
			out = append(out, raw[i])
			continue
		}
		// Need at least one more byte for the command.
		if i+1 >= n {
			break
		}
		cmd := raw[i+1]
		switch cmd {
		case iac: // escaped 0xFF => literal 0xFF
			out = append(out, iac)
			i++
		case will, wont, do, dont:
			if i+2 >= n {
				i = n
				break
			}
			opt := raw[i+2]
			t.answer(cmd, opt)
			i += 2
		case sb: // skip subnegotiation until IAC SE
			j := i + 2
			for j+1 < n && !(raw[j] == iac && raw[j+1] == se) {
				j++
			}
			i = j + 1
		default:
			i++ // 2-byte command with no option
		}
	}
	return out
}

// answer replies to an option request: everything is refused except remote
// echo and suppress-go-ahead, the pair that puts the session in the ordinary
// character-at-a-time mode network gear expects.
//
// Refusing those two is not merely suboptimal — a NEC IX2105 opens with
// "IAC WILL ECHO, IAC WILL SUPPRESS-GO-AHEAD" and closes the connection the
// moment a username arrives on a session where the client said DONT to both.
// The login then failed with nothing in the log but the device's "login: "
// prompt, since the drop looked like a device that simply never answered.
func (t *telnetSession) answer(cmd, opt byte) {
	var reply byte
	switch cmd {
	case will:
		if opt == optEcho || opt == optSuppressGoAhead {
			reply = do // let the far end echo and run full duplex
		} else {
			reply = dont
		}
	case do:
		if opt == optSuppressGoAhead {
			reply = will
		} else {
			reply = wont // no terminal type, window size, or the rest
		}
	default:
		return // WONT/DONT need no acknowledgement
	}
	_, _ = t.conn.Write([]byte{iac, reply, opt})
}
