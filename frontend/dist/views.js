// Tab renderers and editors for PalaTerm. Loaded after app.js.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

// ---- Devices tab ----
function connOptions(sel) {
  return ["ssh", "telnet", "serial"].map(c =>
    `<option value="${c}" ${sel === c ? "selected" : ""}>${c}</option>`).join("");
}
function osOptions(sel) {
  return PROFILES.map(p => `<option value="${esc(p.key)}" ${sel === p.key ? "selected" : ""}>${esc(p.name)}</option>`).join("");
}
function cmdSetOptions(sel) {
  return `<option value="">${esc(t("(なし)"))}</option>` + (INV.commandSets || []).map(s =>
    `<option value="${esc(s.name)}" ${sel === s.name ? "selected" : ""}>${esc(s.name)}</option>`).join("");
}

// Device tab has two steps: pick a scope (all devices or a group), then the
// device list. deviceView tracks which step is showing.
let deviceView = { mode: "select", group: null };

function renderDevices() {
  if (deviceView.mode === "select") return renderGroupChooser();
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
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("機器グループを選択"))}</div>
        <div class="page-sub">${esc(t("グループを選ぶか、新規に作成してください。グループ作成後にそのグループへ機器を追加します"))}</div></div>
      <div class="row-inline">
        <button class="btn" id="imp-csv">${esc(t("CSV読込"))}</button>
        <button class="btn" id="exp-csv">${esc(t("CSV書出"))}</button>
      </div>
    </div>
    <div class="chooser">
      <button class="choice new" id="choice-new">
        <div class="choice-t">${esc(t("＋ 新規グループ作成"))}</div>
        <div class="choice-n">${esc(t("会社・拠点などの単位で作成"))}</div>
      </button>
      ${groups.map(g => `<button class="choice" draggable="true" data-open="${esc(g.name)}" data-grp="${esc(g.name)}">
        <div class="choice-t">${esc(g.name)}</div>
        <div class="choice-n">${esc(t("{n} 台", { n: groupCount(g.name) }))} <span class="muted" style="font-size:11px">${esc(t("⠿ ドラッグで並替"))}</span></div>
      </button>`).join("")}
    </div>`;

  document.getElementById("exp-csv").onclick = () => exportCsvDialog("");
  document.getElementById("imp-csv").onclick = () => importCsvDialog("");
  document.getElementById("choice-new").onclick = newGroupPrompt;
  root.querySelectorAll("[data-open]").forEach(b => b.onclick = () => {
    deviceView = { mode: "list", group: b.dataset.open === "__ALL__" ? null : b.dataset.open };
    renderDevices();
  });
  wireGroupDrag(root);
}

// Drag-and-drop reordering of group cards; persists the new order.
function wireGroupDrag(root) {
  let dragEl = null;
  const cards = root.querySelectorAll("[data-grp]");
  cards.forEach(card => {
    card.addEventListener("dragstart", e => {
      dragEl = card; card.classList.add("dragging");
      // Some WebViews need data set for the drag to start at all.
      if (e.dataTransfer) { e.dataTransfer.setData("text/plain", card.dataset.grp); e.dataTransfer.effectAllowed = "move"; }
    });
    card.addEventListener("dragend", async () => {
      card.classList.remove("dragging");
      root.querySelectorAll(".dragover").forEach(c => c.classList.remove("dragover"));
      const order = [...root.querySelectorAll("[data-grp]")].map(c => c.dataset.grp);
      try { await App().ReorderDeviceGroups(order); await refreshInventory(); } catch (e) {}
    });
    card.addEventListener("dragover", e => {
      e.preventDefault();
      if (!dragEl || dragEl === card) return;
      const box = card.getBoundingClientRect();
      const before = (e.clientY - box.top) < box.height / 2;
      card.parentNode.insertBefore(dragEl, before ? card : card.nextSibling);
    });
  });
}

// Create an empty group (name only), then jump into its (empty) device list.
function newGroupPrompt() {
  const node = h(`<div>
    <h3>${esc(t("新規グループ作成"))}</h3>
    <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("会社名・拠点名など。作成後にこのグループへ機器を追加できます。"))}</p>
    <div class="field"><label>${esc(t("グループ名"))}</label><input id="ng-name" placeholder="${esc(t("例: A社"))}" autofocus></div>
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

