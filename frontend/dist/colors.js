// Screen colours of the child windows (Tera Term's ［ウィンドウの設定］ text
// and background colours): one pair per window kind, kept per PC in
// localStorage like the font size, changed from each window's 色 button or
// the 設定 tab. The log viewer differs from the terminal by default, so a
// saved log is never taken for a live session (2026-10-21, field request).
// A device's own 端末の色 (Device.termColor) tints its terminal on top.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

const SCREEN_COLORS_KEY = "palaterm_colors";
const SCREEN_KINDS = ["term", "viewer", "watch", "diff"];
const SCREEN_DEFAULTS = {
  term: { bg: "#000000", fg: "#d6dee8", cursor: "#5b9bff" },
  viewer: { bg: "#f7f3e8", fg: "#1f2937" },
  watch: { bg: "#0f1e33", fg: "#d6dee8" },
  diff: { bg: "#0b0e13", fg: "#cbd5e1" },
};
const SCREEN_PRESETS = [
  { name: "黒", bg: "#000000", fg: "#d6dee8" },
  { name: "濃紺", bg: "#0f1e33", fg: "#d6dee8" },
  { name: "濃い灰", bg: "#1e1e1e", fg: "#d4d4d4" },
  { name: "生成り", bg: "#f7f3e8", fg: "#1f2937" },
  { name: "白", bg: "#ffffff", fg: "#111111" },
  { name: "緑文字", bg: "#000000", fg: "#33ff66" },
  { name: "琥珀", bg: "#000000", fg: "#ffb000" },
];
function screenKindName(kind) {
  return { term: t("端末窓"), viewer: t("ログ表示窓"), watch: t("画面表示窓（見るだけ）"), diff: t("差分窓") }[kind] || kind;
}

// Device tints (Device.termColor): dark backgrounds for the terminal and a
// bright band colour for its title bar, so a production device stands out
// among windows side by side.
const DEVICE_TINTS = {
  red: { name: "赤", bg: "#2b0d0d", band: "#dc2626" },
  orange: { name: "橙", bg: "#2b1a0b", band: "#ea580c" },
  yellow: { name: "黄", bg: "#26230a", band: "#ca8a04" },
  green: { name: "緑", bg: "#0c2414", band: "#16a34a" },
  blue: { name: "青", bg: "#0c1a33", band: "#2563eb" },
  purple: { name: "紫", bg: "#1f0f30", band: "#9333ea" },
};
const deviceTintKeys = () => Object.keys(DEVICE_TINTS);

const isHexColor = v => typeof v === "string" && /^#[0-9a-fA-F]{6}$/.test(v);

function loadScreenColorsAll() {
  try { return JSON.parse(localStorage.getItem(SCREEN_COLORS_KEY) || "{}") || {}; } catch (e) { return {}; }
}
// The colours for one window kind: the saved ones over the defaults.
function screenColors(kind) {
  const def = SCREEN_DEFAULTS[kind] || SCREEN_DEFAULTS.term;
  const saved = loadScreenColorsAll()[kind] || {};
  const out = { ...def };
  for (const k of Object.keys(def)) if (isHexColor(saved[k])) out[k] = saved[k].toLowerCase();
  return out;
}
// null puts the kind back to its defaults.
function setScreenColors(kind, c) {
  const all = loadScreenColorsAll();
  if (c) all[kind] = c; else delete all[kind];
  try { localStorage.setItem(SCREEN_COLORS_KEY, JSON.stringify(all)); } catch (e) { }
}
// Another window changing the colours: apply them here too (the windows are
// separate processes sharing one WebView2 profile, so the storage event
// reaches them; where it does not, the next window opened has them).
function onScreenColors(kind, fn) {
  window.addEventListener("storage", e => { if (e.key === SCREEN_COLORS_KEY || e.key === null) fn(screenColors(kind)); });
}
// The editors on the page, refreshed when another window changes the
// colours (one listener; editors taken off the page are dropped).
const screenColorEditors = new Set();
window.addEventListener("storage", e => {
  if (e.key !== SCREEN_COLORS_KEY && e.key !== null) return;
  for (const box of [...screenColorEditors]) { if (box.isConnected) box.refresh(); else screenColorEditors.delete(box); }
});

