package listimport

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
	"golang.org/x/text/unicode/norm"
)

// Suggestion is a first guess at a rule from a sample file: the header row
// and, by the header names, a column for the usual fields — filled in for
// the user to check, never taken as is.
type Suggestion struct {
	HeaderRow int `json:"headerRow"`
	// Fields are the first device's columns, Unit the second device's when
	// the header repeats its names (a redundant pair's backup block).
	Fields []model.ImportField `json:"fields"`
	Unit   []model.ImportField `json:"unit"`
}

// GuessHeaderRow picks the row of the first 30 that looks most like a
// header: the most distinct short texts that are not numbers. A group row
// above the header (the same word over several columns) counts once per
// word, so the row of names below it wins.
func GuessHeaderRow(rows [][]string) int {
	best, bestScore := 1, -1
	for i := 0; i < len(rows) && i < 30; i++ {
		seen := map[string]bool{}
		for _, v := range rows[i] {
			v = foldHeader(v)
			if v == "" || len([]rune(v)) > 40 || isNumberish(v) {
				continue
			}
			seen[v] = true
		}
		score := len(seen)
		// A header has values under it.
		if i+1 >= len(rows) || filled(rows[i+1]) == 0 {
			score /= 2
		}
		if score > bestScore {
			best, bestScore = i+1, score
		}
	}
	return best
}

func filled(row []string) int {
	n := 0
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	return n
}

func isNumberish(v string) bool {
	v = strings.NewReplacer("-", "", "/", "", ".", "", ":", "", " ", "").Replace(v)
	if v == "" {
		return true
	}
	_, err := strconv.ParseFloat(v, 64)
	return err == nil
}

// fieldWords are the words a header holds for each field, tried in this
// order (enablePassword before password: "enable パスワード" is the
// former), and the words that rule a header out ("接続先" is a contact,
// "メールアドレス" no device address).
var fieldWords = []struct {
	key       string
	words     []string
	notWords  []string
	exactOnly []string
}{
	{key: "name", words: []string{"ホスト名", "hostname", "機器名", "装置名", "ノード名", "nodename", "devicename", "機器名称", "ホスト"}, notWords: []string{"ip", "アドレス"}},
	{key: "host", words: []string{"ipアドレス", "管理ip", "ipaddress", "ip", "アドレス", "address"}, notWords: []string{"メール", "mail", "mac", "住所"}},
	{key: "conn", words: []string{"接続方式", "プロトコル", "protocol", "接続"}, notWords: []string{"接続先"}},
	{key: "port", words: []string{"ポート", "port"}, notWords: []string{"ポート数", "スイッチ"}},
	{key: "site", words: []string{"拠点", "設置場所", "設置先", "サイト", "site"}, notWords: []string{"コード", "code"}},
	{key: "role", words: []string{"役割", "用途", "role", "区分"}, notWords: []string{"網", "回線"}},
	{key: "enablePassword", words: []string{"enable", "特権"}},
	{key: "username", words: []string{"ユーザ", "user", "ログインid", "アカウント", "account"}, notWords: []string{"パスワード", "password"}},
	{key: "password", words: []string{"パスワード", "password", "passwd", "pw"}, notWords: []string{"enable", "特権"}},
}

func normWord(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(s)), ""))
}

// reRepeated is the mark ReadTable gives the second and later columns of a
// repeated header ("Router (2)") — only that: "ポート (TCP)" is a header of
// its own.
var reRepeated = regexp.MustCompile(` \(\d+\)$`)

func isRepeated(name string) bool { return reRepeated.MatchString(name) }

// Suggest guesses a rule for the sheet: the header row (when the rule
// still has the first row, as a new rule does) and the columns of the
// usual fields by their header names.
func Suggest(s *sheet.Sheet, r model.ImportRule) Suggestion {
	rows := FilledRows(s, &r)
	if r.HeaderRow <= 1 {
		r.HeaderRow = GuessHeaderRow(rows)
	}
	out := Suggestion{HeaderRow: r.HeaderRow, Fields: []model.ImportField{}, Unit: []model.ImportField{}}
	r.DataRow = 0
	t, err := ReadTable(s, &r)
	if err != nil {
		return out
	}
	byName := map[string]Header{}
	for _, h := range t.Headers {
		byName[h.Name] = h
	}
	used := map[int]bool{}
	for _, fw := range fieldWords {
		for _, h := range t.Headers {
			if used[h.Col] || h.Name == "" || isRepeated(h.Name) {
				continue // a repeated header is the second device's
			}
			w := normWord(h.Name)
			if !hasAny(w, fw.words) || hasAny(w, fw.notWords) {
				continue
			}
			used[h.Col] = true
			out.Fields = append(out.Fields, model.ImportField{Key: fw.key, Source: "column", Column: h.Name, ColIndex: h.Col})
			// The same header again: the second device's column.
			if h2, ok := byName[h.Name+" (2)"]; ok {
				out.Unit = append(out.Unit, model.ImportField{Key: fw.key, Source: "column", Column: h2.Name, ColIndex: h2.Col})
			}
			break
		}
	}
	// A model column gives the OS type and the command set by the guessing
	// steps (the raw model, as before, would not import).
	// The words in order of preference: a 型番 column before a 機種 one.
model:
	for _, mw := range modelWords {
		for _, h := range t.Headers {
			if used[h.Col] || h.Name == "" || isRepeated(h.Name) {
				continue
			}
			if w := normWord(h.Name); strings.Contains(w, normWord(mw)) && !hasAny(w, []string{"コード", "code", "数", "種別", "区分"}) {
				used[h.Col] = true
				out.Fields = append(out.Fields,
					model.ImportField{Key: "osType", Source: "column", Column: h.Name, ColIndex: h.Col, Steps: []model.ImportStep{{Type: "guessOS"}}},
					model.ImportField{Key: "commandSet", Source: "column", Column: h.Name, ColIndex: h.Col, Steps: []model.ImportStep{{Type: "guessSet"}}})
				break model
			}
		}
	}
	// A second device is offered only with a name of its own.
	hasName := false
	for _, f := range out.Unit {
		hasName = hasName || f.Key == "name"
	}
	if !hasName {
		out.Unit = []model.ImportField{}
	}
	return out
}

// modelWords are what a model column is headed with, most telling first
// (a 機器種別 column says ルータ or L2SW, not the model: never taken).
var modelWords = []string{"型番", "型式", "機種", "モデル", "model", "製品名"}

func hasAny(w string, words []string) bool {
	for _, x := range words {
		if strings.Contains(w, normWord(x)) {
			return true
		}
	}
	return false
}
