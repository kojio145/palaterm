package main

// 一覧の取り込み: the App side of package listimport — the rules in the
// vault, trying a rule on a sample file in the editor, the preview and the
// import itself. Reading a file happens outside the lock (an Excel file of
// thousands of rows takes a moment); the plan is worked out again under
// the lock when importing, from the same file and the user's choices, so
// what is written is what the inventory is at that moment.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kojio145/palaterm/internal/bundle"
	"github.com/kojio145/palaterm/internal/guard"
	"github.com/kojio145/palaterm/internal/listimport"
	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ImportFieldInfo is one device field a rule can fill, for the editor.
type ImportFieldInfo struct {
	Key  string `json:"key"`
	Ja   string `json:"ja"`
	Kind string `json:"kind"`
	Hop  int    `json:"hop,omitempty"`
}

// ListImportFields lists the fields a rule can fill.
func (a *App) ListImportFields() []ImportFieldInfo {
	var out []ImportFieldInfo
	for _, c := range listimport.Fields() {
		out = append(out, ImportFieldInfo{Key: c.Key, Ja: c.Ja, Kind: c.Kind, Hop: c.Hop})
	}
	return out
}

func importRulesDir() string { return filepath.Join(exeDir(), "export", "import-rules") }

func (a *App) ruleIndex(id string) int {
	for i := range a.inv.ImportRules {
		if a.inv.ImportRules[i].ID == id {
			return i
		}
	}
	return -1
}

func (a *App) ruleNameTaken(name, exceptID string) bool {
	for _, r := range a.inv.ImportRules {
		if strings.EqualFold(strings.TrimSpace(r.Name), strings.TrimSpace(name)) && r.ID != exceptID {
			return true
		}
	}
	return false
}

// SaveImportRule adds (no ID) or replaces a rule and returns its ID.
func (a *App) SaveImportRule(r model.ImportRule) (string, error) {
	if err := listimport.ValidateRule(&r); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	if a.ruleNameTaken(r.Name, r.ID) {
		a.mu.Unlock()
		return "", fmt.Errorf("同じ名前のルール「%s」があります", r.Name)
	}
	if i := a.ruleIndex(r.ID); r.ID != "" && i >= 0 {
		r.LastUsed = a.inv.ImportRules[i].LastUsed
		a.inv.ImportRules[i] = r
	} else {
		r.ID = model.NewDeviceID()
		a.inv.ImportRules = append(a.inv.ImportRules, r)
	}
	a.mu.Unlock()
	return r.ID, a.persist()
}

// ReorderImportRules puts the rules in the order of their IDs (the list's
// drag to reorder, as the command sets and OS types have).
func (a *App) ReorderImportRules(order []string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	reorderSlots(a.inv.ImportRules, func(r model.ImportRule) string { return r.ID }, order)
	a.mu.Unlock()
	return a.persist()
}

// DeleteImportRule removes a rule.
func (a *App) DeleteImportRule(id string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	i := a.ruleIndex(id)
	if i < 0 {
		a.mu.Unlock()
		return fmt.Errorf("ルールが見つかりません")
	}
	a.inv.ImportRules = append(a.inv.ImportRules[:i], a.inv.ImportRules[i+1:]...)
	a.mu.Unlock()
	return a.persist()
}

// uniqueRuleName is name, or name (2), (3)… when a rule already has it.
func (a *App) uniqueRuleName(name string) string {
	n := name
	for i := 2; a.ruleNameTaken(n, ""); i++ {
		n = fmt.Sprintf("%s (%d)", name, i)
	}
	return n
}

