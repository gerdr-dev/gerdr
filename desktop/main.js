// gerdr 的桌面外壳（Electron）：界面全在网页里（就是浏览器里打开的那一份）。Usually one window; each herdr-style session
// (ui/src/lib/layout.ts Space) can also get a window of its own ("open in new window", ⌘N), each page showing its own one.
//
// 为什么是 Electron、不是系统 WebView（原来的 mac 外壳是 WKWebView）/ mygo / nsh：docs/dev/WHY-ELECTRON.md。
// 一句话：界面在 Chrome 里怎么样，到 App 里就怎么样，mac 和 Windows 一个内核。
//
// 壳只做网页做不了的事：起 / 收后端进程、系统通知、Dock 角标、菜单（mac 上没有「编辑」菜单 ⌘C / ⌘V 不工作）、
// 外部链接交给默认浏览器。
const { app, BrowserWindow, Menu, Notification, dialog, ipcMain, net, shell, nativeTheme, screen } = require("electron");
const { spawn, execFile } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const updater = require("./updater");

const isMac = process.platform === "darwin";

// A second, debug App next to the one in use (its own window state, downloaded backends, single-instance lock);
// pair it with GERDR_DIR / GERDR_PORT for the backend's side.
if (process.env.GERDR_APP_DATA) app.setPath("userData", process.env.GERDR_APP_DATA);

if (!app.requestSingleInstanceLock()) {
  app.quit();
  process.exit(0);
}

// Every open window → the session its page says it shows ({ space, name }; nativeSpace in ui/src/lib/native.ts).
// main is the first one: its size and place are what window.json remembers, and on mac closing the last window only hides it.
const wins = new Map();
let main = null;
let origin = null; // 后端的地址（就绪之后才有）
let backend = null;
let quitting = false;

// ---- 后端 ----

function backendPath() {
  const exe = process.platform === "win32" ? "gerdr.exe" : "gerdr";
  if (process.env.GERDR_BIN) return process.env.GERDR_BIN;
  if (app.isPackaged) return updater.backend().path; // the bundle's, or a newer one updater.js downloaded
  return path.join(__dirname, "..", "bin", exe); // 开发时：make build 出的那个
}

// now: the person chose to update now (or we checked it's idle) — tells appAttach to restart an outdated background
// service without waiting for it to go idle.
function startBackend(now = false) {
  const bin = backendPath();
  if (!fs.existsSync(bin)) return showMessage(`找不到 gerdr 后端：${bin}`);
  // --app：就绪后打印一行 GERDR_READY <url>；已经有一个 gerdr 在跑（比如终端里起的）就直接连它，两边看到的是同一份会话。
  // --parent-pid：App 退出（哪怕是被强杀）时后端跟着退。
  const p = spawn(bin, ["--app", "--parent-pid", String(process.pid)], {
    env: { ...process.env, GERDR_FROM_APP: "1", ...(now ? { GERDR_UPGRADE_NOW: "1" } : {}) },
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  });
  backend = p;
  let out = "";
  let errTail = "";
  // GERDR_READY 可能出现第二次：App 连着的是后台服务 / 终端里那个 gerdr，它退了，我们的后端接了班
  // （cmd/gerdr 的 appAttach）。地址没变就不动，页面的 WebSocket 自己会重连上。
  p.stdout.on("data", (d) => {
    out += d.toString();
    const lines = out.split("\n");
    out = lines.pop().slice(-2000); // 最后那段还没收完一整行
    for (const line of lines) {
      const m = /^GERDR_READY (\S+)/.exec(line.trim());
      if (m && m[1] !== origin) {
        origin = m[1];
        for (const w of allWins()) w.loadURL(pageURL(wins.get(w)?.space));
      }
    }
  });
  p.stderr.on("data", (d) => {
    errTail = (errTail + d.toString()).slice(-16000);
  });
  p.on("error", (e) => showMessage(`起不来后端：${e.message}`));
  p.on("exit", (code, signal) => {
    if (backend === p) backend = null;
    if (quitting || p.replaced) return;
    // 75 = 刚打开后台常驻，这个后端让位给后台服务（cmd/gerdr 的 exitHandover）：重起一个，它只连后台
    if (code === 75) return startBackend();
    // 「已经有一个在跑」那种情况它会正常退出（code 0），那不算出错
    if (!origin || code !== 0 || signal) showMessage(`后端退出了（${signal ?? code}）\n\n${errTail.slice(-2000)}`);
  });
  setTimeout(() => {
    if (!origin) showMessage("后端 30 秒没就绪。");
  }, 30000);
}

