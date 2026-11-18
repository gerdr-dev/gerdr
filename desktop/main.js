// Gerdr's desktop App: Electron as the window system, Gerdr's own logic in a Go process beside it (desktop/core).
//
// Why Electron (again): docs/dev/WHY-ELECTRON-COMEBACK.md. In short, the Browser pane shows a project's page inside the
// App's window (a WebContentsView), which the Lightron shell could only fake with a separate window glued on top.
//
// Split of the work. This file does what only Electron can: windows, the pages' bridge (preload.js), the views of
// projects' pages, menus, Dock, notifications' clicks, quitting. desktop/core (Go, the old tools/chromeshell logic) does
// what is Gerdr's: starting / attaching to the backend, updates (internal/selfupdate), what the menus hold, how a
// notification click finds its window, the views' protocol with the backend (/api/browser/ext). They talk over the
// core's stdin / stdout, one JSON object a line (desktop/core/el.go has the other half and the list of messages).
//
// Two modes:
// - the App (default);
// - --gerdr-browser: no window of the UI, no Dock icon. The backend starts it while the App is closed, as the projects'
//   browser (internal/projbrowser): the same program on the same partitions, so a sign-in in the App is the same cookies
//   (docs/dev/BROWSER-PANE.md). The App starting stops it first (the data directory is one process's at a time).
const { app, BrowserWindow, WebContentsView, Menu, Notification, session, shell, nativeTheme, screen, ipcMain, dialog } = require("electron");
const { spawn } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const readline = require("node:readline");

const isMac = process.platform === "darwin";
const browserOnly = process.argv.includes("--gerdr-browser");
const dev = !app.isPackaged;

// ---- where things live ----

// The App's name: the bundle's (Gerdr, Gerdr exp…); in development a name of its own, so it never shares data with
// the App in use.
// The bundle's executable is named after it (pack.js); app.getName() would be package.json's productName, Gerdr for
// every build.
const appName = dev ? process.env.GERDR_SHELL_NAME || "Gerdr dev shell" : path.basename(process.execPath);
app.setName(appName);
// "<name> Shell": the directory the Lightron shell used (update prefs, downloaded backends: the core's -data). The
// browser data (Electron's userData: the UI's localStorage, the projects' partitions) goes in electron/ inside it.
// Windows: %LOCALAPPDATA%\<name>, the WebView2 shell's (tools/winshell: its update prefs, downloaded backends).
const shellDir =
  process.env.GERDR_SHELL_DATA ||
  (process.platform === "win32" ? path.join(process.env.LOCALAPPDATA || app.getPath("appData"), appName) : path.join(app.getPath("appData"), appName + " Shell"));

// What the package says about itself (pack.js writes gerdr-app.json next to the core): its version, its shell id
// (tools/shell-id.sh: an update with the same id only needs the backend) and its id (Windows' AppUserModelID, which
// notifications need).
const pkg = (() => {
  try {
    return JSON.parse(fs.readFileSync(path.join(process.resourcesPath, "gerdr-app.json"), "utf8"));
  } catch {
    return {};
  }
})();
if (process.platform === "win32" && pkg.id) app.setAppUserModelId(pkg.id);
app.setPath("userData", path.join(shellDir, "electron"));
fs.mkdirSync(app.getPath("userData"), { recursive: true });

// From the shell before: the UI's per-device settings are localStorage under http://gerdr in its browser profile
// (macOS, Lightron: <name> Shell/profile/Default; Windows, WebView2: <name>\WebView2\EBWebView\Default). Copied
// over once, before any page loads, so the switch doesn't reset them.
(() => {
  const from = [path.join(shellDir, "profile", "Default", "Local Storage"), path.join(shellDir, "WebView2", "EBWebView", "Default", "Local Storage")].find((p) =>
    fs.existsSync(p),
  );
  const to = path.join(app.getPath("userData"), "Local Storage");
  if (browserOnly || fs.existsSync(to) || !from) return;
  try {
    fs.cpSync(from, to, { recursive: true, filter: (src) => !/\bLOCK$/.test(src) });
  } catch (e) {
    console.error("[shell] settings from the old App:", e);
  }
})();

