package history

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/logstore"
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

// The clock pattern must not reach into colon-separated MAC and IPv6
// addresses: a changed MAC entry or IPv6 next hop is a real difference.
func TestDiffClockNoiseLeavesMACAndIPv6Alone(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"  10   0000.5e00.5301    DYNAMIC     Gi0/1\n", "  10   0000.5e00.5302    DYNAMIC     Gi0/1\n"},
		{"ether 00:11:22:33:44:55\n", "ether 00:11:22:33:44:66\n"},
		{"ether 00:11:22:33:44:55\n", "ether 00:11:22:33:45:55\n"},
		{"S ::/0 via 2001:db8::12:34:56, Gi0/1\n", "S ::/0 via 2001:db8::12:34:57, Gi0/1\n"},
		{"S ::/0 via 2001:db8:0:12:34:56:0:1, Gi0/1\n", "S ::/0 via 2001:db8:0:12:34:57:0:1, Gi0/1\n"},
	} {
		if r := Diff(tc.a, tc.b, true); r.Added+r.Removed == 0 {
			t.Errorf("hidden as noise: %q vs %q", tc.a, tc.b)
		}
	}
	// Clocks in their usual places are still noise.
	for _, tc := range []struct{ a, b string }{
		{"*10:15:02.123 JST Thu Oct 9 2026\n", "*11:40:55.456 JST Thu Oct 9 2026\n"},
		{"Oct  9 10:15:02: %SYS-5-CONFIG_I: Configured from console\n", "Oct  9 11:40:55: %SYS-5-CONFIG_I: Configured from console\n"},
		{"2026-10-09T10:15:02 lab\n", "2026-10-09T11:40:55 lab\n"},
		{"time 1:02:03, 4:05:06\n", "time 7:08:09, 10:11:12\n"},
		{"last at 10:15:02:\n", "last at 11:40:55:\n"},
	} {
		if r := Diff(tc.a, tc.b, true); r.Added+r.Removed != 0 {
			t.Errorf("clock not ignored: %q vs %q: %+v", tc.a, tc.b, r.Lines)
		}
	}
}

// lcsLen is the textbook O(n·m) longest common subsequence, the reference
// for the edit distance the shortest script must reach.
func lcsLen(a, b []string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else {
				cur[j] = max(prev[j], cur[j-1])
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// checkScript verifies that ops rebuild a and b in order and counts them.
func checkScript(t *testing.T, a, b []string, ops []editOp) (same, removed, added int) {
	t.Helper()
	var ra, rb []string
	for _, op := range ops {
		switch op.kind {
		case '=':
			if a[op.i] != b[op.j] {
				t.Fatalf("'=' pairs %q with %q", a[op.i], b[op.j])
			}
			ra = append(ra, a[op.i])
			rb = append(rb, b[op.j])
			same++
		case '-':
			ra = append(ra, a[op.i])
			removed++
		case '+':
			rb = append(rb, b[op.j])
			added++
		}
	}
	if strings.Join(ra, "\n") != strings.Join(a, "\n") || strings.Join(rb, "\n") != strings.Join(b, "\n") {
		t.Fatalf("script does not rebuild its inputs:\n%q\n%q\nops %v", a, b, ops)
	}
	return
}

// The linear-space script is a shortest one: as many edits as the LCS
// allows, on random inputs of every small shape (and the reference walk
// with no budget at all).
func TestMyersIsShortestScript(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 3000; iter++ {
		n, m := rng.Intn(13), rng.Intn(13)
		a := make([]string, n)
		b := make([]string, m)
		for i := range a {
			a[i] = string(rune('a' + rng.Intn(3)))
		}
		for j := range b {
			b[j] = string(rune('a' + rng.Intn(3)))
		}
		ops, approx := myers(a, b)
		if approx {
			t.Fatalf("approx on a tiny input %q %q", a, b)
		}
		_, removed, added := checkScript(t, a, b, ops)
		if want := n + m - 2*lcsLen(a, b); removed+added != want {
			t.Fatalf("%q vs %q: %d edits, shortest is %d: %v", a, b, removed+added, want, ops)
		}
		// Within a change, removed lines come before added ones (the UI
		// folds that order into one changed block).
		for k := 1; k < len(ops); k++ {
			if ops[k-1].kind == '+' && ops[k].kind == '-' {
				t.Fatalf("%q vs %q: an addition before a removal: %v", a, b, ops)
			}
		}
	}
}

func numberedLines(prefix string, n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "%s line %d\n", prefix, i)
	}
	return sb.String()
}

