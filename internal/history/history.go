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
	Interactive bool            `json:"interactive,omitempty"`
	Devices     []DeviceSummary `json:"devices"`
}

// Run is one row of the 実行履歴 list: a run folder plus its summary (or, for
// a folder from before summaries existed, what can be told from its files).
type Run struct {
	Dir         string   `json:"dir"`  // absolute folder path
	Name        string   `json:"name"` // folder name (log_…)
	StartedAt   string   `json:"startedAt"`
	FinishedAt  string   `json:"finishedAt,omitempty"`
	Group       string   `json:"group,omitempty"`
	Stage       string   `json:"stage,omitempty"`
	DryRun      bool     `json:"dryRun,omitempty"`
	Interactive bool     `json:"interactive,omitempty"`
	HasSummary  bool     `json:"hasSummary"`
	OK          int      `json:"ok"`
	Failed      int      `json:"failed"`
	Canceled    int      `json:"canceled"`
	Devices     []Device `json:"devices"`
}

// Device is one device row inside a Run.
type Device struct {
	Name    string `json:"name"`
	Host    string `json:"host,omitempty"`
	Site    string `json:"site,omitempty"`
	Status  string `json:"status"` // ok / error / canceled / unknown
	Error   string `json:"error,omitempty"`
	LogPath string `json:"logPath,omitempty"` // absolute, "" when the file is gone
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
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		// A run folder is one the app named (log_…, the default template) or
		// one that carries a summary (any folder-name template).
		if _, err := os.Stat(filepath.Join(dir, SummaryName)); err != nil && !strings.HasPrefix(e.Name(), "log_") {
			continue
		}
		r := readRun(dir, tok)
		if r.StartedAt == "" {
			if info, err := e.Info(); err == nil {
				r.StartedAt = info.ModTime().Format(time.RFC3339)
			}
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt != runs[j].StartedAt {
			return runs[i].StartedAt > runs[j].StartedAt
		}
		return runs[i].Name > runs[j].Name
	})
	return runs, nil
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
			r.Group, r.Stage, r.DryRun, r.Interactive = s.Group, s.Stage, s.DryRun, s.Interactive
			for _, d := range s.Devices {
				dv := Device{Name: d.Name, Host: d.Host, Site: d.Site, Error: d.Error}
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

// parseDirName reads log_<yyyymmdd_hhmmss>[_<stage>].
func parseDirName(name string) (time.Time, string, bool) {
	rest := strings.TrimPrefix(name, "log_")
	if len(rest) < 15 {
		return time.Time{}, "", false
	}
	ts, err := time.ParseInLocation("20060102_150405", rest[:15], time.Local)
	if err != nil {
		return time.Time{}, "", false
	}
	stage := strings.TrimPrefix(rest[15:], "_")
	return ts, stage, true
}
