// PalaTerm frontend. Talks to the Go backend via window.go.main.App.* and
// receives live run progress through window.runtime.EventsOn.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

const App = () => window.go.main.App;
const rt = () => window.runtime;

const APP_VERSION = "1.5.11";
const APP_AUTHOR = "KJO";

// ---- small helpers ----
function h(html) { const tpl = document.createElement("template"); tpl.innerHTML = html.trim(); return tpl.content.firstChild; }
function esc(s) { return String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function $(sel) { return document.querySelector(sel); }

let toastTimer;
function toast(msg, kind) {
  const el = $("#toast");
  el.textContent = msg;
  el.className = "toast " + (kind || "");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.add("hidden"), 3200);
}

// Styled in-app confirm dialog (replaces the native "wails.localhost" confirm).
// message may contain simple markup; escape dynamic values with esc() upstream.
function uiConfirm({ title = t("確認"), message = "", okLabel = "OK", danger = false } = {}) {
  return new Promise(resolve => {
    const node = h(`<div>
      <h3>${esc(title)}</h3>
      <p style="font-size:14px;line-height:1.8;margin:4px 0 10px">${message}</p>
      <div class="modal-actions">
        <button class="btn" id="uc-cancel">${esc(t("キャンセル"))}</button>
        <button class="btn ${danger ? "danger" : "primary"}" id="uc-ok" autofocus>${esc(okLabel)}</button>
      </div>
    </div>`);
    openModal(node, "mid");
    node.querySelector("#uc-cancel").onclick = () => { closeModal(); resolve(false); };
    node.querySelector("#uc-ok").onclick = () => { closeModal(); resolve(true); };
  });
}

function openModal(node, sizeClass) {
  const m = $("#modal");
  m.className = "modal" + (sizeClass ? " " + sizeClass : "");
  m.innerHTML = ""; m.appendChild(node);
  $("#modal-overlay").classList.remove("hidden");
}
function closeModal() { $("#modal-overlay").classList.add("hidden"); }

// Excel-like bulk toggling for enable-checkboxes: a plain click applies one
// row; Shift+クリック applies the clicked state to the whole range since the
// previously clicked row. boxes must be in visual order with data-enable set.
// The anchor row is remembered per table key because every toggle re-renders
// the table (and would otherwise forget where the range started).
const _rangeAnchor = {};
function wireShiftRange(key, boxes, apply) {
  boxes.forEach((cb, i) => {
    cb.addEventListener("click", async e => {
      const last = _rangeAnchor[key] ?? -1;
      let names;
      if (e.shiftKey && last >= 0 && last !== i && last < boxes.length) {
        const [a, b] = [Math.min(last, i), Math.max(last, i)];
        names = [];
        for (let j = a; j <= b; j++) names.push(boxes[j].dataset.enable);
      } else {
        names = [cb.dataset.enable];
      }
      _rangeAnchor[key] = i;
      await apply(names, cb.checked);
    });
    // Shift+クリックの巻き添えでテキスト選択が走るのを防ぐ。
    cb.addEventListener("mousedown", e => { if (e.shiftKey) e.preventDefault(); });
  });
}

