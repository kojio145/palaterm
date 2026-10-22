package bundle

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// The vault's "custom:MyOS" is a profile since renamed "Lab OS" with other
// steps: the bundle's MyOS under the same key is another profile. Even with
// OverwriteShared it must not replace "Lab OS"; it comes in under a key of
// its own and the bundle's device is pointed at it.
func TestApplyDoesNotReplaceRenamedProfileByKey(t *testing.T) {
	inv := &model.Inventory{
		CustomProfiles: []profile.Profile{{Key: "custom:MyOS", Name: "Lab OS", Prompt: "lab#"}},
		Devices:        []model.Device{{ID: "d1", Name: "lab1", Host: "192.0.2.50", Conn: model.ConnSSH, OSType: "custom:MyOS", Enabled: true}},
	}
	c := Select(sampleInventory(), "A社", false)
	for _, opt := range []Options{{}, {OverwriteShared: true}} {
		inv := inv.Clone()
		s, _ := Apply(inv, c, opt)
		var lab, mine *profile.Profile
		for i := range inv.CustomProfiles {
			switch inv.CustomProfiles[i].Name {
			case "Lab OS":
				lab = &inv.CustomProfiles[i]
			case "MyOS":
				mine = &inv.CustomProfiles[i]
			}
		}
		if lab == nil || lab.Key != "custom:MyOS" || lab.Prompt != "lab#" {
			t.Fatalf("%+v: the renamed profile changed: %+v", opt, lab)
		}
		if mine == nil || mine.Key == "custom:MyOS" {
			t.Fatalf("%+v: bundle profile = %+v", opt, mine)
		}
		for _, d := range inv.Devices {
			if d.Name == "a-fw1" && d.OSType != mine.Key {
				t.Errorf("%+v: a-fw1 OSType = %q, want %q", opt, d.OSType, mine.Key)
			}
			if d.Name == "lab1" && d.OSType != "custom:MyOS" {
				t.Errorf("%+v: lab1 OSType = %q", opt, d.OSType)
			}
		}
		if s.ProfilesNew < 1 {
			t.Errorf("%+v: summary = %+v", opt, s)
		}
	}
}

// Renamed but unchanged (same steps) is still the same profile: matched by
// key, nothing added.
func TestApplyMatchesRenamedSameProfileByKey(t *testing.T) {
	inv := &model.Inventory{
		CustomProfiles: []profile.Profile{
			{Key: "cisco-ios", Name: "Cisco IOS", Prompt: "#"},
			{Key: "generic", Name: "Generic", Prompt: "$"},
			{Key: "custom:MyOS", Name: "My OS (renamed)", Prompt: ">"},
		},
	}
	c := Select(sampleInventory(), "A社", false)
	s, _ := Apply(inv, c, Options{})
	if s.ProfilesNew != 0 || len(inv.CustomProfiles) != 3 {
		t.Errorf("summary = %+v, profiles = %+v", s, inv.CustomProfiles)
	}
}
