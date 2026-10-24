package listimport

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kojio145/palaterm/internal/model"
)

// Row is one device of a value row after the rule: each field's value
// (steps run, default filled in), and why it is left out when it is. A row
// giving two devices (a redundant pair) is two Rows of one Line.
type Row struct {
	Line int `json:"line"`
	// Unit is which device of the line (1 = the first; 2… = the rule's
	// Units), Label that unit's name (バックアップ). ID names the device
	// of the line uniquely: the line itself for the first, so choices and
	// messages keep their row numbers.
	Unit   int               `json:"unit"`
	ID     int               `json:"id"`
	Label  string            `json:"label,omitempty"`
	Values map[string]string `json:"values"`
	// Raw is each field's value before its steps (the rule editor shows it
	// as the sample the steps start from).
	Raw map[string]string `json:"raw"`
	// Filled are the fields whose value is the default (the source was
	// empty after the steps).
	Filled []string `json:"filled"`
	// From names, per field, the fallback column its value came from
	// (only for those read from one).
	From map[string]string `json:"from,omitempty"`
	// Guessed are the fields a guessing step decided: an OS type or command
	// set chosen from this PalaTerm's, or none found (empty, the default
	// then). Either way the value depends on this PalaTerm, not the file.
	Guessed []string `json:"guessed,omitempty"`
	// Lost are the fields whose column the file does not have (see
	// sourceValueFrom): their value (empty, or the default) is not the
	// source's, so an existing device keeps its own.
	Lost []string `json:"lost,omitempty"`
	// Key is the row's match key: the key column's text (KeyBy names the
	// column), or the name when there is none or it is empty (KeyBy "").
	Key      string `json:"key"`
	KeyBy    string `json:"keyBy,omitempty"`
	Excluded string `json:"excluded,omitempty"`
	// Exclusion is the condition that left the row out (nil: none, or no
	// name): the page words it in its own language.
	Exclusion *model.ImportCond `json:"exclusion,omitempty"`
	// Keys are the fields this device's values give (the cells an import
	// writes to it).
	Keys []string `json:"-"`
}

// UnitID names the unit-th device of a line (see Row.ID).
func UnitID(line, unit int) int { return line + (unit-1)*1000000 }

// RowLabel is how messages name a row: "12", or "12（バックアップ）" /
// "12-2" for a further device of it.
func RowLabel(line, unit int, label string) string {
	switch {
	case unit <= 1:
		return strconv.Itoa(line)
	case label != "":
		return fmt.Sprintf("%d（%s）", line, label)
	}
	return fmt.Sprintf("%d-%d", line, unit)
}

// unitFields is the first device's fields with a unit's own in their place
// (a unit's field keeps the first one's default when it has none — except
// the name: a line without a second device has an empty name cell, and the
// first device's default there would make a phantom device of every such
// line, all named alike).
func unitFields(base []model.ImportField, u model.ImportUnit) []model.ImportField {
	out := append([]model.ImportField(nil), base...)
	for _, o := range u.Fields {
		if o.Source == "" {
			continue
		}
		found := false
		for i := range out {
			if out[i].Key == o.Key {
				if o.Default == "" && o.Key != "name" {
					o.Default = out[i].Default
				}
				out[i], found = o, true
				break
			}
		}
		if !found {
			out = append(out, o)
		}
	}
	return out
}

// Converted is the rule run over a table.
type Converted struct {
	Rows []Row `json:"rows"`
	// Missing names the columns the rule refers to that the table does not
	// have: their fields read empty — a new device takes the default, an
	// existing one keeps its value (Row.Lost).
	Missing []string `json:"missing"`
}

// Mapped is the rule's fields that give a value (a source or a default):
// the cells an import writes. The name always is one.
func Mapped(r *model.ImportRule) []model.ImportField {
	var out []model.ImportField
	for _, f := range r.Fields {
		if f.Source != "" || f.Default != "" {
			out = append(out, f)
		}
	}
	return out
}

