// Package csvio converts the device inventory to and from CSV, so devices can
// be bulk-edited in Excel/spreadsheets (as the legacy tool allowed).
//
// Written for the person filling it in: one value per column, never several
// packed into one cell. Each jump host is a run of its own columns —
// bastion1Host, bastion1Port, bastion1Username, … then bastion2… — written
// for every hop in use and at least the first, so a jump host can be added
// in Excel. The columns are ordered as one fills them in: the device, how to
// log in, then the jump hosts. A Japanese export (lang "ja") has Japanese
// headers (「機器名」「IPアドレス」「踏み台1_ホスト」…) and words for the
// fixed values (SSH / シリアル, パスワード / 公開鍵, はい / いいえ); import
// accepts either language for both.
//
// Older CSVs still import: the single "bastions" column of v1.6.4 and before
// ("method:host:port:user:pass:auth:keyfile | …"), its "bastionExtras" JSON
// column (v1.5.16 and before) and the per-hop extras columns of v1.5.17–
// v1.6.4. (v1.6.4 and before read only the packed column: a CSV written
// here loses its jump hosts there.) A file saved again as Shift_JIS (Excel's
// plain 「CSV（コンマ区切り）」) is read as such. Passwords are written in
// clear text — treat exported CSV as sensitive and delete it after re-import.
package csvio

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kojio145/palaterm/internal/model"
	"golang.org/x/text/encoding/japanese"
)

// col is one CSV column: its key (the English header) and its Japanese header.
type col struct{ key, ja string }

// deviceCols are the device's own columns, in the order they are filled in.
var deviceCols = []col{
	{"name", "機器名"}, {"group", "グループ"}, {"site", "拠点"}, {"role", "役割"},
	{"host", "IPアドレス"}, {"conn", "接続方式"}, {"port", "ポート"},
	{"serialPort", "シリアルポート"}, {"baud", "ボーレート"},
	{"osType", "OSタイプ"}, {"commandSet", "コマンドセット"}, {"enabled", "有効"},
	{"useGroupCreds", "グループ既定の認証情報を使う"},
	{"username", "ユーザー名"}, {"password", "パスワード"}, {"enablePassword", "enableパスワード"},
	{"authMethod", "認証方式"}, {"keyFile", "鍵ファイル"}, {"keyPassphrase", "鍵パスフレーズ"},
	{"legacyAlgos", "旧暗号を許可"}, {"useMyBastion", "自分の踏み台を使う"}, {"bastionMode", "踏み台の通り方"},
	{"cli", "CLIを使う"}, {"gui", "GUIを使う"}, {"guiScheme", "GUIの方式"}, {"guiPort", "GUIポート"},
}

// hopFields are one jump host's columns (prefixed bastion<n> / 踏み台<n>_).
var hopFields = []col{
	{"Method", "接続方式"}, {"Host", "ホスト"}, {"Port", "ポート"},
	{"Username", "ユーザー名"}, {"Password", "パスワード"},
	{"AuthMethod", "認証方式"}, {"KeyFile", "鍵ファイル"}, {"KeyPassphrase", "鍵パスフレーズ"},
	{"LegacyAlgos", "旧暗号を許可"}, {"JumpCommand", "ジャンプコマンド"},
}

func hopKey(n int, f col) string { return "bastion" + strconv.Itoa(n) + f.key }
func hopJa(n int, f col) string  { return "踏み台" + strconv.Itoa(n) + "_" + f.ja }

// BOM is the UTF-8 byte order mark every exported CSV starts with: without
// it Excel on a Japanese Windows opens the file as Shift_JIS and every
// group name and site reads as mojibake. Import strips it again.
const BOM = "\ufeff"

// cellOut guards a value against spreadsheet formula injection: one that
// starts like a formula (= + - @, a tab or CR) — say a device name or site
// typed by someone else, opened later in Excel — gets a leading apostrophe,
// so Excel shows it as text instead of running it. A value that itself
// starts with an apostrophe gets one too, so cellIn can always strip exactly
// one and every value reads back as it was written.
func cellOut(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r'", rune(v[0])) {
		return "'" + v
	}
	return v
}

