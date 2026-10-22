// Device table editor (機器一覧 → 表で編集). Loaded after views.js.
//
// The scope's devices as a spreadsheet: one row per device, one column per
// CSV column (internal/csvio GridColumns), edited the way Excel is — select,
// type, Ctrl+C / Ctrl+V to and from Excel, Ctrl+D, fill handle, undo, find
// and replace. Nothing reaches the device list until 保存; until then the
// table is a draft the backend keeps in the vault (autosaved), so a lock, a
// restart or a row that will not save loses nothing. Cell values are the
// CSV's canonical English words (ssh, password, true, tunnel); the table
// shows them in the user's language and reads either back.

let GRID_COUNTS = {};   // scope -> rows its draft has not saved (badges)
let GRID_COLS = null;   // the backend's column list, fetched once
let G = null;           // the open table
const GRID_VIS_KEY = "palaterm_grid_cols";
const GRID_UNGROUPED = "__ungrouped__";
const GRID_UNDO_MAX = 100;

// ---- values ----
const GRID_BOOL_T = ["true", "1", "yes", "y", "on", "はい", "○", "◯", "有効"];
const GRID_BOOL_F = ["false", "0", "no", "n", "off", "いいえ", "×", "無効"];
// The choices are built once per inventory load: the table asks for them
// for every cell it draws, and a thousand rows made that the slow part.
let gridOptCache = { inv: null, prof: null, m: {}, lbl: {} };
function gridOptions(kind) {
  if (gridOptCache.inv !== INV || gridOptCache.prof !== PROFILES) gridOptCache = { inv: INV, prof: PROFILES, m: {}, lbl: {} };
  if (!(kind in gridOptCache.m)) gridOptCache.m[kind] = gridOptionsBuild(kind);
  return gridOptCache.m[kind];
}
function gridOptLabel(kind, v) {
  const opts = gridOptions(kind);
  if (!opts) return v;
  let m = gridOptCache.lbl[kind];
  if (!m) m = gridOptCache.lbl[kind] = new Map(opts.map(o => [o.v, o.label]));
  return m.has(v) ? m.get(v) : v;
}
function gridOptionsBuild(kind) {
  switch (kind) {
    case "bool": return [{ v: "true", label: t("はい"), alias: GRID_BOOL_T }, { v: "false", label: t("いいえ"), alias: GRID_BOOL_F }];
    case "conn": return [{ v: "ssh", label: "SSH" }, { v: "telnet", label: "Telnet" }, { v: "serial", label: t("シリアル"), alias: ["シリアル"] }];
    case "hopMethod": return [{ v: "ssh", label: "SSH" }, { v: "telnet", label: "Telnet" }];
    case "auth": return [{ v: "password", label: t("パスワード"), alias: ["パスワード"] }, { v: "publickey", label: t("公開鍵"), alias: ["公開鍵", "鍵", "public key"] }];
    case "mode": return [{ v: "shell", label: t("コマンド入力"), alias: ["コマンド入力"] }, { v: "tunnel", label: t("SSHトンネル"), alias: ["SSHトンネル", "SSH トンネル", "トンネル", "ssh tunnel"] }];
    case "os": return PROFILES.map(p => ({ v: p.key, label: p.name }));
    case "set": return [{ v: "", label: t("（なし）"), alias: ["（なし）"] }].concat((INV.commandSets || []).map(s => ({ v: s.name, label: s.name })));
    case "group": return (INV.deviceGroups || []).map(g => ({ v: g.name, label: g.name }));
    case "scheme": return [{ v: "https", label: "https" }, { v: "http", label: "http" }];
    case "tint": return [{ v: "", label: t("なし"), alias: ["なし", "none"] }].concat(deviceTintKeys().map(k => ({ v: k, label: t(DEVICE_TINTS[k].name), alias: [DEVICE_TINTS[k].name] })));
  }
  return null;
}
const GRID_FREE = { text: 1, secret: 1, int: 1, group: 1 };
// gridNorm turns what was typed or pasted into the cell's stored value.
function gridNorm(col, text) {
  text = String(text ?? "");
  if (col.kind !== "secret") text = text.replace(/\r?\n/g, " ");
  if (GRID_FREE[col.kind]) return col.kind === "secret" ? text : text.trim();
  const s = text.trim();
  if (s === "") return "";
  const low = s.toLowerCase();
  const opts = gridOptions(col.kind) || [];
  // The stored value first: two OS profiles may share a display name.
  for (const o of opts) if (o.v.toLowerCase() === low) return o.v;
  for (const o of opts) {
    if (o.label.toLowerCase() === low || (o.alias || []).some(a => a.toLowerCase() === low)) return o.v;
  }
  return s; // unknown: kept as typed, and the check names the cell
}
// gridLabel is how a stored value shows in the table (and copies out).
function gridLabel(col, v) {
  v = v || "";
  if (col.kind === "secret") return v ? (G && G.showPw ? v : "●●●●") : "";
  if (v === "" || GRID_FREE[col.kind]) return v;
  return gridOptLabel(col.kind, v);
}
function gridCopyText(col, v) {
  if (col.kind === "secret") return G.showPw ? (v || "") : "";
  return gridLabel(col, v);
}
function gridColTitle(c) {
  return c.hop ? t("踏み台{n} {f}", { n: c.hop, f: t(c.field) }) : t(c.ja);
}
const GRID_W = {
  name: 150, group: 110, site: 100, role: 90, host: 130, conn: 80, port: 78, serialPort: 110, baud: 96,
  osType: 150, commandSet: 150, enabled: 64, useGroupCreds: 100, username: 110, password: 100, enablePassword: 110,
  authMethod: 90, keyFile: 180, keyPassphrase: 110, legacyAlgos: 90, bastionMode: 120,
  Method: 80, Host: 130, Port: 78, Username: 100, Password: 100, AuthMethod: 90, KeyFile: 160, KeyPassphrase: 110, LegacyAlgos: 90, JumpCommand: 170,
};
const GRID_COLW_KEY = "palaterm_grid_colw";
let gridColWSaved = null;
function gridColW(c) {
  if (!gridColWSaved) { try { gridColWSaved = JSON.parse(localStorage.getItem(GRID_COLW_KEY) || "{}") || {}; } catch (e) { gridColWSaved = {}; } }
  return gridColWSaved[c.key] || GRID_W[c.hop ? c.key.replace(/^bastion\d/, "") : c.key] || 110;
}
function gridSetColW(key, w) {
  gridColW({ key });
  gridColWSaved[key] = Math.round(w);
  try { localStorage.setItem(GRID_COLW_KEY, JSON.stringify(gridColWSaved)); } catch (e) { }
}

function sameCells(a, b) {
  a = a || {}; b = b || {};
  for (const k in a) if ((a[k] || "") !== (b[k] || "")) return false;
  for (const k in b) if ((a[k] || "") !== (b[k] || "")) return false;
  return true;
}
let gridUidN = 0;
function gridUid() { return "n" + Date.now().toString(36) + (gridUidN++).toString(36); }
function gridScopeGroup() { return G.scope && G.scope !== GRID_UNGROUPED ? G.scope : ""; }
function gridDefaults() {
  if (G.defaults) return G.defaults;
  const os = (PROFILES.find(p => p.key === "cisco-ios") || PROFILES[0] || {}).key || "";
  return (G.defaults = Object.freeze({ group: gridScopeGroup(), conn: "ssh", enabled: "true", authMethod: "password", osType: os }));
}
// A new row starts empty, the way a spreadsheet's rows are; the defaults
// (the table's group, SSH, enabled …) go in when something is first typed
// or pasted into it (gridApplyDefaults). cells given here count as typed.
function gridNewRow(cells) {
  const r = { uid: gridUid(), id: "", base: null, cells: { ...(cells || {}) } };
  if (cells) { r.cells = { ...gridDefaults(), ...cells }; r.init = true; }
  return r;
}
function gridApplyDefaults() {
  const d = gridDefaults();
  G.rows.forEach(r => {
    if (r.id || r.init || !Object.values(r.cells).some(v => v)) return;
    for (const k in d) if (!r.cells[k]) r.cells[k] = d[k];
    r.init = true;
  });
}
// A new row nobody typed into (the empty rows under the table, an inserted
// row left empty, or one whose values were all cleared) is not saved.
function gridIsBlank(r) {
  if (r.id) return false;
  const d = gridDefaults();
  return Object.keys(r.cells).every(k => !r.cells[k] || (r.init && r.cells[k] === (d[k] || "")));
}
// The table always ends in GRID_PAD empty rows to type or paste into, as a
// sheet does; they are topped up after every change and never saved.
const GRID_PAD = 20;
function gridPad() {
  let n = 0;
  for (let i = G.rows.length - 1; i >= 0 && gridIsBlank(G.rows[i]); i--) n++;
  for (; n < GRID_PAD; n++) G.rows.push(gridNewRow());
}
function gridDirty(r) { return !gridIsBlank(r) && (!r.id || !sameCells(r.base, r.cells)); }
function gridChangeCount() { return G ? G.rows.filter(gridDirty).length + G.deleted.length : 0; }
function gridPayloadRows() { return G.rows.filter(r => !gridIsBlank(r)); }

// ---- badges ----
async function loadGridCounts() {
  try { GRID_COUNTS = (await App().GridDraftCounts()) || {}; } catch (e) { GRID_COUNTS = {}; }
}
function gridScopeOf(group) { return group === DEV_UNGROUPED ? GRID_UNGROUPED : (group || ""); }
function gridDraftBadge(scope, small) {
  const n = GRID_COUNTS[scope || ""];
  if (!n) return "";
  return `<span class="badge grid-draft" data-grid-open="${esc(scope || "")}" style="${small ? "font-size:10px;padding:2px 7px" : ""}" data-tip="${esc(t("機器一覧の「表で編集」で保存するまで、一括実行や接続は保存済みの内容で行います。クリックで表を開きます"))}">${esc(t("下書き {n} 行（未反映）", { n }))}</span>`;
}
function wireGridBadges(root) {
  root.querySelectorAll("[data-grid-open]").forEach(b => b.onclick = e => {
    e.stopPropagation();
    const scope = b.dataset.gridOpen;
    switchTab("devices");
    openGrid(scope);
  });
}

// ---- open / close ----
async function openGrid(scope) {
  // Another scope's table open (a draft badge clicked): its cell being typed
  // in and its pending draft are kept first.
  if (G) await gridBeforeLeave();
  if (!GRID_COLS) GRID_COLS = await App().GridColumns();
  const fresh = await App().GridRows(scope);
  const draft = await App().GetGridDraft(scope);
  let rows = fresh, deleted = [], restored = 0;
  if (draft) {
    // Rows the draft changed stay as the draft has them (a device changed
    // elsewhere meanwhile is asked about at 保存); unchanged rows are read
    // anew, and devices added elsewhere since join at the end.
    const byId = new Map(fresh.map(r => [r.id, r]));
    const seen = new Set();
    rows = [];
    for (const r of draft.rows || []) {
      if (!r.id) { rows.push(r); restored++; continue; }
      seen.add(r.id);
      if (!sameCells(r.base, r.cells)) { rows.push(r); restored++; }
      else if (byId.has(r.id)) rows.push(byId.get(r.id));
    }
    deleted = (draft.deleted || []).filter(d => { seen.add(d.id); return byId.has(d.id); });
    restored += deleted.length;
    fresh.forEach(r => { if (!seen.has(r.id)) rows.push(r); });
  }
  G = {
    scope, cols: GRID_COLS, rows, deleted,
    sel: { ar: 0, ac: 0, fr: 0, fc: 0 }, edit: null, undo: [], redo: [], showPw: false,
    errors: {}, conflicts: {}, sort: { key: "", dir: 1 }, filter: "", colFilter: {}, view: [], vc: [], clip: null,
    savedAt: draft && draft.saved ? new Date(draft.saved) : null, saveT: null, chkT: null, mounted: false,
  };
  G.vis = gridLoadVis();
  gridPad();
  deviceView = { mode: "grid", group: scope === GRID_UNGROUPED ? DEV_UNGROUPED : (scope || null) };
  renderGrid(true);
  if (restored) toast(t("下書きを復元しました（{n} 行）", { n: restored }), "ok");
  gridScheduleCheck(0);
}

function gridLoadVis() {
  let saved = null;
  try { saved = JSON.parse(localStorage.getItem(GRID_VIS_KEY) || "null"); } catch (e) { }
  if (Array.isArray(saved) && saved.length) return new Set(["name", ...saved]);
  // First time: the device's own columns, plus the first jump host's when
  // any device here has one.
  const vis = new Set(G.cols.filter(c => !c.hop).map(c => c.key));
  if (G.rows.some(r => r.cells.bastion1Host)) G.cols.filter(c => c.hop === 1).forEach(c => vis.add(c.key));
  return vis;
}
// gridOnScreen: the table is open and its tab showing. Keys and focus go to
// it only then (a hidden table took typing after a dialog closed elsewhere).
function gridOnScreen() {
  const tab = document.getElementById("tab-devices");
  return !!(G && G.mounted && document.getElementById("g-wrap") && tab && tab.classList.contains("active") && $("#modal-overlay").classList.contains("hidden"));
}
function gridSaveVis() { try { localStorage.setItem(GRID_VIS_KEY, JSON.stringify([...G.vis])); } catch (e) { } }

// gridBeforeLeave writes the draft now (before a lock or quit reloads the page).
async function gridBeforeLeave() {
  if (!G) return;
  if (G.edit) gridCommitEdit();
  if (G.saveT || gridChangeCount()) await gridSaveDraft();
}

function uiChoice({ title, message, buttons }) {
  return new Promise(resolve => {
    const node = h(`<div>
      <h3>${esc(title)}</h3>
      <div style="font-size:14px;line-height:1.8;margin:4px 0 10px">${message}</div>
      <div class="modal-actions" style="flex-wrap:wrap">${buttons.map(b => `<button class="btn ${b.cls || ""}" data-choice="${esc(b.id)}">${esc(b.label)}</button>`).join("")}</div>
    </div>`);
    openModal(node, "mid");
    node.querySelectorAll("[data-choice]").forEach(b => b.onclick = () => { closeModal(); resolve(b.dataset.choice); });
  });
}

