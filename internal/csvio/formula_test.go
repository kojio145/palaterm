package csvio

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// Values that start like a spreadsheet formula are written with a leading
// apostrophe (Excel shows them as text), and every value — one starting
// with an apostrophe included — reads back exactly as it was.
func TestFormulaInjectionGuardRoundTrips(t *testing.T) {
	in := []model.Device{{
		Name: "=HYPERLINK(\"http://x\")", Site: "+cmd|' /C calc'!A0", Role: "@SUM(1)", Group: "-g",
		Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "cisco-ios",
		Username: "'quoted", Password: "-pw=1", EnablePassword: "plain",
	}}
	text, err := Export(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"\n=HYPERLINK", ",=HYPERLINK", ",+cmd", ",@SUM", ",-g", ",-pw"} {
		if strings.Contains(text, bad) {
			t.Errorf("unescaped %q in:\n%s", bad, text)
		}
	}
	out, err := Import(text)
	if err != nil {
		t.Fatal(err)
	}
	d := out[0]
	if d.Name != in[0].Name || d.Site != in[0].Site || d.Role != in[0].Role || d.Group != in[0].Group ||
		d.Username != "'quoted" || d.Password != "-pw=1" || d.EnablePassword != "plain" {
		t.Errorf("round trip changed values: %+v", d)
	}
}
