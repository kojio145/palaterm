package csvio

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/kojio145/palaterm/internal/model"
)

// The device table editor (機器一覧の「表で編集」) works on the same columns
// as the CSV, one cell per value, so what can be typed in Excel can be typed
// in the table and the two never disagree. A row is a map from the column
// key (the English CSV header) to its text; the values are the canonical
// English words of an English export (ssh, password, true, tunnel), which
// the table shows in the user's language.

// GridCol is one column of the table editor.
type GridCol struct {
	Key string `json:"key"`
	Ja  string `json:"ja"` // header, translated by the UI like any Japanese text
	// Kind tells the editor how to show and normalise the cell: text,
	// secret, int, bool, conn, auth, mode, hopMethod, os, set, group.
	Kind string `json:"kind"`
	// Hop is the jump host's number for its columns (1–MaxBastions), Field
	// its field's Japanese name.
	Hop   int    `json:"hop,omitempty"`
	Field string `json:"field,omitempty"`
}

var deviceKinds = map[string]string{
	"group": "group", "conn": "conn", "port": "int", "baud": "int",
	"osType": "os", "commandSet": "set", "enabled": "bool", "useGroupCreds": "bool",
	"password": "secret", "enablePassword": "secret", "keyPassphrase": "secret",
	"authMethod": "auth", "legacyAlgos": "bool", "bastionMode": "mode", "guiPort": "int", "useMyBastion": "bool",
	"cli": "bool", "gui": "bool", "guiScheme": "scheme",
}

var hopKinds = map[string]string{
	"Method": "hopMethod", "Port": "int", "Password": "secret", "AuthMethod": "auth",
	"KeyPassphrase": "secret", "LegacyAlgos": "bool",
}

// GridColumns lists every column: the device's own, then each jump host's.
func GridColumns() []GridCol {
	var out []GridCol
	for _, c := range deviceCols {
		out = append(out, GridCol{Key: c.key, Ja: c.ja, Kind: orDefault(deviceKinds[c.key], "text")})
	}
	for n := 1; n <= model.MaxBastions; n++ {
		for _, f := range hopFields {
			out = append(out, GridCol{Key: hopKey(n, f), Ja: hopJa(n, f), Kind: orDefault(hopKinds[f.key], "text"), Hop: n, Field: f.ja})
		}
	}
	return out
}

// DeviceCells renders a device as a table row (only the jump hosts it has).
func DeviceCells(d model.Device) map[string]string {
	d.NormalizeBastions()
	c := map[string]string{
		"name": d.Name, "group": d.Group, "site": d.Site, "role": d.Role,
		"host": d.Host, "conn": string(d.Conn), "port": itoa(d.Port),
		"serialPort": d.SerialPort, "baud": itoa(d.Baud),
		"osType": d.OSType, "commandSet": d.CommandSet,
		"enabled": boolOut(d.Enabled, false), "useGroupCreds": boolOut(d.UseGroupCreds, false),
		"username": d.Username, "password": d.Password, "enablePassword": d.EnablePassword,
		"authMethod": orDefault(string(d.AuthMethod), "password"), "keyFile": d.KeyFile, "keyPassphrase": d.KeyPassphrase,
		"legacyAlgos": boolOut(d.LegacyAlgos, false), "bastionMode": modeOut(d.BastionMode, len(d.Bastions) > 0 || d.UseMyBastion, false),
		"useMyBastion": boolOut(d.UseMyBastion, false),
		"guiPort":      itoa(d.GUIPort),
		"cli":          boolOut(!d.CLIOff, false), "gui": boolOut(d.GUIOn, false), "guiScheme": d.GUIScheme,
	}
	for i, b := range d.Bastions {
		if i >= model.MaxBastions {
			break
		}
		p := "bastion" + strconv.Itoa(i+1)
		c[p+"Method"] = string(b.Method)
		c[p+"Host"] = b.Host
		c[p+"Port"] = itoa(b.Port)
		c[p+"Username"] = b.Username
		c[p+"Password"] = b.Password
		c[p+"AuthMethod"] = orDefault(string(b.AuthMethod), "password")
		c[p+"KeyFile"] = b.KeyFile
		c[p+"KeyPassphrase"] = b.KeyPassphrase
		c[p+"LegacyAlgos"] = boolOut(b.LegacyAlgos, false)
		c[p+"JumpCommand"] = b.JumpCommand
	}
	return c
}

// CellError is a problem with one cell of a row.
type CellError struct {
	Col string `json:"col"`
	Msg string `json:"msg"`
}

