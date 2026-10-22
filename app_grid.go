package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/kojio145/palaterm/internal/csvio"
	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/schedule"
)

// The device table editor (機器一覧 → 表で編集): the devices of one scope as
// rows of CSV cells, edited in the window like a spreadsheet and saved here
// in one go. Until then the table is a draft kept in the vault
// (Inventory.GridDrafts), so a lock, a restart or a row that will not save
// loses nothing.

// GridColumns lists the table's columns (the CSV's).
func (a *App) GridColumns() []csvio.GridCol { return csvio.GridColumns() }

// inGridScope reports whether d belongs to the table's scope.
func inGridScope(d model.Device, scope string) bool {
	switch scope {
	case "":
		return true
	case model.GridUngrouped:
		return d.Group == ""
	}
	return d.Group == scope
}

// GridRows returns the scope's devices as table rows, in stored order.
func (a *App) GridRows(scope string) ([]model.GridRow, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return nil, fmt.Errorf("vault is locked")
	}
	rows := []model.GridRow{}
	for _, d := range a.inv.Devices {
		if !inGridScope(d, scope) {
			continue
		}
		c := csvio.DeviceCells(d)
		rows = append(rows, model.GridRow{UID: d.ID, ID: d.ID, Base: c, Cells: copyCells(c)})
	}
	return rows, nil
}

func copyCells(c map[string]string) map[string]string {
	out := make(map[string]string, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

// GetGridDraft returns the scope's draft, or nil when there is none.
func (a *App) GetGridDraft(scope string) (*model.GridDraft, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return nil, fmt.Errorf("vault is locked")
	}
	d, ok := a.inv.GridDrafts[scope]
	if !ok {
		return nil, nil
	}
	return &d, nil
}

// SaveGridDraft keeps the table's current state (autosaved as it is edited).
// A draft with nothing changed in it is dropped instead.
func (a *App) SaveGridDraft(scope string, d model.GridDraft) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	if gridDraftChanges(d) == 0 {
		delete(a.inv.GridDrafts, scope)
	} else {
		if a.inv.GridDrafts == nil {
			a.inv.GridDrafts = map[string]model.GridDraft{}
		}
		d.Saved = time.Now().Format(time.RFC3339)
		a.inv.GridDrafts[scope] = d
	}
	a.mu.Unlock()
	a.persistSoon()
	return nil
}

// DiscardGridDraft throws the scope's draft away.
func (a *App) DiscardGridDraft(scope string) error {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return fmt.Errorf("vault is locked")
	}
	_, had := a.inv.GridDrafts[scope]
	delete(a.inv.GridDrafts, scope)
	a.mu.Unlock()
	if had {
		return a.persist()
	}
	return nil
}

// renameGridDrafts follows a group rename: the group's draft moves to the
// new name, and every draft's cells and base naming the group follow too
// (or each row would read as changed elsewhere at 保存).
func renameGridDrafts(inv *model.Inventory, oldName, newName string) {
	if oldName == newName || len(inv.GridDrafts) == 0 {
		return
	}
	fix := func(rows []model.GridRow) {
		for _, r := range rows {
			for _, c := range []map[string]string{r.Base, r.Cells} {
				if c != nil && c["group"] == oldName {
					c["group"] = newName
				}
			}
		}
	}
	for scope, d := range inv.GridDrafts {
		fix(d.Rows)
		fix(d.Deleted)
		if scope == oldName {
			delete(inv.GridDrafts, scope)
			inv.GridDrafts[newName] = d
		} else {
			inv.GridDrafts[scope] = d
		}
	}
}

func gridDraftChanges(d model.GridDraft) int {
	n := len(d.Deleted)
	for _, r := range d.Rows {
		if r.Dirty() {
			n++
		}
	}
	return n
}

// GridDraftCounts says how many rows each scope's draft has not saved yet
// (the 「下書き n 行（未反映）」 badge).
func (a *App) GridDraftCounts() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]int{}
	if a.inv == nil {
		return out
	}
	for scope, d := range a.inv.GridDrafts {
		if n := gridDraftChanges(d); n > 0 {
			out[scope] = n
		}
	}
	return out
}

// GridApply is a save from the table. Mode "check" only reports; "all"
// saves when nothing is wrong; "valid" saves the rows that are fine and
// leaves the rest. Overwrite lists the rows (UID, or "del:"+ID for a
// deletion) whose device was changed elsewhere since the table read it and
// that are to be saved over it anyway.
type GridApply struct {
	Scope     string          `json:"scope"`
	Rows      []model.GridRow `json:"rows"`
	Deleted   []model.GridRow `json:"deleted"`
	Mode      string          `json:"mode"`
	Overwrite []string        `json:"overwrite"`
}

