package main

// 最新版の確認: the 管理 tab's page asks GitHub for the newest public release
// of PalaTerm, and on request downloads its PalaTerm.exe into the user's
// Downloads folder. Nothing is sent or fetched until a button is pressed;
// the running exe is never replaced (the user swaps it after closing).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	releaseAPI  = "https://api.github.com/repos/kojio145/palaterm/releases/latest"
	releasesAPI = "https://api.github.com/repos/kojio145/palaterm/releases?per_page=50"
	releasePage = "https://github.com/kojio145/palaterm/releases/latest"
	// manualChanges is the Japanese manual's section of one version's
	// changes (#changes-1941 for v1.9.41); manualChangesTop where the
	// sections start (the newest first), for when no version is known.
	manualChanges    = "https://kojio145.github.io/palaterm/manual.html#changes-%s"
	manualChangesTop = "https://kojio145.github.io/palaterm/manual.html#changes"
	// usageRaw is the user guide of a tag: its 「vX での変更」 sections are
	// the release notes in Japanese.
	usageRaw = "https://raw.githubusercontent.com/kojio145/palaterm/%s/docs/USAGE.md"
	// Only an asset of this repository's releases is downloaded.
	releaseAssetPrefix = "https://github.com/kojio145/palaterm/releases/download/"
	updateExeName      = "PalaTerm.exe"
	maxUpdateExe       = 64 << 20
)

// UpdateInfo is the newest public release compared with this build.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Newer     bool   `json:"newer"`
	Published string `json:"published"`
	URL       string `json:"url"`
	HasExe    bool   `json:"hasExe"`
	ExeSize   int64  `json:"exeSize"`
	// HasDigest: the release gives the exe's SHA-256 (the download is
	// checked against it; without one, against the size only).
	HasDigest bool `json:"hasDigest"`
	// SaveDir is the folder a download goes to.
	SaveDir string `json:"saveDir"`
	// Notes are the changes of every release newer than this build, the
	// newest first: in Japanese from the user guide when the page is in
	// Japanese and the guide could be read (Lang "ja"), else the English
	// release notes.
	Notes []ReleaseNote `json:"notes"`
	Lang  string        `json:"lang"`
}