// ExportImportRule writes a rule to export\import-rules\<名前>.json (the
// defaults of password fields left out) and returns the file.
func (a *App) ExportImportRule(id string) (string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	i := a.ruleIndex(id)
	if i < 0 {
		a.mu.Unlock()
		return "", fmt.Errorf("ルールが見つかりません")
	}
	r := a.inv.ImportRules[i]
	a.mu.Unlock()
	data, err := listimport.ExportRule(r, "PalaTerm "+appVersion)
	if err != nil {
		return "", err
	}
	dir := importRulesDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, bundle.FileNameSafe(r.Name)+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ImportImportRuleFile reads a rule file someone handed over (a dialog
// picks it) and adds it — renamed "(2)" when the name is taken. Returns the
// new rule's ID ("" when the dialog is cancelled).
func (a *App) ImportImportRuleFile() (string, error) {
	defer recoverBound(a.ctx, "ImportImportRuleFile")
	_ = os.MkdirAll(importRulesDir(), 0o700)
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            a.word("変換ルールを読み込む", "Load an import rule"),
		DefaultDirectory: importRulesDir(),
		Filters: []runtime.FileFilter{
			{DisplayName: a.word("変換ルール (*.json)", "Import rule (*.json)"), Pattern: "*.json"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > 4<<20 {
		return "", fmt.Errorf("変換ルールのファイルではありません")
	}
	r, err := listimport.ParseRule(data)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return "", fmt.Errorf("vault is locked")
	}
	r.ID = model.NewDeviceID()
	r.Name = a.uniqueRuleName(r.Name)
	a.inv.ImportRules = append(a.inv.ImportRules, r)
	a.mu.Unlock()
	return r.ID, a.persist()
}

// PickListFile asks for a device list to import or to try a rule on.
func (a *App) PickListFile() (string, error) {
	defer recoverBound(a.ctx, "PickListFile")
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: a.word("機器一覧のファイルを選ぶ", "Choose a device list file"),
		Filters: []runtime.FileFilter{
			{DisplayName: a.word("機器一覧（Excel・CSV など）", "Device list (Excel, CSV, …)"), Pattern: "*.xls;*.xlsx;*.xlsm;*.csv;*.tsv;*.txt;*.htm;*.html;*.xml"},
			{DisplayName: a.word("すべてのファイル", "All files"), Pattern: "*.*"},
		},
	})
}

// bookCache keeps the last file read: the rule editor tries the rule on
// the same sample at every change, and the import screen asks again at
// every choice — the rule's result is kept with it (rule = the rule it
// was run with), so a choice only matches the rows again.
var bookCache struct {
	sync.Mutex
	path  string
	stamp string
	book  *sheet.Book
	rule  string
	table *listimport.Table
	conv  *listimport.Converted
	// gen counts the forgets: a read that began before one is not kept
	// (the screen was left, or the vault locked, while it read).
	gen uint64
}

// forgetBook drops the file kept: at a lock, and when the import screen is
// left — a list read for an import often holds every password.
func forgetBook() {
	bookCache.Lock()
	bookCache.gen++
	bookCache.path, bookCache.stamp, bookCache.book = "", "", nil
	bookCache.rule, bookCache.table, bookCache.conv = "", nil, nil
	bookCache.Unlock()
}

// listReadHook runs between reading a list and keeping it (tests only).
var listReadHook func()

// ForgetListFile is forgetBook for the page (the import screen was left).
func (a *App) ForgetListFile() { forgetBook() }

// fileStamp tells one version of a file from the next (size and time).
func fileStamp(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("ファイルを開けません: %v", err)
	}
	return fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano()), nil
}