// cellIn undoes cellOut.
func cellIn(v string) string { return strings.TrimPrefix(v, "'") }

// Export renders devices as CSV text with English headers and values.
func Export(devices []model.Device) (string, error) { return ExportLang(devices, "en") }

// ExportLang renders devices as CSV text; lang "ja" writes Japanese headers
// and words for the fixed values.
func ExportLang(devices []model.Device, lang string) (string, error) {
	ja := lang == "ja"
	var buf bytes.Buffer
	buf.WriteString(BOM)
	w := csv.NewWriter(&buf)
	hops := 1 // always room to add a jump host in Excel
	for i := range devices {
		d := devices[i]
		d.NormalizeBastions()
		hops = max(hops, len(d.Bastions))
	}
	var head []string
	for _, c := range deviceCols {
		head = append(head, pick(ja, c.ja, c.key))
	}
	for n := 1; n <= hops; n++ {
		for _, f := range hopFields {
			head = append(head, pick(ja, hopJa(n, f), hopKey(n, f)))
		}
	}
	if err := w.Write(head); err != nil {
		return "", err
	}
	for i := range devices {
		d := devices[i]
		d.NormalizeBastions()
		rec := []string{
			d.Name, d.Group, d.Site, d.Role,
			d.Host, connOut(string(d.Conn), ja), itoa(d.Port),
			d.SerialPort, itoa(d.Baud),
			d.OSType, d.CommandSet, boolOut(d.Enabled, ja),
			boolOut(d.UseGroupCreds, ja),
			d.Username, d.Password, d.EnablePassword,
			authOut(string(d.AuthMethod), ja), d.KeyFile, d.KeyPassphrase,
			boolOut(d.LegacyAlgos, ja), boolOut(d.UseMyBastion, ja), modeOut(d.BastionMode, len(d.Bastions) > 0 || d.UseMyBastion, ja),
			boolOut(!d.CLIOff, ja), boolOut(d.GUIOn, ja), d.EffectiveGUIScheme(), itoa(d.GUIPort),
		}
		for n := 0; n < hops; n++ {
			if n < len(d.Bastions) {
				b := d.Bastions[n]
				rec = append(rec, connOut(string(b.Method), ja), b.Host, itoa(b.Port), b.Username, b.Password,
					authOut(string(b.AuthMethod), ja), b.KeyFile, b.KeyPassphrase, boolOut(b.LegacyAlgos, ja), b.JumpCommand)
			} else {
				rec = append(rec, make([]string, len(hopFields))...)
			}
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

func pick(ja bool, a, b string) string {
	if ja {
		return a
	}
	return b
}

// Import parses CSV text (with header) into devices. Column order follows the
// header names (English or Japanese), so users may reorder or omit optional
// columns.
func Import(text string) ([]model.Device, error) {
	if !utf8.ValidString(text) {
		// Saved again by Excel as 「CSV（コンマ区切り）」: Shift_JIS, and
		// every Japanese header and value would otherwise be unreadable.
		dec, err := japanese.ShiftJIS.NewDecoder().String(text)
		if err != nil || strings.ContainsRune(dec, utf8.RuneError) {
			return nil, fmt.Errorf("文字コードを読み取れません。Excel では「CSV UTF-8（コンマ区切り）」で保存してください")
		}
		text = dec
	}
	text = strings.TrimPrefix(text, BOM)
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1 // tolerate ragged rows
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty CSV")
	}
	// Header → column index, keyed by the lower-case English key: a Japanese
	// header is mapped to its key first.
	// Matched ignoring case in both languages (「ENABLEパスワード」).
	alias := map[string]string{}
	for _, c := range deviceCols {
		alias[strings.ToLower(c.ja)] = strings.ToLower(c.key)
	}
	for n := 1; n <= model.MaxBastions; n++ {
		for _, f := range hopFields {
			alias[strings.ToLower(hopJa(n, f))] = strings.ToLower(hopKey(n, f))
		}
	}
	idx := map[string]int{}
	for i, name := range rows[0] {
		h := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, BOM)))
		if k, ok := alias[h]; ok {
			h = k
		}
		idx[h] = i
	}
	if _, ok := idx["name"]; !ok {
		return nil, fmt.Errorf("1 行目（見出し）に「機器名」（name）の列がありません")
	}
	has := func(key string) bool { _, ok := idx[strings.ToLower(key)]; return ok }
	get := func(row []string, key string) string {
		if i, ok := idx[strings.ToLower(key)]; ok && i < len(row) {
			return strings.TrimSpace(cellIn(row[i]))
		}
		return ""
	}
	// secret is get without trimming: a password may begin or end with a
	// space, and must read back as it was written.
	secret := func(row []string, key string) string {
		if i, ok := idx[strings.ToLower(key)]; ok && i < len(row) {
			return cellIn(row[i])
		}
		return ""
	}
	// The one-column-per-field jump hosts are there when any hop's host
	// column is (bastion1Host deleted in Excel leaves bastion2Host read).
	perHop := false
	for n := 1; n <= model.MaxBastions; n++ {
		perHop = perHop || has(hopKey(n, hopFields[1]))
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
			Role:           get(row, "role"),
			Host:           get(row, "host"),
			Conn:           model.ConnMethod(orDefault(connIn(get(row, "conn")), "ssh")),
			Port:           atoi(get(row, "port")),
			SerialPort:     get(row, "serialPort"),
			Baud:           atoi(get(row, "baud")),
			OSType:         orDefault(get(row, "osType"), "generic"),
			CommandSet:     get(row, "commandSet"),
			AuthMethod:     model.AuthMethod(orDefault(authIn(get(row, "authMethod")), "password")),
			KeyFile:        get(row, "keyFile"),
			KeyPassphrase:  secret(row, "keyPassphrase"),
			Username:       get(row, "username"),
			Password:       secret(row, "password"),
			EnablePassword: secret(row, "enablePassword"),
			LegacyAlgos:    parseBool(get(row, "legacyAlgos"), false),
			Enabled:        parseBool(get(row, "enabled"), true),
			UseGroupCreds:  parseBool(get(row, "useGroupCreds"), false),
			BastionMode:    modeIn(get(row, "bastionMode")),
			UseMyBastion:   parseBool(get(row, "useMyBastion"), false),
			GUIPort:        atoi(get(row, "guiPort")),
			CLIOff:         !parseBool(get(row, "cli"), true),
			GUIOn:          parseBool(get(row, "gui"), false),
			GUIScheme:      schemeIn(get(row, "guiScheme")),
		}
		if perHop {
			// One column per jump-host field. A hop is there when its host is.
			for n := 1; n <= model.MaxBastions; n++ {
				hv := func(f int) string { return get(row, hopKey(n, hopFields[f])) }
				hs := func(f int) string { return secret(row, hopKey(n, hopFields[f])) }
				if hv(1) == "" {
					continue
				}
				d.Bastions = append(d.Bastions, model.Bastion{
					Method:        model.BastionMethod(orDefault(connIn(hv(0)), "ssh")),
					Host:          hv(1),
					Port:          atoi(hv(2)),
					Username:      hv(3),
					Password:      hs(4),
					AuthMethod:    model.AuthMethod(orDefault(authIn(hv(5)), "password")),
					KeyFile:       hv(6),
					KeyPassphrase: hs(7),
					LegacyAlgos:   parseBool(hv(8), false),
					JumpCommand:   hv(9),
				})
			}
		}
		if len(d.Bastions) == 0 && get(row, "bastions") != "" {
			// v1.6.4 and before: the packed "bastions" column, its extras in
			// the JSON column (≤ v1.5.16) or per-hop extras columns.
			d.Bastions = decodeBastions(get(row, "bastions"))
			applyBastionExtras(d.Bastions, get(row, "bastionExtras"))
			for n := range d.Bastions {
				p := "bastion" + strconv.Itoa(n+1)
				b := &d.Bastions[n]
				if has(p + "KeyPassphrase") {
					b.KeyPassphrase = get(row, p+"KeyPassphrase")
				}
				if has(p + "LegacyAlgos") {
					b.LegacyAlgos = parseBool(get(row, p+"LegacyAlgos"), false)
				}
				if has(p + "JumpCommand") {
					b.JumpCommand = get(row, p+"JumpCommand")
				}
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// schemeIn reads a GUI scheme: http, or https (also for anything else).
func schemeIn(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "http":
		return "http"
	case "https":
		return "https"
	}
	return ""
}

// ---- the fixed values, in the words of the export's language ----

func connOut(v string, ja bool) string {
	if !ja {
		return v
	}
	switch v {
	case "ssh":
		return "SSH"
	case "telnet":
		return "Telnet"
	case "serial":
		return "シリアル"
	}
	return v
}

func connIn(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "ssh":
		return "ssh"
	case "telnet":
		return "telnet"
	case "serial", "シリアル":
		return "serial"
	}
	return strings.TrimSpace(v)
}

