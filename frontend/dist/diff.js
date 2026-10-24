// Diff window (child process: PalaTerm.exe --diff <A> <B> [label]). Two logs
// side by side the way WinMerge shows them: changed lines yellow, lines only
// in A red, only in B green, placeholders where one side has nothing, a
// location bar on the left marking every difference, and 前の差分 / 次の差分
// to walk them. Loaded in every window; only runs when DiffWin is bound.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

async function bootDiffWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "var(--bg)";
  const D = () => window.go.main.DiffWin;
  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:var(--bg);font-family:'Segoe UI',sans-serif">
    <div style="display:flex;align-items:center;gap:8px;padding:6px 12px;border-bottom:1px solid var(--border);flex-wrap:wrap">
      <div id="dw-title" style="color:var(--text);font-size:13px;font-weight:600;white-space:nowrap"></div>
      <div id="dw-verdict" style="font-size:13px;font-weight:600;padding:3px 10px;border-radius:999px;white-space:nowrap"></div>
      <div style="flex:1"></div>
      <button class="btn sm" id="dw-prev" data-tip="${esc(t("前の差分（Alt+↑ / F7）"))}">▲ ${esc(t("前の差分"))}</button>
      <span id="dw-pos" class="muted" style="font-size:12px;min-width:80px;text-align:center"></span>
      <button class="btn sm" id="dw-next" data-tip="${esc(t("次の差分（Alt+↓ / F8）"))}">▼ ${esc(t("次の差分"))}</button>
      <label style="display:flex;align-items:center;gap:5px;color:#c7d0dc;font-size:12px;white-space:nowrap"><input type="checkbox" id="dw-noise" checked style="width:auto"> ${esc(t("時刻・カウンタの違いを無視"))}</label>
      <label style="display:flex;align-items:center;gap:5px;color:#c7d0dc;font-size:12px;white-space:nowrap"><input type="checkbox" id="dw-only" style="width:auto"> ${esc(t("差分の周辺だけ表示"))}</label>
      <button class="btn sm" id="dw-color" data-tip="${esc(t("文字色・背景色を変えます（この PC のすべての差分窓に反映）"))}">${esc(t("色"))}</button>
      <button class="btn sm" id="dw-font-dec" data-tip="${esc(t("文字を小さく（Ctrl+-）"))}">A−</button>
      <button class="btn sm" id="dw-font-inc" data-tip="${esc(t("文字を大きく（Ctrl++）"))}">A+</button>
      <button class="btn sm" id="dw-find" data-tip="${esc(t("検索（Ctrl+F）"))}">${esc(t("検索"))}</button>
    </div>
    <div id="dw-findbar" class="hidden" style="display:flex;align-items:center;gap:6px;padding:4px 12px;background:#161c25;border-bottom:1px solid var(--border);font-size:12px">
      <input id="dw-find-in" placeholder="${esc(t("検索（Enter=次 / Shift+Enter=前 / Esc=閉じる）"))}" style="flex:1;max-width:420px;background:#0b0e13;color:var(--text);border:1px solid var(--border);border-radius:6px;padding:4px 8px;font-size:12px">
      <span id="dw-find-n" class="muted" style="min-width:70px"></span>
      <button class="btn sm" id="dw-find-prev" data-tip="${esc(t("前を検索（Shift+Enter）"))}">▲</button>
      <button class="btn sm" id="dw-find-next" data-tip="${esc(t("次を検索（Enter）"))}">▼</button>
      <button class="btn sm" id="dw-find-close" data-tip="${esc(t("閉じる（Esc）"))}">✕</button>
    </div>
    <div style="display:flex;align-items:stretch;border-bottom:1px solid var(--border);font-size:12px">
      <div style="width:22px"></div>
      <div class="dw-head" style="flex:1;padding:4px 10px;background:var(--panel);border-right:1px solid var(--border);display:flex;gap:8px;align-items:center"><span class="badge stage-before">A</span><span id="dw-name-a" style="color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1"></span><button class="btn sm" id="dw-open-a">${esc(t("ログ表示"))}</button></div>
      <div class="dw-head" style="flex:1;padding:4px 10px;background:var(--panel);display:flex;gap:8px;align-items:center"><span class="badge stage-after">B</span><span id="dw-name-b" style="color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1"></span><button class="btn sm" id="dw-open-b">${esc(t("ログ表示"))}</button></div>
    </div>
    <div style="flex:1;min-height:0;display:flex">
      <div id="dw-map" data-tip="${esc(t("クリックでその位置へ"))}" style="width:22px;position:relative;background:#0b0e13;border-right:1px solid var(--border);cursor:pointer"></div>
      <div id="dw-body" class="dw-body" style="flex:1;overflow:auto;background:#0b0e13"></div>
    </div>
    <div id="dw-status" class="muted" style="font-size:11px;padding:3px 12px;border-top:1px solid var(--border);display:flex;gap:18px">
      <span><i class="dw-sw" style="background:rgba(250,204,21,.35)"></i> ${esc(t("変更された行"))}</span>
      <span><i class="dw-sw" style="background:rgba(239,68,68,.35)"></i> ${esc(t("A にだけある行"))}</span>
      <span><i class="dw-sw" style="background:rgba(34,197,94,.35)"></i> ${esc(t("B にだけある行"))}</span>
      <span id="dw-counts" style="flex:1;text-align:right"></span>
    </div>
  </div>`);
  document.body.appendChild(wrap);
  const body = document.getElementById("dw-body"), map = document.getElementById("dw-map");
  // 表示の色 (colors.js): the text area's colours; on a light background
  // the highlight colours switch to their dark-text versions (dw-light).
  const applyColors = c => {
    body.style.background = c.bg; map.style.background = c.bg;
    body.style.setProperty("--dw-fg", c.fg);
    body.classList.toggle("dw-light", isLightColor(c.bg));
  };
  applyColors(screenColors("diff"));
  onScreenColors("diff", applyColors);
  document.getElementById("dw-color").onclick = e => openScreenColorPanel("diff", e.currentTarget, applyColors);
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
      const color = b.del && b.add ? "#facc15" : b.del ? "var(--err)" : "var(--ok)";
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
  // Find: a text in either side (case-insensitive); the row is scrolled to
  // and marked. Fifty changed blocks of a running-config are walked by the
  // interface name this way, not one by one.
  const fbar = document.getElementById("dw-findbar"), fin = document.getElementById("dw-find-in"), fn = document.getElementById("dw-find-n");
  let fMatches = [], fIdx = -1, fLast = "";
  function fCollect(q) { const lq = q.toLowerCase(), res = []; if (!lq) return res; rows.forEach((r, i) => { if ((r.a || "").toLowerCase().includes(lq) || (r.b || "").toLowerCase().includes(lq)) res.push(i); }); return res; }
  function fShow() {
    body.querySelectorAll("tr.dw-found").forEach(tr => tr.classList.remove("dw-found"));
    if (!fMatches.length) { fn.textContent = fLast ? t("該当なし") : ""; return; }
    const i = fMatches[fIdx];
    let tr = body.querySelector(`tr[data-i="${i}"]`);
    if (!tr && document.getElementById("dw-only").checked) { document.getElementById("dw-only").checked = false; render(); tr = body.querySelector(`tr[data-i="${i}"]`); }
    if (tr) { tr.classList.add("dw-found"); body.scrollTop = Math.max(0, tr.offsetTop - body.clientHeight / 3); }
    fn.textContent = `${fIdx + 1} / ${fMatches.length}`;
  }
  function fNext(dir) { const q = fin.value; if (q !== fLast) { fLast = q; fMatches = fCollect(q); fIdx = dir >= 0 ? -1 : 0; } if (!fMatches.length) { fShow(); return; } fIdx = (fIdx + dir + fMatches.length) % fMatches.length; fShow(); fin.focus(); }
  function openFind() { fbar.classList.remove("hidden"); fin.focus(); fin.select(); }
  function closeFind() { fbar.classList.add("hidden"); body.querySelectorAll("tr.dw-found").forEach(tr => tr.classList.remove("dw-found")); }
  document.getElementById("dw-find").onclick = openFind;
  document.getElementById("dw-find-next").onclick = () => fNext(1);
  document.getElementById("dw-find-prev").onclick = () => fNext(-1);
  document.getElementById("dw-find-close").onclick = closeFind;
  fin.addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); fNext(e.shiftKey ? -1 : 1); } else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); closeFind(); } });
  fin.addEventListener("input", () => { fLast = ""; fn.textContent = ""; });
  async function load() {
    const noise = document.getElementById("dw-noise").checked;
    doc = await D().Load(noise);
    document.getElementById("dw-title").textContent = (doc.label ? doc.label + "　" : "") + t("差分");
    document.getElementById("dw-name-a").textContent = doc.nameA; document.getElementById("dw-name-a").dataset.tip = doc.pathA;
    document.getElementById("dw-name-b").textContent = doc.nameB; document.getElementById("dw-name-b").dataset.tip = doc.pathB;
    if (doc.err) { body.innerHTML = `<div style="padding:14px;color:var(--err)">${esc(terr(doc.err))}</div>`; return; }
    const al = align(doc.result.lines || []);
    rows = al.out; blocks = al.bl; cur = blocks.length ? 0 : -1;
    const r = doc.result;
    const changed = r.added + r.removed > 0;
    const v = document.getElementById("dw-verdict");
    v.textContent = changed ? t("差分あり: 追加 {a} 行 / 削除 {b} 行 / {c} か所", { a: r.added, b: r.removed, c: blocks.length }) : t("差分なし（A と B は同じ内容）");
    v.style.background = changed ? "rgba(250,204,21,.18)" : "rgba(34,197,94,.18)";
    v.style.color = changed ? "#fcd34d" : "#86efac";
    // approx: the two sides were too large or too different for the exact
    // comparison, and the lines were matched one for one instead.
    document.getElementById("dw-counts").textContent = t("同じ {c} 行", { c: r.same }) + (noise ? "　" + t("（空行とプロンプトだけの行は比較から除外）") : "")
      + (r.approx ? "　" + t("（大きすぎるため概算: 行をそのまま突き合わせています）") : "");
    render();
    if (cur >= 0) setTimeout(() => goTo(0), 0);
  }
  document.getElementById("dw-noise").onchange = load;
  document.getElementById("dw-only").onchange = () => { render(); if (cur >= 0) goTo(cur); };
  document.getElementById("dw-open-a").onclick = () => D().OpenLog("a").catch(e => toast(t("ログを開けません") + ": " + terr(e), "err"));
  document.getElementById("dw-open-b").onclick = () => D().OpenLog("b").catch(e => toast(t("ログを開けません") + ": " + terr(e), "err"));
  // Font size (wireFontZoom, app.js).
  const zoom = wireFontZoom("palaterm_diff_font", px => { body.style.fontSize = px + "px"; updateView(); }, { def: 12 });
  zoom.set(zoom.size);
  zoom.buttons(document.getElementById("dw-font-inc"), document.getElementById("dw-font-dec"));
  zoom.wheel(body);
  document.addEventListener("keydown", e => {
    if ((e.altKey && e.key === "ArrowDown") || e.key === "F8") { e.preventDefault(); goTo(cur + 1); return; }
    if ((e.altKey && e.key === "ArrowUp") || e.key === "F7") { e.preventDefault(); goTo(cur - 1); return; }
    if (zoom.key(e)) { e.preventDefault(); return; }
    if (e.ctrlKey && (e.key === "f" || e.key === "F" || e.code === "KeyF")) { e.preventDefault(); openFind(); return; }
    if (e.key === "Escape" && !document.getElementById("sc-pop")) { if (!fbar.classList.contains("hidden")) closeFind(); else D().Close(); }
  });
  await load();
}
