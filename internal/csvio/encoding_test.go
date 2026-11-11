package csvio

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// The character set travels through the CSV as a "文字コード" / "encoding"
// column: Shift_JIS written out and read back in any spelling, UTF-8 left
// empty so a file from before the column looks the same.
func TestEncodingColumnRoundTrip(t *testing.T) {
	in := []model.Device{
		{Name: "lab-rt1", Host: "192.0.2.1", Conn: model.ConnSSH, OSType: "generic", Enabled: true, Encoding: model.EncodingSJIS},
		{Name: "lab-rt2", Host: "192.0.2.2", Conn: model.ConnSSH, OSType: "generic", Enabled: true},
	}
	for _, lang := range []string{"ja", "en"} {
		text, err := ExportLang(in, lang)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "Shift_JIS") {
			t.Errorf("%s: Shift_JIS not written:\n%s", lang, text)
		}
		out, err := Import(text)
		if err != nil {
			t.Fatal(err)
		}
		if out[0].Encoding != model.EncodingSJIS || out[1].Encoding != "" {
			t.Errorf("%s: encodings %q / %q", lang, out[0].Encoding, out[1].Encoding)
		}
	}
	// Hand-written spellings.
	for _, cell := range []string{"sjis", "CP932", "shift-jis"} {
		out, err := Import("name,host,encoding\nlab-rt3,192.0.2.3," + cell + "\n")
		if err != nil || len(out) != 1 || out[0].Encoding != model.EncodingSJIS {
			t.Errorf("%q: %v %+v", cell, err, out)
		}
	}
	// The table row carries it the same way.
	cells := DeviceCells(in[0])
	if cells["encoding"] != model.EncodingSJIS {
		t.Errorf("row cell %q", cells["encoding"])
	}
	cells["encoding"] = "Shift_JIS"
	back, errs := CellsDevice(cells)
	if len(errs) != 0 || back.Encoding != model.EncodingSJIS {
		t.Errorf("row read: %v %q", errs, back.Encoding)
	}
	cells["encoding"] = "euc-jp"
	if _, errs := CellsDevice(cells); len(errs) == 0 {
		t.Error("euc-jp accepted by the row")
	}
}