// Two unrelated 6,000-line captures took ≈2.4 GB with the frontier-copying
// walk (10,000 lines ran out of memory); the result must now come in
// linear memory and within a few seconds, and say it is approximate.
func TestDiffLargeUnrelatedInputsStayBounded(t *testing.T) {
	a, b := numberedLines("alpha", 6000), numberedLines("beta", 6000)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	r := Diff(a, b, false)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	alloc := after.TotalAlloc - before.TotalAlloc
	t.Logf("6000x6000 unrelated: %v, %d MB allocated", elapsed, alloc>>20)
	if alloc > 64<<20 {
		t.Fatalf("allocated %d MB", alloc>>20)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("took %v", elapsed)
	}
	// 12,000 edits are within the budget for 12,000 lines: the exact script.
	if r.Approx || r.Added != 6000 || r.Removed != 6000 || r.Same != 0 {
		t.Fatalf("counts: approx=%v +%d -%d =%d", r.Approx, r.Added, r.Removed, r.Same)
	}

	// Past the edit budget (60,000 edits, budget 10,000) the stretch is
	// compared line for line, still in bounds, and the result says so.
	a, b = numberedLines("alpha", 30000), numberedLines("beta", 30000)
	runtime.ReadMemStats(&before)
	start = time.Now()
	r = Diff(a, b, false)
	elapsed = time.Since(start)
	runtime.ReadMemStats(&after)
	alloc = after.TotalAlloc - before.TotalAlloc
	t.Logf("30000x30000 unrelated: %v, %d MB allocated", elapsed, alloc>>20)
	if alloc > 128<<20 || elapsed > 10*time.Second {
		t.Fatalf("%v, %d MB", elapsed, alloc>>20)
	}
	if !r.Approx || r.Added != 30000 || r.Removed != 30000 || r.Same != 0 {
		t.Fatalf("over the budget: approx=%v +%d -%d =%d", r.Approx, r.Added, r.Removed, r.Same)
	}

	// Two large captures of one device with a handful of scattered changes
	// get the exact script, in the same bounds.
	la := strings.Split(strings.TrimSuffix(numberedLines("cfg", 20000), "\n"), "\n")
	lb := append([]string{}, la...)
	lb[100], lb[9000], lb[19990] = "cfg changed 100", "cfg changed 9000", "cfg changed 19990"
	lb = append(lb[:5000], append([]string{"cfg inserted a", "cfg inserted b"}, lb[5000:]...)...)
	runtime.ReadMemStats(&before)
	r = Diff(strings.Join(la, "\n"), strings.Join(lb, "\n"), false)
	runtime.ReadMemStats(&after)
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 64<<20 {
		t.Fatalf("scattered changes allocated %d MB", alloc>>20)
	}
	if r.Approx || r.Added != 5 || r.Removed != 3 || r.Same != 19997 {
		t.Fatalf("scattered changes: approx=%v +%d -%d =%d", r.Approx, r.Added, r.Removed, r.Same)
	}

	// Beyond the line cap the middle is compared line for line, and the
	// result says so.
	big := strings.Split(strings.TrimSuffix(numberedLines("big", 60000), "\n"), "\n")
	bigB := append([]string{}, big...)
	for i := 1; i < len(bigB); i += 2 {
		bigB[i] = "big other " + bigB[i]
	}
	r = Diff(strings.Join(big, "\n"), strings.Join(bigB, "\n"), false)
	if !r.Approx || r.Added != 30000 || r.Removed != 30000 || r.Same != 30000 {
		t.Fatalf("over the cap: approx=%v +%d -%d =%d", r.Approx, r.Added, r.Removed, r.Same)
	}
}

