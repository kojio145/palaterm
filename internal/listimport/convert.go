package listimport

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kojio145/palaterm/internal/model"
)

// Row is one value row after the rule: each field's value (steps run,
// default filled in), and why it is left out when it is.
type Row struct {
	Line   int               `json:"line"`
	Values map[string]string `json:"values"`
	// Raw is each field's value before its steps (the rule editor shows it
	// as the sample the steps start from).
	Raw map[string]string `json:"raw"`
	// Filled are the fields whose value is the default (the source was
	// empty after the steps).
	Filled []string `json:"filled"`
	// Key is the row's match key: the key column's text (KeyBy names the
	// column), or the name when there is none or it is empty (KeyBy "").
	Key      string `json:"key"`
	KeyBy    string `json:"keyBy,omitempty"`
	Excluded string `json:"excluded,omitempty"`
	// Exclusion is the condition that left the row out (nil: none, or no
	// name): the page words it in its own language.
	Exclusion *model.ImportCond `json:"exclusion,omitempty"`
}

// Converted is the rule run over a table.
type Converted struct {
	Rows []Row `json:"rows"`
	// Missing names the columns the rule refers to that the table does not
	// have (the fields read empty, so their defaults apply).
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

// Convert runs the rule over the table's rows.
func Convert(t *Table, r *model.ImportRule) *Converted {
	fields := Mapped(r)
	missing := map[string]bool{}
	out := &Converted{Missing: []string{}}
	keyCol := 0
	if kc := strings.TrimSpace(r.Match.KeyColumn); kc != "" {
		if keyCol = t.column(kc, 0); keyCol == 0 {
			missing[kc] = true
		}
	}
	for _, src := range t.Rows {
		row := Row{Line: src.Line, Values: map[string]string{}, Raw: map[string]string{}, Filled: []string{}}
		for i := range fields {
			f := &fields[i]
			v, miss := t.sourceValue(f, src)
			for _, m := range miss {
				missing[m] = true
			}
			row.Raw[f.Key] = v
			v = ApplySteps(v, f.Steps)
			if strings.TrimSpace(v) == "" && f.Default != "" {
				v = f.Default
				row.Filled = append(row.Filled, f.Key)
			}
			row.Values[f.Key] = v
		}
		name := strings.TrimSpace(row.Values["name"])
		row.Key = name
		if keyCol != 0 {
			if k := strings.TrimSpace(src.Cells[keyCol]); k != "" {
				row.Key, row.KeyBy = k, strings.TrimSpace(r.Match.KeyColumn)
			}
		}
		if name == "" {
			row.Excluded = "機器名が空です"
		} else if c := excluded(t, r, src, row.Values, missing); c != nil {
			row.Excluded, row.Exclusion = fmt.Sprintf("除外条件: %s%s", condWhat(*c), condWords(*c)), c
		}
		out.Rows = append(out.Rows, row)
	}
	for m := range missing {
		out.Missing = append(out.Missing, m)
	}
	sort.Strings(out.Missing)
	return out
}

// excluded returns the first exclusion condition the row meets.
func excluded(t *Table, r *model.ImportRule, src SrcRow, vals map[string]string, missing map[string]bool) *model.ImportCond {
	for i, c := range r.Exclude {
		var v string
		if c.Target == "field" {
			v = vals[c.Key]
		} else {
			col := t.column(c.Key, 0)
			if col == 0 {
				missing[c.Key] = true
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
