// Command sets, Run, Log Settings, and OS Types tabs.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

// ---- Command sets tab ----
function renderCommands() {
  const root = document.getElementById("tab-commands");
  // Sort / filter (display only; see listView in app.js). The text box
  // also searches the commands themselves, so "show arp" finds every set
  // that runs it.
  const LV = "commands";
  const focus = listViewFocus();
  const sets = INV.commandSets || [];
  // A set window started hidden ahead of View, so it opens at once (the
  // backend keeps just one while this tab is shown, and this is a no-op
  // while it has one; nothing while there are no sets). switchTab drops it
  // on leaving the tab; a redraw after that (unlock, a save) asks again.
  if (sets.length) App().PrewarmCommandSetWindow().catch(() => {});
  const visible = listViewApply(LV, sets,
    s => [s.name, ...(s.commands || []).map(c => c.text)],
    (s, k) => k === "count" ? (s.commands || []).length : s.name);
  const rows = visible.map(s => `
    <tr data-key="${esc(s.name)}">
      ${listViewDragCell(LV)}
      <td><div class="clip" data-tip="${esc(s.name)}"><b>${esc(s.name)}</b></div></td>
      <td>${esc(t("{n} コマンド", { n: (s.commands || []).length }))}${s.pauseOff ? `<br><span class="muted" style="font-size:11px" data-tip="${esc(t("「待機秒を使う」がオフのセットです（プロンプトが出たらすぐ次のコマンド）"))}">${esc(t("待機秒なし"))}</span>` : ""}</td>
      <td class="mono muted cs-preview" data-view="${esc(s.name)}" style="cursor:pointer"><div class="clip" data-tip="${esc((s.commands || []).map(c => c.text).join("\n"))}">${esc((s.commands || []).slice(0, 3).map(c => c.text).join("  /  "))}${(s.commands || []).length > 3 ? " …" : ""}</div></td>
      <td class="ops"><div class="ops-btns">
        <button class="btn sm act-view" data-view="${esc(s.name)}" data-tip="${esc(t("読み取り専用の別窓で開きます（端末窓の横に置いて見ながら打てます）"))}">${esc(LANG === "ja" ? "表示" : t("コマンドセット:表示"))}</button>
        <button class="btn sm act-edit" data-edit="${esc(s.name)}">${esc(t("編集"))}</button>
        <button class="btn sm act-copy" data-copy="${esc(s.name)}">${esc(t("複製"))}</button>
        <button class="btn sm act-del" data-del="${esc(s.name)}">${esc(t("削除"))}</button>
      </div></td>
    </tr>`).join("");

  renderListTab(root, `
    <div class="page-head">
      <div><div class="page-title">${esc(LANG === "ja" ? "コマンドセット" : t("メニュー:コマンドセット"))}</div>
        <div class="page-sub">${esc(t("機器ごとに割り当てて一括実行するコマンド群。書き出しは「機器」タブの一式書き出し（グループ単位、または全機器）にまとまっています。ここでは1ファイル（CSV / 旧 .list）を単体で読み込めます"))}</div></div>
      <div class="row-inline">
        <button class="btn" id="cs-imp" data-tip="${esc(t("コマンドセット 1 ファイル（CSV / 旧 .list）を読み込みます"))}">${esc(t("読み込み"))}</button>
        <button class="btn primary" id="add-set">${esc(t("＋ セットを追加"))}</button>
      </div>
    </div>
    ${sets.length ? listViewBar(LV, t("絞り込み（セット名・コマンド）")) : ""}
    <div class="panel scrollx">
      ${sets.length === 0
        ? `<div class="empty">${esc(t("コマンドセットがありません。"))}</div>`
        : visible.length === 0 ? `<div class="empty">${esc(t("絞り込みに一致するコマンドセットがありません"))}</div>`
        : `<table class="fixed" data-colw="commands"><thead><tr><th style="width:30px"></th>${listViewTh(LV, "name", t("名前"), "width:260px")}${listViewTh(LV, "count", t("コマンド数"), "width:120px")}<th>${esc(t("プレビュー"))}</th><th style="width:250px"></th></tr></thead>
           <tbody id="cs-body">${rows}</tbody></table>`}
    </div>`, [listView(LV)]);
  listViewWire(LV, root, renderCommands, focus);
  wireColResize(root);

  if (!listViewActive(LV)) wireRowReorder(root.querySelector("#cs-body"), async order => {
    try { await App().ReorderCommandSets(order); await refreshInventory(); }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); await refreshInventory(); }
  });
  document.getElementById("add-set").onclick = () => editCommandSet(null);
  document.getElementById("cs-imp").onclick = async () => {
    try {
      const res = await importOneFile(() => App().ImportCommandSetFile(), t("コマンドセット"));
      if (res) { await refreshInventory(); toast(res.same ? t("「{n}」は同じ内容で登録済みです", { n: res.name }) : t("「{n}」を読み込みました", { n: res.name }), "ok"); }
    } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
  };
  // 表示 (and a click on the preview): the set in a read-only window of its
  // own; the same set again brings that window to the front.
  root.querySelectorAll("[data-view]").forEach(b => b.onclick = () =>
    App().OpenCommandSetWindow(b.dataset.view).catch(e => toast(t("開けませんでした") + ": " + terr(e), "err")));
  root.querySelectorAll("[data-edit]").forEach(b => b.onclick = () =>
    editCommandSet(INV.commandSets.find(s => s.name === b.dataset.edit)));
  root.querySelectorAll("[data-copy]").forEach(b => b.onclick = () => {
    // As a device's 複製: a new, unsaved set filled from the source (its
    // name with _copy) — nothing exists until 保存, so キャンセル leaves no
    // "backup_copy" behind in the list.
    const src = (INV.commandSets || []).find(s => s.name === b.dataset.copy);
    if (!src) return;
    const taken = new Set((INV.commandSets || []).map(s => s.name));
    let nm = src.name + "_copy";
    for (let i = 2; taken.has(nm); i++) nm = `${src.name}_copy${i}`;
    editCommandSet(null, { ...JSON.parse(JSON.stringify(src)), name: nm });
    const inp = document.getElementById("s-name"); if (inp) { inp.focus(); inp.select(); }
  });
  root.querySelectorAll("[data-del]").forEach(b => b.onclick = async () => {
    // The devices on this set are named: deleting it leaves them with a
    // set that no longer exists (they then only log in).
    const users = (INV.devices || []).filter(d => d.commandSet === b.dataset.del);
    const note = users.length ? `<br><span style="font-size:12px;color:var(--warn)">${esc(t("⚠ このセットを割り当てている機器が {n} 台あります。削除すると、その機器はコマンドセット未割り当て（ログインのみ）になります: {list}", { n: users.length, list: users.slice(0, 10).map(d => d.name).join(", ") + (users.length > 10 ? " …" : "") }))}</span>` : "";
    if (!(await uiConfirm({ title: t("コマンドセットを削除"), message: t("コマンドセット「<b>{n}</b>」を削除しますか？", { n: esc(b.dataset.del) }) + note, okLabel: t("削除"), danger: true }))) return;
    try { await App().DeleteCommandSet(b.dataset.del); await refreshInventory(); toast(t("削除しました"), "ok"); }
    catch (e) { toast(t("削除失敗") + ": " + terr(e), "err"); }
  });
}

// The small window of まとめて追加: a text box over the set editor (the
// editor is itself the modal, so this sits on top of it rather than
// replacing it). onAdd receives the non-empty lines, trimmed.
function bulkCommandsDialog(onAdd) {
  const ov = h(`<div class="sub-overlay">
    <div class="sub-modal">
      <h3>${esc(t("コマンドをまとめて追加"))}</h3>
      <div class="muted" style="font-size:12px;margin:-8px 0 10px">${esc(t("1 行に 1 コマンドを入力してください。待機秒はリモート・シリアルともすべて 1 秒で追加されます（追加後に個別に変更できます）。空行は無視します。"))}</div>
      <div class="field" style="margin:0"><textarea id="bulk-text" style="min-height:220px" placeholder="show clock&#10;show running-config"></textarea></div>
      <div class="modal-actions">
        <span class="muted" style="font-size:12px;margin-right:auto;align-self:center">${esc(t("Ctrl+Enter で追加 / Esc で閉じる"))}</span>
        <button class="btn" id="bulk-cancel" type="button">${esc(t("キャンセル"))}</button>
        <button class="btn primary" id="bulk-ok" type="button">${esc(t("追加"))}</button>
      </div>
    </div>
  </div>`);
  document.body.appendChild(ov);
  const ta = ov.querySelector("#bulk-text");
  const close = () => ov.remove();
  const ok = () => {
    const lines = ta.value.split(/\r\n|\r|\n/).map(l => l.trim()).filter(l => l);
    if (!lines.length) { toast(t("コマンドを入力してください"), "err"); ta.focus(); return; }
    close();
    onAdd(lines);
  };
  ov.querySelector("#bulk-cancel").onclick = close;
  ov.querySelector("#bulk-ok").onclick = ok;
  ta.addEventListener("keydown", e => {
    if (e.key === "Enter" && e.ctrlKey) { e.preventDefault(); ok(); }
    else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(); }
  });
  ta.focus();
}

// editCommandSet edits set, or adds a new one (filled from tpl for 複製).
function editCommandSet(set, tpl) {
  const s = set || tpl || { name: "", commands: [] };
  // Working copy: one row per command, each with its own pause (sec).
  let cmds = (s.commands || []).map(c => ({
    text: c.text || "", pauseSec: c.pauseSec ?? 1, serialSec: c.serialSec ?? 1, wait: c.wait || "",
  }));
  if (cmds.length === 0) cmds = [{ text: "", pauseSec: 1, serialSec: 1, wait: "" }];

  const node = h(`<div>
    <h3>${esc(set ? t("コマンドセットを編集") : t("コマンドセットを追加"))}</h3>
    <div class="field"><label>${esc(t("セット名"))} <span class="req">${esc(t("必須"))}</span></label><input id="s-name" value="${esc(s.name)}" ${set ? `data-tip="${esc(t("名前を変えると、このセットを割り当てている機器もそのまま新しい名前を使います"))}"` : ""}></div>
    <div class="row-inline" style="gap:16px;flex-wrap:wrap">
      <label style="font-size:12px;color:var(--text-dim)">${esc(t("コマンド（1行1コマンド・待機秒はコマンドごとに指定）"))}</label>
      <label style="font-size:12px;font-weight:normal;display:flex;gap:6px;align-items:center;cursor:pointer" data-tip="${esc(t("オフにすると、このセットの全コマンドで待機秒を使わず、プロンプト（または「待つ文字列」）が出たらすぐ次のコマンドを送ります。待機秒の値は残るので、オンに戻せば元どおりです"))}"><input type="checkbox" id="s-usepause" style="width:auto" ${s.pauseOff ? "" : "checked"}> ${esc(t("待機秒を使う ⓘ"))}</label>
    </div>
    <div class="scroll"><table style="margin-top:6px">
      <thead><tr>
        <th style="width:26px"></th><th style="width:36px">#</th><th>${esc(t("コマンド"))}</th>
        <th style="width:170px"><span data-tip="${esc(t("このコマンドのあと、機器の今の行がこの文字列で終わったら次のコマンドを送ります（例: (config)#）。文字そのまま・行末で一致（行の途中に出ただけでは待ち続けます）。空欄なら OS タイプのプロンプト（コマンド完了の目印）を待ちます"))}">${esc(t("待つ文字列（任意） ⓘ"))}</span></th>
        <th style="width:110px">${esc(t("待機(リモート)"))}</th><th style="width:110px">${esc(t("待機(シリアル)"))}</th><th style="width:76px"></th>
      </tr></thead>
      <tbody id="cmd-rows"></tbody>
    </table></div>
    <button class="btn sm" id="add-cmd" type="button" style="margin-top:8px">${esc(t("＋ 行を追加"))}</button>
    <button class="btn sm" id="bulk-cmd" type="button" style="margin-top:8px" data-tip="${esc(t("複数のコマンドを 1 行 1 コマンドで入力して一度に追加します（待機はすべて 1 秒）"))}">${esc(t("＋ まとめて追加…"))}</button>
    <div class="muted" style="font-size:12px;margin-top:6px">${esc(t("貼り付けは「行を追加」後の各コマンド欄へ。複数行をまとめて貼るとその行数ぶん自動で分割されます。"))}</div>
    <div class="modal-actions">
      <button class="btn" id="cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="save" data-tip="Ctrl+Enter">${esc(t("保存"))}</button>
    </div>
  </div>`);
  openModal(node, "set-edit");

  const tbody = node.querySelector("#cmd-rows");
  function collect() {
    const rows = tbody.querySelectorAll("tr");
    const arr = [];
    rows.forEach(r => arr.push({
      text: r.querySelector(".c-text").value,
      pauseSec: parseInt(r.querySelector(".c-pause").value, 10) || 0,
      serialSec: parseInt(r.querySelector(".c-spause").value, 10) || 0,
      wait: r.querySelector(".c-wait").value,
    }));
    return arr;
  }
  // 待機秒を使う off: the pause fields stay (their values are kept) but
  // are greyed out, since the run ignores them.
  const usePause = node.querySelector("#s-usepause");
  function syncPause() {
    tbody.querySelectorAll(".c-pause, .c-spause").forEach(inp => {
      inp.disabled = !usePause.checked;
      inp.dataset.tip = usePause.checked ? "" : t("「待機秒を使う」がオフのため使いません");
    });
  }
  usePause.onchange = syncPause;
  function render() {
    tbody.innerHTML = cmds.map((c, i) => `
      <tr data-key="${i}">
        <td class="drag-handle" data-tip="${dragHandleTip()}">⠿</td>
        <td class="muted">${i + 1}</td>
        <td><input class="c-text mono" style="width:100%" value="${esc(c.text)}" placeholder="show running-config"></td>
        <td><input class="c-wait mono" style="width:100%" value="${esc(c.wait || "")}" placeholder="${esc(t("（プロンプト）"))}"></td>
        <td><input class="c-pause" type="number" style="width:100%" value="${c.pauseSec}"></td>
        <td><input class="c-spause" type="number" style="width:100%" value="${c.serialSec}"></td>
        <td style="white-space:nowrap">
          <button class="btn sm" type="button" data-ins="${i}" data-tip="${esc(t("この行の下に挿入"))}">＋</button>
          <button class="btn sm act-del" type="button" data-rm="${i}" data-tip="${esc(t("この行を削除"))}">×</button>
        </td>
      </tr>`).join("");
    tbody.querySelectorAll("[data-rm]").forEach(btn => btn.onclick = () => {
      cmds = collect(); cmds.splice(parseInt(btn.dataset.rm, 10), 1);
      if (cmds.length === 0) cmds = [{ text: "", pauseSec: 1, serialSec: 1, wait: "" }];
      render();
    });
    // Insert a blank row directly below this one, and focus it.
    tbody.querySelectorAll("[data-ins]").forEach(btn => btn.onclick = () => {
      const i = parseInt(btn.dataset.ins, 10);
      cmds = collect();
      cmds.splice(i + 1, 0, { text: "", pauseSec: cmds[i].pauseSec, serialSec: cmds[i].serialSec, wait: "" });
      render();
      const inputs = tbody.querySelectorAll(".c-text");
      if (inputs[i + 1]) inputs[i + 1].focus();
    });
    // Enter in a command field opens the next row (the ＋ of this row), the
    // way a list is typed; Ctrl+Enter saves (app.js).
    tbody.querySelectorAll(".c-text").forEach((inp, i) => inp.addEventListener("keydown", e => {
      if (e.key !== "Enter" || e.ctrlKey || e.metaKey || e.isComposing) return;
      e.preventDefault();
      const ins = tbody.querySelector(`[data-ins="${i}"]`);
      if (ins) ins.click();
    }));
    // Multi-line paste in a command field splits into multiple rows.
    tbody.querySelectorAll(".c-text").forEach((inp, i) => inp.addEventListener("paste", e => {
      const txt = (e.clipboardData || window.clipboardData).getData("text");
      if (txt && txt.includes("\n")) {
        e.preventDefault();
        cmds = collect();
        const lines = txt.split("\n").map(l => l.trim()).filter(l => l);
        const add = lines.map(text => ({ text, pauseSec: cmds[i].pauseSec, serialSec: cmds[i].serialSec, wait: "" }));
        cmds.splice(i, 1, ...add);
        render();
      }
    }));
    // Drag-reorder rows; collect() reads the new DOM order.
    wireRowReorder(tbody, () => { cmds = collect(); render(); });
    syncPause();
  }
  render();

  node.querySelector("#add-cmd").onclick = () => {
    cmds = collect(); cmds.push({ text: "", pauseSec: 1, serialSec: 1, wait: "" }); render();
  };
  // まとめて追加: one command per line, each with a 1-second pause, appended
  // after the existing rows (a lone empty row is replaced instead).
  node.querySelector("#bulk-cmd").onclick = () => bulkCommandsDialog(lines => {
    cmds = collect().filter((c, i, all) => !(all.length === 1 && !c.text.trim()));
    cmds.push(...lines.map(text => ({ text, pauseSec: 1, serialSec: 1, wait: "" })));
    render();
    toast(t("{n} 行を追加しました", { n: lines.length }), "ok");
  });
  guardDiscard(node, "#cancel");
  node.querySelector("#save").onclick = async () => {
    const name = node.querySelector("#s-name").value.trim();
    if (!name) { toast(t("セット名は必須です"), "err"); return; }
    const commands = collect().filter(c => c.text.trim().length > 0)
      .map(c => ({ text: c.text.trim(), pauseSec: c.pauseSec, serialSec: c.serialSec, wait: c.wait.trim() }));
    try {
      // An edited set may have been renamed: move it (and the devices that
      // use it) to the new name first, then save the commands under it.
      if (set && name !== set.name) await App().RenameCommandSet(set.name, name);
      // A new set must not land on an existing name (SaveCommandSet replaces
      // by name: a typo of "backup" used to overwrite backup's 30 lines).
      if (set) await App().SaveCommandSet({ name, commands, pauseOff: !usePause.checked });
      else await App().CreateCommandSet({ name, commands, pauseOff: !usePause.checked });
      closeModal(); await refreshInventory(); toast(t("保存しました"), "ok");
    }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  };
}

