package history

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// GrepRequest is one search over the log files under Root: every text file
// of every run folder (Dir == ""), or of the one folder Dir. The caller
// checks Dir lies under Root.
type GrepRequest struct {
	Root  string `json:"root"`
	Dir   string `json:"dir,omitempty"`
	Query string `json:"query"`
	// Regex reads Query as a regular expression (RE2); otherwise it is the
	// text itself, whatever characters it holds.
	Regex bool `json:"regex,omitempty"`
	// CaseSensitive: a and A differ. Off, as the viewer's find.
	CaseSensitive bool `json:"caseSensitive,omitempty"`
	// Days limits the files to those written within the last Days days
	// (their modification time); 0 = every file.
	Days int `json:"days,omitempty"`
	// MaxHits caps the lines returned (0 = GrepMaxHits); the count of hits
	// goes on, so the summary still says how many there are.
	MaxHits int `json:"maxHits,omitempty"`
}

// GrepHit is one matching line of a file: its number as the log viewer
// counts lines (the file's text as it was on the screen, see
// CleanTranscript), its text, and where the matches are in it (character
// offsets, start inclusive, end exclusive) for the page to light.
type GrepHit struct {
	Line  int      `json:"line"`
	Text  string   `json:"text"`
	Spans [][2]int `json:"spans,omitempty"`
}

// GrepFile is a file with its matching lines. Count is how many lines
// matched in all — more than len(Hits) once the cap was reached.
type GrepFile struct {
	Path string `json:"path"`
	// Rel is the path under the root (folder\file), what the page shows.
	Rel   string    `json:"rel"`
	Count int       `json:"count"`
	Hits  []GrepHit `json:"hits"`
}

// GrepResult is what a search found.
type GrepResult struct {
	Files []GrepFile `json:"files"`
	// NoHit lists the files searched that had no match (their Rel paths),
	// so "which devices do NOT show this" can be answered too.
	NoHit []string `json:"noHit"`
	// Searched: files read; Skipped: files left out (unreadable, larger
	// than GrepMaxFile); Hits: matching lines in all; Truncated: Hits went
	// past the cap, so Files holds only the first MaxHits of them.
	Searched  int  `json:"searched"`
	Skipped   int  `json:"skipped"`
	Hits      int  `json:"hits"`
	Truncated bool `json:"truncated"`
	// ElapsedMs: how long the search took.
	ElapsedMs int64 `json:"elapsedMs"`
}

const (
	// GrepMaxHits is the default cap on the lines returned.
	GrepMaxHits = 5000
	// GrepMaxFile is the largest file read (bytes); a bigger one is skipped
	// and counted, rather than held in memory whole.
	GrepMaxFile = 64 << 20
	// grepMaxSpans is how many matches of one line are lit.
	grepMaxSpans = 50
	// grepWorkers is how many files are read at once.
	grepWorkers = 4
)

// ErrGrepQuery is returned for an empty query.
var ErrGrepQuery = errors.New("検索する文字を入力してください")

// grepMatcher finds the matches of a query in one line (character offsets).
type grepMatcher func(line string) [][2]int

// newGrepMatcher compiles the request's query.
func newGrepMatcher(req GrepRequest) (grepMatcher, error) {
	q := req.Query
	if q == "" {
		return nil, ErrGrepQuery
	}
	if !req.Regex {
		q = regexp.QuoteMeta(q)
	}
	if !req.CaseSensitive {
		q = "(?i)" + q
	}
	re, err := regexp.Compile(q)
	if err != nil {
		return nil, fmt.Errorf("正規表現が不正です: %s", regexErrText(err))
	}
	return func(line string) [][2]int {
		locs := re.FindAllStringIndex(line, grepMaxSpans)
		if len(locs) == 0 {
			return nil
		}
		// Byte offsets → character offsets, walking the line once.
		spans := make([][2]int, 0, len(locs))
		bytePos, charPos := 0, 0
		conv := func(b int) int {
			charPos += utf8.RuneCountInString(line[bytePos:b])
			bytePos = b
			return charPos
		}
		for _, l := range locs {
			if l[1] == l[0] {
				continue // an empty match (a regex like "x*") lights nothing
			}
			s := conv(l[0])
			e := conv(l[1])
			spans = append(spans, [2]int{s, e})
		}
		if len(spans) == 0 {
			return nil
		}
		return spans
	}, nil
}