// 退出时先让后端自己收尾（把会话日志写完），最多等 3 秒。后端关机本身 < 0.1 秒（docs：⌘Q 卡 4 秒那次修过）。
// Windows 没有 SIGTERM（kill 就是硬杀，日志来不及写）：不发信号，让它看到父进程没了自己退（waitParent）。
function stopBackend() {
  return new Promise((resolve) => {
    const p = backend;
    if (!p) return resolve();
    const t = setTimeout(() => {
      try {
        p.kill("SIGKILL");
      } catch {}
      resolve();
    }, 3000);
    p.once("exit", () => {
      clearTimeout(t);
      resolve();
    });
    if (process.platform === "win32") {
      clearTimeout(t);
      resolve();
    } else p.kill("SIGTERM");
  });
}

// Swap in the backend updater.js picked (a downloaded update). The page keeps its address; if the new one comes up
// on the same one its WebSocket just reconnects. Windows has no SIGTERM (stopBackend only waits for the parent to
// go): kill it outright there.
function restartBackend(now) {
  const p = backend;
  if (p) p.replaced = true;
  if (p && process.platform === "win32") p.kill();
  stopBackend().then(() => startBackend(now));
}

// ---- 更新（updater.js） ----

/** What a restart would cut off, from the gerdr the windows show (server busy()); null = couldn't ask. */
async function fetchBusy() {
  if (!origin) return null;
  try {
    const u = new URL(origin);
    const tok = u.searchParams.get("token");
    const r = await net.fetch(`${u.origin}/api/busy`, { headers: tok ? { Authorization: `Bearer ${tok}` } : {}, cache: "no-store" });
    return r.ok ? await r.json() : null;
  } catch {
    return null;
  }
}

const isIdle = (b) => !!b && b.turns + b.waiting + b.tasks + b.terms === 0;

function describeBusy(b) {
  if (!b) return "正在跑的 agent 和终端会被打断。";
  const parts = [];
  if (b.turns) parts.push(`${b.turns} 个 agent 正在跑`);
  if (b.waiting) parts.push(`${b.waiting} 个在等你批准`);
  if (b.tasks) parts.push(`${b.tasks} 个后台任务`);
  if (b.terms) parts.push(`${b.terms} 个终端`);
  return `重启会打断：${parts.join("、")}。空闲的会话不受影响，下次发话自己接上。`;
}

let applying = false;
// With automatic updates off, a downloaded backend still waits for a click, unless that click said "when idle".
let idleOK = "";
// The update being swapped in, until the running gerdr reports it (Settings shows "upgrading"); also keeps the poll
// from restarting again and again when a swap didn't take (e.g. the service didn't come back new).
let upgrading = { version: "", at: 0 };
const upgradingNow = (p) => !!p && upgrading.version === p.version && Date.now() - upgrading.at < 2 * 60 * 1000;

/** ask: the person clicked "update" — if something would be cut off, say what and let them pick. */
async function applyUpdate(ask) {
  const p = updater.pending();
  if (!p || applying) return;
  if (ask) {
    const b = await fetchBusy();
    if (!isIdle(b)) {
      const { response } = await dialog.showMessageBox(activeWin() ?? undefined, {
        type: "question",
        message: `更新到 ${p.version}`,
        detail: describeBusy(b) + (p.kind === "app" ? "\n\n这次更新换的是整个 App，会退出再重新打开。" : ""),
        buttons: ["现在更新", "等空闲时自动更新", "取消"],
        defaultId: 1,
        cancelId: 2,
      });
      if (response === 1) idleOK = p.version;
      if (response !== 0) return;
    }
  }
  applying = true;
  upgrading = { version: p.version, at: Date.now() };
  pushUpdate();
  try {
    if (p.kind === "app") {
      if (await updater.installApp(p.file)) app.quit();
    } else restartBackend(true);
  } finally {
    applying = false;
  }
}

