package model

import (
	"fmt"
	"strings"
)

// 変数ルール: which column of a list file gives which {{name}} of a
// command set, and which column names the device. Reading a list with a
// rule links it to a set (SetFill): the set's {{name}} lines are then run
// with each device's own values.

// FillRule is one 変数ルール. Where the table is read is as an
// ImportRule's (sheet, header row, …); Key is the column naming the device,
// Items the {{name}}s and their columns.
type FillRule struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	Sheet          string `json:"sheet,omitempty"`
	SheetIndex     int    `json:"sheetIndex,omitempty"`
	HeaderRow      int    `json:"headerRow"`
	DataRow        int    `json:"dataRow,omitempty"`
	StartCol       string `json:"startCol,omitempty"`
	MergeOnlyFirst bool   `json:"mergeOnlyFirst,omitempty"`

	Key   ImportField `json:"key"`
	Items []FillItem  `json:"items"`

	SampleName string `json:"sampleName,omitempty"`
	LastUsed   string `json:"lastUsed,omitempty"`
}

// FillItem is one {{name}} of a rule: its column (as an ImportField reads
// one: a column, fallbacks, steps) and what an empty value does.
type FillItem struct {
	Name string `json:"name"`
	ImportField
	// Empty: "" — the device is not run (the value is needed); "skip" —
	// the lines holding it are left out. A Default fills an empty value
	// first.
	Empty string `json:"empty,omitempty"`
}

// FillEmptySkip is FillItem.Empty for "leave the line out".
const FillEmptySkip = "skip"

// MaxFillItems is how many {{name}}s one rule may give.
const MaxFillItems = 50

// fillKeyPrefix keeps an item's field key apart from the device fields an
// ImportRule knows (an item named "host" is not the IP address).
const fillKeyPrefix = "fill:"

// FillItemKey is the field key an item reads under as an ImportRule.
func FillItemKey(name string) string { return fillKeyPrefix + name }

// AsImportRule is the rule as the list reader takes it: the device column
// under "name", each item under FillItemKey.
func (r *FillRule) AsImportRule() *ImportRule {
	ir := &ImportRule{Sheet: r.Sheet, SheetIndex: r.SheetIndex, HeaderRow: r.HeaderRow, DataRow: r.DataRow,
		StartCol: r.StartCol, MergeOnlyFirst: r.MergeOnlyFirst}
	k := r.Key
	k.Key = "name"
	ir.Fields = append(ir.Fields, k)
	for _, it := range r.Items {
		f := it.ImportField
		f.Key = FillItemKey(it.Name)
		ir.Fields = append(ir.Fields, f)
	}
	return ir
}

// CheckFillRule refuses a rule that cannot be read or used: no name, no
// device column, an item without a column or with a name a command line
// cannot hold, two items of one name.
func CheckFillRule(r *FillRule) error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return fmt.Errorf("ルール名は必須です")
	}
	if r.HeaderRow < 1 {
		return fmt.Errorf("見出しの行は 1 以上にしてください")
	}
	if r.DataRow != 0 && r.DataRow <= r.HeaderRow {
		return fmt.Errorf("データの開始行は見出しの行より下にしてください")
	}
	if r.Key.Source == "" {
		return fmt.Errorf("機器名の列を選んでください")
	}
	if len(r.Items) == 0 {
		return fmt.Errorf("変数を 1 つ以上追加してください")
	}
	if len(r.Items) > MaxFillItems {
		return fmt.Errorf("変数が多すぎます（%d 個まで）", MaxFillItems)
	}
	seen := map[string]bool{}
	for i := range r.Items {
		it := &r.Items[i]
		it.Name = strings.TrimSpace(it.Name)
		if it.Name == "" {
			return fmt.Errorf("%d 番目の変数の名前が空です", i+1)
		}
		if !fillNameRe.MatchString(it.Name) {
			return fmt.Errorf("変数の名前「%s」は使えません（文字・数字・「_」「-」で、数字や記号で始めない）", it.Name)
		}
		if err := checkFillName(it.Name); err != nil {
			return err
		}
		if seen[strings.ToLower(it.Name)] {
			return fmt.Errorf("変数「%s」が 2 つあります", it.Name)
		}
		seen[strings.ToLower(it.Name)] = true
		if it.Source == "" {
			return fmt.Errorf("変数「%s」の列を選んでください", it.Name)
		}
		if it.Empty != "" && it.Empty != FillEmptySkip {
			it.Empty = ""
		}
	}
	return nil
}

// SetFill is the list linked to a command set and the values read from it.
type SetFill struct {
	RuleID string `json:"ruleId"`
	// Rule is the rule's name when read (shown when the rule is gone).
	Rule string `json:"rule"`
	// File is the list's name, Path where it was (読み直す reads it again),
	// Read when it was read ("2006-01-02 15:04").
	File string `json:"file"`
	Path string `json:"path"`
	Read string `json:"read"`
	// Items are the rule's {{name}}s in order; Skip those whose empty value
	// leaves the line out.
	Items []string `json:"items"`
	Skip  []string `json:"skip,omitempty"`
	// Devices are the device names of the list, each with its rows (name →
	// value), in the list's order.
	Devices []FillDevice `json:"devices"`
}

// FillDevice is one device of a linked list.
type FillDevice struct {
	Name string              `json:"name"`
	Rows []map[string]string `json:"rows"`
}

// FillKey is how a device name is matched to a list's: case and the
// spaces around it do not count.
func FillKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// RowsFor are the device's rows of the list (nil: the list has none).
func (f *SetFill) RowsFor(device string) []map[string]string {
	if f == nil {
		return nil
	}
	k := FillKey(device)
	for i := range f.Devices {
		if FillKey(f.Devices[i].Name) == k {
			return f.Devices[i].Rows
		}
	}
	return nil
}

// SkipSet is Skip as a set.
func (f *SetFill) SkipSet() map[string]bool {
	if f == nil || len(f.Skip) == 0 {
		return nil
	}
	m := map[string]bool{}
	for _, n := range f.Skip {
		m[n] = true
	}
	return m
}

// Clone is a deep copy (a copied set does not share its values' maps).
func (f *SetFill) Clone() *SetFill {
	if f == nil {
		return nil
	}
	c := *f
	c.Items = append([]string(nil), f.Items...)
	c.Skip = append([]string(nil), f.Skip...)
	c.Devices = make([]FillDevice, len(f.Devices))
	for i, d := range f.Devices {
		rows := make([]map[string]string, len(d.Rows))
		for j, r := range d.Rows {
			m := make(map[string]string, len(r))
			for k, v := range r {
				m[k] = v
			}
			rows[j] = m
		}
		c.Devices[i] = FillDevice{Name: d.Name, Rows: rows}
	}
	return &c
}
