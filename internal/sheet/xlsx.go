package sheet

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"
)

// .xlsx (Office Open XML) reader on archive/zip + encoding/xml. Decoders run
// non-strict and stop at the first unrecoverable XML error, keeping what was
// read up to there.

const (
	maxZipTotal = 256 << 20 // decompressed bytes over the whole file
	maxZipEntry = 128 << 20 // decompressed bytes of one part
)

var (
	errXLSB    = errors.New("xlsb 形式には対応していません。Excel で xlsx 形式に保存し直してください")
	errZipBomb = errors.New("展開後のサイズが大きすぎるため読み込めません")
)

type xlsxReader struct {
	files map[string]*zip.File // lower-cased, slash-normalised names
	total int64
	book  *Book
}

type xlsxRel struct{ target, typ string }

func normPart(name string) string {
	return strings.ToLower(strings.TrimLeft(strings.ReplaceAll(name, `\`, "/"), "/"))
}

func readXLSX(data []byte) (*Book, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errFormat
	}
	x := &xlsxReader{files: map[string]*zip.File{}, book: newBook("xlsx")}
	for _, f := range zr.File {
		if n := normPart(f.Name); x.files[n] == nil {
			x.files[n] = f
		}
	}

	wbPath := "xl/workbook.xml"
	for _, rel := range x.rels("_rels/.rels", "") {
		if strings.HasSuffix(rel.typ, "/officeDocument") {
			wbPath = rel.target
			break
		}
	}
	wb, err := x.part(wbPath)
	if err != nil && wbPath != "xl/workbook.xml" {
		wbPath = "xl/workbook.xml"
		wb, err = x.part(wbPath)
	}
	if err != nil {
		if errors.Is(err, errZipBomb) {
			return nil, err
		}
		if x.files["xl/workbook.bin"] != nil {
			return nil, errXLSB
		}
		return nil, errFormat
	}

	rels := x.rels(relsPath(wbPath), path.Dir(wbPath))
	sstPath, stylesPath := path.Join(path.Dir(wbPath), "sharedStrings.xml"), path.Join(path.Dir(wbPath), "styles.xml")
	for _, rel := range rels {
		switch {
		case strings.HasSuffix(rel.typ, "/sharedStrings"):
			sstPath = rel.target
		case strings.HasSuffix(rel.typ, "/styles"):
			stylesPath = rel.target
		}
	}
	sst := x.sharedStrings(sstPath)
	st := x.styles(stylesPath)
	if st != nil {
		st.date1904 = workbookDate1904(wb)
	}

	for i, ws := range parseWorkbookSheets(wb) {
		target := ""
		if rel, ok := rels[ws.rid]; ok {
			if rel.typ != "" && !strings.HasSuffix(rel.typ, "/worksheet") {
				continue // chart, dialog and macro sheets hold no cell grid
			}
			target = rel.target
		} else {
			target = path.Join(path.Dir(wbPath), "worksheets", "sheet"+strconv.Itoa(i+1)+".xml")
		}
		name := ws.name
		if name == "" {
			name = sheetName(i + 1)
		}
		x.readSheet(target, name, sst, st)
	}
	if len(x.book.Sheets) == 0 {
		x.book.warn("シートが見つかりませんでした")
	}
	return x.book, nil
}

func relsPath(part string) string {
	return path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
}

// part returns a part's decompressed bytes. A part that cannot be read to
// the end returns what was read with a non-nil error.
func (x *xlsxReader) part(name string) ([]byte, error) {
	f := x.files[normPart(name)]
	if f == nil {
		return nil, errFormat
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	limit := min(int64(maxZipEntry), maxZipTotal-x.total)
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	x.total += int64(len(b))
	if int64(len(b)) > limit {
		return nil, errZipBomb
	}
	return b, err
}

func newXMLDecoder(b []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return d
}

func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// rels parses a relationships part; targets are resolved against base.
func (x *xlsxReader) rels(name, base string) map[string]xlsxRel {
	out := map[string]xlsxRel{}
	b, _ := x.part(name)
	if len(b) == 0 {
		return out
	}
	d := newXMLDecoder(b)
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Relationship" || strings.EqualFold(attr(se, "TargetMode"), "External") {
			continue
		}
		t := strings.ReplaceAll(attr(se, "Target"), `\`, "/")
		if strings.HasPrefix(t, "/") {
			t = strings.TrimLeft(t, "/")
		} else {
			t = path.Join(base, t)
		}
		out[attr(se, "Id")] = xlsxRel{target: t, typ: attr(se, "Type")}
	}
	return out
}

type wbSheet struct{ name, rid string }

// workbookDate1904 reads <workbookPr date1904="1"/> ("true" also).
func workbookDate1904(b []byte) bool {
	d := newXMLDecoder(b)
	for {
		tok, err := d.Token()
		if err != nil {
			return false
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "workbookPr" {
			v := strings.TrimSpace(attr(se, "date1904"))
			return v == "1" || strings.EqualFold(v, "true")
		}
	}
}

func parseWorkbookSheets(b []byte) []wbSheet {
	var out []wbSheet
	d := newXMLDecoder(b)
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
			rid := ""
			for _, a := range se.Attr {
				if a.Name.Local == "id" && a.Name.Space != "" {
					rid = a.Value
				}
			}
			out = append(out, wbSheet{name: attr(se, "name"), rid: rid})
		}
	}
	return out
}

func (x *xlsxReader) sharedStrings(name string) []string {
	b, err := x.part(name)
	if len(b) == 0 {
		return nil
	}
	if err != nil {
		x.book.warn("共有文字列の一部を読み取れませんでした")
	}
	var out []string
	var sb strings.Builder
	inSI, inT, rph := false, false, 0
	d := newXMLDecoder(b)
	for {
		tok, err := d.Token()
		if err != nil {
			if err != io.EOF {
				x.book.warn("共有文字列の一部を読み取れませんでした")
			}
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				sb.Reset()
			case "rPh":
				rph++ // phonetic (furigana) runs are not part of the value
			case "t":
				inT = inSI && rph == 0
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				if inSI {
					out = append(out, sb.String())
				}
				inSI, inT = false, false
			case "rPh":
				rph = max(rph-1, 0)
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				sb.Write(t)
			}
		}
	}
	return out
}

type xlsxStyles struct {
	numFmts map[int]string
	xfFmt   []int
	// date1904: the workbook counts dates from 1904 (Mac Excel's
	// <workbookPr date1904="1"/>), as BIFF's DATEMODE says for xls.
	date1904 bool
}

// code is a cell style's custom number format ("" for none or a
// built-in one).
func (s *xlsxStyles) code(xf int) string {
	if s == nil || xf < 0 || xf >= len(s.xfFmt) {
		return ""
	}
	return s.numFmts[s.xfFmt[xf]]
}

func (s *xlsxStyles) isDate(xf int) bool {
	if s == nil || xf < 0 || xf >= len(s.xfFmt) {
		return false
	}
	id := s.xfFmt[xf]
	if code, ok := s.numFmts[id]; ok {
		return isDateFormatCode(code)
	}
	return isBuiltinDateFmt(id)
}

func (x *xlsxReader) styles(name string) *xlsxStyles {
	b, _ := x.part(name)
	if len(b) == 0 {
		return nil
	}
	s := &xlsxStyles{numFmts: map[int]string{}}
	inXfs := false
	d := newXMLDecoder(b)
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "numFmt":
				if id, err := strconv.Atoi(attr(t, "numFmtId")); err == nil {
					s.numFmts[id] = attr(t, "formatCode")
				}
			case "cellXfs":
				inXfs = true
			case "xf":
				if inXfs {
					id, _ := strconv.Atoi(attr(t, "numFmtId"))
					s.xfFmt = append(s.xfFmt, id)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "cellXfs" {
				inXfs = false
			}
		}
	}
	return s
}

func (x *xlsxReader) readSheet(target, name string, sst []string, st *xlsxStyles) {
	sb := &sheetBuilder{name: name}
	defer sb.finish(x.book)
	b, err := x.part(target)
	if errors.Is(err, errZipBomb) {
		x.book.warn("シート「%s」は展開後のサイズが大きすぎるため読み込んでいません", name)
		return
	}
	if len(b) == 0 {
		x.book.warn("シート「%s」を読み取れませんでした", name)
		return
	}
	if err != nil {
		x.book.warn("シート「%s」の一部を読み取れませんでした（ファイルが壊れています）", name)
	}

	row, col := -1, -1
	var (
		inCell, inV, inIS, inT bool
		rph                    int
		cr, cc, style          int
		typ                    string
		v, is                  strings.Builder
	)
	d := newXMLDecoder(b)
	for {
		tok, err := d.Token()
		if err != nil {
			if err != io.EOF {
				x.book.warn("シート「%s」の一部を読み取れませんでした（XML が壊れています）", name)
			}
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				if n, err := strconv.Atoi(attr(t, "r")); err == nil && n >= 1 {
					row = n - 1
				} else {
					row++
				}
				col = -1
			case "c":
				if r, c, ok := parseCellRef(attr(t, "r")); ok {
					row, col = r, c
				} else {
					col++
				}
				row = max(row, 0)
				inCell, cr, cc = true, row, col
				typ = attr(t, "t")
				style, _ = strconv.Atoi(attr(t, "s"))
				v.Reset()
				is.Reset()
			case "v":
				inV = inCell
			case "is":
				inIS = inCell
			case "rPh":
				rph++
			case "t":
				inT = inIS && rph == 0
			case "mergeCell":
				if r1, c1, r2, c2, ok := parseRangeRef(attr(t, "ref")); ok {
					sb.merge(r1, c1, r2, c2)
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "is":
				inIS = false
			case "rPh":
				rph = max(rph-1, 0)
			case "t":
				inT = false
			case "c":
				if inCell {
					sb.set(cr, cc, xlsxValue(typ, v.String(), is.String(), style, sst, st))
				}
				inCell = false
			}
		case xml.CharData:
			if inV {
				v.Write(t)
			} else if inT {
				is.Write(t)
			}
		}
	}
}

func xlsxValue(typ, v, is string, style int, sst []string, st *xlsxStyles) string {
	switch typ {
	case "s":
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && i >= 0 && i < len(sst) {
			return sst[i]
		}
		return ""
	case "inlineStr":
		if is == "" {
			return v
		}
		return is
	case "str":
		return v
	case "b":
		switch strings.TrimSpace(v) {
		case "1", "true":
			return "TRUE"
		case "0", "false":
			return "FALSE"
		}
		return ""
	case "e":
		return ""
	case "d":
		return formatISODateTime(v)
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return v
	}
	if st.isDate(style) {
		if s, ok := formatSerialDate(f, st.date1904); ok {
			return s
		}
	}
	if s, ok := formatPadded(f, st.code(style)); ok {
		return s
	}
	return formatNumber(f)
}

// parseCellRef parses "B3" (or "$B$3") into 0-based row and column.
func parseCellRef(ref string) (row, col int, ok bool) {
	ref = strings.ReplaceAll(strings.TrimSpace(ref), "$", "")
	i := 0
	for i < len(ref) && i < 4 {
		ch := ref[i] | 0x20
		if ch < 'a' || ch > 'z' {
			break
		}
		col = col*26 + int(ch-'a'+1)
		i++
	}
	if i == 0 || i == len(ref) {
		return 0, 0, false
	}
	r, err := strconv.Atoi(ref[i:])
	if err != nil || r < 1 || r > 1<<24 {
		return 0, 0, false
	}
	return r - 1, col - 1, true
}

func parseRangeRef(ref string) (r1, c1, r2, c2 int, ok bool) {
	a, b, found := strings.Cut(ref, ":")
	if !found {
		return
	}
	var ok1, ok2 bool
	r1, c1, ok1 = parseCellRef(a)
	r2, c2, ok2 = parseCellRef(b)
	return r1, c1, r2, c2, ok1 && ok2
}