// GridConflict is a row whose device changed (or went) elsewhere meanwhile.
type GridConflict struct {
	UID     string            `json:"uid"` // "del:"+ID for a deletion
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Gone    bool              `json:"gone"`    // deleted elsewhere
	Current map[string]string `json:"current"` // the device as it is now
	Diffs   []GridDiff        `json:"diffs"`
}

// GridDiff is a cell the other change touched: as the table read it (Base),
// as it is now (Theirs) and as the table has it (Mine).
type GridDiff struct {
	Col    string `json:"col"`
	Base   string `json:"base"`
	Theirs string `json:"theirs"`
	Mine   string `json:"mine"`
}

// GridReport is what a check or a save found and did.
type GridReport struct {
	Errors    map[string][]csvio.CellError `json:"errors"` // by row UID
	Conflicts []GridConflict               `json:"conflicts"`
	Added     int                          `json:"added"`
	Changed   int                          `json:"changed"`
	Deleted   int                          `json:"deleted"`
	Applied   bool                         `json:"applied"`
	// Saved are the rows written, as they now read (new rows with their ID).
	Saved map[string]model.GridRow `json:"saved"`
	// SavedDeletes are the IDs of the devices deleted.
	SavedDeletes []string `json:"savedDeletes"`
	// dropPins are the GUI certificate pins of deleted or re-addressed
	// devices, removed by ApplyGrid once the vault write succeeded.
	dropPins []string `json:"-"`
}

// ApplyGrid checks and saves the table.
func (a *App) ApplyGrid(req GridApply) (*GridReport, error) {
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("vault is locked")
	}
	rep, next, renames := planGrid(a.inv, req)
	if next == nil {
		a.mu.Unlock()
		return rep, nil
	}
	var stopped []SchedLogEntry
	defer func() {
		a.mu.Unlock()
		a.logStopped(stopped) // only once the save went through (nil otherwise)
	}()
	// Kept to put back if the vault cannot be written: the window still
	// shows the table as unsaved, and memory must agree with it. The write
	// happens under the same lock, so nothing else changes in between and
	// putting these back undoes only this save.
	prevDevices, prevGroups := a.inv.Devices, append([]model.DeviceGroup(nil), a.inv.DeviceGroups...)
	prevSched := cloneSchedules(a.inv.Schedules)
	prevPins := a.inv.KnownGUICerts
	if prevPins != nil {
		prevPins = make(map[string]string, len(a.inv.KnownGUICerts))
		for k, v := range a.inv.KnownGUICerts {
			prevPins[k] = v
		}
	}
	// Deleted or moved to another group: a schedule naming the device would
	// run on whatever takes its name (stopped against the names as they
	// were, before the renames are followed).
	stop := stopLeaving(a.inv, a.inv.Devices, next)
	for group, m := range renames {
		renameSchedDevices(a.inv, group, m)
	}
	for _, d := range next {
		a.ensureGroupExists(d.Group)
	}
	a.inv.Devices = next
	a.inv.EnsureDeviceIDs()
	for _, k := range rep.dropPins {
		delete(a.inv.KnownGUICerts, k)
	}
	if err := a.saveVaultLocked(a.password); err != nil {
		a.inv.Devices, a.inv.DeviceGroups, a.inv.Schedules, a.inv.KnownGUICerts = prevDevices, prevGroups, prevSched, prevPins
		return nil, err
	}
	stopped = stop
	return rep, nil
}

func cloneSchedules(in []schedule.Schedule) []schedule.Schedule {
	if in == nil {
		return nil
	}
	b, _ := json.Marshal(in)
	var out []schedule.Schedule
	_ = json.Unmarshal(b, &out)
	return out
}

// renameSchedDevices follows the table's renames in the group's schedules,
// all at once: applied one after another, a renumbering (sw1→sw2, sw2→sw3)
// or a swap would carry a schedule's sw1 on to sw3 — another device.
func renameSchedDevices(inv *model.Inventory, group string, m map[string]string) {
	for i := range inv.Schedules {
		sc := &inv.Schedules[i]
		if sc.Group != group || sc.Names == nil {
			continue
		}
		// A fresh slice: copies of the schedules handed out earlier share
		// the old one and must not change under them.
		names := append([]string(nil), sc.Names...)
		for j, n := range names {
			if nn, ok := m[n]; ok {
				names[j] = nn
			}
		}
		sc.Names = names
	}
}

