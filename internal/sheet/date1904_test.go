package sheet

import "testing"

// A workbook saved by Mac Excel counts dates from 1904: serial 0 is
// 1904-01-01, not 1899-12-30.
func TestXLSXDate1904(t *testing.T) {
	build := func(pr string) []byte {
		return buildZip(
			[2]string{"_rels/.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
			[2]string{"xl/workbook.xml", `<?xml version="1.0"?><workbook ` + xlsxNS + `>` + pr + `<sheets><sheet name="s" sheetId="1" r:id="rId1"/></sheets></workbook>`},
			[2]string{"xl/_rels/workbook.xml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
			[2]string{"xl/styles.xml", `<?xml version="1.0"?><styleSheet ` + xlsxNS + `><cellXfs count="2"><xf numFmtId="0"/><xf numFmtId="14"/></cellXfs></styleSheet>`},
			[2]string{"xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet ` + xlsxNS + `><sheetData><row r="1"><c r="A1" s="1"><v>366</v></c></row></sheetData></worksheet>`},
		)
	}
	for _, c := range []struct{ pr, want string }{
		{``, "1900/12/31"},
		{`<workbookPr date1904="1"/>`, "1905/01/01"},
		{`<workbookPr date1904="true" defaultThemeVersion="1"/>`, "1905/01/01"},
		{`<workbookPr date1904="0"/>`, "1900/12/31"},
	} {
		b := mustRead(t, build(c.pr), "xlsx")
		if got := b.Sheets[0].Rows[0][0]; got != c.want {
			t.Errorf("%q: %q, want %q", c.pr, got, c.want)
		}
	}
}
