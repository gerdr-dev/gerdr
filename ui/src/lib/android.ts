// 跑在安卓 App（仓库里的 android/，一个 WebView 加载远程访问的地址）里时，壳注入的那条线。另一半在
// android/app/src/main/java/io/github/zbysir/gerdr/Bridge.kt，消息格式两边要对得上（docs/dev/ANDROID.md「桥」）：
// JSON 字符串、按 t 分派。**对不上是完全静默的**（返回键 400ms 后按「没处理」走、通知点开不跳）。
//
// 和桌面 App 那条（native.ts，Electron 的 gerdrNative）是两回事：形状不一样（这边是字符串、t 字段），
// 能做的事也不一样，分开写。
//
// 壳用 addWebMessageListener 注入 gerdrAndroid，**只对它连的那台服务器的 origin 注入** —— 所以「有这个对象」
// 就是「我在 App 里，而且是 App 认的那台」。对象在页面脚本跑之前就在了，模块顶层读一次就够。

import { lightboxOpen } from "@/components/Lightbox";
import { focusSession } from "./actions";
import { activeTab } from "./layout";
import { get, set } from "./store";

export type AndroidMsg = { t: string; [k: string]: unknown };

type Port = { postMessage(data: string): void; onmessage: ((e: { data: string }) => void) | null };

const port = (globalThis as unknown as { gerdrAndroid?: Port }).gerdrAndroid;
const subs = new Map<string, Set<(m: AndroidMsg) => void>>();
/** 每一类消息最近的那一条：后挂上的组件（设置是点开才挂的）也拿得到，不用等壳再发一次 */
const last = new Map<string, AndroidMsg>();

export const isAndroid = () => !!port;

export function postAndroid(m: AndroidMsg) {
  port?.postMessage(JSON.stringify(m));
}

/**
 * 订阅壳发来的某一类消息，挂上时有最近一条就先回调一次。返回退订函数。
 * replay = false 给「事件」类（按键、返回、通知点开）：补发最近一条等于凭空再按一次。
 */
export function onAndroid(t: string, f: (m: AndroidMsg) => void, replay = true): () => void {
  let s = subs.get(t);
  if (!s) subs.set(t, (s = new Set()));
  s.add(f);
  const m = replay ? last.get(t) : undefined;
  if (m) f(m);
  return () => {
    s.delete(f);
  };
}

if (port) {
  port.onmessage = (e) => {
    let m: AndroidMsg;
    try {
      m = JSON.parse(e.data);
    } catch {
      return;
    }
    last.set(m.t, m);
    subs.get(m.t)?.forEach((f) => f(m));
  };
}

/**
 * 页面起来之后接上壳：hello（壳收到它才有往回发的通道）、局域网候选、返回键、通知点开、音量键。
 * App.tsx 在 boot 之后调一次。
 */
export function initAndroid() {
  if (!port) return;
  // 热更新会再调一次（App 的 effect 重跑，或者这个模块整个重新执行）：先把上一次挂的拆掉，不然每更新一次
  // 就多一份 —— 按一下音量键跳两格、返回键回两次。挂在 globalThis 上，模块重新执行了也找得到上一次的
  const g = globalThis as unknown as { __gerdrAndroidOff?: () => void };
  g.__gerdrAndroidOff?.();
  document.documentElement.classList.add("android");
  postAndroid({ t: "hello" });
  syncAndroidTheme();
  sendLan();
  // 局域网 IP 会变（换 Wi-Fi、DHCP）：回到前台时再报一次，壳会拿新候选重探
  const onVisible = () => document.visibilityState === "visible" && sendLan();
  document.addEventListener("visibilitychange", onVisible);
  const offs = [
    onAndroid("back", () => postAndroid({ t: "back", handled: closeTop() }), false),
    onAndroid("focus", (m) => typeof m.session === "string" && focusSession(m.session), false),
    onAndroid("key", (m) => (volumeKey(m.k === "up" ? "ArrowUp" : "ArrowDown"), syncMedia()), false),
  ];
  // Media events don't bubble, but they do go through the capture phase
  for (const ev of MEDIA_EVENTS) document.addEventListener(ev, syncMedia, true);
  playing = false;
  syncMedia();
  g.__gerdrAndroidOff = () => {
    for (const ev of MEDIA_EVENTS) document.removeEventListener(ev, syncMedia, true);
    document.removeEventListener("visibilitychange", onVisible);
    offs.forEach((off) => off());
  };
}

