package sheet

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
)

// Text formats: HTML, SpreadsheetML 2003 and delimited text. Business
// systems often save these with an .xls extension, so they are told apart
// by their first bytes.

func decodeWith(d *encoding.Decoder, b []byte) string {
	out, err := d.Bytes(b)
	if err != nil {
		return toValidUTF8(b)
	}
	return string(out)
}

func decodeShiftJIS(b []byte) string {
	return decodeWith(japanese.ShiftJIS.NewDecoder(), b)
}

func toValidUTF8(b []byte) string {
	return strings.ToValidUTF8(string(b), string(utf8.RuneError))
}

func decodeUTF16(b []byte, bigEndian bool) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		if bigEndian {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
	}
	return string(utf16.Decode(u))
}

// looksUTF16LE catches BOM-less UTF-16LE text (ASCII-heavy content leaves a
// zero in every odd byte).
func looksUTF16LE(b []byte) bool {
	n := min(len(b), 512) &^ 1
	if n < 4 {
		return false
	}
	zeros := 0
	for i := 1; i < n; i += 2 {
		if b[i] == 0 && b[i-1] != 0 {
			zeros++
		}
	}
	return zeros*10 >= (n/2)*7
}

var (
	reHTMLCharset = regexp.MustCompile(`(?i)<meta[^>]*charset\s*=\s*["']?\s*([A-Za-z0-9_.:\-]+)`)
	reXMLEncoding = regexp.MustCompile(`(?i)<\?xml[^>]*encoding\s*=\s*["']([A-Za-z0-9_.:\-]+)`)
)

const sniffLen = 64 << 10

// sniffMarkup returns "xml2003", "html" or "" (delimited text) from the
// start of the (ASCII-compatible) text.
func sniffMarkup(head string) string {
	if len(head) > sniffLen {
		head = head[:sniffLen]
	}
	h := strings.ToLower(strings.TrimLeft(head, " \t\r\n\x00"+string(rune(0xFEFF))))
	if strings.HasPrefix(h, "<") && strings.Contains(h, "urn:schemas-microsoft-com:office:spreadsheet") &&
		(strings.HasPrefix(h, "<?xml") || strings.Contains(h, "workbook")) && !strings.HasPrefix(h, "<html") {
		return "xml2003"
	}
	if strings.HasPrefix(h, "<html") || strings.HasPrefix(h, "<!doctype html") || strings.Contains(h, "<table") {
		return "html"
	}
	return ""
}

func readText(data []byte) (*Book, error) {
	var text, kind string
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		text = decodeUTF16(data[2:], false)
		kind = sniffMarkup(text)
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		text = decodeUTF16(data[2:], true)
		kind = sniffMarkup(text)
	case looksUTF16LE(data):
		text = decodeUTF16(data, false)
		kind = sniffMarkup(text)
	default:
		raw := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
		head := raw[:min(len(raw), sniffLen)]
		kind = sniffMarkup(string(head))
		if kind == "" && bytes.IndexByte(head, 0) >= 0 {
			return nil, errFormat // binary: not text whatever the extension says
		}
		text = decodeBytes(raw, kind, len(raw) < len(data))
	}
	switch kind {
	case "xml2003":
		return readXML2003(text)
	case "html":
		return readHTML(text)
	}
	return readDelimited(text)
}

// decodeBytes picks the text encoding: UTF-8 when valid (or BOM-marked),
// else the charset the markup declares, else Shift_JIS — what Japanese
// Excel and most Japanese business systems write.
func decodeBytes(raw []byte, kind string, hadBOM bool) string {
	if hadBOM || utf8.Valid(raw) {
		return toValidUTF8(raw)
	}
	head := raw[:min(len(raw), 4096)]
	var m [][]byte
	switch kind {
	case "html":
		m = reHTMLCharset.FindSubmatch(head)
	case "xml2003":
		m = reXMLEncoding.FindSubmatch(head)
	}
	if m != nil {
		if enc, err := htmlindex.Get(string(m[1])); err == nil && enc != unicode.UTF8 {
			return decodeWith(enc.NewDecoder(), raw)
		}
	}
	return decodeShiftJIS(raw)
}

// readDelimited parses CSV or TSV. The delimiter is whichever of tab and
// comma dominates the first lines; Excel's "sep=;" first line is honoured.
func readDelimited(text string) (*Book, error) {
	comma := ','
	first, rest, _ := strings.Cut(text, "\n")
	first = strings.TrimRight(first, "\r")
	if strings.HasPrefix(strings.ToLower(first), "sep=") && utf8.RuneCountInString(first) == 5 {
		r, _ := utf8.DecodeRuneInString(first[4:])
		if r != '"' && r != '\r' && r != '\n' && r != utf8.RuneError {
			comma, text = r, rest
		}
	} else if guessTab(text) {
		comma = '\t'
	}
	format := "csv"
	if comma == '\t' {
		format = "tsv"
	}
	b := newBook(format)
	sb := &sheetBuilder{name: strings.ToUpper(format)}

	cr := csv.NewReader(strings.NewReader(text))
	cr.Comma = comma
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	row := 0
	for row < maxGridRows {
		off := cr.InputOffset()
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if !errors.As(err, &pe) {
				break
			}
			b.warn("区切り文字の解析に失敗した行以降は、引用符を無視して単純に分割しました")
			row = splitLines(sb, text[min(int(off), len(text)):], comma, row)
			break
		}
		for c, v := range rec {
			sb.set(row, c, v)
		}
		if len(rec) > maxGridCols {
			sb.clipped = true
		}
		row++
	}
	if row >= maxGridRows {
		sb.clipped = true
	}
	sb.finish(b)
	return b, nil
}

func guessTab(text string) bool {
	tabs, commas, lines := 0, 0, 0
	for line := range strings.SplitSeq(text[:min(len(text), sniffLen)], "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		tabs += strings.Count(line, "\t")
		commas += strings.Count(line, ",")
		if lines++; lines >= 10 {
			break
		}
	}
	return tabs > 0 && tabs >= commas
}

// splitLines is the fallback for text encoding/csv cannot parse: one record
// per line, fields split on the delimiter, surrounding quotes removed.
func splitLines(sb *sheetBuilder, text string, comma rune, row int) int {
	for line := range strings.SplitSeq(text, "\n") {
		if row >= maxGridRows {
			sb.clipped = true
			break
		}
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		for c, f := range strings.Split(line, string(comma)) {
			if len(f) >= 2 && f[0] == '"' && f[len(f)-1] == '"' {
				f = strings.ReplaceAll(f[1:len(f)-1], `""`, `"`)
			}
			sb.set(row, c, f)
		}
		row++
	}
	return row
}