// ---- engine switches (before ready) ----

// The pages live at http://gerdr/: the origin the UI's per-device settings (localStorage) have been kept under since
// 0.2.23, and the name a notification shows. Mapped to the backend's port, known here only as the configured one;
// a backend that comes up elsewhere relaunches the App with the right one (onReady).
const port = Number(process.env.GERDR_PORT) || 7870;
app.commandLine.appendSwitch("host-rules", `MAP gerdr 127.0.0.1:${port}`);
// Device-bound sessions would tie a Google sign-in to one program (internal/projbrowser noBoundSessions)
app.commandLine.appendSwitch("disable-features", "EnableBoundSessionCredentials,DeviceBoundSessions");
// A project's page keeps painting when the App's window is covered or minimized: a phone may be watching it
app.commandLine.appendSwitch("disable-backgrounding-occluded-windows");
app.commandLine.appendSwitch("disable-renderer-backgrounding");
app.commandLine.appendSwitch("disable-background-timer-throttling");

// Development: the DevTools protocol on a port, to drive the App's pages from a script (never in the packaged App)
if (dev && process.env.GERDR_SHELL_DEBUG_PORT) app.commandLine.appendSwitch("remote-debugging-port", process.env.GERDR_SHELL_DEBUG_PORT);

// ---- one at a time ----

// The browser-only process holds the data directory while the App is closed: the App asks it to go (its pid file),
// then takes the lock.
const browserPidFile = path.join(app.getPath("userData"), "gerdr-browser.pid");
function takeLock() {
  // Asked once: a second requestSingleInstanceLock after a failed one fails again (measured), so the browser-only
  // process is sent away first
  if (!browserOnly) stopBrowserOnly();
  return app.requestSingleInstanceLock({ browserOnly });
}
function stopBrowserOnly() {
  let pid = 0;
  try {
    pid = Number(fs.readFileSync(browserPidFile, "utf8"));
  } catch {}
  if (!pid) return;
  try {
    process.kill(pid, "SIGTERM");
  } catch {
    return; // not running
  }
  const until = Date.now() + 8000;
  while (Date.now() < until) {
    try {
      process.kill(pid, 0);
    } catch {
      return; // gone
    }
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 100);
  }
}
if (!takeLock()) {
  app.quit();
  process.exit(0);
}
if (browserOnly) {
  fs.writeFileSync(browserPidFile, String(process.pid));
  process.on("SIGTERM", () => app.quit());
  app.on("will-quit", () => {
    try {
      if (fs.readFileSync(browserPidFile, "utf8") === String(process.pid)) fs.unlinkSync(browserPidFile);
    } catch {}
  });
}

// ---- the core ----

let core = null;
let coreReady = false;
let quitting = false;
let nextReply = 1;
const replies = new Map();
const log = (...a) => console.error("[shell]", ...a);

function coreBinary() {
  if (process.env.GERDR_CORE) return process.env.GERDR_CORE;
  const exe = process.platform === "win32" ? "gerdr-shell.exe" : "gerdr-shell";
  if (!dev) return path.join(process.resourcesPath, exe);
  return path.join(__dirname, "core", exe); // go build -o desktop/core/gerdr-shell ./desktop/core
}

