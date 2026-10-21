package model

import (
	"fmt"
	"regexp"
	"strings"
)

// ImportRule is one 変換ルール of 一覧の取り込み: how a device list another
// system exported (Excel, CSV, …) is read and turned into PalaTerm's device
// fields. Rules live in the vault (a default value may be a password) and
// are handed to other people as a JSON file without those secrets.
type ImportRule struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Where the table is: the sheet (by name, or failing that its number,
	// 1-based; 0 = the first), the header row and the first value row
	// (1-based; 0 = the row after the header), and the column the table
	// starts at ("" = the header row's first filled cell, else a letter).
	Sheet      string `json:"sheet,omitempty"`
	SheetIndex int    `json:"sheetIndex,omitempty"`
	HeaderRow  int    `json:"headerRow"`
	DataRow    int    `json:"dataRow,omitempty"`
	StartCol   string `json:"startCol,omitempty"`
	// MergeOnlyFirst leaves a merged area's value in its top-left cell only;
	// by default every cell of the area reads the value (a site written once
	// for five rows applies to all five).
	MergeOnlyFirst bool `json:"mergeOnlyFirst,omitempty"`

	Fields  []ImportField `json:"fields"`
	Exclude []ImportCond  `json:"exclude,omitempty"`
	Match   ImportMatch   `json:"match"`

	// Units are more devices taken from the same row — a redundant pair
	// listed as one line (primary and backup) gives a second device. Each
	// sets where its fields differ from the first device's (the name at
	// least); every other field is read as the first device's is.
	Units []ImportUnit `json:"units,omitempty"`

	// SampleName is the file the rule was last tried on (a hint shown in
	// the list, not a path). LastUsed is the date of the last import.
	SampleName string `json:"sampleName,omitempty"`
	LastUsed   string `json:"lastUsed,omitempty"`
}

// MaxImportUnits is how many devices one row may give (the first and up
// to three more).
const MaxImportUnits = 4

// ImportUnit is a further device of a row: its label (バックアップ, shown
// in the preview) and the fields read differently from the first device.
type ImportUnit struct {
	Label  string        `json:"label,omitempty"`
	Fields []ImportField `json:"fields"`
}

// ImportField fills one device field (a csvio grid column key: name, host,
// osType, password, bastion1Host, …).
type ImportField struct {
	Key string `json:"key"`
	// Source: "column" (one column, found by its header text, or failing
	// that by ColIndex, 1-based from column A), "template" (text with
	// {header} placeholders, joining columns) or "" (no source: the default
	// only).
	Source   string       `json:"source,omitempty"`
	Column   string       `json:"column,omitempty"`
	ColIndex int          `json:"colIndex,omitempty"`
	Template string       `json:"template,omitempty"`
	Steps    []ImportStep `json:"steps,omitempty"`
	// Default fills the field when the value is empty after the steps.
	Default string `json:"default,omitempty"`
}

// ImportStep is one step of a field's processing, applied in order.
//
//	trim     前後の空白を除く
//	split    区切って残す: Sep, Pos first|last, Keep left|right; no Sep in
//	         the value: unchanged, or empty with Miss "empty"
//	replace  文字を置き換える・削る: From → To
//	regex    正規表現: Pattern → To ($1 …)
//	map      置き換え表: Rows; Miss keep|empty for a value no row matches
//	lower / upper  小文字・大文字にする
//	width    全角を半角にする (NFKC)
type ImportStep struct {
	Type    string         `json:"type"`
	Sep     string         `json:"sep,omitempty"`
	Pos     string         `json:"pos,omitempty"`
	Keep    string         `json:"keep,omitempty"`
	From    string         `json:"from,omitempty"`
	To      string         `json:"to,omitempty"`
	Pattern string         `json:"pattern,omitempty"`
	Rows    []ImportMapRow `json:"rows,omitempty"`
	Miss    string         `json:"miss,omitempty"`
}