/**
 * 局域网直连的候选（`https://192.168.x.x:7878`）递给壳。换路是壳的事、**不换 origin**：壳按本页域名对这些地址做
 * TLS 握手（真证书、验域名），通了就让连接从那儿走，地址栏、cookie、passkey 全不变（docs/dev/ANDROID.md「局域网直连」）。
 * 服务端每次现报（IP 会变），没开直连口时是空的。
 */
async function sendLan() {
  try {
    const r = await fetch("/api/remote/lan", { credentials: "same-origin" });
    if (!r.ok) return;
    const { origins } = (await r.json()) as { origins?: string[] };
    if (origins?.length) postAndroid({ t: "lan", origins });
  } catch {
    /* 拿不到就不直连，照走中继 */
  }
}

/**
 * 系统栏（状态栏 / 导航栏）是壳画的，底色跟着页面走，不然浅色主题顶上一条黑。读计算后的背景色（token 只在 index.css
 * 一处），所以要在 .dark 类落到 <html> 上**之后**调。
 */
export function syncAndroidTheme() {
  if (!port) return;
  const bg = getComputedStyle(document.body).backgroundColor;
  if (!bg) return;
  postAndroid({ t: "theme", bg, light: !document.documentElement.classList.contains("dark") });
}

/**
 * 返回键：先收最上面那一层（和桌面上按 Esc 同一个顺序），没东西可收就回 false，壳把 App 退到后台（不是关掉）。
 * **不往会话里发 Esc** —— 返回键是「离开」的意思，替人打断 agent 正在干的事不对。
 */
function closeTop(): boolean {
  // 图片灯箱：自己画的浮层（不是 Radix，下面那个选择器认不出来），压在所有弹窗上面（文件查看器里也能点开图），先关它。
  // 发 Esc 而不是直接清状态：它在窗口上（捕获阶段）收 Esc，走自己的关闭动画
  if (lightboxOpen()) {
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", code: "Escape", bubbles: true, cancelable: true }));
    return true;
  }
  // 弹窗（设置、命令面板、查看器……都是 Radix 的 Dialog）：发一个 Esc 给它，它自己关
  const dlg = document.querySelector<HTMLElement>('[role="dialog"][data-state="open"], [role="alertdialog"][data-state="open"]');
  if (dlg) {
    dlg.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", code: "Escape", bubbles: true, cancelable: true }));
    if (get().dialog) set({ dialog: null });
    return true;
  }
  const s = get();
  if (s.dialog) return (set({ dialog: null }), true);
  if (s.switcher) return (set({ switcher: false }), true);
  if (s.drawer) return (set({ drawer: false }), true);
  // 软键盘开着：先收键盘（输入框失焦）
  const a = document.activeElement as HTMLElement | null;
  if (a && (a.tagName === "TEXTAREA" || a.tagName === "INPUT")) return (a.blur(), true);
  return false;
}

const MEDIA_EVENTS = ["play", "playing", "pause", "ended", "emptied"];
let playing = false;

/**
 * Tell the shell whether a video / audio in the page is playing: while one is, the volume keys stay volume keys
 * instead of turning into ↑ / ↓ (MainActivity.dispatchKeyEvent). Rescans the DOM rather than trusting the event's
 * element: a player the virtual chat list unmounts is paused off-document, and that pause never reaches us. The
 * rescan on every volume key catches up with that case (that one press is lost, the next one is volume again).
 */
function syncMedia() {
  const now = [...document.querySelectorAll<HTMLMediaElement>("video, audio")].some((m) => !m.paused && !m.ended);
  if (now === playing) return;
  playing = now;
  postAndroid({ t: "media", playing: now });
}

/**
 * 音量键当 ↑ / ↓（设置里能关）：等于在焦点那个会话的输入框里按方向键 —— 输入框空着时翻之前发过的话，
 * agent 在问问题时在选项间移动（Composer 的 onKeyDown 那一套）。手机竖屏上不用去够软键盘。
 * 不聚焦输入框：聚焦会把软键盘弹起来。
 */
function volumeKey(key: "ArrowUp" | "ArrowDown") {
  const pane = activeTab(get().layout).focus;
  const box = pane ? document.querySelector<HTMLTextAreaElement>(`textarea[data-composer="${CSS.escape(pane)}"]`) : null;
  box?.dispatchEvent(new KeyboardEvent("keydown", { key, code: key, bubbles: true, cancelable: true }));
}
