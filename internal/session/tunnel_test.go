package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// tunnelHop is a fake SSH jump host or device: it logs in user "ops" with
// password "pw" (or the key in authKey), forwards direct-tcpip channels unless
// noForward, and answers a shell with prompt.
type tunnelHop struct {
	addr      string
	noForward bool
	authKey   ssh.PublicKey
	prompt    string
	mu        sync.Mutex
	forwarded []string // direct-tcpip targets asked for
}

func startTunnelHop(t *testing.T, prompt string, noForward bool, authKey ssh.PublicKey) *tunnelHop {
	t.Helper()
	h := &tunnelHop{noForward: noForward, authKey: authKey, prompt: prompt}
	cfg := &ssh.ServerConfig{}
	if authKey == nil {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == "ops" && string(pw) == "pw" {
				return nil, nil
			}
			return nil, errorString("denied")
		}
	} else {
		cfg.PublicKeyCallback = func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == "ops" && string(k.Marshal()) == string(authKey.Marshal()) {
				return nil, nil
			}
			return nil, errorString("denied")
		}
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	h.addr = ln.Addr().String()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go h.serve(c, cfg)
		}
	}()
	return h
}

func (h *tunnelHop) serve(c net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		switch nc.ChannelType() {
		case "direct-tcpip":
			if h.noForward {
				nc.Reject(ssh.Prohibited, "forwarding disabled")
				continue
			}
			// RFC 4254 7.2: host string, port uint32, origin string, port.
			d := nc.ExtraData()
			n := binary.BigEndian.Uint32(d)
			host := string(d[4 : 4+n])
			port := binary.BigEndian.Uint32(d[4+n:])
			target := net.JoinHostPort(host, strconv.Itoa(int(port)))
			h.mu.Lock()
			h.forwarded = append(h.forwarded, target)
			h.mu.Unlock()
			out, err := net.Dial("tcp", target)
			if err != nil {
				nc.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}
			ch, rq, err := nc.Accept()
			if err != nil {
				out.Close()
				continue
			}
			go ssh.DiscardRequests(rq)
			go func() { io.Copy(ch, out); ch.Close() }()
			go func() { io.Copy(out, ch); out.Close() }()
		case "session":
			ch, rq, err := nc.Accept()
			if err != nil {
				continue
			}
			go func() {
				for r := range rq {
					r.Reply(r.Type == "pty-req" || r.Type == "shell", nil)
				}
			}()
			go func() {
				ch.Write([]byte(h.prompt))
				buf := make([]byte, 256)
				for {
					n, err := ch.Read(buf)
					if err != nil {
						return
					}
					ch.Write(buf[:n]) // echo
				}
			}()
		default:
			nc.Reject(ssh.UnknownChannelType, "no")
		}
	}
}

func hopOf(t *testing.T, h *tunnelHop) model.Bastion {
	host, port := splitAddr(t, h.addr)
	return model.Bastion{Host: host, Port: port, Method: model.BastionSSH, Username: "ops", Password: "pw"}
}