// ---- Excel-like sort / filter for the list tabs ----
// One state per list (devices, groups, command sets, OS types, history),
// kept across re-renders. Display only: the stored order — the one drag-
// reorder edits and the backend runs in — is never touched. While a sort or
// a filter is on, the rows no longer show the stored order, so the drag
// handles are switched off until 「並び順を戻す」 / 「絞り込み解除」.
const listViews = {};
function listView(key) {
  return listViews[key] || (listViews[key] = { sort: { key: "", dir: 1 }, text: "", sel: {} });
}
function listViewActive(key) {
  const v = listView(key);
  return !!(v.sort.key || v.text.trim() || Object.values(v.sel).some(Boolean));
}
// items → the rows to show. searchOf(item) lists the strings the text box
// matches against; sortVal(item, sortKey) gives the value a column sorts
// by; selOf maps each select's id to item → value for exact matching.
function listViewApply(key, items, searchOf, sortVal, selOf) {
  const v = listView(key);
  const q = v.text.trim().toLowerCase();
  let out = items.filter(it => {
    if (q && !searchOf(it).some(s => String(s ?? "").toLowerCase().includes(q))) return false;
    for (const id of Object.keys(v.sel)) {
      if (v.sel[id] && selOf && selOf[id] && String(selOf[id](it) ?? "") !== v.sel[id]) return false;
    }
    return true;
  });
  if (v.sort.key) {
    const val = it => { const x = sortVal(it, v.sort.key); return typeof x === "number" ? x : String(x ?? "").toLowerCase(); };
    out = out.slice().sort((a, b) => { const x = val(a), y = val(b); return (x < y ? -1 : x > y ? 1 : 0) * v.sort.dir; });
  }
  return out;
}
// A sortable column header (click = sort, click again = reverse).
function listViewTh(key, col, label, style) {
  const v = listView(key);
  const arrow = v.sort.key === col ? (v.sort.dir > 0 ? " ▲" : " ▼") : "";
  return `<th ${style ? `style="${style}"` : ""} class="sortable" data-lv="${esc(key)}" data-sort="${esc(col)}" data-tip="${esc(t("クリックで並び替え"))}">${esc(label)}${arrow}</th>`;
}
// The filter row: a text box, optional selects ({id, label, values}), and
// the clear buttons that appear while something is on.
function listViewBar(key, placeholder, selects) {
  const v = listView(key);
  const sels = (selects || []).map(s => `<select class="btn lv-sel" data-lv="${esc(key)}" data-sel="${esc(s.id)}" style="padding-right:8px">
      <option value="">${esc(s.label)}</option>${s.values.map(x => `<option value="${esc(x)}" ${v.sel[s.id] === x ? "selected" : ""}>${esc(x)}</option>`).join("")}</select>`).join("");
  const filtered = !!(v.text.trim() || Object.values(v.sel).some(Boolean));
  return `<div class="row-inline lv-bar" style="gap:8px;flex-wrap:wrap;margin-bottom:12px">
      <input id="lv-text-${esc(key)}" class="btn" style="width:280px;text-align:left;cursor:text" placeholder="${esc(placeholder)}" value="${esc(v.text)}">
      ${sels}
      ${filtered ? `<button class="btn sm lv-clear" data-lv="${esc(key)}">${esc(t("✕ 絞り込み解除"))}</button>` : ""}
      ${v.sort.key ? `<button class="btn sm lv-sort-clear" data-lv="${esc(key)}">${esc(t("並び順を戻す"))}</button>` : ""}
    </div>`;
}
// The ⠿ cell of a drag-reorderable row: live, or switched off while the
// list is sorted or filtered.
function listViewDragCell(key) {
  if (listViewActive(key)) {
    return `<td class="drag-handle off" data-tip="${esc(t("並び替え・絞り込み中はドラッグで順序を変えられません（「並び順を戻す」「絞り込み解除」で戻ります）"))}">⠿</td>`;
  }
  return `<td class="drag-handle" data-tip="${dragHandleTip()}">⠿</td>`;
}
// Call before rebuilding the tab's HTML, and pass the result to listViewWire:
// the text box re-renders the list on every keystroke, and this puts the
// caret back where it was.
function listViewFocus() {
  const el = document.activeElement;
  return el && el.id && el.id.startsWith("lv-text-") ? { id: el.id, pos: el.selectionStart } : null;
}
function listViewWire(key, root, rerender, focus) {
  const v = listView(key);
  const txt = root.querySelector(`#lv-text-${CSS.escape(key)}`);
  if (txt) {
    txt.addEventListener("input", () => { v.text = txt.value; rerender(); });
    if (focus && focus.id === txt.id) { txt.focus(); try { txt.setSelectionRange(focus.pos, focus.pos); } catch (e) { } }
  }
  root.querySelectorAll(`.lv-sel[data-lv="${CSS.escape(key)}"]`).forEach(s => s.onchange = () => { v.sel[s.dataset.sel] = s.value; rerender(); });
  root.querySelectorAll(`.lv-clear[data-lv="${CSS.escape(key)}"]`).forEach(b => b.onclick = () => { v.text = ""; v.sel = {}; rerender(); });
  root.querySelectorAll(`.lv-sort-clear[data-lv="${CSS.escape(key)}"]`).forEach(b => b.onclick = () => { v.sort = { key: "", dir: 1 }; rerender(); });
  root.querySelectorAll(`th.sortable[data-lv="${CSS.escape(key)}"]`).forEach(el => el.onclick = () => {
    const k = el.dataset.sort;
    v.sort = v.sort.key === k ? { key: k, dir: -v.sort.dir } : { key: k, dir: 1 };
    rerender();
  });
}

