//go:build !windows

package main

import (
	"net/http"
	"net/url"
)

// updateProxy is the environment's proxy off Windows (see
// update_proxy_windows.go).
func updateProxy(req *http.Request) (*url.URL, error) { return http.ProxyFromEnvironment(req) }

func proxyScriptOnly() bool { return false }
