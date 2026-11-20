package main

// The proxy of 最新版の確認. Go reads only the environment (HTTPS_PROXY);
// a company PC has its proxy in the Windows settings instead, and the check
// failed there. So when the environment names none, the proxy set by hand
// in Windows (設定 → ネットワークとインターネット → プロキシ → 手動) is
// used. A setup script (PAC) and automatic detection are not read: the
// error then says so. On a PC whose policy makes the proxy settings
// per-machine (ProxySettingsPerUser = 0), Windows itself reads the
// machine's settings instead of the user's, and so does this.

import (
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	inetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	inetPolicyKey   = `Software\Policies\Microsoft\Windows\CurrentVersion\Internet Settings`
)

// proxyVals is one registry key's proxy: the server set by hand ("" when
// it is off), its exceptions, and whether a setup script is set.
type proxyVals struct {
	server, bypass string
	script         bool
}

// readProxyVals reads a key of Internet Settings (read only).
func readProxyVals(root registry.Key, key string) (v proxyVals) {
	k, err := registry.OpenKey(root, key, registry.QUERY_VALUE)
	if err != nil {
		return v
	}
	defer k.Close()
	if pac, _, err := k.GetStringValue("AutoConfigURL"); err == nil && strings.TrimSpace(pac) != "" {
		v.script = true
	}
	if on, _, err := k.GetIntegerValue("ProxyEnable"); err != nil || on == 0 {
		return v
	}
	server, _, _ := k.GetStringValue("ProxyServer")
	v.server = strings.TrimSpace(server)
	v.bypass, _, _ = k.GetStringValue("ProxyOverride")
	return v
}

// proxyPerMachine reports the policy that makes the proxy settings the
// machine's (ProxySettingsPerUser = 0): the user's are then not used.
func proxyPerMachine() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, inetPolicyKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("ProxySettingsPerUser")
	return err == nil && v == 0
}

// pickProxyVals is the setting that counts: the user's, or with the
// per-machine policy the machine's (its own key, else the policy's).
func pickProxyVals(perMachine bool, user, machine, policy proxyVals) proxyVals {
	if !perMachine {
		return user
	}
	if machine.server != "" || machine.script {
		return machine
	}
	return policy
}

// winProxySetting is the proxy set by hand in Windows (server "" when it
// is off) and its exceptions, and whether a setup script is set.
func winProxySetting() (server, bypass string, script bool) {
	var v proxyVals
	if proxyPerMachine() {
		v = pickProxyVals(true, proxyVals{}, readProxyVals(registry.LOCAL_MACHINE, inetSettingsKey), readProxyVals(registry.LOCAL_MACHINE, inetPolicyKey))
	} else {
		v = readProxyVals(registry.CURRENT_USER, inetSettingsKey)
	}
	return v.server, v.bypass, v.script
}

// winProxyFor is the proxy u goes through with Windows' setting: server is
// "host:port" (every protocol) or "http=host:port;https=host:port" (one per
// protocol), bypass the hosts that go direct ("*.example.com;<local>").
// nil: direct.
func winProxyFor(server, bypass string, u *url.URL) *url.URL {
	if server == "" || u == nil {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	for _, p := range strings.FieldsFunc(bypass, func(r rune) bool { return r == ';' || r == ' ' }) {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "<local>" {
			if !strings.Contains(host, ".") {
				return nil
			}
			continue
		}
		if ok, _ := path.Match(p, host); ok {
			return nil
		}
	}
	pick := ""
	if strings.Contains(server, "=") {
		for _, e := range strings.FieldsFunc(server, func(r rune) bool { return r == ';' || r == ' ' }) {
			k, v, _ := strings.Cut(e, "=")
			if strings.EqualFold(strings.TrimSpace(k), u.Scheme) {
				pick = strings.TrimSpace(v)
			}
		}
	} else {
		pick = server
	}
	if pick == "" {
		return nil
	}
	if !strings.Contains(pick, "://") {
		pick = "http://" + pick
	}
	pu, err := url.Parse(pick)
	if err != nil || pu.Host == "" || (pu.Scheme != "http" && pu.Scheme != "https") {
		return nil
	}
	return pu
}

// updateProxy is the proxy of a request to GitHub: the environment's when
// it names one, else Windows' own.
func updateProxy(req *http.Request) (*url.URL, error) {
	if u, err := http.ProxyFromEnvironment(req); u != nil || err != nil {
		return u, err
	}
	server, bypass, _ := winProxySetting()
	return winProxyFor(server, bypass, req.URL), nil
}

// proxyScriptOnly reports a PC whose only proxy setting is a setup script:
// PalaTerm goes direct there, which such a network usually refuses. (Not
// so when the environment names a proxy: that one was used.)
func proxyScriptOnly() bool {
	if os.Getenv("HTTPS_PROXY") != "" || os.Getenv("https_proxy") != "" {
		return false
	}
	server, _, script := winProxySetting()
	return script && server == ""
}