// CellsDevice reads a table row back into a device, strictly: where the CSV
// import falls back to a default for a word it does not know, the table
// says which cell is wrong. The device's own checks (Validate) are not run
// here.
func CellsDevice(c map[string]string) (model.Device, []CellError) {
	var errs []CellError
	get := func(k string) string { return strings.TrimSpace(c[k]) }
	bad := func(k, msg string) { errs = append(errs, CellError{k, msg}) }
	num := func(k string) int {
		v := get(k)
		if v == "" {
			return 0
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			bad(k, "数字で入力してください")
			return 0
		}
		return n
	}
	flag := func(k string, def bool) bool {
		v := get(k)
		if v == "" {
			return def
		}
		if !boolWord(v) {
			bad(k, "はい／いいえで入力してください")
			return def
		}
		return parseBool(v, def)
	}
	conn := func(k string, allowed ...string) string {
		v := strings.ToLower(connIn(get(k)))
		if v == "" {
			return "ssh"
		}
		for _, a := range allowed {
			if v == a {
				return v
			}
		}
		bad(k, "接続方式は "+strings.Join(allowed, " / ")+" のどれかです")
		return "ssh"
	}
	auth := func(k string) model.AuthMethod {
		v := authIn(get(k))
		switch v {
		case "":
			return model.AuthPassword
		case "password", "publickey":
			return model.AuthMethod(v)
		}
		bad(k, "認証方式は パスワード / 公開鍵 のどちらかです")
		return model.AuthPassword
	}

	scheme := func(k string) string {
		switch v := strings.ToLower(get(k)); v {
		case "", "https":
			return "" // https is the default, stored as ""
		case "http":
			return v
		}
		bad(k, "GUIの方式は http / https のどちらかです")
		return ""
	}

	d := model.Device{
		Name: get("name"), Group: get("group"), Site: get("site"), Role: get("role"),
		Host: get("host"), Conn: model.ConnMethod(conn("conn", "ssh", "telnet", "serial")),
		Port: num("port"), SerialPort: get("serialPort"), Baud: num("baud"),
		OSType: orDefault(get("osType"), "generic"), CommandSet: get("commandSet"),
		Enabled: flag("enabled", true), UseGroupCreds: flag("useGroupCreds", false),
		Username: get("username"), Password: c["password"], EnablePassword: c["enablePassword"],
		AuthMethod: auth("authMethod"), KeyFile: get("keyFile"), KeyPassphrase: c["keyPassphrase"],
		LegacyAlgos: flag("legacyAlgos", false), GUIPort: num("guiPort"), UseMyBastion: flag("useMyBastion", false),
		CLIOff: !flag("cli", true), GUIOn: flag("gui", false), GUIScheme: scheme("guiScheme"),
	}
	switch m := modeIn(get("bastionMode")); m {
	case model.BastionViaShell, model.BastionViaTunnel:
		d.BastionMode = m
	default:
		bad("bastionMode", "踏み台の通り方は コマンド入力 / SSHトンネル のどちらかです")
	}
	for n := 1; n <= model.MaxBastions; n++ {
		p := "bastion" + strconv.Itoa(n)
		used := false
		for _, f := range hopFields {
			if v := get(p + f.key); v != "" && !(f.key == "Method" && v == "ssh") && !(f.key == "AuthMethod" && v == "password") && !(f.key == "LegacyAlgos" && !parseBool(v, true)) {
				used = true
			}
		}
		if get(p+"Host") == "" {
			if used {
				bad(p+"Host", fmt.Sprintf("踏み台%dのホストが空です（この段を使わないなら踏み台%dの欄をすべて空にしてください）", n, n))
			}
			continue
		}
		d.Bastions = append(d.Bastions, model.Bastion{
			Method:        model.BastionMethod(conn(p+"Method", "ssh", "telnet")),
			Host:          get(p + "Host"),
			Port:          num(p + "Port"),
			Username:      get(p + "Username"),
			Password:      c[p+"Password"],
			AuthMethod:    auth(p + "AuthMethod"),
			KeyFile:       get(p + "KeyFile"),
			KeyPassphrase: c[p+"KeyPassphrase"],
			LegacyAlgos:   flag(p+"LegacyAlgos", false),
			JumpCommand:   get(p + "JumpCommand"),
		})
	}
	if len(d.Bastions) == 0 && !d.UseMyBastion {
		d.BastionMode = model.BastionViaShell
	}
	// A device outside every group has no group defaults to use.
	if d.Group == "" {
		d.UseGroupCreds = false
	}
	return d, errs
}

func boolWord(v string) bool {
	return parseBool(v, true) == parseBool(v, false)
}

var reHopN = regexp.MustCompile(`(?:踏み台|（)(\d)段目`)

// ErrorColumn names the column a Validate message is about, so the table
// can mark the cell; a message it cannot place goes on the name.
func ErrorColumn(msg string) string {
	if m := reHopN.FindStringSubmatch(msg); m != nil {
		p := "bastion" + m[1]
		switch {
		case strings.Contains(msg, "すべて SSH") || strings.Contains(msg, "接続方式"):
			return p + "Method"
		case strings.Contains(msg, "ポート"):
			return p + "Port"
		case strings.Contains(msg, "ユーザー名"):
			return p + "Username"
		case strings.Contains(msg, "秘密鍵"):
			return p + "KeyFile"
		}
		return p + "Host"
	}
	switch {
	case strings.Contains(msg, "すべて SSH"):
		return "bastion1Method"
	case strings.Contains(msg, "GUIポート"):
		return "guiPort"
	case strings.Contains(msg, "GUIの方式"):
		return "guiScheme"
	case strings.Contains(msg, "CLI と GUI"):
		return "cli"
	case strings.Contains(msg, "踏み台の通り方"):
		return "bastionMode"
	case strings.Contains(msg, "シリアル接続の機器は踏み台"):
		return "conn"
	case strings.Contains(msg, "名前"), strings.Contains(msg, "機器名"), strings.Contains(msg, "ホスト名"):
		return "name"
	case strings.Contains(msg, "IPアドレス"):
		return "host"
	case strings.Contains(msg, "ポート"):
		return "port"
	case strings.Contains(msg, "ボーレート"):
		return "baud"
	case strings.Contains(msg, "接続方式"):
		return "conn"
	case strings.Contains(msg, "秘密鍵"):
		return "keyFile"
	case strings.Contains(msg, "ユーザー名"):
		return "username"
	}
	return "name"
}
