// バージョン情報 (管理; 最新版の確認 until the about dialog moved in):
// compares this build with the newest public release on
// GitHub and downloads its PalaTerm.exe on request (update_app.go). Nothing
// goes over the network until a button is pressed.
// User-visible strings are Japanese source text wrapped in t() (see i18n.js).

let UPD = { info: null, busy: "", error: "", path: "" };

function renderUpdate() {
  const root = document.getElementById("tab-update");
  const i = UPD.info;
  root.innerHTML = `
    <div class="page-head">
      <div><div class="page-title">${esc(t("バージョン情報"))}</div>
        <div class="page-sub">${esc(t("GitHub の公開リリースと、この PalaTerm の版を比べます。ボタンを押したときだけ通信します"))}</div></div>
    </div>
    <div class="panel" style="padding:18px 20px;max-width:760px">
      <div class="fl-kv" style="grid-template-columns:150px 1fr">
        <label>${esc(t("この PalaTerm"))}</label><b>v${esc(i ? i.current : APP_VERSION)}</b>
        ${i ? `<label>${esc(t("最新版"))}</label><div><b>v${esc(i.latest)}</b> <span class="muted" style="font-size:12px">${esc(i.published ? t("（{d} 公開）", { d: i.published }) : "")}</span></div>` : ""}
      </div>
      ${i && !i.newer ? `<div class="upd-ok">${esc(t("お使いの版は最新です"))}</div>` : ""}
      ${i && i.newer ? `<div class="upd-new">
        <div style="font-size:15px;font-weight:600">${esc(t("新しい版があります（v{c} → v{v}）", { c: i.current, v: i.latest }))}</div>
        ${!UPD.path ? (i.hasExe ? `<div class="muted" style="font-size:12px;margin:4px 0 10px">${esc(t("最新版の PalaTerm.exe を {d} に保存します（今の PalaTerm はそのまま動きます）", { d: i.saveDir || t("PalaTerm のフォルダ") }))}</div>
          <button class="btn run upd-big" id="upd-dl" ${UPD.busy ? "disabled" : ""}>${esc(UPD.busy === "dl" ? t("ダウンロード中…") : t("⬇ 最新版をダウンロード（{m} MB）", { m: (i.exeSize / 1048576).toFixed(1) }))}</button>`
          : `<div class="muted" style="font-size:12px;margin-top:4px">${esc(t("最新版に PalaTerm.exe が添付されていません。マニュアルのページから入手してください"))}</div>`)
        : `<div style="margin-top:8px;line-height:1.9">✓ ${esc(t("ダウンロードしました: {p}", { p: UPD.path }))}</div>
          <div class="section-label" style="margin:10px 0 4px">${esc(t("あとの手順"))}</div>
          <ol class="upd-steps">
            <li>${esc(t("PalaTerm の窓をすべて閉じます（対話接続の窓も。トレイにいるときは、トレイのアイコンの右クリック → 終了）"))}</li>
            <li>${esc(i.inAppDir ? t("今の PalaTerm.exe を消してから、ダウンロードしたファイルの名前を PalaTerm.exe に変えます（同じフォルダにあります）")
              : t("ダウンロードしたファイルの名前を PalaTerm.exe に変えて、今の PalaTerm.exe に上書きします"))}</li>
            <li>${esc(t("PalaTerm.exe を起動します。データ（data・logs などのフォルダ）はそのまま使えます"))}</li>
          </ol>
          <div class="row-inline" style="gap:8px;margin-top:8px;flex-wrap:wrap">
            <button class="btn primary" id="upd-folder">${esc(t("ダウンロードしたフォルダを開く"))}</button>
            ${i.inAppDir ? "" : `<button class="btn" id="upd-appdir">${esc(t("今の PalaTerm のフォルダを開く"))}</button>`}
          </div>`}
      </div>` : ""}
      ${UPD.error ? `<div class="warn-box err" style="margin-top:14px">${esc(terr(UPD.error))}</div>` : ""}
      <div class="row-inline" style="gap:8px;margin-top:16px;flex-wrap:wrap">
        <button class="btn ${i ? "" : "primary"}" id="upd-check" ${UPD.busy ? "disabled" : ""}>${esc(UPD.busy === "check" ? t("確認中…") : i ? t("もう一度確認する") : t("最新版を確認する"))}</button>
        ${i || UPD.error ? `<button class="btn" id="upd-page" data-tip="${esc(LANG === "ja" ? t("公開している日本語のマニュアルの、最新版の「変更点」の節をブラウザで開きます") : t("リリースのページを開く"))}">${esc(LANG === "ja" ? "マニュアルで変更点を見る" : t("リリースのページを開く"))}</button>` : ""}
      </div>
      ${i && (i.notes || []).length ? `<div class="section-label" style="margin:18px 0 6px">${esc(t("この版から最新版までの変更点（{n} 版分・新しい順）", { n: i.notes.length }))}</div>
        ${i.lang !== (LANG === "ja" ? "ja" : "en") ? `<div class="muted" style="font-size:12px;margin-bottom:6px">${esc(t("日本語の説明を読めなかったため、英語のリリースノートを表示しています"))}</div>` : ""}
        <div class="upd-notes">${i.notes.map(n => `<div class="upd-note">
          <div class="upd-note-h"><b>v${esc(n.version)}</b>${n.title ? ` <span>${esc(n.title)}</span>` : ""}<span class="muted" style="font-size:12px;margin-left:auto">${esc(n.published || "")}</span></div>
          <div class="upd-note-b">${updNoteHtml(n.body)}</div></div>`).join("")}</div>` : ""}
    </div>
    <div class="about-card">
      <div class="brand"><span class="logo">Pala</span>Term</div>
      <div class="brand-sub" style="margin-bottom:0">Parallel SSH / Telnet / Serial Access</div>
      <div class="about-rows">${esc(t("作者"))}: <span id="ab-author">${esc(APP_AUTHOR)}</span><br>MIT License &copy; 2026 ${esc(APP_AUTHOR)}</div>
      <div class="about-links"><a href="#" id="ab-github">GitHub</a>${LANG === "ja" ? `<span>·</span><a href="#" id="ab-manual">マニュアル</a>` : ""}</div>
    </div>`;
  const q = s => root.querySelector(s);
  q("#upd-check").onclick = async () => {
    const was = UPD;
    UPD = { info: null, busy: "check", error: "", path: "" };
    renderUpdate();
    try { UPD.info = await App().CheckLatestVersion(); } catch (e) { UPD.error = terr(e); }
    // The exe downloaded before this check is still the latest: its steps
    // stay on the page.
    if (UPD.info && was.info && was.path && UPD.info.latest === was.info.latest) UPD.path = was.path;
    UPD.busy = "";
    renderUpdate();
  };
  if (q("#upd-dl")) q("#upd-dl").onclick = async () => {
    // Nothing is fetched until the user says yes here.
    const i = UPD.info;
    const ok = await uiConfirm({
      title: t("最新版のダウンロード"),
      message: t("PalaTerm v{v} の PalaTerm.exe（{m} MB）を GitHub の公開リリースからダウンロードして、{d} に保存します。よろしいですか？", { v: esc(i.latest), m: (i.exeSize / 1048576).toFixed(1), d: esc(i.saveDir || t("PalaTerm のフォルダ")) })
        + `<br><span class="muted" style="font-size:12px">${esc(i.hasDigest ? t("保存したファイルは、リリースに登録された SHA-256 と照合します。今の PalaTerm は置き換えず、起動もしません")
          : t("このリリースには SHA-256 の登録がないため、保存したファイルは大きさだけ照合します。今の PalaTerm は置き換えず、起動もしません"))}</span>`,
      okLabel: t("ダウンロードする"),
    });
    if (!ok) return;
    UPD.busy = "dl"; UPD.error = ""; UPD.path = "";
    renderUpdate();
    try { UPD.path = await App().DownloadLatestVersion(); } catch (e) { UPD.error = terr(e); }
    UPD.busy = "";
    renderUpdate();
  };
  if (q("#upd-folder")) q("#upd-folder").onclick = () => App().OpenUpdateFolder().catch(e => toast(t("開けませんでした") + ": " + terr(e), "err"));
  if (q("#upd-appdir")) q("#upd-appdir").onclick = () => App().OpenAppFolder().catch(e => toast(t("開けませんでした") + ": " + terr(e), "err"));
  if (q("#upd-page")) q("#upd-page").onclick = () => App().OpenReleasePage(UPD.info ? UPD.info.latest : "");
  q("#ab-github").onclick = e => { e.preventDefault(); App().OpenProjectPage("github"); };
  if (q("#ab-manual")) q("#ab-manual").onclick = e => { e.preventDefault(); App().OpenProjectPage("manual"); };
  // Not a button on purpose: three clicks in a row on the author's name.
  q("#ab-author").onclick = e => { if (e.detail === 3) showAuthorIcon(); };
}

