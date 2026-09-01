// Standalone terminal window (child process: PalaTerm.exe --connect <device>).
// Fills the whole window with an xterm terminal, resizable in both directions.

const Term = () => window.go.main.Term;

function b64ToBytes(b64) {
  const bin = atob(b64);
  const arr = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
  return arr;
}
function bytesToB64(bytes) {
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin);
}

async function bootTerminalWindow() {
  document.getElementById("lock").classList.add("hidden");
  document.getElementById("app").classList.add("hidden");
  document.body.style.background = "#000";

  const wrap = h(`<div style="position:fixed;inset:0;display:flex;flex-direction:column;background:#000">
    <div id="tw-bar" style="display:flex;align-items:center;justify-content:space-between;padding:6px 12px;background:#11151c;border-bottom:1px solid #2a3442;font-family:'Segoe UI',sans-serif">
      <div id="tw-title" style="color:#e6ecf3;font-size:13px">${esc(t("対話接続"))}</div>
      <span id="tw-state" class="pill run">${esc(t("接続中…"))}</span>
    </div>
    <div id="tw-host" style="flex:1;min-height:0;background:#000;padding:6px"></div>
    <div id="tw-pw" class="hidden" style="position:absolute;inset:0;display:flex;align-items:center;justify-content:center;background:rgba(0,0,0,.85)">
      <div style="width:340px;background:#1a212b;border:1px solid #2a3442;border-radius:12px;padding:24px">
        <div style="color:#e6ecf3;margin-bottom:12px">${esc(t("マスターパスワード"))}</div>
        <div id="tw-pw-field" style="position:relative">
          <input id="tw-pw-in" type="password" style="width:100%;background:#11151c;color:#e6ecf3;border:1px solid #2a3442;border-radius:8px;padding:9px" autofocus>
        </div>
        <button id="tw-pw-ok" style="width:100%;margin-top:12px;background:#16a34a;color:#fff;border:none;border-radius:8px;padding:9px;cursor:pointer">${esc(t("接続"))}</button>
      </div>
    </div>
  </div>`);
  document.body.appendChild(wrap);
  // Same show/hide eye affordance as the main window's master-password fields.
  wirePasswordToggles(document.getElementById("tw-pw-field"));

  const term = new Terminal({
    convertEol: false, cursorBlink: true,
    fontFamily: '"Consolas", monospace', fontSize: 13,
    theme: { background: "#000000", foreground: "#d6dee8", cursor: "#5b9bff" },
  });
  const fit = new (window.FitAddon.FitAddon)();
  term.loadAddon(fit);
  term.open(document.getElementById("tw-host"));
  term.focus();
  let closed = false;

  function doFit() {
    try { fit.fit(); Term().Resize(term.cols, term.rows).catch(() => {}); } catch (e) {}
  }
  setTimeout(doFit, 0);
  new ResizeObserver(() => doFit()).observe(document.getElementById("tw-host"));

  const stateEl = document.getElementById("tw-state");
  const titleEl = document.getElementById("tw-title");

  rt().EventsOn("term:data", ev => { term.write(b64ToBytes(ev.data)); });
  // Connection progress: "接続中… (n/m秒)" until ready or closed.
  let connecting = true;
  rt().EventsOn("term:progress", ev => {
    if (!connecting) return;
    stateEl.textContent = t("接続中… ({a}/{b}秒)", { a: ev.elapsed, b: ev.total });
  });
  rt().EventsOn("term:ready", ev => {
    connecting = false;
    stateEl.textContent = t("接続済み"); stateEl.className = "pill ok";
    titleEl.textContent = `${ev.device}  ${ev.host} / ${ev.conn}`;
    term.focus(); doFit();
  });
  rt().EventsOn("term:closed", ev => {
    closed = true;
    connecting = false;
    stateEl.textContent = ev.error ? t("接続失敗") : t("切断");
    stateEl.className = "pill err";
    if (ev.error) term.write("\r\n\x1b[31m" + t("[接続エラー] ") + terr(ev.error) + "\x1b[0m\r\n");
    else term.write("\r\n\x1b[33m" + t("[切断されました]") + "\x1b[0m\r\n");
    if (ev.logPath) term.write("\x1b[36m" + t("[ログ保存] ") + ev.logPath + "\x1b[0m\r\n");
  });

  term.onData(d => {
    if (closed) return;
    Term().Send(bytesToB64(new TextEncoder().encode(d))).catch(() => {});
  });
  window.addEventListener("beforeunload", () => { if (!closed) Term().Close(); });

  // Kick off: use the stdin password if present, else prompt in-window.
  const res = await Term().Start();
  if (titleEl.textContent === t("対話接続")) titleEl.textContent = res.name || t("対話接続");
  if (res.needPassword) {
    const pwOverlay = document.getElementById("tw-pw");
    pwOverlay.classList.remove("hidden");
    const submit = () => {
      const pw = document.getElementById("tw-pw-in").value;
      if (!pw) return;
      pwOverlay.classList.add("hidden");
      Term().StartWithPassword(pw);
    };
    document.getElementById("tw-pw-ok").onclick = submit;
    document.getElementById("tw-pw-in").addEventListener("keydown", e => { if (e.key === "Enter") submit(); });
    document.getElementById("tw-pw-in").focus();
  }
}
