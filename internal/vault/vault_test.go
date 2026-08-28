package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inv.enc")

	inv := &model.Inventory{
		Version: 1,
		Devices: []model.Device{{
			Name: "core-sw1", Host: "10.0.0.1", Conn: model.ConnSSH,
			OSType: "cisco-ios", Username: "admin", Password: "s3cr3t",
			EnablePassword: "en@ble", Enabled: true,
		}},
		Settings: model.DefaultSettings(),
	}

	if err := Save(path, "master-pw", inv); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path, "master-pw")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Devices) != 1 || got.Devices[0].Password != "s3cr3t" {
		t.Fatalf("round trip mismatch: %+v", got.Devices)
	}
}

func TestWrongPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inv.enc")
	inv := &model.Inventory{Version: 1, Settings: model.DefaultSettings()}
	if err := Save(path, "correct", inv); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, "wrong"); err != ErrWrongPassword {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}
}

func TestPlaintextNotOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inv.enc")
	inv := &model.Inventory{
		Version:  1,
		Devices:  []model.Device{{Name: "d", Password: "TOPSECRET123", Enabled: true}},
		Settings: model.DefaultSettings(),
	}
	if err := Save(path, "pw", inv); err != nil {
		t.Fatal(err)
	}
	data, err := readFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if contains(data, "TOPSECRET123") {
		t.Fatal("plaintext password found in encrypted file")
	}
}

// A pre-rename vault (old "EXSSHv1\n" magic) is rejected with a clear error,
// not treated as a wrong password.
func TestOldMagicRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.enc")

	// Fabricate an old-format file: old magic + plausible salt/nonce/ciphertext.
	oldMagic := []byte("EXSSHv1\n")
	body := make([]byte, saltLen+nonceLen+64)
	buf := append(append([]byte{}, oldMagic...), body...)
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path, "pw")
	if err == nil {
		t.Fatal("old-magic vault must be rejected")
	}
	if err == ErrWrongPassword {
		t.Fatal("old-magic vault should fail as a format error, not a wrong password")
	}
}