function startCore() {
  const args = ["-data", shellDir, "-name", appName];
  if (browserOnly) args.push("-browser", process.env.GERDR_BROWSER_BACKEND || "");
  if (process.env.GERDR_BIN) args.push("-gerdr", process.env.GERDR_BIN);
  core = spawn(coreBinary(), args, {
    stdio: ["pipe", "pipe", "inherit"],
    env: {
      ...process.env,
      GERDR_SHELL_PACKAGED: dev ? "" : "1",
      GERDR_SHELL_PID: String(process.pid),
      GERDR_APP_VERSION: pkg.version || "",
      GERDR_SHELL_ID: pkg.shell || "",
    },
    windowsHide: true,
  });
  readline.createInterface({ input: core.stdout }).on("line", (line) => {
    let m;
    try {
      m = JSON.parse(line);
    } catch {
      return log("core:", line);
    }
    handle(m).then(
      (result) => m.id && send({ ev: "reply", id: m.id, ok: true, result: result ?? null }),
      (err) => m.id && send({ ev: "reply", id: m.id, ok: false, error: String(err?.message || err) }),
    );
  });
  core.on("exit", (code) => {
    core = null;
    if (!quitting) {
      log("the core exited", code);
      if (!browserOnly) dialog.showErrorBox(appName, `Gerdr's shell process exited (${code}). See ~/Library/Logs/${appName}.log`);
      app.exit(1);
    } else app.exit(0);
  });
}

function send(m) {
  if (core?.stdin.writable) core.stdin.write(JSON.stringify(m) + "\n");
}

// ---- windows ----

const wins = new Map(); // id → BrowserWindow
let main = null; // the first: its bounds are what window.json keeps
let entry = ""; // where a new window opens (http://gerdr/…, a login link while the backend's cookie isn't there yet)

const statePath = () => path.join(app.getPath("userData"), "window.json");
function loadState() {
  try {
    const st = JSON.parse(fs.readFileSync(statePath(), "utf8"));
    // the screen it was on is gone (an external display unplugged): not off in the void
    const visible = screen.getAllDisplays().some((d) => {
      const a = d.workArea;
      return st.x >= a.x - 50 && st.y >= a.y - 50 && st.x < a.x + a.width && st.y < a.y + a.height;
    });
    return visible ? st : { width: st.width, height: st.height };
  } catch {
    return { width: 1280, height: 820 };
  }
}
function saveState() {
  if (!main || main.isDestroyed()) return;
  try {
    fs.writeFileSync(statePath(), JSON.stringify({ ...main.getNormalBounds(), maximized: main.isMaximized() }));
  } catch {}
}

const winId = (w) => String(w.id);
const allWins = () => [...wins.values()].filter((w) => !w.isDestroyed());
function focusedWin() {
  const f = BrowserWindow.getFocusedWindow();
  return f && wins.has(winId(f)) ? f : main && !main.isDestroyed() ? main : (allWins()[0] ?? null);
}

// pageURL: an address the core gives (the backend's own, http://127.0.0.1:7870/…) at the pages' host.
function pageURL(u) {
  try {
    const x = new URL(u);
    if (x.hostname === "127.0.0.1" || x.hostname === "localhost") {
      // hostname and port both: setting host alone keeps the old port (http://gerdr:7870, another origin)
      x.hostname = "gerdr";
      x.port = "";
    }
    return x.toString();
  } catch {
    return u;
  }
}
const isPage = (u) => {
  try {
    return new URL(u).host === "gerdr";
  } catch {
    return false;
  }
};

