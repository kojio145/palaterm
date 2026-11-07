package listimport

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kojio145/palaterm/internal/csvio"
	"github.com/kojio145/palaterm/internal/model"
	"golang.org/x/text/unicode/norm"
)

// The columns, built once: a preview asks about them for every cell of
// every row.
var (
	gridCols  = csvio.GridColumns()
	gridByKey = func() map[string]csvio.GridCol {
		m := make(map[string]csvio.GridCol, len(gridCols))
		for _, c := range gridCols {
			m[c.Key] = c
		}
		return m
	}()
)

// Fields are the device fields a rule can fill: every grid column but the
// group (chosen when importing).
func Fields() []csvio.GridCol {
	var out []csvio.GridCol
	for _, c := range gridCols {
		if c.Key != "group" {
			out = append(out, c)
		}
	}
	return out
}

// FieldLabel is a field's Japanese name (an unknown key: itself).
func FieldLabel(key string) string {
	if c, ok := gridByKey[key]; ok {
		return c.Ja
	}
	return key
}

// IsField reports whether key is a field a rule can fill.
func IsField(key string) bool {
	_, ok := gridByKey[key]
	return ok && key != "group"
}

func isSecret(key string) bool { return gridByKey[key].Kind == "secret" }

// Cell is one field of a row in the preview: the file's value, the
// device's current one, and what the import does with it.
//
//	same  equal (nothing to do)
//	take  the source changed it, PalaTerm did not: the file's value
//	keep  PalaTerm changed it, the source did not: kept
//	ask   both changed: Choice decides
//	nobase no record of the last import (a device made by hand, or a
//	      column added to the rule) and the two differ: Choice decides,
//	      kept unless chosen (it is not "both changed": nothing says who
//	      changed it)
//	fill  no record of the last import, PalaTerm's is empty and the file
//	      has a value (a column added to the rule after the last import, a
//	      device made by hand): the file's value — the column was mapped
//	      for that, and nothing of PalaTerm's is lost. A yes/no is never
//	      "empty" (an empty cell is its "no"), so it is asked as before.
//	guess the value is a guess from this PalaTerm's OS types or command
//	      sets: an existing device keeps its own unless chosen otherwise
//	lost  the file has no column for it (renamed or gone at the source):
//	      the value is not the source's, an existing device keeps its own
//	      (no choice: the page says so above the preview)
//	new   a new device
type Cell struct {
	Key    string `json:"key"`
	File   string `json:"file"`
	Cur    string `json:"cur"`
	State  string `json:"state"`
	Choice string `json:"choice"` // "file" | "keep": what is written
	Secret bool   `json:"secret,omitempty"`
	Filled bool   `json:"filled,omitempty"`
	// Guessed: a guessing step gave the file's value (shown as 推測).
	Guessed bool `json:"guessed,omitempty"`
}

// Cand is an existing device of the group, as the preview names it.
type Cand struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
}

// PlanRow is one row of the preview.
//
// Status: add, upd, same, ask, err, skip. MatchBy: key (the key it was
// imported under), name, norm (the name ignoring the rule's differences),
// ip (only the address matched: confirm), pick (chosen by hand).
type PlanRow struct {
	Line int `json:"line"`
	// ID names the device of the line (Row.ID): choices go by it. Unit and
	// Label tell a further device of a line (a redundant pair's backup).
	ID         int      `json:"id"`
	Unit       int      `json:"unit"`
	Label      string   `json:"label,omitempty"`
	Status     string   `json:"status"`
	Name       string   `json:"name"`
	Host       string   `json:"host"`
	Site       string   `json:"site,omitempty"`
	Link       string   `json:"link"` // "new", "skip" or the device ID written to
	MatchBy    string   `json:"matchBy,omitempty"`
	MatchName  string   `json:"matchName,omitempty"`
	Candidates []Cand   `json:"candidates"`
	Cells      []Cell   `json:"cells"`
	Note       string   `json:"note,omitempty"`
	Errors     []string `json:"errors"`
	// Exclusion is the condition that left the row out (the page words it).
	Exclusion *model.ImportCond `json:"exclusion,omitempty"`
	// RuleSkip: the rule leaves the row out (an exclusion or no name) —
	// nothing to choose for it, unlike a row set to skip in the preview.
	RuleSkip bool `json:"ruleSkip,omitempty"`
	// Changes: importing the row changes a device (a new one, or a value
	// taken from the file); a matched row without changes only records
	// the import.
	Changes bool `json:"changes"`

	dev *model.Device       // what is written (rows imported only)
	rec *model.ImportRecord // the record written with it
}