// ---- Excel-like column resizing ----
// A table marked data-colw="<key>" gets a drag handle on the right edge of
// every header cell but the last: drag to resize that column, double-click
// to give it its default width back. Widths are remembered per table in
// localStorage (a per-PC convenience, like the font size), and put back by
// wireColResize on every render. Tables must use table-layout: fixed for a
// header width to hold, which these all do.
function colWidthsKey(key) { return "palaterm_colw_" + key; }
function loadColWidths(key) {
  try { return JSON.parse(localStorage.getItem(colWidthsKey(key)) || "{}") || {}; } catch (e) { return {}; }
}
function saveColWidths(key, w) {
  try { localStorage.setItem(colWidthsKey(key), JSON.stringify(w)); } catch (e) { }
}
// fitPanels caps every scrolling list panel at the window's bottom edge, so
// the panel (not the page) scrolls: the title, toolbar and filter row above
// it stay where they are, and the sticky header row stays in view. Called
// after each render (from wireColResize) and on window resize.
function fitPanels(root) {
  (root || document).querySelectorAll(".panel.scrollx").forEach(p => {
    const top = p.getBoundingClientRect().top;
    const h = Math.max(160, Math.floor(window.innerHeight - top - 28));
    p.style.maxHeight = h + "px";
  });
}
window.addEventListener("resize", () => fitPanels(document));

function wireColResize(root) {
  fitPanels(root);
  root.querySelectorAll("table[data-colw]").forEach(table => {
    const key = table.dataset.colw;
    const ths = [...table.querySelectorAll("thead th")];
    const saved = loadColWidths(key);
    ths.forEach((th, i) => {
      // The template's own width is the default a double-click returns to.
      th.dataset.defw = th.style.width || "";
      if (saved[i]) th.style.width = saved[i] + "px";
      if (i === ths.length - 1) return;
      const grip = document.createElement("div");
      grip.className = "col-resizer";
      grip.dataset.tip = t("ドラッグで列幅を変更（ダブルクリックで既定に戻す）");
      th.appendChild(grip);
      grip.addEventListener("click", e => e.stopPropagation());
      grip.addEventListener("dblclick", e => {
        e.stopPropagation();
        delete saved[i]; saveColWidths(key, saved);
        th.style.width = th.dataset.defw || "";
      });
      grip.addEventListener("pointerdown", e => {
        e.preventDefault(); e.stopPropagation();
        const x0 = e.clientX, w0 = th.getBoundingClientRect().width;
        grip.setPointerCapture(e.pointerId);
        table.classList.add("resizing");
        const move = ev => { th.style.width = Math.max(40, Math.round(w0 + ev.clientX - x0)) + "px"; };
        const up = () => {
          grip.removeEventListener("pointermove", move);
          grip.removeEventListener("pointerup", up);
          table.classList.remove("resizing");
          saved[i] = Math.round(th.getBoundingClientRect().width);
          saveColWidths(key, saved);
        };
        grip.addEventListener("pointermove", move);
        grip.addEventListener("pointerup", up);
      });
    });
  });
}

