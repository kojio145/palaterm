// 予約 tab: scheduled batch runs (see sched_app.go). The schedules live in
// the vault; the main window's process runs them while it is open, locked or
// hidden in the tray. A schedule only collects: a command set that changes
// the device is refused when saving and again at every firing.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

const WEEKDAYS = ["日", "月", "火", "水", "木", "金", "土"];
let schedRenderTimer = 0;

function fmtSchedTime(iso) { return iso ? fmtRunTime(iso) : ""; }

function slotLabel(s) {
  const tm = s.time || "";
  if (s.kind === "once") return t("1回 {d} {t}", { d: s.date || "", t: tm });
  if (s.kind === "daily") return t("毎日 {t}", { t: tm });
  if (s.kind === "weekly") return t("毎週 {w} {t}", { w: (s.weekdays || []).map(w => t(WEEKDAYS[w])).join("・"), t: tm });
  if (s.kind === "interval") return t("{n}時間おき（{d} {t} から）", { n: s.everyHours || 0, d: s.date || "", t: tm });
  return s.kind;
}

function schedTarget(sc) {
  const g = sc.ungrouped ? t("（グループ未設定）") : sc.group;
  const devs = sc.names ? t("{n} 台に固定", { n: sc.names.length }) : t("実行時にチェックされている機器");
  return `${g} ／ ${devs}`;
}

function schedKindLabel(e) {
  return ({ run: t("実行"), missed: t("見逃し"), skipped: t("スキップ"), refused: t("拒否"), error: t("エラー") })[e.kind] || e.kind;
}

