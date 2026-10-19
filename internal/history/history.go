// Package history records what a batch did (a small JSON next to its logs)
// and reads those records back as the 実行履歴 list, so past runs can be
// browsed from the app instead of by rummaging through log_ folders, and two
// runs — a before and an after — can be laid side by side.
//
// Nothing here touches the App lock or the Wails runtime; the functions are
// pure file I/O so they can be tested on a temp folder.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kojio145/palaterm/internal/logstore"
)

// SummaryName is the record written into each run folder.
const SummaryName = "palaterm-run.json"

// Format identifies the summary layout; bump when the layout changes.
const Format = "palaterm-run/1"

// DeviceSummary is one device's outcome in a run.
type DeviceSummary struct {
	Name       string  `json:"name"`
	Host       string  `json:"host"`
	Site       string  `json:"site,omitempty"`
	CommandSet string  `json:"commandSet,omitempty"`
	Success    bool    `json:"success"`
	Canceled   bool    `json:"canceled,omitempty"`
	Error      string  `json:"error,omitempty"`
	LogFile    string  `json:"logFile,omitempty"` // file name inside the run folder
	ElapsedSec float64 `json:"elapsedSec"`
	// Warnings: commands the device refused (see runner.DeviceResult).
	Warnings []string `json:"warnings,omitempty"`
}

// Summary is palaterm-run.json: what one batch did.
type Summary struct {
	Format     string `json:"format"`
	App        string `json:"app"`
	StartedAt  string `json:"startedAt"`  // RFC3339
	FinishedAt string `json:"finishedAt"` // RFC3339
	Group      string `json:"group,omitempty"`
	Stage      string `json:"stage,omitempty"` // before / during / after
	DryRun     bool   `json:"dryRun,omitempty"`
	// Interactive marks a 対話接続 session (one device, operated by hand),
	// recorded in its own run folder like a batch so it shows in the history.
	Interactive bool `json:"interactive,omitempty"`
	// Schedule names the 予約 that started the batch ("" = started by hand).
	Schedule string `json:"schedule,omitempty"`
	// Rounds is how many runs of a 予約 the folder holds (each round adds
	// its logs to the same folder); 0 = a single run. Devices is then each
	// device's latest round, and FailedRounds counts the rounds in which a
	// device failed.
	Rounds       int             `json:"rounds,omitempty"`
	FailedRounds int             `json:"failedRounds,omitempty"`
	Devices      []DeviceSummary `json:"devices"`
}

// Read returns dir's summary (false when it has none, or an unreadable one).
func Read(dir string) (Summary, bool) {
	var s Summary
	data, err := os.ReadFile(filepath.Join(dir, SummaryName))
	if err != nil || json.Unmarshal(data, &s) != nil {
		return Summary{}, false
	}
	return s, true
}

// AddRound folds the round cur into prev, the summary already in a 予約's
// folder: the first start stays, the count goes up, and each device's row
// becomes its latest one (a device not in this round keeps its last).
func AddRound(prev, cur Summary) Summary {
	out := cur
	out.StartedAt = prev.StartedAt
	if out.StartedAt == "" {
		out.StartedAt = cur.StartedAt
	}
	n := prev.Rounds
	if n == 0 {
		n = 1
	}
	out.Rounds = n + 1
	out.FailedRounds = prev.FailedRounds
	if prev.Rounds == 0 && hasFailure(prev.Devices) {
		out.FailedRounds = 1
	}
	if hasFailure(cur.Devices) {
		out.FailedRounds++
	}
	idx := map[string]int{}
	devs := append([]DeviceSummary{}, prev.Devices...)
	for i, d := range devs {
		idx[d.Name] = i
	}
	for _, d := range cur.Devices {
		if i, ok := idx[d.Name]; ok {
			devs[i] = d
		} else {
			idx[d.Name] = len(devs)
			devs = append(devs, d)
		}
	}
	out.Devices = devs
	return out
}

func hasFailure(devs []DeviceSummary) bool {
	for _, d := range devs {
		if !d.Success {
			return true
		}
	}
	return false
}

