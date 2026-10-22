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
  if (s.kind === "weekly") return t("毎週 {w} {t}", { w: (s.weekdays || []).map(w => t(WEEKDAYS[w])).join(t("・")), t: tm });
  if (s.kind === "interval") {
    const c = s.count ? t("{n}回", { n: s.count }) : s.until ? t("{u} まで", { u: s.until }) : t("止めるまで");
    // startNow: in the save confirmation, before the start time is taken.
    if (s.startNow) return t("{p}おき・{c}（保存したらすぐ開始）", { p: fmtPeriod(slotPeriodSec(s)), c });
    return t("{p}おき・{c}（{d} {t} から）", { p: fmtPeriod(slotPeriodSec(s)), c, d: s.date || "", t: tm });
  }
  return s.kind;
}

// The devices a schedule runs on, as names (a schedule from v1.6.0–1 may
// have no list: then the group's checked devices, as it ran then).
function schedDeviceList(sc) {
  // A named device turned GUI-only since is not run (snapshotOf skips it).
  if (sc.names) {
    // Names are unique within a group only: the GUI-only devices of this group.
    const off = new Set((INV.devices || []).filter(d => d.cliOff && (sc.ungrouped ? !d.group : d.group === sc.group)).map(d => d.name));
    return sc.names.filter(n => !off.has(n));
  }
  return (INV.devices || []).filter(d => (sc.ungrouped ? !d.group : d.group === sc.group) && d.enabled && !d.cliOff).map(d => d.name);
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
  return t("機器ごとの設定：{s}", { s: sets.join(t("、")) || t("なし") });
}

function schedKindLabel(e) {
  return ({ run: t("実行"), missed: t("見逃し"), skipped: t("スキップ"), refused: t("拒否"), error: t("エラー"), stopped: t("停止") })[e.kind] || e.kind;
}

let schedRenderGen = 0;
// The tab shows one list at a time — the schedules or their 実行記録 — in
// a panel that fills the window like every other tab's list (its own
// scroll, header row fixed, columns resizable, sort and filter).
const schedTab = { view: "list", data: null };
async function renderSchedules() {
  const root = document.getElementById("tab-schedule");
  // Draws overlap (an event, a button): only the newest one is shown, not
  // whichever answer came back last.
  const gen = ++schedRenderGen;
  let list = [], st = {}, log = [];
  try {
    [list, st, log] = await Promise.all([App().ListSchedules(), App().GetScheduleStatus(), App().ScheduleLog(100)]);
  } catch (e) {
    if (gen === schedRenderGen) root.innerHTML = pageError(t("予約"), t("読み込み失敗") + ": " + terr(e));
    return;
  }
  if (gen !== schedRenderGen) return;
  schedTab.data = { list: list || [], st: st || {}, log: log || [] };
  renderSchedulesView();
}

