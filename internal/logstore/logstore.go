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
	// Stage is the maintenance-window stage of the run ("before" / "during" /
	// "after", or empty). It fills {stage}; a template without that
	// placeholder gets "_<stage>" appended before the extension, so a stage
	// chosen in the run tab always shows up in the file name.
	Stage string
}

// DefaultTemplate is the log file name a new vault starts with; vaults still
// on OldDefaultTemplate are moved to it on unlock.
const (
	DefaultTemplate    = "{host}_{site}_{stage}_{date}_{time}.txt"
	OldDefaultTemplate = "{host}_Config_{date}_{time}.txt"
)

// DefaultDirTemplate names the per-run folder: log_<date>_<time>[_<stage>].
// Placeholders: {date} {time} {hhmm} {group} {stage}; a run with no stage
// leaves {stage} empty and the surrounding "_" is dropped.
const DefaultDirTemplate = "log_{date}_{time}_{stage}"

// StageWork is the stage recorded for an interactive (single-device) session:
// it is work being done by hand, so its logs read the same as a batch run
// taken 作業中.
const StageWork = "work"

// StageToken is what {stage} expands to: a plain capture with no stage is a
// "Config" log (the historical default name), work in progress — a batch
// marked 作業中 or an interactive session — is "work", and before / after
// stay as they are.
func StageToken(stage string) string {
	switch stage {
	case "":
		return "Config"
	case "during", StageWork:
		return StageWork
	}
	return stage
}

// Placeholders supported in the name template:
//
//	{host}   device name
//	{ip}     device host/IP
//	{os}     OS type key
//	{group}  group (company)
//	{site}   site / location
//	{stage}  run stage (before / during / after)
//	{date}   yyyymmdd
//	{time}   hhmmss
//	{hhmm}   hhmm
func expandName(tmpl string, f Fields, now time.Time) string {
	if f.Stage != "" && !strings.Contains(tmpl, "{stage}") {
		ext := filepath.Ext(tmpl)
		tmpl = strings.TrimSuffix(tmpl, ext) + "_{stage}" + ext
	}
	r := strings.NewReplacer(
		"{host}", sanitize(f.Host),
		"{ip}", sanitize(f.IP),
		"{os}", sanitize(f.OS),
		"{group}", sanitize(f.Group),
		"{site}", sanitize(f.Site),
		"{stage}", sanitize(StageToken(f.Stage)),
		"{date}", now.Format("20060102"),
		"{time}", now.Format("150405"),
		"{hhmm}", now.Format("1504"),
	)
	name := r.Replace(tmpl)
	// An empty placeholder (a device with no site) must not leave "__" or a
	// leading/trailing "_" behind.
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	ext := filepath.Ext(name)
	base := strings.Trim(strings.TrimSuffix(name, ext), "_")
	name = base + ext
	if base == "" {
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
	path := Path(dir, tmpl, f, now)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Path returns the file Write would create, without writing it — for a
// transcript that is rewritten in place while it grows (the interactive
// window's autosave) the name must be fixed once, at connect time.
func Path(dir, tmpl string, f Fields, now time.Time) string {
	return filepath.Join(ResolveRoot(dir), expandName(tmpl, f, now))
}

// RunDir builds the per-run output folder from tmpl (DefaultDirTemplate when
// empty) and makes it unique: a template without {time} can name the same
// folder twice in a day, and the second run then gets "_2", "_3", … rather
// than mixing its logs and overwriting the first run's summary.
func RunDir(root, tmpl, group, stage string, now time.Time) string {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = DefaultDirTemplate
	}
	stageTok := ""
	if stage != "" {
		stageTok = StageToken(stage)
	}
	r := strings.NewReplacer(
		"{group}", sanitize(group),
		"{stage}", sanitize(stageTok),
		"{date}", now.Format("20060102"),
		"{time}", now.Format("150405"),
		"{hhmm}", now.Format("1504"),
	)
	name := r.Replace(tmpl)
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	name = strings.Trim(name, "_. ")
	if name == "" {
		name = fmt.Sprintf("log_%s", now.Format("20060102_150405"))
	}
	base := filepath.Join(ResolveRoot(root), name)
	dir := base
	for i := 2; ; i++ {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return dir
		}
		dir = fmt.Sprintf("%s_%d", base, i)
	}
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