// ---- Run tab ----
let running = false;
// This PC's lid setting, read from Windows (never changed) when a run
// starts: a laptop whose lid sleeps it stops the run when closed, which the
// run tab says while the run goes. Screen lock does not stop it.
let runPower = null;
function runLidNote() {
  if (running && runPower === null) {
    runPower = {}; // asked once per run
    App().GetPowerInfo().then(p => { runPower = p || {}; if (running && document.querySelector("#tab-run.active")) renderRun(); }).catch(() => { });
  }
  if (!running || !runPower || !runPower.lidPresent) return "";
  const act = runPower.onAC === false ? runPower.lidActionDC : runPower.lidActionAC;
  if (act === 0) return "";
  return `<div class="warn-box" style="margin:-6px 0 14px">${esc(t("ⓘ この PC は、ふたを閉じるとスリープして実行が止まります。ふたは開けたままにしてください（画面ロックは大丈夫です）"))}</div>`;
}
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
// （グループ未設定） in the group selector: the devices without a group (a
// deleted group leaves its devices there; they could not be run before).
const RUN_UNGROUPED = "__ungrouped__";
function runGroupName() { return runGroupSel === RUN_UNGROUPED ? "" : runGroupSel; }
function runGroupLabel() { return runGroupSel === RUN_UNGROUPED ? t("（グループ未設定）") : runGroupSel; }
// The run tab's devices: the chosen group's, those with a CLI (a GUI-only
// device has nothing to run).
function runGroupDevices() {
  return runGroupSel ? (INV.devices || []).filter(d => (d.group || "") === runGroupName() && !d.cliOff) : [];
}
// The command set for the next run only: "" = each device's own; a set's
// name = that set on every target; RUN_ADHOC = the commands typed in runAdHoc
// (one per line, 1-second pause). Nothing is saved to the devices.
const RUN_ADHOC = "__adhoc__";
let runSetSel = "", runAdHoc = "";
function runAdHocCommands() {
  return runAdHoc.split(/\r?\n/).map(s => s.trim()).filter(Boolean).map(text => ({ text, pauseSec: 1, serialSec: 1 }));
}
// The set name the run tab shows for a device, and the set object it runs.
function runSetName(d) { return runSetSel === RUN_ADHOC ? t("（今回のみ入力したコマンド）") : (runSetSel || d.commandSet || ""); }
// A device's own set that was deleted (or renamed by hand in a CSV): the
// row says so instead of showing the name as if it still ran.
function runSetMissing(d) {
  return !runSetSel && !!d.commandSet && !(INV.commandSets || []).some(s => s.name === d.commandSet);
}
function runSetOf(d) {
  if (runSetSel === RUN_ADHOC) return { commands: runAdHocCommands() };
  const name = runSetSel || (d && d.commandSet);
  return (INV.commandSets || []).find(s => s.name === name) || null;
}
// The RunWith / CheckDangerousCommands request for names in the chosen group.
function runRequest(names, dryRun) {
  return {
    names, group: runGroupName(), ungrouped: runGroupSel === RUN_UNGROUPED, stage: runStage, dryRun: !!dryRun, direct: runDirect,
    commandSet: runSetSel && runSetSel !== RUN_ADHOC ? runSetSel : "",
    adHoc: runSetSel === RUN_ADHOC ? runAdHocCommands() : [],
  };
}
// 作業タイミング of the next run: "" / before / during / after. It goes into
// the run folder and log file names (…_before.txt) so the logs of one
// maintenance job can be told apart, and the 実行履歴 tab can pair a before
// with an after and show what changed.
let runStage = "";
// 踏み台を使わずに接続: this run (and the 接続 buttons of the run tab) reach
// every device directly, its jump hosts left out — on site, plugged into
// the devices' network. Nothing is saved.
let runDirect = false;
function devHasBastion(d) { return !!d.useMyBastion || (d.bastions || []).some(b => b.host) || !!(d.bastion && d.bastion.host); }
// The line a run's confirmation adds when it goes without jump hosts — or,
// going through them, when devices ask for My jump host and none is
// registered (each of them would only fail).
function directNote(direct, names) {
  if (direct) return `<br><span style="font-size:12px;color:var(--warn)">${esc(t("踏み台を使わずに、この PC から各機器へ直接接続します"))}</span>`;
  const set = new Set(names || []);
  const n = runGroupDevices().filter(d => set.has(d.name) && d.useMyBastion).length;
  if (n && !(INV.myBastions || []).some(b => b.host)) return `<br><span style="font-size:12px;color:var(--err)">${esc(t("⚠ 「自分の踏み台を先に通る」機器が {n} 台ありますが、自分の踏み台が登録されていません（設定タブ）。この機器は接続できません", { n }))}</span>`;
  return "";
}
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
    if (q && ![d.name, d.host, d.site || "", d.role || "", runSetName(d), profileName(d.osType)].some(v => String(v).toLowerCase().includes(q))) return false;
    if (runFilter.site && (d.site || "") !== runFilter.site) return false;
    if (runFilter.status && !(st.phase === runFilter.status || (runFilter.status === "running" && ["connecting", "login", "saving"].includes(st.phase)))) return false;
    return true;
  });
  if (runSort.key) {
    const val = d => {
      if (runSort.key === "phase") { const st = runState[d.name] || { phase: d.enabled ? "queued" : "off" }; return PHASE_ORDER[st.phase] ?? 9; }
      return String(d[runSort.key] || "").toLowerCase();
    };
    // Natural order: 10.0.0.9 before 10.0.0.10, sw2 before sw10.
    out = out.slice().sort((a, b) => { const x = val(a), y = val(b); return (typeof x === "number" ? (x < y ? -1 : x > y ? 1 : 0) : x.localeCompare(y, undefined, { numeric: true })) * runSort.dir; });
  }
  return out;
}
// Devices the run buttons act on: the checked ones of the group, narrowed to
// the visible rows while a filter is on.
// A device of the run tab's group by name. The run (and its live events) is
// keyed by name, which is unique within a group but not across groups.
function runDev(name) {
  return runGroupDevices().find(x => x.name === name);
}
function runTargetDevices() {
  const devs = runGroupDevices();
  return (runFilterActive() ? runVisible(devs) : devs).filter(d => d.enabled);
}
function stageLabel(st) {
  return ({ "": t("指定なし"), before: t("作業前"), during: t("作業中"), after: t("作業後"), work: t("作業中"), schedule: t("予約") })[st] || st;
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
  // A set chosen for this run that has since been renamed or deleted: back to
  // each device's own (the selector could not show it, and the run would fail).
  if (runSetSel && runSetSel !== RUN_ADHOC && !(INV.commandSets || []).some(s => s.name === runSetSel)) runSetSel = "";
  // Targets appear only after a group is chosen (no accidental all-device
  // runs). Every device of the group is listed so 実行対象 can be toggled
  // right here; unchecked ones show as 対象外.
  const devs = runGroupDevices();
  const visible = runVisible(devs);
  const filtered = runFilterActive();
  // Keep the filter box's focus and caret across the re-render.
  const focusId = document.activeElement && document.activeElement.id;
  const focusSel = focusId === "run-filter" ? boxSelection(document.activeElement) : null;
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
      <td><input type="checkbox" data-enable="${esc(d.id)}" ${d.enabled ? "checked" : ""} ${running ? "disabled" : ""}></td>
      <td><span class="dot st-${st.phase === "off" ? "queued" : st.phase}"></span>${statusLabel(st.phase)}${st.warnings && st.warnings.length ? ` <span class="badge" style="color:var(--warn);border:1px solid var(--warn);background:none" data-tip="${esc(st.warnings.join(String.fromCharCode(10)))}">${esc(t("警告 {n}", { n: st.warnings.length }))}</span>` : ""}${pbar}</td>
      <td><div class="clip" data-tip="${esc(d.name)}"><b>${esc(d.name)}</b></div></td>
      <td class="mono"><div class="clip" data-tip="${esc(d.host)}">${esc(d.host)}</div></td>
      <td><div class="clip" data-tip="${esc(d.site || "")}">${esc(d.site || "")}</div></td>
      <td><div class="clip" data-tip="${esc(d.role || "")}">${esc(d.role || "")}</div></td>
      <td class="muted"><div class="clip" data-tip="${esc(runSetMissing(d) ? t("コマンドセット「{s}」は存在しません（削除または改名）。機器の編集で割り当て直してください。このままだとログインのみで終わります", { s: d.commandSet }) : runSetName(d) || t("コマンドセット未割り当て: ログインのみで終わります"))}" ${runSetSel ? 'style="color:var(--accent)"' : (runSetMissing(d) || !runSetName(d) ? 'style="color:var(--warn)"' : "")}>${esc(runSetMissing(d) ? t("{s}（存在しません）", { s: d.commandSet }) : runSetName(d) || t("（なし）"))}</div></td>
      <td class="muted mono"><div class="${st.phase === "error" || (st.warnings && st.warnings.length) ? "clip2 run-msg" : "clip"}" ${st.phase === "error" || (st.warnings && st.warnings.length) ? `data-msg="${esc(d.name)}" data-tip="${esc(t("クリックで全文を表示"))}"` : `data-tip="${esc(runMsg(st))}"`}>${esc(runMsgShort(st))}</div></td>
      <td style="text-align:right;white-space:nowrap">
        ${st.output ? `<span class="link" data-output="${esc(d.name)}" data-tip="${esc(t("接続確認で機器が表示した内容を見ます（ログは残していません）"))}">${esc(t("出力"))}</span> ` : ""}
        ${st.logPath ? `<span class="link" data-watch="${esc(st.logPath)}" data-tip="${esc(t("機器の画面を表示のみで開きます（実行中は出力をそのまま追います）"))}">${esc(t("画面"))}</span> <span class="link" data-log="${esc(st.logPath)}">${esc(t("ログ表示"))}</span> ` : ""}
        ${!running && /ホストキーが前回接続時と異なります/.test(st.message || "") ? `<button class="btn sm warn-outline" data-hk="${esc(d.id)}">${esc(t("鍵を許可して再実行"))}</button> ` : ""}
        ${running && ["queued", "connecting", "login", "running", "saving"].includes(st.phase) && !cancelling
          ? `<button class="btn sm err-outline" data-stop="${esc(d.name)}" ${st.stopping ? "disabled" : ""} data-tip="${esc(t("この機器だけ止めます（ほかの機器は続けます）"))}">${esc(st.stopping ? t("中止中…") : t("止める"))}</button> ` : ""}
        ${!running ? `<button class="btn sm act-edit" data-editdev="${esc(d.id)}" data-tip="${esc(t("この機器の設定（アドレス・認証情報・踏み台など）を開きます。失敗の原因をここで直して ↻ で再実行"))}">${esc(t("編集"))}</button> ` : ""}
        <button class="btn sm act-conn" data-term="${esc(d.id)}" data-tip="${esc(CONNECT_TIP)}">${esc(t("接続"))}</button>
      </td>
    </tr>`;
  }).join("");

  const enabledCount = (filtered ? visible : devs).filter(d => d.enabled).length;
  // 並べて接続 opens the ticked devices that have a CLI (GUI-only ones have
  // no terminal): the count on its button.
  const tileOrder = new Map(visible.map((d, i) => [d.id, i]));
  const tileDevs = runTargetDevices().filter(d => !d.cliOff).sort((a, b) => (tileOrder.get(a.id) ?? 1e9) - (tileOrder.get(b.id) ?? 1e9));
  const tileCount = tileDevs.length;
  const hiddenChecked = filtered ? devs.filter(d => d.enabled && !visible.includes(d)).length : 0;
  const sites = [...new Set(devs.map(d => d.site || "").filter(Boolean))].sort();
  const sortArrow = k => runSort.key === k ? (runSort.dir > 0 ? " ▲" : " ▼") : "";
  const th = (k, label, w) => `<th ${w ? `style="width:${w}"` : ""} class="sortable" data-sort="${k}" data-tip="${esc(t("クリックで並び替え"))}">${esc(label)}${sortArrow(k)}</th>`;
  const vals = Object.values(runState);
  const failedCount = vals.filter(s => s.phase === "error").length;
  const canceledCount = vals.filter(s => s.phase === "canceled").length;
  const retryLabel = failedCount && canceledCount ? t("↻ 失敗・中止のみ再実行（{n}台）", { n: failedCount + canceledCount })
    : canceledCount ? t("↻ 中止分のみ再実行（{n}台）", { n: canceledCount })
    : t("↻ 失敗のみ再実行（{n}台）", { n: failedCount });
  const hasResults = vals.length > 0;
  const groups = INV.deviceGroups || [];
  const ungroupedCount = (INV.devices || []).filter(d => !d.group).length;
  // The table panel scrolls inside itself and a run re-renders on every
  // device event: the old panel element is kept (only its contents swapped)
  // so the scroll position - and a scrollbar drag in progress - survive.
  // A different group / filter / sort starts from the top as before.
  const oldPanel = root.querySelector(".panel.scrollx");
  // The jump-host choice shows only for a group that has jump hosts (and is
  // off for one that has none, so it is never on unseen).
  const anyBastion = devs.some(devHasBastion);
  if (!anyBastion) runDirect = false;
  const viewKey = JSON.stringify([runGroupSel, runFilter, runSort]);
  const html = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("実行"))}${runGroupSel ? ` <span style="font-size:15px;font-weight:600;color:var(--accent)">${esc(runGroupSel === RUN_UNGROUPED ? runGroupLabel() : t("グループ「{g}」", { g: runGroupSel }))}</span>` : ""}</div>
        <div class="page-sub">${esc(runGroupSel ? (filtered ? t("絞り込み中: 表示 {b} 台のうち対象 {a} 台（表示されている機器だけが実行されます）", { a: enabledCount, b: visible.length }) : t("対象 {a} / {b} 台（チェック=実行対象。Shift+クリックで範囲をまとめて切替）", { a: enabledCount, b: devs.length })) : t("まず「グループで対象を選択…」から実行するグループを選んでください"))}${runGroupSel ? " " + gridDraftBadge(runGroupSel, true) : ""}</div></div>
      <div class="row-inline" style="gap:10px;align-items:center;flex-wrap:wrap;justify-content:flex-end">
        <div class="run-stat" style="margin-left:0">
          <div>${esc(t("成功"))} <b id="rc-ok" style="color:var(--ok)">0</b></div>
          <div>${esc(t("失敗"))} <b id="rc-err" style="color:var(--err)">0</b></div>
          <div>${esc(t("実行中"))} <b id="rc-run">0</b></div>
          <div><span data-tip="${esc(t("完了済みコマンド数と経過時間から算出した全体の予想残り時間。実行が進むほど精度が上がります"))}">${esc(t("予想残り"))}</span> <b id="rc-eta">${running ? computeEta() : "—"}</b></div>
        </div>
        ${!running && lastRunDir ? `<button class="btn" id="btn-rundir" data-tip="${esc(t("直前の一括実行のログが入ったフォルダを開きます: {d}", { d: lastRunDir }))}">${esc(t("📂 この実行のフォルダ"))}</button>` : ""}
        <button class="btn" id="btn-logdir">${esc(t("ログフォルダを開く"))}</button>
      </div>
    </div>
    <div class="run-toolbar">
      <button class="btn run" id="btn-run" ${running || enabledCount === 0 ? "disabled" : ""}>${esc(filtered ? t("▶ 表示中の {n} 台を実行", { n: enabledCount }) : t("▶ 一括実行"))}</button>
      <button class="btn" id="btn-dry" ${running || enabledCount === 0 ? "disabled" : ""} data-tip="${esc(t("接続とログインだけを行い、コマンドは送りません。ログも残しません。認証情報や到達性の事前確認に"))}">${esc(t("⚡ 接続確認のみ"))}</button>
      <button class="btn danger" id="btn-cancel" ${running && !cancelling ? "" : "disabled"}>${esc(cancelling ? t("中止中…") : t("■ 中止"))}</button>
      ${!running && (failedCount || canceledCount) ? `<button class="btn err-outline" id="btn-retry">${esc(retryLabel)}</button>` : ""}
      ${!running && failedCount ? `<button class="btn" id="btn-fail-csv" data-tip="${esc(t("エラーになった機器を、機器名・IP・拠点・接続方式・踏み台・エラー内容の表（CSV）にして export\\run-results に書き出します。パスワードは含みません"))}">${esc(t("⇩ エラーの機器をCSV出力（{n}台）", { n: failedCount }))}</button>` : ""}
      ${runGroupSel && devs.length ? `<button class="btn" id="btn-tile" ${tileCount === 0 ? "disabled" : ""} data-tip="${esc(t("チェックした実行対象の機器（絞り込み中は表示中の機器だけ）の端末窓をまとめて開き、画面いっぱいに並べます（最大 16 台）。各窓の「同時入力」をオンにすると、どれか 1 つの窓で打ったキーがオンの窓すべてに送られます（最初からオンで開くかは設定タブで選べます）。「踏み台: 使わない」もこの窓に効きます"))}">${esc(t("並べて接続（{n}台）", { n: tileCount }))}</button>` : ""}
      ${!running && hasResults ? `<button class="btn" id="btn-clear" data-tip="${esc(t("表の結果を消して全機器を「順番待ち」に戻します（ログは残ります）"))}">${esc(t("✕ 結果をクリア"))}</button>` : ""}
      <div style="flex-basis:100%;height:0;margin:-4px 0 0"></div>
      ${groups.length || ungroupedCount ? `<select id="run-grp" class="btn" style="padding-right:8px" ${running ? "disabled" : ""}>
        <option value="">${esc(t("グループで対象を選択…"))}</option>
        ${groups.map(g => `<option value="${esc(g.name)}" ${g.name === runGroupSel ? "selected" : ""}>${esc(t("{g}（{n}台）", { g: g.name, n: groupCount(g.name) }))}</option>`).join("")}
        ${ungroupedCount ? `<option value="${RUN_UNGROUPED}" ${runGroupSel === RUN_UNGROUPED ? "selected" : ""}>${esc(t("{g}（{n}台）", { g: t("（グループ未設定）"), n: ungroupedCount }))}</option>` : ""}
      </select>` : ""}
      <select id="run-stage" class="btn" style="padding-right:8px" ${running ? "disabled" : ""} data-tip="${esc(t("作業前／作業中／作業後のどのタイミングの取得かを選びます。ログのフォルダ名とファイル名に付き、実行履歴・比較タブで作業前と作業後を比較できます"))}">
        ${STAGES.map(st => `<option value="${st}" ${st === runStage ? "selected" : ""}>${esc(st ? t("作業タイミング: {s}", { s: stageLabel(st) }) : t("作業タイミング: 指定なし"))}</option>`).join("")}
      </select>
      ${anyBastion ? `<select id="run-route" class="btn${runDirect ? " warn-outline" : ""}" style="padding-right:8px" ${running ? "disabled" : ""} data-tip="${esc(t("この実行と、この表の「接続」だけ、踏み台を通らずにこの PC から機器へ直接接続します（現場で機器のネットワークにつないでいるときなど）。機器の設定は変わりません"))}">
        <option value="" ${runDirect ? "" : "selected"}>${esc(t("踏み台: 機器の設定どおり"))}</option>
        <option value="direct" ${runDirect ? "selected" : ""}>${esc(t("踏み台: 使わない（直接接続）"))}</option>
      </select>` : ""}
      <select id="run-set" class="btn" style="padding-right:8px;max-width:260px" ${running ? "disabled" : ""} data-tip="${esc(t("この実行だけ、全機器で使うコマンドセットを差し替えます（機器の割り当ては変わりません）。「今回だけ入力…」で、その場で打ったコマンドを流せます"))}">
        <option value="" ${!runSetSel ? "selected" : ""}>${esc(t("コマンド: 機器ごとの設定どおり"))}</option>
        ${(INV.commandSets || []).map(cs => `<option value="${esc(cs.name)}" ${cs.name === runSetSel ? "selected" : ""}>${esc(t("コマンド: {s}（今回のみ）", { s: cs.name }))}</option>`).join("")}
        <option value="${RUN_ADHOC}" ${runSetSel === RUN_ADHOC ? "selected" : ""}>${esc(runSetSel === RUN_ADHOC ? t("コマンド: 今回だけ入力（{n} 行）", { n: runAdHocCommands().length }) : t("コマンド: 今回だけ入力…"))}</option>
      </select>
      ${runSetSel === RUN_ADHOC && !running ? `<button class="btn sm" id="run-adhoc-edit">${esc(t("入力を編集…"))}</button>` : ""}
    </div>
    ${runLidNote()}
    ${runGroupSel && devs.length ? `<div class="run-toolbar" style="margin-top:-8px;gap:8px">
      <input id="run-filter" class="btn" style="width:280px;text-align:left;cursor:text" placeholder="${esc(t("絞り込み（機器名・IP・拠点・役割・OS・コマンドセット）"))}" value="${esc(runFilter.text)}">
      <select id="run-filter-site" class="btn" style="padding-right:8px"><option value="">${esc(t("拠点: すべて"))}</option>${sites.map(s => `<option value="${esc(s)}" ${s === runFilter.site ? "selected" : ""}>${esc(s)}</option>`).join("")}</select>
      <select id="run-filter-status" class="btn" style="padding-right:8px"><option value="">${esc(t("状態: すべて"))}</option>${["queued", "off", "done", "error", "canceled", "running"].map(p => `<option value="${p}" ${p === runFilter.status ? "selected" : ""}>${esc(statusLabel(p))}</option>`).join("")}</select>
      ${filtered ? `<button class="btn sm" id="run-filter-clear">${esc(t("✕ 絞り込み解除"))}</button>` : ""}
      ${runSort.key ? `<button class="btn sm" id="run-sort-clear">${esc(t("並び順を戻す"))}</button>` : ""}
      ${hiddenChecked ? `<span class="warn-box inline">${esc(t("⚠ 非表示の行にチェック済みが {n} 台あります。この実行には含まれません（含めるには絞り込みを解除）", { n: hiddenChecked }))}</span>` : ""}
    </div>` : ""}
    <div class="panel scrollx">
      ${devs.length === 0 ? `<div class="empty">${esc(runGroupSel ? t("このグループには機器がありません。機器一覧で追加してください。") : t("「グループで対象を選択…」から実行するグループを選んでください。"))}</div>`
        : visible.length === 0 ? `<div class="empty">${esc(t("絞り込みに一致する機器がありません"))}</div>`
        : `<table class="fixed" data-colw="run"><thead><tr>
           <th style="width:36px"><input type="checkbox" id="run-chk-all" ${visible.length && visible.every(d => d.enabled) ? "checked" : ""} ${running ? "disabled" : ""} data-tip="${esc(filtered ? t("表示中の行を全選択/全解除") : t("全選択/全解除"))}"></th>
           ${th("phase", t("状態"), "126px")}${th("name", t("機器名"), "132px")}${th("host", t("IPアドレス"), "112px")}
           ${th("site", t("拠点名"), "84px")}${th("role", t("役割"), "84px")}${th("commandSet", t("コマンドセット"), "126px")}
           <th>${esc(t("メッセージ"))}</th><th style="width:${LANG === "ja" ? 220 : 240}px"></th></tr></thead><tbody id="run-body">${rows}</tbody></table>`}
    </div>`;
  // A detached element loses its scroll offset, so the old panel stays in
  // the DOM (only its contents swapped), and the filter box being typed in
  // stays as it is (see patchKeeping): the nodes around them are drawn anew.
  const keepPanel = oldPanel && oldPanel.dataset.view === viewKey
    ? { o: oldPanel, find: f => f.querySelector(".panel.scrollx"), inner: true } : null;
  patchKeeping(root, html.replace('<div class="panel scrollx">', `<div class="panel scrollx" data-view="${esc(viewKey)}">`),
    [keepPanel, keepFocusedBox(root, "run-filter")]);

  // Filter / sort wiring. The text box re-renders on every keystroke; a box
  // kept across the redraw is wired already and keeps its caret.
  const fIn = document.getElementById("run-filter");
  if (fIn && !fIn._lvWired) {
    fIn._lvWired = true;
    wireFilterInput(fIn, () => { runFilter.text = fIn.value; renderRun(); }, val => { runFilter.text = val; });
  }
  if (fIn && focusId === "run-filter" && document.activeElement !== fIn) { fIn.focus(); restoreBoxSelection(fIn, focusSel); }
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
  wireColResize(root);
  root.querySelectorAll("[data-hk]").forEach(b => b.onclick = async () => {
    const dev = devById(b.dataset.hk);
    if (!dev) return;
    const n = dev.name;
    const ok = await uiConfirm({
      title: t("ホストキーが前回と異なります"),
      message: t("機器「<b>{n}</b>」のホストキーが前回接続時と変わっています。<br><b>機器を交換した／OSを再インストールした</b>場合だけ許可してください。心当たりがなければ、なりすましの可能性があるため許可せずネットワーク管理者に確認してください。", { n: esc(n) }),
      okLabel: t("機器を交換したので許可して再実行"),
      danger: true,
    });
    if (!ok) return;
    if ((await confirmRerun([n])) === false) return;
    try { await App().ClearHostKeys(dev.id); doRun([n], lastRunDry, true); }
    catch (e) { toast(terr(e), "err"); }
  });

  document.getElementById("btn-run").onclick = () => startRun(false);
  // 並べて接続: the same devices a run would take (the ticked ones, only the
  // visible ones while filtered), in the table's order.
  const btnTile = document.getElementById("btn-tile");
  if (btnTile) btnTile.onclick = () => tiledConnect(tileDevs.map(d => d.id), runDirect);
  document.getElementById("btn-dry").onclick = () => startRun(true);
  const setSel = document.getElementById("run-set");
  if (setSel) setSel.onchange = async () => {
    if (setSel.value === RUN_ADHOC && !(await adHocDialog())) { setSel.value = runSetSel; return; }
    runSetSel = setSel.value;
    renderRun();
  };
  const adEdit = document.getElementById("run-adhoc-edit");
  if (adEdit) adEdit.onclick = async () => { if (await adHocDialog()) renderRun(); };
  const stageSel = document.getElementById("run-stage");
  if (stageSel) stageSel.onchange = () => { runStage = stageSel.value; };
  const routeSel = document.getElementById("run-route");
  if (routeSel) routeSel.onchange = () => { runDirect = routeSel.value === "direct"; renderRun(); };
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
  const btnFailCsv = document.getElementById("btn-fail-csv");
  if (btnFailCsv) btnFailCsv.onclick = exportRunFailures;
  const btnClear = document.getElementById("btn-clear");
  if (btnClear) btnClear.onclick = () => { for (const k in runState) delete runState[k]; renderRun(); };
  document.getElementById("btn-logdir").onclick = () => App().OpenLogDir().catch(e => toast(terr(e), "err"));
  const btnRunDir = document.getElementById("btn-rundir");
  if (btnRunDir) btnRunDir.onclick = () => App().OpenRunFolder(lastRunDir).catch(e => toast(terr(e), "err"));
  // 編集 on a row: a failed login is fixed where it failed, and ↻ re-runs it.
  root.querySelectorAll("[data-editdev]").forEach(b => b.onclick = () => { const d = devById(b.dataset.editdev); if (d) editDevice(d); });
  wireGridBadges(root);
  const runGrp = document.getElementById("run-grp");
  if (runGrp) runGrp.onchange = async () => {
    // Results (and the 失敗のみ再実行 button) belong to the previous group's
    // run — warn before dropping them along with the switch.
    const failed = unfinishedNames().length;
    if (failed) {
      const ok = await uiConfirm({
        title: t("実行結果のクリア"),
        message: t("グループを切り替えると、前回の実行結果と再実行ボタン（失敗・中止 {n}台）は消えます。<br>切り替えますか？", { n: failed }),
        okLabel: t("切り替える"),
        danger: true,
      });
      if (!ok) { runGrp.value = runGroupSel; return; }
    }
    for (const k in runState) delete runState[k];
    // Each device keeps its own チェック state — switching groups no longer
    // rewrites Enabled flags, so selections survive group changes.
    runGroupSel = runGrp.value;
    // 踏み台を使わない is for the group it was chosen for: another group
    // starts from its devices' own settings again.
    runDirect = false;
    renderRun(); // the heading names the group: no toast on top
  };
  // 実行対象 toggles (Shift+クリック=範囲一括、ヘッダ=全選択/全解除)。
  // Optimistic: the UI flips instantly, the vault write happens behind it.
  const applyEnable = async (ids, on) => {
    const set = new Set(ids);
    (INV.devices || []).forEach(d => { if (set.has(d.id)) d.enabled = on; });
    renderRun();
    try { await App().SetAllDevicesEnabled(on, ids); }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); await refreshInventory(); }
  };
  const runChkAll = document.getElementById("run-chk-all");
  if (runChkAll) runChkAll.onclick = () => applyEnable(visible.map(d => d.id), runChkAll.checked);
  wireShiftRange("run:" + runGroupSel, [...root.querySelectorAll("[data-enable]")], applyEnable);
  root.querySelectorAll("[data-log]").forEach(el => el.onclick = () => showLog(el.dataset.log));
  root.querySelectorAll("[data-output]").forEach(el => el.onclick = () => showRunOutput(el.dataset.output));
  root.querySelectorAll("[data-msg]").forEach(el => el.onclick = () => showRunMessage(el.dataset.msg));
  root.querySelectorAll("[data-stop]").forEach(el => el.onclick = () => stopDevice(el.dataset.stop));
  root.querySelectorAll("[data-watch]").forEach(el => el.onclick = () => App().OpenWatchWindow(el.dataset.watch).catch(e => toast(t("画面を開けません") + ": " + terr(e), "err")));
  wireConnectButtons(root, () => runDirect);
  updateCounts();
}

