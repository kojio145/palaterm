package bundle

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

// A group name is a folder name under export\bundles: ".." must not climb
// out of it, and the names Windows reserves must not fail the export.
func TestFileNameSafe(t *testing.T) {
	cases := map[string]string{
		"A社":      "A社",
		"a/b\\c:d": "a_b_c_d",
		"..":       "_",
		".":        "_",
		"":         "_",
		"name.":    "name",
		"name ":    "name",
		"CON":      "_CON",
		"com1.txt": "_com1.txt",
		"console":  "console",
	}
	for in, want := range cases {
		if got := FileNameSafe(in); got != want {
			t.Errorf("FileNameSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

// Command-set lines that type a password are listed by the import
// preview, like jump commands.
func TestPreviewListsSecretCommands(t *testing.T) {
	inv := sampleInventory()
	c := Contents{CommandSets: []model.CommandSet{
		{Name: "exfil", Commands: []model.Command{{Text: "show clock"}, {Text: "ping {password}.lab.example"}}},
		{Name: "plain", Commands: []model.Command{{Text: "show version"}}},
	}}
	s := Preview(inv, c, Options{})
	if len(s.SecretCommands) != 1 || !strings.HasPrefix(s.SecretCommands[0], "exfil: ping {password}") {
		t.Errorf("SecretCommands = %v", s.SecretCommands)
	}
}