async function gridClose() {
  if (G.busy) { toast(t("保存中です。終わるまでお待ちください"), "err"); return; }
  if (G.edit) gridCommitEdit();
  await gridSaveDraft();
  const n = gridChangeCount();
  if (n) {
    const c = await uiChoice({
      title: t("表の編集を終える"),
      message: esc(t("保存していない変更が {n} 行あります。下書きは残しておき、あとで「表で編集」から続けられます。", { n })),
      buttons: [{ id: "cancel", label: t("キャンセル") }, { id: "discard", label: t("下書きを破棄して閉じる"), cls: "danger" }, { id: "keep", label: t("下書きを残して閉じる"), cls: "primary" }],
    });
    if (c !== "keep" && c !== "discard") return;
    if (c === "discard") await App().DiscardGridDraft(G.scope);
  }
  const group = deviceView.group;
  G = null;
  deviceView = { mode: "list", group };
  await loadGridCounts();
  await refreshInventory();
}

// ---- render ----
function gridView() {
  G.vc = G.cols.filter(c => G.vis.has(c.key));
  let idx = G.rows.map((_, i) => i);
  if (G.hidden) idx = idx.filter(i => !G.hidden.has(G.rows[i].uid));
  if (G.sort.key) {
    // The order is fixed when sorting is chosen (gridResort), as the filter
    // is: a row edited afterwards stays where it is, so Enter / Tab go on in
    // the same row instead of following it to its new place. Rows filled in
    // since (the empty rows at the bottom, a paste past the end) follow the
    // sorted ones in table order, so they stay where they were typed; the
    // empty rows stay at the bottom. Inserting rows in the middle waits until
    // the sort is kept or cleared (gridNotSorted).
    if (!G.sortRank) gridResort();
    const big = G.sortRank.size;
    const rank = i => { const x = G.sortRank.get(G.rows[i].uid); return x !== undefined ? x : big + i; };
    idx.sort((a, b) => (gridIsBlank(G.rows[a]) - gridIsBlank(G.rows[b])) || rank(a) - rank(b));
  }
  G.view = idx;
}

// gridRefilter applies the filters (the text box and the headers' value
// lists) once, as Excel does: the rows that do not match are hidden, and a
// row edited or added afterwards stays in sight until the filter changes —
// a row being typed into must not vanish.
function gridResort() {
  G.sortRank = null;
  if (!G.sort.key) return;
  const col = G.cols.find(c => c.key === G.sort.key);
  const order = G.rows.map((r, i) => i).filter(i => !gridIsBlank(G.rows[i]));
  order.sort((a, b) => G.sort.dir * gridLabel(col, G.rows[a].cells[col.key]).localeCompare(gridLabel(col, G.rows[b].cells[col.key]), undefined, { numeric: true }) || a - b);
  G.sortRank = new Map(order.map((i, n) => [G.rows[i].uid, n]));
}
function gridRefilter() {
  const f = (G.filter || "").toLowerCase();
  const cols = Object.entries(G.colFilter).map(([k, set]) => [G.cols.find(c => c.key === k), set]).filter(([c]) => c);
  if (!f && !cols.length) { G.hidden = null; return; }
  G.vc = G.cols.filter(c => G.vis.has(c.key));
  G.hidden = new Set(G.rows.filter(r => !gridIsBlank(r) && (
    (f && !G.vc.some(c => gridLabel(c, r.cells[c.key]).toLowerCase().includes(f))) ||
    cols.some(([c, set]) => !set.has(gridLabel(c, r.cells[c.key])))
  )).map(r => r.uid));
}

function renderGrid(force) {
  const root = document.getElementById("tab-devices");
  if (!G) {
    root.innerHTML = `<div class="empty">${esc(t("読み込み中…"))}</div>`;
    openGrid(gridScopeOf(deviceView.group)).catch(e => toast(terr(e), "err"));
    return;
  }
  // Redrawn only when asked: a refresh from elsewhere (an inventory reload)
  // must not throw away a cell being typed into.
  if (!force && G.mounted && document.getElementById("g-wrap")) return;
  gridView();
  const scopeLabel = G.scope === GRID_UNGROUPED ? t("（グループ未設定）") : G.scope ? t("グループ「{g}」", { g: G.scope }) : t("全機器");
  root.innerHTML = `
    <div class="page-head">
      <div class="row-inline" style="gap:12px">
        <button class="btn g-close" id="g-close">${esc(t("← 表の編集を終える"))}</button>
        <div><div class="page-title">${esc(t("表で編集"))} <span class="muted" style="font-size:14px;font-weight:400">/ ${esc(scopeLabel)}</span></div>
          <div class="page-sub" id="g-status"></div></div>
      </div>
      <div class="row-inline" style="flex-wrap:wrap;gap:6px">
        <button class="btn sm" id="g-undo" data-tip="Ctrl+Z">${esc(t("元に戻す"))}</button>
        <button class="btn sm" id="g-redo" data-tip="Ctrl+Y">${esc(t("やり直す"))}</button>
        <button class="btn sm" id="g-find" data-tip="${esc(t("Ctrl+F で検索、Ctrl+H で置換"))}">${esc(t("検索・置換"))}</button>
        <button class="btn sm" id="g-cols">${esc(t("列の表示…"))}</button>
        <button class="btn sm" id="g-pw">${esc(G.showPw ? t("パスワードを隠す") : t("パスワードを表示"))}</button>
        <button class="btn sm danger" id="g-discard">${esc(t("下書きを破棄"))}</button>
        <button class="btn primary" id="g-save" data-tip="Ctrl+S">${esc(t("保存"))}</button>
        <button class="btn g-close" id="g-close2" data-tip="${esc(t("機器一覧に戻ります。保存していない変更は下書きとして残せます"))}">${esc(t("表の編集を終える"))}</button>
      </div>
    </div>
    <div class="row-inline g-tools" style="gap:6px;margin-bottom:6px;flex-wrap:wrap">
      <input id="g-filter" class="btn" style="width:220px;text-align:left;cursor:text" placeholder="${esc(t("絞り込み（表示中の列）"))}" value="${esc(G.filter)}">
      <span class="g-sep"></span>
      <button class="btn sm" id="g-ins-above" data-tip="Ctrl+Shift++">${esc(t("上に行を挿入"))}</button>
      <button class="btn sm" id="g-ins-below">${esc(t("下に行を挿入"))}</button>
      <button class="btn sm" id="g-copyrows" data-tip="${esc(t("選んだ行をまるごとコピーします（隠している列も含む）。「コピーした行を挿入」や Excel に貼れます"))}">${esc(t("行をコピー"))}</button>
      <button class="btn sm" id="g-pasterows" data-tip="${esc(t("コピーした行（Excel からの行も可）を、選んでいる行の上に挿入します。Shift+クリックで下に挿入"))}">${esc(t("コピーした行を挿入"))}</button>
      <button class="btn sm" id="g-duprows" data-tip="${esc(t("選んだ行のすぐ下に複製を作ります（機器名には _2 などが付きます）"))}">${esc(t("行を複製"))}</button>
      <button class="btn sm" id="g-up" data-tip="Alt+↑">${esc(t("↑ 上へ移動"))}</button>
      <button class="btn sm" id="g-down" data-tip="Alt+↓">${esc(t("↓ 下へ移動"))}</button>
      <button class="btn sm danger" id="g-delrows" data-tip="Ctrl+－">${esc(t("行を削除"))}</button>
      <button class="btn sm" id="g-addrow">${esc(t("＋ 末尾に行を追加"))}</button>
    </div>
    <div class="row-inline" style="gap:12px;margin-bottom:8px;flex-wrap:wrap">
      <span class="muted" style="font-size:12px">${esc(t("Ctrl+C / Ctrl+V で Excel と相互にコピー・貼り付け ／ Ctrl+D 上の値を下へ ／ 右下の ■ をドラッグで連番（ダブルクリックで最後の行まで） ／ 見出しの ▾ で並べ替え・絞り込み ／ 行番号の左の ＋ でその下に行を挿入"))}</span>
      <span class="muted" id="g-selinfo" style="font-size:12px;margin-left:auto"></span>
    </div>
    <div id="g-findbar" class="g-findbar hidden"></div>
    <div class="grid-wrap" id="g-wrap" tabindex="0">
      <table class="grid" id="g-table">
        <colgroup><col style="width:46px">${G.vc.map(c => `<col style="width:${gridColW(c)}px">`).join("")}</colgroup>
        <thead><tr><th class="g-corner g-rh" data-tip="${esc(t("すべて選択"))}"></th>${G.vc.map((c, ci) => `<th data-c="${ci}" class="${ci === 0 && c.key === "name" ? "g-stick" : ""}" data-tip="${esc(gridColTitle(c))}"><button class="g-hmenu${G.colFilter[c.key] || G.sort.key === c.key ? " on" : ""}" type="button" data-hmenu="${esc(c.key)}" tabindex="-1" data-tip="${esc(t("並べ替え・絞り込み"))}">${G.sort.key === c.key ? (G.sort.dir > 0 ? "▲" : "▼") : ""}${G.colFilter[c.key] ? "⏷" : "▾"}</button>${esc(gridColTitle(c))}<span class="g-rz" data-rz="${ci}" data-tip="${esc(t("ドラッグで列幅を変更（ダブルクリックで内容に合わせる）"))}"></span></th>`).join("")}</tr></thead>
        <tbody id="g-body"></tbody>
      </table>
      <div class="g-fill hidden" id="g-fill" data-tip="${esc(t("ドラッグで連番・コピー（Ctrl を押しながらで連番とコピーを入れ替え）。ダブルクリックで最後の行まで"))}"></div>
      <div class="g-clip hidden" id="g-clip"></div>
      <button class="g-dd hidden" id="g-dd" type="button" tabindex="-1" data-tip="${esc(t("一覧から選ぶ（ダブルクリック・Space・F4 でも開きます。複数のセルを選んでいればまとめて入ります）"))}">▼</button>
      <div class="g-list hidden" id="g-list" tabindex="-1"></div>
    </div>
    <datalist id="g-dl"></datalist>`;
  G.mounted = true;
  G.rendered = null;
  G.wrap = document.getElementById("g-wrap");
  gridWire(root);
  gridRenderBody();
  G.wrap.focus({ preventScroll: true });
}

// gridRenderBody draws the rows. With the same rows in the same order and
// the same columns as last time, only the rows whose HTML changed are
// replaced: an edit to one cell of a thousand-row table redraws one row.
function gridRenderBody() {
  const body = document.getElementById("g-body");
  if (!body) return;
  const vc = G.vc;
  const tipNew = t("新しい行（まだ保存していません）"), tipChg = t("変更した行（まだ保存していません）");
  const tipConf = t("他の画面で変更されています（保存のときにどちらを残すか選べます）"), tipIns = esc(t("この行の下に行を挿入")), markNew = t("新");
  const rowsHtml = G.view.map((ri, r) => {
    const row = G.rows[ri];
    const e = G.errors[row.uid] || {};
    const conf = G.conflicts[row.uid];
    const blank = gridIsBlank(row);
    const mark = blank ? "" : !row.id ? markNew : !sameCells(row.base, row.cells) ? "●" : "";
    const tip = conf ? tipConf : blank ? "" : !row.id ? tipNew : mark ? tipChg : "";
    let tr = `<tr class="${blank ? "g-blank" : !row.id ? "g-new" : ""}${conf ? " g-conf" : ""}"><th class="g-rh" data-r="${r}" ${tip ? `data-tip="${esc(tip)}"` : ""}><button class="g-ins" type="button" data-ins="${r}" tabindex="-1" data-tip="${tipIns}">＋</button>${mark ? `<span class="g-mark${!row.id ? " new" : ""}">${esc(mark)}</span>` : ""}${r + 1}</th>`;
    vc.forEach((c, ci) => {
      const v = row.cells[c.key] || "";
      const b = row.base ? (row.base[c.key] || "") : null;
      const cls = [];
      if (ci === 0 && c.key === "name") cls.push("g-stick");
      if (b === null ? v !== "" && !blank : v !== b) cls.push("chg");
      if (e[c.key]) cls.push("err");
      tr += `<td data-r="${r}" data-c="${ci}" class="${cls.join(" ")}"${e[c.key] ? ` data-tip="${esc(e[c.key])}"` : ""}>${esc(gridLabel(c, v))}</td>`;
    });
    return tr + "</tr>";
  });
  const sig = G.view.map(i => G.rows[i].uid).join(",") + "|" + vc.map(c => c.key).join(",");
  const prev = G.rendered;
  if (prev && prev.sig === sig && body.rows.length === rowsHtml.length && rowsHtml.length) {
    rowsHtml.forEach((hh, r) => { if (hh !== prev.rows[r]) body.rows[r].outerHTML = hh; });
  } else {
    body.innerHTML = rowsHtml.join("") || `<tr><td colspan="${vc.length + 1}" class="empty">${esc(G.filter ? t("絞り込みに一致する行がありません") : t("行がありません。「＋ 末尾に行を追加」か、Excel からの貼り付けで追加できます"))}</td></tr>`;
  }
  G.rendered = { sig, rows: rowsHtml };
  G.tds = G.view.length ? [...body.rows].map(tr => [...tr.cells].slice(1)) : [];
  G.painted = [];
  gridPaintSel();
  gridStatus();
}

