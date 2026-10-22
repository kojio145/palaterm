// Package sheet turns the bytes of a spreadsheet-like file into a grid of
// strings per sheet, for "一覧の取り込み" (importing device lists exported by
// other business systems).
//
// Those exports are messy: a ".xls" may really be HTML, SpreadsheetML 2003 or
// tab-separated text, and a real BIFF .xls may be one Excel opens only in
// Protected View ("このファイルに問題が見つかりました"). So the format is
// decided from the content, never the extension, and every reader is
// tolerant: unknown or broken parts are skipped, whatever cell values can be
// read are kept, and problems become short Japanese warnings rather than
// errors. Read never panics.
//
// Supported: BIFF8/BIFF5 .xls (own compound-file and BIFF reader), .xlsx
// (archive/zip + encoding/xml), HTML tables, SpreadsheetML 2003 XML, and
// CSV/TSV in UTF-8, UTF-16 (with BOM) or Shift_JIS.
package sheet

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// Range is a merged-cell area, 0-based, inclusive.
type Range struct{ R1, C1, R2, C2 int }

// Sheet is one worksheet (or one HTML table) as strings.
type Sheet struct {
	Name   string
	Rows   [][]string // Rows[r][c]; ragged rows allowed; values NOT trimmed
	Merges []Range
}

// Book is the result of reading one file.
type Book struct {
	Format   string // "xls", "xlsx", "html", "xml2003", "csv", "tsv"
	Sheets   []Sheet
	Warnings []string // Japanese, user-facing, short
}

const (
	maxFileSize = 64 << 20
	// The materialised grid is capped well below Excel's address space
	// (1,048,576 × 16,384): a device list never needs more, and a corrupt or
	// hostile file must not make us allocate gigabytes.
	maxGridRows = 200000
	maxGridCols = 1000
	maxMerges   = 100000
	maxWarnings = 50
	// MaxCells caps the cells a sheet holds in all (each row as long as
	// its last filled cell): 200,000 rows each with one cell in column ALL
	// would else be 200 million string slots (3 GB) from a small file.
	// Merged areas filled in later (listimport) count against it too.
	MaxCells = 5000000
)

var (
	errFormat    = errors.New("このファイルの形式を読み取れませんでした")
	errEncrypted = errors.New("パスワードで保護されたファイルは読み込めません。Excel で保護を外して保存し直してください")
	errEmpty     = errors.New("ファイルが空です")
	errTooLarge  = fmt.Errorf("ファイルが大きすぎます（上限 %d MB）", maxFileSize>>20)
)

var oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// ReadFile reads a file from disk (at most 64 MiB) and parses it with Read.
func ReadFile(path string) (*Book, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("ファイルを開けませんでした（%v）", err)
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > maxFileSize {
		return nil, errTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("ファイルを読み込めませんでした（%v）", err)
	}
	if len(data) > maxFileSize {
		return nil, errTooLarge
	}
	return Read(data)
}

// Read detects the format from the content and parses it. It never panics:
// a bug triggered by a malformed file becomes an error instead.
func Read(data []byte) (book *Book, err error) {
	defer func() {
		if r := recover(); r != nil {
			book = nil
			err = fmt.Errorf("このファイルを読み取れませんでした（内部エラー: %v）", r)
		}
	}()
	return read(data)
}

// read is Read without the recover, so tests can see panics.
func read(data []byte) (*Book, error) {
	if len(data) == 0 {
		return nil, errEmpty
	}
	if len(data) > maxFileSize {
		return nil, errTooLarge
	}
	switch {
	case bytes.HasPrefix(data, oleMagic):
		return readOLE(data)
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return readXLSX(data)
	}
	return readText(data)
}

func newBook(format string) *Book {
	return &Book{Format: format, Sheets: []Sheet{}, Warnings: []string{}}
}

// warn adds a warning once; repeated identical problems (e.g. every sheet of
// a damaged file) would only bury the useful ones.
func (b *Book) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	for _, w := range b.Warnings {
		if w == msg {
			return
		}
	}
	if len(b.Warnings) < maxWarnings {
		b.Warnings = append(b.Warnings, msg)
	}
}

// sheetBuilder collects cells and merges with the size caps applied.
type sheetBuilder struct {
	name    string
	rows    [][]string
	merges  []Range
	clipped bool
	cells   int // the slots the rows hold (see MaxCells)
}

func (s *sheetBuilder) set(r, c int, v string) {
	if r < 0 || c < 0 {
		return
	}
	if r >= maxGridRows || c >= maxGridCols {
		s.clipped = true
		return
	}
	if v == "" {
		return // empty cells stay implicit so sparse sheets stay small
	}
	for len(s.rows) <= r {
		s.rows = append(s.rows, nil)
	}
	row := s.rows[r]
	if c >= len(row) {
		if s.cells+c+1-len(row) > MaxCells {
			s.clipped = true
			return
		}
		s.cells += c + 1 - len(row)
		row = append(row, make([]string, c+1-len(row))...)
	}
	row[c] = v
	s.rows[r] = row
}

func (s *sheetBuilder) merge(r1, c1, r2, c2 int) {
	if r1 < 0 || c1 < 0 || r2 < r1 || c2 < c1 || (r1 == r2 && c1 == c2) {
		return
	}
	if r1 >= maxGridRows || c1 >= maxGridCols {
		s.clipped = true
		return
	}
	r2 = min(r2, maxGridRows-1)
	c2 = min(c2, maxGridCols-1)
	if len(s.merges) >= maxMerges {
		return
	}
	s.merges = append(s.merges, Range{r1, c1, r2, c2})
}

func (s *sheetBuilder) finish(b *Book) {
	if s.clipped {
		b.warn("シート「%s」が大きすぎるため、%d 行・%d 列（合計 %d セル）を超える部分は読み込んでいません", s.name, maxGridRows, maxGridCols, MaxCells)
	}
	rows := s.rows
	if rows == nil {
		rows = [][]string{}
	}
	for i, r := range rows {
		if r == nil {
			rows[i] = []string{}
		}
	}
	merges := s.merges
	if merges == nil {
		merges = []Range{}
	}
	b.Sheets = append(b.Sheets, Sheet{Name: s.name, Rows: rows, Merges: merges})
}
