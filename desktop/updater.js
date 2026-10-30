// Self-update for the desktop App, in two sizes.
//
// Almost every release only changes the Go backend (which also carries the UI, internal/uisite): the Electron shell
// (main.js, preload.js, this file, the Electron version) rarely moves. So a release publishes, next to the ~140MB
// dmg, the brotli-compressed backend alone (~10MB) and update.json saying which shell it was built against (gerdrShell: a hash
// of the shell's files, desktop/shell-id.sh). Same shell → download only the backend into userData/backend/<version>/
// and run that instead of the one in the bundle (never write into the bundle: that breaks its code signature, and
// macOS App Management can block writes into /Applications). Different shell → download the whole App and swap the
// bundle (mac) / run the installer (Windows).
//
// Restarting the backend cuts off running turns, permission requests, background tasks and terminals, so a downloaded
// update is applied by itself only when the running gerdr says it is idle (/api/busy); otherwise the page shows it and
// the person picks the moment. The background service (docs/dev/SERVICE.md) follows the same rule in cmd/gerdr
// appAttach. Idle sessions don't mind: the next message resumes them.
const { app, net } = require("electron");
const { spawn, execFile } = require("node:child_process");
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const { Readable, Transform } = require("node:stream");
const { pipeline } = require("node:stream/promises");
const zlib = require("node:zlib");

const REPO = "gerdr-dev/gerdr";
// GERDR_UPDATE_FEED: try updates against a local feed (files are then resolved next to it)
const FEED = process.env.GERDR_UPDATE_FEED || `https://github.com/${REPO}/releases/latest/download/update.json`;
const PLAT = `${process.platform}-${process.arch}`; // darwin-arm64 / darwin-x64 / win32-x64: keys in update.json
const SHELL = require("./package.json").gerdrShell || ""; // build.sh puts it in the packaged package.json
const EXE = process.platform === "win32" ? "gerdr.exe" : "gerdr";

const root = () => path.join(app.getPath("userData"), "backend");
const stagingDir = () => path.join(app.getPath("userData"), "update");

/**
 * A release (x.y.z) or a local build (git describe: x.y.z-N-gabc, maybe -dirty) as [x, y, z, N]: N commits past
 * the x.y.z tag, 0 for the release. So a local build is behind the next release (0.2.3-70 < 0.2.4) and updates
 * like any other install, but never "updates" back to the release it was built on top of (0.2.4-3 > 0.2.4).
 * Anything else (0.1.0-dev) doesn't update.
 */
function parseVer(v) {
  const m = /^(\d+)\.(\d+)\.(\d+)(?:-(\d+)-g[0-9a-f]+)?(?:-dirty)?$/.exec(String(v || ""));
  return m ? [m[1], m[2], m[3], m[4] || 0].map(Number) : null;
}

function newer(a, b) {
  const x = parseVer(a);
  const y = parseVer(b);
  if (!x || !y) return false;
  for (let i = 0; i < 4; i++) if (x[i] !== y[i]) return x[i] > y[i];
  return false;
}

const enabled = () => app.isPackaged && !!parseVer(app.getVersion()) && !!SHELL && !process.env.GERDR_BIN && !process.env.GERDR_NO_UPDATE;

function readCurrent() {
  try {
    return JSON.parse(fs.readFileSync(path.join(root(), "current.json"), "utf8"));
  } catch {
    return null;
  }
}

/**
 * The backend to run: a downloaded one if it was built for this shell and is newer than the bundle's, else the
 * bundle's. A newer App installed over an older download wins by version, the download is then dropped (cleanup).
 */
function backend() {
  const bundled = { path: path.join(process.resourcesPath, EXE), version: app.getVersion(), bundled: true };
  if (!enabled()) return bundled;
  const c = readCurrent();
  if (!c || c.shell !== SHELL || !newer(c.version, bundled.version)) return bundled;
  const p = path.join(root(), c.version, EXE);
  return fs.existsSync(p) ? { path: p, version: c.version } : bundled;
}

/** Drop downloaded backends other than the one in use (and the one about to be). Windows can't delete a running exe: ignored. */
function cleanup() {
  const keep = new Set([backend().version, readCurrent()?.version]);
  let names = [];
  try {
    names = fs.readdirSync(root());
  } catch {
    return;
  }
  for (const n of names) if (parseVer(n) && !keep.has(n)) fs.rm(path.join(root(), n), { recursive: true, force: true }, () => {});
}

async function fetchJSON(url) {
  const r = await net.fetch(url, { cache: "no-store" });
  if (!r.ok) throw new Error(`${url}: ${r.status}`);
  return r.json();
}