// Backend messages that flow through untranslated get their known phrases
// mapped here (terr also converts embedded security-error fragments).
// The message cell: the warnings of a run that finished with refused
// commands come first (the log path is behind ログ表示 anyway).
function runMsg(st) {
  if (st.warnings && st.warnings.length) return "⚠ " + st.warnings.join(" ／ ");
  return fmtRunMsg(st.message);
}
// The message as the table shows it: a finished device's log path (the
// whole absolute path took the column; ログ表示 is right beside it) becomes
// the file name; everything else as it is, the cell wrapping to two lines.
function runMsgShort(st) {
  const m = runMsg(st);
  if (st.phase === "done" && st.logPath && m === st.logPath) return m.split(/[\\/]/).pop();
  return m;
}
// The whole message of a row, in a dialog: a hint-laden error runs to two
// or three sentences, and hovering 300 rows one by one does not scale.
function showRunMessage(name) {
  const st = runState[name];
  if (!st) return;
  const node = h(`<div>
    <h3>${esc(name)} <span class="muted" style="font-size:13px;font-weight:400">/ ${esc(statusLabel(st.phase))}</span></h3>
    <div style="font-size:14px;line-height:1.8;white-space:pre-wrap;word-break:break-word;max-height:60vh;overflow:auto">${esc(runMsg(st))}</div>
    <div class="modal-actions"><button class="btn" id="rm-close" autofocus>${esc(t("閉じる"))}</button><button class="btn primary" id="rm-copy">${esc(t("コピー"))}</button></div>
  </div>`);
  openModal(node, "mid");
  node.querySelector("#rm-close").onclick = closeModal;
  node.querySelector("#rm-copy").onclick = () => copyText(runMsg(st), t("コピーしました"));
}
// What the device showed during a failed 接続確認のみ (no log is written
// for a dry run, so this is the only place to read the hop's banner or the
// prompt the login stopped at).
function showRunOutput(name) {
  const st = runState[name];
  if (!st || !st.output) return;
  const node = h(`<div>
    <h3>${esc(t("接続確認の出力"))} <span class="muted" style="font-size:13px;font-weight:400">/ ${esc(name)}</span></h3>
    <div class="muted" style="font-size:12px;margin:-4px 0 8px">${esc(t("機器（踏み台）が表示した内容の末尾です。パスワードは伏せています。接続確認はログを残さないため、この画面を閉じると消えます"))}</div>
    <pre class="log-view" style="max-height:55vh">${esc(st.output)}</pre>
    <div class="modal-actions"><button class="btn" id="ro-close" autofocus>${esc(t("閉じる"))}</button><button class="btn primary" id="ro-copy">${esc(t("コピー"))}</button></div>
  </div>`);
  openModal(node, "wide");
  node.querySelector("#ro-close").onclick = closeModal;
  node.querySelector("#ro-copy").onclick = () => copyText(st.output, t("コピーしました"));
}

