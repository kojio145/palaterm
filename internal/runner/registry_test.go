package runner

import (
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

// testRegistry mirrors the app's unlock path: the default profiles quoted
// into an otherwise empty registry.
func testRegistry() *profile.Registry {
	reg := profile.NewRegistry()
	for _, p := range profile.Defaults() {
		reg.Add(profile.Quote(p))
	}
	return reg
}

// Secrets (device / enable / bastion passwords) are masked out of saved logs;
// very short ones are left alone to avoid shredding the log text.
func TestRedactSecrets(t *testing.T) {
	dev := &model.Device{Password: "s3cretPW", EnablePassword: "enable99",
		Bastions: []model.Bastion{{Host: "j", Method: model.BastionSSH, Password: "jumpPW99"}}}
	in := "login s3cretPW then enable99 via jumpPW99 done"
	out := RedactSecrets(in, dev)
	for _, leak := range []string{"s3cretPW", "enable99", "jumpPW99"} {
		if strings.Contains(out, leak) {
			t.Fatalf("secret %q leaked: %s", leak, out)
		}
	}
	short := &model.Device{Password: "ab"}
	if RedactSecrets("cable abs", short) != "cable abs" {
		t.Fatal("short passwords must not be redacted")
	}
}
