// Standalone terminal window (child process: PalaTerm.exe --connect <device>).
// Fills the whole window with an xterm terminal, resizable in both directions.

const Term = () => window.go.main.Term;

function b64ToBytes(b64) {
  const bin = atob(b64);
  const arr = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
  return arr;
}
function bytesToB64(bytes) {
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin);
}

// ---- 貼り付けの確認ウィンドウ（PalaTerm.exe --paste）----
async function bootPasteWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "var(--panel)";
  const P = () => window.go.main.Paste;
  const req = await P().Load();
  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;padding:14px 18px;background:var(--panel);font-family:'Segoe UI',sans-serif">
    <div style="display:flex;align-items:center;gap:12px;margin-bottom:6px">
      <div style="color:var(--text);font-size:15px;font-weight:600">${esc(t("貼り付けの確認"))}</div>
      <div class="muted" style="font-size:12px;flex:1">${esc(req.device || "")}</div>
      <button class="btn sm" id="pw-font-dec" data-tip="${esc(t("文字を小さく（Ctrl+-）"))}">A−</button>
      <button class="btn sm" id="pw-font-inc" data-tip="${esc(t("文字を大きく（Ctrl++）"))}">A+</button>
    </div>
    <div id="pw-info" class="muted" style="font-size:12px;margin-bottom:8px"></div>
    ${req.bcast > 0 ? `<div id="pw-bcast" style="font-size:13px;font-weight:600;margin-bottom:8px;padding:6px 10px;border:1px solid #f97316;border-radius:8px;background:#431407;color:#fed7aa">${esc(t("同時入力中: この内容はほかの {n} 台の窓にも送られます", { n: req.bcast }))}</div>` : ""}
    <textarea id="pw-text" spellcheck="false" style="flex:1;min-height:120px;width:100%;resize:none;background:#0b0e13;color:#d6dee8;border:1px solid var(--border);border-radius:8px;padding:8px;font-family:Consolas,monospace;font-size:13px;white-space:pre;overflow:auto"></textarea>
    <label style="display:flex;align-items:center;gap:6px;color:#c7d0dc;font-size:12px;margin:8px 0 12px"><input type="checkbox" id="pw-cr" style="width:auto"> ${esc(t("最後に Enter を送る"))}</label>
    <div class="modal-actions" style="margin-top:0">
      <button class="btn" id="pw-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="pw-ok">${esc(t("貼り付け"))}</button>
    </div>
    <div class="muted" style="font-size:11px;margin-top:8px">${esc(t("Enter = 貼り付け ／ Shift+Enter = 改行を挿入 ／ Esc = キャンセル"))}</div>
  </div>`);
  document.body.appendChild(wrap);
  const ta = document.getElementById("pw-text"), cr = document.getElementById("pw-cr");
  ta.value = req.text || "";
  // 最後に Enter を送る: on by default, then whatever was last chosen here
  // (Alt+V always asks for the Enter, without changing what is remembered).
  // A one-line paste (confirmed only because 同時入力 is on) sends no Enter
  // unless Alt+V asked for it, as without 同時入力.
  cr.checked = req.single ? !!req.cr : (!!req.cr || pasteCrPref());
  cr.addEventListener("change", () => setPasteCrPref(cr.checked));
  // Font size: same controls as the terminal window, remembered separately.
  const FONT_DEF = 13, FONT_MIN = 8, FONT_MAX = 32;
  let fontSize = FONT_DEF;
  try { fontSize = Math.min(FONT_MAX, Math.max(FONT_MIN, parseInt(localStorage.getItem("palaterm_paste_font"), 10) || FONT_DEF)); } catch (e) {}
  function setFont(n) {
    fontSize = Math.min(FONT_MAX, Math.max(FONT_MIN, n));
    ta.style.fontSize = fontSize + "px";
    try { localStorage.setItem("palaterm_paste_font", String(fontSize)); } catch (e) {}
  }
  setFont(fontSize);
  document.getElementById("pw-font-inc").onclick = () => { setFont(fontSize + 1); ta.focus(); };
  document.getElementById("pw-font-dec").onclick = () => { setFont(fontSize - 1); ta.focus(); };
  ta.addEventListener("wheel", e => { if (!e.ctrlKey) return; e.preventDefault(); setFont(fontSize + (e.deltaY < 0 ? 1 : -1)); }, { passive: false });
  const info = () => { document.getElementById("pw-info").textContent = t("{n} 行。各行の末尾は Enter として機器へ送られます。内容はここで編集できます。", { n: ta.value.split(/\r\n|\r|\n/).length }); };
  info();
  ta.addEventListener("input", info);
  const submit = () => P().Submit(ta.value, cr.checked);
  const cancel = () => P().Cancel();
  document.getElementById("pw-ok").onclick = submit;
  document.getElementById("pw-cancel").onclick = cancel;
  // Keys work wherever the focus happens to be: Enter pastes the moment the
  // window appears, before any click into the textarea.
  document.addEventListener("keydown", e => {
    if (e.ctrlKey && (e.key === "+" || e.key === "=" || e.code === "Equal" || e.code === "NumpadAdd")) { e.preventDefault(); setFont(fontSize + 1); return; }
    if (e.ctrlKey && (e.key === "-" || e.code === "Minus" || e.code === "NumpadSubtract")) { e.preventDefault(); setFont(fontSize - 1); return; }
    if (e.ctrlKey && (e.key === "0" || e.code === "Digit0" || e.code === "Numpad0")) { e.preventDefault(); setFont(FONT_DEF); return; }
    if (e.key === "Escape") { e.preventDefault(); cancel(); return; }
    // Enter or Ctrl+Enter (the primary button, as in the main window's
    // dialogs) pastes; Shift+Enter is left to the textarea (a new line), and
    // an Enter that confirms an IME conversion is not a paste.
    if (e.key === "Enter" && !e.shiftKey && !e.altKey && !e.isComposing && e.keyCode !== 229) { e.preventDefault(); submit(); }
  });
  const grab = () => { ta.focus(); ta.setSelectionRange(0, 0); };
  grab();
  window.addEventListener("focus", grab);
  // The OS window may become active a beat after the page is ready; keep
  // asking for the foreground and refocusing until it sticks.
  [100, 400, 900].forEach(ms => setTimeout(() => { if (document.activeElement !== ta) grab(); }, ms));
  P().Focus().catch(() => {});
}

// The paste confirmation's 最後に Enter を送る: on until the user turns it
// off, and from then on what they last chose (shared by every window: they
// all use the same WebView2 profile).
function pasteCrPref() {
  try { return localStorage.getItem("palaterm_paste_cr") !== "0"; } catch (e) { return true; }
}
function setPasteCrPref(on) {
  try { localStorage.setItem("palaterm_paste_cr", on ? "1" : "0"); } catch (e) {}
}

async function bootTerminalWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "#000";

  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:#000">
    <div id="tw-bar" style="display:flex;align-items:center;justify-content:space-between;padding:6px 12px;background:var(--bg);border-bottom:1px solid var(--border);font-family:'Segoe UI',sans-serif">
      <div id="tw-title" style="color:var(--text);font-size:13px;flex:1 1 120px;min-width:60px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(t("対話接続"))}</div>
      <div class="tw-tools" style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;justify-content:flex-end">
        <span id="tw-bc-pill" class="pill hidden" style="background:#7c2d12;color:#fed7aa;border:1px solid #f97316"></span>
        <button class="btn sm" id="tw-bcast" data-tip="${esc(t("オンにした窓どうしで、この窓で打ったキーをすべての窓へ同時に送ります（Tera Term のブロードキャストコマンドのリアルタイムモードと同じ）。ログイン中・切断中・ロック中の窓には送りません"))}">${esc(t("同時入力"))}</button>
        <button class="btn sm" id="tw-tile" data-tip="${esc(t("開いている端末窓をすべて、この窓のある画面いっぱいに並べます（開いた順）"))}">${esc(t("並べ直す"))}</button>
        <span id="tw-copied" class="pill ok hidden">${esc(t("コピーしました"))}</span>
        <button class="btn sm" id="tw-color" data-tip="${esc(t("文字色・背景色を変えます（この PC のすべての端末窓に反映）"))}">${esc(t("色"))}</button>
        <button class="btn sm" id="tw-font-dec" data-tip="${esc(t("文字を小さく（Ctrl+-）"))}">A−</button>
        <button class="btn sm" id="tw-font-inc" data-tip="${esc(t("文字を大きく（Ctrl++）"))}">A+</button>
        <button class="btn sm" id="tw-find" data-tip="${esc(t("検索（Ctrl+Shift+F。Ctrl+F は機器へ送ります）"))}">${esc(t("検索"))}</button>
        <button class="btn sm" id="tw-paste" data-tip="${esc(t("貼り付け（Alt+V）"))}">${esc(t("貼り付け…"))}</button>
        <button class="btn sm" id="tw-log" data-tip="${esc(t("接続した時点からログをファイルへ書いています（Tera Term と同じリアルタイム記録）。停止すると以後の内容は記録されず、再開すると同じファイルに続きが書かれます"))}">${esc(t("ログ停止"))}</button>
        <button class="btn sm" id="tw-logview" data-tip="${esc(t("この対話接続のログを別ウィンドウで開きます（追記を自動で追います）"))}">${esc(t("ログ表示"))}</button>
        <button class="btn sm" id="tw-logdir" data-tip="${esc(t("この対話接続のログがあるフォルダを開きます（まだ何も記録していなければログフォルダ）"))}">${esc(t("ログフォルダを開く"))}</button>
        <button class="btn sm hidden" id="tw-break" data-tip="${esc(t("シリアル回線にブレーク信号を送ります（Alt+B。Tera Term と同じ）。Cisco の起動中に送ると ROMMON に入れます（パスワードリカバリ）"))}">${esc(t("ブレーク"))}</button>
        <button class="btn sm ok-outline hidden" id="tw-reconnect" data-tip="${esc(t("同じ機器にもう一度接続します（切断後に Enter キーでも再接続できます）"))}">${esc(t("再接続"))}</button>
        <button class="btn sm warn-outline hidden" id="tw-switch" data-tip="${esc(t("自動ログインをここで打ち切り、今の画面のまま手で操作します（機器が想定外の画面で止まっているときに）"))}">${esc(t("手動に切り替え"))}</button>
        <span id="tw-state" class="pill run">${esc(t("接続中…"))}</span>
      </div>
    </div>
    <div id="tw-takeover" class="hidden" style="display:flex;align-items:center;gap:10px;padding:6px 12px;background:#2a2412;border-bottom:1px solid #b45309;font-family:'Segoe UI',sans-serif;font-size:12px;color:#fcd34d">
      <span id="tw-takeover-msg" style="flex:1">${esc(t("自動ログインに失敗しましたが、回線はつながったままです。機器が出している画面に合わせて、ここから手で操作できます。"))}</span>
      <button class="btn sm warn-outline" id="tw-takeover-ok">${esc(t("手動で続ける"))}</button>
      <button class="btn sm" id="tw-takeover-close">${esc(t("切断する"))}</button>
    </div>
    <div id="tw-findbar" class="hidden" style="display:flex;align-items:center;gap:6px;padding:4px 12px;background:#161c25;border-bottom:1px solid var(--border);font-family:'Segoe UI',sans-serif;font-size:12px">
      <input id="tw-find-in" placeholder="${esc(t("検索（Enter=次 / Shift+Enter=前 / Esc=閉じる）"))}" style="flex:1;max-width:420px;background:#0b0e13;color:var(--text);border:1px solid var(--border);border-radius:6px;padding:4px 8px;font-size:12px">
      <span id="tw-find-n" class="muted" style="min-width:70px"></span>
      <button class="btn sm" id="tw-find-prev" data-tip="${esc(t("前を検索（Shift+Enter）"))}">▲</button>
      <button class="btn sm" id="tw-find-next" data-tip="${esc(t("次を検索（Enter）"))}">▼</button>
      <button class="btn sm" id="tw-find-close" data-tip="${esc(t("閉じる（Esc）"))}">✕</button>
    </div>
    <div id="tw-host" style="flex:1;min-height:0;background:#000;padding:6px 6px 12px 6px"></div>
    <div id="tw-paste-dlg" class="sub-overlay hidden" style="z-index:30;background:rgba(0,0,0,.6)">
      <div class="sub-modal" style="display:flex;flex-direction:column;width:calc(100vw - 32px);height:calc(100vh - 80px);padding:16px 22px">
        <h3 style="margin-bottom:6px">${esc(t("貼り付けの確認"))} <span class="muted" style="font-size:11px;font-weight:400">${esc(t("（ウィンドウを広げると編集欄も広がります）"))}</span></h3>
        <div id="tw-paste-info" class="muted" style="font-size:12px;margin-bottom:8px"></div>
        <textarea id="tw-paste-text" spellcheck="false" style="flex:1;min-height:120px;width:100%;resize:none;background:#0b0e13;color:#d6dee8;border:1px solid var(--border);border-radius:8px;padding:8px;font-family:Consolas,monospace;font-size:13px;white-space:pre;overflow:auto"></textarea>
        <label style="display:flex;align-items:center;gap:6px;color:#c7d0dc;font-size:12px;margin:8px 0 12px"><input type="checkbox" id="tw-paste-cr" style="width:auto"> ${esc(t("最後に Enter を送る"))}</label>
        <div class="modal-actions" style="margin-top:0">
          <button class="btn" id="tw-paste-cancel">${esc(t("キャンセル"))}</button>
          <button class="btn primary" id="tw-paste-ok">${esc(t("貼り付け"))}</button>
        </div>
        <div class="muted" style="font-size:11px;margin-top:8px">${esc(t("Enter = 貼り付け ／ Shift+Enter = 改行を挿入 ／ Esc = キャンセル"))}</div>
      </div>
    </div>
    <div id="tw-hk" class="sub-overlay hidden" style="z-index:30;background:rgba(0,0,0,.7)">
      <div class="sub-modal" style="width:min(620px,92vw);border-color:var(--warn)">
        <h3 style="color:var(--warn)">${esc(t("ホストキーが前回と異なります"))}</h3>
        <div id="tw-hk-msg" style="font-size:13px;line-height:1.8;margin-bottom:6px"></div>
        <div class="muted" style="font-size:12px;line-height:1.7">${esc(t("機器を交換した／OSを再インストールした場合だけ許可してください。心当たりがなければ、なりすましの可能性があるため許可せずネットワーク管理者に確認してください。"))}</div>
        <div class="modal-actions">
          <button class="btn" id="tw-hk-cancel">${esc(t("キャンセル"))}</button>
          <button class="btn danger" id="tw-hk-ok">${esc(t("機器を交換したので許可して再接続"))}</button>
        </div>
      </div>
    </div>
    <div id="tw-close" class="sub-overlay hidden" style="z-index:35;background:rgba(0,0,0,.7)">
      <div class="sub-modal" style="width:min(460px,92vw)">
        <h3>${esc(t("接続中です"))}</h3>
        <div style="color:#c7d0dc;font-size:13px;line-height:1.8">${esc(t("この機器との接続を切断して、ウィンドウを閉じますか？ ログはここまでの内容で保存されます。"))}</div>
        <div class="modal-actions">
          <button class="btn" id="tw-close-cancel">${esc(t("キャンセル"))}</button>
          <button class="btn danger" id="tw-close-ok">${esc(t("切断して閉じる"))}</button>
        </div>
      </div>
    </div>
    <div id="tw-lock" class="sub-overlay hidden" style="z-index:40;background:#0b0e13">
      <div class="sub-modal" style="width:min(360px,92vw);padding:24px">
        <h3 style="margin-bottom:6px">🔒 ${esc(t("ロック中"))}</h3>
        <div class="muted" style="font-size:12px;line-height:1.7;margin-bottom:12px">${esc(t("接続は保たれています。マスターパスワードを入力すると画面に戻ります。"))}</div>
        <div id="tw-lock-field">
          <input id="tw-lock-in" type="password" style="width:100%;background:var(--bg);color:var(--text);border:1px solid var(--border);border-radius:8px;padding:9px">
        </div>
        <div id="tw-lock-err" style="color:#f87171;font-size:12px;min-height:18px;margin-top:6px"></div>
        <button class="btn primary" id="tw-lock-ok" style="width:100%;margin-top:6px">${esc(t("ロック解除"))}</button>
      </div>
    </div>
    <div id="tw-pw" class="sub-overlay hidden" style="z-index:30;background:rgba(0,0,0,.85)">
      <div class="sub-modal" style="width:min(340px,92vw);padding:24px">
        <h3>${esc(t("マスターパスワード"))}</h3>
        <div id="tw-pw-field">
          <input id="tw-pw-in" type="password" style="width:100%;background:var(--bg);color:var(--text);border:1px solid var(--border);border-radius:8px;padding:9px" autofocus>
        </div>
        <button class="btn primary" id="tw-pw-ok" style="width:100%;margin-top:12px">${esc(t("接続"))}</button>
      </div>
    </div>
  </div>`);
  document.body.appendChild(wrap);
  // Same show/hide eye affordance as the main window's master-password fields.
  wirePasswordToggles(document.getElementById("tw-pw-field"));
  wirePasswordToggles(document.getElementById("tw-lock-field"));

  // Font size: remembered per machine (localStorage), changed with the A−/A+
  // buttons, Ctrl+wheel, Ctrl+plus/minus; Ctrl+0 restores the default.
  const FONT_DEF = 13, FONT_MIN = 8, FONT_MAX = 32;
  let fontSize = FONT_DEF;
  try { fontSize = Math.min(FONT_MAX, Math.max(FONT_MIN, parseInt(localStorage.getItem("palaterm_term_font"), 10) || FONT_DEF)); } catch (e) {}
  // Scrollback: 10,000 lines like Tera Term's default (xterm.js keeps 1,000,
  // and a core switch's show running-config scrolled out of reach of Ctrl+F).
  const term = new Terminal({
    convertEol: false, cursorBlink: true, scrollback: 10000,
    fontFamily: '"Consolas", monospace', fontSize,
    theme: xtermTheme(screenColors("term")),
  });
  const fit = new (window.FitAddon.FitAddon)();
  term.loadAddon(fit);
  term.open(document.getElementById("tw-host"));
  term.focus();

  // ---- 表示の色 ----
  // The terminal colours of this PC (色 button / 設定 tab), with the device's
  // own 端末の色 over them: its tint as the background and its colour as a
  // band under the title bar, so windows side by side tell apart.
  let devTint = "";
  const twBar = document.getElementById("tw-bar"), twHost = document.getElementById("tw-host");
  function termColorsNow() {
    const c = screenColors("term"), tint = DEVICE_TINTS[devTint];
    if (tint) {
      c.bg = tint.bg;
      if (contrastRatio(c.fg, c.bg) < 4.5) c.fg = SCREEN_DEFAULTS.term.fg;
      if (contrastRatio(c.cursor, c.bg) < 2) c.cursor = SCREEN_DEFAULTS.term.cursor;
    }
    return c;
  }
  function applyTermColors() {
    const c = termColorsNow(), tint = DEVICE_TINTS[devTint];
    term.options.theme = xtermTheme(c);
    document.body.style.background = c.bg;
    wrap.style.background = c.bg;
    twHost.style.background = c.bg;
    twBar.style.borderBottom = tint ? `4px solid ${tint.band}` : "1px solid var(--border)";
  }
  applyTermColors();
  onScreenColors("term", applyTermColors);
  rt().EventsOn("term:device", ev => { devTint = (ev && ev.tint) || ""; applyTermColors(); });
  document.getElementById("tw-color").onclick = e => openScreenColorPanel("term", e.currentTarget, applyTermColors, () => term.focus());
  let closed = false;
  let locked = false; // the lock overlay is up: keys go nowhere

  // 貼り付けキー。xterm.js の既定は Ctrl+V を制御文字 ^V として機器へ送り、Shift+Insert も
  // ESC[2~ を送ってしまい、どちらもブラウザ既定の貼り付けを抑止する（Ctrl+Shift+V だけが貼り付く）。
  // Windows の Win+V（クリップボード履歴）は項目を選ぶと Ctrl+V を打鍵注入するので、同じ理由で
  // 貼り付かなかった（2026-09-13 実測）。ここで false を返すと xterm は何もせず、ブラウザ既定の
  // 貼り付けが textarea の paste イベントとして xterm に渡り、機器へ送られる。
  // 判定は e.code と e.key の両方で行う（キー注入ツールなどスキャンコード無しの入力では
  // e.code が空になるため）。
  // Alt+V は Tera Term の「貼り付け<CR>」: 確認ダイアログを開き、末尾 Enter をオンにしておく。
  term.attachCustomKeyEventHandler(e => {
    if (locked || hkOpen) return false;
    if (e.type !== "keydown" || e.metaKey) return true;
    // Alt+B = break, on a serial line only; elsewhere it stays the shell's /
    // IOS's "back one word" (Esc-B).
    if (e.altKey && !e.ctrlKey && (e.code === "KeyB" || e.key === "b" || e.key === "B") && !breakBtn.classList.contains("hidden")) { sendBreak(); return false; }
    const isV = e.code === "KeyV" || e.key === "v" || e.key === "V";
    const isIns = e.code === "Insert" || e.key === "Insert";
    if (e.altKey && !e.ctrlKey && isV) { pasteFromClipboard(true); return false; }
    if (e.altKey) return true;
    // Ctrl+Shift+F finds in the scrollback; plain Ctrl+F reaches the device
    // (^F: page down in less / vi, cursor right on IOS) as in Tera Term.
    if (e.ctrlKey && e.shiftKey && (e.code === "KeyF" || e.key === "f" || e.key === "F")) { openFind(); return false; }
    if (e.ctrlKey && (e.key === "+" || e.key === "=" || e.code === "Equal" || e.code === "NumpadAdd")) { setFont(fontSize + 1); return false; }
    if (e.ctrlKey && (e.key === "-" || e.code === "Minus" || e.code === "NumpadSubtract")) { setFont(fontSize - 1); return false; }
    if (e.ctrlKey && (e.key === "0" || e.code === "Digit0" || e.code === "Numpad0")) { setFont(FONT_DEF); return false; }
    if (e.ctrlKey && !e.shiftKey && isV) return false;
    if (e.shiftKey && !e.ctrlKey && isIns) return false;
    return true;
  });

  // ---- 貼り付けの確認（Tera Term 風）----
  // すべての貼り付け経路（Ctrl+V / Shift+Insert / Ctrl+Shift+V / Win+V の注入 / Alt+V /
  // 右クリック / ツールバー）を pasteOrConfirm に集約する（1 行はそのまま、2 行以上はダイアログ）。キーボード経路はブラウザ既定の
  // 貼り付けが xterm の textarea に paste イベントとして届くので、xterm より先（capture）で
  // 止めて本文を取り出す。Alt+V・右クリック・ボタンはクリップボードを Go 側から読む。
  // 送る内容はダイアログで編集でき、改行は xterm と同じく CR に正規化して機器へ送る。
  const pasteDlg = document.getElementById("tw-paste-dlg");
  const pasteText = document.getElementById("tw-paste-text");
  const pasteCr = document.getElementById("tw-paste-cr");
  const pasteInfo = document.getElementById("tw-paste-info");
  // Every send waits for the one before it: the backend serves each call on
  // a goroutine of its own, and two quick keys could reach the device (and,
  // with 同時入力, the other windows) the other way round. local: this
  // device only (SendReply), never relayed.
  let sendChain = Promise.resolve();
  function sendKeys(text, local) {
    const b64 = bytesToB64(new TextEncoder().encode(text));
    sendChain = sendChain.then(() => (local ? Term().SendReply(b64) : Term().Send(b64)).catch(() => {}));
  }
  function sendText(text, local) {
    if (closed || !text) return;
    if (connecting && !switchBtn.classList.contains("hidden")) { if (switchBtn.disabled) switchingHint(); else loginHint(); return; }
    sendKeys(text, local);
  }
  // The confirmation is its own OS window (Tera Term style, see
  // paste_app.go); the in-window dialog below is only the fallback if that
  // window cannot be started.
  // single: a one-line paste, confirmed only because of 同時入力 (no Enter
  // unless withCR).
  function openPasteDialog(text, withCR, single) {
    if (closed) return;
    term.clearSelection();
    Term().OpenPasteWindow(bytesToB64(new TextEncoder().encode(text)), !!withCR, !!single)
      .catch(e => { if (/既に開いています/.test(String(e))) flashNote(terr(e), 0, "err"); else openPasteDialogInline(text, withCR, single); });
  }
  rt().EventsOn("term:paste-done", () => { term.focus(); setTimeout(() => term.focus(), 150); setTimeout(() => term.focus(), 500); });
  // Whether the inline dialog's text goes to the other windows: decided
  // when it opens, as it says (the backend does the same for the window).
  let inlineRelay = false;
  function openPasteDialogInline(text, withCR, single) {
    if (closed) return;
    pasteText.value = text;
    pasteCr.checked = single ? !!withCR : (!!withCR || pasteCrPref());
    inlineRelay = bc.join;
    const n = text.split(/\r\n|\r|\n/).length;
    pasteInfo.textContent = t("{n} 行。各行の末尾は Enter として機器へ送られます。内容はここで編集できます。", { n })
      + (inlineRelay && bcPeers() > 0 ? "　" + t("同時入力中: この内容はほかの {n} 台の窓にも送られます", { n: bcPeers() }) : "");
    pasteDlg.classList.remove("hidden");
    pasteText.focus();
    pasteText.setSelectionRange(0, 0);
  }
  function closePasteDialog() { pasteDlg.classList.add("hidden"); term.focus(); }
  function confirmPaste() {
    let s = pasteText.value.replace(/\r\n|\r|\n/g, "\r");
    if (pasteCr.checked && !s.endsWith("\r")) s += "\r";
    closePasteDialog();
    sendText(s, !inlineRelay);
  }
  // One line goes straight to the device without the confirmation, as in
  // Tera Term, and without Enter: the user reads it at the prompt and decides
  // whether to run it. A line copied whole carries a trailing newline, which
  // is dropped for the same reason. Alt+V (貼り付け<CR>) asks for the Enter,
  // so it gets one. Two or more lines still open the confirmation.
  function pasteOrConfirm(text, withCR) {
    if (locked) return; // every paste path ends here
    const one = text.replace(/(\r\n|\r|\n)$/, "");
    // While 同時入力 is on, a paste is always confirmed (the window says how
    // many other windows get it), even one line by right-click. Decided by
    // this window's own switch, not the count, which may lag behind a peer
    // that has just logged in. A single line keeps its no-Enter rule.
    if (bc.join) {
      const single = !!one && !/[\r\n]/.test(one);
      openPasteDialog(single ? one : text, withCR, single);
      return;
    }
    if (one && !/[\r\n]/.test(one)) {
      term.clearSelection();
      sendText(withCR ? one + "\r" : one);
      term.focus();
      return;
    }
    openPasteDialog(text, withCR);
  }
  function pasteFromClipboard(withCR) {
    Term().GetClipboard().then(txt => { if (txt) pasteOrConfirm(txt, withCR); }).catch(() => {});
  }
  document.getElementById("tw-paste-ok").onclick = confirmPaste;
  pasteCr.addEventListener("change", () => setPasteCrPref(pasteCr.checked));
  document.getElementById("tw-paste-cancel").onclick = closePasteDialog;
  pasteText.addEventListener("keydown", e => {
    // Enter or Ctrl+Enter pastes (as the paste window); Shift+Enter inserts a new line.
    if (e.key === "Enter" && !e.shiftKey && !e.altKey && !e.isComposing && e.keyCode !== 229) { e.preventDefault(); confirmPaste(); }
    else if (e.key === "Escape") { e.preventDefault(); closePasteDialog(); }
  });
  term.textarea.addEventListener("paste", e => {
    const txt = (e.clipboardData || window.clipboardData || {}).getData ? (e.clipboardData || window.clipboardData).getData("text") : "";
    e.preventDefault(); e.stopImmediatePropagation();
    if (txt) pasteOrConfirm(txt, false);
  }, true);
  // Right-click follows the 設定 tab, as Tera Term's 2 options: paste at
  // once (default), a menu whose 貼り付け pastes, or nothing.
  document.getElementById("tw-host").addEventListener("contextmenu", e => {
    e.preventDefault();
    if (locked) return;
    Term().RightClickPaste().catch(() => "paste").then(mode => {
      if (mode === "menu") showCtxMenu([[t("貼り付け"), () => pasteFromClipboard(false)]], e);
      else if (mode !== "off") pasteFromClipboard(false);
    });
  });
  document.getElementById("tw-paste").onclick = () => pasteFromClipboard(false);
  document.getElementById("tw-logdir").onclick = () => {
    Term().OpenLogDir().catch(e => flashNote(t("フォルダを開けません") + ": " + terr(e), 0, "err"));
    term.focus();
  };
  document.getElementById("tw-logview").onclick = () => {
    Term().OpenLogWindow().catch(e => flashNote(t("ログを開けません") + ": " + terr(e), 0, "err"));
    term.focus();
  };

  // ---- フォントサイズ ----
  function setFont(n) {
    n = Math.min(FONT_MAX, Math.max(FONT_MIN, n));
    if (n === fontSize) return;
    fontSize = n;
    term.options.fontSize = n;
    try { localStorage.setItem("palaterm_term_font", String(n)); } catch (e) {}
    doFit();
    term.focus();
  }
  document.getElementById("tw-font-inc").onclick = () => setFont(fontSize + 1);
  document.getElementById("tw-font-dec").onclick = () => setFont(fontSize - 1);
  document.getElementById("tw-host").addEventListener("wheel", e => {
    if (!e.ctrlKey) return;
    e.preventDefault();
    setFont(fontSize + (e.deltaY < 0 ? 1 : -1));
  }, { passive: false });

  // ---- 検索（画面バッファ内・大文字小文字を区別しない）----
  // Walks the whole scrollback, selects the match and scrolls it into view.
  // The selection is made by code, so the auto-copy below must not fire.
  const findBar = document.getElementById("tw-findbar");
  const findIn = document.getElementById("tw-find-in");
  const findN = document.getElementById("tw-find-n");
  let matches = [], matchIdx = -1, lastQuery = "", suppressCopy = false;
  function collectMatches(q) {
    const res = [];
    if (!q) return res;
    const buf = term.buffer.active, lq = q.toLowerCase();
    for (let y = 0; y < buf.length; y++) {
      const line = buf.getLine(y);
      if (!line) continue;
      const str = line.translateToString(true), ls = str.toLowerCase();
      let i = 0;
      while ((i = ls.indexOf(lq, i)) >= 0) { res.push({ y, x: i }); i += lq.length; }
    }
    return res;
  }
  function showMatch() {
    if (!matches.length) { findN.textContent = lastQuery ? t("該当なし") : ""; return; }
    const m = matches[matchIdx];
    suppressCopy = true;
    term.select(m.x, m.y, lastQuery.length);
    term.scrollToLine(Math.max(0, m.y - Math.floor(term.rows / 2)));
    setTimeout(() => { suppressCopy = false; }, 50);
    findN.textContent = `${matchIdx + 1} / ${matches.length}`;
  }
  function findNext(dir) {
    const q = findIn.value;
    if (q !== lastQuery) { lastQuery = q; matches = collectMatches(q); matchIdx = dir >= 0 ? -1 : 0; }
    if (!matches.length) { showMatch(); return; }
    matchIdx = (matchIdx + dir + matches.length) % matches.length;
    showMatch();
  }
  function openFind() {
    findBar.classList.remove("hidden");
    const sel = term.getSelection();
    if (sel && !sel.includes("\n")) findIn.value = sel;
    findIn.focus(); findIn.select();
    doFit();
  }
  function closeFind() {
    findBar.classList.add("hidden");
    term.clearSelection();
    doFit();
    term.focus();
  }
  document.getElementById("tw-find").onclick = openFind;
  document.getElementById("tw-find-next").onclick = () => findNext(1);
  document.getElementById("tw-find-prev").onclick = () => findNext(-1);
  document.getElementById("tw-find-close").onclick = closeFind;
  findIn.addEventListener("keydown", e => {
    if (e.key === "Enter") { e.preventDefault(); findNext(e.shiftKey ? -1 : 1); }
    else if (e.key === "Escape") { e.preventDefault(); closeFind(); }
  });
  findIn.addEventListener("input", () => { lastQuery = ""; findN.textContent = ""; });

  // ---- ログ停止 / ログ開始 ----
  // Recording is on from the connection; the one button toggles it. While
  // stopped it reads 「ログ開始」 and the state pill shows that nothing is
  // being recorded.
  let logging = true;
  const logBtn = document.getElementById("tw-log");
  function renderLogBtn() {
    logBtn.textContent = logging ? t("ログ停止") : t("ログ開始");
    logBtn.classList.toggle("danger", !logging);
  }
  logBtn.onclick = () => {
    if (closed) return;
    const next = !logging;
    Term().SetLogging(next).then(p => {
      logging = next;
      renderLogBtn();
      flashNote(logging ? t("ログを再開しました") : (p ? t("ログを停止しました: {f}", { f: p.split(/[\\/]/).pop() }) : t("ログを停止しました")));
    }).catch(e => flashNote(terr(e), 0, "err"));
    term.focus();
  };

  // Tera Term 風の「選択で自動コピー」: マウスで選択した範囲を、選択が確定した時点で
  // クリップボードへ入れる（ドラッグ中は連続で発火するので少し待ってから1回）。
  // 書き込みは Go 側（Wails runtime）経由。WebView2 内の navigator.clipboard は
  // フォーカスや権限の条件で失敗することがあるため、そちらは保険のフォールバック。
  // マウスを離した瞬間にも一度コピーする（ドラッグ終了＝確定）。同じ内容の二重コピーは抑止し、
  // 入ったことが分かるようにバーに「コピーしました」を短く出す。
  let selTimer = null, lastCopied = "", copiedTimer = null;
  const copiedEl = document.getElementById("tw-copied");
  // kind "err": red, and long enough to be read.
  function flashNote(text, ms, kind) {
    copiedEl.textContent = text;
    copiedEl.className = "pill " + (kind || "ok");
    clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => copiedEl.classList.add("hidden"), ms || (kind === "err" ? 6000 : 1800));
  }
  function copySelection() {
    if (suppressCopy) return;
    const s = term.getSelection();
    if (!s || s === lastCopied) return;
    lastCopied = s;
    const flash = () => flashNote(t("コピーしました"), 1200);
    Term().SetClipboard(s).then(flash).catch(() => {
      try { navigator.clipboard.writeText(s).then(flash).catch(() => {}); } catch (e) {}
    });
  }
  term.onSelectionChange(() => {
    clearTimeout(selTimer);
    if (suppressCopy) return;
    if (!term.hasSelection()) { lastCopied = ""; return; }
    selTimer = setTimeout(copySelection, 150);
  });
  document.getElementById("tw-host").addEventListener("mouseup", () => setTimeout(copySelection, 0));

  // Fit the terminal to the window, then make sure the last row really is
  // inside it. FitAddon's row count can come out one row too many (measured:
  // the last row ran 1–5px past the window bottom, eating the padding — and
  // worse at 125% display scaling), which cut the prompt line in half at the
  // bottom of the window (2026-10-11, field report). Drop rows until the
  // screen ends above the host's bottom padding.
  function doFit() {
    try {
      fit.fit();
      const host = document.getElementById("tw-host");
      const screen = host.querySelector(".xterm-screen");
      if (screen) {
        const limit = host.getBoundingClientRect().bottom - (parseFloat(getComputedStyle(host).paddingBottom) || 0);
        for (let i = 0; i < 4 && term.rows > 2 && screen.getBoundingClientRect().bottom > limit + 0.5; i++) {
          term.resize(term.cols, term.rows - 1);
        }
      }
      Term().Resize(term.cols, term.rows).catch(() => {});
    } catch (e) {}
  }
  setTimeout(doFit, 0);
  new ResizeObserver(() => doFit()).observe(document.getElementById("tw-host"));

  const stateEl = document.getElementById("tw-state");
  const titleEl = document.getElementById("tw-title");

  rt().EventsOn("term:data", ev => { term.write(b64ToBytes(ev.data)); });
  // Connection progress: "接続中… (n/m秒)" until ready or closed.
  let connecting = true;
  rt().EventsOn("term:progress", ev => {
    if (!connecting) return;
    // Past the bound only the elapsed time is honest to show.
    stateEl.textContent = ev.elapsed > ev.total
      ? t("接続中… ({a}秒)", { a: ev.elapsed })
      : t("接続中… ({a}/{b}秒)", { a: ev.elapsed, b: ev.total });
  });
  // While the automatic login runs, the device's screen is visible only to
  // the login logic — the user sees "接続中…". If they already know the
  // device is at a screen no profile will get past (initial setup dialog,
  // forced password change), this button stops the login right away and
  // hands them the line, instead of waiting out the timeout.
  const switchBtn = document.getElementById("tw-switch");
  switchBtn.onclick = () => {
    switchBtn.disabled = true;
    term.write("\x1b[33m" + t("[手動に切り替えました。自動ログインは打ち切り、以後の入力はそのまま機器へ送られます]") + "\x1b[0m\r\n");
    Term().SwitchToManual().catch(e => { switchBtn.disabled = false; term.write("\r\n\x1b[31m" + terr(e) + "\x1b[0m\r\n"); });
  };
  function showSwitch(on) { switchBtn.classList.toggle("hidden", !on); switchBtn.disabled = false; }
  rt().EventsOn("term:ready", ev => {
    connecting = false;
    setHkAsk(false);
    closed = false;
    showSwitch(false);
    takeoverBar.classList.add("hidden"); doFit();
    stateEl.textContent = ev.manual ? t("接続済み（手動）") : t("接続済み"); stateEl.className = "pill ok";
    reconnectBtn.classList.add("hidden");
    breakBtn.classList.toggle("hidden", ev.conn !== "serial");
    titleEl.textContent = `${ev.device}  ${ev.host} / ${ev.conn}` + (ev.hops ? `  ${t("踏み台 {n} 段経由", { n: ev.hops })}` : "") + (ev.manual ? `  ${t("（自動ログインなし）")}` : "");
    setOSTitle(ev.device, ev.host, "");
    // A console opened by hand shows nothing until the device answers; say
    // which port this is and what to do, so a wrong COM port or baud rate
    // is the first thing checked rather than the last.
    if (ev.manual && ev.conn === "serial") {
      term.write("\x1b[33m" + t("[{p} を開きました。何も表示されなければ Enter を押して機器の応答を確認してください。応答が無ければ COM ポート・ボーレート・結線を確認]", { p: ev.host }) + "\x1b[0m\r\n");
    }
    term.focus(); doFit();
    // The OS window may still be behind the main window: ask for the
    // foreground once more now that there is something to type into.
    Term().Focus().catch(() => {});
    [150, 500].forEach(ms => setTimeout(() => term.focus(), ms));
  });
  // A changed host key is answered in the window: allow (device replaced)
  // and reconnect, or leave it refused.
  // Like the 接続中です question: キャンセル (also Esc) has the focus, and
  // everything behind is inert so keys never reach the terminal meanwhile.
  const hkDlg = document.getElementById("tw-hk");
  let hkOpen = false;
  function showHk(msg) {
    if (msg !== undefined) document.getElementById("tw-hk-msg").textContent = msg;
    hkOpen = true;
    hkDlg.classList.remove("hidden");
    if (locked) return; // the lock screen decides what is inert; shown again on unlock
    [...wrap.children].forEach(el => { el.inert = el !== hkDlg; });
    setTimeout(() => document.getElementById("tw-hk-cancel").focus(), 30);
  }
  function hideHk() {
    hkOpen = false;
    hkDlg.classList.add("hidden");
    if (locked) { setInert(true); return; }
    [...wrap.children].forEach(el => { el.inert = false; });
    term.focus();
  }
  // Closing the question (キャンセル / Esc) leaves the window able to ask it
  // again: the 再接続 button turns into 再接続（ホストキーを確認）, and it or
  // Enter brings the question back rather than retrying into the same refusal.
  let hkAsk = false;
  function setHkAsk(on) {
    hkAsk = on;
    reconnectBtn.textContent = on ? t("再接続（ホストキーを確認）") : t("再接続");
    reconnectBtn.dataset.tip = on
      ? t("ホストキーの確認をもう一度表示します（Enter キーでも表示できます）")
      : t("同じ機器にもう一度接続します（切断後に Enter キーでも再接続できます）");
  }
  function dismissHk() {
    hideHk();
    setHkAsk(true);
    reconnectBtn.classList.remove("hidden");
    term.write("\x1b[90m" + t("[Enter または「再接続（ホストキーを確認）」でホストキーの確認をもう一度表示します]") + "\x1b[0m\r\n");
  }
  document.getElementById("tw-hk-cancel").onclick = dismissHk;
  hkDlg.addEventListener("keydown", e => { if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); dismissHk(); } });
  document.getElementById("tw-hk-ok").onclick = () => {
    hideHk();
    setHkAsk(false);
    reconnectBtn.classList.add("hidden");
    closed = false; connecting = true; typedHint = false; switchHint = false;
    stateEl.textContent = t("接続中…"); stateEl.className = "pill run";
    showSwitch(!res.manual);
    term.write("\r\n\x1b[33m" + t("[ホストキーを更新して再接続します]") + "\x1b[0m\r\n");
    Term().AllowHostKeyChange().catch(e => { closed = true; term.write("\r\n\x1b[31m" + terr(e) + "\x1b[0m\r\n"); });
  };
  // A failed automatic login whose line is still open (ev.takeover): the
  // device is at a screen the profile could not get past, and the user can
  // read it now. Offer the keyboard instead of hanging up.
  const takeoverBar = document.getElementById("tw-takeover");
  let takeoverAtJump = false; // the held line is on a jump host (ev.atJump)
  document.getElementById("tw-takeover-ok").onclick = () => {
    takeoverBar.classList.add("hidden"); doFit();
    term.write("\x1b[33m" + (takeoverAtJump
      ? t("[手動操作に切り替えました。以後の入力はそのまま踏み台へ送られます]")
      : t("[手動操作に切り替えました。以後の入力はそのまま機器へ送られます]")) + "\x1b[0m\r\n");
    Term().Takeover().then(() => term.focus()).catch(e => { term.write("\r\n\x1b[31m" + terr(e) + "\x1b[0m\r\n"); });
  };
  document.getElementById("tw-takeover-close").onclick = () => {
    takeoverBar.classList.add("hidden"); doFit();
    Term().Discard().catch(() => {});
  };
  // The OS window title (the taskbar's label): eight windows side by side
  // tell a disconnected one from a live one by it.
  let osTitleDev = "", osTitleHost = "";
  function setOSTitle(dev, host, state) {
    if (dev) { osTitleDev = dev; osTitleHost = host || ""; }
    const base = "PalaTerm - " + osTitleDev + (osTitleHost ? " - " + osTitleHost : "");
    try { rt().WindowSetTitle(base + (state ? " - " + state : "")); } catch (e) {}
  }
  rt().EventsOn("term:closed", ev => {
    closed = true;
    setHkAsk(false);
    connecting = false;
    showSwitch(false);
    breakBtn.classList.add("hidden");
    // A failed jump leaves the line on the jump host's shell, not the
    // device's screen: say so, or it passes for the device.
    const failState = ev.takeover ? (ev.atJump ? t("踏み台で停止") : t("自動ログイン失敗")) : t("接続失敗");
    stateEl.textContent = ev.error ? failState : t("切断");
    stateEl.className = "pill err";
    setOSTitle("", "", ev.error ? failState : t("切断"));
    if (ev.error && /ホストキーが前回接続時と異なります/.test(ev.error)) {
      showHk(terr(ev.error));
    }
    if (ev.error) term.write("\r\n\x1b[31m" + t("[接続エラー] ") + terr(ev.error) + "\x1b[0m\r\n");
    else term.write("\r\n\x1b[33m" + t("[切断されました]") + "\x1b[0m\r\n");
    if (ev.logPath) term.write("\x1b[36m" + t("[ログ保存] ") + ev.logPath + "\x1b[0m\r\n");
    // A session that ended (not a takeover offer, not a host-key question)
    // can be opened again from here.
    if (!ev.takeover && !(ev.error && /ホストキーが前回接続時と異なります/.test(ev.error)) && !/マスターパスワード/.test(ev.error || "")) {
      reconnectBtn.classList.remove("hidden");
      term.write("\x1b[90m" + t("[Enter または「再接続」でもう一度接続します]") + "\x1b[0m\r\n");
    }
    if (ev.takeover) {
      takeoverAtJump = !!ev.atJump;
      document.getElementById("tw-takeover-msg").textContent = ev.atJump
        ? t("機器へは入れず、踏み台のシェルに戻っています。ここから手で機器へつなぐこともできます。")
        : t("自動ログインに失敗しましたが、回線はつながったままです。機器が出している画面に合わせて、ここから手で操作できます。");
      term.write("\x1b[33m" + (ev.atJump
        ? t("[回線は踏み台につながったままです。上の「手動で続ける」で踏み台から操作できます]")
        : t("[回線はつながったままです。上の「手動で続ける」でこのまま操作できます]")) + "\x1b[0m\r\n");
      takeoverBar.classList.remove("hidden"); doFit();
    }
  });

  // ---- 再接続 / ブレーク / ロック ----
  const reconnectBtn = document.getElementById("tw-reconnect");
  const breakBtn = document.getElementById("tw-break");
  let reconnecting = false;
  function canReconnect() { return !reconnectBtn.classList.contains("hidden") && !reconnecting; }
  function reconnect() {
    if (hkAsk) { showHk(); return; } // the host-key question again (see dismissHk)
    reconnecting = true;
    reconnectBtn.classList.add("hidden");
    closed = false; connecting = true; typedHint = false; switchHint = false;
    stateEl.textContent = t("接続中…"); stateEl.className = "pill run";
    showSwitch(!res.manual);
    term.write("\r\n\x1b[33m" + t("[再接続します]") + "\x1b[0m\r\n");
    Term().Reconnect().catch(e => { closed = true; term.write("\r\n\x1b[31m" + terr(e) + "\x1b[0m\r\n"); })
      .finally(() => { reconnecting = false; });
  }
  reconnectBtn.onclick = () => { reconnect(); term.focus(); };
  function sendBreak() {
    if (breakBtn.classList.contains("hidden")) return;
    Term().SendBreak().then(() => flashNote(t("ブレーク信号を送りました"))).catch(e => flashNote(terr(e), 0, "err"));
    term.focus();
  }
  breakBtn.onclick = sendBreak;
  // Lock: the main window's ロック (term:lock), or this window's own idle time
  // (the vault's 自動ロック setting; keyboard and mouse here count, output
  // from the device does not). The session stays up underneath.
  const lockEl = document.getElementById("tw-lock"), lockIn = document.getElementById("tw-lock-in");
  // Everything but the lock screen is inert while locked: Tab could otherwise
  // reach the toolbar (貼り付け…, ログ停止) or the terminal's input behind it.
  const setInert = on => [...wrap.children].forEach(el => { if (el !== lockEl) el.inert = on; });
  function showLock() {
    closeScreenColorPanel(); // nothing but the lock screen stays usable
    locked = true;
    setInert(true);
    lockEl.classList.remove("hidden");
    document.getElementById("tw-lock-err").textContent = "";
    lockIn.value = ""; setTimeout(() => lockIn.focus(), 50);
  }
  let unlocking = false;
  async function tryUnlock() {
    if (unlocking) return; // one attempt at a time (a wrong one is answered after a delay)
    unlocking = true;
    const okBtn = document.getElementById("tw-lock-ok");
    okBtn.disabled = true;
    try { await doUnlock(); } finally { unlocking = false; okBtn.disabled = false; }
  }
  async function doUnlock() {
    if (await Term().Unlock(lockIn.value)) {
      locked = false; setInert(false); lockEl.classList.add("hidden"); lastInput = Date.now();
      if (hkOpen) showHk(); else term.focus();
    }
    else { document.getElementById("tw-lock-err").textContent = t("マスターパスワードが違います"); lockIn.select(); }
  }
  document.getElementById("tw-lock-ok").onclick = tryUnlock;
  lockIn.addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); tryUnlock(); } });
  rt().EventsOn("term:lock", showLock);
  // × while connected: the backend holds the close and asks here, in the
  // window's own style. キャンセル (also Esc) is where the focus starts, so a
  // stray Enter keeps the session.
  const closeDlg = document.getElementById("tw-close");
  // While the question shows, everything behind it is inert: the window
  // coming to the front on × gave the terminal the focus back, and keys
  // typed then went to the device instead of the dialog.
  let inertBefore = null;
  const hideCloseDlg = () => {
    closeDlg.classList.add("hidden");
    closeDlg.style.zIndex = "35";
    if (inertBefore) { [...wrap.children].forEach((el, i) => { el.inert = inertBefore[i]; }); inertBefore = null; }
    // Locked meanwhile (or before): the lock decides what stays inert.
    if (locked) { setInert(true); lockIn.focus(); } else if (hkOpen) document.getElementById("tw-hk-cancel").focus(); else term.focus();
  };
  rt().EventsOn("term:close-request", () => {
    // Closing sends nothing to the device, so a locked window may be closed
    // too: the question then shows above the lock screen.
    if (locked) closeDlg.style.zIndex = "45";
    if (!inertBefore) {
      inertBefore = [...wrap.children].map(el => el.inert);
      [...wrap.children].forEach(el => { el.inert = el !== closeDlg; });
    }
    closeDlg.inert = false;
    closeDlg.classList.remove("hidden");
    setTimeout(() => document.getElementById("tw-close-cancel").focus(), 30);
  });
  document.getElementById("tw-close-cancel").onclick = hideCloseDlg;
  document.getElementById("tw-close-ok").onclick = () => {
    document.getElementById("tw-close-ok").disabled = true;
    Term().ConfirmClose().catch(() => {});
  };
  closeDlg.addEventListener("keydown", e => { if (e.key === "Escape") { e.preventDefault(); hideCloseDlg(); } });
  Term().IsLocked().then(v => { if (v) showLock(); }).catch(() => {});
  let lastInput = Date.now(), lockWarned = false;
  // Keys arriving from another window (同時入力) are use of this window too:
  // otherwise every window but the one typed in would lock on its own idle
  // time and drop out of 同時入力 in the middle of the work.
  rt().EventsOn("term:remote-input", () => { if (!locked) { lastInput = Date.now(); lockWarned = false; wrap.dataset.remoteInput = String(lastInput); } });
  ["keydown", "mousedown", "mousemove", "wheel"].forEach(ev => document.addEventListener(ev, () => { if (!locked) { lastInput = Date.now(); lockWarned = false; } }, { passive: true }));
  setInterval(async () => {
    if (locked) return;
    const min = await Term().AutoLockMinutes().catch(() => 0);
    if (!(min > 0)) return;
    const idle = Date.now() - lastInput;
    // A minute's notice (someone watching debug output has not typed, but
    // is here): any key or mouse move cancels it.
    if (idle >= min * 60000 - 60000 && idle < min * 60000 && !lockWarned) { lockWarned = true; flashNote(t("あと 1 分ほどでロックします（キーかマウスを動かすと延長）"), 8000); }
    if (idle >= min * 60000) { await Term().Lock().catch(() => {}); showLock(); }
  }, 15000);
  try { Term().SetLang(LANG); } catch (e) {}

  // Keys typed (or text pasted) while the automatic login runs go nowhere:
  // the line is the login's until it is done. Said once per connection,
  // with the way to take over — unless 手動に切り替え was already pressed
  // (the line is being handed over; nothing to press any more).
  let typedHint = false, switchHint = false;
  // 手動に切り替え pressed, the line not yet handed over: nothing to press.
  function switchingHint() {
    if (switchHint) return;
    switchHint = true;
    term.write("\r\n\x1b[33m" + t("[手動に切り替え中です。接続が渡るまで少しお待ちください]") + "\x1b[0m\r\n");
  }
  function loginHint() {
    if (typedHint) return;
    typedHint = true;
    term.write("\r\n\x1b[33m" + t("[自動ログイン中のため、キー入力は機器に送られません。手で操作するときは上の「手動に切り替え」を押してください]") + "\x1b[0m\r\n");
  }
  // xterm.js answers a terminal query the device sends (ESC[c, ESC[6n,
  // ESC[5n …) through onData too; that is no typing of the user's. Only
  // those reply shapes: Esc, the arrows and F-keys start with ESC as well
  // and are keys.
  // Replies: DA (ESC[?…c / ESC[>…c), CPR (ESC[r;cR, ESC[?r;cR), DSR (ESC[0n),
  // DECRPM (ESC[…$y), focus in/out (ESC[I / ESC[O), window ops (ESC[…t),
  // OSC colour replies (ESC]…), DCS replies (ESC P … ESC \). A key with a
  // modifier can look like a CPR (Shift+F3 = ESC[1;2R): a reply's row is at
  // least 1 and a key's first number is 1 with a modifier 2–8 — told apart
  // by xterm.js only for "1;N" with N ≤ 8, which no terminal query answers.
  // Mouse reports: X10 / normal (ESC[M + 3 bytes), SGR (ESC[<b;x;yM / m),
  // urxvt (ESC[b;x;yM).
  const isMouseReport = d => /^(\x1b\[M[\s\S]{3}|\x1b\[<\d+;\d+;\d+[Mm]|\x1b\[\d+;\d+;\d+M)+$/.test(d);
  const isTermReply = d => /^\x1b(\[(\?[\d;]*c|>[\d;]*c|\??\d+;\d+R|0n|\??[\d;]*\$y|[IO]|[\d;]*t)|\][^\x07]*(\x07|\x1b\\)|P[^\x1b]*\x1b\\)$/.test(d)
    && !/^\x1b\[1;[2-8]R$/.test(d);
  term.onData(d => {
    if (locked || hkOpen) return;
    // After a disconnect, Enter opens the session again (the 再接続 button).
    if (closed) { if (d === "\r" && canReconnect()) reconnect(); return; }
    if (connecting && !switchBtn.classList.contains("hidden")) {
      if (isTermReply(d)) return;
      if (switchBtn.disabled) switchingHint(); else loginHint();
      return;
    }
    // A reply to the device's own query is this device's alone: never
    // relayed to the other windows by 同時入力.
    // A reply to the device's own query, or a mouse report (vim / htop with
    // mouse on), is this device's alone: never relayed by 同時入力.
    if (isTermReply(d) || isMouseReport(d)) { sendKeys(d, true); return; }
    sendKeys(d, false);
  });
  // Close drops the live session; Discard drops a line held for takeover.
  window.addEventListener("beforeunload", () => { if (!closed) Term().Close(); else Term().Discard().catch(() => {}); });

  // ---- 同時入力 / 並べ直す（broadcast.go・term_bcast.go）----
  // On: keys typed here go to every window that has it on too, and those
  // windows' keys come here. Shown in orange around the whole window, with
  // how many windows take part, so it is never on unnoticed.
  const bcBtn = document.getElementById("tw-bcast"), bcPill = document.getElementById("tw-bc-pill");
  const bcFrame = h(`<div id="tw-bc-frame" class="hidden" style="position:absolute;inset:0;border:3px solid #f97316;pointer-events:none;z-index:25"></div>`);
  wrap.appendChild(bcFrame);
  let bc = { join: false, ready: false, joined: 0, readyN: 0 };
  function renderBcast() {
    bcBtn.classList.toggle("bc-on", bc.join);
    bcBtn.textContent = bc.join ? t("同時入力 オン") : t("同時入力");
    bcFrame.classList.toggle("hidden", !bc.join);
    bcPill.classList.toggle("hidden", !bc.join);
    if (bc.join) {
      bcPill.textContent = bc.joined > 1 ? t("同時入力中（{r} / {n} 台）", { r: bc.readyN, n: bc.joined }) : t("同時入力: 相手の窓なし");
      bcPill.dataset.tip = t("同時入力がオンの窓 {n} 台のうち、いまキーを受け取れる窓が {r} 台です（ログイン中・切断中・ロック中の窓は受け取りません）", { n: bc.joined, r: bc.readyN });
    }
  }
  rt().EventsOn("term:bcast", ev => { bc = ev || bc; renderBcast(); });
  // How many other windows get what is typed here now.
  function bcPeers() { return bc.join ? Math.max(0, (bc.readyN || 0) - (bc.ready ? 1 : 0)) : 0; }
  Term().BroadcastState().then(ev => { bc = ev || bc; renderBcast(); }).catch(() => {});
  bcBtn.onclick = () => {
    Term().SetBroadcast(!bc.join).then(ev => {
      bc = ev || bc; renderBcast();
      flashNote(bc.join ? t("同時入力をオンにしました") : t("同時入力をオフにしました"));
    }).catch(e => flashNote(terr(e), 0, "err"));
    term.focus();
  };
  document.getElementById("tw-tile").onclick = () => { Term().Tile().catch(e => flashNote(terr(e), 0, "err")); term.focus(); };

  // Kick off: use the stdin password if present, else prompt in-window.
  const res = await Term().Start();
  if (titleEl.textContent === t("対話接続")) titleEl.textContent = res.name || t("対話接続");
  // The OS title keeps the device's name even when the first connection
  // fails before term:ready has named it.
  if (!osTitleDev && res.name) osTitleDev = res.name;
  // Only an automatic login can be switched away from.
  showSwitch(!res.manual);
  if (res.needPassword) {
    const pwOverlay = document.getElementById("tw-pw");
    pwOverlay.classList.remove("hidden");
    const submit = () => {
      const pw = document.getElementById("tw-pw-in").value;
      if (!pw) return;
      pwOverlay.classList.add("hidden");
      Term().StartWithPassword(pw);
    };
    document.getElementById("tw-pw-ok").onclick = submit;
    document.getElementById("tw-pw-in").addEventListener("keydown", e => { if (e.key === "Enter") submit(); });
    document.getElementById("tw-pw-in").focus();
  }
}
