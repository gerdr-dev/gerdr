// Gerdr's desktop App: the frozen loader (docs/dev/SHELL.md).
//
// This file, preload.js and package.json (its Electron version) are what the App's package is; changing them changes
// the shell id (tools/shell-id.sh) and makes the next release a whole-App download for everyone. So it does only what
// has to happen before Electron is ready or can never change:
//
//   - the App's name and directories, the engine switches, the single-instance lock (all before ready);
//   - starting the core, `gerdr shell-core` from the bundle's gerdr binary (internal/desktop), which hands itself over
//     to a newer downloaded backend's when there is one;
//   - running the main-process script the core sends first ({op: "script", path}: internal/desktop/shell/main.js,
//     embedded in that binary), and carrying the core's lines to it and its answers back.
//
// Everything else (windows, the Browser pane's views, menus, notifications, quitting) is in that script and updates
// with the backend. Keep this side's contract with it stable (the ctx below): an old loader runs new scripts for a
// long time. When the script or a handed-over core fails before it runs, the core is started again without the
// handoff, on the bundle's own binary and script.
//
// Two modes:
// - the App (default);
// - --gerdr-browser: no window of the UI, no Dock icon. The backend starts it while the App is closed, as the projects'
//   browser (internal/projbrowser): the same program on the same partitions, so a sign-in in the App is the same cookies
//   (docs/dev/BROWSER-PANE.md). The App starting stops it first (the data directory is one process's at a time).
"use strict";
const electron = require("electron");
const { app, dialog } = electron;
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
// http://gerdr isn't a secure context by itself (only https, localhost and 127.0.0.1 are), and the APIs that need one
// are then missing: navigator.clipboard (every copy button), web notifications, crypto.subtle. The Lightron shell had
// this switch; the move back to Electron (e413039) dropped it and copy buttons broke.
app.commandLine.appendSwitch("unsafely-treat-insecure-origin-as-secure", "http://gerdr");
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

// ---- the core and its script ----

const log = (...a) => console.error("[shell]", ...a);
const state = { quitting: false }; // shared with the script (ctx.state)
let core = null;
let handler = null; // the script's: (core message) → result
let tries = 0; // core starts: the second one skips the handoff (a newer backend's core or script that failed)
let scriptTimer = null;

function coreBinary() {
  if (process.env.GERDR_CORE) return process.env.GERDR_CORE;
  const exe = process.platform === "win32" ? "gerdr.exe" : "gerdr";
  if (!dev) return path.join(process.resourcesPath, exe);
  return path.join(__dirname, "..", "bin", exe); // make build
}

function send(m) {
  if (core?.stdin.writable) core.stdin.write(JSON.stringify(m) + "\n");
}

function startCore() {
  tries++;
  const args = ["shell-core", "-data", shellDir, "-name", appName];
  if (browserOnly) args.push("-browser", process.env.GERDR_BROWSER_BACKEND || "");
  if (process.env.GERDR_BIN) args.push("-gerdr", process.env.GERDR_BIN);
  if (tries > 1) args.push("-no-handoff");
  const proc = spawn(coreBinary(), args, {
    stdio: ["pipe", "pipe", "inherit"],
    env: {
      ...process.env,
      GERDR_SHELL_PACKAGED: dev ? "" : "1",
      GERDR_SHELL_PID: String(process.pid),
      GERDR_SHELL_RESOURCES: process.resourcesPath,
      GERDR_APP_VERSION: pkg.version || "",
      GERDR_SHELL_ID: pkg.shell || "",
    },
    windowsHide: true,
  });
  core = proc;
  // no script within a while: the core is stuck (a handed-over one that hangs); the bundle's then
  clearTimeout(scriptTimer);
  scriptTimer = setTimeout(() => !handler && retry(proc, "no script from the core"), 30000);
  readline.createInterface({ input: proc.stdout }).on("line", (line) => {
    let m;
    try {
      m = JSON.parse(line);
    } catch {
      return log("core:", line);
    }
    if (m.op === "script") return runScript(proc, m);
    if (!handler) return log("core message before its script:", line);
    Promise.resolve()
      .then(() => handler(m))
      .then(
        (result) => m.id && send({ ev: "reply", id: m.id, ok: true, result: result ?? null }),
        (err) => m.id && send({ ev: "reply", id: m.id, ok: false, error: String(err?.message || err) }),
      );
  });
  proc.on("exit", (code) => {
    if (core !== proc) return; // replaced (retry)
    core = null;
    if (state.quitting) return app.exit(0);
    if (!handler) return retry(proc, `the core exited (${code}) before its script ran`);
    log("the core exited", code);
    if (!browserOnly) dialog.showErrorBox(appName, `Gerdr's shell process exited (${code}). See ~/Library/Logs/${appName}.log`);
    app.exit(1);
  });
}

// retry: the core (or its script) failed before anything was up: once more on the bundle's own binary and script.
function retry(proc, why) {
  log(why);
  if (core === proc) core = null;
  try {
    proc.kill();
  } catch {}
  if (tries < 2) return startCore();
  if (!browserOnly) dialog.showErrorBox(appName, `Gerdr couldn't start: ${why}. See ~/Library/Logs/${appName}.log`);
  app.exit(1);
}

function runScript(proc, m) {
  clearTimeout(scriptTimer);
  if (handler) return log("a second script from the core: ignored");
  let mod;
  try {
    mod = require(m.path);
    mod.start({
      electron,
      appName,
      shellDir,
      pkg,
      port,
      browserOnly,
      dev,
      preload: path.join(__dirname, "preload.js"),
      state,
      send,
      onCore: (fn) => (handler = fn),
      coreAlive: () => !!core,
      log,
    });
  } catch (e) {
    handler = null;
    return retry(proc, `the shell script ${m.path} failed: ${e?.stack || e}`);
  }
  if (!handler) retry(proc, `the shell script ${m.path} didn't take the core's messages`);
}

app.whenReady().then(startCore);
