// Command sets, Run, Log Settings, and OS Types tabs.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

// ---- Command sets tab ----
function renderCommands() {
  const root = document.getElementById("tab-commands");
  const rows = (INV.commandSets || []).map(s => `
    <tr data-key="${esc(s.name)}">
      <td class="drag-handle" data-tip="${dragHandleTip()}">⠿</td>
      <td><b>${esc(s.name)}</b></td>
      <td>${esc(t("{n} コマンド", { n: (s.commands || []).length }))}</td>
      <td class="mono muted">${esc((s.commands || []).slice(0, 3).map(c => c.text).join("  /  "))}${(s.commands || []).length > 3 ? " …" : ""}</td>
      <td style="text-align:right">
        <button class="btn sm act-edit" data-edit="${esc(s.name)}">${esc(t("編集"))}</button>
        <button class="btn sm act-copy" data-copy="${esc(s.name)}">${esc(t("複製"))}</button>
        <button class="btn sm act-del" data-del="${esc(s.name)}">${esc(t("削除"))}</button>
      </td>
    </tr>`).join("");

  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("コマンドセット"))}</div>
        <div class="page-sub">${esc(t("機器ごとに割り当てて一括実行するコマンド群。書き出しは「機器」タブの一式書出（グループ単位、または全機器）にまとまっています。ここでは1ファイル（CSV / 旧 .list）を単体で読み込めます"))}</div></div>
      <div class="row-inline">
        <button class="btn" id="cs-imp" data-tip="${esc(t("コマンドセット 1 ファイル（CSV / 旧 .list）を読み込みます"))}">${esc(t("読込"))}</button>
        <button class="btn primary" id="add-set">${esc(t("＋ セットを追加"))}</button>
      </div>
    </div>
    <div class="panel">
      ${(INV.commandSets || []).length === 0
        ? `<div class="empty">${esc(t("コマンドセットがありません。"))}</div>`
        : `<table><thead><tr><th style="width:30px"></th><th>${esc(t("名前"))}</th><th>${esc(t("コマンド数"))}</th><th>${esc(t("プレビュー"))}</th><th></th></tr></thead>
           <tbody id="cs-body">${rows}</tbody></table>`}
    </div>`;

  wireRowReorder(root.querySelector("#cs-body"), async order => {
    try { await App().ReorderCommandSets(order); await refreshInventory(); }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); await refreshInventory(); }
  });
  document.getElementById("add-set").onclick = () => editCommandSet(null);
  document.getElementById("cs-imp").onclick = async () => {
    try {
      const nm = await App().ImportCommandSetFile();
      if (nm) { await refreshInventory(); toast(t("「{n}」を読み込みました", { n: nm }), "ok"); }
    } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
  };
  root.querySelectorAll("[data-edit]").forEach(b => b.onclick = () =>
    editCommandSet(INV.commandSets.find(s => s.name === b.dataset.edit)));
  root.querySelectorAll("[data-copy]").forEach(b => b.onclick = async () => {
    try { const nm = await App().CopyCommandSet(b.dataset.copy); await refreshInventory(); toast(t("複製しました: {n}", { n: nm }), "ok"); }
    catch (e) { toast(t("複製失敗") + ": " + terr(e), "err"); }
  });
  root.querySelectorAll("[data-del]").forEach(b => b.onclick = async () => {
    if (!(await uiConfirm({ title: t("コマンドセットを削除"), message: t("コマンドセット「<b>{n}</b>」を削除しますか？", { n: esc(b.dataset.del) }), okLabel: t("削除"), danger: true }))) return;
    await App().DeleteCommandSet(b.dataset.del); await refreshInventory(); toast(t("削除しました"), "ok");
  });
}

function editCommandSet(set) {
  const s = set || { name: "", commands: [] };
  // Working copy: one row per command, each with its own pause (sec).
  let cmds = (s.commands || []).map(c => ({
    text: c.text || "", pauseSec: c.pauseSec ?? 1, serialSec: c.serialSec ?? 1,
  }));
  if (cmds.length === 0) cmds = [{ text: "", pauseSec: 1, serialSec: 1 }];

  const node = h(`<div>
    <h3>${esc(set ? t("コマンドセットを編集") : t("コマンドセットを追加"))}</h3>
    <div class="field"><label>${esc(t("セット名"))} <span class="req">${esc(t("必須"))}</span></label><input id="s-name" value="${esc(s.name)}" ${set ? "readonly" : ""}></div>
    <label style="font-size:12px;color:var(--text-dim)">${esc(t("コマンド（1行1コマンド・待機秒はコマンドごとに指定）"))}</label>
    <div class="scroll"><table style="margin-top:6px">
      <thead><tr>
        <th style="width:26px"></th><th style="width:36px">#</th><th>${esc(t("コマンド"))}</th>
        <th style="width:110px">${esc(t("待機(リモート)"))}</th><th style="width:110px">${esc(t("待機(シリアル)"))}</th><th style="width:76px"></th>
      </tr></thead>
      <tbody id="cmd-rows"></tbody>
    </table></div>
    <button class="btn sm" id="add-cmd" type="button" style="margin-top:8px">${esc(t("＋ 行を追加"))}</button>
    <div class="muted" style="font-size:12px;margin-top:6px">${esc(t("貼り付けは「行を追加」後の各コマンド欄へ。複数行をまとめて貼るとその行数ぶん自動で分割されます。"))}</div>
    <div class="modal-actions">
      <button class="btn" id="cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="save">${esc(t("保存"))}</button>
    </div>
  </div>`);
  openModal(node);

  const tbody = node.querySelector("#cmd-rows");
  function collect() {
    const rows = tbody.querySelectorAll("tr");
    const arr = [];
    rows.forEach(r => arr.push({
      text: r.querySelector(".c-text").value,
      pauseSec: parseInt(r.querySelector(".c-pause").value, 10) || 0,
      serialSec: parseInt(r.querySelector(".c-spause").value, 10) || 0,
    }));
    return arr;
  }
  function render() {
    tbody.innerHTML = cmds.map((c, i) => `
      <tr data-key="${i}">
        <td class="drag-handle" data-tip="${dragHandleTip()}">⠿</td>
        <td class="muted">${i + 1}</td>
        <td><input class="c-text mono" style="width:100%" value="${esc(c.text)}" placeholder="show running-config"></td>
        <td><input class="c-pause" type="number" style="width:100%" value="${c.pauseSec}"></td>
        <td><input class="c-spause" type="number" style="width:100%" value="${c.serialSec}"></td>
        <td style="white-space:nowrap">
          <button class="btn sm" type="button" data-ins="${i}" title="${esc(t("この行の下に挿入"))}">＋</button>
          <button class="btn sm act-del" type="button" data-rm="${i}">×</button>
        </td>
      </tr>`).join("");
    tbody.querySelectorAll("[data-rm]").forEach(btn => btn.onclick = () => {
      cmds = collect(); cmds.splice(parseInt(btn.dataset.rm, 10), 1);
      if (cmds.length === 0) cmds = [{ text: "", pauseSec: 1, serialSec: 1 }];
      render();
    });
    // Insert a blank row directly below this one, and focus it.
    tbody.querySelectorAll("[data-ins]").forEach(btn => btn.onclick = () => {
      const i = parseInt(btn.dataset.ins, 10);
      cmds = collect();
      cmds.splice(i + 1, 0, { text: "", pauseSec: cmds[i].pauseSec, serialSec: cmds[i].serialSec });
      render();
      const inputs = tbody.querySelectorAll(".c-text");
      if (inputs[i + 1]) inputs[i + 1].focus();
    });
    // Multi-line paste in a command field splits into multiple rows.
    tbody.querySelectorAll(".c-text").forEach((inp, i) => inp.addEventListener("paste", e => {
      const txt = (e.clipboardData || window.clipboardData).getData("text");
      if (txt && txt.includes("\n")) {
        e.preventDefault();
        cmds = collect();
        const lines = txt.split("\n").map(l => l.trim()).filter(l => l);
        const add = lines.map(text => ({ text, pauseSec: cmds[i].pauseSec, serialSec: cmds[i].serialSec }));
        cmds.splice(i, 1, ...add);
        render();
      }
    }));
    // Drag-reorder rows; collect() reads the new DOM order.
    wireRowReorder(tbody, () => { cmds = collect(); render(); });
  }
  render();

  node.querySelector("#add-cmd").onclick = () => {
    cmds = collect(); cmds.push({ text: "", pauseSec: 1, serialSec: 1 }); render();
  };
  node.querySelector("#cancel").onclick = closeModal;
  node.querySelector("#save").onclick = async () => {
    const name = node.querySelector("#s-name").value.trim();
    if (!name) { toast(t("セット名は必須です"), "err"); return; }
    const commands = collect().filter(c => c.text.trim().length > 0)
      .map(c => ({ text: c.text.trim(), pauseSec: c.pauseSec, serialSec: c.serialSec }));
    try { await App().SaveCommandSet({ name, commands }); closeModal(); await refreshInventory(); toast(t("保存しました"), "ok"); }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  };
}

// ---- Run tab ----
let running = false;
// True between pressing ■ 中止 and the backend's run:done, so the button can
// say 中止中… and the completion toast can say what was stopped.
let cancelling = false;
// Devices the last run left unfinished: real failures plus the ones 中止 cut
// off or never started. Both can be re-run with one button.
function unfinishedNames() {
  return Object.keys(runState).filter(n => ["error", "canceled"].includes((runState[n] || {}).phase));
}
// Group last chosen with the run tab's group selector (kept across re-renders
// so the selector and the heading show what is targeted).
let runGroupSel = "";
// 作業タイミング of the next run: "" / before / during / after. It goes into
// the run folder and log file names (…_before.txt) so the logs of one
// maintenance job can be told apart, and the 実行履歴 tab can pair a before
// with an after and show what changed.
let runStage = "";
const STAGES = ["", "before", "during", "after"];
// Excel-like sort and filter of the run table. Display only: the backend
// runs devices in inventory order. While a filter is active, only the
// VISIBLE checked devices run, so a row the filter hides can never be run
// by mistake (the toolbar says how many hidden rows are checked).
let runSort = { key: "", dir: 1 };
const runFilter = { text: "", site: "", status: "" };
const PHASE_ORDER = { error: 0, canceled: 1, connecting: 2, login: 3, running: 4, saving: 5, queued: 6, done: 7, off: 8 };
function runFilterActive() { return !!(runFilter.text || runFilter.site || runFilter.status); }
function runVisible(devs) {
  const q = runFilter.text.trim().toLowerCase();
  let out = devs.filter(d => {
    const st = runState[d.name] || { phase: d.enabled ? "queued" : "off" };
    if (q && ![d.name, d.host, d.site || "", d.commandSet || ""].some(v => String(v).toLowerCase().includes(q))) return false;
    if (runFilter.site && (d.site || "") !== runFilter.site) return false;
    if (runFilter.status && st.phase !== runFilter.status) return false;
    return true;
  });
  if (runSort.key) {
    const val = d => {
      if (runSort.key === "phase") { const st = runState[d.name] || { phase: d.enabled ? "queued" : "off" }; return PHASE_ORDER[st.phase] ?? 9; }
      return String(d[runSort.key] || "").toLowerCase();
    };
    out = out.slice().sort((a, b) => { const x = val(a), y = val(b); return (x < y ? -1 : x > y ? 1 : 0) * runSort.dir; });
  }
  return out;
}
// Devices the run buttons act on: the checked ones of the group, narrowed to
// the visible rows while a filter is on.
function runTargetDevices() {
  const devs = runGroupSel ? (INV.devices || []).filter(d => (d.group || "") === runGroupSel) : [];
  return (runFilterActive() ? runVisible(devs) : devs).filter(d => d.enabled);
}
function stageLabel(st) {
  return ({ "": t("指定なし"), before: t("作業前"), during: t("作業中"), after: t("作業後"), work: t("作業中") })[st] || st;
}

// Fraction of a device's whole run that is behind it, as a percentage.
// Connect and login get fixed slices up front so the bar moves as soon as work
// starts; the command phase fills the long middle; a device that errored keeps
// the position it reached, which is what says "it died during login".
function runPct(st) {
  const total = st.total || 0;
  const cmdPart = total > 0 ? Math.round((st.done || 0) / total * 70) : 35;
  switch (st.phase) {
    case "connecting": return 10;
    case "login": return 25;
    case "running": return 25 + cmdPart;
    case "saving": return 97;
    case "done": return 100;
    case "error": return Math.max(12, 25 + cmdPart);
    case "canceled": return st.total > 0 ? Math.max(12, 25 + cmdPart) : 0;
    default: return 0; // queued / off
  }
}

function renderRun() {
  const root = document.getElementById("tab-run");
  // Targets appear only after a group is chosen (no accidental all-device
  // runs). Every device of the group is listed so 実行対象 can be toggled
  // right here; unchecked ones show as 対象外.
  const devs = runGroupSel
    ? (INV.devices || []).filter(d => (d.group || "") === runGroupSel)
    : [];
  const visible = runVisible(devs);
  const filtered = runFilterActive();
  // Keep the filter box's focus and caret across the re-render.
  const focusId = document.activeElement && document.activeElement.id;
  const focusPos = focusId === "run-filter" ? document.activeElement.selectionStart : 0;
  const rows = visible.map(d => {
    const st = runState[d.name] || { phase: d.enabled ? "queued" : "off" };
    // One continuous bar per device across 接続 → ログイン → コマンド → 保存, so a
    // row shows how far along it is even before the first command is sent (the
    // bar used to appear only once commands started, leaving connect/login blank).
    const pct = runPct(st);
    const tip = st.total > 0
      ? t("{a} / {b} コマンド完了", { a: st.done || 0, b: st.total })
      : statusLabel(st.phase);
    // The number line only means something once a command count is known; the
    // phase itself is already spelled out beside the status dot. During a
    // command's configured 待機 the seconds still to go ride alongside it, so a
    // long pause reads as a countdown instead of a frozen row.
    // Beside the count: how long until *all* of this device's remaining
    // commands are done (not just the current command's pause) — refreshed
    // every second by updateEta via the data-dev hook.
    const eta = devEta(d.name, st);
    const pnum = st.total > 0 ? `<div class="pnum">${st.done || 0}/${st.total}<span class="pwait pdev-eta" data-dev="${esc(d.name)}">${eta ? " " + esc(eta) : ""}</span></div>` : "";
    const pbar = st.phase === "off" ? ""
      : `<div class="pbar" data-tip="${esc(tip)}"><i class="pf-${st.phase}" style="width:${pct}%"></i></div>${pnum}`;
    return `<tr>
      <td><input type="checkbox" data-enable="${esc(d.name)}" ${d.enabled ? "checked" : ""} ${running ? "disabled" : ""}></td>
      <td><span class="dot st-${st.phase === "off" ? "queued" : st.phase}"></span>${statusLabel(st.phase)}${pbar}</td>
      <td><div class="clip" data-tip="${esc(d.name)}"><b>${esc(d.name)}</b></div></td>
      <td class="mono"><div class="clip" data-tip="${esc(d.host)}">${esc(d.host)}</div></td>
      <td><div class="clip" data-tip="${esc(d.site || "")}">${esc(d.site || "")}</div></td>
      <td class="muted"><div class="clip" data-tip="${esc(d.commandSet || "")}">${esc(d.commandSet || t("（なし）"))}</div></td>
      <td class="muted mono"><div class="clip" data-tip="${esc(fmtRunMsg(st.message))}">${esc(fmtRunMsg(st.message))}</div></td>
      <td style="text-align:right;white-space:nowrap">
        ${st.logPath ? `<span class="link" data-log="${esc(st.logPath)}">${esc(t("ログ表示"))}</span> ` : ""}
        ${!running && /ホストキーが前回接続時と異なります/.test(st.message || "") ? `<button class="btn sm" data-hk="${esc(d.name)}" style="color:var(--warn);border-color:var(--warn)">${esc(t("鍵を許可して再実行"))}</button> ` : ""}
        <button class="btn sm act-conn" data-term="${esc(d.name)}">${esc(t("接続"))}</button>
      </td>
    </tr>`;
  }).join("");

  const enabledCount = (filtered ? visible : devs).filter(d => d.enabled).length;
  const hiddenChecked = filtered ? devs.filter(d => d.enabled && !visible.includes(d)).length : 0;
  const sites = [...new Set(devs.map(d => d.site || "").filter(Boolean))].sort();
  const sortArrow = k => runSort.key === k ? (runSort.dir > 0 ? " ▲" : " ▼") : "";
  const th = (k, label, w) => `<th ${w ? `style="width:${w}"` : ""} class="sortable" data-sort="${k}" data-tip="${esc(t("クリックで並び替え"))}">${esc(label)}${sortArrow(k)}</th>`;
  const vals = Object.values(runState);
  const failedCount = vals.filter(s => s.phase === "error").length;
  const canceledCount = vals.filter(s => s.phase === "canceled").length;
  const retryLabel = failedCount && canceledCount ? t("↻ 失敗・中断のみ再実行（{n}台）", { n: failedCount + canceledCount })
    : canceledCount ? t("↻ 中断分のみ再実行（{n}台）", { n: canceledCount })
    : t("↻ 失敗のみ再実行（{n}台）", { n: failedCount });
  const hasResults = vals.length > 0;
  const groups = INV.deviceGroups || [];
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("実行"))}${runGroupSel ? ` <span style="font-size:15px;font-weight:600;color:var(--accent)">${esc(t("グループ「{g}」", { g: runGroupSel }))}</span>` : ""}</div>
        <div class="page-sub">${esc(runGroupSel ? (filtered ? t("絞り込み中: 表示 {b} 台のうち対象 {a} 台（表示されている機器だけが実行されます）", { a: enabledCount, b: visible.length }) : t("対象 {a} / {b} 台（チェック=実行対象。Shift+クリックで範囲をまとめて切替）", { a: enabledCount, b: devs.length })) : t("まず「グループで対象を選択…」から実行するグループを選んでください"))}</div></div>
    </div>
    <div class="run-toolbar">
      <button class="btn run" id="btn-run" ${running || enabledCount === 0 ? "disabled" : ""}>${esc(filtered ? t("▶ 表示中の {n} 台を実行", { n: enabledCount }) : t("▶ 一括実行"))}</button>
      <button class="btn" id="btn-dry" ${running || enabledCount === 0 ? "disabled" : ""} data-tip="${esc(t("接続とログインだけを行い、コマンドは送りません。ログも残しません。認証情報や到達性の事前確認に"))}">${esc(t("⚡ 接続確認のみ"))}</button>
      <button class="btn danger" id="btn-cancel" ${running && !cancelling ? "" : "disabled"}>${esc(cancelling ? t("中止中…") : t("■ 中止"))}</button>
      ${!running && (failedCount || canceledCount) ? `<button class="btn" id="btn-retry" style="color:var(--err);border-color:var(--err)">${esc(retryLabel)}</button>` : ""}
      ${!running && hasResults ? `<button class="btn" id="btn-clear" data-tip="${esc(t("表の結果を消して全機器を「待機」に戻します（ログは残ります）"))}">${esc(t("✕ 結果をクリア"))}</button>` : ""}
      ${groups.length ? `<select id="run-grp" class="btn" style="padding-right:8px" ${running ? "disabled" : ""}>
        <option value="">${esc(t("グループで対象を選択…"))}</option>
        ${groups.map(g => `<option value="${esc(g.name)}" ${g.name === runGroupSel ? "selected" : ""}>${esc(t("{g}（{n}台）", { g: g.name, n: groupCount(g.name) }))}</option>`).join("")}
      </select>` : ""}
      <select id="run-stage" class="btn" style="padding-right:8px" ${running ? "disabled" : ""} data-tip="${esc(t("作業前／作業中／作業後のどのタイミングの取得かを選びます。ログのフォルダ名とファイル名に付き、実行履歴タブで作業前と作業後を比較できます"))}">
        ${STAGES.map(st => `<option value="${st}" ${st === runStage ? "selected" : ""}>${esc(st ? t("作業タイミング: {s}", { s: stageLabel(st) }) : t("作業タイミング: 指定なし"))}</option>`).join("")}
      </select>
      <button class="btn" id="btn-logdir">${esc(t("ログフォルダを開く"))}</button>
      <div class="run-stat">
        <div>${esc(t("成功"))} <b id="rc-ok" style="color:var(--ok)">0</b></div>
        <div>${esc(t("失敗"))} <b id="rc-err" style="color:var(--err)">0</b></div>
        <div>${esc(t("実行中"))} <b id="rc-run">0</b></div>
        <div><span data-tip="${esc(t("完了済みコマンド数と経過時間から算出した全体の予想残り時間。実行が進むほど精度が上がります"))}">${esc(t("予想残り"))}</span> <b id="rc-eta">${running ? computeEta() : "—"}</b></div>
      </div>
    </div>
    ${runGroupSel && devs.length ? `<div class="run-toolbar" style="margin-top:-8px;gap:8px">
      <input id="run-filter" class="btn" style="width:280px;text-align:left;cursor:text" placeholder="${esc(t("絞り込み（ホスト名・IP・拠点・コマンドセット）"))}" value="${esc(runFilter.text)}">
      <select id="run-filter-site" class="btn" style="padding-right:8px"><option value="">${esc(t("拠点: すべて"))}</option>${sites.map(s => `<option value="${esc(s)}" ${s === runFilter.site ? "selected" : ""}>${esc(s)}</option>`).join("")}</select>
      <select id="run-filter-status" class="btn" style="padding-right:8px"><option value="">${esc(t("状態: すべて"))}</option>${["queued", "off", "done", "error", "canceled", "running"].map(p => `<option value="${p}" ${p === runFilter.status ? "selected" : ""}>${esc(statusLabel(p))}</option>`).join("")}</select>
      ${filtered ? `<button class="btn sm" id="run-filter-clear">${esc(t("✕ 絞り込み解除"))}</button>` : ""}
      ${runSort.key ? `<button class="btn sm" id="run-sort-clear">${esc(t("並び順を戻す"))}</button>` : ""}
      ${hiddenChecked ? `<span style="font-size:12px;color:var(--warn);border:1px solid var(--warn);border-radius:8px;padding:5px 10px">${esc(t("⚠ 非表示の行にチェック済みが {n} 台あります。この実行には含まれません（含めるには絞り込みを解除）", { n: hiddenChecked }))}</span>` : ""}
    </div>` : ""}
    <div class="panel">
      ${devs.length === 0 ? `<div class="empty">${esc(runGroupSel ? t("このグループに実行対象の機器がありません。機器一覧でチェックしてください。") : t("「グループで対象を選択…」から実行するグループを選んでください。"))}</div>`
        : visible.length === 0 ? `<div class="empty">${esc(t("絞り込みに一致する機器がありません"))}</div>`
        : `<table class="fixed"><thead><tr>
           <th style="width:36px"><input type="checkbox" id="run-chk-all" ${visible.length && visible.every(d => d.enabled) ? "checked" : ""} ${running ? "disabled" : ""} title="${esc(filtered ? t("表示中の行を全選択/全解除") : t("全選択/全解除"))}"></th>
           ${th("phase", t("状態"), "170px")}${th("name", t("ホスト名"), "200px")}${th("host", t("IPアドレス"), "140px")}
           ${th("site", t("拠点名"), "120px")}${th("commandSet", t("コマンドセット"), "190px")}
           <th>${esc(t("メッセージ"))}</th><th style="width:230px"></th></tr></thead><tbody id="run-body">${rows}</tbody></table>`}
    </div>`;

  // Filter / sort wiring. The text box re-renders on every keystroke and the
  // focus is put back afterwards (see focusId above).
  const fIn = document.getElementById("run-filter");
  if (fIn) {
    fIn.addEventListener("input", () => { runFilter.text = fIn.value; renderRun(); });
    if (focusId === "run-filter") { fIn.focus(); try { fIn.setSelectionRange(focusPos, focusPos); } catch (e) { } }
  }
  const fSite = document.getElementById("run-filter-site");
  if (fSite) fSite.onchange = () => { runFilter.site = fSite.value; renderRun(); };
  const fSt = document.getElementById("run-filter-status");
  if (fSt) fSt.onchange = () => { runFilter.status = fSt.value; renderRun(); };
  const fClr = document.getElementById("run-filter-clear");
  if (fClr) fClr.onclick = () => { runFilter.text = runFilter.site = runFilter.status = ""; renderRun(); };
  const sClr = document.getElementById("run-sort-clear");
  if (sClr) sClr.onclick = () => { runSort = { key: "", dir: 1 }; renderRun(); };
  root.querySelectorAll("th.sortable").forEach(el => el.onclick = () => {
    const k = el.dataset.sort;
    runSort = runSort.key === k ? { key: k, dir: -runSort.dir } : { key: k, dir: 1 };
    renderRun();
  });
  root.querySelectorAll("[data-hk]").forEach(b => b.onclick = async () => {
    const n = b.dataset.hk;
    const ok = await uiConfirm({
      title: t("ホストキーが前回と異なります"),
      message: t("機器「<b>{n}</b>」のホストキーが前回接続時と変わっています。<br><b>機器を交換した／OSを再インストールした</b>場合だけ許可してください。心当たりがなければ、なりすましの可能性があるため許可せずネットワーク管理者に確認してください。", { n: esc(n) }),
      okLabel: t("機器を交換したので許可して再実行"),
      danger: true,
    });
    if (!ok) return;
    try { await App().ClearHostKeys(n); doRun([n], lastRunDry, true); }
    catch (e) { toast(terr(e), "err"); }
  });

  document.getElementById("btn-run").onclick = () => startRun(false);
  document.getElementById("btn-dry").onclick = () => startRun(true);
  const stageSel = document.getElementById("run-stage");
  if (stageSel) stageSel.onchange = () => { runStage = stageSel.value; };
  document.getElementById("btn-cancel").onclick = async () => {
    // Stopping is immediate for queued devices; a device in the middle of a
    // command stops as soon as that command's read returns. The confirm
    // prevents a stray click from killing a long batch.
    const inFlight = Object.values(runState).filter(s => ["connecting", "login", "running", "saving"].includes(s.phase)).length;
    const queued = Object.values(runState).filter(s => s.phase === "queued").length;
    const ok = await uiConfirm({
      title: t("一括実行を中止"),
      message: t("実行中 <b>{a} 台</b>を打ち切り、未実行 <b>{b} 台</b>は開始しません。<br>完了済みの機器のログはそのまま残ります。中止しますか？", { a: inFlight, b: queued }),
      okLabel: t("■ 中止する"),
      danger: true,
    });
    if (!ok || !running) return;
    cancelling = true;
    renderRun();
    App().CancelRun();
  };
  const btnRetry = document.getElementById("btn-retry");
  if (btnRetry) btnRetry.onclick = retryUnfinished;
  const btnClear = document.getElementById("btn-clear");
  if (btnClear) btnClear.onclick = () => { for (const k in runState) delete runState[k]; renderRun(); };
  document.getElementById("btn-logdir").onclick = () => App().OpenLogDir();
  const runGrp = document.getElementById("run-grp");
  if (runGrp) runGrp.onchange = async () => {
    // Results (and the 失敗のみ再実行 button) belong to the previous group's
    // run — warn before dropping them along with the switch.
    const failed = unfinishedNames().length;
    if (failed) {
      const ok = await uiConfirm({
        title: t("実行結果のクリア"),
        message: t("グループを切り替えると、前回の実行結果と再実行ボタン（失敗・中断 {n}台）は消えます。<br>切り替えますか？", { n: failed }),
        okLabel: t("切り替える"),
        danger: true,
      });
      if (!ok) { runGrp.value = runGroupSel; return; }
    }
    for (const k in runState) delete runState[k];
    // Each device keeps its own チェック state — switching groups no longer
    // rewrites Enabled flags, so selections survive group changes.
    runGroupSel = runGrp.value;
    renderRun();
    if (runGroupSel) toast(t("グループ「{g}」を選択しました", { g: runGroupSel }), "ok");
  };
  // 実行対象 toggles (Shift+クリック=範囲一括、ヘッダ=全選択/全解除)。
  // Optimistic: the UI flips instantly, the vault write happens behind it.
  const applyEnable = async (names, on) => {
    const set = new Set(names);
    (INV.devices || []).forEach(d => { if (set.has(d.name)) d.enabled = on; });
    renderRun();
    try { await App().SetAllDevicesEnabled(on, names); }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); await refreshInventory(); }
  };
  const runChkAll = document.getElementById("run-chk-all");
  if (runChkAll) runChkAll.onclick = () => applyEnable(visible.map(d => d.name), runChkAll.checked);
  wireShiftRange("run:" + runGroupSel, [...root.querySelectorAll("[data-enable]")], applyEnable);
  root.querySelectorAll("[data-log]").forEach(el => el.onclick = () => showLog(el.dataset.log));
  root.querySelectorAll("[data-term]").forEach(b => b.onclick = async () => {
    try { await App().SpawnTerminal(b.dataset.term); toast(t("対話接続ウィンドウを開きました"), "ok"); }
    catch (e) { toast(t("接続失敗") + ": " + terr(e), "err"); }
  });
  updateCounts();
}

