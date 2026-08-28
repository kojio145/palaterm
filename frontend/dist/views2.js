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
        <div class="page-sub">${esc(t("機器ごとに割り当てて一括実行するコマンド群。CSV書出は export\\command-sets\\ に1セット1ファイルで保存されます"))}</div></div>
      <div class="row-inline">
        <button class="btn" id="cs-imp">${esc(t("CSV読込"))}</button>
        <button class="btn" id="cs-exp">${esc(t("CSV書出"))}</button>
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
  document.getElementById("cs-exp").onclick = async () => {
    try {
      const dir = await App().ExportCommandSets();
      if (dir) toast(t("書き出しました: {p}", { p: dir }), "ok");
    } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
  };
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
    <div class="field"><label>${esc(t("セット名"))}</label><input id="s-name" value="${esc(s.name)}" ${set ? "readonly" : ""}></div>
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
// Group last chosen with the run tab's group selector (kept across re-renders
// so the selector and the heading show what is targeted).
let runGroupSel = "";

function renderRun() {
  const root = document.getElementById("tab-run");
  // Targets appear only after a group is chosen (no accidental all-device
  // runs). Every device of the group is listed so 実行対象 can be toggled
  // right here; unchecked ones show as 対象外.
  const devs = runGroupSel
    ? (INV.devices || []).filter(d => (d.group || "") === runGroupSel)
    : [];
  const rows = devs.map(d => {
    const st = runState[d.name] || { phase: d.enabled ? "queued" : "off" };
    const pbar = (st.total > 0)
      ? `<div class="pbar" data-tip="${esc(t("{a} / {b} コマンド完了", { a: st.done || 0, b: st.total }))}"><i class="pf-${st.phase}" style="width:${Math.min(100, Math.round((st.done || 0) / st.total * 100))}%"></i></div><div class="pnum">${st.done || 0}/${st.total}</div>`
      : "";
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
        <button class="btn sm act-conn" data-term="${esc(d.name)}">${esc(t("接続"))}</button>
      </td>
    </tr>`;
  }).join("");

  const enabledCount = devs.filter(d => d.enabled).length;
  const failedCount = Object.keys(runState).filter(n => (runState[n] || {}).phase === "error").length;
  const groups = INV.deviceGroups || [];
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("実行"))}${runGroupSel ? ` <span style="font-size:15px;font-weight:600;color:var(--accent)">${esc(t("グループ「{g}」", { g: runGroupSel }))}</span>` : ""}</div>
        <div class="page-sub">${esc(runGroupSel ? t("対象 {a} / {b} 台（チェック=実行対象。Shift+クリックで範囲をまとめて切替）", { a: enabledCount, b: devs.length }) : t("まず「グループで対象を選択…」から実行するグループを選んでください"))}</div></div>
    </div>
    <div class="run-toolbar">
      <button class="btn run" id="btn-run" ${running || enabledCount === 0 ? "disabled" : ""}>${esc(t("▶ 一括実行"))}</button>
      <button class="btn danger" id="btn-cancel" ${running ? "" : "disabled"}>${esc(t("■ 中止"))}</button>
      ${!running && failedCount ? `<button class="btn" id="btn-retry" style="color:var(--err);border-color:var(--err)">${esc(t("↻ 失敗のみ再実行（{n}台）", { n: failedCount }))}</button>` : ""}
      ${groups.length ? `<select id="run-grp" class="btn" style="padding-right:8px" ${running ? "disabled" : ""}>
        <option value="">${esc(t("グループで対象を選択…"))}</option>
        ${groups.map(g => `<option value="${esc(g.name)}" ${g.name === runGroupSel ? "selected" : ""}>${esc(t("{g}（{n}台）", { g: g.name, n: groupCount(g.name) }))}</option>`).join("")}
      </select>` : ""}
      <button class="btn" id="btn-logdir">${esc(t("ログフォルダを開く"))}</button>
      <div class="run-stat">
        <div>${esc(t("成功"))} <b id="rc-ok" style="color:var(--ok)">0</b></div>
        <div>${esc(t("失敗"))} <b id="rc-err" style="color:var(--err)">0</b></div>
        <div>${esc(t("実行中"))} <b id="rc-run">0</b></div>
        <div><span data-tip="${esc(t("完了済みコマンド数と経過時間から算出した全体の予想残り時間。実行が進むほど精度が上がります"))}">${esc(t("予想残り"))}</span> <b id="rc-eta">${running ? computeEta() : "—"}</b></div>
      </div>
    </div>
    <div class="panel">
      ${devs.length === 0 ? `<div class="empty">${esc(runGroupSel ? t("このグループに実行対象の機器がありません。機器一覧でチェックしてください。") : t("「グループで対象を選択…」から実行するグループを選んでください。"))}</div>`
        : `<table><thead><tr>
           <th style="width:36px"><input type="checkbox" id="run-chk-all" ${devs.length && devs.every(d => d.enabled) ? "checked" : ""} ${running ? "disabled" : ""} title="${esc(t("全選択/全解除"))}"></th>
           <th style="width:120px">${esc(t("状態"))}</th><th>${esc(t("ホスト名"))}</th><th style="width:140px">${esc(t("IPアドレス"))}</th>
           <th style="width:120px">${esc(t("拠点名"))}</th><th style="width:190px">${esc(t("コマンドセット"))}</th>
           <th>${esc(t("メッセージ"))}</th><th></th></tr></thead><tbody id="run-body">${rows}</tbody></table>`}
    </div>`;

  document.getElementById("btn-run").onclick = startRun;
  document.getElementById("btn-cancel").onclick = () => App().CancelRun();
  const btnRetry = document.getElementById("btn-retry");
  if (btnRetry) btnRetry.onclick = retryFailed;
  document.getElementById("btn-logdir").onclick = () => App().OpenLogDir();
  const runGrp = document.getElementById("run-grp");
  if (runGrp) runGrp.onchange = async () => {
    // Results (and the 失敗のみ再実行 button) belong to the previous group's
    // run — warn before dropping them along with the switch.
    const failed = Object.keys(runState).filter(n => (runState[n] || {}).phase === "error").length;
    if (failed) {
      const ok = await uiConfirm({
        title: t("実行結果のクリア"),
        message: t("グループを切り替えると、前回の実行結果と「↻ 失敗のみ再実行（{n}台）」ボタンは消えます。<br>切り替えますか？", { n: failed }),
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
  if (runChkAll) runChkAll.onclick = () => applyEnable(devs.map(d => d.name), runChkAll.checked);
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
    running: t("実行中"), saving: t("保存中"), done: t("完了"), error: t("エラー") }[p]) || p;
}

async function startRun() {
  // Only the checked devices of the selected group run (RunSelected), so
  // checkbox states in other groups are never touched.
  const targets = (INV.devices || [])
    .filter(d => d.enabled && (!runGroupSel || (d.group || "") === runGroupSel))
    .map(d => d.name);
  if (!targets.length) return;
  const scope = runGroupSel ? t("グループ「<b>{g}</b>」の ", { g: esc(runGroupSel) }) : "";
  const ok = await uiConfirm({
    title: t("一括実行"),
    message: t("{s}<b>{n} 台</b>に一括実行します。よろしいですか？", { s: scope, n: targets.length }),
    okLabel: t("▶ 実行"),
  });
  if (!ok) return;
  doRun(targets);
}

// The actual batch kick-off (shared by 一括実行 and 失敗のみ再実行).
// With names, only those devices run (checkbox state is left untouched).
async function doRun(names) {
  for (const k in runState) delete runState[k];
  const targets = names || (INV.devices || []).filter(d => d.enabled).map(d => d.name);
  targets.forEach(n => runState[n] = { phase: "queued" });
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
  try { names ? await App().RunSelected(names) : await App().RunBatch(); }
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
function computeEta() {
  let doneCmds = 0, totalCmds = 0;
  for (const n of runTargets) {
    const tt = runTotals[n] || 0;
    totalCmds += tt;
    const st = runState[n] || {};
    if (st.phase === "done" || st.phase === "error") doneCmds += tt;
    else doneCmds += Math.min(st.done || 0, tt);
  }
  if (!running) return "";
  if (doneCmds <= 0 || totalCmds <= 0) return t("計測中…");
  const elapsed = (Date.now() - runStartMs) / 1000;
  return fmtDur(elapsed / doneCmds * (totalCmds - doneCmds));
}

function updateEta() {
  if (!running) { clearInterval(etaTimer); etaTimer = 0; }
  const el = document.getElementById("rc-eta");
  if (el) el.textContent = running ? computeEta() : "—";
}

// Re-run only the devices that errored in the last batch.
async function retryFailed() {
  const failed = Object.keys(runState).filter(n => (runState[n] || {}).phase === "error");
  if (!failed.length) return;
  const ok = await uiConfirm({
    title: t("失敗のみ再実行"),
    message: t("失敗した <b>{n} 台</b>のみ再実行します。よろしいですか？", { n: failed.length }),
    okLabel: t("↻ 再実行"),
  });
  if (!ok) return;
  doRun(failed);
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

async function showLog(path) {
  try {
    const content = await App().ReadLogFile(path);
    const node = h(`<div><h3>${esc(t("ログ"))}: ${esc(path.split(/[\\/]/).pop())}</h3>
      <div class="log-view">${esc(content)}</div>
      <div class="modal-actions"><button class="btn" id="close">${esc(t("閉じる"))}</button></div></div>`);
    openModal(node);
    node.querySelector("#close").onclick = closeModal;
  } catch (e) { toast(t("ログを開けません") + ": " + terr(e), "err"); }
}

// live events from the backend
function wireEvents() {
  rt().EventsOn("run:event", ev => {
    const prev = runState[ev.device] || {};
    runState[ev.device] = { phase: ev.phase, message: ev.message,
      done: ev.phase === "running" ? (ev.done || 0) : prev.done,
      total: ev.phase === "running" ? (ev.total || 0) : prev.total,
      logPath: ev.phase === "done" ? ev.message : prev.logPath };
    const active = document.querySelector(".tab.active");
    if (active && active.id === "tab-run") renderRun();
  });
  rt().EventsOn("run:done", results => {
    running = false;
    clearInterval(etaTimer); etaTimer = 0;
    (results || []).forEach(r => {
      const prev = runState[r.device] || {};
      runState[r.device] = {
        phase: r.success ? "done" : "error",
        message: r.success ? (r.logPath || "") : r.error,
        logPath: r.logPath,
        done: r.success ? prev.total : prev.done,
        total: prev.total,
      };
    });
    const active = document.querySelector(".tab.active");
    if (active && active.id === "tab-run") renderRun();
    // Clear the "実行中" notice on the settings tab (unless mid-edit).
    else if (active && active.id === "tab-settings" && !settingsDirty) renderSettings();
    toast(t("実行が完了しました"), "ok");
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
      <div class="field"><label>${esc(t("ログファイル名テンプレート"))}</label>
        <input id="set-tmpl" value="${esc(s.logNameTemplate || "{host}_Config_{date}_{time}.txt")}"></div>
      <div class="muted" style="font-size:12px;margin-top:2px">${esc(t("ファイル名に使える変数（クリックでコピー）:"))}</div>
      <table class="ph-table"><tbody>
        <tr><td class="mono">{host}</td><td>${esc(t("ホスト名"))}</td><td class="mono">{date}</td><td>${esc(t("日付（yyyymmdd）"))}</td></tr>
        <tr><td class="mono">{ip}</td><td>${esc(t("IPアドレス"))}</td><td class="mono">{time}</td><td>${esc(t("時刻（hhmmss）"))}</td></tr>
        <tr><td class="mono">{os}</td><td>${esc(t("OS種別"))}</td><td class="mono">{hhmm}</td><td>${esc(t("時刻（hhmm）"))}</td></tr>
        <tr><td class="mono">{group}</td><td>${esc(t("グループ名"))}</td><td class="mono">{site}</td><td>${esc(t("拠点名"))}</td></tr>
      </tbody></table>
      <div class="muted" style="font-size:12px;margin-top:6px">${esc(t("※ログ保存フォルダに相対パス（例: logs）を指定した場合、PalaTerm.exe と同じフォルダが基準になります"))}</div>
      <div class="modal-actions"><button class="btn primary" id="set-save">${esc(t("保存"))}</button></div>
    </div>
    <div class="panel danger-zone" style="padding:18px 22px;max-width:620px;margin-top:22px">
      <div class="row-inline" style="justify-content:space-between;gap:14px">
        <div><b style="color:var(--err)">${esc(t("危険な操作"))}</b>
          <div class="muted" style="font-size:12px">${esc(t("すべてのデータ（機器・グループ・コマンドセット・OSタイププロファイル・マスターパスワード）を削除して初期状態に戻します。実行にはマスターパスワードの入力が必要です"))}</div></div>
        <button class="btn danger" id="set-reset" style="white-space:nowrap">${esc(t("リセット（全データ初期化）…"))}</button>
      </div>
    </div>`;
  document.getElementById("set-reset").onclick = resetVaultFlow;

  // Track unsaved edits so tab switches can warn (see canLeaveSettings).
  settingsDirty = false;
  ["set-par", "set-ct", "set-cmt", "set-dir", "set-tmpl"].forEach(id => {
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
      logNameTemplate: document.getElementById("set-tmpl").value || "{host}_Config_{date}_{time}.txt",
    };
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
        <div class="page-sub">${esc(t("機器種別ごとのログイン自動化（expect/send）。待つ文字は「#」「assword:」のような文字そのまま（部分一致）。ファイル書出は export\\os-profiles\\ に1プロファイル1ファイル（JSON）。削除してもファイルは消えないので、読込でいつでも戻せます"))}</div></div>
      <div class="row-inline" style="gap:6px;white-space:nowrap">
        <button class="btn" id="prof-imp">${esc(t("ファイル読込"))}</button>
        <button class="btn" id="prof-exp">${esc(t("ファイル書出"))}</button>
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
    if (!(await uiConfirm({ title: t("プロファイルを削除"), message: t("OSタイププロファイル「<b>{n}</b>」を削除しますか？<br><span class=\"muted\" style=\"font-size:12px\">書き出し済みのJSONファイルは残るため、「ファイル読込」でいつでも戻せます</span>", { n: esc(p ? p.name : "") }), okLabel: t("削除"), danger: true }))) return;
    try {
      await App().DeleteProfile(b.dataset.pdel);
      INV = await App().GetInventory();
      PROFILES = await App().ListProfiles();
      renderOSTypes();
      toast(t("削除しました"), "ok");
    } catch (e) { toast(terr(e), "err"); }
  });
  document.getElementById("prof-add").onclick = () => editProfile(null);
  document.getElementById("prof-exp").onclick = async () => {
    try {
      const dir = await App().ExportOSProfiles();
      if (dir) toast(t("書き出しました: {p}", { p: dir }), "ok");
    } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
  };
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
        <div class="field"><label>${esc(t("プロファイル名"))}</label><input id="p-name" value="${esc(p.name || "")}" ${prof ? "readonly" : ""} placeholder="${esc(t("例: MyRouter OS"))}"></div>
        <div class="field"><label><span data-tip="${esc(t("コマンドが打てる状態のときに機器が出す表示（例: Router#）。コマンド送信後、これが再び現れたら「出力が終わった」と判定して次のコマンドを送ります。「#」のように末尾の記号だけ書いておけば、設定モードのプロンプト（例: FG(console)#）にも一致します"))}">${esc(t("showコマンド完了の目印（必須） ⓘ"))}</span></label><input id="p-prompt" class="mono" value="${esc(p.prompt || "")}"></div>
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