function openWindow(url) {
  const first = !main || main.isDestroyed();
  let st;
  if (first) st = loadState();
  else {
    const b = (focusedWin() ?? main).getNormalBounds();
    st = { width: b.width, height: b.height, x: b.x + 28, y: b.y + 28 };
  }
  const w = new BrowserWindow({
    ...st,
    minWidth: 640,
    minHeight: 420,
    title: appName,
    show: false,
    backgroundColor: nativeTheme.shouldUseDarkColors ? "#0f0f0f" : "#ffffff",
    autoHideMenuBar: true,
    // Our own title bar (docs/dev/TITLEBAR.md): macOS draws only the traffic lights, at the window's top-left over the
    // UI's rail, centred on its 40px headers; the UI makes room (window.gerdrNative.titleBar, ui/src/lib/native.ts)
    ...(isMac ? { titleBarStyle: "hidden", trafficLightPosition: { x: 16, y: 14 } } : {}),
    webPreferences: { preload: path.join(__dirname, "preload.js"), contextIsolation: true, nodeIntegration: false, sandbox: true, spellcheck: false },
  });
  const id = winId(w);
  wins.set(id, w);
  if (first) main = w;
  if (st.maximized) w.maximize();
  w.once("ready-to-show", () => w.show());
  for (const ev of ["resize", "move", "close"]) w.on(ev, () => w === main && saveState());
  // macOS: closing the last window hides it (the Dock icon brings it back), the backend and the sessions go on; ⌘Q quits
  w.on("close", (e) => {
    if (isMac && !quitting && allWins().length === 1) {
      e.preventDefault();
      w.hide();
    }
  });
  w.on("closed", () => {
    wins.delete(id);
    if (main === w) main = allWins()[0] ?? null;
    for (const v of views.values()) if (v.host === w) parkView(v);
    send({ ev: "window", win: id, open: false });
  });
  const wc = w.webContents;
  wc.setWindowOpenHandler(({ url }) => {
    openExternal(url);
    return { action: "deny" };
  });
  wc.on("will-navigate", (e, url) => {
    if (isPage(url) || url.startsWith("data:")) return;
    e.preventDefault();
    openExternal(url);
  });
  // the page's process crashed or was reclaimed: load it again (the sessions are in the backend)
  wc.on("render-process-gone", (_e, d) => {
    if (d.reason !== "clean-exit") setTimeout(() => !w.isDestroyed() && wc.loadURL(entry || wc.getURL()), 500);
  });
  // the page's zoom changed (⌘+ / ⌘-): its views follow with the next box it reports
  w.loadURL(url);
  send({ ev: "window", win: id, open: true });
  return w;
}

function openExternal(url) {
  if (/^(https?|mailto):/i.test(url)) shell.openExternal(url);
}

function showWindow(w) {
  if (!w) return;
  if (w.isMinimized()) w.restore();
  w.show();
  w.focus();
}

// A still of a project's page for the pane to show while one of the UI's menus / dialogs is over it (a view is drawn
// above the page, nothing of the UI can be). Taken here, in-process, as Claude Desktop does: through the backend and
// the protocol it took long enough that a lightbox opened under the live page and was covered until the still came.
ipcMain.handle("gerdr:shot", async (e, tab) => {
  const v = [...views.values()].find((x) => x.tab === tab);
  if (!v || v.view.webContents.isDestroyed()) return null;
  const img = await v.view.webContents.capturePage();
  // PNG: a JPEG still read softer than the live page next to it
  return img.isEmpty() ? null : "data:image/png;base64," + img.toPNG().toString("base64");
});

ipcMain.on("gerdr", (e, m) => {
  const w = BrowserWindow.fromWebContents(e.sender);
  if (!w || !wins.has(winId(w))) return;
  send({ ev: "message", win: winId(w), data: m });
});

// ---- views: a project's page inside a window (the Browser pane) ----

// One per page the backend opens in the App (chromeext/app.go, through the core's views.go). In a window's content
// view over the pane's box while the pane shows it; parked otherwise, in a hidden window of its own that keeps
// painting, so a phone watching or an agent driving it still sees it live.
const views = new Map(); // id → { view, host, park, profile }
let nextView = 1;

function viewOf(id) {
  const v = views.get(String(id));
  if (!v) throw new Error("no such view");
  return v;
}

function parkWindow(width, height) {
  return new BrowserWindow({ show: false, width, height, paintWhenInitiallyHidden: true, webPreferences: { backgroundThrottling: false } });
}

function parkView(v) {
  if (quitting || !views.has(v.id)) return;
  if (v.radius) {
    v.radius = 0;
    v.view.setBorderRadius(0);
  }
  if (v.host && !v.host.isDestroyed()) v.host.contentView.removeChildView(v.view);
  v.host = null;
  if (!v.park || v.park.isDestroyed()) {
    const b = v.view.getBounds();
    v.park = parkWindow(Math.max(b.width, 400), Math.max(b.height, 300));
  }
  v.park.contentView.addChildView(v.view);
  const b = v.park.getContentBounds();
  v.view.setBounds({ x: 0, y: 0, width: b.width, height: b.height });
}