// ImportMapRow is one line of a 置き換え表: Match "equals" or "contains"
// (both ignore case).
type ImportMapRow struct {
	Match string `json:"match,omitempty"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// ImportCond leaves a row out (除外条件). Target "field" looks at a device
// field after processing (Key = its key), "column" at a column's raw text
// (Key = its header). Op: empty, notempty, contains, notcontains, equals,
// notequals (ignoring case and surrounding spaces).
type ImportCond struct {
	Target string `json:"target"`
	Key    string `json:"key"`
	Op     string `json:"op"`
	Value  string `json:"value,omitempty"`
}

// ImportMatch says how a row finds the device it updates in a group that
// already has devices. A device remembers the key it was imported under
// (Inventory.ImportRecords) so a rename on either side does not lose it; failing
// that, names are compared ignoring the differences switched on here;
// failing that, an equal IP address is offered for confirmation.
type ImportMatch struct {
	IgnoreCase bool `json:"ignoreCase"`
	Width      bool `json:"width"`
	Space      bool `json:"space"`
	Domain     bool `json:"domain,omitempty"`
	Dash       bool `json:"dash,omitempty"`
	// KeyColumn, when set, is a column (an asset number, say) that
	// identifies a device better than its name, and is matched first.
	KeyColumn string `json:"keyColumn,omitempty"`
}

// ImportStepTypes are the step types a rule may use.
var ImportStepTypes = []string{"trim", "split", "replace", "regex", "map", "lower", "upper", "width"}

// ImportCondOps are the operators an exclusion condition may use.
var ImportCondOps = []string{"empty", "notempty", "contains", "notcontains", "equals", "notequals"}

var reColLetters = regexp.MustCompile(`^[A-Za-z]{1,3}$`)

// ColumnNumber reads a column letter (A = 1, Z = 26, AA = 27); 0 if it is
// not one.
func ColumnNumber(s string) int {
	s = strings.TrimSpace(s)
	if !reColLetters.MatchString(s) {
		return 0
	}
	n := 0
	for _, r := range strings.ToUpper(s) {
		n = n*26 + int(r-'A'+1)
	}
	return n
}

// ColumnLetter is ColumnNumber's inverse ("" for n < 1).
func ColumnLetter(n int) string {
	s := ""
	for n > 0 {
		n--
		s = string(rune('A'+n%26)) + s
		n /= 26
	}
	return s
}

// Validate checks a rule before it is saved or used. Field keys are
// checked by the caller (it knows the columns).
func (r *ImportRule) Validate() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return fmt.Errorf("ルール名を入力してください")
	}
	if len([]rune(r.Name)) > 80 {
		return fmt.Errorf("ルール名は 80 文字までです")
	}
	if r.HeaderRow < 1 {
		return fmt.Errorf("見出しの行は 1 以上にしてください")
	}
	if r.DataRow != 0 && r.DataRow <= r.HeaderRow {
		return fmt.Errorf("値の開始行は見出しの行より後にしてください")
	}
	if r.SheetIndex < 0 {
		return fmt.Errorf("シートの番号は 1 以上にしてください")
	}
	if c := strings.TrimSpace(r.StartCol); c != "" && ColumnNumber(c) == 0 {
		return fmt.Errorf("表の開始列は A、D、AB のような列の記号で入力してください")
	}
	seen := map[string]bool{}
	hasName := false
	for i := range r.Fields {
		f := &r.Fields[i]
		if seen[f.Key] {
			return fmt.Errorf("項目「%s」が 2 回設定されています", f.Key)
		}
		seen[f.Key] = true
		switch f.Source {
		case "", "column", "template":
		default:
			return fmt.Errorf("項目「%s」の元の値の種類が不正です", f.Key)
		}
		if f.Source == "column" && strings.TrimSpace(f.Column) == "" && f.ColIndex < 1 {
			return fmt.Errorf("項目「%s」の元の列を選んでください", f.Key)
		}
		if f.Source == "template" && strings.TrimSpace(f.Template) == "" {
			return fmt.Errorf("項目「%s」の組み立てを入力してください", f.Key)
		}
		if f.Key == "name" && (f.Source != "" || strings.TrimSpace(f.Default) != "") {
			hasName = true
		}
		for j := range f.Steps {
			if err := f.Steps[j].Validate(); err != nil {
				return fmt.Errorf("項目「%s」の加工手順 %d: %v", f.Key, j+1, err)
			}
		}
	}
	if !hasName {
		return fmt.Errorf("機器名の元の値を設定してください")
	}
	if len(r.Units)+1 > MaxImportUnits {
		return fmt.Errorf("1 行から作れる機器は %d 台までです", MaxImportUnits)
	}
	for u := range r.Units {
		unit := &r.Units[u]
		unit.Label = strings.TrimSpace(unit.Label)
		if len([]rune(unit.Label)) > 40 {
			return fmt.Errorf("%d 台目の名前は 40 文字までです", u+2)
		}
		seen := map[string]bool{}
		named := false
		for i := range unit.Fields {
			f := &unit.Fields[i]
			if seen[f.Key] {
				return fmt.Errorf("%d 台目の項目「%s」が 2 回設定されています", u+2, f.Key)
			}
			seen[f.Key] = true
			switch f.Source {
			case "column":
				if strings.TrimSpace(f.Column) == "" && f.ColIndex < 1 {
					return fmt.Errorf("%d 台目の項目「%s」の元の列を選んでください", u+2, f.Key)
				}
			case "template":
				if strings.TrimSpace(f.Template) == "" {
					return fmt.Errorf("%d 台目の項目「%s」の組み立てを入力してください", u+2, f.Key)
				}
			default:
				return fmt.Errorf("%d 台目の項目「%s」の元の値を選んでください", u+2, f.Key)
			}
			if f.Key == "name" {
				named = true
			}
			for j := range f.Steps {
				if err := f.Steps[j].Validate(); err != nil {
					return fmt.Errorf("%d 台目の項目「%s」の加工手順 %d: %v", u+2, f.Key, j+1, err)
				}
			}
		}
		if !named {
			return fmt.Errorf("%d 台目の機器名の元の値を設定してください（1 台目と同じ名前にはできません）", u+2)
		}
	}
	for i, c := range r.Exclude {
		if c.Target != "field" && c.Target != "column" {
			return fmt.Errorf("除外条件 %d の対象が不正です", i+1)
		}
		if strings.TrimSpace(c.Key) == "" {
			return fmt.Errorf("除外条件 %d の対象を選んでください", i+1)
		}
		ok := false
		for _, op := range ImportCondOps {
			ok = ok || c.Op == op
		}
		if !ok {
			return fmt.Errorf("除外条件 %d の比べ方が不正です", i+1)
		}
	}
	return nil
}

// Validate checks one step.
func (s *ImportStep) Validate() error {
	switch s.Type {
	case "trim", "lower", "upper", "width":
	case "split":
		if s.Sep == "" {
			return fmt.Errorf("区切り文字を入力してください")
		}
		if s.Pos != "first" && s.Pos != "last" {
			s.Pos = "first"
		}
		if s.Keep != "left" && s.Keep != "right" {
			s.Keep = "right"
		}
		if s.Miss != "empty" {
			s.Miss = ""
		}
	case "replace":
		if s.From == "" {
			return fmt.Errorf("置き換える文字を入力してください")
		}
	case "regex":
		if s.Pattern == "" {
			return fmt.Errorf("正規表現を入力してください")
		}
		if len(s.Pattern) > 500 {
			return fmt.Errorf("正規表現が長すぎます（500 文字まで）")
		}
		if _, err := regexp.Compile(s.Pattern); err != nil {
			return fmt.Errorf("正規表現の書き方が正しくありません（%v）", err)
		}
	case "map":
		if s.Miss != "keep" && s.Miss != "empty" {
			s.Miss = "keep"
		}
		for i := range s.Rows {
			if s.Rows[i].Match != "contains" {
				s.Rows[i].Match = "equals"
			}
		}
	default:
		return fmt.Errorf("加工の種類「%s」は使えません", s.Type)
	}
	return nil
}

// ImportRecord is what 一覧の取り込み last wrote to a device.
type ImportRecord struct {
	// Key is what the import matched the device by: the name it had in the
	// source file, or the value of the rule's key column (KeyBy names that
	// column; "" = the name). A later import finds the device by it even
	// when either side renamed it.
	Key   string `json:"key"`
	KeyBy string `json:"keyBy,omitempty"`
	// Base holds the cells (csvio grid keys → canonical text) as the import
	// wrote them, so the next one tells a value changed in the source from
	// one edited here: only the source's changes are taken.
	Base map[string]string `json:"base"`
}

// PruneImportRecords drops the records of devices no longer there.
func (inv *Inventory) PruneImportRecords() {
	if len(inv.ImportRecords) == 0 {
		return
	}
	have := make(map[string]bool, len(inv.Devices))
	for i := range inv.Devices {
		have[inv.Devices[i].ID] = true
	}
	for id := range inv.ImportRecords {
		if !have[id] {
			delete(inv.ImportRecords, id)
		}
	}
}
