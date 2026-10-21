package sheet

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mustRead(t *testing.T, data []byte, format string) *Book {
	t.Helper()
	b, err := Read(data)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if b.Format != format {
		t.Fatalf("format = %q, want %q", b.Format, format)
	}
	return b
}

func checkRows(t *testing.T, s Sheet, want [][]string) {
	t.Helper()
	if !reflect.DeepEqual(s.Rows, want) {
		t.Errorf("sheet %q rows:\n got %q\nwant %q", s.Name, s.Rows, want)
	}
}

func sheetNames(b *Book) []string {
	var n []string
	for _, s := range b.Sheets {
		n = append(n, s.Name)
	}
	return n
}

func TestXLSBiff8(t *testing.T) {
	cases := []struct {
		name  string
		shift uint
		pad   int
	}{
		{"big stream, 512-byte sectors", 9, 5000},
		{"mini stream", 9, 0},
		{"big stream, 4096-byte sectors", 12, 5000},
		{"mini stream, 4096-byte sectors", 12, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := biff8Workbook(tc.pad)
			if (len(ws) < 4096) != (tc.pad == 0) {
				t.Fatalf("fixture size %d does not exercise the intended stream", len(ws))
			}
			b := mustRead(t, buildCFB(tc.shift, cfbStream{"Workbook", ws}, cfbStream{"\x05SummaryInformation", []byte("x")}), "xls")
			if len(b.Warnings) != 0 {
				t.Errorf("warnings: %q", b.Warnings)
			}
			if got := sheetNames(b); !reflect.DeepEqual(got, []string{"機器", "予備"}) {
				t.Fatalf("sheets = %q", got)
			}
			checkRows(t, b.Sheets[0], biff8Sheet1Want)
			if want := []Range{{7, 0, 8, 1}}; !reflect.DeepEqual(b.Sheets[0].Merges, want) {
				t.Errorf("merges = %v, want %v", b.Sheets[0].Merges, want)
			}
			checkRows(t, b.Sheets[1], [][]string{{"spare-sw02"}, {"core-sw01"}})
		})
	}
}

func TestXLSBiff5(t *testing.T) {
	b := mustRead(t, buildCFB(9, cfbStream{"BOOK", biff5Book()}), "xls")
	if got := sheetNames(b); !reflect.DeepEqual(got, []string{"機器"}) {
		t.Fatalf("sheets = %q", got)
	}
	checkRows(t, b.Sheets[0], [][]string{{"本社3F", "23"}})
}

func TestXLSTruncatedBIFF(t *testing.T) {
	ws := biff8Workbook(0)
	b, err := Read(buildCFB(9, cfbStream{"Workbook", ws[:len(ws)-12]}))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(b.Warnings) == 0 {
		t.Error("expected a warning for the cut-off sheet")
	}
	checkRows(t, b.Sheets[0], biff8Sheet1Want)
	checkRows(t, b.Sheets[1], [][]string{{"spare-sw02"}})
}

func TestXLSTruncatedFile(t *testing.T) {
	ws := biff8Workbook(5000)
	file := buildCFB(9, cfbStream{"Workbook", ws})
	tail := ws[len(ws)-12:]
	i := bytes.LastIndex(file, tail)
	if i < 0 {
		t.Fatal("stream tail not found in file")
	}
	b, err := Read(file[:i+4])
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(b.Warnings) == 0 {
		t.Error("expected warnings for the truncated file")
	}
	checkRows(t, b.Sheets[0], biff8Sheet1Want)
}

func TestXLSEncrypted(t *testing.T) {
	ws := biff8Workbook(0, rec(0x002F, u16(1), make([]byte, 52)))
	if _, err := Read(buildCFB(9, cfbStream{"Workbook", ws})); err == nil || err.Error() != errEncrypted.Error() {
		t.Errorf("err = %v, want password error", err)
	}
	// An encrypted xlsx is an OLE file with these streams.
	ole := buildCFB(9, cfbStream{"EncryptionInfo", make([]byte, 200)}, cfbStream{"EncryptedPackage", make([]byte, 5000)})
	if _, err := Read(ole); err == nil || err.Error() != errEncrypted.Error() {
		t.Errorf("encrypted xlsx: err = %v, want password error", err)
	}
}