// Backend messages that flow through untranslated get their known phrases
// mapped here (terr also converts embedded security-error fragments).
function fmtRunMsg(m) {
  return terr(String(m || "")).replace(/context canceled/g, t("中止しました"));
}

function statusLabel(p) {
  return ({ queued: t("待機"), off: t("対象外"), connecting: t("接続中"), login: t("ログイン中"),
    running: t("実行中"), saving: t("保存中"), done: t("完了"), error: t("エラー"), canceled: t("中止") }[p]) || p;
}

async function startRun(dryRun) {
  // Only the checked devices of the selected group run (RunSelected), so
  // checkbox states in other groups are never touched.
  const targets = runTargetDevices().map(d => d.name);
  if (!targets.length) return;
  const scope = (runGroupSel ? t("グループ「<b>{g}</b>」の ", { g: esc(runGroupSel) }) : "") + (runFilterActive() ? t("（絞り込み表示中の）") : "");
  if (dryRun) {
    const ok = await uiConfirm({
      title: t("接続確認のみ"),
      message: t("{s}<b>{n} 台</b>に接続してログインまで確認します。<br>コマンドは送らず、ログも残しません。よろしいですか？", { s: scope, n: targets.length }),
      okLabel: t("⚡ 接続確認"),
    });
    if (!ok) return;
    doRun(targets, true);
    return;
  }
  // A command set that changes the device (conf t / write / reload …) gets
  // its own warning with an explicit acknowledgement before anything runs.
  const warnings = await App().CheckDangerousCommands(targets).catch(() => []);
  if (warnings && warnings.length) {
    if (!(await dangerConfirm(warnings, targets.length, scope))) return;
  } else {
    const stage = runStage ? `<br><span class="muted" style="font-size:12px">${esc(t("作業タイミング: {s}", { s: stageLabel(runStage) }))}</span>` : "";
    const ok = await uiConfirm({
      title: t("一括実行"),
      message: t("{s}<b>{n} 台</b>に一括実行します。よろしいですか？", { s: scope, n: targets.length }) + stage,
      okLabel: t("▶ 実行"),
    });
    if (!ok) return;
  }
  doRun(targets, false);
}