// modeOut writes how the jump hosts are passed through, for a device that
// has any ("shell" / "tunnel"; コマンド入力 / SSHトンネル).
func modeOut(v model.BastionMode, hops, ja bool) string {
	if !hops {
		return ""
	}
	if v == model.BastionViaTunnel {
		return pick(ja, "SSHトンネル", "tunnel")
	}
	return pick(ja, "コマンド入力", "shell")
}

func modeIn(v string) model.BastionMode {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), " ", "")) {
	case "", "shell", "コマンド入力":
		return model.BastionViaShell
	case "tunnel", "sshtunnel", "sshトンネル", "トンネル":
		return model.BastionViaTunnel
	}
	return model.BastionMode(strings.TrimSpace(v)) // Validate names it
}

func authOut(v string, ja bool) string {
	if !ja {
		return v
	}
	switch v {
	case "password", "":
		return "パスワード"
	case "publickey":
		return "公開鍵"
	}
	return v
}

func authIn(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "password", "パスワード":
		return "password"
	case "publickey", "公開鍵", "鍵":
		return "publickey"
	}
	return strings.TrimSpace(v)
}

func boolOut(b, ja bool) string {
	switch {
	case ja && b:
		return "はい"
	case ja:
		return "いいえ"
	case b:
		return "true"
	}
	return "false"
}

