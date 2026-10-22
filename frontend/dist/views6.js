// 一覧の取り込み (tab "import"): device lists another system exported
// (Excel, CSV, …) turned into a group's devices by 変換ルール. Three views:
// the rule list, the rule editor (tried live on a sample file) and the
// import (a preview to check before anything changes). The conversion
// itself runs in Go (internal/listimport); this page only edits the rule
// and shows what Go worked out.

let LI = { view: "list", rule: null, ruleDirty: false, sample: null, test: null, more: false, imp: null };
let LI_FIELDS = null; // [{key, ja, kind, hop}]

// The fields shown before 「ほかの項目を表示」.
const LI_MAIN = ["name", "host", "conn", "port", "osType", "username", "password", "enablePassword", "site", "role", "useGroupCreds", "commandSet"];

async function liFields() {
  if (!LI_FIELDS) LI_FIELDS = await App().ListImportFields();
  return LI_FIELDS;
}
function liField(key) { return (LI_FIELDS || []).find(f => f.key === key) || { key, ja: key, kind: "text" }; }
function liFieldLabel(key) {
  const f = liField(key);
  return f.hop ? t("踏み台{n}_", { n: f.hop }) + t(f.ja.replace(/^踏み台\d_/, "")) : t(f.ja);
}
function liRules() { return INV.importRules || []; }
function liRule(id) { return liRules().find(r => r.id === id); }

function renderListImport() {
  const root = document.getElementById("tab-import");
  liFields().then(() => {
    if (LI.view === "edit" && LI.rule) liRenderEdit(root);
    else if (LI.view === "import" && LI.imp) liRenderImport(root);
    else liRenderList(root);
  }).catch(e => { root.innerHTML = `<div class="empty">${esc(terr(e))}</div>`; });
}

// ---- the rule list ----

function liWhere(r) {
  const parts = [];
  if (r.sheet) parts.push(t("シート「{s}」", { s: r.sheet }));
  else if (r.sheetIndex > 1) parts.push(t("シート {n} 枚目", { n: r.sheetIndex }));
  parts.push(t("見出し {n} 行目", { n: r.headerRow }));
  parts.push(t("値 {n} 行目〜", { n: r.dataRow || r.headerRow + 1 }));
  if (r.startCol) parts.push(t("{c} 列から", { c: r.startCol }));
  return parts.join(" · ");
}