// Plan is the preview of an import: nothing is changed until Apply.
type Plan struct {
	Rows   []PlanRow      `json:"rows"`
	Counts map[string]int `json:"counts"`
	// Missing are the group's devices no row of the file matched: kept
	// unless the import is told to delete them.
	Missing []Cand `json:"missing"`
	// Devices are every device of the group (for linking a row by hand).
	Devices []Cand `json:"devices"`
}

// Decision is the user's choice for one device of a row (by Row.ID) in the preview: Link "new",
// "skip" or a device ID ("" = as proposed), and per field "file" / "keep".
type Decision struct {
	Link    string            `json:"link"`
	Choices map[string]string `json:"choices"`
}

// Options steer the plan.
type Options struct {
	Group     string
	Decisions map[int]Decision // by Row.ID (the line number for a line's first device)
}

// normName makes two names comparable per the rule's match settings.
func normName(s string, m model.ImportMatch) string {
	s = strings.TrimSpace(s)
	if m.Width {
		s = norm.NFKC.String(s)
	}
	if m.Space {
		s = strings.Join(strings.Fields(s), "")
	}
	if m.IgnoreCase {
		s = strings.ToLower(s)
	}
	if m.Dash {
		s = strings.ReplaceAll(s, "_", "-")
	}
	if m.Domain && !looksLikeAddress(s) {
		if i := strings.IndexByte(s, '.'); i > 0 {
			s = s[:i]
		}
	}
	return s
}

func looksLikeAddress(s string) bool {
	return strings.Trim(s, "0123456789.") == "" || strings.Contains(s, ":")
}

// fileRow is one row being planned.
type fileRow struct {
	row *Row
	// cells are the file's values of the mapped fields, canonicalised — but
	// an invalid value as the file has it: canonicalising replaces it with a
	// fallback that may equal the device's, and the cell would read "same"
	// with its error never shown.
	cells  map[string]string
	errs   map[string]string // cell errors of the file's values
	dev    model.Device      // the file's values as a device (new rows)
	pr     *PlanRow
	linked int // index in inv.Devices, -1 none
	// pickErr: the device picked by hand for this row is already another
	// row's pick.
	pickErr string
}

