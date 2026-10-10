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
        <button class="btn" id="imp-csv">${esc(t("読込"))}</button>
        <button class="btn" id="exp-csv">${esc(t("一式書出"))}</button>
      </div>
    </div>
    <div class="chooser">
      <button class="choice new" id="choice-new">
        <div class="choice-t">${esc(t("＋ 新規グループ作成"))}</div>
        <div class="choice-n">${esc(t("会社・拠点などの単位で作成"))}</div>
      </button>
      ${groups.map(g => `<div class="choice" role="button" tabindex="0" draggable="true" data-open="${esc(g.name)}" data-grp="${esc(g.name)}">
        <div class="row-inline" style="justify-content:space-between;align-items:flex-start">
          <div class="choice-t">${esc(g.name)}</div>
          <button class="btn sm act-copy" type="button" data-dup="${esc(g.name)}" title="${esc(t("グループを複製"))}">${esc(t("複製"))}</button>
        </div>
        <div class="choice-n">${esc(t("{n} 台", { n: groupCount(g.name) }))} <span class="muted" style="font-size:11px">${esc(t("⠿ ドラッグで並替"))}</span></div>
      </div>`).join("")}
    </div>`;

  document.getElementById("exp-csv").onclick = () => exportBundleDialog("");
  document.getElementById("imp-csv").onclick = () => importDialog("");
  document.getElementById("choice-new").onclick = newGroupPrompt;
  root.querySelectorAll("[data-open]").forEach(b => {
    b.onclick = e => {
      if (e.target.closest("[data-dup]")) return;
      deviceView = { mode: "list", group: b.dataset.open === "__ALL__" ? null : b.dataset.open };
      renderDevices();
    };
    b.onkeydown = e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); b.onclick(e); } };
  });
  root.querySelectorAll("[data-dup]").forEach(b => b.onclick = e => { e.stopPropagation(); duplicateGroupDialog(b.dataset.dup); });
  wireGroupDrag(root);
}

// 複製: a new group with the same default credentials and copies of every
// device (names get a suffix — they name the log files, so they stay unique).
function duplicateGroupDialog(name) {
  const n = groupCount(name);
  const node = h(`<div>
    <h3>${esc(t("グループを複製"))} <span class="muted" style="font-size:14px;font-weight:400">/ ${esc(name)}</span></h3>
    <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("グループ「{g}」の既定の認証情報と機器 {n} 台をコピーして新しいグループを作ります。機器名はログファイル名になるため重複できず、接尾辞を付けて複製します。", { g: name, n }))}</p>
    <div class="field"><label>${esc(t("新しいグループ名"))} <span class="req">${esc(t("必須"))}</span></label><input id="dg-name" value="${esc(name + "_copy")}" autofocus></div>
    <div class="field"><label>${esc(t("機器名の接尾辞"))} <span class="req">${esc(t("必須"))}</span></label><input id="dg-suffix" value="_copy"></div>
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
    if (n > 0 && !suffix) { toast(t("機器名の接尾辞を入力してください"), "err"); return; }
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
      const was = (INV.deviceGroups || []).map(g => g.name);
      dragEl = null;
      if (order.join("\u0000") === was.join("\u0000")) return; // dropped where it was
      try {
        await App().ReorderDeviceGroups(order); await refreshInventory();
        toast(t("並び順を保存しました"), "ok");
      } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
    });
    // Cards are laid out in a grid, so the drop side follows the pointer's
    // position within the card both ways (left/top half = before it).
    card.addEventListener("dragover", e => {
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
      if (!dragEl || dragEl === card) return;
      card.classList.add("dragover");
      const box = card.getBoundingClientRect();
      const before = (e.clientX - box.left) / box.width + (e.clientY - box.top) / box.height < 1;
      card.parentNode.insertBefore(dragEl, before ? card : card.nextSibling);
    });
    card.addEventListener("dragleave", () => card.classList.remove("dragover"));
    card.addEventListener("drop", e => { e.preventDefault(); card.classList.remove("dragover"); });
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
      <td><input class="cell inl-role" data-name="${esc(d.name)}" value="${esc(d.role || "")}" placeholder="${esc(t("役割"))}"></td>
      <td>
        <select class="cell inl-conn" data-name="${esc(d.name)}">${connOptions(d.conn)}</select>
        ${nb ? `<span class="muted" style="font-size:11px">${esc(t("踏{n}", { n: nb }))}</span>` : ""}
        ${d.useGroupCreds ? `<span class="badge" data-tip="${esc(t("グループ既定の認証情報でログインします"))}">${esc(t("既定"))}</span>` : ""}
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
        ${scopeGroup ? `<button class="btn" id="grp-creds" data-tip="${esc(t("このグループの機器が共通で使うユーザー名・パスワード。機器ごとに「グループ既定を使う／個別に設定」を選べます"))}">${esc(t("グループ既定の認証情報…"))}</button>
        <button class="btn" id="grp-rename">${esc(t("グループ名変更"))}</button>
        <button class="btn danger" id="grp-delete">${esc(t("グループ削除"))}</button>` : ""}
        <button class="btn" id="imp-csv">${esc(t("読込"))}</button>
        <button class="btn" id="exp-csv">${esc(t("一式書出"))}</button>
        <button class="btn primary" id="add-dev">${esc(t("＋ 機器を追加"))}</button>
      </div>
    </div>
    <div class="panel">
      ${devs.length === 0
        ? `<div class="empty">${esc(scopeGroup ? t("このグループに機器がありません。") : t("機器がありません。「＋ 機器を追加」から登録してください。"))}</div>`
        : `<table><thead><tr><th style="width:30px"></th>
            <th>${esc(t("ホスト名"))}</th><th style="width:130px">${esc(t("IPアドレス"))}</th><th style="width:100px">${esc(t("拠点名"))}</th><th style="width:100px">${esc(t("役割"))}</th><th style="width:100px">${esc(t("接続"))}</th>
            <th style="width:170px">OS</th><th style="width:170px">${esc(t("コマンドセット"))}</th><th style="width:240px"></th></tr></thead>
            <tbody id="dev-body">${rows}</tbody></table>`}
    </div>`;

  document.getElementById("back-choose").onclick = () => { deviceView = { mode: "select", group: null }; renderDevices(); };
  // Adding inside a group scope pre-assigns that group to the new device.
  document.getElementById("add-dev").onclick = () => editDevice(null, scopeGroup ? scopeGroup.name : "");
  if (scopeGroup) {
    document.getElementById("grp-creds").onclick = () => groupCredsDialog(scopeGroup.name);
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
        <div class="field"><label>${esc(t("新しいグループ名"))} <span class="req">${esc(t("必須"))}</span></label><input id="rn-name" value="${esc(scopeGroup.name)}" autofocus></div>
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
  document.getElementById("exp-csv").onclick = () => exportBundleDialog(scopeGroup ? scopeGroup.name : "");
  document.getElementById("imp-csv").onclick = () => importDialog(scopeGroup ? scopeGroup.name : "");

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
  root.querySelectorAll(".inl-role").forEach(el => el.onchange = () => inlineSave(el.dataset.name, { role: el.value.trim() }));
  // Switching the method here must carry the port with it, the way the editor
  // dialog already does: leaving 22 behind on a switch to Telnet points a
  // Telnet client at the SSH port, where it reads the "SSH-2.0-..." banner and
  // then waits forever for a login prompt. A port the user chose deliberately
  // (anything but the two standard ones) survives the switch.
  root.querySelectorAll(".inl-conn").forEach(el => el.onchange = () => {
    const patch = { conn: el.value };
    const dev = (INV.devices || []).find(x => x.name === el.dataset.name) || {};
    const port = dev.port || 0;
    if (el.value !== "serial" && (!port || port === 22 || port === 23)) {
      patch.port = el.value === "ssh" ? 22 : 23;
    }
    inlineSave(el.dataset.name, patch);
  });
  root.querySelectorAll(".inl-os").forEach(el => el.onchange = () => inlineSave(el.dataset.name, { osType: el.value }));
  root.querySelectorAll(".inl-set").forEach(el => el.onchange = () => inlineSave(el.dataset.name, { commandSet: el.value }));
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
      <div class="field"><label>${esc(t("ユーザー名"))}</label><input id="gc-user" value="${esc(g.username || "")}"></div>
      <div class="field"><label>${esc(t("パスワード"))}</label><input id="gc-pw" type="password" value="${esc(g.password || "")}"></div>
    </div>
    <div class="field"><label>${esc(t("enable / 昇格パスワード（任意・ない機種は空欄）"))}</label><input id="gc-en" type="password" value="${esc(g.enablePassword || "")}"></div>
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer;font-size:13px"><input type="checkbox" id="gc-all" style="width:auto"> ${esc(t("このグループの全機器（{n} 台）を「グループ既定を使う」に切り替える", { n: members.length }))}</label>
    <div class="muted" style="font-size:12px;margin-top:4px">${esc(t("オフのままなら各機器の設定は変わりません。個別のパスワードが要る機器は、機器の編集画面で「この機器に個別に設定」を選んでください。"))}</div>
    <div class="modal-actions">
      <button class="btn" id="gc-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="gc-save">${esc(t("保存"))}</button>
    </div>
  </div>`);
  openModal(node, "mid");
  wirePasswordToggles(node);
  node.querySelector("#gc-cancel").onclick = closeModal;
  node.querySelector("#gc-save").onclick = async () => {
    const out = { ...g, name: groupName,
      username: node.querySelector("#gc-user").value,
      password: node.querySelector("#gc-pw").value,
      enablePassword: node.querySelector("#gc-en").value };
    try {
      await App().SaveDeviceGroup(out);
      if (node.querySelector("#gc-all").checked) {
        for (const d of members) if (!d.useGroupCreds) await App().SaveDevice({ ...d, useGroupCreds: true });
      }
      closeModal(); await refreshInventory(); toast(t("保存しました"), "ok");
    } catch (e) { toast(t("保存失敗") + ": " + terr(e), "err"); }
  };
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
      const dir = await App().ExportBundle(v === "__ALL__" ? "" : v);
      if (!dir) return;
      if (await uiConfirm({ title: t("書き出しました"), message: `<span class="muted" style="font-size:12px">${esc(dir)}</span>`, okLabel: t("フォルダを開く") })) {
        try { await App().OpenExportFolder(dir); } catch (e) { toast(terr(e), "err"); }
      }
    } catch (e) { toast(t("書き出し失敗") + ": " + terr(e), "err"); }
  };
}

