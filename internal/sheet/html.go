package sheet

import (
	"errors"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// HTML tables, which many systems (and Excel's "Web page" save) write under
// an .xls name. Each top-level <table> becomes one sheet; a table nested in
// a cell only contributes its text to that cell.

var errNoTable = errors.New("このファイルには表が見つかりませんでした")

const maxHTMLDepth = 512 // html.Parse output is a tree; bound our recursion

func readHTML(text string) (*Book, error) {
	doc, err := html.Parse(strings.NewReader(text))
	if err != nil {
		return nil, errFormat
	}
	var tables []*html.Node
	var walk func(n *html.Node, depth int)
	walk = func(n *html.Node, depth int) {
		if depth > maxHTMLDepth {
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Table {
			tables = append(tables, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, depth+1)
		}
	}
	walk(doc, 0)
	if len(tables) == 0 {
		return nil, errNoTable
	}
	b := newBook("html")
	for i, t := range tables {
		readHTMLTable(t, "表"+strconv.Itoa(i+1), b)
	}
	return b, nil
}

// tableRows lists the table's own <tr>s (directly or in thead/tbody/tfoot).
func tableRows(t *html.Node) []*html.Node {
	var rows []*html.Node
	for c := t.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.DataAtom {
		case atom.Tr:
			rows = append(rows, c)
		case atom.Thead, atom.Tbody, atom.Tfoot:
			for r := c.FirstChild; r != nil; r = r.NextSibling {
				if r.Type == html.ElementNode && r.DataAtom == atom.Tr {
					rows = append(rows, r)
				}
			}
		}
	}
	return rows
}

func htmlAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func spanAttr(n *html.Node, key string, maxSpan int) int {
	v, err := strconv.Atoi(strings.TrimSpace(htmlAttr(n, key)))
	if err != nil || v < 1 {
		return 1 // rowspan=0 ("to the end") is rare in exports; treat as 1
	}
	return min(v, maxSpan)
}

func readHTMLTable(t *html.Node, name string, b *Book) {
	sb := &sheetBuilder{name: name}
	defer sb.finish(b)
	// busy[c] is the first row at which column c is free again, so cells
	// covered by an earlier rowspan push later cells to the right.
	var busy []int
	for r, tr := range tableRows(t) {
		if r >= maxGridRows {
			sb.clipped = true
			return
		}
		c := 0
		for td := tr.FirstChild; td != nil; td = td.NextSibling {
			if td.Type != html.ElementNode || (td.DataAtom != atom.Td && td.DataAtom != atom.Th) {
				continue
			}
			for c < len(busy) && busy[c] > r {
				c++
			}
			if c >= maxGridCols {
				sb.clipped = true
				break
			}
			cs, rs := spanAttr(td, "colspan", 1000), spanAttr(td, "rowspan", 65534)
			cs = min(cs, maxGridCols-c)
			sb.set(r, c, cellText(td))
			// Excel's "mso-ignore:colspan" marks text overflowing into empty
			// neighbours, not a real merge.
			if (cs > 1 || rs > 1) && !strings.Contains(strings.ReplaceAll(htmlAttr(td, "style"), " ", ""), "mso-ignore:colspan") {
				sb.merge(r, c, r+rs-1, c+cs-1)
			}
			if rs > 1 {
				for len(busy) < c+cs {
					busy = append(busy, 0)
				}
				for k := c; k < c+cs; k++ {
					busy[k] = max(busy[k], r+rs)
				}
			}
			c += cs
		}
	}
}

// Markers used while collecting cell text: a hard break (<br>) is kept one
// for one; soft breaks (block boundaries) collapse into a single newline.
const (
	hardBreak = '\x00'
	softBreak = '\x01'
)

func cellText(td *html.Node) string {
	var sb strings.Builder
	var rec func(n *html.Node, depth int)
	rec = func(n *html.Node, depth int) {
		if depth > maxHTMLDepth {
			return
		}
		switch n.Type {
		case html.TextNode:
			sb.WriteString(strings.Map(func(r rune) rune {
				if r == hardBreak || r == softBreak {
					return ' '
				}
				return r
			}, n.Data))
			return
		case html.ElementNode:
			switch n.DataAtom {
			case atom.Br:
				sb.WriteByte(hardBreak)
				return
			case atom.Script, atom.Style, atom.Head:
				return
			case atom.P, atom.Div, atom.Tr, atom.Li, atom.Table:
				sb.WriteByte(softBreak)
				defer sb.WriteByte(softBreak)
			case atom.Td, atom.Th:
				if n != td {
					sb.WriteByte(' ')
					defer sb.WriteByte(' ')
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c, depth+1)
		}
	}
	rec(td, 0)
	return normalizeCellText(sb.String())
}

func isCollapsible(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v', 0xA0:
		return true
	}
	return false // U+3000 (全角スペース) is content, not layout
}

// normalizeCellText collapses whitespace like a browser does, turns break
// markers into newlines and trims the ends.
func normalizeCellText(s string) string {
	var out strings.Builder
	space := false
	hard, soft := 0, false
	flushBreaks := func() {
		n := hard
		if n == 0 && soft {
			n = 1
		}
		if n > 0 && out.Len() > 0 {
			out.WriteString(strings.Repeat("\n", n))
		}
		hard, soft = 0, false
	}
	for _, r := range s {
		switch {
		case r == hardBreak:
			hard++
			space = false
		case r == softBreak:
			soft = true
			space = false
		case isCollapsible(r):
			space = true
		default:
			if hard > 0 || soft {
				flushBreaks()
			} else if space && out.Len() > 0 {
				out.WriteByte(' ')
			}
			space = false
			out.WriteRune(r)
		}
	}
	return out.String()
}
