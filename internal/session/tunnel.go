package session

import (
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// DialTunnel reaches dev through its jump hosts the way OpenSSH's ProxyJump
// does: an SSH login to the first hop, then each next hop — and finally the
// device — over a TCP channel opened through the previous one (direct-tcpip).
// Every hop is logged into from this PC, so its key file, its passphrase, its
// レガシー暗号 setting and its host-key pin all apply, which typing "ssh" on a
// hop's shell cannot offer. The device is an SSH login of its own or a
// Telnet stream; nothing is typed on any hop.
//
// opts.Legacy is the device's setting; each hop uses its own. Host keys are
// pinned under model.TunnelName, the chain that leads to the endpoint.
func DialTunnel(hops []model.Bastion, dev *model.Device, timeout time.Duration, opts DialOpts) (Session, error) {
	// Settled before anything is dialed: a device that cannot be reached
	// this way gets no hop logged into and no stray connection in its log.
	switch dev.Conn {
	case model.ConnTelnet, model.ConnSSH:
	default:
		return nil, fmt.Errorf("SSH トンネルで接続できるのは SSH と Telnet の機器だけです（%s）", dev.Conn)
	}
	clients, via, err := dialChain(hops, timeout, opts)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (Session, error) {
		closeHops(clients)
		return nil, err
	}

	addr := model.HopAddr(dev.Host, dev.EffectivePort())
	conn, err := tunnelConn(clients, via, addr, timeout)
	if err != nil {
		return fail(fmt.Errorf("機器（%s）: %w", dev.Host, err))
	}
	if dev.Conn == model.ConnTelnet {
		return &telnetSession{conn: conn, hops: clients}, nil
	}
	cfg, err := sshConfig(dev.Username, dev.Password, dev.AuthMethod, dev.KeyFile, dev.KeyPassphrase, timeout, opts)
	if err != nil {
		conn.Close()
		return fail(err)
	}
	name := model.TunnelName(via, addr)
	var s *sshSession
	err = withTimeout(conn, name, timeout, func() error {
		c, err := sshHandshakeNoTimer(conn, name, cfg)
		if err != nil {
			return sshDialError(name, err, dev.Username, dev.AuthMethod, opts)
		}
		if s, err = startShell(c, opts); err != nil {
			c.Close()
			return err
		}
		return nil
	})
	if err != nil {
		if s != nil {
			s.client.Close()
		}
		return fail(err)
	}
	startKeepalive(s.client)
	s.hops = clients
	return s, nil
}

// dialChain logs into each jump host in turn from this PC, every next one
// through a channel of the previous (see DialTunnel), and returns the
// clients with the name the last one is pinned under. All hops must be SSH.
func dialChain(hops []model.Bastion, timeout time.Duration, opts DialOpts) ([]*ssh.Client, string, error) {
	if len(hops) == 0 {
		return nil, "", errors.New("踏み台がありません")
	}
	var clients []*ssh.Client
	fail := func(err error) ([]*ssh.Client, string, error) {
		closeHops(clients)
		return nil, "", err
	}
	via := ""
	for i := range hops {
		b := &hops[i]
		if b.Method != model.BastionSSH {
			return fail(fmt.Errorf("SSH トンネルで通る踏み台はすべて SSH にしてください（%d段目が %s）", i+1, b.Method))
		}
		addr := model.HopAddr(b.Host, b.EffectivePort())
		hopOpts := opts
		hopOpts.Legacy = effectiveBastionLegacy(b, opts)
		cfg, err := sshConfig(b.Username, b.Password, b.AuthMethod, b.KeyFile, b.KeyPassphrase, timeout, hopOpts)
		if err != nil {
			return fail(fmt.Errorf("踏み台%d段目（%s）: %w", i+1, b.Host, err))
		}
		conn, err := tunnelConn(clients, via, addr, timeout)
		if err != nil {
			return fail(fmt.Errorf("踏み台%d段目（%s）: %w", i+1, b.Host, err))
		}
		name := model.TunnelName(via, addr)
		c, err := sshHandshake(conn, name, cfg, timeout)
		if err != nil {
			return fail(fmt.Errorf("踏み台%d段目（%s）: %w", i+1, b.Host, sshDialError(name, err, b.Username, b.AuthMethod, hopOpts)))
		}
		startKeepalive(c)
		clients = append(clients, c)
		via = name
	}
	return clients, via, nil
}

// tunnelConn opens a TCP stream to addr: straight from this PC for the first
// hop, otherwise as a channel through the last hop in clients (reached as
// via). A hop that refuses the channel says why, in words the user can act on.
func tunnelConn(clients []*ssh.Client, via, addr string, timeout time.Duration) (net.Conn, error) {
	if len(clients) == 0 {
		return net.DialTimeout("tcp", addr, timeout)
	}
	last := clients[len(clients)-1]
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := last.Dial("tcp", addr)
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, channelError(via, addr, r.err)
		}
		return r.c, nil
	case <-time.After(timeout):
		// The hop is still trying; closing the chain (the caller does) ends it.
		go func() {
			if r := <-ch; r.c != nil {
				r.c.Close()
			}
		}()
		return nil, fmt.Errorf("踏み台（%s）から %s への接続がタイムアウトしました", via, addr)
	}
}

