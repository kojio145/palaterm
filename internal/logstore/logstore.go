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
	Role  string // role (core / edge / FW …)
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
	// Schedule is a 予約's word: its folder carries it where the stage goes,
	// and so do its log files when it has no stage of its own.
	Schedule string `json:"schedule"`
	// None is the word a run with no stage (作業タイミング: 指定なし) puts in
	// its log file names. Its folder carries no stage word at all.
	None string `json:"none"`
}

// DefaultStageTokens is the naming a new vault starts with.
var DefaultStageTokens = StageTokens{Before: "before", Work: StageWork, After: "after", Schedule: "reserve", None: "Current"}

// StageSchedule is the stage a 予約's folder (and its stage-less log files)
// is named for; it expands to StageTokens.Schedule.
const StageSchedule = "schedule"

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
	if strings.TrimSpace(t.Schedule) == "" {
		t.Schedule = DefaultStageTokens.Schedule
	}
	if strings.TrimSpace(t.None) == "" {
		t.None = DefaultStageTokens.None
	}
	return StageTokens{Before: strings.TrimSpace(t.Before), Work: strings.TrimSpace(t.Work), After: strings.TrimSpace(t.After),
		Schedule: strings.TrimSpace(t.Schedule), None: strings.TrimSpace(t.None)}
}

// Token is what {stage} expands to: a plain capture with no stage takes the
// 指定なし word (default "Current"; "Config" before v1.7.2); before, during /
// work and after take the configured word.
func (t StageTokens) Token(stage string) string {
	t = t.Filled()
	switch stage {
	case "":
		return t.None
	case "before":
		return t.Before
	case "during", StageWork:
		return t.Work
	case "after":
		return t.After
	case StageSchedule:
		return t.Schedule
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
	case strings.ToLower(t.Schedule), "reserve", "予約":
		return StageSchedule
	}
	return tok
}

// Validate rejects words that cannot be part of a file name, that are the
// same for two stages, or that would be read as a different stage.
func (t StageTokens) Validate() error {
	t = t.Filled()
	words := []struct{ label, w string }{{"作業前", t.Before}, {"作業中", t.Work}, {"作業後", t.After}, {"予約", t.Schedule}, {"指定なし", t.None}}
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
	defaults := map[string]string{"before": "作業前", "work": "作業中", "during": "作業中", "after": "作業後", "reserve": "予約", "schedule": "予約", "config": "指定なし", "current": "指定なし"}
	for _, x := range words {
		if lbl, ok := defaults[strings.ToLower(x.w)]; ok && lbl != x.label {
			return fmt.Errorf("%sの付与文字列 %q は「%s」の既定語なので使えません", x.label, x.w, lbl)
		}
	}
	return nil
}

// DefaultTemplate is the log file name a new vault starts with; vaults still
// on one of the earlier defaults (OldDefaultTemplates) are moved to it on
// unlock, so a user who never touched the setting follows the current form
// while a template they edited stays theirs.
const DefaultTemplate = "{host}_{site}_{role}_{stage}_{date}_{time}.txt"

// OldDefaultTemplates are the defaults of earlier releases, oldest first:
// the original (host and a fixed "Config"), then the v1.5 one that added
// site and stage. Role joined in v1.5.8.
var OldDefaultTemplates = []string{
	"{host}_Config_{date}_{time}.txt",
	"{host}_{site}_{stage}_{date}_{time}.txt",
}

// IsOldDefaultTemplate reports whether tmpl is a default from an earlier
// release (and so safe to move to DefaultTemplate).
func IsOldDefaultTemplate(tmpl string) bool {
	for _, t := range OldDefaultTemplates {
		if tmpl == t {
			return true
		}
	}
	return false
}

// DefaultDirTemplate names the per-run folder:
// <date>_<time>[_<group>][_<stage>]. Placeholders: {date} {time} {hhmm}
// {group} {stage}; an empty group or stage is dropped with one neighbouring
// separator. The group joined (and the "log_" prefix left) in v1.5.12, at
// the user's request: folders sort by time and say whose run it was.
const DefaultDirTemplate = "{date}_{time}_{group}_{stage}"

