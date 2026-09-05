// PalaTerm i18n. Loaded before app.js so t() is available everywhere.
//
// Japanese is the source language: every user-visible string in the code is
// written in Japanese and wrapped in t(). The EN table below maps them to
// English; a missing entry falls back to the original string. The language
// lives in localStorage (needed on the lock screen, before the vault opens).
// Default is English.

const LANG = (() => {
  try { return localStorage.getItem("palaterm_lang") || "en"; } catch { return "en"; }
})();

// t("日本語", {n: 5}) — translate (when LANG !== "ja") then substitute {n}.
function t(s, vars) {
  let out = LANG === "ja" ? s : (I18N_EN[s] !== undefined ? I18N_EN[s] : s);
  if (vars) for (const k of Object.keys(vars)) out = out.split("{" + k + "}").join(String(vars[k]));
  return out;
}

// terr(e) — translate a backend error message: exact match first, then the
// pattern table (patterns replace in place, so a translated fragment inside a
// longer wrapped error also converts).
function terr(e) {
  let s = String(e ?? "");
  if (LANG === "ja") return s;
  if (I18N_EN[s] !== undefined) return I18N_EN[s];
  for (const [re, rep] of I18N_EN_PATTERNS) {
    if (re.test(s)) s = s.replace(re, rep);
  }
  return s;
}

// Backend messages that carry a dynamic value.
const I18N_EN_PATTERNS = [
  [/^「(.+)」は既に登録されています$/, '"$1" is already registered'],
  [/^同名のプロファイル「(.+)」が既にあります$/, 'A profile named "$1" already exists'],
  [/^機器「(.+)」がこのプロファイルを使用中のため削除できません$/, 'Cannot delete: device "$1" uses this profile'],
  [/^IPアドレスの形式が不正です: (.+)$/, "Invalid IP address: $1"],
  [/^ポート番号が範囲外です: (.+)（0〜65535）$/, "Port out of range: $1 (0–65535)"],
  [/^接続方式が不正です: (.+)$/, "Invalid connection method: $1"],
  [/^踏み台(\d)段目のホスト形式が不正です: (.+)$/, "Bastion hop $1: invalid host: $2"],
  [/^踏み台(\d)段目の接続方式が不正です$/, "Bastion hop $1: invalid connection method"],
  [/^JSONの形式が不正です: (.+)$/, "Invalid JSON: $1"],
  [/ホストキーが前回接続時と異なります（(.+?)、今回: (.+?) \/ 記録: (.+?)）。中間者攻撃または機器交換の可能性があります。機器を交換した場合は機器の編集画面で「ホストキー記録を削除」してから接続し直してください/,
    'Host key for $1 changed (now: $2 / pinned: $3). Possible man-in-the-middle attack or a replaced device. If the device was replaced, use "Clear pinned host key" in the device editor and reconnect.'],
  [/— 機器が新しい暗号方式に対応していない可能性があります。機器の編集画面で「レガシー暗号を許可」を有効にしてください/,
    '— the device may not support modern algorithms. Enable "Allow legacy ciphers" in the device editor.'],
  [/接続方式=Telnet ですが、ポート(\d+)はSSH用です。機器の編集画面でポートを23に直すか、接続方式をSSHに変更してください/,
    "The method is Telnet but port $1 is the SSH port. In the device editor, set the port to 23 or change the method to SSH."],
  [/接続方式=SSH ですが、ポート(\d+)はTelnet用です。機器の編集画面でポートを22に直すか、接続方式をTelnetに変更してください/,
    "The method is SSH but port $1 is the Telnet port. In the device editor, set the port to 22 or change the method to Telnet."],
  [/^ログインはできましたが、機器は「(.+?)」を表示していて、OSタイプ「(.+?)」が待つプロンプト「(.+?)」になりません。機器の権限レベル（昇格が必要か）と、機器の編集画面のOSタイプ設定を確認してください$/,
    'Logged in, but the device is showing "$1" while OS type "$2" waits for "$3". Check whether the account needs privilege escalation, and the OS type set in the device editor.'],
  [/^認証に失敗しました。ユーザー名とパスワードを確認してください$/,
    "Authentication failed — check the username and password"],
  [/^コンソールから応答がありません。ケーブルの結線・COMポート・ボーレートと、機器の電源を確認してください$/,
    "No response from the console — check the cable, COM port, baud rate, and that the device is powered on"],
  [/^接続はできましたが、機器から何も受信しませんでした。ポート番号と機器の状態を確認してください$/,
    "Connected, but nothing was received from the device — check the port number and the device"],
  [/^踏み台(\d+)段目のログインに失敗: /, "Jump host #$1 login failed: "],
  [/^踏み台(\d+)段目へのジャンプに失敗: /, "Jump to hop #$1 failed: "],
  [/^機器へのジャンプに失敗: /, "Jump to the device failed: "],
  [/(\S+) がログインを拒否しました（ユーザー名・パスワードを確認してください）/,
    "$1 rejected the login — check the username and password"],
  [/(\S+) のログイン後にプロンプトが出ませんでした/, "no shell prompt from $1 after logging in"],
  [/^(.+) は既に接続中です$/, "$1 is already connected"],
  [/^(.+) は接続していません$/, "$1 is not connected"],
  [/^この鍵はPuTTY形式（\.ppk）です。PuTTYgenで開き「Conversions → Export OpenSSH key」で変換したファイルを指定してください: (.+)$/,
    'This is a PuTTY key (.ppk). Open it in PuTTYgen, use "Conversions → Export OpenSSH key", and point at the converted file: $1'],
  [/^秘密鍵のパスフレーズが違います: (.+)$/, "Wrong private-key passphrase: $1"],
  [/この秘密鍵はパスフレーズで保護されています。編集画面の「秘密鍵のパスフレーズ」を入力してください: (.+)$/,
    'This private key is passphrase-protected. Enter its passphrase in the editor ("Private key passphrase"): $1'],
];

