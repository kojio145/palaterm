// 実行履歴 tab: past batches (one row per log_ folder under the log root,
// read from the palaterm-run.json each batch leaves behind), and the
// before/after comparison of two runs' logs.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

// Which run rows are expanded, and the two runs picked for comparison. Kept
// across re-renders of the tab.
const histState = { open: {}, a: "", b: "", c: "", runs: [] };
// Runs that represent the work itself: a batch tagged 作業中, or an
// interactive session (stage "work").
function isWorkRun(r) { return r.stage === "during" || r.stage === "work"; }

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

function runTitle(r) {
  return `${fmtRunTime(r.startedAt)}${r.group ? "  " + r.group : ""}${r.stage ? "  [" + stageLabel(r.stage) + "]" : ""}`;
}

async function renderHistory() {
  const root = document.getElementById("tab-history");
  root.innerHTML = `<div class="page-head"><div><div class="page-title">${esc(t("実行履歴"))}</div></div></div><div class="empty">${esc(t("読み込み中…"))}</div>`;
  let runs = [];
  try { runs = await App().ListRunHistory() || []; }
  catch (e) { root.innerHTML = `<div class="empty">${esc(t("履歴を読めません")) + ": " + esc(terr(e))}</div>`; return; }
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
    const after = runs.find(r => r.stage === "after" && (!aRun || !aRun.group || r.group === aRun.group) && r.dir !== histState.a);
    histState.b = after ? after.dir : "";
  }
  const runOpt = sel => `<option value="">${esc(t("（選択）"))}</option>` + runs.map(r =>
    `<option value="${esc(r.dir)}" ${r.dir === sel ? "selected" : ""}>${esc(runTitle(r))}</option>`).join("");
  // The work run (作業中 batch or interactive session) between A and B, same
  // group when known: it says what was done, so the compare can conclude
  // whether that work shows up in the after logs.
  const aRun = runs.find(r => r.dir === histState.a), bRun = runs.find(r => r.dir === histState.b);
  if (aRun && bRun && (!histState.c || !runs.some(r => r.dir === histState.c))) {
    const mid = runs.find(r => isWorkRun(r) && r.startedAt > aRun.startedAt && r.startedAt < bRun.startedAt && (!aRun.group || !r.group || r.group === aRun.group));
    histState.c = mid ? mid.dir : "";
  }
  const workOpt = `<option value="">${esc(t("（なし）"))}</option>` + runs.filter(isWorkRun).map(r =>
    `<option value="${esc(r.dir)}" ${r.dir === histState.c ? "selected" : ""}>${esc(runTitle(r) + (r.interactive ? "  " + t("単独接続") : ""))}</option>`).join("");

  // Sort / filter of the run rows (display only; see listView in app.js).
  // The comparison selects above the table always list every run.
  const LV = "history";
  const focus = listViewFocus();
  const groupsSeen = [...new Set(runs.map(r => r.group || "").filter(Boolean))].sort();
  const stagesSeen = [...new Set(runs.map(r => r.stage || "").filter(Boolean))];
  const stageName = r => r.stage ? stageLabel(r.stage) + (r.interactive ? " " + t("単独接続") : "") : "";
  const visible = listViewApply(LV, runs,
    r => [fmtRunTime(r.startedAt), r.group, stageName(r), ...r.devices.map(d => d.name)],
    (r, k) => k === "time" ? (r.startedAt || "") : k === "stage" ? stageName(r) : k === "result" ? (r.hasSummary ? r.failed * 100000 + r.ok : -1) : (r.group || ""),
    { group: r => r.group || "", stage: r => stageLabel(r.stage || "") });
  const rows = visible.map(r => {
    const open = !!histState.open[r.dir];
    const counts = r.hasSummary
      ? `<span style="color:var(--ok)">${r.ok}</span> / <span style="color:var(--err)">${r.failed}</span>${r.canceled ? ` / <span style="color:var(--warn)">${r.canceled}</span>` : ""}`
      : `<span class="muted">${esc(t("{n} ファイル", { n: r.devices.length }))}</span>`;
    const devRows = open ? `<tr class="hist-devs"><td colspan="7" style="padding:0">
      <table><tbody>${r.devices.map(d => `<tr>
        <td style="width:36px"></td>
        <td style="width:140px"><span class="dot st-${d.status === "ok" ? "done" : d.status === "unknown" ? "queued" : d.status}"></span>${esc(({ ok: t("完了"), error: t("エラー"), canceled: t("中止"), unknown: t("—") })[d.status] || d.status)}</td>
        <td style="width:200px"><b>${esc(d.name)}</b></td>
        <td class="mono" style="width:140px">${esc(d.host || "")}</td>
        <td class="muted mono"><div class="clip" data-tip="${esc(fmtRunMsg(d.error || ""))}">${esc(fmtRunMsg(d.error || ""))}</div></td>
        <td style="text-align:right;width:120px">${d.logPath ? `<span class="link" data-log="${esc(d.logPath)}">${esc(t("ログ表示"))}</span>` : `<span class="muted">${esc(t("ログなし"))}</span>`}</td>
      </tr>`).join("") || `<tr><td class="muted" style="padding:8px 48px">${esc(t("ログファイルがありません"))}</td></tr>`}</tbody></table></td></tr>` : "";
    return `<tr>
      <td style="width:36px"><span class="link" data-toggle="${esc(r.dir)}" style="text-decoration:none">${open ? "▾" : "▸"}</span></td>
      <td style="width:170px" class="mono">${esc(fmtRunTime(r.startedAt))}</td>
      <td style="width:110px">${stageBadge(r.stage)}${r.interactive ? `<span class="badge">${esc(t("単独接続"))}</span>` : ""}</td>
      <td><div class="clip" data-tip="${esc(r.group || "")}">${esc(r.group || "")}</div></td>
      <td style="width:130px">${counts}</td>
      <td style="width:120px" class="muted" style="font-size:12px">${r.hasSummary ? "" : esc(t("旧形式"))}</td>
      <td style="text-align:right;white-space:nowrap;width:260px">
        <button class="btn sm" data-cmp-a="${esc(r.dir)}" ${histState.a === r.dir ? 'style="border-color:var(--accent)"' : ""}>${esc(t("比較元(A)"))}</button>
        <button class="btn sm" data-cmp-b="${esc(r.dir)}" ${histState.b === r.dir ? 'style="border-color:var(--accent)"' : ""}>${esc(t("比較先(B)"))}</button>
        <button class="btn sm" data-folder="${esc(r.dir)}">${esc(t("フォルダ"))}</button>
      </td>
    </tr>${devRows}`;
  }).join("");

  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("実行履歴"))}</div>
        <div class="page-sub">${esc(t("ログフォルダ内の実行ごとのフォルダ（log_日時[_作業タイミング]）の一覧。▸ で機器ごとの結果、A/B で 2 回の実行を選んで「比較」すると、同じ機器の作業前・作業後のログの差分が見られます"))}</div></div>
      <div class="row-inline">
        <button class="btn" id="hist-refresh">${esc(t("再読込"))}</button>
        <button class="btn" id="hist-logdir">${esc(t("ログフォルダを開く"))}</button>
      </div>
    </div>
    <div class="panel" style="padding:14px 18px;margin-bottom:16px">
      <div class="row-inline" style="gap:10px;flex-wrap:wrap">
        <b style="white-space:nowrap">${esc(t("比較"))}</b>
        <span class="muted" style="font-size:12px">A</span><select id="cmp-a" class="btn" style="max-width:360px">${runOpt(histState.a)}</select>
        <span class="muted" style="font-size:12px">B</span><select id="cmp-b" class="btn" style="max-width:360px">${runOpt(histState.b)}</select>
        <button class="btn primary" id="cmp-go" ${histState.a && histState.b && histState.a !== histState.b ? "" : "disabled"}>${esc(t("比較する"))}</button>
      </div>
      <div class="row-inline" style="gap:10px;flex-wrap:wrap;margin-top:8px">
        <span class="muted" style="font-size:12px" data-tip="${esc(t("A と B の間に行った作業（作業中の一括実行、または単独接続）。選ぶと、その作業内容と、作業前→作業後の差分から「変更が反映されたか」の結論を出します"))}">${esc(t("作業中（任意） ⓘ"))}</span><select id="cmp-c" class="btn" style="max-width:420px">${workOpt}</select>
        <span class="muted" style="font-size:12px">${esc(t("同じ機器名のログ同士を比べます（A=作業前・B=作業後）"))}</span>
      </div>
      <div id="cmp-result"></div>
    </div>
    ${runs.length ? listViewBar(LV, t("絞り込み（日時・グループ・作業・機器名）"), [
      { id: "group", label: t("グループ: すべて"), values: groupsSeen },
      { id: "stage", label: t("作業: すべて"), values: [...new Set(stagesSeen.map(s => stageLabel(s)))] },
    ]) : ""}
    <div class="panel">
      ${runs.length === 0 ? `<div class="empty">${esc(t("実行履歴がありません。一括実行するとここに記録されます。"))}</div>`
        : visible.length === 0 ? `<div class="empty">${esc(t("絞り込みに一致する実行がありません"))}</div>`
        : `<table class="fixed" data-colw="history"><thead><tr><th style="width:36px"></th>${listViewTh(LV, "time", t("開始日時"), "width:170px")}${listViewTh(LV, "stage", t("作業"), "width:110px")}${listViewTh(LV, "group", t("グループ"))}${listViewTh(LV, "result", t("成功 / 失敗"), "width:130px")}<th style="width:120px"></th><th style="width:260px"></th></tr></thead>
          <tbody>${rows}</tbody></table>`}
    </div>`;

  // Re-rendering for a sort / filter change must not re-read the log root:
  // the rows come from the runs already in hand.
  listViewWire(LV, root, renderHistoryView, focus);
  wireColResize(root);
  document.getElementById("hist-refresh").onclick = renderHistory;
  document.getElementById("hist-logdir").onclick = () => App().OpenLogDir();
  root.querySelectorAll("[data-toggle]").forEach(el => el.onclick = () => { histState.open[el.dataset.toggle] = !histState.open[el.dataset.toggle]; renderHistoryView(); });
  root.querySelectorAll("[data-log]").forEach(el => el.onclick = () => showLog(el.dataset.log));
  root.querySelectorAll("[data-folder]").forEach(b => b.onclick = () => App().OpenRunFolder(b.dataset.folder).catch(e => toast(terr(e), "err")));
  root.querySelectorAll("[data-cmp-a]").forEach(b => b.onclick = () => { histState.a = b.dataset.cmpA; renderHistory(); });
  root.querySelectorAll("[data-cmp-b]").forEach(b => b.onclick = () => { histState.b = b.dataset.cmpB; renderHistory(); });
  document.getElementById("cmp-a").onchange = e => { histState.a = e.target.value; renderHistory(); };
  document.getElementById("cmp-b").onchange = e => { histState.b = e.target.value; histState.c = ""; renderHistory(); };
  document.getElementById("cmp-c").onchange = e => { histState.c = e.target.value || "-"; renderHistory(); };
  document.getElementById("cmp-go").onclick = compareRuns;
}

// Pair the devices of run A and run B by name and show, per device, how many
// lines differ (noise such as clock readings ignored) with a button for the
// side-by-side view.
async function compareRuns() {
  const a = histState.runs.find(r => r.dir === histState.a);
  const b = histState.runs.find(r => r.dir === histState.b);
  const out = document.getElementById("cmp-result");
  if (!a || !b) return;
  const byName = {};
  a.devices.forEach(d => { if (d.logPath) byName[d.name] = { a: d.logPath }; });
  b.devices.forEach(d => { if (d.logPath) (byName[d.name] = byName[d.name] || {}).b = d.logPath; });
  const names = Object.keys(byName);
  const pairs = names.filter(n => byName[n].a && byName[n].b);
  if (!pairs.length) {
    out.innerHTML = `<div class="muted" style="font-size:13px;margin-top:10px">${esc(t("両方の実行にログがある機器がありません（機器名が一致するログ同士を比べます）"))}</div>`;
    return;
  }
  out.innerHTML = `<div class="muted" style="font-size:13px;margin-top:10px">${esc(t("比較中… ({n} 台)", { n: pairs.length }))}</div>`;
  const rows = [];
  for (const n of pairs) {
    try {
      const r = await App().DiffLogFiles(byName[n].a, byName[n].b, true);
      rows.push({ name: n, added: r.added, removed: r.removed, same: r.same });
    } catch (e) { rows.push({ name: n, err: terr(e) }); }
  }
  const onlyA = names.filter(n => byName[n].a && !byName[n].b);
  const onlyB = names.filter(n => !byName[n].a && byName[n].b);
  // Strict verdict: the setting lines typed during the work (its log) are
  // looked up one by one in the after log; the before log tells a line the
  // work added from one that was already there.
  const work = histState.c && histState.c !== "-" ? histState.runs.find(r => r.dir === histState.c) : null;
  const workDev = {}; if (work) work.devices.forEach(d => { workDev[d.name] = d; });
  const strict = {};
  if (work) {
    for (const r of rows) {
      if (r.err) continue;
      const w = workDev[r.name];
      if (!w || !w.logPath) { strict[r.name] = { noLog: true }; continue; }
      try { strict[r.name] = await App().StrictVerify(w.logPath, byName[r.name].a, byName[r.name].b); }
      catch (e) { strict[r.name] = { err: terr(e) }; }
    }
  }
  let banner = "";
  if (work) {
    const judged = rows.filter(r => strict[r.name] && strict[r.name].commands && strict[r.name].commands.length);
    const okDev = judged.filter(r => strict[r.name].missing === 0).length;
    const ngDev = judged.length - okDev;
    const totalCmd = judged.reduce((s, r) => s + strict[r.name].commands.length, 0);
    const missCmd = judged.reduce((s, r) => s + strict[r.name].missing, 0);
    const noWork = rows.filter(r => strict[r.name] && (strict[r.name].noLog || (strict[r.name].commands && !strict[r.name].commands.length))).length;
    let cls, headline;
    if (!judged.length) { cls = "warn"; headline = t("結論を出せません: 作業中のログに設定コマンドが見つかりません（show などの参照コマンドだけ、または作業ログの無い機器）"); }
    else if (ngDev === 0) { cls = "ok"; headline = t("✅ 結論: 作業中に設定した内容（{c} 件）が、作業後に全 {n} 台で反映されています", { c: totalCmd, n: okDev }); }
    else if (okDev === 0) { cls = "err"; headline = t("⚠ 結論: 作業中に設定した内容のうち {m} / {c} 件が、作業後に反映されていません（{n} 台）", { m: missCmd, c: totalCmd, n: ngDev }); }
    else { cls = "warn"; headline = t("⚠ 結論: 作業中に設定した内容のうち {m} / {c} 件が、{b} 台で作業後に反映されていません（{a} 台はすべて反映）", { m: missCmd, c: totalCmd, a: okDev, b: ngDev }); }
    banner = `<div style="margin-top:12px;padding:10px 14px;border-radius:8px;border:1px solid var(--${cls});font-size:14px;font-weight:600;color:var(--${cls})">${esc(headline)}
      <div class="muted" style="font-size:12px;font-weight:400;margin-top:4px">${esc(t("作業中の実行: {t}", { t: runTitle(work) + (work.interactive ? "（" + t("単独接続") + "）" : "") }))}${noWork ? "　" + esc(t("判定対象外 {n} 台（作業ログ無し、または設定コマンド無し）", { n: noWork })) : ""}　${esc(t("※ 作業中のログから設定コマンド行を抜き出し、作業後のログ（show running-config 等）にその行があるかを 1 件ずつ照合しています"))}</div></div>`;
  }
  const verdict = r => {
    if (r.err) return `<span style="color:var(--err)">${esc(t("判定不能"))}</span>`;
    if (!work) return "";
    const s = strict[r.name];
    if (!s) return "";
    if (s.err) return `<span style="color:var(--err)">${esc(s.err)}</span>`;
    if (s.noLog) return `<span class="muted">${esc(t("作業ログなし（対象外）"))}</span>`;
    if (!s.commands.length) return `<span class="muted">${esc(t("設定コマンドなし（参照のみ {n} 件）", { n: s.typed }))}</span>`;
    const head = s.missing === 0
      ? `<span style="color:var(--ok)">${esc(t("✅ {c} 件すべて反映", { c: s.commands.length }))}</span>`
      : `<span style="color:var(--err)">${esc(t("⚠ {m} / {c} 件が未反映", { m: s.missing, c: s.commands.length }))}</span>`;
    const lines = s.commands.map(c => `<div class="mono" style="font-size:11px;white-space:nowrap">${c.status === "missing" ? `<span style="color:var(--err)">✗</span>` : c.status === "already" ? `<span class="muted">＝</span>` : `<span style="color:var(--ok)">✓</span>`} ${esc(c.line)}${c.status === "already" ? ` <span class="muted">${esc(t("（作業前からあり）"))}</span>` : ""}</div>`).join("");
    return `<details ${s.missing ? "open" : ""}><summary style="cursor:pointer">${head}</summary><div style="margin-top:4px;max-height:160px;overflow:auto">${lines}</div></details>`;
  };
  out.innerHTML = banner + `
    <table style="margin-top:12px"><thead><tr><th>${esc(t("ホスト名"))}</th><th style="width:120px">${esc(t("差分"))}</th><th style="width:150px">${esc(t("追加 / 削除（行）"))}</th>${work ? `<th>${esc(t("結論（設定行の照合）"))}</th>` : ""}<th style="width:120px"></th></tr></thead><tbody>
      ${rows.map(r => `<tr>
        <td><b>${esc(r.name)}</b></td>
        <td>${r.err ? `<span style="color:var(--err)">${esc(r.err)}</span>` : (r.added + r.removed === 0 ? `<span style="color:var(--ok)">${esc(t("差分なし"))}</span>` : `<span style="color:var(--warn)">${esc(t("差分あり"))}</span>`)}</td>
        <td class="mono">${r.err ? "" : `<span style="color:var(--ok)">+${r.added}</span> / <span style="color:var(--err)">-${r.removed}</span>`}</td>
        ${work ? `<td>${verdict(r)}</td>` : ""}
        <td style="text-align:right">${r.err ? "" : `<button class="btn sm" data-diff="${esc(r.name)}">${esc(t("差分を開く"))}</button>`}</td>
      </tr>`).join("")}
    </tbody></table>
    ${onlyA.length || onlyB.length ? `<div class="muted" style="font-size:12px;margin-top:8px">${onlyA.length ? esc(t("A のみ: {d}", { d: onlyA.join(", ") })) : ""}${onlyA.length && onlyB.length ? "　" : ""}${onlyB.length ? esc(t("B のみ: {d}", { d: onlyB.join(", ") })) : ""}</div>` : ""}`;
  out.querySelectorAll("[data-diff]").forEach(btn => btn.onclick = () => showDiff(btn.dataset.diff, byName[btn.dataset.diff].a, byName[btn.dataset.diff].b));
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
