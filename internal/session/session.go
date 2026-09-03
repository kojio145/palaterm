// Package session provides the transport layer: SSH, Telnet and serial
// console connections behind one io.ReadWriteCloser abstraction. Multi-hop
// jump-host traversal is driven by the runner (which owns the expect engine);
// this package only opens the first transport.
package session

import (
	"fmt"
	"io"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// Session is a bidirectional byte stream to a device's interactive shell.
type Session interface {
	io.ReadWriteCloser
}

// Resizer is implemented by sessions whose terminal size can change (SSH).
// Telnet and serial sessions do not implement it.
type Resizer interface {
	Resize(cols, rows int) error
}

// LineEnder is implemented by transports that end a line with something other
// than the CRLF the network protocols use. The transport owns this because it
// is a property of how the far end reads bytes, not of what is being sent.
type LineEnder interface {
	LineEnding() string
}

// DialDirect opens a session straight to a device (no jump host).
func DialDirect(d *model.Device, connectTimeout time.Duration, opts DialOpts) (Session, error) {
	switch d.Conn {
	case model.ConnSSH:
		return dialSSH(d.Host, d.EffectivePort(), d.Username, d.Password,
			d.AuthMethod, d.KeyFile, d.KeyPassphrase, connectTimeout, opts)
	case model.ConnTelnet:
		return dialTelnet(d.Host, d.EffectivePort(), connectTimeout)
	case model.ConnSerial:
		return dialSerial(d.SerialPort, d.EffectiveBaud())
	default:
		return nil, fmt.Errorf("unknown connection method %q", d.Conn)
	}
}

// effectiveBastionLegacy resolves which algorithm policy a jump host is dialed
// with. It is the hop's own setting, never the device's: the two endpoints are
// different machines, so one of them being old is no reason to weaken the
// other. devOpts is accepted (and deliberately ignored for this field) to keep
// the decision in one named place rather than an assignment easily lost in a
// later edit.
func effectiveBastionLegacy(b *model.Bastion, devOpts DialOpts) bool {
	return b.LegacyAlgos
}

// DialBastionHead opens the transport to the first jump host in a chain. For an
// SSH bastion, authentication happens here; for a Telnet bastion, the caller
// (runner) answers the login prompts over the returned stream.
func DialBastionHead(b *model.Bastion, connectTimeout time.Duration, opts DialOpts) (Session, error) {
	switch b.Method {
	case model.BastionSSH:
		port := b.Port
		if port == 0 {
			port = 22
		}
		opts.Legacy = effectiveBastionLegacy(b, opts)
		return dialSSH(b.Host, port, b.Username, b.Password, b.AuthMethod, b.KeyFile, b.KeyPassphrase, connectTimeout, opts)
	case model.BastionTelnet:
		port := b.Port
		if port == 0 {
			port = 23
		}
		return dialTelnet(b.Host, port, connectTimeout)
	default:
		return nil, fmt.Errorf("unknown bastion method %q", b.Method)
	}
}
