package main

// The fixes of the 2026-11-19 review of v1.10.0 – v1.10.4.

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kojio145/palaterm/internal/model"
)

func reviewFillInv(cmds ...string) *model.Inventory {
	cs := model.CommandSet{Name: "lab-set", Fill: &model.SetFill{File: "lab.csv", Items: []string{"NH", "CMD"}, Devices: []model.FillDevice{
		{Name: "lab-rt1", Rows: []map[string]string{{"NH": "192.0.2.1", "CMD": "reload"}}},
		{Name: "lab-rt2", Rows: []map[string]string{{"NH": "192.0.2.2", "CMD": "show clock"}, {"NH": "192.0.2.3", "CMD": "show clock"}}},
	}}}
	for _, c := range cmds {
		cs.Commands = append(cs.Commands, model.Command{Text: c})
	}
	return &model.Inventory{CommandSets: []model.CommandSet{cs}, Devices: []model.Device{
		{Name: "lab-rt1", CommandSet: "lab-set"}, {Name: "lab-rt2", CommandSet: "lab-set"}, {Name: "lab-rt9", CommandSet: "lab-set"},
	}}
}

// A line that is only a variable changes the device when its value does:
// the check reads what is sent, per device.
func TestDangerOfReadsFilledCommands(t *testing.T) {
	warns := dangerOf(reviewFillInv("show ip route {{NH}}", "{{CMD}}"))
	if len(warns) != 1 || warns[0].Device != "lab-rt1" || len(warns[0].Hits) != 1 || warns[0].Hits[0].Text != "reload" || warns[0].Hits[0].Index != 1 {
		t.Fatalf("only lab-rt1 is sent a reload: %+v", warns)
	}
	// Written out, the set warns for every device as it always did (the
	// one without a row too: it is read as written).
	warns = dangerOf(reviewFillInv("show ip route {{NH}}", "reload"))
	if len(warns) != 3 {
		t.Fatalf("a written reload warns for each device: %+v", warns)
	}
	// A set without variables is untouched.
	inv := reviewFillInv("reload")
	inv.CommandSets[0].Fill = nil
	if warns = dangerOf(inv); len(warns) != 3 {
		t.Fatalf("no variables: %+v", warns)
	}
}

func TestFillNameNotInList(t *testing.T) {
	inv := reviewFillInv("show ip route {{NHX}}")
	fd := fillDevice(setNamed(inv, "lab-set"), "lab-rt1")
	if !strings.Contains(fd.Error, "「NHX」という変数はありません") || !strings.Contains(fd.Error, "NH、CMD") {
		t.Fatalf("the name is said: %q", fd.Error)
	}
	// {{name?}} of a name the list lacks leaves its line out as before,
	// with a note.
	inv = reviewFillInv("show clock", "description {{descr?}}")
	fd = fillDevice(setNamed(inv, "lab-set"), "lab-rt1")
	if fd.Error != "" || len(fd.Commands) != 1 || len(fd.Notes) != 1 || !strings.Contains(fd.Notes[0], "「descr」") {
		t.Fatalf("optional unknown: %+v", fd)
	}
	// The device the list has no row for is still said first.
	if fd = fillDevice(setNamed(reviewFillInv("{{NHX}}"), "lab-set"), "lab-rt9"); !strings.Contains(fd.Error, "この機器の行がありません") {
		t.Fatalf("no row: %q", fd.Error)
	}
}

func TestFillNameCaseDoesNotCount(t *testing.T) {
	inv := reviewFillInv("show ip route {{nh}}", "ping {{Nh?}}")
	fd := fillDevice(setNamed(inv, "lab-set"), "lab-rt1")
	if fd.Error != "" || strings.Join(fd.Commands, "|") != "show ip route 192.0.2.1|ping 192.0.2.1" {
		t.Fatalf("{{nh}} takes NH: %+v", fd)
	}
	// The rule's 「空ならその行を送らない」 too.
	cs := setNamed(inv, "lab-set")
	cs.Fill.Skip = []string{"NH"}
	cs.Fill.Devices[0].Rows[0]["NH"] = ""
	cs.Commands = []model.Command{{Text: "show clock"}, {Text: "show ip route {{nh}}"}}
	if fd = fillDevice(cs, "lab-rt1"); fd.Error != "" || len(fd.Commands) != 1 {
		t.Fatalf("skip by another case: %+v", fd)
	}
}

func TestFillRowsNotUsedNote(t *testing.T) {
	inv := reviewFillInv("show ip route {{NH}}")
	fd := fillDevice(setNamed(inv, "lab-set"), "lab-rt2")
	if len(fd.Notes) != 1 || !strings.Contains(fd.Notes[0], "2 行") || len(fd.Commands) != 1 {
		t.Fatalf("two rows, no block: %+v", fd)
	}
	if fd = fillDevice(setNamed(inv, "lab-set"), "lab-rt1"); len(fd.Notes) != 0 {
		t.Fatalf("one row: %+v", fd.Notes)
	}
	inv = reviewFillInv("{{#each}}", "show ip route {{NH}}", "{{/each}}")
	if fd = fillDevice(setNamed(inv, "lab-set"), "lab-rt2"); len(fd.Notes) != 0 || len(fd.Commands) != 2 {
		t.Fatalf("a block uses every row: %+v", fd)
	}
}