// BuildPlan works out what importing the rows into opt.Group would do.
// inv is not changed.
func BuildPlan(inv *model.Inventory, conv *Converted, r *model.ImportRule, opt Options) *Plan {
	p := &Plan{Counts: map[string]int{}, Missing: []Cand{}, Devices: []Cand{}}
	var group []int // indexes of the group's devices
	for i := range inv.Devices {
		if inv.Devices[i].Group == opt.Group {
			group = append(group, i)
			d := &inv.Devices[i]
			p.Devices = append(p.Devices, Cand{ID: d.ID, Name: d.Name, Host: d.Host})
		}
	}
	claimed := map[int]bool{}
	known := knownChoices(inv)
	var rows []*fileRow
	for i := range conv.Rows {
		row := &conv.Rows[i]
		pr := PlanRow{Line: row.Line, ID: row.ID, Unit: row.Unit, Label: row.Label, Name: strings.TrimSpace(row.Values["name"]), Candidates: []Cand{}, Cells: []Cell{}, Errors: []string{}}
		if row.Excluded != "" {
			pr.Status, pr.Link, pr.Note, pr.Exclusion, pr.RuleSkip = "skip", "skip", row.Excluded, row.Exclusion, true
			pr.Host, pr.Site = strings.TrimSpace(row.Values["host"]), strings.TrimSpace(row.Values["site"])
			p.Rows = append(p.Rows, pr)
			continue
		}
		fc := map[string]string{"group": opt.Group}
		for _, k := range row.Keys {
			fc[k] = row.Values[k]
		}
		// An OS type written as its display name ("Cisco IOS") is the key
		// ("cisco-ios"): the device holds keys, and a name would have gone
		// in as an OS type nobody has (review 2026-11-07 D-A2).
		if v, ok := fc["osType"]; ok {
			fc["osType"] = known.osKey(v)
		}
		dev, errs := csvio.CellsDevice(fc)
		dev.Group = opt.Group
		canon := csvio.DeviceCells(dev)
		// The file's hops under their own numbers (CellsDevice closed a
		// gap: a list with the second hop only wrote it as the first, and
		// an existing device's own first hop read as changed while the
		// second was never written).
		for k, v := range csvio.HopCells(fc) {
			canon[k] = v
		}
		fr := &fileRow{row: row, cells: map[string]string{}, errs: map[string]string{}, dev: dev, linked: -1}
		for _, k := range row.Keys {
			fr.cells[k] = canon[k]
		}
		for _, e := range errs {
			fr.errs[e.Col] = e.Msg
			if _, ok := fr.cells[e.Col]; ok {
				fr.cells[e.Col] = row.Values[e.Col]
			}
		}
		pr.Name, pr.Host, pr.Site = dev.Name, dev.Host, dev.Site
		p.Rows = append(p.Rows, pr)
		fr.pr = &p.Rows[len(p.Rows)-1]
		rows = append(rows, fr)
	}
	// p.Rows no longer grows: the pointers into it stay valid.
	idIdx := make(map[int]*PlanRow, len(p.Rows))
	for i := range p.Rows {
		idIdx[p.Rows[i].ID] = &p.Rows[i]
	}
	for _, fr := range rows {
		fr.pr = idIdx[fr.row.ID]
	}
	label := func(row *Row) string { return RowLabel(row.Line, row.Unit, row.Label) }

	// The group's devices looked up by key, name and address, each
	// normalised once: comparing every row with every device would
	// normalise thousands × thousands of names on each preview.
	idxByID := map[string]int{}
	byKey := map[string][]int{}  // keyOf(record's KeyBy, normalised record Key)
	byName := map[string][]int{} // the name as it is
	byNorm := map[string][]int{} // the name per the rule's differences
	byHost := map[string][]int{} // the address, lower case
	for _, i := range group {
		d := &inv.Devices[i]
		idxByID[d.ID] = i
		if rec, ok := inv.ImportRecords[d.ID]; ok && rec.Key != "" {
			k := keyOf(rec.KeyBy, normKey(rec.Key, rec.KeyBy, r.Match))
			byKey[k] = append(byKey[k], i)
		}
		byName[d.Name] = append(byName[d.Name], i)
		n := normName(d.Name, r.Match)
		byNorm[n] = append(byNorm[n], i)
		if h := strings.ToLower(strings.TrimSpace(d.Host)); h != "" {
			byHost[h] = append(byHost[h], i)
		}
	}
	free := func(l []int) []int {
		var out []int
		for _, i := range l {
			if !claimed[i] {
				out = append(out, i)
			}
		}
		return out
	}
	rowKey := func(row *Row) string {
		return keyOf(row.KeyBy, normKey(row.Key, row.KeyBy, r.Match))
	}
	cand := func(i int) Cand { d := &inv.Devices[i]; return Cand{ID: d.ID, Name: d.Name, Host: d.Host} }
	link := func(fr *fileRow, i int, by string) {
		fr.linked = i
		claimed[i] = true
		fr.pr.Link, fr.pr.MatchBy, fr.pr.MatchName = inv.Devices[i].ID, by, inv.Devices[i].Name
	}
	resolved := map[*fileRow]bool{}

	// The user's own choices first: a device picked by hand is taken before
	// any guess can claim it. Picked twice: the later row says so instead
	// of quietly matching something else.
	pickedBy := map[int]*Row{} // device index → the row that picked it
	for _, fr := range rows {
		d, ok := opt.Decisions[fr.row.ID]
		if !ok || d.Link == "" {
			continue
		}
		switch d.Link {
		case "new", "skip":
			fr.pr.Link = d.Link
			resolved[fr] = true
		default:
			i, ok := idxByID[d.Link]
			if !ok {
				break // a device gone meanwhile: matched as usual
			}
			if by, taken := pickedBy[i]; taken {
				fr.pr.Link = d.Link
				fr.pickErr = fmt.Sprintf("機器「%s」は %s 行目で選ばれています。どちらかを別の機器にしてください", inv.Devices[i].Name, label(by))
				resolved[fr] = true
				break
			}
			pickedBy[i] = fr.row
			link(fr, i, "pick")
			resolved[fr] = true
		}
	}
	// 1. the key it was imported under, 2. the name, 3. the address.
	passes := []func(fr *fileRow) ([]int, string){
		func(fr *fileRow) ([]int, string) {
			if fr.row.Key == "" {
				return nil, ""
			}
			return free(byKey[rowKey(fr.row)]), "key"
		},
		func(fr *fileRow) ([]int, string) {
			if exact := free(byName[fr.dev.Name]); len(exact) == 1 {
				return exact, "name"
			}
			// The loose list holds the exact ones too: several of either
			// is a choice for the user.
			return free(byNorm[normName(fr.dev.Name, r.Match)]), "norm"
		},
		func(fr *fileRow) ([]int, string) {
			if fr.dev.Host == "" {
				return nil, ""
			}
			return free(byHost[strings.ToLower(fr.dev.Host)]), "ip"
		},
	}
	for _, pass := range passes {
		for _, fr := range rows {
			if resolved[fr] {
				continue
			}
			c, by := pass(fr)
			switch {
			case len(c) == 1:
				link(fr, c[0], by)
				resolved[fr] = true
			case len(c) > 1:
				// Several devices fit: never a guess. Not imported until
				// one is picked.
				for _, i := range c {
					fr.pr.Candidates = append(fr.pr.Candidates, cand(i))
				}
				fr.pr.Link = "skip"
				fr.pr.Note = "候補が複数あります。どの機器か選んでください"
				resolved[fr] = true
			}
		}
	}
	for _, fr := range rows {
		if !resolved[fr] {
			fr.pr.Link = "new"
		}
	}

	// The cells, the device written and the row's status.
	for _, fr := range rows {
		planCells(inv, known, fr, fr.row.Keys, opt.Decisions[fr.row.ID])
	}
	// Names stay unique in the group: a row may not write a name that a
	// device keeps — one no row writes to, or one whose row fails — or that
	// an earlier row writes. A row failing frees nothing and reserves its
	// device's present name, which can fail another row: repeated until
	// nothing changes (each round fails at least one more row).
	writes := func(fr *fileRow) bool { return fr.pr.dev != nil && len(fr.pr.Errors) == 0 }
	for again := true; again; {
		again = false
		taken := map[string]*Row{} // name → the row writing it (nil: a device kept as it is)
		for _, i := range group {
			if !claimed[i] {
				taken[inv.Devices[i].Name] = nil
			}
		}
		for _, fr := range rows {
			if fr.linked >= 0 && !writes(fr) {
				taken[inv.Devices[fr.linked].Name] = nil
			}
		}
		for _, fr := range rows {
			if !writes(fr) {
				continue
			}
			n := fr.pr.dev.Name
			by, clash := taken[n]
			switch {
			case !clash:
				taken[n] = fr.row
				continue
			case by == nil:
				fr.pr.Errors = append(fr.pr.Errors, fmt.Sprintf("同じ名前の機器「%s」がグループにあります（結びつける機器を選ぶか、名前を変えてください）", n))
			default:
				fr.pr.Errors = append(fr.pr.Errors, fmt.Sprintf("機器名「%s」が %s 行目と重なっています", n, label(by)))
			}
			again = true
		}
	}
	for _, fr := range rows {
		if len(fr.pr.Errors) > 0 {
			fr.pr.Status, fr.pr.dev, fr.pr.Changes = "err", nil, false
		}
	}
	// Missing: the group's devices the file does not have. A device a row
	// of the file stands for — by key, name or address, as the passes match
	// — is not one, whatever becomes of that row (excluded, set to
	// 取り込まない or to a new device, undecided between candidates,
	// failing): offering it for deletion would delete a device the list
	// still has — and the row would not replace it.
	inFile := map[int]bool{}
	for _, fr := range rows {
		for _, c := range fr.pr.Candidates {
			inFile[idxByID[c.ID]] = true
		}
	}
	for i := range conv.Rows {
		row := &conv.Rows[i]
		if row.Key != "" {
			for _, j := range byKey[rowKey(row)] {
				inFile[j] = true
			}
		}
		if n := strings.TrimSpace(row.Values["name"]); n != "" {
			for _, j := range byNorm[normName(n, r.Match)] {
				inFile[j] = true
			}
		}
		// By the address alone the match is only a proposal: a row the user
		// declared another device (新規) frees the device at its address —
		// the list, by the user's word, no longer has it.
		if opt.Decisions[row.ID].Link == "new" {
			continue
		}
		if h := strings.ToLower(strings.TrimSpace(row.Values["host"])); h != "" {
			for _, j := range byHost[h] {
				inFile[j] = true
			}
		}
	}
	for _, i := range group {
		if !claimed[i] && !inFile[i] {
			p.Missing = append(p.Missing, cand(i))
		}
	}
	for i := range p.Rows {
		p.Counts[p.Rows[i].Status]++
	}
	return p
}