// regexErrText strips the "error parsing regexp: " the library prefixes.
func regexErrText(err error) string {
	return strings.TrimPrefix(err.Error(), "error parsing regexp: ")
}

// Grep searches the log files. Files are the ones isLogFile admits, under
// every folder of Root (or under Dir), folders newest first by name, files
// in name order. ctx stops a long search early (what was found is returned,
// Truncated set).
func Grep(ctx context.Context, req GrepRequest) (*GrepResult, error) {
	start := time.Now()
	match, err := newGrepMatcher(req)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(req.Root)
	if err != nil {
		return nil, err
	}
	top := root
	if req.Dir != "" {
		if top, err = filepath.Abs(req.Dir); err != nil {
			return nil, err
		}
	}
	maxHits := req.MaxHits
	if maxHits <= 0 {
		maxHits = GrepMaxHits
	}
	var since time.Time
	if req.Days > 0 {
		since = start.Add(-time.Duration(req.Days) * 24 * time.Hour)
	}

	// The files to read, in the order they are reported.
	type target struct{ path, rel string }
	var targets []target
	res := &GrepResult{Files: []GrepFile{}, NoHit: []string{}}
	err = filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == top {
				return err
			}
			res.Skipped++
			return nil
		}
		if d.IsDir() {
			if p != top && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isLogFile(d.Name()) {
			return nil
		}
		if !since.IsZero() {
			info, err := d.Info()
			if err != nil || info.ModTime().Before(since) {
				return nil
			}
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		targets = append(targets, target{p, rel})
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Newest folder first (folder names start with the date and time), the
	// files of a folder in name order.
	sort.SliceStable(targets, func(i, j int) bool {
		di, dj := filepath.Dir(targets[i].rel), filepath.Dir(targets[j].rel)
		if di != dj {
			return di > dj
		}
		return targets[i].rel < targets[j].rel
	})

	// Read in parallel, report in order.
	type outcome struct {
		file    GrepFile
		skipped bool
		count   int
	}
	outs := make([]outcome, len(targets))
	var wg sync.WaitGroup
	sem := make(chan struct{}, grepWorkers)
	for i, tg := range targets {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, tg target) {
			defer wg.Done()
			defer func() { <-sem }()
			f, ok := grepFile(ctx, tg.path, tg.rel, match, maxHits)
			if !ok {
				outs[i].skipped = true
				return
			}
			outs[i].file = f
			outs[i].count = f.Count
		}(i, tg)
	}
	wg.Wait()

	left := maxHits
	for _, o := range outs {
		if o.skipped {
			res.Skipped++
			continue
		}
		res.Searched++
		if o.count == 0 {
			res.NoHit = append(res.NoHit, o.file.Rel)
			continue
		}
		res.Hits += o.count
		f := o.file
		if len(f.Hits) > left {
			f.Hits = f.Hits[:left]
			res.Truncated = true
		}
		left -= len(f.Hits)
		if len(f.Hits) > 0 || left == 0 {
			res.Files = append(res.Files, f)
		}
	}
	if ctx.Err() != nil {
		res.Truncated = true
	}
	res.ElapsedMs = time.Since(start).Milliseconds()
	return res, nil
}

// grepFile reads one file and returns its matching lines (up to maxHits of
// them; Count says how many there were). ok is false for a file that could
// not be read or is too large.
func grepFile(ctx context.Context, path, rel string, match grepMatcher, maxHits int) (GrepFile, bool) {
	f := GrepFile{Path: path, Rel: rel, Hits: []GrepHit{}}
	info, err := os.Stat(path)
	if err != nil || info.Size() > GrepMaxFile {
		return f, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return f, false
	}
	// The viewer shows CleanTranscript's text and counts its lines, so the
	// line numbers here open at the right place there.
	text := CleanTranscript(string(raw))
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i%2000 == 0 && ctx.Err() != nil {
			break
		}
		spans := match(line)
		if spans == nil {
			continue
		}
		f.Count++
		if len(f.Hits) < maxHits {
			f.Hits = append(f.Hits, GrepHit{Line: i + 1, Text: line, Spans: spans})
		}
	}
	return f, true
}
