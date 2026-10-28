// 和桌面 App 外壳之间的桥。浏览器里跑时这些都退化成网页自己的实现。
// 外壳现在是 Electron（desktop/preload.js 挂的 gerdrNative）；老的 mac 外壳是 WKWebView（messageHandlers.gerdr），
// 两边消息形状一样，都认。

type Bridge = { postMessage(m: unknown): void };

function bridge(): Bridge | undefined {
  const w = window as any;
  return w.gerdrNative ?? w.webkit?.messageHandlers?.gerdr;
}

export const isNative = () => !!bridge();

export function nativeNotify(title: string, body: string, sessionId?: string) {
  const b = bridge();
  if (b) {
    b.postMessage({ type: "notify", title, body, sessionId });
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
