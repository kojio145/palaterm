// 予約 tab: scheduled batch runs (see sched_app.go). The schedules live in
// the vault; the main window's process runs them while it is open, locked or
// hidden in the tray. A schedule only collects: a command set that changes
// the device is refused when saving and again at every firing.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

const WEEKDAYS = ["日", "月", "火", "水", "木", "金", "土"];
let schedRenderTimer = 0;

function fmtSchedTime(iso) { return iso ? fmtRunTime(iso) : ""; }

// An interval slot's period in seconds (everyHours is the v1.6.0–1 form).
function slotPeriodSec(s) { return s.everySeconds || (s.everyHours || 0) * 3600; }
function fmtPeriod(sec) {
  if (sec && sec % 3600 === 0) return t("{n}時間", { n: sec / 3600 });
  if (sec && sec % 60 === 0) return t("{n}分", { n: sec / 60 });
  return t("{n}秒", { n: sec });
}

function slotLabel(s) {
  const tm = s.time || "";
  if (s.kind === "once") return t("1回 {d} {t}", { d: s.date || "", t: tm });
  if (s.kind === "daily") return t("毎日 {t}", { t: tm });
  if (s.kind === "weekly") return t("毎週 {w} {t}", { w: (s.weekdays || []).map(w => t(WEEKDAYS[w])).join("・"), t: tm });
  if (s.kind === "interval") return t("{p}おき・{c}（{d} {t} から）", {
    p: fmtPeriod(slotPeriodSec(s)), c: s.count ? t("{n}回", { n: s.count }) : t("回数の制限なし"), d: s.date || "", t: tm });
  return s.kind;
}