// keyOf joins a key column's name and a normalised key into one map key.
func keyOf(by, k string) string { return by + "\x00" + k }

// normKey makes match keys comparable. A name (by "") follows the rule's
// name settings; a key column's value (an asset number "NW.0012") only
// ignores case, width and spaces — cutting it at a dot or folding "_"
// would make different keys equal.
func normKey(s, by string, m model.ImportMatch) string {
	if by == "" {
		return normName(s, m)
	}
	return normName(s, model.ImportMatch{IgnoreCase: m.IgnoreCase, Width: m.Width, Space: m.Space})
}

// choices is what this PalaTerm has for the fields with a fixed list: the
// OS types (by key, and by display name folded). A value from the file
// that is neither is written as it is (the lists are PalaTerm's own: the
// operator fills them in after the import — user decision 2026-11-07,
// the review's D-A2 refusal withdrawn).
type choices struct {
	profiles map[string]bool
	osByName map[string]string
}

func knownChoices(inv *model.Inventory) choices {
	c := choices{profiles: map[string]bool{}, osByName: map[string]string{}}
	for _, p := range inv.CustomProfiles {
		c.profiles[p.Key] = true
		c.osByName[foldChoice(p.Name)] = p.Key
	}
	return c
}

// foldChoice is a name as names are compared: width, case and spaces
// ignored (the rule normalizeChoice applies to the other fixed lists).
func foldChoice(v string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(v)), ""))
}

