package csvio

import (
	"reflect"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// The table editor saves a device from its row alone: a Device or Bastion
// field the row does not carry would be wiped by any edit made in the
// table. This test fills every field and requires each to survive a trip
// through the row — a field added later fails here until the table (and the
// CSV) carry it, or it is listed below as one the row deliberately leaves out.
func TestGridRowCarriesEveryDeviceField(t *testing.T) {
	notInRow := map[string]bool{
		"ID":      true, // the row's own key, kept beside the cells
		"Bastion": true, // legacy single jump host, folded into Bastions on load
	}
	hop := model.Bastion{Host: "192.0.2.10", Method: model.BastionSSH, Port: 2222, Username: "j", Password: "jp",
		AuthMethod: model.AuthPublicKey, KeyFile: `C:\k\hop`, KeyPassphrase: "hp", LegacyAlgos: true, JumpCommand: "ssh -l {user} {host}"}
	d := model.Device{
		ID: "x", Name: "rt1", Host: "192.0.2.1", Conn: model.ConnSSH, Port: 2200, SerialPort: "COM3", Baud: 19200,
		Group: "lab", Site: "本社", Role: "コア", OSType: "cisco-ios", CommandSet: "bk",
		AuthMethod: model.AuthPublicKey, KeyFile: `C:\k\id`, KeyPassphrase: "pp", Username: "ops", Password: "pw", EnablePassword: "en",
		LegacyAlgos: true, Bastions: []model.Bastion{hop}, BastionMode: model.BastionViaTunnel,
		Bastion: &model.Bastion{Host: "192.0.2.99"}, UseGroupCreds: true, Enabled: true, GUIPort: 10443, UseMyBastion: true, CLIOff: true, GUIOn: true, GUIScheme: "http", TermColor: "red",
	}
	// Mine only exists on copies resolved for a connection, never in a row.
	resolvedOnly := map[string]bool{"Mine": true}
	requireAllSet := func(v reflect.Value, what string) {
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).IsZero() && !resolvedOnly[v.Type().Field(i).Name] {
				t.Errorf("%s.%s is not filled in this test: add it (and to the table and CSV columns)", what, v.Type().Field(i).Name)
			}
		}
	}
	requireAllSet(reflect.ValueOf(d), "Device")
	requireAllSet(reflect.ValueOf(hop), "Bastion")

	back, errs := CellsDevice(DeviceCells(d))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	dv, bv := reflect.ValueOf(d), reflect.ValueOf(back)
	for i := 0; i < dv.NumField(); i++ {
		name := dv.Type().Field(i).Name
		if notInRow[name] {
			continue
		}
		if !reflect.DeepEqual(dv.Field(i).Interface(), bv.Field(i).Interface()) {
			t.Errorf("Device.%s does not survive the table row: %v → %v", name, dv.Field(i).Interface(), bv.Field(i).Interface())
		}
	}
}
