// Package csvio converts the device inventory to and from CSV, so devices can
// be bulk-edited in Excel/spreadsheets (as the legacy tool allowed).
//
// The bastion chain is encoded in a single column as
//
//	method:host:port:user:pass:auth:keyfile | method:host:...
//
// (up to model.MaxBastions hops, "|"-separated, outermost first). Empty trailing fields may be
// omitted. A bastion's key passphrase, legacy-cipher flag and jump command are
// not part of the encoding — set those in the app. The field order is fixed
// with keyfile last so a Windows path's own colons survive the round trip, so
// new fields cannot simply be appended. Passwords are written in clear text —
// treat exported CSV as sensitive and delete it after re-import.
package csvio

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/kojio145/palaterm/internal/model"
)

var header = []string{
	"name", "group", "site", "host", "conn", "port", "serialPort", "baud",
	"osType", "commandSet", "authMethod", "keyFile", "keyPassphrase",
	"username", "password", "enablePassword", "bastions", "enabled",
}

// Export renders devices as CSV text with a header row.
func Export(devices []model.Device) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return "", err
	}
	for i := range devices {
		d := devices[i]
		d.NormalizeBastions()
		rec := []string{
			d.Name, d.Group, d.Site, d.Host, string(d.Conn), itoa(d.Port), d.SerialPort, itoa(d.Baud),
			d.OSType, d.CommandSet, string(d.AuthMethod), d.KeyFile, d.KeyPassphrase,
			d.Username, d.Password, d.EnablePassword,
			encodeBastions(d.Bastions), boolStr(d.Enabled),
		}
		if err := w.Write(rec); err != nil {
			return "", err
		}
	}
	w.Flush()
	return buf.String(), w.Error()
}

// Import parses CSV text (with header) into devices. Column order follows the
// header names, so users may reorder or omit optional columns.
func Import(text string) ([]model.Device, error) {
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1 // tolerate ragged rows
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty CSV")
	}
	idx := map[string]int{}
	for i, name := range rows[0] {
		idx[strings.TrimSpace(strings.ToLower(name))] = i
	}
	if _, ok := idx["name"]; !ok {
		return nil, fmt.Errorf("CSV must have a 'name' column in the header row")
	}

	get := func(row []string, key string) string {
		if i, ok := idx[key]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}

	var out []model.Device
	for _, row := range rows[1:] {
		name := get(row, "name")
		if name == "" {
			continue // skip blank lines
		}
		d := model.Device{
			Name:           name,
			Group:          get(row, "group"),
			Site:           get(row, "site"),
			Host:           get(row, "host"),
			Conn:           model.ConnMethod(orDefault(get(row, "conn"), "ssh")),
			Port:           atoi(get(row, "port")),
			SerialPort:     get(row, "serialport"),
			Baud:           atoi(get(row, "baud")),
			OSType:         orDefault(get(row, "ostype"), "generic"),
			CommandSet:     get(row, "commandset"),
			AuthMethod:     model.AuthMethod(orDefault(get(row, "authmethod"), "password")),
			KeyFile:        get(row, "keyfile"),
			KeyPassphrase:  get(row, "keypassphrase"),
			Username:       get(row, "username"),
			Password:       get(row, "password"),
			EnablePassword: get(row, "enablepassword"),
			Bastions:       decodeBastions(get(row, "bastions")),
			Enabled:        parseBool(get(row, "enabled"), true),
		}
		out = append(out, d)
	}
	return out, nil
}

func encodeBastions(bs []model.Bastion) string {
	if len(bs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(bs))
	for _, b := range bs {
		f := []string{string(b.Method), b.Host, itoa(b.Port), b.Username, b.Password, string(b.AuthMethod), b.KeyFile}
		parts = append(parts, strings.Join(f, ":"))
	}
	return strings.Join(parts, "|")
}

func decodeBastions(s string) []model.Bastion {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []model.Bastion
	for _, hop := range strings.Split(s, "|") {
		// keyfile is the last field, so SplitN lets a Windows path's own
		// colons ("C:\Users\...") survive the round trip.
		f := strings.SplitN(hop, ":", 7)
		g := func(i int) string {
			if i < len(f) {
				return strings.TrimSpace(f[i])
			}
			return ""
		}
		b := model.Bastion{
			Method:     model.BastionMethod(g(0)),
			Host:       g(1),
			Port:       atoi(g(2)),
			Username:   g(3),
			Password:   g(4),
			AuthMethod: model.AuthMethod(g(5)),
			KeyFile:    g(6),
		}
		if b.Host != "" && b.Method != model.BastionNone {
			out = append(out, b)
		}
		if len(out) >= model.MaxBastions {
			break
		}
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return ""
	}
	return strconv.Itoa(i)
}
func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
func parseBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "y", "on":
		return true
	case "false", "0", "no", "n", "off":
		return false
	}
	return def
}
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
