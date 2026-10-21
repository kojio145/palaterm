package sheet

import "testing"

func TestFormatPadded(t *testing.T) {
	for _, c := range []struct {
		v    float64
		code string
		want string
		ok   bool
	}{
		{1, "000", "001", true},
		{12, `"TKY-"0000`, "TKY-0012", true},
		{12, `\N0000`, "N0012", true},
		{7, `00" 号機"`, "07 号機", true},
		{12345, "000", "12345", true}, // longer than the format: as it is
		{1, "000;-000", "001", true},
		{1.5, "000", "", false}, // a fraction: not this shape
		{1, "0", "", false},     // the plain number
		{1, "#,##0", "", false},
		{1, "0.00", "", false},
		{1, "00-00", "", false},
		{-1, "000", "", false},
		{1, "", "", false},
	} {
		got, ok := formatPadded(c.v, c.code)
		if got != c.want || ok != c.ok {
			t.Errorf("formatPadded(%v, %q) = %q %v, want %q %v", c.v, c.code, got, ok, c.want, c.ok)
		}
	}
}

// An xlsx site code typed as 1 and shown as 001 reads 001.
func TestXLSXPadded(t *testing.T) {
	b := mustRead(t, buildZip(
		[2]string{"_rels/.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		[2]string{"xl/workbook.xml", `<?xml version="1.0"?><workbook ` + xlsxNS + `><sheets><sheet name="s" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		[2]string{"xl/_rels/workbook.xml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
		[2]string{"xl/styles.xml", `<?xml version="1.0"?><styleSheet ` + xlsxNS + `><numFmts count="1"><numFmt numFmtId="164" formatCode="000"/></numFmts><cellXfs count="2"><xf numFmtId="0"/><xf numFmtId="164"/></cellXfs></styleSheet>`},
		[2]string{"xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet ` + xlsxNS + `><sheetData><row r="1"><c r="A1" s="1"><v>1</v></c><c r="B1"><v>1</v></c></row></sheetData></worksheet>`},
	), "xlsx")
	if row := b.Sheets[0].Rows[0]; row[0] != "001" || row[1] != "1" {
		t.Errorf("row = %q", row)
	}
}
