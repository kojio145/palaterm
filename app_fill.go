package main

// 変数: a command set with {{name}} lines runs with each device's own
// values. A 変数ルール (model.FillRule) says which column of a list
// file gives which name and which column names the device; reading a list
// with it links the list to a set (model.SetFill, kept in the vault). A run
// fills each device's set in from the values linked (fillSets); the run
// tab and the set editor show the result first (PreviewFill, PreviewFillSet).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kojio145/palaterm/internal/bundle"
	"github.com/kojio145/palaterm/internal/listimport"
	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// maxFillListRows caps the value rows one list may link.
const maxFillListRows = 20000

func fillRulesDir() string { return filepath.Join(exeDir(), "export", "fill-rules") }

func (a *App) fillRuleIndex(id string) int {
	for i := range a.inv.FillRules {
		if a.inv.FillRules[i].ID == id {
			return i
		}
	}
	return -1
}

func (a *App) fillRuleNameTaken(name, exceptID string) bool {
	for _, r := range a.inv.FillRules {
		if strings.EqualFold(strings.TrimSpace(r.Name), strings.TrimSpace(name)) && r.ID != exceptID {
			return true
		}
	}
	return false
}

// SaveFillRule adds (no ID) or replaces a 変数ルール and returns its ID.
func (a *App) SaveFillRule(r model.FillRule) (string, error) {
	if err := model.CheckFillRule(&r); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", errVaultLocked
	}
	if a.fillRuleNameTaken(r.Name, r.ID) {
		a.mu.Unlock()
		return "", fmt.Errorf("同じ名前のルール「%s」があります", r.Name)
	}
	if i := a.fillRuleIndex(r.ID); r.ID != "" && i >= 0 {
		r.LastUsed = a.inv.FillRules[i].LastUsed
		a.inv.FillRules[i] = r
	} else {
		r.ID = model.NewDeviceID()
		a.inv.FillRules = append(a.inv.FillRules, r)
	}
	a.mu.Unlock()
	return r.ID, a.persist()
}

// ReorderFillRules puts the rules in the order of their IDs.
func (a *App) ReorderFillRules(order []string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return errVaultLocked
	}
	reorderSlots(a.inv.FillRules, func(r model.FillRule) string { return r.ID }, order)
	a.mu.Unlock()
	return a.persist()
}

// DeleteFillRule removes a rule. The sets it linked keep their values.
func (a *App) DeleteFillRule(id string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return errVaultLocked
	}
	i := a.fillRuleIndex(id)
	if i < 0 {
		a.mu.Unlock()
		return fmt.Errorf("ルールが見つかりません")
	}
	a.inv.FillRules = append(a.inv.FillRules[:i], a.inv.FillRules[i+1:]...)
	a.mu.Unlock()
	return a.persist()
}

// fillRuleFile is a 変数ルール as written to a file.
type fillRuleFile struct {
	Format  string         `json:"format"`
	Version int            `json:"version"`
	App     string         `json:"app"`
	Rule    model.FillRule `json:"rule"`
}

const fillRuleFormat = "palaterm-fill-rule"

