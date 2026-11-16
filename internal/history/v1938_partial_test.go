package history

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kojio145/palaterm/internal/logstore"
)

// v1.9.38 (2026-11-16): two folders that do not hold the same number of
// files compare what they can — the number of a device is not taken for a
// part of the date that follows it, a file numbered in the order it was
// taken carries its name without the number, and a run folder without a
// summary does not make one device of several.

func TestV1938DeviceNumberBeforeDate(t *testing.T) {
	tok := logstore.StageTokens{}
	for in, want := range map[string]string{
		// A device's number followed by a date that is whole in itself.
		"lab-sw-01_20261101_0900.log":    "lab-sw-01",
		"lab-sw-02_20261101_0900.log":    "lab-sw-02",
		"lab-sw-01_20261101.log":         "lab-sw-01",
		"lab-rt-10_20261101090000.log":   "lab-rt-10",
		"lab-sw-01_2026-11-01.log":       "lab-sw-01",
		"lab-sw-02_2026-11-01_09-00.log": "lab-sw-02",
		"lab-sw-03_2026年11月01日.log":      "lab-sw-03",
		"lab-sw-01_before_20261101.log":  "lab-sw-01",
		// A date or a time written in parts: as before.
		"sw01_20260901_14_30.txt":   "sw01",
		"sw2_2026-09-01_14h30.log":  "sw2",
		"sw01_09-01-2026.log":       "sw01",
		"sw01_11_01_0900.log":       "sw01",
		"lab-sw-02_09-01-2026.log":  "lab-sw-02",
		"lab-sw-02_11_01_0900.log":  "lab-sw-02",
		"20261101_090000_sw01.log":  "sw01",
		"2026-11-01_lab-sw-01.log":  "lab-sw-01",
		"192.0.2.1_20261101_09.log": "192.0.2.1",
	} {
		if got := externalDeviceKey(in, tok); got != want {
			t.Errorf("externalDeviceKey(%q) = %q, want %q", in, got, want)
		}
	}
	// One number before a year or a time is the device's: two devices do
	// not become one.
	a, b := externalDeviceKey("lab-sw-02_1101.log", tok), externalDeviceKey("lab-sw-03_1101.log", tok)
	if a == b {
		t.Errorf("lab-sw-02 and lab-sw-03 are one device: %q", a)
	}
}

func TestV1938FileKeys(t *testing.T) {
	for in, want := range map[string]string{
		"01_lab-sw01":   "lab-sw01",
		"2 lab-sw02":    "lab-sw02",
		"003-lab-sw03":  "lab-sw03",
		"12__lab_sw12":  "lab_sw12",
		"lab-sw01":      "",
		"192.0.2.1":     "", // an address, not a numbered file
		"10_20":         "", // no name left
		"1234_lab-sw01": "", // not a sequence number
		"01lab-sw01":    "", // no separator: the name may open with digits
		"01_":           "",
	} {
		got := strings.Join(fileKeys(in), "|")
		if got != want {
			t.Errorf("fileKeys(%q) = %q, want %q", in, got, want)
		}
	}
}

