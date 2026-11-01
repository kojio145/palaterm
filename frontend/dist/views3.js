// 実行履歴・比較 tab: past batches (one row per log_ folder under the log root,
// read from the palaterm-run.json each batch leaves behind), and the
// before/after comparison of two runs' logs.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

// Rows drawn at first (and added per さらに表示): a log folder of
// thousands of runs drew thousands of rows every time the tab opened.
const HIST_PAGE = 200;

// Which run rows are expanded, and the two runs picked for comparison. Kept
// across re-renders of the tab.
const histState = { open: {}, failedOnly: {}, a: "", b: "", c: "", k: "", runs: [], limit: HIST_PAGE };
// The checklist chosen for the comparison ("" / "-" = none).
function cmpChecklist() { return histState.k && histState.k !== "-" ? histState.k : ""; }
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

// A 作業中 folder holds one log per interactive session of the group, so a
// device name may appear several times in run.devices (oldest first, each
// with its own startedAt). The list row and the comparison use each
// device's latest session; every session is still offered as a log link.
// `sessions` / `deviceCount` / a device's `startedAt` come from newer
// backends only: without them a name appears once and nothing changes.
function runSessions(r) { return (r && r.sessions) || 0; }
const byStart = (x, y) => (x.startedAt || "").localeCompare(y.startedAt || "");
// Every log of one device name in a run, oldest first.
function devSessionsOf(run, name) {
  return (run.devices || []).filter(d => d.name === name).sort(byStart);
}
// One entry per device name: the latest session's fields, plus `logs` =
// every session's entry that has a log (oldest first).
function latestDevices(run) {
  const by = new Map();
  (run.devices || []).forEach(d => { const l = by.get(d.name) || []; l.push(d); by.set(d.name, l); });
  const out = [];
  by.forEach(list => {
    const sorted = list.slice().sort(byStart);
    out.push({ ...sorted[sorted.length - 1], logs: sorted.filter(d => d.logPath) });
  });
  return out;
}
// The 成功 / 失敗 / 中止 counts of a run: the summary's for a batch; per
// device (its latest session) for a work folder of several sessions, so
// three reconnects to one switch do not read as three devices.
function runCounts(r) {
  if (runSessions(r) <= 1) return { ok: r.ok || 0, failed: r.failed || 0, canceled: r.canceled || 0 };
  const c = { ok: 0, failed: 0, canceled: 0 };
  latestDevices(r).forEach(d => { if (d.status === "ok") c.ok++; else if (d.status === "error") c.failed++; else if (d.status === "canceled") c.canceled++; });
  return c;
}
// How many distinct devices a run touched.
function runDeviceCount(r) { return r.deviceCount || new Set((r.devices || []).map(d => d.name)).size; }
// The time of day of one session's log, the label of its log link.
function sessionLabel(d, i) {
  const s = fmtRunTime(d.startedAt);
  return s ? s.slice(11, 16) : `#${i + 1}`;
}

// A history row's message: its error, or the commands the device refused.
function histMsg(d) {
  if (d.warnings && d.warnings.length) return "⚠ " + d.warnings.join(" ／ ");
  return fmtRunMsg(d.error || "");
}

