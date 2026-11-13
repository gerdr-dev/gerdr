// 和桌面 App 外壳之间的桥。浏览器里跑时这些都退化成网页自己的实现。
// The shells (tools/chromeshell, tools/winshell) put window.gerdrNative there (their bridge.js); the WKWebView shell
// before them used messageHandlers.gerdr, same messages, still accepted.

import { toast } from "sonner";
import { api } from "./api";
import { locale, t } from "./i18n";
import { set } from "./store";

type Bridge = { postMessage(m: unknown): void };

function bridge(): Bridge | undefined {
  const w = window as any;
  return w.gerdrNative ?? w.webkit?.messageHandlers?.gerdr;
}

export const isNative = () => !!bridge();

// The App's own menus and dialogs follow this page's language (the shell's locale message); the language is fixed for the life of
// the page (lib/i18n.ts), so telling once on load is enough. Older shells ignore the message.
bridge()?.postMessage({ type: "locale", locale: locale() });

/**
 * Desktop App: where a picked / pasted / dropped File sits on this machine, so it is attached by its path (folders
 * too) instead of uploaded. undefined in a browser, in a shell too old to have it, and for files not on disk.
 */
export function nativeFilePath(f: File): string | undefined {
  const get = (window as any).gerdrNative?.getPathForFile;
  return (typeof get === "function" && get(f)) || undefined;
}

export const hasNativeFilePath = () => typeof (window as any).gerdrNative?.getPathForFile === "function";

/** space: the session (layout.ts Space) the conversation is in, so clicking it brings up the window showing that one. */
export function nativeNotify(title: string, body: string, sessionId?: string, space?: string) {
  const b = bridge();
  if (b) {
    b.postMessage({ type: "notify", title, body, sessionId, space });
    return true;
  }
  return false;
}

/**
 * App 里: bring up the window showing `space` and go to the conversation there, as a notification click does (the
 * macOS shell's __noteClick, tools/chromeshell noteClick; with no such window it goes to this one). false = not
 * handled (a browser, or the Windows shell with its one window): the caller goes to it here.
 */
export function nativeFocusSession(sessionId: string, space: string) {
  const w = window as any;
  if (w.gerdrNative?.shell !== "chrome") return false;
  w.gerdrNative.postMessage({ type: "__noteClick", sessionId, space });
  return true;
}

export function nativeBadge(count: number) {
  bridge()?.postMessage({ type: "badge", count });
}

/** App 里：用系统默认程序打开本机文件（reveal = 在访达 / 资源管理器里显示）。相对路径按 cwd 算。 */
export function nativeOpenPath(path: string, cwd: string | undefined, reveal: boolean) {
  bridge()?.postMessage({ type: "openPath", path, cwd, reveal });
}

export function nativeOpenURL(url: string) {
  const b = bridge();
  if (b) b.postMessage({ type: "open", url });
  else window.open(url, "_blank", "noopener");
}

/** App 里界面所在的渲染进程号（量内存用）。浏览器里没有。 */
export async function nativeWebPid(): Promise<number | undefined> {
  const b = bridge();
  if (!b) return undefined;
  const w = window as any;
  if (!w.__GERDR_WEB_PID__) {
    b.postMessage({ type: "webPid" });
    for (let i = 0; i < 10 && !w.__GERDR_WEB_PID__; i++) await new Promise((r) => setTimeout(r, 50));
  }
  return w.__GERDR_WEB_PID__ || undefined;
}

/** App 里：open a window of its own for a (herdr-style) session. */
export function nativeNewWindow(space: string) {
  bridge()?.postMessage({ type: "newWindow", space });
}

/** App 里：tell the shell which session this window shows (its title, where a notification click goes, what ⌘N copies). */
export function nativeSpace(id: string, name: string) {
  bridge()?.postMessage({ type: "space", id, name });
}

/** App 里：菜单上显示 / 认的键跟着设置走（动作 id → accelerator，null = 菜单上不挂键）。 */
export function nativeMenuKeys(keys: Record<string, string | null>) {
  bridge()?.postMessage({ type: "menuKeys", keys });
}

/** The desktop App's update status, pushed by the shell (desktop/main.js updateStatus) to window.gerdr.appUpdate. */
export type AppUpdateStatus = {
  enabled: boolean; // false: this build doesn't update itself (0.1.0-dev, GERDR_BIN, GERDR_NO_UPDATE)
  auto?: boolean; // automatic updates (Settings → 版本); absent from shells before the switch existed
  app: string; // the App (shell) version
  backend: string; // the backend the App runs: the bundle's or a downloaded newer one
  running: string; // the version of the gerdr actually answering (the background service may still be older)
  busy: { turns: number; waiting: number; tasks: number; terms: number } | null; // what a restart would cut off
  pending: { kind: "backend" | "app"; version: string } | null; // downloaded, waiting to be applied
  phase: "idle" | "checking" | "downloading" | "applying";
  progress: number; // download %, while downloading
  downloading: string; // the version being downloaded
  error: string;
  checkedAt: number;
};

/** App 里：ask the shell for the update status (it answers through window.gerdr.appUpdate); check = look for a release now. */
export function nativeAskUpdate(check = false) {
  bridge()?.postMessage({ type: "update", check });
}

/** App 里：turn automatic updates on / off (the shell keeps it, desktop/updater.js setAuto). */
export function nativeSetAutoUpdate(on: boolean) {
  bridge()?.postMessage({ type: "update", auto: on });
}

/** App 里：apply the downloaded update now (the shell asks first if it would cut something off). */
export function nativeApplyUpdate() {
  bridge()?.postMessage({ type: "update", apply: true });
}