function openView(profile, url, tab) {
  const id = String(nextView++);
  const view = new WebContentsView({
    webPreferences: {
      partition: "persist:" + (profile || "gerdr-default"),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
      backgroundThrottling: false,
    },
  });
  const v = { id, view, host: null, park: null, profile, tab: tab || 0 };
  views.set(id, v);
  const wc = view.webContents;
  // a page opening another (target=_blank, window.open): another view, reported as a popup of this one
  wc.setWindowOpenHandler(({ url }) => {
    send({ ev: "view", view: id, kind: "popup", url });
    return { action: "deny" };
  });
  const dbg = wc.debugger;
  dbg.attach("1.3");
  dbg.on("message", (_e, method, params, sessionId) => {
    if (!sessionId) send({ ev: "view", view: id, kind: "cdp", method, params });
  });
  dbg.on("detach", () => closeView(v, true));
  // a click in the page: its pane becomes the focused one (the pane can't see clicks on a view drawn over it)
  wc.on("focus", () => send({ ev: "view", view: id, kind: "focus" }));
  wc.on("destroyed", () => closeView(v, true));
  parkView(v);
  wc.loadURL(url || "about:blank").catch(() => {});
  return id;
}

function closeView(v, gone) {
  if (!views.has(v.id)) return;
  views.delete(v.id);
  if (v.host && !v.host.isDestroyed()) v.host.contentView.removeChildView(v.view);
  if (v.park && !v.park.isDestroyed()) v.park.destroy();
  const wc = v.view.webContents; // gone already when the App quits
  if (wc && !wc.isDestroyed()) wc.close();
  send({ ev: "view", view: v.id, kind: "closed" });
  if (browserOnly) armIdle();
}

// placeView: rect is the pane's box in CSS px of the window's page; the page's zoom turns it into the window's points.
// Rounded inwards (rounding to nearest put the page a point over the pane's right border), and its corners rounded as
// the pane's inside are (radius, CSS px): a view is drawn over the page, square corners showed over the pane's.
function placeView(v, w, rect, radius) {
  if (!w || w.isDestroyed()) throw new Error("no such window");
  const z = w.webContents.getZoomFactor() || 1;
  const [rx, ry, rw, rh] = rect;
  const x = Math.ceil(rx * z),
    y = Math.ceil(ry * z);
  const width = Math.floor((rx + rw) * z) - x,
    height = Math.floor((ry + rh) * z) - y;
  if (v.host !== w) {
    if (v.host && !v.host.isDestroyed()) v.host.contentView.removeChildView(v.view);
    if (v.park && !v.park.isDestroyed()) v.park.contentView.removeChildView(v.view);
    w.contentView.addChildView(v.view);
    v.host = w;
  }
  v.view.setBounds({ x, y, width: Math.max(width, 1), height: Math.max(height, 1) });
  const r = Math.max(0, Math.round((radius || 0) * z));
  if (v.radius !== r) {
    v.radius = r;
    v.view.setBorderRadius(r);
  }
}

// ---- menus ----