async function renderSchedules() {
  const root = document.getElementById("tab-schedule");
  let list = [], st = {}, log = [];
  try {
    [list, st, log] = await Promise.all([App().ListSchedules(), App().GetScheduleStatus(), App().ScheduleLog(100)]);
  } catch (e) {
    root.innerHTML = `<div class="empty">${esc(terr(e))}</div>`;
    return;
  }
  list = list || []; log = log || [];
  // Can this PC be woken for a 予約? The power plan's "Allow wake timers"
  // for the current power source, and modern standby (S0 low-power idle).
  const pw = st.power || {};
  const timers = pw.onAC === false ? pw.wakeTimersDC : pw.wakeTimersAC;
  const timersOff = timers === 0;
  const wtName = v => v === 0 ? t("無効") : v === 1 ? t("有効") : v === 2 ? t("重要なスリープ解除タイマーのみ") : t("不明");
  const warns = [];
  if (timersOff) warns.push(t("この PC は電源オプションの「スリープ解除タイマーの許可」が無効です（電源接続時: {ac}・バッテリー: {dc}）。スリープ中は予約が動きません。予約の時刻には PC を起こしておくか、下の「予約がある間は PC を自動スリープさせない」をオンにしてください",
    { ac: wtName(pw.wakeTimersAC), dc: wtName(pw.wakeTimersDC) }));
  if (pw.modernStandby) warns.push(t("この PC はモダンスタンバイ（S0 低電力アイドル）です。スタンバイ中は Windows がアプリを止めるため、スリープ中は予約が動かない可能性があります。確実に動かすには、下の「予約がある間は PC を自動スリープさせない」をオンにしてください"));
  const wakeMsg = st.wake === "ok" && timersOff ? t("設定済み（ただし PC 側でスリープ解除タイマーが無効のため、スリープからは起こせません）")
    : st.wake === "ok" ? t("設定済み（{t} に PC をスリープから起こします）", { t: fmtSchedTime(st.wakeAt) })
    : st.wake === "unsupported" ? t("この PC ではスリープ解除タイマーが使えません（スリープ中は実行されません）")
    : st.wake === "none" || !st.wake ? t("なし（次の予約がありません）")
    : t("設定できませんでした: {e}", { e: st.wake });
  const rows = list.map(sc => `
    <tr>
      <td style="width:44px"><input type="checkbox" data-en="${esc(sc.id)}" ${sc.enabled ? "checked" : ""} title="${esc(t("有効／無効"))}"></td>
      <td><div class="clip" data-tip="${esc(sc.name)}"><b>${esc(sc.name)}</b></div></td>
      <td><div style="font-size:13px">${esc(schedTarget(sc))}</div>
        <div class="muted" style="font-size:12px">${esc(sc.commandSet ? t("コマンド: {s}", { s: sc.commandSet }) : t("コマンド: 機器ごとの設定どおり"))}${sc.stage ? " " + stageBadge(sc.stage) : ""}</div></td>
      <td><div style="font-size:12px;line-height:1.6">${(sc.slots || []).map(s => esc(slotLabel(s))).join("<br>")}</div></td>
      <td class="mono" style="width:160px">${sc.enabled ? (sc.next ? esc(fmtSchedTime(sc.next)) : `<span class="muted">${esc(t("（終了）"))}</span>`) : `<span class="muted">${esc(t("（無効）"))}</span>`}</td>
      <td style="text-align:right;white-space:nowrap;width:230px">
        <button class="btn sm" data-now="${esc(sc.id)}">${esc(t("今すぐ実行"))}</button>
        <button class="btn sm" data-edit="${esc(sc.id)}">${esc(t("編集"))}</button>
        <button class="btn sm act-del" data-del="${esc(sc.id)}">${esc(t("削除"))}</button>
      </td>
    </tr>`).join("");
  const logRows = log.map(e => `
    <tr>
      <td class="mono" style="width:170px">${esc(fmtSchedTime(e.due))}</td>
      <td><div class="clip" data-tip="${esc(e.schedule)}">${esc(e.schedule)}${e.manual ? ` <span class="badge">${esc(t("今すぐ"))}</span>` : ""}</div></td>
      <td style="width:90px"><span class="dot st-${e.kind === "run" ? (e.failed ? "error" : "done") : e.kind === "missed" || e.kind === "skipped" ? "canceled" : "error"}"></span>${esc(schedKindLabel(e))}</td>
      <td style="width:120px">${e.kind === "run" ? `<span style="color:var(--ok)">${e.ok || 0}</span> / <span style="color:var(--err)">${e.failed || 0}</span>` : ""}</td>
      <td class="muted"><div class="clip" data-tip="${esc(terr(e.message || ""))}">${esc(terr(e.message || ""))}</div></td>
      <td style="text-align:right;width:100px">${e.runDir ? `<span class="link" data-folder="${esc(e.runDir)}">${esc(t("フォルダ"))}</span>` : ""}</td>
    </tr>`).join("");
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("予約"))}</div>
        <div class="page-sub">${esc(t("決まった日時に一括実行してログを取ります（show 系の取得専用。設定を変更するコマンドを含むセットは予約できません）。PalaTerm が起動している間だけ動きます。× で閉じるとタスクトレイに残って動き続け、ロック中も実行します"))}</div></div>
      <div class="row-inline"><button class="btn primary" id="sc-add">${esc(t("＋ 予約を追加"))}</button></div>
    </div>
    <div class="panel" style="padding:14px 18px;margin-bottom:16px;font-size:13px;line-height:1.9">
      ${st.running ? `<div style="color:var(--run)"><b>${esc(t("▶ 実行中: {n}", { n: st.running }))}</b> ${esc(t("（{d} / {t} 台）", { d: st.done, t: st.total }))} <button class="btn sm" id="sc-cancel">${esc(t("■ 中止"))}</button></div>` : ""}
      ${st.queued ? `<div style="color:var(--warn)">${esc(t("順番待ち: {n} 件（ほかの一括実行が終わりしだい実行。30 分を過ぎたらスキップ）", { n: st.queued }))}</div>` : ""}
      <div>${esc(t("次の実行"))}: <b class="mono">${st.next ? esc(fmtSchedTime(st.next)) : "—"}</b>${st.nextName ? "　" + esc(st.nextName) : ""}</div>
      <div>${esc(t("スリープ解除タイマー"))}: ${esc(wakeMsg)}</div>
      ${warns.map(w => `<div style="color:var(--warn);font-size:12px;line-height:1.7;margin-top:4px">⚠ ${esc(w)}</div>`).join("")}
      <label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer;margin-top:6px"><input type="checkbox" id="sc-keepawake" style="width:auto" ${st.keepAwake ? "checked" : ""}> <span>${esc(t("予約がある間は PC を自動スリープさせない"))} <span class="muted" style="font-size:12px">${esc(t("（有効な予約が残っている間ずっと。画面の消灯は止めません。ノート PC の蓋閉じ・手動スリープは止められないので、蓋を閉じて使うときは電源オプションの「カバーを閉じたときの動作」を「何もしない」に）"))}</span></span></label>
      <details style="margin-top:4px"><summary class="link" style="text-decoration:none">${esc(t("スリープ・蓋閉じ・シャットダウンのときは？"))}</summary>
        <div class="muted" style="font-size:12px;line-height:1.8;margin-top:6px">${esc(t("・スリープ中: 予約の 90 秒前に PC を起こして実行します。Windows の電源オプション →「スリープ解除タイマーの許可」が「有効」（バッテリー駆動時も）である必要があります。会社の PC では管理者の設定で無効になっていることがあります"))}<br>
        ${esc(t("・ノート PC の蓋閉じ: 電源設定で「蓋を閉じたときの動作」がスリープなら、上と同じくスリープ解除で動きます。休止状態・シャットダウンでは動きません"))}<br>
        ${esc(t("・PalaTerm を終了（トレイの「終了」）すると予約は動きません。次に起動して一度ロック解除すると再開します"))}<br>
        ${esc(t("・スリープ中などで実行できなかった回は「見逃し」として記録し、さかのぼっては実行しません。ほかの一括実行と重なったら順番待ちし、30 分を過ぎたらスキップします"))}<br>
        ${esc(t("・ロック中も実行するため、予約の対象機器の認証情報はロック中もメモリに残します（ファイルには書きません）"))}</div></details>
    </div>
    <div class="panel scrollx" style="margin-bottom:16px">
      ${list.length ? `<table class="fixed"><thead><tr><th style="width:44px"></th><th style="width:170px">${esc(t("名前"))}</th><th>${esc(t("対象"))}</th><th style="width:230px">${esc(t("日時"))}</th><th style="width:160px">${esc(t("次の実行"))}</th><th style="width:230px"></th></tr></thead><tbody>${rows}</tbody></table>`
        : `<div class="empty">${esc(t("予約はありません。「＋ 予約を追加」または実行タブの「⏰ 予約…」から作れます"))}</div>`}
    </div>
    <div class="page-title" style="font-size:15px;margin:6px 0 8px">${esc(t("予約の実行記録"))}</div>
    <div class="panel scrollx">
      ${log.length ? `<table class="fixed"><thead><tr><th style="width:170px">${esc(t("予定日時"))}</th><th style="width:200px">${esc(t("予約"))}</th><th style="width:90px">${esc(t("結果"))}</th><th style="width:120px">${esc(t("成功 / 失敗"))}</th><th>${esc(t("メッセージ"))}</th><th style="width:100px"></th></tr></thead><tbody>${logRows}</tbody></table>`
        : `<div class="empty">${esc(t("まだ記録はありません"))}</div>`}
    </div>`;

  root.querySelector("#sc-add").onclick = () => editSchedule(null);
  root.querySelector("#sc-keepawake").onchange = async e => {
    try { await App().SetScheduleKeepAwake(e.target.checked); toast(t("保存しました"), "ok"); }
    catch (err) { toast(terr(err), "err"); e.target.checked = !e.target.checked; }
  };
  const cancel = root.querySelector("#sc-cancel");
  if (cancel) cancel.onclick = async () => {
    if (await uiConfirm({ title: t("予約の実行を中止"), message: t("実行中の予約「{n}」を中止しますか？", { n: esc(st.running) }), okLabel: t("中止する"), danger: true })) App().CancelScheduledRun();
  };
  root.querySelectorAll("[data-en]").forEach(cb => cb.onchange = async () => {
    try { await App().SetScheduleEnabled(cb.dataset.en, cb.checked); }
    catch (e) { toast(terr(e), "err"); }
    renderSchedules();
  });
  root.querySelectorAll("[data-now]").forEach(b => b.onclick = async () => {
    const sc = list.find(x => x.id === b.dataset.now);
    if (!(await uiConfirm({ title: t("今すぐ実行"), message: t("予約「{n}」を今すぐ 1 回実行しますか？（ほかの一括実行中なら終わってから）", { n: esc(sc ? sc.name : "") }), okLabel: t("▶ 実行") }))) return;
    try { await App().RunScheduleNow(b.dataset.now); toast(t("実行を予約しました"), "ok"); }
    catch (e) { toast(terr(e), "err"); }
  });
  root.querySelectorAll("[data-edit]").forEach(b => b.onclick = () => editSchedule(list.find(x => x.id === b.dataset.edit)));
  root.querySelectorAll("[data-del]").forEach(b => b.onclick = async () => {
    const sc = list.find(x => x.id === b.dataset.del);
    if (!(await uiConfirm({ title: t("予約を削除"), message: t("予約「<b>{n}</b>」を削除しますか？", { n: esc(sc ? sc.name : "") }), okLabel: t("削除"), danger: true }))) return;
    try { await App().DeleteSchedule(b.dataset.del); renderSchedules(); }
    catch (e) { toast(terr(e), "err"); }
  });
  root.querySelectorAll("[data-folder]").forEach(b => b.onclick = () => App().OpenRunFolder(b.dataset.folder).catch(e => toast(terr(e), "err")));
}

// Re-draw the tab when the scheduler reports a change (a run starting or
// ending, a log line) — at most a few times a second.
function wireScheduleEvents() {
  rt().EventsOn("sched:changed", () => {
    if (!document.querySelector("#tab-schedule.active") || document.querySelector("#modal-overlay:not(.hidden)")) return;
    clearTimeout(schedRenderTimer);
    schedRenderTimer = setTimeout(renderSchedules, 250);
  });
  rt().EventsOn("sched:done", e => {
    toast(t("予約「{n}」の一括実行が終わりました（成功 {a}・失敗 {b}）", { n: e.schedule, a: e.ok || 0, b: e.failed || 0 }), e.failed ? "err" : "ok");
  });
}

function todayStr(addDays) {
  const d = new Date(Date.now() + (addDays || 0) * 86400000);
  const p = n => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// The 予約 editor. prefill (from the run tab's ⏰ 予約…) carries the run
// choices: group, devices, command set, stage.
function editSchedule(sc, prefill) {
  const groups = INV.deviceGroups || [];
  const ungroupedN = (INV.devices || []).filter(d => !d.group).length;
  const s = sc ? JSON.parse(JSON.stringify(sc)) : {
    id: "", name: "", enabled: true, group: "", ungrouped: false, names: null, commandSet: "", stage: "",
    slots: [{ kind: "daily", time: "03:00:00", date: todayStr(1), weekdays: [], everyHours: 6 }], ...(prefill || {}),
  };
  let slots = (s.slots || []).map(x => ({ kind: x.kind, date: x.date || todayStr(1), time: x.time || "03:00:00", weekdays: x.weekdays || [], everyHours: x.everyHours || 6 }));
  const grpVal = s.ungrouped ? RUN_UNGROUPED : (s.group || "");
  const node = h(`<div>
    <h3>${esc(sc ? t("予約を編集") : t("予約を追加"))}</h3>
    <div class="grid-2">
      <div class="field"><label>${esc(t("予約名"))} <span class="req">${esc(t("必須"))}</span></label><input id="sc-name" value="${esc(s.name)}" placeholder="${esc(t("例: 毎晩の設定バックアップ"))}"></div>
      <div class="field"><label>${esc(t("状態"))}</label><label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer;min-height:38px"><input type="checkbox" id="sc-enabled" style="width:auto" ${s.enabled ? "checked" : ""}> ${esc(t("有効"))}</label></div>
    </div>
    <div class="grid-2">
      <div class="field"><label>${esc(t("グループ"))} <span class="req">${esc(t("必須"))}</span></label><select id="sc-group">
        <option value="">${esc(t("（選択）"))}</option>
        ${groups.map(g => `<option value="${esc(g.name)}" ${g.name === grpVal ? "selected" : ""}>${esc(g.name)}</option>`).join("")}
        ${ungroupedN ? `<option value="${RUN_UNGROUPED}" ${grpVal === RUN_UNGROUPED ? "selected" : ""}>${esc(t("（グループ未設定）"))}</option>` : ""}
      </select></div>
      <div class="field"><label>${esc(t("コマンド"))}</label><select id="sc-set">
        <option value="">${esc(t("機器ごとの設定どおり"))}</option>
        ${(INV.commandSets || []).map(cs => `<option value="${esc(cs.name)}" ${cs.name === s.commandSet ? "selected" : ""}>${esc(cs.name)}</option>`).join("")}
      </select></div>
    </div>
    <div class="field"><label>${esc(t("対象機器"))}</label>
      <label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer"><input type="radio" name="sc-devmode" value="fixed" style="width:auto" ${s.names ? "checked" : ""}> ${esc(t("下でチェックした機器に固定"))}</label>
      <label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer"><input type="radio" name="sc-devmode" value="live" style="width:auto" ${s.names ? "" : "checked"}> ${esc(t("実行時に機器一覧でチェックされている機器（グループの今の選択に従う）"))}</label>
      <div id="sc-devs" class="panel" style="margin-top:6px;padding:8px 12px;max-height:160px;overflow:auto"></div></div>
    <div class="field"><label>${esc(t("作業タイミング"))}</label><select id="sc-stage">
      ${STAGES.map(x => `<option value="${x}" ${x === (s.stage || "") ? "selected" : ""}>${esc(x ? stageLabel(x) : t("指定なし"))}</option>`).join("")}
    </select></div>
    <div class="field"><label>${esc(t("日時（枠）"))} <span class="req">${esc(t("必須"))}</span> <span class="muted" style="font-weight:normal;font-size:12px">${esc(t("複数の枠を組み合わせられます（例: 毎日 03:00 ＋ 毎週日曜 12:00）"))}</span></label>
      <div id="sc-slots"></div>
      <button class="btn sm" id="sc-add-slot" type="button" style="margin-top:6px">${esc(t("＋ 枠を追加"))}</button></div>
    <div class="muted" style="font-size:12px">${esc(t("設定を変更するコマンド（conf t・write など）を含むセットは予約できません。実行のたびにも確認し、含まれていれば実行しません"))}</div>
    <div class="modal-actions">
      <button class="btn" id="sc-cancel-edit">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="sc-save">${esc(t("保存"))}</button>
    </div>
  </div>`);
  openModal(node, "set-edit");
  const grpSel = node.querySelector("#sc-group");
  const devBox = node.querySelector("#sc-devs");
  let picked = new Set(s.names || []);
  function renderDevs() {
    const g = grpSel.value;
    const devs = !g ? [] : (INV.devices || []).filter(d => g === RUN_UNGROUPED ? !d.group : d.group === g);
    if (!s.names && !picked.size) devs.filter(d => d.enabled).forEach(d => picked.add(d.name));
    const fixed = node.querySelector('input[name="sc-devmode"]:checked').value === "fixed";
    devBox.style.opacity = fixed ? "1" : ".5";
    devBox.innerHTML = devs.length ? devs.map(d => `<label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer;font-size:13px"><input type="checkbox" style="width:auto" data-dev="${esc(d.name)}" ${picked.has(d.name) ? "checked" : ""} ${fixed ? "" : "disabled"}> ${esc(d.name)} <span class="muted mono" style="font-size:12px">${esc(d.host || d.serialPort || "")}</span></label>`).join("")
      : `<span class="muted" style="font-size:12px">${esc(t("グループを選ぶと機器が並びます"))}</span>`;
    devBox.querySelectorAll("[data-dev]").forEach(cb => cb.onchange = () => { cb.checked ? picked.add(cb.dataset.dev) : picked.delete(cb.dataset.dev); });
  }
  grpSel.onchange = () => { picked = new Set(); s.names = null; renderDevs(); };
  node.querySelectorAll('input[name="sc-devmode"]').forEach(r => r.onchange = renderDevs);
  renderDevs();

  const slotBox = node.querySelector("#sc-slots");
  function collectSlots() {
    slotBox.querySelectorAll("[data-slot]").forEach(row => {
      const i = +row.dataset.slot;
      const q = sel => row.querySelector(sel);
      slots[i].kind = q(".sl-kind").value;
      slots[i].time = q(".sl-time").value.length === 5 ? q(".sl-time").value + ":00" : q(".sl-time").value;
      if (q(".sl-date")) slots[i].date = q(".sl-date").value;
      if (q(".sl-hours")) slots[i].everyHours = parseInt(q(".sl-hours").value, 10) || 0;
      if (q(".sl-wd")) slots[i].weekdays = [...row.querySelectorAll(".sl-wd:checked")].map(x => +x.value);
    });
  }
  function renderSlots() {
    slotBox.innerHTML = slots.map((sl, i) => `
      <div data-slot="${i}" class="row-inline" style="gap:8px;flex-wrap:wrap;padding:6px 0;border-bottom:1px solid var(--border)">
        <select class="sl-kind btn" style="width:auto">
          ${[["once", "1回"], ["daily", "毎日"], ["weekly", "毎週"], ["interval", "n時間おき"]].map(([k, l]) => `<option value="${k}" ${sl.kind === k ? "selected" : ""}>${esc(t(l))}</option>`).join("")}
        </select>
        ${sl.kind === "once" || sl.kind === "interval" ? `<input class="sl-date" type="date" value="${esc(sl.date)}" style="width:auto">` : ""}
        ${sl.kind === "weekly" ? WEEKDAYS.map((w, wi) => `<label class="row-inline" style="gap:3px;font-weight:normal;cursor:pointer;font-size:13px"><input type="checkbox" class="sl-wd" value="${wi}" style="width:auto" ${sl.weekdays.includes(wi) ? "checked" : ""}>${esc(t(w))}</label>`).join("") : ""}
        <input class="sl-time" type="time" step="1" value="${esc(sl.time)}" style="width:auto">
        ${sl.kind === "interval" ? `<span class="muted" style="font-size:12px">${esc(t("から"))}</span><input class="sl-hours" type="number" min="1" max="168" value="${sl.everyHours}" style="width:80px"><span class="muted" style="font-size:12px">${esc(t("時間おき"))}</span>` : ""}
        <button class="btn sm act-del" type="button" data-rm-slot="${i}" style="margin-left:auto">×</button>
      </div>`).join("");
    slotBox.querySelectorAll(".sl-kind").forEach(selEl => selEl.onchange = () => { collectSlots(); renderSlots(); });
    slotBox.querySelectorAll("[data-rm-slot]").forEach(b => b.onclick = () => { collectSlots(); slots.splice(+b.dataset.rmSlot, 1); renderSlots(); });
  }
  renderSlots();
  node.querySelector("#sc-add-slot").onclick = () => { collectSlots(); slots.push({ kind: "daily", date: todayStr(1), time: "03:00:00", weekdays: [], everyHours: 6 }); renderSlots(); };
  guardDiscard(node, "#sc-cancel-edit");
  node.querySelector("#sc-save").onclick = async () => {
    collectSlots();
    const g = grpSel.value;
    const fixed = node.querySelector('input[name="sc-devmode"]:checked').value === "fixed";
    const names = fixed ? [...devBox.querySelectorAll("[data-dev]:checked")].map(x => x.dataset.dev) : null;
    if (fixed && !names.length) { toast(t("対象機器を 1 台以上チェックしてください"), "err"); return; }
    const obj = {
      id: s.id || "", name: node.querySelector("#sc-name").value.trim(), enabled: node.querySelector("#sc-enabled").checked,
      group: g === RUN_UNGROUPED ? "" : g, ungrouped: g === RUN_UNGROUPED, names,
      commandSet: node.querySelector("#sc-set").value, stage: node.querySelector("#sc-stage").value,
      slots: slots.map(x => ({ kind: x.kind, time: x.time, date: x.kind === "once" || x.kind === "interval" ? x.date : "",
        weekdays: x.kind === "weekly" ? x.weekdays : [], everyHours: x.kind === "interval" ? x.everyHours : 0 })),
    };
    try {
      await App().SaveSchedule(obj);
      closeModal(); toast(t("予約を保存しました"), "ok");
      switchTab("schedule");
    } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  };
}

// The run tab's ⏰ 予約…: a new schedule with the run tab's current choices.
function scheduleFromRun() {
  if (!runGroupSel) { toast(t("先にグループを選んでください"), "err"); return; }
  const names = runTargetDevices().map(d => d.name);
  editSchedule(null, {
    group: runGroupSel === RUN_UNGROUPED ? "" : runGroupSel, ungrouped: runGroupSel === RUN_UNGROUPED,
    names: names.length ? names : null,
    commandSet: runSetSel && runSetSel !== RUN_ADHOC ? runSetSel : "", stage: runStage,
  });
}

wireScheduleEvents();