function gridStatus() {
  const el = document.getElementById("g-status");
  if (!el) return;
  const added = G.rows.filter(r => !r.id && !gridIsBlank(r)).length;
  const changed = G.rows.filter(r => r.id && !sameCells(r.base, r.cells)).length;
  const errs = Object.keys(G.errors).length;
  const parts = [esc(t("全 {n} 行", { n: G.rows.filter(r => !gridIsBlank(r)).length }))];
  if (changed) parts.push(esc(t("変更 {n} 行", { n: changed })));
  if (added) parts.push(esc(t("追加 {n} 行", { n: added })));
  if (G.deleted.length) parts.push(esc(t("削除 {n} 行", { n: G.deleted.length })));
  if (errs) parts.push(`<span style="color:var(--err)">${esc(t("エラー {n} 行", { n: errs }))}</span>`);
  if (Object.keys(G.conflicts).length) parts.push(`<span style="color:var(--warn)">${esc(t("他の画面と競合 {n} 行", { n: Object.keys(G.conflicts).length }))}</span>`);
  if (G.sort.key || G.filter || Object.keys(G.colFilter).length) parts.push(esc(t("並べ替え・絞り込みは表示だけ（保存される順番は変わりません）")) + (G.sort.key ? ` <a href="#" class="link" id="g-sortfix">${esc(t("この並び順で確定"))}</a>` : "") + ` <a href="#" class="link" id="g-viewreset">${esc(t("解除"))}</a>`);
  if (G.busy) parts.push(`<b style="color:var(--accent)">${esc(t("保存中…"))}</b>`);
  if (gridChangeCount()) parts.push(esc(G.savedAt ? t("下書きを自動保存 {t}", { t: G.savedAt.toTimeString().slice(0, 8) }) : t("下書きは自動保存されます")));
  else parts.push(esc(t("未保存の変更はありません")));
  el.innerHTML = parts.join(" ・ ");
  const fix = document.getElementById("g-sortfix");
  if (fix) fix.onclick = e => { e.preventDefault(); gridSortFix(); };
  const vr = document.getElementById("g-viewreset");
  if (vr) vr.onclick = e => { e.preventDefault(); G.sort = { key: "", dir: 1 }; G.colFilter = {}; G.filter = ""; gridRefilter(); renderGrid(true); };
  const u = document.getElementById("g-undo"), rd = document.getElementById("g-redo");
  if (u) u.disabled = !G.undo.length;
  if (rd) rd.disabled = !G.redo.length;
}

// ---- selection ----
function gridRange() {
  const s = G.sel;
  return { r0: Math.min(s.ar, s.fr), r1: Math.max(s.ar, s.fr), c0: Math.min(s.ac, s.fc), c1: Math.max(s.ac, s.fc) };
}
function gridClampSel() {
  const R = Math.max(0, G.view.length - 1), C = Math.max(0, G.vc.length - 1);
  const s = G.sel;
  s.ar = Math.min(Math.max(0, s.ar), R); s.fr = Math.min(Math.max(0, s.fr), R);
  s.ac = Math.min(Math.max(0, s.ac), C); s.fc = Math.min(Math.max(0, s.fc), C);
}
function gridPaintSel(fillTo) {
  (G.painted || []).forEach(td => td.classList.remove("in", "cur", "fillp"));
  document.querySelectorAll("#g-table th.hl").forEach(th => th.classList.remove("hl"));
  G.painted = [];
  const fill = document.getElementById("g-fill");
  if (!G.view.length || !G.tds.length) { if (fill) fill.classList.add("hidden"); return; }
  gridClampSel();
  const g = gridRange();
  for (let r = g.r0; r <= g.r1; r++) for (let c = g.c0; c <= g.c1; c++) {
    const td = G.tds[r] && G.tds[r][c];
    if (td) { td.classList.add("in"); G.painted.push(td); }
  }
  if (fillTo) {
    for (let r = fillTo.r0; r <= fillTo.r1; r++) for (let c = fillTo.c0; c <= fillTo.c1; c++) {
      const td = G.tds[r] && G.tds[r][c];
      if (td && !td.classList.contains("in")) { td.classList.add("fillp"); G.painted.push(td); }
    }
  }
  const cur = G.tds[G.sel.fr] && G.tds[G.sel.fr][G.sel.fc];
  if (cur) { cur.classList.add("cur"); G.painted.push(cur); }
  const head = document.querySelectorAll("#g-table thead th[data-c]");
  for (let c = g.c0; c <= g.c1; c++) if (head[c]) head[c].classList.add("hl");
  const rhs = document.querySelectorAll("#g-body th.g-rh");
  for (let r = g.r0; r <= g.r1; r++) if (rhs[r]) rhs[r].classList.add("hl");
  const info = document.getElementById("g-selinfo");
  if (info) {
    const nr = g.r1 - g.r0 + 1, nc = g.c1 - g.c0 + 1;
    info.textContent = nr * nc > 1 ? t("選択: {r} 行 × {c} 列", { r: nr, c: nc }) : "";
  }
  gridPaintClip();
  // The fill handle sits on the bottom-right corner of the selection.
  const corner = G.tds[g.r1] && G.tds[g.r1][g.c1];
  if (fill && corner && !G.edit) {
    const wr = G.wrap.getBoundingClientRect(), cr = corner.getBoundingClientRect();
    // Inside the cell's corner: hanging over its edge, the handle of the
    // last row sat under the table's horizontal scrollbar and could not be
    // grabbed.
    fill.style.left = (cr.right - wr.left + G.wrap.scrollLeft - 10) + "px";
    fill.style.top = (cr.bottom - wr.top + G.wrap.scrollTop - 10) + "px";
    fill.classList.remove("hidden");
  } else if (fill) fill.classList.add("hidden");
  // The ▼ of a column with choices sits at the right end of the current cell.
  const dd = document.getElementById("g-dd");
  const ccol = G.vc[G.sel.fc];
  if (dd && cur && !G.edit && gridChoices(ccol)) {
    const wr = G.wrap.getBoundingClientRect(), cr = cur.getBoundingClientRect();
    dd.style.left = (cr.right - wr.left + G.wrap.scrollLeft - 20) + "px";
    dd.style.top = (cr.top - wr.top + G.wrap.scrollTop + 1) + "px";
    dd.style.height = Math.max(cr.height - 2, 16) + "px";
    dd.classList.remove("hidden");
  } else if (dd) dd.classList.add("hidden");
}

// ---- choice list (▼): pick a column's value from a list, as Excel's
// data-validation dropdown. Applies to every selected cell of a column with
// the same choices, so a whole block of rows is set in one pick. ----
// (group is free text — a new group's name is typed — so it keeps the
// suggestions of the text editor instead.)
function gridChoices(col) {
  if (!col || col.kind === "secret" || col.kind === "text" || col.kind === "int" || col.kind === "group") return null;
  const opts = gridOptions(col.kind);
  return opts && opts.length ? opts : null;
}
function gridOpenList() {
  if (G.edit || G.busy || !G.view.length) return;
  const col = G.vc[G.sel.fc], row = gridRowAt(G.sel.fr), td = G.tds[G.sel.fr] && G.tds[G.sel.fr][G.sel.fc];
  const opts = gridChoices(col);
  const list = document.getElementById("g-list");
  if (!opts || !row || !td || !list) return;
  gridEnsureVisible(G.sel.fr, G.sel.fc);
  const cur = row.cells[col.key] || "";
  list.innerHTML = opts.map((o, i) => `<div class="g-opt${o.v === cur ? " on" : ""}" data-i="${i}">${esc(o.label)}</div>`).join("");
  const wr = G.wrap.getBoundingClientRect(), cr = td.getBoundingClientRect();
  list.style.left = (cr.left - wr.left + G.wrap.scrollLeft) + "px";
  list.style.minWidth = Math.max(cr.width, 160) + "px";
  list.classList.remove("hidden");
  // Below the cell, or above it when the table has no room left below.
  const below = wr.bottom - cr.bottom, h = Math.min(list.scrollHeight, 280);
  list.style.top = (below < h + 8 && cr.top - wr.top > below ? cr.top - wr.top + G.wrap.scrollTop - h - 2 : cr.bottom - wr.top + G.wrap.scrollTop + 1) + "px";
  G.list = { opts, i: Math.max(0, opts.findIndex(o => o.v === cur)), kind: col.kind };
  gridListHL();
  list.focus({ preventScroll: true });
}
function gridListHL() {
  const list = document.getElementById("g-list");
  list.querySelectorAll(".g-opt").forEach((el, i) => el.classList.toggle("hl", i === G.list.i));
  const el = list.querySelector(`.g-opt[data-i="${G.list.i}"]`);
  if (el) el.scrollIntoView({ block: "nearest" });
}
function gridCloseList() {
  const list = document.getElementById("g-list");
  if (list) list.classList.add("hidden");
  if (G) G.list = null;
}
function gridPickList(i) {
  const L = G.list;
  if (!L) return;
  const o = L.opts[i];
  gridCloseList();
  if (!o) return;
  // Over several rows (a whole column picked by its header) the empty rows
  // below the devices stay empty: a value there would make each an error row.
  const g = gridRange(), cells = [], many = g.r1 > g.r0;
  for (let r = g.r0; r <= g.r1; r++) {
    if (many && gridIsBlank(gridRowAt(r))) continue;
    for (let c = g.c0; c <= g.c1; c++) if (G.vc[c] && G.vc[c].kind === L.kind) cells.push([r, c, o.v]);
  }
  gridSetCells(cells);
  gridPaintSel();
  G.wrap.focus({ preventScroll: true });
}
function gridListKey(e) {
  const L = G && G.list;
  if (!L) return;
  e.stopPropagation();
  const n = L.opts.length;
  const go = i => { e.preventDefault(); L.i = Math.max(0, Math.min(n - 1, i)); gridListHL(); };
  if (e.key === "ArrowDown") return go(L.i + 1);
  if (e.key === "ArrowUp") return go(L.i - 1);
  if (e.key === "PageDown") return go(L.i + 10);
  if (e.key === "PageUp") return go(L.i - 10);
  if (e.key === "Home") return go(0);
  if (e.key === "End") return go(n - 1);
  if (e.key === "Enter" || e.key === "Tab") { e.preventDefault(); gridPickList(L.i); return; }
  if (e.key === "Escape") { e.preventDefault(); gridCloseList(); G.wrap.focus({ preventScroll: true }); return; }
  // A letter jumps to the next choice that starts with it.
  if (e.key.length === 1 && !e.ctrlKey && !e.altKey) {
    const ch = e.key.toLowerCase();
    for (let k = 1; k <= n; k++) {
      const j = (L.i + k) % n;
      if (L.opts[j].label.toLowerCase().startsWith(ch)) return go(j);
    }
  }
}
// gridShowSel scrolls the selection's moving end into view, except along a
// whole row or column: as in Excel, picking a row by its number (or Shift+
// Space) must not slide the table to its last column, nor a column header
// to the last row.
function gridShowSel() {
  const g = gridRange(), R = G.view.length - 1, C = G.vc.length - 1;
  gridEnsureVisible(G.sel.fr, G.sel.fc, C > 0 && g.c0 === 0 && g.c1 === C, R > 0 && g.r0 === 0 && g.r1 === R);
}
function gridEnsureVisible(r, c, keepX, keepY) {
  const td = G.tds[r] && G.tds[r][c];
  if (!td) return;
  const w = G.wrap, wr = w.getBoundingClientRect(), tr = td.getBoundingClientRect();
  const head = document.querySelector("#g-table thead").getBoundingClientRect().height;
  let left = 46;
  const stick = document.querySelector("#g-table thead th.g-stick");
  if (stick && c > 0) left += stick.getBoundingClientRect().width;
  if (keepY) { /* whole column: stay */ }
  else if (tr.top < wr.top + head) w.scrollTop -= wr.top + head - tr.top;
  else if (tr.bottom > wr.bottom - 14) w.scrollTop += tr.bottom - wr.bottom + 14;
  if (keepX) { /* whole row: stay */ }
  else if (tr.left < wr.left + left) w.scrollLeft -= wr.left + left - tr.left;
  else if (tr.right > wr.right - 14) w.scrollLeft += tr.right - wr.right + 14;
}
function gridSelect(ar, ac, fr, fc) {
  G.sel = { ar, ac, fr: fr ?? ar, fc: fc ?? ac };
  gridClampSel();
  gridPaintSel();
  gridShowSel();
}
function gridMove(dr, dc, extend) {
  const s = G.sel;
  const fr = s.fr + dr, fc = s.fc + dc;
  if (extend) { s.fr = fr; s.fc = fc; gridClampSel(); gridPaintSel(); gridEnsureVisible(s.fr, s.fc); }
  else gridSelect(fr, fc);
}

// ---- edits (every change goes through gridMutate: undo, redraw, autosave) ----
function gridSnap() { return JSON.stringify({ rows: G.rows, deleted: G.deleted }); }
function gridMutate(fn) {
  if (G.busy && !G.allowBusy) { toast(t("保存中です。終わるまでお待ちください"), "err"); return false; }
  const before = gridSnap();
  fn();
  gridApplyDefaults();
  if (G.clip && G.clip.cut && !G.pasting) G.clip = null;
  if (gridSnap() === before) return false;
  G.undo.push(before);
  // Each step keeps the whole table: a big table keeps fewer steps (about
  // 40 MB of text in all, never fewer than 10).
  const max = Math.max(10, Math.min(GRID_UNDO_MAX, Math.floor(20e6 / Math.max(before.length, 1))));
  while (G.undo.length > max) G.undo.shift();
  G.redo = [];
  gridAfterChange();
  return true;
}
function gridAfterChange() {
  gridPad();
  gridView();
  gridRenderBody();
  gridScheduleSave();
}
function gridUndo(redo) {
  if (G.busy) { toast(t("保存中です。終わるまでお待ちください"), "err"); return; }
  const from = redo ? G.redo : G.undo, to = redo ? G.undo : G.redo;
  if (!from.length) return;
  // As in Excel, undo cancels a pending cut or copy (its cells may change).
  G.clip = null;
  to.push(gridSnap());
  const o = JSON.parse(from.pop());
  G.rows = o.rows; G.deleted = o.deleted;
  gridAfterChange();
}
function gridRowAt(r) { return G.rows[G.view[r]]; }
// gridSetCells sets [viewRow, viewCol, text] cells, each normalised for its
// column (a pasted 「はい」 lands as true).
function gridSetCells(list) {
  return gridMutate(() => list.forEach(([r, c, text]) => {
    const row = gridRowAt(r), col = G.vc[c];
    if (row && col) row.cells[col.key] = gridNorm(col, text);
  }));
}