function renderDeviceList() {
  const root = document.getElementById("tab-devices");
  let devs = INV.devices || [];
  // Scope to the chosen group (by the device's Group field), if any.
  const scopeGroup = deviceView.group
    ? (INV.deviceGroups || []).find(g => g.name === deviceView.group) : null;
  if (scopeGroup) {
    devs = devs.filter(d => (d.group || "") === scopeGroup.name);
  }
  const rows = devs.map(d => {
    const nb = (d.bastions || []).length || (d.bastion && d.bastion.host ? 1 : 0);
    return `
    <tr data-row="${esc(d.name)}" data-key="${esc(d.name)}">
      <td class="drag-handle" data-tip="${dragHandleTip()}">⠿</td>
      <td><input class="cell inl-name" data-name="${esc(d.name)}" value="${esc(d.name)}" style="font-weight:600"></td>
      <td><input class="cell inl-host mono" data-name="${esc(d.name)}" value="${esc(d.host)}"></td>
      <td><input class="cell inl-site" data-name="${esc(d.name)}" value="${esc(d.site || "")}" placeholder="${esc(t("拠点"))}"></td>
      <td>
        <select class="cell inl-conn" data-name="${esc(d.name)}">${connOptions(d.conn)}</select>
        ${nb ? `<span class="muted" style="font-size:11px">${esc(t("踏{n}", { n: nb }))}</span>` : ""}
      </td>
      <td><select class="cell inl-os" data-name="${esc(d.name)}">${osOptions(d.osType)}</select></td>
      <td><select class="cell inl-set" data-name="${esc(d.name)}">${cmdSetOptions(d.commandSet)}</select></td>
      <td style="text-align:right;white-space:nowrap">
        <button class="btn sm act-conn" data-term="${esc(d.name)}">${esc(t("接続"))}</button>
        <button class="btn sm act-edit" data-edit="${esc(d.name)}">${esc(t("編集"))}</button>
        <button class="btn sm act-copy" data-copy="${esc(d.name)}">${esc(t("複製"))}</button>
        <button class="btn sm act-del" data-del="${esc(d.name)}">${esc(t("削除"))}</button>
      </td>
    </tr>`;
  }).join("");

  const groups = INV.deviceGroups || [];
  const scopeLabel = scopeGroup ? esc(t("グループ「{g}」", { g: scopeGroup.name })) : esc(t("全機器"));
  root.innerHTML = `
    <div class="page-head">
      <div class="row-inline" style="gap:12px">
        <button class="btn sm" id="back-choose">${esc(t("← 対象選択"))}</button>
        <div><div class="page-title">${esc(t("機器一覧"))} <span class="muted" style="font-size:14px;font-weight:400">/ ${scopeLabel}</span></div>
          <div class="page-sub">${esc(t("{n} 台表示 / 実行対象のオンオフは実行画面で。表の項目は直接編集できます", { n: devs.length }))}</div></div>
      </div>
      <div class="row-inline">
        ${scopeGroup ? `<button class="btn" id="grp-rename">${esc(t("グループ名変更"))}</button>
        <button class="btn danger" id="grp-delete">${esc(t("グループ削除"))}</button>` : ""}
        <button class="btn" id="imp-csv">${esc(t("CSV読込"))}</button>
        <button class="btn" id="exp-csv">${esc(t("CSV書出"))}</button>
        <button class="btn primary" id="add-dev">${esc(t("＋ 機器を追加"))}</button>
      </div>
    </div>
    <div class="panel">
      ${devs.length === 0
        ? `<div class="empty">${esc(scopeGroup ? t("このグループに機器がありません。") : t("機器がありません。「＋ 機器を追加」から登録してください。"))}</div>`
        : `<table><thead><tr><th style="width:30px"></th>
            <th>${esc(t("ホスト名"))}</th><th style="width:140px">${esc(t("IPアドレス"))}</th><th style="width:120px">${esc(t("拠点名"))}</th><th style="width:100px">${esc(t("接続"))}</th>
            <th style="width:200px">OS</th><th style="width:200px">${esc(t("コマンドセット"))}</th><th></th></tr></thead>
            <tbody id="dev-body">${rows}</tbody></table>`}
    </div>`;

  document.getElementById("back-choose").onclick = () => { deviceView = { mode: "select", group: null }; renderDevices(); };
  // Adding inside a group scope pre-assigns that group to the new device.
  document.getElementById("add-dev").onclick = () => editDevice(null, scopeGroup ? scopeGroup.name : "");
  if (scopeGroup) {
    document.getElementById("grp-delete").onclick = async () => {
      if (!(await uiConfirm({ title: t("グループを削除"), message: t("グループ「<b>{g}</b>」を削除しますか？<br><span class=\"muted\" style=\"font-size:12px\">所属機器は残り、グループ未設定になります</span>", { g: esc(scopeGroup.name) }), okLabel: t("削除"), danger: true }))) return;
      await App().DeleteDeviceGroup(scopeGroup.name);
      await refreshInventory();
      deviceView = { mode: "select", group: null };
      renderDevices();
      toast(t("グループを削除しました"), "ok");
    };
    document.getElementById("grp-rename").onclick = () => {
      const node = h(`<div>
        <h3>${esc(t("グループ名を変更"))}</h3>
        <div class="field"><label>${esc(t("新しいグループ名"))}</label><input id="rn-name" value="${esc(scopeGroup.name)}" autofocus></div>
        <div class="modal-actions"><button class="btn" id="rn-cancel">${esc(t("キャンセル"))}</button>
          <button class="btn primary" id="rn-ok">${esc(t("変更"))}</button></div>
      </div>`);
      openModal(node, "mid");
      node.querySelector("#rn-cancel").onclick = closeModal;
      node.querySelector("#rn-ok").onclick = async () => {
        const nn = node.querySelector("#rn-name").value.trim();
        if (!nn) { toast(t("名前を入力してください"), "err"); return; }
        try {
          await App().RenameDeviceGroup(scopeGroup.name, nn);
          closeModal(); await refreshInventory();
          deviceView = { mode: "list", group: nn }; renderDevices();
          toast(t("変更しました"), "ok");
        } catch (e) { toast(t("変更失敗") + ": " + terr(e), "err"); }
      };
    };
  }
  document.getElementById("exp-csv").onclick = () => exportCsvDialog(scopeGroup ? scopeGroup.name : "");
  document.getElementById("imp-csv").onclick = () => importCsvDialog(scopeGroup ? scopeGroup.name : "");

  root.querySelectorAll("[data-edit]").forEach(b => b.onclick = () =>
    editDevice(INV.devices.find(d => d.name === b.dataset.edit)));
  root.querySelectorAll("[data-term]").forEach(b => b.onclick = async () => {
    try { await App().SpawnTerminal(b.dataset.term); toast(t("対話接続ウィンドウを開きました"), "ok"); }
    catch (e) { toast(t("接続失敗") + ": " + terr(e), "err"); }
  });
  root.querySelectorAll("[data-del]").forEach(b => b.onclick = () => delDevice(b.dataset.del));
  root.querySelectorAll("[data-copy]").forEach(b => b.onclick = async () => {
    try { const nm = await App().CopyDevice(b.dataset.copy); await refreshInventory(); toast(t("複製しました: {n}", { n: nm }), "ok"); }
    catch (e) { toast(t("複製失敗") + ": " + terr(e), "err"); }
  });
  // Inline edits (name / host / conn / os / command set) save on change, with validation.
  root.querySelectorAll(".inl-name").forEach(el => el.onchange = async () => {
    const nn = el.value.trim();
    const old = el.dataset.name;
    if (!nn || nn === old) { el.value = old; return; }
    try {
      await App().RenameDevice(old, nn);
      await refreshInventory();
      toast(t("ホスト名を変更しました"), "ok");
    } catch (e) {
      toast(t("変更できません") + ": " + terr(e), "err");
      await refreshInventory(); // revert the cell to the stored value
    }
  });
  // Drag-reorder rows (multi-select on the ⠿ handle); persists the order.
  wireRowReorder(root.querySelector("#dev-body"), async order => {
    try { await App().ReorderDevices(order); await refreshInventory(); }
    catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); await refreshInventory(); }
  });
  root.querySelectorAll(".inl-host").forEach(el => el.onchange = () => inlineSave(el.dataset.name, { host: el.value.trim() }));
  root.querySelectorAll(".inl-site").forEach(el => el.onchange = () => inlineSave(el.dataset.name, { site: el.value.trim() }));
  root.querySelectorAll(".inl-conn").forEach(el => el.onchange = () => inlineSave(el.dataset.name, { conn: el.value }));
  root.querySelectorAll(".inl-os").forEach(el => el.onchange = () => inlineSave(el.dataset.name, { osType: el.value }));
  root.querySelectorAll(".inl-set").forEach(el => el.onchange = () => inlineSave(el.dataset.name, { commandSet: el.value }));
}

