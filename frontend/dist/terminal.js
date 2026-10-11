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
  document.body.style.background = "#1a212b";
  const P = () => window.go.main.Paste;
  const req = await P().Load();
  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;padding:14px 18px;background:#1a212b;font-family:'Segoe UI',sans-serif">
    <div style="display:flex;align-items:center;gap:12px;margin-bottom:6px">
      <div style="color:#e6ecf3;font-size:15px;font-weight:600">${esc(t("貼り付けの確認"))}</div>
      <div class="muted" style="font-size:12px;flex:1">${esc(req.device || "")}</div>
      <button class="btn sm" id="pw-font-dec" title="Ctrl+-">A−</button>
      <button class="btn sm" id="pw-font-inc" title="Ctrl++">A＋</button>
    </div>
    <div id="pw-info" class="muted" style="font-size:12px;margin-bottom:8px"></div>
    <textarea id="pw-text" spellcheck="false" style="flex:1;min-height:120px;width:100%;resize:none;background:#0b0e13;color:#d6dee8;border:1px solid #2a3442;border-radius:8px;padding:8px;font-family:Consolas,monospace;font-size:13px;white-space:pre;overflow:auto"></textarea>
    <label style="display:flex;align-items:center;gap:6px;color:#c7d0dc;font-size:12px;margin:8px 0 12px"><input type="checkbox" id="pw-cr" style="width:auto"> ${esc(t("最後に Enter を送る"))}</label>
    <div style="display:flex;justify-content:flex-end;gap:8px">
      <button class="btn" id="pw-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="pw-ok">${esc(t("貼り付け"))}</button>
    </div>
    <div class="muted" style="font-size:11px;margin-top:8px">${esc(t("Enter = 貼り付け ／ Shift+Enter = 改行を挿入 ／ Esc = キャンセル"))}</div>
  </div>`);
  document.body.appendChild(wrap);
  const ta = document.getElementById("pw-text"), cr = document.getElementById("pw-cr");
  ta.value = req.text || "";
  cr.checked = !!req.cr;
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
    if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.altKey) { e.preventDefault(); submit(); }
  });
  const grab = () => { ta.focus(); ta.setSelectionRange(0, 0); };
  grab();
  window.addEventListener("focus", grab);
  // The OS window may become active a beat after the page is ready; keep
  // asking for the foreground and refocusing until it sticks.
  [100, 400, 900].forEach(ms => setTimeout(() => { if (document.activeElement !== ta) grab(); }, ms));
  P().Focus().catch(() => {});
}

async function bootTerminalWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "#000";

  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:#000">
    <div id="tw-bar" style="display:flex;align-items:center;justify-content:space-between;padding:6px 12px;background:#11151c;border-bottom:1px solid #2a3442;font-family:'Segoe UI',sans-serif">
      <div id="tw-title" style="color:#e6ecf3;font-size:13px">${esc(t("対話接続"))}</div>
      <div style="display:flex;align-items:center;gap:8px">
        <span id="tw-copied" class="pill ok hidden">${esc(t("コピーしました"))}</span>
        <button class="btn sm" id="tw-font-dec" title="Ctrl+-">A−</button>
        <button class="btn sm" id="tw-font-inc" title="Ctrl++">A＋</button>
        <button class="btn sm" id="tw-find" title="Ctrl+F">${esc(t("検索"))}</button>
        <button class="btn sm" id="tw-paste" title="Alt+V">${esc(t("貼り付け…"))}</button>
        <button class="btn sm" id="tw-log" data-tip="${esc(t("接続した時点からログをファイルへ書いています（Tera Term と同じリアルタイム記録）。停止すると以後の内容は記録されず、再開すると同じファイルに続きが書かれます"))}">${esc(t("ログ停止"))}</button>
        <button class="btn sm" id="tw-logview" data-tip="${esc(t("このセッションのログを別ウィンドウで開きます（追記を自動で追います）"))}">${esc(t("ログ表示"))}</button>
        <button class="btn sm" id="tw-logdir">${esc(t("ログフォルダ"))}</button>
        <button class="btn sm hidden" id="tw-switch" style="color:#fcd34d;border-color:#b45309" data-tip="${esc(t("自動ログインをここで打ち切り、今の画面のまま手で操作します（機器が想定外の画面で止まっているときに）"))}">${esc(t("手動に切り替え"))}</button>
        <span id="tw-state" class="pill run">${esc(t("接続中…"))}</span>
      </div>
    </div>
    <div id="tw-takeover" class="hidden" style="display:flex;align-items:center;gap:10px;padding:6px 12px;background:#2a2412;border-bottom:1px solid #b45309;font-family:'Segoe UI',sans-serif;font-size:12px;color:#fcd34d">
      <span style="flex:1">${esc(t("自動ログインに失敗しましたが、回線はつながったままです。機器が出している画面に合わせて、ここから手で操作できます。"))}</span>
      <button class="btn sm" id="tw-takeover-ok" style="color:#fcd34d;border-color:#b45309">${esc(t("手動で続ける"))}</button>
      <button class="btn sm" id="tw-takeover-close">${esc(t("切断する"))}</button>
    </div>
    <div id="tw-findbar" class="hidden" style="display:flex;align-items:center;gap:6px;padding:4px 12px;background:#161c25;border-bottom:1px solid #2a3442;font-family:'Segoe UI',sans-serif;font-size:12px">
      <input id="tw-find-in" placeholder="${esc(t("検索（Enter=次 / Shift+Enter=前 / Esc=閉じる）"))}" style="flex:1;max-width:420px;background:#0b0e13;color:#e6ecf3;border:1px solid #2a3442;border-radius:6px;padding:4px 8px;font-size:12px">
      <span id="tw-find-n" class="muted" style="min-width:70px"></span>
      <button class="btn sm" id="tw-find-prev">▲</button>
      <button class="btn sm" id="tw-find-next">▼</button>
      <button class="btn sm" id="tw-find-close">✕</button>
    </div>
    <div id="tw-host" style="flex:1;min-height:0;background:#000;padding:6px"></div>
    <div id="tw-paste-dlg" class="hidden" style="position:absolute;inset:0;z-index:30;display:flex;align-items:center;justify-content:center;background:rgba(0,0,0,.6)">
      <div style="display:flex;flex-direction:column;width:calc(100vw - 32px);height:calc(100vh - 80px);background:#1a212b;border:1px solid #2a3442;border-radius:12px;padding:16px 22px;font-family:'Segoe UI',sans-serif">
        <div style="color:#e6ecf3;font-size:14px;font-weight:600;margin-bottom:6px">${esc(t("貼り付けの確認"))} <span class="muted" style="font-size:11px;font-weight:400">${esc(t("（ウィンドウを広げると編集欄も広がります）"))}</span></div>
        <div id="tw-paste-info" class="muted" style="font-size:12px;margin-bottom:8px"></div>
        <textarea id="tw-paste-text" spellcheck="false" style="flex:1;min-height:120px;width:100%;resize:none;background:#0b0e13;color:#d6dee8;border:1px solid #2a3442;border-radius:8px;padding:8px;font-family:Consolas,monospace;font-size:13px;white-space:pre;overflow:auto"></textarea>
        <label style="display:flex;align-items:center;gap:6px;color:#c7d0dc;font-size:12px;margin:8px 0 12px"><input type="checkbox" id="tw-paste-cr" style="width:auto"> ${esc(t("最後に Enter を送る"))}</label>
        <div style="display:flex;justify-content:flex-end;gap:8px">
          <button class="btn" id="tw-paste-cancel">${esc(t("キャンセル"))}</button>
          <button class="btn primary" id="tw-paste-ok">${esc(t("貼り付け"))}</button>
        </div>
        <div class="muted" style="font-size:11px;margin-top:8px">${esc(t("Enter = 貼り付け ／ Shift+Enter = 改行を挿入 ／ Esc = キャンセル"))}</div>
      </div>
    </div>
    <div id="tw-hk" class="hidden" style="position:absolute;inset:0;z-index:30;display:flex;align-items:center;justify-content:center;background:rgba(0,0,0,.7)">
      <div style="width:min(620px,92vw);background:#1a212b;border:1px solid #b45309;border-radius:12px;padding:20px 22px;font-family:'Segoe UI',sans-serif">
        <div style="color:#fcd34d;font-size:15px;font-weight:600;margin-bottom:8px">${esc(t("ホストキーが前回と異なります"))}</div>
        <div id="tw-hk-msg" style="color:#e6ecf3;font-size:13px;line-height:1.8;margin-bottom:6px"></div>
        <div class="muted" style="font-size:12px;line-height:1.7;margin-bottom:14px">${esc(t("機器を交換した／OSを再インストールした場合だけ許可してください。心当たりがなければ、なりすましの可能性があるため許可せずネットワーク管理者に確認してください。"))}</div>
        <div style="display:flex;justify-content:flex-end;gap:8px">
          <button class="btn" id="tw-hk-cancel">${esc(t("キャンセル"))}</button>
          <button class="btn danger" id="tw-hk-ok">${esc(t("機器を交換したので許可して再接続"))}</button>
        </div>
      </div>
    </div>
    <div id="tw-pw" class="hidden" style="position:absolute;inset:0;z-index:30;display:flex;align-items:center;justify-content:center;background:rgba(0,0,0,.85)">
      <div style="width:340px;background:#1a212b;border:1px solid #2a3442;border-radius:12px;padding:24px">
        <div style="color:#e6ecf3;margin-bottom:12px">${esc(t("マスターパスワード"))}</div>
        <div id="tw-pw-field" style="position:relative">
          <input id="tw-pw-in" type="password" style="width:100%;background:#11151c;color:#e6ecf3;border:1px solid #2a3442;border-radius:8px;padding:9px" autofocus>
        </div>
        <button id="tw-pw-ok" style="width:100%;margin-top:12px;background:#16a34a;color:#fff;border:none;border-radius:8px;padding:9px;cursor:pointer">${esc(t("接続"))}</button>
      </div>
    </div>
  </div>`);
  document.body.appendChild(wrap);
  // Same show/hide eye affordance as the main window's master-password fields.
  wirePasswordToggles(document.getElementById("tw-pw-field"));

  // Font size: remembered per machine (localStorage), changed with the A−/A＋
  // buttons, Ctrl+wheel, Ctrl+plus/minus; Ctrl+0 restores the default.
  const FONT_DEF = 13, FONT_MIN = 8, FONT_MAX = 32;
  let fontSize = FONT_DEF;
  try { fontSize = Math.min(FONT_MAX, Math.max(FONT_MIN, parseInt(localStorage.getItem("palaterm_term_font"), 10) || FONT_DEF)); } catch (e) {}
  const term = new Terminal({
    convertEol: false, cursorBlink: true,
    fontFamily: '"Consolas", monospace', fontSize,
    theme: { background: "#000000", foreground: "#d6dee8", cursor: "#5b9bff" },
  });
  const fit = new (window.FitAddon.FitAddon)();
  term.loadAddon(fit);
  term.open(document.getElementById("tw-host"));
  term.focus();
  let closed = false;

  // 貼り付けキー。xterm.js の既定は Ctrl+V を制御文字 ^V として機器へ送り、Shift+Insert も
  // ESC[2~ を送ってしまい、どちらもブラウザ既定の貼り付けを抑止する（Ctrl+Shift+V だけが貼り付く）。
  // Windows の Win+V（クリップボード履歴）は項目を選ぶと Ctrl+V を打鍵注入するので、同じ理由で
  // 貼り付かなかった（2026-09-13 実測）。ここで false を返すと xterm は何もせず、ブラウザ既定の
  // 貼り付けが textarea の paste イベントとして xterm に渡り、機器へ送られる。
  // 判定は e.code と e.key の両方で行う（キー注入ツールなどスキャンコード無しの入力では
  // e.code が空になるため）。
  // Alt+V は Tera Term の「貼り付け<CR>」: 確認ダイアログを開き、末尾 Enter をオンにしておく。
  term.attachCustomKeyEventHandler(e => {
    if (e.type !== "keydown" || e.metaKey) return true;
    const isV = e.code === "KeyV" || e.key === "v" || e.key === "V";
    const isIns = e.code === "Insert" || e.key === "Insert";
    if (e.altKey && !e.ctrlKey && isV) { pasteFromClipboard(true); return false; }
    if (e.altKey) return true;
    if (e.ctrlKey && !e.shiftKey && (e.code === "KeyF" || e.key === "f" || e.key === "F")) { openFind(); return false; }
    if (e.ctrlKey && (e.key === "+" || e.key === "=" || e.code === "Equal" || e.code === "NumpadAdd")) { setFont(fontSize + 1); return false; }
    if (e.ctrlKey && (e.key === "-" || e.code === "Minus" || e.code === "NumpadSubtract")) { setFont(fontSize - 1); return false; }
    if (e.ctrlKey && (e.key === "0" || e.code === "Digit0" || e.code === "Numpad0")) { setFont(FONT_DEF); return false; }
    if (e.ctrlKey && !e.shiftKey && isV) return false;
    if (e.shiftKey && !e.ctrlKey && isIns) return false;
    return true;
  });

  // ---- 貼り付けの確認（Tera Term 風）----
  // すべての貼り付け経路（Ctrl+V / Shift+Insert / Ctrl+Shift+V / Win+V の注入 / Alt+V /
  // 右クリック / ツールバー）を一つのダイアログに集約する。キーボード経路はブラウザ既定の
  // 貼り付けが xterm の textarea に paste イベントとして届くので、xterm より先（capture）で
  // 止めて本文を取り出す。Alt+V・右クリック・ボタンはクリップボードを Go 側から読む。
  // 送る内容はダイアログで編集でき、改行は xterm と同じく CR に正規化して機器へ送る。
  const pasteDlg = document.getElementById("tw-paste-dlg");
  const pasteText = document.getElementById("tw-paste-text");
  const pasteCr = document.getElementById("tw-paste-cr");
  const pasteInfo = document.getElementById("tw-paste-info");
  function sendText(text) {
    if (closed || !text) return;
    Term().Send(bytesToB64(new TextEncoder().encode(text))).catch(() => {});
  }
  // The confirmation is its own OS window (Tera Term style, see
  // paste_app.go); the in-window dialog below is only the fallback if that
  // window cannot be started.
  function openPasteDialog(text, withCR) {
    if (closed) return;
    term.clearSelection();
    Term().OpenPasteWindow(bytesToB64(new TextEncoder().encode(text)), !!withCR)
      .catch(e => { if (/既に開いています/.test(String(e))) flashNote(terr(e)); else openPasteDialogInline(text, withCR); });
  }
  rt().EventsOn("term:paste-done", () => { term.focus(); setTimeout(() => term.focus(), 150); setTimeout(() => term.focus(), 500); });
  function openPasteDialogInline(text, withCR) {
    if (closed) return;
    pasteText.value = text;
    pasteCr.checked = !!withCR;
    const n = text.split(/\r\n|\r|\n/).length;
    pasteInfo.textContent = t("{n} 行。各行の末尾は Enter として機器へ送られます。内容はここで編集できます。", { n });
    pasteDlg.classList.remove("hidden");
    pasteText.focus();
    pasteText.setSelectionRange(0, 0);
  }
  function closePasteDialog() { pasteDlg.classList.add("hidden"); term.focus(); }
  function confirmPaste() {
    let s = pasteText.value.replace(/\r\n|\r|\n/g, "\r");
    if (pasteCr.checked && !s.endsWith("\r")) s += "\r";
    closePasteDialog();
    sendText(s);
  }
  function pasteFromClipboard(withCR) {
    Term().GetClipboard().then(txt => { if (txt) openPasteDialog(txt, withCR); }).catch(() => {});
  }
  document.getElementById("tw-paste-ok").onclick = confirmPaste;
  document.getElementById("tw-paste-cancel").onclick = closePasteDialog;
  pasteText.addEventListener("keydown", e => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); confirmPaste(); }
    else if (e.key === "Escape") { e.preventDefault(); closePasteDialog(); }
  });
  term.textarea.addEventListener("paste", e => {
    const txt = (e.clipboardData || window.clipboardData || {}).getData ? (e.clipboardData || window.clipboardData).getData("text") : "";
    e.preventDefault(); e.stopImmediatePropagation();
    if (txt) openPasteDialog(txt, false);
  }, true);
  document.getElementById("tw-host").addEventListener("contextmenu", e => { e.preventDefault(); pasteFromClipboard(false); });
  document.getElementById("tw-paste").onclick = () => pasteFromClipboard(false);
  document.getElementById("tw-logdir").onclick = () => {
    Term().OpenLogDir().catch(() => {});
    term.focus();
  };
  document.getElementById("tw-logview").onclick = () => {
    Term().OpenLogWindow().catch(e => flashNote(terr(e)));
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
    }).catch(e => flashNote(terr(e)));
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
  function flashNote(text, ms) {
    copiedEl.textContent = text;
    copiedEl.classList.remove("hidden");
    clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => copiedEl.classList.add("hidden"), ms || 1800);
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

  function doFit() {
    try { fit.fit(); Term().Resize(term.cols, term.rows).catch(() => {}); } catch (e) {}
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
    closed = false;
    showSwitch(false);
    takeoverBar.classList.add("hidden"); doFit();
    stateEl.textContent = ev.manual ? t("接続済み（手動）") : t("接続済み"); stateEl.className = "pill ok";
    titleEl.textContent = `${ev.device}  ${ev.host} / ${ev.conn}` + (ev.manual ? `  ${t("（自動ログインなし）")}` : "");
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
  const hkDlg = document.getElementById("tw-hk");
  document.getElementById("tw-hk-cancel").onclick = () => { hkDlg.classList.add("hidden"); term.focus(); };
  document.getElementById("tw-hk-ok").onclick = () => {
    hkDlg.classList.add("hidden");
    closed = false; connecting = true;
    stateEl.textContent = t("接続中…"); stateEl.className = "pill run";
    showSwitch(!res.manual);
    term.write("\r\n\x1b[33m" + t("[ホストキーを更新して再接続します]") + "\x1b[0m\r\n");
    Term().AllowHostKeyChange().catch(e => { closed = true; term.write("\r\n\x1b[31m" + terr(e) + "\x1b[0m\r\n"); });
  };
  // A failed automatic login whose line is still open (ev.takeover): the
  // device is at a screen the profile could not get past, and the user can
  // read it now. Offer the keyboard instead of hanging up.
  const takeoverBar = document.getElementById("tw-takeover");
  document.getElementById("tw-takeover-ok").onclick = () => {
    takeoverBar.classList.add("hidden"); doFit();
    term.write("\x1b[33m" + t("[手動操作に切り替えました。以後の入力はそのまま機器へ送られます]") + "\x1b[0m\r\n");
    Term().Takeover().then(() => term.focus()).catch(e => { term.write("\r\n\x1b[31m" + terr(e) + "\x1b[0m\r\n"); });
  };
  document.getElementById("tw-takeover-close").onclick = () => {
    takeoverBar.classList.add("hidden"); doFit();
    Term().Discard().catch(() => {});
  };
  rt().EventsOn("term:closed", ev => {
    closed = true;
    connecting = false;
    showSwitch(false);
    stateEl.textContent = ev.error ? (ev.takeover ? t("自動ログイン失敗") : t("接続失敗")) : t("切断");
    stateEl.className = "pill err";
    if (ev.error && /ホストキーが前回接続時と異なります/.test(ev.error)) {
      document.getElementById("tw-hk-msg").textContent = terr(ev.error);
      hkDlg.classList.remove("hidden");
    }
    if (ev.error) term.write("\r\n\x1b[31m" + t("[接続エラー] ") + terr(ev.error) + "\x1b[0m\r\n");
    else term.write("\r\n\x1b[33m" + t("[切断されました]") + "\x1b[0m\r\n");
    if (ev.logPath) term.write("\x1b[36m" + t("[ログ保存] ") + ev.logPath + "\x1b[0m\r\n");
    if (ev.takeover) {
      term.write("\x1b[33m" + t("[回線はつながったままです。上の「手動で続ける」でこのまま操作できます]") + "\x1b[0m\r\n");
      takeoverBar.classList.remove("hidden"); doFit();
    }
  });

  term.onData(d => {
    if (closed) return;
    Term().Send(bytesToB64(new TextEncoder().encode(d))).catch(() => {});
  });
  // Close drops the live session; Discard drops a line held for takeover.
  window.addEventListener("beforeunload", () => { if (!closed) Term().Close(); else Term().Discard().catch(() => {}); });

  // Kick off: use the stdin password if present, else prompt in-window.
  const res = await Term().Start();
  if (titleEl.textContent === t("対話接続")) titleEl.textContent = res.name || t("対話接続");
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