let lastBusy = null; // the running gerdr's /api/busy, from the last poll

/** Everything Settings → 版本 shows (ui/src/components/UpdateSetting.tsx reads the same shape). */
function updateStatus() {
  const st = updater.state();
  const p = updater.pending();
  return {
    enabled: updater.enabled(),
    auto: updater.auto(),
    app: app.getVersion(),
    backend: updater.backend().version,
    running: lastBusy?.version ?? "",
    busy: lastBusy,
    pending: p ? { kind: p.kind, version: p.version } : null,
    phase: upgradingNow(p) ? "applying" : st.phase,
    progress: st.progress,
    downloading: st.downloading,
    error: st.error,
    checkedAt: st.checkedAt,
  };
}

/** Tell every page (window.gerdr.appUpdate in ui/src/App.tsx); a page without that hook (older UI) gets a notification instead. */
const notified = new Set();
async function pushUpdate(only) {
  const p = updater.pending();
  buildMenu();
  const arg = JSON.stringify(updateStatus());
  const shown = await Promise.all(
    (only ? [only] : allWins()).map((w) =>
      origin ? w.webContents.executeJavaScript(`!!(window.gerdr && window.gerdr.appUpdate && (window.gerdr.appUpdate(${arg}), 1))`).catch(() => false) : false,
    ),
  );
  if (only || !p || shown.some(Boolean) || notified.has(p.version) || !Notification.isSupported()) return;
  notified.add(p.version);
  const n = new Notification({ title: "Gerdr 有新版本", body: `${p.version} 已下载好，点这里更新` });
  n.on("click", () => applyUpdate(true));
  n.show();
}

// Every 15s: what would a restart cut off, is the running gerdr behind what we have (noteRunning), and if an update
// is waiting and nothing would be cut off, apply it (with automatic updates on, see the check below). A whole-App update quits and reopens the windows, so that one
// always waits for a click.
function watchUpdates() {
  if (!updater.enabled()) return;
  updater.start(() => pushUpdate());
  const poll = async () => {
    const b = await fetchBusy();
    if (!b) return;
    const changed = JSON.stringify(b) !== JSON.stringify(lastBusy);
    lastBusy = b;
    const before = updater.pending();
    updater.noteRunning(b.version); // pushes by itself when what's pending changes
    const p = updater.pending();
    if (changed && p === before) pushUpdate();
    if (p?.kind !== "backend" || !isIdle(b) || upgradingNow(p)) return;
    // Automatic updates off: a downloaded release waits for a click. Catching the service up with the bundle (an App
    // installed by hand) isn't an update to hold back: that one goes ahead either way.
    if (!updater.auto() && !updater.backend().bundled && idleOK !== p.version) return;
    applyUpdate(false);
  };
  setTimeout(poll, 5 * 1000);
  setInterval(poll, 15 * 1000);
}

async function checkFromMenu() {
  if (updater.pending()) return applyUpdate(true);
  const msg = await updater.check();
  if (!updater.pending()) dialog.showMessageBox(activeWin() ?? undefined, { message: "检查更新", detail: msg });
}

// ---- 窗口 ----

const statePath = () => path.join(app.getPath("userData"), "window.json");

function loadState() {
  try {
    const s = JSON.parse(fs.readFileSync(statePath(), "utf8"));
    // 上次在的那块屏幕没了（拔了外接屏）就别用旧位置，不然窗口开在看不见的地方
    const visible = screen.getAllDisplays().some((d) => {
      const a = d.workArea;
      return s.x >= a.x - 50 && s.y >= a.y - 50 && s.x < a.x + a.width && s.y < a.y + a.height;
    });
    return visible ? s : { width: s.width, height: s.height };
  } catch {
    return { width: 1280, height: 820 };
  }
}