/**
 * An update that waits for the person to click: a whole new App (the shell has to restart), or any update with automatic
 * updates off. The settings button carries a dot while one waits, so a toast closed with "Later" isn't the only trace.
 */
export function updateWaitsForClick(u: AppUpdateStatus | null): boolean {
  const p = u?.pending;
  return !!p && u!.phase !== "applying" && (p.kind === "app" || u!.auto === false);
}

/**
 * Keep the status for Settings, and toast an update once per version only when it needs a click. One that goes in by
 * itself (the backend, automatic updates on) says nothing until it is in ("updated to", noteUpdated); while it waits
 * for agents to finish, Settings → General → Version says on what. The text says what the click does to the person —
 * windows closing, agents cut off or not — not how the update is made: "the whole App" meant nothing to people.
 */
let toasted = "";
export function onAppUpdate(u: AppUpdateStatus) {
  set({ appUpdate: u });
  relayUpdate(u);
  noteUpdated(u.running);
  const p = u.pending;
  if (!updateWaitsForClick(u)) return void toast.dismiss("app-update");
  if (!p || toasted === p.version) return;
  toasted = p.version;
  void serviceOn().then((service) => {
    const app = p.kind === "app";
    toast(app ? t("native.updateRestart", { version: p.version }) : t("native.updateReady", { version: p.version }), {
      id: "app-update",
      duration: Infinity,
      closeButton: true,
      description: app ? restartEffect(u, service) : t("native.updateReadyDesc"),
      action: { label: app ? t("native.restartToUpdate") : t("native.update"), onClick: nativeApplyUpdate },
      cancel: { label: t("native.later"), onClick: () => {} },
      // Two buttons beside the text squeezed it into a narrow column: the text takes the row, the buttons wrap under
      // it, right-aligned by sonner's margin-left: auto, kept on "Later" only so the two stay together. Sonner's own
      // gap, flex and margins are unlayered CSS, which beats Tailwind's layered utilities: hence the `!`.
      classNames: { toast: "flex-wrap gap-y-3!", content: "basis-full!", actionButton: "ml-0!" },
    });
  });
}

/**
 * What restarting the App does. With the background service on, agents run in the service and carry on while the
 * shell is swapped (docs/dev/SERVICE.md); without it, quitting the App ends them. null = didn't find out: say only
 * what is sure.
 */
export function restartEffect(u: AppUpdateStatus, service: boolean | null): string {
  const b = u.busy;
  const busy = !!b && b.turns + b.waiting + b.tasks + b.terms > 0;
  if (service) return t("native.restartKeepsAgents");
  if (service === false && busy) return t("native.restartCutsAgents");
  return t("native.restartWindows");
}

let service: Promise<boolean | null> | null = null;
function serviceOn(): Promise<boolean | null> {
  service ??= api
    .service()
    .then((s) => s.installed)
    .catch(() => null);
  return service;
}

/**
 * Say so once the backend answering is newer than the one this App saw last. An automatic update applies itself while
 * nobody is looking (the shell restarts the backend once idle) and used to leave no trace. Kept in localStorage: the
 * restart may reload the page, and a whole-App update relaunches it. A `make install` counts too, which is wanted.
 * Each window compares with what it saw itself first: the shell pushes the status to every window and they share
 * localStorage, so going by storage alone only the first window to hear of it said anything (often a hidden one).
 */
const LAST_RUNNING = "gerdr.lastRunning";
let seenRunning: string | null = null;
function noteUpdated(running: string) {
  if (!parseVer(running)) return; // not reported yet, or a 0.1.0-dev build
  const last = seenRunning ?? localStorage.getItem(LAST_RUNNING);
  seenRunning = running;
  if (last === running) return;
  localStorage.setItem(LAST_RUNNING, running);
  if (!last || !newer(running, last)) return; // first run: nothing to compare with
  toast.success(t("native.updated", { version: running }), {
    id: "app-updated",
    duration: 10_000,
    closeButton: true,
    description: t("native.updatedFrom", { version: last }),
  });
}

/** x.y.z or a local build's x.y.z-N-gabc[-dirty] as [x, y, z, N], like internal/selfupdate ParseVer. */
function parseVer(v: string): number[] | null {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:-(\d+)-g[0-9a-f]+)?(?:-dirty)?$/.exec(v.trim());
  return m ? [1, 2, 3, 4].map((i) => Number(m[i] ?? 0)) : null;
}

function newer(a: string, b: string): boolean {
  const x = parseVer(a);
  const y = parseVer(b);
  if (!x || !y) return false;
  const i = x.findIndex((n, k) => n !== y[k]);
  return i >= 0 && x[i] > y[i];
}

/**
 * Pass the status on to the backend for devices without a shell (a phone over remote access, server/appupdate.go),
 * and keep re-sending it: the backend takes a window that went quiet for 90s as closed.
 */
let relayed: AppUpdateStatus | null = null;
function relayUpdate(u: AppUpdateStatus) {
  const first = !relayed;
  relayed = u;
  api.reportAppUpdate(u).catch(() => {});
  if (first) setInterval(() => relayed && api.reportAppUpdate(relayed).catch(() => {}), 30_000);
}

/** A button pressed on another device ("app_update_op", addressed to this page): do it as if pressed here. */
export function runRelayedUpdateOp(op: string, on: boolean) {
  if (op === "check") nativeAskUpdate(true);
  else if (op === "apply") nativeApplyUpdate();
  else if (op === "auto") nativeSetAutoUpdate(on);
}
