package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiffBasic(t *testing.T) {
	a := "hostname r1\ninterface Gi0/1\n ip address 10.0.0.1 255.255.255.0\n!\n"
	b := "hostname r1\ninterface Gi0/1\n ip address 10.0.0.2 255.255.255.0\n description uplink\n!\n"
	r := Diff(a, b, false)
	if r.Removed != 1 || r.Added != 2 || r.Same != 3 {
		t.Fatalf("counts: -%d +%d =%d, lines=%+v", r.Removed, r.Added, r.Same, r.Lines)
	}
	// Edit script must reproduce both sides.
	var ga, gb []string
	for _, l := range r.Lines {
		if l.Kind != "+" {
			ga = append(ga, l.A)
		}
		if l.Kind != "-" {
			gb = append(gb, l.B)
		}
	}
	if len(ga) != 4 || len(gb) != 5 || ga[2] != " ip address 10.0.0.1 255.255.255.0" || gb[3] != " description uplink" {
		t.Fatalf("reconstruction wrong: %v / %v", ga, gb)
	}
}

func TestDiffEmptyAndIdentical(t *testing.T) {
	if r := Diff("", "", true); len(r.Lines) != 0 {
		t.Fatalf("empty: %+v", r)
	}
	r := Diff("a\nb\n", "a\nb\n", true)
	if r.Added+r.Removed != 0 || r.Same != 2 {
		t.Fatalf("identical: %+v", r)
	}
}

func TestDiffIgnoresBlankAndPromptLines(t *testing.T) {
	a := "R1#show run\nhostname r1\nR1#\nR1#\n\nIX-A(config)# \n"
	b := "R1#show run\nhostname r1\nR1#\n"
	if r := Diff(a, b, true); r.Added+r.Removed != 0 {
		t.Fatalf("prompt-only lines must not count: %+v", r.Lines)
	}
	if r := Diff(a, b, false); r.Added+r.Removed == 0 {
		t.Fatalf("raw diff should differ")
	}
	// Line numbers still point at the original lines.
	r := Diff("x\n\nhostname r1\n", "x\nhostname r2\n", true)
	if r.Removed != 1 || r.Added != 1 || r.Lines[1].LineA != 3 || r.Lines[2].LineB != 2 {
		t.Fatalf("line numbers after dropping blanks: %+v", r.Lines)
	}
}

func TestDiffIgnoresNoise(t *testing.T) {
	a := "r1#show clock\n*10:15:02.123 JST Thu Oct 9 2026\nr1 uptime is 3 days, 2 hours\n  5 minute input rate 1000 bits/sec\nhostname r1\n"
	b := "r1#show clock\n*11:40:55.456 JST Thu Oct 9 2026\nr1 uptime is 3 days, 4 hours\n  5 minute input rate 2000 bits/sec\nhostname r1\n"
	if r := Diff(a, b, true); r.Added+r.Removed != 0 {
		t.Fatalf("noise not ignored: %+v", r.Lines)
	}
	if r := Diff(a, b, false); r.Added+r.Removed == 0 {
		t.Fatalf("raw diff should differ")
	}
}

func TestDiffIgnoresRouteAges(t *testing.T) {
	a := "S*   0.0.0.0/0 [1/1] via 192.0.2.1, GigaEthernet1.0, 37d20h54m31s\nC    192.0.2.0/24 [0/0] is directly connected, GigaEthernet1.0, 37d20h54m31s\n"
	b := "S*   0.0.0.0/0 [1/1] via 192.0.2.1, GigaEthernet1.0, 37d20h55m26s\nC    192.0.2.0/24 [0/0] is directly connected, GigaEthernet1.0, 2w3d\n"
	if r := Diff(a, b, true); r.Added+r.Removed != 0 {
		t.Fatalf("route ages not ignored: %+v", r.Lines)
	}
	// A real change in the same line still shows.
	c := "S*   0.0.0.0/0 [1/1] via 192.0.2.254, GigaEthernet1.0, 1d2h3m4s\n"
	if r := Diff(a, c, true); r.Added+r.Removed == 0 {
		t.Fatalf("changed next hop must be a difference")
	}
}

func TestListReadsSummaryAndLegacy(t *testing.T) {
	root := t.TempDir()
	// A run with a summary.
	d1 := filepath.Join(root, "log_20261009_100000_before")
	os.MkdirAll(d1, 0o755)
	os.WriteFile(filepath.Join(d1, "r1_Config_20261009_100000_before.txt"), []byte("x"), 0o644)
	if err := Write(d1, Summary{StartedAt: "2026-10-09T10:00:00+09:00", Group: "A社", Stage: "before",
		Devices: []DeviceSummary{
			{Name: "r1", Host: "10.0.0.1", Success: true, LogFile: "r1_Config_20261009_100000_before.txt"},
			{Name: "r2", Host: "10.0.0.2", Error: "timeout"},
			{Name: "r3", Canceled: true, Error: "中止しました（未実行）"},
		}}); err != nil {
		t.Fatal(err)
	}
	// A legacy run (files only).
	d2 := filepath.Join(root, "log_20261001_090000")
	os.MkdirAll(d2, 0o755)
	os.WriteFile(filepath.Join(d2, "sw1_Config_20261001_090000.txt"), []byte("y"), 0o644)
	// Not a run folder.
	os.WriteFile(filepath.Join(root, "r9_interactive_20261001_090000.txt"), []byte("z"), 0o644)

	runs, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Name != "log_20261009_100000_before" {
		t.Fatalf("runs: %+v", runs)
	}
	r := runs[0]
	if !r.HasSummary || r.Group != "A社" || r.Stage != "before" || r.OK != 1 || r.Failed != 1 || r.Canceled != 1 {
		t.Fatalf("summary run: %+v", r)
	}
	if r.Devices[0].LogPath == "" || r.Devices[1].LogPath != "" {
		t.Fatalf("log paths: %+v", r.Devices)
	}
	l := runs[1]
	if l.HasSummary || l.StartedAt == "" || len(l.Devices) != 1 || l.Devices[0].Name != "sw1" || l.Devices[0].Status != "unknown" {
		t.Fatalf("legacy run: %+v", l)
	}
	if _, err := List(filepath.Join(root, "missing")); err != nil {
		t.Fatalf("missing root should be empty, got %v", err)
	}
}