function saveState() {
  if (!main || main.isDestroyed()) return;
  const b = main.getNormalBounds();
  try {
    fs.writeFileSync(statePath(), JSON.stringify({ ...b, maximized: main.isMaximized() }));
  } catch {}
}

const allWins = () => [...wins.keys()].filter((w) => !w.isDestroyed());

/** The window a menu command is for: the focused one of ours, else the main one. */
function activeWin() {
  const f = BrowserWindow.getFocusedWindow();
  return f && wins.has(f) ? f : main && !main.isDestroyed() ? main : allWins()[0] ?? null;
}

/**
 * The page's address, optionally opened on one project (/<project id>, which the page switches to). origin is the login
 * link (/auth?token=…), so the page path goes in its `next` (see rescueURL).
 */
function pageURL(space) {
  const u = new URL(origin);
  const page = space ? `/${encodeURIComponent(space)}` : "/";
  if (u.pathname === "/auth") u.searchParams.set("next", page);
  else {
    u.pathname = page;
    u.search = "";
  }
  return u.toString();
}

function createWindow(space) {
  const first = !main || main.isDestroyed();
  let st = first ? loadState() : null;
  if (!first) {
    // A further window: same size as the one it was opened from, nudged down-right so it doesn't sit exactly on top
    const b = (activeWin() ?? main).getNormalBounds();
    st = { width: b.width, height: b.height, x: b.x + 28, y: b.y + 28 };
  }
  const w = new BrowserWindow({
    ...st,
    minWidth: 640,
    minHeight: 420,
    title: "Gerdr",
    show: false,
    backgroundColor: nativeTheme.shouldUseDarkColors ? "#0f0f0f" : "#ffffff",
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      spellcheck: false,
    },
  });
  wins.set(w, { space });
  if (first) main = w;
  if (st.maximized) w.maximize();
  w.once("ready-to-show", () => w.show());
  for (const ev of ["resize", "move", "close"]) w.on(ev, () => w === main && saveState());
  // mac：关掉最后一个窗口 = 藏起来（点 Dock 图标再出来），后端和会话照跑；⌘Q 才是真退出。Extra windows really close.
  w.on("close", (e) => {
    if (isMac && !quitting && allWins().length === 1) {
      e.preventDefault();
      w.hide();
    }
  });
  w.on("closed", () => {
    wins.delete(w);
    if (main === w) main = allWins()[0] ?? null;
  });

  const wc = w.webContents;
  // 外部链接交给默认浏览器；后端自己的地址留在窗口里
  wc.setWindowOpenHandler(({ url }) => {
    openExternal(url);
    return { action: "deny" };
  });
  wc.on("will-navigate", (e, url) => {
    if (sameOrigin(url) || url.startsWith("data:")) return;
    e.preventDefault();
    openExternal(url);
  });
  // 页面进程崩了 / 被系统收了：重新载入（会话都在后端，界面重开就接上）
  wc.on("render-process-gone", (_e, d) => {
    if (d.reason !== "clean-exit" && origin) setTimeout(() => !w.isDestroyed() && wc.loadURL(pageURL(wins.get(w)?.space)), 500);
  });

  if (origin) w.loadURL(pageURL(space));
  else showMessage("启动中…", w);
  return w;
}

function sameOrigin(url) {
  try {
    return !!origin && new URL(url).origin === new URL(origin).origin;
  } catch {
    return false;
  }
}

function openExternal(url) {
  if (/^(https?|mailto):/i.test(url)) shell.openExternal(url);
}

function showMessage(text, only) {
  const targets = only ? [only] : allWins();
  if (!targets.length) return;
  const esc = text.replace(/&/g, "&amp;").replace(/</g, "&lt;");
  const dark = nativeTheme.shouldUseDarkColors;
  const html = `<html><body style="font:13px -apple-system,'Segoe UI',sans-serif;color:#888;display:flex;height:100vh;margin:0;align-items:center;justify-content:center;background:${dark ? "#0f0f0f" : "#fff"}"><pre style="white-space:pre-wrap;max-width:640px;font:inherit">${esc}</pre></body></html>`;
  for (const w of targets) w.loadURL("data:text/html;charset=utf-8," + encodeURIComponent(html));
}