func TestXLSX(t *testing.T) {
	b := mustRead(t, xlsxFixture(), "xlsx")
	if got := sheetNames(b); !reflect.DeepEqual(got, []string{"機器", "隠し"}) {
		t.Fatalf("sheets = %q", got)
	}
	checkRows(t, b.Sheets[0], [][]string{
		{"core-sw01", "本社3F ", "admin/x9/Lp2q"},
		{"ops", "192.0.2.10", "", "22", "1.5"},
		{"2025/01/01", "2025/01/01", "TRUE", "", "0.3"},
	})
	if want := []Range{{4, 0, 5, 1}}; !reflect.DeepEqual(b.Sheets[0].Merges, want) {
		t.Errorf("merges = %v, want %v", b.Sheets[0].Merges, want)
	}
	checkRows(t, b.Sheets[1], [][]string{{}, {"", "spare"}})
	if len(b.Warnings) != 0 {
		t.Errorf("warnings: %q", b.Warnings)
	}
}

func TestXLSXNoStyles(t *testing.T) {
	z := buildZip(
		[2]string{"xl/workbook.xml", `<workbook><sheets><sheet name="S" r:id="x"/></sheets></workbook>`},
		[2]string{"xl/worksheets/sheet1.xml", `<worksheet><sheetData><row><c s="1"><v>45658</v></c></row></sheetData></worksheet>`},
	)
	b := mustRead(t, z, "xlsx")
	checkRows(t, b.Sheets[0], [][]string{{"45658"}})
}

const htmlFixture = `<html><head><meta http-equiv="Content-Type" content="text/html; charset=Shift_JIS"></head><body>
<table border=1>
<tr><th colspan=2>機器</th><th>場所</th></tr>
<tr><td rowspan=2>core-sw01</td><td x:num>192.0.2.1</td><td>本社<br>
  3F</td></tr>
<tr><td>192.0.2.2</td><td>A&amp;B &lt;x&gt;
   多　空白&nbsp;</td></tr>
<tr><td>a<table><tr><td>in1</td><td>in2</td></tr></table></td><td style="mso-ignore:colspan" colspan=2>long text</td></tr>
</table>
<table><tr><td>second</td></tr></table>
</body></html>`

func TestHTMLShiftJIS(t *testing.T) {
	b := mustRead(t, sjis(htmlFixture), "html")
	if got := sheetNames(b); !reflect.DeepEqual(got, []string{"表1", "表2"}) {
		t.Fatalf("sheets = %q", got)
	}
	checkRows(t, b.Sheets[0], [][]string{
		{"機器", "", "場所"},
		{"core-sw01", "192.0.2.1", "本社\n3F"},
		{"", "192.0.2.2", "A&B <x> 多　空白"},
		{"a\nin1 in2", "long text"},
	})
	if want := []Range{{0, 0, 0, 1}, {1, 0, 2, 0}}; !reflect.DeepEqual(b.Sheets[0].Merges, want) {
		t.Errorf("merges = %v, want %v", b.Sheets[0].Merges, want)
	}
	checkRows(t, b.Sheets[1], [][]string{{"second"}})
}

func TestHTMLWithoutDeclaration(t *testing.T) {
	// What a system writes as "list.xls": a bare UTF-8 table fragment.
	b := mustRead(t, []byte("\xEF\xBB\xBF<table><tr><td>core-sw01</td><td>ops</td></tr></table>"), "html")
	checkRows(t, b.Sheets[0], [][]string{{"core-sw01", "ops"}})
	if _, err := Read([]byte("<html><body>no table</body></html>")); err == nil {
		t.Error("expected an error for HTML without a table")
	}
}

const xml2003Fixture = `<?xml version="1.0" encoding="Shift_JIS"?>
<?mso-application progid="Excel.Sheet"?>
<Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet" xmlns:html="http://www.w3.org/TR/REC-html40">
<Worksheet ss:Name="機器一覧"><Table>
<Row><Cell ss:MergeAcross="1"><Data ss:Type="String">機器</Data></Cell><Cell><Data ss:Type="String">IP</Data></Cell></Row>
<Row ss:Index="3"><Cell><Data ss:Type="String">core-sw01</Data></Cell><Cell ss:Index="3"><Data ss:Type="Number">22</Data></Cell><Cell><ss:Data ss:Type="String" xmlns="http://www.w3.org/TR/REC-html40"><B>192.0.2.</B><Font>1</Font></ss:Data></Cell></Row>
<Row><Cell><Data ss:Type="DateTime">2025-01-01T00:00:00.000</Data></Cell><Cell><Data ss:Type="Boolean">1</Data></Cell><Cell ss:MergeDown="1"><Data ss:Type="Error">#N/A</Data></Cell></Row>
</Table></Worksheet>
<Worksheet ss:Name="空"><Table></Table></Worksheet>
</Workbook>`