// The core sends the menus as data ({label, accelerator, role, id, submenu, separator}); a click goes back with the
// item's id and the focused window.
const roles = {
  hide: "hide",
  hideOthers: "hideOthers",
  unhide: "unhide",
  quit: "quit",
  close: "close",
  minimize: "minimize",
  zoom: "zoom",
  front: "front",
  toggleFullScreen: "togglefullscreen",
  zoomIn: "zoomIn",
  zoomOut: "zoomOut",
  resetZoom: "resetZoom",
  devTools: "toggleDevTools",
  reload: "reload",
  editMenu: "editMenu",
  windowMenu: "windowMenu",
  undo: "undo",
  redo: "redo",
  cut: "cut",
  copy: "copy",
  paste: "paste",
  selectAll: "selectAll",
};
function menuItems(items) {
  return (items || []).map((it) => {
    if (it.separator) return { type: "separator" };
    const o = { label: it.label || undefined };
    if (it.accelerator) o.accelerator = it.accelerator.replace(/\bCmd\b/g, "CmdOrCtrl");
    if (it.role && roles[it.role]) o.role = roles[it.role];
    if (it.submenu) o.submenu = menuItems(it.submenu);
    if (it.id) {
      // a key the page handles itself: shown in the menu, not taken from the page
      o.registerAccelerator = false;
      o.click = () => {
        const w = focusedWin();
        send({ ev: "menu", item: it.id, win: w ? winId(w) : "" });
      };
    }
    return o;
  });
}

// ---- what the core asks ----

async function handle(m) {
  switch (m.op) {
    case "start": // the backend is up: the first window, at its login link
      entry = pageURL(m.url);
      if (m.port && Number(m.port) !== port) {
        // the backend is on another port than the one mapped: start again with it
        log("backend on port", m.port, "mapped", port, ": relaunching");
        app.relaunch({ execPath: process.execPath, args: process.argv.slice(1).concat([]), env: { ...process.env, GERDR_PORT: String(m.port) } });
        quitting = true;
        send({ ev: "quit" });
        return;
      }
      if (!browserOnly) openWindow(entry);
      return;
    case "newWindow":
      return winId(openWindow(pageURL(m.url)));
    case "windows":
      return allWins().map(winId);
    case "eval": {
      const w = wins.get(m.win);
      if (!w) throw new Error("no such window");
      return await w.webContents.executeJavaScript(m.js, true);
    }
    case "load": {
      const w = wins.get(m.win);
      if (w) await w.loadURL(pageURL(m.url)).catch(() => {});
      return;
    }
    case "focus":
      return showWindow(wins.get(m.win));
    case "badge":
      if (isMac) app.dock?.setBadge(m.label || "");
      return;
    case "menu":
      // Windows: no menu bar. Its accelerators would take Ctrl+C / Ctrl+W before the page, which has keys of its own
      // (Ctrl+C twice stops an agent, the prefix key…); copy and paste work without it there.
      if (isMac) Menu.setApplicationMenu(Menu.buildFromTemplate(menuItems(m.items)));
      return;
    case "openExternal":
      return openExternal(m.url);
    case "openPath":
      if (m.reveal) shell.showItemInFolder(m.path);
      else await shell.openPath(m.path);
      return;
    case "dockMenu":
      if (isMac) app.dock?.setMenu(Menu.buildFromTemplate(menuItems(m.items)));
      return;
    case "webPid": {
      const w = wins.get(m.win);
      return w ? w.webContents.getOSProcessId() : 0;
    }
    case "quit":
      app.quit();
      return;
    case "engine":
      // args: a development Electron needs this app's path before its own arguments
      return { exe: process.execPath, args: dev ? [app.getAppPath()] : [], dataDir: app.getPath("userData"), kind: "electron" };
    case "openView":
      armIdle();
      return openView(m.profile, m.url, m.tab);
    case "viewCdp": {
      const v = viewOf(m.view);
      return await v.view.webContents.debugger.sendCommand(m.method, m.params || {});
    }
    case "viewPlace":
      return placeView(viewOf(m.view), wins.get(m.win), m.rect, m.radius);
    case "viewHide":
      return parkView(viewOf(m.view));
    case "viewClose":
      return closeView(viewOf(m.view));
    case "notify": {
      const title = String(m.title || appName),
        body = String(m.body || "");
      if (!Notification.isSupported()) return scriptNotify(title, body);
      const n = new Notification({ title, body, silent: false });
      live.add(n);
      if (live.size > 50) live.delete(live.values().next().value); // ones left in Notification Center never close
      // macOS delivers these only to an App signed properly; an ad-hoc / self-signed build gets "not allowed"
      n.on("failed", () => {
        live.delete(n);
        scriptNotify(title, body);
      });
      n.on("click", () => {
        live.delete(n);
        send({ ev: "noteClick", win: m.win, sessionId: m.sessionId, space: m.space });
      });
      n.on("close", () => live.delete(n));
      n.show();
      return;
    }
    case "dialog": {
      const r = await dialog.showMessageBox(focusedWin() ?? undefined, { type: m.kind || "info", message: m.title || "", detail: m.detail || "", buttons: m.buttons || ["OK"], defaultId: m.defaultId ?? 0, cancelId: m.cancelId ?? 0 });
      return r.response;
    }
  }
  throw new Error("unknown op " + m.op);
}
const live = new Set(); // notifications: a collected one loses its click handler

