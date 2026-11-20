package main

// The fixes of the 2026-11-20 second review of the v1.10.5 candidate.

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// labProxy answers every CONNECT with code (as a company proxy that wants
// a login, or blocks the site, does).
func labProxy(t *testing.T, code int, text string) *url.URL {
	t.Helper()
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
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil || strings.TrimSpace(line) == "" {
						break
					}
				}
				fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nProxy-Authenticate: Basic realm=\"lab\"\r\nContent-Length: 0\r\n\r\n", code, text)
			}()
		}
	}()
	u, _ := url.Parse("http://" + ln.Addr().String())
	return u
}

// A proxy's 407 to the CONNECT of an https request comes back from Go as
// an error, not as a response: the words for it must still be the ones
// about authentication (whatever status text the proxy sends), and a 403
// names the proxy.
func TestProxyRefusalOfConnectIsNamed(t *testing.T) {
	for _, c := range []struct {
		code       int
		text, want string
	}{
		{407, "Proxy Authentication Required", "プロキシが認証を求めました（HTTP 407）"},
		{407, "authenticationrequired", "プロキシが認証を求めました（HTTP 407）"},
		{403, "Forbidden", "プロキシに断られました（HTTP 403）"},
		{502, "Bad Gateway", "インターネットに出られない PC では使えません"},
	} {
		tr := updateTransport.Clone()
		tr.Proxy = http.ProxyURL(labProxy(t, c.code, c.text))
		cl := &http.Client{Timeout: 10 * time.Second, Transport: tr}
		resp, err := cl.Get("https://lab.invalid/x")
		if err == nil {
			resp.Body.Close()
			t.Fatalf("%d: a response came back", c.code)
		}
		t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
		got := connectErr("GitHub に接続できませんでした", err).Error()
		if !strings.Contains(got, c.want) {
			t.Errorf("%d %s: %s", c.code, c.text, got)
		}
		if c.code == 407 && got != proxyAuthMsg {
			t.Errorf("407 says the same as statusErr: %s", got)
		}
	}
}

// With the policy that makes the proxy settings per-machine, the machine's
// settings count and the user's (left over from before) do not.
func TestPickProxyVals(t *testing.T) {
	user := proxyVals{server: "user.lab.example:8080"}
	machine := proxyVals{server: "machine.lab.example:8080", bypass: "<local>"}
	policy := proxyVals{server: "policy.lab.example:8080"}
	if got := pickProxyVals(false, user, machine, policy); got != user {
		t.Errorf("per user: %+v", got)
	}
	if got := pickProxyVals(true, user, machine, policy); got != machine {
		t.Errorf("per machine: %+v", got)
	}
	if got := pickProxyVals(true, user, proxyVals{}, policy); got != policy {
		t.Errorf("per machine, set in the policy key: %+v", got)
	}
	if got := pickProxyVals(true, user, proxyVals{script: true}, policy); !got.script || got.server != "" {
		t.Errorf("per machine, a setup script: %+v", got)
	}
	if got := pickProxyVals(true, user, proxyVals{}, proxyVals{}); got.server != "" || got.script {
		t.Errorf("per machine, nothing set: the user's is not used: %+v", got)
	}
}

// A setup script is not blamed while the environment names a proxy.
func TestProxyScriptOnlyNotWithEnvProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.lab.example:8080")
	if proxyScriptOnly() {
		t.Fatal("the environment's proxy was the one used")
	}
}
