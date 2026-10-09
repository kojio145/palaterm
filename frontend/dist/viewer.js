// Log-viewer window (child process: PalaTerm.exe --view <file>). Shows one
// log file read-only in its own OS window, with find, font zoom and an
// optional follow mode for a log that is still being written (an interactive
// session's). Loaded in every window; only runs when Viewer is bound.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

async function bootViewerWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "#11151c";
  const V = () => window.go.main.Viewer;
  const doc = await V().Load();
  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:#11151c;font-family:'Segoe UI',sans-serif">
    <div style="display:flex;align-items:center;gap:8px;padding:6px 12px;background:#11151c;border-bottom:1px solid #2a3442">
      <div style="color:#e6ecf3;font-size:13px;font-weight:600;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${esc(doc.path)}">${esc(t("ログ"))}: ${esc(doc.name)}</div>
      <span id="lv-note" class="pill ok hidden"></span>
      <label style="display:flex;align-items:center;gap:5px;color:#c7d0dc;font-size:12px;white-space:nowrap"><input type="checkbox" id="lv-follow" style="width:auto"> ${esc(t("自動更新（追記を追う）"))}</label>
      <button class="btn sm" id="lv-reload">${esc(t("再読込"))}</button>
      <button class="btn sm" id="lv-font-dec" title="Ctrl+-">A−</button>
      <button class="btn sm" id="lv-font-inc" title="Ctrl++">A＋</button>
      <button class="btn sm" id="lv-find" title="Ctrl+F">${esc(t("検索"))}</button>
      <button class="btn sm" id="lv-copy">${esc(t("全文コピー"))}</button>
      <button class="btn sm" id="lv-folder">${esc(t("フォルダ"))}</button>
    </div>
    <div id="lv-findbar" class="hidden" style="display:flex;align-items:center;gap:6px;padding:4px 12px;background:#161c25;border-bottom:1px solid #2a3442;font-size:12px">
      <input id="lv-find-in" placeholder="${esc(t("検索（Enter=次 / Shift+Enter=前 / Esc=閉じる）"))}" style="flex:1;max-width:420px;background:#0b0e13;color:#e6ecf3;border:1px solid #2a3442;border-radius:6px;padding:4px 8px;font-size:12px">
      <span id="lv-find-n" class="muted" style="min-width:70px"></span>
      <button class="btn sm" id="lv-find-prev">▲</button>
      <button class="btn sm" id="lv-find-next">▼</button>
      <button class="btn sm" id="lv-find-close">✕</button>
    </div>
    <textarea id="lv-text" readonly spellcheck="false" style="flex:1;min-height:0;width:100%;resize:none;background:#0b0e13;color:#cbd5e1;border:none;padding:10px 14px;font-family:Consolas,monospace;font-size:12px;white-space:pre;overflow:auto;outline:none"></textarea>
    <div id="lv-status" class="muted" style="font-size:11px;padding:3px 12px;border-top:1px solid #2a3442"></div>
  </div>`);
  document.body.appendChild(wrap);
  const ta = document.getElementById("lv-text"), note = document.getElementById("lv-note"), status = document.getElementById("lv-status");
  let noteTimer = null;
  function flash(text) { note.textContent = text; note.classList.remove("hidden"); clearTimeout(noteTimer); noteTimer = setTimeout(() => note.classList.add("hidden"), 1500); }
  function show(d, keepPos) {
    const atBottom = ta.scrollTop + ta.clientHeight >= ta.scrollHeight - 4;
    const top = ta.scrollTop;
    ta.value = d.err ? t("ログを開けません") + ": " + terr(d.err) : d.text;
    const lines = d.text ? d.text.split(/\r\n|\r|\n/).length : 0;
    status.textContent = d.err ? "" : t("{n} 行 / {b} バイト", { n: lines, b: d.text.length }) + "　" + d.path;
    if (!keepPos || atBottom) ta.scrollTop = ta.scrollHeight; else ta.scrollTop = top;
  }
  show(doc, false);
  async function reload(auto) {
    const d = await V().Load();
    if (d.text !== ta.value) { show(d, true); if (!auto) flash(t("再読込しました")); }
    else if (!auto) flash(t("変更なし"));
  }
  document.getElementById("lv-reload").onclick = () => reload(false);
  // Follow mode: poll every 2 s. On by default when the file is still being
  // written (it was opened from a live interactive session), off otherwise.
  const follow = document.getElementById("lv-follow");
  follow.checked = !!doc.live;
  setInterval(() => { if (follow.checked) reload(true); }, 2000);
  document.getElementById("lv-folder").onclick = () => V().OpenFolder().catch(e => flash(terr(e)));
  document.getElementById("lv-copy").onclick = () => { ta.select(); document.execCommand("copy"); flash(t("コピーしました")); ta.setSelectionRange(0, 0); };

  // Font size (shared key with the terminal window's setting).
  const FONT_DEF = 12, FONT_MIN = 8, FONT_MAX = 32;
  let fontSize = FONT_DEF;
  try { fontSize = Math.min(FONT_MAX, Math.max(FONT_MIN, parseInt(localStorage.getItem("palaterm_viewer_font"), 10) || FONT_DEF)); } catch (e) {}
  function setFont(n) { fontSize = Math.min(FONT_MAX, Math.max(FONT_MIN, n)); ta.style.fontSize = fontSize + "px"; try { localStorage.setItem("palaterm_viewer_font", String(fontSize)); } catch (e) {} }
  setFont(fontSize);
  document.getElementById("lv-font-inc").onclick = () => setFont(fontSize + 1);
  document.getElementById("lv-font-dec").onclick = () => setFont(fontSize - 1);
  ta.addEventListener("wheel", e => { if (!e.ctrlKey) return; e.preventDefault(); setFont(fontSize + (e.deltaY < 0 ? 1 : -1)); }, { passive: false });

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
    const lh = parseFloat(getComputedStyle(ta).lineHeight) || fontSize * 1.4;
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
  fin.addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); findNext(e.shiftKey ? -1 : 1); } else if (e.key === "Escape") { e.preventDefault(); closeFind(); } });
  fin.addEventListener("input", () => { lastQ = ""; fn.textContent = ""; });
  document.addEventListener("keydown", e => {
    if (e.ctrlKey && (e.key === "f" || e.key === "F" || e.code === "KeyF")) { e.preventDefault(); openFind(); return; }
    if (e.ctrlKey && (e.key === "+" || e.key === "=" || e.code === "Equal" || e.code === "NumpadAdd")) { e.preventDefault(); setFont(fontSize + 1); return; }
    if (e.ctrlKey && (e.key === "-" || e.code === "Minus" || e.code === "NumpadSubtract")) { e.preventDefault(); setFont(fontSize - 1); return; }
    if (e.ctrlKey && (e.key === "0" || e.code === "Digit0" || e.code === "Numpad0")) { e.preventDefault(); setFont(FONT_DEF); return; }
    if (e.key === "Escape" && bar.classList.contains("hidden")) { V().Close(); }
  });
  ta.focus();
}