// The tab opens at once on the runs read last time (if any) and redraws
// when the fresh list arrives.
let histLoading = 0;
// Two run folders the same (the runner's path and the history's may
// differ in slashes or case on Windows).
function sameRunDir(x, y) {
  const n = s => String(s || "").replace(/\\/g, "/").replace(/\/+$/, "").toLowerCase();
  return !!x && n(x) === n(y);
}
// ⇄ 作業前と比較 on the run tab: the 作業後 batch that just finished is B,
// the newest 作業前 of its group taken before it is A, and the comparison
// runs at once (switching to the tab, checking A and B and pressing 比較する
// used to be three steps at the end of the night). A list read of its own:
// the tab's renderHistory may still be reading, and this one supersedes it.
async function compareAfterRun(afterDir, group) {
  const seq = ++histLoading;
  let runs;
  try { runs = await App().ListRunHistory() || []; }
  catch (e) { toast(t("履歴を読めません") + ": " + terr(e), "err"); return false; }
  if (seq !== histLoading) return false;
  histState.runs = runs;
  const b = runs.find(r => sameRunDir(r.dir, afterDir));
  if (!b) {
    toast(t("この実行が履歴に見つかりません（ログフォルダを確認してください）"), "warn");
    renderHistoryView();
    return false;
  }
  const g = b.group || group || "";
  const a = runs.find(r => r.stage === "before" && (r.group || "") === g && r.dir !== b.dir
    && (!r.startedAt || !b.startedAt || r.startedAt <= b.startedAt));
  histState.b = b.dir; histState.c = "";
  if (!a) {
    histState.a = "";
    renderHistoryView();
    toast(g ? t("グループ「{g}」の作業前の実行が履歴にありません。A を選んで「比較する」を押してください", { g })
      : t("作業前の実行が履歴にありません。A を選んで「比較する」を押してください"), "warn");
    return false;
  }
  histState.a = a.dir;
  renderHistoryView();
  await compareRuns();
  return true;
}
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
  // when one exists), so the common case is one click. "-" is （選択）
  // picked by the user: it stays empty (the default used to come back at
  // once, so A could never be cleared), as 作業中's （なし） below.
  if (histState.a !== "-" && (!histState.a || !runs.some(r => r.dir === histState.a))) {
    // The group whose 作業後 the run tab took last comes first: two groups
    // worked on the same night used to default to whichever was newest.
    const lastReq = typeof lastRunReq !== "undefined" ? lastRunReq : null;
    const prefGroup = lastReq && lastReq.stage === "after" && lastReq.group ? lastReq.group : "";
    const before = (prefGroup && runs.find(r => r.stage === "before" && r.group === prefGroup)) || runs.find(r => r.stage === "before");
    histState.a = before ? before.dir : "";
  }
  if (histState.b !== "-" && (!histState.b || !runs.some(r => r.dir === histState.b))) {
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
    (r, k) => k === "time" ? (r.startedAt || "") : k === "stage" ? stageName(r) : k === "result" ? (r.hasSummary ? runCounts(r).failed * 100000 + runCounts(r).ok : -1) : (r.group || ""),
    { group: r => r.group || "", stage: r => stageLabel(r.stage || "") });
  const shown = visible.slice(0, histState.limit);
  const rows = shown.map(r => {
    const open = !!histState.open[r.dir];
    const rc = runCounts(r);
    const counts = r.hasSummary
      ? `<span style="color:var(--ok)">${rc.ok}</span> / <span style="color:var(--err)">${rc.failed}</span>${rc.canceled ? ` / <span style="color:var(--warn)">${rc.canceled}</span>` : ""}`
      : `<span class="muted">${esc(t("{n} ファイル", { n: r.devices.length }))}</span>`;
    const failedOnly = !!histState.failedOnly[r.dir];
    const ORDER = { error: 0, canceled: 1, unknown: 2, ok: 3 };
    // One row per device (its latest session in a work folder of several).
    const devs = latestDevices(r);
    const devList = devs.sort((x, y) => (ORDER[x.status] ?? 2) - (ORDER[y.status] ?? 2));
    const nFailed = devs.filter(d => d.status === "error" || d.status === "canceled").length;
    const shownDevs = failedOnly ? devList.filter(d => d.status === "error" || d.status === "canceled") : devList;
    const sessions = runSessions(r);
    // ログ表示 of a device: one link, or one per session (its time of day)
    // when the work folder holds several of this device.
    const logLinks = d => d.logs && d.logs.length > 1
      ? `<span class="muted" style="font-size:11px">${esc(t("ログ"))}</span> ` + d.logs.map((l, i) => `<span class="link" data-log="${esc(l.logPath)}" data-tip="${esc(t("{t} のセッションのログ: {f}", { t: fmtRunTime(l.startedAt) || sessionLabel(l, i), f: l.logFile || l.logPath }))}">${esc(sessionLabel(l, i))}</span>`).join(" ")
      : d.logPath ? `<span class="link" data-log="${esc(d.logPath)}">${esc(t("ログ表示"))}</span>` : `<span class="muted">${esc(t("ログなし"))}</span>`;
    const devRows = open ? `<tr class="hist-devs"><td colspan="7" style="padding:0">
      ${r.rounds > 1 ? `<div class="muted" style="font-size:12px;padding:6px 48px">${esc(t("各機器の最新の回の結果です。ほかの回のログは「フォルダ」で開けます"))}</div>` : ""}
      ${sessions > 1 ? `<div class="muted" style="font-size:12px;padding:6px 48px">${esc(t("この作業中フォルダには {n} セッション分のログがあります（機器ごとに最新のセッションの結果。各セッションのログは右の時刻から開けます）", { n: sessions }))}</div>` : ""}
      ${r.devices.length > 10 && nFailed ? `<div style="font-size:12px;padding:6px 48px"><label style="display:flex;align-items:center;gap:6px;cursor:pointer;font-weight:normal"><input type="checkbox" class="hist-failed-only" data-dir="${esc(r.dir)}" style="width:auto" ${failedOnly ? "checked" : ""}> ${esc(t("失敗・中止の {n} 台だけ表示（失敗が先に並びます）", { n: nFailed }))}</label></div>` : ""}
      <table><tbody>${shownDevs.map(d => `<tr>
        <td style="width:36px"></td>
        <td style="width:140px"><span class="dot st-${d.status === "ok" ? "done" : d.status === "unknown" ? "queued" : d.status}"></span>${esc(({ ok: t("完了"), error: t("エラー"), canceled: t("中止"), unknown: t("—") })[d.status] || d.status)}</td>
        <td style="width:200px"><b>${esc(d.name)}</b></td>
        <td class="mono" style="width:140px">${esc(d.host || "")}</td>
        <td class="muted mono"><div class="clip" data-tip="${esc(histMsg(d))}">${esc(histMsg(d))}</div></td>
        <td style="text-align:right;min-width:120px;white-space:nowrap">${logLinks(d)}</td>
      </tr>`).join("") || `<tr><td class="muted" style="padding:8px 48px">${esc(t("ログファイルがありません"))}</td></tr>`}</tbody></table></td></tr>` : "";
    return `<tr>
      <td style="width:36px"><span class="link" data-toggle="${esc(r.dir)}" style="text-decoration:none">${open ? "▾" : "▸"}</span></td>
      <td style="width:170px" class="mono">${esc(fmtRunTime(r.startedAt))}</td>
      <td style="width:110px">${stageBadge(r.stage)}${r.interactive ? `<span class="badge">${esc(t("対話接続"))}</span>` : ""}${runSessions(r) > 1 ? `<span class="badge" data-tip="${esc(t("同じグループの対話接続 {n} セッション分のログが 1 つの作業中フォルダにまとまっています（{d} 台）。成功 / 失敗は機器ごと（最新のセッション）の数です", { n: runSessions(r), d: runDeviceCount(r) }))}">${esc(t("{n} セッション・{d} 台", { n: runSessions(r), d: runDeviceCount(r) }))}</span>` : ""}${r.external ? `<span class="badge" data-tip="${esc(t("PalaTerm 以外で取ったログのフォルダ（logs フォルダに置いたもの）。機器名はファイル名から、作業前・作業中・作業後はフォルダ名の語（before / after / 作業前 など）から推定しています"))}">${esc(t("外部"))}</span>` : ""}${r.schedule ? `<span class="badge" data-tip="${esc(t("予約「{n}」による実行", { n: r.schedule }))}">${esc(t("予約"))}</span>` : ""}</td>
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
        <button class="btn" id="hist-versions" ${runs.length ? "" : "disabled"} data-tip="${esc(t("機器を 1 台選ぶと、すべての実行のログがその機器の「版」として新しい順に並びます。設定が前の版から変わったかの印つき。任意の 2 版を差分窓で比べられます"))}">${esc(t("機器ごとの版"))}</button>
      </div>
    </div>
    <div class="panel" style="padding:14px 18px;margin-bottom:16px">
      <div class="row-inline" style="gap:10px;flex-wrap:wrap">
        <b style="white-space:nowrap">${esc(t("比較"))}</b>
        <span class="muted" style="font-size:12px">A</span><select id="cmp-a" class="btn" style="max-width:360px" data-tip="${esc(runTipOf(histState.a))}">${runOpt(histState.a)}</select>
        <span class="muted" style="font-size:12px">B</span><select id="cmp-b" class="btn" style="max-width:360px" data-tip="${esc(runTipOf(histState.b))}">${runOpt(histState.b)}</select>
        <button class="btn primary" id="cmp-go" ${aRun && bRun && histState.a !== histState.b ? "" : "disabled"}>${esc(t("比較する"))}</button>
        ${bOlder ? `<span style="font-size:12px;color:var(--warn)">${esc(t("⚠ B は A より前の実行です（A=作業前・B=作業後 の順になっていません）"))}</span>` : ""}
      </div>
      <div class="row-inline" style="gap:10px;flex-wrap:wrap;margin-top:8px">
        <span class="muted" style="font-size:12px" data-tip="${esc(t("A と B の間に行った作業（作業中の一括実行、または対話接続）。選ぶと、その作業内容と、作業前→作業後の差分から「変更が反映されたか」の結論を出します"))}">${esc(t("作業中（任意） ⓘ"))}</span><select id="cmp-c" class="btn" style="max-width:420px" data-tip="${esc(histState.c && histState.c !== "-" ? runTipOf(histState.c) : "")}">${workOpt}</select>
        <span class="muted" style="font-size:12px">${esc(t("同じ機器名のログ同士を比べます（A=作業前・B=作業後）"))}</span>
      </div>
      <div class="row-inline" style="gap:10px;flex-wrap:wrap;margin-top:8px">
        <span class="muted" style="font-size:12px" data-tip="${esc(t("「あるべき設定行」の一覧。選ぶと、作業後（B）のログに各行があるかを機器ごとに PASS / FAIL（値が違う）/ MISSING（無い）で判定します"))}">${esc(t("チェックリスト（任意） ⓘ"))}</span>
        <select id="cmp-k" class="btn" style="max-width:320px"><option value="">${esc(t("（なし）"))}</option>${(INV.checklists || []).map(c => `<option value="${esc(c.name)}" ${cmpChecklist() === c.name ? "selected" : ""}>${esc(c.name)}（${c.lines.length}）</option>`).join("")}</select>
        <button class="btn sm" id="cmp-k-edit">${esc(t("チェックリストを編集…"))}</button>
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
  // 再読み込み drops the comparison shown: the folders may have changed
  // under the same names (a schedule's next round, an external folder
  // replaced), and the old verdict would be drawn again as if current.
  document.getElementById("hist-refresh").onclick = () => { histState.cmp = null; renderHistory(); };
  const more = document.getElementById("hist-more");
  if (more) more.onclick = () => { histState.limit += HIST_PAGE; renderHistoryView(); };
  document.getElementById("hist-logdir").onclick = () => App().OpenLogDir();
  document.getElementById("hist-versions").onclick = deviceVersionsDialog;
  root.querySelectorAll("[data-toggle]").forEach(el => el.onclick = () => { histState.open[el.dataset.toggle] = !histState.open[el.dataset.toggle]; renderHistoryView(); });
  root.querySelectorAll(".hist-failed-only").forEach(el => el.onchange = () => { histState.failedOnly[el.dataset.dir] = el.checked; renderHistoryView(); });
  root.querySelectorAll("[data-log]").forEach(el => el.onclick = () => showLog(el.dataset.log));
  root.querySelectorAll("[data-folder]").forEach(b => b.onclick = () => App().OpenRunFolder(b.dataset.folder).catch(e => toast(terr(e), "err")));
  // Picking A / B / 作業中 only redraws: the runs are already in hand.
  // Another A or B (the row button or the select): the work run between
  // them is chosen again.
  root.querySelectorAll("[data-cmp-a]").forEach(b => b.onclick = () => { if (histState.a !== b.dataset.cmpA) histState.c = ""; histState.a = b.dataset.cmpA; renderHistoryView(); });
  root.querySelectorAll("[data-cmp-b]").forEach(b => b.onclick = () => { if (histState.b !== b.dataset.cmpB) histState.c = ""; histState.b = b.dataset.cmpB; renderHistoryView(); });
  document.getElementById("cmp-a").onchange = e => { histState.a = e.target.value || "-"; histState.c = ""; renderHistoryView(); };
  document.getElementById("cmp-b").onchange = e => { histState.b = e.target.value || "-"; histState.c = ""; renderHistoryView(); };
  document.getElementById("cmp-c").onchange = e => { histState.c = e.target.value || "-"; renderHistoryView(); };
  document.getElementById("cmp-k").onchange = e => { histState.k = e.target.value || "-"; renderHistoryView(); };
  document.getElementById("cmp-k-edit").onclick = () => checklistEditor();
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

// The logs of a run that stand for name: every session of the exact name,
// else of the only name that loosely matches. Oldest first; empty when
// none.
function devsFor(run, ...names) {
  for (const n of names) { const l = n ? devSessionsOf(run, n) : []; if (l.length) return l; }
  const hits = [...new Set(run.devices.filter(d => names.some(n => namesPair(d.name, n))).map(d => d.name))];
  return hits.length === 1 ? devSessionsOf(run, hits[0]) : [];
}

// Named work runs of the group that lie outside A–B but may belong to the
// same job: after the last batch (作業前 / 作業後) taken before A, or
// before the first 作業前 taken after B — a session opened before the
// 作業前 was taken, or after the 作業後. Earlier jobs' work (before that
// batch) is not counted. Those already read (works) are not counted either.
function workRunsOutside(a, b, works) {
  if (!a || !b || a.external || b.external || !a.startedAt || !b.startedAt) return 0;
  const g = a.group || b.group || "";
  const runs = (histState.runs || []).filter(r => r.startedAt && !r.external && (!g || !r.group || r.group === g));
  const batch = r => (r.stage === "before" || r.stage === "after") && !r.interactive;
  const lo = runs.filter(r => batch(r) && r.startedAt < a.startedAt).reduce((m, r) => r.startedAt > m ? r.startedAt : m, "");
  const hi = runs.filter(r => r.stage === "before" && !r.interactive && r.startedAt > b.startedAt).reduce((m, r) => !m || r.startedAt < m ? r.startedAt : m, "");
  return runs.filter(r => isNamedWork(r) && r.dir !== a.dir && r.dir !== b.dir && !(works || []).includes(r)
    && ((r.startedAt < a.startedAt && r.startedAt > lo) || (r.startedAt > b.startedAt && (!hi || r.startedAt < hi)))).length;
}

// The work run's name in the verdict banner, the copied result and the
// exports: its title, 対話接続, and how many sessions its folder holds and
// how many other work folders were read with it (0 / 1 left out).
function cmpWorkName(work, works) {
  const parts = [];
  if (runSessions(work) > 1) parts.push(t("{s} セッション", { s: runSessions(work) }));
  const more = (works || []).filter(w => w !== work).length;
  if (more) parts.push(t("ほか {m} フォルダを含む", { m: more }));
  return runTitle(work) + (work.interactive ? "（" + t("対話接続") + "）" : "") + (parts.length ? "（" + parts.join("・") + "）" : "");
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
  // A name that appears several times in A or B (a work folder of several
  // sessions picked as A or B): its latest log is compared, and na / nb
  // let the row say so.
  const byName = {};
  latestDevices(a).forEach(d => { byName[d.name] = { a: d.logPath || "", sa: d.status, ea: d.error || "", na: d.logs.length }; });
  latestDevices(b).forEach(d => { const e = byName[d.name] = byName[d.name] || {}; e.b = d.logPath || ""; e.sb = d.status; e.eb = d.error || ""; e.nb = d.logs.length; });
  // Names that pair nowhere by exact match pair with the only name on the
  // other side they stand for (namesPair), and the row says so.
  const aOnly = Object.keys(byName).filter(n => byName[n].sa && !byName[n].sb);
  const bOnly = Object.keys(byName).filter(n => !byName[n].sa && byName[n].sb);
  const loose = {};
  for (const an of aOnly) {
    const hits = bOnly.filter(bn => !loose[bn] && namesPair(an, bn));
    if (hits.length === 1) {
      const bn = hits[0], eb = byName[bn];
      byName[an].b = eb.b; byName[an].sb = eb.sb; byName[an].eb = eb.eb; byName[an].nb = eb.nb; byName[an].pairedWith = bn;
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
      // Whole logs and, when both hold a configuration listing, the
      // listings alone: a device whose counters moved is not one whose
      // configuration changed.
      const d = await App().DiffLogFiles(e.a, e.b, { ignoreNoise: true, ignoreSecrets: true });
      const n = c => c.added + c.removed + (c.changed || 0) + (c.moved || 0);
      r.whole = d.whole; r.hasConfig = !!(d.configA && d.configB); r.config = r.hasConfig ? d.config : null;
      r.added = d.whole.added; r.removed = d.whole.removed; r.changed = d.whole.changed || 0; r.moved = d.whole.moved || 0;
      r.cfgDiff = r.hasConfig ? n(d.config) : 0;
      r.anyDiff = n(d.whole);
      r.impact = d.impact || ""; r.high = d.high || 0; r.mid = d.mid || 0;
    } catch (err) { r.err = terr(err); }
    if (cmpChecklist() && !r.err) {
      try { r.check = await App().VerifyChecklist(cmpChecklist(), e.b); }
      catch (err) { r.check = { err: terr(err) }; }
    }
    if (work && !r.err) {
      // Every session's log of this device across the work folders, oldest
      // first (a device's own startedAt, else its folder's): StrictVerify
      // reads them as one work.
      const paths = works.flatMap(w => devsFor(w, r.name, e.pairedWith).map(d => ({ p: d.logPath, at: d.startedAt || w.startedAt || "" })))
        .filter(x => x.p).sort((x, y) => x.at.localeCompare(y.at)).map(x => x.p);
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
  histState.cmp = { key, a, b, work, works, byName, rows, failRows, onlyA, onlyB, at: new Date().toISOString() };
  drawCompare();
}

// The compared devices, the ones that need attention first.
function cmpSorted(C) {
  return C.rows.map((r, i) => ({ r, i, k: cmpRank(r) })).sort((x, y) => x.k - y.k || x.i - y.i);
}

function cmpKey() { return [histState.a, histState.b, histState.c, cmpChecklist()].join("|"); }

// How much attention a device's row needs: shown in this order.
function cmpRank(r) {
  const s = r.strict;
  if (r.err || (s && s.err)) return 0;
  if (r.check && (r.check.fail || r.check.missing)) return 1;
  if (r.hasConfig && r.cfgDiff) return 1;
  if (s && s.missing) return 1;
  if (s && s.unexpectedCount) return 2;
  if (s && s.unverified) return 3;
  if (r.anyDiff) return 4;
  return 5;
}

// The 差分 cell: whether the configuration changed, only the state, nothing,
// or (no listing on one side) the whole logs.
function cmpDiffText(r) {
  if (r.err) return t("判定不能");
  if (r.hasConfig) {
    if (r.cfgDiff) return t("設定の差分あり");
    return r.anyDiff ? t("状態のみ変化") : t("差分なし");
  }
  return r.anyDiff ? t("差分あり（設定が見当たらない）") : t("差分なし（設定が見当たらない）");
}
function cmpDiffColor(r) { return r.err ? "err" : r.hasConfig ? (r.cfgDiff ? "warn" : r.anyDiff ? "muted" : "ok") : (r.anyDiff ? "warn" : "ok"); }
// The impact badge: the reading order of the devices.
function cmpImpactHTML(r) {
  if (r.err || !r.hasConfig || !r.cfgDiff) return `<span class="muted">—</span>`;
  const m = { high: ["高", "var(--err)"], mid: ["中", "var(--warn)"], low: ["低", "var(--muted)"] }[r.impact];
  if (!m) return `<span class="muted">—</span>`;
  return `<span style="color:${m[1]};font-weight:600" data-tip="${esc(t("影響が大きい行 {h}・中 {m}（アドレス・経路・ACL・ポリシー・ポートの up/down が「高」）", { h: r.high || 0, m: r.mid || 0 }))}">${esc(t(m[0]))}${r.high ? ` ${r.high}` : ""}</span>`;
}
// The checklist column: counts and the lines that are not as expected.
function cmpCheckText(c) {
  if (!c) return "";
  if (c.err) return c.err;
  if (c.noConfig) return t("判定不能（設定の表示なし）");
  return t("PASS {p} / FAIL {f} / MISSING {m}", { p: c.pass, f: c.fail, m: c.missing }) + (c.unverified ? t("（要確認 {u}）", { u: c.unverified }) : "");
}
function cmpCheckHTML(c) {
  if (!c) return "";
  const text = cmpCheckText(c);
  if (c.err) return `<span style="color:var(--err)">${esc(text)}</span>`;
  if (c.noConfig) return `<span class="muted">${esc(text)}</span>`;
  const bad = c.fail + c.missing;
  const mark = st => st === "pass" ? `<span style="color:var(--ok)">✓</span>` : st === "fail" ? `<span style="color:var(--err)">✗</span>` : st === "missing" ? `<span style="color:var(--err)">－</span>` : `<span style="color:var(--warn)">？</span>`;
  const lines = (c.results || []).map(x => `<div class="mono" style="font-size:11px;white-space:nowrap"${x.block ? ` data-tip="${esc(t("ブロック: {p}", { p: x.block }))}"` : ""}>${mark(x.status)} ${esc(x.text)}${x.status === "fail" && x.found ? ` <span class="muted">→ ${esc(x.found)}</span>` : ""}${x.status === "missing" ? ` <span class="muted">${esc(t("（無い）"))}</span>` : ""}</div>`).join("");
  return `<details ${bad ? "open" : ""}><summary style="cursor:pointer"><span style="color:var(--${bad ? "err" : c.unverified ? "warn" : "ok"})">${esc(text)}</span></summary><div style="margin-top:4px;max-height:160px;overflow:auto">${lines}</div></details>`;
}
// "+a / -b / ~c / ↕d" for one count set.
function cmpCountsHTML(c) {
  if (!c) return "";
  return `<span style="color:var(--ok)">+${c.added}</span> / <span style="color:var(--err)">-${c.removed}</span>` + (c.changed ? ` / <span style="color:#fcd34d">~${c.changed}</span>` : "") + (c.moved ? ` / <span style="color:#c4b5fd">${esc(t("順序"))} ${c.moved}</span>` : "");
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
    noBlock: t("作業後のログにそのブロックがありません（PASS にはしません）"),
    notStored: t("設定に残らないコマンドです"),
    default: t("既定値のため表示されない可能性があります"),
    spacing: t("画面端での折り返しによる空白の違いを無視して確認"),
  })[c.note] || (c.status === "superseded" ? t("後の行で取り消し・上書き") : "");
}

function cmpChangedText(u) { return `${u.kind} ${u.line}${u.block ? "  ［" + u.block + "］" : ""}`; }

// A or B held several logs of this device (a work folder of several
// sessions): the latest was compared, and the row says so.
function cmpDupNote(e) {
  if (!e) return "";
  const parts = [];
  if (e.na > 1) parts.push("A: " + t("同じ機器のログが {n} 件、最新を使用", { n: e.na }));
  if (e.nb > 1) parts.push("B: " + t("同じ機器のログが {n} 件、最新を使用", { n: e.nb }));
  return parts.length ? `<div class="muted" style="font-size:11px;font-weight:400">${esc(parts.join(" ／ "))}</div>` : "";
}

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
    // Work of the group outside A–B that was not read: said, so a session
    // opened before the 作業前 (or after the 作業後) is not taken as absent.
    const outside = workRunsOutside(a, b, works);
    if (outside) notes.push(t("A より前／B より後の作業中が {n} 件あり、含めていません", { n: outside }));
    const workName = cmpWorkName(work, works);
    C.headline = headline;
    banner = `<div style="margin-top:12px;padding:10px 14px;border-radius:8px;border:1px solid var(--${cls});font-size:14px;font-weight:600;color:var(--${cls})">${esc(headline)}
      ${notes.length ? `<div style="font-size:12px;font-weight:400;margin-top:4px;color:var(--warn)">${notes.map(esc).join("<br>")}</div>` : ""}
      <div class="muted" style="font-size:12px;font-weight:400;margin-top:4px">${esc(t("作業中の実行: {t}", { t: workName }))}${noWork ? "　" + esc(t("判定対象外 {n} 台（作業ログ無し、または設定コマンド無し）", { n: noWork })) : ""}　${esc(t("比較した時刻: {t}", { t: fmtRunTime(C.at) }))}　${esc(t("※ 作業中のログから設定コマンド行を抜き出し、作業後のログ（show running-config 等）にその設定があるかを 1 件ずつ照合しています（省略形・暗号化されたパスワード・まとめられた VLAN 一覧なども照合。？ はログからは判定できない行）"))}</div></div>`;
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
      ${needs && all.length > 1 ? `<label style="display:flex;align-items:center;gap:6px;cursor:pointer;font-weight:normal"><input type="checkbox" id="cmp-only-ng" style="width:auto" ${onlyNg ? "checked" : ""}> ${esc(work ? t("設定の差分・未反映・作業外の変更・要確認のある機器だけ表示（{n} 台）", { n: needs }) : t("設定の差分・判定不能の機器だけ表示（{n} 台）", { n: needs }))}</label>` : ""}
      <span style="flex:1"></span>
      <span class="muted" style="font-size:12px" data-tip="${esc(t("この結果を出した時刻。チェックリストを直したあと、ログを取り直したあとは「比較する」をもう一度押してください"))}">${esc(t("比較した時刻: {t}", { t: fmtRunTime(C.at) }))}</span>
      <button class="btn sm" id="cmp-copy" data-tip="${esc(t("結論と機器ごとの結果を、Excel や作業報告に貼れる形（タブ区切り）でコピーします"))}">${esc(t("結果をコピー"))}</button>
      <button class="btn sm" id="cmp-csv" data-tip="${esc(t("機器ごとの結果を export\\compare に CSV で書き出します"))}">${esc(t("CSV に書き出す"))}</button>
      <button class="btn sm" id="cmp-html" data-tip="${esc(t("機器ごとの結果と設定の差分（左右・変わった部分の強調つき）を 1 枚の HTML に書き出します（export\\compare。外部通信なし・スクリプトなし）"))}">${esc(t("HTML レポート"))}</button>
    </div>`;
  out.innerHTML = groupWarn + banner + tools + `
    ${failRows.length ? `<div style="margin-top:10px;font-size:13px;color:var(--err)">${esc(t("⚠ {n} 台は A または B で失敗しているため比較していません（下の表の先頭に赤で示します）", { n: failRows.length }))}</div>` : ""}
    <table style="margin-top:12px"><thead><tr><th>${esc(t("機器名"))}</th><th style="width:150px">${esc(t("差分"))}</th><th style="width:150px" data-tip="${esc(t("show running-config 等の出力だけを比べた行数（追加 / 削除 / 変更 / 順序）"))}">${esc(t("設定（行）"))}</th><th style="width:150px" data-tip="${esc(t("設定以外（show の出力など）の行数。設定が見当たらないときはログ全体"))}">${esc(t("その他（行）"))}</th><th style="width:70px" data-tip="${esc(t("設定の変更の影響の大きさ（読む順番の目安）。アドレス・経路・ACL・ポリシー・ポートの up/down は高、VLAN・管理系は中、説明文は低"))}">${esc(t("影響"))}</th>${cmpChecklist() ? `<th>${esc(t("期待値（チェックリスト）"))}</th>` : ""}${work ? `<th>${esc(t("結論（設定行の照合）"))}</th>` : ""}<th style="width:120px"></th></tr></thead><tbody>
      ${failRows.map(r => `<tr style="background:rgba(239,68,68,.08)">
        <td><b>${esc(r.name)}</b></td>
        <td><span style="color:var(--err)">${esc(t("{w} で失敗", { w: r.fail }))}</span></td>
        <td colspan="${3 + (work ? 1 : 0) + (cmpChecklist() ? 1 : 0)}" class="muted" style="font-size:12px"><div class="clip" data-tip="${esc(r.msg)}">${esc(r.msg)}</div></td>
        <td style="text-align:right;white-space:nowrap">${r.a ? `<span class="link" data-log="${esc(r.a)}">A</span> ` : ""}${r.b ? `<span class="link" data-log="${esc(r.b)}">B</span>` : ""}</td>
      </tr>`).join("")}
      ${rows.map(r => `<tr>
        <td><b>${esc(r.name)}</b>${byName[r.name] && byName[r.name].pairedWith ? ` <span class="muted" style="font-size:11px" data-tip="${esc(t("名前が完全には一致しないため、先頭が同じログ同士を比べています"))}">↔ ${esc(byName[r.name].pairedWith)}</span>` : ""}${cmpDupNote(byName[r.name])}</td>
        <td>${r.err ? `<span style="color:var(--err)">${esc(r.err)}</span>` : `<span style="color:var(--${cmpDiffColor(r)})">${esc(cmpDiffText(r))}</span>`}</td>
        <td class="mono">${r.err ? "" : r.hasConfig ? cmpCountsHTML(r.config) : `<span class="muted">${esc(t("—"))}</span>`}</td>
        <td class="mono">${r.err ? "" : cmpCountsHTML(r.hasConfig ? cmpRest(r) : r.whole)}</td>
        <td>${cmpImpactHTML(r)}</td>
        ${cmpChecklist() ? `<td>${cmpCheckHTML(r.check)}</td>` : ""}
        ${work ? `<td>${verdict(r)}</td>` : ""}
        <td style="text-align:right;white-space:nowrap">${r.err ? "" : `<button class="btn sm" data-diff="${esc(r.name)}">${esc(t("差分を開く"))}</button>`}${r.err || !(r.hasConfig && r.cfgDiff) ? "" : ` <button class="btn sm" data-cmd="rollback" data-dev="${esc(r.name)}" data-tip="${esc(t("作業後（B）の機器を作業前（A）の設定に戻すコマンドを差分から作ります（実行はしません）"))}">${esc(t("戻す"))}</button> <button class="btn sm" data-cmd="forward" data-dev="${esc(r.name)}" data-tip="${esc(t("作業前（A）の機器を作業後（B）と同じにするコマンドを差分から作ります（別の機器への横展開に。実行はしません）"))}">${esc(t("揃える"))}</button>`}</td>
      </tr>`).join("")}
    </tbody></table>
    ${onlyNg ? `<div class="muted" style="font-size:12px;margin-top:8px">${esc(t("ほか {n} 台は問題なし（チェックを外すと表示）", { n: all.length - rows.length }))}</div>` : ""}
    ${onlyA.length || onlyB.length ? `<div class="muted" style="font-size:12px;margin-top:8px">${esc(t("片方の実行にしか含まれていない機器:"))} ${onlyA.length ? esc(t("A のみ: {d}", { d: onlyA.join(", ") })) : ""}${onlyA.length && onlyB.length ? "　" : ""}${onlyB.length ? esc(t("B のみ: {d}", { d: onlyB.join(", ") })) : ""}</div>` : ""}`;
  out.querySelectorAll("[data-diff]").forEach(btn => btn.onclick = () => showDiff(btn.dataset.diff, byName[btn.dataset.diff].a, byName[btn.dataset.diff].b));
  out.querySelectorAll("[data-cmd]").forEach(btn => btn.onclick = () => cmdDialog(btn.dataset.dev, byName[btn.dataset.dev].a, byName[btn.dataset.dev].b, btn.dataset.cmd, b.group || a.group || ""));
  out.querySelectorAll("[data-log]").forEach(el => el.onclick = () => showLog(el.dataset.log));
  const only = document.getElementById("cmp-only-ng");
  if (only) only.onchange = () => { histState.cmpOnlyNg = only.checked; drawCompare(); };
  document.getElementById("cmp-copy").onclick = async () => {
    const tsv = cmpTable(true).map(r => r.map(v => String(v).replace(/[\t\r\n]+/g, " / ")).join("\t")).join("\n");
    await copyText(tsv, t("比較結果をコピーしました"));
  };
  document.getElementById("cmp-html").onclick = async () => {
    const devices = C.failRows.map(r => ({ name: r.name, error: t("{w} で失敗", { w: r.fail }) + ": " + r.msg })).concat(cmpSorted(C).map(({ r }) => ({ name: r.name, pathA: byName[r.name].a, pathB: byName[r.name].b, verdict: work ? cmpVerdictText(r, work) : "", check: cmpChecklist() ? cmpCheckText(r.check) : "", error: r.err || "" })));
    try {
      const path = await App().ExportCompareReport({ title: t("比較レポート") + " " + (a.group || ""), runA: runTitle(a), runB: runTitle(b), work: work ? cmpWorkName(work, works) : "", headline: C.headline || "", lang: LANG, devices });
      await exportDoneDialog(path);
    } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
  };
  document.getElementById("cmp-csv").onclick = async () => {
    try {
      const path = await App().ExportCompareResult(cmpTable(false).map(r => r.map(v => String(v))));
      await exportDoneDialog(path);
    } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
  };
}

// After an export: the file can be opened at once (the default app for
// .html / .csv: OpenExportFile) or its folder shown. ファイルを開く only
// when the binding is there.
function exportDoneDialog(path) {
  const dir = path.slice(0, Math.max(path.lastIndexOf("\\"), path.lastIndexOf("/")));
  const canOpenFile = typeof App().OpenExportFile === "function";
  return new Promise(resolve => {
    const node = h(`<div>
      <h3>${esc(t("書き出しました"))}</h3>
      <p style="font-size:14px;line-height:1.8;margin:4px 0 10px"><span class="muted" style="font-size:12px">${esc(path)}</span></p>
      <div class="modal-actions">
        <button class="btn" id="xd-close">${esc(t("閉じる"))}</button>
        <button class="btn" id="xd-folder">${esc(t("フォルダを開く"))}</button>
        ${canOpenFile ? `<button class="btn primary" id="xd-file" autofocus data-tip="${esc(t("既定のアプリ（ブラウザ・Excel など）で開きます"))}">${esc(t("ファイルを開く"))}</button>` : ""}
      </div>
    </div>`);
    openModal(node, "mid");
    const done = () => { closeModal(); resolve(); };
    node.querySelector("#xd-close").onclick = done;
    node.querySelector("#xd-folder").onclick = async () => { done(); try { await App().OpenExportFolder(dir); } catch (e) { toast(terr(e), "err"); } };
    const f = node.querySelector("#xd-file");
    if (f) f.onclick = async () => { done(); try { await App().OpenExportFile(path); } catch (e) { toast(terr(e), "err"); } };
  });
}

// The comparison as a table (every device, in the order shown, the header
// first). withTitle adds the runs and the verdict above it, for pasting into
// a work report.
function cmpTable(withTitle) {
  const C = histState.cmp;
  const { a, b, work, works } = C;
  const head = [t("機器名"), t("組にした名前"), t("差分"), t("追加（行）"), t("削除（行）"), t("設定 追加"), t("設定 削除"), t("設定 変更"), t("設定 順序"), t("その他 追加"), t("その他 削除"), t("影響")];
  if (cmpChecklist()) head.push(t("期待値"), t("期待値に合わない行"));
  if (work) head.push(t("結論"), t("未反映"), t("要確認"), t("作業外の変更"), t("未反映・要確認の行"), t("作業外の変更の行"));
  const out = [];
  if (withTitle) {
    out.push([t("比較"), `A: ${runTitle(a)}`, `B: ${runTitle(b)}`].concat(work ? [t("作業中の実行: {t}", { t: cmpWorkName(work, works) })] : []));
    if (C.headline) out.push([C.headline]);
    out.push([]);
  }
  out.push(head);
  // A failed device: why, in the 差分 column (there with or without a work).
  C.failRows.forEach(r => out.push([r.name, "", t("{w} で失敗", { w: r.fail }) + ": " + r.msg, "", "", "", "", "", "", "", "", ""]));
  for (const { r } of cmpSorted(C)) {
    const cfg = r.hasConfig ? r.config : null, rest = r.err ? null : (r.hasConfig ? cmpRest(r) : r.whole);
    const row = [r.name, (C.byName[r.name] && C.byName[r.name].pairedWith) || "", r.err ? r.err : cmpDiffText(r), r.err ? "" : r.added, r.err ? "" : r.removed,
      cfg ? cfg.added : "", cfg ? cfg.removed : "", cfg ? (cfg.changed || 0) : "", cfg ? (cfg.moved || 0) : "", rest ? rest.added : "", rest ? rest.removed : "",
      r.err || !r.cfgDiff ? "" : ({ high: t("高"), mid: t("中"), low: t("低") }[r.impact] || "")];
    if (cmpChecklist()) row.push(cmpCheckText(r.check), ((r.check && r.check.results) || []).filter(x => x.status !== "pass").map(x => `${x.status === "fail" ? "✗" : x.status === "missing" ? "－" : "？"} ${x.text}${x.found ? " → " + x.found : ""}`).join("\n"));
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

// The lines outside the configuration listing: the whole logs' counts less
// the listings' (never below zero: the two are computed apart).
function cmpRest(r) {
  const w = r.whole, c = r.config;
  const z = n => Math.max(0, n);
  return { added: z(w.added - c.added), removed: z(w.removed - c.removed), changed: z((w.changed || 0) - (c.changed || 0)), moved: z((w.moved || 0) - (c.moved || 0)) };
}

// Checklists: the expected configuration lines, written like a configuration
// excerpt (an indented line belongs to the margin line above it, which is
// its block). Saved in the vault; chosen in the comparison panel.
async function checklistEditor() {
  let lists = (INV.checklists || []).map(c => ({ name: c.name, lines: c.lines }));
  let cur = lists.find(c => c.name === cmpChecklist()) || lists[0] || null;
  const node = h(`<div>
    <h3>${esc(t("チェックリスト（あるべき設定行）"))}</h3>
    <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("設定の抜き書きのように書きます: 行頭から書いた行はそのまま探す行、字下げした行はその上の行（ブロック）の中で探します。省略形・暗号化されたパスワード・まとめられた VLAN 一覧も照合されます。「!」「#」で始まる行は無視します。"))}</p>
    <div class="row-inline" style="gap:8px;flex-wrap:wrap">
      <select id="ck-sel" class="btn" style="max-width:260px"></select>
      <button class="btn sm" id="ck-new">${esc(t("＋ 新しいチェックリスト"))}</button>
      <span style="flex:1"></span>
      <button class="btn sm" id="ck-del">${esc(t("削除"))}</button>
    </div>
    <div class="field" style="margin-top:8px"><label>${esc(t("名前"))}</label><input id="ck-name" placeholder="${esc(t("例: 標準設定（拠点ルータ）"))}"></div>
    <div class="field"><label>${esc(t("あるべき設定行"))}</label><textarea id="ck-text" class="mono" style="min-height:220px;font-size:12px" spellcheck="false" placeholder="hostname sw-lab-01&#10;ntp server 192.0.2.123&#10;interface GigabitEthernet0/1&#10; switchport mode trunk"></textarea></div>
    <div class="row-inline" style="gap:6px;flex-wrap:wrap;font-size:12px">
      <span class="muted">${esc(t("雛形から足す"))}</span>
      <select id="ck-fam" class="btn sm"><option value="cisco">${esc(t("Cisco IOS 系"))}</option><option value="fortigate">FortiGate</option><option value="junos">Junos</option><option value="yamaha">Yamaha RTX</option></select>
      <select id="ck-kind" class="btn sm"><option value="hostname">${esc(t("ホスト名"))}</option><option value="ntp">NTP</option><option value="dns">DNS</option><option value="syslog">Syslog</option><option value="snmp">${esc(t("SNMP コミュニティ"))}</option><option value="gateway">${esc(t("既定ゲートウェイ"))}</option></select>
      <input id="ck-vals" placeholder="${esc(t("値（複数はカンマ区切り）"))}" style="width:260px">
      <button class="btn sm" id="ck-add">${esc(t("足す"))}</button>
    </div>
    <div class="modal-actions">
      <button class="btn" id="ck-close">${esc(t("閉じる"))}</button>
      <button class="btn primary" id="ck-save">${esc(t("保存"))}</button>
    </div>
  </div>`);
  openModal(node, "wide");
  const sel = node.querySelector("#ck-sel"), nameIn = node.querySelector("#ck-name"), ta = node.querySelector("#ck-text");
  // What the fields held when the list was shown: switching lists, ＋ 新しい,
  // 閉じる and Esc ask before throwing away edits (guardDiscard cannot be
  // used: the fields are reused by every list).
  let snap = "";
  const snapNow = () => JSON.stringify([nameIn.value, ta.value]);
  const dirty = () => snapNow() !== snap;
  async function discardOk() {
    return !dirty() || await subConfirm(esc(t("編集した内容は保存されていません。破棄しますか？")), t("破棄する"));
  }
  async function show() {
    sel.innerHTML = lists.map(c => `<option value="${esc(c.name)}" ${cur && c.name === cur.name ? "selected" : ""}>${esc(c.name)}（${c.lines.length}）</option>`).join("") || `<option value="">${esc(t("（まだありません）"))}</option>`;
    nameIn.value = cur ? cur.name : "";
    ta.value = cur ? await App().FormatChecklistLines(cur.lines) : "";
    node.querySelector("#ck-del").disabled = !cur;
    snap = snapNow();
  }
  sel.onchange = async () => {
    if (!(await discardOk())) { sel.value = cur ? cur.name : ""; return; }
    cur = lists.find(c => c.name === sel.value) || null; await show();
  };
  node.querySelector("#ck-new").onclick = async () => { if (!(await discardOk())) return; cur = null; await show(); nameIn.focus(); };
  node.querySelector("#ck-add").onclick = async () => {
    const vals = node.querySelector("#ck-vals").value.split(/[,、]/).map(s => s.trim()).filter(Boolean);
    if (!vals.length) { toast(t("値を入力してください"), "err"); return; }
    const lines = await App().ChecklistTemplate(node.querySelector("#ck-fam").value, node.querySelector("#ck-kind").value, vals);
    if (!lines.length) { toast(t("この組み合わせの雛形はありません"), "warn"); return; }
    const add = await App().FormatChecklistLines(lines);
    ta.value = (ta.value.replace(/\s+$/, "") + "\n" + add).replace(/^\n/, "");
  };
  node.querySelector("#ck-save").onclick = async () => {
    const name = nameIn.value.trim();
    if (!name) { toast(t("名前を入力してください"), "err"); return; }
    const lines = await App().ParseChecklistText(ta.value);
    if (!lines.length) { toast(t("あるべき設定行を 1 行以上書いてください"), "err"); return; }
    // A new list, or a rename, under a name another list has: refused (it
    // used to replace that list without a word). The backend refuses it
    // too (ChecklistExists / RenameChecklist).
    if (!cur || cur.name !== name) {
      let taken = lists.some(c => c.name === name);
      if (!taken && typeof App().ChecklistExists === "function") { try { taken = !!(await App().ChecklistExists(name)); } catch (e) { /* the save below decides */ } }
      if (taken) { toast(t("同じ名前のチェックリスト「{n}」があります。別の名前にしてください", { n: name }), "err"); return; }
    }
    // A rename is one step in the vault (RenameChecklist): deleting the old
    // list first lost it when the save after it failed.
    if (cur && cur.name !== name) {
      try { await App().RenameChecklist(cur.name, name); } catch (e) { toast(t("名前を変えられません") + ": " + terr(e), "err"); return; }
    }
    try { await App().SaveChecklist({ name, lines }); } catch (e) { toast(t("保存に失敗") + ": " + terr(e), "err"); return; }
    await refreshInventory();
    lists = (INV.checklists || []).map(c => ({ name: c.name, lines: c.lines }));
    cur = lists.find(c => c.name === name) || null;
    histState.k = name;
    // The comparison drawn was judged against the lines before this save.
    histState.cmp = null;
    toast(t("チェックリスト「{n}」を保存しました（{c} 行）", { n: name, c: lines.length }), "ok");
    await show();
  };
  node.querySelector("#ck-del").onclick = async () => {
    if (!cur) return;
    // Over the editor (uiConfirm would replace it).
    if (!(await subConfirm(`<b>${esc(t("チェックリストを削除しますか？"))}</b><br>${esc(cur.name)}`, t("削除"), t("キャンセル")))) return;
    try { await App().DeleteChecklist(cur.name); } catch (e) { toast(terr(e), "err"); return; }
    await refreshInventory();
    lists = (INV.checklists || []).map(c => ({ name: c.name, lines: c.lines }));
    if (histState.k === cur.name) histState.k = "";
    histState.cmp = null;
    cur = lists[0] || null;
    await show();
  };
  // 閉じる is also what Esc presses (app.js): the guard covers both.
  node.querySelector("#ck-close").onclick = async () => { if (!(await discardOk())) return; closeModal(); renderHistoryView(); };
  await show();
}

// 機器ごとの版: every log of one device across the runs, newest first, with
// whether its configuration differs from the version before; any two open
// in the diff window (the older as A).
const dvState = { name: "", a: null, b: null };
async function deviceVersionsDialog() {
  const names = [...new Set((histState.runs || []).flatMap(r => (r.devices || []).filter(d => d.logPath).map(d => d.name)))].sort((x, y) => x.localeCompare(y, undefined, { numeric: true }));
  if (!names.length) { toast(t("ログのある機器がありません"), "warn"); return; }
  if (!names.includes(dvState.name)) { dvState.name = names[0]; dvState.a = dvState.b = null; }
  const node = h(`<div>
    <h3>${esc(t("機器ごとの版"))}</h3>
    <div class="row-inline" style="gap:10px;flex-wrap:wrap">
      <span class="muted" style="font-size:12px">${esc(t("機器"))}</span>
      <select id="dv-name" class="btn" style="max-width:320px">${names.map(n => `<option value="${esc(n)}" ${n === dvState.name ? "selected" : ""}>${esc(n)}</option>`).join("")}</select>
      <span id="dv-summary" class="muted" style="font-size:12px"></span>
      <span style="flex:1"></span>
      <button class="btn primary" id="dv-diff" disabled>${esc(t("A と B の差分を開く"))}</button>
    </div>
    <div id="dv-body" style="margin-top:10px;max-height:60vh;overflow:auto"></div>
    <div class="modal-actions"><button class="btn" id="dv-close">${esc(t("閉じる"))}</button></div>
  </div>`);
  openModal(node, "wide");
  node.querySelector("#dv-close").onclick = closeModal;
  const body = node.querySelector("#dv-body"), sum = node.querySelector("#dv-summary"), go = node.querySelector("#dv-diff");
  let vers = [];
  const fmtSize = n => n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1048576).toFixed(1)} MB`;
  function draw() {
    const pick = v => v && vers.find(x => x.logPath === v);
    const va = pick(dvState.a), vb = pick(dvState.b);
    go.disabled = !(va && vb && va.logPath !== vb.logPath);
    body.innerHTML = vers.length ? `<table><thead><tr><th style="width:150px">${esc(t("日時"))}</th><th style="width:90px">${esc(t("作業"))}</th><th>${esc(t("グループ"))}</th><th>${esc(t("ファイル"))}</th><th style="width:80px">${esc(t("大きさ"))}</th><th style="width:70px">${esc(t("設定"))}</th><th style="width:90px" data-tip="${esc(t("show running-config 等の出力を、時刻・カウンタ・証明書等を除いて 1 つ古い版と比べた結果"))}">${esc(t("前の版から"))}</th><th style="width:170px"></th></tr></thead><tbody>
      ${vers.map(v => `<tr${v.logPath === dvState.a || v.logPath === dvState.b ? ' style="background:rgba(96,165,250,.10)"' : ""}>
        <td class="mono">${esc(fmtRunTime(v.startedAt))}</td><td>${stageBadge(v.stage)}${v.external ? ` <span class="badge">${esc(t("外部"))}</span>` : ""}</td><td>${esc(v.group || "")}</td>
        <td class="mono" style="font-size:12px"><div class="clip" data-tip="${esc(v.logPath)}">${esc(v.file)}</div></td><td class="mono">${esc(fmtSize(v.size))}</td>
        <td>${v.hasConfig ? `<span style="color:var(--ok)">${esc(t("あり"))}</span>` : `<span class="muted">${esc(t("なし"))}</span>`}</td>
        <td>${!v.hasConfig ? "" : v.changed ? `<span style="color:var(--warn)">${esc(t("⚡ 変更"))}</span>` : v.first ? `<span class="muted">${esc(t("（最古）"))}</span>` : `<span class="muted">—</span>`}</td>
        <td style="text-align:right;white-space:nowrap"><button class="btn sm ${v.logPath === dvState.a ? "primary" : ""}" data-dv-a="${esc(v.logPath)}">A</button> <button class="btn sm ${v.logPath === dvState.b ? "primary" : ""}" data-dv-b="${esc(v.logPath)}">B</button> <button class="btn sm" data-dv-log="${esc(v.logPath)}">${esc(t("ログ表示"))}</button></td>
      </tr>`).join("")}</tbody></table>` : `<div class="empty">${esc(t("この機器のログがありません"))}</div>`;
    const withCfg = vers.filter(v => v.hasConfig).length, changed = vers.filter(v => v.changed).length;
    sum.textContent = vers.length ? t("{n} 版（設定あり {c}・設定が変わった版 {m}）", { n: vers.length, c: withCfg, m: changed }) : "";
    body.querySelectorAll("[data-dv-a]").forEach(b => b.onclick = () => { dvState.a = dvState.a === b.dataset.dvA ? null : b.dataset.dvA; draw(); });
    body.querySelectorAll("[data-dv-b]").forEach(b => b.onclick = () => { dvState.b = dvState.b === b.dataset.dvB ? null : b.dataset.dvB; draw(); });
    body.querySelectorAll("[data-dv-log]").forEach(b => b.onclick = () => showLog(b.dataset.dvLog));
  }
  async function load() {
    body.innerHTML = `<div class="muted" style="font-size:13px">${esc(t("読み込み中…"))}</div>`;
    try { vers = await App().DeviceVersions(dvState.name); } catch (e) { vers = []; toast(terr(e), "err"); }
    vers = vers || [];
    // The oldest version with a configuration has nothing to be compared with.
    for (let i = vers.length - 1; i >= 0; i--) if (vers[i].hasConfig) { vers[i].first = true; break; }
    if (!vers.some(v => v.logPath === dvState.a)) dvState.a = null;
    if (!vers.some(v => v.logPath === dvState.b)) dvState.b = null;
    draw();
  }
  node.querySelector("#dv-name").onchange = e => { dvState.name = e.target.value; dvState.a = dvState.b = null; load(); };
  go.onclick = () => {
    let va = vers.find(x => x.logPath === dvState.a), vb = vers.find(x => x.logPath === dvState.b);
    if (!va || !vb) return;
    if ((va.startedAt || "") > (vb.startedAt || "")) { [va, vb] = [vb, va]; toast(t("古い方を A（前）、新しい方を B（後）にして開きます"), "ok"); }
    showDiff(dvState.name, va.logPath, vb.logPath);
  };
  await load();
}

// 戻す / 揃える: the commands made from the comparison of one device, in a
// dialog to copy, save as a command set, or put into the run tab's 今回だけ
// 入力 (never sent from here).
// The command lines of the dialog's text: comments ("!" / "#") and blank
// lines dropped, indentation dropped (a device ignores it; a leading blank
// would be typed to one that does not).
function cmdLinesOf(text) { return text.split(/\r?\n/).map(s => s.trim()).filter(s => s && !s.startsWith("!") && !s.startsWith("#")); }
function cmdDirLabel(dir) { return dir === "forward" ? t("揃える（A → B）") : t("戻す（B → A）"); }
function cmdFamilyLabel(f) { return { cisco: t("Cisco IOS 系"), fortigate: "FortiGate", junos: "Junos", yamaha: "Yamaha RTX", comware: t("Comware/VRP 系"), generic: t("不明（一覧のみ）") }[f] || f; }
// The comment line put above the generated commands (the copy, the dialog's
// text, the paste confirmation): which device and which way, so a paste into
// the wrong one of eight windows shows itself. Dropped by cmdLinesOf, so a
// command set or the run tab never gets it. "!" is Cisco's comment, "#" the
// others'.
function cmdHeaderLine(family, dir, name) {
  return `${family === "cisco" ? "!" : "#"} PalaTerm ${cmdDirLabel(dir)} ${name} ${fmtRunTime(new Date().toISOString())}`;
}
async function cmdDialog(name, pathA, pathB, dir, group) {
  // The device of the compared run's group only: a same-name device of
  // another group is another device (its OS type, its run tab row).
  const dev = (INV.devices || []).find(d => d.name === name && (d.group || "") === (group || "")) || null;
  let g;
  try { g = await App().GenerateCommands(pathA, pathB, dir, dev ? dev.osType || "" : ""); }
  catch (e) { toast(t("コマンドを作れません") + ": " + terr(e), "err"); return; }
  const text = (g.lines || []).map(l => l.text).join("\n");
  const shown = text ? cmdHeaderLine(g.family, dir, name) + "\n" + text : "";
  const now = new Date(), p2 = n => String(n).padStart(2, "0");
  // Local time (toISOString gave UTC: nine hours off in Japan).
  const stamp = `${now.getFullYear()}${p2(now.getMonth() + 1)}${p2(now.getDate())}_${p2(now.getHours())}${p2(now.getMinutes())}`;
  const noDevTip = group ? t("比較した実行のグループ「{g}」に同名の機器がありません。実行タブへは渡せません（コピーして使ってください）", { g: group })
    : t("この機器はグループ未設定の機器一覧に無いため、実行タブへは渡せません（コピーして使ってください）");
  const notes = [];
  if (g.noConfig) notes.push(t("A か B のログに設定の表示（show running-config 等）が無いため作れません"));
  else if (g.empty) notes.push(t("差分はありますが、コマンドにできる行がありませんでした"));
  if (g.manual) notes.push(t("「! 手で確認」の行が {n} 行あります（機械的に逆にできない行）", { n: g.manual }));
  if (g.skipped) notes.push(t("証明書・暗号化値・UUID の差分 {n} 行は対象外です", { n: g.skipped }));
  if (g.reordered) notes.push(t("依存関係（参照 → 定義）の順に並べ替えています"));
  (g.warnings || []).forEach(w => notes.push(w));
  const node = h(`<div>
    <h3>${esc(t("{d}: {n}", { d: cmdDirLabel(dir), n: name }))}</h3>
    <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("差分から機械的に作ったコマンドです。投入前に必ず目で確認してください。機器の種類は「{f}」と判断しました（{by}）。", { f: cmdFamilyLabel(g.family), by: g.familyBy === "osType" ? t("OS タイプから") : t("設定の内容から") }))}</p>
    ${notes.length ? `<div class="warn-box" style="font-size:12px">${notes.map(esc).join("<br>")}</div>` : ""}
    <div class="field"><textarea id="cg-text" class="mono" style="min-height:260px;font-size:12px" spellcheck="false">${esc(shown)}</textarea></div>
    <div class="row-inline" style="gap:8px;flex-wrap:wrap">
      <input id="cg-name" placeholder="${esc(t("コマンドセット名"))}" value="${esc((dir === "forward" ? t("揃える") : t("戻す")) + "-" + name + "-" + stamp)}" style="flex:1;min-width:200px">
      <button class="btn" id="cg-save" ${text ? "" : "disabled"}>${esc(t("コマンドセットとして保存"))}</button>
    </div>
    <div class="modal-actions">
      <button class="btn" id="cg-close">${esc(t("閉じる"))}</button>
      <button class="btn" id="cg-copy" ${text ? "" : "disabled"}>${esc(t("コピー"))}</button>
      <button class="btn primary" id="cg-run" ${text && dev ? "" : "disabled"} data-tip="${esc(dev ? t("実行タブを開き、この機器だけを対象にして「今回だけ入力」にコマンドを入れます（作業タイミングは「作業中」）。実行はまだしません") : noDevTip)}">${esc(t("実行タブへ（今回だけ入力）"))}</button>
    </div>
  </div>`);
  openModal(node, "wide");
  const ta = node.querySelector("#cg-text");
  node.querySelector("#cg-close").onclick = closeModal;
  node.querySelector("#cg-copy").onclick = () => copyText(ta.value, t("コマンドをコピーしました"));
  node.querySelector("#cg-save").onclick = async () => {
    const nm = node.querySelector("#cg-name").value.trim();
    if (!nm) { toast(t("コマンドセット名を入力してください"), "err"); return; }
    if (nm.startsWith("__")) { toast(t("「__」で始まる名前は使えません（内部の目印と重なります）"), "err"); return; }
    const cmds = cmdLinesOf(ta.value).map(s => ({ text: s, pauseSec: 1, serialSec: 1 }));
    if (!cmds.length) { toast(t("コマンドがありません"), "err"); return; }
    // Over the dialog (uiConfirm would replace it, and the commands with it).
    if ((INV.commandSets || []).some(s => s.name === nm) && !(await subConfirm(`<b>${esc(t("上書きしますか？"))}</b><br>${esc(t("同じ名前のコマンドセット「{n}」があります。上書きします", { n: nm }))}`, t("上書き"), t("キャンセル")))) return;
    try { await App().SaveCommandSet({ name: nm, commands: cmds, pauseOff: false }); await refreshInventory(); toast(t("コマンドセット「{n}」を保存しました", { n: nm }), "ok"); }
    catch (e) { toast(t("保存に失敗") + ": " + terr(e), "err"); }
  };
  node.querySelector("#cg-run").onclick = async () => {
    if (!dev) return;
    const lines = cmdLinesOf(ta.value);
    if (!lines.length) { toast(t("コマンドがありません"), "err"); return; }
    for (const k in runState) delete runState[k];
    runGroupSel = dev.group || RUN_UNGROUPED;
    // This device by its exact name (runFilter.exact, views2.js), not the
    // substring filter: "lab-sw1" also showed lab-sw10… as targets.
    runFilter.text = ""; runFilter.site = ""; runFilter.status = "";
    if (typeof runFilterSetExact === "function") runFilterSetExact(dev.name); else runFilter.exact = dev.name;
    // The work itself: the comparison finds it between 作業前 and 作業後.
    runStage = "during";
    runSetSel = RUN_ADHOC; runAdHoc = lines.join("\n");
    closeModal();
    await switchTab("run");
    toast(t("『{n}』だけを対象に、作業中として実行します。「今回だけ入力」にコマンドを入れました。内容を確かめてから「▶ 一括実行」を押してください", { n: dev.name }), "ok");
  };
}

// The diff opens in its own WinMerge-style window (PalaTerm.exe --diff).
async function showDiff(name, pathA, pathB) {
  try { await App().OpenDiffWindow(pathA, pathB, name); }
  catch (e) { toast(t("差分を開けません") + ": " + terr(e), "err"); }
}
