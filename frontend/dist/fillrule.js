// 変数 (fill-in): a command set's {{name}} lines run with each device's
// own values. This file holds the コマンドセット tab's 変数ルール band
// (folded by default: the sets stay as they were), the rule editor (built
// of 一覧ファイルの変換's parts: 読み取り位置, the sample grid, the steps), the
// dialog that reads a list with a rule and links it to a set, the set
// editor's 変数 band, and the run tab's per-device note and check. The
// reading and filling-in is Go's (app_fill.go).
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

const FILL_SRC = String.raw`\{\{\s*([#/]?)([\p{L}_][\p{L}\p{N}_\-]*)(\?)?\s*\}\}`;
// A new RegExp each time: a shared /g one carries its lastIndex over.
function fillMatches(text) { return [...String(text || "").matchAll(new RegExp(FILL_SRC, "gu"))]; }
function setHasFill(cmds) { return (cmds || []).some(c => fillMatches(c.text).length > 0); }
const FILL_EACH = "{{#each}}", FILL_END = "{{/each}}";
// A line that is a repeat marker (shown in the repeat colour). No /g: test
// keeps no position.
const FILL_MARK_RE = /^\s*\{\{\s*[#/]each\s*\}\}\s*$/;
function fillChip(name) { return `<span class="fill-chip">{{${esc(name)}}}</span>`; }
function fillRules() { return INV.fillRules || []; }
function fillRule(id) { return fillRules().find(r => r.id === id); }

// FR: the variable rules' state in the コマンドセット一覧 tab. view "sets" shows
// the command sets as always; "list" the rules' page (変数の取り込み設定, from
// the tab's button of that name — as 機器一覧 reaches 他システムの一覧の
// 変換ルール); "edit" a rule's editor. sample is the picked sample file's path
// (the backend reads only that one), test what trying the rule on it gave.
let FR = { view: "sets", rule: null, start: "", sample: null, test: null };

function frWhere(r) {
  const parts = [];
  if (r.sheet) parts.push(t("シート「{s}」", { s: r.sheet }));
  else if (r.sheetIndex > 1) parts.push(t("シート {n} 枚目", { n: r.sheetIndex }));
  parts.push(t("見出し {n} 行目", { n: r.headerRow }));
  return parts.join(" · ");
}

function frShow(view) {
  FR = { view, rule: null, start: "", sample: null, test: null };
  const content = document.querySelector(".content");
  if (content) content.scrollTop = 0;
  renderCommands();
}

// ---- the rules' page (変数の取り込み設定) ----

function frRenderList(root) {
  const rules = fillRules();
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("変数の取り込み設定"))}</div>
        <div class="page-sub">${esc(t("一覧ファイル（Excel・CSV など）の何列目を、どの変数にするかを、ルールで決めます。ルールでファイルを読み込むと、変数入りのコマンドセットができます"))}${hintI(t("コマンドに {{変数名}} と書いた所が、実行のとき、機器ごとの値に置き換わります。読み込んだ値はコマンドセットと一緒に保存されます"))}</div></div>
      <div class="row-inline" style="gap:6px;white-space:nowrap">
        <button class="btn" id="fr-back">${esc(t("← コマンドセット一覧"))}</button>
        <button class="btn" id="fr-imp" data-tip="${esc(t("人から受け取った変数ルール（JSON）を読み込みます"))}">${esc(t("ルールを読み込む"))}</button>
        <button class="btn primary" id="fr-add">${esc(t("＋ ルールを作る"))}</button>
      </div>
    </div>
    <div class="panel scrollx cs-scroll">${rules.length === 0
      ? `<div class="empty" style="line-height:1.8">${esc(t("変数ルールがありません。「＋ ルールを作る」で、一覧ファイルのどの列を、どの変数にするかを決めてください。"))}</div>`
      : `<table class="fixed"><thead><tr><th style="width:30px"></th><th style="width:250px">${esc(t("ルール名"))}</th><th style="width:140px">${esc(t("機器名の列"))}</th>
          <th>${esc(t("変数"))}</th><th style="width:170px">${esc(t("読み取り位置"))}</th><th style="width:400px"></th></tr></thead><tbody id="fr-body">
        ${rules.map(r => `<tr data-key="${esc(r.id)}">
          <td class="drag-handle" data-tip="${dragHandleTip()}">⠿</td>
          <td><div class="clip" data-tip="${esc(r.name)}"><b>${esc(r.name)}</b></div></td>
          <td class="muted"><div class="clip">${esc(t("列「{c}」", { c: (r.key && r.key.column) || "" }))}</div></td>
          <td><div class="clip">${(r.items || []).map(i => fillChip(i.name)).join(" ")}</div></td>
          <td class="muted" style="font-size:12px"><div class="clip">${esc(frWhere(r))}</div></td>
          <td class="ops"><div class="ops-btns">
            <button class="btn sm primary" data-frread="${esc(r.id)}" data-tip="${esc(t("このルールで一覧ファイルを読み込み、コマンドセットに紐づけます"))}">${esc(t("このルールで一覧を読み込む…"))}</button>
            <button class="btn sm act-edit" data-fredit="${esc(r.id)}">${esc(t("編集"))}</button>
            <button class="btn sm act-copy" data-frcopy="${esc(r.id)}">${esc(t("複製"))}</button>
            <button class="btn sm" data-frexp="${esc(r.id)}" data-tip="${esc(t("export\\fill-rules\\ に書き出します"))}">${esc(t("書き出し"))}</button>
            <button class="btn sm act-del" data-frdel="${esc(r.id)}">${esc(t("削除"))}</button>
          </div></td></tr>`).join("")}</tbody></table>`}</div>`;
  root.querySelector("#fr-back").onclick = () => frShow("sets");
  root.querySelector("#fr-add").onclick = () => frOpenEditor(null);
  root.querySelector("#fr-imp").onclick = async () => {
    try {
      const id = await App().ImportFillRuleFile();
      if (!id) return;
      await refreshInventory();
      toast(t("変数ルール「{n}」を読み込みました", { n: (fillRule(id) || {}).name || "" }), "ok");
    } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
  };
  const body = root.querySelector("#fr-body");
  if (body) wireRowReorder(body, async order => {
    try { await App().ReorderFillRules(order); await refreshInventory(); }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); await refreshInventory(); }
  });
  root.querySelectorAll("[data-frread]").forEach(b => b.onclick = () => fillLinkDialog(b.dataset.frread, null));
  root.querySelectorAll("[data-fredit]").forEach(b => b.onclick = () => frOpenEditor(b.dataset.fredit));
  root.querySelectorAll("[data-frcopy]").forEach(b => b.onclick = () => frOpenEditor(null, b.dataset.frcopy));
  root.querySelectorAll("[data-frexp]").forEach(b => b.onclick = async () => {
    try {
      const path = await App().ExportFillRule(b.dataset.frexp);
      const ok = await uiConfirm({ title: t("書き出しました"), message: esc(path) + "<br>" + esc(t("このファイルを渡すと、相手の PalaTerm の「ルールを読み込む」で使えます。")), okLabel: t("フォルダを開く"), cancelLabel: t("閉じる") });
      if (ok) App().OpenExportFolder(path.replace(/[\\/][^\\/]*$/, "")).catch(() => {});
    } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
  });
  root.querySelectorAll("[data-frdel]").forEach(b => b.onclick = async () => {
    const r = fillRule(b.dataset.frdel);
    if (!r) return;
    const ok = await uiConfirm({ title: t("変数ルールを削除"), message: t("変数ルール「<b>{n}</b>」を削除しますか？<br><span class=\"muted\" style=\"font-size:12px\">このルールで読み込んだコマンドセットの値はそのまま残ります（読み直しはできなくなります）</span>", { n: esc(r.name) }), okLabel: t("削除"), danger: true });
    if (!ok) return;
    try { await App().DeleteFillRule(r.id); await refreshInventory(); toast(t("削除しました"), "ok"); }
    catch (e) { toast(t("削除失敗") + ": " + terr(e), "err"); }
  });
}

// ---- the rule editor ----

function frBlankRule() {
  return { id: "", name: "", sheet: "", sheetIndex: 0, headerRow: 1, dataRow: 0, startCol: "", mergeOnlyFirst: false,
    key: { key: "", source: "", column: "", steps: [] }, items: [] };
}

function frOpenEditor(id, copyFrom) {
  const src = fillRule(id || copyFrom);
  const r = src ? JSON.parse(JSON.stringify(src)) : frBlankRule();
  if (copyFrom) {
    r.id = "";
    const taken = new Set(fillRules().map(x => x.name));
    let nm = r.name + "_copy";
    for (let i = 2; taken.has(nm); i++) nm = `${r.name}_copy${i}`;
    r.name = nm;
  }
  r.key = r.key || { source: "", column: "", steps: [] };
  r.items = r.items || [];
  FR = { view: "edit", rule: r, start: JSON.stringify(r), sample: null, test: null };
  renderCommands();
}

function frLeave() {
  App().ForgetListFile().catch(() => {});
  frShow("list");
}

function frRuleOut() {
  const r = JSON.parse(JSON.stringify(FR.rule));
  r.headerRow = Math.max(1, parseInt(r.headerRow, 10) || 1);
  r.dataRow = Math.max(0, parseInt(r.dataRow, 10) || 0);
  r.items.forEach(it => {
    const m = frEmptyMode(it);
    it.empty = m === "skip" ? "skip" : "";
    if (m !== "default") it.default = "";
    delete it._mode;
  });
  return r;
}

// An item's 空のとき as the editor shows it: stop / skip / default.
function frEmptyMode(it) { return it._mode || (it.empty === "skip" ? "skip" : it.default ? "default" : ""); }

let frTestTimer = null, frTestSeq = 0;
function frTestSoon(rerender) { clearTimeout(frTestTimer); frTestTimer = setTimeout(() => frRunTest(rerender), 250); }
async function frRunTest(rerender = true) {
  if (!FR.sample || FR.view !== "edit") return;
  const seq = ++frTestSeq;
  try {
    const res = await App().TestFillRule(FR.sample, frRuleOut());
    if (seq !== frTestSeq || FR.view !== "edit") return;
    FR.test = res;
  } catch (e) {
    if (seq !== frTestSeq) return;
    FR.test = { error: terr(e), grid: [], headers: [], rows: [], sheets: [], warnings: [], missing: [] };
  }
  const root = document.getElementById("tab-commands");
  if (rerender || !root.querySelector("#fr-res")) frRenderEdit(root);
  else frRenderResults(root);
}

function frHeaders() { return (FR.test && FR.test.headers) || []; }
function frColOpt(name) {
  const h = frHeaders().find(x => x.name === name);
  return h ? `${name}（${colLetter(h.col)}）` : name;
}
function frColSelect(attr, cur, need) {
  const names = frHeaders().map(x => x.name).filter(Boolean);
  if (cur && !names.includes(cur)) names.push(cur);
  return `<select ${attr} class="${need && !cur ? "li-need" : ""}" data-guard-skip>
    <option value="" ${cur ? "" : "selected"}>${esc(t("（選んでください）"))}</option>
    ${names.map(n => `<option value="${esc(n)}" ${n === cur ? "selected" : ""}>${esc(frColOpt(n))}${frHeaders().some(x => x.name === n) || !FR.sample ? "" : esc(t("（見本にない）"))}</option>`).join("")}</select>`;
}
function frSteps(field, attr) {
  if (!field.source) return "";
  return `<div class="li-steps">${(field.steps || []).map(s => `<button class="li-step" ${attr}>${esc(liStepLabel(s))}</button>`).join("")}
    <button class="li-step add" ${attr} data-tip="${esc(t("加工手順を編集"))}">${(field.steps || []).length ? "✎" : esc(t("＋ 加工"))}</button></div>`;
}
// The first value row of the sample: what the results column shows.
function frSample() { return ((FR.test && FR.test.rows) || [])[0] || null; }

function frGridHTML() {
  const test = FR.test, r = FR.rule;
  if (!test || !(test.grid || []).length) return `<div class="muted" style="font-size:12px;padding:6px 2px">${esc(t("見本ファイルを選ぶと、ここにファイルの先頭が表示されます"))}</div>`;
  const nc = Math.max(...test.grid.map(row => row.length));
  const hr = parseInt(r.headerRow, 10) || 1, dr = parseInt(r.dataRow, 10) || hr + 1;
  const cols = (test.headers || []).map(h => h.col);
  const c1 = cols.length ? Math.min(...cols) : 1, c2 = cols.length ? Math.max(...cols) : nc;
  let html = `<div class="scrollx li-sheetbox"><table class="li-sheet"><thead><tr><th></th>`;
  for (let c = 1; c <= nc; c++) html += `<th data-frcol="${c}" data-tip="${esc(t("この列から表を読む"))}">${colLetter(c)}</th>`;
  html += `</tr></thead><tbody>`;
  test.grid.forEach((row, i) => {
    const n = i + 1;
    html += `<tr class="${n === hr ? "h" : n >= dr ? "v" : ""}"><td class="rn" data-frrow="${n}" data-tip="${esc(t("この行を見出しにする"))}">${n}</td>`;
    for (let c = 1; c <= nc; c++) html += `<td class="${c < c1 || c > c2 ? "out" : ""}">${esc(row[c - 1] || "")}</td>`;
    html += `</tr>`;
  });
  return html + `</tbody></table></div>
    <div class="li-legend"><span><i class="h"></i>${esc(t("見出しの行"))}</span><span><i class="v"></i>${esc(t("読み取る値"))}</span><span><i class="out"></i>${esc(t("表の外（読まない）"))}</span></div>`;
}

function frResultsHTML() {
  const test = FR.test, r = FR.rule;
  if (!FR.sample) return `<div class="muted" style="font-size:12px">${esc(t("見本ファイルを選ぶと、ここに読み取り結果が出ます"))}</div>`;
  if (!test) return `<div class="muted" style="font-size:12px">${esc(t("読み込み中…"))}</div>`;
  if (!r.key.source) return `<div class="muted" style="font-size:12px">${esc(t("機器名の列を選ぶと、ここに読み取り結果が出ます"))}</div>`;
  const rows = test.rows || [];
  return `<div class="muted" style="font-size:12px;margin-bottom:6px">${esc(t("値の行 {n} → 機器 {d} 台（先頭 {k} 行を表示）", { n: test.total || 0, d: test.devices || 0, k: rows.length }))}</div>
    <div class="scrollx"><table class="li-res-t"><thead><tr><th>${esc(t("行"))}</th><th>${esc(t("機器名"))}</th>${r.items.map(it => `<th>${fillChip(it.name || "?")}</th>`).join("")}</tr></thead><tbody>
    ${rows.map(row => `<tr><td class="muted">${row.line}</td><td>${row.cont ? `<span class="muted">${esc(t("↳ 上と同じ（{d}）", { d: row.device }))}</span>` : `<b>${esc(row.device)}</b>`}</td>
      ${r.items.map(it => `<td class="mono">${row.values[it.name] ? esc(row.values[it.name]) : `<span class="muted">${esc(t("（空）"))}</span>`}</td>`).join("")}</tr>`).join("")}
    </tbody></table></div>`;
}
function frRenderResults(root) {
  const box = root.querySelector("#fr-res");
  if (box) box.innerHTML = frResultsHTML();
  const s = frSample();
  root.querySelectorAll("[data-frval]").forEach(el => {
    const k = el.dataset.frval;
    const v = !s ? "" : k === "" ? s.device : s.values[k];
    el.innerHTML = !s ? "" : v ? esc(v) : `<span class="muted">${esc(t("（空）"))}</span>`;
  });
}

function frRenderEdit(root) {
  const r = FR.rule, test = FR.test;
  const sheets = (test && test.sheets) || (r.sheet ? [r.sheet] : []);
  const s = frSample();
  const sampleVal = k => !s ? "" : (k === "" ? s.device : s.values[k]) ? esc(k === "" ? s.device : s.values[k]) : `<span class="muted">${esc(t("（空）"))}</span>`;
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(r.id ? t("変数ルールの編集") : t("変数ルールを作る"))}</div>
        <div class="page-sub">${esc(t("見本のファイルを読み込むと、列を選べて、結果がその場で出ます。見本はルールに保存しません"))}</div></div>
      <div class="row-inline" style="gap:6px;white-space:nowrap">
        <button class="btn" id="fr-cancel">${esc(t("キャンセル"))}</button>
        <button class="btn primary" id="fr-save" data-tip="Ctrl+Enter">${esc(t("保存"))}</button>
      </div>
    </div>
    <div class="panel li-sec"><div class="li-grid2">
      <div class="field"><label>${esc(t("ルール名"))} <span class="req">${esc(t("必須"))}</span></label><input type="text" id="fr-name" value="${esc(r.name)}" placeholder="${esc(t("例: 案件A 経路一覧"))}" data-guard-skip></div>
      <div class="field"><label>${esc(t("見本ファイル（ルールには保存しません）"))}</label>
        <div class="row-inline" style="gap:6px"><input type="text" readonly value="${esc(FR.sample ? FR.sample.split(/[\\/]/).pop() : "")}" placeholder="${esc(t("未選択"))}" style="flex:1" data-guard-skip>
        <button class="btn sm" id="fr-sample">${esc(t("選ぶ…"))}</button></div>
        ${!FR.sample && r.sampleName ? `<div class="muted" style="font-size:11px;margin-top:4px">${esc(t("前回の見本: {f}（確かめるには選び直してください）", { f: r.sampleName }))}</div>` : ""}
        ${test && test.format ? `<div class="muted" style="font-size:11px;margin-top:4px">${esc(t("形式: {f}", { f: test.format }))}</div>` : ""}
      </div>
    </div></div>

    <div class="panel li-sec">
      <div class="li-h"><b>${esc(t("読み取り位置"))}${hintI(t("見本の行番号を押すと見出しの行、列の記号を押すと表の開始列になります"))}</b></div>
      <div class="li-fields li-pos">
        <div class="field"><label>${esc(t("シート"))}</label><select id="fr-sheet" data-guard-skip>
          <option value="idx:0" ${!r.sheet && !r.sheetIndex ? "selected" : ""}>${esc(t("1 枚目"))}</option>
          ${sheets.map(sn => `<option value="${esc("name:" + sn)}" ${r.sheet === sn ? "selected" : ""}>${esc(t("名前で「{s}」", { s: sn }))}</option>`).join("")}
          ${((test && test.sheets) || []).slice(1).map((sn, i) => `<option value="idx:${i + 2}" ${!r.sheet && r.sheetIndex === i + 2 ? "selected" : ""}>${esc(t("{n} 枚目", { n: i + 2 }))}</option>`).join("")}
        </select></div>
        <div class="field"><label for="fr-hrow">${esc(t("見出しの行（何行目）"))}${hintI(t("項目名（ホスト名・IP など）が書いてある行"))}</label><input type="number" min="1" id="fr-hrow" value="${r.headerRow}" data-guard-skip></div>
        <div class="field"><label for="fr-drow">${esc(t("データの開始行（何行目）"))}${hintI(t("普通は空欄。見出しとデータの間に説明の行があるときだけ"))}</label><input type="number" min="0" id="fr-drow" value="${r.dataRow || ""}" placeholder="${esc(t("空欄＝見出しのすぐ下"))}" data-guard-skip></div>
        <div class="field"><label for="fr-scol">${esc(t("表の左端の列（AE など）"))}${hintI(t("普通は空欄。表の左にメモなど関係ない列があるときだけ"))}</label><input type="text" id="fr-scol" value="${esc(r.startCol || "")}" placeholder="${esc(t("空欄＝自動"))}" maxlength="3" data-guard-skip></div>
        <div class="field"><label>${esc(t("結合セル"))}</label><select id="fr-merge" data-guard-skip>
          <option value="fill" ${r.mergeOnlyFirst ? "" : "selected"}>${esc(t("すべてのセルで同じ値"))}</option>
          <option value="first" ${r.mergeOnlyFirst ? "selected" : ""}>${esc(t("左上のセルだけ"))}</option></select></div>
      </div>
      ${frGridHTML()}
      ${test && test.error ? `<div class="li-err">${esc(terr(test.error))}</div>` : ""}
      ${test && (test.warnings || []).length ? `<div class="li-warn">${test.warnings.map(w => esc(terr(w))).join("<br>")}</div>` : ""}
      ${test && (test.missing || []).length ? `<div class="li-warn">${esc(t("見本に無い列: {c}", { c: test.missing.join(t("、")) }))}</div>` : ""}
    </div>

    <div class="panel li-sec">
      <div class="li-h"><b>${esc(t("変数と列の対応"))}${hintI(t("変数の名前は、コマンドの中で {{名前}} と書いて使います。コマンドセットの編集画面では、ボタンを押すだけで入れられます"))}</b>
        <span class="muted">${esc(t("どの列の値を、どの変数にするか"))}</span></div>
      <div class="scrollx"><table class="li-map"><thead><tr>
        <th style="width:230px">${esc(t("変数"))}</th><th style="width:230px">${esc(t("元の値（列）"))}</th><th>${esc(t("加工手順"))}</th>
        <th style="width:230px">${esc(t("空のとき"))}</th><th style="width:180px">${esc(s ? t("見本 {n} 行目の結果", { n: s.line }) : t("見本の結果"))}</th><th style="width:44px"></th></tr></thead><tbody>
        <tr><td><b>${esc(t("機器名（照合）"))}</b> <span class="req">${esc(t("必須"))}</span><div class="muted" style="font-size:11px">${esc(t("PalaTerm の機器名と照らし合わせる列（大文字小文字は区別しません）"))}</div></td>
          <td>${frColSelect('data-frkey', r.key.column, true)}</td><td>${frSteps(r.key, 'data-frstep=""')}</td>
          <td class="muted" style="font-size:12px">${esc(t("空の行は、上の機器の続きになります"))}</td><td class="li-res" data-frval="">${sampleVal("")}</td><td></td></tr>
        ${r.items.map((it, i) => `<tr>
          <td><input type="text" data-frname="${i}" value="${esc(it.name)}" placeholder="${esc(t("例: NH"))}" maxlength="64" style="width:100%" data-guard-skip>
            <div style="margin-top:4px">${it.name ? fillChip(it.name) : ""}</div></td>
          <td>${frColSelect(`data-frcol-sel="${i}"`, it.column, true)}</td>
          <td>${frSteps(it, `data-frstep="${i}"`)}</td>
          <td><select data-frempty="${i}" data-guard-skip>
              <option value="" ${frEmptyMode(it) === "" ? "selected" : ""}>${esc(t("その機器は実行しない"))}</option>
              <option value="skip" ${frEmptyMode(it) === "skip" ? "selected" : ""}>${esc(t("その行だけ送らない"))}</option>
              <option value="default" ${frEmptyMode(it) === "default" ? "selected" : ""}>${esc(t("決まった値を使う"))}</option></select>
            ${frEmptyMode(it) === "default" ? `<input type="text" data-frdef="${i}" value="${esc(it.default || "")}" placeholder="${esc(t("空のときの値"))}" style="width:100%;margin-top:4px" data-guard-skip>` : ""}</td>
          <td class="li-res" data-frval="${esc(it.name)}">${it.name ? sampleVal(it.name) : ""}</td>
          <td><button class="btn sm act-del" data-frdelitem="${i}" data-tip="${esc(t("この変数を外す"))}">×</button></td></tr>`).join("")}
        <tr><td colspan="6"><button class="btn sm" id="fr-additem">${esc(t("＋ 変数を追加"))}</button>
          <span class="muted" style="font-size:12px;margin-left:8px">${esc(t("列を先に選ぶと、名前は見出しから入ります（あとで短く変えられます）"))}</span></td></tr>
      </tbody></table></div>
      <details id="fr-more" style="margin-top:10px" ${FR.more ? "open" : ""}>
        <summary style="cursor:pointer;font-size:13px">${esc(t("読み取った行を見る"))} <span class="muted" style="font-size:12px">${esc(t("同じ機器名の行が何行もあると、その機器の 2 行目以降になります"))}</span></summary>
        <div id="fr-res" style="margin-top:8px">${frResultsHTML()}</div>
      </details>
    </div>`;
  root.querySelector("#fr-more").addEventListener("toggle", e => { FR.more = e.target.open; });
  frWireEdit(root);
}

// A header as an item name: what a {{name}} can hold (letters, digits,
// "_" and "-", not starting with a digit or a mark).
function frNameFrom(header) {
  let n = String(header || "").normalize("NFKC").replace(/[^\p{L}\p{N}_\-]/gu, "");
  if (n && !/^[\p{L}_]/u.test(n)) n = "_" + n;
  return n.slice(0, 64);
}

function frWireEdit(root) {
  const r = FR.rule;
  const q = s => root.querySelector(s);
  q("#fr-name").oninput = e => { r.name = e.target.value; };
  q("#fr-cancel").onclick = async () => {
    if (JSON.stringify(r) !== FR.start && !(await uiConfirm({ title: t("編集をやめる"), message: esc(t("編集した内容は保存されていません。破棄して閉じますか？")), okLabel: t("破棄して閉じる"), danger: true }))) return;
    frLeave();
  };
  q("#fr-save").onclick = frSave;
  q("#fr-sample").onclick = async () => {
    try {
      const path = await App().PickListFile();
      if (!path) return;
      FR.sample = path;
      r.sampleName = path.split(/[\\/]/).pop();
      // A new rule starts at the header row the file seems to have.
      if (!r.key.source && !r.items.length) {
        const hr = await App().SuggestFillHeader(path).catch(() => 0);
        if (hr > 0) r.headerRow = hr;
      }
      FR.test = null;
      frRenderEdit(root);
      frRunTest(true);
    } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
  };
  q("#fr-sheet").onchange = e => {
    const v = e.target.value;
    if (v.startsWith("name:")) { r.sheet = v.slice(5); r.sheetIndex = 0; } else { r.sheet = ""; r.sheetIndex = parseInt(v.slice(4), 10) || 0; }
    frTestSoon(true);
  };
  q("#fr-hrow").oninput = e => { r.headerRow = parseInt(e.target.value, 10) || 1; frTestSoon(true); };
  q("#fr-drow").oninput = e => { r.dataRow = parseInt(e.target.value, 10) || 0; frTestSoon(true); };
  q("#fr-scol").oninput = e => { r.startCol = e.target.value.trim().toUpperCase(); frTestSoon(true); };
  q("#fr-merge").onchange = e => { r.mergeOnlyFirst = e.target.value === "first"; frTestSoon(true); };
  root.querySelectorAll("[data-frrow]").forEach(td => td.onclick = () => { r.headerRow = +td.dataset.frrow; r.dataRow = 0; frRunTest(true); });
  root.querySelectorAll("[data-frcol]").forEach(th => th.onclick = () => { r.startCol = colLetter(+th.dataset.frcol); frRunTest(true); });
  const setCol = (f, v) => { f.source = v ? "column" : ""; f.column = v; f.colIndex = 0; if (!v) f.steps = []; };
  q("[data-frkey]").onchange = e => { setCol(r.key, e.target.value); frRunTest(true); if (!FR.sample) frRenderEdit(root); };
  root.querySelectorAll("[data-frcol-sel]").forEach(sel => sel.onchange = () => {
    const it = r.items[+sel.dataset.frcolSel];
    setCol(it, sel.value);
    if (!it.name && sel.value) it.name = frNameFrom(sel.value);
    frRenderEdit(root);
    frRunTest(true);
  });
  root.querySelectorAll("[data-frname]").forEach(inp => inp.oninput = () => { r.items[+inp.dataset.frname].name = inp.value.trim(); frTestSoon(false); });
  root.querySelectorAll("[data-frname]").forEach(inp => inp.onchange = () => frRenderEdit(root));
  root.querySelectorAll("[data-frempty]").forEach(sel => sel.onchange = () => {
    const it = r.items[+sel.dataset.frempty];
    it._mode = sel.value;
    it.empty = sel.value === "skip" ? "skip" : "";
    if (sel.value !== "default") it.default = "";
    frRenderEdit(root);
    frTestSoon(false);
  });
  root.querySelectorAll("[data-frdef]").forEach(inp => inp.oninput = () => { r.items[+inp.dataset.frdef].default = inp.value; frTestSoon(false); });
  root.querySelectorAll("[data-frdelitem]").forEach(b => b.onclick = () => { r.items.splice(+b.dataset.frdelitem, 1); frRenderEdit(root); frRunTest(false); });
  q("#fr-additem").onclick = () => { r.items.push({ name: "", source: "", column: "", steps: [] }); frRenderEdit(root); };
  root.querySelectorAll("[data-frstep]").forEach(b => b.onclick = () => {
    const i = b.dataset.frstep;
    const f = i === "" ? r.key : r.items[+i];
    const s = frSample();
    liStepEditor("", 0, {
      field: f, label: i === "" ? t("機器名（照合）") : "{{" + (f.name || "?") + "}}",
      sample: !s ? "" : i === "" ? s.rawDevice : (s.raw || {})[f.name],
      done: () => { frRenderEdit(root); frRunTest(true); },
    });
  });
  root.onkeydown = e => { if (FR.view === "edit" && e.key === "Enter" && e.ctrlKey) { e.preventDefault(); frSave(); } };
}

async function frSave() {
  const r = frRuleOut();
  if (!r.name.trim()) { toast(t("ルール名を入力してください"), "err"); return; }
  try {
    await App().SaveFillRule(r);
    await refreshInventory();
    frLeave();
    toast(t("保存しました"), "ok");
  } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
}

// ---- reading a list with a rule: links it to a set ----

// fillLinkDialog picks a list, reads it with the rule and links it to a
// new set or an existing one. set: an existing set to link to (the set
// editor's 紐づける / 別のファイルにする); then done() runs after the link.
async function fillLinkDialog(ruleId, set, done) {
  const rule = fillRule(ruleId);
  if (!rule) return;
  let path;
  try { path = await App().PickListFile(); } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); return; }
  if (!path) return;
  let info;
  try { info = await App().ReadFillList(path, ruleId); }
  catch (e) { App().ForgetListFile().catch(() => {}); toast(t("読み込み失敗") + ": " + terr(e), "err"); return; }
  const sets = (INV.commandSets || []).map(s => s.name);
  const node = h(`<div>
    <h3>${esc(set ? t("変数の一覧を紐づける") : t("ファイルを読み込んでコマンドセットを作る"))}</h3>
    <div class="fl-kv">
      <label>${esc(t("使うルール"))}</label><b>${esc(rule.name)}</b>
      <label>${esc(t("ファイル"))}</label><div><span class="fill-file">📄 ${esc(info.file)}</span></div>
      <label>${esc(t("読み取り結果"))}</label><div>${esc(t("機器 {d} 台・{n} 行", { d: info.devices, n: info.rows }))}　${(info.items || []).map(fillChip).join(" ")}</div>
    </div>
    ${info.unknown.length ? `<div class="warn-box" style="margin-top:10px">${esc(t("PalaTerm に登録のない機器名が {n} 台あります（その行は使いません）: {list}", { n: info.devices - info.known, list: info.unknown.join(", ") + (info.devices - info.known > info.unknown.length ? " …" : "") }))}</div>` : ""}
    ${(info.missing || []).length ? `<div class="warn-box err">${esc(t("ファイルに無い列: {c}（その変数は空になります）", { c: info.missing.join(t("、")) }))}</div>` : ""}
    ${(info.warnings || []).length ? `<div class="warn-box">${info.warnings.map(w => esc(terr(w))).join("<br>")}</div>` : ""}
    ${set ? `<div class="fl-kv" style="margin-top:12px"><label>${esc(t("紐づけるセット"))}</label><b>${esc(set)}</b></div>` : `
    <div class="fl-kv" style="margin-top:12px">
      <label>${esc(t("作り方"))}</label><div class="row-inline" style="gap:16px">
        <label style="display:flex;gap:6px;align-items:center;font-weight:normal"><input type="radio" name="fl-mode" value="new" checked style="width:auto"> ${esc(t("新しいコマンドセットを作る"))}</label>
        <label style="display:flex;gap:6px;align-items:center;font-weight:normal"><input type="radio" name="fl-mode" value="old" ${sets.length ? "" : "disabled"} style="width:auto"> ${esc(t("既存のセットに紐づける"))}</label></div>
      <label>${esc(t("セット名"))}</label><div><input type="text" id="fl-new" value="${esc(rule.name)}">
        <select id="fl-old" class="hidden" style="width:100%">${sets.map(n => `<option value="${esc(n)}">${esc(n)}</option>`).join("")}</select></div>
    </div>`}
    <div class="muted" style="font-size:12px;margin-top:12px;line-height:1.8">${esc(t("読み込んだ値はコマンドセットと一緒に保存されます（暗号化して保存）。ファイルを直したときは「読み直す」で取り込み直せます"))}</div>
    <div class="modal-actions"><button class="btn" id="fl-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="fl-ok">${esc(set ? t("紐づける") : t("作って編集する"))}</button></div>
  </div>`);
  // Over the set editor (its own modal) as a small window; alone, a modal.
  const sub = !!set;
  let ov = null;
  if (sub) { ov = h(`<div class="sub-overlay"><div class="sub-modal" style="width:min(760px,94vw)"></div></div>`); ov.firstElementChild.appendChild(node); document.body.appendChild(ov); }
  else openModal(node, "mid");
  const close = () => { if (ov) ov.remove(); else closeModal(); App().ForgetListFile().catch(() => {}); };
  const mode = () => (node.querySelector('input[name="fl-mode"]:checked') || {}).value || "old";
  node.querySelectorAll('input[name="fl-mode"]').forEach(rb => rb.onchange = () => {
    node.querySelector("#fl-new").classList.toggle("hidden", mode() !== "new");
    node.querySelector("#fl-old").classList.toggle("hidden", mode() === "new");
    const ok = node.querySelector("#fl-ok");
    ok.textContent = mode() === "new" ? t("作って編集する") : t("紐づける");
  });
  node.querySelector("#fl-cancel").onclick = close;
  node.querySelector("#fl-ok").onclick = async () => {
    const isNew = !set && mode() === "new";
    const name = set || (isNew ? node.querySelector("#fl-new").value.trim() : node.querySelector("#fl-old").value);
    if (!name) { toast(t("セット名は必須です"), "err"); return; }
    try {
      await App().LinkFillList({ path, ruleId, set: name, new: isNew });
      close();
      await refreshInventory();
      toast(t("「{n}」に変数の一覧を紐づけました", { n: name }), "ok");
      if (done) done();
      else { frShow("sets"); editCommandSet((INV.commandSets || []).find(s => s.name === name)); }
    } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  };
}