// ---- drag-reorder for table rows (multi-select capable) ----
// Rows need a data-key attribute and a .drag-handle cell. Selection happens
// on the handle: click = select that row, Ctrl+クリック = add/remove,
// Shift+クリック = range. Dragging any selected row moves the whole selection
// as one block (relative order kept). When the drag ends, onReorder receives
// every row key in the new DOM order.
function wireRowReorder(tbody, onReorder) {
  if (!tbody) return;
  const rows = () => [...tbody.querySelectorAll("tr[data-key]")];
  let lastIdx = -1;
  const clearSel = () => rows().forEach(r => r.classList.remove("row-sel"));
  const selected = () => rows().filter(r => r.classList.contains("row-sel"));

  rows().forEach((tr, i) => {
    const handle = tr.querySelector(".drag-handle");
    if (!handle) return;
    handle.addEventListener("click", e => {
      const all = rows();
      if (e.shiftKey && lastIdx >= 0 && lastIdx < all.length) {
        const [a, b] = [Math.min(lastIdx, i), Math.max(lastIdx, i)];
        if (!e.ctrlKey) clearSel();
        for (let j = a; j <= b; j++) all[j].classList.add("row-sel");
      } else if (e.ctrlKey) {
        tr.classList.toggle("row-sel");
        lastIdx = i;
      } else {
        const wasSole = tr.classList.contains("row-sel") && selected().length === 1;
        clearSel();
        if (!wasSole) tr.classList.add("row-sel");
        lastIdx = i;
      }
      e.preventDefault();
    });
    handle.addEventListener("mousedown", e => { if (e.shiftKey) e.preventDefault(); });
    // The row becomes draggable only while the pointer is on its handle, so
    // dragging from an inline-edit input never moves the row.
    handle.addEventListener("pointerdown", () => {
      tr.draggable = true;
      document.addEventListener("pointerup", () => { tr.draggable = false; }, { once: true });
    });
    tr.addEventListener("dragstart", e => {
      if (!tr.classList.contains("row-sel")) { clearSel(); tr.classList.add("row-sel"); }
      selected().forEach(r => r.classList.add("dragging"));
      // Some WebViews need data set for the drag to start at all.
      if (e.dataTransfer) { e.dataTransfer.setData("text/plain", tr.dataset.key); e.dataTransfer.effectAllowed = "move"; }
    });
    tr.addEventListener("dragend", async () => {
      rows().forEach(r => { r.classList.remove("dragging"); r.draggable = false; });
      await onReorder(rows().map(r => r.dataset.key));
    });
    tr.addEventListener("dragover", e => {
      e.preventDefault();
      const block = rows().filter(r => r.classList.contains("dragging"));
      if (!block.length || block.includes(tr)) return;
      const box = tr.getBoundingClientRect();
      const before = (e.clientY - box.top) < box.height / 2;
      const ref = before ? tr : tr.nextSibling;
      block.forEach(r => tbody.insertBefore(r, ref));
    });
  });
}

// The ⓘ tooltip every drag handle carries.
function dragHandleTip() {
  return esc(t("ドラッグで並び替え。クリック=選択 / Ctrl+クリック=追加選択 / Shift+クリック=範囲選択 — 選択した複数行はまとめてドラッグで移動できます"));
}