// OldDefaultDirTemplates are the folder-name defaults of earlier releases.
// A vault still on one moves to DefaultDirTemplate on unlock.
var OldDefaultDirTemplates = []string{"log_{date}_{time}_{stage}"}

// IsOldDefaultDirTemplate reports whether tmpl is an earlier release's
// folder-name default (and so safe to move to DefaultDirTemplate).
func IsOldDefaultDirTemplate(tmpl string) bool {
	for _, t := range OldDefaultDirTemplates {
		if tmpl == t {
			return true
		}
	}
	return false
}

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
//	{role}   role
//	{stage}  run stage (before / during / after)
//	{date}   yyyymmdd
//	{time}   hhmmss
//	{hhmm}   hhmm
func expandName(tmpl string, f Fields, now time.Time) string {
	if f.Stage != "" && !strings.Contains(tmpl, "{stage}") {
		ext := filepath.Ext(tmpl)
		tmpl = strings.TrimSuffix(tmpl, ext) + "_{stage}" + ext
	}
	vals := map[string]string{
		"{host}":  sanitize(f.Host),
		"{ip}":    sanitize(f.IP),
		"{os}":    sanitize(f.OS),
		"{group}": sanitize(f.Group),
		"{site}":  sanitize(f.Site),
		"{role}":  sanitize(f.Role),
		"{stage}": sanitize(f.Tokens.Token(f.Stage)),
		"{date}":  now.Format("20060102"),
		"{time}":  now.Format("150405"),
		"{hhmm}":  now.Format("1504"),
	}
	name := expand(dropEmpty(tmpl, vals), vals)
	ext := filepath.Ext(name)
	base := strings.Trim(strings.TrimSuffix(name, ext), "_")
	name = base + ext
	if base == "" {
		name = sanitize(f.Host) + ".txt"
	}
	return name
}

// separators are the characters a template uses between placeholders; one of
// them goes away together with an empty placeholder.
const separators = "_-. "

// dropEmpty removes from tmpl every placeholder whose value is empty (a
// device with no site or role, a run with no stage), together with ONE
// adjacent separator — the one before it, or the one after it when the
// placeholder opens the name — so "{host}_{site}_{stage}_{date}" for a device
// without a site reads "r1_before_20261009", not "r1__before_20261009", and
// "{site}-{host}" reads "r1". Placeholders with values, and everything the
// user typed between them, are left exactly as written. Optional labels are
// never required for this: an empty one simply drops out of the name.
func dropEmpty(tmpl string, vals map[string]string) string {
	for ph, v := range vals {
		if v != "" {
			continue
		}
		for {
			i := strings.Index(tmpl, ph)
			if i < 0 {
				break
			}
			j := i + len(ph)
			switch {
			case i > 0 && strings.ContainsRune(separators, rune(tmpl[i-1])):
				tmpl = tmpl[:i-1] + tmpl[j:]
			case j < len(tmpl) && strings.ContainsRune(separators, rune(tmpl[j])):
				tmpl = tmpl[:i] + tmpl[j+1:]
			default:
				tmpl = tmpl[:i] + tmpl[j:]
			}
		}
	}
	return tmpl
}