// readList reads the file (the one kept, while it has not changed) and,
// given a rule, runs it. The reading and the converting happen outside the
// cache's lock, so a lock (which forgets the cache) never waits for a big
// file. What is read is kept only while the vault is open, checked under
// the cache's lock: Lock closes the vault, then forgets the cache, so a
// read finishing around a lock is either not kept or forgotten.
func (a *App) readList(path string, r *model.ImportRule, env *listimport.Env, envSig string) (*sheet.Book, string, *listimport.Table, *listimport.Converted, error) {
	if path == "" {
		return nil, "", nil, nil, fmt.Errorf("ファイルが選ばれていません")
	}
	stamp, err := fileStamp(path)
	if err != nil {
		return nil, "", nil, nil, err
	}
	sig := ""
	if r != nil {
		sig = ruleSig(r) + envSig
	}
	bookCache.Lock()
	if a.locked() {
		bookCache.Unlock()
		return nil, "", nil, nil, fmt.Errorf("vault is locked")
	}
	gen := bookCache.gen
	var b *sheet.Book
	if bookCache.book != nil && bookCache.path == path && bookCache.stamp == stamp {
		b = bookCache.book
		if r == nil {
			bookCache.Unlock()
			return b, stamp, nil, nil, nil
		}
		if bookCache.conv != nil && bookCache.rule == sig {
			t, conv := bookCache.table, bookCache.conv
			bookCache.Unlock()
			return b, stamp, t, conv, nil
		}
	}
	bookCache.Unlock()

	fresh := b == nil
	if fresh {
		if b, err = sheet.ReadFile(path); err != nil {
			return nil, "", nil, nil, err
		}
	}
	var t *listimport.Table
	var conv *listimport.Converted
	if r != nil {
		sh, err := listimport.PickSheet(b, r)
		if err != nil {
			return nil, "", nil, nil, err
		}
		if t, err = listimport.ReadTable(sh, r); err != nil {
			return nil, "", nil, nil, err
		}
		conv = listimport.Convert(t, r, env)
	}
	if listReadHook != nil {
		listReadHook()
	}

	bookCache.Lock()
	defer bookCache.Unlock()
	if a.locked() {
		return nil, "", nil, nil, fmt.Errorf("vault is locked")
	}
	if bookCache.gen != gen {
		// Forgotten while reading (the screen left): answered, not kept.
		return b, stamp, t, conv, nil
	}
	if fresh || bookCache.path != path || bookCache.stamp != stamp {
		bookCache.path, bookCache.stamp, bookCache.book = path, stamp, b
		bookCache.rule, bookCache.table, bookCache.conv = "", nil, nil
	}
	if r != nil {
		bookCache.rule, bookCache.table, bookCache.conv = sig, t, conv
	}
	return b, stamp, t, conv, nil
}