function focusWindow(w = activeWin()) {
  if (!w) return void createWindow();
  if (w.isMinimized()) w.restore();
  w.show();
  w.focus();
}

function runInPage(js, w = activeWin()) {
  if (w && !w.isDestroyed() && origin) w.webContents.executeJavaScript(js).catch(() => {});
}

// Every window's page notifies on its own, so two windows would pop the same notification twice: drop repeats for a few seconds
const recentNotes = new Map();
function seenNote(key) {
  const now = Date.now();
  for (const [k, t] of recentNotes) if (now - t > 5000) recentNotes.delete(k);
  if (recentNotes.has(key)) return true;
  recentNotes.set(key, now);
  return false;
}

// Live notifications: an Electron Notification that gets garbage collected loses its click handler
const liveNotes = new Set();

// macOS only delivers UNUserNotificationCenter notifications for properly code-signed apps; the ad-hoc signed build
// (adhoc-sign.js, no Developer ID) gets "Notifications are not allowed for this application" and nothing shows.
// Fall back to AppleScript's `display notification`: it shows up attributed to Script Editor and clicking it can't
// bring up the session, but the user does learn an agent is waiting. Text goes in as argv, never spliced into the script.
function scriptNotify(title, body) {
  if (!isMac) return;
  const script = "on run argv\ndisplay notification (item 2 of argv) with title (item 1 of argv) sound name \"Glass\"\nend run";
  execFile("osascript", ["-e", script, title, body], () => {});
}

// ---- 网页 → 壳 ----

ipcMain.on("gerdr", (e, m) => {
  const win = BrowserWindow.fromWebContents(e.sender);
  if (!m || typeof m.type !== "string" || !win || !wins.has(win)) return;
  switch (m.type) {
    case "notify": {
      if (seenNote(`${m.title}\n${m.body}\n${m.sessionId}`)) return;
      const title = String(m.title ?? "Gerdr");
      const body = String(m.body ?? "");
      if (!Notification.isSupported()) return void scriptNotify(title, body);
      const n = new Notification({ title, body, silent: false });
      liveNotes.add(n);
      if (liveNotes.size > 50) liveNotes.delete(liveNotes.values().next().value); // ones left in Notification Center never close
      n.on("close", () => liveNotes.delete(n));
      n.on("failed", () => {
        liveNotes.delete(n);
        scriptNotify(title, body);
      });
      n.on("click", () => {
        liveNotes.delete(n);
        // The window showing the conversation's session if there is one, else the one that notified (its page switches over)
        const target = allWins().find((w) => m.space && wins.get(w)?.space === m.space) ?? (win.isDestroyed() ? activeWin() : win);
        focusWindow(target);
        if (m.sessionId) runInPage(`window.gerdr && window.gerdr.focusSession(${JSON.stringify(String(m.sessionId))})`, target);
      });
      n.show();
      break;
    }
    case "badge": {
      const n = Number(m.count) || 0;
      if (isMac) app.setBadgeCount(n);
      if (n > 0 && !allWins().some((w) => w.isFocused())) {
        if (isMac) app.dock?.bounce("informational");
        else win.flashFrame(true);
      }
      break;
    }
    case "space":
      wins.set(win, { space: String(m.id ?? ""), name: String(m.name ?? "") });
      break;
    case "newWindow":
      focusWindow(createWindow(String(m.space ?? "") || undefined));
      break;
    case "open":
      openExternal(String(m.url ?? ""));
      break;
    case "openPath": {
      // 对话里的图片 / 视频下面那两个按钮（ChatMedia）：用系统程序打开、在访达里显示。相对路径按会话目录算
      let p = String(m.path ?? "");
      if (p.startsWith("~/")) p = path.join(app.getPath("home"), p.slice(2));
      if (!path.isAbsolute(p)) p = path.resolve(String(m.cwd || app.getPath("home")), p);
      if (!fs.existsSync(p)) break;
      if (m.reveal) shell.showItemInFolder(p);
      else shell.openPath(p);
      break;
    }
    case "menuKeys":
      // 菜单上的键跟着界面设置里的快捷键走（ui/src/lib/keys.ts 的 DEFAULT_KEYS + 用户改的）
      if (m.keys && typeof m.keys === "object") {
        menuKeys = m.keys;
        buildMenu();
      }
      break;
    case "update":
      // the page asking for the update status (it just loaded), "check now" / "update now" in Settings or the toast
      if (typeof m.auto === "boolean") {
        updater.setAuto(m.auto);
        pushUpdate(win);
      } else if (m.apply) applyUpdate(true);
      else if (m.check) updater.check().then(() => pushUpdate(win));
      else pushUpdate(win);
      break;
    case "webPid":
      // 设置里的「资源」要量界面所在那个渲染进程的内存（它不是后端起的，后端看不见）
      runInPage(`window.__GERDR_WEB_PID__ = ${win.webContents.getOSProcessId()}`, win);
      break;
  }
});