// ---- the packed "bastions" column of v1.6.4 and before (import only) ----

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

// bastionExtra is the per-hop part the packed column could not carry, as
// the JSON "bastionExtras" column of v1.5.16 and before wrote it.
type bastionExtra struct {
	KeyPassphrase string `json:"keyPassphrase,omitempty"`
	LegacyAlgos   bool   `json:"legacyAlgos,omitempty"`
	JumpCommand   string `json:"jumpCommand,omitempty"`
}

// applyBastionExtras merges the old JSON "bastionExtras" column into the
// decoded hops by position. A malformed or absent column leaves them as is.
func applyBastionExtras(bs []model.Bastion, s string) {
	s = strings.TrimSpace(s)
	if s == "" || len(bs) == 0 {
		return
	}
	var extras []bastionExtra
	if err := json.Unmarshal([]byte(s), &extras); err != nil {
		return
	}
	for i := range bs {
		if i >= len(extras) {
			break
		}
		bs[i].KeyPassphrase = extras[i].KeyPassphrase
		bs[i].LegacyAlgos = extras[i].LegacyAlgos
		bs[i].JumpCommand = extras[i].JumpCommand
	}
}

func itoa(i int) string {
	if i == 0 {
		return ""
	}
	return strconv.Itoa(i)
}
func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }
func parseBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "y", "on", "はい", "○", "◯", "有効":
		return true
	case "false", "0", "no", "n", "off", "いいえ", "×", "無効":
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
