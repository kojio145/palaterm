package sheet

import "testing"

// A cell's note (<Comment>) has a <Data> of its own: the cell keeps its
// value, and the note's text goes nowhere.
func TestXML2003CommentIgnored(t *testing.T) {
	const doc = `<?xml version="1.0"?>
<?mso-application progid="Excel.Sheet"?>
<Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet">
<Worksheet ss:Name="s"><Table>
<Row><Cell><Data ss:Type="String">core-sw01</Data><Comment ss:Author="ops"><ss:Data><Font>replaced 2025</Font></ss:Data></Comment></Cell>
<Cell><Comment><ss:Data>note first</ss:Data></Comment><Data ss:Type="String">192.0.2.1</Data></Cell></Row>
</Table></Worksheet></Workbook>`
	b := mustRead(t, []byte(doc), "xml2003")
	row := b.Sheets[0].Rows[0]
	if len(row) < 2 || row[0] != "core-sw01" || row[1] != "192.0.2.1" {
		t.Errorf("row = %q", row)
	}
}
