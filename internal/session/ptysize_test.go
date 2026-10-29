package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
	"golang.org/x/crypto/ssh"
)

// The pty is requested at the size DialOpts carries (an interactive
// window's own screen), and at 120x40 when none is given (a batch run).
func TestPtySizeFromDialOpts(t *testing.T) {
	type size struct{ cols, rows uint32 }
	run := func(opts DialOpts) size {
		cfg := &ssh.ServerConfig{NoClientAuth: true}
		_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
		signer, _ := ssh.NewSignerFromKey(hostPriv)
		cfg.AddHostKey(signer)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		var mu sync.Mutex
		var got size
		seen := make(chan struct{}, 1)
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
			if err != nil {
				return
			}
			defer sc.Close()
			go ssh.DiscardRequests(reqs)
			for nc := range chans {
				ch, chReqs, err := nc.Accept()
				if err != nil {
					return
				}
				go func() {
					for req := range chReqs {
						if req.Type == "pty-req" {
							// string term, uint32 cols, uint32 rows, …
							p := req.Payload
							n := binary.BigEndian.Uint32(p[:4])
							p = p[4+n:]
							mu.Lock()
							got = size{binary.BigEndian.Uint32(p[:4]), binary.BigEndian.Uint32(p[4:8])}
							mu.Unlock()
							seen <- struct{}{}
						}
						req.Reply(req.Type == "shell" || req.Type == "pty-req", nil)
					}
				}()
				go func() { _, _ = io_copyDiscard(ch) }()
			}
		}()
		host, port := splitAddr(t, ln.Addr().String())
		sess, err := dialSSH(host, port, "u", "p", model.AuthPassword, "", "", 2*time.Second, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		select {
		case <-seen:
		case <-time.After(2 * time.Second):
			t.Fatal("no pty-req")
		}
		mu.Lock()
		defer mu.Unlock()
		return got
	}
	if got := run(DialOpts{Cols: 157, Rows: 43}); got != (size{157, 43}) {
		t.Errorf("pty = %+v, want the window's 157x43", got)
	}
	if got := run(DialOpts{}); got != (size{120, 40}) {
		t.Errorf("pty = %+v, want the default 120x40", got)
	}
}