// Run is one row of the 実行履歴 list: a run folder plus its summary (or, for
// a folder from before summaries existed, what can be told from its files).
type Run struct {
	Dir         string `json:"dir"`  // absolute folder path
	Name        string `json:"name"` // folder name (log_…)
	StartedAt   string `json:"startedAt"`
	FinishedAt  string `json:"finishedAt,omitempty"`
	Group       string `json:"group,omitempty"`
	Stage       string `json:"stage,omitempty"`
	DryRun      bool   `json:"dryRun,omitempty"`
	Interactive bool   `json:"interactive,omitempty"`
	Schedule    string `json:"schedule,omitempty"`
	// Rounds / FailedRounds: see Summary (a 予約's folder).
	Rounds       int      `json:"rounds,omitempty"`
	FailedRounds int      `json:"failedRounds,omitempty"`
	HasSummary   bool     `json:"hasSummary"`
	// External: a folder under the log root that PalaTerm did not write
	// (logs brought in from another job or another tool, dropped into
	// logs\ by hand). Listed so it can be compared like a run: its devices
	// are guessed from the file names, its stage from the folder name.
	External bool     `json:"external,omitempty"`
	OK       int      `json:"ok"`
	Failed   int      `json:"failed"`
	Canceled int      `json:"canceled"`
	Devices  []Device `json:"devices"`
}

// Device is one device row inside a Run.
type Device struct {
	Name    string `json:"name"`
	Host    string `json:"host,omitempty"`
	Site    string `json:"site,omitempty"`
	Status  string `json:"status"` // ok / error / canceled / unknown
	Error   string `json:"error,omitempty"`
	LogPath string `json:"logPath,omitempty"` // absolute, "" when the file is gone
	// Warnings: commands the device refused during that run.
	Warnings []string `json:"warnings,omitempty"`
}

// Write stores s as dir/palaterm-run.json.
func Write(dir string, s Summary) error {
	s.Format = Format
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, SummaryName), append(data, '\r', '\n'), 0o644)
}

// List returns the runs under root (an absolute log folder), newest first.
// A run is any sub-folder named log_<yyyymmdd_hhmmss>[_<stage>].
//
// tok are the stage words in use (see logstore.StageTokens): a folder
// without a summary is sorted into its stage by the word in its name.
func List(root string, tok logstore.StageTokens) ([]Run, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var runs []Run
	seen := map[string]bool{}
	// one lists dir as a run: from its summary or its app-style name, or —
	// for any other folder holding log files — as an external folder named
	// label (the folder name, or parent/child one level down).
	one := func(dir, label string, e os.DirEntry, external bool) {
		seen[dir] = true
		var dirMod time.Time
		if info, err := e.Info(); err == nil {
			dirMod = info.ModTime()
		}
		key := cacheKey{dirMod: dirMod, tok: tok}
		if sum, err := os.Stat(filepath.Join(dir, SummaryName)); err == nil {
			key.sumMod, key.sumSize = sum.ModTime(), sum.Size()
		}
		if r, ok := cached(dir, key); ok {
			runs = append(runs, r)
			return
		}
		var r Run
		if external {
			r = readExternal(dir, label, tok)
		} else {
			r = readRun(dir, tok)
		}
		if r.StartedAt == "" && !dirMod.IsZero() {
			r.StartedAt = dirMod.Format(time.RFC3339)
		}
		store(dir, key, r)
		runs = append(runs, r)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		// A run folder is one that carries a summary — whatever the folder-
		// name template made of its name — or, for a folder without one (a
		// run from before summaries, an interactive window that crashed),
		// one whose name starts the way the app's templates do: the old
		// "log_" prefix, or the date and time the current default opens with.
		_, serr := os.Stat(filepath.Join(dir, SummaryName))
		_, _, appName := parseDirName(e.Name())
		if serr == nil || appName {
			one(dir, e.Name(), e, false)
			continue
		}
		// Anything else under the log root is a folder the user put there:
		// the logs of an earlier job, taken by hand or by another tool. With
		// log files in it, it is listed as is; a job folder holding the
		// before / during / after folders is listed one level down
		// ("job\before"), so a whole job can be dropped in and compared.
		if hasLogFiles(dir) {
			one(dir, e.Name(), e, true)
			continue
		}
		subs, _ := os.ReadDir(dir)
		for _, s := range subs {
			if s.IsDir() && hasLogFiles(filepath.Join(dir, s.Name())) {
				one(filepath.Join(dir, s.Name()), e.Name()+string(filepath.Separator)+s.Name(), s, true)
			}
		}
	}
	forget(root, seen)
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt != runs[j].StartedAt {
			return runs[i].StartedAt > runs[j].StartedAt
		}
		return runs[i].Name > runs[j].Name
	})
	return runs, nil
}

