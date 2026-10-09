// Diff window (child process: PalaTerm.exe --diff <A> <B> [label]). Two logs
// side by side the way WinMerge shows them: changed lines yellow, lines only
// in A red, only in B green, placeholders where one side has nothing, a
// location bar on the left marking every difference, and 前の差分 / 次の差分
// to walk them. Loaded in every window; only runs when DiffWin is bound.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

async function bootDiffWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "#11151c";
  const D = () => window.go.main.DiffWin;
  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:#11151c;font-family:'Segoe UI',sans-serif">
    <div style="display:flex;align-items:center;gap:8px;padding:6px 12px;border-bottom:1px solid #2a3442;flex-wrap:wrap">
      <div id="dw-title" style="color:#e6ecf3;font-size:13px;font-weight:600;white-space:nowrap"></div>
      <div id="dw-verdict" style="font-size:13px;font-weight:600;padding:3px 10px;border-radius:999px;white-space:nowrap"></div>
      <div style="flex:1"></div>
      <button class="btn sm" id="dw-prev" title="Alt+↑ / F7">▲ ${esc(t("前の差分"))}</button>
      <span id="dw-pos" class="muted" style="font-size:12px;min-width:80px;text-align:center"></span>
      <button class="btn sm" id="dw-next" title="Alt+↓ / F8">▼ ${esc(t("次の差分"))}</button>
      <label style="display:flex;align-items:center;gap:5px;color:#c7d0dc;font-size:12px;white-space:nowrap"><input type="checkbox" id="dw-noise" checked style="width:auto"> ${esc(t("時刻・カウンタの違いを無視"))}</label>
      <label style="display:flex;align-items:center;gap:5px;color:#c7d0dc;font-size:12px;white-space:nowrap"><input type="checkbox" id="dw-only" style="width:auto"> ${esc(t("差分の周辺だけ表示"))}</label>
      <button class="btn sm" id="dw-font-dec" title="Ctrl+-">A−</button>
      <button class="btn sm" id="dw-font-inc" title="Ctrl++">A＋</button>
    </div>
    <div style="display:flex;align-items:stretch;border-bottom:1px solid #2a3442;font-size:12px">
      <div style="width:22px"></div>
      <div class="dw-head" style="flex:1;padding:4px 10px;background:#1a212b;border-right:1px solid #2a3442;display:flex;gap:8px;align-items:center"><span class="badge stage-before">A</span><span id="dw-name-a" style="color:#e6ecf3;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1"></span><button class="btn sm" id="dw-open-a">${esc(t("ログ表示"))}</button></div>
      <div class="dw-head" style="flex:1;padding:4px 10px;background:#1a212b;display:flex;gap:8px;align-items:center"><span class="badge stage-after">B</span><span id="dw-name-b" style="color:#e6ecf3;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1"></span><button class="btn sm" id="dw-open-b">${esc(t("ログ表示"))}</button></div>
    </div>
    <div style="flex:1;min-height:0;display:flex">
      <div id="dw-map" title="${esc(t("クリックでその位置へ"))}" style="width:22px;position:relative;background:#0b0e13;border-right:1px solid #2a3442;cursor:pointer"></div>
      <div id="dw-body" class="dw-body" style="flex:1;overflow:auto;background:#0b0e13"></div>
    </div>
    <div id="dw-status" class="muted" style="font-size:11px;padding:3px 12px;border-top:1px solid #2a3442;display:flex;gap:18px">
      <span><i class="dw-sw" style="background:rgba(250,204,21,.35)"></i> ${esc(t("変更された行"))}</span>
      <span><i class="dw-sw" style="background:rgba(239,68,68,.35)"></i> ${esc(t("A にだけある行"))}</span>
      <span><i class="dw-sw" style="background:rgba(34,197,94,.35)"></i> ${esc(t("B にだけある行"))}</span>
      <span id="dw-counts" style="flex:1;text-align:right"></span>
    </div>
  </div>`);
  document.body.appendChild(wrap);
  const body = document.getElementById("dw-body"), map = document.getElementById("dw-map");
  let doc = null, rows = [], blocks = [], cur = -1;

  // Fold the edit script into side-by-side rows: a run of removed lines
  // followed by added lines is one changed block shown row for row.
  function align(lines) {
    const out = []; const bl = []; let i = 0;
    while (i < lines.length) {
      if (lines[i].kind === "=") { out.push({ kind: "same", a: lines[i].a, b: lines[i].b, la: lines[i].lineA, lb: lines[i].lineB }); i++; continue; }
      const del = [], add = [];
      while (i < lines.length && lines[i].kind !== "=") { (lines[i].kind === "-" ? del : add).push(lines[i]); i++; }
      const start = out.length, n = Math.max(del.length, add.length);
      for (let k = 0; k < n; k++) {
        const d = del[k], a = add[k];
        out.push({ kind: d && a ? "changed" : d ? "deleted" : "added", a: d ? d.a : null, b: a ? a.b : null, la: d ? d.lineA : 0, lb: a ? a.lineB : 0, block: bl.length });
      }
      bl.push({ start, end: out.length - 1, del: del.length, add: add.length });
    }
    return { out, bl };
  }
  function render() {
    if (!doc) return;
    const only = document.getElementById("dw-only").checked;
    const keep = rows.map(() => !only);
    if (only) { const ctx = 3; rows.forEach((r, i) => { if (r.kind !== "same") for (let j = Math.max(0, i - ctx); j <= Math.min(rows.length - 1, i + ctx); j++) keep[j] = true; }); }
    const html = []; let skip = 0;
    const flush = () => { if (skip) { html.push(`<tr class="dw-skip"><td colspan="4">${esc(t("… {n} 行同じ …", { n: skip }))}</td></tr>`); skip = 0; } };
    rows.forEach((r, i) => {
      if (!keep[i]) { skip++; return; }
      flush();
      const ph = `<td class="dw-ln"></td><td class="dw-ph"></td>`;
      const left = r.a === null ? ph : `<td class="dw-ln">${r.la || ""}</td><td class="dw-txt">${esc(r.a)}</td>`;
      const right = r.b === null ? ph : `<td class="dw-ln">${r.lb || ""}</td><td class="dw-txt">${esc(r.b)}</td>`;
      html.push(`<tr class="dw-${r.kind}" data-i="${i}" ${r.block !== undefined ? `data-block="${r.block}"` : ""}>${left}${right}</tr>`);
    });
    flush();
    body.innerHTML = rows.length ? `<table class="dw-table"><colgroup><col style="width:52px"><col style="width:calc(50% - 52px)"><col style="width:52px"><col style="width:calc(50% - 52px)"></colgroup><tbody>${html.join("")}</tbody></table>`
      : `<div class="muted" style="padding:14px">${esc(t("差分はありません"))}</div>`;
    // Location bar: one marker per block, placed by row position.
    map.innerHTML = blocks.map((b, k) => {
      const top = (b.start / Math.max(1, rows.length)) * 100, hgt = Math.max(0.4, ((b.end - b.start + 1) / Math.max(1, rows.length)) * 100);
      const color = b.del && b.add ? "#facc15" : b.del ? "#ef4444" : "#22c55e";
      return `<div class="dw-mark" data-k="${k}" style="top:${top}%;height:${hgt}%;background:${color}"></div>`;
    }).join("") + `<div id="dw-view" style="position:absolute;left:0;right:0;border:1px solid rgba(255,255,255,.35);pointer-events:none"></div>`;
    map.querySelectorAll(".dw-mark").forEach(m => m.onclick = e => { e.stopPropagation(); goTo(parseInt(m.dataset.k, 10)); });
    updateView();
    markCurrent();
  }
  function updateView() {
    const v = document.getElementById("dw-view"); if (!v) return;
    const total = body.scrollHeight || 1;
    v.style.top = (body.scrollTop / total * 100) + "%";
    v.style.height = (body.clientHeight / total * 100) + "%";
  }
  body.addEventListener("scroll", updateView);
  map.addEventListener("click", e => {
    const frac = (e.clientY - map.getBoundingClientRect().top) / map.clientHeight;
    body.scrollTop = frac * body.scrollHeight - body.clientHeight / 2;
  });
  function markCurrent() {
    body.querySelectorAll("tr.dw-cur").forEach(tr => tr.classList.remove("dw-cur"));
    if (cur >= 0) body.querySelectorAll(`tr[data-block="${cur}"]`).forEach(tr => tr.classList.add("dw-cur"));
    document.getElementById("dw-pos").textContent = blocks.length ? `${cur + 1} / ${blocks.length}` : t("差分なし");
    map.querySelectorAll(".dw-mark").forEach(m => m.classList.toggle("cur", parseInt(m.dataset.k, 10) === cur));
  }
  function goTo(k) {
    if (!blocks.length) return;
    cur = (k + blocks.length) % blocks.length;
    markCurrent();
    const tr = body.querySelector(`tr[data-block="${cur}"]`);
    if (tr) body.scrollTop = Math.max(0, tr.offsetTop - body.clientHeight / 3);
  }
  document.getElementById("dw-next").onclick = () => goTo(cur + 1);
  document.getElementById("dw-prev").onclick = () => goTo(cur - 1);
  async function load() {
    const noise = document.getElementById("dw-noise").checked;
    doc = await D().Load(noise);
    document.getElementById("dw-title").textContent = (doc.label ? doc.label + "　" : "") + t("差分");
    document.getElementById("dw-name-a").textContent = doc.nameA; document.getElementById("dw-name-a").title = doc.pathA;
    document.getElementById("dw-name-b").textContent = doc.nameB; document.getElementById("dw-name-b").title = doc.pathB;
    if (doc.err) { body.innerHTML = `<div style="padding:14px;color:var(--err)">${esc(terr(doc.err))}</div>`; return; }
    const al = align(doc.result.lines || []);
    rows = al.out; blocks = al.bl; cur = blocks.length ? 0 : -1;
    const r = doc.result;
    const changed = r.added + r.removed > 0;
    const v = document.getElementById("dw-verdict");
    v.textContent = changed ? t("差分あり: 追加 {a} 行 / 削除 {b} 行 / {c} か所", { a: r.added, b: r.removed, c: blocks.length }) : t("差分なし（A と B は同じ内容）");
    v.style.background = changed ? "rgba(250,204,21,.18)" : "rgba(34,197,94,.18)";
    v.style.color = changed ? "#fcd34d" : "#86efac";
    document.getElementById("dw-counts").textContent = t("同じ {c} 行", { c: r.same }) + (noise ? "　" + t("（空行とプロンプトだけの行は比較から除外）") : "");
    render();
    if (cur >= 0) setTimeout(() => goTo(0), 0);
  }
  document.getElementById("dw-noise").onchange = load;
  document.getElementById("dw-only").onchange = () => { render(); if (cur >= 0) goTo(cur); };
  document.getElementById("dw-open-a").onclick = () => D().OpenLog("a").catch(() => {});
  document.getElementById("dw-open-b").onclick = () => D().OpenLog("b").catch(() => {});
  // Font size.
  const FONT_DEF = 12, FONT_MIN = 8, FONT_MAX = 32;
  let fontSize = FONT_DEF;
  try { fontSize = Math.min(FONT_MAX, Math.max(FONT_MIN, parseInt(localStorage.getItem("palaterm_diff_font"), 10) || FONT_DEF)); } catch (e) {}
  function setFont(n) { fontSize = Math.min(FONT_MAX, Math.max(FONT_MIN, n)); body.style.fontSize = fontSize + "px"; try { localStorage.setItem("palaterm_diff_font", String(fontSize)); } catch (e) {} updateView(); }
  setFont(fontSize);
  document.getElementById("dw-font-inc").onclick = () => setFont(fontSize + 1);
  document.getElementById("dw-font-dec").onclick = () => setFont(fontSize - 1);
  body.addEventListener("wheel", e => { if (!e.ctrlKey) return; e.preventDefault(); setFont(fontSize + (e.deltaY < 0 ? 1 : -1)); }, { passive: false });
  document.addEventListener("keydown", e => {
    if ((e.altKey && e.key === "ArrowDown") || e.key === "F8") { e.preventDefault(); goTo(cur + 1); return; }
    if ((e.altKey && e.key === "ArrowUp") || e.key === "F7") { e.preventDefault(); goTo(cur - 1); return; }
    if (e.ctrlKey && (e.key === "+" || e.key === "=" || e.code === "Equal" || e.code === "NumpadAdd")) { e.preventDefault(); setFont(fontSize + 1); return; }
    if (e.ctrlKey && (e.key === "-" || e.code === "Minus" || e.code === "NumpadSubtract")) { e.preventDefault(); setFont(fontSize - 1); return; }
    if (e.ctrlKey && (e.key === "0" || e.code === "Digit0" || e.code === "Numpad0")) { e.preventDefault(); setFont(FONT_DEF); return; }
    if (e.key === "Escape") { D().Close(); }
  });
  await load();
}