// 今回だけ入力: commands typed for one run, one per line (1-second pause).
// resolve(true) when entered, false when cancelled.
function adHocDialog() {
  return new Promise(resolve => {
    const node = h(`<div>
      <h3>${esc(t("今回だけ実行するコマンド"))}</h3>
      <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("1 行に 1 コマンド。対象の全機器に、機器ごとのコマンドセットの代わりにこのコマンドを送ります（待機は各 1 秒・保存はされません）。"))}</p>
      <div class="field"><textarea id="ah-text" style="min-height:200px" placeholder="show clock&#10;show ip interface brief">${esc(runAdHoc)}</textarea></div>
      <div class="modal-actions">
        <button class="btn" id="ah-cancel">${esc(t("キャンセル"))}</button>
        <button class="btn primary" id="ah-ok">${esc(t("決定"))}</button>
      </div>
    </div>`);
    openModal(node, "mid");
    const ta = node.querySelector("#ah-text");
    ta.focus();
    guardDiscard(node, "#ah-cancel", null, () => resolve(false));
    node.querySelector("#ah-ok").onclick = () => {
      const prev = runAdHoc;
      runAdHoc = ta.value;
      if (!runAdHocCommands().length) { runAdHoc = prev; toast(t("コマンドを入力してください"), "err"); return; }
      closeModal(); resolve(true);
    };
  });
}

// The developer-shaped fragments the Go side composes ("expect timeout",
// "login: waiting for …") read in Japanese on the Japanese UI; terr handles
// the English one.
const RUN_MSG_JA = [
  [/login: waiting for prompt: expect timeout/g, "ログイン後のプロンプトを待ちましたが、時間内に出ませんでした"],
  [/login: waiting for "([^"]*)": expect timeout/g, "ログイン中に「$1」を待ちましたが、時間内に出ませんでした"],
  [/^login: /g, "ログイン: "],
  [/command "([^"]*)": expect timeout/g, "コマンド「$1」の応答待ちがタイムアウトしました"],
  [/^command "([^"]*)": /g, "コマンド「$1」: "],
  [/expect timeout/g, "応答待ちタイムアウト"],
  [/\bsend: /g, "送信エラー: "],
];
function fmtRunMsg(m) {
  let s = terr(String(m || "")).replace(/context canceled/g, t("中止しました"));
  if (LANG === "ja") for (const [re, rep] of RUN_MSG_JA) s = s.replace(re, rep);
  return s;
}

function statusLabel(p) {
  return ({ queued: t("順番待ち"), off: t("対象外"), connecting: t("接続中"), login: t("ログイン中"),
    running: t("実行中"), saving: t("保存中"), done: t("完了"), error: t("エラー"), canceled: t("中止") }[p]) || p;
}

// The run's 作業タイミング, shown large and colour-coded in the run
// confirmation: the comparison picks its A/B by it, so a run tagged wrong
// (or not at all) is caught before it starts, not in the history tab.
function stageBanner(st) {
  if (!st) return `<span class="stage-banner stage-none"><span class="sb-label">${esc(t("作業タイミング"))}</span><span class="sb-value">${esc(t("指定なし"))}</span>
    <span class="sb-note">${esc(t("作業前後の比較で自動では選ばれません。必要ならキャンセルして、実行タブの「作業タイミング」を選んでください"))}</span></span>`;
  return `<span class="stage-banner stage-${esc(st === "work" ? "during" : st)}"><span class="sb-label">${esc(t("作業タイミング"))}</span><span class="sb-value">${esc(stageLabel(st))}</span></span>`;
}
function stageRunLabel(st) { return st ? t("▶ {s}として実行", { s: stageLabel(st) }) : t("▶ 実行"); }

async function startRun(dryRun) {
  // Only the checked devices of the selected group run (RunSelected), so
  // checkbox states in other groups are never touched.
  const targets = runTargetDevices().map(d => d.name);
  if (!targets.length) return;
  const scope = (runGroupSel ? t("グループ「<b>{g}</b>」の ", { g: esc(runGroupLabel()) }) : "") + (runFilterActive() ? t("（絞り込み表示中の）") : "");
  // Rows of a run already on the table whose every target failed or was
  // cut short (the 状態: エラー filter, then ▶): the rows that finished
  // stay, as they do for ↻ — the run itself uses the toolbar as it is.
  const keepRows = runFilterActive() && Object.keys(runState).length > 0
    && targets.every(n => ["error", "canceled"].includes((runState[n] || {}).phase));
  if (dryRun) {
    // Nothing is sent and nothing is written: no question unless the run
    // is narrowed (a filter) or routed differently (踏み台を使わない).
    if (!runFilterActive() && !runDirect) { doRun(targets, true, false, keepRows); return; }
    const ok = await uiConfirm({
      title: t("接続確認のみ"),
      message: t("{s}<b>{n} 台</b>に接続してログインまで確認します。<br>コマンドは送らず、ログも残しません。よろしいですか？", { s: scope, n: targets.length }) + directNote(runDirect, targets),
      okLabel: t("⚡ 接続確認"),
    });
    if (!ok) return;
    doRun(targets, true, false, keepRows);
    return;
  }
  // Devices that would run no commands at all (no set assigned, none chosen
  // for this run) only log in and show 完了 — said before the run, not
  // discovered in an empty log afterwards.
  const req0 = runRequest(targets, false);
  const noSet = targets.filter(n => reqCommandCount(req0, runDev(n)) === 0);
  // A set that types a password into a command line ({password} /
  // {enable}) sends it wherever that line points: named here, with the
  // note that such a run is logged masked whatever the setting says.
  const secretNote = secretCommandNote(req0, targets);
  // A command set that changes the device (conf t / write / reload …) gets
  // its own warning with an explicit acknowledgement before anything runs.
  const warnings = await App().CheckDangerousCommands(runRequest(targets, false)).catch(() => []);
  if (warnings && warnings.length) {
    if (!(await dangerConfirm(warnings, targets.length, scope, runStage, secretNote))) return;
  } else {
    const stage = (runSetSel ? `<br><span style="font-size:12px;color:var(--accent)">${esc(t("コマンド（今回のみ）: {s}", { s: runSetSel === RUN_ADHOC ? runAdHocCommands().map(c => c.text).join(" / ") : runSetSel }))}</span>` : "")
      + (noSet.length ? `<br><span style="font-size:12px;color:var(--warn)">${esc(t("⚠ コマンドセット未割り当ての機器 {n} 台はログインのみで終わります（ログはログイン画面だけ）: {list}", { n: noSet.length, list: noSet.slice(0, 10).join(", ") + (noSet.length > 10 ? " …" : "") }))}</span>` : "")
      + secretNote;
    const ok = await uiConfirm({
      title: t("一括実行"),
      message: stageBanner(runStage) + t("{s}<b>{n} 台</b>に一括実行します。よろしいですか？", { s: scope, n: targets.length }) + stage + directNote(runDirect, targets),
      okLabel: stageRunLabel(runStage),
    });
    if (!ok) return;
  }
  doRun(targets, false, false, keepRows);
}