// 読込: a bundle's palaterm-bundle.json (devices + command sets + OS
// profiles) or a bare devices CSV. Options first, then the file picker, then a
// preview of what will change before anything is written.
function importDialog(defaultGroup) {
  const groups = INV.deviceGroups || [];
  const opts = groups.map(g => `<option value="${esc(g.name)}" ${g.name === defaultGroup ? "selected" : ""}>${esc(g.name)}</option>`).join("");
  const node = h(`<div>
    <h3>${esc(t("読み込み"))}</h3>
    <div class="muted" style="font-size:12px;margin-bottom:8px">${esc(t("一式の目録（palaterm-bundle.json）を選ぶと機器・コマンドセット・OSタイププロファイルをまとめて、機器CSV（*.csv）を選ぶと機器だけを読み込みます。機器は機器名をキーに、既存は上書き・新規は追加です。"))}</div>
    <div class="field"><label>${esc(t("読み込んだ機器の所属グループ"))}</label>
      <select id="ci-mode">
        <option value="keep">${esc(t("ファイルのグループをそのまま使う"))}</option>
        <option value="assign" ${defaultGroup ? "selected" : ""}>${esc(t("指定グループに追加する"))}</option>
      </select></div>
    <div class="field" id="ci-grp-wrap"><label>${esc(t("追加先グループ"))} <span class="req">${esc(t("必須"))}</span></label>
      <input id="ci-grp" list="ci-grp-list" value="${esc(defaultGroup || "")}" placeholder="${esc(t("グループ名"))}">
      <datalist id="ci-grp-list">${opts}</datalist></div>
    <div class="field"><label style="font-weight:normal"><input type="checkbox" id="ci-ow" style="width:auto"> ${esc(t("同名の既存コマンドセット・OSタイププロファイルも一式の内容で上書きする"))}</label></div>
    <div class="muted" style="font-size:12px">${esc(t("オフのときは、既にあるコマンドセット・プロファイルはそのまま残し、無いものだけ追加します（他のグループが使っている設定を壊さないため）。"))}</div>
    <div class="modal-actions"><button class="btn" id="ci-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="ci-ok">${esc(t("ファイルを選択"))}</button></div>
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
    const ow = node.querySelector("#ci-ow").checked;
    closeModal();
    let pv;
    try { pv = await App().PickImportFile(target, ow); }
    catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); return; }
    if (!pv) return;
    const s = pv.summary;
    const lines = [t("機器: {n} 台（新規 {a}・上書き {b}）", { n: s.devices, a: s.devicesNew, b: s.devicesUpdated })];
    if (pv.kind === "bundle") {
      lines.push(t("コマンドセット: {n}（新規 {a}・上書き {b}・既存のまま {c}）", { n: s.sets, a: s.setsNew, b: s.setsUpdated, c: s.setsSkipped }));
      lines.push(t("OSタイププロファイル: {n}（新規 {a}・上書き {b}・既存のまま {c}）", { n: s.profiles, a: s.profilesNew, b: s.profilesUpdated, c: s.profilesSkipped }));
    }
    if (s.groupsCreated.length) lines.push(t("新規グループ: {g}", { g: s.groupsCreated.join(", ") }));
    if (s.missingSets.length) lines.push(t("⚠ 見つからないコマンドセット（機器は読み込まれますが割り当ては空扱い）: {g}", { g: s.missingSets.join(", ") }));
    if (s.missingProfiles.length) lines.push(t("⚠ 見つからないOSタイプ（機器は読み込まれますが汎用プロファイルで動きます）: {g}", { g: s.missingProfiles.join(", ") }));
    const msg = `<span class="muted" style="font-size:12px">${esc(pv.path)}</span><br>` + lines.map(esc).join("<br>");
    if (!(await uiConfirm({ title: t("読み込み内容の確認"), message: msg, okLabel: t("読み込む") }))) return;
    try {
      const r = await App().ApplyImport(pv.path, target, ow);
      PROFILES = await App().ListProfiles();
      await refreshInventory();
      toast(t("{n} 台を読み込みました", { n: r.devices }), "ok");
    } catch (e) { toast(t("読み込み失敗") + ": " + terr(e), "err"); }
  };
}

function editDevice(dev, preGroup) {
  const preG = preGroup ? (INV.deviceGroups || []).find(g => g.name === preGroup) : null;
  const preHasCreds = !!(preG && (preG.username || preG.password || preG.enablePassword));
  // A device added inside a group that has default credentials starts on
  // them; everything else starts on its own fields.
  const d = dev || { conn: "ssh", osType: "cisco-ios", authMethod: "password", enabled: true, group: preGroup || "", useGroupCreds: preHasCreds };
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
    <p class="muted" style="font-size:12px;margin:-10px 0 12px"><span class="req">${esc(t("必須"))}</span> ${esc(t("の付いた項目以外は任意です（空欄のままで保存できます）"))}</p>
    <div class="grid-2">
      <div class="field"><label>${esc(t("ホスト名（ログファイル名に使用）"))} <span class="req">${esc(t("必須"))}</span></label><input id="f-name" value="${esc(d.name || "")}" ${dev ? "readonly" : ""}></div>
      <div class="field"><label>${esc(t("グループ"))}</label>
        <div class="row-inline" id="f-group-ro" style="gap:12px;min-height:38px;align-items:center"><b>${esc(d.group || t("（未設定）"))}</b>
          <span class="link" id="f-group-change" style="font-size:12px">${esc(t("別のグループへ移す…"))}</span></div>
        <input id="f-group" list="${groupListId}" value="${esc(d.group || "")}" placeholder="${esc(t("例: A社（空欄=未設定）"))}" style="display:none">
        <datalist id="${groupListId}">${groupOpts}</datalist></div>
    </div>
    <div class="grid-2">
      <div class="field"><label>${esc(t("IPアドレス"))} <span class="req" id="f-host-req">${esc(t("必須"))}</span></label><input id="f-host" value="${esc(d.host || "")}"></div>
      <div class="field"><label>${esc(t("拠点（任意）"))}</label><input id="f-site" value="${esc(d.site || "")}" placeholder="${esc(t("例: 本社 / 東京DC"))}"></div>
    </div>
    <div class="grid-2">
      <div class="field"><label><span data-tip="${esc(t("機器の役割を表す自由なラベルです。拠点と同じく、ログ設定のテンプレートで {role} として使えます"))}">${esc(t("役割（任意） ⓘ"))}</span></label><input id="f-role" value="${esc(d.role || "")}" placeholder="${esc(t("例: コア / エッジ / FW"))}"></div>
    </div>
    <div class="grid-3">
      <div class="field"><label>${esc(t("接続方式"))} <span class="req">${esc(t("必須"))}</span></label><select id="f-conn">
        <option value="ssh" ${d.conn === "ssh" ? "selected" : ""}>SSH</option>
        <option value="telnet" ${d.conn === "telnet" ? "selected" : ""}>Telnet</option>
        <option value="serial" ${d.conn === "serial" ? "selected" : ""}>${esc(t("シリアル"))}</option></select></div>
      <div class="field"><label>${esc(t("OSタイプ"))} <span class="req">${esc(t("必須"))}</span></label><select id="f-os">${profOpts}</select></div>
      <div class="field"><label>${esc(t("コマンドセット"))}</label><select id="f-set">${setOpts}</select></div>
    </div>
    <div id="conn-extra"></div>

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
        <option value="publickey" ${d.authMethod === "publickey" ? "selected" : ""}>${esc(t("公開鍵 (Ed25519/RSA/ECDSA)"))}</option></select></div>
    </div>
    <div class="grid-2" id="fw-pwrow">
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
    // A serial device has no address: the IP field stops being required.
    node.querySelector("#f-host-req").style.display = c === "serial" ? "none" : "";
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
  function renderBastions() {
    bhost.innerHTML = bastions.map((b, i) => `
      <div class="card" data-bastion-row style="padding:14px 16px;margin:10px 0">
        <div class="row-inline" style="justify-content:space-between;margin-bottom:8px">
          <b>${esc(t("{n}段目の踏み台", { n: i + 1 }))}</b>
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
        <div class="field" style="margin-bottom:8px${b.method === "telnet" || i > 0 ? ";display:none" : ""}" data-b-legacy>
          <label><input class="b-legacy" type="checkbox" ${b.legacyAlgos ? "checked" : ""}>
            <span data-tip="${esc(t("この踏み台とのSSHで古い暗号方式も候補に含めます。機器側の同名設定とは独立しています（踏み台と機器は別のマシンなので、片方が古いことをもう片方の暗号強度を下げる理由にはしません）"))}">${esc(t("この踏み台にレガシー暗号を許可 ⓘ"))}</span></label></div>
        <div class="grid-3">
          <div class="field" style="margin-bottom:8px"><label>${esc(t("ユーザー"))} <span class="req">${esc(t("必須"))}</span></label><input class="b-user" value="${esc(b.username || "")}"></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("パスワード"))}</label><input class="b-pw" type="password" value="${esc(b.password || "")}"></div>
          <div class="field" style="margin-bottom:8px${i > 0 ? ";display:none" : ""}"><label>${esc(t("SSH認証"))}</label><select class="b-auth">
            <option value="password" ${b.authMethod === "password" ? "selected" : ""}>${esc(t("パスワード"))}</option>
            <option value="publickey" ${b.authMethod === "publickey" ? "selected" : ""}>${esc(t("公開鍵"))}</option></select></div>
        </div>
        ${i > 0 ? `<div class="muted" style="font-size:12px;margin-bottom:8px">${esc(t(NOTE_HOP))}</div>` : ""}
        <div class="grid-2" ${i > 0 ? 'style="display:none"' : ""}>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("秘密鍵ファイル（公開鍵認証時）"))}</label>
            <div class="row-inline" style="gap:6px">
              <input class="b-key" style="flex:1" value="${esc(b.keyFile || "")}">
              <button class="btn sm b-key-pick" type="button">${esc(t("参照…"))}</button>
            </div></div>
          <div class="field" style="margin-bottom:8px"><label>${esc(t("鍵のパスフレーズ（任意）"))}</label>
            <input class="b-keypass" type="password" value="${esc(b.keyPassphrase || "")}"></div>
        </div>
        <div class="field" style="margin-bottom:0"><label>${esc(t("次ホップへのジャンプコマンド（空欄=標準。NW機器踏み台などコマンド形式が違う場合に指定）"))}</label>
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
      const first = row === bhost.querySelector("[data-bastion-row]");
      row.querySelector("[data-b-legacy]").style.display = sel.value === "telnet" || !first ? "none" : "";
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

  node.querySelector("#cancel").onclick = closeModal;
  node.querySelector("#save").onclick = async () => {
    const conn = node.querySelector("#f-conn").value;
    const out = {
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