// The 接続 button's tooltip, and its two ways of opening the terminal: a
// plain click logs in automatically; Shift+click or a right-click opens the
// line with no automatic login at all (initial setup over a serial console,
// a device with no password yet, a screen no OS profile anticipates).
const CONNECT_TIP = t("自動でログインした端末を開きます。Shift+クリック（または右クリック）で自動ログインなしの端末（シリアルでの初期設定・パスワード未設定の機器・想定外の画面で止まる機器に）");
function wireConnectButtons(root) {
  const open = async (name, manual) => {
    try {
      await App().SpawnTerminal(name, manual);
      toast(manual ? t("自動ログインなしの端末を開きました") : t("対話接続ウィンドウを開きました"), "ok");
    } catch (e) { toast(t("接続失敗") + ": " + terr(e), "err"); }
  };
  root.querySelectorAll("[data-term]").forEach(b => {
    b.onclick = e => open(b.dataset.term, !!e.shiftKey);
    b.oncontextmenu = e => { e.preventDefault(); open(b.dataset.term, true); };
  });
}

// Click-to-copy for .ph-table variable cells inside scope.
function wireCopyCells(scope) {
  scope.querySelectorAll(".ph-table td.mono").forEach(td => td.onclick = async () => {
    const v = td.textContent.trim();
    try {
      await navigator.clipboard.writeText(v);
      toast(t("{v} をコピーしました", { v }), "ok");
    } catch (e) {
      // Fallback: Wails runtime clipboard (navigator.clipboard needs a secure context).
      try { await rt().ClipboardSetText(v); toast(t("{v} をコピーしました", { v }), "ok"); }
      catch (e2) { toast(t("コピーできませんでした"), "err"); }
    }
  });
}

// ---- instant styled tooltip (data-tip) ----
const tipEl = document.createElement("div");
tipEl.className = "tip hidden";
document.body.appendChild(tipEl);
// While a drag is in progress no mouseout ever arrives for the handle the
// drag started on, so the handle's tooltip would sit over the rows being
// reordered; hide it at dragstart and keep it hidden until the drop.
let dragInProgress = false;
document.addEventListener("dragstart", () => { dragInProgress = true; tipEl.classList.add("hidden"); }, true);
document.addEventListener("dragend", () => { dragInProgress = false; }, true);
document.addEventListener("mouseover", e => {
  if (dragInProgress) return;
  const el = e.target.closest ? e.target.closest("[data-tip]") : null;
  const text = el && el.dataset.tip;
  if (!text) { tipEl.classList.add("hidden"); return; }
  tipEl.textContent = text;
  tipEl.style.left = "0px"; tipEl.style.top = "0px";
  tipEl.classList.remove("hidden");
  const r = el.getBoundingClientRect();
  const tw = tipEl.offsetWidth, th = tipEl.offsetHeight;
  // Above the hovered cell; falls back to below at the top of the window.
  let x = r.left, y = r.top - th - 8;
  if (x + tw > window.innerWidth - 12) x = Math.max(12, window.innerWidth - tw - 12);
  if (y < 12) y = r.bottom + 8;
  tipEl.style.left = x + "px"; tipEl.style.top = y + "px";
});
document.addEventListener("mouseout", e => {
  if (e.target.closest && e.target.closest("[data-tip]")) tipEl.classList.add("hidden");
});
document.addEventListener("scroll", () => tipEl.classList.add("hidden"), true);

// Add a show/hide (eye) toggle to every password input inside scope.
function wirePasswordToggles(scope) {
  scope.querySelectorAll('input[type="password"]').forEach(inp => {
    if (inp.dataset.eyed) return;
    inp.dataset.eyed = "1";
    inp.style.paddingRight = "40px";
    const parent = inp.parentElement;
    parent.style.position = "relative";
    const btn = document.createElement("button");
    btn.type = "button"; btn.className = "eye-btn"; btn.textContent = "👁";
    btn.title = t("表示 / 非表示");
    btn.setAttribute("aria-label", t("表示 / 非表示"));
    btn.onclick = () => {
      const showing = inp.type === "text";
      inp.type = showing ? "password" : "text";
      btn.classList.toggle("on", !showing);
      // Keep typing where it was: clicking the button steals focus otherwise.
      inp.focus();
    };
    parent.appendChild(btn);
  });
}
// Backdrop clicks do NOT close the modal, so half-entered forms aren't lost.
// Close only via the modal's own キャンセル / 閉じる buttons.