// Convert runs the rule over the table's rows; env is what the guessing
// steps choose from (this PalaTerm's OS types and command sets).
func Convert(t *Table, r *model.ImportRule, env *Env) *Converted {
	fields := Mapped(r)
	missing := map[string]bool{}
	out := &Converted{Missing: []string{}}
	keyCol := 0
	if kc := strings.TrimSpace(r.Match.KeyColumn); kc != "" {
		if keyCol = t.column(kc, 0); keyCol == 0 {
			missing[kc] = true
		}
	}
	units := [][]model.ImportField{fields}
	labels := []string{""}
	for _, u := range r.Units {
		units = append(units, unitFields(fields, u))
		labels = append(labels, u.Label)
	}
	for _, src := range t.Rows {
		for ui, fs := range units {
			unit := ui + 1
			row := Row{Line: src.Line, Unit: unit, ID: UnitID(src.Line, unit), Label: labels[ui],
				Values: map[string]string{}, Raw: map[string]string{}, Filled: []string{}}
			for i := range fs {
				f := &fs[i]
				row.Keys = append(row.Keys, f.Key)
				v, from, miss, lost := t.sourceValueFrom(f, src)
				for _, m := range miss {
					missing[m] = true
				}
				if lost {
					row.Lost = append(row.Lost, f.Key)
				}
				if from != "" {
					if row.From == nil {
						row.From = map[string]string{}
					}
					row.From[f.Key] = from
				}
				row.Raw[f.Key] = v
				v, guessed := applySteps(v, f.Steps, env)
				if guessed {
					row.Guessed = append(row.Guessed, f.Key)
				}
				if strings.TrimSpace(v) == "" && f.Default != "" {
					v = f.Default
					row.Filled = append(row.Filled, f.Key)
				}
				row.Values[f.Key] = normalizeChoice(f.Key, v)
			}
			name := strings.TrimSpace(row.Values["name"])
			if unit > 1 && name == "" {
				continue // a row without a second device (not every line is a pair)
			}
			row.Key = name
			if keyCol != 0 {
				if k := strings.TrimSpace(src.Cells[keyCol]); k != "" {
					// One asset number for the pair: the further devices
					// are told apart by their unit.
					if unit > 1 {
						k += "#" + strconv.Itoa(unit)
					}
					row.Key, row.KeyBy = k, strings.TrimSpace(r.Match.KeyColumn)
				}
			}
			if name == "" {
				row.Excluded = "機器名が空です"
			} else if c := excluded(t, r, src, row.Values, row.Lost, missing); c != nil {
				row.Excluded, row.Exclusion = fmt.Sprintf("除外条件: %s%s", condWhat(*c), condWords(*c)), c
			}
			out.Rows = append(out.Rows, row)
		}
	}
	for m := range missing {
		out.Missing = append(out.Missing, m)
	}
	sort.Strings(out.Missing)
	return out
}

// excluded returns the first exclusion condition the row meets. A condition
// on a column the table does not have (or on a field whose value is lost
// with its column) is not judged: the empty value it would read says
// nothing about the device, and "状態 が空" would leave out every row of a
// file whose 状態 column was renamed. The column is reported missing instead.
func excluded(t *Table, r *model.ImportRule, src SrcRow, vals map[string]string, lost []string, missing map[string]bool) *model.ImportCond {
	for i, c := range r.Exclude {
		var v string
		if c.Target == "field" {
			if slices.Contains(lost, c.Key) {
				continue
			}
			v = vals[c.Key]
		} else {
			col := t.column(c.Key, 0)
			if col == 0 {
				missing[c.Key] = true
				continue
			}
			v = src.Cells[col]
		}
		if condMet(c, v) {
			return &r.Exclude[i]
		}
	}
	return nil
}

func condWhat(c model.ImportCond) string {
	if c.Target == "field" {
		return FieldLabel(c.Key)
	}
	return c.Key
}

func condMet(c model.ImportCond, v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	w := strings.ToLower(strings.TrimSpace(c.Value))
	switch c.Op {
	case "empty":
		return v == ""
	case "notempty":
		return v != ""
	case "contains":
		return w != "" && strings.Contains(v, w)
	case "notcontains":
		return w != "" && !strings.Contains(v, w)
	case "equals":
		return v == w
	case "notequals":
		return v != w
	}
	return false
}

func condWords(c model.ImportCond) string {
	switch c.Op {
	case "empty":
		return "が空"
	case "notempty":
		return "が空でない"
	case "contains":
		return "が「" + c.Value + "」を含む"
	case "notcontains":
		return "が「" + c.Value + "」を含まない"
	case "equals":
		return "が「" + c.Value + "」"
	case "notequals":
		return "が「" + c.Value + "」でない"
	}
	return ""
}
