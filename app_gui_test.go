package main

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A GUI window reports its certificate under the key it was opened with,
// and the pin is recorded only while a device still answers to that key:
// the device may have been deleted or re-addressed while the window was
// open (2026-10-24 review: the pin came back as an orphan).
func TestGUICertOnlyForLiveKey(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	dev := model.Device{Name: "fw1", Host: "192.0.2.10", Conn: model.ConnSSH, OSType: "cisco-ios",
		Username: "ops", Password: "pw", Enabled: true, GUIOn: true}
	if err := a.SaveDevice(dev); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	d := a.inv.Devices[0]
	a.mu.Unlock()
	key := d.GUICertKey()
	fp := strings.Repeat("ab", 32)
	a.setGUICert(key, fp)
	if got := a.inv.KnownGUICerts[key]; got != fp {
		t.Fatalf("pin of a live device = %q, want %q", got, fp)
	}
	gone := "gui nobody 192.0.2.99:443"
	a.setGUICert(gone, "ca")
	if _, ok := a.inv.KnownGUICerts[gone]; ok {
		t.Error("pin recorded under a key no device has")
	}
	if err := a.DeleteDevice(d.ID); err != nil {
		t.Fatal(err)
	}
	a.setGUICert(key, fp) // the window was still open when the device went
	if _, ok := a.inv.KnownGUICerts[key]; ok {
		t.Error("orphan pin recorded after the device was deleted")
	}
}
