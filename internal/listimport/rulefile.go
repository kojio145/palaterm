package listimport

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

// RuleFormat identifies a rule file; bump the number when the layout
// changes in a way an older PalaTerm cannot read.
const RuleFormat = "palaterm-import-rule/1"

// RuleFile is a rule written out to hand to someone else
// (export\import-rules\<名前>.json).
type RuleFile struct {
	Format     string           `json:"format"`
	App        string           `json:"app"`
	ExportedAt string           `json:"exportedAt"`
	Rule       model.ImportRule `json:"rule"`
}

// ValidateRule checks a rule, its field keys included.
func ValidateRule(r *model.ImportRule) error {
	for _, f := range r.Fields {
		if !IsField(f.Key) {
			return fmt.Errorf("項目「%s」は PalaTerm の機器にありません", f.Key)
		}
	}
	for _, u := range r.Units {
		for _, f := range u.Fields {
			if !IsField(f.Key) {
				return fmt.Errorf("項目「%s」は PalaTerm の機器にありません", f.Key)
			}
		}
	}
	for _, c := range r.Exclude {
		if c.Target == "field" && !IsField(c.Key) {
			return fmt.Errorf("除外条件の項目「%s」は PalaTerm の機器にありません", c.Key)
		}
	}
	return r.Validate()
}

// ExportRule renders a rule for someone else: the mapping travels, the
// secrets do not. A password field keeps where its value comes from and
// the steps that only cut it out of the cell (split, trim, case, width),
// but loses every text a password could be written in: its default, a
// combined template, the 置き換え表 / replace / regex steps (their "to"
// may be one), and an exclusion comparing it with a value. The ID, the
// date last used and the sample file's name (a local file) stay home.
func ExportRule(r model.ImportRule, app string) ([]byte, error) {
	out := r
	out.ID, out.LastUsed, out.SampleName = "", "", ""
	out.Fields = stripSecrets(r.Fields)
	out.Units = nil
	for _, u := range r.Units {
		var fs []model.ImportField
		for _, f := range stripSecrets(u.Fields) {
			if f.Source != "" { // a unit's field without a source reads as the first device's
				fs = append(fs, f)
			}
		}
		out.Units = append(out.Units, model.ImportUnit{Label: u.Label, Fields: fs})
	}
	// The columns a password is read from: an exclusion comparing one of
	// them with a value ("元の列: パスワード が Init#1234") names a
	// password too.
	secretCols := map[string]bool{}
	all := append([]model.ImportField(nil), r.Fields...)
	for _, u := range r.Units {
		all = append(all, u.Fields...)
	}
	for _, f := range all {
		if !isSecret(f.Key) {
			continue
		}
		switch f.Source {
		case "column":
			secretCols[foldHeader(f.Column)] = true
			for _, fb := range f.Fallback {
				secretCols[foldHeader(fb)] = true
			}
		case "template":
			for _, m := range reTemplate.FindAllStringSubmatch(f.Template, -1) {
				secretCols[foldHeader(m[1])] = true
			}
		}
	}
	out.Exclude = nil
	for _, c := range r.Exclude {
		if c.Value != "" && ((c.Target == "field" && isSecret(c.Key)) || (c.Target == "column" && secretCols[foldHeader(c.Key)])) {
			continue
		}
		out.Exclude = append(out.Exclude, c)
	}
	return json.MarshalIndent(RuleFile{Format: RuleFormat, App: app, ExportedAt: time.Now().Format("2006-01-02 15:04"), Rule: out}, "", "  ")
}

// stripSecrets is a field list without the texts a password could be
// written in (see ExportRule).
func stripSecrets(fields []model.ImportField) []model.ImportField {
	var out []model.ImportField
	for _, f := range fields {
		if isSecret(f.Key) {
			f.Default = ""
			if f.Source == "template" {
				f.Source, f.Template = "", ""
			}
			var keep []model.ImportStep
			for _, s := range f.Steps {
				switch s.Type {
				case "split", "trim", "lower", "upper", "width":
					keep = append(keep, s)
				}
			}
			f.Steps = keep
			if f.Source == "" && len(f.Steps) == 0 {
				continue // nothing left of it
			}
		}
		out = append(out, f)
	}
	return out
}

// ParseRule reads a rule file.
func ParseRule(data []byte) (model.ImportRule, error) {
	var f RuleFile
	if err := json.Unmarshal(data, &f); err != nil {
		return model.ImportRule{}, fmt.Errorf("変換ルールのファイルではありません（%v）", err)
	}
	if !strings.HasPrefix(f.Format, "palaterm-import-rule/") {
		return model.ImportRule{}, fmt.Errorf("変換ルールのファイルではありません")
	}
	if f.Format != RuleFormat {
		return model.ImportRule{}, fmt.Errorf("新しい PalaTerm で書き出された変換ルールです（%s）。PalaTerm を更新してください", f.Format)
	}
	r := f.Rule
	r.ID, r.LastUsed = "", ""
	if err := ValidateRule(&r); err != nil {
		return model.ImportRule{}, err
	}
	return r, nil
}