// ---- state ----
let INV = null;       // decrypted inventory cache
let PROFILES = [];    // [{key,name}]
let SERIAL = [];      // COM ports
const runState = {};  // name -> {phase, message, done, total, logPath}

// ---- boot ----
async function boot() {
  applyStaticI18n();
  // Standalone terminal window? (child process binds Term, not App)
  if (window.go && window.go.main && window.go.main.Term) {
    bootTerminalWindow();
    return;
  }
  // Diff window (binds DiffWin).
  if (window.go && window.go.main && window.go.main.DiffWin) {
    bootDiffWindow();
    return;
  }
  // Log-viewer window (binds Viewer).
  if (window.go && window.go.main && window.go.main.Viewer) {
    bootViewerWindow();
    return;
  }
  // Paste-confirmation window (child of a terminal window; binds Paste).
  if (window.go && window.go.main && window.go.main.Paste) {
    bootPasteWindow();
    return;
  }
  const exists = await App().VaultExists();
  renderLock(exists);
}

// Translate the static sidebar texts authored in Japanese in index.html.
function applyStaticI18n() {
  document.querySelectorAll(".sidebar .nav-btn").forEach(b => b.textContent = t(b.textContent.trim()));
}

function renderLock(exists) {
  const body = $("#lock-body");
  if (exists) {
    body.innerHTML = `
      <div class="field"><label>${esc(t("マスターパスワード"))}</label>
        <input id="pw" type="password" autofocus /></div>
      <button class="btn primary" id="unlock" style="width:100%">${esc(t("ロック解除"))}</button>
      <div class="muted" style="margin-top:12px;font-size:12px">${esc(t("暗号化された機器情報を復号します"))}</div>`;
    $("#unlock").onclick = doUnlock;
    $("#pw").addEventListener("keydown", e => { if (e.key === "Enter") doUnlock(); });
    wirePasswordToggles(body);
  } else {
    body.innerHTML = `
      <div class="muted" style="margin-bottom:16px;font-size:13px">
        ${esc(t("はじめての起動です。機器情報を暗号化して保存するためのマスターパスワードを設定してください。"))}</div>
      <div class="field"><label>${esc(t("マスターパスワード"))}</label><input id="pw" type="password" /></div>
      <div class="field"><label>${esc(t("確認のため再入力"))}</label><input id="pw2" type="password" /></div>
      <button class="btn primary" id="create" style="width:100%">${esc(t("作成して開始"))}</button>`;
    $("#create").onclick = doCreate;
    wirePasswordToggles(body);
  }
}

async function doUnlock() {
  const pw = $("#pw").value;
  if (!pw) return;
  try {
    await App().Unlock(pw);
    await enterApp();
  } catch (e) { toast(t("パスワードが違うか、ファイルが壊れています"), "err"); }
}

async function doCreate() {
  const pw = $("#pw").value, pw2 = $("#pw2").value;
  if (!pw) { toast(t("パスワードを入力してください"), "err"); return; }
  if (pw !== pw2) { toast(t("確認用パスワードが一致しません"), "err"); return; }
  try { await App().CreateVault(pw); await enterApp(); }
  catch (e) { toast(t("作成に失敗しました") + ": " + terr(e), "err"); }
}

async function enterApp() {
  $("#lock").classList.add("hidden");
  $("#app").classList.remove("hidden");
  PROFILES = await App().ListProfiles();
  SERIAL = await App().ListSerialPorts() || [];
  await refreshInventory();
  switchTab("devices");
}

async function refreshInventory() {
  INV = await App().GetInventory();
  if (!INV.devices) INV.devices = [];
  if (!INV.commandSets) INV.commandSets = [];
  const active = document.querySelector(".tab.active");
  if (active) renderTab(active.id.replace("tab-", ""));
}