// ---- cell editor ----
function gridBeginEdit(initial) {
  if (G.edit || !G.view.length || G.busy) return;
  const r = G.sel.fr, c = G.sel.fc;
  const col = G.vc[c], row = gridRowAt(r), td = G.tds[r] && G.tds[r][c];
  if (!col || !row || !td) return;
  gridEnsureVisible(r, c);
  const inp = document.createElement("input");
  inp.className = "g-editor";
  inp.type = col.kind === "secret" && !G.showPw ? "password" : "text";
  inp.spellcheck = false;
  const v = row.cells[col.key] || "";
  inp.value = initial != null ? initial : (col.kind === "secret" ? v : gridLabel(col, v));
  // Suggestions: the column's choices, or (as Excel's AutoComplete) the
  // values already in the column. Never for passwords.
  const opts = gridOptions(col.kind);
  const dl = document.getElementById("g-dl");
  let labels = opts ? opts.map(o => o.label) : [];
  if (col.kind !== "secret" && (!opts || col.kind === "group")) {
    const seen = new Set(labels);
    for (const r of G.rows) { const x = r.cells[col.key]; if (x && !seen.has(x) && seen.size < 300) { seen.add(x); labels.push(x); } }
  }
  if (dl && labels.length && col.kind !== "secret") {
    dl.innerHTML = labels.map(l => `<option value="${esc(l)}"></option>`).join("");
    inp.setAttribute("list", "g-dl");
  }
  const wr = G.wrap.getBoundingClientRect(), cr = td.getBoundingClientRect();
  inp.style.left = (cr.left - wr.left + G.wrap.scrollLeft) + "px";
  inp.style.top = (cr.top - wr.top + G.wrap.scrollTop) + "px";
  inp.style.minWidth = Math.max(cr.width, 140) + "px";
  inp.style.height = cr.height + "px";
  G.wrap.appendChild(inp);
  G.edit = { r, c, inp, uid: row.uid, key: col.key, mode: initial != null ? "enter" : "edit" };
  document.getElementById("g-fill").classList.add("hidden");
  inp.focus();
  if (initial == null) inp.setSelectionRange(inp.value.length, inp.value.length);
  inp.addEventListener("keydown", gridEditKey);
  inp.addEventListener("blur", () => { if (G && G.edit && G.edit.inp === inp) gridCommitEdit(); });
}
function gridCommitEdit(move, all) {
  const e = G.edit;
  if (!e) return;
  G.edit = null;
  const text = e.inp.value;
  e.inp.remove();
  if (all) {
    const g = gridRange(), list = [];
    for (let r = g.r0; r <= g.r1; r++) for (let c = g.c0; c <= g.c1; c++) list.push([r, c, text]);
    gridSetCells(list);
  } else {
    // The row may have moved (sorted) while typing: find it by its UID.
    const r = G.view.findIndex(i => G.rows[i].uid === e.uid);
    const c = G.vc.findIndex(x => x.key === e.key);
    if (r >= 0 && c >= 0) gridSetCells([[r, c, text]]);
  }
  if (move) gridMove(move[0], move[1]);
  else gridPaintSel();
  G.wrap.focus({ preventScroll: true });
}
function gridCancelEdit() {
  const e = G.edit;
  if (!e) return;
  G.edit = null;
  e.inp.remove();
  gridPaintSel();
  G.wrap.focus({ preventScroll: true });
}
const GRID_ARROWS = { ArrowUp: [-1, 0], ArrowDown: [1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1] };
function gridEditKey(ev) {
  ev.stopPropagation();
  // Keys while the IME is converting (Enter to confirm, arrows to pick a
  // candidate) are the IME's, not the table's.
  if (ev.isComposing || ev.keyCode === 229) return;
  // As in Excel: started by typing, an arrow key enters the value and moves
  // (F2 switches to moving within the text, and back).
  if (ev.key === "F2") { ev.preventDefault(); G.edit.mode = G.edit.mode === "enter" ? "edit" : "enter"; return; }
  if (G.edit.mode === "enter" && GRID_ARROWS[ev.key] && !ev.shiftKey && !ev.ctrlKey && !ev.altKey) {
    ev.preventDefault(); gridCommitEdit(GRID_ARROWS[ev.key]); return;
  }
  if (ev.key === "Enter" && (ev.ctrlKey || ev.metaKey)) { ev.preventDefault(); gridCommitEdit(null, true); return; }
  if (ev.key === "Enter") { ev.preventDefault(); gridCommitEdit(ev.shiftKey ? [-1, 0] : [1, 0]); return; }
  if (ev.key === "Tab") { ev.preventDefault(); gridCommitEdit(ev.shiftKey ? [0, -1] : [0, 1]); return; }
  if (ev.key === "Escape") { ev.preventDefault(); gridCancelEdit(); return; }
  // Ctrl+R / Ctrl+D would reload / bookmark the page from inside the editor.
  if ((ev.ctrlKey || ev.metaKey) && ["r", "d", "s"].includes(ev.key.toLowerCase())) ev.preventDefault();
}