// The 実行履歴 tab re-reads the log folder every time it opens, and a log
// folder of thousands of runs (a 予約 every few seconds, before v1.6.6 one
// folder per round) took a second or more. A folder's row is kept while
// the folder and its summary are unchanged: a file added to or removed from
// it changes the folder's time, a rewritten summary its own time or size.
type cacheKey struct {
	dirMod, sumMod time.Time
	sumSize        int64
	tok            logstore.StageTokens
}

type cacheEnt struct {
	key cacheKey
	run Run
}

var (
	cacheMu  sync.Mutex
	runCache = map[string]cacheEnt{}
)

func cached(dir string, k cacheKey) (Run, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := runCache[dir]
	if !ok || e.key != k {
		return Run{}, false
	}
	return copyRun(e.run), true
}

func store(dir string, k cacheKey, r Run) {
	cacheMu.Lock()
	runCache[dir] = cacheEnt{key: k, run: copyRun(r)}
	cacheMu.Unlock()
}

// copyRun copies the slices a caller could change.
func copyRun(r Run) Run {
	r.Devices = append([]Device{}, r.Devices...)
	return r
}

// forget drops the cached rows of root's folders that are gone (seen holds
// the folders this listing found, one or two levels under root).
func forget(root string, seen map[string]bool) {
	root = filepath.Clean(root)
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for dir := range runCache {
		under := filepath.Dir(dir) == root || filepath.Dir(filepath.Dir(dir)) == root
		if under && !seen[dir] {
			delete(runCache, dir)
		}
	}
}

// hasLogFiles reports whether dir holds at least one log-looking file.
func hasLogFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && isLogFile(e.Name()) {
			return true
		}
	}
	return false
}

// isLogFile: the file kinds a captured console log comes as. Screenshots,
// spreadsheets and the like in a job folder are not devices.
func isLogFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".log", ".text", ".out", ".cap", ".cfg", ".conf", ".config", "":
		return !strings.HasPrefix(name, ".") && name != SummaryName
	}
	return false
}

// readExternal builds a Run from a folder PalaTerm did not write: one device
// per log file, named by the file name with the stage words, dates and
// times taken out (sw01_before_20260901.txt → sw01), so the same device's
// before and after files pair up; the stage from the folder's name; the
// time from the newest file.
func readExternal(dir, label string, tok logstore.StageTokens) Run {
	r := Run{Dir: dir, Name: label, External: true, Devices: []Device{}, Stage: stageFromName(label, tok)}
	entries, _ := os.ReadDir(dir)
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() || !isLogFile(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		r.Devices = append(r.Devices, Device{Name: externalDeviceKey(e.Name(), tok), Status: "unknown", LogPath: filepath.Join(dir, e.Name())})
	}
	if !newest.IsZero() {
		r.StartedAt = newest.Format(time.RFC3339)
	}
	return r
}

// stageWords are the words a folder or file name carries for a stage, in
// any tool's habit, plus the user's own (tok).
func stageWords(tok logstore.StageTokens) map[string]string {
	tok = tok.Filled()
	w := map[string]string{
		"before": "before", "pre": "before", "作業前": "before", "事前": "before",
		"after": "after", "post": "after", "作業後": "after", "事後": "after",
		"during": "during", "work": "during", "作業中": "during",
	}
	w[strings.ToLower(tok.Before)] = "before"
	w[strings.ToLower(tok.After)] = "after"
	w[strings.ToLower(tok.Work)] = "during"
	return w
}

// stageFromName reads the stage out of a folder name ("案件X\作業前",
// "sw_before_2026", "post-change"): "" when none or more than one is named.
func stageFromName(name string, tok logstore.StageTokens) string {
	words := stageWords(tok)
	found := ""
	for _, f := range nameFields(name) {
		st, ok := words[strings.ToLower(f)]
		if !ok {
			// Japanese stage words are not separated from the rest: "作業前ログ".
			for w, s := range words {
				if isCJK(w) && strings.Contains(f, w) {
					st, ok = s, true
					break
				}
			}
		}
		if !ok {
			continue
		}
		if found != "" && found != st {
			return "" // "before_after_diff": not a stage folder
		}
		found = st
	}
	return found
}

