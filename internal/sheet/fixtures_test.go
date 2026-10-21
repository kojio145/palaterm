package sheet

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"math"
	"unicode/utf16"

	"golang.org/x/text/encoding/japanese"
)

// Fixture builders: a minimal compound-file writer, BIFF record helpers and
// an xlsx zip writer. All values are fictional (192.0.2.0/24, core-sw01, ops).

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func u16(v int) []byte { return binary.LittleEndian.AppendUint16(nil, uint16(v)) }
func u32(v uint32) []byte {
	return binary.LittleEndian.AppendUint32(nil, v)
}
func f64(v float64) []byte {
	return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v))
}

func rec(id int, body ...[]byte) []byte {
	b := cat(body...)
	return cat(u16(id), u16(len(b)), b)
}

func utf16le(s string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(s)) {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

func sjis(s string) []byte {
	b, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(s))
	if err != nil {
		panic(err)
	}
	return b
}

// xlStr builds a BIFF8 unicode string, compressed when every char is Latin-1.
func xlStr(s string, cchBytes int) []byte {
	r := []rune(s)
	var head []byte
	if cchBytes == 1 {
		head = []byte{byte(len(r))}
	} else {
		head = u16(len(utf16.Encode(r)))
	}
	latin := true
	for _, c := range r {
		if c > 0xFF {
			latin = false
		}
	}
	if latin {
		b := make([]byte, len(r))
		for i, c := range r {
			b[i] = byte(c)
		}
		return cat(head, []byte{0}, b)
	}
	return cat(head, []byte{1}, utf16le(s))
}

func bof(vers, dt int) []byte {
	if vers == 0x0500 {
		return rec(0x0809, u16(vers), u16(dt), make([]byte, 4))
	}
	return rec(0x0809, u16(vers), u16(dt), make([]byte, 12))
}

func cellHdr(r, c, xf int) []byte { return cat(u16(r), u16(c), u16(xf)) }

func xfRec(fmtID int) []byte { return rec(0x00E0, u16(0), u16(fmtID), make([]byte, 16)) }

func rkInt(v int32, x100 bool) uint32 {
	rk := uint32(v)<<2 | 2
	if x100 {
		rk |= 1
	}
	return rk
}

func rkFloat(v float64, x100 bool) uint32 {
	rk := uint32(math.Float64bits(v) >> 32)
	if x100 {
		rk |= 1
	}
	return rk
}

func formula(r, c, xf int, result []byte) []byte {
	return rec(0x0006, cellHdr(r, c, xf), result, u16(0), u32(0), u16(0))
}

// biff8SST is an SST whose strings straddle CONTINUE records: s2's chars
// switch from 8-bit to UTF-16 in the continuation, s3's rich-text runs and
// s4's start lie across the next boundary, s4 carries Far East data.
func biff8SST() []byte {
	c1 := cat(u32(5), u32(5),
		u16(9), []byte{0}, []byte("core-sw01"),
		u16(4), []byte{1}, utf16le("本社3F"),
		u16(17), []byte{0}, []byte("admin/x9"))
	c2 := cat([]byte{1}, utf16le("/Lp2q本社3F"),
		u16(3), []byte{0x08}, u16(2), []byte("ops"), []byte{0, 0, 1, 0})
	c3 := cat([]byte{3, 0, 2, 0},
		u16(10), []byte{0x04}, u32(6), []byte("192.0.2.10"), []byte{1, 2, 3, 4, 5, 6})
	return cat(rec(0x00FC, c1), rec(0x003C, c2), rec(0x003C, c3))
}

