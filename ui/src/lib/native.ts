// 和 mac App（WKWebView 外壳）之间的桥。浏览器里跑时这些都退化成网页自己的实现。

type Bridge = { postMessage(m: unknown): void };

function bridge(): Bridge | undefined {
  return (window as any).webkit?.messageHandlers?.gerdr;
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

export function nativeOpenURL(url: string) {
  const b = bridge();
  if (b) b.postMessage({ type: "open", url });
  else window.open(url, "_blank", "noopener");
}

/** mac App 里界面所在的 WebContent 进程号（量内存用）。浏览器里没有。 */
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
