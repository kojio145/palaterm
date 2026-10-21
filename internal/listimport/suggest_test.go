package listimport

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/sheet"
)

func sugMap(fs []model.ImportField) map[string]string {
	m := map[string]string{}
	for _, f := range fs {
		m[f.Key] = f.Column
	}
	return m
}

// A list with a title, a group row over the header, and the pair's blocks
// repeating their names: the header row is found, the usual fields get
// their columns, and the second block becomes device 2.
func TestSuggest(t *testing.T) {
	s := &sheet.Sheet{Rows: [][]string{
		{"機器台帳（架空）"},
		{},
		{"管理", "", "メイン", "メイン", "メイン", "バックアップ", "バックアップ", "共通", "共通", "共通", "共通"},
		{"No", "接続先 担当", "ホスト名", "管理IP", "役割", "ホスト名", "管理IP", "接続方式", "設置場所", "ユーザー名", "パスワード"},
		{"1", "ops", "fw01a", "192.0.2.1", "FW", "fw01b", "192.0.2.2", "SSH", "本社", "ops", "x9"},
		{"2", "ops", "core01", "192.0.2.11", "コア", "", "", "TELNET", "本社", "ops", "x9"},
	}}
	sg := Suggest(s, model.ImportRule{HeaderRow: 1})
	if sg.HeaderRow != 4 {
		t.Fatalf("header row = %d, want 4", sg.HeaderRow)
	}
	got := sugMap(sg.Fields)
	want := map[string]string{"name": "ホスト名", "host": "管理IP", "role": "役割", "conn": "接続方式", "site": "設置場所", "username": "ユーザー名", "password": "パスワード"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all %v)", k, got[k], v, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("fields = %v", got)
	}
	unit := sugMap(sg.Unit)
	if unit["name"] != "ホスト名 (2)" || unit["host"] != "管理IP (2)" || len(unit) != 2 {
		t.Errorf("unit = %v", unit)
	}
	// The guessed rule reads the sample as meant.
	r := &model.ImportRule{Name: "x", HeaderRow: sg.HeaderRow, Fields: sg.Fields, Units: []model.ImportUnit{{Fields: sg.Unit}}}
	if err := ValidateRule(r); err != nil {
		t.Fatal(err)
	}
	c := convert(t, s, r)
	if len(c.Rows) != 3 || c.Rows[1].Values["name"] != "fw01b" || c.Rows[1].Values["host"] != "192.0.2.2" || c.Rows[2].Values["conn"] != "telnet" {
		t.Errorf("rows = %+v", c.Rows)
	}
}

func TestSuggestKeepsAChosenHeaderRow(t *testing.T) {
	s := &sheet.Sheet{Rows: [][]string{{"x"}, {"機器名", "IP"}, {"sw1", "192.0.2.1"}}}
	if sg := Suggest(s, model.ImportRule{HeaderRow: 2}); sg.HeaderRow != 2 || sugMap(sg.Fields)["name"] != "機器名" {
		t.Errorf("%+v", sg)
	}
	// No second block, no device 2; an email column is no address.
	s = &sheet.Sheet{Rows: [][]string{{"装置名", "メールアドレス", "IPアドレス"}, {"sw1", "ops", "192.0.2.1"}}}
	sg := Suggest(s, model.ImportRule{HeaderRow: 1})
	if m := sugMap(sg.Fields); m["host"] != "IPアドレス" || m["name"] != "装置名" || len(sg.Unit) != 0 {
		t.Errorf("%+v", sg)
	}
}
