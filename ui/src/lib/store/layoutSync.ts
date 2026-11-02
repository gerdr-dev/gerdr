import { api, clientId } from "../api";
import { validLayout, viewOf, withView, type Layout, type View } from "../layout";
import { get, set } from "../store";

// ---- 布局：本地先改，300ms 后写回服务端（别的窗口 / 设备跟着变）----

let layoutTimer: ReturnType<typeof setTimeout> | undefined;

// 这个端自己的「看的位置」（layout.ts 的 View）：存在本机浏览器里，主机和远程（不同的 origin）天然各一份。
const VIEW_KEY = "gerdr.view";
function saveView(l: Layout) {
  try {
    localStorage.setItem(VIEW_KEY, JSON.stringify(viewOf(l)));
  } catch {}
}
export function loadView(): View | null {
  try {
    const v = JSON.parse(localStorage.getItem(VIEW_KEY) ?? "null");
    return v && typeof v.active === "string" && v.ws && v.tabs ? v : null;
  } catch {
    return null;
  }
}

export function setLayout(fn: (l: Layout) => Layout) {
  const prev = get().layout;
  const next = fn(prev);
  if (next === prev) return;
  set({ layout: next });
  saveView(next);
  clearTimeout(layoutTimer);
  layoutTimer = setTimeout(() => {
    api.putLayout(get().layout, clientId).catch(() => {});
  }, 300);
  afterLayoutChange(prev, next);
}

/** Save the layout now instead of in 300ms (setLayout), for a step the server must see it for (deleting a closed project). */
export function flushLayout(): Promise<unknown> {
  clearTimeout(layoutTimer);
  return api.putLayout(get().layout, clientId);
}

/** 别的端存的布局（或重连时服务端那份）：结构照收，「看的位置」留自己的 —— 手机上切 tab，主机不跟着动。 */
export function applyRemoteLayout(l: unknown) {
  if (validLayout(l)) {
    const prev = get().layout;
    const next = withView(l, viewOf(prev));
    set({ layout: next });
    afterLayoutChange(prev, next);
  }
}

const layoutListeners = new Set<(prev: Layout, next: Layout) => void>();
export function onLayoutChange(fn: (prev: Layout, next: Layout) => void) {
  layoutListeners.add(fn);
  return () => layoutListeners.delete(fn);
}
function afterLayoutChange(prev: Layout, next: Layout) {
  for (const fn of layoutListeners) fn(prev, next);
}