// ---- 菜单 ----
// mac：菜单是必需的（「编辑」那几项承担 ⌘C / ⌘V / ⌘A / ⌘Z），其余照原来 mac 外壳那套。
// Windows：不要菜单栏 —— 菜单的加速键会先于网页吃掉 Ctrl+C / Ctrl+W 这些（网页自己有一套快捷键：
// Ctrl+C 两次关 agent、前缀键……），复制粘贴 Chromium 自己会处理。

// origin 是带令牌的登录链接（/auth?token=…）：直接在后面拼路径会变成错的令牌（踩过：「bad token」）。
// 走登录再跳过去（/auth 认 next），cookie 过期了也进得去。
function rescueURL() {
  const u = new URL(origin);
  if (u.pathname === "/auth") u.searchParams.set("next", "/rescue");
  else u.pathname = "/rescue";
  return u.toString();
}

// 急救面板挂在页面右边：页面在就直接拉出来（不刷新，主界面现在什么样就什么样）；
// 页面没加载出来 / 不是 gerdr 的页面，就走 /rescue 重新打开（会跳回主界面并把面板打开）。
async function openRescue() {
  const win = activeWin();
  if (!origin || !win) return;
  const ok = await win.webContents.executeJavaScript("!!(window.__gerdrRescue && (window.__gerdrRescue.open(), 1))").catch(() => false);
  if (!ok) win.loadURL(rescueURL());
}

// 界面发来的「动作 → 键」（null = 不挂键）。没发过（界面还没起来、老界面）就用下面写的默认。
// 按键先到网页、网页自己处理掉，菜单收不到；菜单上的键主要是显示用，外加界面坏了时还能点。
let menuKeys = {};

function cmd(label, id, fallback) {
  const accelerator = id in menuKeys ? menuKeys[id] || undefined : fallback;
  // runInPage goes to the focused window
  return { label, accelerator, click: () => runInPage(`window.gerdr && window.gerdr.runCommand(${JSON.stringify(id)})`) };
}

// ⌘N: another window on the session the focused one shows (switch it there with the session bar)
const newWindow = () => focusWindow(createWindow(wins.get(activeWin())?.space || undefined));

function buildMenu() {
  if (!isMac) return Menu.setApplicationMenu(null);
  try {
    buildMacMenu();
  } catch (e) {
    // 界面发来的键 Electron 不认（写法怪）：退回默认的键，别让菜单整个没了
    console.error("menu:", e);
    menuKeys = {};
    buildMacMenu();
  }
}