function liRenderList(root) {
  const rules = liRules();
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("一覧の取り込み"))}</div>
        <div class="page-sub">${esc(t("他のシステムが出力した機器一覧（Excel・CSV など）を、変換ルールでグループへ取り込みます。1 ファイルが 1 グループになります"))}</div></div>
      <div class="row-inline" style="gap:6px;white-space:nowrap">
        <button class="btn" id="li-rimp" data-tip="${esc(t("人から受け取った変換ルール（JSON）を読み込みます"))}">${esc(t("ルールを読み込む"))}</button>
        <button class="btn primary" id="li-radd">${esc(t("＋ ルールを作る"))}</button>
      </div>
    </div>
    <div class="li-drop">
      <div style="flex:1 1 260px;min-width:0">
        <div class="li-drop-t">${esc(t("取り込むファイルを選んでください"))}</div>
        <div class="muted" style="font-size:12px">${esc(t("xls・xlsx・CSV・TSV に対応します。Excel で開くと保護ビューの警告が出るファイルもそのまま読めます"))}</div>
      </div>
      <button class="btn primary" id="li-pick" ${rules.length ? "" : "disabled"}>${esc(t("ファイルを選んで取り込む…"))}</button>
    </div>
    <div class="panel scrollx">
      ${rules.length === 0
        ? `<div class="muted" style="font-size:13px;padding:14px;line-height:1.8">${esc(t("変換ルールがありません。「＋ ルールを作る」で、元のファイルのどの列を PalaTerm のどの項目にするかを決めてください。見本のファイルを読み込むと、結果を見ながら作れます。"))}</div>`
        : `<table class="fixed" data-colw="li-rules"><thead><tr>
            <th style="width:240px">${esc(t("ルール名"))}</th><th>${esc(t("読み取り位置"))}</th>
            <th style="width:90px">${esc(t("項目"))}</th><th style="width:90px">${esc(t("除外条件"))}</th>
            <th style="width:120px">${esc(t("最後に使った日"))}</th><th style="width:260px"></th></tr></thead><tbody>
          ${rules.map(r => `<tr>
            <td><div class="clip"><b>${esc(r.name)}</b></div>${r.sampleName ? `<div class="muted clip" style="font-size:11px">${esc(r.sampleName)}</div>` : ""}</td>
            <td class="muted" style="font-size:12px"><div class="clip2">${esc(liWhere(r))}</div></td>
            <td>${esc(t("{n} 項目", { n: (r.fields || []).filter(f => f.source || f.default).length }))}</td>
            <td>${esc(t("{n} 件", { n: (r.exclude || []).length }))}</td>
            <td class="muted">${esc(r.lastUsed || t("未使用"))}</td>
            <td style="text-align:right;white-space:nowrap">
              <button class="btn sm act-edit" data-redit="${esc(r.id)}">${esc(t("編集"))}</button>
              <button class="btn sm act-copy" data-rcopy="${esc(r.id)}">${esc(t("複製"))}</button>
              <button class="btn sm" data-rexp="${esc(r.id)}" data-tip="${esc(t("export\\import-rules\\ に書き出します。パスワード欄の既定値・置き換えの値は含めません"))}">${esc(t("書き出し"))}</button>
              <button class="btn sm act-del" data-rdel="${esc(r.id)}">${esc(t("削除"))}</button>
            </td></tr>`).join("")}</tbody></table>`}
    </div>`;
  wireColResize(root);
  root.querySelector("#li-radd").onclick = () => liOpenEditor(null);
  root.querySelector("#li-rimp").onclick = async () => {
    try {
      const id = await App().ImportImportRuleFile();
      if (!id) return;
      INV = await App().GetInventory();
      toast(t("変換ルール「{n}」を読み込みました", { n: (liRule(id) || {}).name || "" }), "ok");
      renderListImport();
    } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
  };
  root.querySelector("#li-pick").onclick = () => liStartImport();
  root.querySelectorAll("[data-redit]").forEach(b => b.onclick = () => liOpenEditor(b.dataset.redit));
  root.querySelectorAll("[data-rcopy]").forEach(b => b.onclick = async () => {
    try { await App().CopyImportRule(b.dataset.rcopy); INV = await App().GetInventory(); renderListImport(); }
    catch (e) { toast(t("複製失敗") + ": " + terr(e), "err"); }
  });
  root.querySelectorAll("[data-rexp]").forEach(b => b.onclick = () => liExport(b.dataset.rexp));
  root.querySelectorAll("[data-rdel]").forEach(b => b.onclick = async () => {
    const r = liRule(b.dataset.rdel);
    if (!r) return;
    const ok = await uiConfirm({ title: t("ルールの削除"), message: esc(t("変換ルール「{n}」を削除しますか？ 取り込んだ機器はそのまま残ります。", { n: r.name })), okLabel: t("削除"), danger: true });
    if (!ok) return;
    try { await App().DeleteImportRule(r.id); INV = await App().GetInventory(); renderListImport(); }
    catch (e) { toast(t("削除失敗") + ": " + terr(e), "err"); }
  });
}

async function liExport(id) {
  try {
    const path = await App().ExportImportRule(id);
    const ok = await uiConfirm({ title: t("書き出しました"), message: esc(path) + "<br>" + esc(t("このファイルを渡すと、相手の PalaTerm の「ルールを読み込む」で使えます。パスワード欄の既定値・置き換えの値は含めていません（受け取った人が入れてください）。")), okLabel: t("フォルダを開く"), cancelLabel: t("閉じる") });
    if (ok) App().OpenExportFolder(path.replace(/[\\/][^\\/]*$/, "")).catch(() => {});
  } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
}

// ---- the rule editor ----

function liBlankRule() {
  return {
    id: "", name: "", sheet: "", sheetIndex: 0, headerRow: 1, dataRow: 0, startCol: "", mergeOnlyFirst: false,
    fields: [{ key: "name", source: "", steps: [] }, { key: "host", source: "", steps: [] }, { key: "conn", source: "", default: "ssh", steps: [] }],
    exclude: [], match: { ignoreCase: true, width: true, space: true, domain: false, dash: false, keyColumn: "" },
  };
}

function liOpenEditor(id) {
  const src = id ? liRule(id) : null;
  LI.rule = src ? JSON.parse(JSON.stringify(src)) : liBlankRule();
  LI.rule.fields = LI.rule.fields || [];
  LI.rule.exclude = LI.rule.exclude || [];
  LI.rule.match = LI.rule.match || {};
  LI.ruleStart = JSON.stringify(LI.rule);
  LI.test = null; LI.more = false;
  LI.tent = new Set(); LI.tentUndo = null; LI.tentN = 0;
  LI.view = "edit";
  renderListImport();
  if (LI.sample) liRunTest();
}

function liDirty() { return LI.rule && JSON.stringify(LI.rule) !== LI.ruleStart; }

function liFieldRule(key) { return LI.rule.fields.find(f => f.key === key); }
function liEnsureField(key) {
  let f = liFieldRule(key);
  if (!f) { f = { key, source: "", steps: [] }; LI.rule.fields.push(f); }
  f.steps = f.steps || [];
  return f;
}

function liStepLabel(s) {
  switch (s.type) {
    case "trim": return t("前後の空白を除く");
    case "lower": return t("小文字にする");
    case "upper": return t("大文字にする");
    case "width": return t("全角を半角にする");
    case "split": return t("「{s}」の{p}で分けて{k}", { s: s.sep, p: s.pos === "last" ? t("最後") : t("最初"), k: s.keep === "left" ? t("左") : t("右") }) + (s.miss === "empty" ? t("（無ければ空）") : "");
    case "replace": return s.to ? t("「{a}」→「{b}」", { a: s.from, b: s.to }) : t("「{a}」を削る", { a: s.from });
    case "regex": return t("正規表現 {p}", { p: s.pattern });
    case "map": return t("置き換え表（{n} 件）", { n: (s.rows || []).length });
    case "guessOS": return t("機種から OS タイプを推測");
    case "guessSet": return t("機種からコマンドセットを推測");
  }
  return s.type;
}

// The first value row the rule does not leave out: the sample the results
// column and the step editor show.
function liSampleRow(unit = 1) {
  const rows = ((LI.test && LI.test.rows) || []).filter(r => (r.unit || 1) === unit);
  return rows.find(r => !r.excluded) || rows[0] || null;
}

// liUnitName names the device a rule's unit u (0-based) gives: 「2台目
// （バックアップ）」.
function liUnitName(u) {
  const un = (LI.rule && LI.rule.units || [])[u] || {};
  return t("{n}台目", { n: u + 2 }) + (un.label ? t("（{l}）", { l: un.label }) : "");
}
function liUnitField(u, key) {
  const un = LI.rule.units[u];
  return (un.fields || []).find(f => f.key === key);
}
function liEnsureUnitField(u, key) {
  const un = LI.rule.units[u];
  un.fields = un.fields || [];
  let f = un.fields.find(x => x.key === key);
  if (!f) { f = { key, source: "", steps: [] }; un.fields.push(f); }
  f.steps = f.steps || [];
  return f;
}

function liValueInput(key, val, id) {
  const f = liField(key);
  const opts = gridOptions(f.kind);
  if (opts) {
    const has = opts.some(o => o.v === (val || ""));
    return `<select id="${id}" data-guard-skip>
      <option value="" ${val ? "" : "selected"}>${esc(t("（なし）"))}</option>
      ${opts.filter(o => o.v !== "").map(o => `<option value="${esc(o.v)}" ${o.v === val ? "selected" : ""}>${esc(o.label)}</option>`).join("")}
      ${val && !has ? `<option value="${esc(val)}" selected>${esc(val)}</option>` : ""}</select>`;
  }
  return `<input type="${f.kind === "secret" ? "password" : "text"}" id="${id}" value="${esc(val || "")}" placeholder="${esc(t("（なし）"))}" data-guard-skip autocomplete="off">`;
}

function liShowValue(key, v) {
  if (v === undefined || v === null || v === "") return `<span class="muted">${esc(t("（空）"))}</span>`;
  const f = liField(key);
  if (f.kind === "secret") return `<span class="li-secret" data-secret="${esc(v)}">●●●●●●</span>`;
  const opts = gridOptions(f.kind);
  return esc(opts ? gridOptLabel(f.kind, v) : v);
}

function liRenderEdit(root) {
  const r = LI.rule;
  const test = LI.test;
  const headers = (test && test.headers) || [];
  const hdrNames = headers.map(h => h.name).filter(n => n);
  const sample = liSampleRow();
  const fields = LI_FIELDS.filter(f => LI.more || LI_MAIN.includes(f.key) || (liFieldRule(f.key) && (liFieldRule(f.key).source || liFieldRule(f.key).default)));
  const colOpts = (cur) => {
    const list = hdrNames.slice();
    if (cur && !list.includes(cur)) list.push(cur);
    return list;
  };
  // A header with its Excel column letter (「機器名称（AG）」): a wide
  // sheet's columns are known by their letters.
  const colLet = liColLetter;
  const colOpt = liColOpt;
  const units = r.units || (r.units = []);
  // What "as device 1" takes, in words: the column (or the combination,
  // the default) device 1 reads — so the choice says where the value
  // comes from instead of a bare "same".
  const sameAs = liSameAs;
  const unitCell = (u, f) => {
    const uf = liUnitField(u, f.key) || { steps: [] };
    const cur = uf.source === "column" ? "col:" + (uf.column || "") : uf.source === "template" ? "tpl" : "";
    const isName = f.key === "name";
    // The device's name decides whether a row has this device: it is the
    // one field never taken from device 1.
    const first = isName ? `<option value="" ${cur ? "" : "selected"}>${esc(t("（選んでください）"))}</option>`
      : `<option value="">${esc(sameAs(f.key))}</option>`;
    return `<select data-usrc="${u}:${esc(f.key)}" class="${isName && !cur ? "li-need" : ""}" data-guard-skip>
      ${first}
      ${colOpts(uf.source === "column" ? uf.column : "").map(n => `<option value="${esc("col:" + n)}" ${cur === "col:" + n ? "selected" : ""}>${esc(colOpt(n))}${hdrNames.includes(n) ? "" : esc(t("（見本にない）"))}</option>`).join("")}
      <option value="tpl" ${cur === "tpl" ? "selected" : ""}>${esc(t("組み立て（列をつなげる）"))}</option></select>
      ${uf.source === "template" ? `<input type="text" class="li-tpl" data-utpl="${u}:${esc(f.key)}" value="${esc(uf.template || "")}" placeholder="{機器名(副)}" data-guard-skip>` : ""}
      ${fbHTML(uf, `data-ufb="${u}:${esc(f.key)}`, `data-ufbadd="${u}:${esc(f.key)}`)}
      ${uf.source ? `<div class="li-steps" style="margin-top:4px">${(uf.steps || []).map(s => `<button class="li-step" data-ustep="${u}:${esc(f.key)}">${esc(liStepLabel(s))}</button>`).join("")}
        <button class="li-step add" data-ustep="${u}:${esc(f.key)}" title="${esc(t("加工手順を編集"))}">${(uf.steps || []).length ? "✎" : esc(t("＋ 加工"))}</button></div>` : ""}
      ${LI.tent.has(`u${u}:${f.key}`) ? `<span class="li-tentb" title="${esc(t("見出しの名前から仮に選びました"))}">${esc(t("仮"))}</span>` : ""}
      <div class="li-ures" data-ures="${u}:${esc(f.key)}">${liUnitRes(u, f.key)}</div>`;
  };
  // The fallback columns under a column choice: read in turn while the
  // value is still empty (an address in the PPPoE or the gateway column).
  const fbHTML = (fr, attr, addAttr) => fr.source !== "column" ? "" :
    (fr.fallback || []).map((fb, i) => `<div class="li-fb"><span class="muted">${esc(t("空のときは →"))}</span>
      <select ${attr}:${i}" data-guard-skip>
        <option value="" ${fb ? "" : "selected"}>${esc(fb ? t("（外す）") : t("（選んでください）"))}</option>
        ${colOpts(fb).filter(n => n !== fr.column).map(n => `<option value="${esc(n)}" ${fb === n ? "selected" : ""}>${esc(colOpt(n))}${hdrNames.includes(n) ? "" : esc(t("（見本にない）"))}</option>`).join("")}
      </select></div>`).join("")
    + `<button class="li-fbadd" ${addAttr}" type="button">${esc(t("＋ 空のときは別の列"))}</button>`;
  const srcSelect = (f) => {
    const fr = liFieldRule(f.key) || {};
    const cur = fr.source === "column" ? "col:" + (fr.column || "") : fr.source === "template" ? "tpl" : "";
    return `<select data-src="${esc(f.key)}" data-guard-skip>
      <option value="">${esc(t("（使わない）"))}</option>
      ${colOpts(fr.source === "column" ? fr.column : "").map(n => `<option value="${esc("col:" + n)}" ${cur === "col:" + n ? "selected" : ""}>${esc(colOpt(n))}${hdrNames.includes(n) ? "" : esc(t("（見本にない）"))}</option>`).join("")}
      <option value="tpl" ${cur === "tpl" ? "selected" : ""}>${esc(t("組み立て（列をつなげる）"))}</option></select>
      ${fr.source === "template" ? `<input type="text" class="li-tpl" data-tpl="${esc(f.key)}" value="${esc(fr.template || "")}" placeholder="{拠点コード}-{機器名称}" data-guard-skip>` : ""}
      ${fbHTML(fr, `data-fb="${esc(f.key)}`, `data-fbadd="${esc(f.key)}`)}`;
  };
  const condTargets = () => {
    const mapped = r.fields.filter(f => f.source || f.default).map(f => f.key);
    return mapped.map(k => ({ v: "field:" + k, l: t("変換後: {f}", { f: liFieldLabel(k) }) }))
      .concat(hdrNames.map(n => ({ v: "column:" + n, l: colLet(n) ? t("元の列({l}): {c}", { l: colLet(n), c: n }) : t("元の列: {c}", { c: n }) })));
  };
  const OPS = [["empty", t("が空")], ["notempty", t("が空でない")], ["contains", t("が次を含む")], ["notcontains", t("が次を含まない")], ["equals", t("が次と一致")], ["notequals", t("が次と一致しない")]];

  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(r.id ? t("ルールの編集") : t("ルールを作る"))}</div>
        <div class="page-sub">${esc(t("見本のファイルを読み込むと、列を選べて、結果がその場で出ます。見本はルールに保存しません"))}</div></div>
      <div class="row-inline" style="gap:6px;white-space:nowrap">
        ${r.id ? `<button class="btn" id="li-eexp" data-tip="${esc(t("保存してから export\\import-rules\\ に書き出します。パスワード欄の既定値・置き換えの値は含めません"))}">${esc(t("書き出し"))}</button>` : ""}
        <button class="btn" id="li-ecancel">${esc(t("やめる"))}</button>
        <button class="btn primary" id="li-esave">${esc(t("保存"))}</button>
      </div>
    </div>

    <div class="panel li-sec"><div class="li-grid2">
      <div class="field"><label>${esc(t("ルール名"))}</label><input type="text" id="li-name" value="${esc(r.name)}" placeholder="${esc(t("例: 資産管理（月次出力）"))}" data-guard-skip></div>
      <div class="field"><label>${esc(t("見本ファイル（ルールには保存しません）"))}</label>
        <div class="row-inline" style="gap:6px"><input type="text" readonly value="${esc(LI.sample ? LI.sample.split(/[\\/]/).pop() : "")}" placeholder="${esc(t("未選択"))}" style="flex:1" data-guard-skip>
        <button class="btn sm" id="li-sample">${esc(t("選ぶ…"))}</button></div>
        ${test ? `<div class="muted" style="font-size:11px;margin-top:4px">${esc(t("形式: {f}", { f: test.format }))}${test.total ? " · " + esc(t("値の行 {n}（除外 {x}）", { n: test.total, x: test.excluded })) : ""}</div>` : ""}
      </div>
    </div></div>

    <div class="panel li-sec">
      <div class="li-h"><b>${esc(t("読み取り位置"))}</b><span class="muted">${esc(t("見本の行番号を押すと見出しの行、列の記号を押すと表の開始列になります"))}</span></div>
      <div class="li-fields li-pos">
        <div class="field"><label>${esc(t("シート"))}</label><select id="li-sheet" data-guard-skip>
          <option value="idx:0" ${!r.sheet && !r.sheetIndex ? "selected" : ""}>${esc(t("1 枚目"))}</option>
          ${((test && test.sheets) || (r.sheet ? [r.sheet] : [])).map((s, i) => `<option value="${esc("name:" + s)}" ${r.sheet === s ? "selected" : ""}>${esc(t("名前で「{s}」", { s }))}</option>`).join("")}
          ${((test && test.sheets) || []).slice(1).map((s, i) => `<option value="idx:${i + 2}" ${!r.sheet && r.sheetIndex === i + 2 ? "selected" : ""}>${esc(t("{n} 枚目", { n: i + 2 }))}</option>`).join("")}
        </select></div>
        <div class="field"><label for="li-hrow">${esc(t("見出しの行（何行目）"))}${LI.tent.has("headerRow") ? ` <span class="li-tentb">${esc(t("仮"))}</span>` : ""}</label><input type="number" min="1" id="li-hrow" value="${r.headerRow}" data-guard-skip>
          <div class="li-hint">${esc(t("項目名（ホスト名・IP など）が書いてある行"))}</div></div>
        <div class="field"><label for="li-drow">${esc(t("データの開始行（何行目）"))}</label><input type="number" min="0" id="li-drow" value="${r.dataRow || ""}" placeholder="${esc(t("空欄＝見出しのすぐ下"))}" data-guard-skip>
          <div class="li-hint">${esc(t("普通は空欄。見出しとデータの間に説明の行があるときだけ"))}</div></div>
        <div class="field"><label for="li-scol">${esc(t("表の左端の列（AE など）"))}</label><input type="text" id="li-scol" value="${esc(r.startCol || "")}" placeholder="${esc(t("空欄＝自動"))}" maxlength="3" data-guard-skip>
          <div class="li-hint">${esc(t("普通は空欄。表の左にメモなど関係ない列があるときだけ"))}</div></div>
        <div class="field"><label>${esc(t("結合セル"))}</label><select id="li-merge" data-guard-skip>
          <option value="fill" ${r.mergeOnlyFirst ? "" : "selected"}>${esc(t("すべてのセルで同じ値"))}</option>
          <option value="first" ${r.mergeOnlyFirst ? "selected" : ""}>${esc(t("左上のセルだけ"))}</option></select></div>
      </div>
      <div id="li-gridbox">${liGridHTML()}</div>
      ${test && test.error ? `<div class="li-err">${esc(terr(test.error))}</div>` : ""}
      ${test && (test.warnings || []).length ? `<div class="li-warn">${test.warnings.map(w => esc(terr(w))).join("<br>")}</div>` : ""}
    </div>

    <div class="panel li-sec">
      <div class="li-h"><b>${esc(t("項目の紐付け"))}</b><span class="muted">${esc(t("加工手順は上から順に行います。加工したあとも空のときは既定値で埋めます"))}</span></div>
      ${LI.tentUndo ? `<div class="li-tentbar"><span>${esc(t("見出しの名前から仮の紐付けを入れました（{n} 項目）。「仮」の札の欄を確かめて、違っていれば選び直してください", { n: LI.tentN }))}</span>
        <span class="row-inline" style="gap:6px"><button class="btn sm" id="li-tundo">${esc(t("元に戻す"))}</button><button class="btn sm" id="li-tok">${esc(t("確認した（札を消す）"))}</button></span></div>` : ""}
      <div class="li-units">
        <span class="muted">${esc(t("1 行に 2 台分（冗長構成のメインとバックアップなど）が載っている一覧は、2 台目を追加して、2 台目で値が違う項目だけ元の値を選びます。選ばない項目は 1 台目と同じ値です。2 台目の機器名が空の行では 2 台目を作りません。"))}</span>
        <div class="row-inline" style="flex-wrap:wrap;gap:6px 12px;margin-top:6px">
          ${units.map((un, u) => `<span class="li-unitchip"><b>${esc(t("{n}台目", { n: u + 2 }))}</b>
            <input type="text" data-ulabel="${u}" value="${esc(un.label || "")}" placeholder="${esc(t("名前（例: バックアップ）"))}" maxlength="40" data-guard-skip>
            <button class="btn sm act-del" data-udel="${u}" title="${esc(t("この台を外す"))}">×</button></span>`).join("")}
          ${units.length < 3 ? `<button class="btn sm" id="li-uadd">${esc(units.length ? t("＋ {n}台目を追加", { n: units.length + 2 }) : t("＋ 2台目を追加（冗長構成など）"))}</button>` : ""}
        </div>
      </div>
      <div class="scrollx"><table class="li-map"><thead><tr>
        <th style="width:190px">${esc(t("PalaTerm の項目"))}</th><th style="width:250px">${esc(units.length ? t("元の値（1台目）") : t("元の値"))}</th>
        <th>${esc(t("加工手順"))}</th><th style="width:180px">${esc(t("空のときの既定値"))}</th>
        <th style="width:190px">${esc(sample ? t("見本 {n} 行目の結果", { n: sample.line }) : t("見本の結果"))}</th>
        ${units.map((un, u) => `<th style="width:240px">${esc(liUnitName(u))}</th>`).join("")}</tr></thead><tbody>
        ${fields.map(f => {
          const fr = liFieldRule(f.key) || { steps: [] };
          const used = fr.source || fr.default;
          return `<tr class="${used ? "" : "li-unused"}">
            <td><b>${esc(liFieldLabel(f.key))}</b>${f.key === "name" ? ` <span class="li-req">${esc(t("必須"))}</span>` : ""}</td>
            <td>${srcSelect(f)}${LI.tent.has(f.key) ? `<span class="li-tentb" title="${esc(t("見出しの名前から仮に選びました"))}">${esc(t("仮"))}</span>` : ""}</td>
            <td><div class="li-steps">${(fr.steps || []).map((s, i) => `<button class="li-step" data-step="${esc(f.key)}">${esc(liStepLabel(s))}</button>`).join("")}
              ${fr.source ? `<button class="li-step add" data-step="${esc(f.key)}" title="${esc(t("加工手順を編集"))}">${(fr.steps || []).length ? "✎" : esc(t("＋ 加工"))}</button>` : ""}</div></td>
            <td>${liValueInput(f.key, fr.default || "", "li-def-" + f.key)}</td>
            <td class="li-res" data-res="${esc(f.key)}">${sample && used ? liShowValue(f.key, sample.values[f.key]) + ((sample.filled || []).includes(f.key) ? ` <span class="li-filled">${esc(t("既定"))}</span>` : "") + liGuessNote(sample, f.key) + liFromNote(sample, f.key) : ""}</td>
            ${units.map((un, u) => `<td class="li-ucell">${unitCell(u, f)}</td>`).join("")}
          </tr>`;
        }).join("")}
        <tr><td colspan="${5 + units.length}"><button class="btn sm" id="li-more">${esc(LI.more ? t("主な項目だけ表示") : t("ほかの項目を表示（踏み台・GUI・端末の色など）"))}</button></td></tr>
      </tbody></table></div>
    </div>

    <div class="panel li-sec">
      <div class="li-h"><b>${esc(t("既存の機器との照合"))}</b><span class="muted">${esc(t("既存のグループへ取り込むとき、同じ機器を次の順に探します"))}</span></div>
      <ol class="li-ol">
        <li>${esc(t("前回この機器を取り込んだときの元の名前（または照合に使う列の値）。PalaTerm 側で名前を変えても見失いません"))}</li>
        <li>${esc(t("機器名（下の違いは無視して比べます）"))}</li>
        <li>${esc(t("IP アドレス。見つかっても自動では結びつけず、プレビューで確かめてもらいます"))}</li>
      </ol>
      <div class="row-inline" style="flex-wrap:wrap;gap:6px 18px;font-size:13px">
        ${[["ignoreCase", t("大文字・小文字")], ["width", t("全角・半角")], ["space", t("空白")], ["domain", t("ドメイン部（最初の「.」から後ろ）")], ["dash", t("「-」と「_」")]].map(([k, l]) =>
          `<label class="row-inline" style="gap:6px"><input type="checkbox" data-match="${k}" ${r.match[k] ? "checked" : ""} data-guard-skip> ${esc(l)}</label>`).join("")}
      </div>
      <div class="field" style="max-width:340px;margin-top:10px"><label>${esc(t("照合に使う列（任意・名前より優先）"))}</label>
        <select id="li-keycol" data-guard-skip><option value="">${esc(t("（使わない）"))}</option>
        ${colOpts(r.match.keyColumn).map(n => `<option value="${esc(n)}" ${r.match.keyColumn === n ? "selected" : ""}>${esc(colOpt(n))}</option>`).join("")}</select></div>
    </div>

    <div class="panel li-sec">
      <div class="li-h"><b>${esc(t("除外条件"))}</b><span class="muted">${esc(t("どれか 1 つに当てはまる行は取り込みません（機器名が空の行は常に取り込みません）"))}</span></div>
      ${r.exclude.map((c, i) => `<div class="row-inline li-cond">
        <select data-ctgt="${i}" data-guard-skip>${condTargets().concat(condTargets().some(o => o.v === c.target + ":" + c.key) || !c.key ? [] : [{ v: c.target + ":" + c.key, l: c.key }]).map(o => `<option value="${esc(o.v)}" ${o.v === c.target + ":" + c.key ? "selected" : ""}>${esc(o.l)}</option>`).join("")}</select>
        <select data-cop="${i}" data-guard-skip>${OPS.map(([v, l]) => `<option value="${v}" ${c.op === v ? "selected" : ""}>${esc(l)}</option>`).join("")}</select>
        ${c.op === "empty" || c.op === "notempty" ? "" : `<input type="text" data-cval="${i}" value="${esc(c.value || "")}" data-guard-skip>`}
        <button class="btn sm act-del" data-cdel="${i}">×</button></div>`).join("")}
      <button class="btn sm" id="li-cadd">${esc(t("＋ 条件を追加"))}</button>
    </div>

    <div id="li-resbox">${liResultsHTML()}</div>`;
  liWireEdit(root);
}

// The sample rows as the rule turns them (redrawn on every test).
function liResultsHTML() {
  const test = LI.test, r = LI.rule;
  const shown = (test && test.rows) || [];
  if (!shown.length) return "";
  const mappedKeys = r.fields.filter(f => f.source || f.default).map(f => f.key).slice(0, 9);
  return `<div class="panel li-sec">
      <div class="li-h"><b>${esc(t("見本の結果（先頭 {n} 行）", { n: shown.length }))}</b><span class="muted">${esc(t("取り込むときは、このあと既存の機器と照合して追加・更新を決めます"))}</span></div>
      <div class="scrollx"><table class="li-res-t"><thead><tr><th>${esc(t("行"))}</th>${(r.units || []).length ? `<th>${esc(t("台"))}</th>` : ""}${mappedKeys.map(k => `<th>${esc(liFieldLabel(k))}</th>`).join("")}<th></th></tr></thead><tbody>
        ${shown.map((row, i) => {
          const unitsN = (r.units || []).length;
          let html = `<tr class="${row.excluded ? "li-skip" : ""}"><td class="muted">${row.line}</td>${unitsN ? `<td class="muted">${(row.unit || 1) > 1 ? esc(liUnitName(row.unit - 2)) : esc(t("1台目"))}</td>` : ""}${mappedKeys.map(k => `<td>${liShowValue(k, row.values[k])}${(row.filled || []).includes(k) ? ` <span class="li-filled">${esc(t("既定"))}</span>` : ""}</td>`).join("")}<td class="muted" style="font-size:12px">${esc(liExclText(row))}</td></tr>`;
          // After a line's last device: the further devices it does not
          // have, said so (a line with one device among pairs is normal).
          const next = shown[i + 1];
          if (unitsN && (!next || next.line !== row.line)) {
            const have = new Set(shown.filter(x => x.line === row.line).map(x => x.unit || 1));
            for (let u = 0; u < unitsN; u++) {
              if (!have.has(u + 2)) html += `<tr class="li-nounit"><td class="muted">${row.line}</td><td class="muted">${esc(liUnitName(u))}</td><td colspan="${mappedKeys.length + 1}" class="muted">${esc(t("この行に 2台目はありません（2台目の機器名が空）"))}</td></tr>`;
            }
          }
          return html;
        }).join("")}
      </tbody></table></div>
      ${(test.missing || []).length ? `<div class="li-warn">${esc(t("見本に無い列: {c}", { c: test.missing.join("、") }))}</div>` : ""}
    </div>`;
}

function liGridHTML() {
  const test = LI.test, r = LI.rule;
  if (!test || !(test.grid || []).length) return LI.sample ? "" : `<div class="muted" style="font-size:12px;padding:6px 2px">${esc(t("見本ファイルを選ぶと、ここにファイルの先頭が表示されます"))}</div>`;
  const nc = Math.max(...test.grid.map(row => row.length));
  const hr = r.headerRow, dr = r.dataRow || r.headerRow + 1;
  const cols = (test.headers || []).map(h => h.col);
  const c1 = cols.length ? Math.min(...cols) : 1, c2 = cols.length ? Math.max(...cols) : nc;
  let html = `<div class="scrollx li-sheetbox"><table class="li-sheet"><thead><tr><th></th>`;
  for (let c = 1; c <= nc; c++) html += `<th data-col="${c}" title="${esc(t("この列から表を読む"))}">${colLetter(c)}</th>`;
  html += `</tr></thead><tbody>`;
  test.grid.forEach((row, i) => {
    const n = i + 1;
    const cls = n === hr ? "h" : n >= dr ? "v" : "";
    html += `<tr class="${cls}"><td class="rn" data-row="${n}" title="${esc(t("この行を見出しにする"))}">${n}</td>`;
    for (let c = 1; c <= nc; c++) {
      const out = c < c1 || c > c2;
      html += `<td class="${out ? "out" : ""}">${esc(row[c - 1] || "")}</td>`;
    }
    html += `</tr>`;
  });
  return html + `</tbody></table></div>
    <div class="li-legend"><span><i class="h"></i>${esc(t("見出しの行"))}</span><span><i class="v"></i>${esc(t("読み取る値"))}</span><span><i class="out"></i>${esc(t("表の外（読まない）"))}</span></div>`;
}
function colLetter(n) { let s = ""; while (n > 0) { n--; s = String.fromCharCode(65 + n % 26) + s; n = Math.floor(n / 26); } return s; }

let liTestTimer = null;
function liTestSoon(rerender) {
  clearTimeout(liTestTimer);
  liTestTimer = setTimeout(() => liRunTest(rerender), 250);
}
// liRunTest tries the rule as edited on the sample. rerender redraws the
// whole editor (the headers or the fields shown may have changed); else
// only the results update, so the field being typed in keeps its focus.
let liTestSeq = 0;
async function liRunTest(rerender = true) {
  if (!LI.sample || LI.view !== "edit") return;
  const seq = ++liTestSeq;
  try {
    const res = await App().TestImportRule(LI.sample, liRuleOut());
    if (seq !== liTestSeq || LI.view !== "edit") return;
    const before = JSON.stringify(LI.test && LI.test.headers);
    LI.test = res;
    if (rerender || before !== JSON.stringify(res.headers)) renderListImport();
    else liRefreshResults();
  } catch (e) { toast(terr(e), "err"); }
}
function liRefreshResults() {
  const root = document.getElementById("tab-import");
  const sample = liSampleRow();
  root.querySelectorAll("[data-res]").forEach(td => {
    const k = td.dataset.res, fr = liFieldRule(k);
    td.innerHTML = sample && fr && (fr.source || fr.default) ? liShowValue(k, sample.values[k]) + ((sample.filled || []).includes(k) ? ` <span class="li-filled">${esc(t("既定"))}</span>` : "") + liGuessNote(sample, k) + liFromNote(sample, k) : "";
  });
  const box = root.querySelector("#li-gridbox");
  if (box) box.innerHTML = liGridHTML();
  const rb = root.querySelector("#li-resbox");
  if (rb) rb.innerHTML = liResultsHTML();
  // Device 2's column follows what device 1 now gives (a default typed
  // there shows at once): its "→ value" and its "as device 1 (…)" choice.
  root.querySelectorAll("[data-ures]").forEach(d => {
    const s = d.dataset.ures, i = s.indexOf(":");
    d.innerHTML = liUnitRes(+s.slice(0, i), s.slice(i + 1));
  });
  root.querySelectorAll("select[data-usrc]").forEach(sel => {
    const s = sel.dataset.usrc, key = s.slice(s.indexOf(":") + 1);
    if (key !== "name" && sel.options.length) sel.options[0].textContent = liSameAs(key);
  });
  root.querySelectorAll(".li-secret").forEach(s => s.onclick = () => { s.textContent = s.textContent.startsWith("●") ? s.dataset.secret : "●●●●●●"; });
  liWireGrid(root);
}

// The rule as saved: fields without a source or default are left out.
function liRuleOut() {
  const r = JSON.parse(JSON.stringify(LI.rule));
  r.fields = r.fields.filter(f => f.source || f.default || (f.steps || []).length);
  r.fields.forEach(f => { if (!f.source) f.steps = []; });
  const fbClean = f => { f.fallback = f.source === "column" ? (f.fallback || []).filter(x => x) : []; if (!f.fallback.length) delete f.fallback; };
  r.fields.forEach(fbClean);
  (r.units || []).forEach(u => (u.fields || []).forEach(fbClean));
  r.units = (r.units || []).map(u => ({ label: (u.label || "").trim(), fields: (u.fields || []).filter(f => f.source) }));
  r.headerRow = Math.max(1, parseInt(r.headerRow, 10) || 1);
  r.dataRow = Math.max(0, parseInt(r.dataRow, 10) || 0);
  if (LI.sample) r.sampleName = LI.sample.split(/[\\/]/).pop();
  return r;
}

function liWireGrid(root) {
  root.querySelectorAll(".li-sheet [data-row]").forEach(td => td.onclick = () => {
    LI.tent && LI.tent.delete("headerRow");
    LI.rule.headerRow = parseInt(td.dataset.row, 10);
    if (LI.rule.dataRow && LI.rule.dataRow <= LI.rule.headerRow) LI.rule.dataRow = 0;
    liRunTest(true);
  });
  root.querySelectorAll(".li-sheet [data-col]").forEach(th => th.onclick = () => {
    LI.rule.startCol = colLetter(parseInt(th.dataset.col, 10));
    liRunTest(true);
  });
}

function liWireEdit(root) {
  const r = LI.rule;
  const q = s => root.querySelector(s);
  q("#li-name").oninput = e => { r.name = e.target.value; };
  q("#li-sample").onclick = async () => {
    try {
      const p = await App().PickListFile();
      if (!p) return;
      LI.sample = p;
      if (liCanSuggest()) await liSuggest();
      await liRunTest(true);
    } catch (e) { toast(terr(e), "err"); }
  };
  q("#li-sheet").onchange = e => {
    const v = e.target.value;
    if (v.startsWith("name:")) { r.sheet = v.slice(5); r.sheetIndex = 0; } else { r.sheet = ""; r.sheetIndex = parseInt(v.slice(4), 10) || 0; }
    liRunTest(true);
  };
  const tu = q("#li-tundo");
  if (tu) tu.onclick = () => {
    LI.rule = JSON.parse(LI.tentUndo);
    LI.tent = new Set(); LI.tentUndo = null;
    if (LI.sample) liRunTest(true); else renderListImport();
  };
  const tk = q("#li-tok");
  if (tk) tk.onclick = () => { LI.tent = new Set(); LI.tentUndo = null; renderListImport(); };
  q("#li-hrow").onchange = e => {
    LI.tent.delete("headerRow");
    r.headerRow = Math.max(1, parseInt(e.target.value, 10) || 1);
    // As a click on the row number does: values start after the header.
    if (r.dataRow && r.dataRow <= r.headerRow) r.dataRow = 0;
    if (LI.sample) liRunTest(true); else renderListImport();
  };
  q("#li-drow").onchange = e => { r.dataRow = Math.max(0, parseInt(e.target.value, 10) || 0); liRunTest(true); };
  q("#li-scol").onchange = e => { r.startCol = e.target.value.trim().toUpperCase(); liRunTest(true); };
  q("#li-merge").onchange = e => { r.mergeOnlyFirst = e.target.value === "first"; liRunTest(true); };
  liWireGrid(root);
  root.querySelectorAll("[data-src]").forEach(sel => sel.onchange = () => {
    LI.tent.delete(sel.dataset.src);
    const f = liEnsureField(sel.dataset.src), v = sel.value;
    if (v.startsWith("col:")) {
      f.source = "column"; f.column = v.slice(4);
      const hd = ((LI.test && LI.test.headers) || []).find(h => h.name === f.column);
      f.colIndex = hd ? hd.col : 0;
    } else if (v === "tpl") { f.source = "template"; f.template = f.template || ""; }
    else { f.source = ""; }
    if (LI.sample) liRunTest(true); else renderListImport();
  });
  root.querySelectorAll("[data-tpl]").forEach(inp => inp.oninput = () => { liEnsureField(inp.dataset.tpl).template = inp.value; liTestSoon(false); });
  root.querySelectorAll("[id^=li-def-]").forEach(el => {
    const key = el.id.slice(7);
    const on = () => {
      const f = liEnsureField(key);
      f.default = el.value;
      // Typed in place: redrawing the editor here would take the focus
      // away after the first character. The row's look and the results
      // follow without it.
      const tr = el.closest("tr");
      if (tr) tr.classList.toggle("li-unused", !(f.source || f.default));
      liTestSoon(false);
    };
    el.tagName === "SELECT" ? el.onchange = on : el.oninput = on;
  });
  root.querySelectorAll("[data-step]").forEach(b => b.onclick = () => liStepEditor(b.dataset.step));
  q("#li-more").onclick = () => { LI.more = !LI.more; renderListImport(); };
  const rerun = () => { if (LI.sample) liRunTest(true); else renderListImport(); };
  const uk = el => { const s = el.dataset.usrc || el.dataset.utpl || el.dataset.ustep; const i = s.indexOf(":"); return [+s.slice(0, i), s.slice(i + 1)]; };
  root.querySelectorAll("[data-usrc]").forEach(sel => sel.onchange = () => {
    const [u, key] = uk(sel), v = sel.value;
    LI.tent.delete(`u${u}:${key}`);
    if (!v) { // as the first device: the unit's own setting goes
      r.units[u].fields = (r.units[u].fields || []).filter(f => f.key !== key);
    } else {
      const f = liEnsureUnitField(u, key);
      if (v.startsWith("col:")) {
        f.source = "column"; f.column = v.slice(4);
        const hd = ((LI.test && LI.test.headers) || []).find(h => h.name === f.column);
        f.colIndex = hd ? hd.col : 0;
      } else { f.source = "template"; f.template = f.template || ""; }
    }
    rerun();
  });
  const fbSet = (f, i, v) => {
    f.fallback = f.fallback || [];
    if (v) f.fallback[i] = v; else f.fallback.splice(i, 1);
    rerun();
  };
  root.querySelectorAll("[data-fb]").forEach(sel => sel.onchange = () => {
    const s = sel.dataset.fb, i = s.lastIndexOf(":");
    fbSet(liEnsureField(s.slice(0, i)), +s.slice(i + 1), sel.value);
  });
  root.querySelectorAll("[data-fbadd]").forEach(b => b.onclick = () => {
    const f = liEnsureField(b.dataset.fbadd);
    (f.fallback = f.fallback || []).push("");
    renderListImport();
  });
  root.querySelectorAll("[data-ufb]").forEach(sel => sel.onchange = () => {
    const p = sel.dataset.ufb.split(":"), i = +p.pop(), u = +p.shift();
    fbSet(liEnsureUnitField(u, p.join(":")), i, sel.value);
  });
  root.querySelectorAll("[data-ufbadd]").forEach(b => b.onclick = () => {
    const s = b.dataset.ufbadd, j = s.indexOf(":");
    const f = liEnsureUnitField(+s.slice(0, j), s.slice(j + 1));
    (f.fallback = f.fallback || []).push("");
    renderListImport();
  });
  root.querySelectorAll("[data-utpl]").forEach(inp => inp.oninput = () => { const [u, key] = uk(inp); liEnsureUnitField(u, key).template = inp.value; liTestSoon(false); });
  root.querySelectorAll("[data-ustep]").forEach(b => b.onclick = () => { const [u, key] = uk(b); liStepEditor(key, u + 1); });
  root.querySelectorAll("[data-ulabel]").forEach(inp => inp.oninput = () => { r.units[+inp.dataset.ulabel].label = inp.value; });
  root.querySelectorAll("[data-ulabel]").forEach(inp => inp.onchange = () => rerun());
  root.querySelectorAll("[data-udel]").forEach(b => b.onclick = () => { r.units.splice(+b.dataset.udel, 1); rerun(); });
  const ua = q("#li-uadd");
  if (ua) ua.onclick = () => { r.units.push({ label: r.units.length ? "" : t("バックアップ"), fields: [] }); rerun(); };
  root.querySelectorAll("[data-match]").forEach(cb => cb.onchange = () => { r.match[cb.dataset.match] = cb.checked; });
  q("#li-keycol").onchange = e => { r.match.keyColumn = e.target.value; };
  root.querySelectorAll("[data-ctgt]").forEach(sel => sel.onchange = () => {
    const c = r.exclude[+sel.dataset.ctgt], v = sel.value, i = v.indexOf(":");
    c.target = v.slice(0, i); c.key = v.slice(i + 1); liTestSoon(false);
  });
  root.querySelectorAll("[data-cop]").forEach(sel => sel.onchange = () => { r.exclude[+sel.dataset.cop].op = sel.value; if (LI.sample) liRunTest(true); else renderListImport(); });
  root.querySelectorAll("[data-cval]").forEach(inp => inp.oninput = () => { r.exclude[+inp.dataset.cval].value = inp.value; liTestSoon(false); });
  root.querySelectorAll("[data-cdel]").forEach(b => b.onclick = () => { r.exclude.splice(+b.dataset.cdel, 1); if (LI.sample) liRunTest(true); else renderListImport(); });
  q("#li-cadd").onclick = () => {
    r.exclude.push({ target: "field", key: "name", op: "empty", value: "" });
    if (LI.sample) liRunTest(true); else renderListImport();
  };
  root.querySelectorAll(".li-secret").forEach(s => s.onclick = () => { s.textContent = s.textContent.startsWith("●") ? s.dataset.secret : "●●●●●●"; });
  q("#li-ecancel").onclick = async () => {
    if (liDirty() && !(await uiConfirm({ title: t("確認"), message: esc(t("保存していない変更があります。破棄しますか？")), okLabel: t("破棄"), danger: true }))) return;
    LI.view = "list"; LI.rule = null; App().ForgetListFile(); renderListImport();
  };
  q("#li-esave").onclick = () => liSave(false);
  const ex = q("#li-eexp");
  if (ex) ex.onclick = () => liSave(true);
}

async function liSave(thenExport) {
  try {
    const id = await App().SaveImportRule(liRuleOut());
    INV = await App().GetInventory();
    LI.rule.id = id;
    LI.ruleStart = JSON.stringify(LI.rule);
    if (thenExport) { await liExport(id); renderListImport(); return; }
    toast(t("保存しました"), "ok");
    LI.view = "list"; LI.rule = null;
    App().ForgetListFile();
    renderListImport();
  } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
}

// ---- the step editor (a dialog over the rule editor) ----

const LI_STEP_TYPES = [["split", "区切って残す"], ["map", "置き換え表"], ["replace", "文字を置き換える・削る"], ["regex", "正規表現"], ["trim", "前後の空白を除く"], ["width", "全角を半角にする"], ["lower", "小文字にする"], ["upper", "大文字にする"],
  ["guessOS", "機種から OS タイプを推測"], ["guessSet", "機種からコマンドセットを推測"]];
// The guessing steps belong to one field each.
const LI_STEP_FOR = { guessOS: "osType", guessSet: "commandSet" };

function liStepEditor(key, unit = 0) {
  // unit: 0 = the first device's field, u = the u-th further device's.
  const f = unit ? liEnsureUnitField(unit - 1, key) : liEnsureField(key);
  const steps = JSON.parse(JSON.stringify(f.steps || []));
  const secret = liField(key).kind === "secret";
  const sample = liSampleRow(unit + 1);
  let input = sample ? (sample.raw[key] || "") : "";
  const node = h(`<div>
    <h3>${esc(t("加工手順: {f}", { f: liFieldLabel(key) }))}${unit ? esc("（" + liUnitName(unit - 1) + "）") : ""}</h3>
    <div class="field"><label>${esc(t("元の値（見本・書き換えて試せます）"))}</label>
      <div class="row-inline" style="gap:6px"><input type="${secret ? "password" : "text"}" id="se-in" class="mono" style="flex:1" value="${esc(input)}" autocomplete="off" data-guard-skip>
      ${secret ? `<button class="btn sm" id="se-show" type="button">${esc(t("表示"))}</button>` : ""}</div></div>
    <div id="se-list" class="li-flow"></div>
    <div class="row-inline" style="gap:6px;margin-top:8px">
      <select id="se-type" data-guard-skip>${LI_STEP_TYPES.filter(([v]) => !LI_STEP_FOR[v] || LI_STEP_FOR[v] === key).map(([v, l]) => `<option value="${v}">${esc(t(l))}</option>`).join("")}</select>
      <button class="btn sm" id="se-add" type="button">${esc(t("＋ 手順を追加"))}</button>
    </div>
    <div class="li-result"><span class="muted">${esc(t("結果"))}</span> <span id="se-out" class="mono"></span></div>
    <div class="muted" style="font-size:12px;line-height:1.7">${esc(t("区切り文字が見つからない値は、手順の設定に従って「そのまま」か「空」にして次の手順へ渡します。結果が空なら既定値で埋めます。"))}</div>
    <div class="modal-actions">
      <button class="btn" id="se-cancel" type="button">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="se-ok" type="button">${esc(t("決定"))}</button>
    </div></div>`);
  openModal(node, "wide");
  const list = node.querySelector("#se-list");
  const inp = node.querySelector("#se-in");
  let shownPw = false;
  const show = v => v === "" ? t("（空）") : (secret && !shownPw ? "●".repeat(Math.min(v.length, 12)) : v);
  const cfg = (s, i) => {
    const a = `data-i="${i}"`;
    switch (s.type) {
      case "split": return `${esc(t("区切り文字"))} <input type="text" ${a} data-k="sep" value="${esc(s.sep || "")}" style="width:60px" class="mono" data-guard-skip>
        <select ${a} data-k="pos" data-guard-skip><option value="first" ${s.pos !== "last" ? "selected" : ""}>${esc(t("最初"))}</option><option value="last" ${s.pos === "last" ? "selected" : ""}>${esc(t("最後"))}</option></select> ${esc(t("で分けて"))}
        <select ${a} data-k="keep" data-guard-skip><option value="left" ${s.keep === "left" ? "selected" : ""}>${esc(t("左を残す"))}</option><option value="right" ${s.keep !== "left" ? "selected" : ""}>${esc(t("右を残す"))}</option></select>
        <span style="flex-basis:100%;height:0"></span>${esc(t("区切りが無い値は"))}
        <select ${a} data-k="miss" data-guard-skip><option value="" ${s.miss !== "empty" ? "selected" : ""}>${esc(t("そのまま"))}</option><option value="empty" ${s.miss === "empty" ? "selected" : ""}>${esc(t("空にする"))}</option></select>`;
      case "replace": return `<input type="text" ${a} data-k="from" value="${esc(s.from || "")}" style="width:120px" data-guard-skip> ${esc(t("を"))} <input type="text" ${a} data-k="to" value="${esc(s.to || "")}" placeholder="${esc(t("（削る）"))}" style="width:120px" data-guard-skip> ${esc(t("に"))}`;
      case "regex": return `<input type="text" ${a} data-k="pattern" value="${esc(s.pattern || "")}" class="mono" style="width:200px" placeholder="^(\\S+)\\s.*$" data-guard-skip> → <input type="text" ${a} data-k="to" value="${esc(s.to || "")}" class="mono" style="width:100px" placeholder="$1" data-guard-skip>`;
      case "map": return `<table class="li-maptbl"><thead><tr><th>${esc(t("比べ方"))}</th><th>${esc(t("元の値"))}</th><th>${esc(t("→ PalaTerm の値"))}</th><th></th></tr></thead><tbody>
        ${(s.rows || []).map((r, j) => `<tr><td><select ${a} data-j="${j}" data-k="match" data-guard-skip><option value="equals" ${r.match !== "contains" ? "selected" : ""}>${esc(t("一致"))}</option><option value="contains" ${r.match === "contains" ? "selected" : ""}>${esc(t("含む"))}</option></select></td>
          <td><input type="text" ${a} data-j="${j}" data-k="from" value="${esc(r.from || "")}" data-guard-skip></td>
          <td>${key === "osType" ? `<select ${a} data-j="${j}" data-k="to" data-guard-skip><option value="">${esc(t("（選ぶ）"))}</option>${PROFILES.map(p => `<option value="${esc(p.key)}" ${p.key === r.to ? "selected" : ""}>${esc(p.name)}</option>`).join("")}</select>` : `<input type="text" ${a} data-j="${j}" data-k="to" value="${esc(r.to || "")}" data-guard-skip>`}</td>
          <td><button class="btn sm act-del" type="button" data-rowdel="${i}:${j}">×</button></td></tr>`).join("")}
        </tbody></table><button class="btn sm" type="button" data-rowadd="${i}">${esc(t("＋ 行を追加"))}</button>
        <span style="margin-left:10px">${esc(t("表に無い値は"))} <select ${a} data-k="miss" data-guard-skip><option value="keep" ${s.miss !== "empty" ? "selected" : ""}>${esc(t("そのまま"))}</option><option value="empty" ${s.miss === "empty" ? "selected" : ""}>${esc(t("空にする（既定値で埋まる）"))}</option></select></span>`;
    }
    if (s.type === "guessOS") return `<span class="muted">${esc(t("機種の値（IX2215・C2960 など）から、この PalaTerm にある OS タイプを選びます。判定できないときは空になり、既定値で埋まります。先に置き換え表を置けば、そちらが優先です"))}</span>`;
    if (s.type === "guessSet") return `<span class="muted">${esc(t("機種の値から、この PalaTerm にあるコマンドセットのうち、名前がメーカー・種別（Cisco と L2 など）に合うものが 1 つだけのときに選びます。設定変更・保存のコマンドを含むコマンドセットは選びません。取り込み直しでは、既存の機器のコマンドセットは変えません"))}</span>`;
    return `<span class="muted">${esc(t("設定はありません"))}</span>`;
  };
  let seq = 0;
  const trace = async () => {
    const my = ++seq;
    let vals = [];
    try { vals = await App().TraceImportSteps(inp.value, steps); } catch (e) { }
    if (my !== seq) return;
    list.querySelectorAll("[data-after]").forEach(el => { const v = vals[+el.dataset.after]; el.textContent = "→ " + show(v === undefined ? "" : v); });
    node.querySelector("#se-out").textContent = show(vals.length ? vals[vals.length - 1] : inp.value);
  };
  const draw = () => {
    list.innerHTML = steps.length ? steps.map((s, i) => `<div class="li-sitem">
      <div class="li-sn">${i + 1}</div>
      <div style="min-width:0"><div class="li-st">${esc(t(LI_STEP_TYPES.find(x => x[0] === s.type)?.[1] || s.type))}</div>
        <div class="li-scfg">${cfg(s, i)}</div><div class="li-safter mono" data-after="${i}"></div></div>
      <div class="row-inline" style="gap:4px;align-items:flex-start">
        <button class="btn sm" type="button" data-up="${i}" ${i ? "" : "disabled"}>↑</button>
        <button class="btn sm act-del" type="button" data-del="${i}">×</button></div></div>`).join("")
      : `<div class="muted" style="font-size:13px;padding:6px 0">${esc(t("手順がありません。元の値がそのまま使われます"))}</div>`;
    trace();
  };
  list.addEventListener("input", e => {
    const el = e.target, i = el.dataset.i;
    if (i === undefined) return;
    const s = steps[+i], j = el.dataset.j;
    if (j !== undefined) s.rows[+j][el.dataset.k] = el.value; else s[el.dataset.k] = el.value;
    trace();
  });
  list.addEventListener("click", e => {
    const b = e.target.closest("button");
    if (!b) return;
    if (b.dataset.del !== undefined) steps.splice(+b.dataset.del, 1);
    else if (b.dataset.up !== undefined) { const i = +b.dataset.up; [steps[i - 1], steps[i]] = [steps[i], steps[i - 1]]; }
    else if (b.dataset.rowadd !== undefined) steps[+b.dataset.rowadd].rows.push({ match: "equals", from: "", to: "" });
    else if (b.dataset.rowdel !== undefined) { const [i, j] = b.dataset.rowdel.split(":").map(Number); steps[i].rows.splice(j, 1); }
    else return;
    draw();
  });
  // Chromium reports a select through "input" too; "change" is the sure one.
  list.addEventListener("change", e => {
    const el = e.target;
    if (el.tagName !== "SELECT" || el.dataset.i === undefined) return;
    const s = steps[+el.dataset.i], j = el.dataset.j;
    if (j !== undefined) s.rows[+j][el.dataset.k] = el.value; else s[el.dataset.k] = el.value;
    trace();
  });
  inp.oninput = trace;
  const sh = node.querySelector("#se-show");
  if (sh) sh.onclick = () => { shownPw = !shownPw; inp.type = shownPw ? "text" : "password"; sh.textContent = shownPw ? t("隠す") : t("表示"); trace(); };
  node.querySelector("#se-add").onclick = () => {
    const type = node.querySelector("#se-type").value;
    const s = { type };
    if (type === "split") Object.assign(s, { sep: "/", pos: "first", keep: "right" });
    if (type === "map") Object.assign(s, { rows: [{ match: "contains", from: "", to: "" }, { match: "contains", from: "", to: "" }], miss: "empty" });
    steps.push(s);
    draw();
  };
  const start = JSON.stringify(steps);
  node.querySelector("#se-cancel").onclick = async () => {
    if (JSON.stringify(steps) !== start && !(await subConfirm(esc(t("加工手順の変更を破棄しますか？")), t("破棄")))) return;
    closeModal();
  };
  node.querySelector("#se-ok").onclick = () => {
    for (const s of steps) {
      if (s.type === "split" && !s.sep) { toast(t("区切り文字を入力してください"), "err"); return; }
      if (s.type === "replace" && !s.from) { toast(t("置き換える文字を入力してください"), "err"); return; }
      if (s.type === "regex" && !s.pattern) { toast(t("正規表現を入力してください"), "err"); return; }
      if (s.type === "map") s.rows = (s.rows || []).filter(r => r.from);
    }
    f.steps = steps;
    closeModal();
    if (LI.sample) liRunTest(true); else renderListImport();
  };
  draw();
}

// ---- the import ----

async function liStartImport(path) {
  try {
    if (!path) path = await App().PickListFile();
    if (!path) return;
    const base = path.split(/[\\/]/).pop().replace(/\.[^.]+$/, "");
    const rules = liRules();
    // The rule last used on a file of this name, else the one last used.
    const byFile = rules.find(r => r.sampleName && r.sampleName.replace(/\.[^.]+$/, "") === base);
    const last = rules.slice().sort((a, b) => String(b.lastUsed || "").localeCompare(String(a.lastUsed || "")))[0];
    const groups = (INV.deviceGroups || []).map(g => g.name);
    const same = groups.find(g => g === base);
    LI.imp = { path, ruleId: (byFile || last || {}).id || "", group: same || base, newGroup: !same, decisions: {}, deleteIds: [], preview: null, error: "", filter: { skip: true } };
    LI.view = "import";
    renderListImport();
    liPreview();
  } catch (e) { toast(terr(e), "err"); }
}

let liPrevSeq = 0;
async function liPreview() {
  const imp = LI.imp;
  if (!imp) return;
  const seq = ++liPrevSeq;
  imp.busy = true;
  try {
    const p = await App().PreviewListImport({ path: imp.path, ruleId: imp.ruleId, group: imp.group, newGroup: imp.newGroup, decisions: imp.decisions, deleteIds: imp.deleteIds });
    if (seq !== liPrevSeq || LI.imp !== imp) return;
    imp.preview = p; imp.error = "";
    // A device linked to a row since it was ticked is no longer missing:
    // its tick goes, or the count and the warning would speak of it.
    const offered = new Set((p.plan.missing || []).map(m => m.id));
    imp.deleteIds = imp.deleteIds.filter(id => offered.has(id));
  } catch (e) {
    if (seq !== liPrevSeq || LI.imp !== imp) return;
    imp.preview = null; imp.error = terr(e);
  }
  imp.busy = false;
  liRedrawImport();
}

// liRedrawImport redraws the import screen after a preview. The group name
// being typed keeps its focus and caret, and a redraw waits for an IME
// conversion in progress (it would cut it off).
let liComposing = false, liRedrawLater = false;
function liRedrawImport() {
  if (LI.view !== "import" || !LI.imp) return;
  const root = document.getElementById("tab-import");
  const a = document.activeElement;
  const typing = a && a.id === "li-gnew" && root.contains(a);
  if (typing && liComposing) { liRedrawLater = true; return; }
  const sel = typing ? [a.selectionStart, a.selectionEnd] : null;
  liRenderImport(root);
  const g = sel && root.querySelector("#li-gnew");
  if (g) { g.focus(); g.setSelectionRange(sel[0], sel[1]); }
}

const LI_STATUS = {
  add: ["li-p-add", "追加"], upd: ["li-p-upd", "更新"], ask: ["li-p-ask", "要確認"],
  err: ["li-p-err", "取り込めない"], same: ["li-p-same", "変更なし"], skip: ["li-p-skip", "取り込まない"],
};
const LI_STATE = { take: "元のシステムで変更", keep: "PalaTerm で変更済み", ask: "両方で変更", guess: "推測（既存の機器は変えません）", new: "" };

// liRuleSkipped: a row the rule leaves out (an exclusion, no name) — no
// device to choose for it.
function liRuleSkipped(row) {
  return row.status === "skip" && !(row.candidates || []).length && (String(row.note || "").startsWith("除外条件") || row.note === "機器名が空です");
}

function liMatchNote(row) {
  switch (row.matchBy) {
    case "key": return t("前回の取り込みと同じ機器（{n}）", { n: row.matchName });
    case "name": return "";
    case "norm": return t("表記ゆれで一致: {n}", { n: row.matchName });
    case "ip": return t("IP アドレスだけ一致: {n}。同じ機器か確かめてください", { n: row.matchName });
    case "pick": return t("選んだ機器: {n}", { n: row.matchName });
  }
  return "";
}

function liRenderImport(root) {
  const imp = LI.imp, pv = imp.preview, plan = pv && pv.plan;
  const rules = liRules();
  const groups = (INV.deviceGroups || []).map(g => g.name);
  const counts = (plan && plan.counts) || {};
  const rows = plan ? plan.rows : [];
  // Hiding 取り込まない hides the rows the rule leaves out: a row with
  // candidates to pick from, or set to 取り込まない here, stays in view.
  const visible = rows.filter(r => !imp.filter[r.status] || (r.status === "skip" && !liRuleSkipped(r)));
  const devs = plan ? plan.devices : [];
  const devByID = new Map(devs.map(d => [d.id, d]));
  // The device picker per row offers what matters for that row (the device
  // it is linked to, the candidates) and 「別の機器を選ぶ…」 for the rest: a
  // list of every device on every row was rows × devices options.
  const linkOpts = row => {
    const ids = [];
    if (devByID.has(row.link)) ids.push(row.link);
    for (const c of row.candidates || []) if (!ids.includes(c.id)) ids.push(c.id);
    return ids.map(id => devByID.get(id)).filter(Boolean)
      .map(d => `<option value="${esc(d.id)}" ${row.link === d.id ? "selected" : ""}>${esc(t("既存: {n}", { n: d.name }))}${d.host ? " (" + esc(d.host) + ")" : ""}</option>`).join("");
  };
  const writes = rows.filter(r => ["add", "upd", "ask", "same"].includes(r.status) && r.link !== "skip" && !(r.errors || []).length);
  const nAdd = writes.filter(r => r.link === "new").length;
  const nUpd = writes.filter(r => r.link !== "new" && r.changes).length;
  const cellLine = (row, c) => {
    const ch = c.choice === "file";
    const cur = c.secret ? (c.cur ? "●●●●●●" : t("（空）")) : (c.cur || t("（空）"));
    const fil = c.secret ? (c.file ? "●●●●●●" : t("（空）")) : (c.file || t("（空）"));
    return `<div class="li-cell"><span class="li-ck">${esc(liFieldLabel(c.key))}</span>
      <span class="li-cv">${c.secret ? esc(t("変更あり")) : `<span class="${ch ? "li-old" : "li-new"}"><span class="li-src">PalaTerm</span> ${esc(cur)}</span> <span class="li-src">/</span> <span class="${ch ? "li-new" : "li-old"}"><span class="li-src">${esc(t("ファイル"))}</span> ${esc(fil)}</span>`}</span>
      ${LI_STATE[c.state] ? `<span class="li-cs li-cs-${c.state}">${esc(t(LI_STATE[c.state]))}</span>` : c.guessed ? `<span class="li-guess">${esc(t("推測"))}</span>` : ""}
      <select data-choice="${row.id}:${esc(c.key)}" data-guard-skip>
        <option value="keep" ${ch ? "" : "selected"}>${esc(t("PalaTerm の値を残す"))}</option>
        <option value="file" ${ch ? "selected" : ""}>${esc(t("ファイルの値にする"))}</option></select></div>`;
  };
  const summary = row => {
    const vals = (row.cells || []).filter(c => c.key !== "name" && c.key !== "host" && c.key !== "site" && c.file).slice(0, 5)
      .map(c => esc(liFieldLabel(c.key)) + ": " + (c.secret ? "●●●" : esc(c.file)) + (c.filled ? `<span class="li-filled">${esc(t("既定"))}</span>` : "") + (c.guessed ? `<span class="li-guess">${esc(t("推測"))}</span>` : ""));
    return vals.join("、");
  };
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("取り込み: {f}", { f: imp.path.split(/[\\/]/).pop() }))}</div>
        <div class="page-sub">${esc(t("内容を確かめてから「取り込む」を押してください。この時点では何も変わっていません"))}</div></div>
      <div class="row-inline" style="gap:6px;white-space:nowrap">
        <button class="btn" id="li-repick">${esc(t("ファイルを選び直す"))}</button>
        <button class="btn" id="li-back">${esc(t("戻る"))}</button>
      </div>
    </div>
    <div class="panel li-sec">
      <div class="li-fields">
        <div class="field" style="min-width:260px"><label>${esc(t("変換ルール"))}</label><select id="li-irule">
          ${rules.map(r => `<option value="${esc(r.id)}" ${r.id === imp.ruleId ? "selected" : ""}>${esc(r.name)}</option>`).join("")}</select></div>
        ${pv ? `<div class="field"><label>${esc(t("読み取ったシート"))}</label><div style="padding-top:6px">${esc(pv.sheet)} <span class="muted">(${esc(pv.format)})</span></div></div>` : ""}
      </div>
      <div class="li-target">
        <label class="li-opt ${imp.newGroup ? "on" : ""}"><span class="row-inline" style="gap:8px"><input type="radio" name="li-tgt" value="new" ${imp.newGroup ? "checked" : ""}> <b>${esc(t("新しいグループを作る"))}</b></span>
          <input type="text" id="li-gnew" value="${esc(imp.newGroup ? imp.group : "")}" placeholder="${esc(t("グループ名"))}" data-guard-skip></label>
        <label class="li-opt ${imp.newGroup ? "" : "on"}"><span class="row-inline" style="gap:8px"><input type="radio" name="li-tgt" value="old" ${imp.newGroup ? "" : "checked"} ${groups.length ? "" : "disabled"}> <b>${esc(t("既存のグループへ取り込む"))}</b></span>
          <select id="li-gold" ${groups.length ? "" : "disabled"} data-guard-skip>${groups.map(g => `<option value="${esc(g)}" ${!imp.newGroup && g === imp.group ? "selected" : ""}>${esc(g)}</option>`).join("")}</select>
          <span class="muted" style="font-size:11px">${esc(t("同じ機器は更新します。PalaTerm で直した値は残します"))}</span></label>
      </div>
    </div>
    ${imp.error ? `<div class="li-err">${esc(imp.error)}</div>` : ""}
    ${pv && (pv.warnings || []).length ? `<div class="li-warn">${pv.warnings.map(w => esc(terr(w))).join("<br>")}</div>` : ""}
    ${pv && (pv.missing || []).length ? `<div class="li-warn">${esc(t("ファイルに無い列: {c}（その項目は既定値で埋めます）", { c: pv.missing.join("、") }))}</div>` : ""}
    ${plan ? `<div class="panel">
      <div class="li-h" style="padding:12px 14px 0"><b>${esc(t("プレビュー（{n} 行）", { n: rows.length }))}</b>
        <div class="chips">${Object.keys(LI_STATUS).filter(k => counts[k]).map(k => `<button class="li-pill ${LI_STATUS[k][0]}" data-filt="${k}" aria-pressed="${imp.filter[k] ? "false" : "true"}">${esc(t(LI_STATUS[k][1]))} ${counts[k]}</button>`).join("")}</div></div>
      <div class="scrollx"><table class="li-prev"><thead><tr><th style="width:56px">${esc(t("行"))}</th><th style="width:110px">${esc(t("判定"))}</th>
        <th style="width:170px">${esc(t("機器名"))}</th><th style="width:130px">${esc(t("IPアドレス"))}</th><th style="width:110px">${esc(t("拠点名"))}</th><th>${esc(t("内容・変更点"))}</th><th style="width:230px">${esc(t("どの機器に"))}</th></tr></thead><tbody>
        ${visible.length ? visible.map(row => {
          const [cls, lbl] = LI_STATUS[row.status] || ["", row.status];
          const diff = (row.cells || []).filter(c => c.state !== "same" && c.state !== "new");
          const excluded = liRuleSkipped(row);
          return `<tr class="li-r-${row.status}">
            <td class="muted">${row.unit > 1 ? `${row.line}-${row.unit}` : row.line}</td><td><span class="li-pill ${cls}">${esc(t(lbl))}</span></td>
            <td class="mono"><div class="clip">${esc(row.name || t("（空）"))}</div>${row.unit > 1 ? `<span class="li-unit">${esc(row.label || t("{n}台目", { n: row.unit }))}</span>` : ""}</td><td class="mono">${esc(row.host || "")}</td><td><div class="clip">${esc(row.site || "")}</div></td>
            <td>${row.status === "add" ? `<div class="muted" style="font-size:12px">${summary(row)}</div>` : ""}
              ${diff.map(c => cellLine(row, c)).join("")}
              ${liMatchNote(row) ? `<div class="li-note">${esc(liMatchNote(row))}</div>` : ""}
              ${row.note ? `<div class="li-note">${esc(row.exclusion ? liExclText(row) : terr(row.note))}</div>` : ""}
              ${(row.errors || []).map(e => `<div class="li-rerr">${esc(terr(e))}</div>`).join("")}</td>
            <td>${excluded ? "" : `<select data-link="${row.id}" data-guard-skip>
              <option value="new" ${row.link === "new" ? "selected" : ""}>${esc(t("新しい機器として追加"))}</option>
              <option value="skip" ${row.link === "skip" ? "selected" : ""}>${esc(t("取り込まない"))}</option>
              ${linkOpts(row)}
              ${devs.length ? `<option value="__pick">${esc(t("別の機器を選ぶ…"))}</option>` : ""}</select>`}</td></tr>`;
        }).join("") : `<tr><td colspan="7" class="empty">${esc(t("表示する行がありません"))}</td></tr>`}
      </tbody></table></div>
      ${(plan.missing || []).length ? `<div class="li-missing"><b>${esc(t("このファイルに無い機器が {n} 台あります。削除はしません。", { n: plan.missing.length }))}</b>
        <div class="li-mlist">${plan.missing.map(d => `<label class="row-inline" style="gap:6px"><input type="checkbox" data-mdel="${esc(d.id)}" ${imp.deleteIds.includes(d.id) ? "checked" : ""} data-guard-skip> ${esc(t("削除する"))}: <span class="mono">${esc(d.name)}</span> <span class="muted">${esc(d.host || "")}</span></label>`).join("")}</div></div>` : ""}
      <div class="li-foot">
        <span class="muted" style="font-size:12px">${esc(t("追加 {a}・更新 {u}。取り込めない行と取り込まない行は飛ばします", { a: nAdd, u: nUpd }))}${imp.deleteIds.length ? esc(t("。削除 {d}", { d: imp.deleteIds.length })) : ""}</span>
        <div class="row-inline" style="gap:6px"><button class="btn" id="li-icancel">${esc(t("やめる"))}</button>
        <button class="btn primary" id="li-apply" ${writes.length || imp.deleteIds.length ? "" : "disabled"}>${esc(t("取り込む"))}</button></div>
      </div>
    </div>` : imp.busy ? `<div class="empty">${esc(t("読み込み中…"))}</div>` : ""}`;
  liWireImport(root);
}