// inlineSave merges a patch into an existing device and saves it, showing a
// validation error (and reverting the table) if the edit is rejected.
async function inlineSave(name, patch) {
  const cur = (INV.devices || []).find(d => d.name === name);
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

async function delDevice(name) {
  if (!(await uiConfirm({ title: t("機器を削除"), message: t("機器「<b>{n}</b>」を削除しますか？", { n: esc(name) }), okLabel: t("削除"), danger: true }))) return;
  await App().DeleteDevice(name); await refreshInventory(); toast(t("削除しました"), "ok");
}

// CSV export: choose which group (or all) to write out.
function exportCsvDialog(defaultGroup) {
  const groups = INV.deviceGroups || [];
  const opts = `<option value="__ALL__">${esc(t("全機器（{n}台）", { n: (INV.devices || []).length }))}</option>` +
    groups.map(g => `<option value="${esc(g.name)}" ${g.name === defaultGroup ? "selected" : ""}>${esc(t("{g}（{n}台）", { g: g.name, n: groupCount(g.name) }))}</option>`).join("");
  const node = h(`<div>
    <h3>${esc(t("CSV書き出し"))}</h3>
    <div class="field"><label>${esc(t("書き出す対象"))}</label><select id="cx-grp">${opts}</select></div>
    <div class="muted" style="font-size:12px">${esc(t("※パスワードも平文で書き出されます。編集後は削除してください。"))}</div>
    <div class="modal-actions"><button class="btn" id="cx-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="cx-ok">${esc(t("書き出す"))}</button></div>
  </div>`);
  openModal(node, "mid");
  node.querySelector("#cx-cancel").onclick = closeModal;
  node.querySelector("#cx-ok").onclick = async () => {
    const v = node.querySelector("#cx-grp").value;
    closeModal();
    try {
      const p = await App().ExportDevicesCSVGroup(v === "__ALL__" ? "" : v);
      if (p) toast(t("書き出しました: {p}", { p }), "ok");
    } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
  };
}

// CSV import: keep the CSV's own groups, or force everything into one group.
function importCsvDialog(defaultGroup) {
  const groups = INV.deviceGroups || [];
  const opts = groups.map(g => `<option value="${esc(g.name)}" ${g.name === defaultGroup ? "selected" : ""}>${esc(g.name)}</option>`).join("");
  const node = h(`<div>
    <h3>${esc(t("CSV読み込み"))}</h3>
    <div class="field"><label>${esc(t("読み込んだ機器の所属グループ"))}</label>
      <select id="ci-mode">
        <option value="keep">${esc(t("CSVのグループをそのまま使う"))}</option>
        <option value="assign" ${defaultGroup ? "selected" : ""}>${esc(t("指定グループに追加する"))}</option>
      </select></div>
    <div class="field" id="ci-grp-wrap"><label>${esc(t("追加先グループ"))}</label>
      <input id="ci-grp" list="ci-grp-list" value="${esc(defaultGroup || "")}" placeholder="${esc(t("グループ名"))}">
      <datalist id="ci-grp-list">${opts}</datalist></div>
    <div class="modal-actions"><button class="btn" id="ci-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="ci-ok">${esc(t("ファイルを選択して読み込む"))}</button></div>
  </div>`);
  openModal(node, "mid");
  const modeSel = node.querySelector("#ci-mode");
  const grpWrap = node.querySelector("#ci-grp-wrap");
  const syncMode = () => { grpWrap.style.display = modeSel.value === "assign" ? "" : "none"; };
  modeSel.onchange = syncMode; syncMode();
  node.querySelector("#ci-cancel").onclick = closeModal;
  node.querySelector("#ci-ok").onclick = async () => {
    const target = modeSel.value === "assign" ? node.querySelector("#ci-grp").value.trim() : "";
    if (modeSel.value === "assign" && !target) { toast(t("グループ名を入力してください"), "err"); return; }
    closeModal();
    try {
      const n = await App().ImportDevicesCSVToGroup(target);
      if (n > 0) { await refreshInventory(); toast(t("{n} 台を読み込みました", { n }), "ok"); }
    } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
  };
}

function editDevice(dev, preGroup) {
  const d = dev || { conn: "ssh", osType: "cisco-ios", authMethod: "password", enabled: true, group: preGroup || "" };
  // Working copy of the bastion chain (fold legacy single bastion in).
  let bastions = (d.bastions && d.bastions.length) ? d.bastions.map(x => ({ ...x }))
    : (d.bastion && d.bastion.host ? [{ ...d.bastion }] : []);
  const profOpts = PROFILES.map(p => `<option value="${esc(p.key)}" ${d.osType === p.key ? "selected" : ""}>${esc(p.name)}</option>`).join("");
  const setOpts = `<option value="">${esc(t("(なし)"))}</option>` + (INV.commandSets || []).map(s =>
    `<option value="${esc(s.name)}" ${d.commandSet === s.name ? "selected" : ""}>${esc(s.name)}</option>`).join("");
  const serialOpts = `<option value="">${esc(t("自動検出"))}</option>` + SERIAL.map(p =>
    `<option value="${esc(p)}" ${d.serialPort === p ? "selected" : ""}>${esc(p)}</option>`).join("");

  const groupListId = "grouplist-dev";
  const groupOpts = (INV.deviceGroups || []).map(g => `<option value="${esc(g.name)}">`).join("");
  const node = h(`<div>
    <h3>${esc(dev ? t("機器を編集") : t("機器を追加"))}</h3>
    <div class="grid-2">
      <div class="field"><label>${esc(t("ホスト名（ログファイル名に使用）"))}</label><input id="f-name" value="${esc(d.name || "")}" ${dev ? "readonly" : ""}></div>
      <div class="field"><label>${esc(t("グループ（会社）"))}</label>
        <input id="f-group" list="${groupListId}" value="${esc(d.group || "")}" placeholder="${esc(t("例: A社（空欄=未設定）"))}">
        <datalist id="${groupListId}">${groupOpts}</datalist></div>
    </div>
    <div class="grid-2">
      <div class="field"><label>${esc(t("IPアドレス"))}</label><input id="f-host" value="${esc(d.host || "")}"></div>
      <div class="field"><label>${esc(t("拠点（任意）"))}</label><input id="f-site" value="${esc(d.site || "")}" placeholder="${esc(t("例: 本社 / 東京DC"))}"></div>
    </div>
    <div class="grid-3">
      <div class="field"><label>${esc(t("接続方式"))}</label><select id="f-conn">
        <option value="ssh" ${d.conn === "ssh" ? "selected" : ""}>SSH</option>
        <option value="telnet" ${d.conn === "telnet" ? "selected" : ""}>Telnet</option>
        <option value="serial" ${d.conn === "serial" ? "selected" : ""}>${esc(t("シリアル"))}</option></select></div>
      <div class="field"><label>${esc(t("OSタイプ"))}</label><select id="f-os">${profOpts}</select></div>
      <div class="field"><label>${esc(t("コマンドセット"))}</label><select id="f-set">${setOpts}</select></div>
    </div>
    <div id="conn-extra"></div>

    <div class="section-label">${esc(t("認証情報（暗号化保存）"))}</div>
    <div class="grid-2">
      <div class="field"><label>${esc(t("ユーザー名"))}</label><input id="f-user" value="${esc(d.username || "")}"></div>
      <div class="field" id="fw-authmethod"><label>${esc(t("SSH認証方式"))}</label><select id="f-auth">
        <option value="password" ${d.authMethod === "password" ? "selected" : ""}>${esc(t("パスワード"))}</option>
        <option value="publickey" ${d.authMethod === "publickey" ? "selected" : ""}>${esc(t("公開鍵 (Ed25519/RSA/ECDSA)"))}</option></select></div>
    </div>
    <div class="grid-2">
      <div class="field"><label>${esc(t("パスワード"))}</label><input id="f-pw" type="password" value="${esc(d.password || "")}"></div>
      <div class="field"><label>${esc(t("enable / 昇格パスワード（任意・ない機種は空欄）"))}</label><input id="f-en" type="password" value="${esc(d.enablePassword || "")}"></div>
    </div>
    <div class="field" id="fw-keyfile"><label>${esc(t("秘密鍵ファイル"))}</label>
      <div class="row-inline" style="gap:6px">
        <input id="f-key" style="flex:1" value="${esc(d.keyFile || "")}" placeholder="C:\\Users\\...\\id_ed25519">
        <button class="btn sm" id="f-key-pick" type="button">${esc(t("参照…"))}</button>
      </div></div>
    <div class="field" id="fw-keypass"><label>${esc(t("秘密鍵のパスフレーズ（保護されていない鍵は空欄・暗号化保存）"))}</label>
      <input id="f-keypass" type="password" value="${esc(d.keyPassphrase || "")}"></div>
    <div class="field" id="fw-legacy">
      <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
        <input type="checkbox" id="f-legacy" ${d.legacyAlgos ? "checked" : ""} style="width:auto">
        <span data-tip="${esc(t("SSHで古い暗号方式（SHA-1系の鍵交換・CBC・3DES）も候補に含めます。通常はオフのまま（強い方式のみ）。新しい方式に対応していない古い機器で接続エラーになる場合だけ有効にしてください"))}">${esc(t("レガシー暗号を許可（古い機器向け・通常はオフ） ⓘ"))}</span>
      </label></div>
    ${dev && d.conn !== "serial" ? `<div class="field" id="fw-hostkey">
      <div class="row-inline" style="gap:10px;align-items:center">
        <button class="btn sm" id="f-hk-clear" type="button">${esc(t("ホストキー記録を削除"))}</button>
        <span class="muted" style="font-size:12px" data-tip="${esc(t("SSHのホストキーは初回接続時に記録され、次回以降は一致を検証します（なりすまし防止）。機器を交換・OS再インストールしてホストキーが変わった場合のみ、記録を削除して再接続してください"))}">${esc(t("機器交換でホストキーが変わったときに使用"))} ⓘ</span>
      </div></div>` : ""}

    <div class="section-label">${esc(t("踏み台サーバ（任意・最大5段。外側＝手前から順に）"))}</div>
    <div id="bastions-host"></div>
    <button class="btn sm" id="add-bastion" type="button" style="margin-top:6px">${esc(t("＋ 踏み台を追加"))}</button>

    <div class="modal-actions">
      <button class="btn" id="cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="save">${esc(t("保存"))}</button>
    </div>
  </div>`);

  openModal(node);
  wirePasswordToggles(node);

  const connExtra = node.querySelector("#conn-extra");
  function syncConn() {
    const c = node.querySelector("#f-conn").value;
    if (c === "serial") {
      connExtra.innerHTML = `<div class="grid-2">
        <div class="field"><label>${esc(t("COMポート"))}</label><select id="f-serial">${serialOpts}</select></div>
        <div class="field"><label>${esc(t("ボーレート"))}</label><input id="f-baud" type="number" value="${d.baud || 9600}"></div></div>`;
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
  }
  node.querySelector("#f-conn").onchange = syncConn;
  node.querySelector("#f-auth").onchange = syncAuth;
  node.querySelector("#f-key-pick").onclick = async () => {
    try {
      const p = await App().PickKeyFile();
      if (p) node.querySelector("#f-key").value = p;
    } catch (e) { toast(t("選択失敗") + ": " + terr(e), "err"); }
  };
  const hkClear = node.querySelector("#f-hk-clear");
  if (hkClear) hkClear.onclick = async () => {
    if (!(await uiConfirm({ title: t("ホストキー記録を削除"), message: t("機器「<b>{n}</b>」（踏み台含む）のホストキー記録を削除しますか？<br><span class=\"muted\" style=\"font-size:12px\">次回接続時に新しいホストキーを記録し直します。機器を交換していないのにキーが変わった場合は、削除せずネットワーク管理者に確認してください</span>", { n: esc(d.name || "") }), okLabel: t("削除"), danger: true }))) return;
    try { await App().ClearHostKeys(d.name); toast(t("ホストキー記録を削除しました"), "ok"); }
    catch (e) { toast(terr(e), "err"); }
  };
  syncConn();

  // ---- bastion chain (up to 5 hops) ----
  const bhost = node.querySelector("#bastions-host");
  const addBtn = node.querySelector("#add-bastion");
  function collectBastions() {
    const rows = bhost.querySelectorAll("[data-bastion-row]");
    const arr = [];
    rows.forEach(r => arr.push({
      host: r.querySelector(".b-host").value.trim(),
      method: r.querySelector(".b-method").value,
      username: r.querySelector(".b-user").value,
      password: r.querySelector(".b-pw").value,
      authMethod: r.querySelector(".b-auth").value,
      keyFile: r.querySelector(".b-key").value,
      keyPassphrase: r.querySelector(".b-keypass").value,
      jumpCommand: r.querySelector(".b-jump").value.trim(),
    }));
    return arr;
  }
  function renderBastions() {
    bhost.innerHTML = bastions.map((b, i) => `
      <div class="card" data-bastion-row style="padding:14px 16px;margin:10px 0">
        <div class="row-inline" style="justify-content:space-between;margin-bottom:8px">
          <b>${esc(t("{n}段目の踏み台", { n: i + 1 }))}</b>
          <button class="btn sm act-del" type="button" data-rm="${i}">${esc(t("削除"))}</button>
        </div>
        <div class="grid-2">
          <div class="field" style="margin-bottom:8px"><label>${esc(t("ホスト"))}</label><input class="b-host" value="${esc(b.host || "")}"></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("接続方式"))}</label><select class="b-method">
            <option value="ssh" ${b.method === "ssh" ? "selected" : ""}>SSH</option>
            <option value="telnet" ${b.method === "telnet" ? "selected" : ""}>Telnet</option></select></div>
        </div>
        <div class="grid-3">
          <div class="field" style="margin-bottom:8px"><label>${esc(t("ユーザー"))}</label><input class="b-user" value="${esc(b.username || "")}"></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("パスワード"))}</label><input class="b-pw" type="password" value="${esc(b.password || "")}"></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("SSH認証"))}</label><select class="b-auth">
            <option value="password" ${b.authMethod === "password" ? "selected" : ""}>${esc(t("パスワード"))}</option>
            <option value="publickey" ${b.authMethod === "publickey" ? "selected" : ""}>${esc(t("公開鍵"))}</option></select></div>
        </div>
        <div class="grid-2">
          <div class="field" style="margin-bottom:8px"><label>${esc(t("秘密鍵ファイル（公開鍵認証時）"))}</label>
            <div class="row-inline" style="gap:6px">
              <input class="b-key" style="flex:1" value="${esc(b.keyFile || "")}">
              <button class="btn sm b-key-pick" type="button">${esc(t("参照…"))}</button>
            </div></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("鍵のパスフレーズ（任意）"))}</label>
            <input class="b-keypass" type="password" value="${esc(b.keyPassphrase || "")}"></div>
        </div>
        <div class="field" style="margin-bottom:0"><label>${esc(t("次ホップへのジャンプコマンド（空欄=標準。NW機器踏み台などコマンド形式が違う場合に指定）"))}</label>
          <input class="b-jump mono" value="${esc(b.jumpCommand || "")}" placeholder="${esc(t("例: ssh -l {user} {host}　（使用可: {user} {host} {port}）"))}"></div>
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
    // Pre-fill the last used key path when a bastion switches to public key.
    bhost.querySelectorAll(".b-auth").forEach(sel => sel.onchange = () => {
      const inp = sel.closest("[data-bastion-row]").querySelector(".b-key");
      if (sel.value === "publickey" && !inp.value && (INV.settings || {}).lastKeyFile) {
        inp.value = INV.settings.lastKeyFile;
      }
    });
    wirePasswordToggles(bhost);
    addBtn.style.display = bastions.length >= 5 ? "none" : "";
  }
  addBtn.onclick = () => {
    bastions = collectBastions();
    if (bastions.length >= 5) return;
    bastions.push({ method: "ssh", authMethod: "password" });
    renderBastions();
  };
  renderBastions();

  node.querySelector("#cancel").onclick = closeModal;
  node.querySelector("#save").onclick = async () => {
    const conn = node.querySelector("#f-conn").value;
    const out = {
      name: node.querySelector("#f-name").value.trim(),
      group: node.querySelector("#f-group").value.trim(),
      site: node.querySelector("#f-site").value.trim(),
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
      enabled: d.enabled !== false,
    };
    if (conn === "serial") {
      out.serialPort = node.querySelector("#f-serial").value;
      out.baud = parseInt(node.querySelector("#f-baud").value, 10) || 9600;
    } else {
      out.port = parseInt(node.querySelector("#f-port").value, 10) || 0;
    }
    const chain = collectBastions().filter(b => b.host && b.method);
    if (chain.length) out.bastions = chain.slice(0, 5);
    if (!out.name) { toast(t("ホスト名は必須です"), "err"); return; }
    if (conn !== "serial" && !out.host) { toast(t("IPアドレスは必須です"), "err"); return; }
    // New device: refuse a name that already exists.
    if (!dev && await App().DeviceNameExists(out.name)) {
      toast(t("「{n}」は既に登録されています。別のホスト名にしてください", { n: out.name }), "err");
      return;
    }
    try { await App().SaveDevice(out); closeModal(); await refreshInventory(); toast(t("保存しました"), "ok"); }
    catch (e) { toast(terr(e), "err"); }
  };
}
