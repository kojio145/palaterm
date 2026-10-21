package sheet

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// A minimal, tolerant reader for the OLE2 compound file binary format
// (MS-CFB), the container of .xls files. Every sector number read from the
// file is bounds-checked and every chain walk carries a visited set, so a
// damaged FAT ends a stream early instead of looping or panicking.

const (
	secEnd    = 0xFFFFFFFE
	secMaxReg = 0xFFFFFFFA // sector numbers at or above this are markers
	noStream  = 0xFFFFFFFF
)

type cfbEntry struct {
	name               string
	typ                byte // 1 storage, 2 stream, 5 root
	left, right, child uint32
	start              uint32
	size               uint64
}

type cfbFile struct {
	data       []byte
	secSize    int
	miniSize   int
	cutoff     uint64
	fat        []uint32
	miniFAT    []uint32
	entries    []cfbEntry
	miniStream []byte
	damaged    bool // some structure (FAT, directory, mini stream) was cut short
}

func le16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

func openCFB(data []byte) (*cfbFile, error) {
	if len(data) < 512 {
		return nil, errFormat
	}
	f := &cfbFile{data: data}
	shift := le16(data[0x1E:])
	if shift != 9 && shift != 12 {
		shift = 9 // the only two sizes the spec allows; guess the common one
	}
	f.secSize = 1 << shift
	ms := le16(data[0x20:])
	if ms < 2 || ms >= shift {
		ms = 6
	}
	f.miniSize = 1 << ms
	f.cutoff = uint64(le32(data[0x38:]))
	if f.cutoff == 0 || f.cutoff > 1<<20 {
		f.cutoff = 4096
	}
	maxSectors := len(data)/f.secSize + 1

	// FAT sector list: 109 entries in the header, then the DIFAT chain.
	var fatSecs []uint32
	addFatSec := func(s uint32) {
		if s < secMaxReg && len(fatSecs) < maxSectors {
			fatSecs = append(fatSecs, s)
		}
	}
	for i := 0; i < 109; i++ {
		addFatSec(le32(data[0x4C+4*i:]))
	}
	seen := map[uint32]bool{}
	for next := le32(data[0x44:]); next < secMaxReg && !seen[next]; {
		seen[next] = true
		sec := f.sector(next)
		if len(sec) < f.secSize {
			f.damaged = true
			break
		}
		n := f.secSize/4 - 1
		for i := 0; i < n; i++ {
			addFatSec(le32(sec[4*i:]))
		}
		next = le32(sec[4*n:])
	}
	for _, s := range fatSecs {
		sec := f.sector(s)
		if sec == nil {
			// Keep later FAT sectors at their indexes: fill with "free".
			f.damaged = true
			sec = make([]byte, f.secSize)
			for i := range sec {
				sec[i] = 0xFF
			}
		}
		for i := 0; i+4 <= len(sec); i += 4 {
			f.fat = append(f.fat, le32(sec[i:]))
		}
	}

	dir, ok := f.follow(le32(data[0x30:]), f.fat, f.sector, f.secSize, uint64(len(data)))
	if !ok {
		f.damaged = true
	}
	for i := 0; i+128 <= len(dir); i += 128 {
		e := dir[i : i+128]
		nl := int(le16(e[0x40:]))
		if nl > 64 {
			nl = 64
		}
		var name []uint16
		for j := 0; j+1 < nl; j += 2 {
			c := le16(e[j:])
			if c == 0 {
				break
			}
			name = append(name, c)
		}
		size := binary.LittleEndian.Uint64(e[0x78:])
		if f.secSize == 512 {
			size &= 0xFFFFFFFF // version 3 files may leave garbage in the high half
		}
		f.entries = append(f.entries, cfbEntry{
			name: string(utf16.Decode(name)), typ: e[0x42],
			left: le32(e[0x44:]), right: le32(e[0x48:]), child: le32(e[0x4C:]),
			start: le32(e[0x74:]), size: size,
		})
	}
	if len(f.entries) == 0 || f.entries[0].typ != 5 {
		return nil, errFormat
	}

	mf, ok := f.follow(le32(data[0x3C:]), f.fat, f.sector, f.secSize, uint64(len(data)))
	if !ok {
		f.damaged = true
	}
	for i := 0; i+4 <= len(mf); i += 4 {
		f.miniFAT = append(f.miniFAT, le32(mf[i:]))
	}
	root := f.entries[0]
	if root.size > 0 {
		ms, ok := f.follow(root.start, f.fat, f.sector, f.secSize, root.size)
		if !ok || uint64(len(ms)) < root.size {
			f.damaged = true
		}
		f.miniStream = ms
	}
	return f, nil
}

// sector returns sector n, possibly short if the file is truncated, or nil
// when it lies entirely outside the file.
func (f *cfbFile) sector(n uint32) []byte {
	off := (uint64(n) + 1) * uint64(f.secSize)
	if off >= uint64(len(f.data)) {
		return nil
	}
	end := min(off+uint64(f.secSize), uint64(len(f.data)))
	return f.data[off:end]
}

func (f *cfbFile) miniSector(n uint32) []byte {
	off := uint64(n) * uint64(f.miniSize)
	if off >= uint64(len(f.miniStream)) {
		return nil
	}
	end := min(off+uint64(f.miniSize), uint64(len(f.miniStream)))
	return f.miniStream[off:end]
}

// follow reads a sector chain until limit bytes, the end-of-chain marker, or
// damage. ok is false only for damage (bad link, loop, missing sector).
func (f *cfbFile) follow(start uint32, fat []uint32, get func(uint32) []byte, unit int, limit uint64) ([]byte, bool) {
	buf := make([]byte, 0, min(limit, 1<<20))
	seen := map[uint32]bool{}
	s := start
	for uint64(len(buf)) < limit {
		if s == secEnd {
			return buf, true
		}
		if s >= secMaxReg || seen[s] {
			return buf, false
		}
		seen[s] = true
		sec := get(s)
		if sec == nil {
			return buf, false
		}
		buf = append(buf, sec...)
		if len(sec) < unit {
			return buf, false // truncated file
		}
		if int(s) >= len(fat) {
			if uint64(len(buf)) >= limit {
				break
			}
			return buf, false
		}
		s = fat[s]
	}
	if uint64(len(buf)) > limit {
		buf = buf[:limit]
	}
	return buf, true
}

// find returns the stream with the given name (case-insensitive). Streams
// directly under the root win, so a "Workbook" inside an embedded OLE object
// is not mistaken for the file's own.
func (f *cfbFile) find(name string) *cfbEntry {
	seen := map[uint32]bool{}
	stack := []uint32{f.entries[0].child}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if i == noStream || int(i) >= len(f.entries) || seen[i] {
			continue
		}
		seen[i] = true
		e := &f.entries[i]
		if e.typ == 2 && strings.EqualFold(e.name, name) {
			return e
		}
		stack = append(stack, e.left, e.right)
	}
	for i := range f.entries {
		if e := &f.entries[i]; e.typ == 2 && strings.EqualFold(e.name, name) {
			return e
		}
	}
	return nil
}

// stream returns the stream's bytes; truncated reports that fewer bytes than
// the directory claims could be read.
func (f *cfbFile) stream(e *cfbEntry) (data []byte, truncated bool) {
	size := min(e.size, uint64(len(f.data)))
	if e.size < f.cutoff {
		data, _ = f.follow(e.start, f.miniFAT, f.miniSector, f.miniSize, size)
	} else {
		data, _ = f.follow(e.start, f.fat, f.sector, f.secSize, size)
	}
	return data, uint64(len(data)) < e.size
}
