import { useEffect, useState, useSyncExternalStore } from "react";

/**
 * 手机这一档的上界（px，不含）= Tailwind 的 `md`（768）。改这个数要连 `max-md:` 那些 class 一起改。
 *
 * 为什么是 768 不是更窄：桌面那套是「侧栏 224px + tab 栏 + 分屏」，低于 768 时侧栏一占、两个 pane 一分，
 * 每个 pane 只剩三百来 px，对话流和输入框都读不了。平板竖屏（768+）照旧用桌面那套。
 */
export const PHONE_MAX = 768;

const mq = () => window.matchMedia(`(max-width: ${PHONE_MAX - 0.02}px)`);

export const isPhone = () => mq().matches;

/** 现在是不是手机那一档（跟着窗口 / 转屏变）。 */
export function usePhone() {
  const [phone, setPhone] = useState(isPhone);
  useEffect(() => {
    const m = mq();
    const f = () => setPhone(m.matches);
    f();
    m.addEventListener("change", f);
    return () => m.removeEventListener("change", f);
  }, []);
  return phone;
}

/** 触屏（没有实体指针）。判「要不要自动聚焦」看这个，不看屏幕多宽 —— 平板也是手指。 */
export const isCoarse = () => window.matchMedia("(pointer: coarse)").matches;

/** On screen: laid out and not made invisible (an element under `display: none` has no client rects). */
export const onScreen = (el: Element) => el.getClientRects().length > 0 && getComputedStyle(el).visibility === "visible";

/**
 * The dialogs (`role=dialog` / `alertdialog`) on screen, bottom to top (they are portaled in the order they open).
 *
 * "In the DOM" is not "open over the page": a view kept mounted out of sight (a panel while Code shows, another
 * project's panel) keeps an open drawer of its own, invisible. Every "is a dialog open" check that just did
 * querySelector("[role=dialog]") stopped working while one sat there: no focus in the composer after a new tab, a split,
 * a copy, no typing into the chat. Ask this (or dialogOver) instead.
 */
export function dialogsOnScreen(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>("[role=dialog], [role=alertdialog]")].filter(onScreen);
}

/**
 * Whether a dialog on screen stands between the user and `el`, so focus / keys must stay with it: the topmost one
 * doesn't contain `el` (a chat inside a panel's drawer is the dialog's own content). Without `el`: any dialog on screen.
 */
export function dialogOver(el?: Element | null): boolean {
  const all = dialogsOnScreen();
  const top = all[all.length - 1];
  return !!top && !(el && top.contains(el));
}

/**
 * Keeps the page as tall as the visual viewport: writes its height, rounded, into `--vvh` on html for the host's CSS
 * (html / body / #root's height), or with `{ height: true }` straight into html's own `height` (the host's body and
 * root then take 100%).
 *
 * iOS Safari doesn't shrink the layout viewport for the soft keyboard, only the visual one: without this the page stays
 * as tall as before and the keyboard covers the composer. Android Chrome shrinks the layout viewport itself, so there
 * it's a harmless repeat.
 *
 * `height` is the cheap one: Chromium restyles every element in the page when any custom property on html changes
 * (registered non-inherited or not), and the keyboard changes this one each time it comes up or goes down. In a long
 * chat on an Android phone that was tens of ms in the frame the keyboard starts going down; html's own height
 * restyles a handful (tools/scrolltest/grow.mjs).
 */
export function useViewportHeight({ height = false }: { height?: boolean } = {}) {
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const f = () => {
      seen(vv);
      const h = `${Math.round(vv.height)}px`;
      if (height) document.documentElement.style.height = h;
      else document.documentElement.style.setProperty("--vvh", h);
      // 键盘弹起时 iOS 会把整页往上推一截（滚动的是 window），我们已经自己缩了高度，推回来
      if (window.scrollY) window.scrollTo(0, 0);
    };
    f();
    vv.addEventListener("resize", f);
    vv.addEventListener("scroll", f);
    return () => {
      vv.removeEventListener("resize", f);
      vv.removeEventListener("scroll", f);
    };
  }, [height]);
}

// The tallest the visual viewport has been at the current width: the height with no soft keyboard. Reset when the
// width changes (rotation, split screen), since the old height means nothing there.
let tallest = 0;
let tallestW = 0;
const seen = (vv: VisualViewport) => {
  if (vv.width !== tallestW) ((tallestW = vv.width), (tallest = 0));
  tallest = Math.max(tallest, vv.height);
};

/**
 * Whether the soft keyboard is showing. Focus doesn't tell: on Android, hiding the keyboard with its own button or a
 * swipe leaves the text box focused, and a later tap anywhere in the page brings the keyboard back. So this goes by
 * the viewport instead: more than 150px shorter than its keyboard-less height means a keyboard is taking the space.
 */
export function keyboardUp() {
  const vv = window.visualViewport;
  if (!vv) return true;
  seen(vv);
  return vv.height < tallest - 150;
}

// Is a hardware keyboard there to press shortcuts on? Decides whether key hints are drawn (menu keycaps, the ↵ on a
// question card's submit button): on a phone without one they're dead weight, with one (a tablet on a keyboard case)
// they're the guide.
//
// A page can't ask the system, so:
// - Not touch-first (a desktop, a laptop): yes, always.
// - A host that knows (the Android App, from Configuration.hardKeyboardHidden): its answer, via setHwKeyboard.
// - Any other touch device (a browser, an iPad, an older App): no, until a key arrives that only a real keyboard sends
//   (a physical key code; soft keyboards send none, or 229 while composing), and no again once a soft keyboard shows
//   (the viewport shrinks): the system doesn't raise one while a hardware keyboard is attached.

let kbGuessed = false;
let kbNative: boolean | undefined;
const kbSubs = new Set<() => void>();
const kbEmit = () => kbSubs.forEach((f) => f());

export function hasHwKeyboard(): boolean {
  if (typeof window === "undefined" || !isCoarse()) return true;
  if (kbNative !== undefined) return kbNative;
  return kbGuessed;
}

/** The host's own answer (true / false), overriding the guess; undefined goes back to guessing. */
export function setHwKeyboard(v: boolean | undefined) {
  if (v === kbNative) return;
  kbNative = v;
  kbEmit();
}

const setKbGuess = (v: boolean) => {
  if (v === kbGuessed) return;
  kbGuessed = v;
  kbEmit();
};

/** A key only a real keyboard sends: soft keyboards leave `code` empty (or say 229 while composing). */
const physicalKey = (e: KeyboardEvent) =>
  !e.isComposing && e.keyCode !== 229 && !!e.code && e.code !== "Unidentified" && !/^(Audio|Volume|Media|Browser)/.test(e.code);

let kbStarted = false;
function kbStart() {
  if (kbStarted || typeof window === "undefined") return;
  kbStarted = true;
  window.addEventListener("keydown", (e) => physicalKey(e) && setKbGuess(true), true);
  window.visualViewport?.addEventListener("resize", () => keyboardUp() && setKbGuess(false));
  // Turning a tablet changes coarse / fine on some devices, and the answer with it
  window.matchMedia("(pointer: coarse)").addEventListener("change", kbEmit);
}

export function useHwKeyboard(): boolean {
  kbStart();
  return useSyncExternalStore((f) => (kbSubs.add(f), () => void kbSubs.delete(f)), hasHwKeyboard);
}
