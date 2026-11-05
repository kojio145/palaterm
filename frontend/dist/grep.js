// Grep window (child process: PalaTerm.exe --grep <log root> [run folder]).
// One text searched in every log file of the log folder (or of one run
// folder) at once, the hits listed by file the way an editor's grep does
// ("file(line): text"), each hit opening its log at that line. Files with
// no hit can be listed too: after a status run, "which devices do NOT show
// this" is as often the question. The search itself is history.Grep (Go).
// Loaded in every window; only runs when GrepWin is bound.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

async function bootGrepWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "var(--bg)";
  const G = () => window.go.main.GrepWin;
  const init = await G().Load();
  // Options remembered per PC.
  const OPTS_KEY = "palaterm_grep_opts";
  let opts = { regex: false, cs: false, days: 0, noHit: false };
  try { opts = Object.assign(opts, JSON.parse(localStorage.getItem(OPTS_KEY) || "{}")); } catch (e) { /* defaults */ }
  const saveOpts = () => { try { localStorage.setItem(OPTS_KEY, JSON.stringify(opts)); } catch (e) { /* ignore */ } };
  const scopeOpts = (init.dir ? `<option value="${esc(init.dir)}">${esc(t("この実行のフォルダ（{n}）", { n: init.dirName }))}</option>` : "")
    + `<option value="">${esc(t("ログフォルダ全体"))}</option>`
    + init.folders.filter(f => f.dir !== init.dir).map(f => `<option value="${esc(f.dir)}">${esc(f.name)}</option>`).join("");
  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:var(--bg);font-family:'Segoe UI',sans-serif">
    <div style="display:flex;align-items:center;gap:8px;padding:6px 12px;border-bottom:1px solid var(--border)">
      <div style="color:var(--text);font-size:13px;font-weight:600;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" data-tip="${esc(init.root)}">${esc(t("ログの grep"))}: ${esc(init.dirName || init.root)}</div>
      <span id="gw-note" class="pill ok hidden"></span>
      <button class="btn sm" id="gw-font-dec" data-tip="${esc(t("文字を小さく（Ctrl+-）"))}">A−</button>
      <button class="btn sm" id="gw-font-inc" data-tip="${esc(t("文字を大きく（Ctrl++）"))}">A+</button>
      <button class="btn sm" id="gw-copy" disabled data-tip="${esc(t("結果を「ファイル(行): 内容」の形でクリップボードにコピーします"))}">${esc(t("結果をコピー"))}</button>
      <button class="btn sm" id="gw-save" disabled data-tip="${esc(t("結果を「ファイル(行): 内容」の形で export\\grep に保存します"))}">${esc(t("結果を保存"))}</button>
      <button class="btn sm" id="gw-folder" data-tip="${esc(t("検索している範囲のフォルダをエクスプローラーで開きます"))}">${esc(t("フォルダを開く"))}</button>
    </div>
    <div style="display:flex;align-items:center;gap:8px;padding:6px 12px;border-bottom:1px solid var(--border);flex-wrap:wrap;font-size:12px;color:#c7d0dc">
      <input id="gw-q" placeholder="${esc(t("検索する文字（Enter で検索）"))}" style="flex:1;min-width:240px;background:#0b0e13;color:var(--text);border:1px solid var(--border);border-radius:6px;padding:5px 8px;font-size:13px">
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap" data-tip="${esc(t("チェックすると検索する文字を正規表現として読みます（例: Gi1/0/[1-4]|down）。外すと、記号もそのままの文字として探します"))}"><input type="checkbox" id="gw-regex" style="width:auto"> ${esc(t("正規表現"))}</label>
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap"><input type="checkbox" id="gw-cs" style="width:auto"> ${esc(t("大文字小文字を区別"))}</label>
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap">${esc(t("範囲"))}
        <select id="gw-scope" style="width:auto;max-width:320px;font-size:12px;padding:2px 6px">${scopeOpts}</select></label>
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap" data-tip="${esc(t("ファイルの更新日時で絞ります"))}">${esc(t("期間"))}
        <select id="gw-days" style="width:auto;font-size:12px;padding:2px 6px">
          <option value="0">${esc(t("すべて"))}</option><option value="1">${esc(t("最近 {n} 日", { n: 1 }))}</option><option value="7">${esc(t("最近 {n} 日", { n: 7 }))}</option><option value="30">${esc(t("最近 {n} 日", { n: 30 }))}</option><option value="90">${esc(t("最近 {n} 日", { n: 90 }))}</option>
        </select></label>
      <button class="btn sm primary" id="gw-go">${esc(t("検索"))}</button>
    </div>
    <div style="display:flex;align-items:center;gap:12px;padding:4px 12px;border-bottom:1px solid var(--border);font-size:12px;color:#c7d0dc;flex-wrap:wrap">
      <span id="gw-sum" class="muted">${esc(t("検索する文字を入れて Enter。範囲のすべてのログファイルをまとめて検索します"))}</span>
      <span style="flex:1"></span>
      <label style="display:flex;align-items:center;gap:5px;white-space:nowrap" data-tip="${esc(t("検索したのに一致する行が無かったファイル（＝その内容が出ていない機器）も一覧の下に並べます"))}"><input type="checkbox" id="gw-nohit" style="width:auto"> ${esc(t("一致しなかったファイルも表示"))}</label>
    </div>
    <div id="gw-body" class="mono" style="flex:1;min-height:0;overflow:auto;background:#0b0e13;font-family:Consolas,monospace;font-size:12px;padding:6px 0"></div>
    <div id="gw-status" class="muted" style="font-size:11px;padding:3px 12px;border-top:1px solid var(--border)">${esc(t("クリックでその行をログ表示窓で開きます。ファイル名の行で折りたたみ"))}</div>
  </div>`);
  document.body.appendChild(wrap);
  const q = document.getElementById("gw-q"), body = document.getElementById("gw-body"), sum = document.getElementById("gw-sum"), note = document.getElementById("gw-note");
  const regex = document.getElementById("gw-regex"), cs = document.getElementById("gw-cs"), scope = document.getElementById("gw-scope"), days = document.getElementById("gw-days"), noHit = document.getElementById("gw-nohit");
  const go = document.getElementById("gw-go"), copyBtn = document.getElementById("gw-copy"), saveBtn = document.getElementById("gw-save");
  regex.checked = !!opts.regex; cs.checked = !!opts.cs; noHit.checked = !!opts.noHit;
  days.value = String([0, 1, 7, 30, 90].includes(opts.days) ? opts.days : 0);
  if (init.dir) scope.value = init.dir;
  let noteTimer = null;
  function flash(text, kind) { note.textContent = text; note.className = "pill " + (kind || "ok"); clearTimeout(noteTimer); noteTimer = setTimeout(() => note.classList.add("hidden"), kind === "err" ? 6000 : 1500); }

  // Font size (its own key; wireFontZoom, app.js).
  const zoom = wireFontZoom("palaterm_grep_font", px => { body.style.fontSize = px + "px"; }, { def: 12 });
  zoom.set(zoom.size);
  zoom.buttons(document.getElementById("gw-font-inc"), document.getElementById("gw-font-dec"));
  zoom.wheel(body);

  // The last search: its request and result (for the copy / save text).
  let last = null;
  const open = {}; // file index → collapsed
  const request = () => ({ dir: scope.value, query: q.value, regex: regex.checked, caseSensitive: cs.checked, days: parseInt(days.value, 10) || 0 });

  // A hit's text with its matches lit. Spans are character offsets from Go
  // (runes); a line with characters outside the BMP is split by character
  // so they still land.
  function lit(text, spans) {
    if (!spans || !spans.length) return esc(text);
    const astral = /[\uD800-\uDFFF]/.test(text);
    const chars = astral ? Array.from(text) : null;
    const slice = (a, b) => astral ? chars.slice(a, b).join("") : text.slice(a, b);
    const end = astral ? chars.length : text.length;
    let html = "", pos = 0;
    spans.forEach(([s, e]) => { if (s < pos) return; html += esc(slice(pos, s)) + "<mark class=\"gw-m\">" + esc(slice(s, e)) + "</mark>"; pos = e; });
    return html + esc(slice(pos, end));
  }
  function draw() {
    if (!last) { body.innerHTML = ""; return; }
    const res = last.res;
    let html = "";
    res.files.forEach((f, i) => {
      const folded = !!open[i];
      html += `<div class="gw-file" data-i="${i}" style="display:flex;align-items:center;gap:8px;padding:4px 12px;cursor:pointer;color:var(--text);background:var(--panel);border-top:1px solid var(--border);position:sticky;top:0">
        <span style="width:12px;display:inline-block">${folded ? "▸" : "▾"}</span><b style="flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(f.rel)}</b>
        <span class="muted" style="white-space:nowrap">${esc(t("{n} 件", { n: f.count.toLocaleString() }))}${f.count > f.hits.length ? ` <span style="color:var(--warn)">${esc(t("（{n} 件のみ表示）", { n: f.hits.length }))}</span>` : ""}</span>
        <span class="link" data-openfile="${esc(f.path)}" data-tip="${esc(t("このログを開きます"))}" style="font-size:11px">${esc(t("ログ表示"))}</span></div>`;
      if (!folded) html += f.hits.map(hit => `<div class="gw-hit" data-path="${esc(f.path)}" data-line="${hit.line}" style="display:flex;gap:10px;padding:1px 12px 1px 32px;cursor:pointer;white-space:pre;color:#cbd5e1"><span class="muted" style="min-width:5ch;text-align:right;user-select:none">${hit.line}</span><span>${lit(hit.text, hit.spans)}</span></div>`).join("");
    });
    if (noHit.checked && res.noHit.length) {
      html += `<div style="padding:8px 12px 2px;color:var(--warn);font-weight:600;border-top:1px solid var(--border);margin-top:8px">${esc(t("一致なし（{n} ファイル）", { n: res.noHit.length }))}</div>`;
      html += res.noHit.map(r => `<div class="muted" style="padding:1px 12px 1px 32px;white-space:pre">${esc(r)}</div>`).join("");
    }
    if (!res.files.length && !(noHit.checked && res.noHit.length)) html = `<div class="muted" style="padding:20px 12px;text-align:center">${esc(t("該当なし"))}</div>`;
    body.innerHTML = html;
    body.querySelectorAll(".gw-file").forEach(el => el.onclick = e => { if (e.target.dataset.openfile) return; const i = el.dataset.i; open[i] = !open[i]; draw(); });
    body.querySelectorAll("[data-openfile]").forEach(el => el.onclick = () => G().OpenLog(el.dataset.openfile, 0).catch(e => flash(t("ログを開けません") + ": " + terr(e), "err")));
    body.querySelectorAll(".gw-hit").forEach(el => {
      el.onclick = () => G().OpenLog(el.dataset.path, parseInt(el.dataset.line, 10)).catch(e => flash(t("ログを開けません") + ": " + terr(e), "err"));
      el.onmouseenter = () => { el.style.background = "rgba(56,189,248,.12)"; };
      el.onmouseleave = () => { el.style.background = ""; };
    });
  }
  function summary(res) {
    let s = t("{h} 件 / {f} ファイル（検索 {s} ファイル・{ms} ms）", { h: res.hits.toLocaleString(), f: res.files.length.toLocaleString(), s: res.searched.toLocaleString(), ms: res.elapsedMs });
    if (res.skipped) s += "　" + t("読めない／大きすぎるファイル {n}", { n: res.skipped });
    if (res.truncated) s += "　" + t("※先頭 {n} 件まで表示（範囲や文字で絞ってください）", { n: shownHits(res).toLocaleString() });
    return s;
  }
  const shownHits = res => res.files.reduce((n, f) => n + f.hits.length, 0);
  async function search() {
    const req = request();
    if (!req.query) { q.focus(); return; }
    opts = { regex: req.regex, cs: req.caseSensitive, days: req.days, noHit: noHit.checked }; saveOpts();
    go.disabled = true; sum.textContent = t("検索中…"); sum.style.color = "";
    try {
      const res = await G().Search(req);
      last = { req, res };
      for (const k in open) delete open[k];
      draw();
      sum.textContent = summary(res);
      copyBtn.disabled = saveBtn.disabled = !res.files.length && !res.noHit.length;
    } catch (e) {
      last = null; draw();
      sum.textContent = t("検索できません") + ": " + terr(e); sum.style.color = "var(--err)";
      copyBtn.disabled = saveBtn.disabled = true;
    }
    go.disabled = false;
  }
  // The result as text, one line per hit — what an editor's grep writes.
  function resultText() {
    if (!last) return "";
    const { req, res } = last;
    const lines = [
      t("検索: {q}（{m}・大文字小文字を{c}）", { q: req.query, m: req.regex ? t("正規表現") : t("文字"), c: req.caseSensitive ? t("区別する") : t("区別しない") }),
      t("場所: {w}", { w: req.dir || init.root }) + (req.days ? "　" + t("最近 {n} 日", { n: req.days }) : ""),
      summary(res), "",
    ];
    res.files.forEach(f => f.hits.forEach(hit => lines.push(`${f.rel}(${hit.line}): ${hit.text}`)));
    if (noHit.checked && res.noHit.length) { lines.push("", t("一致なし（{n} ファイル）", { n: res.noHit.length }) + ":"); res.noHit.forEach(r => lines.push(r)); }
    return lines.join("\n") + "\n";
  }
  go.onclick = search;
  q.addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); search(); } });
  noHit.onchange = () => { opts.noHit = noHit.checked; saveOpts(); draw(); if (last) copyBtn.disabled = saveBtn.disabled = !last.res.files.length && !last.res.noHit.length; };
  copyBtn.onclick = () => copyText(resultText(), t("コピーしました"));
  saveBtn.onclick = () => G().SaveText(resultText()).then(p => flash(t("保存しました: {p}", { p }))).catch(e => flash(t("保存できません") + ": " + terr(e), "err"));
  document.getElementById("gw-folder").onclick = () => G().OpenFolder(scope.value).catch(e => flash(t("フォルダを開けません") + ": " + terr(e), "err"));
  ctxExtraItems = () => last ? [[t("結果をコピー"), copyBtn.onclick]] : [];
  document.addEventListener("keydown", e => {
    if (e.ctrlKey && (e.key === "f" || e.key === "F" || e.code === "KeyF")) { e.preventDefault(); q.focus(); q.select(); return; }
    if (zoom.key(e)) { e.preventDefault(); return; }
    if (e.key === "Escape" && !document.getElementById("sc-pop")) G().Close();
  });
  q.focus();
}