function liWireImport(root) {
  const imp = LI.imp;
  const q = s => root.querySelector(s);
  const again = () => { imp.preview && (imp.busy = true); liPreview(); };
  const leave = () => { LI.view = "list"; LI.imp = null; App().ForgetListFile(); renderListImport(); };
  q("#li-back").onclick = leave;
  if (q("#li-icancel")) q("#li-icancel").onclick = leave;
  q("#li-repick").onclick = () => liStartImport();
  q("#li-irule").onchange = e => { imp.ruleId = e.target.value; imp.decisions = {}; again(); };
  root.querySelectorAll("input[name=li-tgt]").forEach(rb => rb.onchange = () => {
    imp.newGroup = rb.value === "new";
    imp.group = imp.newGroup ? (q("#li-gnew").value || imp.path.split(/[\\/]/).pop().replace(/\.[^.]+$/, "")) : (q("#li-gold").value || "");
    imp.decisions = {}; imp.deleteIds = [];
    again();
  });
  // Typing the new group's name: the preview follows a moment after the
  // last key (not during an IME conversion), the field keeping its focus.
  let gt;
  const gn = q("#li-gnew");
  liComposing = false; liRedrawLater = false;
  const typed = () => {
    if (!imp.newGroup) return;
    imp.group = gn.value;
    clearTimeout(gt);
    if (!liComposing) gt = setTimeout(again, 400);
  };
  gn.oninput = typed;
  gn.addEventListener("compositionstart", () => { liComposing = true; clearTimeout(gt); });
  gn.addEventListener("compositionend", () => {
    liComposing = false;
    typed();
    if (liRedrawLater) { liRedrawLater = false; liRedrawImport(); }
  });
  q("#li-gold").onchange = e => { if (!imp.newGroup) { imp.group = e.target.value; imp.decisions = {}; imp.deleteIds = []; again(); } };
  root.querySelectorAll("[data-filt]").forEach(b => b.onclick = () => { imp.filter[b.dataset.filt] = !imp.filter[b.dataset.filt]; renderListImport(); });
  root.querySelectorAll("[data-link]").forEach(sel => sel.onchange = async () => {
    let v = sel.value;
    if (v === "__pick") {
      v = await liPickDevice((imp.preview && imp.preview.plan.devices) || []);
      if (!v) { renderListImport(); return; } // cancelled: the select as it was
    }
    const d = imp.decisions[sel.dataset.link] = imp.decisions[sel.dataset.link] || { link: "", choices: {} };
    d.link = v;
    again();
  });
  root.querySelectorAll("[data-choice]").forEach(sel => sel.onchange = () => {
    const i = sel.dataset.choice.indexOf(":");
    const line = sel.dataset.choice.slice(0, i), key = sel.dataset.choice.slice(i + 1);
    const d = imp.decisions[line] = imp.decisions[line] || { link: "", choices: {} };
    d.choices[key] = sel.value;
    again();
  });
  root.querySelectorAll("[data-mdel]").forEach(cb => cb.onchange = () => {
    imp.deleteIds = imp.deleteIds.filter(id => id !== cb.dataset.mdel);
    if (cb.checked) imp.deleteIds.push(cb.dataset.mdel);
    renderListImport();
  });
  const ap = q("#li-apply");
  if (ap) ap.onclick = async () => {
    const pv = imp.preview;
    const asks = pv.plan.rows.filter(r => r.status === "ask").length;
    // Both questions when both apply: ticking a deletion must not let the
    // rows to check go in unasked.
    if (asks && !(await uiConfirm({ title: t("確認"), message: esc(t("要確認の行が {n} 行あります。表示されている選択のとおりに取り込みますか？", { n: asks })), okLabel: t("取り込む") }))) return;
    if (imp.deleteIds.length && !(await uiConfirm({ title: t("確認"), message: esc(t("このファイルに無い機器 {n} 台を削除します。よろしいですか？", { n: imp.deleteIds.length })), okLabel: t("削除して取り込む"), danger: true }))) return;
    try {
      ap.disabled = true;
      const res = await App().ApplyListImport({ path: imp.path, ruleId: imp.ruleId, group: imp.group, newGroup: imp.newGroup, decisions: imp.decisions, deleteIds: imp.deleteIds, stamp: pv.stamp });
      await refreshInventory();
      const group = imp.group.trim();
      LI.view = "list"; LI.imp = null;
      App().ForgetListFile();
      const msg = t("取り込みました（追加 {a}・更新 {u}・削除 {d}）", { a: res.added, u: res.updated, d: res.deleted });
      if ((res.failed || []).length) toast(msg + " " + t("取り込めなかった行: {n}", { n: res.failed.length }), "warn"); else toast(msg, "ok");
      deviceView = { mode: "list", group };
      switchTab("devices");
    } catch (e) {
      ap.disabled = false;
      toast(t("取り込み失敗") + ": " + terr(e), "err");
      // The file or the rule changed: show what it is now.
      if (String(e).includes("プレビューのあとで変わりました")) liPreview();
    }
  };
}

