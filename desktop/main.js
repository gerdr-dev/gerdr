// gerdr 的桌面外壳（Electron）：一个窗口，界面全在网页里（就是浏览器里打开的那一份）。
//
// 为什么是 Electron、不是系统 WebView（原来的 mac 外壳是 WKWebView）/ mygo / nsh：docs/dev/WHY-ELECTRON.md。
// 一句话：界面在 Chrome 里怎么样，到 App 里就怎么样，mac 和 Windows 一个内核。
//
// 壳只做网页做不了的事：起 / 收后端进程、系统通知、Dock 角标、菜单（mac 上没有「编辑」菜单 ⌘C / ⌘V 不工作）、
// 外部链接交给默认浏览器。
const { app, BrowserWindow, Menu, Notification, ipcMain, shell, nativeTheme, screen } = require("electron");
const { spawn } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const isMac = process.platform === "darwin";

if (!app.requestSingleInstanceLock()) {
  app.quit();
  process.exit(0);
}

let win = null;
let origin = null; // 后端的地址（就绪之后才有）
let backend = null;
let quitting = false;

// ---- 后端 ----

function backendPath() {
  const exe = process.platform === "win32" ? "gerdr.exe" : "gerdr";
  if (process.env.GERDR_BIN) return process.env.GERDR_BIN;
  if (app.isPackaged) return path.join(process.resourcesPath, exe);
  return path.join(__dirname, "..", "bin", exe); // 开发时：make build 出的那个
}