// WCAG contrast ratio of two #rrggbb colours (1 … 21).
function colorLum(hex) {
  const n = parseInt(hex.slice(1), 16);
  const ch = [n >> 16 & 255, n >> 8 & 255, n & 255].map(v => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); });
  return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2];
}
function contrastRatio(a, b) {
  const x = colorLum(a), y = colorLum(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}
const isLightColor = hex => colorLum(hex) > 0.4;

// xterm.js theme for a terminal-like window. On a light background the
// ANSI white / yellow a device may print are darkened to stay readable.
function xtermTheme(c) {
  const light = isLightColor(c.bg);
  const th = {
    background: c.bg, foreground: c.fg, cursor: c.cursor || (light ? "#1d4ed8" : "#5b9bff"),
    selectionBackground: light ? "rgba(37,99,235,.28)" : "rgba(148,163,184,.38)",
  };
  if (light) Object.assign(th, { white: "#4b5563", brightWhite: "#111827", yellow: "#a16207", brightYellow: "#854d0e", cursorAccent: c.bg });
  return th;
}

// The editor: presets, the two (three) colour pickers, 既定に戻す and the
// contrast warning. apply(colours) is called on every change; the colours
// are saved at once.
function screenColorEditor(kind, apply) {
  let cur = screenColors(kind);
  const hasCursor = "cursor" in SCREEN_DEFAULTS[kind];
  const box = h(`<div class="sc-edit">
    <div class="sc-presets">${SCREEN_PRESETS.map((p, i) => `<button type="button" class="sc-preset" data-sc-p="${i}" style="background:${p.bg};color:${p.fg}" data-tip="${esc(t(p.name))}">Aa</button>`).join("")}</div>
    <div class="sc-pickers">
      <label>${esc(t("文字"))} <input type="color" class="sc-fg"></label>
      <label>${esc(t("背景"))} <input type="color" class="sc-bg"></label>
      ${hasCursor ? `<label>${esc(t("カーソル"))} <input type="color" class="sc-cur"></label>` : ""}
      <button type="button" class="btn sm sc-reset">${esc(t("既定に戻す"))}</button>
    </div>
    <div class="sc-sample"></div>
    <div class="sc-warn hidden">${esc(t("文字と背景の色が近く、読みにくい組み合わせです"))}</div>
    ${kind === "term" || kind === "watch" ? `<div class="sc-note">${esc(t("機器が色を付けて出す文字（Linux の ls など）は機器側の色のままです。明るい背景では白と黄色だけ濃くして表示します"))}</div>` : ""}
  </div>`);
  const fg = box.querySelector(".sc-fg"), bg = box.querySelector(".sc-bg"), cu = box.querySelector(".sc-cur");
  const sample = box.querySelector(".sc-sample"), warn = box.querySelector(".sc-warn");
  function show() {
    fg.value = cur.fg; bg.value = cur.bg; if (cu) cu.value = cur.cursor;
    sample.style.background = cur.bg; sample.style.color = cur.fg;
    sample.innerHTML = `${esc(screenKindName(kind))}: Router# show ip interface brief<span style="background:${cur.cursor || "transparent"};margin-left:2px">${cur.cursor ? "&nbsp;" : ""}</span>`;
    warn.classList.toggle("hidden", contrastRatio(cur.fg, cur.bg) >= 3);
  }
  function commit() { setScreenColors(kind, cur); show(); apply(cur); }
  box.querySelectorAll("[data-sc-p]").forEach(b => b.onclick = () => {
    const p = SCREEN_PRESETS[+b.dataset.scP];
    cur = { ...cur, bg: p.bg, fg: p.fg };
    if (hasCursor) cur.cursor = isLightColor(p.bg) ? "#1d4ed8" : SCREEN_DEFAULTS.term.cursor;
    commit();
  });
  fg.oninput = () => { cur = { ...cur, fg: fg.value }; commit(); };
  bg.oninput = () => { cur = { ...cur, bg: bg.value }; commit(); };
  if (cu) cu.oninput = () => { cur = { ...cur, cursor: cu.value }; commit(); };
  box.querySelector(".sc-reset").onclick = () => { setScreenColors(kind, null); cur = screenColors(kind); show(); apply(cur); };
  box.refresh = () => { cur = screenColors(kind); show(); };
  // Another window changed the colours: show them here too, or the next
  // change made here would save this editor's stale copy over them.
  for (const b of [...screenColorEditors]) if (!b.isConnected) screenColorEditors.delete(b);
  screenColorEditors.add(box);
  show();
  return box;
}

// The 色 button's pop-over under anchor. onClose runs when it closes (the
// terminal takes the keyboard back).
function openScreenColorPanel(kind, anchor, apply, onClose) {
  const old = document.getElementById("sc-pop");
  if (old) {
    // Through its own close: a bare remove() left its Esc / click listeners
    // behind, and the next Esc never reached the terminal (vi).
    old._close();
    if (old.dataset.kind === kind) return;
  }
  const pop = h(`<div id="sc-pop" class="sc-pop" data-kind="${kind}">
    <div class="sc-pop-head"><b>${esc(t("表示の色"))}</b> <span class="muted">${esc(screenKindName(kind))}</span>
      <button type="button" class="btn sm sc-close" data-tip="${esc(t("閉じる"))}">✕</button></div></div>`);
  pop.appendChild(screenColorEditor(kind, apply));
  pop.appendChild(h(`<div class="sc-note">${esc(t("この PC に保存され、同じ種類の窓すべてに使われます（設定タブでも変えられます）"))}</div>`));
  document.body.appendChild(pop);
  const r = anchor.getBoundingClientRect();
  pop.style.top = Math.round(r.bottom + 6) + "px";
  pop.style.left = Math.max(8, Math.min(window.innerWidth - pop.offsetWidth - 8, Math.round(r.right - pop.offsetWidth))) + "px";
  const close = () => {
    pop.remove();
    document.removeEventListener("mousedown", outside, true);
    document.removeEventListener("keydown", esc_, true);
    onClose && onClose();
  };
  const outside = e => { if (!pop.contains(e.target) && e.target !== anchor) close(); };
  const esc_ = e => { if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(); } };
  pop._close = close;
  pop.querySelector(".sc-close").onclick = close;
  setTimeout(() => {
    document.addEventListener("mousedown", outside, true);
    document.addEventListener("keydown", esc_, true);
  }, 0);
}

