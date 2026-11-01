// Diff window (child process: PalaTerm.exe --diff <A> <B> [label]). Two logs
// side by side the way WinMerge shows them: changed lines yellow with the
// changed part lit, lines only in A red, only in B green, blocks that moved
// marked 順序, placeholders where one side has nothing, a location bar on
// the left marking every difference, and 前の差分 / 次の差分 to walk them.
// The comparison itself is history.DiffWith (Go): blocks paired by their
// heading, the configuration listing alone when both logs have one
// (範囲: 設定だけ), secrets and noise left out and counted. Loaded in every
// window; only runs when DiffWin is bound.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

async function bootDiffWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "var(--bg)";
  const D = () => window.go.main.DiffWin;
  // Options remembered per PC: what is left out, the range, the folding.
  const OPTS_KEY = "palaterm_diff_opts";
  let opts = { noise: true, secrets: true, configOnly: true, fold: true };
  try { opts = Object.assign(opts, JSON.parse(localStorage.getItem(OPTS_KEY) || "{}")); } catch (e) { /* defaults */ }
  const saveOpts = () => { try { localStorage.setItem(OPTS_KEY, JSON.stringify(opts)); } catch (e) { /* ignore */ } };
  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:var(--bg);font-family:'Segoe UI',sans-serif">
    <div style="display:flex;align-items:center;gap:8px;padding:6px 12px;border-bottom:1px solid var(--border);flex-wrap:wrap">
      <div id="dw-title" style="color:var(--text);font-size:13px;font-weight:600;white-space:nowrap"></div>
      <div id="dw-verdict" style="font-size:13px;font-weight:600;padding:3px 10px;border-radius:999px;white-space:nowrap"></div>
      <button class="btn sm hidden" id="dw-impact" data-tip="${esc(t("設定の変更の影響の大きさ（読む順番の目安）と、変わったオブジェクトを参照している行。クリックで一覧")) }"></button>
      <div id="dw-writing" class="hidden" style="font-size:12px;color:var(--warn);white-space:nowrap">${esc(t("⚠ 取得中のログです（内容が増える可能性があります）"))}</div>
      <div style="flex:1"></div>
      <button class="btn sm" id="dw-prev" data-tip="${esc(t("前の差分（Alt+↑ / F7）"))}">▲ ${esc(t("前の差分"))}</button>
      <span id="dw-pos" class="muted" style="font-size:12px;min-width:80px;text-align:center"></span>
      <button class="btn sm" id="dw-next" data-tip="${esc(t("次の差分（Alt+↓ / F8）"))}">▼ ${esc(t("次の差分"))}</button>
      <button class="btn sm" id="dw-color" data-tip="${esc(t("文字色・背景色を変えます（この PC のすべての差分窓に反映）"))}">${esc(t("色"))}</button>
      <button class="btn sm" id="dw-font-dec" data-tip="${esc(t("文字を小さく（Ctrl+-）"))}">A−</button>
      <button class="btn sm" id="dw-font-inc" data-tip="${esc(t("文字を大きく（Ctrl++）"))}">A+</button>
      <button class="btn sm" id="dw-find" data-tip="${esc(t("検索（Ctrl+F）"))}">${esc(t("検索"))}</button>
      <button class="btn sm" id="dw-cmd-back" data-tip="${esc(t("B の機器を A の設定に戻すコマンドを差分から作って右に表示します（コピー用。実行はしません）"))}">${esc(t("戻す"))}</button>
      <button class="btn sm" id="dw-cmd-fwd" data-tip="${esc(t("A の機器を B と同じにするコマンドを差分から作って右に表示します（コピー用。実行はしません）"))}">${esc(t("揃える"))}</button>
    </div>
    <div style="display:flex;align-items:center;gap:10px;padding:4px 12px;border-bottom:1px solid var(--border);flex-wrap:wrap;font-size:12px;color:#c7d0dc">
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap">${esc(t("範囲"))}
        <select id="dw-range" style="width:auto;font-size:12px;padding:2px 6px"><option value="config">${esc(t("設定だけ（show running-config 等）"))}</option><option value="whole">${esc(t("ログ全体"))}</option></select></label>
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap">${esc(t("セクション"))}
        <select id="dw-sec" style="width:auto;max-width:260px;font-size:12px;padding:2px 6px"></select></label>
      <label id="dw-cmd-wrap" style="display:flex;align-items:center;gap:5px;white-space:nowrap">${esc(t("コマンド"))}
        <select id="dw-cmd" style="width:auto;max-width:260px;font-size:12px;padding:2px 6px"></select></label>
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap" data-tip="${esc(t("選んだセクション／コマンドの範囲だけを表示します"))}"><input type="checkbox" id="dw-filter" style="width:auto"> ${esc(t("絞込"))}</label>
      <span style="flex:1"></span>
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap"><input type="checkbox" id="dw-fold" style="width:auto"> ${esc(t("同じ部分を折りたたむ"))}</label>
      <button class="btn sm" id="dw-expand" data-tip="${esc(t("折りたたんだ部分をすべて開く／すべて畳む"))}">${esc(t("すべて展開"))}</button>
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap"><input type="checkbox" id="dw-noise" style="width:auto"> ${esc(t("時刻・カウンタの違いを無視"))}</label>
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap" data-tip="${esc(t("証明書・暗号化された値・UUID の行は比較から外し、薄く表示します（外した行数は下に出ます）"))}"><input type="checkbox" id="dw-secrets" style="width:auto"> ${esc(t("証明書・暗号化値・UUID の違いを無視"))}</label>
    </div>
    <div id="dw-findbar" class="hidden" style="display:flex;align-items:center;gap:6px;padding:4px 12px;background:#161c25;border-bottom:1px solid var(--border);font-size:12px">
      <input id="dw-find-in" placeholder="${esc(t("検索（Enter=次 / Shift+Enter=前 / Esc=閉じる）"))}" style="flex:1;max-width:420px;background:#0b0e13;color:var(--text);border:1px solid var(--border);border-radius:6px;padding:4px 8px;font-size:12px">
      <span id="dw-find-n" class="muted" style="min-width:70px"></span>
      <button class="btn sm" id="dw-find-prev" data-tip="${esc(t("前を検索（Shift+Enter）"))}">▲</button>
      <button class="btn sm" id="dw-find-next" data-tip="${esc(t("次を検索（Enter）"))}">▼</button>
      <button class="btn sm" id="dw-find-close" data-tip="${esc(t("閉じる（Esc）"))}">✕</button>
    </div>
    <div id="dw-note" class="hidden" style="font-size:12px;padding:4px 12px;border-bottom:1px solid var(--border);color:var(--warn)"></div>
    <div style="display:flex;align-items:stretch;border-bottom:1px solid var(--border);font-size:12px">
      <div style="width:22px"></div>
      <div class="dw-head" style="flex:1;padding:4px 10px;background:var(--panel);border-right:1px solid var(--border);display:flex;gap:8px;align-items:center"><span class="badge stage-before">A</span><span id="dw-name-a" style="color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1"></span><button class="btn sm" id="dw-open-a">${esc(t("ログ表示"))}</button></div>
      <div class="dw-head" style="flex:1;padding:4px 10px;background:var(--panel);display:flex;gap:8px;align-items:center"><span class="badge stage-after">B</span><span id="dw-name-b" style="color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1"></span><button class="btn sm" id="dw-open-b">${esc(t("ログ表示"))}</button></div>
    </div>
    <div style="flex:1;min-height:0;display:flex">
      <div id="dw-map" data-tip="${esc(t("クリックでその位置へ"))}" style="width:22px;position:relative;background:#0b0e13;border-right:1px solid var(--border);cursor:pointer"></div>
      <div id="dw-body" class="dw-body" style="flex:1;overflow:auto;background:#0b0e13"></div>
      <div id="dw-cmdpane" class="hidden" style="width:38%;min-width:280px;border-left:1px solid var(--border);display:flex;flex-direction:column;background:var(--panel)">
        <div style="display:flex;align-items:center;gap:8px;padding:5px 10px;border-bottom:1px solid var(--border);font-size:12px">
          <b id="dw-cmd-title" style="color:var(--text)"></b><span style="flex:1"></span>
          <button class="btn sm" id="dw-cmd-copy">${esc(t("コピー"))}</button>
          <button class="btn sm" id="dw-cmd-close" data-tip="${esc(t("閉じる"))}">✕</button>
        </div>
        <div id="dw-cmd-note" class="muted" style="font-size:11px;padding:4px 10px;border-bottom:1px solid var(--border)"></div>
        <pre id="dw-cmd-text" class="mono" style="flex:1;margin:0;padding:8px 10px;overflow:auto;font-size:12px;white-space:pre;color:var(--text)"></pre>
      </div>
    </div>
    <div id="dw-status" class="muted" style="font-size:11px;padding:3px 12px;border-top:1px solid var(--border);display:flex;gap:18px;flex-wrap:wrap">
      <span><i class="dw-sw" style="background:rgba(250,204,21,.35)"></i> ${esc(t("変更された行"))}</span>
      <span><i class="dw-sw" style="background:rgba(239,68,68,.35)"></i> ${esc(t("A にだけある行"))}</span>
      <span><i class="dw-sw" style="background:rgba(34,197,94,.35)"></i> ${esc(t("B にだけある行"))}</span>
      <span><i class="dw-sw" style="background:rgba(167,139,250,.45)"></i> ${esc(t("順序が変わったブロック"))}</span>
      <span id="dw-chunk" style="color:var(--warn)"></span>
      <span id="dw-counts" style="flex:1;text-align:right"></span>
    </div>
  </div>`);
  document.body.appendChild(wrap);
  const $ = id => document.getElementById(id);
  const body = $("dw-body"), map = $("dw-map");
  // 表示の色 (colors.js): the text area's colours; on a light background
  // the highlight colours switch to their dark-text versions (dw-light).
  const applyColors = c => {
    body.style.background = c.bg; map.style.background = c.bg;
    body.style.setProperty("--dw-fg", c.fg);
    body.classList.toggle("dw-light", isLightColor(c.bg));
  };
  applyColors(screenColors("diff"));
  onScreenColors("diff", applyColors);
  $("dw-color").onclick = e => openScreenColorPanel("diff", e.currentTarget, applyColors);
  $("dw-noise").checked = opts.noise; $("dw-secrets").checked = opts.secrets; $("dw-fold").checked = opts.fold;
  $("dw-range").value = opts.configOnly ? "config" : "whole";

  let doc = null, rows = [], blocks = [], cur = -1;
  // folds: start row → end row (inclusive) of each run of equal rows that
  // is folded; expanded holds the keys ("la:lb" of the first row) opened
  // by a click. filter: the row range shown by 絞込, or null.
  let folds = [], expanded = new Set(), filter = null, jumpRow = -1;
  const FOLD_MIN = 9, FOLD_CTX = 3;

  // Rows: one per line of the result. A changed line ("~") is one row with
  // its lit segments; a moved heading is an equal row flagged 順序.
  function build(lines) {
    const out = []; const bl = [];
    const kindOf = l => l.noise ? "noise" : l.kind === "=" ? (l.moved ? "moved" : "same") : l.kind === "~" ? "changed" : l.kind === "-" ? "deleted" : "added";
    let open = null;
    lines.forEach(l => {
      const r = { kind: kindOf(l), a: l.kind === "+" ? null : (l.a || ""), b: l.kind === "-" ? null : (l.b || ""), la: l.lineA || 0, lb: l.lineB || 0, segA: l.segA, segB: l.segB, head: !!l.head, moved: !!l.moved, noise: !!l.noise };
      if (r.noise) { r.a = l.a || null; r.b = l.b || null; }
      const i = out.length;
      if (r.kind === "same" || r.kind === "noise") open = null;
      else {
        // A run of differing rows is one block; a moved heading is a block
        // of its own (its lines are equal).
        if (r.kind === "moved" || !open || open.kind === "moved") { open = { start: i, end: i, kind: r.kind }; bl.push(open); }
        else open.end = i;
        r.block = bl.length - 1;
      }
      out.push(r);
    });
    return { out, bl };
  }
  const foldKey = i => `${rows[i].la}:${rows[i].lb}`;
  // The runs of equal rows long enough to fold (FOLD_CTX rows kept on each
  // side of a difference).
  function computeFolds() {
    folds = [];
    if (!opts.fold || filter) return;
    let i = 0;
    while (i < rows.length) {
      if (rows[i].kind !== "same" && rows[i].kind !== "noise") { i++; continue; }
      let j = i;
      while (j < rows.length && (rows[j].kind === "same" || rows[j].kind === "noise")) j++;
      const from = i === 0 ? i : i + FOLD_CTX, to = j === rows.length ? j - 1 : j - 1 - FOLD_CTX;
      if (to - from + 1 >= FOLD_MIN) folds.push({ start: from, end: to });
      i = j;
    }
  }
  const segHTML = (segs, text, kind) => segs ? segs.map(s => s.k === "=" ? esc(s.t) : `<span class="dw-seg-${s.k === "-" ? "del" : "add"}">${esc(s.t)}</span>`).join("") : esc(text);
  // Rows go into the page this many at a time (one chunk per animation
  // frame): a show tech-class log with the folding off is 100,000 rows,
  // and one innerHTML of them froze the window. done() runs once every
  // row is in (at once when there are few).
  const CHUNK = 2000;
  let renderSeq = 0;
  function render(done) {
    if (!doc) return;
    computeFolds();
    const foldAt = {}; folds.forEach(f => { if (!expanded.has(foldKey(f.start))) foldAt[f.start] = f; });
    const html = [];
    const lo = filter ? filter.from : 0, hi = filter ? filter.to : rows.length;
    if (filter) {
      const n = rows.slice(lo, hi).filter(r => r.block !== undefined).length;
      html.push(`<tr class="dw-banner"><td colspan="4">📂 ${esc(filter.name)} ─ ${esc(n ? t("変更 {n} 行（全 {m} 行を表示中）", { n, m: hi - lo }) : t("このセクションに差分はありません（全 {m} 行を表示中）", { m: hi - lo }))}</td></tr>`);
    }
    for (let i = lo; i < hi; i++) {
      const f = foldAt[i];
      if (f) {
        html.push(`<tr class="dw-skip" data-fold="${i}"><td colspan="4">${esc(t("… {n} 行同じ …（クリックで展開）", { n: f.end - f.start + 1 }))}</td></tr>`);
        i = f.end; continue;
      }
      const r = rows[i];
      const ph = `<td class="dw-ln"></td><td class="dw-ph"></td>`;
      const badge = r.moved ? `<span class="dw-moved-badge" data-tip="${esc(t("A と B で並び順が違うブロック"))}">${esc(t("順序"))}</span>` : "";
      const left = r.a === null ? ph : `<td class="dw-ln">${r.la || ""}</td><td class="dw-txt dw-a">${badge}${segHTML(r.segA, r.a, "-")}</td>`;
      const right = r.b === null ? ph : `<td class="dw-ln">${r.lb || ""}</td><td class="dw-txt dw-b">${segHTML(r.segB, r.b, "+")}</td>`;
      html.push(`<tr class="dw-${r.kind}${r.head ? " dw-head" : ""}" data-i="${i}" ${r.block !== undefined ? `data-block="${r.block}"` : ""}>${left}${right}</tr>`);
    }
    const seq = ++renderSeq;
    const chunkNote = $("dw-chunk");
    const table = first => `<table class="dw-table"><colgroup><col style="width:52px"><col style="width:calc(50% - 52px)"><col style="width:52px"><col style="width:calc(50% - 52px)"></colgroup><tbody>${first}</tbody></table>`;
    chunkNote.textContent = "";
    const wireSkips = () => body.querySelectorAll("tr.dw-skip").forEach(tr => { if (!tr.onclick) tr.onclick = () => { expanded.add(foldKey(parseInt(tr.dataset.fold, 10))); render(); }; });
    if (!rows.length) body.innerHTML = `<div class="muted" style="padding:14px">${esc(t("差分はありません"))}</div>`;
    else if (html.length <= CHUNK) body.innerHTML = table(html.join(""));
    else {
      body.innerHTML = table(html.slice(0, CHUNK).join(""));
      const tb = body.querySelector("tbody");
      const onDone = done;
      let at = CHUNK;
      const more = () => {
        if (seq !== renderSeq) return; // drawn again meanwhile
        tb.insertAdjacentHTML("beforeend", html.slice(at, at + CHUNK).join(""));
        at += CHUNK;
        wireSkips();
        if (at < html.length) { chunkNote.textContent = t("描画中… {a} / {b} 行", { a: at, b: html.length }); requestAnimationFrame(more); return; }
        chunkNote.textContent = "";
        updateView(); markCurrent();
        if (jumpRow >= 0) { const tr = body.querySelector(`tr[data-i="${jumpRow}"]`); if (tr) tr.classList.add("dw-jump"); }
        if (onDone) onDone();
      };
      chunkNote.textContent = t("描画中… {a} / {b} 行", { a: at, b: html.length });
      requestAnimationFrame(more);
      done = null; // called by the last chunk
    }
    wireSkips();
    $("dw-expand").textContent = folds.length && folds.every(f => expanded.has(foldKey(f.start))) ? t("すべて畳む") : t("すべて展開");
    $("dw-expand").disabled = !folds.length;
    // Location bar: one marker per block, placed by row position.
    const vis = blocks.filter(b => !filter || (b.start >= lo && b.start < hi));
    map.innerHTML = vis.map(b => {
      const k = blocks.indexOf(b);
      const top = ((b.start - lo) / Math.max(1, hi - lo)) * 100, hgt = Math.max(0.4, ((b.end - b.start + 1) / Math.max(1, hi - lo)) * 100);
      const color = b.kind === "moved" ? "#a78bfa" : b.kind === "changed" ? "#facc15" : b.kind === "deleted" ? "var(--err)" : "var(--ok)";
      return `<div class="dw-mark" data-k="${k}" style="top:${top}%;height:${hgt}%;background:${color}"></div>`;
    }).join("") + `<div id="dw-view" style="position:absolute;left:0;right:0;border:1px solid rgba(255,255,255,.35);pointer-events:none"></div>`;
    map.querySelectorAll(".dw-mark").forEach(m => m.onclick = e => { e.stopPropagation(); goTo(parseInt(m.dataset.k, 10)); });
    updateView();
    markCurrent();
    if (jumpRow >= 0) { const tr = body.querySelector(`tr[data-i="${jumpRow}"]`); if (tr) tr.classList.add("dw-jump"); }
    if (done) done();
  }
  function updateView() {
    const v = $("dw-view"); if (!v) return;
    const total = body.scrollHeight || 1;
    v.style.top = (body.scrollTop / total * 100) + "%";
    v.style.height = (body.clientHeight / total * 100) + "%";
  }
  body.addEventListener("scroll", updateView);
  map.addEventListener("click", e => {
    const frac = (e.clientY - map.getBoundingClientRect().top) / map.clientHeight;
    body.scrollTop = frac * body.scrollHeight - body.clientHeight / 2;
  });
  // The blocks the navigation walks: all, or those inside the 絞込 range.
  const navBlocks = () => filter ? blocks.map((b, k) => ({ b, k })).filter(x => x.b.start >= filter.from && x.b.start < filter.to).map(x => x.k) : blocks.map((b, k) => k);
  function markCurrent() {
    body.querySelectorAll("tr.dw-cur").forEach(tr => tr.classList.remove("dw-cur"));
    if (cur >= 0) body.querySelectorAll(`tr[data-block="${cur}"]`).forEach(tr => tr.classList.add("dw-cur"));
    const nav = navBlocks();
    $("dw-pos").textContent = nav.length ? `${nav.indexOf(cur) + 1} / ${nav.length}` : t("差分なし");
    map.querySelectorAll(".dw-mark").forEach(m => m.classList.toggle("cur", parseInt(m.dataset.k, 10) === cur));
  }
  // Scroll a row into the middle, opening the fold it sits in.
  function reveal(i) {
    const f = folds.find(f => i >= f.start && i <= f.end);
    if (f && !expanded.has(foldKey(f.start))) { expanded.add(foldKey(f.start)); render(); }
    const tr = body.querySelector(`tr[data-i="${i}"]`);
    if (tr) body.scrollTop = Math.max(0, tr.offsetTop - body.clientHeight / 3);
    return tr;
  }
  function goTo(k) {
    const nav = navBlocks();
    if (!nav.length) return;
    if (!nav.includes(k)) k = nav[0];
    cur = k;
    markCurrent();
    reveal(blocks[cur].start);
  }
  function step(dir) {
    const nav = navBlocks();
    if (!nav.length) return;
    const at = nav.indexOf(cur);
    goTo(nav[((at < 0 ? (dir > 0 ? -1 : 0) : at) + dir + nav.length) % nav.length]);
  }
  $("dw-next").onclick = () => step(1);
  $("dw-prev").onclick = () => step(-1);
  // Section / command jumps. The same item chosen again jumps again: the
  // select is reset to its placeholder when it opens.
  const promptRe = /^!?[A-Za-z0-9][\w.\-@~:/]*(?:\([^)]*\))?\s?[#>]\s*\S/;
  const rowText = r => (r.b !== null ? r.b : r.a) || "";
  function fillSelect(sel, items, placeholder) {
    const groups = new Map();
    items.forEach(j => { const g = items.length > 5 ? (j.group || "") : ""; if (!groups.has(g)) groups.set(g, []); groups.get(g).push(j); });
    let html = `<option value="">${esc(placeholder)}</option>`;
    for (const [g, list] of groups) {
      const opts = list.map(j => `<option value="${j.row}">${esc(j.name)}</option>`).join("");
      html += g ? `<optgroup label="${esc(g)}">${opts}</optgroup>` : opts;
    }
    sel.innerHTML = html;
    sel.disabled = !items.length;
  }
  // A section runs to the next line at its depth or above that is not one
  // of its closing lines ("!", "end", "next", "}"); a command's output to the
  // next prompt line.
  const closerRe = /^\s*(!|end|exit|next|quit|\}|#)\s*$/i;
  function rangeOf(row, isCmd) {
    const r = rows[row], depth = depthOf(r);
    let end = rows.length;
    for (let i = row + 1; i < rows.length; i++) {
      const x = rows[i], s = rowText(x);
      if (isCmd ? promptRe.test(s) : (depthOf(x) <= depth && !closerRe.test(s) && s.trim() !== "")) { end = i; break; }
    }
    return { from: row, to: end };
  }
  const depthOf = r => { const s = rowText(r); return s.length - s.replace(/^[ \t]+/, "").length; };
  let picked = null; // { row, name, isCmd }
  // Show only the range of the picked section / command; the current
  // difference becomes the first one inside it.
  function applyFilter() {
    filter = Object.assign({ name: picked.name }, rangeOf(picked.row, picked.isCmd));
    render(); body.scrollTop = 0;
    const nav = navBlocks();
    cur = nav.length ? nav[0] : -1;
    markCurrent();
  }
  function jump(row, name, isCmd) {
    picked = { row, name, isCmd };
    jumpRow = row;
    if ($("dw-filter").checked) applyFilter();
    else { filter = null; render(); reveal(row); }
    setTimeout(() => { if (jumpRow === row) { jumpRow = -1; body.querySelectorAll("tr.dw-jump").forEach(tr => tr.classList.remove("dw-jump")); } }, 2500);
  }
  for (const [id, isCmd] of [["dw-sec", false], ["dw-cmd", true]]) {
    const sel = $(id);
    sel.addEventListener("mousedown", () => { sel.dataset.last = sel.value; sel.value = ""; });
    sel.addEventListener("blur", () => { if (!sel.value && sel.dataset.last) sel.value = sel.dataset.last; });
    sel.addEventListener("change", () => {
      if (!sel.value) return;
      const other = $(isCmd ? "dw-sec" : "dw-cmd"); other.value = ""; other.dataset.last = "";
      const row = parseInt(sel.value, 10);
      jump(row, sel.options[sel.selectedIndex].textContent, isCmd);
    });
  }
  $("dw-filter").onchange = () => {
    if ($("dw-filter").checked && picked) applyFilter();
    else if (!$("dw-filter").checked && filter) { const row = filter.from; filter = null; render(); reveal(row); markCurrent(); }
    else if (!picked) { toast(t("先にセクションかコマンドを選んでください"), "warn"); $("dw-filter").checked = false; }
  };
  $("dw-fold").onchange = () => { opts.fold = $("dw-fold").checked; saveOpts(); render(); if (cur >= 0) reveal(blocks[cur].start); };
  $("dw-expand").onclick = () => {
    if (folds.every(f => expanded.has(foldKey(f.start)))) expanded.clear(); else folds.forEach(f => expanded.add(foldKey(f.start)));
    render();
  };
  // One side at a time: pressing the mouse in A's column makes only A
  // selectable (and the other way round), so a drag takes one side.
  body.addEventListener("mousedown", e => {
    const td = e.target.closest("td"); if (!td) return;
    const side = td.classList.contains("dw-a") ? "a" : td.classList.contains("dw-b") ? "b" : "";
    if (side && body.dataset.side !== side) { body.dataset.side = side; const s = window.getSelection(); if (s) s.removeAllRanges(); }
  });
  function selectSide() {
    const side = body.dataset.side || "b";
    body.dataset.side = side;
    const tb = body.querySelector("tbody"); if (!tb) return;
    const s = window.getSelection(); if (!s) return;
    const range = document.createRange(); range.selectNodeContents(tb);
    s.removeAllRanges(); s.addRange(range);
  }
  const sideText = side => rows.filter(r => (side === "a" ? r.a : r.b) !== null).map(r => side === "a" ? r.a : r.b).join("\r\n");
  body.addEventListener("contextmenu", e => {
    e.preventDefault(); e.stopPropagation();
    const items = [];
    const picked = String(window.getSelection() || "");
    if (picked.trim()) items.push([t("選択した文字をコピー"), () => copyText(picked, t("コピーしました"))]);
    const tr = e.target.closest("tr"); const r = tr && tr.dataset.i !== undefined ? rows[parseInt(tr.dataset.i, 10)] : null;
    if (r && r.a !== null) items.push([t("この行をコピー（A）"), () => copyText(r.a, t("コピーしました"))]);
    if (r && r.b !== null) items.push([t("この行をコピー（B）"), () => copyText(r.b, t("コピーしました"))]);
    items.push([t("A 側をすべてコピー"), () => copyText(sideText("a"), t("A 側をコピーしました"))]);
    items.push([t("B 側をすべてコピー"), () => copyText(sideText("b"), t("B 側をコピーしました"))]);
    showCtxMenu(items, e);
  });
  // Find: a text in either side (case-insensitive); the row is scrolled to
  // and marked. Fifty changed blocks of a running-config are walked by the
  // interface name this way, not one by one.
  const fbar = $("dw-findbar"), fin = $("dw-find-in"), fn = $("dw-find-n");
  let fMatches = [], fIdx = -1, fLast = "";
  function fCollect(q) { const lq = q.toLowerCase(), res = []; if (!lq) return res; rows.forEach((r, i) => { if ((r.a || "").toLowerCase().includes(lq) || (r.b || "").toLowerCase().includes(lq)) res.push(i); }); return res; }
  function fShow() {
    body.querySelectorAll("tr.dw-found").forEach(tr => tr.classList.remove("dw-found"));
    if (!fMatches.length) { fn.textContent = fLast ? t("該当なし") : ""; return; }
    const i = fMatches[fIdx];
    if (filter && (i < filter.from || i >= filter.to)) { filter = null; $("dw-filter").checked = false; render(); }
    const tr = reveal(i);
    if (tr) tr.classList.add("dw-found");
    fn.textContent = `${fIdx + 1} / ${fMatches.length}`;
  }
  function fNext(dir) { const q = fin.value; if (q !== fLast) { fLast = q; fMatches = fCollect(q); fIdx = dir >= 0 ? -1 : 0; } if (!fMatches.length) { fShow(); return; } fIdx = (fIdx + dir + fMatches.length) % fMatches.length; fShow(); fin.focus(); }
  function openFind() { fbar.classList.remove("hidden"); fin.focus(); fin.select(); }
  function closeFind() { fbar.classList.add("hidden"); body.querySelectorAll("tr.dw-found").forEach(tr => tr.classList.remove("dw-found")); }
  $("dw-find").onclick = openFind;
  $("dw-find-next").onclick = () => fNext(1);
  $("dw-find-prev").onclick = () => fNext(-1);
  $("dw-find-close").onclick = closeFind;
  fin.addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); fNext(e.shiftKey ? -1 : 1); } else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); closeFind(); } });
  fin.addEventListener("input", () => { fLast = ""; fn.textContent = ""; });

  // Load (and reload when an option changes). The current block, the
  // scroll position and the opened folds are kept across a reload.
  let loading = false;
  async function load() {
    if (loading) return;
    loading = true;
    const keep = doc ? { la: cur >= 0 && blocks[cur] ? rows[blocks[cur].start].la : 0, lb: cur >= 0 && blocks[cur] ? rows[blocks[cur].start].lb : 0, scroll: body.scrollTop, expanded: new Set(expanded) } : null;
    opts.noise = $("dw-noise").checked; opts.secrets = $("dw-secrets").checked; opts.configOnly = $("dw-range").value === "config";
    saveOpts();
    try { doc = await D().Load({ ignoreNoise: opts.noise, ignoreSecrets: opts.secrets, configOnly: opts.configOnly }); }
    finally { loading = false; }
    $("dw-title").textContent = (doc.label ? doc.label + "　" : "") + t("差分");
    $("dw-name-a").textContent = doc.nameA; $("dw-name-a").dataset.tip = doc.pathA;
    $("dw-name-b").textContent = doc.nameB; $("dw-name-b").dataset.tip = doc.pathB;
    $("dw-writing").classList.toggle("hidden", !doc.writing);
    if (doc.err) { body.innerHTML = `<div style="padding:14px;color:var(--err)">${esc(terr(doc.err))}</div>`; return; }
    const r = doc.result;
    const al = build(r.lines || []);
    rows = al.out; blocks = al.bl;
    filter = null; $("dw-filter").checked = false; picked = null; jumpRow = -1;
    expanded = keep ? keep.expanded : new Set();
    cur = -1;
    if (keep && (keep.la || keep.lb)) {
      // The block nearest the one that was current.
      let best = -1, dist = Infinity;
      blocks.forEach((b, k) => { const x = rows[b.start]; const d = Math.abs((x.la || x.lb) - (keep.la || keep.lb)); if (d < dist) { dist = d; best = k; } });
      cur = best;
    } else if (blocks.length) cur = 0;
    // Range: only when both logs hold a configuration listing.
    const range = $("dw-range");
    const both = r.configA && r.configB;
    range.disabled = !both;
    range.value = r.mode === "config" ? "config" : "whole";
    range.parentElement.dataset.tip = both ? t("設定だけ: 両方のログの show running-config 等の出力だけを比べます。ログ全体: show の出力も含めてすべて比べます")
      : !r.configA && !r.configB ? t("A・B とも設定の表示（show running-config 等）が見当たらないため、ログ全体を比べています")
        : !r.configA ? t("A のログに設定の表示（show running-config 等）が見当たらないため、ログ全体を比べています") : t("B のログに設定の表示（show running-config 等）が見当たらないため、ログ全体を比べています");
    const note = $("dw-note");
    const notes = [];
    if (opts.configOnly && !both) notes.push(range.parentElement.dataset.tip);
    $("dw-cmd-wrap").classList.toggle("hidden", r.mode === "config");
    fillSelect($("dw-sec"), r.sections || [], t("-- セクションへ移動 --"));
    fillSelect($("dw-cmd"), r.commands || [], t("-- コマンドへ移動 ({n}) --", { n: (r.commands || []).length }));
    const changed = r.added + r.removed + (r.changed || 0) + (r.moved || 0) > 0;
    const ib = $("dw-impact");
    if (r.impact && changed) {
      const lvl = { high: ["高", "#f87171"], mid: ["中", "#fbbf24"], low: ["低", "#9ca3af"], none: ["—", "#9ca3af"] }[r.impact.level] || ["—", "#9ca3af"];
      ib.innerHTML = `${esc(t("影響"))}: <b style="color:${lvl[1]}">${esc(t(lvl[0]))}</b> ${r.impact.high ? esc(t("高 {n}", { n: r.impact.high })) : ""}${r.impact.refs && r.impact.refs.length ? " · " + esc(t("参照 {n}", { n: r.impact.refs.length })) : ""}`;
      ib.classList.remove("hidden");
    } else ib.classList.add("hidden");
    const v = $("dw-verdict");
    v.textContent = changed ? t("差分あり: 追加 {a} / 削除 {b} / 変更 {c} / 順序 {d}（{e} か所）", { a: r.added, b: r.removed, c: r.changed || 0, d: r.moved || 0, e: blocks.length }) : t("差分なし（A と B は同じ内容）");
    v.style.background = changed ? "rgba(250,204,21,.18)" : "rgba(34,197,94,.18)";
    v.style.color = changed ? "#fcd34d" : "#86efac";
    if (!changed && (opts.noise || opts.secrets) && (r.noiseLines || r.secretLines)) notes.push(t("ノイズを除外しています（時刻・カウンタ等 {n} 行、証明書・暗号化値・UUID {m} 行）。チェックを外すと差分が出る場合があります", { n: r.noiseLines || 0, m: r.secretLines || 0 }));
    note.textContent = notes.join("　");
    note.classList.toggle("hidden", !notes.length);
    // approx: the two sides were too large or too different for the exact
    // comparison, and the lines were matched one for one instead.
    const parts = [t("同じ {c} 行", { c: r.same })];
    if (opts.noise) parts.push(t("時刻・カウンタ等 {n} 行を除外", { n: r.noiseLines || 0 }));
    if (r.secretLines) parts.push(t("証明書・暗号化値・UUID {n} 行を除外（薄く表示）", { n: r.secretLines }));
    if (r.mode === "config") parts.push(t("設定だけを比較中"));
    if (r.approx) parts.push(t("（大きすぎるため概算: 行をそのまま突き合わせています）"));
    $("dw-counts").textContent = parts.join("　");
    // The scroll position / the first block: once every row is in (a
    // chunked draw has only its first rows when render returns).
    render(() => {
      if (keep) { body.scrollTop = keep.scroll; if (cur >= 0) markCurrent(); }
      else if (cur >= 0) setTimeout(() => goTo(cur), 0);
    });
  }
  $("dw-noise").onchange = load;
  $("dw-secrets").onchange = load;
  $("dw-range").onchange = load;
  // 戻す / 揃える: the commands made from the comparison, in a pane on the
  // right for copying (the compare tab also saves and hands them to the
  // run tab; this window has no vault).
  let cmdText = "";
  async function showCommands(dir) {
    const g = await D().Commands(dir);
    const pane = $("dw-cmdpane");
    pane.classList.remove("hidden");
    $("dw-cmd-title").textContent = dir === "forward" ? t("揃える（A → B）") : t("戻す（B → A）");
    const fam = { cisco: t("Cisco IOS 系"), fortigate: "FortiGate", junos: "Junos", yamaha: "Yamaha RTX", comware: t("Comware/VRP 系"), generic: t("不明（一覧のみ）") }[g.family] || g.family;
    const notes = [t("機器の種類は「{f}」と判断（設定の内容から）。投入前に必ず目で確認してください", { f: fam })];
    if (g.noConfig) notes.push(t("A か B のログに設定の表示（show running-config 等）が無いため作れません"));
    else if (g.empty) notes.push(t("差分はありますが、コマンドにできる行がありませんでした"));
    if (g.manual) notes.push(t("「! 手で確認」の行が {n} 行あります（機械的に逆にできない行）", { n: g.manual }));
    if (g.skipped) notes.push(t("証明書・暗号化値・UUID の差分 {n} 行は対象外です", { n: g.skipped }));
    if (g.reordered) notes.push(t("依存関係（参照 → 定義）の順に並べ替えています"));
    (g.warnings || []).forEach(w => notes.push(w));
    $("dw-cmd-note").textContent = notes.join("　");
    // A comment line first — which device, which way, when — so a paste
    // into the wrong window shows itself ("!" is Cisco's comment, "#" the
    // others'; a command set made from the copy drops it).
    const lines = g.lines || [];
    const header = lines.length ? `${g.family === "cisco" ? "!" : "#"} PalaTerm ${$("dw-cmd-title").textContent} ${doc && doc.label ? doc.label : ""} ${fmtRunTime(new Date().toISOString())}` : "";
    cmdText = (header ? [header] : []).concat(lines.map(l => l.text)).join("\n");
    $("dw-cmd-text").innerHTML = (header ? [`<span class="dw-cmd-ctx">${esc(header)}</span>`] : []).concat(lines.map(l => `<span class="${l.context ? "dw-cmd-ctx" : ""}${l.note ? " dw-cmd-manual" : ""}"${l.note ? ` data-tip="${esc(l.note)}"` : ""}>${esc(l.note ? "! " + t("手で確認") + ": " + l.text : l.text)}</span>`)).join("\n");
    updateView();
  }
  // 影響: the impact items and the references of changed objects, in the
  // same pane; a click on an item goes to its row.
  function showImpact() {
    const imp = doc && doc.result && doc.result.impact;
    if (!imp) return;
    const pane = $("dw-cmdpane");
    pane.classList.remove("hidden");
    $("dw-cmd-title").textContent = t("影響の読み方");
    $("dw-cmd-note").textContent = t("高 {h}・中 {m}・低 {l}。アドレス・経路・ACL・ポリシー・ポートの up/down は「高」、VLAN・管理系は「中」、説明文は「低」。行をクリックするとその場所へ", { h: imp.high, m: imp.mid, l: imp.low });
    const lv = { high: ["高", "#f87171"], mid: ["中", "#fbbf24"], low: ["低", "#9ca3af"] };
    const items = (imp.items || []).slice().sort((x, y) => (["high", "mid", "low"].indexOf(x.level)) - (["high", "mid", "low"].indexOf(y.level)));
    let html = items.map(it => `<span class="dw-imp" data-row="${it.row}" style="cursor:pointer"><b style="color:${lv[it.level][1]}">${esc(t(lv[it.level][0]))}</b> ${esc(it.text)}${it.section ? ` <span class="muted">［${esc(it.section)}］</span>` : ""} <span class="muted">— ${esc(it.reason)}</span></span>`).join("\n");
    if (imp.refs && imp.refs.length) {
      html += "\n\n" + esc(t("変わったオブジェクトの参照先:")) + "\n" + imp.refs.map(r => `<b>${esc(r.name)}</b> ← ` + r.rows.map(row => `<span class="dw-imp" data-row="${row}" style="cursor:pointer;text-decoration:underline">${esc(rowText(rows[row] || { a: "", b: "" }).trim())}</span>`).join("　")).join("\n");
    }
    cmdText = items.map(it => `${t(lv[it.level][0])} ${it.text}${it.section ? " [" + it.section + "]" : ""} - ${it.reason}`).join("\n");
    $("dw-cmd-text").innerHTML = html || esc(t("変更はありません"));
    $("dw-cmd-text").querySelectorAll(".dw-imp").forEach(el => el.onclick = () => { const i = parseInt(el.dataset.row, 10); jumpRow = i; reveal(i); render(); setTimeout(() => { jumpRow = -1; render(); }, 2500); });
    updateView();
  }
  $("dw-impact").onclick = showImpact;
  $("dw-cmd-back").onclick = () => showCommands("rollback").catch(e => toast(terr(e), "err"));
  $("dw-cmd-fwd").onclick = () => showCommands("forward").catch(e => toast(terr(e), "err"));
  $("dw-cmd-copy").onclick = () => copyText(cmdText, t("コマンドをコピーしました"));
  const closeCmdPane = () => { $("dw-cmdpane").classList.add("hidden"); updateView(); };
  $("dw-cmd-close").onclick = closeCmdPane;
  $("dw-open-a").onclick = () => D().OpenLog("a").catch(e => toast(t("ログを開けません") + ": " + terr(e), "err"));
  $("dw-open-b").onclick = () => D().OpenLog("b").catch(e => toast(t("ログを開けません") + ": " + terr(e), "err"));
  // Font size (wireFontZoom, app.js).
  const zoom = wireFontZoom("palaterm_diff_font", px => { body.style.fontSize = px + "px"; updateView(); }, { def: 12 });
  zoom.set(zoom.size);
  zoom.buttons($("dw-font-inc"), $("dw-font-dec"));
  zoom.wheel(body);
  document.addEventListener("keydown", e => {
    if ((e.altKey && e.key === "ArrowDown") || e.key === "F8") { e.preventDefault(); step(1); return; }
    if ((e.altKey && e.key === "ArrowUp") || e.key === "F7") { e.preventDefault(); step(-1); return; }
    if (zoom.key(e)) { e.preventDefault(); return; }
    if (e.ctrlKey && (e.key === "f" || e.key === "F" || e.code === "KeyF")) { e.preventDefault(); openFind(); return; }
    if (e.ctrlKey && (e.key === "a" || e.key === "A" || e.code === "KeyA") && !isTextField(e.target)) { e.preventDefault(); selectSide(); return; }
    // Esc: the search bar, then the 戻す / 揃える / 影響 pane, then the window.
    if (e.key === "Escape" && !document.getElementById("sc-pop")) {
      if (!fbar.classList.contains("hidden")) closeFind();
      else if (!$("dw-cmdpane").classList.contains("hidden")) closeCmdPane();
      else D().Close();
    }
  });
  await load();
}
