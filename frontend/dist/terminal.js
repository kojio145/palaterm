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

async function bootTerminalWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "#000";

  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:#000">
    <div id="tw-bar" style="display:flex;align-items:center;justify-content:space-between;padding:6px 12px;background:#11151c;border-bottom:1px solid #2a3442;font-family:'Segoe UI',sans-serif">
      <div id="tw-title" style="color:#e6ecf3;font-size:13px">${esc(t("対話接続"))}</div>
      <div style="display:flex;align-items:center;gap:8px">
        <span id="tw-copied" class="pill ok hidden">${esc(t("コピーしました"))}</span>
        <button class="btn sm" id="tw-paste" title="Alt+V">${esc(t("貼り付け…"))}</button>
        <button class="btn sm" id="tw-logdir">${esc(t("ログフォルダ"))}</button>
        <span id="tw-state" class="pill run">${esc(t("接続中…"))}</span>
      </div>
    </div>
    <div id="tw-host" style="flex:1;min-height:0;background:#000;padding:6px"></div>
    <div id="tw-paste-dlg" class="hidden" style="position:absolute;inset:0;display:flex;align-items:center;justify-content:center;background:rgba(0,0,0,.6)">
      <div style="width:min(760px,90vw);background:#1a212b;border:1px solid #2a3442;border-radius:12px;padding:20px 22px;font-family:'Segoe UI',sans-serif">
        <div style="color:#e6ecf3;font-size:14px;font-weight:600;margin-bottom:6px">${esc(t("貼り付けの確認"))}</div>
        <div id="tw-paste-info" class="muted" style="font-size:12px;margin-bottom:8px"></div>
        <textarea id="tw-paste-text" spellcheck="false" style="width:100%;height:min(320px,45vh);resize:vertical;background:#0b0e13;color:#d6dee8;border:1px solid #2a3442;border-radius:8px;padding:8px;font-family:Consolas,monospace;font-size:13px;white-space:pre;overflow:auto"></textarea>
        <label style="display:flex;align-items:center;gap:6px;color:#c7d0dc;font-size:12px;margin:8px 0 12px"><input type="checkbox" id="tw-paste-cr" style="width:auto"> ${esc(t("最後に Enter を送る"))}</label>
        <div style="display:flex;justify-content:flex-end;gap:8px">
          <button class="btn" id="tw-paste-cancel">${esc(t("キャンセル"))}</button>
          <button class="btn primary" id="tw-paste-ok">${esc(t("貼り付け"))}</button>
        </div>
        <div class="muted" style="font-size:11px;margin-top:8px">${esc(t("Enter = 貼り付け ／ Shift+Enter = 改行を挿入 ／ Esc = キャンセル"))}</div>
      </div>
    </div>
    <div id="tw-pw" class="hidden" style="position:absolute;inset:0;display:flex;align-items:center;justify-content:center;background:rgba(0,0,0,.85)">
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

  const term = new Terminal({
    convertEol: false, cursorBlink: true,
    fontFamily: '"Consolas", monospace', fontSize: 13,
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
  function openPasteDialog(text, withCR) {
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

  // Tera Term 風の「選択で自動コピー」: マウスで選択した範囲を、選択が確定した時点で
  // クリップボードへ入れる（ドラッグ中は連続で発火するので少し待ってから1回）。
  // 書き込みは Go 側（Wails runtime）経由。WebView2 内の navigator.clipboard は
  // フォーカスや権限の条件で失敗することがあるため、そちらは保険のフォールバック。
  // マウスを離した瞬間にも一度コピーする（ドラッグ終了＝確定）。同じ内容の二重コピーは抑止し、
  // 入ったことが分かるようにバーに「コピーしました」を短く出す。
  let selTimer = null, lastCopied = "", copiedTimer = null;
  const copiedEl = document.getElementById("tw-copied");
  function copySelection() {
    const s = term.getSelection();
    if (!s || s === lastCopied) return;
    lastCopied = s;
    const flash = () => {
      copiedEl.classList.remove("hidden");
      clearTimeout(copiedTimer);
      copiedTimer = setTimeout(() => copiedEl.classList.add("hidden"), 1200);
    };
    Term().SetClipboard(s).then(flash).catch(() => {
      try { navigator.clipboard.writeText(s).then(flash).catch(() => {}); } catch (e) {}
    });
  }
  term.onSelectionChange(() => {
    clearTimeout(selTimer);
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
  rt().EventsOn("term:ready", ev => {
    connecting = false;
    stateEl.textContent = t("接続済み"); stateEl.className = "pill ok";
    titleEl.textContent = `${ev.device}  ${ev.host} / ${ev.conn}`;
    term.focus(); doFit();
  });
  rt().EventsOn("term:closed", ev => {
    closed = true;
    connecting = false;
    stateEl.textContent = ev.error ? t("接続失敗") : t("切断");
    stateEl.className = "pill err";
    if (ev.error) term.write("\r\n\x1b[31m" + t("[接続エラー] ") + terr(ev.error) + "\x1b[0m\r\n");
    else term.write("\r\n\x1b[33m" + t("[切断されました]") + "\x1b[0m\r\n");
    if (ev.logPath) term.write("\x1b[36m" + t("[ログ保存] ") + ev.logPath + "\x1b[0m\r\n");
  });

  term.onData(d => {
    if (closed) return;
    Term().Send(bytesToB64(new TextEncoder().encode(d))).catch(() => {});
  });
  window.addEventListener("beforeunload", () => { if (!closed) Term().Close(); });

  // Kick off: use the stdin password if present, else prompt in-window.
  const res = await Term().Start();
  if (titleEl.textContent === t("対話接続")) titleEl.textContent = res.name || t("対話接続");
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