// ---- navigation ----
document.querySelectorAll(".nav-btn[data-tab]").forEach(b =>
  b.onclick = () => switchTab(b.dataset.tab));
$("#btn-reset").onclick = resetVaultFlow;
$("#btn-lock").onclick = async () => {
  // Unsaved settings edits would be lost by the reload — warn first.
  if (typeof canLeaveSettings === "function" && !(await canLeaveSettings("lock"))) return;
  await App().Lock(); location.reload();
};

// ---- change master password ----
$("#btn-chpw").onclick = () => {
  const node = h(`<div>
    <h3>${esc(t("マスターパスワード変更"))}</h3>
    <div class="field"><label>${esc(t("現在のマスターパスワード"))}</label><input id="cp-cur" type="password" autofocus></div>
    <div class="field"><label>${esc(t("新しいマスターパスワード"))}</label><input id="cp-new" type="password"></div>
    <div class="field"><label>${esc(t("確認のため再入力"))}</label><input id="cp-new2" type="password"></div>
    <div class="modal-actions">
      <button class="btn" id="cp-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="cp-ok">${esc(t("変更"))}</button>
    </div>
  </div>`);
  openModal(node, "mid");
  wirePasswordToggles(node);
  node.querySelector("#cp-cancel").onclick = closeModal;
  node.querySelector("#cp-ok").onclick = async () => {
    const cur = node.querySelector("#cp-cur").value;
    const nw = node.querySelector("#cp-new").value;
    const nw2 = node.querySelector("#cp-new2").value;
    if (!nw) { toast(t("新しいパスワードを入力してください"), "err"); return; }
    if (nw !== nw2) { toast(t("確認用パスワードが一致しません"), "err"); return; }
    try {
      await App().ChangeMasterPassword(cur, nw);
      closeModal();
      toast(t("マスターパスワードを変更しました"), "ok");
    } catch (e) { toast(terr(e), "err"); }
  };
};

// ---- reset (delete the vault: every device / set / profile / password) ----
// Lives at the bottom of the ログ設定 tab (危険な操作), deliberately away from
// everyday buttons. Requires the master password on top of two confirmations.
async function resetVaultFlow() {
  const ok = await uiConfirm({
    title: t("すべてのデータをリセット"),
    message: t("リセットすると、登録済みの<b>すべての機器・グループ・コマンドセット・OSタイププロファイル、およびマスターパスワード</b>が完全に削除されます。<br>この操作は<b>元に戻せません</b>。<br><span class=\"muted\" style=\"font-size:12px\">ログファイルと export フォルダ内の書き出しファイルは削除されず残ります</span>"),
    okLabel: t("リセットする"),
    danger: true,
  });
  if (!ok) return;
  // A second, plainer question before the password: the first dialog is
  // easy to click through while reading the list of what gets deleted.
  const sure = await uiConfirm({
    title: t("本当によろしいですか？"),
    message: t("すべてのデータが削除され、<b>元に戻せません</b>。本当に初期化しますか？"),
    okLabel: t("はい、初期化する"),
    danger: true,
  });
  if (!sure) return;
  const node = h(`<div>
    <h3>${esc(t("最終確認"))}</h3>
    <p style="font-size:14px;line-height:1.8;margin:4px 0 10px">${t("本当にすべてのデータを削除して初期化しますか？")}<br>
      ${esc(t("続行するにはマスターパスワードを入力してください"))}</p>
    <div class="field"><label>${esc(t("マスターパスワード"))}</label><input id="rs-pw" type="password" autofocus></div>
    <div class="modal-actions">
      <button class="btn" id="rs-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn danger" id="rs-ok">${esc(t("削除して初期化"))}</button>
    </div>
  </div>`);
  openModal(node, "mid");
  wirePasswordToggles(node);
  node.querySelector("#rs-cancel").onclick = closeModal;
  node.querySelector("#rs-ok").onclick = async () => {
    const pw = node.querySelector("#rs-pw").value;
    if (!pw) { toast(t("パスワードを入力してください"), "err"); return; }
    try { await App().ResetVault(pw); location.reload(); }
    catch (e) { toast(t("リセット失敗") + ": " + terr(e), "err"); }
  };
}