// ExportFillRule writes a rule to export\fill-rules\<名前>.json and returns
// the file.
func (a *App) ExportFillRule(id string) (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", errVaultLocked
	}
	i := a.fillRuleIndex(id)
	if i < 0 {
		a.mu.Unlock()
		return "", fmt.Errorf("ルールが見つかりません")
	}
	r := a.inv.FillRules[i]
	a.mu.Unlock()
	r.ID, r.LastUsed = "", ""
	data, err := json.MarshalIndent(fillRuleFile{Format: fillRuleFormat, Version: 1, App: "PalaTerm " + appVersion, Rule: r}, "", "  ")
	if err != nil {
		return "", err
	}
	dir := fillRulesDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, bundle.FileNameSafe(r.Name)+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ImportFillRuleFile reads a rule file someone handed over (a dialog picks
// it) and adds it, renamed "(2)" when the name is taken. Returns the new
// rule's ID ("" when the dialog is cancelled).
func (a *App) ImportFillRuleFile() (string, error) {
	defer recoverBound(a.ctx, "ImportFillRuleFile")
	_ = os.MkdirAll(fillRulesDir(), 0o700)
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            a.word("変数ルールを読み込む", "Load a variable rule"),
		DefaultDirectory: dialogDir(fillRulesDir()),
		Filters: []runtime.FileFilter{
			{DisplayName: a.word("変数ルール (*.json)", "Variable rule (*.json)"), Pattern: "*.json"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	data, err := readRuleFile(path)
	if err != nil {
		return "", err
	}
	r, err := parseFillRule(data)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", errVaultLocked
	}
	r.ID = model.NewDeviceID()
	n := r.Name
	for i := 2; a.fillRuleNameTaken(n, ""); i++ {
		n = fmt.Sprintf("%s (%d)", r.Name, i)
	}
	r.Name = n
	a.inv.FillRules = append(a.inv.FillRules, r)
	a.mu.Unlock()
	return r.ID, a.persist()
}

func parseFillRule(data []byte) (model.FillRule, error) {
	var f fillRuleFile
	if err := json.Unmarshal(data, &f); err != nil || f.Format != fillRuleFormat {
		return model.FillRule{}, fmt.Errorf("変数ルールのファイルではありません")
	}
	r := f.Rule
	r.LastUsed, r.SampleName = "", ""
	if err := model.CheckFillRule(&r); err != nil {
		return model.FillRule{}, err
	}
	return r, nil
}

// fillRead is a list read with a rule: the devices and their rows, in the
// list's order, and what reading said.
type fillRead struct {
	Sheet    string
	Fill     *model.SetFill
	Rows     int
	Excluded int
	Missing  []string
	Warnings []string
	// lines are the rows as the editor shows them (the first few).
	lines []FillTestRow
}

// FillTestRow is one value row as a rule reads it.
type FillTestRow struct {
	Line   int               `json:"line"`
	Device string            `json:"device"`
	Values map[string]string `json:"values"`
	// Raw are the cells before the steps (the step editor starts from
	// them); RawDevice the device cell's.
	Raw       map[string]string `json:"raw"`
	RawDevice string            `json:"rawDevice"`
	// Cont: the row's device cell is empty; it is one more row of the
	// device above.
	Cont bool `json:"cont,omitempty"`
	// Unit is which device of the line (1 = the first; 2… = the rule's
	// Units); Excluded the condition that left the row out (its rows are
	// then not used).
	Unit     int               `json:"unit"`
	Excluded *model.ImportCond `json:"excluded,omitempty"`
}

// fillContMark stands in for an empty device cell while a list is read, so
// the reader judges the exclusions of a row continuing the device above
// too (it leaves a row with no name out before them).
const fillContMark = "\x00cont"

// readFill runs a rule over a book. A row whose device cell is empty is one
// more row of the device above (a list writes the name once for several
// lines); one with no value at all is a blank line and is skipped. A rule
// with units reads each line once per device: a line whose first device
// cell is empty continues every device of the line above; one whose
// further device cell is empty has no such device. A row meeting an
// exclusion is left out with the rows continuing it.
func readFill(b *sheet.Book, r *model.FillRule) (*fillRead, error) {
	ir := r.AsImportRule()
	sh, err := listimport.PickSheet(b, ir)
	if err != nil {
		return nil, err
	}
	t, err := listimport.ReadTable(sh, ir)
	if err != nil {
		return nil, err
	}
	out := &fillRead{Warnings: append(append([]string{}, b.Warnings...), t.Warnings...), Sheet: sh.Name}
	missing := map[string]bool{}
	var convs []*listimport.Converted
	for u := 0; u <= len(r.Units); u++ {
		ur := r.UnitRule(u)
		ur.Fields[0].Default = fillContMark
		c := listimport.Convert(t, ur, nil)
		for _, m := range c.Missing {
			missing[m] = true
		}
		convs = append(convs, c)
	}
	for m := range missing {
		out.Missing = append(out.Missing, m)
	}
	sort.Strings(out.Missing)
	f := &model.SetFill{RuleID: r.ID, Rule: r.Name, Devices: []model.FillDevice{}}
	for _, it := range r.Items {
		f.Items = append(f.Items, it.Name)
		if it.Empty == model.FillEmptySkip {
			f.Skip = append(f.Skip, it.Name)
		}
	}
	at := map[string]int{}
	cur := make([]int, len(convs))
	for u := range cur {
		cur[u] = -1
	}
	full := false
	for i := range convs[0].Rows {
		lineCont := slices.Contains(convs[0].Rows[i].Filled, "name")
		for u, c := range convs {
			row := c.Rows[i]
			vals, raw := map[string]string{}, map[string]string{}
			any := false
			for _, it := range r.Items {
				v := strings.TrimSpace(row.Values[model.FillItemKey(it.Name)])
				vals[it.Name] = v
				raw[it.Name] = row.Raw[model.FillItemKey(it.Name)]
				any = any || v != ""
			}
			cont := slices.Contains(row.Filled, "name")
			name := ""
			if !cont {
				name = strings.TrimSpace(row.Values["name"])
			}
			switch {
			case cont && (!any || cur[u] < 0 || (u > 0 && !lineCont)):
				// a blank line, nothing above to continue, or a line
				// without this device
				if u > 0 && !lineCont {
					cur[u] = -1
				}
				continue
			case row.Exclusion != nil:
				// The device's own row: its continuing rows go with it. A
				// continuing row: only itself.
				if !cont {
					cur[u] = -1
				}
			case !cont:
				k := model.FillKey(name)
				d, ok := at[k]
				if !ok {
					d = len(f.Devices)
					at[k] = d
					f.Devices = append(f.Devices, model.FillDevice{Name: name})
				}
				cur[u] = d
			}
			dev := name
			if cur[u] >= 0 {
				dev = f.Devices[cur[u]].Name
			}
			if len(out.lines) < 8+4*len(r.Units) {
				out.lines = append(out.lines, FillTestRow{Line: row.Line, Unit: u + 1, Device: dev, Values: vals, Raw: raw,
					RawDevice: row.Raw["name"], Cont: cont, Excluded: row.Exclusion})
			}
			if row.Exclusion != nil {
				out.Excluded++
				continue
			}
			if out.Rows == maxFillListRows {
				full = true
				break
			}
			out.Rows++
			f.Devices[cur[u]].Rows = append(f.Devices[cur[u]].Rows, vals)
		}
		if full {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%d 行を超える分は読みませんでした", maxFillListRows))
			break
		}
	}
	out.Fill = f
	return out, nil
}

// FillRuleTest is a rule tried on a sample file, for the rule editor.
type FillRuleTest struct {
	File     string              `json:"file"`
	Format   string              `json:"format"`
	Sheets   []string            `json:"sheets"`
	Sheet    string              `json:"sheet"`
	Grid     [][]string          `json:"grid"`
	Cols     int                 `json:"cols"`
	Headers  []listimport.Header `json:"headers"`
	Rows     []FillTestRow       `json:"rows"`
	Total    int                 `json:"total"`    // value rows read
	Excluded int                 `json:"excluded"` // rows an exclusion left out
	Devices  int                 `json:"devices"`  // devices those rows name
	Missing  []string            `json:"missing"`
	Warnings []string            `json:"warnings"`
	Error    string              `json:"error,omitempty"`
}

// TestFillRule tries a rule (as edited) on a sample file picked with
// PickListFile.
func (a *App) TestFillRule(path string, r model.FillRule) (*FillRuleTest, error) {
	defer recoverBound(a.ctx, "TestFillRule")
	b, _, _, _, err := a.readList(path, nil, nil, "")
	if err != nil {
		return nil, err
	}
	out := &FillRuleTest{File: filepath.Base(path), Format: b.Format, Sheets: []string{}, Grid: [][]string{}, Headers: []listimport.Header{}, Rows: []FillTestRow{}, Missing: []string{}, Warnings: append([]string{}, b.Warnings...)}
	for _, s := range b.Sheets {
		out.Sheets = append(out.Sheets, s.Name)
	}
	ir := r.AsImportRule()
	if ir.HeaderRow < 1 {
		ir.HeaderRow = 1
	}
	sh, err := listimport.PickSheet(b, ir)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.Sheet = sh.Name
	out.Grid, out.Cols = sampleGrid(listimport.FilledRows(sh, ir), ir.HeaderRow)
	t, err := listimport.ReadTable(sh, ir)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.Headers = t.Headers
	out.Warnings = append(out.Warnings, t.Warnings...)
	if r.Key.Source == "" {
		return out, nil
	}
	r.HeaderRow = ir.HeaderRow
	rd, err := readFill(b, &r)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.Rows, out.Total, out.Devices, out.Missing, out.Excluded = rd.lines, rd.Rows, len(rd.Fill.Devices), rd.Missing, rd.Excluded
	if out.Rows == nil {
		out.Rows = []FillTestRow{}
	}
	if out.Missing == nil {
		out.Missing = []string{}
	}
	return out, nil
}

// SuggestFillHeader guesses the header row of a sample file's first sheet
// (a new rule starts from it).
func (a *App) SuggestFillHeader(path string) (int, error) {
	defer recoverBound(a.ctx, "SuggestFillHeader")
	b, _, _, _, err := a.readList(path, nil, nil, "")
	if err != nil {
		return 0, err
	}
	if len(b.Sheets) == 0 {
		return 1, nil
	}
	return listimport.GuessHeaderRow(listimport.FilledRows(&b.Sheets[0], &model.ImportRule{HeaderRow: 1})), nil
}

// FillListRead is a list read with a rule before it is linked: what the
// link would hold.
type FillListRead struct {
	File    string   `json:"file"`
	Rule    string   `json:"rule"`
	Items   []string `json:"items"`
	Rows    int      `json:"rows"`
	Devices int      `json:"devices"`
	// Unknown are the list's devices no device of this PalaTerm is named
	// (the first few); Known how many are.
	Unknown  []string `json:"unknown"`
	Known    int      `json:"known"`
	Missing  []string `json:"missing"`
	Warnings []string `json:"warnings"`
}

func (a *App) fillRuleCopy(id string) (model.FillRule, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return model.FillRule{}, errVaultLocked
	}
	i := a.fillRuleIndex(id)
	if i < 0 {
		return model.FillRule{}, fmt.Errorf("変数ルールが見つかりません")
	}
	return a.inv.FillRules[i], nil
}

// readFillFile reads path with the rule. checked: path is the file the
// page picked (PickListFile), or one a link already holds.
func (a *App) readFillFile(path string, r *model.FillRule, picked bool) (*fillRead, error) {
	var b *sheet.Book
	var err error
	if picked {
		b, _, _, _, err = a.readList(path, nil, nil, "")
	} else {
		b, err = sheet.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	rd, err := readFill(b, r)
	if err != nil {
		return nil, err
	}
	a.matchFillNames(rd.Fill, r.Match)
	if len(rd.Fill.Devices) == 0 {
		return nil, fmt.Errorf("機器名の列「%s」に値のある行がありません（見出しの行・シートがルールと合っているか確かめてください）", r.Key.Column)
	}
	rd.Fill.File, rd.Fill.Path = filepath.Base(path), path
	rd.Fill.Read = time.Now().Format("2006-01-02 15:04")
	return rd, nil
}

// matchFillNames names each device of a read list as PalaTerm does when the
// rule's match settings (全角・半角, spaces, the domain part, "-" and "_")
// find it: the set then finds the device as it always does (case and the
// spaces around aside). Rows of list names that come to one device are
// joined in the list's order. A name matching none is kept as the list has
// it, as is one that would match two devices.
func (a *App) matchFillNames(f *model.SetFill, m model.ImportMatch) {
	m.IgnoreCase, m.KeyColumn = true, ""
	if !m.Width && !m.Space && !m.Domain && !m.Dash {
		return
	}
	a.mu.Lock()
	byNorm := map[string]string{}
	if a.inv != nil {
		for _, d := range a.inv.Devices {
			k := listimport.NormName(d.Name, m)
			if _, dup := byNorm[k]; dup {
				byNorm[k] = ""
			} else {
				byNorm[k] = d.Name
			}
		}
	}
	a.mu.Unlock()
	var out []model.FillDevice
	at := map[string]int{}
	for _, d := range f.Devices {
		if pal := byNorm[listimport.NormName(d.Name, m)]; pal != "" {
			d.Name = pal
		}
		k := model.FillKey(d.Name)
		if i, ok := at[k]; ok {
			out[i].Rows = append(out[i].Rows, d.Rows...)
			continue
		}
		at[k] = len(out)
		out = append(out, d)
	}
	f.Devices = out
}

// ReadFillList reads a picked list with a rule, for the dialog that links
// it: nothing is kept.
func (a *App) ReadFillList(path, ruleID string) (*FillListRead, error) {
	defer recoverBound(a.ctx, "ReadFillList")
	r, err := a.fillRuleCopy(ruleID)
	if err != nil {
		return nil, err
	}
	rd, err := a.readFillFile(path, &r, true)
	if err != nil {
		return nil, err
	}
	return a.fillListRead(rd, r.Name), nil
}

// fillListRead is what a read of a list holds, for the page.
func (a *App) fillListRead(rd *fillRead, rule string) *FillListRead {
	out := &FillListRead{File: rd.Fill.File, Rule: rule, Items: rd.Fill.Items, Rows: rd.Rows, Devices: len(rd.Fill.Devices),
		Unknown: []string{}, Missing: rd.Missing, Warnings: rd.Warnings}
	if out.Missing == nil {
		out.Missing = []string{}
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	a.mu.Lock()
	known := map[string]bool{}
	if a.inv != nil {
		for _, d := range a.inv.Devices {
			known[model.FillKey(d.Name)] = true
		}
	}
	a.mu.Unlock()
	for _, d := range rd.Fill.Devices {
		if known[model.FillKey(d.Name)] {
			out.Known++
		} else if len(out.Unknown) < 20 {
			out.Unknown = append(out.Unknown, d.Name)
		}
	}
	return out
}

// FillLinkReq links a picked list to a set: a new one (New, named Set) or
// an existing one.
type FillLinkReq struct {
	Path   string `json:"path"`
	RuleID string `json:"ruleId"`
	Set    string `json:"set"`
	New    bool   `json:"new"`
}

// LinkFillList reads the picked list with the rule and links it to the set.
func (a *App) LinkFillList(req FillLinkReq) error {
	defer recoverBound(a.ctx, "LinkFillList")
	r, err := a.fillRuleCopy(req.RuleID)
	if err != nil {
		return err
	}
	rd, err := a.readFillFile(req.Path, &r, true)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(req.Set)
	if req.New {
		cs := model.CommandSet{Name: name}
		if err := checkCommandSet(&cs); err != nil {
			return err
		}
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return errVaultLocked
	}
	snap := a.snapshotLocked()
	idx := -1
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == name {
			idx = i
		}
	}
	switch {
	case req.New && idx >= 0:
		a.mu.Unlock()
		return fmt.Errorf("「%s」は既にあります", name)
	case req.New:
		a.inv.CommandSets = append(a.inv.CommandSets, model.CommandSet{Name: name, Commands: []model.Command{}, Fill: rd.Fill})
	case idx < 0:
		a.mu.Unlock()
		return fmt.Errorf("コマンドセット「%s」が見つかりません", name)
	default:
		a.inv.CommandSets[idx].Fill = rd.Fill
	}
	if i := a.fillRuleIndex(r.ID); i >= 0 {
		a.inv.FillRules[i].LastUsed = listimport.Today()
	}
	a.mu.Unlock()
	return a.persistOr(snap, nil)
}

// RereadFillList reads a set's linked list again, from where it was, with
// its rule as it is now, and returns what it read (as ReadFillList does).
func (a *App) RereadFillList(set string) (*FillListRead, error) {
	defer recoverBound(a.ctx, "RereadFillList")
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return nil, errVaultLocked
	}
	var fill *model.SetFill
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == set {
			fill = a.inv.CommandSets[i].Fill
		}
	}
	a.mu.Unlock()
	if fill == nil {
		return nil, fmt.Errorf("このコマンドセットは一覧ファイルを読み込んでいません")
	}
	r, err := a.fillRuleCopy(fill.RuleID)
	if err != nil {
		return nil, fmt.Errorf("変数ルール「%s」が見つかりません（削除されました）。「別の一覧ファイルにする…」でルールを選び直してください", fill.Rule)
	}
	rd, err := a.readFillFile(fill.Path, &r, false)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return nil, errVaultLocked
	}
	snap := a.snapshotLocked()
	found := false
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == set {
			a.inv.CommandSets[i].Fill = rd.Fill
			found = true
		}
	}
	a.mu.Unlock()
	if !found {
		return nil, fmt.Errorf("コマンドセット「%s」が見つかりません", set)
	}
	if err := a.persistOr(snap, nil); err != nil {
		return nil, err
	}
	return a.fillListRead(rd, r.Name), nil
}

