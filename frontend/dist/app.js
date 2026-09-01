// PalaTerm frontend. Talks to the Go backend via window.go.main.App.* and
// receives live run progress through window.runtime.EventsOn.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

const App = () => window.go.main.App;
const rt = () => window.runtime;

const APP_VERSION = "1.0";
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
document.addEventListener("mouseover", e => {
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
  else if (name === "settings") renderSettings();
  else if (name === "ostypes") renderOSTypes();
}

function profileName(key) {
  const p = PROFILES.find(p => p.key === key);
  return p ? p.name : key;
}

boot();