// ---- about dialog ----
$("#btn-about").onclick = () => {
  const node = h(`<div class="about-box">
    <div class="brand"><span class="logo">Pala</span>Term</div>
    <div class="brand-sub" style="margin-bottom:0">Parallel SSH / Telnet / Serial Access</div>
    <div class="about-rows">
      ${esc(t("バージョン"))} ${esc(APP_VERSION)}<br>
      ${esc(t("作者"))}: ${esc(APP_AUTHOR)}<br>
      MIT License &copy; 2026 ${esc(APP_AUTHOR)}
    </div>
    <div class="modal-actions" style="justify-content:center"><button class="btn" id="ab-close">${esc(t("閉じる"))}</button></div>
  </div>`);
  openModal(node);
  node.querySelector("#ab-close").onclick = closeModal;
};

// ---- language selector (persisted; the UI re-renders via reload) ----
{
  const sel = $("#lang-select");
  sel.value = LANG === "ja" ? "ja" : "en";
  sel.onchange = () => {
    try { localStorage.setItem("palaterm_lang", sel.value); } catch { }
    location.reload();
  };
}

async function switchTab(name) {
  // Leaving the settings tab with unsaved edits asks for confirmation.
  if (typeof canLeaveSettings === "function" && !(await canLeaveSettings(name))) return;
  // The devices tab keeps its state (chosen group / list position) across tab
  // switches; 「← 対象選択」 is the way back to the chooser.
  document.querySelectorAll(".nav-btn[data-tab]").forEach(b =>
    b.classList.toggle("active", b.dataset.tab === name));
  document.querySelectorAll(".tab").forEach(el =>
    el.classList.toggle("active", el.id === "tab-" + name));
  renderTab(name);
}

function renderTab(name) {
  if (name === "devices") renderDevices();
  else if (name === "commands") renderCommands();
  else if (name === "run") renderRun();
  else if (name === "history") renderHistory();
  else if (name === "settings") renderSettings();
  else if (name === "ostypes") renderOSTypes();
}

// ---- idle auto-lock ----
// The vault locks itself after Settings.autoLockMin minutes without keyboard
// or mouse input (default 30, 0 = off), the way a screen saver would — never
// in the middle of a batch, whose progress table would be lost with the
// reload. The interactive windows are separate processes and are not
// affected. Runs only in the main window (INV is null in a terminal window).
let lastActivityMs = Date.now();
["mousemove", "mousedown", "keydown", "wheel", "touchstart"].forEach(ev =>
  document.addEventListener(ev, () => { lastActivityMs = Date.now(); }, { passive: true }));
setInterval(async () => {
  if (!INV || !INV.settings || $("#app").classList.contains("hidden")) return;
  const s = INV.settings;
  const min = s.autoLockOff ? 0 : (s.autoLockMin > 0 ? s.autoLockMin : 30);
  if (!min || (typeof running !== "undefined" && running)) return;
  if (Date.now() - lastActivityMs < min * 60000) return;
  try { await App().Lock(); } catch (e) { }
  location.reload();
}, 15000);

function profileName(key) {
  const p = PROFILES.find(p => p.key === key);
  return p ? p.name : key;
}

// Boot once every script has run. app.js is loaded before views*.js and
// terminal.js, and boot() calls straight into them: the main window happened
// to survive because its first call into views.js sits behind an await, but
// the interactive window's bootTerminalWindow() (terminal.js) was reached
// synchronously and did not exist yet — every 対話接続 window opened as a bare
// lock card with nothing on it.
if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
else boot();
