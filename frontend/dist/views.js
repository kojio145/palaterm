// Tab renderers and editors for PalaTerm. Loaded after app.js.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

// ---- Devices tab ----
// The connection methods as the editor names them (SSH / Telnet / シリアル),
// not the stored keys: the list's cell read "ssh" beside a header in words.
const CONN_LABELS = { ssh: "SSH", telnet: "Telnet", serial: "シリアル" };
function connLabel(c) { return c === "serial" ? t(CONN_LABELS.serial) : (CONN_LABELS[c] || c); }
function connOptions(sel) {
  return ["ssh", "telnet", "serial"].map(c =>
    `<option value="${c}" ${sel === c ? "selected" : ""}>${esc(connLabel(c))}</option>`).join("");
}
function osOptions(sel) {
  return PROFILES.map(p => `<option value="${esc(p.key)}" ${sel === p.key ? "selected" : ""}>${esc(p.name)}</option>`).join("");
}
function cmdSetOptions(sel) {
  return `<option value="">${esc(t("（なし）"))}</option>` + (INV.commandSets || []).map(s =>
    `<option value="${esc(s.name)}" ${sel === s.name ? "selected" : ""}>${esc(s.name)}</option>`).join("");
}

// Device tab has two steps: pick a scope (all devices or a group), then the
// device list. deviceView tracks which step is showing.
let deviceView = { mode: "select", group: null };

function renderDevices() {
  if (typeof savePlace === "function") savePlace();
  if (deviceView.mode === "select") return renderGroupChooser();
  if (deviceView.mode === "grid") return renderGrid();
  return renderDeviceList();
}

// Count devices belonging to each group name.
function groupCount(name) {
  return (INV.devices || []).filter(d => (d.group || "") === name).length;
}
function membersOf(name) {
  return (INV.devices || []).filter(d => (d.group || "") === name).map(d => d.name);
}