// The warning shown before a batch whose command sets contain
// configuration-changing commands: every flagged command per device, and an
// acknowledgement checkbox that gates the run button.
function dangerConfirm(warnings, n, scope) {
  return new Promise(resolve => {
    const bySet = {};
    warnings.forEach(w => { (bySet[w.commandSet] = bySet[w.commandSet] || { hits: w.hits, devices: [] }).devices.push(w.device); });
    const blocks = Object.keys(bySet).map(set => `
      <div class="warn-box">
        <div><b>${esc(t("コマンドセット「{s}」", { s: set }))}</b> <span class="muted">${esc(t("— 対象 {n} 台: {d}", { n: bySet[set].devices.length, d: bySet[set].devices.join(", ") }))}</span></div>
        <table style="margin-top:6px"><tbody>
          ${bySet[set].hits.map(h => `<tr><td class="muted" style="padding:3px 8px 3px 0;width:36px">#${h.index + 1}</td><td class="mono" style="padding:3px 8px 3px 0">${esc(h.text)}</td><td class="muted" style="padding:3px 0;color:var(--warn)">${esc(t(h.reason))}</td></tr>`).join("")}
        </tbody></table>
      </div>`).join("");
    const node = h(`<div>
      <h3 style="color:var(--warn)">${esc(t("⚠ 設定を変更するコマンドが含まれています"))}</h3>
      <p style="font-size:14px;line-height:1.8;margin:4px 0 10px">${t("{s}<b>{n} 台</b>に一括実行します。次のコマンドは機器の設定や状態を<b>変更</b>します。実行前に内容を確認してください。", { s: scope, n })}</p>
      ${blocks}
      <label style="display:flex;align-items:center;gap:8px;cursor:pointer;margin-top:10px"><input type="checkbox" id="dg-ack" style="width:auto"> ${esc(t("変更を伴うことを理解したうえで実行します"))}</label>
      <div class="modal-actions">
        <button class="btn" id="dg-cancel">${esc(t("キャンセル"))}</button>
        <button class="btn danger" id="dg-ok" disabled>${esc(t("▶ 変更を実行"))}</button>
      </div>
    </div>`);
    openModal(node, "mid");
    const ack = node.querySelector("#dg-ack"), ok = node.querySelector("#dg-ok");
    ack.onchange = () => { ok.disabled = !ack.checked; };
    node.querySelector("#dg-cancel").onclick = () => { closeModal(); resolve(false); };
    ok.onclick = () => { if (!ack.checked) return; closeModal(); resolve(true); };
  });
}

// The actual batch kick-off (shared by 一括実行 / 接続確認のみ / 失敗のみ再実行).
// Only the named devices run (checkbox state is left untouched). partial
// keeps the rows of devices that already finished (失敗・中断のみ再実行).
let lastRunDry = false;
async function doRun(names, dryRun, partial) {
  // A full run starts from a clean table; a partial re-run (失敗・中断のみ)
  // resets only its own rows, so the devices that already finished keep
  // showing 完了 and their ログ表示 link.
  if (!partial) for (const k in runState) delete runState[k];
  const targets = names;
  lastRunDry = !!dryRun;
  targets.forEach(n => runState[n] = { phase: "queued" });
  cancelling = false;
  // For the overall ETA: how many commands each target will run.
  runTargets = targets.slice();
  runTotals = {};
  targets.forEach(n => {
    const d = (INV.devices || []).find(x => x.name === n);
    const cs = d && (INV.commandSets || []).find(s => s.name === d.commandSet);
    runTotals[n] = cs ? (cs.commands || []).length : 0;
  });
  runStartMs = Date.now();
  clearInterval(etaTimer);
  etaTimer = setInterval(updateEta, 1000);
  running = true;
  renderRun();
  try { await App().RunWith({ names: targets, group: runGroupSel, stage: runStage, dryRun: !!dryRun }); }
  catch (e) { toast(t("実行開始に失敗") + ": " + terr(e), "err"); running = false; renderRun(); }
}

// ---- overall ETA（予想残り時間） ----
let runTargets = [], runTotals = {}, runStartMs = 0, etaTimer = 0;

function fmtDur(sec) {
  sec = Math.max(0, Math.round(sec));
  if (sec < 60) return t("約{n}秒", { n: sec });
  const m = Math.floor(sec / 60), s = sec % 60;
  return s ? t("約{m}分{s}秒", { m, s }) : t("約{m}分", { m });
}

// Elapsed time so far, scaled by commands finished vs. commands left. Devices
// that ended (done/error) count as fully finished so a failed device doesn't
// inflate the estimate.
// Elapsed-per-command scaling went wrong whenever the run was not "every
// device busy with commands": connect/login time counted as command time,
// queued devices behind the parallel limit were ignored, and the configured
// pauses were averaged into the per-command cost. This instead estimates each
// device's remaining time (connect + login as learned from the devices that
// got through it, the exact remaining pauses, and the command time learned
// from finished commands), then lays the queued devices over the parallel
// slots in order, the way the backend does.
function computeEta() {
  if (!running) return "";
  const now = Date.now();
  const sum = arr => arr.reduce((a, b) => a + b, 0);
  let execSum = 0, execN = 0, connSum = 0, connN = 0, cmdsAhead = 0;
  for (const n of runTargets) {
    const st = runState[n] || {};
    const pauses = devPauses(n);
    if (st.connectAt && st.startedAt) { connSum += (st.startedAt - st.connectAt) / 1000; connN++; }
    if (st.phase === "running" && st.startedAt && (st.done || 0) > 0) {
      const done = Math.min(st.done, st.total || st.done);
      execSum += Math.max(0, (now - st.startedAt) / 1000 - sum(pauses.slice(0, done)));
      execN += done;
    } else if (["done", "error", "canceled"].includes(st.phase) && st.startedAt && st.finishedAt && (st.done || 0) > 0) {
      execSum += Math.max(0, (st.finishedAt - st.startedAt) / 1000 - sum(pauses.slice(0, st.done)));
      execN += st.done;
    }
    if (!["done", "error", "canceled"].includes(st.phase)) cmdsAhead += (runTotals[n] || 0);
  }
  if (cmdsAhead > 0 && execN === 0) return t("計測中…");
  const avgExec = execN ? execSum / execN : 0;
  const avgConn = connN ? connSum / connN : 5;
  const limit = (INV.settings || {}).maxParallel > 0 ? INV.settings.maxParallel : Infinity;
  const slots = [], queued = [];
  for (const n of runTargets) {
    const st = runState[n] || {};
    const pauses = devPauses(n);
    const full = avgConn + avgExec * (runTotals[n] || 0) + sum(pauses);
    switch (st.phase) {
      case "running": { const r = devEtaSec(n, st); slots.push(r == null ? avgExec * (st.total || 0) + sum(pauses) : r); break; }
      case "connecting": case "login":
        slots.push(Math.max(0, avgConn - (now - (st.connectAt || now)) / 1000) + avgExec * (runTotals[n] || 0) + sum(pauses)); break;
      case "saving": slots.push(1); break;
      case "queued": queued.push(full); break;
      default: break; // finished
    }
  }
  if (!slots.length && !queued.length) return fmtDur(0);
  if (limit === Infinity) return fmtDur(Math.max(0, ...slots, ...queued));
  while (slots.length < limit) slots.push(0);
  for (const d of queued) {
    let i = 0;
    for (let j = 1; j < slots.length; j++) if (slots[j] < slots[i]) i = j;
    slots[i] += d;
  }
  return fmtDur(Math.max(...slots));
}

function updateEta() {
  if (!running) { clearInterval(etaTimer); etaTimer = 0; }
  const el = document.getElementById("rc-eta");
  if (el) el.textContent = running ? computeEta() : "—";
  document.querySelectorAll(".pdev-eta").forEach(sp => {
    const eta = running ? devEta(sp.dataset.dev, runState[sp.dataset.dev] || {}) : "";
    sp.textContent = eta ? " " + eta : "";
  });
}

// ---- per-device 残り時間 ----
// Configured pauses of a device's command set, one per command (serial
// devices use the serial column).
function devPauses(name) {
  const d = (INV.devices || []).find(x => x.name === name);
  const cs = d && (INV.commandSets || []).find(s => s.name === d.commandSet);
  if (!cs) return [];
  return (cs.commands || []).map(c => d.conn === "serial" ? (c.serialSec || 0) : (c.pauseSec || 0));
}

// Time until every remaining command of this device has finished. The
// configured pauses are known exactly; the time a command itself takes is
// learned from the ones already done on this device (elapsed since the
// command phase started, minus the pauses spent). Until the first command
// finishes there is nothing to learn from, so it says 計測中…. The part of
// the current command already spent is subtracted, so the figure counts
// down steadily instead of jumping at each command boundary.
function devEtaSec(name, st) {
  if (st.phase !== "running" || !st.total || !st.startedAt) return null;
  const pauses = devPauses(name);
  const done = Math.min(st.done || 0, st.total);
  if (done <= 0) return null;
  const sum = arr => arr.reduce((a, b) => a + b, 0);
  const elapsed = (Date.now() - st.startedAt) / 1000;
  const donePause = sum(pauses.slice(0, done));
  const avgExec = Math.max(0, (elapsed - donePause) / done);
  const planned = sum(pauses.slice(done)) + avgExec * (st.total - done);
  const spentInCurrent = Math.max(0, elapsed - (donePause + avgExec * done));
  return Math.max(0, planned - spentInCurrent);
}
function devEta(name, st) {
  if (st.phase !== "running" || !st.total || !st.startedAt) return "";
  const sec = devEtaSec(name, st);
  return t("残り {t}", { t: sec == null ? t("計測中…") : fmtDur(sec) });
}

// Re-run the devices the last batch left unfinished: failures, plus the
// ones 中止 interrupted or never started.
async function retryUnfinished() {
  const names = unfinishedNames();
  if (!names.length) return;
  const ok = await uiConfirm({
    title: t("失敗・中断のみ再実行"),
    message: t("失敗・中断した <b>{n} 台</b>のみ再実行します（完了済みはそのまま）。よろしいですか？", { n: names.length }),
    okLabel: t("↻ 再実行"),
  });
  if (!ok) return;
  doRun(names, lastRunDry, true);
}

function updateRunRow(name) {
  const body = document.getElementById("run-body");
  if (!body) return;
  const d = (INV.devices || []).find(x => x.name === name);
  if (!d) return;
  const st = runState[name] || {};
  // Re-render just the run tab to reflect the change (simple + robust).
  const active = document.querySelector(".tab.active");
  if (active && active.id === "tab-run") renderRun();
}

function updateCounts() {
  const vals = Object.values(runState);
  const ok = vals.filter(s => s.phase === "done").length;
  const err = vals.filter(s => s.phase === "error").length;
  const run = vals.filter(s => ["connecting", "login", "running", "saving"].includes(s.phase)).length;
  const set = (id, v) => { const el = document.getElementById(id); if (el) el.textContent = v; };
  set("rc-ok", ok); set("rc-err", err); set("rc-run", run);
  updateEta();
}

// ログ表示 opens the file in its own window (PalaTerm.exe --view), so it can
// sit beside the run table or another log instead of covering the app.
async function showLog(path) {
  try { await App().OpenLogWindow(path); }
  catch (e) { toast(t("ログを開けません") + ": " + terr(e), "err"); }
}

// live events from the backend
function wireEvents() {
  rt().EventsOn("run:event", ev => {
    const prev = runState[ev.device] || {};
    runState[ev.device] = { phase: ev.phase, message: ev.message,
      done: ev.phase === "running" ? (ev.done || 0) : prev.done,
      total: ev.phase === "running" ? (ev.total || 0) : prev.total,
      startedAt: ev.phase === "running" ? (prev.phase === "running" && prev.startedAt ? prev.startedAt : Date.now()) : prev.startedAt,
      // When the dial began and when the device finished: the overall ETA
      // learns connect+login time and per-command time from these.
      connectAt: ev.phase === "connecting" ? (prev.connectAt || Date.now()) : prev.connectAt,
      finishedAt: ["done", "error", "canceled"].includes(ev.phase) ? (prev.finishedAt || Date.now()) : prev.finishedAt,
      // Seconds left of the command's configured 待機. Absent on every other
      // event, which is what clears the countdown once the pause is over.
      waitSec: ev.waitSec || 0,
      logPath: ev.phase === "done" ? ev.message : prev.logPath };
    const active = document.querySelector(".tab.active");
    if (active && active.id === "tab-run") renderRun();
  });
  rt().EventsOn("run:done", payload => {
    running = false;
    clearInterval(etaTimer); etaTimer = 0;
    // Payload: {results, runDir, dryRun} (a bare array from older builds).
    const results = Array.isArray(payload) ? payload : (payload && payload.results) || [];
    const runDir = (payload && payload.runDir) || "";
    const wasDry = !!(payload && payload.dryRun);
    (results || []).forEach(r => {
      const prev = runState[r.device] || {};
      runState[r.device] = {
        phase: r.success ? "done" : (r.canceled ? "canceled" : "error"),
        message: r.success ? (r.logPath || (wasDry ? t("接続・ログイン OK（コマンドは送っていません）") : "")) : r.error,
        logPath: r.logPath,
        done: r.success ? prev.total : prev.done,
        total: prev.total,
        startedAt: prev.startedAt, connectAt: prev.connectAt, finishedAt: prev.finishedAt || Date.now(),
      };
    });
    const wasCancel = cancelling;
    cancelling = false;
    const active = document.querySelector(".tab.active");
    if (active && active.id === "tab-run") renderRun();
    // Clear the "実行中" notice on the settings tab (unless mid-edit).
    else if (active && active.id === "tab-settings" && !settingsDirty) renderSettings();
    if (wasCancel) {
      const v = Object.values(runState);
      toast(t("中止しました（完了 {a}・失敗 {b}・中断/未実行 {c}）", {
        a: v.filter(s => s.phase === "done").length, b: v.filter(s => s.phase === "error").length, c: v.filter(s => s.phase === "canceled").length }), "ok");
    } else if (wasDry) {
      const v = Object.values(runState);
      toast(t("接続確認が完了しました（OK {a}・失敗 {b}）", { a: v.filter(s => s.phase === "done").length, b: v.filter(s => s.phase === "error").length }), "ok");
    } else if (runStage === "after" && runDir) {
      toast(t("実行が完了しました。実行履歴タブで作業前のログと比較できます"), "ok");
    } else toast(t("実行が完了しました"), "ok");
  });
}

// ---- Log Settings tab ----

// True while the settings form has edits that were not saved yet.
let settingsDirty = false;

// Gate for leaving the settings tab (called by switchTab / lock in app.js):
// warns before unsaved edits are silently lost.
async function canLeaveSettings(nextTab) {
  const active = document.querySelector(".nav-btn.active");
  if (!active || active.dataset.tab !== "settings" || nextTab === "settings" || !settingsDirty) return true;
  const ok = await uiConfirm({
    title: t("未保存の変更"),
    message: t("設定に保存されていない変更があります。<br>保存せずに移動しますか？"),
    okLabel: t("破棄して移動"),
    danger: true,
  });
  if (ok) settingsDirty = false;
  return ok;
}

function renderSettings() {
  const root = document.getElementById("tab-settings");
  const s = INV.settings || {};
  root.innerHTML = `
    <div class="page-head"><div><div class="page-title">${esc(t("ログ設定"))}</div>
      <div class="page-sub">${esc(t("実行とログ保存の共通設定"))}</div></div></div>
    <div class="panel" style="padding:22px;max-width:620px">
      ${running ? `<div style="font-size:13px;color:var(--accent);border:1px solid var(--accent);border-radius:8px;padding:10px 14px;margin-bottom:16px">${esc(t("▶ 一括実行中です。実行中のバッチは開始時点の設定で動くため、ここでの変更は次回の実行から反映されます"))}</div>` : ""}
      <div class="grid-2">
        <div class="field"><label>${esc(t("最大並列数（0=無制限）"))}</label><input id="set-par" type="number" value="${s.maxParallel ?? 10}"></div>
        <div class="field"><label>${esc(t("接続タイムアウト（秒）"))}</label><input id="set-ct" type="number" value="${s.connectTimeout ?? 20}"></div>
      </div>
      <div class="grid-2">
        <div class="field"><label>${esc(t("コマンドタイムアウト（秒）"))}</label><input id="set-cmt" type="number" value="${s.commandTimeout ?? 30}"></div>
        <div class="field"><label>${esc(t("ログ保存フォルダ"))}</label>
          <div class="row-inline" style="gap:6px">
            <input id="set-dir" style="flex:1" value="${esc(s.logDir || "logs")}">
            <button class="btn sm" id="set-dir-pick" type="button">${esc(t("参照…"))}</button>
          </div></div>
      </div>
      <div class="grid-2">
        <div class="field"><label><span data-tip="${esc(t("実行ごとに作るフォルダの名前です。使える変数は {date} {time} {hhmm} {group} {stage}（例: {date}_{group}_{stage}）。同名のフォルダが既にあれば _2, _3 … が付きます"))}">${esc(t("ログフォルダ名テンプレート ⓘ"))}</span></label>
          <input id="set-dtmpl" value="${esc(s.logDirTemplate || "log_{date}_{time}_{stage}")}"></div>
        <div class="field"><label>${esc(t("ログファイル名テンプレート"))}</label>
          <input id="set-tmpl" value="${esc(s.logNameTemplate || "{host}_{site}_{stage}_{date}_{time}.txt")}"></div>
      </div>
      <div class="field"><label><span data-tip="${esc(t("作業タイミングごとに {stage} へ入る文字列です。フォルダ名・ファイル名に付きます。空欄なら既定（before / work / after）。改名しても、以前の既定語で作られたフォルダは実行履歴でそのまま判別されます"))}">${esc(t("作業タイミングの付与文字列 ⓘ"))}</span></label>
        <div style="display:grid;grid-template-columns:1fr 1fr 1fr;gap:10px">
          <div class="row-inline" style="gap:6px"><span class="muted" style="font-size:12px;white-space:nowrap">${esc(t("作業前"))}</span><input id="set-st-before" style="flex:1" placeholder="before" value="${esc(s.stageTokenBefore || "before")}"></div>
          <div class="row-inline" style="gap:6px"><span class="muted" style="font-size:12px;white-space:nowrap">${esc(t("作業中"))}</span><input id="set-st-work" style="flex:1" placeholder="work" value="${esc(s.stageTokenWork || "work")}"></div>
          <div class="row-inline" style="gap:6px"><span class="muted" style="font-size:12px;white-space:nowrap">${esc(t("作業後"))}</span><input id="set-st-after" style="flex:1" placeholder="after" value="${esc(s.stageTokenAfter || "after")}"></div>
        </div></div>
      <div class="grid-2">
        <div class="field"><label><span data-tip="${esc(t("マウス・キーボード操作がこの時間なければ自動でロックします（一括実行中はロックしません）。0 で無効"))}">${esc(t("アイドル時の自動ロック（分・0=無効） ⓘ"))}</span></label>
          <input id="set-lock" type="number" min="0" value="${s.autoLockOff ? 0 : (s.autoLockMin > 0 ? s.autoLockMin : 30)}"></div>
      </div>
      <div class="muted" style="font-size:12px;margin-top:2px">${esc(t("テンプレートに使える変数（クリックでコピー。フォルダ名は {date} {time} {hhmm} {group} {stage} のみ）:"))}</div>
      <table class="ph-table"><tbody>
        <tr><td class="mono">{host}</td><td>${esc(t("ホスト名"))}</td><td class="mono">{date}</td><td>${esc(t("日付（yyyymmdd）"))}</td></tr>
        <tr><td class="mono">{ip}</td><td>${esc(t("IPアドレス"))}</td><td class="mono">{time}</td><td>${esc(t("時刻（hhmmss）"))}</td></tr>
        <tr><td class="mono">{os}</td><td>${esc(t("OS種別"))}</td><td class="mono">{hhmm}</td><td>${esc(t("時刻（hhmm）"))}</td></tr>
        <tr><td class="mono">{group}</td><td>${esc(t("グループ名"))}</td><td class="mono">{site}</td><td>${esc(t("拠点名"))}</td></tr>
        <tr><td class="mono">{stage}</td><td colspan="3">${esc(t("作業タイミングの付与文字列（既定: 作業前=before・作業中と対話接続=work・作業後=after。上の欄で変更可）。指定なしは Config。テンプレートに無いときはファイル名の末尾に自動で付きます"))}</td></tr>
      </tbody></table>
      <div class="muted" style="font-size:12px;margin-top:6px">${esc(t("※ログ保存フォルダに相対パス（例: logs）を指定した場合、PalaTerm.exe と同じフォルダが基準になります"))}</div>
      <div class="modal-actions"><button class="btn primary" id="set-save">${esc(t("保存"))}</button></div>
    </div>`;

  // Track unsaved edits so tab switches can warn (see canLeaveSettings).
  settingsDirty = false;
  ["set-par", "set-ct", "set-cmt", "set-dir", "set-tmpl", "set-dtmpl", "set-lock", "set-st-before", "set-st-work", "set-st-after"].forEach(id => {
    const el = document.getElementById(id);
    if (el) el.addEventListener("input", () => { settingsDirty = true; });
  });
  document.getElementById("set-dir-pick").onclick = async () => {
    try {
      const p = await App().PickLogDir();
      if (p) { document.getElementById("set-dir").value = p; settingsDirty = true; }
    } catch (e) { toast(t("選択失敗") + ": " + terr(e), "err"); }
  };
  // Click a placeholder cell to copy it to the clipboard.
  wireCopyCells(root);
  document.getElementById("set-save").onclick = async () => {
    const out = {
      maxParallel: parseInt(document.getElementById("set-par").value, 10) || 0,
      connectTimeout: parseInt(document.getElementById("set-ct").value, 10) || 20,
      commandTimeout: parseInt(document.getElementById("set-cmt").value, 10) || 30,
      logDir: document.getElementById("set-dir").value || "logs",
      logNameTemplate: document.getElementById("set-tmpl").value || "{host}_{site}_{stage}_{date}_{time}.txt",
      logDirTemplate: document.getElementById("set-dtmpl").value.trim() || "log_{date}_{time}_{stage}",
      // Empty = default (before / work / after); the backend validates.
      stageTokenBefore: document.getElementById("set-st-before").value.trim(),
      stageTokenWork: document.getElementById("set-st-work").value.trim(),
      stageTokenAfter: document.getElementById("set-st-after").value.trim(),
    };
    const lockMin = parseInt(document.getElementById("set-lock").value, 10);
    out.autoLockOff = !(lockMin > 0);
    out.autoLockMin = lockMin > 0 ? lockMin : 30;
    try { await App().SaveSettings(out); settingsDirty = false; await refreshInventory(); toast(t("保存しました"), "ok"); }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  };
}

// ---- OSタイプ設定 tab ----

// All profiles live in the vault and are equally editable — the seeded 14
// defaults included. Deleting never touches the JSON files under
// export\os-profiles\, so ファイル読込 can always restore a profile.
function renderOSTypes() {
  const root = document.getElementById("tab-ostypes");
  const profiles = INV.customProfiles || [];
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("OSタイプ設定"))}</div>
        <div class="page-sub">${esc(t("機器種別ごとのログイン自動化（expect/send）。待つ文字は「#」「assword:」のような文字そのまま（部分一致）。標準プロファイルのJSONは export\\os-profiles\\ に自動で置かれ、アプリが消すことはないので、削除しても読込で戻せます。書き出しは「機器」タブの一式書出にまとまっています"))}</div></div>
      <div class="row-inline" style="gap:6px;white-space:nowrap">
        <button class="btn" id="prof-imp" data-tip="${esc(t("OSタイププロファイルの JSON を 1 ファイル読み込みます（削除した標準プロファイルの復元にも）"))}">${esc(t("読込"))}</button>
        <button class="btn primary" id="prof-add">${esc(t("＋ 追加"))}</button>
      </div>
    </div>
    <div class="panel">
      ${profiles.length === 0
        ? `<div class="muted" style="font-size:13px;padding:14px">${esc(t("プロファイルがありません。「ファイル読込」で export\\os-profiles\\ のJSONから復元するか、「＋ 追加」で作成してください。"))}</div>`
        : `<table><thead><tr><th style="width:30px"></th><th>${esc(t("名前"))}</th><th>${esc(t("完了の目印"))}</th><th style="width:110px">${esc(t("ログイン手順"))}</th><th style="width:220px"></th></tr></thead><tbody id="prof-body">
        ${profiles.map(p => `<tr data-key="${esc(p.key)}">
          <td class="drag-handle" data-tip="${dragHandleTip()}">⠿</td>
          <td><b>${esc(p.name)}</b></td>
          <td class="mono muted"><div class="clip" data-tip="${esc(p.prompt)}">${esc(p.prompt)}</div></td>
          <td class="muted">${esc(t("{n} 行", { n: (p.login || []).length }))}</td>
          <td style="text-align:right;white-space:nowrap">
            <button class="btn sm act-edit" data-pedit="${esc(p.key)}">${esc(t("編集"))}</button>
            <button class="btn sm act-copy" data-pcopy="${esc(p.key)}">${esc(t("複製"))}</button>
            <button class="btn sm act-del" data-pdel="${esc(p.key)}">${esc(t("削除"))}</button>
          </td></tr>`).join("")}</tbody></table>`}
    </div>`;
  wireRowReorder(root.querySelector("#prof-body"), async order => {
    try {
      await App().ReorderProfiles(order);
      INV = await App().GetInventory();
      PROFILES = await App().ListProfiles();
      renderOSTypes();
    } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  });
  root.querySelectorAll("[data-pedit]").forEach(b => b.onclick = () =>
    editProfile((INV.customProfiles || []).find(p => p.key === b.dataset.pedit)));
  root.querySelectorAll("[data-pcopy]").forEach(b => b.onclick = async () => {
    try {
      const nm = await App().CopyProfile(b.dataset.pcopy);
      INV = await App().GetInventory();
      PROFILES = await App().ListProfiles();
      renderOSTypes();
      toast(t("複製しました: {n}", { n: nm }), "ok");
    } catch (e) { toast(t("複製失敗") + ": " + terr(e), "err"); }
  });
  root.querySelectorAll("[data-pdel]").forEach(b => b.onclick = async () => {
    const p = (INV.customProfiles || []).find(x => x.key === b.dataset.pdel);
    if (!(await uiConfirm({ title: t("プロファイルを削除"), message: t("OSタイププロファイル「<b>{n}</b>」を削除しますか？<br><span class=\"muted\" style=\"font-size:12px\">標準プロファイルは export\\os-profiles\\ のJSONから、自作のものは一式書出したフォルダから「ファイル読込」で戻せます</span>", { n: esc(p ? p.name : "") }), okLabel: t("削除"), danger: true }))) return;
    try {
      await App().DeleteProfile(b.dataset.pdel);
      INV = await App().GetInventory();
      PROFILES = await App().ListProfiles();
      renderOSTypes();
      toast(t("削除しました"), "ok");
    } catch (e) { toast(terr(e), "err"); }
  });
  document.getElementById("prof-add").onclick = () => editProfile(null);
  document.getElementById("prof-imp").onclick = async () => {
    try {
      const nm = await App().ImportOSProfileFile();
      if (nm) {
        INV = await App().GetInventory();
        PROFILES = await App().ListProfiles();
        renderOSTypes();
        toast(t("「{n}」を読み込みました", { n: nm }), "ok");
      }
    } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
  };
}

// Profile editor modal (defaults and user-made profiles are edited the same
// way).
function editProfile(prof) {
    // 新規作成時は Cisco IOS 系の実例をすべて埋めた状態で開く（書き換えて使う）。
    // プロンプト・expectは「#」「Username:」等の文字そのまま（部分一致）。
    const p = prof || {
      name: "",
      prompt: "#",
      login: [
        { expect: "Username:", send: "{user}" },
        { expect: "Password:", send: "{password}" },
        { expect: ">", send: "enable" },
        { expect: "Password:", send: "{enable}" },
      ],
      pager: [{ send: "terminal length 0" }],
      disconnect: [{ send: "exit" }],
    };
    // Spread keeps fields the editor doesn't show (enter/sendRaw/delayMs on
    // imported built-ins) attached to their row so a save doesn't drop them.
    let steps = (p.login || []).map(s => ({ ...s, expect: s.expect || "", send: s.send || "" }));
    if (steps.length === 0) steps = [{ expect: "", send: "" }];
    const pagerCmd = (p.pager || []).map(s => s.send || "").filter(Boolean).join("\n");
    const discCmd = (p.disconnect || []).map(s => s.send || "").filter(Boolean).join("\n") || "exit";
    const node = h(`<div>
      <h3>${esc(prof ? t("OSタイププロファイルを編集") : t("OSタイププロファイルを追加"))}</h3>
      ${prof ? "" : `<p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("Cisco IOS系の実例を入れてあります。機器に合わせて書き換えてください。"))}</p>`}
      <div class="grid-2">
        <div class="field"><label>${esc(t("プロファイル名"))} <span class="req">${esc(t("必須"))}</span></label><input id="p-name" value="${esc(p.name || "")}" ${prof ? "readonly" : ""} placeholder="${esc(t("例: MyRouter OS"))}"></div>
        <div class="field"><label><span data-tip="${esc(t("コマンドが打てる状態のときに機器が出す表示（例: Router#）。コマンド送信後、これが再び現れたら「出力が終わった」と判定して次のコマンドを送ります。「#」のように末尾の記号だけ書いておけば、設定モードのプロンプト（例: FG(console)#）にも一致します"))}">${esc(t("showコマンド完了の目印 ⓘ"))}</span> <span class="req">${esc(t("必須"))}</span></label><input id="p-prompt" class="mono" value="${esc(p.prompt || "")}"></div>
      </div>
      <div class="grid-2">
        <div class="field"><label><span data-tip="${esc(t("ログイン直後に流す、出力の一時停止（--More--）をなくすコマンド。OSによりコマンドやモードが違うため複数行OK（設定モードに入る手順ごと書けます）。FortiGateのように機器側で設定済みの場合は空欄でかまいません"))}">${esc(t("ページャ解除コマンド（任意・1行1コマンドで複数行可） ⓘ"))}</span></label>
          <textarea id="p-pager" class="mono" style="min-height:72px" placeholder="config system console&#10;set output standard&#10;end">${esc(pagerCmd)}</textarea></div>
        <div class="field"><label><span data-tip="${esc(t("出力の途中一時停止の表示。これが出たらスペースを送って続きを表示します。ページャ解除コマンドで止まらないOSだけ設定します"))}">${esc(t("ページャ表示の目印（任意） ⓘ"))}</span></label>
          <input id="p-more" class="mono" value="${esc(p.morePrompt || "")}" placeholder="--- more ---"></div>
      </div>
      <label style="font-size:12px;color:var(--text-dim)"><span data-tip="${esc(t("上から順に実行。expect列の表示が出るのを待ち、send列の文字列を入力。認証系の行（{user}/{password}/{enable}を送る行）は表示が出なければ自動でスキップされるため、SSH/Telnetで同じプロファイルが使えます。最後の行の入力が終わると自動で「showコマンド完了の目印」を待つため、目印を待つだけの行は不要です"))}">${esc(t("ログイン手順 ⓘ"))}</span></label>
      <div class="scroll"><table style="margin-top:6px">
        <thead><tr><th style="width:26px"></th><th style="width:36px">#</th><th><span data-tip="${esc(t("プロンプト・expectは待ちたい文字をそのまま書きます（例: Password:）。文字の一部が画面に現れた時点で一致します。大文字小文字は区別されます"))}">${esc(t("expect（待つ表示） ⓘ"))}</span></th><th><span data-tip="${esc(t("expectの表示が出たら入力する文字列。変数 {user} {password} {enable} は機器に登録した認証情報に置き換わります。認証系の行（{user}/{password}/{enable}を送る行）は、その表示が出ない機器・接続方式では自動でスキップされます（SSH直接続なら {user}/{password} の行は即スキップ）"))}">${esc(t("send（入力コマンド） ⓘ"))}</span></th><th style="width:76px"></th></tr></thead>
        <tbody id="p-rows"></tbody>
      </table></div>
      <button class="btn sm" id="p-addrow" type="button" style="margin-top:8px">${esc(t("＋ 行を追加"))}</button>
      <div class="muted" style="font-size:12px;margin-top:10px">${esc(t("send に使える変数（機器に登録した認証情報に置き換わります。クリックでコピー）:"))}</div>
      <table class="ph-table"><tbody>
        <tr><td class="mono">{user}</td><td>${esc(t("ログインユーザー名"))}</td></tr>
        <tr><td class="mono">{password}</td><td>${esc(t("ログインパスワード"))}</td></tr>
        <tr><td class="mono">{enable}</td><td>${esc(t("enable / 昇格パスワード"))}</td></tr>
      </tbody></table>
      <div class="field" style="max-width:300px;margin-top:12px"><label><span data-tip="${esc(t("実行終了時にログアウトのため送るコマンド。上から順に1行ずつ送信します（例: exitを2回でログアウトする機器は2行書く）"))}">${esc(t("切断コマンド（1行1コマンドで複数行可） ⓘ"))}</span></label>
        <textarea id="p-disc" class="mono" style="min-height:60px" placeholder="exit&#10;exit">${esc(discCmd)}</textarea></div>
      <div class="modal-actions">
        <button class="btn" id="p-cancel">${esc(t("キャンセル"))}</button>
        <button class="btn primary" id="p-save">${esc(t("保存"))}</button>
      </div>
    </div>`);
    openModal(node);
    wireCopyCells(node);
    const tbody = node.querySelector("#p-rows");
    function collect() {
      // Rows are read in DOM order (so a drag-reorder sticks); each row's
      // data-idx points back into `steps` so hidden fields the editor doesn't
      // show (enter/sendRaw/delayMs on imported profiles) survive edits.
      const arr = [];
      tbody.querySelectorAll("tr").forEach(r => arr.push({
        ...(steps[parseInt(r.dataset.idx, 10)] || {}),
        expect: r.querySelector(".p-exp").value,
        send: r.querySelector(".p-snd").value,
      }));
      return arr;
    }
    function render() {
      tbody.innerHTML = steps.map((s, i) => `
        <tr data-key="${i}" data-idx="${i}">
          <td class="drag-handle" data-tip="${dragHandleTip()}">⠿</td>
          <td class="muted">${i + 1}</td>
          <td><input class="p-exp mono" style="width:100%" value="${esc(s.expect)}"></td>
          <td><input class="p-snd mono" style="width:100%" value="${esc(s.send)}"></td>
          <td style="white-space:nowrap">
            <button class="btn sm" type="button" data-ins="${i}">＋</button>
            <button class="btn sm act-del" type="button" data-rm="${i}">×</button>
          </td>
        </tr>`).join("");
      tbody.querySelectorAll("[data-rm]").forEach(btn => btn.onclick = () => {
        steps = collect(); steps.splice(parseInt(btn.dataset.rm, 10), 1);
        if (steps.length === 0) steps = [{ expect: "", send: "" }];
        render();
      });
      tbody.querySelectorAll("[data-ins]").forEach(btn => btn.onclick = () => {
        const i = parseInt(btn.dataset.ins, 10);
        steps = collect(); steps.splice(i + 1, 0, { expect: "", send: "" });
        render();
      });
      // Drag-reorder rows; collect() reads the new DOM order.
      wireRowReorder(tbody, () => { steps = collect(); render(); });
    }
    render();
    node.querySelector("#p-addrow").onclick = () => { steps = collect(); steps.push({ expect: "", send: "" }); render(); };
    node.querySelector("#p-cancel").onclick = closeModal;
    node.querySelector("#p-save").onclick = async () => {
      const login = collect().filter(s => s.expect.trim() || s.send.trim() || s.enter || s.sendRaw)
        .map(s => ({ ...s, expect: s.expect.trim(), send: s.send.trim() }));
      const pagerLines = node.querySelector("#p-pager").value
        .split("\n").map(l => l.trim()).filter(Boolean);
      const discLines = node.querySelector("#p-disc").value
        .split("\n").map(l => l.trim()).filter(Boolean);
      const out = {
        key: p.key || "",
        name: node.querySelector("#p-name").value.trim(),
        prompt: node.querySelector("#p-prompt").value.trim(),
        morePrompt: node.querySelector("#p-more").value.trim(),
        login,
        pager: pagerLines.map(l => ({ send: l })),
        // The textarea only shows the send of each disconnect step; if left
        // untouched keep the original steps (their expects included) intact.
        disconnect: (discLines.join("\n") === discCmd && (p.disconnect || []).length)
          ? p.disconnect
          : discLines.map(l => ({ send: l })),
      };
      try {
        await App().SaveProfile(out);
        INV = await App().GetInventory();
        PROFILES = await App().ListProfiles();
        closeModal();
        renderOSTypes();
        toast(t("保存しました"), "ok");
      } catch (e) { toast(terr(e), "err"); }
    };
}

wireEvents();