func readUntil(t *testing.T, r io.Reader, want string) {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		var all []byte
		buf := make([]byte, 256)
		for !strings.Contains(string(all), want) {
			n, err := r.Read(buf)
			if err != nil {
				break
			}
			all = append(all, buf[:n]...)
		}
		got <- string(all)
	}()
	select {
	case s := <-got:
		if !strings.Contains(s, want) {
			t.Fatalf("want %q, got %q", want, s)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}

// Two jump hosts and an SSH device: every hop is an SSH login of this PC's,
// each through the previous one, and each host key is pinned under the chain
// that leads to it.
func TestDialTunnelThroughTwoHopsToSSHDevice(t *testing.T) {
	h1 := startTunnelHop(t, "hop1$ ", false, nil)
	h2 := startTunnelHop(t, "hop2$ ", false, nil)
	devSrv := startTunnelHop(t, "dev# ", false, nil)
	host, port := splitAddr(t, devSrv.addr)
	dev := &model.Device{Host: host, Port: port, Conn: model.ConnSSH, Username: "ops", Password: "pw"}
	store := &mapStore{m: map[string]string{}}

	s, err := DialTunnel([]model.Bastion{hopOf(t, h1), hopOf(t, h2)}, dev, 5*time.Second, DialOpts{HostKey: VerifyHostKey(store)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	readUntil(t, s, "dev# ")
	if _, err := s.Write([]byte("show ver")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, s, "show ver")

	if got := h1.forwarded; len(got) != 1 || got[0] != h2.addr {
		t.Errorf("hop1 forwarded %v, want [%s]", got, h2.addr)
	}
	if got := h2.forwarded; len(got) != 1 || got[0] != devSrv.addr {
		t.Errorf("hop2 forwarded %v, want [%s]", got, devSrv.addr)
	}
	want := []string{h1.addr, h1.addr + ">" + h2.addr, h1.addr + ">" + h2.addr + ">" + devSrv.addr}
	for _, k := range want {
		if store.m[k] == "" {
			t.Errorf("host key not pinned under %q (have %v)", k, store.m)
		}
	}
	d := *dev
	d.Bastions = []model.Bastion{hopOf(t, h1), hopOf(t, h2)}
	d.BastionMode = model.BastionViaTunnel
	if got := d.HostKeyNames(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("HostKeyNames = %v, want %v", got, want)
	}
}

// The point of the tunnel: the second hop logs in with a private key on this
// PC, which a command typed on the first hop could never use.
func TestDialTunnelUsesThisPCsKeyOnSecondHop(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	h1 := startTunnelHop(t, "hop1$ ", false, nil)
	h2 := startTunnelHop(t, "hop2$ ", false, sshPub)
	devSrv := startTunnelHop(t, "dev# ", false, nil)
	b2 := hopOf(t, h2)
	b2.Password, b2.AuthMethod, b2.KeyFile = "", model.AuthPublicKey, keyFile
	host, port := splitAddr(t, devSrv.addr)
	dev := &model.Device{Host: host, Port: port, Conn: model.ConnSSH, Username: "ops", Password: "pw"}

	s, err := DialTunnel([]model.Bastion{hopOf(t, h1), b2}, dev, 5*time.Second, DialOpts{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	readUntil(t, s, "dev# ")
}

// A Telnet device behind an SSH jump host: the stream is the device's own.
func TestDialTunnelToTelnetDevice(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Write([]byte("\r\nlogin: "))
			time.Sleep(time.Second)
			c.Close()
		}
	}()
	h1 := startTunnelHop(t, "hop1$ ", false, nil)
	host, port := splitAddr(t, ln.Addr().String())
	dev := &model.Device{Host: host, Port: port, Conn: model.ConnTelnet}

	s, err := DialTunnel([]model.Bastion{hopOf(t, h1)}, dev, 5*time.Second, DialOpts{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	readUntil(t, s, "login: ")
}

// A jump host that refuses forwarding says so, and what to do about it.
func TestDialTunnelForwardingRefused(t *testing.T) {
	h1 := startTunnelHop(t, "hop1$ ", true, nil)
	dev := &model.Device{Host: "192.0.2.10", Port: 22, Conn: model.ConnSSH, Username: "ops", Password: "pw"}
	_, err := DialTunnel([]model.Bastion{hopOf(t, h1)}, dev, 5*time.Second, DialOpts{})
	if err == nil || !strings.Contains(err.Error(), "転送を許可していません") {
		t.Fatalf("want a forwarding-refused error, got %v", err)
	}
}

// A far end reached through the tunnel that never speaks SSH does not hold
// the dial past the connect timeout (a channel has no deadlines of its own).
func TestDialTunnelHandshakeTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // accept and stay silent
		}
	}()
	h1 := startTunnelHop(t, "hop1$ ", false, nil)
	host, port := splitAddr(t, ln.Addr().String())
	dev := &model.Device{Host: host, Port: port, Conn: model.ConnSSH, Username: "ops", Password: "pw"}
	start := time.Now()
	_, err = DialTunnel([]model.Bastion{hopOf(t, h1)}, dev, 700*time.Millisecond, DialOpts{})
	if err == nil {
		t.Fatal("want a timeout")
	}
	if el := time.Since(start); el > 4*time.Second {
		t.Fatalf("dial took %v", el)
	}
	if !strings.Contains(err.Error(), "タイムアウト") {
		t.Fatalf("want a timeout error, got %v", err)
	}
}

// A Telnet jump host cannot carry a tunnel.
func TestDialTunnelRejectsTelnetHop(t *testing.T) {
	dev := &model.Device{Host: "192.0.2.10", Conn: model.ConnSSH}
	_, err := DialTunnel([]model.Bastion{{Host: "192.0.2.1", Method: model.BastionTelnet}}, dev, time.Second, DialOpts{})
	if err == nil || !strings.Contains(err.Error(), "すべて SSH") {
		t.Fatalf("got %v", err)
	}
}
