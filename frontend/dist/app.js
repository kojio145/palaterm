// PalaTerm frontend. Talks to the Go backend via window.go.main.App.* and
// receives live run progress through window.runtime.EventsOn.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

const App = () => window.go.main.App;
const rt = () => window.runtime;

const APP_VERSION = "1.9.16";
const APP_AUTHOR = "KJO";

// ---- small helpers ----
function h(html) { const tpl = document.createElement("template"); tpl.innerHTML = html.trim(); return tpl.content.firstChild; }
function esc(s) { return String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function $(sel) { return document.querySelector(sel); }

let toastTimer;
// An error stays long enough to be read (a validation message can be a
// sentence or two) and any toast goes on click.
function toast(msg, kind) {
  const el = $("#toast");
  el.textContent = msg;
  el.className = "toast " + (kind || "");
  el.onclick = () => el.classList.add("hidden");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.add("hidden"), kind === "err" ? 9000 : 3200);
}

// An input that keeps a form from being saved is shown at the field itself:
// the field gets the focus and a red outline, and the reason is written
// under it (not only in the toast at the bottom edge). Both clear as soon as
// the field is edited.
function fieldError(inp, msg) {
  if (!inp) { toast(msg, "err"); return; }
  const box = inp.closest(".field") || inp.parentElement;
  let el = box.querySelector(":scope > .field-err");
  if (!el) { el = document.createElement("div"); el.className = "field-err"; box.appendChild(el); }
  el.textContent = msg;
  inp.classList.add("field-bad");
  if (!inp._fieldErrWired) {
    inp._fieldErrWired = true;
    inp.addEventListener("input", () => {
      inp.classList.remove("field-bad");
      const e2 = box.querySelector(":scope > .field-err");
      if (e2) e2.remove();
    });
  }
  inp.focus();
  try { inp.scrollIntoView({ block: "nearest" }); } catch (e) { }
}

// Styled in-app confirm dialog (replaces the native "wails.localhost" confirm).
// message may contain simple markup; escape dynamic values with esc() upstream.
// Enter confirms a plain question (the OK button has the focus); a danger
// one starts on キャンセル so Enter never deletes anything.
function uiConfirm({ title = t("確認"), message = "", okLabel = "OK", cancelLabel = t("キャンセル"), danger = false } = {}) {
  return new Promise(resolve => {
    const node = h(`<div>
      <h3>${esc(title)}</h3>
      <p style="font-size:14px;line-height:1.8;margin:4px 0 10px">${message}</p>
      <div class="modal-actions">
        <button class="btn" id="uc-cancel" ${danger ? "autofocus" : ""}>${esc(cancelLabel)}</button>
        <button class="btn ${danger ? "danger" : "primary"}" id="uc-ok" ${danger ? "" : "autofocus"}>${esc(okLabel)}</button>
      </div>
    </div>`);
    openModal(node, "mid");
    node.querySelector("#uc-cancel").onclick = () => { closeModal(); resolve(false); };
    node.querySelector("#uc-ok").onclick = () => { closeModal(); resolve(true); };
  });
}

// A dialog is open (an editor, a confirmation) — its contents are not saved.
function modalOpen() { return !$("#modal-overlay").classList.contains("hidden"); }

function openModal(node, sizeClass) {
  const m = $("#modal");
  m.className = "modal" + (sizeClass ? " " + sizeClass : "");
  m.innerHTML = ""; m.appendChild(node);
  $("#modal-overlay").classList.remove("hidden");
  // The autofocus attribute only works once per document in Chromium; the
  // dialogs come and go, so the focus is placed by hand — unless the
  // caller already put it somewhere inside.
  requestAnimationFrame(() => {
    const f = node.querySelector("[autofocus]");
    if (f && !node.contains(document.activeElement)) { f.focus(); if (f.select && f.tagName === "INPUT") f.select(); }
  });
}
// Closing also drops a calendar left open by a date picker in the dialog
// (it lives on <body>), and tells the tabs: a list that skipped redrawing
// while the dialog was open (the 予約 tab) catches up.
function closeModal() {
  $("#modal-overlay").classList.add("hidden");
  document.querySelectorAll(".dp-pop").forEach(p => p.remove());
  document.dispatchEvent(new Event("modal-closed"));
}

// Esc closes the open dialog the way its own キャンセル button does (so an
// editor's "discard changes?" check still runs). Not while a small window
// sits on top of it (it handles Esc itself) or the copy menu is open.
document.addEventListener("keydown", e => {
  if (e.defaultPrevented || document.querySelector(".sub-overlay:not(.hidden)") || $("#modal-overlay").classList.contains("hidden")) return;
  // Ctrl+Enter saves (the dialog's primary button): the editors have many
  // fields, and Enter alone belongs to the field it is typed in.
  if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && !e.isComposing) {
    // A key held down repeats: one save, not one per repeat.
    if (e.repeat) { e.preventDefault(); return; }
    const ok = document.querySelector("#modal .modal-actions button.primary:not([disabled])");
    if (ok) { e.preventDefault(); ok.click(); }
    return;
  }
  if (e.key !== "Escape") return;
  const btn = [...document.querySelectorAll("#modal .modal-actions button")]
    .find(b => !b.classList.contains("primary") && !b.classList.contains("danger") && !b.disabled);
  if (btn) { e.preventDefault(); btn.click(); }
});

// Tab / Shift+Tab go round inside the open dialog — or the small window on
// top of it — instead of walking out onto the page behind (which the
// overlay hides, so the focus seemed lost). A calendar popped out of a
// date picker keeps its own keys.
function tabStops(box) {
  return [...box.querySelectorAll('a[href], button, input, select, textarea, summary, [tabindex], [contenteditable="true"]')]
    .filter(el => !el.disabled && el.tabIndex >= 0 && el.type !== "hidden" && !el.closest("[inert]")
      && el.getClientRects().length && getComputedStyle(el).visibility !== "hidden");
}
document.addEventListener("keydown", e => {
  if (e.key !== "Tab" || e.defaultPrevented || e.ctrlKey || e.altKey || e.metaKey || e.isComposing) return;
  const subs = [...document.querySelectorAll(".sub-overlay:not(.hidden)")];
  const ov = $("#modal-overlay");
  const box = subs.length ? subs[subs.length - 1] : ov && !ov.classList.contains("hidden") ? $("#modal") : null;
  if (!box) return;
  const ae = document.activeElement;
  if (ae && ae.closest && ae.closest(".dp-pop")) return;
  const stops = tabStops(box);
  if (!stops.length) { e.preventDefault(); return; }
  const first = stops[0], last = stops[stops.length - 1];
  const go = el => { e.preventDefault(); el.focus(); };
  if (!ae || !box.contains(ae)) { go(e.shiftKey ? last : first); return; }
  if (stops.includes(ae)) {
    if (e.shiftKey && ae === first) go(last);
    else if (!e.shiftKey && ae === last) go(first);
    return;
  }
  // The focus sits on something inside that is not a stop itself (the
  // dialog body after a click): go round when no stop lies that way.
  const after = stops.some(el => ae.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING);
  const before = stops.some(el => ae.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_PRECEDING);
  if (!e.shiftKey && !after) go(first);
  else if (e.shiftKey && !before) go(last);
});

