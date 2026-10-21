// Package listimport is 一覧の取り込み: a device list another system
// exported (read by package sheet) is turned into PalaTerm devices by an
// ImportRule — the table's position, a source and processing steps per
// field, defaults, exclusions — and merged into one group. Re-importing a
// newer list takes only what changed in the source: each device remembers
// the cells the last import wrote (Inventory.ImportRecords), so a value edited
// in PalaTerm since is kept, and one changed on both sides is asked about.
//
// Nothing here touches the App lock or the Wails runtime.
package listimport

import (
	"regexp"
	"strings"
	"sync"

	"github.com/kojio145/palaterm/internal/model"
	"golang.org/x/text/unicode/norm"
)

// ApplySteps runs a field's steps over v in order.
func ApplySteps(v string, steps []model.ImportStep) string {
	for _, s := range steps {
		v = applyStep(v, s)
	}
	return v
}

// TraceSteps is ApplySteps keeping the value after each step (the rule
// editor shows them under each step).
func TraceSteps(v string, steps []model.ImportStep) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		v = applyStep(v, s)
		out = append(out, v)
	}
	return out
}

// reCache keeps compiled patterns: a rule runs the same few patterns over
// every row. Each bound call runs on its own goroutine (the step editor
// traces at every key while a preview may be running), hence the lock: a
// map written from two goroutines at once ends the process.
var (
	reMu    sync.Mutex
	reCache = map[string]*regexp.Regexp{}
)

func compile(p string) *regexp.Regexp {
	reMu.Lock()
	defer reMu.Unlock()
	if re, ok := reCache[p]; ok {
		return re
	}
	re, err := regexp.Compile(p)
	if err != nil {
		re = nil
	}
	if len(reCache) > 256 {
		reCache = map[string]*regexp.Regexp{}
	}
	reCache[p] = re
	return re
}

func applyStep(v string, s model.ImportStep) string {
	switch s.Type {
	case "trim":
		return strings.TrimSpace(v)
	case "lower":
		return strings.ToLower(v)
	case "upper":
		return strings.ToUpper(v)
	case "width":
		return norm.NFKC.String(v)
	case "split":
		if s.Sep == "" {
			return v
		}
		i := strings.Index(v, s.Sep)
		if s.Pos == "last" {
			i = strings.LastIndex(v, s.Sep)
		}
		if i < 0 {
			// No separator: the value as it is (core-sw04 alone stays
			// core-sw04), or empty when asked — a cell holding only the
			// user "ops" has no password to give.
			if s.Miss == "empty" {
				return ""
			}
			return v
		}
		if s.Keep == "left" {
			return v[:i]
		}
		return v[i+len(s.Sep):]
	case "replace":
		if s.From == "" {
			return v
		}
		return strings.ReplaceAll(v, s.From, s.To)
	case "regex":
		if re := compileSafe(s.Pattern); re != nil {
			return re.ReplaceAllString(v, s.To)
		}
		return v
	case "map":
		t := strings.TrimSpace(v)
		for _, r := range s.Rows {
			if r.From == "" {
				continue
			}
			if r.Match == "contains" {
				if strings.Contains(strings.ToLower(t), strings.ToLower(r.From)) {
					return r.To
				}
			} else if strings.EqualFold(t, strings.TrimSpace(r.From)) {
				return r.To
			}
		}
		if s.Miss == "empty" {
			return ""
		}
		return v
	}
	return v
}

// compileSafe is compile for patterns from a rule file someone else wrote:
// Go's regexp runs in linear time, so only the length is capped.
func compileSafe(p string) *regexp.Regexp {
	if p == "" || len(p) > 500 {
		return nil
	}
	return compile(p)
}