/** Stream url into file, checking size and sha256 (the feed and the file come from the same release: this catches truncation, not forgery). */
async function download(url, file, want) {
  const r = await net.fetch(url, { cache: "no-store" });
  if (!r.ok || !r.body) throw new Error(`${url}: ${r.status}`);
  const hash = crypto.createHash("sha256");
  let size = 0;
  const tap = new Transform({
    transform(chunk, _enc, cb) {
      hash.update(chunk);
      size += chunk.length;
      const pct = want.size ? Math.floor((size / want.size) * 100) : 0;
      if (pct !== state.progress) {
        state.progress = pct;
        if (pct % 5 === 0) emit();
      }
      cb(null, chunk);
    },
  });
  fs.mkdirSync(path.dirname(file), { recursive: true });
  await pipeline(Readable.fromWeb(r.body), tap, fs.createWriteStream(file));
  const got = hash.digest("hex");
  if ((want.size && size !== want.size) || (want.sha256 && got !== want.sha256)) {
    fs.rmSync(file, { force: true });
    throw new Error(`${path.basename(file)}: download corrupted (${size} bytes, sha256 ${got})`);
  }
}

const releaseURL = (version, file) =>
  process.env.GERDR_UPDATE_FEED ? new URL(encodeURIComponent(file), FEED).href : `https://github.com/${REPO}/releases/download/v${version}/${encodeURIComponent(file)}`;

async function fetchBackend(feed) {
  const b = feed.backend[PLAT];
  const dir = path.join(root(), feed.version);
  const packed = path.join(root(), `.${feed.version}.br`);
  await download(releaseURL(feed.version, b.file), packed, b);
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
  const tmp = path.join(dir, EXE + ".tmp");
  await pipeline(fs.createReadStream(packed), zlib.createBrotliDecompress(), fs.createWriteStream(tmp, { mode: 0o755 }));
  fs.renameSync(tmp, path.join(dir, EXE));
  fs.rmSync(packed, { force: true });
  // current.json last: until it says so, the half-written directory is never picked
  const cur = path.join(root(), "current.json");
  fs.writeFileSync(cur + ".tmp", JSON.stringify({ version: feed.version, shell: SHELL }));
  fs.renameSync(cur + ".tmp", cur);
}

async function fetchApp(feed) {
  const a = feed.app[PLAT];
  const file = path.join(stagingDir(), a.file);
  if (fs.existsSync(file) && fs.statSync(file).size === a.size) return file; // downloaded before, not installed yet
  fs.rmSync(stagingDir(), { recursive: true, force: true });
  await download(releaseURL(feed.version, a.file), file + ".part", a);
  fs.renameSync(file + ".part", file);
  return file;
}

// ---- state ----

// phase: idle | checking | downloading (progress %, of version) — for Settings (main.js status()).
let state = { phase: "idle", progress: 0, downloading: "", error: "", checkedAt: 0 };
// What's waiting: { kind: "backend" | "app", version, file? } — "backend" is applied by restarting the backend.
let pending = null;
let onChange = () => {};
const emit = () => onChange();

function setPending(p) {
  pending = p;
  emit();
}

/** Look for a release; download what it takes. Returns a short line for the "check for updates" menu item. */
async function check() {
  if (!enabled()) return "This build doesn't update itself (local build).";
  if (state.phase !== "idle") return "Already checking.";
  Object.assign(state, { phase: "checking", error: "" });
  emit();
  try {
    const feed = await fetchJSON(FEED);
    state.checkedAt = Date.now();
    const have = pending?.version && newer(pending.version, backend().version) ? pending.version : backend().version;
    if (!newer(feed.version, have)) return `Up to date (${have}).`;
    Object.assign(state, { phase: "downloading", progress: 0, downloading: feed.version });
    emit();
    if (feed.shell === SHELL && feed.backend?.[PLAT]) {
      await fetchBackend(feed);
      setPending({ kind: "backend", version: feed.version });
    } else if (feed.app?.[PLAT]) {
      setPending({ kind: "app", version: feed.version, file: await fetchApp(feed) });
    } else return `${feed.version} has no package for ${PLAT}.`;
    return `Downloaded ${feed.version}.`;
  } catch (e) {
    state.error = String(e?.message || e);
    return `Update check failed: ${state.error}`;
  } finally {
    Object.assign(state, { phase: "idle", progress: 0, downloading: "" });
    emit();
  }
}

