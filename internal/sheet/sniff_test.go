package sheet

import "testing"

// HTML is told by how the text begins: a CSV with an HTML snippet in a
// 備考 column is still a CSV, while a table fragment, a commented or
// doctyped page and an <html> page are HTML.
func TestSniffMarkupByStart(t *testing.T) {
	b := mustRead(t, []byte("機器名,備考\nlab-sw01,\"<table><tr><td>x</td></tr></table>\"\n"), "csv")
	checkRows(t, b.Sheets[0], [][]string{{"機器名", "備考"}, {"lab-sw01", "<table><tr><td>x</td></tr></table>"}})
	for _, text := range []string{
		"<table><tr><td>lab-sw01</td></tr></table>",
		"\n  <!-- exported -->\n<table><tr><td>lab-sw01</td></tr></table>",
		"<!DOCTYPE html>\n<html><body><table><tr><td>lab-sw01</td></tr></table></body></html>",
		"<HTML><body><table><tr><td>lab-sw01</td></tr></table></body></HTML>",
	} {
		b := mustRead(t, []byte(text), "html")
		checkRows(t, b.Sheets[0], [][]string{{"lab-sw01"}})
	}
}