// channelError explains a refused direct-tcpip channel.
func channelError(via, addr string, err error) error {
	var oc *ssh.OpenChannelError
	if errors.As(err, &oc) {
		switch oc.Reason {
		case ssh.Prohibited:
			return fmt.Errorf("踏み台（%s）が転送を許可していません（%w）。ルーターなど転送できない踏み台では、踏み台の通り方を「踏み台上でコマンドを打つ」にしてください（Linux の踏み台なら sshd の AllowTcpForwarding を確認）", via, err)
		case ssh.ConnectionFailed:
			return fmt.Errorf("踏み台（%s）から %s へ接続できません（%w）", via, addr, err)
		case ssh.UnknownChannelType:
			return fmt.Errorf("踏み台（%s）は SSH トンネル（direct-tcpip）に対応していません（%w）。踏み台の通り方を「踏み台上でコマンドを打つ」にしてください", via, err)
		}
	}
	return fmt.Errorf("踏み台（%s）から %s への転送に失敗: %w", via, addr, err)
}

// sshHandshake completes an SSH login over conn within timeout. name is the
// endpoint's name for the host-key check.
func sshHandshake(conn net.Conn, name string, cfg *ssh.ClientConfig, timeout time.Duration) (*ssh.Client, error) {
	var c *ssh.Client
	err := withTimeout(conn, name, timeout, func() error {
		var err error
		c, err = sshHandshakeNoTimer(conn, name, cfg)
		return err
	})
	if err != nil && c != nil {
		c.Close()
		c = nil
	}
	return c, err
}

func sshHandshakeNoTimer(conn net.Conn, name string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	cc, chans, reqs, err := ssh.NewClientConn(conn, name, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(cc, chans, reqs), nil
}

// withTimeout runs f, closing conn if it takes longer than timeout. A
// channel through a hop has no deadlines (x/crypto's channel conn refuses
// them), and a far end that accepts the stream and never speaks SSH would
// otherwise hold the run forever — see dialSSHClient.
func withTimeout(conn net.Conn, name string, timeout time.Duration, f func() error) error {
	timer := time.AfterFunc(timeout, func() { conn.Close() })
	err := f()
	if !timer.Stop() {
		if err == nil {
			err = errors.New("timeout")
		}
		return fmt.Errorf("%s の応答がタイムアウトしました: %w", name, err)
	}
	return err
}

// closeHops closes jump-host connections, innermost first.
func closeHops(hops []*ssh.Client) {
	for i := len(hops) - 1; i >= 0; i-- {
		_ = hops[i].Close()
	}
}
