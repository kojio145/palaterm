package main

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A table save makes a group only for the rows it saves: a group typed
// into a row comes to be; a group named by some other device of the
// inventory (none in the group list) is not touched by the save.
func TestApplyGridGroupsOfSavedRows(t *testing.T) {
	a := gridApp(t)
	devNamed(a, "solo").Group = "ghost" // a device whose group is not listed
	rows := gridRowsOf(t, a, "lab")
	rows[0].Cells["group"] = "newg"
	rep, err := a.ApplyGrid(GridApply{Scope: "lab", Rows: rows, Mode: "all"})
	if err != nil || !rep.Applied {
		t.Fatalf("%+v %v", rep, err)
	}
	if a.inv.Group("newg") == nil {
		t.Error("the typed group was not made")
	}
	if a.inv.Group("ghost") != nil {
		t.Error("a group of an unsaved device was made")
	}
}

// A device deleted from the table takes its import record (the last
// import's cells, passwords among them) with it, in the same write.
func TestApplyGridPrunesImportRecords(t *testing.T) {
	a := gridApp(t)
	rows := gridRowsOf(t, a, "lab")
	keep, gone := rows[0].ID, rows[2].ID
	a.inv.ImportRecords = map[string]model.ImportRecord{
		keep: {Base: map[string]string{"password": "x9"}},
		gone: {Base: map[string]string{"password": "x9"}},
	}
	rep, err := a.ApplyGrid(GridApply{Scope: "lab", Rows: rows[:2], Deleted: []model.GridRow{rows[2]}, Mode: "all"})
	if err != nil || !rep.Applied || rep.Deleted != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, ok := a.inv.ImportRecords[gone]; ok {
		t.Error("the deleted device's record stayed")
	}
	if _, ok := a.inv.ImportRecords[keep]; !ok {
		t.Error("a kept device's record went")
	}
	// Read back from the vault: what was written agrees.
	a.Lock()
	if err := a.Unlock("master"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.inv.ImportRecords[gone]; ok || len(a.inv.ImportRecords) != 1 {
		t.Errorf("records in the vault: %v", a.inv.ImportRecords)
	}
}
