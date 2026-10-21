package listimport

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

// Header is one column of the table: its header text (spaces and line
// breaks folded) and its column number (1 = A).
type Header struct {
	Name string `json:"name"`
	Col  int    `json:"col"`
}

// Table is the part of a sheet a rule reads: the headers and the value rows.
type Table struct {
	Sheet   string   `json:"sheet"`
	Headers []Header `json:"headers"`
	Rows    []SrcRow `json:"-"`
}

// SrcRow is one value row: its row number in the sheet (1-based) and its
// cells by column number.
type SrcRow struct {
	Line  int
	Cells map[int]string
}

var reSpaces = regexp.MustCompile(`\s+`)

// foldHeader makes a header comparable: Excel headers often carry a line
// break or a double space ("管理\nIP").
func foldHeader(s string) string { return strings.TrimSpace(reSpaces.ReplaceAllString(s, " ")) }

// PickSheet chooses the rule's sheet: by name when it has one and the book
// has it, else by number (0 = the first).
func PickSheet(b *sheet.Book, r *model.ImportRule) (*sheet.Sheet, error) {
	if b == nil || len(b.Sheets) == 0 {
		return nil, fmt.Errorf("ファイルに表がありません")
	}
	if r.Sheet != "" {
		for i := range b.Sheets {
			if strings.EqualFold(strings.TrimSpace(b.Sheets[i].Name), strings.TrimSpace(r.Sheet)) {
				return &b.Sheets[i], nil
			}
		}
		if r.SheetIndex == 0 {
			if len(b.Sheets) == 1 {
				return &b.Sheets[0], nil // a CSV has one sheet, whatever its name
			}
			return nil, fmt.Errorf("シート「%s」がありません", r.Sheet)
		}
	}
	i := r.SheetIndex
	if i < 1 {
		i = 1
	}
	if i > len(b.Sheets) {
		return nil, fmt.Errorf("%d 枚目のシートがありません（%d 枚）", i, len(b.Sheets))
	}
	return &b.Sheets[i-1], nil
}

// cellGrid is a sheet with merged areas filled in (unless the rule asks
// for the top-left cell only).
func cellGrid(s *sheet.Sheet, onlyFirst bool) [][]string {
	if onlyFirst || len(s.Merges) == 0 {
		return s.Rows
	}
	rows := make([][]string, len(s.Rows))
	for i, r := range s.Rows {
		rows[i] = append([]string(nil), r...)
	}
	for _, m := range s.Merges {
		if m.R1 < 0 || m.C1 < 0 || m.R1 >= len(rows) || m.R2 < m.R1 || m.C2 < m.C1 || m.R2-m.R1 > 100000 || m.C2-m.C1 > 1000 {
			continue
		}
		v := ""
		if m.C1 < len(rows[m.R1]) {
			v = rows[m.R1][m.C1]
		}
		for r := m.R1; r <= m.R2 && r < len(rows); r++ {
			for c := m.C1; c <= m.C2; c++ {
				for len(rows[r]) <= c {
					rows[r] = append(rows[r], "")
				}
				if r == m.R1 && c == m.C1 {
					continue
				}
				rows[r][c] = v
			}
		}
	}
	return rows
}

// FilledRows is the sheet's grid as the rule reads it: merged areas
// filled in unless the rule takes their top-left cell only.
func FilledRows(s *sheet.Sheet, r *model.ImportRule) [][]string {
	return cellGrid(s, r.MergeOnlyFirst)
}

// ReadTable cuts the rule's table out of the sheet: the header row from the
// start column to the last filled header, and every value row with any
// cell filled under those headers.
func ReadTable(s *sheet.Sheet, r *model.ImportRule) (*Table, error) {
	rows := cellGrid(s, r.MergeOnlyFirst)
	hr := r.HeaderRow
	if hr < 1 {
		hr = 1
	}
	if hr > len(rows) {
		return nil, fmt.Errorf("見出しの行（%d 行目）がありません。シートは %d 行です", hr, len(rows))
	}
	head := rows[hr-1]
	start := model.ColumnNumber(r.StartCol)
	if start == 0 {
		for c, v := range head {
			if strings.TrimSpace(v) != "" {
				start = c + 1
				break
			}
		}
		if start == 0 {
			return nil, fmt.Errorf("見出しの行（%d 行目）が空です", hr)
		}
	}
	t := &Table{Sheet: s.Name}
	last := 0
	for c := start; c <= len(head); c++ {
		if strings.TrimSpace(head[c-1]) != "" {
			last = c
		}
	}
	if last == 0 {
		return nil, fmt.Errorf("見出しの行（%d 行目）の %s 列から右が空です", hr, model.ColumnLetter(start))
	}
	// The same header twice (a pair's primary and backup blocks, or a
	// repeated group, both with "Router") is two columns: the second and
	// later ones are named "Router (2)"…, so each is chosen — and read —
	// as itself rather than all as the first.
	seen := map[string]int{}
	for c := start; c <= last; c++ {
		name := foldHeader(head[c-1])
		if name != "" {
			seen[name]++
			if n := seen[name]; n > 1 {
				name = fmt.Sprintf("%s (%d)", name, n)
			}
		}
		t.Headers = append(t.Headers, Header{Name: name, Col: c})
	}
	dr := r.DataRow
	if dr == 0 {
		dr = hr + 1
	}
	for i := dr - 1; i < len(rows); i++ {
		row := rows[i]
		cells := map[int]string{}
		any := false
		for _, h := range t.Headers {
			if h.Col-1 < len(row) {
				v := row[h.Col-1]
				cells[h.Col] = v
				any = any || strings.TrimSpace(v) != ""
			}
		}
		if any {
			t.Rows = append(t.Rows, SrcRow{Line: i + 1, Cells: cells})
		}
	}
	return t, nil
}

// column finds a column by header text (the first one with it). Only a
// field without a header text goes by its number: a header renamed or
// moved at the source must show as missing, not quietly read whatever
// column now stands at the old place (a comment as every password).
func (t *Table) column(name string, idx int) int {
	if n := foldHeader(name); n != "" {
		for _, h := range t.Headers {
			if h.Name == n {
				return h.Col
			}
		}
		for _, h := range t.Headers {
			if strings.EqualFold(h.Name, n) {
				return h.Col
			}
		}
		return 0
	}
	if idx > 0 {
		for _, h := range t.Headers {
			if h.Col == idx {
				return idx
			}
		}
	}
	return 0
}

var reTemplate = regexp.MustCompile(`\{([^{}]+)\}`)

// sourceValue is a field's value before its steps, and the columns it
// names that the table does not have (they read empty).
func (t *Table) sourceValue(f *model.ImportField, row SrcRow) (v string, missing []string) {
	switch f.Source {
	case "column":
		c := t.column(f.Column, f.ColIndex)
		if c == 0 {
			return "", []string{f.Column}
		}
		return row.Cells[c], nil
	case "template":
		out := reTemplate.ReplaceAllStringFunc(f.Template, func(m string) string {
			name := m[1 : len(m)-1]
			c := t.column(name, 0)
			if c == 0 {
				missing = append(missing, name)
				return ""
			}
			return strings.TrimSpace(row.Cells[c])
		})
		return out, missing
	}
	return "", nil
}
