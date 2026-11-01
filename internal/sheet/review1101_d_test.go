package sheet

import (
	"fmt"
	"strings"
	"testing"
)

// The cell budget of a whole file: a sheet past it is clipped and the
// warning names the file-wide cap, not the sheet's own.
func TestBookCellBudget(t *testing.T) {
	b := &Book{cells: maxBookCells - 10}
	s := &sheetBuilder{name: "s", book: b}
	s.set(0, 9, "x") // exactly the 10 slots left
	s.set(1, 0, "y") // one more: refused
	s.finish(b)
	// The refused cell's row exists (empty), as with the per-sheet cap.
	if got := b.Sheets[0].Rows; len(got) != 2 || got[0][9] != "x" || len(got[1]) != 0 {
		t.Errorf("rows = %q", got)
	}
	if len(b.Warnings) != 1 || !strings.Contains(b.Warnings[0], "ファイル全体のセル数が上限") {
		t.Errorf("warnings = %q", b.Warnings)
	}
	if b.cells != maxBookCells {
		t.Errorf("book cells = %d, want %d", b.cells, maxBookCells)
	}
}

// labXLS builds a BIFF8 workbook whose sheet directory is given as
// (name, sheet index) pairs over the sheets passed: two entries with the
// same index point at the same substream.
func labXLS(dirEntries [][2]string, sheets ...[]byte) []byte {
	globals := func(offs []uint32) []byte {
		g := cat(bof(0x0600, 0x0005), rec(0x0042, u16(1200)))
		for i, e := range dirEntries {
			var off uint32
			if len(offs) > 0 {
				var idx int
				fmt.Sscan(e[1], &idx)
				off = offs[idx]
			}
			_ = i
			g = cat(g, rec(0x0085, u32(off), []byte{0, 0}, xlStr(e[0], 1)))
		}
		return cat(g, rec(0x000A))
	}
	n := uint32(len(globals(nil)))
	offs := make([]uint32, len(sheets))
	for i, s := range sheets {
		offs[i] = n
		n += uint32(len(s))
	}
	return cat(append([][]byte{globals(offs)}, sheets...)...)
}

func labSheet(label string) []byte {
	return cat(bof(0x0600, 0x0010), rec(0x0204, cellHdr(0, 0, 0), xlStr(label, 2)), rec(0x000A))
}

// Two directory entries at the same offset are one sheet: read once.
func TestXLSDuplicateBoundSheet(t *testing.T) {
	ws := labXLS([][2]string{{"A", "0"}, {"B", "0"}, {"C", "1"}}, labSheet("x"), labSheet("y"))
	b := mustRead(t, buildCFB(9, cfbStream{"Workbook", ws}), "xls")
	if got := sheetNames(b); strings.Join(got, ",") != "A,C" {
		t.Errorf("sheets = %q", got)
	}
	checkRows(t, b.Sheets[0], [][]string{{"x"}})
	checkRows(t, b.Sheets[1], [][]string{{"y"}})
	if len(b.Warnings) != 1 || !strings.Contains(b.Warnings[0], "「B」は別のシートと同じ内容") {
		t.Errorf("warnings = %q", b.Warnings)
	}
}

// At most maxSheets sheets are read from one file.
func TestXLSSheetCountCapped(t *testing.T) {
	var entries [][2]string
	var sheets [][]byte
	for i := 0; i < maxSheets+20; i++ {
		entries = append(entries, [2]string{fmt.Sprintf("S%d", i), fmt.Sprint(i)})
		sheets = append(sheets, labSheet("v"))
	}
	ws := labXLS(entries, sheets...)
	b := mustRead(t, buildCFB(9, cfbStream{"Workbook", ws}), "xls")
	if len(b.Sheets) != maxSheets {
		t.Errorf("%d sheets read, want %d", len(b.Sheets), maxSheets)
	}
	if len(b.Warnings) != 1 || !strings.Contains(b.Warnings[0], "シートが多すぎる") {
		t.Errorf("warnings = %q", b.Warnings)
	}
}

func labXLSX(sheetsXML string) []byte {
	return buildZip(
		[2]string{"_rels/.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		[2]string{"xl/workbook.xml", `<?xml version="1.0"?><workbook ` + xlsxNS + `><sheets>` + sheetsXML + `</sheets></workbook>`},
		[2]string{"xl/_rels/workbook.xml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="/xl/worksheets/sheet1.xml"/></Relationships>`},
		[2]string{"xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet ` + xlsxNS + `><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>x</t></is></c></row></sheetData></worksheet>`},
	)
}

// Two <sheet> entries whose relationships point at the same part (one by
// a relative, one by an absolute target) are one sheet: read once.
func TestXLSXDuplicateSheetTarget(t *testing.T) {
	b := mustRead(t, labXLSX(`<sheet name="A" sheetId="1" r:id="rId1"/><sheet name="B" sheetId="2" r:id="rId2"/><sheet name="C" sheetId="3" r:id="rId1"/>`), "xlsx")
	if got := sheetNames(b); strings.Join(got, ",") != "A" {
		t.Errorf("sheets = %q", got)
	}
	checkRows(t, b.Sheets[0], [][]string{{"x"}})
	if len(b.Warnings) != 2 {
		t.Errorf("warnings = %q", b.Warnings)
	}
}

// At most maxSheets sheets are read from an xlsx too.
func TestXLSXSheetCountCapped(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxSheets+20; i++ {
		fmt.Fprintf(&sb, `<sheet name="S%d" sheetId="%d" r:id="rIdX%d"/>`, i, i+1, i)
	}
	b := mustRead(t, labXLSX(sb.String()), "xlsx")
	// (The sheet-count warning itself is behind the per-sheet ones here,
	// past the warning cap.)
	if len(b.Sheets) != maxSheets {
		t.Errorf("%d sheets read, want %d", len(b.Sheets), maxSheets)
	}
}

// Excel's "sep=" line names one ASCII character; a full-width one is not
// taken as the delimiter (and the line is then data like any other).
func TestDelimitedSepLineASCIIOnly(t *testing.T) {
	b := mustRead(t, []byte("sep=;\r\na;b\r\n"), "csv")
	checkRows(t, b.Sheets[0], [][]string{{"a", "b"}})
	b = mustRead(t, []byte("sep=；\r\na,b\r\n"), "csv")
	checkRows(t, b.Sheets[0], [][]string{{"sep=；"}, {"a", "b"}})
}