// expand substitutes every placeholder in vals.
func expand(tmpl string, vals map[string]string) string {
	pairs := make([]string, 0, 2*len(vals))
	for ph, v := range vals {
		pairs = append(pairs, ph, v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// CleanRoot is the log folder as typed, without the spaces and quotation
// marks around it: Explorer's 「パスのコピー」 gives "D:\logs" in quotes,
// and pasted as it is that names no folder Windows can create.
func CleanRoot(root string) string {
	for {
		s := strings.TrimSpace(root)
		s = strings.TrimPrefix(strings.TrimSuffix(s, `"`), `"`)
		s = strings.TrimPrefix(strings.TrimSuffix(s, `'`), `'`)
		if s == root {
			return s
		}
		root = s
	}
}

// CheckRoot makes sure logs can be written under root: the folder exists or
// can be created, and a file can be made in it. The settings refuse a log
// folder that fails here, rather than every later run failing on it.
func CheckRoot(root string) error {
	dir := ResolveRoot(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ログの保存先フォルダ「%s」を作れません: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".palaterm-check-*")
	if err != nil {
		return fmt.Errorf("ログの保存先フォルダ「%s」に書き込めません: %w", dir, err)
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return nil
}

// ResolveRoot makes a relative log root absolute against the executable's
// folder (the tool is portable, so "logs" means "<exe dir>\logs" — never the
// process working directory, which depends on how the exe was launched).
func ResolveRoot(root string) string {
	root = CleanRoot(root)
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
// An existing file is never overwritten: a 予約 puts every round into one
// folder, and a template without {time} names the same file each round, so
// the later one gets "_2", "_3", … before the extension.
func Write(dir, tmpl string, f Fields, transcript string, now time.Time) (string, error) {
	path, fh, err := create(dir, tmpl, f, now)
	if err != nil {
		return "", err
	}
	_, err = fh.WriteString(transcript)
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

// Claim creates the empty file Write would have written and returns its
// path: a batch device's log is named when its connection starts and then
// filled as the output arrives (see Stream), so the name — "_2" and all —
// has to be taken before the first byte. The caller removes the file if
// nothing ever goes into it.
func Claim(dir, tmpl string, f Fields, now time.Time) (string, error) {
	path, fh, err := create(dir, tmpl, f, now)
	if err != nil {
		return "", err
	}
	if err := fh.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// create makes a new file under the template's name, never an existing one.
func create(dir, tmpl string, f Fields, now time.Time) (string, *os.File, error) {
	path := Path(dir, tmpl, f, now)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", nil, err
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return path, fh, nil
		}
		if !os.IsExist(err) || i > 9999 {
			return "", nil, err
		}
		path = fmt.Sprintf("%s_%d%s", base, i, ext)
	}
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
	base := runDirBase(root, tmpl, group, stage, tok, now)
	dir := base
	// Only a folder that is there moves on to the next number. Any other
	// answer (a name Windows refuses, a share that is gone) ends the search:
	// waiting for "not exist" from a path that can never exist looped
	// forever, and the batch hung before its first device with no way to
	// stop it. Whoever creates the folder then reports the real error.
	for i := 2; i < 10000; i++ {
		if _, err := os.Stat(dir); err != nil {
			return dir
		}
		dir = fmt.Sprintf("%s_%d", base, i)
	}
	return dir
}

// runDirBase is the run folder's path before any "_2" is added.
func runDirBase(root, tmpl, group, stage string, tok StageTokens, now time.Time) string {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = DefaultDirTemplate
	}
	stageTok := ""
	if stage != "" {
		stageTok = tok.Token(stage)
	}
	vals := map[string]string{
		"{group}": sanitize(group),
		"{stage}": sanitize(stageTok),
		"{date}":  now.Format("20060102"),
		"{time}":  now.Format("150405"),
		"{hhmm}":  now.Format("1504"),
	}
	// An empty {group} / {stage} drops out with one neighbouring separator
	// (see dropEmpty), so "{date}_{time}_{group}_{stage}" with neither is
	// "20261009_123456".
	name := strings.Trim(expand(dropEmpty(tmpl, vals), vals), "_. ")
	if name == "" {
		name = now.Format("20060102_150405")
	}
	return filepath.Join(ResolveRoot(root), name)
}

// ClaimRunDir is RunDir that also creates the folder, atomically: two
// terminal windows to devices of one group opened in the same second both
// found the name free and shared the folder, the second summary replacing
// the first in 実行履歴. os.Mkdir fails for whichever comes second, which
// then takes the next number. Falls back to RunDir's answer if the folder
// cannot be created at all (the log write reports that error).
func ClaimRunDir(root, tmpl, group, stage string, tok StageTokens, now time.Time) string {
	base := runDirBase(root, tmpl, group, stage, tok, now)
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		return base
	}
	dir := base
	for i := 2; i < 1000; i++ {
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return dir
		}
		if !os.IsExist(err) {
			return dir
		}
		dir = fmt.Sprintf("%s_%d", base, i)
	}
	return dir
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