// subConfirm asks a yes/no question in a small window over the open dialog
// (uiConfirm would replace the dialog itself). Resolves true for ok.
function subConfirm(message, okLabel, cancelLabel, okClass) {
  return new Promise(resolve => {
    const ov = h(`<div class="sub-overlay"><div class="sub-modal" style="width:min(440px,90vw)">
      <div style="font-size:14px;line-height:1.7">${message}</div>
      <div class="modal-actions">
        <button class="btn" id="sc-no" type="button">${esc(cancelLabel || t("編集に戻る"))}</button>
        <button class="btn ${okClass || "danger"}" id="sc-ok" type="button">${esc(okLabel)}</button>
      </div></div></div>`);
    document.body.appendChild(ov);
    const done = v => { ov.remove(); document.removeEventListener("keydown", onKey, true); resolve(v); };
    const onKey = e => { if (e.key === "Escape") { e.preventDefault(); e.stopImmediatePropagation(); done(false); } };
    document.addEventListener("keydown", onKey, true);
    ov.querySelector("#sc-no").onclick = () => done(false);
    ov.querySelector("#sc-ok").onclick = () => done(true);
    ov.querySelector("#sc-no").focus();
  });
}

// guardDiscard makes an editor's cancel button (and so Esc) ask before
// throwing away edits: the form's values are compared with what they were
// when the editor finished opening. The date pickers' buttons count too;
// fields marked data-guard-skip (a filter, a list redrawn as it is filtered)
// do not, and extra() adds what the editor keeps outside its fields;
// after() runs once the editor is closed (a promise to resolve, say).
function guardDiscard(node, cancelSel, extra, after) {
  const snap = () => JSON.stringify([[...node.querySelectorAll("input:not([data-guard-skip]), select:not([data-guard-skip]), textarea:not([data-guard-skip])")]
    .map(el => el.type === "checkbox" || el.type === "radio" ? el.checked : el.value),
    [...node.querySelectorAll(".dp-val")].map(el => el.textContent), extra ? extra() : null]);
  const start = snap();
  node.querySelector(cancelSel).onclick = async () => {
    if (snap() !== start && !(await subConfirm(esc(t("編集した内容は保存されていません。破棄して閉じますか？")), t("破棄して閉じる")))) return;
    closeModal();
    if (after) after();
  };
}

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
    // Natural order for text: 10.0.0.9 before 10.0.0.10, sw2 before sw10
    // (the table editor sorts the same way).
    const val = it => { const x = sortVal(it, v.sort.key); return typeof x === "number" ? x : String(x ?? "").toLowerCase(); };
    out = out.slice().sort((a, b) => {
      const x = val(a), y = val(b);
      const c = typeof x === "number" && typeof y === "number" ? (x < y ? -1 : x > y ? 1 : 0) : String(x).localeCompare(String(y), undefined, { numeric: true });
      return c * v.sort.dir;
    });
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
  return el && el.id && el.id.startsWith("lv-text-") ? { id: el.id, sel: boxSelection(el) } : null;
}
// A filter box that re-renders its tab (and so itself) on every keystroke:
// while an IME is composing, re-rendering would cut the composition off
// (era → えrあ), so the change is applied once the text is committed.
// The redraw waits for a pause in the typing (150 ms): a list of a thousand
// devices takes about a second to draw, once per word instead of per key.
// onText (optional) keeps the filter's state up to date on every key, so a
// redraw from elsewhere meanwhile (a batch's progress, a schedule's log)
// draws the box with what was typed rather than the text of the last redraw.
let imeComposing = false;
// A text box's selection — its start, end and direction, not just the caret —
// kept across a redraw that rebuilds the box: Ctrl+A then typing replaces
// the whole text even when a batch redraws the tab in between.
function boxSelection(el) {
  try { return [el.selectionStart, el.selectionEnd, el.selectionDirection]; } catch (e) { return null; }
}
function restoreBoxSelection(el, sel) {
  if (!sel) return;
  try { el.setSelectionRange(sel[0], sel[1], sel[2] || "none"); } catch (e) { }
}
function wireFilterInput(el, onChange, onText) {
  let timer = null;
  const fire = () => { timer = null; onChange(); };
  const soon = () => { if (onText) onText(el.value); clearTimeout(timer); timer = setTimeout(fire, 150); };
  el.addEventListener("compositionstart", () => { imeComposing = true; clearTimeout(timer); timer = null; });
  el.addEventListener("compositionend", () => { imeComposing = false; soon(); });
  el.addEventListener("input", e => { if (!e.isComposing && !imeComposing) soon(); });
  el.addEventListener("blur", () => {
    imeComposing = false;
    // Leaving the box draws what was typed now. A box already replaced by
    // a redraw from elsewhere needs none: that redraw used the state.
    if (timer) { clearTimeout(timer); timer = null; if (el.isConnected) setTimeout(onChange, 0); }
  });
}
// ---- Redrawing a tab around the filter box ----
// A list tab is drawn from one HTML string; setting it with innerHTML made
// a new filter box on every keystroke, and putting the caret and selection
// back on the new box lost a key now and then at very fast typing. Instead
// the nodes in keep stay where they are: the old node and each of its
// ancestors are kept (their attributes brought up to date), and everything
// around them is replaced with the new HTML. keep lists {o, n, inner}: o is
// the node in root, n its counterpart in html; inner takes n's children
// into o (a panel that keeps its scroll offset) instead of leaving o's own
// (a text box, kept as it is with what is being typed). A kept node is
// never detached, so it keeps the focus, the selection and an IME
// composition. When the shapes differ, the whole of root is replaced.
function syncAttrs(o, n) {
  // An inline style the template does not declare was set at run time
  // (fitPanels' max-height on a list panel) and stays: removing it let the
  // panel grow to its full height for a moment, and the browser clamped its
  // scroll offset to 0 — the run table jumped to the top on every redraw
  // (a tick, a device event; 2026-10-24 user report).
  [...o.attributes].forEach(a => { if (!n.hasAttribute(a.name) && !(a.name === "style" && o.style.length)) o.removeAttribute(a.name); });
  [...n.attributes].forEach(a => { if (o.getAttribute(a.name) !== a.value) o.setAttribute(a.name, a.value); });
}
// One level of the walk: which new child each kept old child stands for,
// and the pairs below it. null when the shapes differ (a kept node at a
// different depth or tag, or kept children in another order — moving one
// would detach it).
function keepLevel(oParent, nParent, pairs) {
  const childOf = (anc, x) => { while (x && x.parentNode !== anc) x = x.parentNode; return x; };
  const map = new Map(), sub = new Map();
  for (const p of pairs) {
    const nc = childOf(nParent, p.n), oc = childOf(oParent, p.o);
    if (!nc || !oc || nc.nodeName !== oc.nodeName || (nc === p.n) !== (oc === p.o)) return null;
    if (map.has(nc) ? map.get(nc) !== oc : [...map.values()].includes(oc)) return null;
    map.set(nc, oc);
    sub.set(nc, (sub.get(nc) || []).concat(p));
  }
  const order = [...nParent.childNodes].filter(c => map.has(c)).map(c => map.get(c));
  const oldOrder = [...oParent.childNodes].filter(c => order.includes(c));
  if (order.some((c, i) => c !== oldOrder[i])) return null;
  return { map, sub, order };
}
function keepCheck(oParent, nParent, pairs) {
  const lv = keepLevel(oParent, nParent, pairs);
  if (!lv) return false;
  for (const [nc, oc] of lv.map) {
    const ps = lv.sub.get(nc);
    if (ps.some(p => p.o === oc) ? ps.length !== 1 : !keepCheck(oc, nc, ps)) return false;
  }
  return true;
}
function keepApply(oParent, nParent, pairs) {
  const { map, sub, order } = keepLevel(oParent, nParent, pairs);
  [...oParent.childNodes].forEach(c => { if (!order.includes(c)) c.remove(); });
  let k = 0;
  for (const nc of [...nParent.childNodes]) {
    if (map.has(nc)) k++;
    else oParent.insertBefore(nc, order[k] || null);
  }
  for (const [nc, oc] of map) {
    const ps = sub.get(nc);
    syncAttrs(oc, nc);
    if (ps[0].o !== oc) keepApply(oc, nc, ps);
    else if (ps[0].inner) {
      // The offset is put back after the swap as well: the new rows may be
      // laid out before the panel's cap is measured again.
      const [sl, st] = [oc.scrollLeft, oc.scrollTop];
      oc.replaceChildren(...nc.childNodes);
      if (sl || st) { oc.scrollLeft = sl; oc.scrollTop = st; }
    }
    // A kept text box takes the new text only when the state was changed
    // from elsewhere (the clear button): never while an IME is composing,
    // nor when it shows that text already (the user is typing it).
    else if (oc.tagName === "INPUT" && !imeComposing && oc.value !== nc.value) oc.value = nc.value;
  }
}
// keep: [{o, find, inner}] — o is the node in root, find(fragment) its
// counterpart in the new HTML.
function patchKeeping(root, html, keep) {
  const tpl = document.createElement("template");
  tpl.innerHTML = html;
  const pairs = (keep || []).filter(p => p && p.o && p.o !== root && root.contains(p.o))
    .map(p => ({ o: p.o, n: p.find(tpl.content), inner: !!p.inner })).filter(p => p.n);
  if (pairs.length && keepCheck(root, tpl.content, pairs)) keepApply(root, tpl.content, pairs);
  else root.replaceChildren(tpl.content);
}
// The filter box being typed in, as a keep entry (none when the focus is
// elsewhere: then the box is drawn anew like the rest). A list box that has
// the focus (the filter's 拠点 / 状態, a toolbar choice) is kept too: drawn
// anew while a run redraws the tab on every device event, its open list
// closed under the pointer before a value could be picked (2026-10-24 user
// report).
function keepFocusedBox(root, prefix) {
  const el = document.activeElement;
  if (!el || !root.contains(el)) return null;
  if (el.tagName === "INPUT") {
    if (!el.id || !el.id.startsWith(prefix)) return null;
    return { o: el, find: f => f.querySelector("#" + CSS.escape(el.id)) };
  }
  if (el.tagName === "SELECT") {
    if (el.id) return { o: el, find: f => f.querySelector("#" + CSS.escape(el.id)) };
    if (el.dataset.lv && el.dataset.sel) return { o: el, find: f => f.querySelector(`.lv-sel[data-lv="${CSS.escape(el.dataset.lv)}"][data-sel="${CSS.escape(el.dataset.sel)}"]`) };
  }
  return null;
}
// Draw a list tab: the filter box being typed in stays (see patchKeeping).
// With viewKey — what defines the list shown: its filter / sort state and
// its scope — the table panel stays too while that key is unchanged, so a
// redraw (a save, an inventory refresh, a tick) keeps where the user had
// scrolled it; a change of key (another filter, sort or group) starts from
// the top, as the run tab does (2026-10-24 user report: the lists jumped to
// the top on every redraw).
function renderListTab(root, html, viewKey) {
  let keepPanel = null;
  if (viewKey !== undefined) {
    const key = JSON.stringify(viewKey);
    const oldPanel = root.querySelector(".panel.scrollx");
    html = html.replace('<div class="panel scrollx">', `<div class="panel scrollx" data-view="${esc(key)}">`);
    if (oldPanel && oldPanel.dataset.view === key) keepPanel = { o: oldPanel, find: f => f.querySelector(".panel.scrollx"), inner: true };
  }
  patchKeeping(root, html, [keepPanel, keepFocusedBox(root, "lv-text-")]);
}
function listViewWire(key, root, rerender, focus) {
  const v = listView(key);
  const txt = root.querySelector(`#lv-text-${CSS.escape(key)}`);
  if (txt) {
    // A box kept across the redraw is wired already: only the redraw to
    // call is brought up to date.
    txt._lvRerender = rerender;
    if (!txt._lvWired) {
      txt._lvWired = true;
      wireFilterInput(txt, () => { v.text = txt.value; txt._lvRerender(); }, val => { v.text = val; });
    }
    if (focus && focus.id === txt.id && document.activeElement !== txt) { txt.focus(); restoreBoxSelection(txt, focus.sel); }
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

// ---- resizing the window from inside the page ----
// Windows keeps a window's resize band in the invisible frame just outside
// its visible edge; the WebView2 fills the client area right up to that
// edge, so a grab a few pixels inside a corner — where the eye aims — lands
// on the page and nothing happens. The title bar gives the top corners a
// tolerant band of their own; the bottom corners had none (2026-10-24 user
// report: the command set window would not grow from its bottom left; an
// Explorer window measured the same). Thin zones along the page's edges and
// corners resize the window through the runtime, as browsers do.
function wireEdgeResize(min) {
  const R = rt();
  if (!R || !R.WindowGetSize || !R.WindowSetSize || !R.WindowGetPosition || !R.WindowSetPosition) return;
  const E = 5, C = 14; // the edge band, the corner square
  const zones = [
    ["n", `top:0;left:${C}px;right:${C}px;height:${E}px;cursor:ns-resize`],
    ["s", `bottom:0;left:${C}px;right:${C}px;height:${E}px;cursor:ns-resize`],
    ["w", `left:0;top:${C}px;bottom:${C}px;width:${E}px;cursor:ew-resize`],
    ["e", `right:0;top:${C}px;bottom:${C}px;width:${E}px;cursor:ew-resize`],
    ["nw", `top:0;left:0;width:${C}px;height:${C}px;cursor:nwse-resize`],
    ["se", `bottom:0;right:0;width:${C}px;height:${C}px;cursor:nwse-resize`],
    ["ne", `top:0;right:0;width:${C}px;height:${C}px;cursor:nesw-resize`],
    ["sw", `bottom:0;left:0;width:${C}px;height:${C}px;cursor:nesw-resize`],
  ];
  for (const [dir, css] of zones) {
    const z = document.createElement("div");
    z.className = "edge-resize";
    z.dataset.dir = dir;
    z.style.cssText = "position:fixed;z-index:10000;" + css;
    z.addEventListener("pointerdown", async e => {
      if (e.button !== 0) return;
      e.preventDefault();
      if (R.WindowIsMaximised && await R.WindowIsMaximised()) return;
      z.setPointerCapture(e.pointerId);
      const [pos, size] = await Promise.all([R.WindowGetPosition(), R.WindowGetSize()]);
      const sx = e.screenX, sy = e.screenY;
      // One runtime call in flight at a time; the newest pointer position
      // wins (pointermove comes faster than the window can be sized).
      let busy = false, last = null;
      const apply = async () => {
        if (busy || !last) return;
        busy = true;
        const [dx, dy] = last;
        last = null;
        let x = pos.x, y = pos.y, w = size.w, h = size.h;
        if (dir.includes("e")) w = Math.max(min.w, size.w + dx);
        if (dir.includes("s")) h = Math.max(min.h, size.h + dy);
        if (dir.includes("w")) { w = Math.max(min.w, size.w - dx); x = pos.x + (size.w - w); }
        if (dir.includes("n")) { h = Math.max(min.h, size.h - dy); y = pos.y + (size.h - h); }
        try {
          if (x !== pos.x || y !== pos.y || dir.includes("w") || dir.includes("n")) R.WindowSetPosition(x, y);
          R.WindowSetSize(w, h);
        } catch (err) { }
        await new Promise(r => setTimeout(r, 16));
        busy = false;
        if (last) apply();
      };
      const move = ev => { last = [ev.screenX - sx, ev.screenY - sy]; apply(); };
      const up = () => {
        z.removeEventListener("pointermove", move);
        z.removeEventListener("pointerup", up);
        z.removeEventListener("pointercancel", up);
      };
      z.addEventListener("pointermove", move);
      z.addEventListener("pointerup", up);
      z.addEventListener("pointercancel", up);
    });
    document.body.appendChild(z);
  }
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
// The panel's top is measured as if the page were not scrolled: a render
// while .content is scrolled (a long page left inside its tab) would else
// give a cap taller than the window.
function fitPanels(root) {
  (root || document).querySelectorAll(".panel.scrollx").forEach(p => {
    const sc = p.closest(".content");
    const top = p.getBoundingClientRect().top + (sc ? sc.scrollTop : 0);
    const h = Math.max(160, Math.floor(window.innerHeight - top - 28));
    p.style.maxHeight = h + "px";
  });
}
window.addEventListener("resize", () => fitPanels(document));
// Something drawn above a list after it was sized (the device list's
// selection bar, a comparison's result in the history tab, a details box
// opened) moves the list down: the tab's height changes, so it is sized
// again. Sizing a list never changes it a second time, so this settles.
{
  let fitQueued = false;
  const fitObs = new ResizeObserver(() => {
    if (fitQueued) return;
    fitQueued = true;
    requestAnimationFrame(() => { fitQueued = false; const tab = document.querySelector(".tab.active");
      if (!tab) return;
      fitPanels(tab);
      if (tab.id === "tab-import" && typeof liFitPreview === "function") liFitPreview(tab);
    });
  });
  document.querySelectorAll(".tab").forEach(el => fitObs.observe(el));
}

// pageError is a tab that could not load: its title stays, so the page is
// still known by its name, with the reason under it.
function pageError(title, msg) {
  return `<div class="page-head"><div><div class="page-title">${esc(title)}</div></div></div><div class="empty">${esc(msg)}</div>`;
}

// colFitWidth is the width column i needs for its widest value (header
// included), measured in the cells' own font: a field or a list box counts
// its frame and arrow on top of its text.
function colFitWidth(table, i) {
  const span = document.createElement("span");
  span.style.cssText = "position:absolute;visibility:hidden;white-space:nowrap";
  document.body.appendChild(span);
  let w = 0;
  // A cell of two lines (a name over its file name) is as wide as its
  // widest line, not as both side by side.
  const measure = (el, text, extra) => {
    const cs = getComputedStyle(el);
    span.style.font = cs.font; span.style.letterSpacing = cs.letterSpacing; span.style.textTransform = cs.textTransform;
    for (const line of String(text).split("\n")) {
      span.textContent = line.replace(/\s+/g, " ").trim();
      w = Math.max(w, span.offsetWidth + extra);
    }
  };
  const th = table.tHead && table.tHead.rows[0] && table.tHead.rows[0].cells[i];
  if (th) measure(th, headText(th), 24);
  [...(table.tBodies[0] ? table.tBodies[0].rows : [])].slice(0, 2000).forEach(tr => {
    const td = tr.cells[i];
    if (!td || td.colSpan > 1) return;
    const f = td.querySelector("input:not([type=checkbox]), select, textarea");
    if (f) measure(f, cellText(td), 46);
    else measure(td, td.innerText, 24);
  });
  span.remove();
  return w ? Math.max(40, Math.min(600, Math.ceil(w))) : 0;
}

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
      grip.dataset.tip = t("ドラッグで列幅を変更（ダブルクリックで内容に合わせる・もう一度で既定に戻す）");
      th.appendChild(grip);
      grip.addEventListener("click", e => e.stopPropagation());
      // Double-click: as wide as the column's widest value, as in Excel
      // (and in 表で編集); a second double-click gives the default back.
      grip.addEventListener("dblclick", e => {
        e.stopPropagation();
        const fit = colFitWidth(table, i);
        if (!fit || saved[i] === fit) { delete saved[i]; th.style.width = th.dataset.defw || ""; }
        else { saved[i] = fit; th.style.width = fit + "px"; }
        saveColWidths(key, saved);
      });
      grip.addEventListener("pointerdown", e => {
        e.preventDefault(); e.stopPropagation();
        // With table-layout: fixed the table's spare width is shared out
        // over the columns, so each is drawn wider than its style says.
        // Setting the grabbed column alone to its drawn width re-shared the
        // spare width and the column jumped under the pointer. Every column
        // is pinned at its drawn width first; the last one is left free to
        // take up whatever the drag gives or takes.
        ths.forEach((t, j) => { t.style.width = j === ths.length - 1 ? "" : Math.round(t.getBoundingClientRect().width) + "px"; });
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
// Devices are addressed by their internal ID: a name is unique only within
// its group, so two groups may each have a "core-sw1".
function devById(id) {
  return (INV.devices || []).find(d => d.id === id);
}

const CONNECT_TIP = t("自動でログインした端末を開きます。Shift+クリック（または右クリック）で自動ログインなしの端末（シリアルでの初期設定・パスワード未設定の機器・想定外の画面で止まる機器に）");
// direct, when given, says whether to leave the jump hosts out (the run
// tab's 踏み台: 使わない).
function wireConnectButtons(root, direct) {
  const open = async (id, manual) => {
    const dir = !!(direct && direct());
    try {
      await App().SpawnTerminal(id, manual, dir);
      toast((manual ? t("自動ログインなしの端末を開きました") : t("対話接続ウィンドウを開きました")) + (dir ? " " + t("（踏み台なし）") : ""), "ok");
    } catch (e) { toast(t("接続失敗") + ": " + terr(e), "err"); }
  };
  root.querySelectorAll("[data-term]").forEach(b => {
    b.onclick = e => open(b.dataset.term, !!e.shiftKey);
    b.oncontextmenu = e => { e.preventDefault(); open(b.dataset.term, true); };
  });
}

// GUI を開く: the device's web GUI in a PalaTerm window, logged in with the
// device's credentials (App.OpenGUI), always straight to its address. The
// button is there for every device but only works with GUI を使う on.
function guiButton(d) {
  const on = !!d.guiOn;
  const tip = on ? t("管理画面を PalaTerm の窓で開き、この機器のユーザー名・パスワードで自動ログインします")
    : t("機器の編集で「GUI を使う」をオンにすると使えます");
  return `<button class="btn sm act-gui" data-gui="${esc(d.id)}" ${on ? "" : "disabled"} data-tip="${esc(tip)}">${esc(t("GUI"))}</button>`;
}
function wireGUIButtons(root) {
  root.querySelectorAll("[data-gui]").forEach(b => b.onclick = async () => {
    b.disabled = true;
    try {
      const g = await App().OpenGUI(b.dataset.gui);
      toast(t("{d} の GUI を開きました（{u}）", { d: g.device, u: g.url }), "ok");
    } catch (e) { toast(t("GUI を開けません") + ": " + terr(e), "err"); }
    finally { b.disabled = false; }
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

// ---- right-click copy menu for tables ----
// The production build has no browser context menu, so the lists had no way
// to copy what they show. A right-click on any table row offers: the selected
// text, the cell, the row, or the whole table as shown (tab-separated, so it
// pastes into Excel as cells). Only columns with a heading are copied — the
// checkbox, drag-handle and button columns are not data.
async function copyText(v, okMsg) {
  try { await navigator.clipboard.writeText(v); }
  catch (e) {
    // Fallback: Wails runtime clipboard (navigator.clipboard needs a secure context).
    try { await rt().ClipboardSetText(v); }
    catch (e2) { toast(t("コピーできませんでした"), "err"); return; }
  }
  toast(okMsg, "ok");
}
function cellText(td) {
  const inp = td.querySelector("input:not([type=checkbox]), textarea");
  // A password field copies as nothing, even while its 👁 shows it.
  if (inp && (inp.type === "password" || inp.dataset.eyed)) return "";
  if (inp) return inp.value.trim();
  const sel = td.querySelector("select");
  if (sel) return sel.selectedIndex >= 0 ? sel.options[sel.selectedIndex].text.trim() : "";
  // A password a click showed (一覧の取り込み's .li-secret) copies masked.
  if (td.querySelector(".li-secret")) {
    const c = td.cloneNode(true);
    c.querySelectorAll(".li-secret").forEach(s => { s.textContent = "●●●●●●"; });
    return c.textContent.replace(/\s+/g, " ").trim();
  }
  return td.innerText.replace(/\s+/g, " ").trim();
}
function headText(th) {
  return th.innerText.replace(/\s*[▲▼]\s*$/, "").replace(/\s+/g, " ").trim();
}
// Indexes of the columns that carry data (a non-empty heading).
function dataCols(table) {
  const ths = table.tHead ? [...table.tHead.rows[0].cells] : [];
  return ths.map((th, i) => headText(th) ? i : -1).filter(i => i >= 0);
}
const rowTSV = (tr, cols) => cols.map(i => tr.cells[i] ? cellText(tr.cells[i]).replace(/\t/g, " ") : "").join("\t");

const ctxMenu = document.createElement("div");
ctxMenu.className = "ctx-menu hidden";
document.body.appendChild(ctxMenu);
function closeCtxMenu() { ctxMenu.classList.add("hidden"); ctxMenu.innerHTML = ""; }
// Pages can add their own entries (the log viewer adds 全文コピー): a
// function returning [label, action] pairs for the element right-clicked.
let ctxExtraItems = null;
document.addEventListener("contextmenu", e => {
  closeCtxMenu();
  if (e.defaultPrevented) return; // e.g. 接続 (right-click = no automatic login)
  // Text selected on the page, or inside the input / textarea that was
  // right-clicked (its selection is not part of the page selection).
  let picked = "";
  const el = document.activeElement;
  const onEl = el && (el === e.target || el.contains(e.target) || e.target.contains(el));
  const pw = el && (el.dataset.eyed || el.type === "password");
  if (pw && onEl) {
    // A password field (masked or shown by its 👁): nothing to copy from it.
    // Focused but right-clicked elsewhere: the page's selection, as always.
  } else if (el && !pw && (el.tagName === "INPUT" || el.tagName === "TEXTAREA") && onEl && el.selectionEnd > el.selectionStart) {
    picked = el.value.slice(el.selectionStart, el.selectionEnd);
  } else {
    picked = String(window.getSelection() || "");
    if (!picked.trim()) picked = "";
  }
  const items = [];
  if (picked) items.push([t("選択した文字をコピー"), () => copyText(picked, t("コピーしました"))]);
  const td = e.target.closest("td");
  const table = td && td.closest("table");
  if (!table || !table.tBodies.length || !table.tBodies[0].contains(td)) {
    // Outside a list: copy the selection, plus whatever the page adds.
    if (ctxExtraItems) items.push(...(ctxExtraItems(e.target) || []));
    if (!items.length) return;
    e.preventDefault();
    showCtxMenu(items, e);
    return;
  }
  e.preventDefault();
  const tr = td.parentElement;
  const cols = dataCols(table);
  const cell = cellText(td);
  if (cell && cols.includes(td.cellIndex)) items.push([t("このセルをコピー"), () => copyText(cell, t("{v} をコピーしました", { v: cell.length > 40 ? cell.slice(0, 40) + "…" : cell }))]);
  if (cols.length) {
    // Taken now, not on click: the run table is redrawn on every progress
    // event, which would leave these rows detached by the time of the click.
    const row = rowTSV(tr, cols);
    const rows = [...table.tBodies[0].rows].filter(r => r.offsetParent !== null);
    const head = cols.map(i => headText(table.tHead.rows[0].cells[i])).join("\t");
    const all = [head, ...rows.map(r => rowTSV(r, cols))].join("\r\n");
    items.push([t("この行をコピー"), () => copyText(row, t("1 行をコピーしました"))]);
    items.push([t("表示中の表をすべてコピー（Excel に貼れる形式）"), () => copyText(all, t("{n} 行をコピーしました", { n: rows.length }))]);
  }
  if (!items.length) return;
  showCtxMenu(items, e);
});
function showCtxMenu(items, e) {
  items.forEach(([label, fn]) => {
    const b = document.createElement("button");
    b.type = "button";
    b.textContent = label;
    b.onclick = () => { closeCtxMenu(); fn(); };
    ctxMenu.appendChild(b);
  });
  ctxMenu.classList.remove("hidden");
  const w = ctxMenu.offsetWidth, h = ctxMenu.offsetHeight;
  ctxMenu.style.left = Math.min(e.clientX, window.innerWidth - w - 4) + "px";
  ctxMenu.style.top = Math.min(e.clientY, window.innerHeight - h - 4) + "px";
}
document.addEventListener("mousedown", e => { if (!ctxMenu.contains(e.target)) closeCtxMenu(); }, true);
// Esc closes only the menu while it is open (in the log viewer Esc would
// otherwise close the whole window).
document.addEventListener("keydown", e => {
  if (e.key !== "Escape" || ctxMenu.classList.contains("hidden")) return;
  e.preventDefault(); e.stopImmediatePropagation();
  closeCtxMenu();
}, true);
window.addEventListener("blur", closeCtxMenu);
document.addEventListener("scroll", closeCtxMenu, true);

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
    btn.dataset.tip = t("表示 / 非表示");
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
  // Every window — main, terminal, screen, log viewer, diff, paste, command
  // set — resizes from an edge or corner grabbed just inside its frame (see
  // wireEdgeResize). The floors are winfit.go's, per window kind.
  {
    const g = (window.go && window.go.main) || {};
    const min = g.Term || g.Watcher ? { w: 640, h: 400 } : g.DiffWin ? { w: 720, h: 400 } : g.CmdsetWin ? { w: 420, h: 300 } : { w: 480, h: 320 };
    wireEdgeResize(min);
  }
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
  // Command-set window (binds CmdsetWin).
  if (window.go && window.go.main && window.go.main.CmdsetWin) {
    bootCmdsetWindow();
    return;
  }
  // Screen window (binds Watcher).
  if (window.go && window.go.main && window.go.main.Watcher) {
    bootWatchWindow();
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
  App().SetLang(LANG).catch(() => {});
  // × with terminal windows open or a batch running: the backend holds the
  // close and asks here (Windows' own Yes/No box looked out of place).
  // キャンセル has the focus.
  let quitAsking = false;
  rt().EventsOn("app:quit-request", async q => {
    if (quitAsking) return;
    quitAsking = true;
    const n = (q && q.terminals) || 0;
    const lines = [];
    if (q && q.running) lines.push(esc(t("一括実行中です。PalaTerm を終了すると実行が止まります（実行中の機器のログは途中まで残ります）。")));
    if (n > 0) lines.push(esc(t("端末窓が {n} 個開いています。PalaTerm を終了すると、これらも切断して閉じます（ログは保存されます）。", { n })));
    const ok = await subConfirm(`<b>${esc(t("PalaTerm を終了"))}</b><br>` +
      lines.join("<br>") + "<br>" + esc(t("終了しますか？")),
      t("終了する"), t("キャンセル"));
    quitAsking = false;
    if (ok && typeof gridBeforeLeave === "function") await gridBeforeLeave().catch(() => {});
    if (ok) App().ConfirmQuit().catch(() => {});
  });
  restoredTab = restoreUIState();
  const exists = await App().VaultExists();
  // Reloaded with the vault still open (the language switch): straight
  // back in. After ロック the backend is locked and this is false.
  if (exists && await App().Unlocked().catch(() => false)) { await enterApp(); return; }
  renderLock(exists);
}

// Translate the static sidebar texts authored in Japanese in index.html.
function applyStaticI18n() {
  document.documentElement.lang = LANG === "ja" ? "ja" : "en";
  document.querySelectorAll(".sidebar .nav-btn, .sidebar .nav-head").forEach(b => {
    // data-i18n: the menu's own dictionary key, where the same Japanese word
    // means something else elsewhere and so has another English entry
    // (コマンドセット / OSタイプ name a device's one set or type: plural
    // here; 作業 is a run's stage). Japanese keeps the text as written.
    const key = b.dataset.i18n;
    b.textContent = key ? (LANG === "ja" ? b.textContent.trim() : t(key)) : t(b.textContent.trim());
  });
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
    // Started again with schedules on: they wait for this unlock.
    App().SchedulesWaitUnlock().then(w => {
      if (w && $("#unlock")) $("#unlock").insertAdjacentHTML("afterend", `<div style="margin-top:12px;font-size:12px;line-height:1.7;color:var(--warn)">${esc(t("⏸ 予約はロック解除まで動きません。その間に来た回は、解除したときに「見逃し」として記録します"))}</div>`);
    }).catch(() => {});
  } else {
    body.innerHTML = `
      <div class="muted" style="margin-bottom:16px;font-size:13px;line-height:1.7">
        ${esc(t("はじめての起動です。機器情報を暗号化して保存するためのマスターパスワードを設定してください。"))}<br>
        <span style="font-size:12px">${esc(t("以前の PalaTerm を使っていた場合は、ここで作らずに、旧フォルダの data\\palaterm_vault.enc をこの exe の隣の data\\ に置いてから起動し直してください。"))}</span></div>
      <div class="field"><label>${esc(t("マスターパスワード"))}</label><div><input id="pw" type="password" autofocus /></div></div>
      <div class="field"><label>${esc(t("確認のため再入力"))}</label><div><input id="pw2" type="password" /></div></div>
      <button class="btn primary" id="create" style="width:100%">${esc(t("作成して開始"))}</button>
      <div class="muted" style="margin-top:12px;font-size:12px;line-height:1.7">${esc(t("機器情報は PalaTerm.exe の隣の data\\palaterm_vault.enc に暗号化して保存されます。マスターパスワードを忘れると復元できません。"))}</div>`;
    $("#pw2").addEventListener("keydown", e => { if (e.key === "Enter") doCreate(); });
    $("#create").onclick = doCreate;
    wirePasswordToggles(body);
  }
}

async function doUnlock() {
  const pw = $("#pw").value;
  if (!pw) return;
  try {
    await App().Unlock(pw);
  } catch (e) {
    // A damaged vault whose backup opens: offer to restore it (views2.js).
    if (!(await unlockRecover(pw))) {
      // The backend tells a wrong password from a file it could not read (a
      // sync client holding it, a permission): say which, so the password is
      // not retyped at a file problem.
      toast(t("ロックを解除できません") + ": " + terr(e), "err");
      return;
    }
  }
  try {
    await enterApp();
    unlockNotices();
  } catch (e) {
    toast(t("ロックを解除できません") + ": " + terr(e), "err");
  }
}

async function doCreate() {
  const pw = $("#pw").value, pw2 = $("#pw2").value;
  if (!pw) { fieldError($("#pw"), t("パスワードを入力してください")); return; }
  if (pw !== pw2) { fieldError($("#pw2"), t("確認用パスワードが一致しません")); return; }
  try { await App().CreateVault(pw); await enterApp(); }
  catch (e) { toast(t("作成に失敗しました") + ": " + terr(e), "err"); }
}

async function enterApp() {
  $("#lock").classList.add("hidden");
  $("#app").classList.remove("hidden");
  PROFILES = await App().ListProfiles();
  SERIAL = await App().ListSerialPorts() || [];
  await refreshInventory();
  switchTab(restoredTab || "devices");
  restoredTab = null;
  // The idle lock closed a dialog that was being edited: say so, instead
  // of leaving the user to find the edit gone.
  try {
    if (sessionStorage.getItem(LOCK_DISCARDED_KEY)) {
      sessionStorage.removeItem(LOCK_DISCARDED_KEY);
      toast(t("自動ロックのため、編集中だった内容（ダイアログ・変換ルール・設定）は破棄されました"), "warn");
    }
  } catch (e) { }
  // Locked while a batch ran: it went on meanwhile. Still going -> keep
  // following it (■ 中止 works); finished -> show the results it left.
  if (running) {
    const st = await App().GetRunStatus().catch(() => null);
    if (st && st.active) {
      clearInterval(etaTimer); etaTimer = setInterval(updateEta, 1000);
      if (document.querySelector("#tab-run.active")) renderRun();
    } else if (st && st.last) applyRunDone(st.last);
    else { running = false; if (document.querySelector("#tab-run.active")) renderRun(); }
  }
}

// Locking reloads the page, which used to drop the run tab's results (and
// with them ↻ 失敗・中断のみ再実行) and the device list's place — a batch
// left with failures over lunch came back to an empty table. They are kept
// in sessionStorage across the lock (this window only, nothing secret: the
// table's status and messages, the run choices, the open group) and put back
// once, at the next unlock.
const UI_STATE_KEY = "palaterm_ui_state";
function saveUIState() {
  try {
    const tab = document.querySelector(".tab.active");
    sessionStorage.setItem(UI_STATE_KEY, JSON.stringify({
      tab: tab ? tab.id.replace("tab-", "") : "devices",
      runState, runGroupSel, runStage, runSetSel, runAdHoc, lastRunReq, lastRunDry, lastRunDir, deviceView,
      // A batch still running goes on in the backend; its progress keeps
      // arriving after the reload (and is checked again at unlock).
      running, cancelling, runTargets, runTotals, runStartMs,
    }));
  } catch (e) { }
}
// Where the user was (the tab, the device list's group) comes back after a
// restart too: nothing secret, kept in localStorage.
const UI_PLACE_KEY = "palaterm_ui_place";
function savePlace() {
  try {
    const tab = document.querySelector(".tab.active");
    localStorage.setItem(UI_PLACE_KEY, JSON.stringify({ tab: tab ? tab.id.replace("tab-", "") : "devices", deviceView }));
  } catch (e) { }
}
function restoreUIState() {
  let v = null;
  try { v = JSON.parse(sessionStorage.getItem(UI_STATE_KEY) || "null"); sessionStorage.removeItem(UI_STATE_KEY); } catch (e) { }
  if (!v) {
    let pl = null;
    try { pl = JSON.parse(localStorage.getItem(UI_PLACE_KEY) || "null"); } catch (e) { }
    if (pl && pl.deviceView && typeof pl.deviceView === "object") deviceView = pl.deviceView;
    return pl && pl.tab && pl.tab !== "settings" ? pl.tab : null;
  }
  for (const k in runState) delete runState[k];
  Object.assign(runState, v.runState || {});
  runGroupSel = v.runGroupSel || ""; runStage = v.runStage || ""; runSetSel = v.runSetSel || ""; runAdHoc = v.runAdHoc || "";
  lastRunReq = v.lastRunReq || null; lastRunDry = !!v.lastRunDry; lastRunDir = v.lastRunDir || "";
  if (v.deviceView) deviceView = v.deviceView;
  running = !!v.running; cancelling = !!v.cancelling;
  runTargets = v.runTargets || []; runTotals = v.runTotals || {}; runStartMs = v.runStartMs || 0;
  return v.tab === "settings" ? "devices" : v.tab;
}
// The tab to open after the next unlock (restored at page load, so a batch's
// events arriving while the lock screen shows land in the restored table).
let restoredTab = null;

async function refreshInventory() {
  INV = await App().GetInventory();
  if (typeof loadGridCounts === "function") await loadGridCounts();
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
  // So would an editor left open (a device, a schedule, a command set),
  // and a 変換ルール being edited (a page, not a dialog). Both at once are
  // one question over the dialog: a second, page-level confirm would take
  // the dialog's place and the dialog would be gone if it were declined.
  if (modalOpen()) {
    const msg = ruleEditUnsaved()
      ? t("編集中のダイアログと、保存されていない変換ルールの編集内容があります。ロックすると、どちらも失われます。ロックしますか？")
      : t("編集中のダイアログがあります。ロックすると、その内容は保存されずに失われます。ロックしますか？");
    if (!(await subConfirm(esc(msg), t("ロックする")))) return;
  } else if (ruleEditUnsaved() && !(await uiConfirm({ title: t("ロック"), message: esc(t("変換ルールの編集内容が保存されていません。ロックすると失われます。ロックしますか？")), okLabel: t("ロックする"), danger: true }))) return;
  // The ロック button locks the terminal windows too (they stay connected
  // behind their own lock screen); the idle auto-lock below does not, since
  // a terminal can be in use while this window sits idle.
  await App().LockTerminals().catch(() => {});
  if (typeof gridBeforeLeave === "function") await gridBeforeLeave().catch(() => {});
  saveUIState();
  await App().Lock(); location.reload();
};

// ---- change master password ----
$("#btn-chpw").onclick = () => {
  const node = h(`<div>
    <h3>${esc(t("マスターパスワード変更"))}</h3>
    <div class="field"><label>${esc(t("現在のマスターパスワード"))}</label><input id="cp-cur" type="password" autofocus></div>
    <div class="field"><label>${esc(t("新しいマスターパスワード"))}</label><div><input id="cp-new" type="password"></div></div>
    <div class="field"><label>${esc(t("確認のため再入力"))}</label><div><input id="cp-new2" type="password"></div></div>
    <div class="modal-actions">
      <button class="btn" id="cp-cancel">${esc(t("キャンセル"))}</button>
      <button class="btn primary" id="cp-ok">${esc(t("変更"))}</button>
    </div>
  </div>`);
  openModal(node, "mid");
  wirePasswordToggles(node);
  node.querySelector("#cp-cancel").onclick = closeModal;
  // Enter in any field changes it, as in the other one-step dialogs.
  // (Ctrl+Enter is the dialog's own key, handled once for every dialog.)
  node.querySelectorAll("input").forEach(inp => inp.addEventListener("keydown", e => {
    if (e.key !== "Enter" || e.isComposing || e.ctrlKey || e.metaKey) return;
    e.preventDefault();
    if (!e.repeat) node.querySelector("#cp-ok").click();
  }));
  const cpOk = node.querySelector("#cp-ok");
  cpOk.onclick = async () => {
    if (cpOk.disabled) return;
    const cur = node.querySelector("#cp-cur").value;
    const nw = node.querySelector("#cp-new").value;
    const nw2 = node.querySelector("#cp-new2").value;
    if (!nw) { fieldError(node.querySelector("#cp-new"), t("新しいパスワードを入力してください")); return; }
    if (nw !== nw2) { fieldError(node.querySelector("#cp-new2"), t("確認用パスワードが一致しません")); return; }
    // One change at a time: a second press while the first is at work
    // would check the old password against the new one and say it failed.
    cpOk.disabled = true;
    try {
      await App().ChangeMasterPassword(cur, nw);
      closeModal();
      toast(t("マスターパスワードを変更しました"), "ok");
    } catch (e) { toast(t("変更失敗") + ": " + terr(e), "err"); }
    finally { cpOk.disabled = false; }
  };
};

// ---- reset (delete the vault: every device / set / profile / password) ----
// Lives at the bottom of the 設定 tab (危険な操作, wired by renderSettings),
// deliberately away from everyday buttons such as ロック. Requires the
// master password on top of two confirmations.
async function resetVaultFlow() {
  const ok = await uiConfirm({
    title: t("すべてのデータをリセット"),
    message: t("リセットすると、登録済みの<b>すべての機器・グループ・コマンドセット・OSタイププロファイル、およびマスターパスワード</b>が完全に削除されます。<br>この操作は<b>元に戻せません</b>。<br><span class=\"muted\" style=\"font-size:12px\">ログファイルと export フォルダ内の書き出しファイルは削除されず残ります</span>"),
    okLabel: t("初期化する"),
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
  node.querySelector("#rs-pw").addEventListener("keydown", e => { if (e.key === "Enter" && !e.isComposing && !e.repeat) node.querySelector("#rs-ok").click(); });
  const rsOk = node.querySelector("#rs-ok");
  rsOk.onclick = async () => {
    if (rsOk.disabled) return;
    const pw = node.querySelector("#rs-pw").value;
    if (!pw) { toast(t("パスワードを入力してください"), "err"); return; }
    rsOk.disabled = true;
    try { await App().ResetVault(pw); location.reload(); }
    catch (e) { toast(t("リセット失敗") + ": " + terr(e), "err"); rsOk.disabled = false; }
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
  sel.onchange = async () => {
    // The reload drops an open editor; the run tab's state and the table
    // draft are saved the way ロック saves them, and the vault stays open
    // (boot comes straight back in).
    if (typeof canLeaveSettings === "function" && !(await canLeaveSettings("lang"))) {
      sel.value = LANG === "ja" ? "ja" : "en";
      return;
    }
    // An open dialog and an unsaved 変換ルール are asked about together (see
    // the ロック button): two confirms in a row would close the dialog.
    let ok = true;
    if (modalOpen()) {
      const msg = ruleEditUnsaved()
        ? t("編集中のダイアログと、保存されていない変換ルールの編集内容があります。言語を切り替えると、どちらも失われます。切り替えますか？")
        : t("編集中のダイアログがあります。言語を切り替えると、その内容は保存されずに失われます。切り替えますか？");
      ok = await subConfirm(esc(msg), t("切り替える"));
    } else if (ruleEditUnsaved()) {
      ok = await uiConfirm({ title: t("言語の切り替え"), message: esc(t("変換ルールの編集内容が保存されていません。言語を切り替えると失われます。切り替えますか？")), okLabel: t("切り替える"), danger: true });
    }
    if (!ok) {
      sel.value = LANG === "ja" ? "ja" : "en";
      return;
    }
    if (typeof gridBeforeLeave === "function") await gridBeforeLeave().catch(() => {});
    saveUIState();
    try { localStorage.setItem("palaterm_lang", sel.value); } catch { }
    location.reload();
  };
}
// The same choice on the lock screen and the first-run screen: nothing is
// open there to lose. What the last ロック kept for the next unlock (the
// run tab's results, the tab) is kept again across this reload.
{
  const sel = $("#lock-lang");
  sel.value = LANG === "ja" ? "ja" : "en";
  sel.onchange = () => {
    saveUIState();
    try {
      const v = JSON.parse(sessionStorage.getItem(UI_STATE_KEY) || "null");
      if (v && restoredTab) { v.tab = restoredTab; sessionStorage.setItem(UI_STATE_KEY, JSON.stringify(v)); }
    } catch (e) { }
    try { localStorage.setItem("palaterm_lang", sel.value); } catch (e) { }
    location.reload();
  };
}

async function switchTab(name) {
  // Leaving the settings tab with unsaved edits asks for confirmation.
  if (typeof canLeaveSettings === "function" && !(await canLeaveSettings(name))) return;
  // The devices tab keeps its state (chosen group / list position) across tab
  // switches; 「← 対象選択」 is the way back to the chooser.
  // The spare command-set window (started ahead of View so it opens at once)
  // lives while the Command sets tab is shown: leaving the tab drops it,
  // showing the tab starts one (renderCommands).
  const leaving = document.querySelector(".tab.active");
  if (leaving && leaving.id === "tab-commands" && name !== "commands") App().DropSpareCommandSetWindow().catch(() => {});
  document.querySelectorAll(".nav-btn[data-tab]").forEach(b =>
    b.classList.toggle("active", b.dataset.tab === name));
  document.querySelectorAll(".tab").forEach(el =>
    el.classList.toggle("active", el.id === "tab-" + name));
  savePlace();
  // A tab starts at its top: a long page scrolled down (the import preview)
  // would otherwise leave the next tab scrolled, and fitPanels then measures
  // its list from above the window — the list runs past the bottom and the
  // title and toolbar scroll away with it.
  const content = document.querySelector(".content");
  if (content) content.scrollTop = 0;
  if (name !== "import" && typeof liLeftTab === "function") liLeftTab();
  // Coming from a group's device list with no group chosen in the run tab
  // yet: start on that group instead of an empty table.
  if (name === "run" && !runGroupSel && !running && typeof deviceView !== "undefined" &&
      deviceView.mode === "list" && deviceView.group) runGroupSel = deviceView.group;
  renderTab(name);
}

function renderTab(name) {
  if (name === "devices") renderDevices();
  else if (name === "commands") renderCommands();
  else if (name === "run") renderRun();
  else if (name === "history") renderHistory();
  else if (name === "schedule") renderSchedules();
  else if (name === "settings") renderSettings();
  else if (name === "ostypes") renderOSTypes();
  else if (name === "import") renderListImport();
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
  if (!min) return;
  if (Date.now() - lastActivityMs < min * 60000) return;
  // A device GUI window being used counts as activity: locking closes those
  // windows, and a policy edited there for forty minutes went with them.
  const guiAgo = await App().GUIActiveAgoMs().catch(() => -1);
  if (guiAgo >= 0 && guiAgo < min * 60000) { lastActivityMs = Date.now() - guiAgo; return; }
  // A batch still running is no reason to stay open: it goes on behind the
  // lock screen and enterApp picks it up again (GetRunStatus) at unlock.
  // An open editor is lost — noted for a word after the unlock.
  if (modalOpen() || ruleEditUnsaved() || (typeof settingsDirty !== "undefined" && (settingsDirty || myLoginsDirty))) { try { sessionStorage.setItem(LOCK_DISCARDED_KEY, "1"); } catch (e) { } }
  if (typeof gridBeforeLeave === "function") await gridBeforeLeave().catch(() => {});
  saveUIState();
  try { await App().Lock(); } catch (e) { }
  location.reload();
}, 15000);
const LOCK_DISCARDED_KEY = "palaterm.lockDiscarded";
// ruleEditUnsaved: the 一覧の取り込み rule editor (a page) holds edits not
// saved — a reload (lock, language) would lose them as it loses a dialog.
function ruleEditUnsaved() { return typeof liDirty === "function" && typeof LI !== "undefined" && LI.view === "edit" && liDirty(); }

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
