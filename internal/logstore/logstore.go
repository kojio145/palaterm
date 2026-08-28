// Package logstore turns a device transcript into a log file on disk, using a
// configurable name template.
package logstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fields are the per-device values available to the name template.
type Fields struct {
	Host  string // device name
	IP    string // host/IP
	OS    string // OS type key
	Group string // group (company)
	Site  string // site / location
}

// Placeholders supported in the name template:
//
//	{host}   device name
//	{ip}     device host/IP
//	{os}     OS type key
//	{group}  group (company)
//	{site}   site / location
//	{date}   yyyymmdd
//	{time}   hhmmss
//	{hhmm}   hhmm
func expandName(tmpl string, f Fields, now time.Time) string {
	r := strings.NewReplacer(
		"{host}", sanitize(f.Host),
		"{ip}", sanitize(f.IP),
		"{os}", sanitize(f.OS),
		"{group}", sanitize(f.Group),
		"{site}", sanitize(f.Site),
		"{date}", now.Format("20060102"),
		"{time}", now.Format("150405"),
		"{hhmm}", now.Format("1504"),
	)
	name := r.Replace(tmpl)
	if name == "" {
		name = sanitize(f.Host) + ".txt"
	}
	return name
}

// ResolveRoot makes a relative log root absolute against the executable's
// folder (the tool is portable, so "logs" means "<exe dir>\logs" — never the
// process working directory, which depends on how the exe was launched).
func ResolveRoot(root string) string {
	if root == "" {
		root = "logs"
	}
	if filepath.IsAbs(root) {
		return root
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), root)
	}
	return root
}

// Write saves transcript under dir using the template, returning the full path.
// The run's timestamp is passed in so all logs in one batch share a folder time.
func Write(dir, tmpl string, f Fields, transcript string, now time.Time) (string, error) {
	dir = ResolveRoot(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := expandName(tmpl, f, now)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// RunDir builds the per-run output folder: <root>/log_<yyyymmdd_hhmmss>.
func RunDir(root string, now time.Time) string {
	return filepath.Join(ResolveRoot(root), fmt.Sprintf("log_%s", now.Format("20060102_150405")))
}

func sanitize(s string) string {
	repl := func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ':
			return '_'
		}
		return r
	}
	return strings.Map(repl, s)
}