// ---- clipboard (tab-separated, as Excel copies and pastes) ----
// A value starting like a formula (= + - @, a tab or CR) would run as one when
// pasted into Excel (checked against Excel 16: "=1+1" pastes as a formula), so
// it goes out with a leading apostrophe, as the CSV export writes it; one that
// starts with an apostrophe gets one too, and pasting back strips exactly one
// — every value comes back as it was.
function gridCellOut(v) { return v && "=+-@\t\r'".includes(v[0]) ? "'" + v : v; }
function gridCellIn(v) { return v && v.length > 1 && v[0] === "'" && "=+-@\t\r'".includes(v[1]) ? v.slice(1) : v; }
function gridTSV(g) {
  const lines = [];
  for (let r = g.r0; r <= g.r1; r++) {
    const row = gridRowAt(r);
    const vals = [];
    for (let c = g.c0; c <= g.c1; c++) {
      const col = G.vc[c];
      const v = gridCellOut(gridCopyText(col, row.cells[col.key]));
      vals.push(/[\t\r\n"]/.test(v) ? '"' + v.replace(/"/g, '""') + '"' : v);
    }
    lines.push(vals.join("\t"));
  }
  return lines.join("\r\n");
}
function gridParseTSV(text) {
  const rows = [];
  let row = [], cur = "", q = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (q) {
      if (ch === '"') { if (text[i + 1] === '"') { cur += '"'; i++; } else q = false; }
      else cur += ch;
      continue;
    }
    if (ch === '"' && cur === "") { q = true; continue; }
    if (ch === "\t") { row.push(cur); cur = ""; continue; }
    if (ch === "\r" || ch === "\n") {
      if (ch === "\r" && text[i + 1] === "\n") i++;
      row.push(cur); rows.push(row); row = []; cur = "";
      continue;
    }
    cur += ch;
  }
  if (cur !== "" || row.length) { row.push(cur); rows.push(row); }
  return rows;
}
// Copy and cut leave a dashed frame round the source, as in Excel. A cut
// empties its cells only when it is pasted (Esc forgets it). Whole rows
// copied here also remember every cell (hidden columns too), for 「コピーし
// た行を挿入」.
async function gridCopy(cut) {
  if (!G.view.length) return;
  const g = gridRange();
  const text = gridTSV(g);
  try { await rt().ClipboardSetText(text); }
  catch (e) { toast(t("コピーできませんでした") + ": " + terr(e), "err"); return; }
  const uids = [], keys = [];
  for (let r = g.r0; r <= g.r1; r++) uids.push(gridRowAt(r).uid);
  for (let c = g.c0; c <= g.c1; c++) keys.push(G.vc[c].key);
  const whole = g.c0 === 0 && g.c1 === G.vc.length - 1;
  // raw: the cells as stored (passwords included, whatever is shown), so a
  // paste of this table's own copy writes the real values.
  const raw = uids.map(u => { const c = G.rows.find(x => x.uid === u).cells; return keys.map(k => c[k] || ""); });
  G.clip = { text, cut, uids, keys, raw, rows: whole ? uids.map(u => ({ ...G.rows.find(x => x.uid === u).cells })) : null };
  gridPaintClip();
  const n = (g.r1 - g.r0 + 1) * (g.c1 - g.c0 + 1);
  if (cut) {
    toast(t("{n} セルを切り取りました（貼り付け先を選んで Ctrl+V。Esc で取り消し）", { n }), "ok");
  } else {
    const pw = !G.showPw && G.vc.slice(g.c0, g.c1 + 1).some(c => c.kind === "secret");
    toast(pw ? t("{n} セルをコピーしました（パスワードは表示中だけコピーされます）", { n }) : t("{n} セルをコピーしました", { n }), "ok");
  }
}
async function gridPaste() {
  let text = "";
  try { text = await rt().ClipboardGetText(); } catch (e) { }
  // A copy of hidden password cells is empty text, yet still this table's
  // own copy (pasted from its stored values).
  const own = G.clip && gridClipText(G.clip.text) === gridClipText(text || "");
  if (!text && !own) { toast(t("クリップボードが空です"), "err"); return; }
  gridPasteText(text || "");
}
function gridClipText(text) {
  if (text.endsWith("\r\n")) return text.slice(0, -2);
  if (text.endsWith("\n")) return text.slice(0, -1);
  return text;
}
function gridPaintClip() {
  const el = document.getElementById("g-clip");
  if (!el) return;
  const c = G.clip;
  const r0 = c ? G.view.findIndex(i => G.rows[i].uid === c.uids[0]) : -1;
  const r1 = c ? G.view.findIndex(i => G.rows[i].uid === c.uids[c.uids.length - 1]) : -1;
  const c0 = c ? G.vc.findIndex(x => x.key === c.keys[0]) : -1;
  const c1 = c ? G.vc.findIndex(x => x.key === c.keys[c.keys.length - 1]) : -1;
  const a = r0 >= 0 && c0 >= 0 && G.tds[r0] && G.tds[r0][c0], b = r1 >= 0 && c1 >= 0 && G.tds[r1] && G.tds[r1][c1];
  if (!a || !b) { el.classList.add("hidden"); return; }
  const wr = G.wrap.getBoundingClientRect(), ar = a.getBoundingClientRect(), br = b.getBoundingClientRect();
  el.style.left = (ar.left - wr.left + G.wrap.scrollLeft) + "px";
  el.style.top = (ar.top - wr.top + G.wrap.scrollTop) + "px";
  el.style.width = (br.right - ar.left) + "px";
  el.style.height = (br.bottom - ar.top) + "px";
  el.classList.toggle("cut", !!c.cut);
  el.classList.remove("hidden");
}
function gridPasteText(text) {
  text = gridClipText(text);
  // Pasting what this table cut moves it: the cut cells empty, except
  // those the paste itself writes.
  const own = G.clip && gridClipText(G.clip.text) === text ? G.clip : null;
  const cutSrc = own && own.cut ? own : null;
  const data = own ? own.raw : gridParseTSV(text).map(r => r.map(gridCellIn));
  if (!data.length) return;
  const h = data.length, w = Math.max(...data.map(r => r.length));
  const g = G.view.length ? gridRange() : { r0: 0, r1: 0, c0: 0, c1: 0 };
  const sr = g.r1 - g.r0 + 1, sc = g.c1 - g.c0 + 1;
  const list = [];
  let endR = g.r0 + h - 1, endC = Math.min(g.c0 + w - 1, G.vc.length - 1);
  G.pasting = true;
  gridMutate(() => {
    if (h === 1 && w === 1 && (sr > 1 || sc > 1)) {
      // One value into a selection: every selected cell gets it.
      for (let r = g.r0; r <= g.r1; r++) for (let c = g.c0; c <= g.c1; c++) list.push([r, c, data[0][0]]);
      endR = g.r1; endC = g.c1;
    } else if ((sr > h || sc > w) && sr % h === 0 && sc % w === 0) {
      // A block into a selection it fits a whole number of times: tiled.
      for (let r = 0; r < sr; r++) for (let c = 0; c < sc; c++) list.push([g.r0 + r, g.c0 + c, (data[r % h][c % w] ?? "")]);
      endR = g.r1; endC = g.c1;
    } else {
      // Rows past the end become new rows, as in Excel.
      for (let i = 0; i < h; i++) {
        const r = g.r0 + i;
        if (r >= G.view.length) {
          G.rows.push(gridNewRow());
          G.view.push(G.rows.length - 1);
        }
        for (let j = 0; j < w; j++) if (g.c0 + j < G.vc.length) list.push([r, g.c0 + j, data[i][j] ?? ""]);
      }
    }
    const wrote = new Set();
    const keyCol = k => G.cols.find(x => x.key === k);
    list.forEach(([r, c, v]) => {
      const row = gridRowAt(r), col = G.vc[c];
      if (!row || !col) return;
      if (own) v = gridXfer(keyCol(own.keys[(c - g.c0) % own.keys.length]), col, v);
      // A password column reads empty while passwords are hidden (a copy
      // made then has them blank): an empty value pasted over a password
      // leaves it. Delete or the shown passwords clear it on purpose.
      if (col.kind === "secret" && !G.showPw && !own && v === "" && row.cells[col.key]) return;
      row.cells[col.key] = gridNorm(col, v);
      wrote.add(row.uid + "\x00" + col.key);
    });
    if (cutSrc) {
      cutSrc.uids.forEach(u => { const row = G.rows.find(x => x.uid === u); if (row) cutSrc.keys.forEach(k => { if (!wrote.has(u + "\x00" + k)) row.cells[k] = ""; }); });
      G.clip = null;
    }
  });
  G.pasting = false;
  G.sel = { ar: g.r0, ac: g.c0, fr: Math.min(endR, G.view.length - 1), fc: endC };
  gridPaintSel();
  gridShowSel();
}
function gridClear() {
  if (!G.view.length) return;
  const g = gridRange(), list = [];
  for (let r = g.r0; r <= g.r1; r++) for (let c = g.c0; c <= g.c1; c++) list.push([r, c, ""]);
  gridSetCells(list);
}
// Ctrl+D / Ctrl+R: the top row (left column) of the selection copied over
// the rest of it; with one row (column) selected, the one above (left).
function gridFillDown(right) {
  if (!G.view.length) return;
  const g = gridRange(), list = [];
  if (!right) {
    const src = g.r0 === g.r1 ? g.r0 - 1 : g.r0;
    if (src < 0) return;
    for (let r = (g.r0 === g.r1 ? g.r0 : g.r0 + 1); r <= g.r1; r++)
      for (let c = g.c0; c <= g.c1; c++) list.push([r, c, gridLabelRaw(src, c)]);
  } else {
    const src = g.c0 === g.c1 ? g.c0 - 1 : g.c0;
    if (src < 0) return;
    for (let c = (g.c0 === g.c1 ? g.c0 : g.c0 + 1); c <= g.c1; c++)
      for (let r = g.r0; r <= g.r1; r++) list.push([r, c, gridXfer(G.vc[src], G.vc[c], gridLabelRaw(r, src))]);
  }
  gridSetCells(list);
}
// The value of a cell in a form that reads back in another column too.
function gridLabelRaw(r, c) {
  return gridRowAt(r).cells[G.vc[c].key] || "";
}
// gridXfer is a value copied from column src into column dst: a password
// (or passphrase) is never written into a column that shows it in clear.
function gridXfer(src, dst, v) {
  return src && src.kind === "secret" && dst && dst.kind !== "secret" ? "" : v;
}

// ---- fill handle: series (core-sw1 → core-sw2, 192.0.2.10 → .11) or copy ----
function gridSeries(vals, n, kind, ctrl) {
  const copy = () => Array.from({ length: n }, (_, i) => vals[i % vals.length]);
  if (!GRID_FREE[kind] || kind === "secret" || kind === "group") return copy();
  const re = /^(.*?)(\d+)$/;
  const ms = vals.map(v => re.exec(v || ""));
  if (ms.some(m => !m) || ms.some(m => m[1] !== ms[0][1])) return copy();
  const nums = ms.map(m => parseInt(m[2], 10));
  let step = 1;
  if (vals.length === 1) {
    // A plain number is copied, text ending in a number counts on (Excel);
    // Ctrl swaps the two.
    const pure = ms[0][1] === "";
    if (pure !== !!ctrl) return copy();
  } else {
    step = nums[1] - nums[0];
    if (nums.some((x, i) => i && x - nums[i - 1] !== step) || ctrl) return copy();
  }
  const width = ms[0][2].startsWith("0") ? ms[0][2].length : 0;
  const out = [];
  for (let i = 1; i <= n; i++) {
    const x = nums[nums.length - 1] + step * i;
    if (x < 0) return copy();
    out.push(ms[0][1] + String(x).padStart(width, "0"));
  }
  return out;
}
function gridFillTarget(g, r, c) {
  if (r > g.r1) return { dir: "down", t: { r0: g.r1 + 1, r1: r, c0: g.c0, c1: g.c1 } };
  if (r < g.r0) return { dir: "up", t: { r0: r, r1: g.r0 - 1, c0: g.c0, c1: g.c1 } };
  if (c > g.c1) return { dir: "right", t: { r0: g.r0, r1: g.r1, c0: g.c1 + 1, c1: c } };
  if (c < g.c0) return { dir: "left", t: { r0: g.r0, r1: g.r1, c0: c, c1: g.c0 - 1 } };
  return null;
}
function gridFillHint(text) {
  const el = document.getElementById("g-status");
  if (!el) return;
  if (text) { el.dataset.keep = el.innerHTML; el.innerHTML = `<b style="color:var(--accent)">${esc(text)}</b>`; }
  else if (el.dataset.keep != null) { el.innerHTML = el.dataset.keep; delete el.dataset.keep; }
}
function gridFill(g, ft, ctrl) {
  const more = ft.t.r1 - (G.view.length - 1);
  const list = [];
  const vert = ft.dir === "down" || ft.dir === "up";
  if (vert) {
    const n = ft.t.r1 - ft.t.r0 + 1;
    for (let c = g.c0; c <= g.c1; c++) {
      const col = G.vc[c];
      let src = [];
      for (let r = g.r0; r <= g.r1; r++) src.push(gridLabelRaw(r, c));
      if (ft.dir === "up") src.reverse();
      const vals = gridSeries(src, n, col.kind, ctrl);
      vals.forEach((v, i) => list.push([ft.dir === "down" ? ft.t.r0 + i : ft.t.r1 - i, c, v]));
    }
  } else {
    const n = ft.t.c1 - ft.t.c0 + 1;
    for (let r = g.r0; r <= g.r1; r++) {
      const src = [];
      for (let c = g.c0; c <= g.c1; c++) src.push([G.vc[c], gridLabelRaw(r, c)]);
      if (ft.dir === "left") src.reverse();
      // Across columns each value reads into a column of its own kind:
      // counting on only makes sense within one, so it is a copy.
      for (let i = 0; i < n; i++) {
        const c = ft.dir === "right" ? ft.t.c0 + i : ft.t.c1 - i;
        const [sc, v] = src[i % src.length];
        list.push([r, c, gridXfer(sc, G.vc[c], v)]);
      }
    }
  }
  // One step to undo, the rows a fill past the end added included.
  gridMutate(() => {
    for (let i = 0; i < more; i++) { G.rows.push(gridNewRow()); G.view.push(G.rows.length - 1); }
    list.forEach(([r, c, text]) => { const row = gridRowAt(r), col = G.vc[c]; if (row && col) row.cells[col.key] = gridNorm(col, text); });
  });
  G.sel = { ar: Math.min(g.r0, ft.t.r0), ac: Math.min(g.c0, ft.t.c0), fr: Math.max(g.r1, ft.t.r1), fc: Math.max(g.c1, ft.t.c1) };
  gridPaintSel();
}

// ---- rows ----
function gridSelRows() {
  const g = gridRange();
  const out = [];
  for (let r = g.r0; r <= g.r1; r++) out.push(gridRowAt(r));
  return out.filter(Boolean);
}
// gridNotSorted refuses an insert in the middle of a sorted view: the new row
// would not show where it was asked for. Moving rows is refused the same way.
function gridNotSorted() {
  if (!G.sort.key) return true;
  toast(t("並べ替え中は行の挿入・複製・移動はできません（「この並び順で確定」か「解除」をしてから）"), "err");
  return false;
}
// gridInsertAt inserts rows above (or below) the selected rows: as many
// empty rows as are selected, or one per entry of cellsList.
function gridInsertAt(below, cellsList) {
  if (G.busy || !gridNotSorted()) return;
  const g = G.view.length ? gridRange() : null;
  const n = cellsList ? cellsList.length : (g ? g.r1 - g.r0 + 1 : 1);
  if (!n) return;
  const at = !g ? G.rows.length : below ? G.rows.indexOf(gridRowAt(g.r1)) + 1 : G.rows.indexOf(gridRowAt(g.r0));
  const fresh = Array.from({ length: n }, (_, i) => cellsList ? gridNewRow(cellsList[i]) : gridNewRow());
  gridMutate(() => G.rows.splice(at, 0, ...fresh));
  gridSelectRows(fresh, g ? g.c0 : 0, g ? g.c1 : G.vc.length - 1, !cellsList);
}
function gridInsertRows() { gridInsertAt(false); }
// gridSelectRows selects the given rows (whole width when wide).
function gridSelectRows(rows, c0, c1, wide) {
  const idx = rows.map(r => G.view.findIndex(i => G.rows[i] === r)).filter(i => i >= 0);
  if (!idx.length) return;
  gridSelect(Math.min(...idx), wide ? 0 : c0, Math.max(...idx), wide ? G.vc.length - 1 : c1);
}
// The row header's ＋: one empty row right under that row.
function gridInsertBelowRow(r) {
  if (G.busy || !gridNotSorted()) return;
  gridSelect(r, 0, r, G.vc.length - 1);
  gridInsertAt(true);
  gridSelect(r + 1, 0);
}
// 「＋ 末尾に行を追加」: the first empty row under the table's last device.
function gridAddRow() {
  let i = G.rows.length;
  while (i > 0 && gridIsBlank(G.rows[i - 1])) i--;
  if (i >= G.rows.length) { gridMutate(() => G.rows.push(gridNewRow())); }
  const row = G.rows[i];
  const r = G.view.findIndex(x => G.rows[x] === row);
  gridSelect(r, 0);
  gridBeginEdit(null);
}
// Rows copied in this table come back whole (hidden columns too); rows
// copied from Excel land in the selected columns, as Excel's 「コピーした
// セルの挿入」 does. Rows cut here move instead (they keep being the same
// devices).
async function gridInsertCopied(below) {
  if (G.busy || !gridNotSorted()) return;
  let text = "";
  try { text = await rt().ClipboardGetText(); } catch (e) { }
  text = gridClipText(text || "");
  if (!text) { toast(t("クリップボードが空です。先に行をコピーしてください"), "err"); return; }
  const g = G.view.length ? gridRange() : { r0: 0, r1: 0, c0: 0, c1: 0 };
  const clip = G.clip && G.clip.rows && gridClipText(G.clip.text) === text ? G.clip : null;
  if (clip && clip.cut) {
    const moving = clip.uids.map(u => G.rows.find(x => x.uid === u)).filter(Boolean);
    const anchor = below ? gridRowAt(g.r1) : gridRowAt(g.r0);
    if (moving.includes(anchor)) { toast(t("切り取った行の中には挿入できません"), "err"); return; }
    gridMutate(() => {
      G.rows = G.rows.filter(r => !moving.includes(r));
      const at = G.rows.indexOf(anchor) + (below ? 1 : 0);
      G.rows.splice(at, 0, ...moving);
      G.clip = null;
    });
    gridSelectRows(moving, 0, G.vc.length - 1, true);
    toast(t("{n} 行を移動しました", { n: moving.length }), "ok");
    return;
  }
  let list;
  if (clip) list = clip.rows.map(c => ({ ...c }));
  else {
    list = gridParseTSV(text).map(row => row.map(gridCellIn)).map(row => {
      const cells = {};
      row.forEach((v, j) => { const col = G.vc[g.c0 + j]; if (col) cells[col.key] = gridNorm(col, v); });
      return cells;
    });
  }
  gridInsertAt(below, list);
  toast(t("{n} 行を挿入しました", { n: list.length }), "ok");
}
async function gridCopyRows() {
  if (!G.view.length) return;
  const g = gridRange();
  gridSelect(g.r0, 0, g.r1, G.vc.length - 1);
  await gridCopy(false);
}
// Alt+↑ / Alt+↓: the selected rows swap places with the row above / below.
function gridMoveRows(dir) {
  if (!G.view.length) return;
  if (G.busy || !gridNotSorted()) return;
  const g = gridRange();
  const sel = [];
  for (let r = g.r0; r <= g.r1; r++) sel.push(gridRowAt(r));
  const nb = dir < 0 ? gridRowAt(g.r0 - 1) : gridRowAt(g.r1 + 1);
  if (!nb) return;
  gridMutate(() => {
    G.rows = G.rows.filter(r => !sel.includes(r));
    G.rows.splice(G.rows.indexOf(nb) + (dir > 0 ? 1 : 0), 0, ...sel);
  });
  const keep = { c0: g.c0, c1: g.c1 };
  const idx = sel.map(r => G.view.findIndex(i => G.rows[i] === r));
  G.sel = { ar: Math.min(...idx), ac: keep.c0, fr: Math.max(...idx), fc: keep.c1 };
  gridPaintSel();
  gridShowSel();
}
// The rows as sorted become the table's order (and so the saved order).
function gridSortFix() {
  if (G.busy) { toast(t("保存中です。終わるまでお待ちください"), "err"); return; }
  gridMutate(() => {
    const slots = [...G.view].sort((a, b) => a - b);
    const ordered = G.view.map(i => G.rows[i]);
    const next = G.rows.slice();
    slots.forEach((sl, k) => { next[sl] = ordered[k]; });
    G.rows = next;
  });
  G.sort = { key: "", dir: 1 };
  renderGrid(true);
  toast(t("この並び順にしました（保存すると機器の並び順になります）"), "ok");
}
function gridDeleteRows() {
  if (!G.view.length) return;
  const rows = gridSelRows();
  const g = gridRange();
  gridMutate(() => {
    const gone = new Set(rows.map(r => r.uid));
    // Each remembers the nearest row above it that stays, to go back under
    // it if the deletion is called off in the conflict dialog.
    rows.forEach(r => {
      if (!r.id) return;
      let i = G.rows.indexOf(r) - 1;
      while (i >= 0 && gone.has(G.rows[i].uid)) i--;
      G.deleted.push({ uid: r.uid, id: r.id, base: r.base, cells: r.base, after: i >= 0 ? G.rows[i].uid : "" });
    });
    G.rows = G.rows.filter(r => !gone.has(r.uid));
  });
  gridSelect(Math.min(g.r0, G.view.length - 1), g.c0);
  const n = rows.filter(r => r.id).length;
  if (n) toast(t("{n} 行を削除しました（保存するまで機器は消えません。元に戻す: Ctrl+Z）", { n }), "ok");
}
function gridDuplicateRows() {
  if (!G.view.length || G.busy || !gridNotSorted()) return;
  const rows = gridSelRows();
  const g = gridRange();
  const taken = new Set(G.rows.map(r => (r.cells.group || "") + "\x00" + (r.cells.name || "")));
  const copies = rows.map(r => {
    const cells = { ...r.cells };
    if (cells.name) {
      let i = 2, nm;
      do { nm = cells.name + "_" + i++; } while (taken.has((cells.group || "") + "\x00" + nm));
      taken.add((cells.group || "") + "\x00" + nm);
      cells.name = nm;
    }
    return gridNewRow(cells);
  });
  const at = G.rows.indexOf(rows[rows.length - 1]) + 1;
  gridMutate(() => G.rows.splice(at, 0, ...copies));
  const r = G.view.findIndex(i => G.rows[i].uid === copies[0].uid);
  gridSelect(r, g.c0, r + copies.length - 1, g.c1);
}

// ---- draft autosave and live check ----
// The draft goes to the backend right after each change (the backend
// batches the vault writes, and writes the last one on lock and on exit), so
// closing the window straight after an edit loses nothing.
function gridScheduleSave() {
  clearTimeout(G.saveT);
  G.saveT = setTimeout(() => gridSaveDraft().catch(() => { }), 0);
  gridScheduleCheck(700);
}
async function gridSaveDraft() {
  if (!G) return;
  clearTimeout(G.saveT); G.saveT = null;
  await App().SaveGridDraft(G.scope, { rows: gridPayloadRows(), deleted: G.deleted });
  G.savedAt = new Date();
  const n = gridChangeCount();
  if (n) GRID_COUNTS[G.scope] = n; else delete GRID_COUNTS[G.scope];
  gridStatus();
}
function gridScheduleCheck(ms) {
  clearTimeout(G.chkT);
  G.chkT = setTimeout(() => gridCheck().catch(() => { }), ms);
}
function gridApplyReport(rep) {
  G.errors = {};
  for (const [uid, list] of Object.entries(rep.errors || {})) {
    const m = {};
    list.forEach(e => { m[e.col] = m[e.col] ? m[e.col] + " ／ " + terr(e.msg) : terr(e.msg); });
    G.errors[uid] = m;
  }
  G.conflicts = {};
  (rep.conflicts || []).forEach(c => { G.conflicts[c.uid] = c; });
}
async function gridCheck() {
  if (!G) return;
  const scope = G.scope;
  const rep = await App().ApplyGrid({ scope, rows: gridPayloadRows(), deleted: G.deleted, mode: "check", overwrite: [] });
  if (!G || G.scope !== scope) return;
  gridApplyReport(rep);
  if (!G.edit) gridRenderBody(); else gridStatus();
}

// ---- save ----
// While a save runs the table takes no edits (they would be overwritten by
// the saved rows) and no second save.
async function gridSave() {
  if (G.busy) return;
  if (G.edit) gridCommitEdit();
  if (!gridChangeCount()) { toast(t("保存する変更はありません"), "ok"); return; }
  G.busy = true;
  gridStatus();
  try { await gridSaveRun(); } finally { if (G) { G.busy = false; gridStatus(); } }
}
async function gridSaveRun() {
  const over = [];
  let rep;
  for (;;) {
    rep = await App().ApplyGrid({ scope: G.scope, rows: gridPayloadRows(), deleted: G.deleted, mode: "check", overwrite: over });
    gridApplyReport(rep);
    gridRenderBody();
    const open = (rep.conflicts || []).filter(c => !over.includes(c.uid));
    if (!open.length) break;
    const picked = await gridResolveConflicts(open);
    if (!picked) return;
    over.push(...picked);
  }
  const errRows = Object.keys(rep.errors || {}).length;
  let mode = "all";
  if (errRows) {
    const sample = Object.entries(G.errors).slice(0, 5).map(([uid, m]) => {
      const row = G.rows.find(r => r.uid === uid);
      const name = (row && row.cells.name) || t("（名前なし）");
      return `<li><b>${esc(name)}</b>: ${esc(Object.values(m).join(" ／ "))}</li>`;
    }).join("");
    const c = await uiChoice({
      title: t("エラーのある行があります"),
      message: esc(t("{n} 行にエラーがあります（赤枠のセル。マウスを当てると理由が出ます）。", { n: errRows })) + `<ul style="margin:6px 0 0;padding-left:20px;font-size:13px">${sample}</ul>`,
      buttons: [{ id: "cancel", label: t("キャンセル") }, { id: "back", label: t("すべて保存しない（編集に戻る）") }, { id: "valid", label: t("エラーのない行だけ保存（エラー行は下書きとして保存）"), cls: "primary" }],
    });
    if (c === "back") { gridFocusFirstError(); return; }
    if (c !== "valid") return;
    mode = "valid";
  } else {
    const parts = [];
    if (rep.added) parts.push(t("追加 {n} 台", { n: rep.added }));
    if (rep.changed) parts.push(t("変更 {n} 台", { n: rep.changed }));
    if (rep.deleted) parts.push(t("削除 {n} 台", { n: rep.deleted }));
    if (!(await uiConfirm({ title: t("表の変更を保存"), message: esc(t("{list} を保存しますか？", { list: parts.join("・") })), okLabel: t("保存する"), danger: !!rep.deleted }))) return;
  }
  rep = await App().ApplyGrid({ scope: G.scope, rows: gridPayloadRows(), deleted: G.deleted, mode, overwrite: over });
  await gridAfterSave(rep);
}
async function gridAfterSave(rep) {
  const saved = rep.saved || {};
  G.rows = G.rows.map(r => saved[r.uid] ? { ...saved[r.uid], uid: r.uid } : r);
  const gone = new Set(rep.savedDeletes || []);
  G.deleted = G.deleted.filter(d => !gone.has(d.id));
  const n0 = Object.keys(saved).length + gone.size;
  if (rep.applied || n0) { G.undo = []; G.redo = []; }
  gridPad();
  const left = gridChangeCount();
  if (left) await gridSaveDraft(); else { await App().DiscardGridDraft(G.scope); delete GRID_COUNTS[G.scope]; }
  await refreshInventory();
  await gridCheck();
  gridView(); gridRenderBody();
  const n = Object.keys(saved).length + gone.size;
  if (!rep.applied && !n) { toast(t("保存できる行がありませんでした"), "err"); return; }
  toast(left ? t("{n} 台分を保存しました。保存できなかった {m} 行は下書きに残しています", { n, m: left }) : t("{n} 台分を保存しました", { n }), "ok");
}
function gridFocusFirstError() {
  for (let r = 0; r < G.view.length; r++) {
    const e = G.errors[gridRowAt(r).uid];
    if (!e) continue;
    const c = G.vc.findIndex(col => e[col.key]);
    gridSelect(r, Math.max(0, c));
    if (c < 0) toast(t("エラーの列が表示されていません。「列の表示…」で表示してください"), "err");
    return;
  }
}
// Rows whose device changed or went elsewhere since the table read it: for
// each, keep the table's version or the other one. Resolves to the UIDs to
// save over, or null when cancelled.
function gridResolveConflicts(list) {
  return new Promise(resolve => {
    const colOf = k => G.cols.find(c => c.key === k) || { key: k, ja: k, kind: "text" };
    const item = c => {
      const del = c.uid.startsWith("del:");
      const what = c.gone ? t("他の画面で削除されています") : del ? t("この表で削除しましたが、他の画面で変更されています") : t("他の画面で変更されています");
      const diffs = (c.diffs || []).map(d => { const col = colOf(d.col); return `<tr><td>${esc(gridColTitle(col))}</td><td>${esc(gridLabel(col, d.theirs))}</td><td>${del ? "" : esc(gridLabel(col, d.mine))}</td></tr>`; }).join("");
      const mine = c.gone ? t("この表の内容で作り直す") : del ? t("削除する") : t("この表の内容で上書き");
      const theirs = c.gone ? t("削除を受け入れる（この行を消す）") : del ? t("削除をやめる") : t("他の画面での変更を残す（この行の編集を取り消す）");
      return `<div class="card" style="padding:10px 12px;margin:8px 0">
        <b>${esc(c.name || "")}</b> <span class="muted" style="font-size:12px">${esc(what)}</span>
        ${diffs ? `<table style="font-size:12px;margin:6px 0"><thead><tr><th>${esc(t("列"))}</th><th>${esc(t("他の画面での変更後"))}</th><th>${esc(del ? "" : t("この表の値"))}</th></tr></thead><tbody>${diffs}</tbody></table>` : ""}
        <label style="display:block;font-size:13px"><input type="radio" name="gc-${esc(c.uid)}" value="mine" checked style="width:auto"> ${esc(mine)}</label>
        <label style="display:block;font-size:13px"><input type="radio" name="gc-${esc(c.uid)}" value="theirs" style="width:auto"> ${esc(theirs)}</label>
      </div>`;
    };
    const node = h(`<div>
      <h3>${esc(t("他の画面での変更と重なっています"))}</h3>
      <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("表を開いてから、次の機器が機器の編集画面などで変更・削除されました。どちらを残すか選んでください。"))}</p>
      <div style="max-height:55vh;overflow:auto">${list.map(item).join("")}</div>
      <div class="modal-actions"><button class="btn" id="gc-cancel">${esc(t("キャンセル"))}</button><button class="btn primary" id="gc-ok">${esc(t("決定"))}</button></div>
    </div>`);
    openModal(node, "mid");
    node.querySelector("#gc-cancel").onclick = () => { closeModal(); resolve(null); };
    node.querySelector("#gc-ok").onclick = () => {
      const over = [];
      // Part of the save in progress: the one edit allowed while it runs.
      G.allowBusy = true;
      gridMutate(() => list.forEach(c => {
        const pick = node.querySelector(`input[name="gc-${CSS.escape(c.uid)}"]:checked`).value;
        if (pick === "mine") { over.push(c.uid); return; }
        if (c.uid.startsWith("del:")) {
          const was = G.deleted.find(d => d.id === c.id);
          G.deleted = G.deleted.filter(d => d.id !== c.id);
          let end = G.rows.length;
          while (end > 0 && gridIsBlank(G.rows[end - 1])) end--;
          const anchor = was && was.after ? G.rows.findIndex(r => r.uid === was.after) : -1;
          const at = was && was.after === "" ? 0 : anchor >= 0 ? anchor + 1 : end;
          G.rows.splice(at, 0, { uid: c.id, id: c.id, base: c.current, cells: { ...c.current } });
        } else if (c.gone) {
          G.rows = G.rows.filter(r => r.uid !== c.uid);
        } else {
          const row = G.rows.find(r => r.uid === c.uid);
          if (row) { row.base = c.current; row.cells = { ...c.current }; }
        }
      }));
      G.allowBusy = false;
      closeModal();
      resolve(over);
    };
  });
}

// ---- find / replace ----
function gridFind(replace) {
  const bar = document.getElementById("g-findbar");
  const g = gridRange();
  const multi = G.view.length && (g.r1 > g.r0 || g.c1 > g.c0);
  G.findRange = null;
  if (multi) {
    const uids = new Set(), keys = new Set();
    for (let r = g.r0; r <= g.r1; r++) uids.add(gridRowAt(r).uid);
    for (let c = g.c0; c <= g.c1; c++) keys.add(G.vc[c].key);
    G.findRange = { uids, keys };
  }
  bar.innerHTML = `
    <input id="gf-q" class="btn" style="width:200px;text-align:left;cursor:text" placeholder="${esc(t("検索する文字"))}" value="${esc(G.findQ || "")}">
    ${replace ? `<input id="gf-r" class="btn" style="width:200px;text-align:left;cursor:text" placeholder="${esc(t("置換後の文字"))}" value="${esc(G.findR || "")}">` : ""}
    ${multi ? `<label style="font-size:13px;display:flex;align-items:center;gap:4px"><input type="checkbox" id="gf-sel" checked style="width:auto">${esc(t("選択範囲のみ"))}</label>` : ""}
    <label style="font-size:13px;display:flex;align-items:center;gap:4px"><input type="checkbox" id="gf-case" ${G.findCase ? "checked" : ""} style="width:auto">${esc(t("大文字と小文字を区別"))}</label>
    <label style="font-size:13px;display:flex;align-items:center;gap:4px"><input type="checkbox" id="gf-whole" ${G.findWhole ? "checked" : ""} style="width:auto">${esc(t("セル全体が一致"))}</label>
    <button class="btn sm" id="gf-prev">${esc(t("前へ"))}</button>
    <button class="btn sm" id="gf-next">${esc(t("次へ"))}</button>
    ${replace ? `<button class="btn sm" id="gf-rep">${esc(t("置換"))}</button><button class="btn sm" id="gf-all">${esc(t("すべて置換"))}</button>` : ""}
    <span class="muted" id="gf-info" style="font-size:12px"></span>
    <button class="btn sm" id="gf-close" data-tip="${esc(t("閉じる（Esc）"))}">×</button>`;
  bar.classList.remove("hidden");
  const q = bar.querySelector("#gf-q");
  q.focus(); q.select();
  const close = () => { bar.classList.add("hidden"); bar.innerHTML = ""; G.wrap.focus({ preventScroll: true }); };
  bar.querySelector("#gf-close").onclick = close;
  bar.querySelector("#gf-next").onclick = () => gridFindStep(1);
  bar.querySelector("#gf-prev").onclick = () => gridFindStep(-1);
  if (replace) {
    bar.querySelector("#gf-rep").onclick = gridReplaceOne;
    bar.querySelector("#gf-all").onclick = gridReplaceAll;
  }
  bar.onkeydown = e => {
    e.stopPropagation();
    if (e.key === "Escape") { e.preventDefault(); close(); }
    else if (e.key === "Enter") { e.preventDefault(); gridFindStep(e.shiftKey ? -1 : 1); }
    else if ((e.ctrlKey || e.metaKey) && ["r", "d", "s"].includes(e.key.toLowerCase())) e.preventDefault();
  };
  bar.oninput = () => { G.findQ = q.value; const r = bar.querySelector("#gf-r"); if (r) G.findR = r.value; };
  bar.onchange = () => { G.findCase = bar.querySelector("#gf-case").checked; G.findWhole = bar.querySelector("#gf-whole").checked; };
}
function gridHit(text) {
  const q = G.findQ || "";
  const a = G.findCase ? text : text.toLowerCase(), b = G.findCase ? q : q.toLowerCase();
  return G.findWhole ? a === b : a.includes(b);
}
function gridMatches() {
  const q = G.findQ || "";
  if (!q) return [];
  const sel = document.getElementById("gf-sel");
  const only = sel && sel.checked && G.findRange ? G.findRange : null;
  const out = [];
  for (let r = 0; r < G.view.length; r++) for (let c = 0; c < G.vc.length; c++) {
    const col = G.vc[c];
    if (only && !(only.uids.has(gridRowAt(r).uid) && only.keys.has(col.key))) continue;
    if (col.kind === "secret" && !G.showPw) continue;
    if (gridHit(gridLabel(col, gridRowAt(r).cells[col.key]))) out.push([r, c]);
  }
  return out;
}
function gridFindStep(dir) {
  const m = gridMatches();
  const info = document.getElementById("gf-info");
  if (!m.length) { if (info) info.textContent = t("見つかりません"); return; }
  const at = G.sel.fr * 10000 + G.sel.fc;
  let i = dir > 0 ? m.findIndex(([r, c]) => r * 10000 + c > at) : m.map(([r, c]) => r * 10000 + c < at).lastIndexOf(true);
  if (i < 0) i = dir > 0 ? 0 : m.length - 1;
  const keep = G.findRange;
  G.sel = { ar: m[i][0], ac: m[i][1], fr: m[i][0], fc: m[i][1] };
  gridPaintSel(); gridEnsureVisible(m[i][0], m[i][1]);
  G.findRange = keep;
  if (info) info.textContent = t("{n} 件中 {i} 件目", { n: m.length, i: i + 1 });
}
function gridReplaced(col, v) {
  const q = G.findQ || "";
  if (G.findWhole) return gridNorm(col, G.findR || "");
  const re = new RegExp(q.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), G.findCase ? "g" : "gi");
  return gridNorm(col, gridLabelRawVal(col, v).replace(re, () => G.findR || ""));
}
function gridLabelRawVal(col, v) { return col.kind === "secret" || GRID_FREE[col.kind] ? (v || "") : gridLabel(col, v); }
function gridReplaceOne() {
  const m = gridMatches();
  const hit = m.find(([r, c]) => r === G.sel.fr && c === G.sel.fc);
  if (hit) {
    const col = G.vc[hit[1]], row = gridRowAt(hit[0]);
    gridMutate(() => { row.cells[col.key] = gridReplaced(col, row.cells[col.key]); });
  }
  gridFindStep(1);
}
function gridReplaceAll() {
  const m = gridMatches();
  if (!m.length) { document.getElementById("gf-info").textContent = t("見つかりません"); return; }
  const cells = m.map(([r, c]) => [gridRowAt(r), G.vc[c]]);
  gridMutate(() => cells.forEach(([row, col]) => { row.cells[col.key] = gridReplaced(col, row.cells[col.key]); }));
  toast(t("{n} 件を置換しました", { n: m.length }), "ok");
}