func TestXML2003(t *testing.T) {
	for _, enc := range []string{"utf8", "sjis"} {
		data := []byte(xml2003Fixture)
		if enc == "sjis" {
			data = sjis(xml2003Fixture)
		}
		b := mustRead(t, data, "xml2003")
		if got := sheetNames(b); !reflect.DeepEqual(got, []string{"機器一覧", "空"}) {
			t.Fatalf("%s: sheets = %q", enc, got)
		}
		checkRows(t, b.Sheets[0], [][]string{
			{"機器", "", "IP"},
			{},
			{"core-sw01", "", "22", "192.0.2.1"},
			{"2025/01/01", "TRUE"},
		})
		if want := []Range{{0, 0, 0, 1}, {3, 2, 4, 2}}; !reflect.DeepEqual(b.Sheets[0].Merges, want) {
			t.Errorf("%s: merges = %v, want %v", enc, b.Sheets[0].Merges, want)
		}
	}
}

func TestDelimited(t *testing.T) {
	const csvText = "機器名,IP\r\ncore-sw01,192.0.2.1\r\n\"本社,3F\",\"a\"\"b\"\r\n"
	csvWant := [][]string{{"機器名", "IP"}, {"core-sw01", "192.0.2.1"}, {"本社,3F", `a"b`}}
	b := mustRead(t, sjis(csvText), "csv")
	checkRows(t, b.Sheets[0], csvWant)
	if b.Sheets[0].Name != "CSV" {
		t.Errorf("name = %q", b.Sheets[0].Name)
	}
	b = mustRead(t, append([]byte{0xEF, 0xBB, 0xBF}, csvText...), "csv")
	checkRows(t, b.Sheets[0], csvWant)

	const tsvText = "機器名\tIP\tメモ\r\ncore-sw01\t192.0.2.1\t\"a,b\"\r\n"
	u16 := append([]byte{0xFF, 0xFE}, utf16le(tsvText)...)
	b = mustRead(t, u16, "tsv")
	checkRows(t, b.Sheets[0], [][]string{{"機器名", "IP", "メモ"}, {"core-sw01", "192.0.2.1", "a,b"}})
	if b.Sheets[0].Name != "TSV" {
		t.Errorf("name = %q", b.Sheets[0].Name)
	}

	b = mustRead(t, []byte("sep=;\nhost;user\ncore-sw01;ops\n"), "csv")
	checkRows(t, b.Sheets[0], [][]string{{"host", "user"}, {"core-sw01", "ops"}})
}

func TestSplitLinesFallback(t *testing.T) {
	sb := &sheetBuilder{name: "CSV"}
	n := splitLines(sb, "a,\"b\"\r\n\r\nc\"d,e\n", ',', 3)
	if n != 5 {
		t.Errorf("rows = %d", n)
	}
	if want := [][]string{nil, nil, nil, {"a", "b"}, {`c"d`, "e"}}; !reflect.DeepEqual(sb.rows, want) {
		t.Errorf("rows = %q", sb.rows)
	}
}

func TestNumberAndDateFormatting(t *testing.T) {
	for v, want := range map[float64]string{22: "22", 1.5: "1.5", -0.0: "0", 1e15: "1000000000000000", 0.1 + 0.2: "0.3"} {
		if got := formatNumber(v); got != want {
			t.Errorf("formatNumber(%v) = %q, want %q", v, got, want)
		}
	}
	for code, want := range map[string]bool{
		"yyyy/mm/dd": true, `yyyy"年"m"月"d"日"`: true, "[$-411]ggge年m月d日": true, "h:mm": true, "[h]:mm": true,
		"General": false, "#,##0": false, `0"days"`: false, "[Red]0.00": false, `\d0`: false, "0.00E+00": false,
	} {
		if got := isDateFormatCode(code); got != want {
			t.Errorf("isDateFormatCode(%q) = %v", code, got)
		}
	}
	for _, c := range []struct {
		v     float64
		d1904 bool
		want  string
	}{
		{45658, false, "2025/01/01"}, {45658.75, false, "2025/01/01 18:00"}, {1, false, "1900/01/01"},
		{61, false, "1900/03/01"}, {0.5, false, "12:00"}, {0, true, "1904/01/01"},
	} {
		if got, _ := formatSerialDate(c.v, c.d1904); got != c.want {
			t.Errorf("formatSerialDate(%v, %v) = %q, want %q", c.v, c.d1904, got, c.want)
		}
	}
}

