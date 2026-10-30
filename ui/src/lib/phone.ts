import { useEffect, useState } from "react";

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

/**
 * 把可视区域的高度写进 `--vvh`（html / body / #root 的高度用它，见 index.css）。
 *
 * iOS Safari 弹出软键盘时**不缩布局视口**，只缩 visualViewport：不跟着它走的话，整页还是原来那么高，
 * 输入框被键盘盖在下面，人看不见自己在打什么。安卓 Chrome 默认会缩布局视口，这条在那边是无害的重复。
 */
export function useViewportHeight() {
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const f = () => {
      document.documentElement.style.setProperty("--vvh", `${Math.round(vv.height)}px`);
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
  }, []);
}
