package model

import (
	"fmt"
	"regexp"
	"strings"
)

// A command set can hold per-device values (変数) that the run tab fills
// in from a list file (one row per device, more rows for a repeated block):
//
//	interface {{if}}            {{name}}  the device's value; empty stops the device
//	 description {{desc?}}      {{name?}} empty leaves this line out
//	{{#each}}                   on a line of its own: the lines down to
//	vlan {{vlan}}               {{/each}} go out once per row of the device
//	{{/each}}
//
// A name starts with a letter or "_" and holds letters, digits, "_" and "-",
// so the Go templates of a Linux command ("{{.Names}}", "{{json .}}") stay
// text. Outside the block a name takes the device's first row.

var fillRe = regexp.MustCompile(`\{\{\s*([#/]?)([\p{L}_][\p{L}\p{N}_\-]*)(\?)?\s*\}\}`)

// fillNameRe is a name as a 変数ルール's item gives it.
var fillNameRe = regexp.MustCompile(`^[\p{L}_][\p{L}\p{N}_\-]*$`)

// fillValueRe is a value that would read as a key token or a {password}
// variable once it is in the command line: a value is text, never a key.
var fillValueRe = regexp.MustCompile(`\{[^{}]*\}`)

// FillEach / FillEnd are the names of the block markers ({{#each}} {{/each}}).
const (
	FillEach    = "each"
	maxFillName = 64
	maxFillRows = 1000
)

// FillName is one name a set fills in.
type FillName struct {
	Name string `json:"name"`
	// Optional: every use is {{name?}} (an empty value leaves its line out).
	Optional bool `json:"optional,omitempty"`
	// InEach: used inside {{#each}} … {{/each}} (a value per row).
	InEach bool `json:"inEach,omitempty"`
}

// HasFill reports whether a set fills in values (any {{name}} or block).
func HasFill(cmds []Command) bool {
	for _, c := range cmds {
		if fillRe.MatchString(c.Text) {
			return true
		}
	}
	return false
}

// fillLine is one command line read for its placeholders.
type fillLine struct {
	marker string // "#" (block start), "/" (block end) or ""
}

// CheckFill refuses a set whose placeholders cannot be filled: a block
// marker sharing its line, a block not closed or nested, a name a key token
// or a login variable would also read ({{enter}} holds {enter}).
func CheckFill(cmds []Command) error {
	_, err := parseFill(cmds)
	return err
}

func parseFill(cmds []Command) ([]fillLine, error) {
	lines := make([]fillLine, len(cmds))
	open := -1
	for i, c := range cmds {
		for _, m := range fillRe.FindAllStringSubmatch(c.Text, -1) {
			mark, name := m[1], m[2]
			if mark != "" {
				if name != FillEach || m[3] != "" {
					return nil, fmt.Errorf("%d 行目: 繰り返しは {{#each}} と {{/each}} だけ書けます: %s", i+1, m[0])
				}
				if strings.TrimSpace(c.Text) != m[0] {
					return nil, fmt.Errorf("%d 行目: %s は 1 行に単独で書いてください", i+1, m[0])
				}
				lines[i].marker = mark
				continue
			}
			if err := checkFillName(name); err != nil {
				return nil, fmt.Errorf("%d 行目: %w", i+1, err)
			}
		}
		switch lines[i].marker {
		case "#":
			if open >= 0 {
				return nil, fmt.Errorf("%d 行目: {{#each}} の中に {{#each}} は書けません（%d 行目の {{/each}} がまだです）", i+1, open+1)
			}
			open = i
		case "/":
			if open < 0 {
				return nil, fmt.Errorf("%d 行目: {{/each}} に対応する {{#each}} がありません", i+1)
			}
			open = -1
		}
	}
	if open >= 0 {
		return nil, fmt.Errorf("%d 行目の {{#each}} に対応する {{/each}} がありません", open+1)
	}
	return lines, nil
}

