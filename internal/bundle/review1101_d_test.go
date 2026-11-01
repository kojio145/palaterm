package bundle

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/model"
	"github.com/kojio145/palaterm/internal/profile"
)

func labInventory() *model.Inventory {
	return &model.Inventory{
		Devices: []model.Device{
			{Name: "lab-sw1", Host: "192.0.2.10", Conn: model.ConnSSH, Group: "lab", OSType: "generic", Username: "ops", Password: "Lab-Pw1", EnablePassword: "Lab-En1", KeyFile: "C:\\keys\\lab.key", KeyPassphrase: "kp", Enabled: true,
				Bastions: []model.Bastion{{Host: "192.0.2.9", Method: model.BastionSSH, Username: "jops", Password: "jp"}}},
			{Name: "lab-sw2", Host: "192.0.2.11", Conn: model.ConnSSH, Group: "lab", OSType: "generic", UseGroupCreds: true, Enabled: true},
		},
		DeviceGroups: []model.DeviceGroup{{Name: "lab", Username: "gops", Password: "Lab-Gp1", EnablePassword: "Lab-Ge1"}},
	}
}

// SelectWith(noDeviceCreds) leaves the devices' logins and the group's
// defaults out and says so in the manifest; Select (3 arguments) is the
// old behaviour, credentials included.
func TestSelectWithoutDeviceCreds(t *testing.T) {
	c := SelectWith(labInventory(), "lab", false, true)
	d := c.Devices[0]
	if d.Username != "" || d.Password != "" || d.EnablePassword != "" || d.KeyPassphrase != "" {
		t.Errorf("device credentials exported: %q %q %q %q", d.Username, d.Password, d.EnablePassword, d.KeyPassphrase)
	}
	if d.KeyFile != "C:\\keys\\lab.key" || d.Host != "192.0.2.10" {
		t.Errorf("non-secret fields changed: %+v", d)
	}
	if d.Bastions[0].Password != "jp" {
		t.Errorf("jump host login dropped without noBastionCreds")
	}
	if g := c.Manifest.Groups[0]; g.Name != "lab" || g.Username != "" || g.Password != "" || g.EnablePassword != "" {
		t.Errorf("group credentials exported: %+v", g)
	}
	if !c.Manifest.DeviceCredsOmitted {
		t.Error("manifest does not say the credentials were omitted")
	}
	text, _ := formatAll(c)
	for _, secret := range []string{"Lab-Pw1", "Lab-En1", "Lab-Gp1", "Lab-Ge1", "kp"} {
		if strings.Contains(text, secret) {
			t.Errorf("%q in the exported files", secret)
		}
	}

	old := Select(labInventory(), "lab", false)
	if old.Devices[0].Password != "Lab-Pw1" || old.Manifest.Groups[0].Password != "Lab-Gp1" || old.Manifest.DeviceCredsOmitted {
		t.Errorf("Select changed: %+v %+v", old.Devices[0], old.Manifest)
	}
}

// formatAll writes c to a folder and returns every file's text joined.
func formatAll(c Contents) (string, error) {
	dir, err := os.MkdirTemp("", "palaterm-bundle-test")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if _, err := Write(dir, c, "test"); err != nil {
		return "", err
	}
	var sb strings.Builder
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(p)
			sb.Write(b)
		}
		return nil
	})
	return sb.String(), nil
}

// Every file of a bundle is 0600 and every folder 0700 (mode bits do not
// apply on Windows).
func TestBundleFilesPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
	dir := filepath.Join(t.TempDir(), "out")
	inv := sampleInventory()
	if _, err := Write(dir, Select(inv, "", false), "test"); err != nil {
		t.Fatal(err)
	}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %o, want %o", p, info.Mode().Perm(), want)
		}
		return nil
	})
}

// A manifest or CSV over the size cap is refused before it is read whole.
func TestReadRefusesHugeFiles(t *testing.T) {
	dir := t.TempDir()
	mp := filepath.Join(dir, ManifestName)
	if err := os.WriteFile(mp, append([]byte(`{"format":"palaterm-bundle/1"}`), make([]byte, maxJSONFile)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(mp); err == nil || !strings.Contains(err.Error(), "大きすぎます") {
		t.Errorf("huge manifest: %v", err)
	}
	csvPath := filepath.Join(dir, "devices.csv")
	if err := os.WriteFile(csvPath, append([]byte("name\r\n"), make([]byte, maxCSVFile)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDevicesCSV(csvPath); err == nil || !strings.Contains(err.Error(), "大きすぎます") {
		t.Errorf("huge CSV: %v", err)
	}
	if err := os.WriteFile(mp, []byte(`{"format":"palaterm-bundle/1","devices":"devices.csv"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(mp); err == nil || !strings.Contains(err.Error(), "機器CSVが読めません") {
		t.Errorf("bundle with a huge CSV: %v", err)
	}
}

// A row that says useGroupCreds does not take the device's own username,
// password and enable password over (blanking them was the point); the key
// file and its passphrase still are.
func TestKeepCredentialsUseGroupCreds(t *testing.T) {
	inv := labInventory()
	row := model.Device{Name: "lab-sw1", Host: "192.0.2.10", Conn: model.ConnSSH, Group: "lab", OSType: "generic", UseGroupCreds: true, Enabled: true}
	kept := keepCredentials(&row, inv.Devices[0])
	if row.Username != "" || row.Password != "" || row.EnablePassword != "" {
		t.Errorf("own login kept on a useGroupCreds row: %q %q %q", row.Username, row.Password, row.EnablePassword)
	}
	if !kept || row.KeyFile != "C:\\keys\\lab.key" || row.KeyPassphrase != "kp" {
		t.Errorf("key file / passphrase not kept: kept=%v %q %q", kept, row.KeyFile, row.KeyPassphrase)
	}
	row = model.Device{Name: "lab-sw1", Host: "192.0.2.10", Conn: model.ConnSSH, Group: "lab", OSType: "generic", Enabled: true}
	keepCredentials(&row, inv.Devices[0])
	if row.Password != "Lab-Pw1" {
		t.Errorf("own login not kept on an ordinary row: %q", row.Password)
	}
}

// The summary lists the profile steps that send a password to something
// other than a password prompt, and the useGroupCreds devices whose target
// group has no credentials (an import into a new group).
func TestSummarySecretStepsAndGroupNoCreds(t *testing.T) {
	c := Select(labInventory(), "", false)
	c.Profiles = append(c.Profiles, profile.Profile{Key: "custom:LabOS", Name: "LabOS", Prompt: "#",
		Login: []profile.Step{{Expect: "sername:", Send: "{password}"}, {Expect: "assword:", Send: "{password}"}}})
	s := Preview(&model.Inventory{}, c, Options{TargetGroup: "lab-import"})
	if len(s.SecretSteps) != 1 || s.SecretSteps[0] != "LabOS: ログイン手順 1: 「sername:」 → {password}" {
		t.Errorf("SecretSteps = %q", s.SecretSteps)
	}
	if len(s.GroupNoCreds) != 1 || s.GroupNoCreds[0] != "lab-import / lab-sw2" {
		t.Errorf("GroupNoCreds = %q", s.GroupNoCreds)
	}
	// Into the groups the bundle names, their credentials come along.
	s = Preview(&model.Inventory{}, c, Options{})
	if len(s.GroupNoCreds) != 0 {
		t.Errorf("GroupNoCreds with the bundle's groups = %q", s.GroupNoCreds)
	}
}