// ReleaseNote is one release's changes.
type ReleaseNote struct {
	Version   string `json:"version"`
	Published string `json:"published"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

// proxyRefused is a proxy's answer to the CONNECT of an https request when
// it is a refusal PalaTerm has words for (407, 403). Go hands such an
// answer back as an error carrying the status text only, never as a
// response, so statusErr does not see it: updateTransport keeps the code.
type proxyRefused struct{ code int }

func (e proxyRefused) Error() string {
	if e.code == http.StatusProxyAuthRequired {
		return proxyAuthMsg
	}
	return fmt.Sprintf("プロキシに断られました（HTTP %d）。プロキシやセキュリティ製品が GitHub への通信を止めている可能性があります", e.code)
}

const proxyAuthMsg = "プロキシが認証を求めました（HTTP 407）。PalaTerm はプロキシの認証に対応していません。ブラウザーでリリースのページから入手してください"

// updateTransport goes through the PC's proxy (updateProxy).
var updateTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = updateProxy
	t.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, res *http.Response) error {
		if res.StatusCode == http.StatusProxyAuthRequired || res.StatusCode == http.StatusForbidden {
			return proxyRefused{res.StatusCode}
		}
		return nil
	}
	return t
}()

var updateClient = &http.Client{Timeout: 60 * time.Second, Transport: updateTransport}

// downloadClient has no Timeout of its own: a Client's Timeout also covers
// reading the body, and the exe does not come in a minute on a slow line or
// through a proxy that inspects it. The request's context bounds it.
var downloadClient = &http.Client{Transport: updateTransport}

// connectErr is a request that did not reach GitHub, with what to try.
func connectErr(what string, err error) error {
	var pr proxyRefused
	if errors.As(err, &pr) {
		return pr
	}
	if proxyScriptOnly() {
		return fmt.Errorf("%s（この PC のプロキシは自動設定のスクリプトです。PalaTerm はスクリプトを読めないので、Windows の設定でプロキシを手動で入れるか、ブラウザーでリリースのページから入手してください）: %v", what, err)
	}
	return fmt.Errorf("%s（インターネットに出られない PC では使えません。プロキシは Windows の手動設定か、環境変数 HTTPS_PROXY のものを使います）: %v", what, err)
}

// statusErr is an answer that is not 200, said so the user knows what to
// do when it is one of the usual ones; nil for any other.
func statusErr(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			when := "しばらくしてから"
			if sec, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && sec > 0 {
				when = time.Unix(sec, 0).Local().Format("15:04") + " 以降に"
			}
			return fmt.Errorf("GitHub の回数制限にかかりました（同じ回線からの問い合わせは 1 時間に 60 回まで）。%sもう一度お試しください", when)
		}
		return fmt.Errorf("GitHub に断られました（HTTP %d）。プロキシやセキュリティ製品が GitHub への通信を止めている可能性があります", resp.StatusCode)
	case http.StatusProxyAuthRequired:
		return errors.New(proxyAuthMsg)
	}
	return nil
}

// githubGet fetches url (at most limit bytes).
func githubGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "PalaTerm/"+appVersion)
	resp, err := updateClient.Do(req)
	if err != nil {
		return nil, connectErr("GitHub に接続できませんでした", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if err := statusErr(resp); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("GitHub から最新版を取得できませんでした（HTTP %d）", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func latestRelease(ctx context.Context) (*ghRelease, error) {
	data, err := githubGet(ctx, releaseAPI, 4<<20)
	if err != nil {
		return nil, err
	}
	var rel ghRelease
	if err := json.Unmarshal(data, &rel); err != nil {
		return nil, fmt.Errorf("GitHub の応答を読めませんでした: %v", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("GitHub の応答に版数がありません")
	}
	return &rel, nil
}

// noteBody is a release body without its download section (the zip, the
// SHA-256): what changed, not how to get it.
func noteBody(b string) string {
	if i := strings.Index(b, "\n## Download"); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(strings.ReplaceAll(b, "\r\n", "\n"))
}

// usageNotes reads the 「## vX での変更（題）」 sections of a user guide:
// version → title and body.
func usageNotes(guide string) map[string]ReleaseNote {
	out := map[string]ReleaseNote{}
	var cur *ReleaseNote
	var body []string
	flush := func() {
		if cur != nil {
			cur.Body = strings.TrimSpace(strings.Join(body, "\n"))
			out[cur.Version] = *cur
		}
		cur, body = nil, nil
	}
	for _, l := range strings.Split(strings.ReplaceAll(guide, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, "## ") {
			flush()
			h := strings.TrimSpace(l[3:])
			if v, rest, ok := strings.Cut(h, " での変更"); ok && strings.HasPrefix(v, "v") {
				title := strings.TrimSpace(rest)
				title = strings.TrimSuffix(strings.TrimPrefix(title, "（"), "）")
				cur = &ReleaseNote{Version: strings.TrimPrefix(v, "v"), Title: title}
			}
			continue
		}
		if cur != nil {
			body = append(body, l)
		}
	}
	flush()
	return out
}

// releaseNotes are the notes of the releases newer than this build up to
// the latest (a pre-release or a draft is not one), the newest first, and
// their language.
func (a *App) releaseNotes(latestTag, current string) ([]ReleaseNote, string) {
	var notes []ReleaseNote
	if data, err := githubGet(a.ctx, releasesAPI, 8<<20); err == nil {
		var rels []ghRelease
		if json.Unmarshal(data, &rels) == nil {
			notes = notesOf(rels, latestTag, current)
		}
	}
	if a.uiLang() == "en" || len(notes) == 0 {
		return notes, "en"
	}
	data, err := githubGet(a.ctx, fmt.Sprintf(usageRaw, latestTag), 8<<20)
	if err != nil {
		return notes, "en"
	}
	ja := usageNotes(string(data))
	out := make([]ReleaseNote, 0, len(notes))
	for _, n := range notes {
		j, ok := ja[n.Version]
		if !ok {
			return notes, "en" // one without its Japanese: all in English
		}
		j.Published = n.Published
		out = append(out, j)
	}
	return out, "ja"
}

// notesOf are the notes of the releases newer than current up to latestTag,
// the newest first. A draft, a pre-release and a tag past the latest (one
// GitHub does not call the latest) are not what a download gives.
func notesOf(rels []ghRelease, latestTag, current string) []ReleaseNote {
	var notes []ReleaseNote
	for _, r := range rels {
		if r.Draft || r.Prerelease || !versionNewer(r.TagName, current) || versionNewer(r.TagName, latestTag) {
			continue
		}
		n := ReleaseNote{Version: strings.TrimPrefix(r.TagName, "v"), Body: noteBody(r.Body)}
		if !r.PublishedAt.IsZero() {
			n.Published = r.PublishedAt.Local().Format("2006-01-02")
		}
		notes = append(notes, n)
	}
	sort.SliceStable(notes, func(i, j int) bool { return versionNewer(notes[i].Version, notes[j].Version) })
	return notes
}

// versionParts reads "v1.9.41" / "1.9.41" as numbers (missing parts 0).
func versionParts(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, " -+"); i >= 0 {
		v = v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

// versionNewer reports whether a is a later version than b.
func versionNewer(a, b string) bool {
	x, y := versionParts(a), versionParts(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// CheckLatestVersion asks GitHub for the newest public release.
func (a *App) CheckLatestVersion() (*UpdateInfo, error) {
	defer recoverBound(a.ctx, "CheckLatestVersion")
	rel, err := latestRelease(a.ctx)
	if err != nil {
		return nil, err
	}
	info := &UpdateInfo{Current: appVersion, Latest: strings.TrimPrefix(rel.TagName, "v"), URL: releasePage, Notes: []ReleaseNote{}, Lang: "en", SaveDir: updateDir()}
	info.Newer = versionNewer(rel.TagName, appVersion)
	if info.Newer {
		if notes, lang := a.releaseNotes(rel.TagName, appVersion); len(notes) > 0 {
			info.Notes, info.Lang = notes, lang
		}
	}
	if !rel.PublishedAt.IsZero() {
		info.Published = rel.PublishedAt.Local().Format("2006-01-02 15:04")
	}
	for _, as := range rel.Assets {
		if as.Name == updateExeName && strings.HasPrefix(as.URL, releaseAssetPrefix) {
			info.HasExe, info.ExeSize = true, as.Size
			info.HasDigest = strings.HasPrefix(as.Digest, "sha256:")
		}
	}
	return info, nil
}

// updateDir is where a downloaded exe goes: the user's Downloads folder,
// or export\ beside the exe when there is none.
func updateDir() string {
	if h := os.Getenv("USERPROFILE"); h != "" {
		d := filepath.Join(h, "Downloads")
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return filepath.Join(exeDir(), "export")
}

// DownloadLatestVersion downloads the newest release's PalaTerm.exe into
// the Downloads folder as PalaTerm-v<版>.exe and returns its path. The file
// is checked against the size and, when GitHub gives one, the SHA-256 of
// the release asset.
func (a *App) DownloadLatestVersion() (string, error) {
	defer recoverBound(a.ctx, "DownloadLatestVersion")
	rel, err := latestRelease(a.ctx)
	if err != nil {
		return "", err
	}
	var url, digest string
	var size int64
	for _, as := range rel.Assets {
		if as.Name == updateExeName && strings.HasPrefix(as.URL, releaseAssetPrefix) {
			url, size, digest = as.URL, as.Size, as.Digest
		}
	}
	if url == "" {
		return "", fmt.Errorf("最新版（%s）に PalaTerm.exe が添付されていません。リリースのページから入手してください", rel.TagName)
	}
	if size <= 0 || size > maxUpdateExe {
		return "", fmt.Errorf("最新版の PalaTerm.exe の大きさが想定外です（%d バイト）", size)
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "PalaTerm/"+appVersion)
	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", connectErr("ダウンロードできませんでした", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if err := statusErr(resp); err != nil {
			return "", err
		}
		return "", fmt.Errorf("ダウンロードできませんでした（HTTP %d）", resp.StatusCode)
	}
	dir := updateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "PalaTerm-" + rel.TagName + ".exe"
	if !strings.HasPrefix(rel.TagName, "v") {
		name = "PalaTerm-v" + rel.TagName + ".exe"
	}
	final := filepath.Join(dir, filepathSafe(name))
	tmp, err := os.CreateTemp(dir, "PalaTerm-download-*.part")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxUpdateExe+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("ダウンロードが途中で止まりました: %v", err)
	}
	if n != size {
		return "", fmt.Errorf("ダウンロードした大きさが合いません（%d / %d バイト）", n, size)
	}
	if want, ok := strings.CutPrefix(digest, "sha256:"); ok && !strings.EqualFold(want, hex.EncodeToString(h.Sum(nil))) {
		return "", fmt.Errorf("ダウンロードしたファイルの SHA-256 が、リリースに登録された値と合いません")
	}
	_ = os.Remove(final)
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", err
	}
	lastUpdate.Lock()
	lastUpdate.path = final
	lastUpdate.Unlock()
	return final, nil
}

// lastUpdate is the exe this run downloaded last (開く shows it selected).
var lastUpdate struct {
	sync.Mutex
	path string
}

// filepathSafe keeps a file name to letters, digits and . - _.
func filepathSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, s)
}

// OpenUpdateFolder shows the downloaded exe in Explorer: its folder with
// the file selected, or the folder alone when the file is gone.
func (a *App) OpenUpdateFolder() error {
	lastUpdate.Lock()
	p := lastUpdate.path
	lastUpdate.Unlock()
	if p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return showInExplorer(p)
		}
	}
	return startExplorer(updateDir())
}

// OpenReleasePage opens what changed in version (the latest) in the
// browser: the Japanese manual's section of it when the page is in
// Japanese (where the sections start when the version is not known: the
// check failed), else the English release page. Only these fixed addresses.
func (a *App) OpenReleasePage(version string) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, version)
	if a.uiLang() != "en" {
		if digits != "" && len(digits) <= 8 {
			runtime.BrowserOpenURL(a.ctx, fmt.Sprintf(manualChanges, digits))
		} else {
			runtime.BrowserOpenURL(a.ctx, manualChangesTop)
		}
		return
	}
	runtime.BrowserOpenURL(a.ctx, releasePage)
}

// OpenAppFolder shows the folder of the running PalaTerm.exe (where the
// downloaded one goes in its place).
func (a *App) OpenAppFolder() error {
	return startExplorer(exeDir())
}