// ---- columns ----
function gridColsDialog() {
  const own = G.cols.filter(c => !c.hop);
  const hops = [...new Set(G.cols.filter(c => c.hop).map(c => c.hop))];
  const node = h(`<div>
    <h3>${esc(t("列の表示"))}</h3>
    <p class="muted" style="font-size:13px;margin-top:-6px">${esc(t("表に出す列を選びます（この PC に記憶します）。隠した列の値はそのまま保存されます。"))}</p>
    <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:4px 12px;font-size:13px">
      ${own.map(c => `<label style="display:flex;gap:6px;align-items:center"><input type="checkbox" data-col="${esc(c.key)}" ${G.vis.has(c.key) ? "checked" : ""} ${c.key === "name" ? "disabled" : ""} style="width:auto">${esc(gridColTitle(c))}</label>`).join("")}
    </div>
    <div class="section-label" style="margin-top:12px">${esc(t("踏み台（段ごとにまとめて）"))}</div>
    <div class="row-inline" style="gap:14px;font-size:13px;flex-wrap:wrap">
      ${hops.map(n => `<label style="display:flex;gap:6px;align-items:center"><input type="checkbox" data-hop="${n}" ${G.cols.some(c => c.hop === n && G.vis.has(c.key)) ? "checked" : ""} style="width:auto">${esc(t("踏み台{n}", { n }))}</label>`).join("")}
    </div>
    <div class="row-inline" style="gap:6px;margin-top:12px">
      <button class="btn sm" id="gv-basic" type="button">${esc(t("基本の列だけ"))}</button>
      <button class="btn sm" id="gv-all" type="button">${esc(t("すべて表示"))}</button>
    </div>
    <div class="modal-actions">
      <button class="btn" id="gv-ok" type="button">${esc(t("閉じる"))}</button>
    </div>
  </div>`);
  openModal(node, "mid");
  const apply = () => {
    const vis = new Set(["name"]);
    node.querySelectorAll("[data-col]").forEach(b => { if (b.checked) vis.add(b.dataset.col); });
    node.querySelectorAll("[data-hop]").forEach(b => { if (b.checked) G.cols.filter(c => c.hop === +b.dataset.hop).forEach(c => vis.add(c.key)); });
    G.vis = vis;
    gridSaveVis();
    renderGrid(true);
  };
  node.querySelectorAll("input").forEach(b => b.onchange = apply);
  node.querySelector("#gv-basic").onclick = () => {
    const basic = ["name", "group", "site", "role", "host", "conn", "port", "osType", "commandSet", "enabled"];
    node.querySelectorAll("[data-col]").forEach(b => { b.checked = basic.includes(b.dataset.col); });
    node.querySelectorAll("[data-hop]").forEach(b => { b.checked = false; });
    apply();
  };
  node.querySelector("#gv-all").onclick = () => { node.querySelectorAll("input").forEach(b => { b.checked = true; }); apply(); };
  node.querySelector("#gv-ok").onclick = closeModal;
}