func v1938write(t *testing.T, dir string, files ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("lab-sw#show version\nok\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func v1938devs(r Run) (names []string, keys map[string]string) {
	keys = map[string]string{}
	for _, d := range r.Devices {
		names = append(names, d.Name)
		if len(d.Keys) > 0 {
			keys[d.Name] = strings.Join(d.Keys, "|")
		}
	}
	sort.Strings(names)
	return names, keys
}

func TestV1938ExternalFolderNamesAndKeys(t *testing.T) {
	root := t.TempDir()
	v1938write(t, filepath.Join(root, "maint_a"),
		"lab-sw-01_20261101_0900.log", "lab-sw-02_20261101_0900.log", "lab-sw-03_20261101_0901.log", "lab-sw-04_20261101_0901.log")
	v1938write(t, filepath.Join(root, "maint_b"),
		"01_lab-sw01.log", "02_lab-sw03.log", "03_lab-sw04.log")
	runs, err := List(root, logstore.StageTokens{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Run{}
	for _, r := range runs {
		by[r.Name] = r
	}
	names, keys := v1938devs(by["maint_a"])
	if got := strings.Join(names, ","); got != "lab-sw-01,lab-sw-02,lab-sw-03,lab-sw-04" {
		t.Errorf("maint_a names = %s", got)
	}
	if len(keys) != 0 || by["maint_a"].DeviceCount != 4 {
		t.Errorf("maint_a keys = %v, count = %d", keys, by["maint_a"].DeviceCount)
	}
	names, keys = v1938devs(by["maint_b"])
	if got := strings.Join(names, ","); got != "01_lab-sw01,02_lab-sw03,03_lab-sw04" {
		t.Errorf("maint_b names = %s", got)
	}
	if keys["01_lab-sw01"] != "lab-sw01" || keys["02_lab-sw03"] != "lab-sw03" || keys["03_lab-sw04"] != "lab-sw04" {
		t.Errorf("maint_b keys = %v", keys)
	}
}

// A number before a date that sets no two devices of its folder apart: the
// name without it goes along (v1.9.37 read it as a part of the date, and
// the files of two days paired).
func TestV1938NumberBeforeDateAsKey(t *testing.T) {
	root := t.TempDir()
	v1938write(t, filepath.Join(root, "day_a"), "sw01_01_0900.log", "sw02_01_0900.log", "sw03_20261101.log")
	v1938write(t, filepath.Join(root, "day_b"), "sw01_02_1100.log", "sw02_02_1100.log")
	// Devices that differ by the number: no such reading, they are not one.
	v1938write(t, filepath.Join(root, "numbered"), "lab-sw-01_1101.log", "lab-sw-02_1101.log")
	// The name without the number is another device of the folder.
	v1938write(t, filepath.Join(root, "both"), "sw01_20261101.log", "sw01_02_20261101.log")
	runs, err := List(root, logstore.StageTokens{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Run{}
	for _, r := range runs {
		by[r.Name] = r
	}
	names, keys := v1938devs(by["day_a"])
	if got := strings.Join(names, ","); got != "sw01_01,sw02_01,sw03" || keys["sw01_01"] != "sw01" || keys["sw02_01"] != "sw02" || len(keys) != 2 {
		t.Errorf("day_a names = %s, keys = %v", got, keys)
	}
	names, keys = v1938devs(by["day_b"])
	if got := strings.Join(names, ","); got != "sw01_02,sw02_02" || keys["sw01_02"] != "sw01" || keys["sw02_02"] != "sw02" {
		t.Errorf("day_b names = %s, keys = %v", got, keys)
	}
	names, keys = v1938devs(by["numbered"])
	if got := strings.Join(names, ","); got != "lab-sw-01,lab-sw-02" || len(keys) != 0 {
		t.Errorf("numbered names = %s, keys = %v", got, keys)
	}
	names, keys = v1938devs(by["both"])
	if got := strings.Join(names, ","); got != "sw01,sw01_02" || len(keys) != 0 {
		t.Errorf("both names = %s, keys = %v", got, keys)
	}
}

// A run folder of this app's whose summary is gone.
func TestV1938RunFolderWithoutSummary(t *testing.T) {
	root := t.TempDir()
	// The default file names: one name each, several logs of one device
	// stay that device.
	v1938write(t, filepath.Join(root, "20261103_090000_lab_before"),
		"lab-sw01_20261103_090001.log", "lab-sw02_20261103_090001.log", "lab-sw02_20261103_090501.log",
		// A second log of the same second (logstore: _2 after the time).
		"lab-sw03_HQ_before_20261103_090001.txt", "lab-sw03_HQ_before_20261103_090001_2.txt")
	// Names with "_" in them: the guess makes one device of all.
	v1938write(t, filepath.Join(root, "20261104_090000_lab_before"),
		"lab_sw01_20261104_090001.log", "lab_sw02_20261104_090001.log", "lab_sw03_20261104_090002.log")
	// One device with "_" in its name alone in the folder: the guess stays,
	// what the file reads as goes along.
	v1938write(t, filepath.Join(root, "20261104_110000_lab_after"),
		"lab_sw02_20261104_110001.log")
	runs, err := List(root, logstore.StageTokens{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Run{}
	for _, r := range runs {
		by[r.Name] = r
	}
	names, keys := v1938devs(by["20261103_090000_lab_before"])
	if got := strings.Join(names, ","); got != "lab-sw01,lab-sw02,lab-sw02,lab-sw03,lab-sw03" || len(keys) != 1 || keys["lab-sw03"] != "lab-sw03_HQ" {
		t.Errorf("default names = %s, keys = %v", got, keys)
	}
	if n := by["20261103_090000_lab_before"].DeviceCount; n != 3 {
		t.Errorf("default names: %d devices, want 3", n)
	}
	for in, want := range map[string]string{
		"lab-sw03_20261103_090001_2.txt": "lab-sw03",
		"lab-sw03_20261103_0900_12.txt":  "lab-sw03",
		"lab_sw_2.txt":                   "lab_sw_2", // a device's number, not a second log's
		"lab_sw_2_20261103.txt":          "lab_sw_2",
		"2.txt":                          "2",
	} {
		if got := runFileKey(in, logstore.StageTokens{}); got != want {
			t.Errorf("runFileKey(%q) = %q, want %q", in, got, want)
		}
	}
	names, _ = v1938devs(by["20261104_090000_lab_before"])
	if got := strings.Join(names, ","); got != "lab_sw01,lab_sw02,lab_sw03" {
		t.Errorf("names with _ = %s", got)
	}
	if n := by["20261104_090000_lab_before"].DeviceCount; n != 3 {
		t.Errorf("names with _: %d devices, want 3", n)
	}
	names, keys = v1938devs(by["20261104_110000_lab_after"])
	if got := strings.Join(names, ","); got != "lab" || keys["lab"] != "lab_sw02" {
		t.Errorf("one device with _ = %s, keys = %v", got, keys)
	}
}