// liPickDevice asks which device of the group a row stands for: a list
// narrowed as one types (the first 200 shown). Resolves the device ID, or
// "" when cancelled.
function liPickDevice(devs) {
  return new Promise(resolve => {
    const node = h(`<div>
      <h3>${esc(t("結びつける機器を選ぶ"))}</h3>
      <input type="text" id="lp-q" placeholder="${esc(t("機器名・IP アドレスで絞り込み"))}" autocomplete="off" style="width:100%;margin-bottom:8px" data-guard-skip>
      <div id="lp-list" class="li-picklist"></div>
      <div class="modal-actions"><button class="btn" id="lp-cancel" type="button">${esc(t("キャンセル"))}</button></div></div>`);
    openModal(node, "mid");
    const q = node.querySelector("#lp-q"), list = node.querySelector("#lp-list");
    const draw = () => {
      const w = q.value.trim().toLowerCase();
      const hit = devs.filter(d => !w || d.name.toLowerCase().includes(w) || (d.host || "").toLowerCase().includes(w));
      list.innerHTML = hit.slice(0, 200).map(d => `<button class="li-pickrow" type="button" data-id="${esc(d.id)}"><span class="mono">${esc(d.name)}</span> <span class="muted">${esc(d.host || "")}</span></button>`).join("")
        + (hit.length > 200 ? `<div class="muted" style="font-size:12px;padding:6px">${esc(t("ほか {n} 台。絞り込んでください", { n: hit.length - 200 }))}</div>` : "")
        + (hit.length ? "" : `<div class="muted" style="padding:8px">${esc(t("一致する機器がありません"))}</div>`);
    };
    wireFilterInput(q, draw);
    list.onclick = e => { const b = e.target.closest("[data-id]"); if (b) { closeModal(); resolve(b.dataset.id); } };
    node.querySelector("#lp-cancel").onclick = () => { closeModal(); resolve(""); };
    draw();
    q.focus();
  });
}

