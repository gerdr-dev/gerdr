// 和桌面 App 外壳之间的桥。浏览器里跑时这些都退化成网页自己的实现。
// 外壳现在是 Electron（desktop/preload.js 挂的 gerdrNative）；老的 mac 外壳是 WKWebView（messageHandlers.gerdr），
// 两边消息形状一样，都认。

import { toast } from "sonner";
import { set } from "./store";

type Bridge = { postMessage(m: unknown): void };

function bridge(): Bridge | undefined {
  const w = window as any;
  return w.gerdrNative ?? w.webkit?.messageHandlers?.gerdr;
}

export const isNative = () => !!bridge();

/** space: the session (layout.ts Space) the conversation is in, so clicking it brings up the window showing that one. */
export function nativeNotify(title: string, body: string, sessionId?: string, space?: string) {
  const b = bridge();
  if (b) {
    b.postMessage({ type: "notify", title, body, sessionId, space });
    return true;
  }
  return false;
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
 * Keep the status for Settings, and toast a downloaded update once per version — unless nothing is running and automatic
 * updates are on, then the shell applies it by itself in a few seconds anyway (a whole-App update always waits for a click).
 */
let toasted = "";
export function onAppUpdate(u: AppUpdateStatus) {
  set({ appUpdate: u });
  const p = u.pending;
  if (!p || u.phase === "applying") return void toast.dismiss("app-update");
  const b = u.busy;
  const idle = !!b && b.turns + b.waiting + b.tasks + b.terms === 0;
  const auto = u.auto !== false;
  if (toasted === p.version || (p.kind === "backend" && idle && auto)) return;
  toasted = p.version;
  toast(`Gerdr ${p.version} 已下载好`, {
    id: "app-update",
    duration: Infinity,
    closeButton: true,
    description: p.kind === "app" ? "这次要换整个 App：会退出再重新打开" : auto ? "没有 agent 在跑、没开终端时会自己更新" : "自动更新关着：点了才更新",
    action: { label: "现在更新", onClick: nativeApplyUpdate },
  });
}
