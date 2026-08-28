package csvio

import (
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

func TestExportImportRoundTrip(t *testing.T) {
	in := []model.Device{
		{
			Name: "core-sw1", Host: "10.0.0.1", Conn: model.ConnSSH, Port: 22,
			OSType: "cisco-ios", CommandSet: "backup", AuthMethod: model.AuthPassword,
			Username: "admin", Password: "p@ss,word", EnablePassword: "en", Enabled: true,
		},
		{
			Name: "edge-fw", Host: "10.0.0.2", Conn: model.ConnTelnet,
			OSType: "fortinet-fortios", Username: "adm", Password: "x", Enabled: false,
			Bastions: []model.Bastion{
				{Method: model.BastionSSH, Host: "jump1", Username: "j1", Password: "jp1",
					AuthMethod: model.AuthPublicKey, KeyFile: `C:\Users\me\.ssh\id_ed25519`},
				{Method: model.BastionTelnet, Host: "jump2", Username: "j2", Password: "jp2"},
			},
		},
		{
			Name: "key-sw", Host: "10.0.0.3", Conn: model.ConnSSH,
			OSType: "cisco-ios", AuthMethod: model.AuthPublicKey,
			KeyFile: `C:\Users\me\.ssh\id_rsa`, KeyPassphrase: "s3cret pass", Enabled: true,
		},
	}

	text, err := Export(in)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	out, err := Import(text)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 devices, got %d", len(out))
	}
	// Password containing a comma must survive CSV quoting.
	if out[0].Password != "p@ss,word" {
		t.Errorf("password round trip failed: %q", out[0].Password)
	}
	if out[1].Conn != model.ConnTelnet || out[1].Enabled {
		t.Errorf("device2 fields wrong: %+v", out[1])
	}
	if len(out[1].Bastions) != 2 {
		t.Fatalf("want 2 bastions, got %d", len(out[1].Bastions))
	}
	if out[1].Bastions[0].Host != "jump1" || out[1].Bastions[1].Method != model.BastionTelnet {
		t.Errorf("bastion chain round trip failed: %+v", out[1].Bastions)
	}
	// A Windows key path's own colons must survive the bastion encoding.
	if out[1].Bastions[0].KeyFile != `C:\Users\me\.ssh\id_ed25519` {
		t.Errorf("bastion keyFile round trip failed: %q", out[1].Bastions[0].KeyFile)
	}
	if out[2].KeyPassphrase != "s3cret pass" {
		t.Errorf("keyPassphrase round trip failed: %q", out[2].KeyPassphrase)
	}
}

func TestImportMinimalColumns(t *testing.T) {
	// Only name + host + conn given; the rest should default sanely.
	csv := "name,host,conn\nsw1,10.0.0.1,ssh\n"
	out, err := Import(csv)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Name != "sw1" || out[0].Enabled != true {
		t.Fatalf("minimal import wrong: %+v", out)
	}
	if out[0].OSType != "generic" {
		t.Errorf("expected generic OS default, got %q", out[0].OSType)
	}
}

func TestImportCapsBastionsAtMax(t *testing.T) {
	csv := "name,host,bastions\n" +
		"d,10.0.0.1,ssh:h1::::|ssh:h2::::|ssh:h3::::|ssh:h4::::|ssh:h5::::|ssh:h6::::\n"
	out, err := Import(csv)
	if err != nil {
		t.Fatal(err)
	}
	if len(out[0].Bastions) != model.MaxBastions {
		t.Fatalf("expected chain capped at %d, got %d", model.MaxBastions, len(out[0].Bastions))
	}
}