func checkFillName(name string) error {
	if len([]rune(name)) > maxFillName {
		return fmt.Errorf("変数の名前が長すぎます（%d 文字まで）: %s", maxFillName, name)
	}
	// Inside {{name}} the runner would also see {name}: a key token
	// ({enter}) or a login variable ({password}) by that name is refused.
	low := strings.ToLower(name)
	switch low {
	case "user", "password", "enable", "each":
		return fmt.Errorf("変数の名前「%s」は使えません（ほかの書き方と重なるため）", name)
	}
	if keyTokenRe.MatchString("{"+name+"}") || ctrlTokenRe.MatchString("{"+name+"}") {
		return fmt.Errorf("変数の名前「%s」はキー札と重なるため使えません", name)
	}
	return nil
}

// FillNames lists the names a set fills in, in the order they first appear.
func FillNames(cmds []Command) []FillName {
	lines, err := parseFill(cmds)
	if err != nil {
		return nil
	}
	var out []FillName
	at := map[string]int{}
	inEach := false
	for i, c := range cmds {
		switch lines[i].marker {
		case "#":
			inEach = true
			continue
		case "/":
			inEach = false
			continue
		}
		for _, m := range fillRe.FindAllStringSubmatch(c.Text, -1) {
			name, opt := m[2], m[3] != ""
			if j, ok := at[name]; ok {
				out[j].Optional = out[j].Optional && opt
				out[j].InEach = out[j].InEach || inEach
				continue
			}
			at[name] = len(out)
			out = append(out, FillName{Name: name, Optional: opt, InEach: inEach})
		}
	}
	return out
}

// HasFillEach reports whether a set has a {{#each}} block.
func HasFillEach(cmds []Command) bool {
	lines, err := parseFill(cmds)
	if err != nil {
		return false
	}
	for _, l := range lines {
		if l.marker == "#" {
			return true
		}
	}
	return false
}

// fillValue is the row's value of a name: the name as written, else the
// one that differs only in case ({{nh}} takes NH's — a rule's names differ
// by more than case, so at most one does).
func fillValue(row map[string]string, name string) string {
	if v, ok := row[name]; ok {
		return v
	}
	for k, v := range row {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func fillSkips(skip map[string]bool, name string) bool {
	if skip[name] {
		return true
	}
	for k, on := range skip {
		if on && strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// ExpandFill is the set as one device runs it: rows are the device's rows
// of the list (name → value). The block goes out once per row; outside it
// a name takes the first row's value. A value that is empty stops the
// device, unless written {{name?}} or named in skip (the rule's 「空なら
// その行を送らない」): then its line is left out. The markers themselves
// are not sent. Names are matched whatever their case.
func ExpandFill(cmds []Command, rows []map[string]string, skip map[string]bool) ([]Command, error) {
	lines, err := parseFill(cmds)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("変数の値がありません")
	}
	if len(rows) > maxFillRows {
		return nil, fmt.Errorf("変数の行が多すぎます（1 台 %d 行まで）", maxFillRows)
	}
	var out []Command
	// fill returns the line with row's values in, ok false when an
	// optional value is empty (the line is left out).
	fill := func(c Command, row map[string]string) (Command, bool, error) {
		var ferr error
		leave := false
		c.Text = fillRe.ReplaceAllStringFunc(c.Text, func(s string) string {
			m := fillRe.FindStringSubmatch(s)
			v := strings.TrimSpace(fillValue(row, m[2]))
			if v == "" {
				if m[3] != "" || fillSkips(skip, m[2]) {
					leave = true
				} else if ferr == nil {
					ferr = fmt.Errorf("「%s」の値が空です", m[2])
				}
				return ""
			}
			if ferr == nil {
				if err := CheckCommandText(v); err != nil {
					ferr = fmt.Errorf("「%s」の値に%w", m[2], err)
				} else if fillValueRe.MatchString(v) {
					ferr = fmt.Errorf("「%s」の値に {…} は使えません: %s", m[2], v)
				}
			}
			return v
		})
		return c, !leave, ferr
	}
	for i := 0; i < len(cmds); i++ {
		if lines[i].marker == "#" {
			end := i + 1
			for lines[end].marker != "/" {
				end++
			}
			for _, row := range rows {
				for _, c := range cmds[i+1 : end] {
					fc, ok, err := fill(c, row)
					if err != nil {
						return nil, err
					}
					if ok {
						out = append(out, fc)
					}
				}
			}
			i = end
			continue
		}
		fc, ok, err := fill(cmds[i], rows[0])
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, fc)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("変数を入れた結果、送るコマンドがありません")
	}
	return out, nil
}
