# ビルド手順

PalaTerm は Go + Wails(v2) 製の単一 Windows 実行ファイルです。

## 必要なもの

- Go 1.23 以降（開発時は 1.26 で動作確認）
- Windows 10/11（実行時は WebView2 ランタイムが必要。Win10/11 には標準搭載）

## ビルド（推奨・wails CLI 不要）

Wails アプリはビルドタグを付けた `go build` で直接 exe 化できます。
本プロジェクトはフロントエンドをビルド不要の素の HTML/CSS/JS で書いているため、
npm も wails CLI も不要です。

```bash
go build -tags production -ldflags="-H windowsgui -s -w" -o build/bin/PalaTerm.exe .
```

- `-tags production` : 埋め込み済みフロントエンド資産を使う（開発サーバに接続しない）
- `-H windowsgui`    : コンソールウィンドウを出さない GUI アプリにする
- `-s -w`            : デバッグ情報を除去してサイズ削減

生成物: `build/bin/PalaTerm.exe`（約 11MB、単一ファイル、インストール不要）

## テスト

```bash
go test ./...
```

- `internal/vault`  : 暗号化・復号の往復、誤パスワード検知、平文非混入
- `internal/profile`: 14 プロファイルの正規表現が全て有効
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
