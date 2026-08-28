package runner

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

func TestBuildJumpCommand(t *testing.T) {
	cases := []struct {
		name     string
		template string
		method   model.BastionMethod
		host     string
		user     string
		port     int
		want     string
	}{
		{"ssh標準", "", model.BastionSSH, "10.0.0.1", "admin", 0, "ssh admin@10.0.0.1"},
		{"sshユーザーなし", "", model.BastionSSH, "10.0.0.1", "", 0, "ssh 10.0.0.1"},
		{"telnet標準", "", model.BastionTelnet, "10.0.0.2", "admin", 0, "telnet 10.0.0.2"},
		{"IOS形式テンプレート", "ssh -l {user} {host}", model.BastionSSH, "10.0.0.3", "op", 0, "ssh -l op 10.0.0.3"},
		{"ポート付きテンプレート", "ssh -p {port} {user}@{host}", model.BastionSSH, "h1", "u1", 2222, "ssh -p 2222 u1@h1"},
		{"ポート0は空文字", "telnet {host} {port}", model.BastionTelnet, "h2", "", 0, "telnet h2 "},
		{"テンプレートはmethodより優先", "connect {host}", model.BastionTelnet, "h3", "u3", 0, "connect h3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildJumpCommand(c.template, c.method, c.host, c.user, c.port)
			if got != c.want {
				t.Errorf("buildJumpCommand(%q) = %q, want %q", c.template, got, c.want)
			}
		})
	}
}