// AUTHOR_ICON: the image the three clicks pop up (a file in frontend/dist);
// empty = the AUTHOR_HELLO line instead. Not translated on purpose.
const AUTHOR_ICON = "";
const AUTHOR_HELLO = "It's-a me, Kojio! 🎉";

// showAuthorIcon pops the author's icon (or hello) up over the page; a click
// anywhere or Esc puts it away.
function showAuthorIcon() {
  if (document.querySelector(".author-pop")) return;
  const ov = document.createElement("div");
  ov.className = "author-pop";
  ov.innerHTML = AUTHOR_ICON ? `<img src="${esc(AUTHOR_ICON)}" alt="">` : `<div class="author-hello">${esc(AUTHOR_HELLO)}</div>`;
  document.body.appendChild(ov);
  const done = () => { ov.remove(); document.removeEventListener("keydown", onKey, true); };
  const onKey = e => { if (e.key === "Escape") { e.preventDefault(); e.stopImmediatePropagation(); done(); } };
  document.addEventListener("keydown", onKey, true);
  ov.onclick = done;
}

// updNoteHtml shows a note's Markdown plainly: its text escaped, **bold**
// and `code` kept, a "- " line as a list item.
function updNoteHtml(md) {
  return String(md || "").split(/\r?\n/).map(l => {
    let h = esc(l).replace(/\*\*(.+?)\*\*/g, "<b>$1</b>").replace(/`([^`]+)`/g, "<code>$1</code>");
    if (/^\s*[-*] /.test(l)) h = `<div class="upd-li">${h.replace(/^\s*[-*] /, "")}</div>`;
    else if (/^#+ /.test(l)) h = `<div class="upd-sub">${h.replace(/^#+ /, "")}</div>`;
    else h = l.trim() ? `<div>${h}</div>` : `<div style="height:6px"></div>`;
    return h;
  }).join("");
}