// ---- column header menu: sort, filter by value, hide (the ▾, or right-click) ----
function gridHeaderMenu(key, anchor) {
  document.querySelectorAll(".g-hpop").forEach(x => x.remove());
  const col = G.cols.find(c => c.key === key);
  if (!col) return;
  const counts = new Map();
  G.rows.forEach(r => { if (gridIsBlank(r)) return; const l = gridLabel(col, r.cells[key]); counts.set(l, (counts.get(l) || 0) + 1); });
  const vals = [...counts.keys()].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));
  const cur = G.colFilter[key];
  const pop = h(`<div class="g-hpop">
    <button class="g-hi" data-a="asc">${esc(t("昇順で並べ替え"))}</button>
    <button class="g-hi" data-a="desc">${esc(t("降順で並べ替え"))}</button>
    ${G.sort.key ? `<button class="g-hi" data-a="nosort">${esc(t("並べ替えを解除"))}</button>` : ""}
    <div class="g-hsep"></div>
    <div class="g-hlabel">${esc(t("値で絞り込み"))}</div>
    <input class="g-hq" placeholder="${esc(t("値を探す"))}">
    <label class="g-hv"><input type="checkbox" data-all checked style="width:auto"> ${esc(t("（すべて選択）"))}</label>
    <div class="g-hlist">${vals.map(v => `<label class="g-hv"><input type="checkbox" data-v="${esc(v)}" ${!cur || cur.has(v) ? "checked" : ""} style="width:auto"> ${esc(v === "" ? t("（空白）") : v)} <span class="muted">${counts.get(v)}</span></label>`).join("")}</div>
    <div class="row-inline" style="gap:6px;margin-top:6px">
      <button class="btn sm primary" data-a="ok">${esc(t("絞り込む"))}</button>
      <button class="btn sm" data-a="clear">${esc(t("絞り込みを解除"))}</button>
    </div>
    <div class="g-hsep"></div>
    ${key !== "name" ? `<button class="g-hi" data-a="hide">${esc(t("この列を隠す"))}</button>` : ""}
  </div>`);
  document.body.appendChild(pop);
  const r = anchor.getBoundingClientRect();
  pop.style.left = Math.min(r.left, window.innerWidth - pop.offsetWidth - 8) + "px";
  pop.style.top = Math.min(r.bottom + 2, window.innerHeight - pop.offsetHeight - 8) + "px";
  const boxes = () => [...pop.querySelectorAll("[data-v]")];
  const all = pop.querySelector("[data-all]");
  all.checked = boxes().every(b => b.checked);
  all.onchange = () => boxes().forEach(b => { if (b.parentElement.style.display !== "none") b.checked = all.checked; });
  pop.querySelector(".g-hq").oninput = ev => {
    const q = ev.target.value.toLowerCase();
    boxes().forEach(b => { b.parentElement.style.display = b.dataset.v.toLowerCase().includes(q) ? "" : "none"; });
  };
  const close = () => { pop.remove(); document.removeEventListener("mousedown", outside, true); if (G && G.wrap) G.wrap.focus({ preventScroll: true }); };
  const outside = ev => { if (!pop.contains(ev.target)) close(); };
  setTimeout(() => document.addEventListener("mousedown", outside, true), 0);
  pop.onkeydown = ev => { if (ev.key === "Escape") { ev.preventDefault(); ev.stopPropagation(); close(); } };
  pop.querySelectorAll("[data-a]").forEach(b => b.onclick = () => {
    const a = b.dataset.a;
    if (a === "asc" || a === "desc") { G.sort = { key, dir: a === "asc" ? 1 : -1 }; gridResort(); }
    else if (a === "nosort") { G.sort = { key: "", dir: 1 }; G.sortRank = null; }
    else if (a === "ok") {
      const on = boxes().filter(x => x.checked).map(x => x.dataset.v);
      if (on.length === vals.length) delete G.colFilter[key]; else G.colFilter[key] = new Set(on);
    } else if (a === "clear") delete G.colFilter[key];
    else if (a === "hide") { G.vis.delete(key); gridSaveVis(); }
    close();
    gridRefilter();
    renderGrid(true);
  });
  pop.querySelector(".g-hq").focus();
}
// Double-click on a column's edge: as wide as its widest value.
function gridAutoFit(c) {
  const col = G.vc[c];
  if (!col) return;
  const span = document.createElement("span");
  span.style.cssText = "position:absolute;visibility:hidden;white-space:nowrap;font-size:13px";
  document.body.appendChild(span);
  let w = 0;
  const texts = [gridColTitle(col)].concat(G.view.map(i => gridLabel(col, G.rows[i].cells[col.key])));
  texts.forEach(x => { span.textContent = x; w = Math.max(w, span.offsetWidth); });
  span.remove();
  w = Math.max(50, Math.min(600, w + 34));
  gridSetColW(col.key, w);
  renderGrid(true);
}
// Double-click on the fill handle: down to the table's last device row.
function gridFillToEnd() {
  const g = gridRange();
  let last = -1;
  G.view.forEach((i, r) => { if (!gridIsBlank(G.rows[i])) last = r; });
  if (last <= g.r1) return;
  gridFill(g, { dir: "down", t: { r0: g.r1 + 1, r1: last, c0: g.c0, c1: g.c1 } }, false);
}