// ruleSig names a rule's content (what the import reads by), for telling
// whether the rule changed since a preview.
func ruleSig(r *model.ImportRule) string {
	c := *r
	c.LastUsed, c.SampleName = "", ""
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// RuleTestRow is one value row as the rule turns it, for the editor.
type RuleTestRow struct {
	Line     int               `json:"line"`
	Unit     int               `json:"unit"`
	Label    string            `json:"label,omitempty"`
	Values   map[string]string `json:"values"`
	Raw      map[string]string `json:"raw"`
	Filled   []string          `json:"filled"`
	From     map[string]string `json:"from,omitempty"`
	Guessed  []string          `json:"guessed,omitempty"`
	Excluded string            `json:"excluded,omitempty"`
	// Exclusion is the condition behind Excluded (the page words it).
	Exclusion *model.ImportCond `json:"exclusion,omitempty"`
}

// RuleTest is a rule tried on a sample file: the top of the sheet as it
// is, the table's headers, and the first rows converted.
type RuleTest struct {
	File     string              `json:"file"`
	Format   string              `json:"format"`
	Sheets   []string            `json:"sheets"`
	Sheet    string              `json:"sheet"`
	Grid     [][]string          `json:"grid"` // the sheet's top-left corner, merges filled per the rule
	Cols     int                 `json:"cols"` // the columns those rows reach (more than the grid's when it is cut)
	Headers  []listimport.Header `json:"headers"`
	Rows     []RuleTestRow       `json:"rows"`
	Total    int                 `json:"total"`    // value rows
	Excluded int                 `json:"excluded"` // of which left out
	Missing  []string            `json:"missing"`
	Warnings []string            `json:"warnings"`
	Error    string              `json:"error,omitempty"` // the rule cannot read the sheet (the grid still shows)
}

// TestImportRule tries a rule (as edited, unsaved) on a sample file.
func (a *App) TestImportRule(path string, r model.ImportRule) (*RuleTest, error) {
	defer recoverBound(a.ctx, "TestImportRule")
	b, _, _, _, err := a.readList(path, nil, nil, "")
	if err != nil {
		return nil, err
	}
	out := &RuleTest{File: filepath.Base(path), Format: b.Format, Sheets: []string{}, Grid: [][]string{}, Headers: []listimport.Header{}, Rows: []RuleTestRow{}, Missing: []string{}, Warnings: append([]string{}, b.Warnings...)}
	for _, s := range b.Sheets {
		out.Sheets = append(out.Sheets, s.Name)
	}
	sh, err := listimport.PickSheet(b, &r)
	if err != nil {
		out.Error = err.Error() // no grid: nothing of the file shows
		return out, nil
	}
	out.Sheet = sh.Name
	out.Grid, out.Cols = sampleGrid(listimport.FilledRows(sh, &r), r.HeaderRow)
	t, err := listimport.ReadTable(sh, &r)
	if err != nil {
		// The sample still shows: its password cells masked first.
		listimport.MaskSample(out.Grid, nil, &r)
		out.Error = err.Error()
		return out, nil
	}
	out.Headers = t.Headers
	out.Warnings = append(out.Warnings, t.Warnings...)
	// The sample's password cells show masked, as everywhere else: under the
	// header row, the columns a password field reads (and where the header
	// texts it reads stand in the sample, under them).
	listimport.MaskSample(out.Grid, t, &r)
	if _, ok := hasName(&r); !ok {
		return out, nil // nothing mapped yet: the headers are what the editor needs
	}
	env, _ := a.importEnv(listimport.RuleGuesses(&r))
	conv := listimport.Convert(t, &r, env)
	out.Missing = conv.Missing
	out.Total = len(conv.Rows)
	lines := map[int]bool{}
	for _, row := range conv.Rows {
		if row.Excluded != "" {
			out.Excluded++
		}
		// The first 8 lines, each with all its devices (a pair is never
		// shown cut in two).
		if lines[row.Line] || len(lines) < 8 {
			lines[row.Line] = true
			out.Rows = append(out.Rows, RuleTestRow{Line: row.Line, Unit: row.Unit, Label: row.Label, Values: row.Values, Raw: row.Raw, Filled: row.Filled, From: row.From, Guessed: row.Guessed, Excluded: row.Excluded, Exclusion: row.Exclusion})
		}
	}
	return out, nil
}

func hasName(r *model.ImportRule) (model.ImportField, bool) {
	for _, f := range r.Fields {
		if f.Key == "name" && (f.Source != "" || f.Default != "") {
			return f, true
		}
	}
	return model.ImportField{}, false
}

// sampleGrid is the sheet's top-left corner for the editor's picture of
// it: enough rows to show the header and a few values, long cells cut —
// and how many columns those rows reach (the editor says when it cut some).
func sampleGrid(rows [][]string, headerRow int) ([][]string, int) {
	nr := max(12, headerRow+6)
	nr = min(nr, 40, len(rows))
	nc := 0
	for r := 0; r < nr; r++ {
		nc = max(nc, len(rows[r]))
	}
	// 300 columns: past IV (256, as wide as an .xls goes), so an .xls shows
	// whole; a wider xlsx or CSV is cut and the editor says so.
	cols := nc
	nc = min(nc, 300)
	out := make([][]string, nr)
	for r := 0; r < nr; r++ {
		out[r] = make([]string, nc)
		for c := 0; c < nc && c < len(rows[r]); c++ {
			v := strings.ReplaceAll(rows[r][c], "\n", " ")
			if rs := []rune(v); len(rs) > 40 {
				v = string(rs[:40]) + "…"
			}
			out[r][c] = v
		}
	}
	return out, cols
}

// TraceImportSteps runs steps over a sample value, keeping each step's
// result (the step editor shows them as the user types).
func (a *App) TraceImportSteps(v string, steps []model.ImportStep) []string {
	defer recoverBound(a.ctx, "TraceImportSteps")
	env, _ := a.importEnv(listimport.HasGuess(steps))
	return listimport.TraceSteps(v, steps, env)
}

// errListChanged: the file or the rule changed between the preview and
// the import (the page shows the new preview).
const errListChanged = "ファイルまたは変換ルールがプレビューのあとで変わりました。新しいプレビューを確かめてから取り込んでください"

// ListImportReq is one import, previewed or applied.
type ListImportReq struct {
	Path     string `json:"path"`
	RuleID   string `json:"ruleId"`
	Group    string `json:"group"`
	NewGroup bool   `json:"newGroup"`
	// Decisions are the user's choices in the preview, by row number.
	Decisions map[int]listimport.Decision `json:"decisions"`
	// DeleteIDs are the group's unmatched devices to delete.
	DeleteIDs []string `json:"deleteIds"`
	// Stamp is the preview's (ListPreview.Stamp): the import refuses a
	// file or rule changed since, whose rows the choices no longer fit.
	Stamp string `json:"stamp"`
}

// ListPreview is what the import screen shows.
type ListPreview struct {
	File     string           `json:"file"`
	Format   string           `json:"format"`
	Sheet    string           `json:"sheet"`
	Rule     string           `json:"rule"`
	Warnings []string         `json:"warnings"`
	Missing  []string         `json:"missing"` // columns the rule names that the file lacks
	Plan     *listimport.Plan `json:"plan"`
	// Stamp names the file's version and the rule's content previewed.
	Stamp string `json:"stamp"`
}

// readForImport reads the file and runs the rule (outside the lock), and
// names the two (the preview's stamp).
func (a *App) readForImport(req ListImportReq) (*model.ImportRule, *sheet.Book, *listimport.Table, *listimport.Converted, string, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return nil, nil, nil, nil, "", fmt.Errorf("vault is locked")
	}
	i := a.ruleIndex(req.RuleID)
	if i < 0 {
		a.mu.Unlock()
		return nil, nil, nil, nil, "", fmt.Errorf("変換ルールを選んでください")
	}
	r := a.inv.ImportRules[i]
	a.mu.Unlock()
	env, esig := a.importEnv(listimport.RuleGuesses(&r))
	b, stamp, t, conv, err := a.readList(req.Path, &r, env, esig)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	return &r, b, t, conv, stamp + ":" + ruleSig(&r) + ":" + esig, nil
}