func biff8Sheet1() []byte {
	return cat(
		bof(0x0600, 0x0010),
		rec(0x00FD, cellHdr(0, 0, 0), u32(0)),
		rec(0x00FD, cellHdr(0, 1, 0), u32(1)),
		rec(0x00FD, cellHdr(0, 2, 0), u32(2)),
		rec(0x00FD, cellHdr(1, 0, 0), u32(3)),
		rec(0x00FD, cellHdr(1, 1, 0), u32(4)),
		rec(0x0203, cellHdr(1, 2, 0), f64(22)),
		rec(0x027E, cellHdr(2, 0, 0), u32(rkInt(22, false))),
		rec(0x027E, cellHdr(2, 1, 0), u32(rkFloat(1.5, false))),
		rec(0x027E, cellHdr(2, 2, 0), u32(rkInt(12345, true))),
		rec(0x027E, cellHdr(2, 3, 0), u32(rkFloat(0.5, true))),
		rec(0x00BD, u16(3), u16(0),
			u16(0), u32(rkInt(1, false)), u16(0), u32(rkInt(2, false)), u16(0), u32(rkInt(3, false)),
			u16(2)),
		formula(4, 0, 0, []byte{0, 0, 0, 0, 0, 0, 0xFF, 0xFF}),
		rec(0x04BC, make([]byte, 8)), // SHRFMLA between FORMULA and STRING
		rec(0x0207, xlStr("jump1", 2)),
		formula(4, 1, 0, f64(3.25)),
		formula(4, 2, 0, []byte{1, 0, 1, 0, 0, 0, 0xFF, 0xFF}),
		formula(4, 3, 0, []byte{3, 0, 0, 0, 0, 0, 0xFF, 0xFF}),
		rec(0x0205, cellHdr(5, 0, 0), []byte{1, 0}),
		rec(0x0205, cellHdr(5, 1, 0), []byte{0x07, 1}),
		rec(0x0203, cellHdr(6, 0, 1), f64(45658)),
		rec(0x0203, cellHdr(6, 1, 2), f64(45658.5)),
		rec(0x027E, cellHdr(6, 2, 1), u32(rkInt(45658, false))),
		rec(0x0201, cellHdr(7, 0, 0)), // BLANK
		rec(0x00E5, u16(1), u16(7), u16(8), u16(0), u16(1)),
		rec(0x000A),
	)
}

var biff8Sheet1Want = [][]string{
	{"core-sw01", "本社3F", "admin/x9/Lp2q本社3F"},
	{"ops", "192.0.2.10", "22"},
	{"22", "1.5", "123.45", "0.005"},
	{"1", "2", "3"},
	{"jump1", "3.25", "TRUE"},
	{"TRUE"},
	{"2025/01/01", "2025/01/01 12:00", "2025/01/01"},
}

func biff8Sheet2() []byte {
	return cat(
		bof(0x0600, 0x0010),
		bof(0x0600, 0x0020), // embedded chart substream: its content is not cells
		rec(0x0204, cellHdr(9, 9, 0), xlStr("chart-label", 2)),
		rec(0x000A),
		rec(0x0204, cellHdr(0, 0, 0), xlStr("spare-sw02", 2)),
		rec(0x00FD, cellHdr(1, 0, 0), u32(0)),
		rec(0x000A),
	)
}

// biff8Workbook assembles a BIFF8 Workbook stream. pad adds an unknown
// record so the stream can be pushed over the 4096-byte mini-stream cutoff.
func biff8Workbook(pad int, extraGlobals ...[]byte) []byte {
	s1, s2 := biff8Sheet1(), biff8Sheet2()
	globals := func(off1, off2 uint32) []byte {
		g := cat(
			bof(0x0600, 0x0005),
			cat(extraGlobals...),
			rec(0x0042, u16(1200)),
			rec(0x0022, u16(0)),
			rec(0x041E, u16(164), xlStr("yyyy/mm/dd hh:mm", 2)),
			xfRec(0), xfRec(14), xfRec(164),
			rec(0x0085, u32(off1), []byte{0, 0}, xlStr("機器", 1)),
			rec(0x0085, u32(off2), []byte{1, 0}, xlStr("予備", 1)), // hidden sheet: still read
			rec(0x0085, u32(0), []byte{0, 2}, xlStr("グラフ", 1)),   // chart sheet: skipped
			biff8SST(),
		)
		if pad > 0 {
			g = cat(g, rec(0x1234, make([]byte, pad)))
		}
		return cat(g, rec(0x000A))
	}
	n := uint32(len(globals(0, 0)))
	return cat(globals(n, n+uint32(len(s1))), s1, s2)
}

