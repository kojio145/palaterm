package sheet

import (
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

// BIFF5/BIFF8 (.xls) reader. Only the records that carry cell values,
// sheet names, merges and the date formatting of numbers are interpreted;
// everything else is skipped by its length.

const (
	recFormula     = 0x0006
	recEOF         = 0x000A
	recDateMode    = 0x0022
	recFilePass    = 0x002F
	recContinue    = 0x003C
	recCodePage    = 0x0042
	recBoundSheet  = 0x0085
	recMulRK       = 0x00BD
	recRString     = 0x00D6
	recXF          = 0x00E0
	recMergedCells = 0x00E5
	recSST         = 0x00FC
	recLabelSST    = 0x00FD
	recNumber      = 0x0203
	recLabel       = 0x0204
	recBoolErr     = 0x0205
	recString      = 0x0207
	recRK          = 0x027E
	recFormat      = 0x041E
	recBOF         = 0x0809
)

var errOldExcel = errors.New("Excel 4.0 以前の古い形式のため読み込めません。Excel で xlsx 形式に保存し直してください")

func readOLE(data []byte) (*Book, error) {
	cf, err := openCFB(data)
	if err != nil {
		return nil, err
	}
	// Password-protected xlsx/docx are OLE files holding the encrypted package.
	if cf.find("EncryptedPackage") != nil || cf.find("EncryptionInfo") != nil {
		return nil, errEncrypted
	}
	e, biff5Name := cf.find("Workbook"), false
	if e == nil {
		if e = cf.find("Book"); e == nil {
			return nil, errFormat
		}
		biff5Name = true
	}
	ws, truncated := cf.stream(e)
	b := newBook("xls")
	if truncated || cf.damaged {
		b.warn("ファイルの一部が壊れているか途中で切れているため、読み取れた範囲だけを読み込みました")
	}
	if err := parseBIFF(ws, biff5Name, b); err != nil {
		return nil, err
	}
	return b, nil
}

type recStatus int

const (
	recOK recStatus = iota
	recAtEnd
	recCut // header or body runs past the end of the stream
)

func readRec(ws []byte, pos int) (id uint16, body []byte, next int, st recStatus) {
	if pos >= len(ws) {
		return 0, nil, pos, recAtEnd
	}
	if pos+4 > len(ws) {
		return 0, nil, pos, recCut
	}
	id = le16(ws[pos:])
	n := int(le16(ws[pos+2:]))
	if pos+4+n > len(ws) {
		return id, nil, pos, recCut
	}
	return id, ws[pos+4 : pos+4+n], pos + 4 + n, recOK
}

type biff struct {
	ws       []byte
	biff8    bool
	decode   func([]byte) string // 8-bit strings of BIFF5 (codepage)
	date1904 bool
	formats  map[uint16]string
	xfFmt    []uint16
	dateXF   map[uint16]bool
	sst      []string
	book     *Book
}

type boundSheet struct {
	name string
	pos  int
}

func parseBIFF(ws []byte, biff5Name bool, b *Book) error {
	id, body, _, st := readRec(ws, 0)
	if st != recOK || id != recBOF || len(body) < 2 {
		switch id {
		case 0x0009, 0x0209, 0x0409:
			return errOldExcel
		}
		return errFormat
	}
	r := &biff{ws: ws, decode: decodeShiftJIS, formats: map[uint16]string{}, dateXF: map[uint16]bool{}, book: b}
	switch le16(body) {
	case 0x0600:
		r.biff8 = true
	case 0x0500:
		r.biff8 = false
	default:
		r.biff8 = !biff5Name
	}

	var sheets []boundSheet
	pos, globalsEnd := 0, -1
globals:
	for {
		id, body, next, st := readRec(ws, pos)
		if st == recCut {
			b.warn("ブックの設定部分が途中で切れています")
			break
		}
		if st == recAtEnd {
			break
		}
		pos = next
		switch id {
		case recFilePass:
			return errEncrypted
		case recCodePage:
			if len(body) >= 2 {
				r.decode = codepageDecoder(le16(body))
			}
		case recDateMode:
			r.date1904 = len(body) >= 2 && le16(body) == 1
		case recFormat:
			r.readFormat(body)
		case recXF:
			if len(body) >= 4 {
				r.xfFmt = append(r.xfFmt, le16(body[2:]))
			}
		case recBoundSheet:
			if bs, ok := r.readBoundSheet(body); ok {
				sheets = append(sheets, bs)
			}
		case recSST:
			chunks := [][]byte{body}
			chunks, pos = r.continues(chunks, pos)
			r.readSST(chunks)
		case recEOF:
			globalsEnd = pos
			break globals
		}
	}

	if len(sheets) == 0 && globalsEnd > 0 {
		// No sheet directory: take the substreams that follow, in order.
		for p, n := globalsEnd, 1; p < len(ws) && n <= 256; n++ {
			id, _, _, st := readRec(ws, p)
			if st != recOK || id != recBOF {
				break
			}
			p = r.readSheet(p, sheetName(n))
		}
		return nil
	}
	for _, s := range sheets {
		if s.pos <= 0 || s.pos >= len(ws) {
			b.warn("シート「%s」を読み取れませんでした（位置情報が壊れています）", s.name)
			(&sheetBuilder{name: s.name}).finish(b)
			continue
		}
		r.readSheet(s.pos, s.name)
	}
	return nil
}

func sheetName(n int) string {
	return "Sheet" + strconv.Itoa(n)
}

// continues appends the bodies of the CONTINUE records that follow pos.
func (r *biff) continues(chunks [][]byte, pos int) ([][]byte, int) {
	for {
		id, body, next, st := readRec(r.ws, pos)
		if st != recOK || id != recContinue {
			return chunks, pos
		}
		chunks = append(chunks, body)
		pos = next
	}
}

func (r *biff) readFormat(body []byte) {
	if len(body) < 3 {
		return
	}
	id := le16(body)
	if r.biff8 {
		if s, ok := readXLString(&contReader{chunks: [][]byte{body[2:]}}, 2); ok {
			r.formats[id] = s
		}
		return
	}
	n := int(body[2])
	r.formats[id] = r.decode(clip(body[3:], n))
}

func (r *biff) readBoundSheet(body []byte) (boundSheet, bool) {
	if len(body) < 7 {
		return boundSheet{}, false
	}
	pos := int(le32(body))
	if body[5] != 0 { // 0 = worksheet; skip charts, macro and VB sheets
		return boundSheet{}, false
	}
	var name string
	if r.biff8 {
		s, _ := readXLString(&contReader{chunks: [][]byte{body[6:]}}, 1)
		name = s
	} else {
		name = r.decode(clip(body[7:], int(body[6])))
	}
	if pos > math.MaxInt32 {
		pos = -1
	}
	return boundSheet{name: name, pos: pos}, true
}

func (r *biff) readSST(chunks [][]byte) {
	if len(chunks[0]) < 8 {
		return
	}
	unique := int(le32(chunks[0][4:]))
	c := &contReader{chunks: chunks, off: 8}
	r.sst = make([]string, 0, min(max(unique, 0), 1<<16))
	for unique <= 0 || len(r.sst) < unique {
		if c.atEnd() {
			break
		}
		s, ok := readXLString(c, 2)
		if !ok {
			if s != "" {
				r.sst = append(r.sst, s)
			}
			break
		}
		r.sst = append(r.sst, s)
	}
	if unique > 0 && len(r.sst) < unique {
		r.book.warn("共有文字列の一部を読み取れませんでした（%d / %d 件）", len(r.sst), unique)
	}
}

// str decodes a cell string record body (LABEL, RSTRING, STRING) starting
// at the string; chunks after the first are its CONTINUE records.
func (r *biff) str(chunks [][]byte) string {
	if r.biff8 {
		s, _ := readXLString(&contReader{chunks: chunks}, 2)
		return s
	}
	b := chunks[0]
	if len(b) < 2 {
		return ""
	}
	return r.decode(clip(b[2:], int(le16(b))))
}

func clip(b []byte, n int) []byte {
	if n < len(b) {
		return b[:n]
	}
	return b
}

func (r *biff) isDate(xf uint16) bool {
	if v, ok := r.dateXF[xf]; ok {
		return v
	}
	v := false
	if int(xf) < len(r.xfFmt) {
		id := r.xfFmt[xf]
		if code, ok := r.formats[id]; ok {
			v = isDateFormatCode(code)
		} else {
			v = isBuiltinDateFmt(int(id))
		}
	}
	r.dateXF[xf] = v
	return v
}

func (r *biff) num(v float64, xf uint16) string {
	if r.isDate(xf) {
		if s, ok := formatSerialDate(v, r.date1904); ok {
			return s
		}
	}
	if int(xf) < len(r.xfFmt) {
		if s, ok := formatPadded(v, r.formats[r.xfFmt[xf]]); ok {
			return s
		}
	}
	return formatNumber(v)
}

func rkValue(rk uint32) float64 {
	var v float64
	if rk&2 != 0 {
		v = float64(int32(rk) >> 2)
	} else {
		v = math.Float64frombits(uint64(rk&0xFFFFFFFC) << 32)
	}
	if rk&1 != 0 {
		v /= 100
	}
	return v
}

// readSheet parses one worksheet substream starting at its BOF and returns
// the position after its EOF.
func (r *biff) readSheet(pos int, name string) int {
	sb := &sheetBuilder{name: name}
	defer sb.finish(r.book)
	depth := 0
	type cellPos struct{ row, col int }
	var pending *cellPos // FORMULA whose string result follows in STRING
	for {
		id, body, next, st := readRec(r.ws, pos)
		if st == recCut {
			r.book.warn("シート「%s」の一部を読み取れませんでした（ファイルが途中で切れています）", name)
			return len(r.ws)
		}
		if st == recAtEnd {
			if depth > 0 {
				r.book.warn("シート「%s」の終わりが見つかりませんでした（読み取れた範囲だけを読み込みました）", name)
			}
			return pos
		}
		pos = next
		switch id {
		case recBOF:
			depth++
			continue
		case recEOF:
			depth--
			if depth <= 0 {
				return pos
			}
			continue
		}
		if depth > 1 {
			continue // an embedded chart's own substream
		}
		if len(body) < 6 && id != recMergedCells {
			continue
		}
		var row, col int
		var xf uint16
		if len(body) >= 6 {
			row, col, xf = int(le16(body)), int(le16(body[2:])), le16(body[4:])
		}
		switch id {
		case recLabelSST:
			if len(body) >= 10 {
				if i := le32(body[6:]); int64(i) < int64(len(r.sst)) {
					sb.set(row, col, r.sst[i])
				}
			}
		case recLabel, recRString:
			chunks := [][]byte{body[6:]}
			chunks, pos = r.continues(chunks, pos)
			sb.set(row, col, r.str(chunks))
		case recNumber:
			if len(body) >= 14 {
				sb.set(row, col, r.num(math.Float64frombits(binary.LittleEndian.Uint64(body[6:])), xf))
			}
		case recRK:
			if len(body) >= 10 {
				sb.set(row, col, r.num(rkValue(le32(body[6:])), xf))
			}
		case recMulRK:
			// rw, colFirst, then (ixfe, rk) × n, then colLast.
			for i, p := 0, 4; p+6 <= len(body)-2; i, p = i+1, p+6 {
				sb.set(row, col+i, r.num(rkValue(le32(body[p+2:])), le16(body[p:])))
			}
		case recBoolErr:
			if len(body) >= 8 && body[7] == 0 {
				sb.set(row, col, boolStr(body[6] != 0))
			}
		case recFormula:
			pending = nil
			if len(body) < 14 {
				continue
			}
			res := body[6:14]
			if res[6] == 0xFF && res[7] == 0xFF {
				switch res[0] {
				case 0:
					pending = &cellPos{row, col}
				case 1:
					sb.set(row, col, boolStr(res[2] != 0))
				}
				continue // 2 = error, 3 = empty string: nothing to show
			}
			sb.set(row, col, r.num(math.Float64frombits(binary.LittleEndian.Uint64(res)), xf))
		case recString:
			if pending == nil {
				continue
			}
			chunks := [][]byte{body}
			chunks, pos = r.continues(chunks, pos)
			sb.set(pending.row, pending.col, r.str(chunks))
			pending = nil
		case recMergedCells:
			if len(body) < 2 {
				continue
			}
			n := int(le16(body))
			for i, p := 0, 2; i < n && p+8 <= len(body); i, p = i+1, p+8 {
				sb.merge(int(le16(body[p:])), int(le16(body[p+4:])), int(le16(body[p+2:])), int(le16(body[p+6:])))
			}
		}
	}
}

func boolStr(v bool) string {
	if v {
		return "TRUE"
	}
	return "FALSE"
}

// contReader reads a record body followed by its CONTINUE records as one
// byte stream, while letting string decoding see the record boundaries
// (where BIFF8 restarts a string's characters with a fresh option byte).
type contReader struct {
	chunks [][]byte
	ci     int
	off    int
}

func (c *contReader) avail() int {
	if c.ci >= len(c.chunks) {
		return 0
	}
	return len(c.chunks[c.ci]) - c.off
}

func (c *contReader) advance() bool {
	if c.ci >= len(c.chunks) {
		return false
	}
	c.ci++
	c.off = 0
	return c.ci < len(c.chunks)
}

func (c *contReader) atEnd() bool {
	for c.avail() == 0 {
		if !c.advance() {
			return true
		}
	}
	return false
}

func (c *contReader) byte() (byte, bool) {
	if c.atEnd() {
		return 0, false
	}
	b := c.chunks[c.ci][c.off]
	c.off++
	return b, true
}

func (c *contReader) uint(n int) (uint32, bool) {
	var v uint32
	for i := 0; i < n; i++ {
		b, ok := c.byte()
		if !ok {
			return 0, false
		}
		v |= uint32(b) << (8 * i)
	}
	return v, true
}

func (c *contReader) skip(n int) bool {
	for n > 0 {
		if c.atEnd() {
			return false
		}
		k := min(n, c.avail())
		c.off += k
		n -= k
	}
	return true
}

// readXLString reads a BIFF8 unicode string whose character count is
// cchBytes (1 or 2) wide, with optional rich-text runs and Far East data.
// When the characters continue in the next record, that record starts with
// a new option byte that may switch between 8-bit and UTF-16LE. The run and
// Far East blocks are skipped without such a byte. A cut-short string
// returns what was read and ok=false.
func readXLString(c *contReader, cchBytes int) (string, bool) {
	cch, ok := c.uint(cchBytes)
	if !ok {
		return "", false
	}
	flags, ok := c.byte()
	if !ok {
		return "", false
	}
	high := flags&1 != 0
	var runs, ext uint32
	if flags&8 != 0 {
		if runs, ok = c.uint(2); !ok {
			return "", false
		}
	}
	if flags&4 != 0 {
		if ext, ok = c.uint(4); !ok {
			return "", false
		}
	}
	u := make([]uint16, 0, cch)
	for rem := int(cch); rem > 0; {
		if c.avail() == 0 {
			if !c.advance() {
				return string(utf16.Decode(u)), false
			}
			if c.avail() > 0 {
				high = c.chunks[c.ci][c.off]&1 != 0
				c.off++
			}
			continue
		}
		b := c.chunks[c.ci][c.off:]
		if high {
			if len(b) < 2 { // stray odd byte before a boundary
				c.off = len(c.chunks[c.ci])
				continue
			}
			n := min(rem, len(b)/2)
			for i := 0; i < n; i++ {
				u = append(u, le16(b[2*i:]))
			}
			c.off += 2 * n
			rem -= n
		} else {
			n := min(rem, len(b))
			for i := 0; i < n; i++ {
				u = append(u, uint16(b[i])) // "compressed" = Latin-1
			}
			c.off += n
			rem -= n
		}
	}
	s := string(utf16.Decode(u))
	if !c.skip(int(runs)*4) || !c.skip(int(ext)) {
		return s, false
	}
	return s, true
}

// codepageDecoder maps a BIFF CODEPAGE to a decoder for 8-bit strings.
// Unknown pages fall back to Shift_JIS, the likely one for files this tool
// sees (and identical to ASCII for plain text).
func codepageDecoder(cp uint16) func([]byte) string {
	switch cp {
	case 1252, 0x8001:
		return func(b []byte) string { return decodeWith(charmap.Windows1252.NewDecoder(), b) }
	case 10000, 0x8000:
		return func(b []byte) string { return decodeWith(charmap.Macintosh.NewDecoder(), b) }
	case 65001:
		return func(b []byte) string { return toValidUTF8(b) }
	}
	return decodeShiftJIS
}