// closeScreenColorPanel closes the pop-over if one is open (the lock).
function closeScreenColorPanel() {
  const pop = document.getElementById("sc-pop");
  if (pop) pop._close();
}

// The 設定 tab's panel: every window kind in one place.
function screenColorsPanelHTML() {
  return `<div class="panel" style="padding:22px;max-width:620px;margin-top:16px" id="screen-colors">
    <div style="font-weight:600;margin-bottom:4px">${esc(t("表示の色"))}</div>
    <div class="muted" style="font-size:12px;margin-bottom:10px">${esc(t("別ウィンドウで開く画面の文字色・背景色です（Tera Term の［ウィンドウの設定］と同じ）。変更はすぐ保存され、開いている窓にも反映されます。各窓の「色」ボタンからも変えられます。保存先はこの PC です（vault・一式書き出しには入りません）。機器ごとの「端末の色」（機器の編集）は端末窓の背景にさらに重ねて使われます"))}</div>
    <div id="sc-kinds"></div>
  </div>`;
}
function wireScreenColorsPanel() {
  const host = document.getElementById("sc-kinds");
  if (!host) return;
  SCREEN_KINDS.forEach(kind => {
    const row = h(`<div class="sc-kind"><div class="sc-kind-name">${esc(screenKindName(kind))}</div></div>`);
    row.appendChild(screenColorEditor(kind, () => { }));
    host.appendChild(row);
  });
}