// importGroup checks the group the import goes to (caller holds a.mu).
func (a *App) importGroup(req *ListImportReq) error {
	req.Group = strings.TrimSpace(req.Group)
	if req.Group == "" {
		return fmt.Errorf("取り込み先のグループを入力してください")
	}
	exists := a.inv.Group(req.Group) != nil
	if req.NewGroup && exists {
		return fmt.Errorf("グループ「%s」は既にあります。「既存のグループへ取り込む」で選んでください", req.Group)
	}
	if !req.NewGroup && !exists {
		return fmt.Errorf("グループ「%s」が見つかりません", req.Group)
	}
	return nil
}

func maskSecret(v string) string {
	if v == "" {
		return ""
	}
	return "●●●●●●"
}

// PreviewListImport works out what the import would do; nothing changes.
func (a *App) PreviewListImport(req ListImportReq) (*ListPreview, error) {
	r, b, t, conv, stamp, err := a.readForImport(req)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("vault is locked")
	}
	if err := a.importGroup(&req); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	p := listimport.BuildPlan(a.inv, conv, r, listimport.Options{Group: req.Group, Decisions: req.Decisions})
	a.mu.Unlock()
	// Passwords never reach the page: "changed" is told by the state.
	for i := range p.Rows {
		for j := range p.Rows[i].Cells {
			if c := &p.Rows[i].Cells[j]; c.Secret {
				c.File, c.Cur = maskSecret(c.File), maskSecret(c.Cur)
			}
		}
	}
	return &ListPreview{File: filepath.Base(req.Path), Format: b.Format, Sheet: t.Sheet, Rule: r.Name,
		Warnings: append(append([]string{}, b.Warnings...), t.Warnings...), Missing: conv.Missing, Plan: p, Stamp: stamp}, nil
}