// ---- wiring ----
let gridDrag = null; // {mode: "cell"|"row"|"col"|"fill", ...}
function gridCellAt(x, y) {
  const el = document.elementFromPoint(x, y);
  if (!el) return null;
  const td = el.closest("#g-body td[data-r]");
  if (td) return { r: +td.dataset.r, c: +td.dataset.c };
  const rh = el.closest("#g-body th.g-rh");
  if (rh) return { r: +rh.dataset.r, c: null };
  const ch = el.closest("#g-table thead th[data-c]");
  if (ch) return { r: null, c: +ch.dataset.c };
  return null;
}
function gridWire(root) {
  const $g = id => document.getElementById(id);
  $g("g-close").onclick = () => gridClose().catch(e => toast(terr(e), "err"));
  $g("g-close2").onclick = () => gridClose().catch(e => toast(terr(e), "err"));
  $g("g-ins-above").onclick = () => gridInsertAt(false);
  $g("g-ins-below").onclick = () => gridInsertAt(true);
  $g("g-copyrows").onclick = () => gridCopyRows();
  $g("g-pasterows").onclick = e => gridInsertCopied(e.shiftKey);
  $g("g-duprows").onclick = gridDuplicateRows;
  $g("g-up").onclick = () => gridMoveRows(-1);
  $g("g-down").onclick = () => gridMoveRows(1);
  $g("g-delrows").onclick = gridDeleteRows;
  // Toolbar buttons act on the table's selection: give the focus back.
  root.querySelectorAll(".g-tools button, #g-undo, #g-redo").forEach(b => b.addEventListener("click", () => { if (G && G.wrap) G.wrap.focus({ preventScroll: true }); }));
  $g("g-undo").onclick = () => gridUndo(false);
  $g("g-redo").onclick = () => gridUndo(true);
  $g("g-find").onclick = () => gridFind(true);
  $g("g-cols").onclick = gridColsDialog;
  // The filters match what is shown (●●●● while hidden): worked out again.
  $g("g-pw").onclick = () => { G.showPw = !G.showPw; gridRefilter(); renderGrid(true); };
  $g("g-save").onclick = () => gridSave().catch(e => toast(t("保存失敗") + ": " + terr(e), "err"));
  $g("g-addrow").onclick = gridAddRow;
  $g("g-discard").onclick = async () => {
    if (G.busy) { toast(t("保存中です。終わるまでお待ちください"), "err"); return; }
    if (!gridChangeCount()) { toast(t("破棄する変更はありません"), "ok"); return; }
    if (!(await uiConfirm({ title: t("下書きを破棄"), message: esc(t("この表の保存していない変更（{n} 行）をすべて取り消し、保存済みの内容に戻します。", { n: gridChangeCount() })), okLabel: t("破棄する"), danger: true }))) return;
    await App().DiscardGridDraft(G.scope);
    delete GRID_COUNTS[G.scope];
    const scope = G.scope;
    G = null;
    await openGrid(scope);
  };
  const filt = $g("g-filter");
  filt.oninput = () => { G.filter = filt.value; gridRefilter(); gridView(); gridRenderBody(); };
  filt.onkeydown = e => { if (e.key === "Enter" || e.key === "Escape") { e.preventDefault(); G.wrap.focus(); } };
  root.querySelectorAll(".g-hmenu").forEach(b => b.onclick = e => { e.stopPropagation(); gridHeaderMenu(b.dataset.hmenu, b); });
  const wrap = G.wrap;
  wrap.addEventListener("scroll", () => { if (!gridDrag) gridPaintSel(); });
  const glist = document.getElementById("g-list");
  glist.addEventListener("keydown", gridListKey);
  glist.addEventListener("blur", () => setTimeout(() => { if (G && G.list && document.activeElement !== glist) gridCloseList(); }, 0));
  wrap.addEventListener("mousedown", e => {
    if (e.target.closest(".g-editor") || e.target.closest(".g-hmenu")) return;
    if (e.button !== 0) return;
    const opt = e.target.closest(".g-opt");
    if (opt) { e.preventDefault(); gridPickList(+opt.dataset.i); return; }
    if (e.target.closest("#g-list")) { e.preventDefault(); return; }
    if (e.target.id === "g-dd") {
      e.preventDefault();
      const open = !!G.list;
      gridCloseList();
      if (!open) gridOpenList(); else G.wrap.focus({ preventScroll: true });
      return;
    }
    if (G.list) gridCloseList();
    if (G.edit) gridCommitEdit();
    const ins = e.target.closest(".g-ins");
    if (ins) { e.preventDefault(); gridInsertBelowRow(+ins.dataset.ins); return; }
    const rz = e.target.closest(".g-rz");
    if (rz) {
      const c = +rz.dataset.rz;
      gridDrag = { mode: "rz", c, x: e.clientX, w: document.querySelectorAll("#g-table col")[c + 1].getBoundingClientRect().width || gridColW(G.vc[c]) };
      e.preventDefault(); e.stopPropagation();
      return;
    }
    if (e.target.id === "g-fill") {
      gridDrag = { mode: "fill", g: gridRange(), ft: null };
      e.preventDefault();
      return;
    }
    const corner = e.target.closest(".g-corner");
    const at = corner ? null : gridCellAt(e.clientX, e.clientY);
    const R = G.view.length - 1, C = G.vc.length - 1;
    if (corner) { gridSelect(0, 0, R, C); }
    else if (at && at.r != null && at.c != null) {
      if (e.shiftKey) { G.sel.fr = at.r; G.sel.fc = at.c; gridPaintSel(); }
      else gridSelect(at.r, at.c);
      gridDrag = { mode: "cell" };
    } else if (at && at.r != null) {
      if (e.shiftKey) { G.sel.fr = at.r; G.sel.ac = 0; G.sel.fc = C; gridPaintSel(); }
      else gridSelect(at.r, 0, at.r, C);
      gridDrag = { mode: "row" };
    } else if (at && at.c != null) {
      if (e.shiftKey) { G.sel.fc = at.c; G.sel.ar = 0; G.sel.fr = R; gridPaintSel(); }
      else gridSelect(0, at.c, R, at.c);
      gridDrag = { mode: "col" };
    } else return;
    wrap.focus({ preventScroll: true });
    e.preventDefault();
  });
  wrap.addEventListener("dblclick", e => {
    if (e.target.id === "g-fill") { gridFillToEnd(); return; }
    const rz = e.target.closest(".g-rz");
    if (rz) { gridAutoFit(+rz.dataset.rz); return; }
    const at = gridCellAt(e.clientX, e.clientY);
    if (at && at.r != null && at.c != null) {
      gridSelect(at.r, at.c);
      if (gridChoices(G.vc[at.c])) gridOpenList(); else gridBeginEdit(null);
    }
  });
  wrap.addEventListener("contextmenu", e => {
    e.preventDefault(); e.stopPropagation();
    closeCtxMenu();
    if (G.edit) gridCommitEdit();
    const at = gridCellAt(e.clientX, e.clientY);
    if (at && at.r == null && at.c != null) { gridHeaderMenu(G.vc[at.c].key, e.target.closest("th")); return; }
    const g = gridRange();
    if (at && at.r != null && at.c != null && !(at.r >= g.r0 && at.r <= g.r1 && at.c >= g.c0 && at.c <= g.c1)) gridSelect(at.r, at.c);
    else if (at && at.r != null && at.c == null && !(at.r >= g.r0 && at.r <= g.r1)) gridSelect(at.r, 0, at.r, G.vc.length - 1);
    const items = [
      [t("切り取り（Ctrl+X）"), () => gridCopy(true)],
      [t("コピー（Ctrl+C）"), () => gridCopy(false)],
      [t("貼り付け（Ctrl+V）"), () => gridPaste()],
      [t("行をコピー"), () => gridCopyRows()],
      [t("コピーした行を上に挿入"), () => gridInsertCopied(false)],
      [t("コピーした行を下に挿入"), () => gridInsertCopied(true)],
      [t("上に行を挿入（Ctrl+Shift++）"), () => gridInsertAt(false)],
      [t("下に行を挿入"), () => gridInsertAt(true)],
      [t("行を複製"), gridDuplicateRows],
      [t("行を上へ移動（Alt+↑）"), () => gridMoveRows(-1)],
      [t("行を下へ移動（Alt+↓）"), () => gridMoveRows(1)],
      [t("行を削除（Ctrl+－）"), gridDeleteRows],
      [t("セルを空にする（Delete）"), gridClear],
      [t("上の値を下へコピー（Ctrl+D）"), () => gridFillDown(false)],
      [t("左の値を右へコピー（Ctrl+R）"), () => gridFillDown(true)],
    ];
    showCtxMenu(items, e);
  });
  wrap.addEventListener("keydown", gridKey);
}
// The fill handle and the copy frame are placed in pixels: redrawn when the
// window changes size. A dialog closing gives the keys back to the table.
window.addEventListener("resize", () => { if (G && G.mounted && document.getElementById("g-wrap")) gridPaintSel(); });
document.addEventListener("modal-closed", () => {
  setTimeout(() => { if (gridOnScreen() && !G.edit && !document.querySelector(".g-findbar:not(.hidden) input:focus")) G.wrap.focus({ preventScroll: true }); }, 0);
});
document.addEventListener("mousemove", e => {
  if (!gridDrag || !G) return;
  if (gridDrag.mode === "rz") {
    const w = Math.max(40, Math.min(600, gridDrag.w + e.clientX - gridDrag.x));
    const col = document.querySelectorAll("#g-table col")[gridDrag.c + 1];
    if (col) col.style.width = w + "px";
    gridDrag.nw = w;
    return;
  }
  const w = G.wrap.getBoundingClientRect();
  // Dragging past the edge scrolls, as in Excel.
  if (e.clientY > w.bottom - 16) G.wrap.scrollTop += 16;
  else if (e.clientY < w.top + 30) G.wrap.scrollTop -= 16;
  if (e.clientX > w.right - 16) G.wrap.scrollLeft += 24;
  else if (e.clientX < w.left + 50) G.wrap.scrollLeft -= 24;
  const at = gridCellAt(Math.min(Math.max(e.clientX, w.left + 50), w.right - 4), Math.min(Math.max(e.clientY, w.top + 32), w.bottom - 4)) || {};
  if (gridDrag.mode === "fill") {
    // Below the last row the fill goes on into new rows, as in Excel.
    const last = G.tds.length && G.tds[G.tds.length - 1][0];
    if (last && gridDrag.g.r1 === G.view.length - 1 && !G.sort.key) {
      const lr = last.getBoundingClientRect();
      if (e.clientY > lr.bottom) {
        const extra = Math.min(500, Math.ceil((e.clientY - lr.bottom) / Math.max(lr.height, 1)));
        const ft = { dir: "down", t: { r0: gridDrag.g.r1 + 1, r1: gridDrag.g.r1 + extra, c0: gridDrag.g.c0, c1: gridDrag.g.c1 } };
        gridDrag.ft = ft;
        gridPaintSel(ft.t);
        gridFillHint(t("下に {n} 行を追加して埋めます", { n: extra }));
        return;
      }
    }
    gridFillHint("");
    if (at.r == null || at.c == null) return;
    const ft = gridFillTarget(gridDrag.g, at.r, at.c);
    gridDrag.ft = ft;
    gridPaintSel(ft ? ft.t : null);
    return;
  }
  if (at.r == null && at.c == null) return;
  if (gridDrag.mode === "cell" && at.r != null && at.c != null) { G.sel.fr = at.r; G.sel.fc = at.c; }
  else if (gridDrag.mode === "row" && at.r != null) G.sel.fr = at.r;
  else if (gridDrag.mode === "col" && at.c != null) G.sel.fc = at.c;
  else return;
  gridPaintSel();
});
document.addEventListener("mouseup", e => {
  if (!gridDrag) return;
  const d = gridDrag;
  gridDrag = null;
  if (d.mode === "rz" && G) {
    if (d.nw) gridSetColW(G.vc[d.c].key, d.nw);
    gridPaintSel();
    return;
  }
  if (d.mode === "fill" && G) {
    gridFillHint("");
    if (d.ft) gridFill(d.g, d.ft, e.ctrlKey || e.metaKey);
    else gridPaintSel();
  }
});
// While the table is open, keys that would reload the page or open the
// browser's own find bar are the table's.
document.addEventListener("keydown", e => {
  if (!gridOnScreen()) return;
  if ((e.ctrlKey || e.metaKey) && ["r", "f", "h", "p"].includes(e.key.toLowerCase()) && !e.target.closest("#g-wrap")) {
    e.preventDefault();
    if (e.key.toLowerCase() === "f" || e.key.toLowerCase() === "h") gridFind(e.key.toLowerCase() === "h");
  }
  if (e.key === "F5") e.preventDefault();
}, true);

// gridJumpTarget is where Ctrl+arrow lands, as in Excel: from a filled cell
// with a filled neighbour, the last filled cell of that run; otherwise the
// next filled cell, or the edge of the table.
function gridJumpTarget(r, c, dr, dc) {
  const R = G.view.length - 1, C = G.vc.length - 1;
  const inside = (y, x) => y >= 0 && y <= R && x >= 0 && x <= C;
  const filled = (y, x) => !!(gridRowAt(y).cells[G.vc[x].key] || "");
  if (!inside(r + dr, c + dc)) return [r, c];
  if (filled(r, c) && filled(r + dr, c + dc)) {
    while (inside(r + dr, c + dc) && filled(r + dr, c + dc)) { r += dr; c += dc; }
    return [r, c];
  }
  r += dr; c += dc;
  while (inside(r + dr, c + dc) && !filled(r, c)) { r += dr; c += dc; }
  return [r, c];
}

function gridKey(e) {
  if (G.edit || !gridOnScreen()) return;
  const k = e.key, ctrl = e.ctrlKey || e.metaKey, lk = k.toLowerCase();
  const R = G.view.length - 1, C = G.vc.length - 1;
  const done = () => { e.preventDefault(); e.stopPropagation(); };
  if (ctrl) {
    if (lk === "c") { done(); gridCopy(false); return; }
    if (lk === "x") { done(); gridCopy(true); return; }
    if (lk === "v") { done(); gridPaste(); return; }
    if (lk === "z" && e.shiftKey) { done(); gridUndo(true); return; }
    if (lk === "z") { done(); gridUndo(false); return; }
    if (lk === "y") { done(); gridUndo(true); return; }
    if (lk === "d") { done(); gridFillDown(false); return; }
    if (lk === "r") { done(); gridFillDown(true); return; }
    if (lk === "a") { done(); gridSelect(0, 0, R, C); return; }
    if (lk === "f") { done(); gridFind(false); return; }
    if (lk === "h") { done(); gridFind(true); return; }
    if (lk === "s") { done(); gridSave().catch(err => toast(t("保存失敗") + ": " + terr(err), "err")); return; }
    // Ctrl+Shift++ with rows copied inserts them, as Excel does.
    if (k === "+" || k === "=" || k === ";") { done(); if (e.shiftKey && G.clip && G.clip.rows) gridInsertCopied(false); else gridInsertAt(false); return; }
    if (k === " ") { done(); gridSelect(0, G.sel.ac, G.view.length - 1, G.sel.fc); return; }
    if (k === "-") { done(); gridDeleteRows(); return; }
    if (k === "Home") { done(); e.shiftKey ? gridMove(-G.sel.fr, -G.sel.fc, true) : gridSelect(0, 0); return; }
    if (k === "End") {
      done();
      let last = 0;
      G.view.forEach((i, r) => { if (!gridIsBlank(G.rows[i])) last = r; });
      e.shiftKey ? gridMove(last - G.sel.fr, C - G.sel.fc, true) : gridSelect(last, C);
      return;
    }
    if (GRID_ARROWS[k]) {
      done();
      const [dr, dc] = GRID_ARROWS[k];
      const [tr, tc] = gridJumpTarget(G.sel.fr, G.sel.fc, dr, dc);
      gridMove(tr - G.sel.fr, tc - G.sel.fc, e.shiftKey);
      return;
    }
    return;
  }
  if (e.altKey && (k === "ArrowUp" || k === "ArrowDown")) { done(); gridMoveRows(k === "ArrowUp" ? -1 : 1); return; }
  if (k === " " && e.shiftKey) { done(); gridSelect(G.sel.ar, 0, G.sel.fr, G.vc.length - 1); return; }
  const arrows = { ArrowUp: [-1, 0], ArrowDown: [1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1] };
  if (arrows[k]) { done(); gridMove(arrows[k][0], arrows[k][1], e.shiftKey); return; }
  if (k === "Tab") { done(); gridMove(0, e.shiftKey ? -1 : 1); return; }
  if (k === "Enter") { done(); gridMove(e.shiftKey ? -1 : 1, 0); return; }
  if (k === "PageDown") { done(); gridMove(20, 0, e.shiftKey); return; }
  if (k === "PageUp") { done(); gridMove(-20, 0, e.shiftKey); return; }
  if (k === "Home") { done(); gridMove(0, -G.sel.fc, e.shiftKey); return; }
  if (k === "End") { done(); gridMove(0, C - G.sel.fc, e.shiftKey); return; }
  if (k === "F2") { done(); gridBeginEdit(null); return; }
  if ((k === " " || k === "F4") && !e.shiftKey && gridChoices(G.vc[G.sel.fc])) { done(); gridOpenList(); return; }
  if (k === "Delete") { done(); gridClear(); return; }
  if (k === "Backspace") { done(); gridBeginEdit(""); return; }
  if (k === "Escape") { done(); G.clip = null; gridSelect(G.sel.fr, G.sel.fc); return; }
  // Typing starts editing: the editor takes the focus during this keydown,
  // so the key itself (or the IME's composition, key "Process") lands in it.
  if ((k.length === 1 && !e.altKey) || k === "Process") { e.stopPropagation(); gridBeginEdit(""); return; }
}