// planGrid works out a table save against inv without changing it: the
// report, the device list to store (nil when nothing is to be saved) and the
// renames (group → old name → new name) the schedules are to follow.
func planGrid(inv *model.Inventory, req GridApply) (*GridReport, []model.Device, map[string]map[string]string) {
	rep := &GridReport{Errors: map[string][]csvio.CellError{}, Saved: map[string]model.GridRow{}, Conflicts: []GridConflict{}, SavedDeletes: []string{}}
	over := map[string]bool{}
	for _, u := range req.Overwrite {
		over[u] = true
	}
	idx := map[string]int{}
	for i, d := range inv.Devices {
		idx[d.ID] = i
	}
	profiles := map[string]bool{}
	for _, p := range inv.CustomProfiles {
		profiles[p.Key] = true
	}
	sets := map[string]bool{}
	for _, s := range inv.CommandSets {
		sets[s.Name] = true
	}
	addErr := func(uid, col, msg string) {
		rep.Errors[uid] = append(rep.Errors[uid], csvio.CellError{Col: col, Msg: msg})
	}
	conflict := func(uid, id string, base, mine map[string]string, gone bool) {
		c := GridConflict{UID: uid, ID: id, Gone: gone, Diffs: []GridDiff{}}
		if !gone {
			c.Current = csvio.DeviceCells(inv.Devices[idx[id]])
			c.Name = c.Current["name"]
			keys := map[string]bool{}
			for k := range base {
				keys[k] = true
			}
			for k := range c.Current {
				keys[k] = true
			}
			for k := range keys {
				if base[k] != c.Current[k] {
					c.Diffs = append(c.Diffs, GridDiff{Col: k, Base: base[k], Theirs: c.Current[k], Mine: mine[k]})
				}
			}
			sort.Slice(c.Diffs, func(i, j int) bool { return c.Diffs[i].Col < c.Diffs[j].Col })
		} else {
			c.Name = base["name"]
		}
		rep.Conflicts = append(rep.Conflicts, c)
	}

	type cand struct {
		row      model.GridRow
		d        model.Device
		conflict bool
	}
	var cands []*cand
	for _, r := range req.Rows {
		if !r.Dirty() {
			continue
		}
		c := &cand{row: r}
		d, errs := csvio.CellsDevice(r.Cells)
		for _, e := range errs {
			addErr(r.UID, e.Col, e.Msg)
		}
		if r.ID != "" {
			if i, ok := idx[r.ID]; !ok {
				if !over[r.UID] {
					conflict(r.UID, r.ID, csvio.UpgradeBase(r.Base), r.Cells, true)
					c.conflict = true
				}
				// Saved over a device deleted elsewhere: it comes back as new.
			} else {
				if !model.SameCells(csvio.DeviceCells(inv.Devices[i]), csvio.UpgradeBase(r.Base)) && !over[r.UID] {
					conflict(r.UID, r.ID, csvio.UpgradeBase(r.Base), r.Cells, false)
					c.conflict = true
				}
				d.ID = r.ID
			}
		}
		if err := inv.ValidateIn(&d); err != nil {
			addErr(r.UID, csvio.ErrorColumn(err.Error()), err.Error())
		}
		if !profiles[d.OSType] {
			addErr(r.UID, "osType", fmt.Sprintf("OSタイプ「%s」は登録されていません", d.OSType))
		}
		if d.CommandSet != "" && !sets[d.CommandSet] {
			addErr(r.UID, "commandSet", fmt.Sprintf("コマンドセット「%s」は登録されていません", d.CommandSet))
		}
		c.d = d
		cands = append(cands, c)
	}
	deleting := map[string]bool{}
	var gone []string // deleted elsewhere already: done, as far as the table goes
	for _, r := range req.Deleted {
		i, ok := idx[r.ID]
		if !ok {
			gone = append(gone, r.ID)
			continue
		}
		if !model.SameCells(csvio.DeviceCells(inv.Devices[i]), csvio.UpgradeBase(r.Base)) && !over["del:"+r.ID] {
			conflict("del:"+r.ID, r.ID, csvio.UpgradeBase(r.Base), nil, false)
			continue
		}
		deleting[r.ID] = true
	}

	// A name is unique within its group. Counted over the devices as they
	// will be: the saved rows under their new names, every other device
	// (rows left unsaved included) under its stored one. Both of two
	// clashing rows are marked: which one is meant to change is the user's
	// call.
	key := func(d model.Device) string { return d.Group + "\x00" + d.Name }
	dupes := func(saving func(*cand) bool) []*cand {
		count := map[string]int{}
		replaced := map[string]bool{}
		for _, c := range cands {
			if saving(c) && c.d.ID != "" {
				replaced[c.d.ID] = true
			}
		}
		for _, d := range inv.Devices {
			if !deleting[d.ID] && !replaced[d.ID] {
				count[key(d)]++
			}
		}
		for _, c := range cands {
			if saving(c) {
				count[key(c.d)]++
			}
		}
		var out []*cand
		for _, c := range cands {
			if saving(c) && c.d.Name != "" && count[key(c.d)] > 1 {
				out = append(out, c)
			}
		}
		return out
	}
	markDupe := func(c *cand) {
		g := c.d.Group
		if g == "" {
			g = "（グループ未設定）"
		}
		addErr(c.row.UID, "name", fmt.Sprintf("グループ「%s」に「%s」が重複しています", g, c.d.Name))
	}
	// As if every row were saved (what 「保存」 of everything would do) …
	for _, c := range dupes(func(*cand) bool { return true }) {
		markDupe(c)
	}
	// … and, saving only the rows without errors, again until no saved row
	// clashes with one that stays as it is.
	if req.Mode == "valid" {
		for {
			more := dupes(func(c *cand) bool { return !c.conflict && len(rep.Errors[c.row.UID]) == 0 })
			if len(more) == 0 {
				break
			}
			for _, c := range more {
				markDupe(c)
			}
		}
	}

	for _, c := range cands {
		if c.row.ID == "" {
			rep.Added++
		} else {
			rep.Changed++
		}
	}
	rep.Deleted = len(deleting)
	ok := len(rep.Errors) == 0 && len(rep.Conflicts) == 0
	if req.Mode == "check" || (req.Mode == "all" && !ok) || (req.Mode != "all" && req.Mode != "valid") {
		return rep, nil, nil
	}
	rep.SavedDeletes = append(rep.SavedDeletes, gone...)

	next := make([]model.Device, 0, len(inv.Devices))
	for _, d := range inv.Devices {
		if !deleting[d.ID] {
			next = append(next, d)
		} else {
			rep.dropPins = append(rep.dropPins, d.GUICertKey()) // the pin goes with the device (after the save)
		}
	}
	for id := range deleting {
		rep.SavedDeletes = append(rep.SavedDeletes, id)
	}
	sort.Strings(rep.SavedDeletes)
	pos := map[string]int{}
	for i, d := range next {
		pos[d.ID] = i
	}
	newID := map[string]string{}
	renames := map[string]map[string]string{}
	var added []model.Device
	saved := 0
	for _, c := range cands {
		if c.conflict || len(rep.Errors[c.row.UID]) > 0 {
			continue
		}
		d := c.d
		if i, ok := pos[d.ID]; ok && d.ID != "" {
			if old := next[i]; old.Group == d.Group && old.Name != d.Name {
				if renames[d.Group] == nil {
					renames[d.Group] = map[string]string{}
				}
				renames[d.Group][old.Name] = d.Name
			}
			// A GUI pin names the address: moved, the device starts over
			// there (trust on first use) and the old pin goes.
			if old := next[i]; old.GUICertKey() != d.GUICertKey() {
				rep.dropPins = append(rep.dropPins, old.GUICertKey())
			}
			next[i] = d
		} else {
			d.ID = model.NewDeviceID()
			added = append(added, d)
		}
		newID[c.row.UID] = d.ID
		cells := csvio.DeviceCells(d)
		rep.Saved[c.row.UID] = model.GridRow{UID: c.row.UID, ID: d.ID, Base: cells, Cells: copyCells(cells)}
		saved++
	}
	if saved == 0 && len(deleting) == 0 {
		return rep, nil, nil
	}
	// New devices go in right after the table's last stored one (not at the
	// end of the whole list, behind other groups), so the reorder below
	// keeps the table's devices together.
	at := len(next)
	if len(added) > 0 {
		last := -1
		for _, r := range req.Rows {
			if i, ok := pos[r.ID]; ok && r.ID != "" && i > last {
				last = i
			}
		}
		if last >= 0 {
			at = last + 1
		}
		next = append(next[:at], append(added, next[at:]...)...)
	}
	for i, d := range next {
		pos[d.ID] = i
	}
	// The table's row order becomes the stored order of those devices.
	var order []string
	for _, r := range req.Rows {
		id := r.ID
		if n, ok := newID[r.UID]; ok {
			id = n
		}
		if _, ok := pos[id]; ok && id != "" && !deleting[id] {
			order = append(order, id)
		}
	}
	reorderSlots(next, func(d model.Device) string { return d.ID }, order)
	rep.Applied = true
	return rep, next, renames
}
