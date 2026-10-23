// 実行履歴・比較 tab: past batches (one row per log_ folder under the log root,
// read from the palaterm-run.json each batch leaves behind), and the
// before/after comparison of two runs' logs.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

// Rows drawn at first (and added per さらに表示): a log folder of
// thousands of runs drew thousands of rows every time the tab opened.
const HIST_PAGE = 200;

// Which run rows are expanded, and the two runs picked for comparison. Kept
// across re-renders of the tab.
const histState = { open: {}, failedOnly: {}, a: "", b: "", c: "", runs: [], limit: HIST_PAGE };
// Runs that represent the work itself: a batch tagged 作業中, or an
// interactive session (stage "work"). An external folder may be the work
// log whatever it is called, so every one of them is offered.
function isWorkRun(r) { return r.stage === "during" || r.stage === "work" || !!r.external; }
// Whether an external folder is named as the work (中 / during …).
function isNamedWork(r) { return r.stage === "during" || r.stage === "work"; }

function fmtRunTime(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const p = n => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

function stageBadge(st) {
  if (!st) return "";
  return `<span class="badge stage-${esc(st === "work" ? "during" : st)}">${esc(stageLabel(st))}</span>`;
}

// The run's folder name: what the user sees in the log folder, so it names
// the run in the comparison selects.
function runDirName(r) { return r.name || runTitle(r); }

// runTipOf: the chosen run's time, group and stage, the tooltip of a
// comparison select (an option cannot carry the app's tooltip).
function runTipOf(dir) {
  const r = dir && (histState.runs || []).find(x => x.dir === dir);
  return r ? runTitle(r) : "";
}
function runTitle(r) {
  return `${fmtRunTime(r.startedAt)}${r.group ? "  " + r.group : ""}${r.stage ? "  [" + stageLabel(r.stage) + "]" : ""}`;
}

// A history row's message: its error, or the commands the device refused.
function histMsg(d) {
  if (d.warnings && d.warnings.length) return "⚠ " + d.warnings.join(" ／ ");
  return fmtRunMsg(d.error || "");
}

// The tab opens at once on the runs read last time (if any) and redraws
// when the fresh list arrives.
let histLoading = 0;
async function renderHistory() {
  const root = document.getElementById("tab-history");
  const seq = ++histLoading;
  if (histState.runs.length) renderHistoryView();
  else root.innerHTML = `<div class="page-head"><div><div class="page-title">${esc(t("実行履歴・比較"))}</div></div></div><div class="empty">${esc(t("読み込み中…"))}</div>`;
  let runs = [];
  try { runs = await App().ListRunHistory() || []; }
  catch (e) { if (seq === histLoading) root.innerHTML = pageError(t("実行履歴・比較"), t("履歴を読めません") + ": " + terr(e)); return; }
  if (seq !== histLoading) return; // a newer read is on its way
  histState.runs = runs;
  renderHistoryView();
}

// Draws the tab from the runs already read (histState.runs). A sort or
// filter change re-draws without re-reading the log root.
function renderHistoryView() {
  const root = document.getElementById("tab-history");
  const runs = histState.runs;
  // Default comparison: the newest 作業前 and the newest 作業後 (same group
  // when one exists), so the common case is one click.
  if (!histState.a || !runs.some(r => r.dir === histState.a)) {
    const before = runs.find(r => r.stage === "before");
    histState.a = before ? before.dir : "";
  }
  if (!histState.b || !runs.some(r => r.dir === histState.b)) {
    const aRun = runs.find(r => r.dir === histState.a);
    // The newest 作業後 of the same group that came after A: right after a
    // 作業前 is taken, last month's 作業後 is not what B means.
    const sameGroup = r => r.stage === "after" && (!aRun || !aRun.group || r.group === aRun.group) && r.dir !== histState.a;
    const after = runs.find(r => sameGroup(r) && (!aRun || !r.startedAt || !aRun.startedAt || r.startedAt >= aRun.startedAt))
      || runs.find(sameGroup); // none after A: the newest, with the warning below
    histState.b = after ? after.dir : "";
  }
  const runOpt = sel => `<option value="">${esc(t("（選択）"))}</option>` + runs.map(r =>
    `<option value="${esc(r.dir)}" ${r.dir === sel ? "selected" : ""}>${esc(runDirName(r) + (r.external ? "  " + t("［外部］") : ""))}</option>`).join("");
  // The work run (作業中 batch or interactive session) between A and B, same
  // group when known: it says what was done, so the compare can conclude
  // whether that work shows up in the after logs.
  const aRun = runs.find(r => r.dir === histState.a), bRun = runs.find(r => r.dir === histState.b);
  const bOlder = !!(aRun && bRun && aRun.startedAt && bRun.startedAt && bRun.startedAt < aRun.startedAt);
  // "-" is （なし） picked by the user: it stays, so A and B can be compared
  // without a work run (it used to be replaced by the default at once).
  // Picking another A or B clears it, and the default is chosen again.
  if (aRun && bRun && histState.c !== "-" && (!histState.c || !runs.some(r => r.dir === histState.c))) {
    // External folders dropped in together carry the same times, so for an
    // external A / B the external folder named as the work (中, during …)
    // is the default, whatever the clock says.
    const ext = aRun.external && bRun.external ? runs.find(r => r.external && isNamedWork(r)) : null;
    const mid = ext || runs.find(r => isNamedWork(r) && r.startedAt > aRun.startedAt && r.startedAt < bRun.startedAt && (!aRun.group || !r.group || r.group === aRun.group));
    histState.c = mid ? mid.dir : "";
  }
  const workOpt = `<option value="">${esc(t("（なし）"))}</option>` + runs.filter(isWorkRun).map(r =>
    `<option value="${esc(r.dir)}" ${r.dir === histState.c ? "selected" : ""}>${esc(runDirName(r) + (r.interactive ? "  " + t("対話接続") : "") + (r.external ? "  " + t("［外部］") : ""))}</option>`).join("");

  // Sort / filter of the run rows (display only; see listView in app.js).
  // The comparison selects above the table always list every run.
  const LV = "history";
  const focus = listViewFocus();
  const groupsSeen = [...new Set(runs.map(r => r.group || "").filter(Boolean))].sort();
  const stagesSeen = [...new Set(runs.map(r => r.stage || "").filter(Boolean))];
  const stageName = r => (r.stage ? stageLabel(r.stage) : "") + (r.interactive ? " " + t("対話接続") : "") + (r.external ? " " + t("外部") : "");
  const visible = listViewApply(LV, runs,
    r => [fmtRunTime(r.startedAt), r.group, stageName(r), ...r.devices.map(d => d.name)],
    (r, k) => k === "time" ? (r.startedAt || "") : k === "stage" ? stageName(r) : k === "result" ? (r.hasSummary ? r.failed * 100000 + r.ok : -1) : (r.group || ""),
    { group: r => r.group || "", stage: r => stageLabel(r.stage || "") });
  const shown = visible.slice(0, histState.limit);
  const rows = shown.map(r => {
    const open = !!histState.open[r.dir];
    const counts = r.hasSummary
      ? `<span style="color:var(--ok)">${r.ok}</span> / <span style="color:var(--err)">${r.failed}</span>${r.canceled ? ` / <span style="color:var(--warn)">${r.canceled}</span>` : ""}`
      : `<span class="muted">${esc(t("{n} ファイル", { n: r.devices.length }))}</span>`;
    const failedOnly = !!histState.failedOnly[r.dir];
    const ORDER = { error: 0, canceled: 1, unknown: 2, ok: 3 };
    const devList = r.devices.slice().sort((x, y) => (ORDER[x.status] ?? 2) - (ORDER[y.status] ?? 2));
    const nFailed = r.devices.filter(d => d.status === "error" || d.status === "canceled").length;
    const shownDevs = failedOnly ? devList.filter(d => d.status === "error" || d.status === "canceled") : devList;
    const devRows = open ? `<tr class="hist-devs"><td colspan="7" style="padding:0">
      ${r.rounds > 1 ? `<div class="muted" style="font-size:12px;padding:6px 48px">${esc(t("各機器の最新の回の結果です。ほかの回のログは「フォルダ」で開けます"))}</div>` : ""}
      ${r.devices.length > 10 && nFailed ? `<div style="font-size:12px;padding:6px 48px"><label style="display:flex;align-items:center;gap:6px;cursor:pointer;font-weight:normal"><input type="checkbox" class="hist-failed-only" data-dir="${esc(r.dir)}" style="width:auto" ${failedOnly ? "checked" : ""}> ${esc(t("失敗・中止の {n} 台だけ表示（失敗が先に並びます）", { n: nFailed }))}</label></div>` : ""}
      <table><tbody>${shownDevs.map(d => `<tr>
        <td style="width:36px"></td>
        <td style="width:140px"><span class="dot st-${d.status === "ok" ? "done" : d.status === "unknown" ? "queued" : d.status}"></span>${esc(({ ok: t("完了"), error: t("エラー"), canceled: t("中止"), unknown: t("—") })[d.status] || d.status)}</td>
        <td style="width:200px"><b>${esc(d.name)}</b></td>
        <td class="mono" style="width:140px">${esc(d.host || "")}</td>
        <td class="muted mono"><div class="clip" data-tip="${esc(histMsg(d))}">${esc(histMsg(d))}</div></td>
        <td style="text-align:right;width:120px">${d.logPath ? `<span class="link" data-log="${esc(d.logPath)}">${esc(t("ログ表示"))}</span>` : `<span class="muted">${esc(t("ログなし"))}</span>`}</td>
      </tr>`).join("") || `<tr><td class="muted" style="padding:8px 48px">${esc(t("ログファイルがありません"))}</td></tr>`}</tbody></table></td></tr>` : "";
    return `<tr>
      <td style="width:36px"><span class="link" data-toggle="${esc(r.dir)}" style="text-decoration:none">${open ? "▾" : "▸"}</span></td>
      <td style="width:170px" class="mono">${esc(fmtRunTime(r.startedAt))}</td>
      <td style="width:110px">${stageBadge(r.stage)}${r.interactive ? `<span class="badge">${esc(t("対話接続"))}</span>` : ""}${r.external ? `<span class="badge" data-tip="${esc(t("PalaTerm 以外で取ったログのフォルダ（logs フォルダに置いたもの）。機器名はファイル名から、作業前・作業中・作業後はフォルダ名の語（before / after / 作業前 など）から推定しています"))}">${esc(t("外部"))}</span>` : ""}${r.schedule ? `<span class="badge" data-tip="${esc(t("予約「{n}」による実行", { n: r.schedule }))}">${esc(t("予約"))}</span>` : ""}</td>
      <td><div class="clip" data-tip="${esc(r.group || "")}">${esc(r.group || "")}</div></td>
      <td style="width:130px">${counts}</td>
      <td style="width:120px;font-size:12px" class="muted">${r.rounds > 1 ? `<span data-tip="${esc(t("この予約で実行した回数（各機器の結果は最新の回）。失敗のあった回: {f}", { f: r.failedRounds || 0 }))}">${esc(t("{n} 回分", { n: r.rounds }))}${r.failedRounds ? ` <span style="color:var(--err)">${esc(t("(失敗 {f})", { f: r.failedRounds }))}</span>` : ""}</span>` : r.hasSummary ? "" : r.external ? esc(t("外部フォルダ")) : esc(t("旧形式"))}</td>
      <td style="text-align:right;white-space:nowrap;width:260px">
        <button class="btn sm" data-cmp-a="${esc(r.dir)}" ${histState.a === r.dir ? 'style="border-color:var(--accent)"' : ""}>${esc(t("比較元(A)"))}</button>
        <button class="btn sm" data-cmp-b="${esc(r.dir)}" ${histState.b === r.dir ? 'style="border-color:var(--accent)"' : ""}>${esc(t("比較先(B)"))}</button>
        <button class="btn sm" data-folder="${esc(r.dir)}">${esc(t("フォルダを開く"))}</button>
      </td>
    </tr>${devRows}`;
  }).join("");

  renderListTab(root, `
    <div class="page-head">
      <div><div class="page-title">${esc(t("実行履歴・比較"))}</div>
        <div class="page-sub">${esc(t("ログフォルダ内の実行ごとのフォルダ（既定: 日時[_グループ][_作業タイミング]）の一覧。▸ で機器ごとの結果、A/B で 2 回の実行を選んで「比較」すると、同じ機器の作業前・作業後のログの差分が見られます"))}<br>${esc(t("他で取ったログも、フォルダごと logs フォルダの直下に置けば「外部」として並び、A・B・作業中のどれにも選べます（フォルダ名の 前／中／後・before／after などがあれば既定の選択に使います）"))}</div></div>
      <div class="row-inline">
        <button class="btn" id="hist-refresh">${esc(t("再読み込み"))}</button>
        <button class="btn" id="hist-logdir">${esc(t("ログフォルダを開く"))}</button>
      </div>
    </div>
    <div class="panel" style="padding:14px 18px;margin-bottom:16px">
      <div class="row-inline" style="gap:10px;flex-wrap:wrap">
        <b style="white-space:nowrap">${esc(t("比較"))}</b>
        <span class="muted" style="font-size:12px">A</span><select id="cmp-a" class="btn" style="max-width:360px" data-tip="${esc(runTipOf(histState.a))}">${runOpt(histState.a)}</select>
        <span class="muted" style="font-size:12px">B</span><select id="cmp-b" class="btn" style="max-width:360px" data-tip="${esc(runTipOf(histState.b))}">${runOpt(histState.b)}</select>
        <button class="btn primary" id="cmp-go" ${histState.a && histState.b && histState.a !== histState.b ? "" : "disabled"}>${esc(t("比較する"))}</button>
        ${bOlder ? `<span style="font-size:12px;color:var(--warn)">${esc(t("⚠ B は A より前の実行です（A=作業前・B=作業後 の順になっていません）"))}</span>` : ""}
      </div>
      <div class="row-inline" style="gap:10px;flex-wrap:wrap;margin-top:8px">
        <span class="muted" style="font-size:12px" data-tip="${esc(t("A と B の間に行った作業（作業中の一括実行、または対話接続）。選ぶと、その作業内容と、作業前→作業後の差分から「変更が反映されたか」の結論を出します"))}">${esc(t("作業中（任意） ⓘ"))}</span><select id="cmp-c" class="btn" style="max-width:420px" data-tip="${esc(histState.c && histState.c !== "-" ? runTipOf(histState.c) : "")}">${workOpt}</select>
        <span class="muted" style="font-size:12px">${esc(t("同じ機器名のログ同士を比べます（A=作業前・B=作業後）"))}</span>
      </div>
      <div id="cmp-result"></div>
    </div>
    ${runs.length ? listViewBar(LV, t("絞り込み（日時・グループ・作業・機器名）"), [
      { id: "group", label: t("グループ: すべて"), values: groupsSeen },
      { id: "stage", label: t("作業: すべて"), values: [...new Set(stagesSeen.map(s => stageLabel(s)))] },
    ]) : ""}
    <div class="panel scrollx">
      ${runs.length === 0 ? `<div class="empty">${esc(t("実行履歴がありません。一括実行するとここに記録されます。"))}</div>`
        : visible.length === 0 ? `<div class="empty">${esc(t("絞り込みに一致する実行がありません"))}</div>`
        : `<table class="fixed" data-colw="history"><thead><tr><th style="width:36px"></th>${listViewTh(LV, "time", t("開始日時"), "width:170px")}${listViewTh(LV, "stage", t("作業"), "width:110px")}${listViewTh(LV, "group", t("グループ"))}${listViewTh(LV, "result", t("成功 / 失敗"), "width:130px")}<th style="width:120px"></th><th style="width:260px"></th></tr></thead>
          <tbody>${rows}</tbody></table>`}
      ${visible.length > shown.length ? `<div style="padding:10px 14px;text-align:center"><button class="btn" id="hist-more">${esc(t("さらに表示（残り {n} 件）", { n: visible.length - shown.length }))}</button></div>` : ""}
    </div>`, [listView(LV)]);

  // Re-rendering for a sort / filter change must not re-read the log root:
  // the rows come from the runs already in hand.
  listViewWire(LV, root, renderHistoryView, focus);
  wireColResize(root);
  document.getElementById("hist-refresh").onclick = renderHistory;
  const more = document.getElementById("hist-more");
  if (more) more.onclick = () => { histState.limit += HIST_PAGE; renderHistoryView(); };
  document.getElementById("hist-logdir").onclick = () => App().OpenLogDir();
  root.querySelectorAll("[data-toggle]").forEach(el => el.onclick = () => { histState.open[el.dataset.toggle] = !histState.open[el.dataset.toggle]; renderHistoryView(); });
  root.querySelectorAll(".hist-failed-only").forEach(el => el.onchange = () => { histState.failedOnly[el.dataset.dir] = el.checked; renderHistoryView(); });
  root.querySelectorAll("[data-log]").forEach(el => el.onclick = () => showLog(el.dataset.log));
  root.querySelectorAll("[data-folder]").forEach(b => b.onclick = () => App().OpenRunFolder(b.dataset.folder).catch(e => toast(terr(e), "err")));
  // Picking A / B / 作業中 only redraws: the runs are already in hand.
  // Another A or B (the row button or the select): the work run between
  // them is chosen again.
  root.querySelectorAll("[data-cmp-a]").forEach(b => b.onclick = () => { if (histState.a !== b.dataset.cmpA) histState.c = ""; histState.a = b.dataset.cmpA; renderHistoryView(); });
  root.querySelectorAll("[data-cmp-b]").forEach(b => b.onclick = () => { if (histState.b !== b.dataset.cmpB) histState.c = ""; histState.b = b.dataset.cmpB; renderHistoryView(); });
  document.getElementById("cmp-a").onchange = e => { histState.a = e.target.value; histState.c = ""; renderHistoryView(); };
  document.getElementById("cmp-b").onchange = e => { histState.b = e.target.value; histState.c = ""; renderHistoryView(); };
  document.getElementById("cmp-c").onchange = e => { histState.c = e.target.value || "-"; renderHistoryView(); };
  document.getElementById("cmp-go").onclick = compareRuns;
  // The tab was redrawn (a sort, a filter, a row opened): the comparison of
  // the same A / B / 作業中 is still valid, so it is drawn again as it was.
  if (histState.cmp && histState.cmp.key === cmpKey()) drawCompare();
}

// Two names stand for the same device: equal, or one is the other followed
// by a separator and more (an external file may carry more than the name:
// "sw01" and "sw01_HQ" — but not "sw1" and "sw10").
function namesPair(x, y) {
  const p = (x || "").toLowerCase(), q = (y || "").toLowerCase();
  if (!p || !q) return false;
  if (p === q) return true;
  const [s, l] = p.length < q.length ? [p, q] : [q, p];
  return l.startsWith(s) && /[\s　_\-.(（【\[@#]/.test(l[s.length]);
}

// The devices of a run that stand for name: the exact one, else the only
// loose match.
function devFor(run, ...names) {
  for (const n of names) { const d = n && run.devices.find(d => d.name === n); if (d) return d; }
  const hits = run.devices.filter(d => names.some(n => namesPair(d.name, n)));
  return hits.length === 1 ? hits[0] : null;
}

// The work runs read for the verdict: the one picked and, when it lies
// between A and B (PalaTerm's own runs), every other work run between them
// of the same group — a reconnect splits one work into several sessions,
// each with its own folder. Oldest first.
function workRunsFor(a, b, c) {
  if (!c) return [];
  const list = [c];
  const between = r => r.startedAt && a.startedAt && b.startedAt && r.startedAt > a.startedAt && r.startedAt < b.startedAt;
  if (!a.external && !b.external && !c.external && between(c)) {
    histState.runs.forEach(r => { if (r !== c && !r.external && isNamedWork(r) && between(r) && (!c.group || !r.group || r.group === c.group)) list.push(r); });
  }
  return list.sort((x, y) => (x.startedAt || "").localeCompare(y.startedAt || ""));
}

// At most n calls at a time.
async function inPool(items, n, fn) {
  let next = 0;
  await Promise.all(Array.from({ length: Math.min(n, items.length) }, async () => {
    while (next < items.length) { const k = next++; await fn(items[k], k); }
  }));
}

// Pair the devices of run A and run B by name and work out, per device, how
// many lines differ (noise such as clock readings ignored) and — with a work
// run — the verdict of each setting typed. The result is kept in
// histState.cmp, so a sort, a filter or a redraw of the tab does not compare
// again (drawCompare).
let cmpSeq = 0;
async function compareRuns() {
  const a = histState.runs.find(r => r.dir === histState.a);
  const b = histState.runs.find(r => r.dir === histState.b);
  const out = document.getElementById("cmp-result");
  if (!a || !b) return;
  const seq = ++cmpSeq;
  // The selection this comparison is for: picking another A / B / 作業中
  // while it runs must not put this result under the new selection.
  const key = cmpKey();
  histState.cmp = null;
  // Every device of either run is listed. A device that failed in A or B
  // (no log, or a log cut short by an error) is a row of its own, in red,
  // first: three login failures used to hide inside "A のみ: sw3, sw7, sw9".
  const byName = {};
  a.devices.forEach(d => { byName[d.name] = { a: d.logPath || "", sa: d.status, ea: d.error || "" }; });
  b.devices.forEach(d => { const e = byName[d.name] = byName[d.name] || {}; e.b = d.logPath || ""; e.sb = d.status; e.eb = d.error || ""; });
  // Names that pair nowhere by exact match pair with the only name on the
  // other side they stand for (namesPair), and the row says so.
  const aOnly = Object.keys(byName).filter(n => byName[n].sa && !byName[n].sb);
  const bOnly = Object.keys(byName).filter(n => !byName[n].sa && byName[n].sb);
  const loose = {};
  for (const an of aOnly) {
    const hits = bOnly.filter(bn => !loose[bn] && namesPair(an, bn));
    if (hits.length === 1) {
      const bn = hits[0], eb = byName[bn];
      byName[an].b = eb.b; byName[an].sb = eb.sb; byName[an].eb = eb.eb; byName[an].pairedWith = bn;
      loose[bn] = true; delete byName[bn];
    }
  }
  const names = Object.keys(byName);
  // "unknown" (a folder without a summary: an old run, an external folder)
  // is not a failure — only error / canceled are.
  const bad = st => st === "error" || st === "canceled";
  const failedIn = n => { const e = byName[n]; const f = []; if (bad(e.sa)) f.push("A"); if (bad(e.sb)) f.push("B"); return f; };
  const failed = names.filter(n => failedIn(n).length);
  const pairs = names.filter(n => byName[n].a && byName[n].b && !failedIn(n).length);
  if (!pairs.length && !failed.length) {
    // Say which names each side was read as: a file naming habit that keeps
    // the names apart (a stage word or a date left in) shows at once.
    const sample = run => { const ns = [...new Set(run.devices.map(d => d.name))]; return ns.slice(0, 3).join(", ") + (ns.length > 3 ? t(" ほか {n} 台", { n: ns.length - 3 }) : ""); };
    out.innerHTML = `<div class="muted" style="font-size:13px;margin-top:10px">${esc(t("両方の実行にログがある機器がありません（機器名が一致するログ同士を比べます）"))}
      <div class="mono" style="font-size:12px;margin-top:6px">A: ${esc(sample(a) || t("（ログなし）"))}<br>B: ${esc(sample(b) || t("（ログなし）"))}</div>
      <div style="font-size:12px;margin-top:4px">${esc(t("外部フォルダの機器名はファイル名から日付・時刻と作業の語（前／後・作業前ログ など）を除いて読み取っています。A と B で名前が違うときは、ファイル名をそろえてください"))}</div></div>`;
    return;
  }
  // Strict verdict: the setting lines typed during the work (its logs) are
  // looked up one by one in the after log; the before log tells a line the
  // work added from one that was already there.
  const work = histState.c && histState.c !== "-" ? histState.runs.find(r => r.dir === histState.c) : null;
  const works = work ? workRunsFor(a, b, work) : [];
  const rows = pairs.map(n => ({ name: n }));
  let done = 0;
  const progress = () => { if (seq === cmpSeq) out.innerHTML = `<div class="muted" style="font-size:13px;margin-top:10px">${esc(t("比較中… {i} / {n} 台", { i: done, n: pairs.length }))}</div>`; };
  progress();
  await inPool(rows, 4, async r => {
    const e = byName[r.name];
    try {
      const d = await App().DiffLogFiles(e.a, e.b, true);
      r.added = d.added; r.removed = d.removed;
    } catch (err) { r.err = terr(err); }
    if (work && !r.err) {
      const paths = works.map(w => devFor(w, r.name, e.pairedWith)).filter(d => d && d.logPath).map(d => d.logPath);
      r.workLogs = paths.length;
      if (!paths.length) r.strict = { noLog: true };
      else {
        try { r.strict = await App().StrictVerify(paths, e.a, e.b); }
        catch (err) { r.strict = { err: terr(err) }; }
      }
    }
    done++; progress();
  });
  if (seq !== cmpSeq) return; // a newer comparison was started
  // Devices that one run did not include at all (a failure is listed above,
  // not here).
  const onlyA = names.filter(n => byName[n].sa && !byName[n].sb && !failedIn(n).length);
  const onlyB = names.filter(n => !byName[n].sa && byName[n].sb && !failedIn(n).length);
  const failRows = failed.map(n => {
    const e = byName[n]; const where = failedIn(n);
    const msg = where.map(w => `${w}: ${fmtRunMsg((w === "A" ? e.ea : e.eb) || t("ログなし"))}`).join(" ／ ");
    return { name: n, fail: where.join("/"), msg, a: e.a, b: e.b };
  });
  histState.cmp = { key, a, b, work, works, byName, rows, failRows, onlyA, onlyB };
  drawCompare();
}

// The compared devices, the ones that need attention first.
function cmpSorted(C) {
  return C.rows.map((r, i) => ({ r, i, k: cmpRank(r) })).sort((x, y) => x.k - y.k || x.i - y.i);
}

function cmpKey() { return [histState.a, histState.b, histState.c].join("|"); }

// How much attention a device's row needs: shown in this order.
function cmpRank(r) {
  const s = r.strict;
  if (r.err || (s && s.err)) return 0;
  if (s && s.missing) return 1;
  if (s && s.unexpectedCount) return 2;
  if (s && s.unverified) return 3;
  return 4;
}

// The verdict of one device as plain text (the table cell's headline, the
// copy and the CSV).
function cmpVerdictText(r, work) {
  if (r.err) return t("判定不能");
  const s = r.strict;
  if (!work || !s) return "";
  if (s.err) return s.err;
  if (s.noLog) return t("作業ログなし（対象外）");
  if (!s.commands.length) return t("設定コマンドなし（参照のみ {n} 件）", { n: s.typed });
  const unv = s.unverified || 0;
  if (s.missing) return t("⚠ {m} / {c} 件が未反映", { m: s.missing, c: s.commands.length }) + (unv ? t("（要確認 {u} 件）", { u: unv }) : "");
  if (!unv) return t("✅ {c} 件すべて反映", { c: s.commands.length });
  if (s.reflected) return t("✅ 未反映なし（反映 {r} 件・要確認 {u} 件）", { r: s.reflected, u: unv });
  return t("？ 照合できません（{u} 件）", { u: unv });
}

// Why a line was matched the way it was, or why it could not be.
function cmpNoteText(c) {
  return ({
    similar: t("近い行で確認"),
    secret: c.status === "unverified" ? t("暗号化された値のため、変わったかは判定できません") : t("暗号化されて保存"),
    vlan: t("VLAN 一覧で確認"),
    otherBlock: t("作業したのと別のブロックにあります"),
    noConfig: t("作業後のログに設定の表示がありません"),
    notStored: t("設定に残らないコマンドです"),
    default: t("既定値のため表示されない可能性があります"),
    spacing: t("画面端での折り返しによる空白の違いを無視して確認"),
  })[c.note] || (c.status === "superseded" ? t("後の行で取り消し・上書き") : "");
}

function cmpChangedText(u) { return `${u.kind} ${u.line}${u.block ? "  ［" + u.block + "］" : ""}`; }

// Draws the comparison kept in histState.cmp, when it is the comparison of
// the runs selected now.
function drawCompare() {
  const out = document.getElementById("cmp-result");
  const C = histState.cmp;
  if (!out || !C || C.key !== cmpKey()) return;
  const { a, b, work, works, byName, failRows, onlyA, onlyB } = C;
  const sev = cmpSorted(C);
  const needs = sev.filter(x => x.k < 4).length;
  const onlyNg = !!histState.cmpOnlyNg && needs > 0;
  const rows = sev.filter(x => !onlyNg || x.k < 4).map(x => x.r);
  const all = C.rows;
  let banner = "";
  if (work) {
    const judged = all.filter(r => r.strict && r.strict.commands && r.strict.commands.length);
    const okDev = judged.filter(r => r.strict.missing === 0).length;
    const ngDev = judged.length - okDev;
    const totalCmd = judged.reduce((s, r) => s + r.strict.commands.length, 0);
    const missCmd = judged.reduce((s, r) => s + r.strict.missing, 0);
    // "Unverified" lines (the logs cannot tell) are not counted as missing.
    const unvCmd = judged.reduce((s, r) => s + (r.strict.unverified || 0), 0);
    const refCmd = judged.reduce((s, r) => s + r.strict.reflected, 0);
    const noCfg = judged.filter(r => r.strict.noConfig).length;
    const unexDev = all.filter(r => r.strict && r.strict.unexpectedCount).length;
    const unexLines = all.reduce((s, r) => s + ((r.strict && r.strict.unexpectedCount) || 0), 0);
    const noWork = all.filter(r => r.strict && (r.strict.noLog || (r.strict.commands && !r.strict.commands.length))).length;
    let cls, headline;
    if (!judged.length) { cls = "warn"; headline = t("結論を出せません: 作業中のログに設定コマンドが見つかりません（show などの参照コマンドだけ、または作業ログの無い機器）"); }
    else if (!missCmd && !refCmd) { cls = "warn"; headline = t("結論を出せません: 作業後のログで照合できる設定がありません（作業後に show running-config 等を取得してください）"); }
    else if (ngDev === 0 && !unvCmd) { cls = "ok"; headline = t("✅ 結論: 作業中に設定した内容（{c} 件）が、作業後に全 {n} 台で反映されています", { c: totalCmd, n: okDev }); }
    else if (ngDev === 0) { cls = "ok"; headline = t("✅ 結論: 作業中に設定した内容（{c} 件）のうち、作業後に反映されていないものはありません（全 {n} 台・要確認 {u} 件）", { c: totalCmd, n: okDev, u: unvCmd }); }
    else if (okDev === 0) { cls = "err"; headline = t("⚠ 結論: 作業中に設定した内容のうち {m} / {c} 件が、作業後に反映されていません（{n} 台）", { m: missCmd, c: totalCmd, n: ngDev }); }
    else { cls = "warn"; headline = t("⚠ 結論: 作業中に設定した内容のうち {m} / {c} 件が、{b} 台で作業後に反映されていません（{a} 台はすべて反映）", { m: missCmd, c: totalCmd, a: okDev, b: ngDev }); }
    const notes = [];
    if (unexDev) notes.push(t("⚠ 作業中に打っていない設定の変化が {d} 台・{n} 行あります（各機器の「作業外の変更」で確認してください）", { d: unexDev, n: unexLines }));
    if (unvCmd && (ngDev || noCfg)) notes.push(t("？ 要確認 {u} 件（ログからは判定できない行。未反映には数えていません）", { u: unvCmd }));
    if (noCfg) notes.push(t("{n} 台は作業後のログに設定の表示（show running-config 等）が見当たらないため照合できません", { n: noCfg }));
    const workName = runTitle(work) + (work.interactive ? "（" + t("対話接続") + "）" : "") + (works.length > 1 ? t(" ほか {n} 件（同じ作業の別セッション）", { n: works.length - 1 }) : "");
    C.headline = headline;
    banner = `<div style="margin-top:12px;padding:10px 14px;border-radius:8px;border:1px solid var(--${cls});font-size:14px;font-weight:600;color:var(--${cls})">${esc(headline)}
      ${notes.length ? `<div style="font-size:12px;font-weight:400;margin-top:4px;color:var(--warn)">${notes.map(esc).join("<br>")}</div>` : ""}
      <div class="muted" style="font-size:12px;font-weight:400;margin-top:4px">${esc(t("作業中の実行: {t}", { t: workName }))}${noWork ? "　" + esc(t("判定対象外 {n} 台（作業ログ無し、または設定コマンド無し）", { n: noWork })) : ""}　${esc(t("※ 作業中のログから設定コマンド行を抜き出し、作業後のログ（show running-config 等）にその設定があるかを 1 件ずつ照合しています（省略形・暗号化されたパスワード・まとめられた VLAN 一覧なども照合。？ はログからは判定できない行）"))}</div></div>`;
  } else C.headline = "";
  const mark = st => st === "missing" ? `<span style="color:var(--err)">✗</span>` : st === "already" ? `<span class="muted">＝</span>` : st === "superseded" ? `<span class="muted">↷</span>` : st === "unverified" ? `<span style="color:var(--warn)">？</span>` : `<span style="color:var(--ok)">✓</span>`;
  const unexpectedBlock = s => {
    if (!s || !s.unexpectedCount) return "";
    const lines = (s.unexpected || []).map(u => `<div class="mono" style="font-size:11px;white-space:nowrap"><span style="color:var(--${u.kind === "+" ? "ok" : "err"})">${esc(u.kind)}</span> ${esc(u.line)}${u.block ? ` <span class="muted">［${esc(u.block)}］</span>` : ""}</div>`).join("");
    const more = s.unexpectedCount > (s.unexpected || []).length ? `<div class="muted" style="font-size:11px">${esc(t("ほか {n} 行（差分を開くで確認できます）", { n: s.unexpectedCount - s.unexpected.length }))}</div>` : "";
    return `<details open style="margin-top:4px"><summary style="cursor:pointer;color:var(--warn)" data-tip="${esc(t("作業前と作業後で変わった設定のうち、作業中に打った行では説明できないもの（別の場所を変えてしまった、ほかの人の変更、機器が自動で加えた行など）"))}">${esc(t("⚠ 作業外の変更 {n} 行", { n: s.unexpectedCount }))}</summary><div style="margin-top:4px;max-height:160px;overflow:auto">${lines}${more}</div></details>`;
  };
  const verdict = r => {
    const text = cmpVerdictText(r, work);
    if (r.err) return `<span style="color:var(--err)">${esc(text)}</span>`;
    const s = r.strict;
    if (!work || !s) return "";
    if (s.err) return `<span style="color:var(--err)">${esc(text)}</span>`;
    if (s.noLog || !s.commands.length) return `<span class="muted">${esc(text)}</span>` + unexpectedBlock(s);
    const unv = s.unverified || 0;
    const color = s.missing ? "err" : unv && !s.reflected ? "warn" : "ok";
    const lines = s.commands.map(c => {
      const extra = [c.status === "already" ? t("（作業前からあり）") : "", c.match ? "→ " + c.match : "", cmpNoteText(c)].filter(Boolean).join("　");
      return `<div class="mono" style="font-size:11px;white-space:nowrap"${c.parent ? ` data-tip="${esc(t("入力したブロック: {p}", { p: c.parent }))}"` : ""}>${mark(c.status)} ${esc(c.line)}${extra ? ` <span class="muted">${esc(extra)}</span>` : ""}</div>`;
    }).join("");
    const sessions = r.workLogs > 1 ? `<div class="muted" style="font-size:11px">${esc(t("作業ログ {n} 件を合わせて照合", { n: r.workLogs }))}</div>` : "";
    return `<details ${s.missing || unv ? "open" : ""}><summary style="cursor:pointer"><span style="color:var(--${color})">${esc(text)}</span></summary><div style="margin-top:4px;max-height:160px;overflow:auto">${lines}</div>${sessions}</details>` + unexpectedBlock(s);
  };
  // Names only have to be unique within a group, so runs of two groups pair
  // different devices that happen to share a name: say so above the result.
  const ga = a.group || "", gb = b.group || "";
  const groupWarn = ga !== gb
    ? `<div class="warn-box" style="margin-top:10px">${esc(t("⚠ 別のグループの実行同士を比べています（A: {a} ／ B: {b}）。機器名が同じでも別の機器の可能性があります", { a: ga || t("（グループ未設定）"), b: gb || t("（グループ未設定）") }))}</div>`
    : "";
  const tools = `<div class="row-inline" style="gap:10px;flex-wrap:wrap;margin-top:12px;font-size:13px">
      ${needs && all.length > 1 ? `<label style="display:flex;align-items:center;gap:6px;cursor:pointer;font-weight:normal"><input type="checkbox" id="cmp-only-ng" style="width:auto" ${onlyNg ? "checked" : ""}> ${esc(work ? t("未反映・作業外の変更・要確認のある機器だけ表示（{n} 台）", { n: needs }) : t("判定不能の機器だけ表示（{n} 台）", { n: needs }))}</label>` : ""}
      <span style="flex:1"></span>
      <button class="btn sm" id="cmp-copy" data-tip="${esc(t("結論と機器ごとの結果を、Excel や作業報告に貼れる形（タブ区切り）でコピーします"))}">${esc(t("結果をコピー"))}</button>
      <button class="btn sm" id="cmp-csv" data-tip="${esc(t("機器ごとの結果を export\\compare に CSV で書き出します"))}">${esc(t("CSV に書き出す"))}</button>
    </div>`;
  out.innerHTML = groupWarn + banner + tools + `
    ${failRows.length ? `<div style="margin-top:10px;font-size:13px;color:var(--err)">${esc(t("⚠ {n} 台は A または B で失敗しているため比較していません（下の表の先頭に赤で示します）", { n: failRows.length }))}</div>` : ""}
    <table style="margin-top:12px"><thead><tr><th>${esc(t("機器名"))}</th><th style="width:120px">${esc(t("差分"))}</th><th style="width:150px">${esc(t("追加 / 削除（行）"))}</th>${work ? `<th>${esc(t("結論（設定行の照合）"))}</th>` : ""}<th style="width:120px"></th></tr></thead><tbody>
      ${failRows.map(r => `<tr style="background:rgba(239,68,68,.08)">
        <td><b>${esc(r.name)}</b></td>
        <td><span style="color:var(--err)">${esc(t("{w} で失敗", { w: r.fail }))}</span></td>
        <td colspan="${work ? 2 : 1}" class="muted" style="font-size:12px"><div class="clip" data-tip="${esc(r.msg)}">${esc(r.msg)}</div></td>
        <td style="text-align:right;white-space:nowrap">${r.a ? `<span class="link" data-log="${esc(r.a)}">A</span> ` : ""}${r.b ? `<span class="link" data-log="${esc(r.b)}">B</span>` : ""}</td>
      </tr>`).join("")}
      ${rows.map(r => `<tr>
        <td><b>${esc(r.name)}</b>${byName[r.name] && byName[r.name].pairedWith ? ` <span class="muted" style="font-size:11px" data-tip="${esc(t("名前が完全には一致しないため、先頭が同じログ同士を比べています"))}">↔ ${esc(byName[r.name].pairedWith)}</span>` : ""}</td>
        <td>${r.err ? `<span style="color:var(--err)">${esc(r.err)}</span>` : (r.added + r.removed === 0 ? `<span style="color:var(--ok)">${esc(t("差分なし"))}</span>` : `<span style="color:var(--warn)">${esc(t("差分あり"))}</span>`)}</td>
        <td class="mono">${r.err ? "" : `<span style="color:var(--ok)">+${r.added}</span> / <span style="color:var(--err)">-${r.removed}</span>`}</td>
        ${work ? `<td>${verdict(r)}</td>` : ""}
        <td style="text-align:right">${r.err ? "" : `<button class="btn sm" data-diff="${esc(r.name)}">${esc(t("差分を開く"))}</button>`}</td>
      </tr>`).join("")}
    </tbody></table>
    ${onlyNg ? `<div class="muted" style="font-size:12px;margin-top:8px">${esc(t("ほか {n} 台は問題なし（チェックを外すと表示）", { n: all.length - rows.length }))}</div>` : ""}
    ${onlyA.length || onlyB.length ? `<div class="muted" style="font-size:12px;margin-top:8px">${esc(t("片方の実行にしか含まれていない機器:"))} ${onlyA.length ? esc(t("A のみ: {d}", { d: onlyA.join(", ") })) : ""}${onlyA.length && onlyB.length ? "　" : ""}${onlyB.length ? esc(t("B のみ: {d}", { d: onlyB.join(", ") })) : ""}</div>` : ""}`;
  out.querySelectorAll("[data-diff]").forEach(btn => btn.onclick = () => showDiff(btn.dataset.diff, byName[btn.dataset.diff].a, byName[btn.dataset.diff].b));
  out.querySelectorAll("[data-log]").forEach(el => el.onclick = () => showLog(el.dataset.log));
  const only = document.getElementById("cmp-only-ng");
  if (only) only.onchange = () => { histState.cmpOnlyNg = only.checked; drawCompare(); };
  document.getElementById("cmp-copy").onclick = async () => {
    const tsv = cmpTable(true).map(r => r.map(v => String(v).replace(/[\t\r\n]+/g, " / ")).join("\t")).join("\n");
    await copyText(tsv, t("比較結果をコピーしました"));
  };
  document.getElementById("cmp-csv").onclick = async () => {
    try {
      const path = await App().ExportCompareResult(cmpTable(false).map(r => r.map(v => String(v))));
      if (await uiConfirm({ title: t("書き出しました"), message: `<span class="muted" style="font-size:12px">${esc(path)}</span>`, okLabel: t("フォルダを開く"), cancelLabel: t("閉じる") })) {
        try { await App().OpenExportFolder(path.slice(0, Math.max(path.lastIndexOf("\\"), path.lastIndexOf("/")))); } catch (e) { toast(terr(e), "err"); }
      }
    } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
  };
}

// The comparison as a table (every device, in the order shown, the header
// first). withTitle adds the runs and the verdict above it, for pasting into
// a work report.
function cmpTable(withTitle) {
  const C = histState.cmp;
  const { a, b, work } = C;
  const head = [t("機器名"), t("組にした名前"), t("差分"), t("追加（行）"), t("削除（行）")];
  if (work) head.push(t("結論"), t("未反映"), t("要確認"), t("作業外の変更"), t("未反映・要確認の行"), t("作業外の変更の行"));
  const out = [];
  if (withTitle) {
    out.push([t("比較"), `A: ${runTitle(a)}`, `B: ${runTitle(b)}`].concat(work ? [t("作業中の実行: {t}", { t: runTitle(work) })] : []));
    if (C.headline) out.push([C.headline]);
    out.push([]);
  }
  out.push(head);
  // A failed device: why, in the 差分 column (there with or without a work).
  C.failRows.forEach(r => out.push([r.name, "", t("{w} で失敗", { w: r.fail }) + ": " + r.msg, "", ""]));
  for (const { r } of cmpSorted(C)) {
    const row = [r.name, (C.byName[r.name] && C.byName[r.name].pairedWith) || "", r.err ? r.err : (r.added + r.removed ? t("差分あり") : t("差分なし")), r.err ? "" : r.added, r.err ? "" : r.removed];
    if (work) {
      const s = r.strict || {};
      const lines = (s.commands || []).filter(c => c.status === "missing" || c.status === "unverified")
        .map(c => `${c.status === "missing" ? "✗" : "？"} ${c.line}${cmpNoteText(c) ? "（" + cmpNoteText(c) + "）" : ""}`);
      row.push(cmpVerdictText(r, work), s.missing || 0, s.unverified || 0, s.unexpectedCount || 0, lines.join("\n"), (s.unexpected || []).map(cmpChangedText).join("\n"));
    }
    out.push(row);
  }
  return out;
}

// The diff opens in its own WinMerge-style window (PalaTerm.exe --diff).
async function showDiff(name, pathA, pathB) {
  try { await App().OpenDiffWindow(pathA, pathB, name); }
  catch (e) { toast(t("差分を開けません") + ": " + terr(e), "err"); }
}

// (former in-app diff modal, kept for reference by the history of this file)
async function showDiffModal(name, pathA, pathB) {
  const base = p => p.split(/[\\/]/).pop();
  const node = h(`<div>
    <h3>${esc(t("差分"))}: ${esc(name)} <span class="muted" style="font-size:12px;font-weight:400">A = ${esc(base(pathA))}　B = ${esc(base(pathB))}</span></h3>
    <div class="row-inline" style="gap:16px;margin-bottom:10px;font-size:13px">
      <label style="display:flex;align-items:center;gap:6px;cursor:pointer"><input type="checkbox" id="df-noise" checked style="width:auto"> ${esc(t("時刻・稼働時間・カウンタの違いを無視"))}</label>
      <label style="display:flex;align-items:center;gap:6px;cursor:pointer"><input type="checkbox" id="df-only" checked style="width:auto"> ${esc(t("差分行のみ表示（前後 3 行つき）"))}</label>
      <span id="df-stat" class="muted"></span>
    </div>
    <div class="diff-wrap" id="df-body"></div>
    <div class="modal-actions"><button class="btn" id="df-close">${esc(t("閉じる"))}</button></div>
  </div>`);
  openModal(node, "xwide");
  node.querySelector("#df-close").onclick = closeModal;
  let data = null;
  async function load() {
    const noise = node.querySelector("#df-noise").checked;
    node.querySelector("#df-body").innerHTML = `<div class="muted" style="padding:14px">${esc(t("読み込み中…"))}</div>`;
    try { data = await App().DiffLogFiles(pathA, pathB, noise); }
    catch (e) { node.querySelector("#df-body").innerHTML = `<div style="padding:14px;color:var(--err)">${esc(terr(e))}</div>`; return; }
    draw();
  }
  function draw() {
    if (!data) return;
    const only = node.querySelector("#df-only").checked;
    node.querySelector("#df-stat").textContent = t("追加 {a} 行 / 削除 {b} 行 / 同じ {c} 行", { a: data.added, b: data.removed, c: data.same });
    const lines = data.lines || [];
    let keep = lines.map(() => !only);
    if (only) {
      const ctx = 3;
      lines.forEach((l, i) => { if (l.kind !== "=") for (let j = Math.max(0, i - ctx); j <= Math.min(lines.length - 1, i + ctx); j++) keep[j] = true; });
    }
    const out = [];
    let skipping = 0;
    const flushSkip = () => { if (skipping) { out.push(`<tr class="d-skip"><td colspan="4">${esc(t("… {n} 行同じ …", { n: skipping }))}</td></tr>`); skipping = 0; } };
    lines.forEach((l, i) => {
      if (!keep[i]) { skipping++; return; }
      flushSkip();
      const cls = l.kind === "-" ? "d-del" : l.kind === "+" ? "d-add" : "";
      out.push(`<tr class="${cls}"><td class="ln">${l.lineA || ""}</td><td class="side-a" style="width:calc(50% - 46px)">${esc(l.a || "")}</td><td class="ln">${l.lineB || ""}</td><td class="side-b" style="width:calc(50% - 46px)">${esc(l.b || "")}</td></tr>`);
    });
    flushSkip();
    node.querySelector("#df-body").innerHTML = out.length
      ? `<table><tbody>${out.join("")}</tbody></table>`
      : `<div class="muted" style="padding:14px">${esc(t("差分はありません"))}</div>`;
  }
  node.querySelector("#df-noise").onchange = load;
  node.querySelector("#df-only").onchange = draw;
  await load();
}