// ---- the set editor's band ----

// fillEditorBand builds the 変数 band of the set editor for the saved
// set named name (null for a new set), into box. api: { insert(text),
// repeat(), commands() } from the editor.
function fillEditorBand(box, name, api) {
  let devSel = "";
  let seq = 0;
  const draw = () => {
    const cs = name ? (INV.commandSets || []).find(s => s.name === name) : null;
    const f = cs && cs.fill;
    const rules = fillRules();
    const has = setHasFill(api.commands());
    if (!f && !rules.length && !has) { box.innerHTML = ""; return; }
    const items = f ? f.items || [] : [];
    const devs = f ? (f.devices || []).map(d => d.name) : [];
    if (!devs.includes(devSel)) devSel = devs[0] || "";
    box.innerHTML = `<div class="fill-band">
      <div class="row-inline" style="gap:8px;flex-wrap:wrap;align-items:center">
        <b>${esc(t("変数の一覧"))}</b>${hintI(t("コマンドに {{項目名}} と書いた所が、実行のとき、この一覧の機器ごとの値に置き換わります。読み込んだ値はセットと一緒に保存されます"))}
        ${f ? `<span class="fill-file">📄 ${esc(f.file)}</span>
          <span class="muted" style="font-size:12px">${esc(t("ルール「{r}」・{n} 台分・{d} に読み込み", { r: f.rule, n: (f.devices || []).length, d: f.read }))}</span>
          <button class="btn sm" type="button" id="fb-reread" data-tip="${esc(f.path)}">${esc(t("読み直す"))}</button>
          <button class="btn sm" type="button" id="fb-other">${esc(t("別のファイルにする…"))}</button>
          <button class="btn sm" type="button" id="fb-unlink">${esc(t("✕ 外す"))}</button>`
        : name ? `<span class="muted" style="font-size:12px">${esc(t("なし"))}</span>
          ${rules.length ? `<button class="btn sm" type="button" id="fb-link">${esc(t("変数の一覧を紐づける…"))}</button>` : `<span class="muted" style="font-size:12px">${esc(t("（先にコマンドセット一覧の「変数の取り込み設定」でルールを作ってください）"))}</span>`}`
        : `<span class="muted" style="font-size:12px">${esc(t("セットを保存したあとで紐づけられます"))}</span>`}
      </div>
      ${f || has ? `<div class="row-inline" style="gap:6px;flex-wrap:wrap;align-items:center;margin-top:8px">
        <span class="muted" style="font-size:12px">${esc(t("変数（押すとカーソルの位置に入ります）:"))}</span>
        ${items.map(n => `<button class="fill-chip btn-chip" type="button" data-fbins="${esc(n)}">{{${esc(n)}}}</button>`).join("")}
        <button class="btn sm" type="button" id="fb-repeat" data-tip="${esc(t("選んだ行（テキストでは選んだ範囲の行）を {{#each}} と {{/each}} で囲みます。その間は、一覧にあるその機器の行の数だけ繰り返して送ります"))}">${esc(t("🔁 選んだ行を繰り返しにする"))}</button>
      </div>` : ""}
      ${f && setHasFill(api.commands()) ? `<div class="row-inline" style="gap:8px;align-items:center;margin-top:10px">
        <span class="section-label" style="margin:0">${esc(t("機器ごとの中身:"))}</span>
        <select id="fb-dev" data-guard-skip class="btn" style="padding-right:8px;max-width:260px">${devs.map(d => `<option value="${esc(d)}" ${d === devSel ? "selected" : ""}>${esc(d)}</option>`).join("")}</select></div>
        <div id="fb-prev" class="fill-prev mono"></div>` : ""}
    </div>`;
    box.querySelectorAll("[data-fbins]").forEach(b => {
      b.onmousedown = e => e.preventDefault(); // the command box keeps the focus
      b.onclick = () => api.insert("{{" + b.dataset.fbins + "}}");
    });
    const rep = box.querySelector("#fb-repeat");
    if (rep) { rep.onmousedown = e => e.preventDefault(); rep.onclick = () => api.repeat(); }
    const re = box.querySelector("#fb-reread");
    if (re) re.onclick = async () => {
      try { await App().RereadFillList(name); await refreshInventory(); draw(); toast(t("読み直しました"), "ok"); }
      catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
    };
    const pickRule = async () => {
      const rs = fillRules();
      if (rs.length === 1) return rs[0].id;
      return await new Promise(res => {
        const ov = h(`<div class="sub-overlay"><div class="sub-modal"><h3>${esc(t("変数ルールを選ぶ"))}</h3>
          <select id="fbr-sel" style="width:100%">${rs.map(r => `<option value="${esc(r.id)}" ${f && f.ruleId === r.id ? "selected" : ""}>${esc(r.name)}</option>`).join("")}</select>
          <div class="modal-actions"><button class="btn" id="fbr-c">${esc(t("キャンセル"))}</button><button class="btn primary" id="fbr-ok">${esc(t("ファイルを選ぶ…"))}</button></div></div></div>`);
        document.body.appendChild(ov);
        ov.querySelector("#fbr-c").onclick = () => { ov.remove(); res(""); };
        ov.querySelector("#fbr-ok").onclick = () => { const v = ov.querySelector("#fbr-sel").value; ov.remove(); res(v); };
      });
    };
    const link = async () => { const id = await pickRule(); if (id) fillLinkDialog(id, name, () => draw()); };
    const lk = box.querySelector("#fb-link"); if (lk) lk.onclick = link;
    const ot = box.querySelector("#fb-other"); if (ot) ot.onclick = link;
    const un = box.querySelector("#fb-unlink");
    if (un) un.onclick = async () => {
      if (!(await subConfirm(esc(t("変数の一覧の紐づけを外しますか？読み込んだ値も消えます（コマンドはそのまま残ります）")), t("外す")))) return;
      try { await App().UnlinkFillList(name); await refreshInventory(); draw(); }
      catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
    };
    const ds = box.querySelector("#fb-dev");
    if (ds) ds.onchange = () => { devSel = ds.value; preview(); };
    preview();
  };
  const preview = async () => {
    const out = box.querySelector("#fb-prev");
    if (!out || !devSel) return;
    const my = ++seq;
    let p;
    try { p = await App().PreviewFillSet(name, api.commands(), devSel); } catch (e) { p = { error: terr(e) }; }
    if (my !== seq || !box.isConnected) return;
    out.innerHTML = p.error ? `<span style="color:var(--err)">${esc(terr(p.error))}</span>`
      : (p.commands || []).map((c, i) => `<div><span class="muted fill-ln">${i + 1}</span>${esc(c)}</div>`).join("");
  };
  let timer = null;
  draw();
  // The editor calls this as the commands change: the band appears when a
  // {{…}} is typed, and the preview follows.
  return {
    changed: () => {
      const had = !!box.querySelector("#fb-repeat"), has = setHasFill(api.commands());
      if (had !== has || !!box.querySelector("#fb-prev") !== has) { draw(); return; }
      clearTimeout(timer); timer = setTimeout(preview, 300);
    },
  };
}

// ---- the run tab ----

// The devices whose set fills in values (the set the run sends them).
function runFillDev(d) { const cs = runSetOf(d); return !!(cs && setHasFill(cs.commands)); }
function runFillNeeded(devs) { return (devs || []).some(runFillDev); }

// fillRowNote is what the message column says of a device before a run:
// its values, or why it will not run. null: its set fills in nothing.
function fillRowNote(d) {
  if (!runFillDev(d)) return null;
  const cs = runSetOf(d);
  const f = cs.fill;
  if (!f) return { text: t("⚠ 変数の一覧が紐づいていません（コマンドセットの編集で紐づけてください）"), warn: true };
  const key = String(d.name || "").trim().toLowerCase();
  const fd = (f.devices || []).find(x => String(x.name || "").trim().toLowerCase() === key);
  if (!fd) return { text: t("⚠ 変数の一覧にこの機器の行がありません（実行しません）"), warn: true };
  const row = (fd.rows || [])[0] || {};
  const vals = (f.items || []).map(n => `${n}=${row[n] || ""}`).join("  ");
  return { text: (fd.rows.length > 1 ? t("変数 {n} 行: ", { n: fd.rows.length }) : t("変数: ")) + vals, warn: false };
}

// The last PreviewFill of a run's check (reqCommandCount reads it).
let runFillPreview = null;

// fillConfirm shows each device's commands with the values in, before the
// run; resolves true to run okNames.
function fillConfirm(okNames, skipped, warnings, scope, stage, extraNote) {
  return new Promise(resolve => {
    const by = runFillPreview || {};
    const cmdsOf = n => {
      const fd = by[n];
      if (fd && fd.fill) return fd.commands || [];
      const cs = runSetOf(runDev(n));
      return cs ? (cs.commands || []).map(c => c.text) : [];
    };
    const files = [...new Set(okNames.map(n => by[n] && by[n].fill ? by[n].file : "").filter(Boolean))];
    const danger = warnings && warnings.length > 0;
    let sel = okNames[0];
    const node = h(`<div>
      <h3${danger ? ' style="color:var(--warn)"' : ""}>${esc(danger ? t("⚠ 送信内容の確認（設定を変更するコマンドを含みます）") : t("送信内容の確認"))}</h3>
      ${stageBanner(stage || "")}
      <p style="font-size:14px;line-height:1.8;margin:4px 0 10px">${t("{s}<b>{n} 台</b>に一括実行します。", { s: scope, n: okNames.length })}<span class="muted" style="font-size:12px">${esc(t("変数の一覧: {f}", { f: files.join(", ") }))}</span></p>
      <div style="display:grid;grid-template-columns:220px minmax(0,1fr);gap:16px">
        <div style="max-height:52vh;overflow:auto">
          <div class="section-label" style="margin-bottom:6px">${esc(t("実行する機器"))}</div>
          <div id="fc-devs"></div>
          ${skipped.length ? `<div class="section-label" style="margin:14px 0 6px;color:var(--warn)">${esc(t("実行しない機器"))}</div>
            <div style="font-size:12px;line-height:1.8">${skipped.map(s => `<div><b>${esc(s.name)}</b> <span class="muted">${esc(terr(s.why))}</span></div>`).join("")}</div>` : ""}
        </div>
        <div class="panel" style="padding:12px 14px;background:var(--bg-2);max-height:52vh;overflow:auto"><div id="fc-cmds"></div></div>
      </div>
      ${extraNote ? `<p style="font-size:14px;line-height:1.8;margin:8px 0 0">${extraNote}</p>` : ""}
      ${danger ? `<label style="display:flex;align-items:center;gap:8px;cursor:pointer;margin-top:10px"><input type="checkbox" id="fc-ack" style="width:auto"> ${esc(t("変更を伴うことを理解したうえで実行します"))}</label>` : ""}
      <div class="modal-actions"><span class="muted" style="font-size:12px;margin-right:auto;align-self:center">${esc(t("ログには実際に送ったコマンドが残ります"))}</span>
        <button class="btn" id="fc-cancel">${esc(t("戻る"))}</button>
        <button class="btn ${danger ? "danger" : "run"}" id="fc-ok" ${danger ? "disabled" : ""}>${esc(t("▶ この内容で {n} 台に実行", { n: okNames.length }))}</button></div>
    </div>`);
    function draw() {
      node.querySelector("#fc-devs").innerHTML = okNames.map(n => `<div class="fc-dev" data-n="${esc(n)}" style="padding:7px 10px;border-radius:8px;cursor:pointer;display:flex;justify-content:space-between;gap:8px;${n === sel ? "background:var(--accent);color:#fff" : ""}">
        <b class="clip">${esc(n)}</b><span style="font-size:12px;opacity:.85;white-space:nowrap">${esc(t("{n} 行", { n: cmdsOf(n).length }))}</span></div>`).join("");
      const d = runDev(sel);
      node.querySelector("#fc-cmds").innerHTML = `<div style="margin-bottom:8px"><b>${esc(t("{d}（{h}）に送るコマンド", { d: sel, h: d ? d.host : "" }))}</b>
        ${by[sel] && by[sel].fill ? "" : ` <span class="muted" style="font-size:12px">${esc(t("変数なしのセット"))}</span>`}</div>
        <div class="mono" style="font-size:12.5px;white-space:pre;line-height:1.75">${cmdsOf(sel).map((c, i) => `<div><span class="muted" style="display:inline-block;width:28px;text-align:right;margin-right:12px">${i + 1}</span>${esc(c)}</div>`).join("")}</div>`;
      node.querySelectorAll(".fc-dev").forEach(el => el.onclick = () => { sel = el.dataset.n; draw(); });
    }
    draw();
    openModal(node, "xwide");
    const ok = node.querySelector("#fc-ok"), ack = node.querySelector("#fc-ack");
    if (ack) ack.onchange = () => { ok.disabled = !ack.checked; };
    node.querySelector("#fc-cancel").onclick = () => { closeModal(); resolve(false); };
    ok.onclick = () => { if (ack && !ack.checked) return; closeModal(); resolve(true); };
  });
}

// runFillCheck is startRun's step for a run with fill-in sets: the values
// each device gets, those it cannot be given left out. Resolves the names
// to run, or null (stopped).
async function runFillCheck(targets, scope, extra) {
  let p;
  try { p = await App().PreviewFill(runRequest(targets, false)); }
  catch (e) { toast(t("変数を確認できませんでした") + ": " + terr(e), "err"); return null; }
  runFillPreview = {};
  (p.devices || []).forEach(fd => { runFillPreview[fd.device] = fd; });
  const okNames = [], skipped = [];
  targets.forEach(n => { const fd = runFillPreview[n]; if (fd && fd.fill && fd.error) skipped.push({ name: n, why: fd.error }); else okNames.push(n); });
  if (!okNames.length) { toast(t("変数を入れられる機器がありません（各行のメッセージを確かめてください）"), "err"); return null; }
  const req = runRequest(okNames, false);
  const warnings = await App().CheckDangerousCommands(req).catch(() => []);
  if (!(await fillConfirm(okNames, skipped, warnings, scope, runStage, extra(req, okNames)))) return null;
  return okNames;
}