function startBackend() {
  const bin = backendPath();
  if (!fs.existsSync(bin)) return showMessage(`找不到 gerdr 后端：${bin}`);
  // --app：就绪后打印一行 GERDR_READY <url>；已经有一个 gerdr 在跑（比如终端里起的）就直接连它，两边看到的是同一份会话。
  // --parent-pid：App 退出（哪怕是被强杀）时后端跟着退。
  const p = spawn(bin, ["--app", "--parent-pid", String(process.pid)], {
    env: { ...process.env, GERDR_FROM_APP: "1" },
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
        win?.loadURL(origin);
      }
    }
  });
  p.stderr.on("data", (d) => {
    errTail = (errTail + d.toString()).slice(-16000);
  });
  p.on("error", (e) => showMessage(`起不来后端：${e.message}`));
  p.on("exit", (code, signal) => {
    backend = null;
    if (quitting) return;
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
  if (!win || win.isDestroyed()) return;
  const b = win.getNormalBounds();
  try {
    fs.writeFileSync(statePath(), JSON.stringify({ ...b, maximized: win.isMaximized() }));
  } catch {}
}

function createWindow() {
  const st = loadState();
  win = new BrowserWindow({
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
  if (st.maximized) win.maximize();
  win.once("ready-to-show", () => win.show());
  for (const ev of ["resize", "move", "close"]) win.on(ev, saveState);
  // mac：关窗口 = 藏起来（点 Dock 图标再出来），后端和会话照跑；⌘Q 才是真退出
  win.on("close", (e) => {
    if (isMac && !quitting) {
      e.preventDefault();
      win.hide();
    }
  });

  const wc = win.webContents;
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
    if (d.reason !== "clean-exit" && origin) setTimeout(() => wc.loadURL(origin), 500);
  });

  if (origin) win.loadURL(origin);
  else showMessage("启动中…");
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

function showMessage(text) {
  if (!win) return;
  const esc = text.replace(/&/g, "&amp;").replace(/</g, "&lt;");
  const dark = nativeTheme.shouldUseDarkColors;
  const html = `<html><body style="font:13px -apple-system,'Segoe UI',sans-serif;color:#888;display:flex;height:100vh;margin:0;align-items:center;justify-content:center;background:${dark ? "#0f0f0f" : "#fff"}"><pre style="white-space:pre-wrap;max-width:640px;font:inherit">${esc}</pre></body></html>`;
  win.loadURL("data:text/html;charset=utf-8," + encodeURIComponent(html));
}

function focusWindow() {
  if (!win) return;
  if (win.isMinimized()) win.restore();
  win.show();
  win.focus();
}

function runInPage(js) {
  if (win && origin) win.webContents.executeJavaScript(js).catch(() => {});
}

// ---- 网页 → 壳 ----

ipcMain.on("gerdr", (e, m) => {
  if (!m || typeof m.type !== "string" || e.sender !== win?.webContents) return;
  switch (m.type) {
    case "notify": {
      if (!Notification.isSupported()) return;
      const n = new Notification({ title: String(m.title ?? "Gerdr"), body: String(m.body ?? ""), silent: false });
      n.on("click", () => {
        focusWindow();
        if (m.sessionId) runInPage(`window.gerdr && window.gerdr.focusSession(${JSON.stringify(String(m.sessionId))})`);
      });
      n.show();
      break;
    }
    case "badge": {
      const n = Number(m.count) || 0;
      if (isMac) app.setBadgeCount(n);
      if (n > 0 && win && !win.isFocused()) {
        if (isMac) app.dock?.bounce("informational");
        else win.flashFrame(true);
      }
      break;
    }
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
    case "webPid":
      // 设置里的「资源」要量界面所在那个渲染进程的内存（它不是后端起的，后端看不见）
      runInPage(`window.__GERDR_WEB_PID__ = ${win.webContents.getOSProcessId()}`);
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
  if (!origin || !win) return;
  const ok = await win.webContents.executeJavaScript("!!(window.__gerdrRescue && (window.__gerdrRescue.open(), 1))").catch(() => false);
  if (!ok) win.loadURL(rescueURL());
}

function cmd(label, id, accelerator) {
  return { label, accelerator, click: () => runInPage(`window.gerdr && window.gerdr.runCommand(${JSON.stringify(id)})`) };
}

function buildMenu() {
  if (!isMac) return Menu.setApplicationMenu(null);
  const tabs = [];
  for (let i = 1; i <= 9; i++) tabs.push(cmd(`tab ${i}`, `tab${i}`, `Cmd+${i}`));
  Menu.setApplicationMenu(
    Menu.buildFromTemplate([
      {
        label: "Gerdr",
        submenu: [
          { role: "about", label: "关于 Gerdr" },
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
        submenu: [cmd("新建 tab", "newTab", "Cmd+T"), cmd("新工作空间", "newWorkspace", "Cmd+Shift+N"), cmd("会话列表", "sessions", "Cmd+O"), { type: "separator" }, cmd("关闭 pane", "closePane", "Cmd+W")],
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
          cmd("左右分屏", "splitRight", "Cmd+D"),
          cmd("上下分屏", "splitDown", "Cmd+Shift+D"),
          cmd("放大 / 还原 pane", "zoom", "Cmd+Shift+Enter"),
          { type: "separator" },
          { role: "zoomIn", label: "放大", accelerator: "Cmd+=" },
          { role: "zoomOut", label: "缩小" },
          { role: "resetZoom", label: "实际大小" },
          { type: "separator" },
          { label: "重新载入", accelerator: "Cmd+R", click: () => (origin ? win?.loadURL(origin) : win?.reload()) },
          { label: "开发者工具", accelerator: "Cmd+Alt+I", click: () => win?.webContents.toggleDevTools() },
          { label: "在浏览器里打开", click: () => origin && shell.openExternal(origin) },
        ],
      },
      {
        label: "前往",
        submenu: [cmd("下一个 tab", "nextTab", "Cmd+Shift+]"), cmd("上一个 tab", "prevTab", "Cmd+Shift+["), ...tabs, { type: "separator" }, cmd("跳到在等你的会话", "focusAttention", "Cmd+J")],
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

app.on("second-instance", focusWindow);
app.on("activate", () => (win ? focusWindow() : createWindow()));
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
});