func TestReadErrors(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("%PDF-1.4\x00\x01\x02"), oleMagic, buildCFB(9, cfbStream{"WordDocument", []byte("x")}), []byte("PK\x03\x04garbage")} {
		if _, err := Read(data); err == nil {
			t.Errorf("Read(%q...) succeeded", data[:min(len(data), 8)])
		}
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "list.xls")
	os.WriteFile(p, []byte("host\tuser\ncore-sw01\tops\n"), 0o600)
	b, err := ReadFile(p)
	if err != nil || b.Format != "tsv" {
		t.Errorf("ReadFile: %v %v", b, err)
	}
	if _, err := ReadFile(filepath.Join(dir, "missing.xls")); err == nil {
		t.Error("ReadFile of missing file succeeded")
	}
}

func allFixtures() map[string][]byte {
	return map[string][]byte{
		"xls-big":  buildCFB(9, cfbStream{"Workbook", biff8Workbook(5000)}),
		"xls-mini": buildCFB(9, cfbStream{"Workbook", biff8Workbook(0)}),
		"xls-4k":   buildCFB(12, cfbStream{"Workbook", biff8Workbook(5000)}),
		"xls-b5":   buildCFB(9, cfbStream{"Book", biff5Book()}),
		"xlsx":     xlsxFixture(),
		"html":     sjis(htmlFixture),
		"xml2003":  []byte(xml2003Fixture),
		"tsv16":    append([]byte{0xFF, 0xFE}, utf16le("a\tb\r\n\"c\td\"\te\r\n")...),
	}
}

// readNoPanic calls read (without Read's recover) so a panic fails the test
// with the input that caused it.
func readNoPanic(t *testing.T, label string, data []byte) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			path := filepath.Join(t.TempDir(), "crash.bin")
			os.WriteFile(path, data, 0o600)
			t.Fatalf("%s: panic: %v (input saved to %s)", label, r, path)
		}
	}()
	b, err := read(data)
	if err == nil && b == nil {
		t.Fatalf("%s: nil book without error", label)
	}
}

func TestRobustness(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		buf := make([]byte, rng.Intn(3000))
		rng.Read(buf)
		readNoPanic(t, fmt.Sprintf("random %d", i), buf)
		// Random bodies behind each magic number reach the binary parsers.
		readNoPanic(t, fmt.Sprintf("ole random %d", i), append(append([]byte{}, oleMagic...), buf...))
		readNoPanic(t, fmt.Sprintf("zip random %d", i), append([]byte("PK\x03\x04"), buf...))
	}
	for name, fx := range allFixtures() {
		step := max(1, len(fx)/150)
		for n := 0; n < len(fx); n += step {
			readNoPanic(t, fmt.Sprintf("%s prefix %d", name, n), fx[:n])
		}
		for i := 0; i < 400; i++ {
			m := bytes.Clone(fx)
			for k := rng.Intn(8) + 1; k > 0; k-- {
				m[rng.Intn(len(m))] = byte(rng.Intn(256))
			}
			readNoPanic(t, fmt.Sprintf("%s mutation %d", name, i), m)
		}
	}
}

func FuzzRead(f *testing.F) {
	for _, fx := range allFixtures() {
		f.Add(fx)
	}
	f.Add([]byte("a,b\n\"c"))
	f.Fuzz(func(t *testing.T, data []byte) {
		b, err := read(data)
		if err == nil && b == nil {
			t.Fatal("nil book without error")
		}
		if err != nil && strings.TrimSpace(err.Error()) == "" {
			t.Fatal("empty error message")
		}
	})
}