// liLeftTab: the tab was switched away from — the file read for it (often
// with passwords) is not kept meanwhile; it is read again when needed.
function liLeftTab() { App().ForgetListFile().catch(() => {}); }

// liExclText words why a row is left out, in the page's language: the
// condition it met (row.exclusion), or the message (no device name).
function liExclText(row) {
  const c = row.exclusion;
  if (!c) return terr(row.excluded || row.note || "");
  const w = c.target === "field" ? liFieldLabel(c.key) : c.key;
  const v = c.value || "";
  const text = {
    empty: t("{w}が空", { w }), notempty: t("{w}が空でない", { w }),
    contains: t("{w}が「{v}」を含む", { w, v }), notcontains: t("{w}が「{v}」を含まない", { w, v }),
    equals: t("{w}が「{v}」", { w, v }), notequals: t("{w}が「{v}」でない", { w, v }),
  }[c.op] || w;
  return t("除外条件: {x}", { x: text });
}

// liCanSuggest: a new rule nothing has been chosen in yet (a guess never
// overwrites the user's own choices).
function liCanSuggest() {
  const r = LI.rule;
  return r && !r.id && !(r.fields || []).some(f => f.source) && !(r.units || []).length;
}

// liSuggest fills a new rule with a first guess from the sample: the header
// row and the usual fields' columns by their names (and a second device
// when the header repeats them), each marked 仮 until the user chooses.
async function liSuggest() {
  let sg;
  try { sg = await App().SuggestImportRule(LI.sample, liRuleOut()); } catch (e) { return; }
  if (!sg || (!(sg.fields || []).length && sg.headerRow === LI.rule.headerRow)) return;
  const snap = JSON.stringify(LI.rule);
  LI.tent = new Set();
  if (sg.headerRow && sg.headerRow !== LI.rule.headerRow) {
    LI.rule.headerRow = sg.headerRow;
    LI.rule.dataRow = 0;
    LI.tent.add("headerRow");
  }
  for (const f of sg.fields || []) {
    const fr = liEnsureField(f.key);
    fr.source = "column"; fr.column = f.column; fr.colIndex = f.colIndex;
    fr.steps = (f.steps || []).map(st => ({ ...st }));
    LI.tent.add(f.key);
  }
  if ((sg.unit || []).length) {
    LI.rule.units = [{ label: t("バックアップ"), fields: sg.unit.map(f => ({ key: f.key, source: "column", column: f.column, colIndex: f.colIndex, steps: (f.steps || []).map(st => ({ ...st })) })) }];
    for (const f of sg.unit) LI.tent.add("u0:" + f.key);
  }
  LI.tentN = (sg.fields || []).length + (sg.unit || []).length;
  LI.tentUndo = snap;
}