// The devices a schedule runs on, as names (a schedule from v1.6.0–1 may
// have no list: then the group's checked devices, as it ran then).
function schedDeviceList(sc) {
  if (sc.names) return sc.names;
  return (INV.devices || []).filter(d => (sc.ungrouped ? !d.group : d.group === sc.group) && d.enabled).map(d => d.name);
}
function schedDevicesLabel(sc) {
  const g = sc.ungrouped ? t("（グループ未設定）") : sc.group;
  const names = schedDeviceList(sc);
  const shown = names.slice(0, 4).join(", ") + (names.length > 4 ? t(" ほか {n} 台", { n: names.length - 4 }) : "");
  return t("{g}：{n} 台（{list}）", { g, n: names.length, list: shown || t("なし") });
}
// The commands it sends: the set chosen for it, or each device's own set.
function schedCommandsLabel(sc) {
  if (sc.commands && sc.commands.length) return t("直接入力：{s}", { s: sc.commands.join(" ／ ") });
  if (sc.commandSet) return sc.commandSet;
  const names = new Set(schedDeviceList(sc));
  const sets = [...new Set((INV.devices || []).filter(d => names.has(d.name) && (sc.ungrouped ? !d.group : d.group === sc.group))
    .map(d => d.commandSet || t("（未割り当て）")))];
  return t("機器ごとの設定：{s}", { s: sets.join("、") || t("なし") });
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
  // Will a 予約 run on this PC while it sleeps? Only if Windows lets an app
  // wake it (wake timers allowed for the current power source) and the PC
  // is not modern standby (which pauses apps). Otherwise the answer is the
  // keep-awake check box below — said in plain words, details folded away.
  const pw = st.power || {};
  const timers = pw.onAC === false ? pw.wakeTimersDC : pw.wakeTimersAC;
  const canWake = st.wake === "ok" && timers !== 0 && !pw.modernStandby;
  const keep = !!st.keepAwake;
  const wtName = v => v === 0 ? t("無効") : v === 1 ? t("有効") : v === 2 ? t("重要なスリープ解除タイマーのみ") : t("不明");
  const sleepLine = canWake ? `✅ ${esc(t("PC がスリープ中：予約の少し前に PC を起こして実行します"))}`
    : keep ? `✅ ${esc(t("放置しても自動でスリープしないので、予約どおり実行します"))} <span class="muted" style="font-size:12px">${esc(t("（自分でスリープした・ノート PC の蓋を閉じたときは実行されません）"))}</span>`
    : `❌ ${esc(t("PC がスリープ中：実行されません（この PC はスリープから自動では起こせない設定です）"))}`;
  const rows = list.map(sc => `
    <tr>
      <td style="width:44px"><input type="checkbox" data-en="${esc(sc.id)}" ${sc.enabled ? "checked" : ""} title="${esc(t("有効／無効"))}"></td>
      <td><div style="font-size:13px;line-height:1.6"><span class="muted">${esc(t("機器"))}</span>　${esc(schedDevicesLabel(sc))}${sc.stage ? " " + stageBadge(sc.stage) : ""}</div>
        <div style="font-size:13px;line-height:1.6"><span class="muted">${esc(t("コマンド"))}</span>　${esc(schedCommandsLabel(sc))}</div></td>
      <td><div style="font-size:12px;line-height:1.6">${(sc.slots || []).map(s => esc(slotLabel(s))).join("<br>")}</div>
        <div style="font-size:12px;margin-top:4px"><span class="muted">${esc(t("次の実行"))}</span>　<span class="mono">${sc.enabled ? (sc.next ? esc(fmtSchedTime(sc.next)) : esc(t("（終了）"))) : esc(t("（無効）"))}</span></div></td>
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
      <div style="font-weight:600;margin-bottom:2px">${esc(t("この PC での予約の動き"))}</div>
      <div>✅ ${esc(t("PalaTerm が起動中（画面ロック中、× で閉じてトレイにいる間も）：予約どおり実行します"))}</div>
      <div>${sleepLine}</div>
      ${!canWake && !keep ? `<div style="color:var(--warn)">→ ${esc(t("下の「予約がある間は PC を自動スリープさせない」をオンにすれば、このまま使えます（画面ロック・画面の消灯は今までどおり）"))}</div>` : ""}
      <label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer;margin-top:2px"><input type="checkbox" id="sc-keepawake" style="width:auto" ${keep ? "checked" : ""}> ${esc(t("予約がある間は PC を自動スリープさせない"))}<span class="muted" style="font-size:12px">${esc(canWake ? t("（この PC では無くても動きます）") : t("（この PC ではおすすめ）"))}</span></label>
      <div style="margin-top:6px">${esc(t("次の実行"))}：<b class="mono">${st.next ? esc(fmtSchedTime(st.next)) : "—"}</b>${st.nextName ? "　" + esc(st.nextName) : ""}</div>
      <details style="margin-top:4px"><summary class="link" style="text-decoration:none">${esc(t("詳しく"))}</summary>
        <div class="muted" style="font-size:12px;line-height:1.8;margin-top:6px">
        ${esc(t("・Windows の「スリープ解除タイマーの許可」：電源接続時 {ac}／バッテリー {dc}", { ac: wtName(pw.wakeTimersAC), dc: wtName(pw.wakeTimersDC) }))}<br>
        ${esc(pw.modernStandby ? t("・この PC はモダンスタンバイ（スリープ中は Windows がアプリを止める方式）です") : t("・この PC は従来型のスリープです"))}<br>
        ${esc(t("・「予約がある間は PC を自動スリープさせない」は Windows の設定を書き換えません。PalaTerm が動いている間だけ、放置による自動スリープを止めます"))}<br>
        ${esc(t("・休止状態・シャットダウン・PalaTerm の終了（トレイの「終了」）中は実行されません。次に起動して一度ロック解除すると再開します"))}<br>
        ${esc(t("・実行できなかった回は「見逃し」として記録し、さかのぼっては実行しません。ほかの一括実行と重なったら順番待ちし、30 分を過ぎたらスキップします"))}<br>
        ${esc(t("・ロック中も実行するため、予約の対象機器の認証情報はロック中もメモリに残します（ファイルには書きません）"))}</div></details>
    </div>
    <div class="panel scrollx" style="margin-bottom:16px">
      ${list.length ? `<table class="fixed"><thead><tr><th style="width:44px"></th><th>${esc(t("実行する機器・コマンド"))}</th><th style="width:330px">${esc(t("日時"))}</th><th style="width:230px"></th></tr></thead><tbody>${rows}</tbody></table>`
        : `<div class="empty">${esc(t("予約はありません。「＋ 予約を追加」または実行タブの「⏰ 予約…」から作れます"))}</div>`}
    </div>
    <div class="page-title" style="font-size:15px;margin:6px 0 8px">${esc(t("予約の実行記録"))}</div>
    <div class="panel scrollx">
      ${log.length ? `<table class="fixed"><thead><tr><th style="width:170px">${esc(t("予定日時"))}</th><th style="width:200px">${esc(t("予約"))}</th><th style="width:90px">${esc(t("結果"))}</th><th style="width:120px">${esc(t("成功 / 失敗"))}</th><th>${esc(t("メッセージ"))}</th><th style="width:100px"></th></tr></thead><tbody>${logRows}</tbody></table>`
        : `<div class="empty">${esc(t("まだ記録はありません"))}</div>`}
    </div>`;

  root.querySelector("#sc-add").onclick = () => editSchedule(null);
  root.querySelector("#sc-keepawake").onchange = async e => {
    try { await App().SetScheduleKeepAwake(e.target.checked); toast(t("保存しました"), "ok"); renderSchedules(); }
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

// The 予約 editor, in the order a user thinks of it: when, on which
// devices, what. No name to invent: one is made from those three.
// prefill (from the run tab's ⏰ 予約…) carries the run choices: group,
// devices, command set or typed commands, stage.
//
// What people schedule, and what the defaults are for:
//   - a nightly / weekly backup (毎日 03:00, a command set);
//   - watching something from now on during trouble or work (一定間隔,
//     start now, every N seconds, N times, commands typed on the spot);
//   - the before / after captures of night work (1回, 作業前 / 作業後 —
//     the only time the stage matters, so it sits under 詳細設定).
function editSchedule(sc, prefill) {
  const groups = INV.deviceGroups || [];
  const ungroupedN = (INV.devices || []).filter(d => !d.group).length;
  const pre = prefill || {};
  const typed = sc ? (sc.commands || []) : (pre.commands || []);
  const s = sc ? JSON.parse(JSON.stringify(sc)) : {
    id: "", enabled: true, group: "", ungrouped: false, names: null, commandSet: "", stage: "", ...pre,
    // Typed commands usually mean "watch this from now on"; otherwise a
    // nightly run.
    slots: [typed.length ? { kind: "interval", startNow: true, everySeconds: 60, unit: 1 } : { kind: "daily", time: "03:00:00" }],
  };
  const unitOf = sec => sec && sec % 3600 === 0 ? 3600 : sec && sec % 60 === 0 ? 60 : 1;
  let slots = (s.slots || []).map(x => {
    const sec = slotPeriodSec(x) || 60;
    // A new slot says "60 秒" (typing 10 then means 10 seconds); a saved
    // one shows its period in the largest whole unit.
    const unit = x.unit || unitOf(sec);
    return { kind: x.kind, date: x.date || todayStr(0), time: x.time || nextHourStr(), weekdays: x.weekdays || [],
      unit, every: sec / unit, count: x.count || 0, startNow: !!x.startNow };
  });
  const grpVal = s.ungrouped ? RUN_UNGROUPED : (s.group || "");
  const node = h(`<div>
    <h3>${esc(sc ? t("予約を編集") : t("予約を追加"))}</h3>

    <div class="field"><label>${esc(t("① いつ実行しますか"))}</label>
      <div id="sc-slots"></div>
      <button class="btn sm" id="sc-add-slot" type="button" style="margin-top:6px">${esc(t("＋ 日時を追加"))}</button>
      <span class="muted" style="font-size:12px;margin-left:8px">${esc(t("（例: 毎日 03:00 と 毎週日曜 12:00 を両方）"))}</span></div>

    <div class="field"><label>${esc(t("② どの機器で実行しますか"))}</label>
      <div class="row-inline" style="gap:10px;flex-wrap:wrap;margin-bottom:6px">
        <select id="sc-group" style="width:auto;min-width:220px">
          <option value="">${esc(t("グループを選択"))}</option>
          ${groups.map(g => `<option value="${esc(g.name)}" ${g.name === grpVal ? "selected" : ""}>${esc(g.name)}</option>`).join("")}
          ${ungroupedN ? `<option value="${RUN_UNGROUPED}" ${grpVal === RUN_UNGROUPED ? "selected" : ""}>${esc(t("（グループ未設定）"))}</option>` : ""}
        </select>
        <span class="link" id="sc-all" style="font-size:12px">${esc(t("すべて選択"))}</span>
        <span class="link" id="sc-none" style="font-size:12px">${esc(t("すべて解除"))}</span>
      </div>
      <div id="sc-devs" class="panel" style="padding:8px 12px;max-height:150px;overflow:auto"></div></div>

    <div class="field"><label>${esc(t("③ 何を実行しますか"))}</label>
      <label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer"><input type="radio" name="sc-cmdmode" value="set" style="width:auto" ${typed.length ? "" : "checked"}> ${esc(t("コマンドセットから選ぶ"))}</label>
      <select id="sc-set" style="margin:4px 0 8px 26px;width:calc(100% - 26px)">
        <option value="">${esc(t("機器ごとに割り当てたコマンドセット"))}</option>
        ${(INV.commandSets || []).map(cs => `<option value="${esc(cs.name)}" ${cs.name === s.commandSet ? "selected" : ""}>${esc(cs.name)}</option>`).join("")}
      </select>
      <label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer"><input type="radio" name="sc-cmdmode" value="typed" style="width:auto" ${typed.length ? "checked" : ""}> ${esc(t("コマンドを直接入力する（1 行に 1 コマンド）"))}</label>
      <textarea id="sc-cmds" class="mono" rows="4" style="margin:4px 0 0 26px;width:calc(100% - 26px);font-size:13px" placeholder="show interface status&#10;show logging | include %LINK">${esc(typed.join("\n"))}</textarea>
      <div class="muted" style="font-size:12px;margin:4px 0 0 26px">${esc(t("設定を変更するコマンド（conf t・write など）は予約できません（無人で動くため。取得用の show 系だけ）"))}</div></div>

    <details id="sc-more" style="margin:4px 0 8px" ${s.stage || !s.enabled ? "open" : ""}><summary class="link" style="text-decoration:none">${esc(t("詳細設定（ふだんは変更不要）"))}</summary>
      <div class="grid-2" style="margin-top:8px">
        <div class="field"><label>${esc(t("作業タイミング"))}</label><select id="sc-stage">
          ${STAGES.map(x => `<option value="${x}" ${x === (s.stage || "") ? "selected" : ""}>${esc(x ? stageLabel(x) : t("指定なし"))}</option>`).join("")}
        </select>
        <div class="muted" style="font-size:12px;margin-top:4px">${esc(t("ふだんは「指定なし」で OK。夜間作業の前と後に 1 回ずつ取って、実行履歴タブで比べたいときだけ「作業前」「作業後」を選びます"))}</div></div>
        <div class="field"><label>${esc(t("状態"))}</label><label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer;min-height:38px"><input type="checkbox" id="sc-enabled" style="width:auto" ${s.enabled ? "checked" : ""}> ${esc(t("有効（オフにすると実行しません）"))}</label></div>
      </div>
    </details>

    <div class="modal-actions">
      <button class="btn" id="sc-cancel-edit">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="sc-save">${esc(t("保存"))}</button>
    </div>
  </div>`);
  openModal(node, "set-edit");

  // ---- ② devices
  const grpSel = node.querySelector("#sc-group");
  const devBox = node.querySelector("#sc-devs");
  let picked = new Set(s.names || (s.group || s.ungrouped ? schedDeviceList(s) : []));
  function renderDevs() {
    const g = grpSel.value;
    const devs = !g ? [] : (INV.devices || []).filter(d => g === RUN_UNGROUPED ? !d.group : d.group === g);
    devBox.innerHTML = devs.length ? devs.map(d => `<label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer;font-size:13px"><input type="checkbox" style="width:auto" data-dev="${esc(d.name)}" ${picked.has(d.name) ? "checked" : ""}> ${esc(d.name)} <span class="muted mono" style="font-size:12px">${esc(d.host || d.serialPort || "")}</span> <span class="muted" style="font-size:12px">${esc(d.commandSet || "")}</span></label>`).join("")
      : `<span class="muted" style="font-size:12px">${esc(t("グループを選ぶと機器が並びます"))}</span>`;
    devBox.querySelectorAll("[data-dev]").forEach(cb => cb.onchange = () => { cb.checked ? picked.add(cb.dataset.dev) : picked.delete(cb.dataset.dev); });
  }
  grpSel.onchange = () => {
    const g = grpSel.value;
    picked = new Set((INV.devices || []).filter(d => (g === RUN_UNGROUPED ? !d.group : d.group === g) && d.enabled).map(d => d.name));
    renderDevs();
  };
  node.querySelector("#sc-all").onclick = () => { devBox.querySelectorAll("[data-dev]").forEach(cb => picked.add(cb.dataset.dev)); renderDevs(); };
  node.querySelector("#sc-none").onclick = () => { picked = new Set(); renderDevs(); };
  renderDevs();

  // ---- ③ commands: a set, or typed lines (picking one side selects it)
  const setSel = node.querySelector("#sc-set"), cmdBox = node.querySelector("#sc-cmds");
  const mode = v => { node.querySelector(`input[name="sc-cmdmode"][value="${v}"]`).checked = true; };
  setSel.addEventListener("focus", () => mode("set"));
  setSel.addEventListener("change", () => mode("set"));
  cmdBox.addEventListener("focus", () => mode("typed"));

  // ---- ① slots
  const slotBox = node.querySelector("#sc-slots");
  function collectSlots() {
    slotBox.querySelectorAll("[data-slot]").forEach(row => {
      const i = +row.dataset.slot;
      const q = sel => row.querySelector(sel);
      slots[i].kind = q(".sl-kind").value;
      // Date and time are kept in slots[i] by their pickers as they change.
      if (q(".sl-start")) slots[i].startNow = q(".sl-start").value === "now";
      if (q(".sl-every")) {
        slots[i].every = parseInt(q(".sl-every").value, 10) || 0;
        slots[i].unit = parseInt(q(".sl-unit").value, 10) || 1;
        slots[i].count = parseInt(q(".sl-count").value, 10) || 0;
      }
      if (q(".sl-wd")) slots[i].weekdays = [...row.querySelectorAll(".sl-wd:checked")].map(x => +x.value);
    });
  }
  const kinds = [["once", "1回だけ"], ["daily", "毎日"], ["weekly", "毎週"], ["interval", "一定間隔で繰り返す"]];
  function renderSlots() {
    slotBox.innerHTML = slots.map((sl, i) => {
      const at = sl.kind === "once" || (sl.kind === "interval" && !sl.startNow);
      return `
      <div data-slot="${i}" class="sl-row">
        <div class="row-inline" style="gap:8px;flex-wrap:wrap">
          <select class="sl-kind btn" style="width:auto">${kinds.map(([k, l]) => `<option value="${k}" ${sl.kind === k ? "selected" : ""}>${esc(t(l))}</option>`).join("")}</select>
          ${sl.kind === "interval" ? `<span class="muted" style="font-size:12px">${esc(t("開始"))}</span><select class="sl-start btn" style="width:auto"><option value="now" ${sl.startNow ? "selected" : ""}>${esc(t("保存したらすぐ"))}</option><option value="at" ${sl.startNow ? "" : "selected"}>${esc(t("日時を指定"))}</option></select>` : ""}
          ${at ? `<span class="sl-date-host" data-i="${i}"></span>` : ""}
          ${sl.kind === "weekly" ? WEEKDAYS.map((w, wi) => `<label class="row-inline" style="gap:3px;font-weight:normal;cursor:pointer;font-size:13px"><input type="checkbox" class="sl-wd" value="${wi}" style="width:auto" ${sl.weekdays.includes(wi) ? "checked" : ""}>${esc(t(w))}</label>`).join("") : ""}
          ${sl.kind !== "interval" || at ? `<span class="sl-time-host" data-i="${i}"></span>` : ""}
          <button class="btn sm act-del" type="button" data-rm-slot="${i}" style="margin-left:auto" title="${esc(t("この日時を削除"))}">×</button>
        </div>
        ${sl.kind === "interval" ? `<div class="row-inline" style="gap:8px;flex-wrap:wrap;margin-top:6px">
          <input class="sl-every" type="number" min="1" value="${sl.every}" style="width:80px"><select class="sl-unit btn" style="width:auto">${[[1, "秒"], [60, "分"], [3600, "時間"]].map(([u, l]) => `<option value="${u}" ${sl.unit === u ? "selected" : ""}>${esc(t(l))}</option>`).join("")}</select><span>${esc(t("おきに"))}</span>
          <input class="sl-count" type="number" min="1" value="${sl.count || ""}" placeholder="${esc(t("無制限"))}" style="width:90px"><span>${esc(t("回"))}</span>
          <span class="muted" style="font-size:12px">${esc(t("（10 秒から。回数が空欄なら止めるまで続けます）"))}</span></div>` : ""}
      </div>`;
    }).join("");
    slotBox.querySelectorAll(".sl-date-host").forEach(host => {
      const sl = slots[+host.dataset.i];
      if (sl.date < todayStr(0)) sl.date = todayStr(0);
      mountDatePicker(host, sl.date, v => { sl.date = v; }, { noPast: true });
    });
    slotBox.querySelectorAll(".sl-time-host").forEach(host => {
      const sl = slots[+host.dataset.i];
      mountTimePicker(host, sl.time, v => { sl.time = v; });
    });
    slotBox.querySelectorAll(".sl-kind, .sl-start").forEach(selEl => selEl.onchange = () => { collectSlots(); renderSlots(); });
    slotBox.querySelectorAll("[data-rm-slot]").forEach(b => b.onclick = () => { collectSlots(); slots.splice(+b.dataset.rmSlot, 1); renderSlots(); });
  }
  renderSlots();
  node.querySelector("#sc-add-slot").onclick = () => { collectSlots(); slots.push({ kind: "daily", date: todayStr(0), time: "03:00:00", weekdays: [], unit: 1, every: 60, count: 0, startNow: true }); renderSlots(); };
  guardDiscard(node, "#sc-cancel-edit");

  node.querySelector("#sc-save").onclick = async () => {
    collectSlots();
    const g = grpSel.value;
    if (!g) { toast(t("② のグループを選んでください"), "err"); return; }
    const names = [...devBox.querySelectorAll("[data-dev]:checked")].map(x => x.dataset.dev);
    if (!names.length) { toast(t("② の機器を 1 台以上チェックしてください"), "err"); return; }
    const typedMode = node.querySelector('input[name="sc-cmdmode"]:checked').value === "typed";
    const commands = typedMode ? cmdBox.value.split(/\r?\n/).map(x => x.trim()).filter(Boolean) : [];
    if (typedMode && !commands.length) { toast(t("③ に実行するコマンドを入力してください"), "err"); return; }
    if (!slots.length) { toast(t("① の日時を 1 つ以上追加してください"), "err"); return; }
    // 保存したらすぐ: the first firing a few seconds from now.
    const soon = new Date(Date.now() + 5000);
    const p2 = n => String(n).padStart(2, "0");
    const outSlots = slots.map(x => {
      const now = x.kind === "interval" && x.startNow;
      return { kind: x.kind,
        date: x.kind === "once" || x.kind === "interval" ? (now ? ymd(soon) : x.date) : "",
        time: now ? `${p2(soon.getHours())}:${p2(soon.getMinutes())}:${p2(soon.getSeconds())}` : x.time,
        weekdays: x.kind === "weekly" ? x.weekdays : [],
        everySeconds: x.kind === "interval" ? x.every * x.unit : 0, everyHours: 0, count: x.kind === "interval" ? x.count : 0 };
    });
    const obj = {
      id: s.id || "", enabled: node.querySelector("#sc-enabled").checked,
      group: g === RUN_UNGROUPED ? "" : g, ungrouped: g === RUN_UNGROUPED, names,
      commandSet: typedMode ? "" : setSel.value, commands,
      stage: node.querySelector("#sc-stage").value, slots: outSlots,
    };
    obj.name = schedAutoName(obj);
    try {
      await App().SaveSchedule(obj);
      closeModal(); toast(t("予約を保存しました"), "ok");
      switchTab("schedule");
    } catch (e) { toast(t("保存できません") + ": " + terr(e), "err"); }
  };
}

// The name a schedule is listed by, made from what it does — nobody has to
// think one up: "lab・show系・毎日 03:00:00".
function schedAutoName(sc) {
  const g = sc.ungrouped ? t("（グループ未設定）") : sc.group;
  const cmd = sc.commands && sc.commands.length ? sc.commands[0] + (sc.commands.length > 1 ? t(" ほか") : "")
    : sc.commandSet || t("機器ごとのセット");
  const when = (sc.slots || []).length ? slotLabel(sc.slots[0]) + (sc.slots.length > 1 ? t(" ほか") : "") : "";
  return `${g}・${cmd}・${when}`;
}

function nextHourStr() {
  const d = new Date(Date.now() + 3600000);
  return `${String(d.getHours()).padStart(2, "0")}:00:00`;
}

// ---- date / time pickers ----
// WebView2 does not open the native popup of <input type="date">, and typing
// into its segments changes only the one under the caret (the day, the
// seconds), which reads as "the date cannot be changed". So: a calendar of
// our own for the date, and 時 / 分 / 秒 drop-downs for the time.

function ymd(d) {
  const p = n => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}
function parseYmd(s) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s || "");
  return m ? new Date(+m[1], +m[2] - 1, +m[3]) : new Date();
}
function dateLabel(s) {
  const d = parseYmd(s);
  return `${s}（${t(WEEKDAYS[d.getDay()])}）`;
}

// mountDatePicker puts a date button in host; a click opens a month
// calendar under it. onChange gets "YYYY-MM-DD". noPast greys out days
// before today (a once slot cannot be in the past).
function mountDatePicker(host, value, onChange, opts) {
  opts = opts || {};
  let cur = value || ymd(new Date());
  host.classList.add("dp-wrap");
  host.innerHTML = `<button type="button" class="btn dp-btn">📅 <span class="dp-val"></span></button>`;
  const btn = host.querySelector(".dp-btn");
  const set = v => { cur = v; host.querySelector(".dp-val").textContent = dateLabel(v); onChange(v); };
  host.querySelector(".dp-val").textContent = dateLabel(cur);
  let pop = null;
  const close = () => {
    if (!pop) return;
    pop.remove(); pop = null;
    document.removeEventListener("mousedown", outside, true);
    window.removeEventListener("resize", close);
    document.removeEventListener("scroll", close, true);
  };
  const outside = e => { if (pop && !host.contains(e.target) && !pop.contains(e.target)) close(); };
  // Fixed to the window, under the button — or above it when the window
  // has no room below (inside a dialog it was cut off at the bottom).
  const place = () => {
    const r = btn.getBoundingClientRect(), ph = pop.offsetHeight, pw = pop.offsetWidth;
    const below = r.bottom + 4 + ph <= window.innerHeight - 8;
    pop.style.top = (below ? r.bottom + 4 : Math.max(8, r.top - 4 - ph)) + "px";
    pop.style.left = Math.min(r.left, window.innerWidth - pw - 8) + "px";
  };
  btn.onclick = () => {
    if (pop) { close(); return; }
    let view = parseYmd(cur); view.setDate(1);
    pop = document.createElement("div");
    pop.className = "dp-pop";
    document.body.appendChild(pop);
    const today = ymd(new Date());
    const draw = () => {
      const y = view.getFullYear(), m = view.getMonth();
      const first = new Date(y, m, 1).getDay(), days = new Date(y, m + 1, 0).getDate();
      let cells = "";
      for (let i = 0; i < first; i++) cells += `<span></span>`;
      for (let d = 1; d <= days; d++) {
        const v = ymd(new Date(y, m, d));
        const past = opts.noPast && v < today;
        cells += `<button type="button" class="dp-day${v === cur ? " sel" : ""}${v === today ? " today" : ""}" data-v="${v}" ${past ? "disabled" : ""}>${d}</button>`;
      }
      pop.innerHTML = `
        <div class="dp-head"><button type="button" class="btn sm" data-mv="-1">‹</button><b>${esc(t("{y}年{m}月", { y, m: m + 1 }))}</b><button type="button" class="btn sm" data-mv="1">›</button></div>
        <div class="dp-grid">${WEEKDAYS.map(w => `<span class="dp-wd">${esc(t(w))}</span>`).join("")}${cells}</div>
        <div class="dp-foot"><button type="button" class="btn sm" data-q="0">${esc(t("今日"))}</button><button type="button" class="btn sm" data-q="1">${esc(t("明日"))}</button></div>`;
      pop.querySelectorAll("[data-mv]").forEach(b => b.onclick = () => { view.setMonth(view.getMonth() + +b.dataset.mv); draw(); });
      pop.querySelectorAll(".dp-day").forEach(b => b.onclick = () => { set(b.dataset.v); close(); });
      pop.querySelectorAll("[data-q]").forEach(b => b.onclick = () => { set(ymd(new Date(Date.now() + +b.dataset.q * 86400000))); close(); });
    };
    draw();
    place();
    document.addEventListener("mousedown", outside, true);
    window.addEventListener("resize", close);
    // Scrolling the dialog would leave the calendar behind: close it.
    setTimeout(() => document.addEventListener("scroll", close, true), 0);
  };
}

// mountTimePicker: 時 / 分 / 秒 drop-downs; onChange gets "HH:MM:SS".
function mountTimePicker(host, value, onChange) {
  const [h, m, s] = (value || "03:00:00").split(":").map(x => parseInt(x, 10) || 0);
  const opt = (n, sel) => Array.from({ length: n }, (_, i) => `<option value="${i}" ${i === sel ? "selected" : ""}>${String(i).padStart(2, "0")}</option>`).join("");
  host.classList.add("tp-wrap");
  host.innerHTML = `<select class="btn tp-h">${opt(24, h)}</select><span>${esc(t("時"))}</span><select class="btn tp-m">${opt(60, m)}</select><span>${esc(t("分"))}</span><select class="btn tp-s">${opt(60, s)}</select><span>${esc(t("秒"))}</span>`;
  const emit = () => {
    const p = sel => String(host.querySelector(sel).value).padStart(2, "0");
    onChange(`${p(".tp-h")}:${p(".tp-m")}:${p(".tp-s")}`);
  };
  host.querySelectorAll("select").forEach(x => x.onchange = emit);
}

// The run tab's ⏰ 予約…: a new schedule with the run tab's current choices.
function scheduleFromRun() {
  if (!runGroupSel) { toast(t("先にグループを選んでください"), "err"); return; }
  const names = runTargetDevices().map(d => d.name);
  editSchedule(null, {
    group: runGroupSel === RUN_UNGROUPED ? "" : runGroupSel, ungrouped: runGroupSel === RUN_UNGROUPED,
    names: names.length ? names : null,
    commandSet: runSetSel && runSetSel !== RUN_ADHOC ? runSetSel : "", stage: runStage,
    commands: runSetSel === RUN_ADHOC ? runAdHocCommands().map(c => c.text) : [],
  });
}

wireScheduleEvents();
