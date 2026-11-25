// The desktop App's main-process logic: windows, the views of projects' pages (the Browser pane), menus, the Dock,
// notifications, quitting. The hot half of the App (docs/dev/SHELL.md): it ships inside the gerdr binary
// (internal/desktop/hot.go embeds it) and desktop/main.js, the frozen loader, runs the one the core sends, so a change
// here is a backend update (~10MB) and not a whole App. It runs on whatever Electron the installed App has: check for
// an API before relying on a newer one.
//
// The loader keeps what has to happen before Electron is ready or can never change: the App's name and directories,
// the engine switches, the single-instance lock, starting the core (`gerdr shell-core`) and carrying its lines. The
// core's half of the protocol, and the list of messages: internal/desktop/el/el.go.
//
// start(ctx) is called once Electron is ready. ctx (from the loader, keep its shape: an old loader runs a new script):
//   electron, appName, shellDir, pkg, port, browserOnly, dev, preload (path of preload.js), state {quitting},
//   send(m) to the core, onCore(fn) for the core's messages (fn(m) → result or promise; the loader replies), coreAlive(),
//   log(...)
"use strict";
const fs = require("node:fs");
const path = require("node:path");

let ctx;
let app, BrowserWindow, WebContentsView, Menu, Notification, shell, nativeTheme, screen, ipcMain, dialog;
let isMac, browserOnly, dev, appName, port, pkg;
const send = (m) => ctx.send(m);
const log = (...a) => ctx.log(...a);
const quitting = () => ctx.state.quitting;