// liColLetter is a header's Excel column letter ("" when the sample has
// no such header); liColOpt names a column as every choice does:
// 「列(AE): 資産番号」.
function liColLetter(n) {
  const h = ((LI.test && LI.test.headers) || []).find(x => x.name === n);
  return h ? colLetter(h.col) : "";
}
function liColOpt(n) {
  const l = liColLetter(n);
  return l ? t("列({l}): {c}", { l, c: n }) : t("列: {c}", { c: n });
}

// liSameAs words what device 2 takes from device 1 for a field: the column
// (or the combination, the default) device 1 reads.
function liSameAs(key) {
  const fr = liFieldRule(key) || {};
  if (fr.source === "column") return t("1台目と同じ {c}", { c: liColOpt(fr.column) });
  if (fr.source === "template") return t("1台目と同じ 組み立て {t}", { t: fr.template || "" });
  if (fr.default) return t("1台目と同じ（既定値 {v}）", { v: liField(key).kind === "secret" ? "●●●" : fr.default });
  return t("1台目と同じ（使わない）");
}

// liUnitRes is what device u+2's cell of a field shows under its choice:
// the sample's value for it, or why there is none.
function liUnitRes(u, key) {
  const uf = liUnitField(u, key) || {};
  const base = liFieldRule(key) || {};
  const smp = liSampleRow(u + 2);
  const isName = key === "name";
  if (isName && !uf.source) return `<span class="li-needtxt">${esc(t("必須: 2台目の機器名の列を選びます。この列が空の行には 2台目を作りません"))}</span>`;
  if (smp && smp.values && key in smp.values && (uf.source || base.source || base.default)) {
    return "→ " + liShowValue(key, smp.values[key]) + liGuessNote(smp, key) + liFromNote(smp, key) + (isName ? " " + esc(t("（見本 {n} 行目）", { n: smp.line })) : "");
  }
  if (isName && LI.test) return esc(t("見本の先頭の行には 2台目がありません"));
  return "";
}

// liFromNote: which fallback column a sample value came from (nothing when
// from the field's own column).
function liFromNote(row, key) {
  const from = row && row.from && row.from[key];
  if (!from) return "";
  const h = ((LI.test && LI.test.headers) || []).find(x => x.name === from);
  return ` <span class="li-from">${esc(h ? t("（列({l}) から）", { l: colLetter(h.col) }) : t("（{c} から）", { c: from }))}</span>`;
}

// liGuessNote marks a value a guessing step chose.
function liGuessNote(row, key) {
  return row && (row.guessed || []).includes(key) ? ` <span class="li-guess">${esc(t("推測"))}</span>` : "";
}
