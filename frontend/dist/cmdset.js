// Command-set window (child process: PalaTerm.exe --cmdset). One command set,
// read-only, beside a terminal window: line numbers, what each command waits
// for (shown on request), the commands that change a device in orange
// (guard.Check), rows
// picked with the mouse (Shift / Ctrl like a file list) and copied as the
// commands alone. The main window sends the set on the window's stdin and
// again whenever it is saved (CmdsetWin; a rename retitles the window, a
// delete leaves the last contents with a notice). Nothing here can change
// the set: no input but the find box. Loaded in every window; only runs when
// CmdsetWin is bound.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

async function bootCmdsetWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "var(--bg)";
  const C = () => window.go.main.CmdsetWin;
  const lbl = "display:flex;align-items:center;gap:5px;color:#c7d0dc;font-size:12px;white-space:nowrap;cursor:pointer";
  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:var(--bg);font-family:'Segoe UI',sans-serif">
    <div style="display:flex;align-items:center;gap:8px;padding:6px 12px;border-bottom:1px solid var(--border);flex-wrap:wrap">
      <div id="cw-name" style="color:var(--text);font-size:13px;font-weight:600;flex:1;min-width:80px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"></div>
      <span id="cw-count" class="muted" style="font-size:12px;white-space:nowrap"></span>
      <button class="btn sm" id="cw-copy-sel" data-tip="${esc(t("選択した行のコマンドだけをコピーします（Ctrl+C）"))}">${esc(t("選択行をコピー"))}</button>
      <button class="btn sm" id="cw-copy-all" data-tip="${esc(t("全コマンドを 1 行 1 コマンドでコピーします（行番号・待つ文字列は含めません）"))}">${esc(t("コマンドをすべてコピー"))}</button>
      <button class="btn sm" id="cw-find" data-tip="${esc(t("検索（Ctrl+F）"))}">${esc(t("検索"))}</button>
      <button class="btn sm" id="cw-font-dec" data-tip="${esc(t("文字を小さく（Ctrl+-）"))}">A−</button>
      <button class="btn sm" id="cw-font-inc" data-tip="${esc(t("文字を大きく（Ctrl++）"))}">A+</button>
    </div>
    <div style="display:flex;align-items:center;gap:16px;padding:4px 12px;border-bottom:1px solid var(--border);flex-wrap:wrap">
      <label style="${lbl}"><input type="checkbox" id="cw-meta" style="width:auto"> ${esc(t("待つ文字列・待機秒も表示"))}</label>
      <label style="${lbl}" data-tip="${esc(t("ほかの窓（端末窓など）の上に、この窓を常に表示します"))}"><input type="checkbox" id="cw-ontop" style="width:auto"> ${esc(t("常に手前に表示"))}</label>
      <span id="cw-pauseoff" class="muted hidden" style="font-size:11px" data-tip="${esc(t("「待機秒を使う」がオフのセットです（プロンプトが出たらすぐ次のコマンド）"))}">${esc(t("待機秒なし"))}</span>
      <span class="muted" style="font-size:11px;margin-left:auto">${esc(t("この窓では編集できません — 編集はメイン画面の「編集」から"))}</span>
    </div>
    <div id="cw-deleted" class="warn-box err hidden" style="margin:8px 12px 0">${esc(t("このコマンドセットは削除されました"))}</div>
    <div id="cw-danger" class="warn-box hidden" style="margin:8px 12px 0"></div>
    <div id="cw-findbar" class="hidden" style="display:flex;align-items:center;gap:6px;padding:4px 12px;margin-top:6px;background:#161c25;border-top:1px solid var(--border);border-bottom:1px solid var(--border);font-size:12px">
      <input id="cw-find-in" placeholder="${esc(t("検索（Enter=次 / Shift+Enter=前 / Esc=閉じる）"))}" style="flex:1;max-width:420px;background:#0b0e13;color:var(--text);border:1px solid var(--border);border-radius:6px;padding:4px 8px;font-size:12px">
      <span id="cw-find-n" class="muted" style="min-width:70px"></span>
      <button class="btn sm" id="cw-find-prev" data-tip="${esc(t("前を検索（Shift+Enter）"))}">▲</button>
      <button class="btn sm" id="cw-find-next" data-tip="${esc(t("次を検索（Enter）"))}">▼</button>
      <button class="btn sm" id="cw-find-close" data-tip="${esc(t("閉じる（Esc）"))}">✕</button>
    </div>
    <div id="cw-body" class="cw-body" tabindex="0" style="flex:1;min-height:0;overflow:auto;background:#0b0e13;color:#cbd5e1;margin-top:8px;border-top:1px solid var(--border);outline:none;user-select:none"></div>
    <div id="cw-status" class="muted" style="font-size:11px;padding:3px 12px;border-top:1px solid var(--border)"></div>
  </div>`);
  document.body.appendChild(wrap);
  const body = document.getElementById("cw-body"), status = document.getElementById("cw-status");

  let st = await C().Load();
  let sel = new Set(), anchor = -1;
  let hits = [], hitIdx = -1, lastQ = "";
  const cmds = () => (st.doc && st.doc.commands) || [];

  // 待つ文字列・待機秒も表示 and 常に手前に表示: both off by default (the
  // window is mostly for reading and copying the commands — 2026-10-23 user
  // request), remembered per PC.
  const metaBox = document.getElementById("cw-meta"), topBox = document.getElementById("cw-ontop");
  try { metaBox.checked = localStorage.getItem("palaterm_cmdset_meta") === "1"; } catch (e) {}
  try { topBox.checked = localStorage.getItem("palaterm_cmdset_ontop") === "1"; } catch (e) {}
  metaBox.onchange = () => { try { localStorage.setItem("palaterm_cmdset_meta", metaBox.checked ? "1" : "0"); } catch (e) {} render(); };
  topBox.onchange = () => {
    try { localStorage.setItem("palaterm_cmdset_ontop", topBox.checked ? "1" : "0"); } catch (e) {}
    C().SetOnTop(topBox.checked).catch(() => {});
  };
  // (A spare window — nothing in it yet — applies it when it is shown: on a
  // hidden frame it would take the foreground.)
  if (topBox.checked && st.doc && st.doc.name) C().SetOnTop(true).catch(() => {});

  function metaOf(c) {
    const parts = [];
    if (c.wait) parts.push(t("待つ: {w}", { w: c.wait }));
    if (!st.doc.pauseOff) {
      const p = c.pauseSec || 0, s = c.serialSec || 0;
      if (p !== s) parts.push(t("待機 {n} 秒（シリアル {s} 秒）", { n: p, s }));
      else if (p > 0) parts.push(t("待機 {n} 秒", { n: p }));
    }
    return parts.join(t("　"));
  }
  function render() {
    const list = cmds();
    document.getElementById("cw-name").textContent = st.doc.name;
    document.getElementById("cw-name").dataset.tip = st.doc.name;
    document.getElementById("cw-count").textContent = t("{n} コマンド", { n: list.length });
    document.getElementById("cw-pauseoff").classList.toggle("hidden", !metaBox.checked || !st.doc.pauseOff);
    document.getElementById("cw-deleted").classList.toggle("hidden", !st.deleted);
    const nd = list.filter(c => c.danger).length;
    const dg = document.getElementById("cw-danger");
    dg.textContent = t("⚠ 設定を変えるコマンドが {n} 行あります（橙色）", { n: nd });
    dg.classList.toggle("hidden", nd === 0);
    const digits = String(Math.max(1, list.length)).length;
    body.innerHTML = list.length ? list.map((c, i) => {
      const meta = metaBox.checked ? metaOf(c) : "";
      return `<div class="cw-row${c.danger ? " danger" : ""}${sel.has(i) ? " sel" : ""}" data-i="${i}">
        <span class="cw-no" style="min-width:${digits + 1}ch">${i + 1}</span>
        <span class="cw-text">${esc(c.text)}</span>
        ${c.danger ? `<span class="cw-tag">${esc(t(c.danger))}</span>` : ""}
        ${meta ? `<span class="cw-meta">${esc(meta)}</span>` : ""}
      </div>`;
    }).join("") : `<div class="empty">${esc(t("コマンドがありません"))}</div>`;
    paintFind();
    showStatus();
  }
  function showStatus() {
    status.textContent = sel.size
      ? t("{n} 行を選択中（Ctrl+C でコピー）", { n: sel.size })
      : t("行をクリック・ドラッグで選択（Shift で範囲・Ctrl で追加/解除・Ctrl+A ですべて）");
  }
  function paintSel() {
    body.querySelectorAll(".cw-row").forEach(r => r.classList.toggle("sel", sel.has(+r.dataset.i)));
    showStatus();
  }

  // Row selection, as in a file list: click, Shift+click for a range,
  // Ctrl+click to add or remove — and a drag over the lines selects every
  // line from where it started (Ctrl+drag adds them to the selection). A drag
  // past the top or bottom edge scrolls the list.
  let drag = null; // {base, y, timer} while the left button is held
  function rowAt(y) {
    const rows = body.querySelectorAll(".cw-row");
    if (!rows.length) return -1;
    const r = body.getBoundingClientRect();
    const el = document.elementFromPoint(r.left + 8, Math.min(Math.max(y, r.top + 1), r.bottom - 2));
    const row = el && el.closest(".cw-row");
    if (row) return +row.dataset.i;
    // No row under the point (the scroll bar, the space under a short
    // list): the nearest row still in view — above the list its first one,
    // otherwise the last one shown above the pointer. Never the list's end:
    // a drag onto the bottom edge used to select every line down to the
    // last one at once, and the edge scroll brings the rest in a step at a
    // time.
    const inView = [...rows].filter(r2 => { const b = r2.getBoundingClientRect(); return b.bottom > r.top && b.top < r.bottom; });
    if (!inView.length) return -1;
    if (y <= r.top + 1) return +inView[0].dataset.i;
    const above = inView.filter(r2 => r2.getBoundingClientRect().top <= y);
    return +(above.length ? above[above.length - 1] : inView[inView.length - 1]).dataset.i;
  }
  function dragTo(y) {
    const j = rowAt(y);
    if (j < 0 || anchor < 0) return;
    sel = new Set(drag.base);
    for (let k = Math.min(anchor, j); k <= Math.max(anchor, j); k++) sel.add(k);
    paintSel();
  }
  function endDrag() {
    if (!drag) return;
    clearInterval(drag.timer);
    drag = null;
  }
  body.addEventListener("mousedown", e => {
    if (e.button !== 0) return;
    const row = e.target.closest(".cw-row");
    if (!row) return;
    e.preventDefault(); // no text selection; the lines are selected instead
    const i = +row.dataset.i;
    let base;
    if (e.shiftKey && anchor >= 0) {
      base = e.ctrlKey ? new Set(sel) : new Set();
    } else if (e.ctrlKey) {
      if (sel.has(i)) { sel.delete(i); anchor = i; paintSel(); body.focus(); return; }
      base = new Set(sel);
      anchor = i;
    } else {
      base = new Set();
      anchor = i;
    }
    endDrag();
    drag = { base, y: e.clientY, timer: 0 };
    dragTo(e.clientY);
    body.focus();
    // Past an edge the list scrolls on its own while the button is held.
    drag.timer = setInterval(() => {
      if (!drag) return;
      const r = body.getBoundingClientRect();
      if (drag.y < r.top) body.scrollTop -= Math.min(40, r.top - drag.y);
      else if (drag.y > r.bottom) body.scrollTop += Math.min(40, drag.y - r.bottom);
      else return;
      dragTo(drag.y);
    }, 50);
  });
  document.addEventListener("mousemove", e => {
    if (!drag) return;
    if (!(e.buttons & 1)) { endDrag(); return; }
    drag.y = e.clientY;
    dragTo(e.clientY);
  });
  window.addEventListener("mouseup", endDrag);
  window.addEventListener("blur", endDrag);

  // Copy: the commands only (no numbers, tags or waits), one per line.
  const textOf = idx => idx.map(i => cmds()[i].text).join("\r\n");
  function copySel() {
    const idx = [...sel].filter(i => i < cmds().length).sort((a, b) => a - b);
    if (!idx.length) { toast(t("コピーする行を選択してください"), "err"); return; }
    copyText(textOf(idx), t("{n} 行のコマンドをコピーしました", { n: idx.length }));
  }
  function copyAll() {
    if (!cmds().length) return;
    copyText(textOf(cmds().map((c, i) => i)), t("{n} 行のコマンドをコピーしました", { n: cmds().length }));
  }
  document.getElementById("cw-copy-sel").onclick = copySel;
  document.getElementById("cw-copy-all").onclick = copyAll;
  ctxExtraItems = () => [[t("選択行をコピー"), copySel], [t("コマンドをすべてコピー"), copyAll]];

  // Font size (remembered per PC; wireFontZoom, app.js).
  const zoom = wireFontZoom("palaterm_cmdset_font", px => { body.style.fontSize = px + "px"; }, { def: 13 });
  zoom.set(zoom.size);
  zoom.buttons(document.getElementById("cw-font-inc"), document.getElementById("cw-font-dec"));
  zoom.wheel(body);

  // Find (as the log viewer's): case-insensitive over the commands; the
  // matching rows are marked, the current one outlined and scrolled to.
  const bar = document.getElementById("cw-findbar"), fin = document.getElementById("cw-find-in"), fn = document.getElementById("cw-find-n");
  function paintFind() {
    body.querySelectorAll(".cw-row").forEach(r => {
      const i = +r.dataset.i;
      r.classList.toggle("hit", hits.includes(i));
      r.classList.toggle("cur", hitIdx >= 0 && hits[hitIdx] === i);
    });
  }
  function findNext(dir) {
    const q = fin.value;
    if (q !== lastQ) {
      lastQ = q;
      const lq = q.toLowerCase();
      hits = q ? cmds().map((c, i) => c.text.toLowerCase().includes(lq) ? i : -1).filter(i => i >= 0) : [];
      hitIdx = dir >= 0 ? -1 : 0;
    }
    if (!hits.length) { hitIdx = -1; paintFind(); fn.textContent = lastQ ? t("該当なし") : ""; return; }
    hitIdx = (hitIdx + dir + hits.length) % hits.length;
    paintFind();
    const row = body.querySelector(`.cw-row[data-i="${hits[hitIdx]}"]`);
    if (row) row.scrollIntoView({ block: "center" });
    fn.textContent = `${hitIdx + 1} / ${hits.length}`;
  }
  function openFind() { bar.classList.remove("hidden"); fin.focus(); fin.select(); }
  function closeFind() { bar.classList.add("hidden"); hits = []; hitIdx = -1; lastQ = ""; fn.textContent = ""; paintFind(); body.focus(); }
  document.getElementById("cw-find").onclick = openFind;
  document.getElementById("cw-find-next").onclick = () => findNext(1);
  document.getElementById("cw-find-prev").onclick = () => findNext(-1);
  document.getElementById("cw-find-close").onclick = closeFind;
  fin.addEventListener("keydown", e => {
    if (e.key === "Enter") { e.preventDefault(); findNext(e.shiftKey ? -1 : 1); }
    else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); closeFind(); }
  });
  fin.addEventListener("input", () => { lastQ = ""; hits = []; hitIdx = -1; fn.textContent = ""; paintFind(); });

  document.addEventListener("keydown", e => {
    const inFind = document.activeElement === fin;
    if (e.ctrlKey && (e.key === "f" || e.key === "F" || e.code === "KeyF")) { e.preventDefault(); openFind(); return; }
    if (zoom.key(e)) { e.preventDefault(); return; }
    // In the find box, Ctrl+A / Ctrl+C work on its text as usual.
    if (!inFind && e.ctrlKey && (e.key === "a" || e.key === "A" || e.code === "KeyA")) {
      e.preventDefault(); sel = new Set(cmds().map((c, i) => i)); paintSel(); return;
    }
    if (!inFind && e.ctrlKey && (e.key === "c" || e.key === "C" || e.code === "KeyC")) { e.preventDefault(); copySel(); return; }
    // Esc closes an open find bar first, the window only after that.
    if (e.key === "Escape") { if (!bar.classList.contains("hidden")) closeFind(); else C().Close(); }
  });

  // A save in the main window: the set again (or the notice that it is
  // gone). Rows past the new end drop out of the selection.
  function applyUpdate(s) {
    // A spare window (started hidden before the click) gets its first set
    // now: the language and the remembered settings may have changed since
    // it started, so they are read again.
    if (!(st.doc && st.doc.name)) {
      let lang = null;
      try { lang = localStorage.getItem("palaterm_lang"); } catch (e) {}
      if (lang && lang !== LANG) { location.reload(); return; }
      try { metaBox.checked = localStorage.getItem("palaterm_cmdset_meta") === "1"; } catch (e) {}
      try { topBox.checked = localStorage.getItem("palaterm_cmdset_ontop") === "1"; } catch (e) {}
      C().SetOnTop(topBox.checked).catch(() => {});
      zoom.reload();
      setTimeout(() => body.focus(), 0);
    }
    st = s;
    sel = new Set([...sel].filter(i => i < cmds().length));
    if (anchor >= cmds().length) anchor = -1;
    lastQ = ""; hits = []; hitIdx = -1;
    render();
    if (fin.value && !bar.classList.contains("hidden")) findNext(1);
  }
  rt().EventsOn("cmdset:update", applyUpdate);
  render();
  body.focus();
  // A set that arrived between Load() above and the listener just added (a
  // spare shown within a moment of starting) was emitted to nobody: ask
  // again now that the listener is there. The same set twice is harmless.
  if (!(st.doc && st.doc.name)) {
    try {
      const again = await C().Load();
      if (again.doc && again.doc.name) applyUpdate(again);
    } catch (e) {}
  }
}