// The lines of the targets' command sets (or the typed commands) that
// expand a password, as a warning for the confirmation ("" when none).
function secretCommandNote(req, targets) {
  const hit = s => /\{password\}|\{enable\}/.test(s);
  const lines = [];
  if (req.adHoc && req.adHoc.length) {
    req.adHoc.filter(c => hit(c.text)).forEach(c => lines.push(t("（今回のみ入力したコマンド）") + ": " + c.text));
  } else {
    const names = [...new Set(targets.map(n => { const d = runDev(n); return req.commandSet || (d && d.commandSet) || ""; }).filter(Boolean))];
    names.forEach(nm => {
      const cs = (INV.commandSets || []).find(s => s.name === nm);
      (cs ? cs.commands || [] : []).filter(c => hit(c.text)).forEach(c => lines.push(nm + ": " + c.text));
    });
  }
  if (!lines.length) return "";
  return `<br><span style="font-size:12px;color:var(--warn)">${esc(t("🔒 パスワードを送るコマンド（{password} / {enable}）があります。この実行のログはパスワードを伏せて保存します: {list}", { list: lines.join(" ／ ") }))}</span>`;
}

// The warning shown before a batch whose command sets contain
// configuration-changing commands: every flagged command per device, and an
// acknowledgement checkbox that gates the run button.
function dangerConfirm(warnings, n, scope, stage, extraNote) {
  return new Promise(resolve => {
    const bySet = {};
    warnings.forEach(w => { (bySet[w.commandSet] = bySet[w.commandSet] || { hits: w.hits, devices: [] }).devices.push(w.device); });
    const blocks = Object.keys(bySet).map(set => `
      <div class="warn-box">
        <div><b>${esc(t("コマンドセット「{s}」", { s: set === "（今回のみ入力したコマンド）" ? t(set) : set }))}</b> <span class="muted">${esc(t("— 対象 {n} 台: {d}", { n: bySet[set].devices.length, d: bySet[set].devices.slice(0, 10).join(", ") + (bySet[set].devices.length > 10 ? t(" ほか {n} 台", { n: bySet[set].devices.length - 10 }) : "") }))}</span></div>
        <table style="margin-top:6px"><tbody>
          ${bySet[set].hits.map(h => `<tr><td class="muted" style="padding:3px 8px 3px 0;width:36px">#${h.index + 1}</td><td class="mono" style="padding:3px 8px 3px 0">${esc(h.text)}</td><td class="muted" style="padding:3px 0;color:var(--warn)">${esc(t(h.reason))}</td></tr>`).join("")}
        </tbody></table>
      </div>`).join("");
    const node = h(`<div>
      <h3 style="color:var(--warn)">${esc(t("⚠ 設定を変更するコマンドが含まれています"))}</h3>
      ${stageBanner(stage || "")}
      <p style="font-size:14px;line-height:1.8;margin:4px 0 10px">${t("{s}<b>{n} 台</b>に一括実行します。次のコマンドは機器の設定や状態を<b>変更</b>します。実行前に内容を確認してください。", { s: scope, n })}</p>
      ${blocks}
      ${extraNote ? `<p style="font-size:14px;line-height:1.8;margin:4px 0 0">${extraNote}</p>` : ""}
      <label style="display:flex;align-items:center;gap:8px;cursor:pointer;margin-top:10px"><input type="checkbox" id="dg-ack" style="width:auto"> ${esc(t("変更を伴うことを理解したうえで実行します"))}</label>
      <div class="modal-actions">
        <button class="btn" id="dg-cancel">${esc(t("キャンセル"))}</button>
        <button class="btn danger" id="dg-ok" disabled>${esc(stage ? t("▶ {s}として変更を実行", { s: stageLabel(stage) }) : t("▶ 変更を実行"))}</button>
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
// The folder of the last finished batch (its logs together), for the
// 📂 button: handing over a run's logs used to mean opening the log root
// and finding the right date folder.
let lastRunDir = "";
// The request of the last full run. A re-run (失敗・中断のみ / 鍵を許可して
// 再実行) repeats its choices — group, stage, command set, typed commands —
// not whatever the toolbar shows by then: switching the set to a change set
// and pressing ↻ used to send that set, unwarned.
let lastRunReq = null;
function reqCommandCount(req, d) {
  if (req.adHoc && req.adHoc.length) return req.adHoc.length;
  const cs = (INV.commandSets || []).find(s => s.name === (req.commandSet || (d && d.commandSet)));
  return cs ? (cs.commands || []).length : 0;
}
// What a re-run sends: the last run's command choice and stage (not what
// the toolbar shows now), spelled out in its confirmation.
function rerunDetail() {
  const r = lastRunReq || {};
  if (lastRunDry) return `<br><span class="muted" style="font-size:12px">${esc(t("前回と同じく接続確認のみです（コマンドは送りません）"))}</span>` + directNote(r.direct);
  const cmds = r.adHoc && r.adHoc.length ? t("今回だけ入力: {s}", { s: r.adHoc.map(c => c.text).join(" / ") })
    : r.commandSet ? t("{s}（今回のみ）", { s: r.commandSet }) : t("機器ごとの設定どおり");
  return `<br><span class="muted" style="font-size:12px">${esc(t("前回と同じ内容で実行します — コマンド: {c}", { c: cmds }))}${r.stage ? esc(" ／ " + t("作業タイミング: {s}", { s: stageLabel(r.stage) })) : ""}</span>` + directNote(r.direct);
}

// A re-run passes the same change warning a first run does.
async function confirmRerun(names) {
  const req = { ...(lastRunReq || runRequest(names, lastRunDry)), names };
  if (lastRunDry) return null; // 接続確認のみ: nothing is sent
  const warnings = await App().CheckDangerousCommands(req).catch(() => []);
  if (warnings && warnings.length) {
    const scope = runGroupSel ? t("グループ「<b>{g}</b>」の ", { g: esc(runGroupLabel()) }) : "";
    return dangerConfirm(warnings, names.length, scope, req.stage);
  }
  return null; // nothing to warn about: the caller asks its own question
}

async function doRun(names, dryRun, partial, keepRows) {
  // A full run starts from a clean table; a partial re-run (失敗・中断のみ)
  // resets only its own rows, so the devices that already finished keep
  // showing 完了 and their ログ表示 link. keepRows does the same for a
  // full run aimed at the failed rows (the 状態 filter, then ▶).
  if (!partial && !keepRows) for (const k in runState) delete runState[k];
  const targets = names;
  const req = partial && lastRunReq ? { ...lastRunReq, names: targets, dryRun: !!dryRun } : runRequest(targets, dryRun);
  if (!partial) lastRunReq = req;
  lastRunDry = !!dryRun;
  targets.forEach(n => runState[n] = { phase: "queued" });
  cancelling = false;
  // For the overall ETA: how many commands each target will run.
  runTargets = targets.slice();
  runTotals = {};
  targets.forEach(n => {
    runTotals[n] = reqCommandCount(req, runDev(n));
  });
  runStartMs = Date.now();
  clearInterval(etaTimer);
  etaTimer = setInterval(updateEta, 1000);
  running = true;
  runPower = null;
  renderRun();
  try { await App().RunWith(req); }
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
  const d = runDev(name);
  const cs = d && runSetOf(d);
  if (!cs) return [];
  return (cs.commands || []).map(c => cs.pauseOff ? 0 : d.conn === "serial" ? (c.serialSec || 0) : (c.pauseSec || 0));
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
  const danger = await confirmRerun(names);
  if (danger === false) return;
  const ok = danger === true || await uiConfirm({
    title: t("失敗・中止のみ再実行"),
    message: t("失敗・中止した <b>{n} 台</b>のみ再実行します（完了済みはそのまま）。よろしいですか？", { n: names.length }) + rerunDetail(),
    okLabel: t("↻ 再実行"),
  });
  if (!ok) return;
  doRun(names, lastRunDry, true);
}

function updateRunRow(name) {
  const body = document.getElementById("run-body");
  if (!body) return;
  const d = runDev(name);
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

// 中止 on a row: this device only, the rest of the batch goes on. A device
// in the middle of a command is cut off (its log so far is kept); one still
// waiting is not started. Either can be re-run with 失敗・中断のみ再実行.
async function stopDevice(name) {
  const st = runState[name] || {};
  const ok = await uiConfirm({
    title: t("この機器だけ中止"),
    message: st.phase === "queued"
      ? t("<b>{n}</b> は開始しません。ほかの機器はそのまま続けます。中止しますか？", { n: esc(name) })
      : t("<b>{n}</b> の実行を打ち切ります（送信中のコマンドは途中で切れます。ここまでのログは残ります）。ほかの機器はそのまま続けます。中止しますか？", { n: esc(name) }),
    okLabel: t("中止する"),
    danger: true,
  });
  if (!ok || !running) return;
  const cur = runState[name];
  if (!cur || ["done", "error", "canceled"].includes(cur.phase)) return;
  cur.stopping = true;
  renderRun();
  if (!(await App().CancelDevice(name).catch(() => false))) { cur.stopping = false; renderRun(); }
}

// The failed devices as a CSV table (export\run-results), to sort them out
// in Excel: in the group's order, with the 状態 and メッセージ as shown.
async function exportRunFailures() {
  const inGroup = runGroupDevices().map(d => d.name);
  const names = [...inGroup, ...Object.keys(runState).filter(n => !inGroup.includes(n))]
    .filter(n => runState[n] && runState[n].phase === "error");
  const rows = names.map(n => ({ device: n, status: statusLabel("error"), message: runMsg(runState[n]) }));
  try {
    const path = await App().ExportRunFailures(runGroupName(), lastRunDry, rows);
    if (await uiConfirm({ title: t("書き出しました"), message: `<span class="muted" style="font-size:12px">${esc(path)}</span>`, okLabel: t("フォルダを開く"), cancelLabel: t("閉じる") })) {
      try { await App().OpenExportFolder(path.slice(0, Math.max(path.lastIndexOf("\\"), path.lastIndexOf("/")))); } catch (e) { toast(terr(e), "err"); }
    }
  } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
}

// ログ表示 opens the file in its own window (PalaTerm.exe --view), so it can
// sit beside the run table or another log instead of covering the app.
async function showLog(path) {
  try { await App().OpenLogWindow(path); }
  catch (e) { toast(t("ログを開けません") + ": " + terr(e), "err"); }
}

// live events from the backend
let runRenderPending = false;
// A click is a press and a release on the same element. During a busy run the
// table is rebuilt every frame, so the 画面 / ログ表示 / 中止 under the
// pointer was a new element by the release and the click never came. While a
// button is held on the run tab (since runHeld, ms) redraws wait, and the
// release brings them up to date — after the click has gone through. The
// 5-second cap covers a release that never arrives.
let runHeld = 0, runHeldRender = false;
document.addEventListener("mousedown", e => { if (e.target.closest && e.target.closest("#tab-run")) runHeld = Date.now(); }, true);
window.addEventListener("mouseup", () => {
  if (!runHeld) return;
  runHeld = 0;
  if (!runHeldRender) return;
  runHeldRender = false;
  setTimeout(() => {
    const active = document.querySelector(".tab.active");
    if (active && active.id === "tab-run" && !imeComposing) renderRun();
  }, 0);
}, true);
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
      // The log is written while the device runs, so ログ表示 is there from
      // the connection on; the last event says which file was kept (none
      // when nothing was read).
      logPath: ["done", "error", "canceled"].includes(ev.phase) ? (ev.logPath || "") : (ev.logPath || prev.logPath),
      // 中止 pressed on this row, until the device's last event.
      stopping: !["done", "error", "canceled"].includes(ev.phase) && !!prev.stopping };
    // Hundreds of devices send events in bursts: one re-render per frame.
    if (runRenderPending) return;
    runRenderPending = true;
    requestAnimationFrame(() => {
      runRenderPending = false;
      // Not under an IME composition in the filter box: the commit re-renders.
      if (imeComposing) return;
      // Not while a mouse button is held on the tab (see runHeld).
      if (runHeld && Date.now() - runHeld < 5000) { runHeldRender = true; return; }
      const active = document.querySelector(".tab.active");
      if (active && active.id === "tab-run") renderRun();
    });
  });
  rt().EventsOn("run:done", applyRunDone);
  // A bound method stopped on an internal error (recovered in the backend,
  // logged in data\error.log): what it was doing may not have happened.
  rt().EventsOn("app:internal-error", msg => toast(terr(msg) + " " + t("（操作は完了していない可能性があります）"), "err"));
}

// The end of a batch: the run:done event, or - for a page reloaded while the
// batch ran (ロック) and finished meanwhile - the results GetRunStatus kept.
function applyRunDone(payload) {
  // Once per batch: after an unlock the results may come both from
  // GetRunStatus and from the run:done event right behind it.
  if (!running) return;
  running = false;
  clearInterval(etaTimer); etaTimer = 0;
  // Payload: {results, runDir, dryRun} (a bare array from older builds).
  const results = Array.isArray(payload) ? payload : (payload && payload.results) || [];
  const runDir = (payload && payload.runDir) || "";
  if (runDir) lastRunDir = runDir; // 📂 この実行のフォルダ
  const wasDry = !!(payload && payload.dryRun);
  (results || []).forEach(r => {
    const prev = runState[r.device] || {};
    runState[r.device] = {
      phase: r.success ? "done" : (r.canceled ? "canceled" : "error"),
      message: r.success ? (r.logPath || (wasDry ? t("接続・ログイン OK（コマンドは送っていません）") : "")) : r.error,
      logPath: r.logPath,
      output: r.output || "",
      done: r.success ? prev.total : prev.done,
      total: prev.total,
      startedAt: prev.startedAt, connectAt: prev.connectAt, finishedAt: prev.finishedAt || Date.now(),
      warnings: r.warnings || [],
    };
  });
  // The batch stopped on an internal error (logged in data\error.log):
  // devices still shown as running did not finish.
  const internalErr = payload && payload.error ? terr(payload.error) : "";
  if (internalErr) {
    Object.keys(runState).forEach(k => {
      const s = runState[k];
      if (s && s.phase !== "done" && s.phase !== "error" && s.phase !== "canceled") runState[k] = { ...s, phase: "error", message: internalErr };
    });
  }
  const wasCancel = cancelling;
  cancelling = false;
  const active = document.querySelector(".tab.active");
  if (active && active.id === "tab-run") renderRun();
  // Clear the "実行中" notice on the settings tab (unless mid-edit).
  else if (active && active.id === "tab-settings" && !settingsDirty && !myLoginsDirty) renderSettings();
  if (internalErr) {
    toast(t("一括実行が内部エラーで止まりました") + ": " + internalErr, "err");
    return;
  }
  if (wasCancel) {
    const v = Object.values(runState);
    toast(t("中止しました（完了 {a}・失敗 {b}・中止/未実行 {c}）", {
      a: v.filter(s => s.phase === "done").length, b: v.filter(s => s.phase === "error").length, c: v.filter(s => s.phase === "canceled").length }), "ok");
  } else if (wasDry) {
    const v = Object.values(runState);
    toast(t("接続確認が完了しました（OK {a}・失敗 {b}）", { a: v.filter(s => s.phase === "done").length, b: v.filter(s => s.phase === "error").length }), "ok");
  } else if (runStage === "after" && runDir) {
    toast(t("実行が完了しました。実行履歴・比較タブで作業前のログと比較できます"), "ok");
  } else toast(t("実行が完了しました"), "ok");
  // Refused commands are easy to miss in a long table: say so once more.
  const warned = Object.values(runState).filter(s => s.warnings && s.warnings.length).length;
  if (warned && !wasDry) setTimeout(() => toast(t("⚠ {n} 台で機器に拒否されたコマンドがあります（状態列の「警告」）", { n: warned }), "err"), 1500);
}

// ---- Log Settings tab ----

// True while the settings form has edits that were not saved yet.
let settingsDirty = false;
// The 自分の踏み台認証 panel is saved on its own (SaveBastionLogins).
let myLoginsDirty = false;

// Gate for leaving the settings tab (called by switchTab / lock in app.js):
// warns before unsaved edits are silently lost.
async function canLeaveSettings(nextTab) {
  const active = document.querySelector(".nav-btn.active");
  if (!active || active.dataset.tab !== "settings" || nextTab === "settings" || !(settingsDirty || myLoginsDirty)) return true;
  // The button says what happens next: leave the tab, lock, or switch the language.
  const ok = await uiConfirm({
    title: t("未保存の変更"),
    message: nextTab === "lock" ? t("設定に保存されていない変更があります。<br>保存せずにロックしますか？")
      : nextTab === "lang" ? t("設定に保存されていない変更があります。<br>保存せずに言語を切り替えますか？")
      : t("設定に保存されていない変更があります。<br>保存せずに移動しますか？"),
    okLabel: nextTab === "lock" ? t("破棄してロック") : nextTab === "lang" ? t("破棄して切り替える") : t("破棄して移動"),
    danger: true,
  });
  if (ok) settingsDirty = myLoginsDirty = false;
  return ok;
}

function renderSettings() {
  const root = document.getElementById("tab-settings");
  const s = INV.settings || {};
  root.innerHTML = `
    <div class="page-head"><div><div class="page-title">${esc(t("設定"))}</div>
      <div class="page-sub">${esc(t("一括実行・ログ保存・自動ロックの共通設定"))}</div></div></div>
    <div class="panel" style="padding:22px;max-width:620px">
      ${running ? `<div class="info-box" style="margin:0 0 16px">${esc(t("▶ 一括実行中です。実行中のバッチは開始時点の設定で動くため、ここでの変更は次回の実行から反映されます"))}</div>` : ""}
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
          <input id="set-dtmpl" value="${esc(s.logDirTemplate || "{date}_{time}_{group}_{stage}")}"></div>
        <div class="field"><label>${esc(t("ログファイル名テンプレート"))}</label>
          <input id="set-tmpl" value="${esc(s.logNameTemplate || "{host}_{site}_{role}_{stage}_{date}_{time}.txt")}"></div>
      </div>
      <div class="field"><label><span data-tip="${esc(t("作業タイミングごとに {stage} へ入る文字列です。フォルダ名・ファイル名に付きます。空欄なら既定（before / work / after / reserve / Current）。改名しても、以前の既定語で作られたフォルダは実行履歴・比較タブでそのまま判別されます"))}">${esc(t("ログ名に付く作業タイミングの語 ⓘ"))}</span></label>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:8px 16px">
          <div class="row-inline" style="gap:6px"><span class="muted" style="font-size:12px;white-space:nowrap">${esc(t("作業前（before）"))}</span><input id="set-st-before" style="flex:1" placeholder="before" value="${esc(s.stageTokenBefore || "before")}"></div>
          <div class="row-inline" style="gap:6px"><span class="muted" style="font-size:12px;white-space:nowrap">${esc(t("作業中（work）"))}</span><input id="set-st-work" style="flex:1" placeholder="work" value="${esc(s.stageTokenWork || "work")}"></div>
          <div class="row-inline" style="gap:6px"><span class="muted" style="font-size:12px;white-space:nowrap">${esc(t("作業後（after）"))}</span><input id="set-st-after" style="flex:1" placeholder="after" value="${esc(s.stageTokenAfter || "after")}"></div>
          <div class="row-inline" style="gap:6px"><span class="muted" style="font-size:12px;white-space:nowrap" data-tip="${esc(t("予約のログフォルダ名と、作業タイミングを指定していない予約のログファイル名に付きます"))}">${esc(t("予約（reserve）"))}</span><input id="set-st-sched" style="flex:1" placeholder="reserve" value="${esc(s.stageTokenSchedule || "reserve")}"></div>
          <div class="row-inline" style="gap:6px"><span class="muted" style="font-size:12px;white-space:nowrap" data-tip="${esc(t("作業タイミングを「指定なし」で実行したときのログファイル名に付きます（フォルダ名には付きません）"))}">${esc(t("指定なし（Current）"))}</span><input id="set-st-none" style="flex:1" placeholder="Current" value="${esc(s.stageTokenNone || "Current")}"></div>
        </div></div>
      <div class="grid-2">
        <div class="field"><label><span data-tip="${esc(t("マウス・キーボード操作がこの時間なければ自動でロックします（一括実行中も。実行は続き、解除すると表に戻ります）。0 で無効"))}">${esc(t("アイドル時の自動ロック（分・0=無効） ⓘ"))}</span></label>
          <input id="set-lock" type="number" min="0" value="${s.autoLockOff ? 0 : (s.autoLockMin > 0 ? s.autoLockMin : 30)}"></div>
        <div class="field"><label><span data-tip="${esc(t("オンにすると、機器のパスワード・enable パスワード・踏み台のパスワードがログ内に現れた箇所を **** に置き換えて保存します。show running-config 内のユーザー名などに同じ文字列があるとそこも置き換わり、ログが機器の設定と一致しなくなるため既定はオフです。機器は入力したパスワードをエコーしないので、オフでも入力値はログに残りません。コマンドセットの行に {password} / {enable} を書いた場合だけは機器がその行をエコーするため、オフでもそのログはマスクされます"))}">${esc(t("ログ内のパスワードをマスクする ⓘ"))}</span></label>
          <label class="row-inline" style="gap:8px;cursor:pointer;font-weight:normal"><input id="set-mask" type="checkbox" ${s.maskLogSecrets ? "checked" : ""}> ${esc(t("パスワードを **** に置き換えて保存（既定: オフ）"))}</label></div>
      </div>
      <div class="field"><label><span data-tip="${esc(t("端末窓で右クリックしたときの動作です（Tera Term の［その他の設定］-［コピーと貼り付け］と同じ 2 項目）。どちらもオフなら、右クリックですぐに貼り付けます（既定）。「無効にする」がオンなら、もう一方は無視されます。変更は、このあと開く対話窓から反映されます"))}">${esc(t("対話窓の右クリック ⓘ"))}</span></label>
        <label class="row-inline" style="gap:8px;cursor:pointer;font-weight:normal"><input id="set-rc-off" type="checkbox" ${s.rightClickPasteOff ? "checked" : ""}> ${esc(t("マウス右ボタンでの貼り付けを無効にする"))}</label>
        <label class="row-inline" style="gap:8px;cursor:pointer;font-weight:normal;margin-top:4px"><input id="set-rc-confirm" type="checkbox" ${s.rightClickPasteConfirm ? "checked" : ""}> ${esc(t("マウス右ボタンでの貼り付けを確認する（メニューの「貼り付け」で貼り付け）"))}</label></div>
      <div class="field"><label><span data-tip="${esc(t("機器一覧・実行タブの「並べて接続」で開いた窓を、最初から同時入力オンにするかどうかです。オフ（既定）なら、同時入力は各窓の「同時入力」ボタンで必要な窓だけオンにします。変更は、このあと開く窓から反映されます"))}">${esc(t("並べて接続の同時入力 ⓘ"))}</span></label>
        <label class="row-inline" style="gap:8px;cursor:pointer;font-weight:normal"><input id="set-tile-bcast" type="checkbox" ${s.tiledBroadcast ? "checked" : ""}> ${esc(t("並べて接続した窓を同時入力オンで開く（既定: オフ）"))}</label></div>
      <div class="muted" style="font-size:12px;margin-top:2px">${esc(t("テンプレートに使える変数（クリックでコピー。フォルダ名は {date} {time} {hhmm} {group} {stage} のみ）:"))}</div>
      <table class="ph-table"><tbody>
        <tr><td class="mono">{host}</td><td>${esc(t("機器名"))}</td><td class="mono">{date}</td><td>${esc(t("日付（yyyymmdd）"))}</td></tr>
        <tr><td class="mono">{ip}</td><td>${esc(t("IPアドレス"))}</td><td class="mono">{time}</td><td>${esc(t("時刻（hhmmss）"))}</td></tr>
        <tr><td class="mono">{os}</td><td>${esc(t("OS種別"))}</td><td class="mono">{hhmm}</td><td>${esc(t("時刻（hhmm）"))}</td></tr>
        <tr><td class="mono">{group}</td><td>${esc(t("グループ名"))}</td><td class="mono">{site}</td><td>${esc(t("拠点名"))}</td></tr>
        <tr><td class="mono">{role}</td><td colspan="3">${esc(t("役割（機器編集の「役割（任意）」。空なら詰められます）"))}</td></tr>
        <tr><td class="mono">{stage}</td><td colspan="3">${esc(t("作業タイミングの付与文字列（既定: 作業前=before・作業中と対話接続=work・作業後=after・予約=reserve・指定なし=Current。上の欄で変更可。予約で指定なしのときは reserve）。テンプレートに無いときはファイル名の末尾に自動で付きます"))}</td></tr>
      </tbody></table>
      <div class="muted" style="font-size:12px;margin-top:6px">${esc(t("※ログ保存フォルダに相対パス（例: logs）を指定した場合、PalaTerm.exe と同じフォルダが基準になります"))}</div>
      <div class="modal-actions"><button class="btn primary" id="set-save" data-tip="Ctrl+Enter">${esc(t("保存"))}</button></div>
    </div>
    <div class="panel" style="padding:22px;max-width:620px;margin-top:16px" id="my-bastion">
      <div style="font-weight:600;margin-bottom:4px">${esc(t("自分の踏み台"))}</div>
      <div class="muted" style="font-size:12px;margin-bottom:10px">${esc(t("社内の踏み台など、人ごとに秘密鍵が違う踏み台を登録します（最大5段・外側＝手前から順に）。機器の編集で「自分の踏み台（設定タブ）を先に通る」にチェックした機器は、ここの踏み台を順に通ってから、機器ごとの踏み台・機器へ進みます。この PC の vault にだけ保存され、一式書き出し・CSV には入りません（機器には「使う」ことだけが保存されます）。"))}</div>
      <div id="mb-rows"></div>
      <button class="btn sm" id="mb-add" type="button" style="margin-top:6px">${esc(t("＋ 踏み台を追加"))}</button>
      <div class="modal-actions"><button class="btn primary" id="mb-save" data-tip="Ctrl+Enter">${esc(t("保存"))}</button></div>
    </div>
    ${screenColorsPanelHTML()}
    <div class="panel" style="padding:22px;max-width:620px;margin-top:16px;border-color:var(--err)">
      <div style="font-weight:600;margin-bottom:4px;color:var(--err)">${esc(t("危険な操作"))}</div>
      <div class="muted" style="font-size:12px;margin-bottom:10px">${esc(t("すべての機器・グループ・コマンドセット・OSタイププロファイル・マスターパスワードを削除して、はじめての起動の状態に戻します（ログと export フォルダは残ります）。マスターパスワードの入力と 2 回の確認があります。"))}</div>
      <button class="btn danger" id="set-reset" type="button">${esc(t("すべてのデータをリセット（初期化）"))}</button>
    </div>`;
  renderMyBastion();
  wireScreenColorsPanel();
  document.getElementById("set-reset").onclick = resetVaultFlow;

  // Track unsaved edits so tab switches can warn (see canLeaveSettings).
  settingsDirty = myLoginsDirty = false;
  ["set-par", "set-ct", "set-cmt", "set-dir", "set-tmpl", "set-dtmpl", "set-lock", "set-mask", "set-rc-off", "set-rc-confirm", "set-tile-bcast", "set-st-before", "set-st-work", "set-st-after", "set-st-sched", "set-st-none"].forEach(id => {
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
  // Ctrl+Enter saves the panel being typed in, as in every editor.
  root.onkeydown = e => {
    if (e.key !== "Enter" || !(e.ctrlKey || e.metaKey) || e.isComposing || modalOpen()) return;
    e.preventDefault();
    if (e.repeat) return;
    const btn = e.target.closest && e.target.closest(".panel") && e.target.closest(".panel").querySelector(".modal-actions .btn.primary:not([disabled])");
    if (btn) btn.click();
  };
  document.getElementById("set-save").onclick = async () => {
    const out = {
      maxParallel: parseInt(document.getElementById("set-par").value, 10) || 0,
      connectTimeout: parseInt(document.getElementById("set-ct").value, 10) || 20,
      commandTimeout: parseInt(document.getElementById("set-cmt").value, 10) || 30,
      // Without the quotes Explorer's パスのコピー puts around it (the
      // backend also checks the folder can be made, and says why not).
      logDir: document.getElementById("set-dir").value.trim().replace(/^["']+|["']+$/g, "").trim() || "logs",
      logNameTemplate: document.getElementById("set-tmpl").value || "{host}_{site}_{role}_{stage}_{date}_{time}.txt",
      logDirTemplate: document.getElementById("set-dtmpl").value.trim() || "{date}_{time}_{group}_{stage}",
      // Empty = default (before / work / after); the backend validates.
      stageTokenBefore: document.getElementById("set-st-before").value.trim(),
      stageTokenWork: document.getElementById("set-st-work").value.trim(),
      stageTokenAfter: document.getElementById("set-st-after").value.trim(),
      stageTokenSchedule: document.getElementById("set-st-sched").value.trim(),
      stageTokenNone: document.getElementById("set-st-none").value.trim(),
    };
    const lockMin = parseInt(document.getElementById("set-lock").value, 10);
    out.autoLockOff = !(lockMin > 0);
    out.autoLockMin = lockMin > 0 ? lockMin : 30;
    out.maskLogSecrets = !!document.getElementById("set-mask").checked;
    out.rightClickPasteOff = !!document.getElementById("set-rc-off").checked;
    out.rightClickPasteConfirm = !!document.getElementById("set-rc-confirm").checked;
    out.tiledBroadcast = !!document.getElementById("set-tile-bcast").checked;
    document.getElementById("set-dir").value = out.logDir;
    try { await App().SaveSettings(out); settingsDirty = false; await refreshInventory(); toast(t("保存しました"), "ok"); }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  };
}

// 自分の踏み台: the user's own jump host chain (model Inventory.MyBastions,
// up to 5 hops), gone through first by every device marked 自分の踏み台を
// 使う. Saved on its own (SaveMyBastions); rows without a host are dropped.
function renderMyBastion() {
  const panel = document.getElementById("my-bastion");
  if (!panel) return;
  let rows = (INV.myBastions || []).map(x => ({ ...x }));
  const host = panel.querySelector("#mb-rows");
  const collect = () => [...host.querySelectorAll("[data-mb-row]")].map(r => ({
    host: r.querySelector(".mb-host").value.trim(),
    method: r.querySelector(".mb-method").value,
    port: parseInt(r.querySelector(".mb-port").value, 10) || 0,
    username: r.querySelector(".mb-user").value.trim(),
    authMethod: r.querySelector(".mb-auth").value,
    password: r.querySelector(".mb-pw").value,
    keyFile: r.querySelector(".mb-key").value.trim(),
    keyPassphrase: r.querySelector(".mb-keypass").value,
    legacyAlgos: r.querySelector(".mb-legacy").checked,
    jumpCommand: r.querySelector(".mb-jump").value.trim(),
  }));
  const dirty = () => { myLoginsDirty = true; };
  const draw = () => {
    host.innerHTML = rows.length ? rows.map((b, i) => {
      const ssh = (b.method || "ssh") === "ssh", key = ssh && b.authMethod !== "password";
      return `<div class="card" data-mb-row style="padding:14px 16px;margin:10px 0">
        <div class="row-inline" style="justify-content:space-between;margin-bottom:8px">
          <b>${esc(t("{n}段目の踏み台", { n: i + 1 }))}</b>
          <button class="btn sm act-del" type="button" data-mb-rm="${i}">${esc(t("削除"))}</button></div>
        <div class="grid-3">
          <div class="field" style="margin-bottom:8px"><label>${esc(t("ホスト"))} <span class="req">${esc(t("必須"))}</span></label><input class="mb-host" value="${esc(b.host || "")}"></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("接続方式"))}</label><select class="mb-method">
            <option value="ssh" ${ssh ? "selected" : ""}>SSH</option>
            <option value="telnet" ${ssh ? "" : "selected"}>Telnet</option></select></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("ポート"))}</label><input class="mb-port" type="number" value="${esc(String(b.port || (ssh ? 22 : 23)))}"></div>
        </div>
        <div class="grid-3">
          <div class="field" style="margin-bottom:8px"><label>${esc(t("ユーザー"))}${i === 0 ? ` <span class="req">${esc(t("必須"))}</span>` : ""}</label><input class="mb-user" value="${esc(b.username || "")}"></div>
          <div class="field" style="margin-bottom:8px${ssh ? "" : ";display:none"}" data-mb-ssh><label>${esc(t("SSH認証"))}</label><select class="mb-auth">
            <option value="publickey" ${key ? "selected" : ""}>${esc(t("公開鍵"))}</option>
            <option value="password" ${key ? "" : "selected"}>${esc(t("パスワード"))}</option></select></div>
          <div class="field" style="margin-bottom:8px${key ? ";display:none" : ""}" data-mb-pw><label>${esc(t("パスワード"))}</label><input class="mb-pw" type="password" value="${esc(b.password || "")}"></div>
        </div>
        <div class="grid-2" data-mb-key ${key ? "" : 'style="display:none"'}>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("秘密鍵ファイル（公開鍵認証時）"))}</label>
            <div class="row-inline" style="gap:6px"><input class="mb-key" style="flex:1" value="${esc(b.keyFile || "")}">
              <button class="btn sm mb-key-pick" type="button">${esc(t("参照…"))}</button></div></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("鍵のパスフレーズ（任意）"))}</label><input class="mb-keypass" type="password" value="${esc(b.keyPassphrase || "")}"></div>
        </div>
        <div class="field" style="margin-bottom:8px${ssh ? "" : ";display:none"}" data-mb-ssh><label style="font-weight:normal"><input type="checkbox" class="mb-legacy" style="width:auto" ${b.legacyAlgos ? "checked" : ""}> ${esc(t("この踏み台にレガシー暗号を許可"))}</label></div>
        <div class="field" style="margin-bottom:0"><label>${esc(t("次ホップへのジャンプコマンド（空欄=標準。NW機器踏み台などコマンド形式が違う場合に指定）"))}</label>
          <input class="mb-jump mono" value="${esc(b.jumpCommand || "")}" placeholder="ssh {user}@{host}"></div>
      </div>`;
    }).join("") : `<div class="muted" style="font-size:12px">${esc(t("登録はありません"))}</div>`;
    host.querySelectorAll("input,select").forEach(el => { el.addEventListener("input", dirty); el.addEventListener("change", dirty); });
    host.querySelectorAll("[data-mb-rm]").forEach(b => b.onclick = () => { rows = collect(); rows.splice(+b.dataset.mbRm, 1); dirty(); draw(); });
    host.querySelectorAll(".mb-auth").forEach(sel => sel.onchange = () => {
      const r = sel.closest("[data-mb-row]"), key = sel.value === "publickey";
      r.querySelector("[data-mb-pw]").style.display = key ? "none" : "";
      r.querySelector("[data-mb-key]").style.display = key ? "" : "none";
      const inp = r.querySelector(".mb-key");
      if (key && !inp.value && (INV.settings || {}).lastKeyFile) inp.value = INV.settings.lastKeyFile;
      dirty();
    });
    // Telnet: a password only (no SSH auth choice, key or ciphers).
    host.querySelectorAll(".mb-method").forEach(sel => sel.onchange = () => {
      const r = sel.closest("[data-mb-row]"), inp = r.querySelector(".mb-port"), p = parseInt(inp.value, 10) || 0;
      if (!p || p === 22 || p === 23) inp.value = sel.value === "ssh" ? 22 : 23;
      const ssh = sel.value === "ssh", key = ssh && r.querySelector(".mb-auth").value === "publickey";
      r.querySelectorAll("[data-mb-ssh]").forEach(el => { el.style.display = ssh ? "" : "none"; });
      r.querySelector("[data-mb-pw]").style.display = key ? "none" : "";
      r.querySelector("[data-mb-key]").style.display = key ? "" : "none";
      dirty();
    });
    host.querySelectorAll(".mb-key-pick").forEach(b => b.onclick = async () => {
      try {
        const p = await App().PickKeyFile();
        if (p) { b.closest("[data-mb-row]").querySelector(".mb-key").value = p; dirty(); }
      } catch (e) { toast(t("選択失敗") + ": " + terr(e), "err"); }
    });
    wirePasswordToggles(host);
    panel.querySelector("#mb-add").style.display = rows.length >= 5 ? "none" : "";
  };
  draw();
  panel.querySelector("#mb-add").onclick = () => {
    rows = collect();
    if (rows.length >= 5) return;
    rows.push({ method: "ssh", authMethod: "publickey" });
    dirty(); draw();
  };
  panel.querySelector("#mb-save").onclick = async () => {
    const chain = collect();
    try {
      await App().SaveMyBastions(chain);
      myLoginsDirty = false;
      await refreshInventory();
      toast(chain.some(b => b.host) ? t("保存しました") : t("自分の踏み台の登録を消しました"), "ok");
    } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  };
}

// ---- OSタイプ tab ----

// All profiles live in the vault and are equally editable — the seeded 14
// defaults included. Deleting never touches the JSON files under
// export\os-profiles\, so ファイル読み込み can always restore a profile.
function renderOSTypes() {
  const root = document.getElementById("tab-ostypes");
  const profiles = INV.customProfiles || [];
  // Sort / filter (display only; see listView in app.js).
  const LV = "ostypes";
  const focus = listViewFocus();
  const visible = listViewApply(LV, profiles,
    p => [p.name, p.key, p.prompt],
    (p, k) => k === "login" ? (p.login || []).length : p[k]);
  renderListTab(root, `
    <div class="page-head">
      <div><div class="page-title">${esc(LANG === "ja" ? "OSタイプ" : t("メニュー:OSタイプ"))}</div>
        <div class="page-sub">${esc(t("機器種別ごとのログイン自動化（expect/send）。待つ文字は「#」「assword:」のような文字そのまま（部分一致）。標準プロファイルのJSONは export\\os-profiles\\ に自動で置かれ、アプリが消すことはないので、削除しても読み込みで戻せます。書き出しは「機器」タブの一式書き出しにまとまっています"))}</div></div>
      <div class="row-inline" style="gap:6px;white-space:nowrap">
        <button class="btn" id="prof-imp" data-tip="${esc(t("OSタイププロファイルの JSON を 1 ファイル読み込みます（削除した標準プロファイルの復元にも）"))}">${esc(t("読み込み"))}</button>
        <button class="btn primary" id="prof-add">${esc(t("＋ OSタイプを追加"))}</button>
      </div>
    </div>
    ${profiles.length ? listViewBar(LV, t("絞り込み（名前・プロンプト）")) : ""}
    <div class="panel scrollx">
      ${profiles.length === 0
        ? `<div class="empty">${esc(t("プロファイルがありません。「読み込み」で export\\os-profiles\\ のJSONから復元するか、「＋ OSタイプを追加」で作成してください。"))}</div>`
        : visible.length === 0 ? `<div class="empty">${esc(t("絞り込みに一致するプロファイルがありません"))}</div>`
        : `<table class="fixed" data-colw="ostypes"><thead><tr><th style="width:30px"></th>${listViewTh(LV, "name", t("名前"), "width:280px")}${listViewTh(LV, "prompt", t("プロンプト（完了の目印）"))}${listViewTh(LV, "login", t("ログイン手順"), "width:110px")}<th style="width:220px"></th></tr></thead><tbody id="prof-body">
        ${visible.map(p => `<tr data-key="${esc(p.key)}">
          ${listViewDragCell(LV)}
          <td><div class="clip" data-tip="${esc(p.name)}"><b>${esc(p.name)}</b></div></td>
          <td class="mono muted"><div class="clip" data-tip="${esc(p.prompt)}">${esc(p.prompt)}</div></td>
          <td class="muted">${esc(t("{n} 行", { n: (p.login || []).length }))}</td>
          <td class="ops"><div class="ops-btns">
            <button class="btn sm act-edit" data-pedit="${esc(p.key)}">${esc(t("編集"))}</button>
            <button class="btn sm act-copy" data-pcopy="${esc(p.key)}">${esc(t("複製"))}</button>
            <button class="btn sm act-del" data-pdel="${esc(p.key)}">${esc(t("削除"))}</button>
          </div></td></tr>`).join("")}</tbody></table>`}
    </div>`, [listView(LV)]);
  listViewWire(LV, root, renderOSTypes, focus);
  wireColResize(root);
  if (!listViewActive(LV)) wireRowReorder(root.querySelector("#prof-body"), async order => {
    try {
      await App().ReorderProfiles(order);
      INV = await App().GetInventory();
      PROFILES = await App().ListProfiles();
      renderOSTypes();
    } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  });
  root.querySelectorAll("[data-pedit]").forEach(b => b.onclick = () =>
    editProfile((INV.customProfiles || []).find(p => p.key === b.dataset.pedit)));
  root.querySelectorAll("[data-pcopy]").forEach(b => b.onclick = () => {
    // As a device's 複製: opened as a new, unsaved profile (…_copy) — none
    // exists until 保存.
    const src = (INV.customProfiles || []).find(p => p.key === b.dataset.pcopy);
    if (!src) return;
    const taken = new Set((INV.customProfiles || []).map(p => p.name.toLowerCase()));
    let nm = src.name + "_copy";
    for (let i = 2; taken.has(nm.toLowerCase()); i++) nm = `${src.name}_copy${i}`;
    editProfile(null, { ...JSON.parse(JSON.stringify(src)), key: "", name: nm });
    const inp = document.getElementById("p-name"); if (inp) { inp.focus(); inp.select(); }
  });
  root.querySelectorAll("[data-pdel]").forEach(b => b.onclick = async () => {
    const p = (INV.customProfiles || []).find(x => x.key === b.dataset.pdel);
    if (!(await uiConfirm({ title: t("プロファイルを削除"), message: t("OSタイププロファイル「<b>{n}</b>」を削除しますか？<br><span class=\"muted\" style=\"font-size:12px\">標準プロファイルは export\\os-profiles\\ のJSONから、自作のものは一式書き出ししたフォルダから「読み込み」で戻せます</span>", { n: esc(p ? p.name : "") }), okLabel: t("削除"), danger: true }))) return;
    try {
      await App().DeleteProfile(b.dataset.pdel);
      INV = await App().GetInventory();
      PROFILES = await App().ListProfiles();
      renderOSTypes();
      toast(t("削除しました"), "ok");
    } catch (e) { toast(t("削除失敗") + ": " + terr(e), "err"); }
  });
  document.getElementById("prof-add").onclick = () => editProfile(null);
  document.getElementById("prof-imp").onclick = async () => {
    try {
      const res = await importOneFile(() => App().ImportOSProfileFile(), t("OSタイププロファイル"));
      if (res) {
        INV = await App().GetInventory();
        PROFILES = await App().ListProfiles();
        renderOSTypes();
        toast(res.same ? t("「{n}」は同じ内容で登録済みです", { n: res.name }) : t("「{n}」を読み込みました", { n: res.name }), "ok");
      }
    } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
  };
}

// Profile editor modal (defaults and user-made profiles are edited the same
// way).
// editProfile edits prof, or adds a new one (filled from tpl for 複製).
function editProfile(prof, tpl) {
    // 新規作成時は Cisco IOS 系の実例をすべて埋めた状態で開く（書き換えて使う）。
    // プロンプト・expectは「#」「Username:」等の文字そのまま（部分一致）。
    const p = prof || tpl || {
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
      answers: [{ expect: "[confirm]", enter: true }, { expect: "]?", enter: true }],
    };
    // Spread keeps fields the editor doesn't show (enter/sendRaw/delayMs on
    // imported built-ins) attached to their row so a save doesn't drop them.
    let steps = (p.login || []).map(s => ({ ...s, expect: s.expect || "", send: s.send || "" }));
    if (steps.length === 0) steps = [{ expect: "", send: "" }];
    const pagerCmd = (p.pager || []).map(s => s.send || "").filter(Boolean).join("\n");
    const discCmd = (p.disconnect || []).map(s => s.send || "").filter(Boolean).join("\n") || "exit";
    const node = h(`<div>
      <h3>${esc(prof ? t("OSタイププロファイルを編集") : t("OSタイププロファイルを追加"))}</h3>
      ${prof || tpl ? "" : `<p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("Cisco IOS系の実例を入れてあります。機器に合わせて書き換えてください。"))}</p>`}
      <div class="grid-2">
        <div class="field"><label>${esc(t("プロファイル名"))} <span class="req">${esc(t("必須"))}</span></label><input id="p-name" value="${esc(p.name || "")}" placeholder="${esc(t("例: MyRouter OS"))}"></div>
        <div class="field"><label><span data-tip="${esc(t("コマンドが打てる状態のときに機器が出す表示（例: Router#）。コマンド送信後、これが再び現れたら「出力が終わった」と判定して次のコマンドを送ります。「#」のように末尾の記号だけ書いておけば、設定モードのプロンプト（例: FG(console)#）にも一致します"))}">${esc(t("プロンプト（コマンド完了の目印） ⓘ"))}</span> <span class="req">${esc(t("必須"))}</span></label><input id="p-prompt" class="mono" value="${esc(p.prompt || "")}"></div>
      </div>
      <div class="grid-2">
        <div class="field"><label><span data-tip="${esc(t("ログイン直後に流す、出力の一時停止（--More--）をなくすコマンド。OSによりコマンドやモードが違うため複数行OK（設定モードに入る手順ごと書けます）。FortiGateのように機器側で設定済みの場合は空欄でかまいません"))}">${esc(t("ページャ解除コマンド（任意・1行1コマンドで複数行可） ⓘ"))}</span></label>
          <textarea id="p-pager" class="mono" style="min-height:72px" placeholder="config system console&#10;set output standard&#10;end">${esc(pagerCmd)}</textarea></div>
        <div class="field"><label><span data-tip="${esc(t("出力の途中一時停止の表示。これが出たらスペースを送って続きを表示します。ページャ解除コマンドで止まらないOSだけ設定します"))}">${esc(t("ページャ表示の目印（任意） ⓘ"))}</span></label>
          <input id="p-more" class="mono" value="${esc(p.morePrompt || "")}" placeholder="--- more ---"></div>
      </div>
      <label style="font-size:12px;color:var(--text-dim)"><span data-tip="${esc(t("上から順に実行。expect列の表示が出るのを待ち、send列の文字列を入力。認証系の行（{user}/{password}/{enable}を送る行）は表示が出なければ自動でスキップされるため、SSH/Telnetで同じプロファイルが使えます。最後の行の入力が終わると自動で「プロンプト（コマンド完了の目印）」を待つため、目印を待つだけの行は不要です"))}">${esc(t("ログイン手順 ⓘ"))}</span></label>
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
      <label style="font-size:12px;color:var(--text-dim);display:block;margin-top:12px"><span data-tip="${esc(t("一括実行でコマンドが確認を求めてきたとき（例: copy run start の「Destination filename [startup-config]?」、clear counters の「[confirm]」）に自動で返す答え。出力の末尾がこの表示になったら、右の文字を送ります（空欄なら Enter＝機器の既定値で進む）。設定しないと、そのコマンドはタイムアウトまで待って失敗します。{user} {password} などの変数は使えません（想定外の質問へパスワードを送らないため）。既定値の無い質問（「[]?」）には Enter を返しません"))}">${esc(t("確認への自動応答（任意） ⓘ"))}</span></label>
      <div class="scroll"><table style="margin-top:6px">
        <thead><tr><th>${esc(t("待つ表示（質問の末尾）"))}</th><th>${esc(t("送る文字（空欄 = Enter）"))}</th><th style="width:44px"></th></tr></thead>
        <tbody id="p-ans"></tbody>
      </table></div>
      <button class="btn sm" id="p-addans" type="button" style="margin-top:8px">${esc(t("＋ 行を追加"))}</button>
      <div class="modal-actions">
        <button class="btn" id="p-cancel">${esc(t("キャンセル"))}</button>
        <button class="btn" id="p-save-test" data-tip="${esc(t("保存してから実行タブへ移り、この OS タイプの機器を絞り込みます。「⚡ 接続確認のみ」でログインまで試せます"))}">${esc(t("保存して接続確認へ"))}</button>
        <button class="btn primary" id="p-save" data-tip="Ctrl+Enter">${esc(t("保存"))}</button>
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
          <td><input class="p-snd mono" style="width:100%" value="${esc(s.send)}" placeholder="${esc(s.enter ? t("（Enter だけ送る）") : s.sendRaw ? t("（制御文字を送る: {r}）", { r: JSON.stringify(s.sendRaw) }) : "")}"></td>
          <td style="white-space:nowrap">
            <button class="btn sm" type="button" data-ins="${i}" data-tip="${esc(t("この行の下に挿入"))}">＋</button>
            <button class="btn sm act-del" type="button" data-rm="${i}" data-tip="${esc(t("この行を削除"))}">×</button>
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
    // 確認への自動応答: expect → send (empty = Enter).
    let answers = (p.answers || []).map(a => ({ ...a, expect: a.expect || "", send: a.send || "" }));
    const ansBody = node.querySelector("#p-ans");
    const collectAns = () => [...ansBody.querySelectorAll("tr[data-idx]")].map(r => ({
      ...(answers[parseInt(r.dataset.idx, 10)] || {}),
      expect: r.querySelector(".a-exp").value, send: r.querySelector(".a-snd").value,
    }));
    function renderAns() {
      ansBody.innerHTML = answers.map((a, i) => `
        <tr data-idx="${i}">
          <td><input class="a-exp mono" style="width:100%" value="${esc(a.expect)}" placeholder="[confirm]"></td>
          <td><input class="a-snd mono" style="width:100%" value="${esc(a.send)}" placeholder="${esc(t("（Enter）"))}"></td>
          <td><button class="btn sm act-del" type="button" data-arm="${i}" data-tip="${esc(t("この行を削除"))}">×</button></td>
        </tr>`).join("") || `<tr><td colspan="3" class="muted" style="font-size:12px">${esc(t("（なし）"))}</td></tr>`;
      ansBody.querySelectorAll("[data-arm]").forEach(b => b.onclick = () => {
        answers = collectAns(); answers.splice(parseInt(b.dataset.arm, 10), 1); renderAns();
      });
    }
    renderAns();
    node.querySelector("#p-addans").onclick = () => { answers = collectAns(); answers.push({ expect: "", send: "" }); renderAns(); };
    guardDiscard(node, "#p-cancel");
    // 保存して接続確認へ: the run tab, filtered to this OS type's devices,
    // where ⚡ 接続確認のみ tries the login — a profile for a new vendor
    // used to mean saving, finding a device, switching tabs, filtering.
    node.querySelector("#p-save-test").onclick = async () => {
      const saved = await saveProfile();
      if (!saved) return;
      const devs = (INV.devices || []).filter(d => d.osType === saved.key && !d.cliOff);
      if (!devs.length) { toast(t("この OS タイプの機器がまだありません。機器の編集で OS タイプに「{n}」を選んでから、実行タブの「⚡ 接続確認のみ」で試せます", { n: saved.name }), "warn"); return; }
      if (!runGroupSel || !devs.some(d => (d.group || "") === (runGroupSel === RUN_UNGROUPED ? "" : runGroupSel))) {
        for (const k in runState) delete runState[k];
        runGroupSel = devs[0].group || RUN_UNGROUPED;
      }
      runFilter.text = saved.name; runFilter.site = ""; runFilter.status = "";
      await switchTab("run");
      toast(t("OS タイプ「{n}」の機器に絞り込みました。「⚡ 接続確認のみ」でログインまで試せます", { n: saved.name }), "ok");
    };
    // saveProfile writes the editor's profile; the saved profile (key and
    // name) comes back, or null when the save failed.
    async function saveProfile() {
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
        // The textarea only shows the send of each disconnect step: a step
        // whose send is still there keeps its expect (an edit of one line
        // used to drop the expects of all of them).
        disconnect: (discLines.join("\n") === discCmd && (p.disconnect || []).length)
          ? p.disconnect
          : discLines.map(l => { const o = (p.disconnect || []).find(st => (st.send || "") === l); return o ? { ...o } : { send: l }; }),
        answers: collectAns().filter(a => a.expect.trim())
          .map(a => ({ ...a, expect: a.expect.trim(), send: a.send.trim(), enter: !a.send.trim() && !a.sendRaw })),
      };
      try {
        await App().SaveProfile(out);
        INV = await App().GetInventory();
        PROFILES = await App().ListProfiles();
        closeModal();
        renderOSTypes();
        toast(t("保存しました"), "ok");
        return PROFILES.find(x => (out.key && x.key === out.key) || x.name === out.name) || { key: out.key, name: out.name };
      } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); return null; }
    }
    node.querySelector("#p-save").onclick = saveProfile;
}

// importOneFile reads one command-set / OS-profile file (read: the backend
// call with its file dialog). When a set / profile of that name is already
// there and differs, nothing has changed yet: ask whether to replace it, add
// the file under another name, or stop. Returns the FileImport, or null when
// nothing was read.
async function importOneFile(read, what) {
  let res = await read();
  if (!res || (!res.imported && !res.conflict)) return null; // dialog cancelled
  if (res.conflict) {
    const c = await uiChoice({
      title: t("同じ名前があります"),
      message: t("{what}「<b>{n}</b>」は既にあります。読み込んだファイルの内容で置き換えますか？", { what: esc(what), n: esc(res.conflict) }) +
        `<br><span class="muted" style="font-size:12px">${esc(t("置き換えると、今の内容は戻せません（使っている機器はそのまま置き換えた内容を使います）"))}</span>`,
      buttons: [{ id: "cancel", label: t("やめる") }, { id: "rename", label: t("別名で追加（{n}）", { n: res.altName }) }, { id: "replace", label: t("置き換える"), cls: "danger" }],
    });
    res = await App().ResolveFileImport(c === "replace" || c === "rename" ? c : "cancel");
    if (!res || !res.imported) return null;
  }
  return res;
}

// unlockRecover is the lock screen's answer to a failed unlock (app.js
// doUnlock): when the vault is damaged but its previous generation opens with
// this password, offer to restore it. True when the vault is open now.
async function unlockRecover(pw) {
  const info = await App().GetUnlockInfo().catch(() => null);
  if (!info || !info.backupAt) return false;
  const ok = await uiConfirm({
    title: t("保管庫を復元"),
    message: t("保管庫のファイル（data\\palaterm_vault.enc）をこのパスワードで開けませんでした。ファイルが壊れている可能性があります。<br>1 世代前の控え（{at} に保存）はこのパスワードで開けます。控えから復元しますか？", { at: esc(info.backupAt) }) +
      `<br><span class="muted" style="font-size:12px">${esc(t("今のファイルは消さずに palaterm_vault.enc.broken-日時 という名前で残します。控えのあとに行った変更は戻りません。"))}</span>`,
    okLabel: t("1 世代前から復元"),
  });
  if (!ok) return false;
  try {
    const kept = await App().RestoreVaultBackup(pw);
    toast(t("1 世代前の控えから復元しました（壊れていたファイルは data\\{f} として残しました）", { f: kept }), "ok");
    return true;
  } catch (e) { toast(t("復元できません") + ": " + terr(e), "err"); return false; }
}

// unlockNotices tells, right after an unlock, what the backend found: a
// vault saved by a newer PalaTerm (opened read-only, unless the user chooses
// to edit it here anyway), profiles that were given new keys.
async function unlockNotices() {
  const info = await App().GetUnlockInfo().catch(() => null);
  if (!info) return;
  if (info.rekeyed && info.rekeyed.length) {
    toast(t("同じ内部キーを持つOSタイププロファイルを分けました: {list}（機器は先にあったプロファイルのままです。こちらを使う機器があれば選び直してください）", { list: info.rekeyed.join(", ") }), "warn");
  }
  if (info.newer && info.readOnly) {
    const c = await uiChoice({
      title: t("新しい版で保存された保管庫です"),
      message: esc(t("この保管庫は新しい版の PalaTerm{v} で保存されています。この版で保存すると、新しい版で追加された設定が消えます。", { v: info.savedBy ? " (v" + info.savedBy + ")" : "" })) +
        "<br>" + esc(t("新しい版の PalaTerm を使うことをお勧めします。この版では読み取り専用で開きます。")),
      buttons: [{ id: "edit", label: t("この版で編集する（新しい版の設定は消えます）"), cls: "danger" }, { id: "ro", label: t("読み取り専用で開く"), cls: "primary" }],
    });
    if (c === "edit") { await App().AllowOlderSave(); toast(t("この版で保存できるようにしました"), "warn"); }
    else toast(t("読み取り専用で開いています（変更は保存されません）"), "warn");
  }
}

wireEvents();