exports.start = (c) => {
  ctx = c;
  ({ app, BrowserWindow, WebContentsView, Menu, Notification, shell, nativeTheme, screen, ipcMain, dialog } = c.electron);
  isMac = process.platform === "darwin";
  ({ browserOnly, dev, appName, port, pkg } = c);
  c.onCore(handle);
  setUp();
  boot();
};

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
    // UI's rail, centred on its 40px headers; the UI makes room (window.gerdrNative.titleBar, ui/src/lib/native.ts).
    // The lights are 14pt; a header's last pixel is its border, so the space they centre in is 39pt: y 13 puts their
    // centre at 20, the nearest whole point to 19.5 (y 14 measured 1.25pt low on a screenshot, which showed)
    ...(isMac ? { titleBarStyle: "hidden", trafficLightPosition: { x: 16, y: 13 } } : {}),
    webPreferences: { preload: ctx.preload, contextIsolation: true, nodeIntegration: false, sandbox: true, spellcheck: false },
  });
  const id = winId(w);
  wins.set(id, w);
  if (first) main = w;
  if (st.maximized) w.maximize();
  w.once("ready-to-show", () => w.show());
  for (const ev of ["resize", "move", "close"]) w.on(ev, () => w === main && saveState());
  // macOS: closing the last window hides it (the Dock icon brings it back), the backend and the sessions go on; ⌘Q quits
  w.on("close", (e) => {
    if (isMac && !quitting() && allWins().length === 1) {
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

// fitWindow: a page in its own window (the UI's ?browser=) just got a size picked: the window grows or shrinks by dw
// (CSS px of its page) so the page area is that wide (asked for: a phone page centred in a wide window). Within the
// screen it is on; out of full screen / zoom first, which own the size. The usual minimum width (640) would keep a
// phone's page in a wider window, so it goes down to what is asked.
function fitWindow(w, dw) {
  if (!dw || w.isDestroyed() || w.isFullScreen()) return;
  const z = w.webContents.getZoomFactor() || 1;
  const [cw] = w.getContentSize();
  const area = screen.getDisplayMatching(w.getBounds()).workArea;
  const width = Math.max(200, Math.min(Math.round(cw + dw * z), area.width));
  // zoomed: the target is from the zoomed size, set once the window is back from it (macOS animates that)
  if (w.isMaximized()) {
    w.once("unmaximize", () => setTimeout(() => !w.isDestroyed() && sizeWidth(w, width, area), 50));
    w.unmaximize();
    return;
  }
  sizeWidth(w, width, area);
}

function sizeWidth(w, width, area) {
  const [, ch] = w.getContentSize();
  const [, minH] = w.getMinimumSize();
  w.setMinimumSize(Math.min(640, width), minH);
  w.setContentSize(width, ch, true);
  // grown past the screen's right edge: back inside it
  const b = w.getBounds();
  if (b.x + b.width > area.x + area.width) w.setPosition(Math.max(area.x, area.x + area.width - b.width), b.y, true);
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
  if (quitting() || !views.has(v.id)) return;
  if (v.parked && v.park && !v.park.isDestroyed()) return; // there already (the bridge's fast path, then the core)
  if (v.radius) {
    v.radius = 0;
    v.view.setBorderRadius(0);
  }
  const host = v.host && !v.host.isDestroyed() ? v.host : null;
  if (host) host.contentView.removeChildView(v.view);
  v.host = null;
  // The keys go back to the window's own page: with the keyboard in the pane's page (it was clicked last), it stepped
  // aside for a dialog opened by a shortcut and took the keyboard with it into the park window, so Esc didn't close
  // the dialog until something of the UI was clicked (user report: the first Settings after using the page).
  if (host && host.isFocused() && !host.webContents.isFocused()) host.webContents.focus();
  // The page keeps the size it had in the pane: the park window follows it. Sized to the park window instead (made
  // at 400x300 when the view opens, before it has a size), every step aside for one of our menus resized the page
  // twice, and it came back drawn 400 wide in a corner of a black box until its layout caught up (a recording: 1-2
  // frames at each menu close; a page with breakpoints, YouTube, showed its narrow layout).
  // An emulated size (a floating pane's, or one picked: chromeext phone.go) wins: the page paints only what fits in
  // the view, so parked at the docked column's size a page laid out at 1280 sent pictures of its left 531 px (user
  // report: a floating pane showed a corner of the page, cut off).
  const had = v.emu || v.view.getBounds();
  const width = had.width > 1 ? had.width : 400,
    height = had.height > 1 ? had.height : 300;
  if (!v.park || v.park.isDestroyed()) v.park = parkWindow(width, height);
  else v.park.setContentSize(width, height);
  v.park.contentView.addChildView(v.view);
  v.view.setBounds({ x: 0, y: 0, width, height });
  v.parked = true;
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
  // The person's mouse / wheel / keys in the page gone quiet for 500 ms: the pane takes a fresh still then
  // (BrowserScreen's warm still), so one of the UI's dialogs opened by a shortcut, the pointer still in the page,
  // comes up over the page as it is now and not as it was when the pointer last left it. The window's own document
  // sees nothing of what happens in here, so it can't tell by itself.
  wc.on("input-event", () => {
    clearTimeout(v.quiet);
    v.quiet = setTimeout(() => {
      const h = v.host;
      if (!h || h.isDestroyed() || wc.isDestroyed()) return;
      h.webContents.executeJavaScript(`window.dispatchEvent(new CustomEvent("gerdr:browseridle", {detail: ${Number(v.tab) || 0}}))`).catch(() => {});
    }, 500);
  });
  wc.on("destroyed", () => closeView(v, true));
  parkView(v);
  wc.loadURL(url || "about:blank").catch(() => {});
  return id;
}

function closeView(v, gone) {
  if (!views.has(v.id)) return;
  views.delete(v.id);
  clearTimeout(v.quiet);
  clearTimeout(v.reshape);
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
    v.parked = false;
  }
  const b = { x, y, width: Math.max(width, 1), height: Math.max(height, 1) };
  const had = v.view.getBounds();
  v.view.setBounds(b);
  if (had.width !== b.width || had.height !== b.height) reshape(v, w, b);
  const r = Math.max(0, Math.round((radius || 0) * z));
  if (v.radius !== r) {
    v.radius = r;
    v.view.setBorderRadius(r);
  }
}

// emulated: the page's emulated size changed (a DevTools command from the backend, chromeext phone.go). Parked, the
// view and its park window take that size, as drawn (scale), so the page paints all of it; placed, the pane sizes it.
function emulated(v, method, p) {
  if (method === "Emulation.clearDeviceMetricsOverride") v.emu = null;
  if (method !== "Emulation.setDeviceMetricsOverride") return;
  const k = p.scale > 0 && p.scale < 1 ? p.scale : 1;
  v.emu = p.width > 0 && p.height > 0 ? { width: Math.ceil(p.width * k), height: Math.ceil(p.height * k) } : null;
  if (!v.emu || !v.parked || !v.park || v.park.isDestroyed()) return;
  v.park.setContentSize(v.emu.width, v.emu.height);
  v.view.setBounds({ x: 0, y: 0, ...v.emu });
}

// reshape: once a view's size has stopped changing, make it draw that size again. A window zoomed by a double click on
// its title bar (macOS animates it) sometimes left the page showing a frame of another size in a corner of its box, the
// still under it showing round it, until something else resized it (user report, a page in its own window): the view had
// the right bounds and capturePage had the right picture, the screen didn't. A one-point resize and back gives the page
// a new surface, which is what a manual resize did; invalidate alone is the cheap half of that.
function reshape(v, w, b) {
  clearTimeout(v.reshape);
  v.reshape = setTimeout(() => {
    if (v.host !== w || w.isDestroyed() || v.view.webContents.isDestroyed()) return;
    const now = v.view.getBounds();
    if (now.x !== b.x || now.y !== b.y || now.width !== b.width || now.height !== b.height) return; // placed again since
    v.view.setBounds({ ...b, width: Math.max(b.width - 1, 1) });
    setTimeout(() => {
      if (v.host !== w || w.isDestroyed() || v.view.webContents.isDestroyed()) return;
      const cur = v.view.getBounds();
      if (cur.width === Math.max(b.width - 1, 1) && cur.x === b.x && cur.y === b.y) v.view.setBounds(b);
      v.view.webContents.invalidate();
    }, 16);
  }, 300);
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
        ctx.state.quitting = true;
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
    case "close": {
      const w = wins.get(m.win);
      if (w && !w.isDestroyed()) w.close();
      return;
    }
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
    case "focused":
      return !!BrowserWindow.getFocusedWindow();
    case "relaunch":
      // A backend update whose core or this script changed (internal/desktop app.go applyUpdate): they take effect
      // when the App starts again. The windows reopen from window.json; with the background service the agents run on.
      app.relaunch();
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
      // sized first: sized after, the page laid out at the new size in the old view for a moment, and the pictures
      // of that (its left part, cut off) showed in a floating box for half a second (user report)
      emulated(v, m.method, m.params || {});
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

// Windows shows an App's notifications only when a Start menu shortcut carries its AppUserModelID; NSIS can't set
// that property, so the App sets it on the shortcut the installer made (installer.nsi), every start.
function windowsShortcut() {
  if (process.platform !== "win32" || dev || browserOnly || !pkg.id) return;
  const lnk = path.join(app.getPath("appData"), "Microsoft", "Windows", "Start Menu", "Programs", appName + ".lnk");
  const ok = shell.writeShortcutLink(lnk, fs.existsSync(lnk) ? "update" : "create", { target: process.execPath, appUserModelId: pkg.id });
  if (!ok) log("start menu shortcut: not written");
}

function boot() {
  windowsShortcut();
  // The pages say they're Chrome, as they are: "Electron/…" and the App's name in the user agent made sites treat the
  // projects' pages as an embedded browser (Google refuses to sign in to one)
  app.userAgentFallback = app.userAgentFallback.replace(/ (Electron|\S*Gerdr\S*)\/\S+/g, "");
  // The App's own menu bar until the core sends Gerdr's (Edit: ⌘C / ⌘V need it)
  if (!browserOnly && isMac) Menu.setApplicationMenu(Menu.buildFromTemplate([{ role: "appMenu" }, { role: "editMenu" }, { role: "windowMenu" }]));
  if (!isMac) Menu.setApplicationMenu(null);
  send({ ev: "ready", browserOnly, port });
}

function setUp() {
  ipcMain.removeHandler("gerdr:pick-icon-image");
  ipcMain.handle("gerdr:pick-icon-image", async (event, title) => {
    const w = BrowserWindow.fromWebContents(event.sender);
    // Only the main Gerdr page can ask, and only the file chosen in this sheet is read.
    const frame = event.senderFrame;
    if (!w || !wins.has(winId(w)) || !frame || frame.parent || !isPage(frame.url)) return null;
    const { canceled, filePaths } = await dialog.showOpenDialog(w, {
      title: typeof title === "string" ? title : undefined,
      properties: ["openFile"],
      filters: [{ name: "Images", extensions: ["png", "jpg", "jpeg", "webp", "gif", "svg", "avif", "bmp", "ico"] }],
    });
    if (canceled || !filePaths[0]) return null;
    const file = await fs.promises.open(filePaths[0], "r");
    try {
      const stat = await file.stat();
      const maxBytes = 32 * 1024 * 1024;
      if (!stat.isFile()) return null;
      if (stat.size > maxBytes) return { tooLarge: true };
      // Bound the read even if the file grows after stat.
      const bytes = new Uint8Array(stat.size + 1);
      let offset = 0;
      while (offset < bytes.length) {
        const { bytesRead } = await file.read(bytes, offset, bytes.length - offset, null);
        if (!bytesRead) break;
        offset += bytesRead;
      }
      if (offset > stat.size) return { tooLarge: true };
      return { bytes: bytes.buffer.slice(0, offset), svg: path.extname(filePaths[0]).toLowerCase() === ".svg" };
    } finally {
      await file.close();
    }
  });

  ipcMain.removeHandler("gerdr:shot"); // a script the loader ran before this one (and then gave up on) may have set it
  ipcMain.handle("gerdr:shot", async (e, tab) => {
    const v = [...views.values()].find((x) => x.tab === tab);
    if (!v || v.view.webContents.isDestroyed()) return null;
    // The view's own area, said explicitly: with no rect Electron takes the widget's size, which under an emulated
    // size shrunk to fit (a size picked for a narrow pane: 1280 wide at scale 0.41) is the emulated one, and the page
    // came out in the top-left corner of a big dark still (user report: the still under a menu, a small page in a corner)
    const b = v.view.getBounds();
    const img = await v.view.webContents.capturePage({ x: 0, y: 0, width: b.width, height: b.height });
    // PNG: a JPEG still read softer than the live page next to it
    return img.isEmpty() ? null : "data:image/png;base64," + img.toPNG().toString("base64");
  });

  ipcMain.on("gerdr", (e, m) => {
    const w = BrowserWindow.fromWebContents(e.sender);
    if (!w || !wins.has(winId(w))) return;
    // The pane's page steps aside (one of the UI's menus / dialogs is over it) right here, not after the trip to the
    // core and back: each frame it stays is a frame it covers what opened. Only for the window it is in: another
    // window's pane saying "not shown here" has nothing to hide (the core's placeView has the same rule and the say).
    if (m && m.type === "browserView" && m.hide) {
      const v = [...views.values()].find((x) => x.tab === m.tab);
      if (v && v.host === w) parkView(v);
    }
    if (m && m.type === "fitWindow") return fitWindow(w, Number(m.dw) || 0);
    send({ ev: "message", win: winId(w), data: m });
  });

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

  // Quitting: the core stops the backend (SIGTERM, 3 s for its session logs) and exits; then we go (the loader exits
  // once the core is gone, ctx.state.quitting set)
  app.on("before-quit", (e) => {
    if (!ctx.coreAlive()) return;
    if (!quitting()) {
      ctx.state.quitting = true;
      send({ ev: "quit" });
    }
    e.preventDefault();
    setTimeout(() => app.exit(0), 5000);
  });
}