function buildMacMenu() {
  const tabs = [];
  for (let i = 1; i <= 9; i++) tabs.push(cmd(`tab ${i}`, `tab${i}`, `Cmd+${i}`));
  Menu.setApplicationMenu(
    Menu.buildFromTemplate([
      {
        label: "Gerdr",
        submenu: [
          { role: "about", label: "关于 Gerdr" },
          { label: updater.pending() ? `更新到 ${updater.pending().version}…` : "检查更新…", enabled: updater.enabled(), click: checkFromMenu },
          { type: "separator" },
          cmd("设置…", "settings", "Cmd+,"),
          { type: "separator" },
          { role: "hide", label: "隐藏 Gerdr" },
          { role: "hideOthers", label: "隐藏其他" },
          { type: "separator" },
          { role: "quit", label: "退出 Gerdr" },
        ],
      },
      {
        label: "文件",
        submenu: [{ label: "新窗口", accelerator: "Cmd+N", click: newWindow }, { type: "separator" }, cmd("新建 tab", "newTab", "Cmd+T"), cmd("新工作空间", "newWorkspace", "Cmd+Shift+N"), cmd("会话列表", "sessions", "Cmd+O"), { type: "separator" }, cmd("关闭 pane", "closePane", "Cmd+W"), cmd("重新打开刚关的 tab / pane", "reopenClosed", "Cmd+Shift+T")],
      },
      {
        label: "编辑",
        submenu: [
          { role: "undo", label: "撤销" },
          { role: "redo", label: "重做" },
          { type: "separator" },
          { role: "cut", label: "剪切" },
          { role: "copy", label: "复制" },
          { role: "paste", label: "粘贴" },
          { role: "selectAll", label: "全选" },
        ],
      },
      {
        label: "显示",
        submenu: [
          cmd("命令面板", "palette", "Cmd+K"),
          cmd("显示 / 隐藏侧栏", "toggleSidebar", "Cmd+Ctrl+S"),
          // ⌘D 复制焦点那个（会话 / 终端）；空面板的分屏走 prefix 键，菜单上不挂 ⌘ 键
          cmd("复制到右边", "duplicateRight", "Cmd+D"),
          cmd("复制到下面", "duplicateDown", "Cmd+Shift+D"),
          cmd("左右分屏（空面板）", "splitRight"),
          cmd("上下分屏（空面板）", "splitDown"),
          cmd("放大 / 还原 pane", "zoom", "Cmd+Shift+Enter"),
          { type: "separator" },
          { role: "zoomIn", label: "放大", accelerator: "Cmd+=" },
          { role: "zoomOut", label: "缩小" },
          { role: "resetZoom", label: "实际大小" },
          { type: "separator" },
          {
            label: "重新载入",
            accelerator: "Cmd+R",
            click: () => {
              const w = activeWin();
              if (w) origin ? w.loadURL(pageURL(wins.get(w)?.space)) : w.reload();
            },
          },
          { label: "开发者工具", accelerator: "Cmd+Alt+I", click: () => activeWin()?.webContents.toggleDevTools() },
          { label: "在浏览器里打开", click: () => origin && shell.openExternal(origin) },
        ],
      },
      {
        label: "前往",
        submenu: [cmd("下一个 tab", "nextTab", "Cmd+Shift+]"), cmd("上一个 tab", "prevTab", "Cmd+Shift+["), ...tabs, { type: "separator" }, cmd("跳到在等你的会话", "focusAttention", "Cmd+J"), { type: "separator" }, cmd("下一个 session", "nextSpace", "Cmd+Ctrl+]"), cmd("上一个 session", "prevSpace", "Cmd+Ctrl+[")],
      },
      { role: "windowMenu", label: "窗口" },
      {
        label: "帮助",
        submenu: [
          cmd("快捷键", "help", "Cmd+/"),
          { type: "separator" },
          // 急救面板编进了后端、不在界面目录里：主界面改坏了也打得开（菜单项不经过页面，页面坏了也点得动）
          { label: "急救：界面坏了时叫 agent 修", click: openRescue },
        ],
      },
    ]),
  );
}

// ---- 生命周期 ----

app.on("second-instance", () => focusWindow());
app.on("activate", () => focusWindow());
app.on("window-all-closed", () => {
  if (!isMac) app.quit();
});

let stopped = false;
app.on("before-quit", (e) => {
  quitting = true;
  saveState();
  if (stopped) return;
  e.preventDefault();
  stopBackend().then(() => {
    stopped = true;
    app.quit();
  });
});

app.whenReady().then(() => {
  if (process.platform === "win32") app.setAppUserModelId("io.github.zbysir.gerdr"); // Windows 通知要它才显示得出来
  buildMenu();
  createWindow();
  startBackend();
  watchUpdates();
});
