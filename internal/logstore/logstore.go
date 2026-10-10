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
	// Tokens are the words Stage is written as (see StageTokens); the zero
	// value is the default before / work / after.
	Tokens StageTokens
}

// StageTokens are the words {stage} expands to for each run stage. The user
// can rename them in ログ設定 (pre / mid / post, 事前 / 作業 / 事後, …);
// an empty field falls back to its default, so the zero value names logs
// the way they have always been named.
type StageTokens struct {
	Before string `json:"before"`
	Work   string `json:"work"` // a batch marked 作業中, or an interactive session
	After  string `json:"after"`
}

// DefaultStageTokens is the naming a new vault starts with.
var DefaultStageTokens = StageTokens{Before: "before", Work: StageWork, After: "after"}

// Filled returns t with every empty field replaced by its default.
func (t StageTokens) Filled() StageTokens {
	if strings.TrimSpace(t.Before) == "" {
		t.Before = DefaultStageTokens.Before
	}
	if strings.TrimSpace(t.Work) == "" {
		t.Work = DefaultStageTokens.Work
	}
	if strings.TrimSpace(t.After) == "" {
		t.After = DefaultStageTokens.After
	}
	return StageTokens{Before: strings.TrimSpace(t.Before), Work: strings.TrimSpace(t.Work), After: strings.TrimSpace(t.After)}
}

// Token is what {stage} expands to: a plain capture with no stage is a
// "Config" log (the historical default name); before, during / work and
// after take the configured word.
func (t StageTokens) Token(stage string) string {
	t = t.Filled()
	switch stage {
	case "":
		return "Config"
	case "before":
		return t.Before
	case "during", StageWork:
		return t.Work
	case "after":
		return t.After
	}
	return stage
}

// Canonical maps the stage word found in a folder name back to the stage it
// stands for ("before" / "work" / "after"). Both the configured words and
// the defaults are recognised, so folders written before a rename still
// sort into the right stage in the history tab. Anything else is returned
// as it is.
func (t StageTokens) Canonical(tok string) string {
	t = t.Filled()
	switch strings.ToLower(tok) {
	case strings.ToLower(t.Before), "before":
		return "before"
	case strings.ToLower(t.Work), StageWork, "during":
		return StageWork
	case strings.ToLower(t.After), "after":
		return "after"
	}
	return tok
}

// Validate rejects words that cannot be part of a file name, that are the
// same for two stages, or that would be read as a different stage.
func (t StageTokens) Validate() error {
	t = t.Filled()
	words := []struct{ label, w string }{{"作業前", t.Before}, {"作業中", t.Work}, {"作業後", t.After}}
	seen := map[string]string{}
	for _, x := range words {
		if len([]rune(x.w)) > 32 {
			return fmt.Errorf("%sの付与文字列が長すぎます（32文字まで）: %q", x.label, x.w)
		}
		if sanitize(x.w) != x.w {
			return fmt.Errorf("%sの付与文字列にファイル名に使えない文字（空白や \\ / : * ? \" < > |）があります: %q", x.label, x.w)
		}
		if strings.ContainsFunc(x.w, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return fmt.Errorf("%sの付与文字列に制御文字が含まれています: %q", x.label, x.w)
		}
		if strings.HasSuffix(x.w, ".") {
			return fmt.Errorf("%sの付与文字列の末尾にドットは使えません: %q", x.label, x.w)
		}
		if reservedName(x.w) {
			return fmt.Errorf("%sの付与文字列 %q は Windows の予約名のため使えません", x.label, x.w)
		}
		k := strings.ToLower(x.w)
		if prev, dup := seen[k]; dup {
			return fmt.Errorf("%sと%sの付与文字列が同じです: %q", prev, x.label, x.w)
		}
		seen[k] = x.label
	}
	// A word that is another stage's default would be read back as that
	// stage ("after" for 作業前 would list the run as 作業後).
	defaults := map[string]string{"before": "作業前", "work": "作業中", "during": "作業中", "after": "作業後", "config": "指定なし"}
	for _, x := range words {
		if lbl, ok := defaults[strings.ToLower(x.w)]; ok && lbl != x.label {
			return fmt.Errorf("%sの付与文字列 %q は「%s」の既定語なので使えません", x.label, x.w, lbl)
		}
	}
	return nil
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
		"{stage}", sanitize(f.Tokens.Token(f.Stage)),
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
func RunDir(root, tmpl, group, stage string, tok StageTokens, now time.Time) string {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = DefaultDirTemplate
	}
	stageTok := ""
	if stage != "" {
		stageTok = tok.Token(stage)
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

// reservedName reports whether w (with or without an extension) is a name
// Windows refuses as a file or folder: CON, PRN, AUX, NUL, COM1–9, LPT1–9.
func reservedName(w string) bool {
	base := strings.ToUpper(w)
	if i := strings.Index(base, "."); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
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