// externalDeviceKey is the device a log file belongs to: the name without
// its extension, less the fields that are stage words or dates / times.
func externalDeviceKey(file string, tok logstore.StageTokens) string {
	base := strings.TrimSuffix(file, filepath.Ext(file))
	words := stageWords(tok)
	var keep []string
	for _, f := range nameFields(base) {
		lf := strings.ToLower(f)
		if _, ok := words[lf]; ok || isDateOrTime(f) {
			continue
		}
		keep = append(keep, f)
	}
	if len(keep) == 0 {
		return base
	}
	return strings.Join(keep, "_")
}

// nameFields splits a file or folder name on the separators tools use.
func nameFields(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == ' ' || r == '.' || r == '\\' || r == '/' || r == '(' || r == ')' || r == '[' || r == ']' || r == '　'
	})
}

// isDateOrTime: a field that is only digits (with h / m / s / colons at
// most): 20260901, 143000, 1430, the "09" and "01" of an already split
// 2026-09-01, 14h30. Device names carry letters, so a bare number is never
// one. (A file named by digits alone keeps its name: see externalDeviceKey.)
func isDateOrTime(f string) bool {
	digits := 0
	for _, r := range f {
		if r >= '0' && r <= '9' {
			digits++
		} else if r != 'h' && r != 'm' && r != 's' && r != ':' && r != 'T' {
			return false
		}
	}
	return digits >= 1 && digits <= 14 && digits >= len(f)-3
}

func isCJK(s string) bool {
	for _, r := range s {
		if r > 0x2E7F {
			return true
		}
	}
	return false
}

// readRun builds a Run from one folder: the summary when present, otherwise
// the folder's log files with their status unknown.
func readRun(dir string, tok logstore.StageTokens) Run {
	r := Run{Dir: dir, Name: filepath.Base(dir), Devices: []Device{}}
	// Folder name gives the start time and stage even without a summary.
	if ts, stage, ok := parseDirName(r.Name); ok {
		r.StartedAt = ts.Format(time.RFC3339)
		r.Stage = tok.Canonical(stage)
	}
	if data, err := os.ReadFile(filepath.Join(dir, SummaryName)); err == nil {
		var s Summary
		if json.Unmarshal(data, &s) == nil {
			r.HasSummary = true
			if s.StartedAt != "" {
				r.StartedAt = s.StartedAt
			}
			r.FinishedAt = s.FinishedAt
			r.Group, r.Stage, r.DryRun, r.Interactive, r.Schedule = s.Group, s.Stage, s.DryRun, s.Interactive, s.Schedule
			r.Rounds, r.FailedRounds = s.Rounds, s.FailedRounds
			for _, d := range s.Devices {
				dv := Device{Name: d.Name, Host: d.Host, Site: d.Site, Error: d.Error, Warnings: d.Warnings}
				switch {
				case d.Success:
					dv.Status = "ok"
					r.OK++
				case d.Canceled:
					dv.Status = "canceled"
					r.Canceled++
				default:
					dv.Status = "error"
					r.Failed++
				}
				if d.LogFile != "" {
					p := filepath.Join(dir, d.LogFile)
					if _, err := os.Stat(p); err == nil {
						dv.LogPath = p
					}
				}
				r.Devices = append(r.Devices, dv)
			}
			return r
		}
	}
	// No summary (a run from before this feature): list the log files. The
	// device name is the file name up to the first "_" — the default template
	// starts with {host}_ — which is a guess, so the status stays unknown.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || e.Name() == SummaryName {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if i := strings.Index(name, "_"); i > 0 {
			name = name[:i]
		}
		r.Devices = append(r.Devices, Device{Name: name, Status: "unknown", LogPath: filepath.Join(dir, e.Name())})
	}
	return r
}

// parseDirName reads [log_]<yyyymmdd_hhmmss>[_<group>][_<stage>]: the old
// default (log_ prefix, stage only) and the current one (no prefix, group
// then stage). The stage is the last "_" field after the time — a group
// name may itself contain "_", but the stage word cannot (see
// StageTokens.Validate), so the last field is the only safe reading.
func parseDirName(name string) (time.Time, string, bool) {
	rest := strings.TrimPrefix(name, "log_")
	if len(rest) < 15 {
		return time.Time{}, "", false
	}
	ts, err := time.ParseInLocation("20060102_150405", rest[:15], time.Local)
	if err != nil {
		return time.Time{}, "", false
	}
	tail := strings.TrimPrefix(rest[15:], "_")
	stage := tail
	if i := strings.LastIndex(tail, "_"); i >= 0 {
		stage = tail[i+1:]
	}
	return ts, stage, true
}