// Step 1: choose a group (company), or create a new group.
function renderGroupChooser() {
  const root = document.getElementById("tab-devices");
  const devs = INV.devices || [];
  const groups = INV.deviceGroups || [];
  // Sort / filter of the cards. Sorting is a select here (cards have no
  // column headers); the stored card order is what drag-reorder edits, and
  // dragging is off while the cards are sorted or filtered.
  const LV = "groups";
  const focus = listViewFocus();
  const v = listView(LV);
  const visible = listViewApply(LV, groups, g => [g.name], (g, k) => k === "count" ? groupCount(g.name) : g.name);
  const active = listViewActive(LV);
  // Devices without a group (a deleted group leaves them there) get a card
  // of their own; nothing else in this tab would reach them.
  const ungroupedN = (INV.devices || []).filter(d => !d.group).length;
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("機器グループを選択"))}</div>
        <div class="page-sub">${esc(t("グループを選ぶか、新規に作成してください。グループ作成後にそのグループへ機器を追加します"))}</div></div>
      <div class="row-inline">
        <button class="btn" id="imp-csv">${esc(t("読み込み"))}</button>
        <button class="btn" id="exp-csv" ${devs.length ? "" : `disabled data-tip="${esc(t("書き出す機器がありません"))}"`}>${esc(t("一式書き出し"))}</button>
      </div>
    </div>
    ${groups.length ? `<div class="row-inline lv-bar" style="gap:8px;flex-wrap:wrap;margin-bottom:12px">
      <input id="lv-text-${LV}" class="btn" style="width:280px;text-align:left;cursor:text" placeholder="${esc(t("絞り込み（グループ名）"))}" value="${esc(v.text)}">
      <select id="grp-sort" class="btn" style="padding-right:8px">
        <option value="" ${!v.sort.key ? "selected" : ""}>${esc(t("並び順: 手動（ドラッグ）"))}</option>
        <option value="name:1" ${v.sort.key === "name" && v.sort.dir > 0 ? "selected" : ""}>${esc(t("並び順: 名前 ▲"))}</option>
        <option value="name:-1" ${v.sort.key === "name" && v.sort.dir < 0 ? "selected" : ""}>${esc(t("並び順: 名前 ▼"))}</option>
        <option value="count:-1" ${v.sort.key === "count" && v.sort.dir < 0 ? "selected" : ""}>${esc(t("並び順: 台数が多い順"))}</option>
        <option value="count:1" ${v.sort.key === "count" && v.sort.dir > 0 ? "selected" : ""}>${esc(t("並び順: 台数が少ない順"))}</option>
      </select>
      ${v.text.trim() ? `<button class="btn sm lv-clear" data-lv="${LV}">${esc(t("✕ 絞り込み解除"))}</button>` : ""}
    </div>` : ""}
    <div class="chooser">
      <button class="choice new" id="choice-new">
        <div class="choice-t">${esc(t("＋ 新規グループ作成"))}</div>
        <div class="choice-n">${esc(t("会社・拠点などの単位で作成"))}</div>
      </button>
      ${groups.length > 1 && devs.length ? `<div class="choice" role="button" tabindex="0" data-open="__ALL__" style="border-style:dashed" data-tip="${esc(t("すべてのグループの機器を 1 つの一覧で表示します（どのグループにある機器か探すときに）"))}">
        <div class="choice-t">${esc(t("全機器"))}</div>
        <div class="choice-n">${esc(t("{n} 台", { n: devs.length }))}</div>
      </div>` : ""}
      ${visible.map(g => `<div class="choice" role="button" tabindex="0" ${active ? "" : 'draggable="true"'} data-open="${esc(g.name)}" data-grp="${esc(g.name)}">
        <div class="row-inline" style="justify-content:space-between;align-items:flex-start">
          <div class="choice-t clip" data-tip="${esc(g.name)}">${esc(g.name)}</div>
          <button class="btn sm act-copy" type="button" data-dup="${esc(g.name)}" title="${esc(t("グループを複製"))}">${esc(t("複製"))}</button>
        </div>
        <div class="choice-n">${esc(t("{n} 台", { n: groupCount(g.name) }))} <span class="muted" style="font-size:11px">${esc(active ? t("（並び替え・絞り込み中はドラッグ不可）") : t("⠿ ドラッグで並替"))}</span> ${gridDraftBadge(g.name, true)}</div>
      </div>`).join("")}
      ${ungroupedN ? `<div class="choice" role="button" tabindex="0" data-open="${DEV_UNGROUPED}" style="border-style:dashed" data-tip="${esc(t("グループを削除したり、グループ欄を空にした機器です。開いて編集・グループへの移動（一括変更）ができます"))}">
        <div class="choice-t">${esc(t("（グループ未設定）"))}</div>
        <div class="choice-n">${esc(t("{n} 台", { n: ungroupedN }))} ${gridDraftBadge(GRID_UNGROUPED, true)}</div>
      </div>` : ""}
      ${groups.length && !visible.length ? `<div class="empty" style="grid-column:1/-1">${esc(t("絞り込みに一致するグループがありません"))}</div>` : ""}
    </div>
    ${!devs.length ? `<div class="muted" style="font-size:13px;line-height:1.9;margin-top:14px;max-width:720px">
      <b>${esc(t("はじめかた"))}</b><br>
      ${esc(t("① 「＋ 新規グループ作成」で会社・拠点などの単位を作る → ② グループを開いて機器を入れる（「＋ 機器を追加」で 1 台ずつ、「表で編集」で Excel から貼り付け、または「読み込み」で CSV・一式）→ ③ 実行タブでコマンドを流す。サンプルのコマンドセットはコマンドセットタブにあります"))}
    </div>` : ""}`;
  listViewWire(LV, root, renderGroupChooser, focus);
  const gs = document.getElementById("grp-sort");
  if (gs) gs.onchange = () => {
    const [k, d] = gs.value.split(":");
    v.sort = k ? { key: k, dir: parseInt(d, 10) || 1 } : { key: "", dir: 1 };
    renderGroupChooser();
  };

  document.getElementById("exp-csv").onclick = () => { if (devs.length) exportBundleDialog(""); };
  document.getElementById("imp-csv").onclick = () => importDialog("");
  document.getElementById("choice-new").onclick = newGroupPrompt;
  wireGridBadges(root);
  root.querySelectorAll("[data-open]").forEach(b => {
    b.onclick = e => {
      if (e.target.closest("[data-dup]")) return;
      deviceView = { mode: "list", group: b.dataset.open === "__ALL__" ? null : b.dataset.open };
      renderDevices();
    };
    b.onkeydown = e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); b.onclick(e); } };
  });
  root.querySelectorAll("[data-dup]").forEach(b => b.onclick = e => { e.stopPropagation(); duplicateGroupDialog(b.dataset.dup); });
  if (!active) wireGroupDrag(root);
}

// 複製: a new group with the same default credentials and copies of every
// device. Names only have to be unique within a group, so they stay as they
// are unless a suffix is typed.
function duplicateGroupDialog(name) {
  const n = groupCount(name);
  const node = h(`<div>
    <h3>${esc(t("グループを複製"))} <span class="muted" style="font-size:14px;font-weight:400">/ ${esc(name)}</span></h3>
    <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("グループ「{g}」の既定の認証情報と機器 {n} 台をコピーして新しいグループを作ります。機器名はグループが違えば同じままで構いません。", { g: name, n }))}</p>
    <div class="field"><label>${esc(t("新しいグループ名"))} <span class="req">${esc(t("必須"))}</span></label><input id="dg-name" value="${esc(name + "_copy")}" autofocus></div>
    <div class="field"><label>${esc(t("機器名の接尾辞（任意）"))}</label><input id="dg-suffix" value="" placeholder="${esc(t("例: _new（空欄なら同じ機器名のまま）"))}">
      <div class="muted" style="font-size:12px;margin-top:4px">${esc(t("入力すると、コピーした機器名の末尾に付きます（例: sw01 → sw01_new）"))}</div></div>
    <div class="modal-actions">
      <button class="btn" id="dg-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="dg-ok">${esc(t("複製する"))}</button>
    </div>
  </div>`);
  openModal(node, "mid");
  node.querySelector("#dg-cancel").onclick = closeModal;
  const go = async () => {
    const nn = node.querySelector("#dg-name").value.trim();
    const suffix = node.querySelector("#dg-suffix").value;
    if (!nn) { toast(t("グループ名を入力してください"), "err"); return; }
    try {
      const cnt = await App().CopyDeviceGroup(name, nn, suffix);
      closeModal(); await refreshInventory();
      toast(t("グループ「{g}」を複製しました（機器 {n} 台）", { g: nn, n: cnt }), "ok");
    } catch (e) { toast(t("複製失敗") + ": " + terr(e), "err"); }
  };
  node.querySelector("#dg-ok").onclick = go;
  node.querySelector("#dg-name").addEventListener("keydown", e => { if (e.key === "Enter") go(); });
}

// Drag-and-drop reordering of group cards; persists the new order.
// The cards do not move while dragging: moving them under the pointer (the
// earlier way) made the grid reflow, put another card under the pointer and
// moved again, so where a card landed was hit-and-miss. Instead a bar marks
// the slot, the whole card area (gaps included) accepts the drop, and the
// order changes once, on drop.
function wireGroupDrag(root) {
  const area = root.querySelector(".chooser");
  if (!area) return;
  const cards = () => [...area.querySelectorAll("[data-grp]")];
  let dragEl = null, target = null, before = true;
  const clearMark = () => area.querySelectorAll(".drop-before, .drop-after").forEach(c => c.classList.remove("drop-before", "drop-after"));
  // The card nearest the pointer, and which side of it: cards sit in rows,
  // so left/right of its centre decides (above/below for a pointer outside
  // every row).
  const locate = (x, y) => {
    let best = null, bestD = Infinity;
    for (const c of cards()) {
      const r = c.getBoundingClientRect();
      const dx = x < r.left ? r.left - x : x > r.right ? x - r.right : 0;
      const dy = y < r.top ? r.top - y : y > r.bottom ? y - r.bottom : 0;
      const d = dx * dx + dy * dy * 4; // prefer the pointer's own row
      if (d < bestD) { bestD = d; best = c; }
    }
    if (!best) return null;
    const r = best.getBoundingClientRect();
    const inRow = y >= r.top && y <= r.bottom;
    return { card: best, before: inRow ? x < r.left + r.width / 2 : y < r.top };
  };
  cards().forEach(card => {
    card.addEventListener("dragstart", e => {
      dragEl = card; card.classList.add("dragging"); area.classList.add("drag-active");
      // Some WebViews need data set for the drag to start at all.
      if (e.dataTransfer) { e.dataTransfer.setData("text/plain", card.dataset.grp); e.dataTransfer.effectAllowed = "move"; }
    });
    card.addEventListener("dragend", () => {
      card.classList.remove("dragging"); area.classList.remove("drag-active");
      clearMark(); dragEl = target = null;
    });
  });
  area.addEventListener("dragover", e => {
    if (!dragEl) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
    const hit = locate(e.clientX, e.clientY);
    if (!hit) return;
    if (hit.card === target && hit.before === before) return;
    clearMark();
    target = hit.card; before = hit.before;
    if (target !== dragEl) target.classList.add(before ? "drop-before" : "drop-after");
  });
  area.addEventListener("drop", async e => {
    if (!dragEl) return;
    e.preventDefault();
    const moving = dragEl, at = target, side = before;
    clearMark();
    if (!at || at === moving) return;
    const order = cards().map(c => c.dataset.grp).filter(n => n !== moving.dataset.grp);
    let i = order.indexOf(at.dataset.grp);
    if (!side) i++;
    order.splice(i, 0, moving.dataset.grp);
    const was = (INV.deviceGroups || []).map(g => g.name);
    if (order.join("\u0000") === was.join("\u0000")) return; // dropped where it was
    try {
      await App().ReorderDeviceGroups(order); await refreshInventory();
      toast(t("並び順を保存しました"), "ok");
    } catch (err) { toast(t("保存失敗") + ": " + terr(err), "err"); }
  });
}

// Create an empty group (name only), then jump into its (empty) device list.
function newGroupPrompt() {
  const node = h(`<div>
    <h3>${esc(t("新規グループ作成"))}</h3>
    <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("会社名・拠点名など。作成後にこのグループへ機器を追加できます。"))}</p>
    <div class="field"><label>${esc(t("グループ名"))} <span class="req">${esc(t("必須"))}</span></label><input id="ng-name" placeholder="${esc(t("例: A社"))}" autofocus></div>
    <div class="modal-actions">
      <button class="btn" id="ng-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="ng-create">${esc(t("作成して機器追加へ"))}</button>
    </div>
  </div>`);
  openModal(node, "mid");
  const create = async () => {
    const name = node.querySelector("#ng-name").value.trim();
    if (!name) { toast(t("グループ名を入力してください"), "err"); return; }
    if ((INV.deviceGroups || []).some(g => g.name === name)) { toast(t("同名のグループが既にあります"), "err"); return; }
    try {
      await App().SaveDeviceGroup({ name });
      closeModal();
      await refreshInventory();
      deviceView = { mode: "list", group: name };
      renderDevices();
      toast(t("グループ「{g}」を作成しました。機器を追加してください", { g: name }), "ok");
    } catch (e) { toast(t("作成失敗") + ": " + terr(e), "err"); }
  };
  node.querySelector("#ng-cancel").onclick = closeModal;
  node.querySelector("#ng-create").onclick = create;
  node.querySelector("#ng-name").addEventListener("keydown", e => { if (e.key === "Enter") create(); });
}

// deviceView.group for the （グループ未設定） card: the devices without a group.
// Same value as the run tab's RUN_UNGROUPED, so switching tabs carries it over.
const DEV_UNGROUPED = "__ungrouped__";

// Devices ticked in the list's first column for a bulk change / delete. Kept
// across re-renders (inline edits, filtering); IDs that no longer exist drop
// out on the next render.
const devSel = new Set();

// A cell of the list being typed into under an IME: redrawing the table
// would cut the composition off (the save of the previous cell lands while
// the next is being typed), so the redraw waits for the commit.
let cellComposing = false, cellRenderPending = false;

function renderDeviceList() {
  if (cellComposing) { cellRenderPending = true; return; }
  const root = document.getElementById("tab-devices");
  let devs = INV.devices || [];
  // Scope to the chosen group (by the device's Group field), if any.
  const scopeGroup = deviceView.group
    ? (INV.deviceGroups || []).find(g => g.name === deviceView.group) : null;
  // A remembered group that is gone (deleted, or another vault): back to
  // the chooser rather than an unlabelled list.
  if (deviceView.group && deviceView.group !== DEV_UNGROUPED && !scopeGroup) {
    deviceView = { mode: "select", group: null };
    return renderGroupChooser();
  }
  if (scopeGroup) {
    devs = devs.filter(d => (d.group || "") === scopeGroup.name);
  }
  const ungroupedView = !scopeGroup && deviceView.group === DEV_UNGROUPED;
  if (ungroupedView) devs = devs.filter(d => !d.group);
  // Sort / filter (display only; see listView in app.js).
  const LV = "devices";
  const focus = listViewFocus();
  // A cell being edited in place: saving one redraws the table, which
  // would drop the focus — Tab to the next cell, type, Tab… has to keep
  // flowing, so the cell that has the focus (the next one, by the time the
  // save's redraw comes) gets it back below.
  const ae = document.activeElement;
  const cellCls = ae && ae.dataset && ae.dataset.id ? [...ae.classList].find(c => c.startsWith("inl-")) : "";
  // What was typed into that next cell so far goes back too: the save of
  // the previous cell takes a moment (the vault's key derivation), and the
  // redraw at its end used to replace the half-typed value with the saved one.
  const cellFocus = cellCls ? { id: ae.dataset.id, cls: cellCls, pos: ae.selectionStart,
    typed: ae.tagName === "INPUT" && ae.value !== ae.defaultValue ? ae.value : null } : null;
  // The site filter belongs to the scope it was chosen in: a site of group A
  // kept filtering group B (which has no such site) to an empty table while
  // the select said 拠点: すべて.
  const lv = listView(LV);
  const sites = [...new Set(devs.map(d => d.site || "").filter(Boolean))].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));
  if (lv.sel.site && !sites.includes(lv.sel.site)) lv.sel.site = "";
  if (lv.sel.group && !(INV.deviceGroups || []).some(g => g.name === lv.sel.group)) lv.sel.group = "";
  // 全機器: the group is a column and a filter here (that view exists to
  // find which group a device is in).
  const allView = !scopeGroup && !ungroupedView;
  const visible = listViewApply(LV, devs,
    d => (d.cliOff ? [d.name, d.host, d.site, d.role, t("GUI のみ")] : [d.name, d.host, d.site, d.role, connLabel(d.conn), profileName(d.osType), d.commandSet]).concat(allView ? [d.group || ""] : []),
    (d, k) => d.cliOff && (k === "conn" || k === "os" || k === "commandSet") ? "" : k === "os" ? profileName(d.osType) : k === "group" ? (d.group || "") : d[k],
    { site: d => d.site || "", group: d => d.group || "" });
  const groupNames = (INV.deviceGroups || []).map(g => g.name);
  const rows = visible.map(d => {
    const nb = ((d.bastions || []).length || (d.bastion && d.bastion.host ? 1 : 0)) + (d.useMyBastion ? Math.max((INV.myBastions || []).filter(b => b.host).length, 1) : 0);
    return `
    <tr data-row="${esc(d.id)}" data-key="${esc(d.id)}">
      <td><input type="checkbox" class="dev-sel" data-enable="${esc(d.id)}" ${devSel.has(d.id) ? "checked" : ""}></td>
      ${listViewDragCell(LV)}
      <td><input class="cell inl-name" data-id="${esc(d.id)}" data-name="${esc(d.name)}" value="${esc(d.name)}" style="font-weight:600${DEVICE_TINTS[d.termColor] ? `;box-shadow:inset 4px 0 0 ${DEVICE_TINTS[d.termColor].band}` : ""}" data-tip="${esc(scopeGroup || !d.group ? d.name : d.name + "  /  " + t("グループ「{g}」", { g: d.group }))}"></td>
      ${allView ? `<td><div class="clip" data-tip="${esc(d.group || t("（グループ未設定）"))}">${esc(d.group || t("（未設定）"))}</div></td>` : ""}
      <td><input class="cell inl-host mono" data-id="${esc(d.id)}" value="${esc(d.host)}" data-tip="${esc(d.host)}"></td>
      <td><input class="cell inl-site" data-id="${esc(d.id)}" value="${esc(d.site || "")}" placeholder="${esc(t("拠点"))}" data-tip="${esc(d.site || "")}"></td>
      <td><input class="cell inl-role" data-id="${esc(d.id)}" value="${esc(d.role || "")}" placeholder="${esc(t("役割"))}" data-tip="${esc(d.role || "")}"></td>
      ${d.cliOff ? `<td colspan="3"><span class="badge" data-tip="${esc(t("CLI を使わない機器です（GUI だけ）。機器の編集で「CLI を使う」をオンにすると接続方式・OS・コマンドセットを設定できます"))}">${esc(t("GUI のみ"))}</span>
        <span class="muted mono" style="font-size:12px;margin-left:6px">${esc((d.guiScheme === "http" ? "http" : "https") + ":" + (d.guiPort || (d.guiScheme === "http" ? 80 : 443)))}</span>
        ${d.useGroupCreds ? `<span class="badge" data-tip="${esc(t("グループ既定の認証情報でログインします"))}">${esc(t("既定"))}</span>` : ""}</td>` : `
      <td>
        <select class="cell inl-conn" data-id="${esc(d.id)}" data-tip="${esc(connLabel(d.conn) + (nb ? "  " + t("踏み台 {n} 段", { n: nb }) + (d.bastionMode === "tunnel" ? " " + t("（SSH トンネル）") : "") : ""))}">${connOptions(d.conn)}</select>
        ${nb ? `<span class="muted" style="font-size:11px">${esc(t("踏{n}", { n: nb }))}</span>` : ""}
        ${d.useGroupCreds ? `<span class="badge" data-tip="${esc(t("グループ既定の認証情報でログインします"))}">${esc(t("既定"))}</span>` : ""}
      </td>
      <td><select class="cell inl-os" data-id="${esc(d.id)}" data-tip="${esc(profileName(d.osType))}">${osOptions(d.osType)}</select></td>
      <td><select class="cell inl-set" data-id="${esc(d.id)}" data-tip="${esc(d.commandSet || t("（なし）"))}">${cmdSetOptions(d.commandSet)}</select></td>`}
      <td class="ops"><div class="ops-btns">
        <button class="btn sm act-conn" data-term="${esc(d.id)}" ${d.cliOff ? "disabled" : ""} data-tip="${esc(d.cliOff ? t("CLI を使わない機器です（機器の編集で「CLI を使う」をオンにすると使えます）") : CONNECT_TIP)}">${esc(t("接続"))}</button>
        ${guiButton(d)}
        <button class="btn sm act-edit" data-edit="${esc(d.id)}">${esc(t("編集"))}</button>
        <button class="btn sm act-copy" data-copy="${esc(d.id)}" data-tip="${esc(t("この機器を複製します"))}">${esc(t("複製"))}</button>
        <button class="btn sm act-del" data-del="${esc(d.id)}">${esc(t("削除"))}</button>
      </div></td>
    </tr>`;
  }).join("");

  const groups = INV.deviceGroups || [];
  // Forget ticks on devices that are gone; count the ones in this view.
  const allIds = new Set((INV.devices || []).map(d => d.id));
  [...devSel].forEach(id => { if (!allIds.has(id)) devSel.delete(id); });
  const selCount = devs.filter(d => devSel.has(d.id)).length;
  const scopeLabel = scopeGroup ? esc(t("グループ「{g}」", { g: scopeGroup.name })) : esc(ungroupedView ? t("（グループ未設定）") : t("全機器"));
  root.innerHTML = `
    <div class="page-head">
      <div class="row-inline" style="gap:12px">
        <button class="btn sm" id="back-choose">${esc(t("← 対象選択"))}</button>
        <div><div class="page-title">${esc(t("機器一覧"))} <span class="muted" style="font-size:14px;font-weight:400">/ ${scopeLabel}</span></div>
          <div class="page-sub">${esc(t("{n} 台表示 / 実行対象のオンオフは実行画面で。表の項目は直接編集できます", { n: devs.length }))}</div></div>
      </div>
      <div class="row-inline">
        ${scopeGroup ? `<button class="btn" id="grp-creds" data-tip="${esc(t("このグループの機器が共通で使うユーザー名・パスワード。機器ごとに「グループ既定を使う／個別に設定」を選べます"))}">${esc(t("グループ既定の認証情報…"))}</button>
        <button class="btn" id="grp-rename">${esc(t("グループ名変更"))}</button>
        <button class="btn danger" id="grp-delete">${esc(t("グループ削除"))}</button>` : ""}
        ${gridDraftBadge(gridScopeOf(deviceView.group))}
        <button class="btn" id="grid-open" data-tip="${esc(t("Excel のように表の上で複数の機器をまとめて編集します（コピー・貼り付け・Ctrl+D・連番など）。保存するまでは下書きです"))}">${esc(t("表で編集"))}</button>
        <button class="btn" id="imp-csv">${esc(t("読み込み"))}</button>
        <button class="btn" id="exp-csv" ${devs.length ? "" : `disabled data-tip="${esc(t("書き出す機器がありません"))}"`}>${esc(t("一式書き出し"))}</button>
        <button class="btn primary" id="add-dev">${esc(t("＋ 機器を追加"))}</button>
      </div>
    </div>
    ${devs.length ? listViewBar(LV, allView ? t("絞り込み（機器名・IP・グループ・拠点・役割・接続・OS・コマンドセット）") : t("絞り込み（機器名・IP・拠点・役割・接続・OS・コマンドセット）"),
      (allView && groupNames.length ? [{ id: "group", label: t("グループ: すべて"), values: groupNames }] : []).concat([{ id: "site", label: t("拠点: すべて"), values: sites }])) : ""}
    <div id="dev-selbar">${devSelBar(selCount)}</div>
    <div class="panel scrollx">
      ${devs.length === 0
        ? `<div class="empty" style="line-height:2">${esc(scopeGroup ? t("このグループに機器がありません。") : t("機器がありません。"))}<br>
            <span style="font-size:13px">${esc(t("「＋ 機器を追加」で 1 台ずつ登録、「表で編集」で Excel から貼り付け、「読み込み」で CSV や一式を取り込めます"))}</span><br>
            <span class="row-inline" style="gap:8px;justify-content:center;margin-top:10px"><button class="btn primary sm" id="empty-add">${esc(t("＋ 機器を追加"))}</button><button class="btn sm" id="empty-grid">${esc(t("表で編集"))}</button><button class="btn sm" id="empty-imp">${esc(t("読み込み"))}</button></span></div>`
        : visible.length === 0 ? `<div class="empty">${esc(t("絞り込みに一致する機器がありません"))}</div>`
        : `<table class="fixed" data-colw="${allView ? "devices-all" : "devices2"}"><thead><tr><th style="width:28px"><input type="checkbox" id="dev-chk-all" ${visible.length && visible.every(d => devSel.has(d.id)) ? "checked" : ""} title="${esc(t("表示中の行を全選択/全解除"))}"></th><th style="width:28px"></th>
            ${listViewTh(LV, "name", t("機器名"), "width:130px")}${allView ? listViewTh(LV, "group", t("グループ"), "width:110px") : ""}${listViewTh(LV, "host", t("IPアドレス"), "width:106px")}${listViewTh(LV, "site", t("拠点名"), "width:76px")}${listViewTh(LV, "role", t("役割"), "width:76px")}${listViewTh(LV, "conn", t("接続"), "width:80px")}
            ${listViewTh(LV, "os", "OS", "width:112px")}${listViewTh(LV, "commandSet", t("コマンドセット"), "width:112px")}<th style="width:242px"></th></tr></thead>
            <tbody id="dev-body">${rows}</tbody></table>`}
    </div>`;
  listViewWire(LV, root, renderDeviceList, focus);
  if (cellFocus) {
    const el = root.querySelector(`.${cellFocus.cls}[data-id="${CSS.escape(cellFocus.id)}"]`);
    if (el) {
      if (cellFocus.typed !== null && el.tagName === "INPUT") el.value = cellFocus.typed;
      el.focus(); try { el.setSelectionRange(cellFocus.pos, cellFocus.pos); } catch (e) { }
    }
  }
  root.querySelectorAll(".cell").forEach(el => {
    el.addEventListener("compositionstart", () => { cellComposing = true; });
    el.addEventListener("compositionend", () => {
      cellComposing = false;
      if (cellRenderPending) { cellRenderPending = false; setTimeout(renderDeviceList, 0); }
    });
  });
  wireColResize(root);
  const emptyAdd = document.getElementById("empty-add");
  if (emptyAdd) emptyAdd.onclick = () => editDevice(null, scopeGroup ? scopeGroup.name : "");
  const emptyGrid = document.getElementById("empty-grid");
  if (emptyGrid) emptyGrid.onclick = () => openGrid(gridScopeOf(deviceView.group)).catch(e => toast(terr(e), "err"));
  const emptyImp = document.getElementById("empty-imp");
  if (emptyImp) emptyImp.onclick = () => importDialog(scopeGroup ? scopeGroup.name : "");

  document.getElementById("back-choose").onclick = () => { deviceView = { mode: "select", group: null }; renderDevices(); };
  // Adding inside a group scope pre-assigns that group to the new device.
  document.getElementById("add-dev").onclick = () => editDevice(null, scopeGroup ? scopeGroup.name : "");
  if (scopeGroup) {
    document.getElementById("grp-creds").onclick = () => groupCredsDialog(scopeGroup.name);
    document.getElementById("grp-delete").onclick = async () => {
      if (!(await uiConfirm({ title: t("グループを削除"), message: t("グループ「<b>{g}</b>」を削除しますか？<br><span class=\"muted\" style=\"font-size:12px\">所属機器は残り、グループ未設定になります</span>", { g: esc(scopeGroup.name) }), okLabel: t("削除"), danger: true }))) return;
      const renamed = await App().DeleteDeviceGroup(scopeGroup.name);
      await refreshInventory();
      deviceView = { mode: "select", group: null };
      renderDevices();
      // Devices now ungrouped whose name an ungrouped device already had
      // were given "_2" etc. (names stay unique within a group).
      if (renamed && renamed.length) toast(t("グループを削除しました。グループ未設定の機器と名前が重なった機器を改名しました: {list}", { list: renamed.join(", ") }), "ok");
      else toast(t("グループを削除しました"), "ok");
    };
    document.getElementById("grp-rename").onclick = () => {
      const node = h(`<div>
        <h3>${esc(t("グループ名を変更"))}</h3>
        <div class="field"><label>${esc(t("新しいグループ名"))} <span class="req">${esc(t("必須"))}</span></label><input id="rn-name" value="${esc(scopeGroup.name)}" autofocus></div>
        <div class="modal-actions"><button class="btn" id="rn-cancel">${esc(t("キャンセル"))}</button>
          <button class="btn primary" id="rn-ok">${esc(t("変更"))}</button></div>
      </div>`);
      openModal(node, "mid");
      node.querySelector("#rn-cancel").onclick = closeModal;
      const rename = async () => {
        const nn = node.querySelector("#rn-name").value.trim();
        if (!nn) { toast(t("名前を入力してください"), "err"); return; }
        try {
          await App().RenameDeviceGroup(scopeGroup.name, nn);
          closeModal(); await refreshInventory();
          deviceView = { mode: "list", group: nn }; renderDevices();
          toast(t("変更しました"), "ok");
        } catch (e) { toast(t("変更失敗") + ": " + terr(e), "err"); }
      };
      node.querySelector("#rn-ok").onclick = rename;
      node.querySelector("#rn-name").addEventListener("keydown", e => { if (e.key === "Enter" && !e.isComposing) rename(); });
    };
  }
  document.getElementById("exp-csv").onclick = () => { if (devs.length) exportBundleDialog(scopeGroup ? scopeGroup.name : ""); };
  document.getElementById("imp-csv").onclick = () => importDialog(scopeGroup ? scopeGroup.name : "");
  document.getElementById("grid-open").onclick = () => openGrid(gridScopeOf(deviceView.group)).catch(e => toast(terr(e), "err"));
  wireGridBadges(root);

  root.querySelectorAll("[data-edit]").forEach(b => b.onclick = () =>
    editDevice(devById(b.dataset.edit)));
  wireConnectButtons(root);
  wireGUIButtons(root);
  root.querySelectorAll("[data-del]").forEach(b => b.onclick = () => delDevice(b.dataset.del));
  root.querySelectorAll("[data-copy]").forEach(b => b.onclick = () => {
    // The copy opens as a new, unsaved device filled from the source (its
    // name with _copy): nothing exists until 保存, so キャンセル leaves no
    // "sw01_copy" behind in the list.
    const src = devById(b.dataset.copy);
    if (!src) return;
    const taken = new Set((INV.devices || []).filter(d => (d.group || "") === (src.group || "")).map(d => d.name));
    let nm = src.name + "_copy";
    for (let i = 2; taken.has(nm); i++) nm = `${src.name}_copy${i}`;
    const tpl = JSON.parse(JSON.stringify(src));
    tpl.id = ""; tpl.name = nm;
    editDevice(null, src.group || "", tpl);
    const inp = document.getElementById("f-name"); if (inp) { inp.focus(); inp.select(); }
  });
  // Inline edits (name / host / conn / os / command set) save on change, with validation.
  root.querySelectorAll(".inl-name").forEach(el => el.onchange = async () => {
    const nn = el.value.trim();
    const old = el.dataset.name;
    if (!nn || nn === old) { el.value = old; return; }
    try {
      await App().RenameDevice(el.dataset.id, nn);
      await refreshInventory();
      toast(t("機器名を変更しました"), "ok");
    } catch (e) {
      toast(t("変更できません") + ": " + terr(e), "err");
      await refreshInventory(); // revert the cell to the stored value
    }
  });
  // Drag-reorder rows (multi-select on the ⠿ handle); persists the order.
  // Off while sorted or filtered: the rows do not show the stored order then.
  if (!listViewActive(LV)) wireRowReorder(root.querySelector("#dev-body"), async order => {
    try { await App().ReorderDevices(order); await refreshInventory(); }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); await refreshInventory(); }
  });
  root.querySelectorAll(".inl-host").forEach(el => el.onchange = () => inlineSave(el.dataset.id, { host: el.value.trim() }));
  root.querySelectorAll(".inl-site").forEach(el => el.onchange = () => inlineSave(el.dataset.id, { site: el.value.trim() }));
  root.querySelectorAll(".inl-role").forEach(el => el.onchange = () => inlineSave(el.dataset.id, { role: el.value.trim() }));
  // Switching the method here must carry the port with it, the way the editor
  // dialog already does: leaving 22 behind on a switch to Telnet points a
  // Telnet client at the SSH port, where it reads the "SSH-2.0-..." banner and
  // then waits forever for a login prompt. A port the user chose deliberately
  // (anything but the two standard ones) survives the switch.
  root.querySelectorAll(".inl-conn").forEach(el => el.onchange = () => {
    const patch = { conn: el.value };
    const dev = devById(el.dataset.id) || {};
    const port = dev.port || 0;
    if (el.value !== "serial" && (!port || port === 22 || port === 23)) {
      patch.port = el.value === "ssh" ? 22 : 23;
    }
    inlineSave(el.dataset.id, patch);
  });
  // Bulk selection: tick boxes (Shift+クリック = range), header = visible rows.
  // A tick redraws only the boxes and the selection bar, not the 300-row
  // table (each row carries two selects of a dozen options or more).
  const selIds = () => devs.filter(d => devSel.has(d.id)).map(d => d.id);
  const syncSel = () => {
    root.querySelectorAll(".dev-sel").forEach(cb => { cb.checked = devSel.has(cb.dataset.enable); });
    const all = document.getElementById("dev-chk-all");
    if (all) all.checked = visible.length > 0 && visible.every(d => devSel.has(d.id));
    const bar = document.getElementById("dev-selbar");
    if (bar) { bar.innerHTML = devSelBar(selIds().length); wireSelBar(); }
  };
  const applySel = (ids, on) => { ids.forEach(id => on ? devSel.add(id) : devSel.delete(id)); syncSel(); };
  wireShiftRange("devsel", [...root.querySelectorAll(".dev-sel")], applySel);
  const chkAll = document.getElementById("dev-chk-all");
  if (chkAll) chkAll.onclick = () => applySel(visible.map(d => d.id), chkAll.checked);
  function wireSelBar() {
    const bClear = document.getElementById("bulk-clear");
    if (bClear) bClear.onclick = () => { devs.forEach(d => devSel.delete(d.id)); syncSel(); };
    const bTile = document.getElementById("bulk-tile");
    if (bTile) bTile.onclick = () => tiledConnect(visible.filter(d => devSel.has(d.id)).concat(devs.filter(d => devSel.has(d.id) && !visible.includes(d))).map(d => d.id));
    const bEdit = document.getElementById("bulk-edit");
    if (bEdit) bEdit.onclick = () => bulkEditDialog(selIds());
    const bDel = document.getElementById("bulk-del");
    if (bDel) bDel.onclick = async () => {
      const ids = selIds();
      const names = devs.filter(d => devSel.has(d.id)).map(d => d.name);
      const list = names.slice(0, 20).join(", ") + (names.length > 20 ? " …" : "");
      if (!(await uiConfirm({ title: t("機器を一括削除"), message: t("選択した <b>{n} 台</b>を削除しますか？", { n: ids.length }) + `<br><span class="muted" style="font-size:12px">${esc(list)}</span>`, okLabel: t("削除"), danger: true }))) return;
      try { const n = await App().DeleteDevices(ids); ids.forEach(id => devSel.delete(id)); await refreshInventory(); toast(t("{n} 台を削除しました", { n }), "ok"); }
      catch (e) { toast(t("削除失敗") + ": " + terr(e), "err"); }
    };
  }
  wireSelBar();
  root.querySelectorAll(".inl-os").forEach(el => el.onchange = () => inlineSave(el.dataset.id, { osType: el.value }));
  root.querySelectorAll(".inl-set").forEach(el => el.onchange = () => inlineSave(el.dataset.id, { commandSet: el.value }));
}

// 並べて接続 (broadcast.go): the devices' terminal windows in a grid, with
// 同時入力 on only if the 設定 tab says so. Over 16 is refused before anything opens. direct: the run
// tab's 踏み台: 使わない.
let tiledMax = 0; // App.MaxTiled, asked once
async function tiledConnect(ids, direct) {
  if (!tiledMax) tiledMax = await App().MaxTiled().catch(() => 16);
  const max = tiledMax;
  const usable = ids.filter(id => { const d = devById(id); return d && !d.cliOff; });
  if (!usable.length) { toast(t("接続できる機器がありません（GUI だけの機器は対象外です）"), "err"); return; }
  if (usable.length > max) { toast(t("並べて接続できるのは {m} 台までです（{n} 台選択中）", { m: max, n: usable.length }), "err"); return; }
  try {
    const rep = await App().SpawnTiled(usable, !!direct) || {};
    const bc = " " + ((INV.settings || {}).tiledBroadcast ? t("（同時入力オン）") : t("（同時入力はオフ。必要な窓で「同時入力」を押してください）"));
    const skipped = (rep.skipped || []).concat(ids.filter(id => !usable.includes(id)).map(id => {
      const d = devById(id);
      return d ? d.name + ": " + t("CLI を使わない機器です") : t("機器が見つかりません（削除されたか、一覧が更新されました）");
    }));
    if (skipped.length) toast(t("{n} 台の窓を並べて開きました。開けなかった機器: {s}", { n: rep.opened || 0, s: skipped.map(terr).join(" / ") }) + bc, "warn");
    else toast(t("{n} 台の窓を並べて開きました", { n: rep.opened || 0 }) + bc + (direct ? " " + t("（踏み台なし）") : ""), "ok");
  } catch (e) { toast(t("接続失敗") + ": " + terr(e), "err"); }
}

// The bar above the list while devices are ticked ("" when none).
function devSelBar(selCount) {
  if (!selCount) return "";
  return `<div class="row-inline" style="gap:8px;margin:-4px 0 10px;padding:8px 12px;border:1px solid var(--accent);border-radius:10px;font-size:13px">
      <b>${esc(t("{n} 台を選択中", { n: selCount }))}</b>
      <button class="btn sm primary" id="bulk-tile" data-tip="${esc(t("選択した機器の単独接続窓をまとめて開き、画面いっぱいに並べます（最大 16 台。Tera Term の［ウィンドウ］-［並べて表示］と同じ）。各窓の「同時入力」をオンにすると、どれか 1 つの窓で打ったキーがオンの窓すべてに送られます（最初からオンで開くかは設定タブで選べます）"))}">${esc(t("並べて接続"))}</button>
      <button class="btn sm" id="bulk-edit">${esc(t("一括変更…"))}</button>
      <button class="btn sm danger" id="bulk-del">${esc(t("一括削除"))}</button>
      <button class="btn sm" id="bulk-clear">${esc(t("選択解除"))}</button>
      <span class="muted" style="font-size:12px">${esc(t("Shift+クリックで範囲を選択できます"))}</span>
    </div>`;
}

// グループ既定の認証情報: one username / password / enable password shared by
// the group's devices that opt in (機器の編集 → 認証情報の扱い). Devices that
// need something else keep their own; nothing here touches those.
function groupCredsDialog(groupName) {
  const g = (INV.deviceGroups || []).find(x => x.name === groupName) || { name: groupName };
  const members = (INV.devices || []).filter(d => (d.group || "") === groupName);
  const using = members.filter(d => d.useGroupCreds).length;
  const node = h(`<div>
    <h3>${esc(t("グループ既定の認証情報"))} <span class="muted" style="font-size:14px;font-weight:400">/ ${esc(groupName)}</span></h3>
    <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("このグループの機器のうち「グループ既定を使う」にした機器が、ここのユーザー名・パスワードでログインします（暗号化保存）。現在 {a} / {b} 台が使用中。", { a: using, b: members.length }))}</p>
    <div class="grid-2">
      <div class="field"><label>${esc(t("ユーザー名"))}</label><input id="gc-user" value="${esc(g.username || "")}" autofocus></div>
      <div class="field"><label>${esc(t("パスワード"))}</label><input id="gc-pw" type="password" value="${esc(g.password || "")}"></div>
    </div>
    <div class="field"><label>${esc(t("enable / 昇格パスワード（任意・ない機種は空欄）"))}</label><input id="gc-en" type="password" value="${esc(g.enablePassword || "")}"></div>
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer;font-size:13px"><input type="checkbox" id="gc-all" style="width:auto"> ${esc(t("このグループの全機器（{n} 台）を「グループ既定を使う」に切り替える", { n: members.length }))}</label>
    <div class="muted" style="font-size:12px;margin-top:4px">${esc(t("オフのままなら各機器の設定は変わりません。個別のパスワードが要る機器は、機器の編集画面で「この機器に個別に設定」を選んでください。"))}</div>
    <div class="modal-actions">
      <button class="btn" id="gc-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="gc-save" data-tip="Ctrl+Enter">${esc(t("保存"))}</button>
    </div>
  </div>`);
  openModal(node, "mid");
  wirePasswordToggles(node);
  node.querySelector("#gc-cancel").onclick = closeModal;
  const saveBtn = node.querySelector("#gc-save");
  saveBtn.onclick = async () => {
    const out = { ...g, name: groupName,
      username: node.querySelector("#gc-user").value,
      password: node.querySelector("#gc-pw").value,
      enablePassword: node.querySelector("#gc-en").value };
    saveBtn.disabled = true; // one save at a time (the switch below takes a moment)
    try {
      await App().SaveDeviceGroup(out);
      if (node.querySelector("#gc-all").checked) {
        // One write for the whole group: saving the devices one by one took
        // the vault's key derivation per device (minutes for 300).
        const ids = members.filter(d => !d.useGroupCreds).map(d => d.id);
        if (ids.length) await App().UpdateDevices(ids, { useGroupCreds: true });
      }
      closeModal(); await refreshInventory(); toast(t("保存しました"), "ok");
    } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
    finally { saveBtn.disabled = false; }
  };
}

// 一括変更: each field starts on 「変更しない」; only the ones changed are
// sent (UpdateDevices applies them to every selected device, all or none).
function bulkEditDialog(ids) {
  if (!ids.length) return;
  const keep = `<option value="__keep__" selected>${esc(t("（変更しない）"))}</option>`;
  const groups = (INV.deviceGroups || []).map(g => `<option value="${esc(g.name)}">${esc(g.name)}</option>`).join("");
  const sets = (INV.commandSets || []).map(s => `<option value="${esc(s.name)}">${esc(s.name)}</option>`).join("");
  const oses = PROFILES.map(p => `<option value="${esc(p.key)}">${esc(p.name)}</option>`).join("");
  const node = h(`<div>
    <h3>${esc(t("選択した {n} 台を一括変更", { n: ids.length }))}</h3>
    <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("「（変更しない）」以外にした項目だけが、選択した全機器に設定されます。"))}</p>
    <div class="grid-2">
      <div class="field"><label>${esc(t("グループ（移動）"))}</label><select id="bk-group" autofocus>${keep}<option value="">${esc(t("（グループ未設定）"))}</option>${groups}</select>
        <div id="bk-group-note" class="muted" style="font-size:12px;margin-top:4px;color:var(--warn)"></div></div>
      <div class="field"><label>${esc(t("コマンドセット"))}</label><select id="bk-set">${keep}<option value="">${esc(t("（なし）"))}</option>${sets}</select></div>
      <div class="field"><label>${esc(t("OSタイプ"))}</label><select id="bk-os">${keep}${oses}</select></div>
      <div class="field"><label>${esc(t("認証情報"))}</label><select id="bk-cred">${keep}<option value="group">${esc(t("グループ既定を使う"))}</option><option value="own">${esc(t("この機器に個別に設定"))}</option></select></div>
      <div class="field"><label>${esc(t("自分の踏み台（設定タブ）"))}</label><select id="bk-mine">${keep}<option value="on">${esc(t("先に通る"))}</option><option value="off">${esc(t("通らない"))}</option></select></div>
      <div class="field"><label>${esc(t("CLI / GUI"))}</label>
        <div class="row-inline" style="gap:6px"><select id="bk-cli" style="flex:1">${keep}<option value="on">${esc(t("CLI を使う"))}</option><option value="off">${esc(t("CLI を使わない"))}</option></select>
          <select id="bk-gui" style="flex:1">${keep}<option value="on">${esc(t("GUI を使う"))}</option><option value="off">${esc(t("GUI を使わない"))}</option></select></div></div>
      <div class="field"><label><input type="checkbox" id="bk-site-on" style="width:auto"> ${esc(t("拠点を変更"))}</label><input id="bk-site" placeholder="${esc(t("空欄にすると拠点を消します"))}"></div>
      <div class="field"><label><input type="checkbox" id="bk-role-on" style="width:auto"> ${esc(t("役割を変更"))}</label><input id="bk-role" placeholder="${esc(t("空欄にすると役割を消します"))}"></div>
      <div class="field"><label>${esc(t("端末の色"))}</label><select id="bk-tint">${keep}<option value="">${esc(t("なし（窓の色のまま）"))}</option>${deviceTintKeys().map(k => `<option value="${k}">■ ${esc(t(DEVICE_TINTS[k].name))}</option>`).join("")}</select></div>
    </div>
    <details style="margin-top:4px"><summary class="link" style="text-decoration:none;font-size:13px">${esc(t("認証情報をまとめて変更（パスワードの定期変更など）"))}</summary>
      <div class="muted" style="font-size:12px;margin:6px 0 8px">${esc(t("チェックした項目だけが、選択した全機器に設定されます。「グループ既定を使う」機器は、個別の欄が書き換わるだけでログインには既定が使われます。踏み台はその段がある機器にだけ入ります。"))}</div>
      <div class="grid-2">
        <div class="field"><label><input type="checkbox" id="bk-user-on" style="width:auto"> ${esc(t("機器のユーザー名"))}</label><input id="bk-user" autocomplete="off"></div>
        <div class="field"><label><input type="checkbox" id="bk-pass-on" style="width:auto"> ${esc(t("機器のパスワード"))}</label><input id="bk-pass" type="password" autocomplete="new-password"></div>
        <div class="field"><label><input type="checkbox" id="bk-enable-on" style="width:auto"> ${esc(t("enable パスワード"))}</label><input id="bk-enable" type="password" autocomplete="new-password"></div>
        <div class="field"><label>${esc(t("踏み台の段"))}</label><select id="bk-hop">${[1, 2, 3, 4, 5].map(n => `<option value="${n}">${esc(t("{n}段目", { n }))}</option>`).join("")}</select>
          <div id="bk-hop-note" class="muted" style="font-size:12px;margin-top:4px"></div></div>
        <div class="field"><label><input type="checkbox" id="bk-buser-on" style="width:auto"> ${esc(t("踏み台のユーザー名"))}</label><input id="bk-buser" autocomplete="off"></div>
        <div class="field"><label><input type="checkbox" id="bk-bpass-on" style="width:auto"> ${esc(t("踏み台のパスワード"))}</label><input id="bk-bpass" type="password" autocomplete="new-password"></div>
      </div>
    </details>
    <div class="modal-actions">
      <button class="btn" id="bk-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="bk-ok" data-tip="Ctrl+Enter">${esc(t("変更する"))}</button>
    </div>
  </div>`);
  openModal(node, "mid");
  wirePasswordToggles(node);
  const $n = id => node.querySelector(id);
  // How many of the selected devices have the chosen hop: a jump-host
  // password set on 12 of 50 is a rotation that missed 38, said here first.
  const hopNote = () => {
    const n = parseInt($n("#bk-hop").value, 10) || 1;
    const has = (INV.devices || []).filter(d => ids.includes(d.id) && (d.bastions || []).length >= n && d.bastions[n - 1].host).length;
    $n("#bk-hop-note").textContent = t("選択中 {m} / {n} 台にこの段の踏み台があります（ない機器は変わりません）", { m: has, n: ids.length });
    $n("#bk-hop-note").style.color = has < ids.length ? "var(--warn)" : "";
  };
  $n("#bk-hop").onchange = hopNote;
  hopNote();
  $n("#bk-site").oninput = () => { $n("#bk-site-on").checked = true; };
  $n("#bk-role").oninput = () => { $n("#bk-role-on").checked = true; };
  [["#bk-user", "#bk-user-on"], ["#bk-pass", "#bk-pass-on"], ["#bk-enable", "#bk-enable-on"], ["#bk-buser", "#bk-buser-on"], ["#bk-bpass", "#bk-bpass-on"]]
    .forEach(([inp, on]) => { $n(inp).oninput = () => { $n(on).checked = true; }; });
  // Moving devices that log in with their group's default credentials
  // switches them to the new group's (or, ungrouped, to their own): say so.
  const usingGroup = (INV.devices || []).filter(d => ids.includes(d.id) && d.useGroupCreds).length;
  const groupNote = () => {
    const g = $n("#bk-group").value, keepCred = $n("#bk-cred").value === "__keep__";
    $n("#bk-group-note").textContent = g === "__keep__" || !usingGroup || !keepCred ? ""
      : g === "" ? t("「グループ既定を使う」機器 {n} 台は、グループ未設定になるため各機器の個別の認証情報でログインします", { n: usingGroup })
      : t("「グループ既定を使う」機器 {n} 台は、移動先グループ「{g}」の既定の認証情報でログインします", { n: usingGroup, g });
  };
  $n("#bk-group").onchange = groupNote;
  $n("#bk-cred").onchange = groupNote;
  $n("#bk-cancel").onclick = closeModal;
  $n("#bk-ok").onclick = async () => {
    const patch = {};
    const pick = (id, key) => { const v = $n(id).value; if (v !== "__keep__") patch[key] = v; };
    pick("#bk-group", "group"); pick("#bk-set", "commandSet"); pick("#bk-os", "osType"); pick("#bk-tint", "termColor");
    const cred = $n("#bk-cred").value;
    if (cred !== "__keep__") patch.useGroupCreds = cred === "group";
    const onOff = (id, key) => { const v = $n(id).value; if (v !== "__keep__") patch[key] = v === "on"; };
    onOff("#bk-mine", "useMyBastion"); onOff("#bk-cli", "cli"); onOff("#bk-gui", "gui");
    if ($n("#bk-site-on").checked) patch.site = $n("#bk-site").value;
    if ($n("#bk-role-on").checked) patch.role = $n("#bk-role").value;
    if ($n("#bk-user-on").checked) patch.username = $n("#bk-user").value;
    if ($n("#bk-pass-on").checked) patch.password = $n("#bk-pass").value;
    if ($n("#bk-enable-on").checked) patch.enablePassword = $n("#bk-enable").value;
    if ($n("#bk-buser-on").checked || $n("#bk-bpass-on").checked) {
      patch.bastionHop = parseInt($n("#bk-hop").value, 10) || 1;
      if ($n("#bk-buser-on").checked) patch.bastionUsername = $n("#bk-buser").value;
      if ($n("#bk-bpass-on").checked) patch.bastionPassword = $n("#bk-bpass").value;
    }
    if (!Object.keys(patch).length) { toast(t("変更する項目を選んでください"), "err"); return; }
    try {
      const rep = await App().UpdateDevices(ids, patch) || {};
      closeModal(); await refreshInventory();
      if (patch.bastionHop && (rep.hopApplied || rep.hopMissing)) {
        const msg = t("{n} 台を変更しました。踏み台の認証情報は {m} 台に設定しました", { n: ids.length, m: rep.hopApplied || 0 })
          + (rep.hopMissing ? t("（{k} 台にはその段の踏み台がなく、変わっていません）", { k: rep.hopMissing }) : "");
        toast(msg, rep.hopMissing ? "warn" : "ok");
      } else toast(t("{n} 台を変更しました", { n: ids.length }), "ok");
    } catch (e) { toast(t("変更できません") + ": " + terr(e), "err"); }
  };
}

// inlineSave merges a patch into an existing device and saves it, showing a
// validation error (and reverting the table) if the edit is rejected.
async function inlineSave(id, patch) {
  const cur = devById(id);
  if (!cur) return;
  const updated = { ...cur, ...patch };
  try {
    await App().SaveDevice(updated);
    await refreshInventory();
  } catch (e) {
    toast(t("保存できません") + ": " + terr(e), "err");
    await refreshInventory(); // revert the cell to the stored value
  }
}

async function delDevice(id) {
  const d = devById(id);
  if (!d) return;
  if (!(await uiConfirm({ title: t("機器を削除"), message: t("機器「<b>{n}</b>」を削除しますか？", { n: esc(d.name) }), okLabel: t("削除"), danger: true }))) return;
  try { await App().DeleteDevice(id); await refreshInventory(); toast(t("削除しました"), "ok"); }
  catch (e) { toast(t("削除失敗") + ": " + terr(e), "err"); }
}

// The last choice of 踏み台の認証情報を含めない (a per-PC convenience).
function exportNoBastionCreds() {
  try { return localStorage.getItem("palaterm.exportNoBastionCreds") === "1"; } catch (_) { return false; }
}

// 一式書出: pick a group (or all) → export\bundles\<group>\ holding devices.csv
// plus the command sets and OS profiles those devices use (for "all devices":
// every set and profile, i.e. a complete copy of the configuration).
function exportBundleDialog(defaultGroup) {
  const groups = INV.deviceGroups || [];
  const opts = `<option value="__ALL__">${esc(t("全機器（{n}台）", { n: (INV.devices || []).length }))}</option>` +
    groups.map(g => `<option value="${esc(g.name)}" ${g.name === defaultGroup ? "selected" : ""}>${esc(t("{g}（{n}台）", { g: g.name, n: groupCount(g.name) }))}</option>`).join("");
  const node = h(`<div>
    <h3>${esc(t("一式書き出し"))}</h3>
    <div class="field"><label>${esc(t("書き出す対象"))}</label><select id="cx-grp">${opts}</select></div>
    <div class="muted" style="font-size:12px">${esc(t("機器CSV（devices.csv）と、その機器が使うコマンドセット・OSタイププロファイルを export\\bundles\\グループ名\\ にまとめて書き出します。「全機器」はコマンドセット・プロファイルも全件含む完全なバックアップです。"))}</div>
    <div class="field" style="margin-top:10px;margin-bottom:0"><label style="font-weight:normal"><input type="checkbox" id="cx-nobc" style="width:auto" ${exportNoBastionCreds() ? "checked" : ""}>
      <span data-tip="${esc(t("機器ごとに登録した踏み台のユーザー名・パスワード・秘密鍵ファイル・鍵のパスフレーズを空欄にして書き出します（踏み台のホストや通り方は残ります）。「自分の踏み台を使う」機器は、もともと踏み台の IP や認証情報が書き出されません。機器自身のユーザー名・パスワードは書き出されます"))}">${esc(t("踏み台の認証情報を含めない（人に渡す用） ⓘ"))}</span></label></div>
    <div style="font-size:12px;margin-top:6px;color:var(--warn)" id="cx-backup-warn" class="hidden">${esc(t("⚠ このチェックが入っていると、「全機器」でも機器ごとの踏み台の認証情報が入らず、完全なバックアップになりません"))}</div>
    <div class="muted" style="font-size:12px;margin-top:8px" id="cx-plain-note">${esc(t("※パスワードも平文で書き出されます。編集後は削除してください。"))}</div>
    <div class="modal-actions"><button class="btn" id="cx-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="cx-ok">${esc(t("書き出す"))}</button></div>
  </div>`);
  openModal(node, "mid");
  const syncWarn = () => node.querySelector("#cx-backup-warn").classList.toggle("hidden", !(node.querySelector("#cx-nobc").checked && node.querySelector("#cx-grp").value === "__ALL__"));
  node.querySelector("#cx-nobc").onchange = syncWarn;
  node.querySelector("#cx-grp").onchange = syncWarn;
  syncWarn();
  node.querySelector("#cx-cancel").onclick = closeModal;
  node.querySelector("#cx-ok").onclick = async () => {
    const v = node.querySelector("#cx-grp").value;
    const noBC = node.querySelector("#cx-nobc").checked;
    try { localStorage.setItem("palaterm.exportNoBastionCreds", noBC ? "1" : "0"); } catch (_) {}
    closeModal();
    try {
      const grp = v === "__ALL__" ? "" : v;
      // The folder is fixed per group: an earlier export there (devices.csv
      // perhaps open in Excel, half-edited) is asked about before it goes.
      const tgt = await App().BundleExportTarget(grp).catch(() => null);
      if (tgt && tgt.exists) {
        const when = tgt.exportedAt ? tgt.exportedAt.replace("T", " ").slice(0, 19) : "";
        const ok = await uiConfirm({
          title: t("前回の書き出しを上書き"),
          message: t("このフォルダには前回の書き出し（{d}）があります。devices.csv などを上書きしますか？<br><span class=\"muted\" style=\"font-size:12px\">{p}</span>", { d: esc(when || t("日時不明")), p: esc(tgt.dir) }),
          okLabel: t("上書きする"),
          danger: true,
        });
        if (!ok) return;
      }
      const dir = await App().ExportBundle(grp, noBC);
      if (!dir) return;
      if (await uiConfirm({ title: t("書き出しました"), message: `<span class="muted" style="font-size:12px">${esc(dir)}</span>`, okLabel: t("フォルダを開く"), cancelLabel: t("閉じる") })) {
        try { await App().OpenExportFolder(dir); } catch (e) { toast(terr(e), "err"); }
      }
    } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
  };
}

// 読込: a bundle's palaterm-bundle.json (devices + command sets + OS
// profiles) or a bare devices CSV. The file comes first; one screen then
// shows what it holds, the options that apply to that kind of file (a CSV
// has no sets or profiles to overwrite), and what applying it would
// change — previewed again as an option changes — before anything is written.
function importDialog(defaultGroup) {
  (async () => {
    let pv;
    try { pv = await App().PickImportFile(defaultGroup || "", false); }
    catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); return; }
    if (!pv) return;
    importPreviewDialog(pv, defaultGroup || "");
  })();
}

function importPreviewDialog(pv, defaultGroup) {
  const groups = INV.deviceGroups || [];
  const opts = groups.map(g => `<option value="${esc(g.name)}">${esc(g.name)}</option>`).join("");
  const isBundle = pv.kind === "bundle";
  const node = h(`<div>
    <h3>${esc(t("読み込み内容の確認"))}</h3>
    <div class="muted" style="font-size:12px;margin-bottom:8px">${esc(pv.path)}</div>
    <div class="muted" style="font-size:12px;margin-bottom:8px">${esc(isBundle ? t("一式（機器・コマンドセット・OSタイププロファイル）を読み込みます。") : t("機器CSV（機器だけ）を読み込みます。"))} ${esc(t("機器は「グループ名＋機器名」で照合し、既存は上書き・新規は追加です。"))} ${esc(t("既存の機器で認証情報（ユーザー名・パスワード等）の欄が空の行は、その機器の今の認証情報を残します。"))}</div>
    <div class="field"><label>${esc(t("読み込んだ機器の所属グループ"))}</label>
      <select id="ci-mode" autofocus>
        <option value="keep">${esc(t("ファイルのグループをそのまま使う"))}</option>
        <option value="assign" ${defaultGroup ? "selected" : ""}>${esc(t("指定グループに追加する"))}</option>
      </select></div>
    <div class="field" id="ci-grp-wrap"><label>${esc(t("追加先グループ"))} <span class="req">${esc(t("必須"))}</span></label>
      <input id="ci-grp" list="ci-grp-list" value="${esc(defaultGroup)}" placeholder="${esc(t("グループ名"))}">
      <datalist id="ci-grp-list">${opts}</datalist></div>
    ${isBundle ? `<div class="field"><label style="font-weight:normal"><input type="checkbox" id="ci-ow" style="width:auto"> ${esc(t("同名の既存コマンドセット・OSタイププロファイルも一式の内容で上書きする"))}</label></div>
    <div class="muted" style="font-size:12px">${esc(t("オフのときは、既にあるコマンドセット・プロファイルはそのまま残し、無いものだけ追加します（他のグループが使っている設定を壊さないため）。"))}</div>` : ""}
    <div id="ci-summary" style="font-size:14px;line-height:1.8;margin:10px 0"></div>
    <div class="modal-actions"><button class="btn" id="ci-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="ci-ok">${esc(t("読み込む"))}</button></div>
  </div>`);
  openModal(node, "mid");
  const modeSel = node.querySelector("#ci-mode"), grpInp = node.querySelector("#ci-grp");
  const grpWrap = node.querySelector("#ci-grp-wrap"), owBox = node.querySelector("#ci-ow");
  const target = () => modeSel.value === "assign" ? grpInp.value.trim() : "";
  const ow = () => !!(owBox && owBox.checked);
  let cur = pv, gen = 0;
  const render = () => { node.querySelector("#ci-summary").innerHTML = importSummaryLines(cur).map(esc).join("<br>"); };
  // An option changed: what the import would do changes with it (a target
  // group turns updates into new devices, overwriting changes the set
  // counts). A stale answer arriving after a newer request is dropped.
  const refresh = async () => {
    grpWrap.style.display = modeSel.value === "assign" ? "" : "none";
    if (modeSel.value === "assign" && !target()) {
      // Nothing to preview into yet: say so instead of leaving the counts
      // of the previous choice standing.
      node.querySelector("#ci-summary").innerHTML = `<span class="muted">${esc(t("追加先グループを入力すると、読み込み内容がここに出ます"))}</span>`;
      return;
    }
    const my = ++gen;
    try {
      const next = await App().PreviewImportFile(pv.path, target(), ow());
      if (my === gen && next) { cur = next; render(); }
    } catch (e) { if (my === gen) toast(t("読み込み失敗") + ": " + terr(e), "err"); }
  };
  modeSel.onchange = refresh;
  wireFilterInput(grpInp, refresh);
  if (owBox) owBox.onchange = refresh;
  // The file was previewed with the options the dialog opens on.
  grpWrap.style.display = modeSel.value === "assign" ? "" : "none";
  render();
  node.querySelector("#ci-cancel").onclick = closeModal;
  const okBtn = node.querySelector("#ci-ok");
  okBtn.onclick = async () => {
    if (modeSel.value === "assign" && !target()) { toast(t("グループ名を入力してください"), "err"); return; }
    // The dialog stays until the import went through: a failure used to
    // close it and leave the options to be chosen again.
    okBtn.disabled = true;
    try {
      const r = await App().ApplyImport(pv.path, target(), ow());
      PROFILES = await App().ListProfiles();
      closeModal();
      await refreshInventory();
      // Show where the devices went: the target group, or the one group the
      // file named (an import into another group was otherwise invisible).
      const g = target() || (cur && cur.summary && cur.summary.groupsCreated && cur.summary.groupsCreated.length === 1 ? cur.summary.groupsCreated[0] : "");
      if (g && (INV.deviceGroups || []).some(x => x.name === g) && document.querySelector("#tab-devices.active")) {
        deviceView = { mode: "list", group: g }; renderDevices();
      }
      toast(t("{n} 台を読み込みました", { n: r.devices }), "ok");
    } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
    finally { okBtn.disabled = false; }
  };
}

// What an import would do, one line per fact, warnings marked ⚠.
function importSummaryLines(pv) {
  const s = pv.summary;
  const lines = [t("機器: {n} 台（新規 {a}・上書き {b}）", { n: s.devices, a: s.devicesNew, b: s.devicesUpdated })];
  if (pv.kind === "bundle") {
    lines.push(t("コマンドセット: {n}（新規 {a}・上書き {b}・既存のまま {c}）", { n: s.sets, a: s.setsNew, b: s.setsUpdated, c: s.setsSkipped }));
    lines.push(t("OSタイププロファイル: {n}（新規 {a}・上書き {b}・既存のまま {c}）", { n: s.profiles, a: s.profilesNew, b: s.profilesUpdated, c: s.profilesSkipped }));
  }
  if ((s.credsKept || []).length) lines.push(t("認証情報が空の欄は、既存の機器の値を残します（{n} 台）: {g}", { n: s.credsKept.length, g: s.credsKept.slice(0, 10).join(", ") + (s.credsKept.length > 10 ? " …" : "") }));
  if (s.groupsCreated.length) lines.push(t("新規グループ: {g}", { g: s.groupsCreated.join(", ") }));
  if (s.missingSets.length) lines.push(t("⚠ 見つからないコマンドセット（機器は読み込まれますが割り当ては空扱い）: {g}", { g: s.missingSets.join(", ") }));
  if ((s.missingColumns || []).length && s.missingColumnsReset) lines.push(t("⚠ 古い形式のファイルです（{c} の列がありません）。既存の機器 {n} 台は、これらの項目が既定（CLI を使う・GUI を使わない・自分の踏み台を通らない・https）に戻ります", { c: s.missingColumns.map(k => ({ useMyBastion: t("自分の踏み台を使う"), cli: t("CLIを使う"), gui: t("GUIを使う"), guiScheme: t("GUIの方式"), guiPort: t("GUIポート") }[k] || k)).join("・"), n: s.missingColumnsReset }));
  if (s.missingProfiles.length) lines.push(t("⚠ 見つからないOSタイプ（機器は読み込まれますが汎用プロファイルで動きます）: {g}", { g: s.missingProfiles.join(", ") }));
  if ((s.duplicate || []).length) lines.push(t("⚠ 同じグループに同じ機器名の行が重複しているため、2 行目以降は読み込みません: {g}", { g: s.duplicate.join(", ") }));
  if ((s.jumpCommands || []).length) lines.push(t("⚠ 踏み台のジャンプコマンドが設定された行があります。この文字列は踏み台でそのまま実行されるので、内容を確認してください: {g}", { g: s.jumpCommands.join(" ／ ") }));
  if ((s.secretCommands || []).length) lines.push(t("⚠ パスワードを送るコマンド（{password} / {enable}）を含むコマンドセットがあります。どこへ送る行か確認してください（このセットの実行ログは常にパスワードを伏せて保存します）: {g}", { g: s.secretCommands.join(" ／ ") }));
  if ((s.invalid || []).length) lines.push(t("⚠ 内容に問題があるため読み込まない行（{n} 件）: {g}", { n: s.invalid.length, g: s.invalid.map(x => { const i = x.indexOf(": "); return i < 0 ? terr(x) : x.slice(0, i + 2) + terr(x.slice(i + 2)); }).join(" ／ ") }));
  return lines;
}

// editDevice opens the editor on dev; null adds a device (in preGroup), and
// template, when given, is what a new device starts from (a copy of another
// device: nothing is saved until 保存).
function editDevice(dev, preGroup, template) {
  const preG = preGroup ? (INV.deviceGroups || []).find(g => g.name === preGroup) : null;
  const preHasCreds = !!(preG && (preG.username || preG.password || preG.enablePassword));
  // A device added inside a group that has default credentials starts on
  // them; everything else starts on its own fields.
  const d = dev || template || { conn: "ssh", osType: "cisco-ios", authMethod: "password", enabled: true, group: preGroup || "", useGroupCreds: preHasCreds };
  // The COM ports are read again each time the editor opens: an adapter
  // plugged in after the unlock used to stay out of the list until the
  // next lock. The select is refilled when the answer comes.
  App().ListSerialPorts().then(list => {
    SERIAL = list || [];
    const sel = document.getElementById("f-serial");
    if (sel) { const cur = sel.value; sel.innerHTML = serialOptionsFor(cur); }
  }).catch(() => {});
  const serialOptionsFor = cur => `<option value="">${esc(t("自動検出"))}</option>` + SERIAL.map(p =>
    `<option value="${esc(p)}" ${cur === p ? "selected" : ""}>${esc(p)}</option>`).join("") + (cur && !SERIAL.includes(cur) ? `<option value="${esc(cur)}" selected>${esc(cur)}</option>` : "");
  // Working copy of the bastion chain (fold legacy single bastion in).
  let bastions = (d.bastions && d.bastions.length) ? d.bastions.map(x => ({ ...x }))
    : (d.bastion && d.bastion.host ? [{ ...d.bastion }] : []);
  const profOpts = PROFILES.map(p => `<option value="${esc(p.key)}" ${d.osType === p.key ? "selected" : ""}>${esc(p.name)}</option>`).join("");
  const setOpts = `<option value="">${esc(t("（なし）"))}</option>` + (INV.commandSets || []).map(s =>
    `<option value="${esc(s.name)}" ${d.commandSet === s.name ? "selected" : ""}>${esc(s.name)}</option>`).join("");
  const groupListId = "grouplist-dev";
  const groupOpts = (INV.deviceGroups || []).map(g => `<option value="${esc(g.name)}">`).join("");
  const node = h(`<div>
    <h3>${esc(dev ? t("機器を編集") : template ? t("機器を複製") : t("機器を追加"))}</h3>
    <p class="muted" style="font-size:12px;margin:-10px 0 12px"><span class="req">${esc(t("必須"))}</span> ${esc(t("の付いた項目以外は任意です（空欄のままで保存できます）"))}${template ? " " + esc(t("複製元の内容が入っています。保存するまで機器は作られません。")) : ""}</p>
    <div class="grid-2">
      <div class="field"><label>${esc(t("機器名（ログファイル名に使用）"))} <span class="req">${esc(t("必須"))}</span></label><input id="f-name" value="${esc(d.name || "")}" autofocus></div>
      <div class="field"><label>${esc(t("グループ"))}</label>
        <div class="row-inline" id="f-group-ro" style="gap:12px;min-height:38px;align-items:center"><b>${esc(d.group || t("（未設定）"))}</b>
          <span class="link" id="f-group-change" style="font-size:12px">${esc(d.group ? t("別のグループへ移す…") : t("グループを選ぶ…"))}</span></div>
        <input id="f-group" list="${groupListId}" value="${esc(d.group || "")}" placeholder="${esc(t("例: A社（空欄=未設定）"))}" style="display:none">
        <datalist id="${groupListId}">${groupOpts}</datalist></div>
    </div>
    <div class="grid-2">
      <div class="field"><label><span data-tip="${esc(t("IP アドレスまたは DNS で解決できるホスト名（例: 192.0.2.1 / sw01.example.net）"))}">${esc(t("IPアドレス / ホスト名 ⓘ"))}</span> <span class="req" id="f-host-req">${esc(t("必須"))}</span></label><input id="f-host" value="${esc(d.host || "")}"></div>
      <div class="field"><label>${esc(t("拠点（任意）"))}</label><input id="f-site" value="${esc(d.site || "")}" placeholder="${esc(t("例: 本社 / 東京DC"))}"></div>
    </div>
    <div class="grid-2">
      <div class="field"><label><span data-tip="${esc(t("機器の役割を表す自由なラベルです。拠点と同じく、設定タブのテンプレートで {role} として使えます"))}">${esc(t("役割（任意） ⓘ"))}</span></label><input id="f-role" value="${esc(d.role || "")}" placeholder="${esc(t("例: コア / エッジ / FW"))}"></div>
      <div class="field"><label><span data-tip="${esc(t("この機器の単独接続窓の背景とタイトル帯をこの色にします。本番機は赤・検証機は緑のようにしておくと、並べた窓の中で接続先を取り違えにくくなります"))}">${esc(t("端末の色（任意） ⓘ"))}</span></label>
        <select id="f-tint"><option value="">${esc(t("なし（窓の色のまま）"))}</option>${deviceTintKeys().map(k => `<option value="${k}" ${d.termColor === k ? "selected" : ""}>■ ${esc(t(DEVICE_TINTS[k].name))}</option>`).join("")}</select></div>
    </div>
    <div class="row-inline" style="gap:28px;margin:2px 0 10px">
      <label style="display:flex;align-items:center;gap:8px;cursor:pointer"><input type="checkbox" id="f-cli" ${d.cliOff ? "" : "checked"} style="width:auto">
        <span data-tip="${esc(t("SSH / Telnet / シリアルでログインする機器です（一括実行・接続確認・予約・「接続」）。GUI だけの機器はオフにします"))}">${esc(t("CLI を使う ⓘ"))}</span></label>
      <label style="display:flex;align-items:center;gap:8px;cursor:pointer"><input type="checkbox" id="f-gui" ${d.guiOn ? "checked" : ""} style="width:auto">
        <span data-tip="${esc(t("オンにすると、機器一覧の「GUI」で管理画面を PalaTerm の窓で開き、この機器のユーザー名・パスワードで自動ログインします（踏み台は通らず直接開きます）"))}">${esc(t("GUI を使う ⓘ"))}</span></label>
    </div>
    <div class="grid-2" id="fw-gui" style="max-width:420px">
      <div class="field"><label>${esc(t("GUIの方式"))}</label><select id="f-guischeme">
        <option value="https" ${d.guiScheme === "http" ? "" : "selected"}>https</option>
        <option value="http" ${d.guiScheme === "http" ? "selected" : ""}>http</option></select></div>
      <div class="field"><label><span data-tip="${esc(t("https は 443、http は 80 が既定です（方式を切り替えると既定のポートに合わせます）。管理画面のポートを変えている機器は、その番号を入れてください"))}">${esc(t("GUIポート ⓘ"))}</span></label>
        <input id="f-guiport" type="number" min="1" max="65535" value="${esc(String(d.guiPort || (d.guiScheme === "http" ? 80 : 443)))}"></div>
    </div>
    <div class="muted" id="f-http-warn" style="font-size:12px;margin:-4px 0 8px;color:var(--warn)">${esc(t("⚠ http では、自動ログインのユーザー名・パスワードが暗号化されずに送られます"))}</div>
    <div id="fw-cli1">
    <div class="grid-3">
      <div class="field"><label>${esc(t("接続方式"))} <span class="req">${esc(t("必須"))}</span></label><select id="f-conn">
        <option value="ssh" ${d.conn === "ssh" ? "selected" : ""}>SSH</option>
        <option value="telnet" ${d.conn === "telnet" ? "selected" : ""}>Telnet</option>
        <option value="serial" ${d.conn === "serial" ? "selected" : ""}>${esc(t("シリアル"))}</option></select></div>
      <div class="field"><label>${esc(t("OSタイプ"))} <span class="req">${esc(t("必須"))}</span></label><select id="f-os">${profOpts}</select></div>
      <div class="field"><label>${esc(t("コマンドセット"))}</label><select id="f-set">${setOpts}</select></div>
    </div>
    <div id="conn-extra"></div>
    </div>

    <div class="section-label">${esc(t("認証情報（暗号化保存）"))}</div>
    <div class="field" id="fw-credmode"><label><span data-tip="${esc(t("「グループ既定を使う」にすると、ユーザー名・パスワード・enable パスワードはグループの既定（機器一覧の「グループ既定の認証情報…」）が使われ、この機器の欄は使われません。同じグループでもパスワードが違う機器は「個別に設定」のままにします"))}">${esc(t("認証情報の扱い ⓘ"))}</span></label>
      <select id="f-credmode">
        <option value="own" ${d.useGroupCreds ? "" : "selected"}>${esc(t("この機器に個別に設定"))}</option>
        <option value="group" ${d.useGroupCreds ? "selected" : ""}>${esc(t("グループ既定を使う"))}</option>
      </select>
      <div class="muted" id="f-credmode-note" style="font-size:12px;margin-top:4px"></div></div>
    <div class="grid-2">
      <div class="field" id="fw-user"><label>${esc(t("ユーザー名"))}</label><input id="f-user" value="${esc(d.username || "")}"></div>
      <div class="field" id="fw-authmethod"><label>${esc(t("SSH認証方式"))}</label><select id="f-auth">
        <option value="password" ${d.authMethod === "password" ? "selected" : ""}>${esc(t("パスワード"))}</option>
        <option value="publickey" ${d.authMethod === "publickey" ? "selected" : ""}>${esc(t("公開鍵 (Ed25519/RSA/ECDSA/DSA)"))}</option></select></div>
    </div>
    <div class="grid-2" id="fw-pwrow">
      <div class="field"><label>${esc(t("パスワード"))}</label><input id="f-pw" type="password" value="${esc(d.password || "")}"></div>
      <div class="field" id="fw-enable"><label>${esc(t("enable / 昇格パスワード（任意・ない機種は空欄）"))}</label><input id="f-en" type="password" value="${esc(d.enablePassword || "")}"></div>
    </div>
    <div class="field" id="fw-keyfile"><label>${esc(t("秘密鍵ファイル"))}</label>
      <div class="row-inline" style="gap:6px">
        <input id="f-key" style="flex:1" value="${esc(d.keyFile || "")}" placeholder="C:\\Users\\...\\id_ed25519">
        <button class="btn sm" id="f-key-pick" type="button">${esc(t("参照…"))}</button>
      </div></div>
    <div class="field" id="fw-keypass"><label><span data-tip="${esc(t("踏み台上でコマンドを打つ方式で、踏み台の上の鍵のパスフレーズを聞かれる場合（Enter passphrase for key …）は、そのパスフレーズをここに入れてください。聞かれたときに自動で送ります（この方式では秘密鍵ファイル欄は使いません。SSH トンネル方式ではこの PC の秘密鍵ファイルを使います）"))}">${esc(t("秘密鍵のパスフレーズ（保護されていない鍵は空欄・暗号化保存） ⓘ"))}</span></label>
      <input id="f-keypass" type="password" value="${esc(d.keyPassphrase || "")}"></div>
    <div class="field" id="fw-legacy">
      <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
        <input type="checkbox" id="f-legacy" ${d.legacyAlgos ? "checked" : ""} style="width:auto">
        <span data-tip="${esc(t("SSHで古い暗号方式（SHA-1系の鍵交換・CBC・3DES）も候補に含めます。通常はオフのまま（強い方式のみ）。新しい方式に対応していない古い機器で接続エラーになる場合だけ有効にしてください"))}">${esc(t("レガシー暗号を許可（古い機器向け・通常はオフ） ⓘ"))}</span>
      </label></div>

    <div id="fw-cli2">
    <div class="section-label">${esc(t("踏み台サーバ（任意・最大5段。外側＝手前から順に）"))}</div>
    <div class="field" style="margin-bottom:6px">
      <label style="display:flex;align-items:center;gap:8px;cursor:pointer"><input type="checkbox" id="f-mybastion" ${d.useMyBastion ? "checked" : ""} style="width:auto">
        <span data-tip="${esc(t("設定タブで登録した「自分の踏み台」（IP・ユーザー名・秘密鍵など。最大5段）を先に通ってから、下の踏み台・機器へ進みます。機器には「使う」ことだけが保存され、書き出しても踏み台の IP や認証情報は入りません。受け取った人は自分の踏み台を登録すればそのまま使えます"))}">${esc(t("自分の踏み台（設定タブ）を先に通る ⓘ"))}</span></label>
      <div class="muted" id="f-mybastion-note" style="font-size:12px;margin-top:4px"></div></div>
    <div class="field" id="fw-bmode" style="margin-bottom:4px"><label><span data-tip="${esc(t("踏み台上でコマンドを打つ: 手前の踏み台の画面で ssh / telnet を打って次へ進みます。ルーターなどシェルのある踏み台ならどれでも使えますが、2段目以降と機器へのログインは踏み台側の ssh が行います。SSH トンネル: 踏み台の SSH の中を通して、この PC から各段と機器へ直接 SSH ログインします（OpenSSH の ProxyJump と同じ）。全段で秘密鍵・レガシー暗号・ホストキー確認が効きます。踏み台はすべて SSH で、TCP 転送を許可している必要があります（Linux の OpenSSH は標準で許可）"))}">${esc(t("踏み台の通り方 ⓘ"))}</span></label>
      <select id="f-bmode">
        <option value="" ${d.bastionMode === "tunnel" ? "" : "selected"}>${esc(t("踏み台上でコマンドを打つ（標準・ルーターの踏み台も可。表・CSV では「コマンド入力」）"))}</option>
        <option value="tunnel" ${d.bastionMode === "tunnel" ? "selected" : ""}>${esc(t("SSH トンネル（この PC から全段にログイン・鍵が使える）"))}</option>
      </select></div>
    <div id="bastions-host"></div>
    <button class="btn sm" id="add-bastion" type="button" style="margin-top:6px">${esc(t("＋ 踏み台を追加"))}</button>
    </div>

    <div class="modal-actions">
      <button class="btn" id="cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="save" data-tip="Ctrl+Enter">${esc(t("保存"))}</button>
    </div>
  </div>`);

  openModal(node);
  wirePasswordToggles(node);

  const connExtra = node.querySelector("#conn-extra");
  function syncConn() {
    const c = node.querySelector("#f-conn").value;
    if (c === "serial") {
      const cur = node.querySelector("#f-serial") ? node.querySelector("#f-serial").value : (d.serialPort || "");
      connExtra.innerHTML = `<div class="grid-2">
        <div class="field"><label>${esc(t("COMポート"))}</label><div class="row-inline" style="gap:6px"><select id="f-serial" style="flex:1">${serialOptionsFor(cur)}</select>
          <button class="btn sm" id="f-serial-refresh" type="button" data-tip="${esc(t("COM ポートの一覧を読み直します（今挿した USB シリアルを出す）"))}">↻</button></div></div>
        <div class="field"><label>${esc(t("ボーレート"))}</label><input id="f-baud" type="number" value="${d.baud || 9600}"></div></div>`;
      node.querySelector("#f-serial-refresh").onclick = async () => {
        try {
          SERIAL = await App().ListSerialPorts() || [];
          const sel = node.querySelector("#f-serial"); const v = sel.value;
          sel.innerHTML = serialOptionsFor(v);
          toast(SERIAL.length ? t("COM ポート: {n} 件", { n: SERIAL.length }) : t("COM ポートが見つかりません（USB シリアルの接続とドライバーを確認）"), SERIAL.length ? "ok" : "warn");
        } catch (e) { toast(terr(e), "err"); }
      };
    } else {
      // Auto-fill the standard port for the chosen method; a custom port
      // (anything other than 22/23) typed by the user survives the switch.
      const prevEl = node.querySelector("#f-port");
      const prev = prevEl ? (parseInt(prevEl.value, 10) || 0) : (d.port || 0);
      const port = (!prev || prev === 22 || prev === 23) ? (c === "ssh" ? 22 : 23) : prev;
      connExtra.innerHTML = `<div class="field" style="max-width:200px"><label>${esc(t("ポート"))}</label>
        <input id="f-port" type="number" value="${port}"></div>`;
    }
    node.querySelector("#fw-authmethod").style.display = c === "ssh" ? "" : "none";
    node.querySelector("#fw-legacy").style.display = c === "ssh" ? "" : "none";
    syncAuth();
  }
  function syncAuth() {
    const isSSH = node.querySelector("#f-conn").value === "ssh";
    const key = isSSH && node.querySelector("#f-auth").value === "publickey";
    node.querySelector("#fw-keyfile").style.display = key ? "" : "none";
    node.querySelector("#fw-keypass").style.display = key ? "" : "none";
    // Pre-fill the last used private-key path when public key is selected.
    const keyInp = node.querySelector("#f-key");
    if (key && !keyInp.value && (INV.settings || {}).lastKeyFile) {
      keyInp.value = INV.settings.lastKeyFile;
    }
    syncUse();
  }
  // CLI / GUI: what the device is used through. With the CLI off it is a
  // GUI-only device: the connection, OS type, command set, key and jump-host
  // fields go away (the username and password stay — the GUI logs in with
  // them); the GUI's scheme and port show only with the GUI on.
  function syncUse() {
    const cli = node.querySelector("#f-cli").checked, gui = node.querySelector("#f-gui").checked;
    node.querySelector("#fw-cli1").style.display = cli ? "" : "none";
    node.querySelector("#fw-cli2").style.display = cli ? "" : "none";
    node.querySelector("#fw-gui").style.display = gui ? "" : "none";
    node.querySelector("#f-http-warn").style.display = gui && node.querySelector("#f-guischeme").value === "http" ? "" : "none";
    node.querySelector("#fw-enable").style.display = cli ? "" : "none";
    if (!cli) ["#fw-authmethod", "#fw-keyfile", "#fw-keypass", "#fw-legacy"].forEach(s => { node.querySelector(s).style.display = "none"; });
    // A serial device has no address: the IP field is required only for a
    // network CLI or a GUI.
    const c = node.querySelector("#f-conn").value;
    node.querySelector("#f-host-req").style.display = gui || (cli && c !== "serial") ? "" : "none";
  }
  // The GUI port follows the scheme while it is a scheme's default.
  node.querySelector("#f-guischeme").onchange = () => {
    const inp = node.querySelector("#f-guiport"), p = parseInt(inp.value, 10) || 0;
    if (!p || p === 80 || p === 443) inp.value = node.querySelector("#f-guischeme").value === "http" ? 80 : 443;
    syncUse();
  };
  node.querySelector("#f-cli").onchange = () => { syncConn(); };
  node.querySelector("#f-gui").onchange = syncUse;
  // Credential mode: the group choice only exists while a group is named,
  // and the device's own fields hide while the group's defaults are in use.
  const credMode = node.querySelector("#f-credmode");
  function syncCredMode() {
    const gname = node.querySelector("#f-group").value.trim();
    const g = gname ? (INV.deviceGroups || []).find(x => x.name === gname) : null;
    const wrap = node.querySelector("#fw-credmode");
    if (!gname) { credMode.value = "own"; wrap.style.display = "none"; }
    else wrap.style.display = "";
    const useGroup = credMode.value === "group";
    node.querySelector("#fw-user").style.display = useGroup ? "none" : "";
    node.querySelector("#fw-pwrow").style.display = useGroup ? "none" : "";
    const note = node.querySelector("#f-credmode-note");
    if (!useGroup) note.textContent = "";
    else if (g && (g.username || g.password || g.enablePassword)) note.textContent = t("グループ「{g}」の既定（ユーザー名: {u}）でログインします", { g: gname, u: g.username || "—" });
    else note.textContent = t("⚠ グループ「{g}」の既定はまだ未設定です。機器一覧の「グループ既定の認証情報…」で設定してください", { g: gname });
  }
  credMode.onchange = syncCredMode;
  node.querySelector("#f-group").addEventListener("input", syncCredMode);
  // The group is shown as text (it was chosen by opening the group); the
  // input only appears for the rare move to another group.
  node.querySelector("#f-group-change").onclick = () => {
    node.querySelector("#f-group-ro").style.display = "none";
    const inp = node.querySelector("#f-group");
    inp.style.display = ""; inp.focus();
  };
  node.querySelector("#f-conn").onchange = syncConn;
  node.querySelector("#f-auth").onchange = syncAuth;
  node.querySelector("#f-key-pick").onclick = async () => {
    try {
      const p = await App().PickKeyFile();
      if (p) node.querySelector("#f-key").value = p;
    } catch (e) { toast(t("選択失敗") + ": " + terr(e), "err"); }
  };
  syncConn();
  syncCredMode();

  // ---- bastion chain (up to 5 hops) ----
  const bhost = node.querySelector("#bastions-host");
  const addBtn = node.querySelector("#add-bastion");
  function collectBastions() {
    const rows = bhost.querySelectorAll("[data-bastion-row]");
    const arr = [];
    rows.forEach(r => arr.push({
      host: r.querySelector(".b-host").value.trim(),
      method: r.querySelector(".b-method").value,
      port: parseInt(r.querySelector(".b-port").value, 10) || 0,
      username: r.querySelector(".b-user").value,
      password: r.querySelector(".b-pw").value,
      authMethod: r.querySelector(".b-auth").value,
      keyFile: r.querySelector(".b-key").value,
      keyPassphrase: r.querySelector(".b-keypass").value,
      legacyAlgos: r.querySelector(".b-legacy").checked,
      jumpCommand: r.querySelector(".b-jump").value.trim(),
    }));
    return arr;
  }
  // Hops after the first are reached by an ssh/telnet command typed on the
  // previous hop's shell (runner.traverseBastions), so PalaTerm's own key file
  // and legacy-cipher switch only ever apply to hop 1. The fields stay in the
  // DOM (collectBastions reads them) but are hidden with a note for hop 2+.
  const NOTE_HOP = "2段目以降の認証は手前の踏み台の ssh / telnet コマンドが行うため、ここで使われるのはユーザー名とパスワードだけです。鍵認証やレガシー暗号は、手前の踏み台側の ssh 設定（~/.ssh の鍵など）で用意してください。";
  // Through an SSH tunnel every hop is a login made from this PC, so every
  // hop's key and legacy fields apply and no jump command is typed.
  const bmode = node.querySelector("#f-bmode");
  bmode.onchange = () => { bastions = collectBastions(); renderBastions(); };
  const mineBox = node.querySelector("#f-mybastion");
  mineBox.onchange = () => { bastions = collectBastions(); renderBastions(); };
  function renderBastions() {
    const tun = bmode.value === "tunnel";
    const mbs = (INV.myBastions || []).filter(b => b.host);
    const mine = mineBox.checked;
    node.querySelector("#fw-bmode").style.display = bastions.length || mine ? "" : "none";
    node.querySelector("#f-mybastion-note").innerHTML = !mine ? ""
      : mbs.length ? esc(t("自分の踏み台: {c}。その先に踏み台があれば下に足します", { c: mbs.map((b, i) => t("{n}段目 {h}", { n: i + 1, h: b.host + ":" + (b.port || ((b.method || "ssh") === "ssh" ? 22 : 23)) }) + (b.username ? "（" + b.username + "）" : "")).join(" → ") }))
      : `<span style="color:var(--warn)">${esc(t("⚠ 自分の踏み台がまだ登録されていません。設定タブの「自分の踏み台」で登録してください"))}</span>`;
    const hide = i => (i > 0 || mine) && !tun;
    bhost.innerHTML = (tun && bastions.length ? `<div class="muted" style="font-size:12px;margin:4px 0">${esc(t("SSH トンネルでは踏み台はすべて SSH にしてください。踏み台の sshd が TCP 転送を許可していないと接続できません"))}</div>` : "") + bastions.map((b, i) => `
      <div class="card" data-bastion-row style="padding:14px 16px;margin:10px 0">
        <div class="row-inline" style="justify-content:space-between;margin-bottom:8px">
          <b>${esc(mine ? t("{n}段目の踏み台（自分の踏み台の後）", { n: i + 1 }) : t("{n}段目の踏み台", { n: i + 1 }))}</b>
          <button class="btn sm act-del" type="button" data-rm="${i}">${esc(t("削除"))}</button>
        </div>
        <div class="grid-3">
          <div class="field" style="margin-bottom:8px"><label>${esc(t("ホスト"))} <span class="req">${esc(t("必須"))}</span></label><input class="b-host" value="${esc(b.host || "")}"></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("接続方式"))} <span class="req">${esc(t("必須"))}</span></label><select class="b-method">
            <option value="ssh" ${b.method === "ssh" ? "selected" : ""}>SSH</option>
            <option value="telnet" ${b.method === "telnet" ? "selected" : ""}>Telnet</option></select></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("ポート"))}</label>
            <input class="b-port" type="number" value="${esc(String(b.port || (b.method === "telnet" ? 23 : 22)))}"></div>
        </div>
        <div class="field" style="margin-bottom:8px${b.method === "telnet" || hide(i) ? ";display:none" : ""}" data-b-legacy>
          <label><input class="b-legacy" type="checkbox" ${b.legacyAlgos ? "checked" : ""}>
            <span data-tip="${esc(t("この踏み台とのSSHで古い暗号方式も候補に含めます。機器側の同名設定とは独立しています（踏み台と機器は別のマシンなので、片方が古いことをもう片方の暗号強度を下げる理由にはしません）"))}">${esc(t("この踏み台にレガシー暗号を許可 ⓘ"))}</span></label></div>
        <div class="grid-3">
          <div class="field" style="margin-bottom:8px"><label>${esc(t("ユーザー"))} <span class="req b-user-req" ${b.method === "telnet" ? 'style="display:none"' : ""}>${esc(t("必須"))}</span></label><input class="b-user" value="${esc(b.username || "")}"></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("パスワード"))}</label><input class="b-pw" type="password" value="${esc(b.password || "")}"></div>
          <div class="field" style="margin-bottom:8px${hide(i) ? ";display:none" : ""}"><label>${esc(t("SSH認証"))}</label><select class="b-auth">
            <option value="password" ${b.authMethod === "password" ? "selected" : ""}>${esc(t("パスワード"))}</option>
            <option value="publickey" ${b.authMethod === "publickey" ? "selected" : ""}>${esc(t("公開鍵"))}</option></select></div>
        </div>
        ${hide(i) ? `<div class="muted" style="font-size:12px;margin-bottom:8px">${esc(t(NOTE_HOP))}</div>` : ""}
        <div class="grid-2" ${hide(i) ? 'style="display:none"' : ""}>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("秘密鍵ファイル（公開鍵認証時）"))}</label>
            <div class="row-inline" style="gap:6px">
              <input class="b-key" style="flex:1" value="${esc(b.keyFile || "")}">
              <button class="btn sm b-key-pick" type="button">${esc(t("参照…"))}</button>
            </div></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("鍵のパスフレーズ（任意）"))}</label>
            <input class="b-keypass" type="password" value="${esc(b.keyPassphrase || "")}"></div>
        </div>
        <div class="field" style="margin-bottom:0${tun ? ";display:none" : ""}"><label>${esc(t("次ホップへのジャンプコマンド（空欄=標準。NW機器踏み台などコマンド形式が違う場合に指定）"))}</label>
          <input class="b-jump mono" value="${esc(b.jumpCommand || "")}" placeholder="ssh -l {user} {host}">
          <div class="muted" style="font-size:12px;margin-top:6px">${esc(t("例と使える変数（クリックでコピー）:"))}</div>
          <table class="ph-table"><tbody>
            <tr><td class="mono">ssh -l {user} {host}</td><td>${esc(t("例: Cisco IOS など、-l でユーザーを指定する ssh"))}</td></tr>
            <tr><td class="mono">telnet {host} {port}</td><td>${esc(t("例: telnet で次ホップへ"))}</td></tr>
            <tr><td class="mono">{user}</td><td>${esc(t("次ホップのユーザー名"))}</td><td class="mono">{host}</td><td>${esc(t("次ホップのホスト"))}</td><td class="mono">{port}</td><td>${esc(t("次ホップのポート"))}</td></tr>
          </tbody></table></div>
      </div>`).join("");
    bhost.querySelectorAll("[data-rm]").forEach(btn => btn.onclick = () => {
      bastions = collectBastions();
      bastions.splice(parseInt(btn.dataset.rm, 10), 1);
      renderBastions();
    });
    bhost.querySelectorAll(".b-key-pick").forEach(btn => btn.onclick = async () => {
      try {
        const p = await App().PickKeyFile();
        if (p) btn.closest("[data-bastion-row]").querySelector(".b-key").value = p;
      } catch (e) { toast(t("選択失敗") + ": " + terr(e), "err"); }
    });
    // The port follows the method, as it does for the device itself: a hop left
    // on 22 after a switch to Telnet reads the SSH banner and hangs. A port the
    // user picked (anything but the two standard ones) survives. The legacy
    // cipher option is SSH-only, so it hides for a Telnet hop.
    bhost.querySelectorAll(".b-method").forEach(sel => sel.onchange = () => {
      const row = sel.closest("[data-bastion-row]");
      const portInp = row.querySelector(".b-port");
      const p = parseInt(portInp.value, 10) || 0;
      if (!p || p === 22 || p === 23) portInp.value = sel.value === "ssh" ? 22 : 23;
      const first = row === bhost.querySelector("[data-bastion-row]") && !mineBox.checked;
      row.querySelector("[data-b-legacy]").style.display = sel.value === "telnet" || (!first && bmode.value !== "tunnel") ? "none" : "";
      // A Telnet hop may ask for no username at all; an SSH login needs one.
      row.querySelector(".b-user-req").style.display = sel.value === "telnet" ? "none" : "";
    });
    // Pre-fill the last used key path when a bastion switches to public key.
    bhost.querySelectorAll(".b-auth").forEach(sel => sel.onchange = () => {
      const inp = sel.closest("[data-bastion-row]").querySelector(".b-key");
      if (sel.value === "publickey" && !inp.value && (INV.settings || {}).lastKeyFile) {
        inp.value = INV.settings.lastKeyFile;
      }
    });
    wirePasswordToggles(bhost);
    wireCopyCells(bhost);
    addBtn.style.display = bastions.length >= 5 ? "none" : "";
  }
  addBtn.onclick = () => {
    bastions = collectBastions();
    if (bastions.length >= 5) return;
    bastions.push({ method: "ssh", authMethod: "password" });
    renderBastions();
  };
  renderBastions();

  guardDiscard(node, "#cancel");
  node.querySelector("#save").onclick = async () => {
    const conn = node.querySelector("#f-conn").value;
    const out = {
      id: dev ? dev.id : "",
      name: node.querySelector("#f-name").value.trim(),
      group: node.querySelector("#f-group").value.trim(),
      site: node.querySelector("#f-site").value.trim(),
      role: node.querySelector("#f-role").value.trim(),
      host: node.querySelector("#f-host").value.trim(),
      conn,
      osType: node.querySelector("#f-os").value,
      commandSet: node.querySelector("#f-set").value,
      username: node.querySelector("#f-user").value,
      password: node.querySelector("#f-pw").value,
      enablePassword: node.querySelector("#f-en").value,
      authMethod: node.querySelector("#f-auth").value,
      keyFile: node.querySelector("#f-key").value,
      keyPassphrase: node.querySelector("#f-keypass").value,
      legacyAlgos: node.querySelector("#f-legacy").checked,
      useGroupCreds: !!node.querySelector("#f-group").value.trim() && node.querySelector("#f-credmode").value === "group",
      enabled: d.enabled !== false,
      cliOff: !node.querySelector("#f-cli").checked,
      guiOn: node.querySelector("#f-gui").checked,
      guiScheme: node.querySelector("#f-guischeme").value === "http" ? "http" : "",
      useMyBastion: mineBox.checked,
      termColor: node.querySelector("#f-tint").value,
    };
    // The scheme's own port is stored as 0 (= follow the scheme).
    const gp = parseInt(node.querySelector("#f-guiport").value, 10) || 0;
    out.guiPort = gp === (out.guiScheme === "http" ? 80 : 443) ? 0 : gp;
    if (out.cliOff && !out.guiOn) { toast(t("CLI と GUI のどちらかはオンにしてください"), "err"); return; }
    if (conn === "serial") {
      out.serialPort = node.querySelector("#f-serial").value;
      out.baud = parseInt(node.querySelector("#f-baud").value, 10) || 9600;
    } else {
      out.port = parseInt(node.querySelector("#f-port").value, 10) || 0;
    }
    const chain = collectBastions().filter(b => b.host && b.method);
    if (chain.length) out.bastions = chain.slice(0, 5);
    if (chain.length || out.useMyBastion) out.bastionMode = bmode.value;
    if (!out.name) { toast(t("機器名は必須です"), "err"); return; }
    if ((out.guiOn || (!out.cliOff && conn !== "serial")) && !out.host) { toast(t("IPアドレスは必須です"), "err"); return; }
    // The 必須 on a hop's user was only a mark: an SSH hop without one fails
    // at the login, so it is checked here (a Telnet hop may ask for none).
    const noUser = chain.findIndex(b => b.method === "ssh" && !b.username.trim());
    if (!out.cliOff && noUser >= 0) { toast(t("{n}段目の踏み台（SSH）のユーザー名は必須です", { n: noUser + 1 }), "err"); return; }
    // A name only has to be unique within its group (it names the log
    // files; a run is one group). The backend checks again on save.
    const nameChanged = !dev || out.name !== dev.name || out.group !== (dev.group || "");
    if (nameChanged && await App().DeviceNameExists(out.name, out.group, out.id)) {
      toast(out.group ? t("グループ「{g}」に「{n}」は既に登録されています。別の機器名にしてください", { g: out.group, n: out.name })
        : t("グループ未設定の機器に「{n}」は既に登録されています。別の機器名にしてください", { n: out.name }), "err");
      return;
    }
    // A group typed into 別のグループへ移す that does not exist yet is
    // created by the save: asked first, so a typo ("A杜") does not quietly
    // become a new group.
    if (out.group && !(INV.deviceGroups || []).some(g => g.name === out.group)) {
      const mk = await subConfirm(esc(t("グループ「{g}」はまだありません。新しいグループとして作成しますか？", { g: out.group })), t("作成して保存"), t("編集に戻る"), "primary");
      if (!mk) return;
    }
    try { await App().SaveDevice(out); closeModal(); await refreshInventory(); toast(t("保存しました"), "ok"); }
    catch (e) { toast(terr(e), "err"); }
  };
}