// Draws the tab from the data already read: a sort, filter or switch of
// list redraws without asking the backend again.
function renderSchedulesView() {
  const root = document.getElementById("tab-schedule");
  if (!schedTab.data) return;
  const { list, st, log } = schedTab.data;
  // A redraw (a repeat every 10 seconds sends one each round) keeps the
  // list where the user scrolled it.
  const oldPanel = root.querySelector(".panel.scrollx");
  const keepScroll = oldPanel && oldPanel.dataset.view === schedTab.view ? oldPanel.scrollTop : 0;
  const focus = listViewFocus();
  // Will a 予約 run on this PC while it sleeps? Only if Windows lets an app
  // wake it (wake timers allowed for the current power source) and the PC
  // is not modern standby (which pauses apps). Otherwise the answer is the
  // keep-awake check box below — said in plain words, details folded away.
  const pw = st.power || {};
  const timers = pw.onAC === false ? pw.wakeTimersDC : pw.wakeTimersAC;
  // The PC's own settings decide it — not whether a wake-up happens to be
  // armed right now (with nothing scheduled there is none).
  const canWake = st.wake !== "unsupported" && timers !== 0 && !pw.modernStandby;
  const keep = !!st.keepAwake;
  const wtName = v => v === 0 ? t("無効") : v === 1 ? t("有効") : v === 2 ? t("重要なスリープ解除タイマーのみ") : t("不明");
  // One line on what to expect. It describes this PC (it is not a PalaTerm
  // setting, and nothing the user clicked changed it) — so no red cross.
  const sleepLine = canWake ? t("✅ PalaTerm を起動していれば、画面ロック中もスリープ中も動きます")
    : keep ? t("✅ PalaTerm を起動していれば動きます（自分でスリープ・蓋を閉じたときは止まります）")
    : t("ⓘ この PC は、スリープすると予約が止まります");
  // A schedule's state in words: what it is doing and whether it will run
  // again — a bare check box said none of that.
  const finished = sc => !sc.next && !sc.running && !sc.queued && sc.enabled;
  const stateBadge = sc => sc.running ? `<span class="badge sc-st run">${esc(t("実行中"))}</span>`
    : sc.queued ? `<span class="badge sc-st wait">${esc(t("順番待ち"))}</span>`
    : !sc.enabled ? `<span class="badge sc-st off">${esc(t("停止中"))}</span>`
    : finished(sc) ? `<span class="badge sc-st done">${esc(t("終了"))}</span>`
    : `<span class="badge sc-st on">${esc(t("有効"))}</span>`;
  const nextText = sc => !sc.enabled ? t("—（停止中）") : sc.next ? fmtSchedTime(sc.next) + (sc.remaining > 0 ? t("（あと {n} 回）", { n: sc.remaining }) : sc.remaining === -1 && (sc.slots || [])[0] && sc.slots[0].kind === "interval" ? t("（止めるまで）") : "") : t("—（終了）");
  const stateWord = sc => sc.running ? t("実行中") : sc.queued ? t("順番待ち") : !sc.enabled ? t("停止中") : finished(sc) ? t("終了") : t("有効");
  const LV = "schedules", LG = "schedlog";
  const shownList = listViewApply(LV, list,
    sc => [stateWord(sc), schedDevicesLabel(sc), schedCommandsLabel(sc), ...(sc.slots || []).map(slotLabel), nextText(sc)],
    (sc, k) => k === "state" ? stateWord(sc) : k === "when" ? (sc.next || "~") : schedDevicesLabel(sc),
    { state: stateWord });
  const shownLog = listViewApply(LG, log,
    e => [fmtSchedTime(e.due), e.schedule, schedKindLabel(e), terr(e.message || "")],
    (e, k) => k === "due" ? (e.due || "") : k === "kind" ? schedKindLabel(e) : k === "result" ? (e.kind === "run" ? (e.failed || 0) * 100000 + (e.ok || 0) : -1) : (e.schedule || ""),
    { kind: schedKindLabel });
  // Its newest 実行記録 line, when that went wrong: a mark under the state,
  // the line itself as the tip, a click opens the 実行記録.
  const lastMark = sc => {
    const e = sc.lastProblem;
    if (!e) return "";
    const what = e.kind === "run" ? (e.failed ? t("失敗 {n} 台", { n: e.failed }) : t("外れた機器あり")) : schedKindLabel(e);
    const tip = [fmtSchedTime(e.due), what, terr(e.message || "")].filter(Boolean).join(t("　"));
    return `<div class="link sc-last" data-lastlog="1" style="font-size:12px;margin-top:4px;color:${e.kind === "missed" || e.kind === "skipped" || (e.kind === "run" && !e.failed) ? "var(--warn)" : "var(--err)"};text-decoration:none" data-tip="${esc(tip)}">${esc(t("⚠ 直近: {k}", { k: what }))}</div>`;
  };
  const rows = shownList.map(sc => `
    <tr class="${!sc.enabled || finished(sc) ? "sc-dim" : ""}">
      <td style="width:90px">${stateBadge(sc)}${lastMark(sc)}</td>
      <td><div style="font-size:13px;line-height:1.6"><span class="muted">${esc(t("機器"))}</span>${t("　")}${esc(schedDevicesLabel(sc))}${sc.stage ? " " + stageBadge(sc.stage) : ""}</div>
        <div style="font-size:13px;line-height:1.6"><span class="muted">${esc(t("コマンド"))}</span>${t("　")}${esc(schedCommandsLabel(sc))}</div></td>
      <td><div style="font-size:12px;line-height:1.6">${(sc.slots || []).map(x => esc(slotLabel(x))).join("<br>")}</div>
        <div style="font-size:12px;margin-top:4px"><span class="muted">${esc(t("次の実行"))}</span>${t("　")}<span class="mono">${esc(nextText(sc))}</span></div></td>
      <td class="ops" style="width:300px"><div class="ops-btns">
        ${finished(sc) ? "" : `<button class="btn sm" data-now="${esc(sc.id)}">${esc(t("今すぐ実行"))}</button>`}
        ${finished(sc) ? "" : sc.enabled ? `<button class="btn sm" data-stop="${esc(sc.id)}">${esc(t("⏸ 止める"))}</button>` : `<button class="btn sm" data-resume="${esc(sc.id)}">${esc(t("▶ 再開"))}</button>`}
        <button class="btn sm act-edit" data-edit="${esc(sc.id)}">${esc(t("編集"))}</button>
        <button class="btn sm act-copy" data-dup="${esc(sc.id)}" data-tip="${esc(t("同じ日時・機器・コマンドで新しい予約を作ります（別のグループに同じ予約を、など）"))}">${esc(t("複製"))}</button>
        <button class="btn sm act-del" data-del="${esc(sc.id)}">${esc(t("削除"))}</button>
      </div></td>
    </tr>`).join("");
  const nFinished = list.filter(finished).length;
  const logRows = shownLog.map(e => `
    <tr>
      <td class="mono" style="width:170px">${esc(fmtSchedTime(e.due))}</td>
      <td><div class="clip" data-tip="${esc(e.schedule)}">${esc(e.schedule)}${e.manual ? ` <span class="badge">${esc(t("今すぐ"))}</span>` : ""}</div></td>
      <td style="width:90px"><span class="dot st-${e.kind === "run" ? (e.failed ? "error" : "done") : e.kind === "missed" || e.kind === "skipped" || e.kind === "stopped" ? "canceled" : "error"}"></span>${esc(schedKindLabel(e))}</td>
      <td style="width:120px">${e.kind === "run" ? `<span style="color:var(--ok)">${e.ok || 0}</span> / <span style="color:var(--err)">${e.failed || 0}</span>` : ""}</td>
      <td class="muted"><div class="clip" data-tip="${esc(terr(e.message || ""))}">${esc(terr(e.message || ""))}</div></td>
      <td style="text-align:right"><div class="clip">${(e.logs || []).map(l => `<span class="link" data-log="${esc(l.path)}" data-tip="${esc(t("{d} のログを表示", { d: l.device }))}">${esc((e.logs.length > 1 ? l.device + " " : "") + t("ログ表示"))}</span>`).join(t("　"))}</div></td>
    </tr>`).join("");
  renderListTab(root, `
    <div class="page-head">
      <div><div class="page-title">${esc(t("予約"))}</div>
        <div class="page-sub">${esc(t("決まった時刻に自動で実行して、ログを取ります"))}</div></div>
      <div class="row-inline"><button class="btn primary" id="sc-add">${esc(t("＋ 予約を追加"))}</button></div>
    </div>
    <div class="panel" style="padding:14px 18px;margin-bottom:16px;font-size:13px;line-height:1.9">
      ${st.running ? `<div style="color:var(--run)"><b>${esc(t("▶ 実行中: {n}", { n: st.running }))}</b> ${esc(t("（{d} / {t} 台）", { d: st.done, t: st.total }))} <button class="btn sm" id="sc-cancel">${esc(t("■ 中止"))}</button></div>` : ""}
      ${st.queued ? `<div style="color:var(--warn)">${esc(t("順番待ち: {n} 件（ほかの一括実行が終わりしだい実行。30 分を過ぎたらスキップ）", { n: st.queued }))}</div>` : ""}
      <div>${esc(sleepLine)}</div>
      ${canWake ? "" : `<label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer;margin-top:2px"><input type="checkbox" id="sc-keepawake" style="width:auto" ${keep ? "checked" : ""}> ${esc(t("放置してもスリープさせない"))}<span class="muted" style="font-size:12px">${esc(t("（画面ロック・画面オフはそのまま）"))}</span></label>`}
      <div style="margin-top:4px">${esc(t("次の実行"))}${t("：")}<b class="mono">${st.next ? esc(fmtSchedTime(st.next)) : "—"}</b>${st.next && st.nextName ? `<span class="muted" style="font-size:12px">${t("　")}${esc(st.nextName)}</span>` : ""}</div>
      <details style="margin-top:2px"><summary class="link" style="text-decoration:none;font-size:12px">${esc(t("詳しく"))}</summary>
        <div class="muted" style="font-size:12px;line-height:1.8;margin-top:6px">
        ${esc(t("・× で閉じてもトレイで動き続けます。トレイの「終了」で止まります"))}<br>
        ${esc(t("・設定を変えるコマンド（conf t・write など）は予約できません"))}<br>
        ${esc(t("・スリープなどで実行できなかった回は「見逃し」と記録します"))}<br>
        ${esc(t("・「放置してもスリープさせない」は Windows の設定を変えません（PalaTerm 終了で元どおり）"))}<br>
        ${esc(t("・この PC：スリープ解除タイマー 電源接続時 {ac}／バッテリー {dc}・{m}", { ac: wtName(pw.wakeTimersAC), dc: wtName(pw.wakeTimersDC), m: pw.modernStandby ? t("モダンスタンバイ") : t("従来型スリープ") }))}</div></details>
    </div>
    <div class="row-inline" style="gap:6px;margin-bottom:12px">
      <button class="btn" data-scview="list" style="${schedTab.view === "list" ? "border-color:var(--accent);font-weight:600" : ""}">${esc(t("予約一覧（{n}）", { n: list.length }))}</button>
      <button class="btn" data-scview="log" style="${schedTab.view === "log" ? "border-color:var(--accent);font-weight:600" : ""}">${esc(t("予約の実行記録（{n}）", { n: log.length }))}</button>
      <span style="margin-left:auto"></span>
      ${schedTab.view === "list" && nFinished ? `<span class="link" id="sc-del-finished" style="font-size:12px;margin-right:8px">${esc(t("終了した予約（{n} 件）をまとめて削除", { n: nFinished }))}</span>` : ""}
      <button class="btn" id="sc-logdir" data-tip="${esc(t("ログの保存先フォルダを開きます（予約のログは、予約ごとのフォルダに入ります）"))}">${esc(t("ログフォルダを開く"))}</button>
    </div>
    ${schedTab.view === "list" ? `
    ${list.length ? listViewBar(LV, t("絞り込み（機器・コマンド・日時）"), [{ id: "state", label: t("状態: すべて"), values: [...new Set(list.map(stateWord))] }]) : ""}
    <div class="panel scrollx" data-view="list">
      ${!list.length ? `<div class="empty">${esc(t("予約はありません。「＋ 予約を追加」から作れます"))}</div>`
        : !shownList.length ? `<div class="empty">${esc(t("絞り込みに一致する予約がありません"))}</div>`
        : `<table class="fixed" data-colw="schedules"><thead><tr>${listViewTh(LV, "state", t("状態"), "width:90px")}${listViewTh(LV, "target", t("実行する機器・コマンド"))}${listViewTh(LV, "when", t("日時"), "width:320px")}<th style="width:300px"></th></tr></thead><tbody>${rows}</tbody></table>`}
    </div>` : `
    ${log.length ? listViewBar(LG, t("絞り込み（予約・メッセージ）"), [{ id: "kind", label: t("結果: すべて"), values: [...new Set(log.map(schedKindLabel))] }]) : ""}
    <div class="panel scrollx" data-view="log">
      ${!log.length ? `<div class="empty">${esc(t("まだ記録はありません"))}</div>`
        : !shownLog.length ? `<div class="empty">${esc(t("絞り込みに一致する記録がありません"))}</div>`
        : `<table class="fixed" data-colw="schedlog"><thead><tr>${listViewTh(LG, "due", t("予定日時"), "width:170px")}${listViewTh(LG, "schedule", t("予約"), "width:200px")}${listViewTh(LG, "kind", t("結果"), "width:90px")}${listViewTh(LG, "result", t("成功 / 失敗"), "width:120px")}<th>${esc(t("メッセージ"))}</th><th style="width:220px"></th></tr></thead><tbody>${logRows}</tbody></table>`}
    </div>`}`);

  listViewWire(LV, root, renderSchedulesView, focus);
  listViewWire(LG, root, renderSchedulesView, focus);
  wireColResize(root);
  const panel = root.querySelector(".panel.scrollx");
  if (panel && keepScroll) panel.scrollTop = keepScroll;
  root.querySelectorAll("[data-scview]").forEach(b => b.onclick = () => { schedTab.view = b.dataset.scview; renderSchedulesView(); });
  root.querySelectorAll("[data-lastlog]").forEach(b => b.onclick = () => { schedTab.view = "log"; renderSchedulesView(); });
  root.querySelector("#sc-add").onclick = () => editSchedule(null);
  root.querySelector("#sc-logdir").onclick = () => App().OpenLogDir().catch(e => toast(terr(e), "err"));
  const ka = root.querySelector("#sc-keepawake");
  if (ka) ka.onchange = async e => {
    try {
      await App().SetScheduleKeepAwake(e.target.checked);
      toast(e.target.checked ? t("オンにしました（予約がある間、放置してもスリープしません）") : t("オフにしました（元どおりスリープします）"), "ok");
      renderSchedules();
    } catch (err) { toast(terr(err), "err"); e.target.checked = !e.target.checked; }
  };
  const cancel = root.querySelector("#sc-cancel");
  if (cancel) cancel.onclick = async () => {
    if (await uiConfirm({ title: t("予約の実行を中止"), message: t("実行中の予約「{n}」を中止しますか？", { n: esc(st.running) }), okLabel: t("中止する"), danger: true })) App().CancelScheduledRun();
  };
  root.querySelectorAll("[data-stop]").forEach(b => b.onclick = async () => {
    const sc = list.find(x => x.id === b.dataset.stop);
    if (sc && sc.running && !(await uiConfirm({ title: t("予約を止める"), message: t("この予約は今実行中です。止めると、実行中の回も中止します。よろしいですか？"), okLabel: t("⏸ 止める"), danger: true }))) return;
    try { await App().SetScheduleEnabled(b.dataset.stop, false); toast(t("止めました（「▶ 再開」でまた動きます）"), "ok"); }
    catch (e) { toast(t("止められません") + ": " + terr(e), "err"); }
    renderSchedules();
  });
  root.querySelectorAll("[data-resume]").forEach(b => b.onclick = async () => {
    try { await App().SetScheduleEnabled(b.dataset.resume, true); toast(t("再開しました"), "ok"); }
    catch (e) { toast(t("再開できません") + ": " + terr(e), "err"); }
    renderSchedules();
  });
  const delFin = root.querySelector("#sc-del-finished");
  if (delFin) delFin.onclick = async () => {
    if (!(await uiConfirm({ title: t("終了した予約を削除"), message: t("もう実行されない予約（{n} 件）を一覧から削除します。実行記録とログは残ります。", { n: nFinished }), okLabel: t("削除"), danger: true }))) return;
    try { const n = await App().DeleteFinishedSchedules(); toast(t("{n} 件削除しました", { n }), "ok"); }
    catch (e) { toast(t("削除失敗") + ": " + terr(e), "err"); }
    renderSchedules();
  };
  root.querySelectorAll("[data-now]").forEach(b => b.onclick = async () => {
    const sc = list.find(x => x.id === b.dataset.now);
    if (!(await uiConfirm({ title: t("今すぐ実行"), message: t("予約「{n}」を今すぐ 1 回実行しますか？（ほかの一括実行中なら終わってから）", { n: esc(sc ? sc.name : "") }), okLabel: t("▶ 実行") }))) return;
    try { await App().RunScheduleNow(b.dataset.now); toast(t("実行を予約しました"), "ok"); }
    catch (e) { toast(t("実行できません") + ": " + terr(e), "err"); }
  });
  root.querySelectorAll("[data-edit]").forEach(b => b.onclick = () => editSchedule(list.find(x => x.id === b.dataset.edit)));
  // 複製: a new schedule prefilled from this one (its slots as saved, its
  // devices, its commands); nothing is written until 保存.
  root.querySelectorAll("[data-dup]").forEach(b => b.onclick = () => {
    const sc = list.find(x => x.id === b.dataset.dup);
    if (!sc) return;
    editSchedule(null, { group: sc.group || "", ungrouped: !!sc.ungrouped, names: sc.names ? sc.names.slice() : null,
      commandSet: sc.commandSet || "", commands: (sc.commands || []).slice(), stage: sc.stage || "", slots: JSON.parse(JSON.stringify(sc.slots || [])) });
  });
  root.querySelectorAll("[data-del]").forEach(b => b.onclick = async () => {
    const sc = list.find(x => x.id === b.dataset.del);
    if (!(await uiConfirm({ title: t("予約を削除"), message: t("予約「<b>{n}</b>」を削除しますか？", { n: esc(sc ? sc.name : "") }), okLabel: t("削除"), danger: true }))) return;
    try { await App().DeleteSchedule(b.dataset.del); renderSchedules(); toast(t("削除しました"), "ok"); }
    catch (e) { toast(t("削除失敗") + ": " + terr(e), "err"); }
  });
  root.querySelectorAll("[data-log]").forEach(b => b.onclick = () => showLog(b.dataset.log));
}

// Re-draw the tab when the scheduler reports a change (a run starting or
// ending, a log line) — at most a few times a second.
function wireScheduleEvents() {
  rt().EventsOn("sched:changed", () => {
    if (!document.querySelector("#tab-schedule.active") || document.querySelector("#modal-overlay:not(.hidden)")) return;
    clearTimeout(schedRenderTimer);
    schedRenderTimer = setTimeout(renderSchedules, 250);
  });
  // Changes skipped while a dialog was open (a run ending while the editor
  // was up) show once it closes, however it closed.
  document.addEventListener("modal-closed", () => {
    if (!document.querySelector("#tab-schedule.active")) return;
    clearTimeout(schedRenderTimer);
    schedRenderTimer = setTimeout(renderSchedules, 50);
  });
  rt().EventsOn("sched:done", e => {
    if (e.repeat && !e.last && !e.failed) return;
    const missing = (e.missing || []).length ? " " + t("見つからない機器を外しました: {s}", { s: e.missing.join(", ") }) : "";
    toast((e.repeat && e.last ? t("繰り返しの予約が終わりました（成功 {a}・失敗 {b}）", { a: e.ok || 0, b: e.failed || 0 })
      : t("予約を実行しました（成功 {a}・失敗 {b}）", { a: e.ok || 0, b: e.failed || 0 })) + missing, e.failed ? "err" : missing ? "warn" : "ok");
  });
  // A 予約 that did not run as planned (missed, skipped, refused, error,
  // stopped — the backend tells each once in a while, not every round).
  // Gathered for a moment: an unlock can bring several at once, and the
  // action that stopped a schedule (a device deleted) shows its own toast
  // first — this one follows it instead of being wiped by it.
  let pend = [], timer = 0;
  rt().EventsOn("sched:problem", e => {
    pend.push(e);
    clearTimeout(timer);
    timer = setTimeout(() => {
      const list = pend; pend = [];
      const bad = list.some(x => x.kind === "error" || x.kind === "refused" || x.kind === "stopped");
      let msg;
      if (list.length === 1 && list[0].schedule) {
        const x = list[0];
        msg = t("予約「{n}」: {k}", { n: x.schedule, k: schedKindLabel(x) }) + (x.message ? " — " + terr(x.message) : "");
      } else {
        const counts = {};
        list.forEach(x => { const k = schedKindLabel(x); counts[k] = (counts[k] || 0) + 1; });
        msg = t("予約が予定どおりに動かなかった記録が {n} 件あります（{s}）。「予約の実行記録」で確かめてください", { n: list.length, s: Object.entries(counts).map(([k, n]) => `${k} ${n}`).join(t("・")) });
      }
      toast(msg, bad ? "err" : "warn");
      if (document.querySelector("#tab-schedule.active") && !document.querySelector("#modal-overlay:not(.hidden)")) renderSchedules();
    }, 800);
  });
}

function todayStr(addDays) {
  const d = new Date(Date.now() + (addDays || 0) * 86400000);
  const p = n => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// The 予約 editor, in the order a user thinks of it: when, on which
// devices, what. No name to invent: one is made from those three. One
// schedule is one rule of time (another time is another schedule — a
// second rule inside one schedule read as "a second schedule" and confused).
// prefill (a copy of an existing schedule, say) fills the choices: group,
// devices, command set or typed commands, stage.
//
// What people schedule, and what the defaults are for:
//   - a nightly / weekly backup (毎日 03:00, a command set);
//   - watching something from now on during trouble or work (一定間隔,
//     start now, every N seconds, ending after N times or at a time —
//     never silently forever: "止めるまで" is a choice of its own, and the
//     list has ⏸ 止める);
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
    // nightly run. A copy of a schedule brings its own slots.
    slots: (pre.slots && pre.slots.length) ? pre.slots
      : [typed.length ? { kind: "interval", startNow: true, everySeconds: 60, unit: 1, count: 10 } : { kind: "daily", time: "03:00:00" }],
  };
  const unitOf = sec => sec && sec % 3600 === 0 ? 3600 : sec && sec % 60 === 0 ? 60 : 1;
  // The default end time is a whole hour at least an hour away (an hour from
  // 10:59:58 rounded down was 11:00:00 — before a start 5 seconds away).
  const later = new Date(Date.now() + 2 * 3600000);
  let slots = (s.slots || []).map(x => {
    const sec = slotPeriodSec(x) || 60;
    // A new slot says "60 秒" (typing 10 then means 10 seconds); a saved
    // one shows its period in the largest whole unit.
    const unit = x.unit || unitOf(sec);
    const [ud, ut] = (x.until || "").split(" ");
    return { kind: x.kind, date: x.date || todayStr(0), time: x.time || nextHourStr(), weekdays: x.weekdays || [],
      // A copy (prefill) of an interval schedule that already started: the
      // copy starts when saved (its old start is in the past and would be
      // refused).
      unit, every: sec / unit, count: x.count || 10, startNow: !!x.startNow || (!sc && !!prefill && x.kind === "interval" && !!x.date && `${x.date} ${x.time || ""}` < `${todayStr(0)} ${new Date().toTimeString().slice(0, 8)}`),
      // How a repeat ends: after N times (the default), at a time, or when
      // stopped from the list — a copy keeps the original's choice.
      end: x.count ? "count" : x.until ? "until" : ((sc || prefill) && x.kind === "interval" ? "never" : "count"),
      untilDate: ud || ymd(later), untilTime: ut || `${String(later.getHours()).padStart(2, "0")}:00:00`,
      // What a saved slot said: a repeat that began yesterday keeps its
      // start (moving it to today restarted the count and the clock).
      orig: sc ? { date: x.date || "", time: x.time || "", untilDate: ud || "" } : null };
  });
  const grpVal = s.ungrouped ? RUN_UNGROUPED : (s.group || "");
  const node = h(`<div>
    <h3>${esc(sc ? t("予約を編集") : t("予約を追加"))}</h3>

    <div class="field"><label>${esc(t("① いつ実行しますか"))}</label>
      <div id="sc-slots"></div></div>

    <div class="field"><label>${esc(t("② どの機器で実行しますか"))}</label>
      <div class="row-inline" style="gap:8px;flex-wrap:wrap;margin-bottom:6px">
        <select id="sc-group" style="width:auto;min-width:200px">
          <option value="">${esc(t("グループを選択"))}</option>
          ${groups.map(g => `<option value="${esc(g.name)}" ${g.name === grpVal ? "selected" : ""}>${esc(g.name)}</option>`).join("")}
          ${ungroupedN ? `<option value="${RUN_UNGROUPED}" ${grpVal === RUN_UNGROUPED ? "selected" : ""}>${esc(t("（グループ未設定）"))}</option>` : ""}
        </select>
        <input id="sc-filter" data-guard-skip placeholder="${esc(t("絞り込み（機器名・IP・拠点・役割）"))}" style="flex:1;min-width:200px">
      </div>
      <div class="row-inline" style="gap:12px;flex-wrap:wrap;margin-bottom:4px;font-size:12px">
        <span class="link" id="sc-all">${esc(t("表示中をすべて選択"))}</span>
        <span class="link" id="sc-none">${esc(t("表示中をすべて解除"))}</span>
        <label class="row-inline" style="gap:4px;font-weight:normal;cursor:pointer"><input type="checkbox" id="sc-onlysel" data-guard-skip style="width:auto"> ${esc(t("選択中だけ表示"))}</label>
        <b id="sc-count" style="margin-left:auto"></b>
      </div>
      <div id="sc-devs" class="panel sc-devs"></div>
      <div class="muted" style="font-size:11px;margin-top:2px">${esc(t("Shift＋クリックでまとめて選べます"))}</div></div>

    <div class="field"><label>${esc(t("③ 何を実行しますか"))}</label>
      <label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer"><input type="radio" name="sc-cmdmode" value="set" style="width:auto" ${typed.length ? "" : "checked"}> ${esc(t("コマンドセットから選ぶ"))}</label>
      <select id="sc-set" style="margin:4px 0 8px 26px;width:calc(100% - 26px)">
        <option value="">${esc(t("機器ごとに割り当てたコマンドセット"))}</option>
        ${(INV.commandSets || []).map(cs => `<option value="${esc(cs.name)}" ${cs.name === s.commandSet ? "selected" : ""}>${esc(cs.name)}</option>`).join("")}
      </select>
      <label class="row-inline" style="gap:8px;font-weight:normal;cursor:pointer"><input type="radio" name="sc-cmdmode" value="typed" style="width:auto" ${typed.length ? "checked" : ""}> ${esc(t("コマンドを直接入力する（1 行に 1 コマンド）"))}</label>
      <textarea id="sc-cmds" class="mono" rows="3" style="margin:4px 0 0 26px;width:calc(100% - 26px);font-size:13px" placeholder="show interface status&#10;show logging | include %LINK">${esc(typed.join("\n"))}</textarea>
      <div class="muted" style="font-size:12px;margin:4px 0 0 26px">${esc(t("※ conf t・write など、設定を変えるコマンドは使えません"))}</div></div>

    <details id="sc-more" style="margin:4px 0 8px" ${s.stage ? "open" : ""}><summary class="link" style="text-decoration:none">${esc(t("詳細設定（ふだんは変更不要）"))}</summary>
      <div class="field" style="margin-top:8px;max-width:420px"><label>${esc(t("作業タイミング"))}</label><select id="sc-stage">
        ${STAGES.map(x => `<option value="${x}" ${x === (s.stage || "") ? "selected" : ""}>${esc(x ? stageLabel(x) : t("指定なし"))}</option>`).join("")}
      </select>
      <div class="muted" style="font-size:12px;margin-top:4px">${esc(t("作業の前後のログを比べたいときだけ選びます"))}</div></div>
    </details>

    <div class="modal-actions">
      <button class="btn" id="sc-cancel-edit">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="sc-save" data-tip="Ctrl+Enter">${esc(t("保存"))}</button>
    </div>
  </div>`);
  openModal(node, "set-edit");

  // ---- ② devices: filter, select the shown ones, count, Shift+click range
  const grpSel = node.querySelector("#sc-group");
  const devBox = node.querySelector("#sc-devs");
  const filterIn = node.querySelector("#sc-filter"), onlySel = node.querySelector("#sc-onlysel");
  let picked = new Set(s.names || (s.group || s.ungrouped ? schedDeviceList(s) : []));
  let lastClick = -1;
  const groupDevs = () => {
    const g = grpSel.value;
    return !g ? [] : (INV.devices || []).filter(d => (g === RUN_UNGROUPED ? !d.group : d.group === g) && !d.cliOff);
  };
  const shownDevs = () => {
    const q = filterIn.value.trim().toLowerCase();
    return groupDevs().filter(d => (!q || [d.name, d.host, d.serialPort, d.site, d.role, d.commandSet].some(v => String(v || "").toLowerCase().includes(q)))
      && (!onlySel.checked || picked.has(d.name)));
  };
  function renderDevs() {
    const all = groupDevs(), shown = shownDevs();
    node.querySelector("#sc-count").textContent = grpSel.value ? t("選択中 {a} / {b} 台", { a: all.filter(d => picked.has(d.name)).length, b: all.length }) : "";
    devBox.innerHTML = !grpSel.value ? `<div class="muted" style="font-size:12px;padding:6px">${esc(t("グループを選ぶと機器が並びます"))}</div>`
      : !shown.length ? `<div class="muted" style="font-size:12px;padding:6px">${esc(t("絞り込みに一致する機器がありません"))}</div>`
      : shown.map((d, i) => `<label class="sc-dev"><input type="checkbox" data-guard-skip data-dev="${esc(d.name)}" data-i="${i}" ${picked.has(d.name) ? "checked" : ""}>
          <b>${esc(d.name)}</b><span class="mono">${esc(d.host || d.serialPort || "")}</span><span>${esc([d.site, d.role].filter(Boolean).join(" / "))}</span><span class="muted">${esc(d.commandSet || "")}</span></label>`).join("");
    devBox.querySelectorAll("[data-dev]").forEach(cb => cb.addEventListener("click", e => {
      const i = +cb.dataset.i, boxes = [...devBox.querySelectorAll("[data-dev]")];
      const range = e.shiftKey && lastClick >= 0 && lastClick !== i ? boxes.slice(Math.min(lastClick, i), Math.max(lastClick, i) + 1) : [cb];
      range.forEach(b => { b.checked = cb.checked; cb.checked ? picked.add(b.dataset.dev) : picked.delete(b.dataset.dev); });
      lastClick = i;
      node.querySelector("#sc-count").textContent = t("選択中 {a} / {b} 台", { a: groupDevs().filter(d => picked.has(d.name)).length, b: groupDevs().length });
    }));
  }
  grpSel.onchange = () => {
    const g = grpSel.value;
    picked = new Set((INV.devices || []).filter(d => (g === RUN_UNGROUPED ? !d.group : d.group === g) && d.enabled && !d.cliOff).map(d => d.name));
    filterIn.value = ""; lastClick = -1;
    renderDevs();
  };
  filterIn.addEventListener("input", () => { lastClick = -1; renderDevs(); });
  onlySel.onchange = () => { lastClick = -1; renderDevs(); };
  node.querySelector("#sc-all").onclick = () => { shownDevs().forEach(d => picked.add(d.name)); renderDevs(); };
  node.querySelector("#sc-none").onclick = () => { shownDevs().forEach(d => picked.delete(d.name)); renderDevs(); };
  renderDevs();

  // ---- ③ commands: a set, or typed lines (picking one side selects it)
  const setSel = node.querySelector("#sc-set"), cmdBox = node.querySelector("#sc-cmds");
  const mode = v => { node.querySelector(`input[name="sc-cmdmode"][value="${v}"]`).checked = true; };
  setSel.addEventListener("focus", () => mode("set"));
  setSel.addEventListener("change", () => mode("set"));
  cmdBox.addEventListener("focus", () => mode("typed"));

  // ---- ① when
  const slotBox = node.querySelector("#sc-slots");
  function collectSlots() {
    slotBox.querySelectorAll("[data-slot]").forEach(row => {
      const i = +row.dataset.slot;
      const q = sel => row.querySelector(sel);
      const kind = q(".sl-kind").value;
      // Switched to a repeat: it starts as soon as saved, like a new one
      // (a time an hour away would look like "it does not run").
      // Its period starts at 60 秒 (in seconds, so typing 10 means 10 seconds)
      // and it ends after 10 times — the same as a new one.
      if (kind === "interval" && slots[i].kind !== "interval") Object.assign(slots[i], { startNow: true, unit: 1, every: 60, end: "count", count: 10 });
      slots[i].kind = kind;
      // Dates and times are kept in slots[i] by their pickers as they change.
      if (q(".sl-start") && kind === "interval") slots[i].startNow = q(".sl-start").value === "now";
      if (q(".sl-every")) {
        // As typed (1.5 時間 is 90 minutes); checked at 保存, not cut down.
        slots[i].every = Number(q(".sl-every").value) || 0;
        slots[i].unit = parseInt(q(".sl-unit").value, 10) || 1;
        slots[i].count = Number(q(".sl-count").value) || 0;
        const e = row.querySelector(".sl-end:checked");
        if (e) slots[i].end = e.value;
      }
      if (q(".sl-wd")) slots[i].weekdays = [...row.querySelectorAll(".sl-wd:checked")].map(x => +x.value);
    });
  }
  const kinds = [["once", "1回だけ"], ["daily", "毎日"], ["weekly", "毎週"], ["interval", "一定間隔で繰り返す"]];
  function renderSlots() {
    slotBox.innerHTML = slots.map((sl, i) => {
      const at = sl.kind === "once" || (sl.kind === "interval" && !sl.startNow);
      const endName = `sl-end-${i}`;
      return `
      <div data-slot="${i}" class="sl-row">
        <div class="row-inline" style="gap:8px;flex-wrap:wrap">
          <select class="sl-kind btn" style="width:auto">${kinds.map(([k, l]) => `<option value="${k}" ${sl.kind === k ? "selected" : ""}>${esc(t(l))}</option>`).join("")}</select>
          ${sl.kind === "interval" ? `<span class="muted" style="font-size:12px">${esc(t("開始"))}</span><select class="sl-start btn" style="width:auto"><option value="now" ${sl.startNow ? "selected" : ""}>${esc(t("保存したらすぐ"))}</option><option value="at" ${sl.startNow ? "" : "selected"}>${esc(t("日時を指定"))}</option></select>` : ""}
          ${at ? `<span class="sl-date-host" data-i="${i}"></span>` : ""}
          ${sl.kind === "weekly" ? WEEKDAYS.map((w, wi) => `<label class="row-inline" style="gap:3px;font-weight:normal;cursor:pointer;font-size:13px"><input type="checkbox" class="sl-wd" value="${wi}" style="width:auto" ${sl.weekdays.includes(wi) ? "checked" : ""}>${esc(t(w))}</label>`).join("") : ""}
          ${sl.kind !== "interval" || at ? `<span class="sl-time-host" data-i="${i}"></span>` : ""}
          ${slots.length > 1 ? `<button class="btn sm act-del" type="button" data-rm-slot="${i}" style="margin-left:auto" data-tip="${esc(t("この日時を削除"))}">×</button>` : ""}
        </div>
        ${sl.kind === "interval" ? `
        <div class="row-inline" style="gap:8px;flex-wrap:wrap;margin-top:8px">
          <span class="muted" style="font-size:12px;min-width:3em">${esc(t("間隔"))}</span>
          <input class="sl-every" type="number" min="0" step="any" value="${sl.every}" style="width:80px"><select class="sl-unit btn" style="width:auto">${[[1, "秒"], [60, "分"], [3600, "時間"]].map(([u, l]) => `<option value="${u}" ${sl.unit === u ? "selected" : ""}>${esc(t(l))}</option>`).join("")}</select><span>${esc(t("おきに"))}</span>
          <span class="muted" style="font-size:12px">${esc(t("（10 秒から）"))}</span></div>
        <div style="display:flex;gap:8px;margin-top:8px">
          <span class="muted" style="font-size:12px;min-width:3em;padding-top:8px">${esc(t("終わり"))}</span>
          <div style="display:flex;flex-direction:column;gap:6px">
          <label class="row-inline" style="gap:6px;font-weight:normal;cursor:pointer"><input type="radio" class="sl-end" name="${endName}" value="count" style="width:auto" ${sl.end === "count" ? "checked" : ""}><input class="sl-count" type="number" min="1" value="${sl.count || 10}" style="width:80px">${esc(t("回で終わる"))}</label>
          <label class="row-inline" style="gap:6px;font-weight:normal;cursor:pointer"><input type="radio" class="sl-end" name="${endName}" value="until" style="width:auto" ${sl.end === "until" ? "checked" : ""}>${LANG === "ja" ? "" : esc(t("まで")) + "&nbsp;"}<span class="sl-until-date-host" data-i="${i}"></span><span class="sl-until-time-host" data-i="${i}"></span>${LANG === "ja" ? esc(t("まで")) : ""}</label>
          <label class="row-inline" style="gap:6px;font-weight:normal;cursor:pointer"><input type="radio" class="sl-end" name="${endName}" value="never" style="width:auto" ${sl.end === "never" ? "checked" : ""}>${esc(t("止めるまで続ける"))}</label>
          </div>
        </div>
        ${sl.end === "never" ? `<div style="font-size:12px;margin-top:4px;color:var(--warn)">${esc(t("⚠ 「⏸ 止める」を押すまで、ずっと続きます"))}</div>` : ""}` : ""}
      </div>`;
    }).join("") + (slots.length > 1 ? `<div class="muted" style="font-size:12px">${esc(t("この予約には日時が複数あります（以前の版で作ったもの）。別の時刻にしたいときは、予約を分けて作るのがおすすめです"))}</div>` : "");
    slotBox.querySelectorAll(".sl-date-host").forEach(host => {
      const sl = slots[+host.dataset.i];
      if (sl.date < todayStr(0) && !(sl.orig && sl.date === sl.orig.date)) sl.date = todayStr(0);
      mountDatePicker(host, sl.date, v => { sl.date = v; }, { noPast: true });
    });
    slotBox.querySelectorAll(".sl-time-host").forEach(host => {
      const sl = slots[+host.dataset.i];
      mountTimePicker(host, sl.time, v => { sl.time = v; });
    });
    // Picking a value next to a radio picks the radio too.
    const pickEnd = (host, v) => { const r = host.closest("[data-slot]").querySelector(`.sl-end[value="${v}"]`); if (r) r.checked = true; };
    slotBox.querySelectorAll(".sl-until-date-host").forEach(host => {
      const sl = slots[+host.dataset.i];
      if (sl.untilDate < todayStr(0) && !(sl.orig && sl.untilDate === sl.orig.untilDate)) sl.untilDate = todayStr(0);
      mountDatePicker(host, sl.untilDate, v => { sl.untilDate = v; pickEnd(host, "until"); }, { noPast: true });
    });
    slotBox.querySelectorAll(".sl-until-time-host").forEach(host => {
      const sl = slots[+host.dataset.i];
      mountTimePicker(host, sl.untilTime, v => { sl.untilTime = v; pickEnd(host, "until"); });
    });
    slotBox.querySelectorAll(".sl-count").forEach(x => x.addEventListener("focus", () => pickEnd(x, "count")));
    slotBox.querySelectorAll(".sl-kind, .sl-start, .sl-end").forEach(el => el.onchange = () => { collectSlots(); renderSlots(); });
    slotBox.querySelectorAll("[data-rm-slot]").forEach(b => b.onclick = () => { collectSlots(); slots.splice(+b.dataset.rmSlot, 1); renderSlots(); });
  }
  renderSlots();
  guardDiscard(node, "#sc-cancel-edit", () => [...picked].sort());

  const saveBtn = node.querySelector("#sc-save");
  // One save at a time: a second click while the first waits for its
  // confirmation or for the backend made the same schedule twice.
  let saving = false;
  saveBtn.onclick = async () => {
    if (saving) return;
    saving = true; saveBtn.disabled = true;
    try { await saveSchedule(); } finally { saving = false; saveBtn.disabled = false; }
  };
  async function saveSchedule() {
    collectSlots();
    const g = grpSel.value;
    if (!g) { toast(t("② のグループを選んでください"), "err"); return; }
    const names = groupDevs().filter(d => picked.has(d.name)).map(d => d.name);
    if (!names.length) { toast(t("② の機器を 1 台以上チェックしてください"), "err"); return; }
    const typedMode = node.querySelector('input[name="sc-cmdmode"]:checked').value === "typed";
    const commands = typedMode ? cmdBox.value.split(/\r?\n/).map(x => x.trim()).filter(Boolean) : [];
    if (typedMode && !commands.length) { toast(t("③ に実行するコマンドを入力してください"), "err"); return; }
    const p2 = n => String(n).padStart(2, "0");
    const stamp = d => `${ymd(d)} ${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`;
    for (const x of slots) {
      if (x.kind !== "interval") continue;
      const sec = x.every * x.unit;
      if (!(sec > 0) || Math.abs(sec - Math.round(sec)) > 1e-6) { toast(t("① の間隔は秒単位で割り切れる数で入れてください（例 1.5 時間 = 90 分）"), "err"); return; }
      if (x.end === "count" && !(Number.isInteger(x.count) && x.count >= 1)) { toast(t("① の回数を 1 以上の整数で入れてください"), "err"); return; }
      // A start already behind would silently drop the firings before now
      // (they are never caught up on) — unless it is the saved one, kept.
      const start = `${x.date} ${x.time}`;
      const kept = x.orig && x.date === x.orig.date && x.time === x.orig.time;
      if (!x.startNow && !kept && start < stamp(new Date())) { toast(t("① の開始日時が過ぎています。「保存したらすぐ」を選ぶか、今より後の時刻にしてください"), "err"); return; }
    }
    // 保存したらすぐ: the first firing a few seconds after the save itself —
    // taken again once the confirmation is answered (the backend only runs
    // firings after the save, so one taken before a long look at the
    // confirmation was dropped, and the count with it).
    const build = soon => ({
      id: s.id || "", enabled: sc ? !!s.enabled : true,
      group: g === RUN_UNGROUPED ? "" : g, ungrouped: g === RUN_UNGROUPED, names,
      commandSet: typedMode ? "" : setSel.value, commands,
      stage: node.querySelector("#sc-stage").value,
      slots: slots.map(x => {
        const now = x.kind === "interval" && x.startNow;
        const iv = x.kind === "interval";
        return { kind: x.kind,
          date: x.kind === "once" || iv ? (now ? ymd(soon) : x.date) : "",
          time: now ? `${p2(soon.getHours())}:${p2(soon.getMinutes())}:${p2(soon.getSeconds())}` : x.time,
          weekdays: x.kind === "weekly" ? x.weekdays : [],
          everySeconds: iv ? Math.round(x.every * x.unit) : 0, everyHours: 0,
          count: iv && x.end === "count" ? x.count : 0,
          until: iv && x.end === "until" ? `${x.untilDate} ${x.untilTime}` : "" };
      }),
    });
    let obj = build(new Date(Date.now() + 5000));
    // What will happen, in one look, before anything starts.
    const sl0 = obj.slots[0];
    const startsNow = sl0.kind === "interval" && slots[0].startNow;
    const shownNames = new Set(shownDevs().map(d => d.name));
    const hidden = names.filter(n => !shownNames.has(n)).length;
    const lines = [
      `<b>${esc(t("いつ"))}</b>${t("　")}${esc(slotLabel(startsNow ? { ...sl0, startNow: true } : sl0))}`,
      `<b>${esc(t("機器"))}</b>${t("　")}${esc(names.length <= 3 ? names.join(", ") : t("{a} ほか {n} 台", { a: names.slice(0, 3).join(", "), n: names.length - 3 }))}`,
      `<b>${esc(t("コマンド"))}</b>${t("　")}${esc(commands.length ? commands.join(t(" ／ ")) : obj.commandSet || t("機器ごとのコマンドセット"))}`,
    ];
    if (hidden) lines.push(`<span style="color:var(--warn)">${esc(t("絞り込みで表示されていない {n} 台も含みます", { n: hidden }))}</span>`);
    if (startsNow) lines.push(`<span style="color:var(--warn)">${esc(t("保存するとすぐ始まります"))}</span>`);
    if (sl0.kind === "interval" && !sl0.count && !sl0.until) lines.push(`<span style="color:var(--warn)">${esc(t("「⏸ 止める」を押すまで続きます"))}</span>`);
    // Over the editor (not in its place): 戻って直す keeps everything typed.
    if (!(await subConfirm(`<b>${esc(sc ? t("この内容で保存しますか？") : t("この内容で予約しますか？"))}</b><br>${lines.join("<br>")}`, t("保存"), t("戻って直す"), "primary"))) return;
    obj = build(new Date(Date.now() + 5000));
    obj.name = schedAutoName(obj);
    try {
      await App().SaveSchedule(obj);
      closeModal(); toast(t("予約を保存しました（止めるときは一覧の「⏸ 止める」）"), "ok");
      switchTab("schedule");
    } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  }
}

// The name a schedule is listed by, made from what it does — nobody has to
// think one up: "lab・show系・毎日 03:00:00".
function schedAutoName(sc) {
  const g = sc.ungrouped ? t("（グループ未設定）") : sc.group;
  const cmd = sc.commands && sc.commands.length ? sc.commands[0] + (sc.commands.length > 1 ? t(" ほか") : "")
    : sc.commandSet || t("機器ごとのセット");
  const when = (sc.slots || []).length ? slotLabel(sc.slots[0]) + (sc.slots.length > 1 ? t(" ほか") : "") : "";
  return [g, cmd, when].join(t("・"));
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

wireScheduleEvents();
