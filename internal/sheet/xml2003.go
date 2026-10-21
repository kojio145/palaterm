package sheet

import (
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// SpreadsheetML 2003 ("XML スプレッドシート 2003"), another format systems
// emit under an .xls name. Row and cell positions are 1-based ss:Index
// attributes that may skip; cells covered by a merge are not written.

func readXML2003(text string) (*Book, error) {
	b := newBook("xml2003")
	d := newXMLDecoder([]byte(text))
	var (
		sb               *sheetBuilder
		row, col, across int
		inData           bool
		typ              string
		data             strings.Builder
		sheets           int
	)
	finish := func() {
		if sb != nil {
			sb.finish(b)
			sb = nil
		}
	}
	inComment := 0
	for {
		tok, err := d.Token()
		if err != nil {
			if err != io.EOF {
				if sb != nil {
					b.warn("シート「%s」の一部を読み取れませんでした（XML が壊れています）", sb.name)
				} else {
					b.warn("ファイルの一部を読み取れませんでした（XML が壊れています）")
				}
			}
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Worksheet":
				finish()
				sheets++
				name := attr(t, "Name")
				if name == "" {
					name = sheetName(sheets)
				}
				sb = &sheetBuilder{name: name}
				row, col = -1, -1
			case "Row":
				row = indexAttr(t, row+1)
				col = -1
			case "Cell":
				if sb == nil {
					continue
				}
				row = max(row, 0)
				col = indexAttr(t, col+1)
				across = spanCount(attr(t, "MergeAcross"))
				down := spanCount(attr(t, "MergeDown"))
				sb.merge(row, col, row+down, col+across)
			case "Comment":
				// A cell's note carries a <Data> of its own: not the value.
				inComment++
			case "Data":
				if sb != nil && inComment == 0 {
					inData = true
					typ = attr(t, "Type")
					data.Reset()
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "Comment":
				if inComment > 0 {
					inComment--
				}
			case "Data":
				if inData {
					sb.set(row, col, xml2003Value(typ, data.String()))
				}
				inData = false
			case "Cell":
				col += across // the next cell starts after the merged block
				across = 0
			case "Worksheet":
				finish()
			}
		case xml.CharData:
			if inData {
				data.Write(t) // includes text inside rich <Font>/<B> children
			}
		}
	}
	finish()
	if len(b.Sheets) == 0 {
		return nil, errFormat
	}
	return b, nil
}

// indexAttr returns the 0-based position from a 1-based ss:Index, or def.
func indexAttr(se xml.StartElement, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(attr(se, "Index"))); err == nil && n >= 1 && n <= 1<<24 {
		return n - 1
	}
	return def
}

func spanCount(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return min(n, 1<<20)
}

func xml2003Value(typ, v string) string {
	switch typ {
	case "Number":
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return formatNumber(f)
		}
	case "DateTime":
		return formatISODateTime(v)
	case "Boolean":
		switch strings.TrimSpace(v) {
		case "1":
			return "TRUE"
		case "0":
			return "FALSE"
		}
	case "Error":
		return ""
	}
	return v
}