// The summary is replaced whole: a crash in the middle of writing a 予約's
// round must not leave a truncated file (the next round then started the
// folder's history over). The temporary file is gone afterwards.
func TestWriteReplacesSummaryWhole(t *testing.T) {
	dir := t.TempDir()
	first := Summary{StartedAt: "2026-10-09T10:00:00+09:00", Devices: []DeviceSummary{{Name: "r1", Success: true}}}
	if err := Write(dir, first); err != nil {
		t.Fatal(err)
	}
	second := AddRound(first, Summary{StartedAt: "2026-10-09T10:05:00+09:00", Devices: []DeviceSummary{{Name: "r1", Success: false}, {Name: "r2", Success: true}}})
	if err := Write(dir, second); err != nil {
		t.Fatal(err)
	}
	got, ok := Read(dir)
	if !ok || got.Rounds != 2 || len(got.Devices) != 2 || got.StartedAt != first.StartedAt {
		t.Fatalf("read back: %+v %v", got, ok)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != SummaryName {
		t.Fatalf("folder holds %v, want only %s", entries, SummaryName)
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

	runs, err := List(root, logstore.StageTokens{})
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
	if _, err := List(filepath.Join(root, "missing"), logstore.StageTokens{}); err != nil {
		t.Fatalf("missing root should be empty, got %v", err)
	}
}

// The folder-name template is the user's to change, so the history must not
// depend on the "log_" prefix. A folder with a summary is a run whatever it
// is called; one without (a crashed interactive window) still counts when
// it opens with the date and time, as the current default does — group and
// stage after it, the stage read as the last field even if the group name
// has "_" in it. Folders that are neither are left alone.
func TestListFindsRunsWhateverTheFolderTemplate(t *testing.T) {
	root := t.TempDir()
	custom := filepath.Join(root, "A社_作業前取得")
	os.MkdirAll(custom, 0o755)
	if err := Write(custom, Summary{StartedAt: "2026-10-11T09:00:00+09:00", Group: "A社", Stage: "before",
		Devices: []DeviceSummary{{Name: "r1", Success: true}}}); err != nil {
		t.Fatal(err)
	}
	noSummary := filepath.Join(root, "20261011_100000_SHIFT_東京_work")
	os.MkdirAll(noSummary, 0o755)
	os.WriteFile(filepath.Join(noSummary, "sw1_work_20261011_100000.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(root, "backup-old"), 0o755) // not a run

	runs, err := List(root, logstore.StageTokens{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Run{}
	for _, r := range runs {
		got[r.Name] = r
	}
	if len(runs) != 2 {
		t.Fatalf("want 2 runs, got %d: %+v", len(runs), runs)
	}
	if r := got["A社_作業前取得"]; !r.HasSummary || r.Stage != "before" {
		t.Errorf("custom-named run with a summary: %+v", r)
	}
	if r := got["20261011_100000_SHIFT_東京_work"]; r.HasSummary || r.Stage != "work" || r.StartedAt == "" || len(r.Devices) != 1 {
		t.Errorf("date-first run without a summary: %+v", r)
	}
}

// Folders named with renamed stage words (and with the defaults from before
// the rename) sort into their stage.
func TestListCustomStageWords(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"log_20261009_100000_pre", "log_20261009_110000_post", "log_20261009_120000_before", "log_20261009_130000_mid", "log_20261009_140000_odd"} {
		os.MkdirAll(filepath.Join(root, n), 0o755)
	}
	// Japanese words round-trip through the folder name too.
	os.MkdirAll(filepath.Join(root, "log_20261009_150000_事前"), 0o755)
	jp, err := List(root, logstore.StageTokens{Before: "事前", Work: "作業", After: "事後"})
	if err != nil {
		t.Fatal(err)
	}
	if jp[0].Name != "log_20261009_150000_事前" || jp[0].Stage != "before" {
		t.Fatalf("japanese stage word: %+v", jp[0])
	}
	os.RemoveAll(filepath.Join(root, "log_20261009_150000_事前"))
	runs, err := List(root, logstore.StageTokens{Before: "pre", Work: "mid", After: "post"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range runs {
		got[r.Name] = r.Stage
	}
	exp := map[string]string{
		"log_20261009_100000_pre":    "before",
		"log_20261009_110000_post":   "after",
		"log_20261009_120000_before": "before",
		"log_20261009_130000_mid":    "work",
		"log_20261009_140000_odd":    "odd",
	}
	for n, st := range exp {
		if got[n] != st {
			t.Errorf("%s: stage %q, want %q", n, got[n], st)
		}
	}
}

// A 予約's rounds fold into one summary: first start kept, latest result
// per device, rounds and failed rounds counted.
func TestAddRound(t *testing.T) {
	r1 := Summary{StartedAt: "2026-10-14T10:00:00+09:00", Devices: []DeviceSummary{{Name: "a", Success: true}, {Name: "b", Success: false}}}
	r2 := Summary{StartedAt: "2026-10-14T10:00:10+09:00", Devices: []DeviceSummary{{Name: "b", Success: true}, {Name: "c", Success: true}}}
	s := AddRound(r1, r2)
	if s.StartedAt != r1.StartedAt || s.Rounds != 2 || s.FailedRounds != 1 {
		t.Fatalf("got start %s rounds %d failed %d", s.StartedAt, s.Rounds, s.FailedRounds)
	}
	if len(s.Devices) != 3 || !s.Devices[1].Success || s.Devices[2].Name != "c" {
		t.Fatalf("devices %+v", s.Devices)
	}
	s = AddRound(s, r1)
	if s.Rounds != 3 || s.FailedRounds != 2 {
		t.Fatalf("third round: rounds %d failed %d", s.Rounds, s.FailedRounds)
	}
}

// The cached row of a folder follows a summary rewritten in place and a
// folder that disappears.
func TestListCacheFollowsChanges(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "20261014_100000_G")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, Summary{StartedAt: "2026-10-14T10:00:00+09:00", Devices: []DeviceSummary{{Name: "a", Success: true}}}); err != nil {
		t.Fatal(err)
	}
	if runs, _ := List(root, logstore.StageTokens{}); len(runs) != 1 || runs[0].OK != 1 {
		t.Fatalf("first read %+v", runs)
	}
	prev, _ := Read(dir)
	if err := Write(dir, AddRound(prev, Summary{Devices: []DeviceSummary{{Name: "a", Success: false}}})); err != nil {
		t.Fatal(err)
	}
	runs, _ := List(root, logstore.StageTokens{})
	if len(runs) != 1 || runs[0].Failed != 1 || runs[0].Rounds != 2 {
		t.Fatalf("after a new round %+v", runs)
	}
	runs[0].Devices[0].Name = "changed by the caller"
	if again, _ := List(root, logstore.StageTokens{}); again[0].Devices[0].Name != "a" {
		t.Fatal("a caller's change reached the cache")
	}
	os.RemoveAll(dir)
	if runs, _ := List(root, logstore.StageTokens{}); len(runs) != 0 {
		t.Fatalf("deleted folder still listed: %+v", runs)
	}
}
