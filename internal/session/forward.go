package session

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// Forward carries a local port through jump hosts to one address — the
// device's web GUI (GUI を開く): every connection to Local comes out of the
// last hop towards Target, the way "ssh -L" does. It runs until Close, or
// until the first hop's connection drops.
type Forward struct {
	Local  string // "127.0.0.1:<port>" the browser opens
	Target string // the device's "host:port", as the last hop dials it

	ln      net.Listener
	clients []*ssh.Client
	via     string
	timeout time.Duration
	once    sync.Once
	done    chan struct{}
}

// DialForward logs into the jump hosts (dialChain) and starts listening on
// a free port of the loopback address listen ("127.0.0.1" when empty) —
// never another interface: the port is an open door to the device for
// whoever reaches it. Each device gets an address of its own (127.0.0.2,
// .3 …) from the caller: cookies are kept per host, not per port, so two
// GUIs both on 127.0.0.1 would log each other out.
func DialForward(hops []model.Bastion, target, listen string, timeout time.Duration, opts DialOpts) (*Forward, error) {
	if listen == "" {
		listen = "127.0.0.1"
	}
	if ip := net.ParseIP(listen); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("転送の待受はループバックアドレスだけです: %q", listen)
	}
	clients, via, err := dialChain(hops, timeout, opts)
	if err != nil {
		return nil, err
	}
	// The hop must let us through before a browser is sent at the port: a
	// refusal (AllowTcpForwarding no, the device unreachable from the hop)
	// is said now, in words, not as a blank browser page.
	probe, err := tunnelConn(clients, via, target, timeout)
	if err != nil {
		closeHops(clients)
		return nil, err
	}
	probe.Close()
	ln, err := net.Listen("tcp", net.JoinHostPort(listen, "0"))
	if err != nil {
		closeHops(clients)
		return nil, err
	}
	f := &Forward{Local: ln.Addr().String(), Target: target, ln: ln, clients: clients, via: via, timeout: timeout, done: make(chan struct{})}
	go f.accept()
	go func() {
		_ = clients[0].Wait() // the first hop's connection is gone
		f.Close()
	}()
	return f, nil
}

func (f *Forward) accept() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.carry(c)
	}
}

func (f *Forward) carry(local net.Conn) {
	defer local.Close()
	remote, err := tunnelConn(f.clients, f.via, f.Target, f.timeout)
	if err != nil {
		return
	}
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	select {
	case <-done:
	case <-f.done:
	}
}

// Done is closed once the forward has stopped.
func (f *Forward) Done() <-chan struct{} { return f.done }

// Close stops listening and logs out of the jump hosts.
func (f *Forward) Close() {
	f.once.Do(func() {
		close(f.done)
		_ = f.ln.Close()
		closeHops(f.clients)
	})
}
