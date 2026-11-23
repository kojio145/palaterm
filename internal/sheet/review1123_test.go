package sheet

import (
	"archive/zip"
	"bytes"
	"testing"
)

// Excel's _xHHHH_ escape is undone (review 2026-11-23).
func TestUnescapeX(t *testing.T) {
	for in, want := range map[string]string{
		"uplink-to-sw-a_x000D_": "uplink-to-sw-a\r",
		"a_x005F_x0041_b":       "a_x0041_b",
		"plain":                 "plain",
		"a_xZZZZ_b":             "a_xZZZZ_b",
		"tail_x00":              "tail_x00",
		"":                      "",
	} {
		if got := unescapeX(in); got != want {
			t.Errorf("unescapeX(%q) = %q, want %q", in, got, want)
		}
	}
}

// A formula whose text result is one or two characters ("OK", "○") keeps
// it in a .xls (review 2026-11-23).
func TestXLSShortFormulaString(t *testing.T) {
	strRes := cat([]byte{0, 0, 0, 0, 0, 0}, []byte{0xFF, 0xFF})
	sh := cat(bof(0x0600, 0x0010),
		formula(0, 0, 0, strRes), rec(0x0207, xlStr("OK", 2)),
		formula(0, 1, 0, strRes), rec(0x0207, xlStr("○", 2)),
		formula(0, 2, 0, strRes), rec(0x0207, xlStr("sw-a", 2)),
		rec(0x000A))
	ws := labXLS([][2]string{{"S", "0"}}, sh)
	b := mustRead(t, buildCFB(9, cfbStream{"Workbook", ws}), "xls")
	row := b.Sheets[0].Rows[0]
	if len(row) < 3 || row[0] != "OK" || row[1] != "○" || row[2] != "sw-a" {
		t.Fatalf("row = %q", row)
	}
}

// The same through a real .xlsx part.
func TestXLSXEscapedX(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	add := func(n, s string) { w, _ := z.Create(n); w.Write([]byte(s)) }
	add("xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="S" sheetId="1" r:id="rId1"/></sheets></workbook>`)
	add("xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`)
	add("xl/sharedStrings.xml", `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>uplink-to-sw-a_x000D_</t></si><si><t>a_x005F_x0041_b</t></si></sst>`)
	add("xl/worksheets/sheet1.xml", `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="inlineStr"><is><t>x_x0009_y</t></is></c></row></sheetData></worksheet>`)
	z.Close()
	b, err := Read(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r := b.Sheets[0].Rows[0]
	if r[0] != "uplink-to-sw-a\r" || r[1] != "a_x0041_b" || r[2] != "x\ty" {
		t.Fatalf("row = %q", r)
	}
}