const I18N_EN = {
  // ---- sidebar / navigation ----
  "機器一覧": "Devices",
  "コマンドセット": "Command Sets",
  "実行": "Run",
  "ログ設定": "Log Settings",
  "OSタイプ設定": "OS Types",
  "パスワード変更": "Change Password",
  "リセット": "Reset",
  "バージョン情報": "About",
  "ロック": "Lock",

  // ---- lock screen ----
  "マスターパスワード": "Master password",
  "ロック解除": "Unlock",
  "暗号化された機器情報を復号します": "Decrypts your encrypted device data",
  "はじめての起動です。機器情報を暗号化して保存するためのマスターパスワードを設定してください。":
    "First launch. Set a master password used to encrypt and store your device data.",
  "確認のため再入力": "Re-enter to confirm",
  "作成して開始": "Create & start",
  "パスワードが違うか、ファイルが壊れています": "Wrong password, or the vault file is corrupted",
  "パスワードを入力してください": "Enter a password",
  "確認用パスワードが一致しません": "Passwords do not match",
  "作成に失敗しました": "Failed to create",

  // ---- common ----
  "確認": "Confirm",
  "キャンセル": "Cancel",
  "閉じる": "Close",
  "保存": "Save",
  "変更": "Change",
  "削除": "Delete",
  "編集": "Edit",
  "複製": "Duplicate",
  "接続": "Connect",
  "保存しました": "Saved",
  "削除しました": "Deleted",
  "変更しました": "Changed",
  "保存失敗": "Save failed",
  "削除失敗": "Delete failed",
  "変更失敗": "Change failed",
  "複製失敗": "Duplicate failed",
  "作成失敗": "Create failed",
  "接続失敗": "Connection failed",
  "読み込み失敗": "Import failed",
  "書き出し失敗": "Export failed",
  "選択失敗": "Selection failed",
  "コピーできませんでした": "Could not copy",
  "{v} をコピーしました": "Copied {v}",
  "表示 / 非表示": "Show / hide",
  "複製しました: {n}": "Duplicated: {n}",
  "書き出しました: {p}": "Exported to: {p}",
  "「{n}」を読み込みました": 'Imported "{n}"',
  "CSV読込": "Import CSV",
  "CSV書出": "Export CSV",
  "ファイル読込": "Import File",
  "ファイル書出": "Export Files",

  // ---- about / password / reset ----
  "バージョン": "Version",
  "作者": "Author",
  "マスターパスワード変更": "Change Master Password",
  "現在のマスターパスワード": "Current master password",
  "新しいマスターパスワード": "New master password",
  "新しいパスワードを入力してください": "Enter a new password",
  "マスターパスワードを変更しました": "Master password changed",
  "すべてのデータをリセット": "Reset All Data",
  "リセットすると、登録済みの<b>すべての機器・グループ・コマンドセット・OSタイププロファイル、およびマスターパスワード</b>が完全に削除されます。<br>この操作は<b>元に戻せません</b>。<br><span class=\"muted\" style=\"font-size:12px\">ログファイルと export フォルダ内の書き出しファイルは削除されず残ります</span>":
    "Resetting permanently deletes <b>every registered device, group, command set, OS type profile, and the master password</b>.<br>This <b>cannot be undone</b>.<br><span class=\"muted\" style=\"font-size:12px\">Log files and exported files under the export folder are kept.</span>",
  "リセットする": "Reset",
  "最終確認": "Final Confirmation",
  "本当にすべてのデータを削除して初期化しますか？": "Really delete all data and start over?",
  "続行するにはマスターパスワードを入力してください": "Enter the master password to continue.",
  "削除して初期化": "Delete & start over",
  "リセット失敗": "Reset failed",
  "危険な操作": "Danger Zone",
  "すべてのデータ（機器・グループ・コマンドセット・OSタイププロファイル・マスターパスワード）を削除して初期状態に戻します。実行にはマスターパスワードの入力が必要です":
    "Deletes all data (devices, groups, command sets, OS type profiles, and the master password) and starts over. Requires the master password.",
  "リセット（全データ初期化）…": "Reset (erase all data)…",

  // ---- devices tab ----
  "機器グループを選択": "Select a Device Group",
  "グループを選ぶか、新規に作成してください。グループ作成後にそのグループへ機器を追加します":
    "Pick a group or create a new one. You add devices to the group after creating it.",
  "＋ 新規グループ作成": "+ New Group",
  "会社・拠点などの単位で作成": "One per company / site, etc.",
  "{n} 台": "{n} devices",
  "⠿ ドラッグで並替": "⠿ drag to reorder",
  "ドラッグで並び替え。クリック=選択 / Ctrl+クリック=追加選択 / Shift+クリック=範囲選択 — 選択した複数行はまとめてドラッグで移動できます":
    "Drag to reorder. Click = select / Ctrl+click = add to selection / Shift+click = range — drag any selected row to move all selected rows together.",
  "新規グループ作成": "New Group",
  "会社名・拠点名など。作成後にこのグループへ機器を追加できます。":
    "Company or site name. You can add devices to this group after creating it.",
  "グループ名": "Group name",
  "例: A社": "e.g. Acme Corp",
  "作成して機器追加へ": "Create & add devices",
  "グループ名を入力してください": "Enter a group name",
  "同名のグループが既にあります": "A group with this name already exists",
  "グループ「{g}」を作成しました。機器を追加してください": 'Group "{g}" created. Add devices to it.',
  "← 対象選択": "← Groups",
  "グループ「{g}」": 'Group "{g}"',
  "全機器": "All devices",
  "{n} 台表示 / 実行対象のオンオフは実行画面で。表の項目は直接編集できます":
    "{n} devices. Run targets are toggled on the Run tab. Cells are editable in place.",
  "グループ名変更": "Rename Group",
  "グループ削除": "Delete Group",
  "＋ 機器を追加": "+ Add Device",
  "このグループに機器がありません。": "No devices in this group.",
  "機器がありません。「＋ 機器を追加」から登録してください。": 'No devices yet. Use "+ Add Device" to register one.',
  "ホスト名": "Hostname",
  "IPアドレス": "IP Address",
  "拠点名": "Site",
  "拠点": "site",
  "OS": "OS",
  "グループを削除": "Delete Group",
  "グループ「<b>{g}</b>」を削除しますか？<br><span class=\"muted\" style=\"font-size:12px\">所属機器は残り、グループ未設定になります</span>":
    'Delete group "<b>{g}</b>"?<br><span class="muted" style="font-size:12px">Its devices are kept and become ungrouped.</span>',
  "グループを削除しました": "Group deleted",
  "グループ名を変更": "Rename Group",
  "新しいグループ名": "New group name",
  "名前を入力してください": "Enter a name",
  "対話接続ウィンドウを開きました": "Interactive terminal window opened",
  "ホスト名を変更しました": "Hostname changed",
  "変更できません": "Cannot change",
  "保存できません": "Cannot save",
  "機器を削除": "Delete Device",
  "機器「<b>{n}</b>」を削除しますか？": 'Delete device "<b>{n}</b>"?',
  "踏{n}": "via {n}",

  // ---- CSV dialogs ----
  "CSV書き出し": "Export CSV",
  "書き出す対象": "Scope",
  "待機 あと{n}秒": "waiting {n}s",
  "この踏み台にレガシー暗号を許可 ⓘ": "Allow legacy ciphers for this hop ⓘ",
  "この踏み台とのSSHで古い暗号方式も候補に含めます。機器側の同名設定とは独立しています（踏み台と機器は別のマシンなので、片方が古いことをもう片方の暗号強度を下げる理由にはしません）": "Offers the older SSH algorithms on the hop to this jump host. Independent of the device's own setting: they are different machines, and one being old is no reason to weaken the other.",
  "全機器（{n}台）": "All devices ({n})",
  "{g}（{n}台）": "{g} ({n})",
  "※パスワードも平文で書き出されます。編集後は削除してください。":
    "Passwords are written in plain text. Delete the file when done.",
  "※踏み台の「ジャンプコマンド」「秘密鍵のパスフレーズ」「レガシー暗号を許可」はCSVに含まれません。読み込むと空になるので、CSVは完全なバックアップではありません。":
    "A jump host's jump command, key passphrase and legacy-cipher setting are not written to CSV. They come back empty on import, so a CSV is not a complete backup.",
  "※踏み台の「ジャンプコマンド」「秘密鍵のパスフレーズ」「レガシー暗号を許可」はCSVに含まれません。踏み台を使う機器は、読み込み後に編集画面で入れ直してください。":
    "A jump host's jump command, key passphrase and legacy-cipher setting are not carried in CSV. Re-enter them in the device editor after importing.",
  "書き出す": "Export",
  "CSV読み込み": "Import CSV",
  "読み込んだ機器の所属グループ": "Group for imported devices",
  "CSVのグループをそのまま使う": "Keep each row's own group",
  "指定グループに追加する": "Assign all to one group",
  "追加先グループ": "Target group",
  "ファイルを選択して読み込む": "Choose file & import",
  "{n} 台を読み込みました": "Imported {n} devices",

  // ---- device editor ----
  "機器を編集": "Edit Device",
  "機器を追加": "Add Device",
  "ホスト名（ログファイル名に使用）": "Hostname (used in log file names)",
  "グループ（会社）": "Group (company)",
  "例: A社（空欄=未設定）": "e.g. Acme Corp (blank = none)",
  "例: 本社 / 東京DC": "e.g. HQ / Tokyo DC",
  "拠点（任意）": "Site (optional)",
  "接続方式": "Connection",
  "シリアル": "Serial",
  "OSタイプ": "OS Type",
  "認証情報（暗号化保存）": "Credentials (stored encrypted)",
  "ユーザー名": "Username",
  "SSH認証方式": "SSH auth method",
  "パスワード": "Password",
  "公開鍵 (Ed25519/RSA/ECDSA)": "Public key (Ed25519/RSA/ECDSA)",
  "公開鍵": "Public key",
  "enable / 昇格パスワード（任意・ない機種は空欄）": "Enable password (optional)",
  "秘密鍵ファイル": "Private key file",
  "秘密鍵のパスフレーズ（保護されていない鍵は空欄・暗号化保存）": "Private key passphrase (blank for unprotected keys; stored encrypted)",
  "鍵のパスフレーズ（任意）": "Key passphrase (optional)",
  "参照…": "Browse…",
  "COMポート": "COM port",
  "自動検出": "Auto-detect",
  "ボーレート": "Baud rate",
  "ボーレートが不正です": "Invalid baud rate",
  "ポート": "Port",
  "踏み台サーバ（任意・最大5段。外側＝手前から順に）": "Jump hosts (optional, up to 5 hops, outermost first)",
  "＋ 踏み台を追加": "+ Add Jump Host",
  "{n}段目の踏み台": "Jump host #{n}",
  "ホスト": "Host",
  "ユーザー": "User",
  "SSH認証": "SSH auth",
  "秘密鍵ファイル（公開鍵認証時）": "Private key file (for public-key auth)",
  "次ホップへのジャンプコマンド（空欄=標準。NW機器踏み台などコマンド形式が違う場合に指定）":
    "Jump command to the next hop (blank = standard; set when the hop needs a different syntax)",
  "例: ssh -l {user} {host}　（使用可: {user} {host} {port}）": "e.g. ssh -l {user} {host}  (available: {user} {host} {port})",
  "ホスト名は必須です": "Hostname is required",
  "レガシー暗号を許可（古い機器向け・通常はオフ） ⓘ": "Allow legacy ciphers (old devices only; normally off) ⓘ",
  "SSHで古い暗号方式（SHA-1系の鍵交換・CBC・3DES）も候補に含めます。通常はオフのまま（強い方式のみ）。新しい方式に対応していない古い機器で接続エラーになる場合だけ有効にしてください":
    "Additionally offers old SSH algorithms (SHA-1 key exchange, CBC, 3DES). Keep it off normally (strong algorithms only); enable it only when an old device fails to connect with modern ones.",
  "ホストキー記録を削除": "Clear pinned host key",
  "機器交換でホストキーが変わったときに使用": "Use when a replaced device changed its host key",
  "SSHのホストキーは初回接続時に記録され、次回以降は一致を検証します（なりすまし防止）。機器を交換・OS再インストールしてホストキーが変わった場合のみ、記録を削除して再接続してください":
    "SSH host keys are pinned on first connection and verified on every later one (anti-spoofing). Clear the record and reconnect only when the key legitimately changed (replaced device / OS reinstall).",
  "機器「<b>{n}</b>」（踏み台含む）のホストキー記録を削除しますか？<br><span class=\"muted\" style=\"font-size:12px\">次回接続時に新しいホストキーを記録し直します。機器を交換していないのにキーが変わった場合は、削除せずネットワーク管理者に確認してください</span>":
    'Clear the pinned host keys for device "<b>{n}</b>" (including its jump hosts)?<br><span class="muted" style="font-size:12px">The next connection pins the new key. If the key changed without a device replacement, do NOT clear it — check with your network administrator.</span>',
  "ホストキー記録を削除しました": "Pinned host keys cleared",
  "IPアドレスは必須です": "IP address is required",
  "「{n}」は既に登録されています。別のホスト名にしてください": '"{n}" is already registered. Choose another hostname.',

  // ---- command sets tab ----
  "機器ごとに割り当てて一括実行するコマンド群。CSV書出は export\\command-sets\\ に1セット1ファイルで保存されます":
    "Command bundles assigned per device and run in batch. Export writes one CSV per set under export\\command-sets\\.",
  "＋ セットを追加": "+ Add Set",
  "コマンドセットがありません。": "No command sets.",
  "名前": "Name",
  "コマンド数": "Commands",
  "プレビュー": "Preview",
  "{n} コマンド": "{n} commands",
  "コマンドセットを削除": "Delete Command Set",
  "コマンドセット「<b>{n}</b>」を削除しますか？": 'Delete command set "<b>{n}</b>"?',
  "コマンドセットを編集": "Edit Command Set",
  "コマンドセットを追加": "Add Command Set",
  "セット名": "Set name",
  "コマンド（1行1コマンド・待機秒はコマンドごとに指定）": "Commands (one per row; wait seconds per command)",
  "コマンド": "Command",
  "待機(リモート)": "Wait (remote)",
  "待機(シリアル)": "Wait (serial)",
  "この行の下に挿入": "Insert below this row",
  "＋ 行を追加": "+ Add Row",
  "貼り付けは「行を追加」後の各コマンド欄へ。複数行をまとめて貼るとその行数ぶん自動で分割されます。":
    "Paste into any command cell; a multi-line paste splits into that many rows automatically.",
  "セット名は必須です": "Set name is required",

  // ---- run tab ----
  "グループ「{g}」を選択しました": 'Group "{g}" selected',
  "対象 {a} / {b} 台（チェック=実行対象。Shift+クリックで範囲をまとめて切替）":
    "{a} of {b} devices targeted (checkbox = run target; Shift+click toggles a range)",
  "まず「グループで対象を選択…」から実行するグループを選んでください":
    'First pick a group from "Select target group…"',
  "▶ 一括実行": "▶ Run Batch",
  "■ 中止": "■ Abort",
  "↻ 失敗のみ再実行（{n}台）": "↻ Retry Failed ({n})",
  "グループで対象を選択…": "Select target group…",
  "ログフォルダを開く": "Open Log Folder",
  "成功": "OK",
  "失敗": "Failed",
  "実行中": "Running",
  "予想残り": "ETA",
  "完了済みコマンド数と経過時間から算出した全体の予想残り時間。実行が進むほど精度が上がります":
    "Estimated time remaining, computed from commands completed vs. elapsed time. Gets more accurate as the run progresses.",
  "このグループに実行対象の機器がありません。機器一覧でチェックしてください。": "No devices in this group.",
  "「グループで対象を選択…」から実行するグループを選んでください。": 'Pick a group from "Select target group…".',
  "全選択/全解除": "Select / deselect all",
  "状態": "Status",
  "メッセージ": "Message",
  "（なし）": "(none)",
  "(なし)": "(none)",
  "ログ表示": "View Log",
  "{a} / {b} コマンド完了": "{a} / {b} commands done",
  "待機": "Queued",
  "対象外": "Skipped",
  "接続中": "Connecting",
  "ログイン中": "Logging in",
  "保存中": "Saving",
  "完了": "Done",
  "エラー": "Error",
  "中止しました": "aborted",
  "計測中…": "measuring…",
  "約{n}秒": "~{n}s",
  "約{m}分": "~{m}m",
  "約{m}分{s}秒": "~{m}m {s}s",
  "実行結果のクリア": "Clear Run Results",
  "グループを切り替えると、前回の実行結果と「↻ 失敗のみ再実行（{n}台）」ボタンは消えます。<br>切り替えますか？":
    'Switching groups clears the last run\'s results and the "↻ Retry Failed ({n})" button.<br>Switch anyway?',
  "切り替える": "Switch",
  "一括実行": "Run Batch",
  "グループ「<b>{g}</b>」の ": 'Group "<b>{g}</b>": ',
  "{s}<b>{n} 台</b>に一括実行します。よろしいですか？": "{s}run the batch on <b>{n} devices</b>?",
  "▶ 実行": "▶ Run",
  "実行開始に失敗": "Failed to start",
  "失敗のみ再実行": "Retry Failed",
  "失敗した <b>{n} 台</b>のみ再実行します。よろしいですか？": "Re-run only the <b>{n} failed devices</b>?",
  "↻ 再実行": "↻ Retry",
  "実行が完了しました": "Batch finished",
  "ログ": "Log",
  "ログを開けません": "Cannot open log",

  // ---- log settings tab ----
  "実行とログ保存の共通設定": "Shared settings for runs and log output",
  "▶ 一括実行中です。実行中のバッチは開始時点の設定で動くため、ここでの変更は次回の実行から反映されます":
    "▶ A batch is running. It keeps the settings it started with; changes here apply from the next run.",
  "最大並列数（0=無制限）": "Max parallel (0 = unlimited)",
  "接続タイムアウト（秒）": "Connect timeout (s)",
  "コマンドタイムアウト（秒）": "Command timeout (s)",
  "ログ保存フォルダ": "Log folder",
  "ログファイル名テンプレート": "Log file name template",
  "ファイル名に使える変数（クリックでコピー）:": "Variables for the file name (click to copy):",
  "日付（yyyymmdd）": "Date (yyyymmdd)",
  "時刻（hhmmss）": "Time (hhmmss)",
  "OS種別": "OS type",
  "時刻（hhmm）": "Time (hhmm)",
  "※ログ保存フォルダに相対パス（例: logs）を指定した場合、PalaTerm.exe と同じフォルダが基準になります":
    "A relative log folder (e.g. logs) is resolved from the folder PalaTerm.exe runs in.",
  "未保存の変更": "Unsaved Changes",
  "設定に保存されていない変更があります。<br>保存せずに移動しますか？": "You have unsaved settings.<br>Leave without saving?",
  "破棄して移動": "Discard & leave",

  // ---- OS types tab ----
  "機器種別ごとのログイン自動化（expect/send）。待つ文字は「#」「assword:」のような文字そのまま（部分一致）。ファイル書出は export\\os-profiles\\ に1プロファイル1ファイル（JSON）。削除してもファイルは消えないので、読込でいつでも戻せます":
    'Login automation per device type (expect/send). Waits are literal text such as "#" or "assword:" (substring match). Export writes one JSON per profile under export\\os-profiles\\; deleting never touches the files, so Import can always restore one.',
  "＋ 追加": "+ Add",
  "プロファイルがありません。「ファイル読込」で export\\os-profiles\\ のJSONから復元するか、「＋ 追加」で作成してください。":
    'No profiles. Restore from the JSONs under export\\os-profiles\\ via "Import File", or create one with "+ Add".',
  "完了の目印": "Done Marker",
  "ログイン手順": "Login Steps",
  "{n} 行": "{n} steps",
  "プロファイルを削除": "Delete Profile",
  "OSタイププロファイル「<b>{n}</b>」を削除しますか？<br><span class=\"muted\" style=\"font-size:12px\">書き出し済みのJSONファイルは残るため、「ファイル読込」でいつでも戻せます</span>":
    'Delete OS type profile "<b>{n}</b>"?<br><span class="muted" style="font-size:12px">Its exported JSON file is kept, so Import can always bring it back.</span>',

  // ---- profile editor ----
  "OSタイププロファイルを編集": "Edit OS Type Profile",
  "OSタイププロファイルを追加": "Add OS Type Profile",
  "Cisco IOS系の実例を入れてあります。機器に合わせて書き換えてください。":
    "Pre-filled with a Cisco IOS-style example. Adjust it for your device.",
  "プロファイル名": "Profile name",
  "例: MyRouter OS": "e.g. MyRouter OS",
  "showコマンド完了の目印（必須） ⓘ": "Command-done marker (required) ⓘ",
  "コマンドが打てる状態のときに機器が出す表示（例: Router#）。コマンド送信後、これが再び現れたら「出力が終わった」と判定して次のコマンドを送ります。「#」のように末尾の記号だけ書いておけば、設定モードのプロンプト（例: FG(console)#）にも一致します":
    'What the device shows when ready for a command (e.g. Router#). After each command, seeing this again means "output finished" and the next command is sent. Writing just the trailing symbol like "#" also matches config-mode prompts (e.g. FG(console)#).',
  "ページャ解除コマンド（任意・1行1コマンドで複数行可） ⓘ": "Pager-off commands (optional, one per line) ⓘ",
  "ログイン直後に流す、出力の一時停止（--More--）をなくすコマンド。OSによりコマンドやモードが違うため複数行OK（設定モードに入る手順ごと書けます）。FortiGateのように機器側で設定済みの場合は空欄でかまいません":
    "Commands sent right after login to disable output paging (--More--). Multiple lines are fine (including steps to enter config mode). Leave empty when the device is already configured, as on FortiGate.",
  "ページャ表示の目印（任意） ⓘ": "Pager marker (optional) ⓘ",
  "出力の途中一時停止の表示。これが出たらスペースを送って続きを表示します。ページャ解除コマンドで止まらないOSだけ設定します":
    "The mid-output pause marker. When it appears, a space is sent to continue. Only needed for OSes the pager-off commands cannot silence.",
  "ログイン手順 ⓘ": "Login steps ⓘ",
  "上から順に実行。expect列の表示が出るのを待ち、send列の文字列を入力。認証系の行（{user}/{password}/{enable}を送る行）は表示が出なければ自動でスキップされるため、SSH/Telnetで同じプロファイルが使えます。最後の行の入力が終わると自動で「showコマンド完了の目印」を待つため、目印を待つだけの行は不要です":
    "Runs top to bottom: wait for the expect text, then type the send text. Auth rows (sending {user}/{password}/{enable}) are skipped automatically when their prompt never appears, so one profile serves SSH and Telnet. After the last row the command-done marker is awaited automatically — no marker-only row needed.",
  "expect（待つ表示） ⓘ": "expect (text to wait for) ⓘ",
  "プロンプト・expectは待ちたい文字をそのまま書きます（例: Password:）。文字の一部が画面に現れた時点で一致します。大文字小文字は区別されます":
    "Write the literal text to wait for (e.g. Password:). It matches as soon as it appears anywhere in the output. Case-sensitive.",
  "send（入力コマンド） ⓘ": "send (command to type) ⓘ",
  "expectの表示が出たら入力する文字列。変数 {user} {password} {enable} は機器に登録した認証情報に置き換わります。認証系の行（{user}/{password}/{enable}を送る行）は、その表示が出ない機器・接続方式では自動でスキップされます（SSH直接続なら {user}/{password} の行は即スキップ）":
    "Typed once the expect text appears. {user} {password} {enable} are replaced with the device's stored credentials. Auth rows are skipped automatically where their prompt never appears (over direct SSH, {user}/{password} rows are skipped outright).",
  "send に使える変数（機器に登録した認証情報に置き換わります。クリックでコピー）:":
    "Variables for send (replaced with the device's stored credentials; click to copy):",
  "ログインユーザー名": "Login username",
  "ログインパスワード": "Login password",
  "enable / 昇格パスワード": "Enable password",
  "切断コマンド（1行1コマンドで複数行可） ⓘ": "Disconnect commands (one per line) ⓘ",
  "実行終了時にログアウトのため送るコマンド。上から順に1行ずつ送信します（例: exitを2回でログアウトする機器は2行書く）":
    "Sent at the end of a run to log out, one line at a time (e.g. write exit twice for devices that need it twice).",

  // ---- backend errors (static) ----
  "vault is locked": "vault is locked",
  "master password required": "A master password is required",
  "プロファイル名は必須です": "Profile name is required",
  "「showコマンド完了の目印」は必須です": "The command-done marker is required",
  "プロファイル名（name）がありません": "The file has no profile name",
  "「showコマンド完了の目印」（prompt）がありません": "The file has no command-done marker (prompt)",
  "プロファイルが見つかりません": "Profile not found",
  "グループ名は必須です": "Group name is required",
  "新しいグループ名を入力してください": "Enter a new group name",
  "名前は必須です": "Name is required",
  "有効なコマンド行がありません": "No valid command lines",
  "一括実行中です。完了または中止を待ってください": "A batch is running. Wait for it to finish or abort it.",
  "現在のマスターパスワードが違います": "Current master password is incorrect",
  "新しいマスターパスワードを入力してください": "Enter a new master password",

  // ---- terminal window ----
  "対話接続": "Interactive session",
  "接続中…": "Connecting…",
  "接続中… ({a}/{b}秒)": "Connecting… ({a}/{b}s)",
  "接続中… ({a}秒)": "Connecting… ({a}s)",
  "接続済み": "Connected",
  "切断": "Disconnected",
  "[接続エラー] ": "[connection error] ",
  "[切断されました]": "[disconnected]",
  "[ログ保存] ": "[log saved] ",
};