// osKey is the profile key for v: v itself when it is a key, the key of
// the profile whose display name it is, else v as written (shown as
// "⚠ not found" in the device list until the operator sets it).
func (c choices) osKey(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || c.profiles[v] {
		return v
	}
	if k, ok := c.osByName[foldChoice(v)]; ok {
		return k
	}
	return v
}

// planCells fills a row's cells, status and the device it writes.
func planCells(inv *model.Inventory, known choices, fr *fileRow, keys []string, dec Decision) {
	pr := fr.pr
	filled := map[string]bool{}
	for _, k := range fr.row.Filled {
		filled[k] = true
	}
	guessed := map[string]bool{}
	for _, k := range fr.row.Guessed {
		guessed[k] = true
	}
	if fr.pickErr != "" {
		pr.Errors = append(pr.Errors, fr.pickErr)
		pr.Status = "err"
		return
	}
	if pr.Link == "skip" {
		pr.Status = "skip"
		if pr.Note == "" {
			pr.Note = "取り込みません"
		}
		if len(pr.Candidates) > 0 {
			pr.Status = "ask"
		}
		return
	}
	if fr.linked < 0 { // a new device
		for _, k := range keys {
			pr.Cells = append(pr.Cells, Cell{Key: k, File: fr.cells[k], State: "new", Choice: "file", Secret: isSecret(k), Filled: filled[k], Guessed: guessed[k]})
			if msg, ok := fr.errs[k]; ok {
				pr.Errors = append(pr.Errors, FieldLabel(k)+": "+msg)
			}
		}
		// An error on a field the rule does not map (a jump host whose
		// user name and password are mapped but not its host): until
		// v1.9.31 it was dropped with the hop, the row read as a plain
		// 追加 and the device came without the hop — nothing said why.
		for _, k := range sortedKeys(fr.errs) {
			if !mapped(keys, k) {
				pr.Errors = append(pr.Errors, FieldLabel(k)+": "+fr.errs[k]+"（この項目はルールで紐付けていません）")
			}
		}
		d := fr.dev
		d.ID = ""
		stamp(pr, fr, nil, nil)
		if gap, next := csvio.HopGapNext(fr.cells); gap > 0 {
			pr.Errors = append(pr.Errors, hopGapMsg(gap, next))
		}
		if err := inv.ValidateIn(&d); err != nil {
			pr.Errors = append(pr.Errors, err.Error())
		}
		pr.Status, pr.dev, pr.Changes = "add", &d, true
		return
	}
	cur := inv.Devices[fr.linked]
	cc := csvio.DeviceCells(cur)
	rec := inv.ImportRecords[cur.ID]
	lost := map[string]bool{}
	for _, k := range fr.row.Lost {
		lost[k] = true
	}
	asked, took := false, false
	out := map[string]string{}
	for k, v := range cc {
		out[k] = v
	}
	listed := map[string]bool{}   // fields whose file error is on the row already
	fromFile := map[string]bool{} // fields the file's value is written to
	for _, k := range keys {
		f, c := fr.cells[k], cc[k]
		cell := Cell{Key: k, File: f, Cur: c, Secret: isSecret(k), Filled: filled[k], Guessed: guessed[k]}
		b, has := rec.Base[k]
		switch {
		case f == c:
			cell.State, cell.Choice = "same", "keep"
		case lost[k]:
			// The column is gone from the file (renamed at the source, say):
			// the empty value or default it reads says nothing about the
			// device — taking it would blank every device's username and
			// password. Kept, whatever was chosen.
			cell.State, cell.Choice = "lost", "keep"
			pr.Cells = append(pr.Cells, cell)
			continue
		case guessed[k]:
			// Decided by a guess from what this PalaTerm holds (an OS type,
			// a command set — or none found), not by the source: it changes
			// with this PalaTerm's sets, so an existing device keeps its own.
			cell.State, cell.Choice = "guess", "keep"
		case !has && c == "" && f != "" && gridByKey[k].Kind != "bool":
			cell.State, cell.Choice = "fill", "file"
		case !has:
			// No record says who changed it: not "both changed" (review
			// 2026-11-07 D-B12), but a choice all the same, kept by default.
			cell.State, cell.Choice = "nobase", "keep"
		case rec.V == 0 && c == "" && f != "" && b == f && gridByKey[k].Kind != "bool":
			// A record of v1.9.30 or before: the cell was asked and left
			// on its default 残す, yet recorded as if written, so the
			// value the device never had read as PalaTerm's change at
			// every import since. Filled like a cell with no record; a
			// record written now (V 1) tells a deletion made here apart.
			cell.State, cell.Choice = "fill", "file"
		case b == c:
			cell.State, cell.Choice = "take", "file"
		case b == f:
			cell.State, cell.Choice = "keep", "keep"
		default:
			cell.State, cell.Choice = "ask", "keep"
		}
		if ch := dec.Choices[k]; cell.State != "same" && (ch == "file" || ch == "keep") {
			cell.Choice = ch
		}
		if cell.State == "ask" || cell.State == "nobase" {
			asked = true
		}
		if cell.Choice == "file" && f != c {
			out[k] = f
			took = true
			fromFile[k] = true
			if msg, ok := fr.errs[k]; ok {
				pr.Errors = append(pr.Errors, FieldLabel(k)+": "+msg)
				listed[k] = true
			}
		}
		pr.Cells = append(pr.Cells, cell)
	}
	// The file's values failing on a field the rule does not map (a jump
	// host's user name mapped, its host not): the hop read empty and the
	// row passed as 変更なし, the value dropped in silence — said here as a
	// new device's row says it (review 2026-11-07 D-A8).
	for _, k := range sortedKeys(fr.errs) {
		if !mapped(keys, k) {
			pr.Errors = append(pr.Errors, FieldLabel(k)+": "+fr.errs[k]+"（この項目はルールで紐付けていません）")
			listed[k] = true
		}
	}
	d, errs := csvio.CellsDevice(out)
	for _, e := range errs {
		if listed[e.Col] {
			continue // the file's own error is on the row already
		}
		msg := FieldLabel(e.Col) + ": " + e.Msg
		if !mapped(keys, e.Col) {
			msg += "（この項目はルールで紐付けていません）"
		}
		pr.Errors = append(pr.Errors, msg)
	}
	// A hop the file adds with the one before it empty on both sides:
	// CellsDevice would close the gap and write it one hop up.
	if gap, next := csvio.HopGapNext(out); gap > 0 {
		pr.Errors = append(pr.Errors, hopGapMsg(gap, next))
	}
	d.ID, d.Group = cur.ID, cur.Group
	stamp(pr, fr, inv.ImportRecords[cur.ID].Base, lost)
	if err := inv.ValidateIn(&d); err != nil {
		pr.Errors = append(pr.Errors, err.Error())
	}
	pr.Name, pr.Host, pr.Site = d.Name, d.Host, d.Site
	switch {
	case asked || pr.MatchBy == "ip":
		pr.Status = "ask"
	case took:
		pr.Status = "upd"
	default:
		pr.Status = "same"
	}
	pr.dev, pr.Changes = &d, took
}