func TestWinProxyFor(t *testing.T) {
	u := func(s string) *url.URL { x, _ := url.Parse(s); return x }
	api := u("https://api.github.com/repos/x")
	for _, c := range []struct{ server, bypass, want string }{
		{"", "", ""},
		{"proxy.lab.example:8080", "", "http://proxy.lab.example:8080"},
		{"http://proxy.lab.example:8080", "", "http://proxy.lab.example:8080"},
		{"http=192.0.2.10:3128;https=192.0.2.11:3129", "", "http://192.0.2.11:3129"},
		{"http=192.0.2.10:3128", "", ""},
		{"socks=192.0.2.10:1080", "", ""},
		{"proxy.lab.example:8080", "*.github.com;<local>", ""},
		{"proxy.lab.example:8080", "*.lab.example;<local>", "http://proxy.lab.example:8080"},
		{"proxy.lab.example:8080", "API.GITHUB.COM", ""},
	} {
		got := ""
		if p := winProxyFor(c.server, c.bypass, api); p != nil {
			got = p.String()
		}
		if got != c.want {
			t.Errorf("winProxyFor(%q, %q) = %q, want %q", c.server, c.bypass, got, c.want)
		}
	}
	if p := winProxyFor("proxy.lab.example:8080", "<local>", u("https://labhost/x")); p != nil {
		t.Errorf("<local> is a host without a dot: %v", p)
	}
}

// The download is bounded by its context, not by a Timeout that would cut
// a slow one at a minute; both clients go through the PC's proxy.
func TestUpdateClients(t *testing.T) {
	if downloadClient.Timeout != 0 {
		t.Fatalf("downloadClient.Timeout = %v", downloadClient.Timeout)
	}
	if updateClient.Timeout == 0 {
		t.Fatal("the check keeps its timeout")
	}
	for _, c := range []*http.Client{updateClient, downloadClient} {
		tr, ok := c.Transport.(*http.Transport)
		if !ok || tr.Proxy == nil {
			t.Fatalf("transport %T", c.Transport)
		}
	}
}

func TestStatusErr(t *testing.T) {
	resp := func(code int, h map[string]string) *http.Response {
		r := &http.Response{StatusCode: code, Header: http.Header{}}
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	reset := time.Now().Add(20 * time.Minute)
	err := statusErr(resp(403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(reset.Unix(), 10)}))
	if err == nil || !strings.Contains(err.Error(), "回数制限") || !strings.Contains(err.Error(), reset.Local().Format("15:04")) {
		t.Fatalf("rate limit: %v", err)
	}
	if err = statusErr(resp(429, map[string]string{"X-RateLimit-Remaining": "0"})); err == nil || !strings.Contains(err.Error(), "しばらくしてから") {
		t.Fatalf("rate limit without a time: %v", err)
	}
	if err = statusErr(resp(403, nil)); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("403: %v", err)
	}
	if err = statusErr(resp(407, nil)); err == nil || !strings.Contains(err.Error(), "407") {
		t.Fatalf("407: %v", err)
	}
	if err = statusErr(resp(500, nil)); err != nil {
		t.Fatalf("500 keeps the caller's words: %v", err)
	}
}

func TestNotesOfLeavesOutWhatIsNotReleased(t *testing.T) {
	rels := []ghRelease{
		{TagName: "v1.10.7", Body: "draft", Draft: true},
		{TagName: "v1.10.6", Body: "pre", Prerelease: true},
		{TagName: "v1.11.0", Body: "past the latest"},
		{TagName: "v1.10.4", Body: "four"},
		{TagName: "v1.10.5", Body: "five"},
		{TagName: "v1.10.3", Body: "current"},
	}
	notes := notesOf(rels, "v1.10.5", "1.10.3")
	if len(notes) != 2 || notes[0].Version != "1.10.5" || notes[1].Version != "1.10.4" {
		t.Fatalf("notes %+v", notes)
	}
}

// A 複製 keeps the source's list, taken from the vault: what the page
// sends as a Fill is not used (読み直す would read a path no dialog picked).
func TestCreateCommandSetFromTakesTheVaultsFill(t *testing.T) {
	a := newTestApp(t)
	if err := a.CreateVault("master"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.inv.CommandSets = append(a.inv.CommandSets, reviewFillInv("show ip route {{NH}}").CommandSets[0])
	a.mu.Unlock()
	cmds := []model.Command{{Text: "show ip route {{NH}}"}}
	forged := &model.SetFill{Path: "lab/not-picked.csv", Items: []string{"NH"}}
	if err := a.CreateCommandSet(model.CommandSet{Name: "lab-new", Commands: cmds, Fill: forged}); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateCommandSetFrom(model.CommandSet{Name: "lab-copy", Commands: cmds, Fill: forged}, "lab-set"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	src, nw, cp := setNamed(a.inv, "lab-set"), setNamed(a.inv, "lab-new"), setNamed(a.inv, "lab-copy")
	a.mu.Unlock()
	if nw == nil || nw.Fill != nil {
		t.Fatalf("a new set takes no Fill from the page: %+v", nw)
	}
	if cp == nil || cp.Fill == nil || cp.Fill == src.Fill || cp.Fill.File != "lab.csv" || cp.Fill.Path != "" || len(cp.Fill.Devices) != 2 {
		t.Fatalf("the copy has the source's list: %+v", cp)
	}
	if err := a.CreateCommandSetFrom(model.CommandSet{Name: "lab-copy2", Commands: cmds}, "lab-new"); err == nil || !strings.Contains(err.Error(), "複製元「lab-new」") {
		t.Fatalf("a source without a list is said: %v", err)
	}
	if err := a.CreateCommandSetFrom(model.CommandSet{Name: "lab-copy2", Commands: cmds}, "lab-gone"); err == nil {
		t.Fatal("a source that is gone is refused")
	}
}
