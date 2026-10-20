package csvio

import (
	"bytes"
	"encoding/csv"
	"strings"

	"github.com/kojio145/palaterm/internal/model"
)

// RunResult is one row of the run tab's failure list: the device and what
// the table showed for it.
type RunResult struct {
	Device  model.Device
	Status  string // the 状態 column as shown (e.g. 失敗)
	Message string // the メッセージ column as shown
}

// runResultCols are the failure list's columns: enough to tell the devices
// apart and see how each was reached, with the error last. No credentials —
// the file is meant to be passed around while the failures are sorted out.
var runResultCols = []col{
	{"name", "機器名"}, {"group", "グループ"}, {"site", "拠点"}, {"role", "役割"},
	{"host", "IPアドレス"}, {"conn", "接続方式"}, {"port", "ポート"},
	{"serialPort", "シリアルポート"}, {"osType", "OSタイプ"},
	{"bastions", "踏み台"}, {"status", "状態"}, {"message", "メッセージ"},
}

// ExportRunResults renders the run tab's failed devices as CSV (BOM, Excel
// formula guard, Japanese headers for lang "ja"). Jump hosts are one cell,
// "jump1 → jump2", as they are only there to read.
func ExportRunResults(rows []RunResult, lang string) (string, error) {
	ja := lang == "ja"
	var buf bytes.Buffer
	buf.WriteString(BOM)
	w := csv.NewWriter(&buf)
	var head []string
	for _, c := range runResultCols {
		head = append(head, pick(ja, c.ja, c.key))
	}
	if err := w.Write(head); err != nil {
		return "", err
	}
	for _, r := range rows {
		d := r.Device
		d.NormalizeBastions()
		var hops []string
		for _, b := range d.ActiveBastions() {
			hops = append(hops, b.Host)
		}
		port := ""
		if d.Conn != model.ConnSerial {
			port = itoa(d.Port)
		}
		rec := []string{
			d.Name, d.Group, d.Site, d.Role,
			d.Host, connOut(string(d.Conn), ja), port,
			d.SerialPort, d.OSType,
			strings.Join(hops, " → "), r.Status, r.Message,
		}
		for i := range rec {
			rec[i] = cellOut(rec[i])
		}
		if err := w.Write(rec); err != nil {
			return "", err
		}
	}
	w.Flush()
	return buf.String(), w.Error()
}

// ExportTable renders rows (the first one the header) as CSV with the BOM
// and the Excel formula guard: for tables the UI builds as shown, such as
// the run history comparison.
func ExportTable(rows [][]string) (string, error) {
	var buf bytes.Buffer
	buf.WriteString(BOM)
	w := csv.NewWriter(&buf)
	for _, r := range rows {
		rec := make([]string, len(r))
		for i, v := range r {
			rec[i] = cellOut(v)
		}
		if err := w.Write(rec); err != nil {
			return "", err
		}
	}
	w.Flush()
	return buf.String(), w.Error()
}