// stamp keeps what the import writes, for the next one to compare with
// (written to Inventory.ImportRecords by Apply). A field lost from the file
// (an existing device's) keeps the last import's record — or none: when the
// column is back, the comparison is with what the source last said, not with
// an empty value it never said.
func stamp(pr *PlanRow, fr *fileRow, prev map[string]string, lost map[string]bool) {
	rec := &model.ImportRecord{Key: fr.row.Key, KeyBy: fr.row.KeyBy, Base: map[string]string{}, V: 1}
	for k, v := range fr.cells {
		if lost[k] {
			if b, ok := prev[k]; ok {
				rec.Base[k] = b
			}
			continue
		}
		rec.Base[k] = v
	}
	pr.rec = rec
}

func mapped(keys []string, k string) bool {
	for _, x := range keys {
		if x == k {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// UnmappedErrors runs the rule's converted rows through the device
// check and counts, per field the rule does not map, the rows whose
// values fail on it (a jump host with user name and password mapped but
// no host): the rule editor warns about it before any import.
func UnmappedErrors(conv *Converted) []string {
	type ek struct{ key, msg string }
	counts := map[ek]int{}
	var order []ek
	for _, row := range conv.Rows {
		if row.Excluded != "" {
			continue
		}
		fc := map[string]string{}
		for _, k := range row.Keys {
			fc[k] = row.Values[k]
		}
		_, errs := csvio.CellsDevice(fc)
		for _, e := range errs {
			if mapped(row.Keys, e.Col) {
				continue
			}
			k := ek{e.Col, e.Msg}
			if counts[k] == 0 {
				order = append(order, k)
			}
			counts[k]++
		}
	}
	var out []string
	for _, k := range order {
		out = append(out, fmt.Sprintf("%s: %s — この項目はルールで紐付けていません（%d 行）。このままでは取り込めません", FieldLabel(k.key), k.msg, counts[k]))
	}
	return out
}

// hopGapMsg: hop gap is empty while hop next (the first with a host after
// it) has one. A new device cannot hold the gap (its hops are a list), and
// closing it quietly wrote the later hop one up — the device's own first
// hop, set by hand or by 一括変更, then read as changed. The list without
// that hop is imported first, the hop added, and the list imported again
// with the hop's columns (an existing device keeps its hops and takes the
// file's by number).
func hopGapMsg(gap, next int) string {
	return fmt.Sprintf("踏み台%dが空のまま踏み台%dがあります。先に踏み台%dを付けた機器なら、取り込み直すと踏み台%dが足されます（新しい機器は、踏み台%d以降の列を外して取り込み → 踏み台%dを付ける → 列を戻して取り込み直し）", gap, next, gap, next, next, gap)
}

// Result says what Apply did.
type Result struct {
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Same    int      `json:"same"`
	Deleted int      `json:"deleted"`
	Skipped int      `json:"skipped"`
	Failed  []string `json:"failed"` // "line: reason"
	// Renames are the devices the import renamed (old name → new), for
	// the group's schedules to follow.
	Renames map[string]string `json:"-"`
}

// Apply writes the plan into inv: new devices appended, matched ones
// replaced (their ID kept), and the devices in deleteIDs (the group's,
// unmatched) removed. Rows with errors, skipped or undecided are left out.
func Apply(inv *model.Inventory, p *Plan, group string, deleteIDs []string) Result {
	res := Result{Failed: []string{}, Renames: map[string]string{}}
	byID := map[string]int{}
	for i := range inv.Devices {
		byID[inv.Devices[i].ID] = i
	}
	for i := range p.Rows {
		pr := &p.Rows[i]
		switch {
		case pr.Status == "err":
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %s", RowLabel(pr.Line, pr.Unit, pr.Label), strings.Join(pr.Errors, " / ")))
			continue
		case pr.dev == nil:
			res.Skipped++
			continue
		}
		d := *pr.dev
		if j, ok := byID[d.ID]; ok && d.ID != "" {
			old := inv.Devices[j]
			if old.GUICertKey() != d.GUICertKey() {
				delete(inv.KnownGUICerts, old.GUICertKey())
			}
			if old.Name != d.Name {
				res.Renames[old.Name] = d.Name
			}
			inv.Devices[j] = d
			setRecord(inv, d.ID, pr.rec)
			if pr.Changes {
				res.Updated++
			} else {
				res.Same++
			}
			continue
		}
		d.ID = model.NewDeviceID()
		d.Group = group
		inv.Devices = append(inv.Devices, d)
		setRecord(inv, d.ID, pr.rec)
		res.Added++
	}
	if len(deleteIDs) > 0 {
		missing := map[string]bool{}
		for _, c := range p.Missing {
			missing[c.ID] = true
		}
		del := map[string]bool{}
		for _, id := range deleteIDs {
			if missing[id] { // only the group's unmatched devices
				del[id] = true
			}
		}
		out := inv.Devices[:0]
		for _, d := range inv.Devices {
			if del[d.ID] {
				delete(inv.KnownGUICerts, d.GUICertKey())
				res.Deleted++
				continue
			}
			out = append(out, d)
		}
		inv.Devices = out
	}
	inv.EnsureDeviceIDs()
	inv.PruneImportRecords()
	return res
}

func setRecord(inv *model.Inventory, id string, rec *model.ImportRecord) {
	if rec == nil || id == "" {
		return
	}
	if inv.ImportRecords == nil {
		inv.ImportRecords = map[string]model.ImportRecord{}
	}
	inv.ImportRecords[id] = *rec
}

// Today is the date written to a rule's LastUsed.
func Today() string { return time.Now().Format("2006-01-02") }