// AppleScript's notification, when the App's own isn't allowed: attributed to Script Editor and its click can't bring
// up the session, but the person learns an agent is waiting. Text as argv, never spliced into the script.
function scriptNotify(title, body) {
  if (!isMac) return;
  const script = 'on run argv\ndisplay notification (item 2 of argv) with title (item 1 of argv) sound name "Glass"\nend run';
  require("node:child_process").execFile("osascript", ["-e", script, title, body], () => {});
}

// ---- browser-only: quit once nothing has been open for a while ----

let idleTimer = null;
function armIdle() {
  if (!browserOnly) return;
  clearTimeout(idleTimer);
  if (views.size) return;
  idleTimer = setTimeout(() => {
    if (!views.size) app.quit();
  }, 10 * 60 * 1000);
}

// ---- life ----

if (browserOnly && isMac) app.dock?.hide();

app.on("second-instance", () => {
  if (browserOnly) return;
  const w = focusedWin();
  if (w) showWindow(w);
  else if (entry) openWindow(entry);
});

app.on("activate", () => {
  if (browserOnly) return;
  const w = focusedWin();
  if (w) showWindow(w);
  else if (entry) openWindow(entry);
});

app.on("window-all-closed", () => {
  if (!isMac && !browserOnly) app.quit();
});

// Quitting: the core stops the backend (SIGTERM, 3 s for its session logs) and exits; then we go.
app.on("before-quit", (e) => {
  if (!core) return;
  if (!quitting) {
    quitting = true;
    send({ ev: "quit" });
  }
  e.preventDefault();
  setTimeout(() => app.exit(0), 5000);
});

// Windows shows an App's notifications only when a Start menu shortcut carries its AppUserModelID; NSIS can't set
// that property, so the App sets it on the shortcut the installer made (installer.nsi), every start.
function windowsShortcut() {
  if (process.platform !== "win32" || dev || browserOnly || !pkg.id) return;
  const lnk = path.join(app.getPath("appData"), "Microsoft", "Windows", "Start Menu", "Programs", appName + ".lnk");
  const ok = shell.writeShortcutLink(lnk, fs.existsSync(lnk) ? "update" : "create", { target: process.execPath, appUserModelId: pkg.id });
  if (!ok) log("start menu shortcut: not written");
}

app.whenReady().then(() => {
  windowsShortcut();
  // The pages say they're Chrome, as they are: "Electron/…" and the App's name in the user agent made sites treat the
  // projects' pages as an embedded browser (Google refuses to sign in to one)
  app.userAgentFallback = app.userAgentFallback.replace(/ (Electron|\S*Gerdr\S*)\/\S+/g, "");
  // The App's own menu bar until the core sends Gerdr's (Edit: ⌘C / ⌘V need it)
  if (!browserOnly && isMac) Menu.setApplicationMenu(Menu.buildFromTemplate([{ role: "appMenu" }, { role: "editMenu" }, { role: "windowMenu" }]));
  if (!isMac) Menu.setApplicationMenu(null);
  startCore();
  send({ ev: "ready", browserOnly, port });
});
