// Log-viewer window (child process: PalaTerm.exe --view <file>). Shows one
// log file read-only in its own OS window, with find, font zoom and an
// optional follow mode for a log that is still being written (an interactive
// session's). Loaded in every window; only runs when Viewer is bound.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

async function bootViewerWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "var(--bg)";
  const V = () => window.go.main.Viewer;
  const doc = await V().Load();
  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:var(--bg);font-family:'Segoe UI',sans-serif">
    <div style="display:flex;align-items:center;gap:8px;padding:6px 12px;background:var(--bg);border-bottom:1px solid var(--border)">
      <div style="color:var(--text);font-size:13px;font-weight:600;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" data-tip="${esc(doc.path)}">${esc(t("ログ"))}: ${esc(doc.name)}</div>
      <span id="lv-note" class="pill ok hidden"></span>
      <label style="display:flex;align-items:center;gap:5px;color:#c7d0dc;font-size:12px;white-space:nowrap"><input type="checkbox" id="lv-wrap" style="width:auto"> ${esc(t("折り返し"))}</label>
      <label style="display:flex;align-items:center;gap:5px;color:#c7d0dc;font-size:12px;white-space:nowrap"><input type="checkbox" id="lv-follow" style="width:auto"> ${esc(t("自動更新（追記を追う）"))}</label>
      <button class="btn sm" id="lv-reload">${esc(t("再読み込み"))}</button>
      <button class="btn sm" id="lv-color" data-tip="${esc(t("文字色・背景色を変えます（この PC のすべてのログ表示窓に反映）"))}">${esc(t("色"))}</button>
      <button class="btn sm" id="lv-font-dec" data-tip="${esc(t("文字を小さく（Ctrl+-）"))}">A−</button>
      <button class="btn sm" id="lv-font-inc" data-tip="${esc(t("文字を大きく（Ctrl++）"))}">A+</button>
      <button class="btn sm" id="lv-find" data-tip="${esc(t("検索（Ctrl+F）"))}">${esc(t("検索"))}</button>
      <button class="btn sm" id="lv-copy">${esc(t("全文コピー"))}</button>
      <button class="btn sm" id="lv-folder" data-tip="${esc(t("このログがあるフォルダを開きます"))}">${esc(t("フォルダを開く"))}</button>
    </div>
    <div id="lv-findbar" class="hidden" style="display:flex;align-items:center;gap:6px;padding:4px 12px;background:#161c25;border-bottom:1px solid var(--border);font-size:12px">
      <input id="lv-find-in" placeholder="${esc(t("検索（Enter=次 / Shift+Enter=前 / Esc=閉じる）"))}" style="flex:1;max-width:420px;background:#0b0e13;color:var(--text);border:1px solid var(--border);border-radius:6px;padding:4px 8px;font-size:12px">
      <span id="lv-find-n" class="muted" style="min-width:70px"></span>
      <button class="btn sm" id="lv-find-prev" data-tip="${esc(t("前を検索（Shift+Enter）"))}">▲</button>
      <button class="btn sm" id="lv-find-next" data-tip="${esc(t("次を検索（Enter）"))}">▼</button>
      <button class="btn sm" id="lv-find-close" data-tip="${esc(t("閉じる（Esc）"))}">✕</button>
    </div>
    <textarea id="lv-text" readonly spellcheck="false" style="flex:1;min-height:0;width:100%;resize:none;background:#0b0e13;color:#cbd5e1;border:none;padding:10px 14px;font-family:Consolas,monospace;font-size:12px;white-space:pre;overflow:auto;outline:none"></textarea>
    <div id="lv-status" class="muted" style="font-size:11px;padding:3px 12px;border-top:1px solid var(--border)"></div>
  </div>`);
  document.body.appendChild(wrap);
  const ta = document.getElementById("lv-text"), note = document.getElementById("lv-note"), status = document.getElementById("lv-status");
  // 表示の色: by default a light page, unlike the black terminal, so a log
  // is never taken for a live session (colors.js).
  const applyColors = c => { ta.style.background = c.bg; ta.style.color = c.fg; };
  applyColors(screenColors("viewer"));
  onScreenColors("viewer", applyColors);
  document.getElementById("lv-color").onclick = e => openScreenColorPanel("viewer", e.currentTarget, applyColors, () => ta.focus());
  let noteTimer = null;
  // kind "err" shows red and stays long enough to be read.
  function flash(text, kind) { note.textContent = text; note.className = "pill " + (kind || "ok"); clearTimeout(noteTimer); noteTimer = setTimeout(() => note.classList.add("hidden"), kind === "err" ? 6000 : 1500); }
  function show(d, keepPos) {
    const atBottom = ta.scrollTop + ta.clientHeight >= ta.scrollHeight - 4;
    const top = ta.scrollTop;
    // A selection survives the reload (自動更新 rewrites the text every few
    // seconds while a live log grows, which used to drop it mid-copy).
    const selA = ta.selectionStart, selB = ta.selectionEnd;
    const hadSel = keepPos && selB > selA;
    ta.value = d.err ? t("ログを開けません") + ": " + terr(d.err) : d.text;
    if (hadSel) { ta.setSelectionRange(selA, selB); keepPos = true; }
    const lines = d.text ? d.text.split(/\r\n|\r|\n/).length : 0;
    status.textContent = d.err ? "" : t("{n} 行 / {c} 文字", { n: lines.toLocaleString(), c: d.text.length.toLocaleString() }) + "　" + d.path;
    if (!keepPos || (atBottom && !hadSel)) ta.scrollTop = ta.scrollHeight; else ta.scrollTop = top;
  }
  show(doc, false);
  async function reload(auto) {
    const d = await V().Load();
    if (d.text !== ta.value) { show(d, true); if (!auto) flash(t("再読み込みしました")); }
    else if (!auto) flash(t("変更なし"));
  }
  document.getElementById("lv-reload").onclick = () => reload(false);
  // Long lines (a FortiGate config line, a routing table) wrap on request;
  // the choice is remembered per PC.
  const wrapBox = document.getElementById("lv-wrap");
  try { wrapBox.checked = localStorage.getItem("palaterm_viewer_wrap") === "1"; } catch (e) {}
  const applyWrap = () => { ta.style.whiteSpace = wrapBox.checked ? "pre-wrap" : "pre"; ta.style.wordBreak = wrapBox.checked ? "break-all" : ""; };
  wrapBox.onchange = () => { applyWrap(); try { localStorage.setItem("palaterm_viewer_wrap", wrapBox.checked ? "1" : "0"); } catch (e) {} };
  applyWrap();
  // Follow mode: poll every 2 s. On by default when the file is still being
  // written (it was opened from a live interactive session), off otherwise.
  const follow = document.getElementById("lv-follow");
  follow.checked = !!doc.live;
  setInterval(() => { if (follow.checked) reload(true); }, 2000);
  document.getElementById("lv-folder").onclick = () => V().OpenFolder().catch(e => flash(t("フォルダを開けません") + ": " + terr(e), "err"));
  // The whole text through the clipboard (copyText, app.js), not a select
  // of the textarea: selecting it scrolled the log to its top, and the place
  // being read was lost.
  const copyAll = () => copyText(ta.value, t("コピーしました"));
  document.getElementById("lv-copy").onclick = copyAll;
  // Right-click: 選択した文字をコピー (app.js) plus 全文コピー.
  ctxExtraItems = () => [[t("全文コピー"), copyAll]];

  // Font size (its own key; wireFontZoom, app.js).
  const zoom = wireFontZoom("palaterm_viewer_font", px => { ta.style.fontSize = px + "px"; }, { def: 12 });
  zoom.set(zoom.size);
  zoom.buttons(document.getElementById("lv-font-inc"), document.getElementById("lv-font-dec"));
  zoom.wheel(ta);

  // Find: case-insensitive, selects the match in the textarea and scrolls to it.
  const bar = document.getElementById("lv-findbar"), fin = document.getElementById("lv-find-in"), fn = document.getElementById("lv-find-n");
  let matches = [], idx = -1, lastQ = "";
  function collect(q) { const res = []; if (!q) return res; const lq = q.toLowerCase(), s = ta.value.toLowerCase(); let i = 0; while ((i = s.indexOf(lq, i)) >= 0) { res.push(i); i += lq.length; } return res; }
  function showMatch() {
    if (!matches.length) { fn.textContent = lastQ ? t("該当なし") : ""; return; }
    const m = matches[idx];
    ta.focus(); ta.setSelectionRange(m, m + lastQ.length);
    // Scroll the selection into view: estimate the line from the text before it.
    const line = ta.value.slice(0, m).split(/\r\n|\r|\n/).length - 1;
    const lh = parseFloat(getComputedStyle(ta).lineHeight) || zoom.size * 1.4;
    ta.scrollTop = Math.max(0, line * lh - ta.clientHeight / 2);
    fn.textContent = `${idx + 1} / ${matches.length}`;
  }
  function findNext(dir) { const q = fin.value; if (q !== lastQ) { lastQ = q; matches = collect(q); idx = dir >= 0 ? -1 : 0; } if (!matches.length) { showMatch(); return; } idx = (idx + dir + matches.length) % matches.length; showMatch(); fin.focus(); }
  function openFind() { bar.classList.remove("hidden"); fin.focus(); fin.select(); }
  function closeFind() { bar.classList.add("hidden"); ta.focus(); }
  document.getElementById("lv-find").onclick = openFind;
  document.getElementById("lv-find-next").onclick = () => findNext(1);
  document.getElementById("lv-find-prev").onclick = () => findNext(-1);
  document.getElementById("lv-find-close").onclick = closeFind;
  fin.addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); findNext(e.shiftKey ? -1 : 1); } else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); closeFind(); } });
  fin.addEventListener("input", () => { lastQ = ""; fn.textContent = ""; });
  document.addEventListener("keydown", e => {
    if (e.ctrlKey && (e.key === "f" || e.key === "F" || e.code === "KeyF")) { e.preventDefault(); openFind(); return; }
    if (zoom.key(e)) { e.preventDefault(); return; }
    // Esc closes an open find bar first, the window only after that (as the diff window).
    if (e.key === "Escape" && !document.getElementById("sc-pop")) { if (!bar.classList.contains("hidden")) closeFind(); else V().Close(); }
  });
  ta.focus();
}

// Screen window (child process: PalaTerm.exe --watch <file>). A batch
// device's log replayed into a read-only terminal as it grows — what a Tera
// Term macro's window shows — so a device that has gone wrong is seen while
// the batch is still running. The file is polled from where the last read
// ended; the status says how long the device has been quiet. Only runs when
// Watcher is bound.
async function bootWatchWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "#000";
  const W = () => window.go.main.Watcher;
  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:#000;font-family:'Segoe UI',sans-serif">
    <div style="display:flex;align-items:center;gap:8px;padding:6px 12px;background:var(--bg);border-bottom:1px solid var(--border)">
      <div id="wv-name" style="color:var(--text);font-size:13px;font-weight:600;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"></div>
      <span id="wv-state" class="pill" style="font-size:11px"></span>
      <button class="btn sm" id="wv-color" data-tip="${esc(t("文字色・背景色を変えます（この PC のすべての画面表示窓に反映）"))}">${esc(t("色"))}</button>
      <button class="btn sm" id="wv-font-dec" data-tip="${esc(t("文字を小さく（Ctrl+-）"))}">A−</button>
      <button class="btn sm" id="wv-font-inc" data-tip="${esc(t("文字を大きく（Ctrl++）"))}">A+</button>
      <button class="btn sm" id="wv-log" data-tip="${esc(t("同じログを文字で開きます（検索・全文コピー）"))}">${esc(t("ログ表示"))}</button>
    </div>
    <div style="padding:2px 12px;background:var(--bg);color:#8b97a8;font-size:11px;border-bottom:1px solid var(--border)">${esc(t("表示のみ（キー入力は機器に送りません）。操作するときは本体の「接続」から別に接続してください"))}</div>
    <div id="wv-host" style="flex:1;min-height:0;padding:4px 0 0 6px"></div>
  </div>`);
  document.body.appendChild(wrap);

  // Its own size (palaterm_watch_font); until one is chosen here, the
  // terminal window's (wireFontZoom, app.js).
  const zoom = wireFontZoom("palaterm_watch_font", px => { term.options.fontSize = px; refit(); }, { def: 13, fallbackKey: "palaterm_term_font" });
  const term = new Terminal({
    convertEol: false, cursorBlink: false, disableStdin: true, scrollback: 10000,
    fontFamily: '"Consolas", monospace', fontSize: zoom.size,
    theme: xtermTheme(screenColors("watch")),
  });
  const fit = new (window.FitAddon.FitAddon)();
  term.loadAddon(fit);
  term.open(document.getElementById("wv-host"));
  // 表示の色: navy by default, unlike the terminal one types into.
  const applyColors = c => { term.options.theme = xtermTheme(c); document.body.style.background = c.bg; wrap.style.background = c.bg; };
  applyColors(screenColors("watch"));
  onScreenColors("watch", applyColors);
  document.getElementById("wv-color").onclick = e => openScreenColorPanel("watch", e.currentTarget, applyColors);
  const refit = () => { try { fit.fit(); } catch (e) {} };
  refit();
  window.addEventListener("resize", refit);
  zoom.buttons(document.getElementById("wv-font-inc"), document.getElementById("wv-font-dec"));
  document.getElementById("wv-log").onclick = () => W().OpenLog().catch(e => toast(t("ログを開けません") + ": " + terr(e), "err"));

  // Selection → Ctrl+C / right-click copies it (the terminal sends nothing).
  term.attachCustomKeyEventHandler(e => {
    if (e.type === "keydown" && e.ctrlKey && (e.code === "KeyC" || e.key === "c") && term.hasSelection()) { copyText(term.getSelection(), t("コピーしました")); return false; }
    return true;
  });
  ctxExtraItems = () => term.hasSelection() ? [[t("選択した文字をコピー"), () => copyText(term.getSelection(), t("コピーしました"))]] : [];
  document.addEventListener("keydown", e => {
    if (zoom.key(e)) { e.preventDefault(); return; }
    if (e.key === "Escape" && !document.getElementById("sc-pop")) W().Close();
  }, true); // capture: the terminal's own textarea would swallow the keys

  // Poll the file from where the last read ended; a full chunk means more
  // is waiting (a long log opened late), so read again at once.
  const state = document.getElementById("wv-state");
  let off = 0, lastMod = 0, named = false;
  function showState() {
    if (!lastMod) { state.textContent = ""; return; }
    const quiet = Math.max(0, Math.round((Date.now() - lastMod) / 1000));
    state.className = "pill " + (quiet < 10 ? "ok" : "");
    state.textContent = quiet < 3 ? t("出力中") : t("最後の出力から {n} 秒", { n: quiet });
  }
  async function poll() {
    let more = false;
    try {
      const c = await W().Read(off);
      if (!named) { document.getElementById("wv-name").textContent = t("画面") + ": " + c.name; document.getElementById("wv-name").dataset.tip = c.path; named = true; }
      if (c.err) { state.className = "pill"; state.textContent = terr(c.err); }
      else {
        if (c.off < off) term.reset();
        if (c.data) {
          const bin = atob(c.data), bytes = new Uint8Array(bin.length);
          for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
          term.write(bytes);
          more = bytes.length >= 512 * 1024;
        }
        off = c.off;
        lastMod = c.modUnixMs || lastMod;
        showState();
      }
    } catch (e) { }
    setTimeout(poll, more ? 0 : 300);
  }
  setInterval(showState, 1000);
  poll();
}
