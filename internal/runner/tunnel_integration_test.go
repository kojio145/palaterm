package runner

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kojio145/palaterm/internal/model"
)

// fakeTunnelBastion is an SSH jump host that only forwards direct-tcpip
// channels (OpenSSH's ProxyJump use): it has no shell, so a run that typed a
// jump command on it would fail.
func fakeTunnelBastion(t *testing.T) model.Bastion {
	t.Helper()
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(testSigner(t))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "direct-tcpip" {
						nc.Reject(ssh.UnknownChannelType, "forward only")
						continue
					}
					d := nc.ExtraData()
					n := binary.BigEndian.Uint32(d)
					target := net.JoinHostPort(string(d[4:4+n]), strconv.Itoa(int(binary.BigEndian.Uint32(d[4+n:]))))
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
				}
			}()
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return model.Bastion{Method: model.BastionSSH, Host: host, Port: p, Username: "j", Password: "jp"}
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	return host, p
}

// An SSH device behind two tunnel hops: logged in at the transport (no
// login prompts to answer), commands run, and the logout types no "exit"
// for hops that were never shells.
func TestTunnelRunSSHDeviceThroughTwoHops(t *testing.T) {
	addr, stop := fakeCisco(t)
	defer stop()
	host, port := splitHostPort(t, addr)
	dev := &model.Device{
		Name: "router1", Host: host, Conn: model.ConnSSH, Port: port,
		OSType: "cisco-ios", Username: "admin", Password: "x", EnablePassword: "en",
		CommandSet: "backup", Enabled: true,
		Bastions:    []model.Bastion{fakeTunnelBastion(t), fakeTunnelBastion(t)},
		BastionMode: model.BastionViaTunnel,
	}
	if err := dev.Validate(); err != nil {
		t.Fatal(err)
	}
	set := &model.CommandSet{Name: "backup", Commands: []model.Command{{Text: "show version"}}}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.CommandTimeout = 5
	settings.ConnectTimeout = 5

	var events []string
	res := New(testRegistry()).RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(),
		func(e Event) { events = append(events, e.Message) })
	if !res.Success {
		t.Fatalf("run failed: %s\n---\n%s", res.Error, res.Transcript)
	}
	if !strings.Contains(res.Transcript, "Cisco IOS Software, Version 15.7") {
		t.Fatalf("show version output missing:\n%s", res.Transcript)
	}
	if !strings.Contains(strings.Join(events, "\n"), "SSH トンネル") {
		t.Errorf("progress does not say the run goes through a tunnel: %v", events)
	}
	if typedHops(dev) != 0 {
		t.Errorf("typedHops = %d through a tunnel, want 0", typedHops(dev))
	}
}

// A Telnet device behind a tunnel hop: the device's own login prompts are
// answered by its OS profile, as on a direct Telnet connection.
func TestTunnelRunTelnetDevice(t *testing.T) {
	dAddr, dStop := fakeTelnetDevice(t)
	defer dStop()
	host, port := splitHostPort(t, dAddr)
	dev := &model.Device{
		Name: "dev1", Host: host, Port: port, Conn: model.ConnTelnet,
		OSType: "generic", Username: "admin", Password: "pw",
		CommandSet: "chk", Enabled: true,
		Bastions:    []model.Bastion{fakeTunnelBastion(t)},
		BastionMode: model.BastionViaTunnel,
	}
	set := &model.CommandSet{Name: "chk", Commands: []model.Command{{Text: "show version"}}}
	settings := model.DefaultSettings()
	settings.LogDir = t.TempDir()
	settings.ConnectTimeout = 5
	settings.CommandTimeout = 6

	res := New(testRegistry()).RunDevice(context.Background(), dev, set, settings, settings.LogDir, time.Now(), nil)
	if !res.Success {
		t.Fatalf("tunnel run failed: %s\n---\n%s", res.Error, res.Transcript)
	}
	if !strings.Contains(res.Transcript, "dev1#") {
		t.Fatalf("did not reach the device prompt:\n%s", res.Transcript)
	}
}

func TestMissingHopLogin(t *testing.T) {
	ssh := model.Bastion{Host: "jump1.example", Method: model.BastionSSH}
	dev := &model.Device{Name: "fw1", Host: "192.0.2.10", Conn: model.ConnSSH, Bastions: []model.Bastion{ssh}}
	if err := missingHopLogin(dev, dev.ActiveBastions()); err == nil || !strings.Contains(err.Error(), "ユーザー名がありません") {
		t.Errorf("empty first hop: err = %v", err)
	}
	// The own jump host, not registered / without a username: said by name.
	mine := &model.Device{Name: "fw1", Host: "192.0.2.10", Conn: model.ConnSSH, Bastions: []model.Bastion{{Mine: true, Method: model.BastionSSH}}}
	if err := missingHopLogin(mine, mine.ActiveBastions()); err == nil || !strings.Contains(err.Error(), "自分の踏み台が登録されていません") {
		t.Errorf("unregistered own jump host: err = %v", err)
	}
	mine.Bastions[0].Host = "jump1.example"
	if err := missingHopLogin(mine, mine.ActiveBastions()); err == nil || !strings.Contains(err.Error(), "自分の踏み台1段目のユーザー名") {
		t.Errorf("own jump host without a username: err = %v", err)
	}
	withUser := ssh
	withUser.Username = "ops"
	// Typed on the first hop's shell: a later hop may leave the user to
	// the hop's own ssh (e.g. a jump command without {user}).
	dev.Bastions = []model.Bastion{withUser, ssh}
	if err := missingHopLogin(dev, dev.ActiveBastions()); err != nil {
		t.Errorf("shell hop 2 without user refused: %v", err)
	}
	dev.BastionMode = model.BastionViaTunnel
	if err := missingHopLogin(dev, dev.ActiveBastions()); err == nil {
		t.Error("tunnel hop 2 without user accepted")
	}
	telnet := model.Bastion{Host: "jump2.example", Method: model.BastionTelnet}
	dev.BastionMode, dev.Bastions = model.BastionViaShell, []model.Bastion{telnet}
	if err := missingHopLogin(dev, dev.ActiveBastions()); err != nil {
		t.Errorf("telnet hop refused: %v", err)
	}
}
