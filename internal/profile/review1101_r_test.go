package profile

import "testing"

// Review 2026-11-01 (B B-10, B B-6): the Junos operational prompt carries
// its trailing space, which an XML tag's ">" never has; the ArubaOS-Switch
// login row waits for "Press any key to continue" and sends the Enter
// there, instead of waiting for a "#" that does not show until then.
func TestReview1101DefaultPrompts(t *testing.T) {
	var junos, aruba *Profile
	for i := range defaults {
		switch defaults[i].Key {
		case "juniper-junos":
			junos = &defaults[i]
		case "aruba-os-switch":
			aruba = &defaults[i]
		}
	}
	if junos == nil || aruba == nil {
		t.Fatal("juniper-junos / aruba-os-switch missing from the defaults")
	}
	if junos.Prompt != "> " {
		t.Errorf("junos prompt %q, want \"> \" (the space tells it from an XML tag)", junos.Prompt)
	}
	last := aruba.Login[len(aruba.Login)-1]
	if last.Expect != "continue" || !last.Enter || last.Send != "" {
		t.Errorf("aruba last login row %+v, want Expect \"continue\" with Enter", last)
	}
}
