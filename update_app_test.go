package main

import "testing"

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.9.42", "1.9.41", true},
		{"v1.10.0", "1.9.41", true},
		{"v2.0.0", "1.9.41", true},
		{"v1.9.41", "1.9.41", false},
		{"v1.9.40", "1.9.41", false},
		{"1.9.9", "1.9.10", false},
		{"v1.9.42-beta", "1.9.41", true},
	}
	for _, c := range cases {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if filepathSafe("PalaTerm-v1.9.42.exe") != "PalaTerm-v1.9.42.exe" || filepathSafe(`a\..\b`) != "a_.._b" {
		t.Fatal("file name")
	}
}

func TestUsageNotes(t *testing.T) {
	guide := "# PalaTerm\r\n\r\n## v1.9.42 での変更（題 A）\r\n\r\n- 直したこと\r\n\r\n## v1.9.41 での変更（題 B）\r\n\r\n- 前の版\r\n\r\n## 付録\r\n\r\nほか\r\n"
	n := usageNotes(guide)
	if len(n) != 2 || n["1.9.42"].Title != "題 A" || n["1.9.42"].Body != "- 直したこと" || n["1.9.41"].Body != "- 前の版" {
		t.Fatalf("%+v", n)
	}
	if got := noteBody("Fixes.\r\n\r\n## Download\r\n\r\nzip"); got != "Fixes." {
		t.Fatalf("noteBody: %q", got)
	}
}
