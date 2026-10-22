package model

import (
	"testing"

	"github.com/kojio145/palaterm/internal/profile"
)

func TestRekeyDuplicateProfiles(t *testing.T) {
	inv := &Inventory{CustomProfiles: []profile.Profile{
		{Key: "custom:X", Name: "X"},
		{Key: "custom:X_copy", Name: "Y"},
		{Key: "custom:X_copy#2", Name: "Z"},
		{Key: "custom:X_copy", Name: "X_copy"},
	}}
	fixed := inv.RekeyDuplicateProfiles()
	if len(fixed) != 1 || fixed[0] != "X_copy" {
		t.Fatalf("fixed = %v", fixed)
	}
	if inv.CustomProfiles[1].Key != "custom:X_copy" {
		t.Errorf("the first must keep the key: %+v", inv.CustomProfiles[1])
	}
	// custom:X_copy and #2 are taken: the copy gets #3.
	if got := inv.CustomProfiles[3].Key; got != "custom:X_copy#3" {
		t.Errorf("rekeyed = %q", got)
	}
	if again := inv.RekeyDuplicateProfiles(); len(again) != 0 {
		t.Errorf("second pass changed %v", again)
	}
}