/**
 * The backend the running gerdr should be: newer than what it reports means a restart is due, also when nothing was
 * downloaded (the background service still runs the version before a manual App update).
 */
function noteRunning(version) {
  if (pending?.kind === "app") return;
  const want = backend().version;
  if (newer(want, version)) {
    if (pending?.version !== want) setPending({ kind: "backend", version: want });
  } else if (pending) setPending(null);
}

// ---- installing a whole App ----

/** /Applications/Gerdr.app, or null when it can't be swapped in place (translocated, run off the dmg, read-only dir). */
function macBundle() {
  const b = path.resolve(process.execPath, "..", "..", "..");
  if (!b.endsWith(".app") || b.includes("/AppTranslocation/") || b.startsWith("/Volumes/")) return null;
  try {
    fs.accessSync(path.dirname(b), fs.constants.W_OK);
    return b;
  } catch {
    return null;
  }
}

const run = (cmd, args) => new Promise((ok, fail) => execFile(cmd, args, (e, out) => (e ? fail(e) : ok(String(out)))));

/**
 * mac: copy the new bundle out of the dmg next to the old one (same volume, so the swap is two renames — copying
 * into /Applications in place can get stopped halfway, see Makefile install), then after we quit a detached script
 * swaps and relaunches. Anything off the happy path: open the dmg and let the person drag it over.
 * Windows: run the installer silently and relaunch. Resolves true when the caller should quit now.
 */
async function installApp(file) {
  if (process.platform === "win32") {
    spawn(file, ["/S", "--force-run"], { detached: true, stdio: "ignore" }).unref();
    return true;
  }
  const bundle = macBundle();
  if (!bundle || !file.endsWith(".dmg")) return openInstaller(file);
  const staged = path.join(path.dirname(bundle), ".Gerdr-update.app");
  const mnt = path.join(stagingDir(), "mnt");
  try {
    fs.rmSync(staged, { recursive: true, force: true });
    fs.mkdirSync(mnt, { recursive: true });
    await run("hdiutil", ["attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, file]);
    try {
      const inner = fs.readdirSync(mnt).find((n) => n.endsWith(".app"));
      if (!inner) throw new Error("no .app in the dmg");
      await run("ditto", [path.join(mnt, inner), staged]);
    } finally {
      await run("hdiutil", ["detach", "-quiet", mnt]).catch(() => {});
    }
  } catch (e) {
    console.error("update:", e);
    fs.rmSync(staged, { recursive: true, force: true });
    return openInstaller(file);
  }
  // Waits for this process to go, swaps, relaunches; if a rename fails puts the old one back and opens the dmg instead.
  const script = `
while kill -0 "$1" 2>/dev/null; do sleep 0.2; done
old="$2.old"; rm -rf "$old"
if mv "$2" "$old" && mv "$3" "$2"; then rm -rf "$old" "$4"; open "$2"
else [ -d "$2" ] || mv "$old" "$2"; rm -rf "$3"; open "$2"; open "$4"; fi`;
  spawn("/bin/sh", ["-c", script, "sh", String(process.pid), bundle, staged, file], { detached: true, stdio: "ignore" }).unref();
  return true;
}

function openInstaller(file) {
  require("electron").shell.openPath(file);
  return false;
}

// ---- schedule ----

// Automatic updates (Settings → 版本, on by default): look for releases and download them by itself, and apply a
// downloaded backend once idle (main.js). Off: only "check for updates" looks, and what it downloads waits for a click.
// Kept here in userData, per machine: the shell needs it before any page is up, and it's about this App, not gerdr.
const prefsFile = () => path.join(app.getPath("userData"), "update-prefs.json");
function auto() {
  try {
    return JSON.parse(fs.readFileSync(prefsFile(), "utf8")).auto !== false;
  } catch {
    return true;
  }
}
function setAuto(on) {
  fs.writeFileSync(prefsFile(), JSON.stringify({ auto: !!on }));
  if (on) check().then((m) => console.log("update:", m)); // turned back on: don't wait hours for the next round
  emit();
}

let timer = null;
function start(changed) {
  onChange = changed;
  cleanup();
  if (!enabled()) return;
  const tick = () => check().then((m) => console.log("update:", m));
  timer = setTimeout(function loop() {
    if (auto()) tick();
    timer = setTimeout(loop, 4 * 3600 * 1000);
  }, 20 * 1000);
}

module.exports = {
  backend,
  start,
  check,
  noteRunning,
  installApp,
  enabled,
  auto,
  setAuto,
  pending: () => pending,
  state: () => ({ ...state }),
  newer,
};