// UnlinkFillList drops a set's linked list and its values.
func (a *App) UnlinkFillList(set string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return errVaultLocked
	}
	snap := a.snapshotLocked()
	for i := range a.inv.CommandSets {
		if a.inv.CommandSets[i].Name == set {
			a.inv.CommandSets[i].Fill = nil
		}
	}
	a.mu.Unlock()
	return a.persistOr(snap, nil)
}

// checkSetFill refuses linked values a command line could not hold (a
// 複製's, sent back by the page).
func checkSetFill(f *model.SetFill) error {
	for _, d := range f.Devices {
		for _, row := range d.Rows {
			for _, v := range row {
				if err := model.CheckCommandText(v); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// FillPreview is what each device of a run is sent with its values in.
type FillPreview struct {
	Devices []FillDevicePreview `json:"devices"`
}

// FillDevicePreview is one device's: Fill false for a set that fills in
// nothing (it runs as always); Error says why one that does cannot run.
type FillDevicePreview struct {
	Device   string   `json:"device"`
	Set      string   `json:"set"`
	Fill     bool     `json:"fill"`
	File     string   `json:"file,omitempty"`
	Rows     int      `json:"rows,omitempty"`
	Values   []string `json:"values,omitempty"` // "名前=値" of the first row
	Commands []string `json:"commands,omitempty"`
	Error    string   `json:"error,omitempty"`
	// Notes are what the user should know of a device that does run: a
	// {{name?}} the list has no such name for, rows that are not used.
	Notes []string `json:"notes,omitempty"`
}

// PreviewFill fills in each device of a run request.
func (a *App) PreviewFill(req RunRequest) (*FillPreview, error) {
	defer recoverBound(a.ctx, "PreviewFill")
	req.DryRun = false
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return nil, errVaultLocked
	}
	snap, err := a.runSnapshot(req)
	if err != nil {
		return nil, err
	}
	p := &FillPreview{Devices: []FillDevicePreview{}}
	for _, d := range snap.Devices {
		p.Devices = append(p.Devices, fillDevice(setNamed(snap, d.CommandSet), d.Name))
	}
	return p, nil
}

// PreviewFillSet fills in commands (the set editor's, unsaved) with the
// values linked to the set for one device.
func (a *App) PreviewFillSet(set string, cmds []model.Command, device string) (*FillDevicePreview, error) {
	defer recoverBound(a.ctx, "PreviewFillSet")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return nil, errVaultLocked
	}
	cs := model.CommandSet{Name: set, Commands: cmds}
	if cur := setNamed(a.inv, set); cur != nil {
		cs.Fill = cur.Fill
	}
	if err := model.CheckFill(cmds); err != nil {
		return &FillDevicePreview{Device: device, Set: set, Fill: true, Error: err.Error()}, nil
	}
	fd := fillDevice(&cs, device)
	return &fd, nil
}

func fillDevice(cs *model.CommandSet, device string) FillDevicePreview {
	fd := FillDevicePreview{Device: device}
	if cs == nil || !model.HasFill(cs.Commands) {
		if cs != nil {
			fd.Set = cs.Name
		}
		return fd
	}
	fd.Set, fd.Fill = cs.Name, true
	if cs.Fill == nil {
		fd.Error = "一覧ファイルを読み込んでいません"
		return fd
	}
	fd.File = cs.Fill.File
	rows := cs.Fill.RowsFor(device)
	fd.Rows = len(rows)
	if rows == nil {
		fd.Error = "一覧ファイルにこの機器の行がありません"
		return fd
	}
	for _, n := range cs.Fill.Items {
		fd.Values = append(fd.Values, n+"="+rows[0][n])
	}
	// A name the list does not have is most often a slip of the hand: said
	// by name, not as an empty value. Written {{name?}} its line is left out
	// as it always was, with a note.
	var unknown, left []string
	for _, n := range model.FillNames(cs.Commands) {
		if fillHasItem(cs.Fill, n.Name) {
			continue
		}
		if n.Optional {
			left = append(left, n.Name)
		} else {
			unknown = append(unknown, n.Name)
		}
	}
	if len(unknown) > 0 {
		fd.Error = fmt.Sprintf("「%s」という変数はありません（読み込んだ変数: %s）", strings.Join(unknown, "」「"), strings.Join(cs.Fill.Items, "、"))
		return fd
	}
	if len(left) > 0 {
		fd.Notes = append(fd.Notes, fmt.Sprintf("「%s」という変数はありません（その行は送りません）", strings.Join(left, "」「")))
	}
	if len(rows) > 1 && !model.HasFillEach(cs.Commands) {
		fd.Notes = append(fd.Notes, fmt.Sprintf("一覧ファイルにこの機器の行が %d 行あります（1 行目の値だけ使います）", len(rows)))
	}
	cmds, err := expandFor(cs, rows)
	if err != nil {
		fd.Error = err.Error()
		return fd
	}
	for _, c := range cmds {
		fd.Commands = append(fd.Commands, c.Text)
	}
	return fd
}

// fillHasItem reports whether the list has a value by that name (whatever
// its case, as ExpandFill reads it).
func fillHasItem(f *model.SetFill, name string) bool {
	for _, it := range f.Items {
		if strings.EqualFold(it, name) {
			return true
		}
	}
	return false
}

func setNamed(inv *model.Inventory, name string) *model.CommandSet {
	for i := range inv.CommandSets {
		if inv.CommandSets[i].Name == name {
			return &inv.CommandSets[i]
		}
	}
	return nil
}

// expandFor is the set with the device's rows filled in, checked as any
// command line is.
func expandFor(cs *model.CommandSet, rows []map[string]string) ([]model.Command, error) {
	cmds, err := model.ExpandFill(cs.Commands, rows, cs.Fill.SkipSet())
	if err != nil {
		return nil, err
	}
	if err := model.CheckCommands(cmds); err != nil {
		return nil, err
	}
	return cmds, nil
}

// fillSets are the request's devices whose set fills in values, each with
// its own set filled in (runner.Options.DeviceSets), and an error naming
// those that cannot be (no list, no row, an empty value): a run refuses
// them (the run tab leaves them out first); a 予約 runs the others. A dry
// run sends no commands and needs none.
func fillSets(snap *model.Inventory, req RunRequest) (map[string]*model.CommandSet, error) {
	if req.DryRun {
		return nil, nil
	}
	var out map[string]*model.CommandSet
	var bad []string
	for _, d := range snap.Devices {
		cs := setNamed(snap, d.CommandSet)
		if cs == nil || !model.HasFill(cs.Commands) {
			continue
		}
		fd := fillDevice(cs, d.Name)
		if fd.Error != "" {
			bad = append(bad, fmt.Sprintf("%s（%s）", d.Name, fd.Error))
			continue
		}
		cmds, _ := expandFor(cs, cs.Fill.RowsFor(d.Name))
		if out == nil {
			out = map[string]*model.CommandSet{}
		}
		filled := *cs
		filled.Commands = cmds
		filled.Fill = nil
		out[d.Name] = &filled
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		if len(bad) > 5 {
			bad = append(bad[:5], fmt.Sprintf("ほか %d 台", len(bad)-5))
		}
		return out, fmt.Errorf("変数を入れられない機器があります: %s", strings.Join(bad, "、"))
	}
	return out, nil
}

// fillSetsErr refuses a 予約 whose devices run a set that fills in values.
func fillSetsErr(snap *model.Inventory) error {
	var names []string
	seen := map[string]bool{}
	for _, d := range snap.Devices {
		if seen[d.CommandSet] {
			continue
		}
		seen[d.CommandSet] = true
		if cs := setNamed(snap, d.CommandSet); cs != nil && model.HasFill(cs.Commands) {
			names = append(names, "「"+cs.Name+"」")
		}
	}
	if len(names) == 0 {
		return nil
	}
	return fmt.Errorf("変数（{{…}}）のあるコマンドセットは予約では実行できません: %s", strings.Join(names, "、"))
}
