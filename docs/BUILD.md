# ビルド手順

PalaTerm は Go + Wails(v2) 製の単一 Windows 実行ファイルです。

## 必要なもの

- Go 1.26 以降（`go.mod` の `go 1.26.0`）
- Windows 10/11（実行時は WebView2 ランタイムが必要。現行の Win10/11 には入っていますが、Windows 10 LTSC・Windows Server・オフラインイメージには入っていないことがあります。その PC では先に Microsoft の「WebView2 Evergreen Standalone Installer」（オフライン版）を入れてください）

## ビルド（推奨・wails CLI 不要）

Wails アプリはビルドタグを付けた `go build` で直接 exe 化できます。
本プロジェクトはフロントエンドをビルド不要の素の HTML/CSS/JS で書いているため、
npm も wails CLI も不要です。

```bash
go run tools/mkwails.go
go build -modfile build/release.mod -trimpath -buildvcs=false -tags production -ldflags="-H windowsgui -s -w" -o build/bin/PalaTerm.exe .
```

（リポジトリ直下の `build.bat` も同じ 2 行です。リポジトリの直下で実行します。）

- `go run tools/mkwails.go` : Wails の写し（関数を 1 つ直したもの）と、それを指す `build/release.mod` を作る（下の「Wails の写しでビルドする理由」）
- `-modfile build/release.mod` : `go.mod` の代わりにそれを使う（`go.mod`・モジュールキャッシュは書き換えません）
- `-trimpath`        : exe にビルド環境のパスを埋め込まない
- `-buildvcs=false`  : git の状態（コミット・変更の有無）を埋め込まない（下の「公開 exe と同じものを作る」用）
- `-tags production` : 埋め込み済みフロントエンド資産を使う（開発サーバに接続しない）
- `-H windowsgui`    : コンソールウィンドウを出さない GUI アプリにする
- `-s -w`            : デバッグ情報を除去してサイズ削減

生成物: `build/bin/PalaTerm.exe`（約 11MB、単一ファイル、インストール不要）

### Wails の写しでビルドする理由（v1.9.40〜）

対話窓の「貼り付けの確認」ウィンドウをすぐ開くために、PalaTerm は見えない確認ウィンドウを 1 つ先に起動しておきます。
ところが Wails v2.9.2 は、新しいウィンドウを画面の中央へ寄せるときに、**まだ表示していないウィンドウでもアクティブにします**
（`internal/frontend/desktop/windows/winc/form.go` の `Center` が `SetWindowPos` に `SWP_NOACTIVATE` を付けない）。
見えないウィンドウが数十ミリ秒だけキー入力の先になり、その瞬間に対話窓で打った文字が失われました（計測: 320 打鍵中 14）。
アプリの側からは止められないため、公開 exe は **`Center` だけを直した Wails の写し**でビルドします。

- `tools/mkwails.go` が、モジュールキャッシュの Wails（`go.sum` で照合済みのもの）を `build/wails` へ写し、`form.go` の 3 か所を書き換えます:
  `Center` が「まだ見えていない・かつ隠れたまま起動すると名乗ったプロセスのウィンドウ」のときだけ `SWP_NOACTIVATE | SWP_NOZORDER` を付ける／
  その判断に使う import 2 つ／「この exe は直した Wails でできている」と名乗る 1 行。**表示して開くウィンドウは、元の Wails とまったく同じ呼び出しのまま**です
- 書き換える前の `form.go` の SHA-256 を道具が確かめます（Wails を更新すると、変更を見直すまでビルドが止まります）
- `build/release.mod` は `go.mod` に `replace github.com/wailsapp/wails/v2 => ./build/wails` を足しただけのもの。`build/wails`・`build/release.mod`・`build/release.sum` は毎回作り直され、リポジトリには入りません
- 見えない確認ウィンドウを先に起動するのは、直した Wails でできた exe だけです（exe が起動時に自分で確かめます）。
  今までの 1 行（`go build -trimpath -buildvcs=false -tags production -ldflags="-H windowsgui -s -w" -o build/bin/PalaTerm.exe .`）でもビルドでき、
  その exe は確認ウィンドウを押してから起動します（v1.9.39 と同じ動き。打鍵は失われません）
- `go version -m PalaTerm.exe` には `github.com/wailsapp/wails/v2 v2.9.2 => ./build/wails (devel)` と出ます

### 公開 exe と同じものを作る（再現可能なビルド）

v1.9.20 以降のリリースは、タグごとに GitHub Actions（`.github/workflows/build.yml`）でもビルドされ、
同じソースと同じ Go（`go.mod` の `toolchain`）からはバイト単位で同じ exe ができます。
リリースノートの SHA-256 と見比べるには、改行を変換せずに取り出したソースからビルドします。

```bash
git clone -c core.autocrlf=false https://github.com/kojio145/palaterm.git
cd palaterm && git checkout v1.9.40        # 確かめたい版
go run tools/mkwails.go
CGO_ENABLED=0 go build -modfile build/release.mod -trimpath -buildvcs=false -tags production -ldflags="-H windowsgui -s -w" -o build/bin/PalaTerm.exe .
sha256sum build/bin/PalaTerm.exe
```

v1.9.20〜v1.9.39 のタグは `go run tools/mkwails.go` の行と `-modfile build/release.mod` が無い 1 行（その版の `docs/BUILD.md` のとおり）でビルドします。

## テスト

```bash
go test ./...
```

- `internal/vault`  : 暗号化・復号の往復、誤パスワード検知、平文非混入
- `internal/profile`: 標準 17 プロファイル（件数も検査）の待ち文字列が全て正規表現として有効
- `internal/runner` : インプロセス疑似 Cisco 機に対する接続→ログイン→enable→コマンド→ログ保存の E2E
- ルート           : App 層の vault 作成・機器 CRUD・永続化・暗号化往復

## 補足: wails CLI について

`wails build` も使えますが、Go 1.26 では wails v2.9.2 が依存する
`golang.org/x/tools@v0.17.0` がコンパイルできず、wails CLI 自体のインストールに
失敗します。上記の直接 `go build` 方式を推奨します。
（将来 wails 側が新しい x/tools に追随すれば `wails build` も利用可能になります）

## アイコン・バージョン情報（タスクバー / プロパティ表示）

exe にアイコンと VERSIONINFO（製品名 PalaTerm / 作者 KJO / バージョン）を埋め込むため、
`rsrc_windows_amd64.syso`（`build/windows/winres.json` から生成した Windows リソース）を
リポジトリに含めています。`go build` はこの `.syso` を自動でリンクするので、
通常のビルドでアイコンとバージョン情報が付きます。
VERSIONINFO はウイルス対策ソフトの誤検知低減にも寄与します。

リソースを作り直す場合（バージョン番号変更時は `build/windows/winres.json` を先に編集）:

```bash
go run tools/mkicon.go build/windows/icon.ico          # icon.ico と icon_*.png を再生成
go install github.com/tc-hib/go-winres@latest          # 未導入なら
go-winres make --in build/windows/winres.json --out rsrc --arch amd64
```

## コード署名（配布時）

無署名 exe は初回実行時に SmartScreen 警告が出ます。OSS 向け無料署名サービス
（SignPath）や Microsoft Store 経由（2025年に個人登録無料化）で警告を解消できます。
