package csvio

import "testing"

// D-C7: the check marks of a hand-kept list are yes, a lone x is no.
func TestParseBoolCheckMarks(t *testing.T) {
	for _, w := range []string{"✓", "✔", "レ", "済", "〇", "○"} {
		if !parseBool(w, false) {
			t.Errorf("%q not read as yes", w)
		}
	}
	for _, w := range []string{"x", "X", "✗", "×"} {
		if parseBool(w, true) {
			t.Errorf("%q not read as no", w)
		}
	}
	if !parseBool("maybe", true) || parseBool("maybe", false) {
		t.Error("an unknown word must give the default")
	}
}

// D-B1 / D-A9: a row whose only hop is the third names hop 3 after the
// gap, and a Validate message about the device's (renumbered) hop lands
// on the row's column.
func TestHopGapNextAndHopErrorColumn(t *testing.T) {
	c := map[string]string{"bastion3Host": "192.0.2.3"}
	if gap, next := HopGapNext(c); gap != 1 || next != 3 {
		t.Errorf("HopGapNext = %d, %d, want 1, 3", gap, next)
	}
	if gap, next := HopGapNext(map[string]string{"bastion1Host": "192.0.2.1"}); gap != 0 || next != 0 {
		t.Errorf("no gap: %d, %d", gap, next)
	}
	if n := HopNumbers(map[string]string{"bastion2Host": "a", "bastion3Host": "b"}); len(n) != 2 || n[0] != 2 || n[1] != 3 {
		t.Errorf("HopNumbers = %v", n)
	}
	row := map[string]string{"bastion2Host": "192.0.2.2"}
	if col := HopErrorColumn("踏み台1段目のユーザー名は必須です", row); col != "bastion2Username" {
		t.Errorf("hop 1 message on a row with hop 2 only → %s", col)
	}
	if col := HopErrorColumn("踏み台1段目のユーザー名は必須です", map[string]string{"bastion1Host": "x"}); col != "bastion1Username" {
		t.Errorf("no gap → %s", col)
	}
	if col := HopErrorColumn("IPアドレスの形式が不正です: \"x\"", row); col != "host" {
		t.Errorf("non-hop message → %s", col)
	}
}

// D-C1: a control character in the group / site / role, a hop's jump
// command or auth method, mark their own columns.
func TestErrorColumnLabelsAndHopFields(t *testing.T) {
	for msg, col := range map[string]string{
		"グループに制御文字（改行・タブなど）が含まれています: \"a\nb\"":             "group",
		"拠点に制御文字（改行・タブなど）が含まれています: \"a\tb\"":               "site",
		"役割に制御文字（改行・タブなど）が含まれています: \"a\nb\"":               "role",
		"名前に制御文字（改行・タブなど）が含まれています: \"a\nb\"":               "name",
		"踏み台2段目のジャンプコマンドに制御文字（改行・タブなど）が含まれています: \"ssh\n\"": "bastion2JumpCommand",
		"踏み台3段目の認証方式が不正です":                                 "bastion3AuthMethod",
	} {
		if got := ErrorColumn(msg); got != col {
			t.Errorf("%s → %s, want %s", msg, got, col)
		}
	}
}