func biff5Book() []byte {
	sheet := cat(
		bof(0x0500, 0x0010),
		rec(0x0204, cellHdr(0, 0, 0), u16(len(sjis("本社3F"))), sjis("本社3F")),
		rec(0x0203, cellHdr(0, 1, 0), f64(23)),
		rec(0x000A),
	)
	globals := func(off uint32) []byte {
		return cat(
			bof(0x0500, 0x0005),
			rec(0x0042, u16(932)),
			rec(0x0085, u32(off), []byte{0, 0}, []byte{byte(len(sjis("機器")))}, sjis("機器")),
			rec(0x000A),
		)
	}
	return cat(globals(uint32(len(globals(0)))), sheet)
}

type cfbStream struct {
	name string
	data []byte
}

// buildCFB writes a compound file. Streams under 4096 bytes go to the mini
// stream. Big streams are placed last, so cutting the file's tail cuts them.
func buildCFB(shift uint, streams ...cfbStream) []byte {
	const end, free, fatSect, none = 0xFFFFFFFE, 0xFFFFFFFF, 0xFFFFFFFD, 0xFFFFFFFF
	S := 1 << shift
	var sectors [][]byte
	var fat []uint32
	alloc := func(data []byte) uint32 {
		if len(data) == 0 {
			return end
		}
		n := (len(data) + S - 1) / S
		start := uint32(len(sectors))
		for i := 0; i < n; i++ {
			sec := make([]byte, S)
			copy(sec, data[i*S:])
			sectors = append(sectors, sec)
			if i == n-1 {
				fat = append(fat, end)
			} else {
				fat = append(fat, start+uint32(i)+1)
			}
		}
		return start
	}
	starts := make([]uint32, len(streams))
	var mini []byte
	var miniFat []uint32
	var big []int
	nBig := 0
	for i, s := range streams {
		if len(s.data) >= 4096 {
			big = append(big, i)
			nBig += (len(s.data) + S - 1) / S
			continue
		}
		n := (len(s.data) + 63) / 64
		st := uint32(len(miniFat))
		for k := 0; k < n; k++ {
			if k == n-1 {
				miniFat = append(miniFat, end)
			} else {
				miniFat = append(miniFat, st+uint32(k)+1)
			}
		}
		buf := make([]byte, n*64)
		copy(buf, s.data)
		mini = append(mini, buf...)
		starts[i] = st
		if n == 0 {
			starts[i] = end
		}
	}
	miniStart := alloc(mini)
	var mf []byte
	for _, v := range miniFat {
		mf = append(mf, u32(v)...)
	}
	miniFatStart := alloc(mf)
	dirLen := (1 + len(streams)) * 128
	dirStart := alloc(make([]byte, dirLen))
	nDirSecs := (dirLen + S - 1) / S
	k := 1
	for len(sectors)+nBig+k > k*S/4 {
		k++
	}
	fatStart := len(sectors)
	for i := 0; i < k; i++ {
		sectors = append(sectors, make([]byte, S))
		fat = append(fat, fatSect)
	}
	for _, i := range big {
		starts[i] = alloc(streams[i].data)
	}
	for len(fat) < k*S/4 {
		fat = append(fat, free)
	}
	for i := 0; i < k; i++ {
		for j := 0; j < S/4; j++ {
			binary.LittleEndian.PutUint32(sectors[fatStart+i][j*4:], fat[i*S/4+j])
		}
	}
	dir := make([]byte, nDirSecs*S)
	entry := func(i int, name string, typ byte, right, child, start uint32, size int) {
		e := dir[i*128:]
		n := utf16le(name)
		copy(e, n)
		binary.LittleEndian.PutUint16(e[0x40:], uint16(len(n)+2))
		e[0x42], e[0x43] = typ, 1
		binary.LittleEndian.PutUint32(e[0x44:], none)
		binary.LittleEndian.PutUint32(e[0x48:], right)
		binary.LittleEndian.PutUint32(e[0x4C:], child)
		binary.LittleEndian.PutUint32(e[0x74:], start)
		binary.LittleEndian.PutUint64(e[0x78:], uint64(size))
	}
	child := uint32(none)
	if len(streams) > 0 {
		child = 1
	}
	entry(0, "Root Entry", 5, none, child, miniStart, len(mini))
	for i, s := range streams {
		right := uint32(none)
		if i < len(streams)-1 {
			right = uint32(i + 2)
		}
		entry(i+1, s.name, 2, right, none, starts[i], len(s.data))
	}
	for i := 0; i < nDirSecs; i++ {
		copy(sectors[int(dirStart)+i], dir[i*S:])
	}

	hdr := make([]byte, S)
	copy(hdr, oleMagic)
	binary.LittleEndian.PutUint16(hdr[0x18:], 0x3E)
	if S == 512 {
		binary.LittleEndian.PutUint16(hdr[0x1A:], 3)
	} else {
		binary.LittleEndian.PutUint16(hdr[0x1A:], 4)
		binary.LittleEndian.PutUint32(hdr[0x28:], uint32(nDirSecs))
	}
	binary.LittleEndian.PutUint16(hdr[0x1C:], 0xFFFE)
	binary.LittleEndian.PutUint16(hdr[0x1E:], uint16(shift))
	binary.LittleEndian.PutUint16(hdr[0x20:], 6)
	binary.LittleEndian.PutUint32(hdr[0x2C:], uint32(k))
	binary.LittleEndian.PutUint32(hdr[0x30:], dirStart)
	binary.LittleEndian.PutUint32(hdr[0x38:], 4096)
	binary.LittleEndian.PutUint32(hdr[0x3C:], miniFatStart)
	binary.LittleEndian.PutUint32(hdr[0x40:], uint32((len(mf)+S-1)/S))
	binary.LittleEndian.PutUint32(hdr[0x44:], end)
	for i := 0; i < 109; i++ {
		v := uint32(free)
		if i < k {
			v = uint32(fatStart + i)
		}
		binary.LittleEndian.PutUint32(hdr[0x4C+4*i:], v)
	}
	return cat(hdr, bytes.Join(sectors, nil))
}