// ApplyListImport imports the file as previewed with the user's choices.
func (a *App) ApplyListImport(req ListImportReq) (*listimport.Result, error) {
	r, _, _, conv, stamp, err := a.readForImport(req)
	if err != nil {
		return nil, err
	}
	// The choices are by row number: on a file exported again since the
	// preview (or a rule edited since), row 12 may be another device.
	if req.Stamp != stamp {
		return nil, fmt.Errorf("%s", errListChanged)
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("vault is locked")
	}
	if err := a.importGroup(&req); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	p := listimport.BuildPlan(a.inv, conv, r, listimport.Options{Group: req.Group, Decisions: req.Decisions})
	if req.NewGroup {
		a.ensureGroupExists(req.Group)
	}
	before := append([]model.Device(nil), a.inv.Devices...) // Apply changes them in place
	res := listimport.Apply(a.inv, p, req.Group, req.DeleteIDs)
	// A device deleted by the import (or moved out of its group): the
	// schedules naming it stop, against the names as they were — before
	// the renames below are followed.
	stopped := stopLeaving(a.inv, before, a.inv.Devices)
	// A device renamed by the import stays in the group's schedules.
	if len(res.Renames) > 0 {
		renameSchedDevices(a.inv, req.Group, res.Renames)
	}
	if i := a.ruleIndex(r.ID); i >= 0 {
		a.inv.ImportRules[i].LastUsed = listimport.Today()
		a.inv.ImportRules[i].SampleName = filepath.Base(req.Path)
	}
	a.mu.Unlock()
	a.logStopped(stopped)
	if err := a.persist(); err != nil {
		return nil, err
	}
	return &res, nil
}

// SuggestImportRule guesses a new rule from a sample file: the header row
// and the usual fields' columns by their header names (the editor fills
// them in marked 仮 for the user to check).
func (a *App) SuggestImportRule(path string, r model.ImportRule) (*listimport.Suggestion, error) {
	defer recoverBound(a.ctx, "SuggestImportRule")
	b, _, _, _, err := a.readList(path, nil, nil, "")
	if err != nil {
		return nil, err
	}
	sh, err := listimport.PickSheet(b, &r)
	if err != nil {
		return nil, err
	}
	sg := listimport.Suggest(sh, r)
	return &sg, nil
}

// setSafety remembers guard.Check's verdict per command set (by its name
// and commands): the rule editor asks for the environment at every key.
var setSafety struct {
	sync.Mutex
	m map[string]bool
}

func setSafe(name string, texts []string) bool {
	key := name + "\x00" + strings.Join(texts, "\n")
	setSafety.Lock()
	defer setSafety.Unlock()
	if v, ok := setSafety.m[key]; ok {
		return v
	}
	if setSafety.m == nil || len(setSafety.m) > 1000 {
		setSafety.m = map[string]bool{}
	}
	v := len(guard.Check(texts)) == 0
	setSafety.m[key] = v
	return v
}

// importEnv is what the guessing steps choose from: this PalaTerm's OS
// types and command sets, each set marked unsafe when a command in it
// changes a device (guard.Check) — such a set is never guessed. The
// signature changes with them (a preview's stamp then no longer fits).
// Only a rule that guesses needs it (need false: nil, "").
func (a *App) importEnv(need bool) (*listimport.Env, string) {
	if !need {
		return nil, ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return nil, ""
	}
	env := &listimport.Env{}
	var sb strings.Builder
	for _, p := range a.inv.CustomProfiles {
		env.Profiles = append(env.Profiles, listimport.EnvProfile{Key: p.Key, Name: p.Name})
		sb.WriteString(p.Key + "\x00" + p.Name + "\x01")
	}
	for _, cs := range a.inv.CommandSets {
		var texts []string
		for _, c := range cs.Commands {
			texts = append(texts, c.Text)
		}
		safe := setSafe(cs.Name, texts)
		env.Sets = append(env.Sets, listimport.EnvSet{Name: cs.Name, Safe: safe})
		fmt.Fprintf(&sb, "%s\x00%v\x01", cs.Name, safe)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return env, hex.EncodeToString(sum[:6])
}