func buildZip(files ...[2]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f[0])
		if err != nil {
			panic(err)
		}
		w.Write([]byte(f[1]))
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

const xlsxNS = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`

func xlsxFixture() []byte {
	return buildZip(
		[2]string{"[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`},
		[2]string{"_rels/.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		[2]string{"xl/workbook.xml", `<?xml version="1.0"?><workbook ` + xlsxNS + `><sheets>
<sheet name="機器" sheetId="1" r:id="rId1"/><sheet name="隠し" sheetId="2" state="hidden" r:id="rId2"/><sheet name="グラフ" sheetId="3" r:id="rId9"/></sheets></workbook>`},
		[2]string{"xl/_rels/workbook.xml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="/xl/worksheets/sheet2.xml"/>
<Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chartsheet" Target="chartsheets/sheet1.xml"/>
<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>
<Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`},
		[2]string{"xl/sharedStrings.xml", `<?xml version="1.0"?><sst ` + xlsxNS + ` count="3" uniqueCount="3">
<si><t>core-sw01</t></si>
<si><r><rPr><b/></rPr><t>本社</t></r><r><t xml:space="preserve">3F </t></r><rPh sb="0" eb="2"><t>ホンシャ</t></rPh></si>
<si><t>admin/x9/Lp2q</t></si></sst>`},
		[2]string{"xl/styles.xml", `<?xml version="1.0"?><styleSheet ` + xlsxNS + `>
<numFmts count="1"><numFmt numFmtId="164" formatCode="yyyy&quot;年&quot;m&quot;月&quot;d&quot;日&quot;"/></numFmts>
<cellStyleXfs count="1"><xf numFmtId="14"/></cellStyleXfs>
<cellXfs count="3"><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/></cellXfs></styleSheet>`},
		[2]string{"xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet ` + xlsxNS + `><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c></row>
<row r="2"><c r="A2" t="inlineStr"><is><t>ops</t></is></c><c t="str"><f>A1</f><v>192.0.2.10</v></c><c r="D2"><v>22</v></c><c><v>1.5</v></c></row>
<row><c r="A3" s="1"><v>45658</v></c><c r="B3" s="2"><v>45658</v></c><c r="C3" t="b"><v>1</v></c><c r="D3" t="e"><v>#N/A</v></c><c r="E3" s="0"><v>0.30000000000000004</v></c></row>
</sheetData><mergeCells count="1"><mergeCell ref="A5:B6"/></mergeCells></worksheet>`},
		[2]string{"xl/worksheets/sheet2.xml", `<?xml version="1.0"?><worksheet ` + xlsxNS + `><sheetData><row r="2"><c r="B2" t="inlineStr"><is><t>spare</t></is></c></row></sheetData></worksheet>`},
	)
}
