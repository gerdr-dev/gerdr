import { ChevronLeft, ChevronRight, Download, FileSearch, X } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { chatHost } from "../host";
import { t } from "../i18n";
import { cn } from "../utils";

/*
 * 图片灯箱：对话里的图（自己发的、agent 画出来的、工具结果里的）点一下铺满屏幕看。
 *
 * - 从点的那张缩略图的位置放大出来，关的时候缩回去（还在原处的话；翻到别的图了就原地淡出）；
 * - 一组图（同一条消息里的几张 / agent 排成网格的几张）能左右翻：← → 键、两边的按钮、手机上左右划；
 * - 看细节：手机上双指捏合缩放、单指拖动、双击放大 / 还原；电脑上点图放大 / 还原、滚轮（触控板捏合）缩放、拖动；
 * - 关：点空白处、Esc、右上角 ✕；没放大时往下划也能关（和手机相册一样）；
 * - 本机文件右上角多一个「在查看器里打开」（gerdr 的 FileViewer，能看路径、用系统程序打开）。
 * - Videos (item.video) open the same way and start playing; the player's own controls take the pointer, so there is
 *   no zoom / drag on them — swipe on the backdrop to page, tap it to close.
 *
 * 缩放 / 拖动全是自己算的 transform（transform-origin 在图的左上角，view = 平移 + 倍数），不靠浏览器的页面缩放：
 * 页面缩放会把整个 gerdr 一起放大，松手也不会弹回来。所以整层 touch-action: none，手势全归这里。
 * 每帧直接改 style，不走 React 状态（捏合时一秒几十次重渲染会卡）。
 *
 * 状态放在这个模块自己的小 store 里、宿主组件挂一次（App.tsx）：打开灯箱的地方散在对话的各个组件里，
 * 而对话是虚拟列表，行随时会被卸载，灯箱不能挂在行里面。
 */

export type LightboxItem = { url: string; alt?: string; path?: string; cwd?: string; video?: boolean };

type State = { items: LightboxItem[]; index: number; origin?: { el: Element; index: number } } | null;

let state: State = null;
const subs = new Set<() => void>();
const emit = (s: State) => {
  state = s;
  subs.forEach((f) => f());
};

/** from：点的那个缩略图，灯箱从它的位置放大出来、关的时候缩回去 */
export function openLightbox(items: LightboxItem[], index = 0, from?: Element | null) {
  if (!items.length) return;
  const i = Math.max(0, Math.min(index, items.length - 1));
  emit({ items, index: i, origin: from ? { el: from, index: i } : undefined });
}

/** 灯箱开着没有（安卓返回键要先关它，见 lib/android.ts 的 closeTop） */
export const lightboxOpen = () => !!state;

export function LightboxHost() {
  const s = useSyncExternalStore(
    (f) => (subs.add(f), () => subs.delete(f)),
    () => state,
  );
  if (!s) return null;
  return <Lightbox items={s.items} index={s.index} origin={s.origin} />;
}

/**
 * Our own files (/api/fs/raw, /api/blobs) are asked for with ?download=1 so the server sends an attachment header:
 * the Android App's WebView hands downloads to its DownloadManager, which ignores the <a download> name and goes by
 * the header. Anything else (data:, another site's image) is linked as is.
 */
function downloadURL(url: string) {
  if (!/^\/api\/(fs\/raw|blobs\/)/.test(url)) return url;
  return url + (url.includes("?") ? "&" : "?") + "download=1";
}

const downloadName = (it: LightboxItem) => it.path?.split("/").pop() || it.alt || "";

type View = { s: number; x: number; y: number };
type Box = { left: number; top: number; w: number; h: number };

const MAX = 8;
const EASE = "cubic-bezier(0.2, 0.8, 0.2, 1)";

/** 适应屏幕的大小和位置（不放大比屏幕小的图：小图硬拉大是糊的，要看细节自己捏） */
function fitOf(nw: number, nh: number): Box {
  const pad = window.innerWidth < 768 ? 12 : 56;
  const W = window.innerWidth - pad * 2;
  const H = window.innerHeight - pad * 2;
  const k = Math.min(W / nw, H / nh, 1);
  const w = nw * k;
  const h = nh * k;
  return { left: (window.innerWidth - w) / 2, top: (window.innerHeight - h) / 2, w, h };
}

/** 缩略图那个框对应的 view：图缩到盖住缩略图（缩略图多是 object-cover / 定高，取大的那个比例） */
function viewFor(fit: Box, r: DOMRect): View {
  const s = Math.max(r.width / fit.w, r.height / fit.h);
  return { s, x: r.left + r.width / 2 - fit.left - (fit.w * s) / 2, y: r.top + r.height / 2 - fit.top - (fit.h * s) / 2 };
}

/** 缩略图本身就是这张图、已经加载好的话，直接拿它的原始尺寸（灯箱不用等加载，第一帧就能开始放大） */
function natOf(el: Element | undefined, url: string): { w: number; h: number } | null {
  const vid = el instanceof HTMLVideoElement ? el : el?.querySelector("video");
  if (vid) {
    // Thumbnails carry a #t= fragment (so Safari paints a first frame); compare without it.
    if (!vid.videoWidth || vid.src.split("#")[0] !== new URL(url, location.href).href) return null;
    return { w: vid.videoWidth, h: vid.videoHeight };
  }
  const img = el instanceof HTMLImageElement ? el : el?.querySelector("img");
  if (!img || !img.complete || !img.naturalWidth) return null;
  if (img.src !== new URL(url, location.href).href) return null;
  return { w: img.naturalWidth, h: img.naturalHeight };
}

function Lightbox({ items, index, origin }: { items: LightboxItem[]; index: number; origin?: { el: Element; index: number } }) {
  const it = items[index];
  const many = items.length > 1;
  const imgRef = useRef<HTMLImageElement & HTMLVideoElement>(null);
  const bgRef = useRef<HTMLDivElement>(null);
  const chromeRef = useRef<HTMLDivElement>(null);
  const [nat, setNat] = useState(() => natOf(origin?.index === index ? origin.el : undefined, it.url));
  const [, relayout] = useState(0);
  const [zoomed, setZoomed] = useState(false);
  const view = useRef<View>({ s: 1, x: 0, y: 0 });
  const opening = useRef(true);
  const closing = useRef(false);
  const fit = nat ? fitOf(nat.w, nat.h) : null;
  const fitRef = useRef(fit);
  fitRef.current = fit;

  const apply = (v: View, ms = 0) => {
    view.current = v;
    const img = imgRef.current;
    if (!img) return;
    img.style.transition = ms ? `transform ${ms}ms ${EASE}` : "none";
    img.style.transform = `translate(${v.x}px, ${v.y}px) scale(${v.s})`;
  };
  const fade = (o: number, ms = 0) => {
    for (const el of [bgRef.current, chromeRef.current]) {
      if (!el) continue;
      el.style.transition = ms ? `opacity ${ms}ms ${EASE}` : "none";
      el.style.opacity = String(o);
    }
  };

  /** 放大到 s 倍，屏幕上 (px, py) 那一点不动 */
  const zoomAt = (s: number, px: number, py: number, ms = 0) => {
    const f = fitRef.current;
    if (!f) return;
    const v = view.current;
    const cx = (px - f.left - v.x) / v.s;
    const cy = (py - f.top - v.y) / v.s;
    apply(clamp({ s, x: px - f.left - cx * s, y: py - f.top - cy * s }), ms);
    setZoomed(s > 1.01);
  };

  /** 收进合法范围：不小于适应屏幕、不大于 MAX；放大后图比屏幕大的那一维，边不能拖进来留白，比屏幕小的就居中 */
  const clamp = (v: View): View => {
    const f = fitRef.current;
    if (!f) return v;
    const s = Math.min(MAX, Math.max(1, v.s));
    const axis = (pos: number, size: number, start: number, screen: number) => {
      const len = size * s;
      if (len <= screen) return (screen - len) / 2 - start;
      return Math.min(-start, Math.max(screen - len - start, pos));
    };
    return { s, x: axis(v.x, f.w, f.left, window.innerWidth), y: axis(v.y, f.h, f.top, window.innerHeight) };
  };

  const close = () => {
    if (closing.current) return;
    closing.current = true;
    const f = fitRef.current;
    const el = origin && origin.index === index && origin.el.isConnected ? origin.el : null;
    const r = el?.getBoundingClientRect();
    // 缩略图还看得见才缩回去（滚出屏幕 / 被卸载了就原地淡出）
    if (f && r && r.width && r.bottom > 0 && r.top < window.innerHeight) apply(viewFor(f, r), 220);
    else if (imgRef.current) {
      imgRef.current.style.transition = `opacity 160ms ${EASE}`;
      imgRef.current.style.opacity = "0";
    }
    fade(0, 220);
    setTimeout(() => (emit(null), chatHost().refocus?.()), 220);
  };

  const go = (d: number) => {
    if (!many || closing.current) return;
    emit({ items, index: (index + d + items.length) % items.length, origin });
  };

  // 翻到另一张：重新量（新图的尺寸），回到适应屏幕
  const first = useRef(true);
  useLayoutEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    setNat(null);
    setZoomed(false);
    view.current = { s: 1, x: 0, y: 0 };
  }, [index]);

  // 打开的那一下：从缩略图的位置放大到适应屏幕。要等知道图的尺寸（fit）才能算
  useLayoutEffect(() => {
    if (!fit) return;
    if (!opening.current) {
      // 翻到的新图：原地淡入
      apply(view.current);
      imgRef.current?.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 150 });
      return;
    }
    opening.current = false;
    const r = origin && origin.index === index ? origin.el.getBoundingClientRect() : null;
    if (r && r.width) {
      apply(viewFor(fit, r));
      fade(0);
      imgRef.current?.getBoundingClientRect(); // 先把起点排出来，下面的过渡才有起点
      apply({ s: 1, x: 0, y: 0 }, 260);
      fade(1, 260);
    } else {
      apply({ s: 0.94, x: (fit.w * 0.06) / 2, y: (fit.h * 0.06) / 2 });
      fade(0);
      imgRef.current?.getBoundingClientRect();
      apply({ s: 1, x: 0, y: 0 }, 200);
      fade(1, 200);
    }
  }, [fit?.w, fit?.h, fit?.left, fit?.top]);

  // 转屏 / 改窗口大小：重新算适应屏幕的框
  useEffect(() => {
    const f = () => relayout((x) => x + 1);
    window.addEventListener("resize", f);
    return () => window.removeEventListener("resize", f);
  }, []);

  // 捕获阶段拦键：Esc 在输入框里是「打断 agent」，灯箱开着时只该关灯箱
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      const k = e.key;
      if (k !== "Escape" && k !== "ArrowLeft" && k !== "ArrowRight") return;
      // A right-click menu open over the image takes this Esc for itself (AppContextMenu); the lightbox stays
      if (document.querySelector("[data-app-context-menu]")) return;
      e.preventDefault();
      e.stopPropagation();
      if (k === "Escape") close();
      else go(k === "ArrowLeft" ? -1 : 1);
    };
    window.addEventListener("keydown", key, true);
    return () => window.removeEventListener("keydown", key, true);
  });

  // 滚轮 / 触控板捏合（浏览器报成 ctrl+wheel）：以指针为中心缩放。要 passive: false 才拦得住页面缩放
  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = rootRef.current;
    if (!el) return;
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      if (e.target instanceof HTMLVideoElement) return;
      const k = Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.002));
      zoomAt(view.current.s * k, e.clientX, e.clientY);
    };
    el.addEventListener("wheel", wheel, { passive: false });
    return () => el.removeEventListener("wheel", wheel);
  }, []);

  // ---- 指针手势：一个指头拖（放大时平移；没放大时左右翻 / 往下划关），两个指头捏合 ----
  const ptrs = useRef(new Map<number, { x: number; y: number }>());
  const g = useRef<{
    mode: "none" | "pan" | "pinch" | "swipe" | "dismiss";
    sx: number;
    sy: number;
    v: View;
    moved: boolean;
    onImg: boolean;
    dist?: number;
    c?: { x: number; y: number };
  }>({ mode: "none", sx: 0, sy: 0, v: { s: 1, x: 0, y: 0 }, moved: false, onImg: false });
  const lastTap = useRef<{ t: number; x: number; y: number } | null>(null);

  const startOne = (p: { x: number; y: number }, onImg: boolean, moved = false) => {
    g.current = { mode: view.current.s > 1.01 ? "pan" : "none", sx: p.x, sy: p.y, v: { ...view.current }, moved, onImg };
  };
  const startPinch = () => {
    const [a, b] = [...ptrs.current.values()];
    const f = fitRef.current;
    if (!f) return;
    const mx = (a.x + b.x) / 2;
    const my = (a.y + b.y) / 2;
    const v = view.current;
    g.current = {
      ...g.current,
      mode: "pinch",
      moved: true,
      v: { ...v },
      dist: Math.hypot(a.x - b.x, a.y - b.y),
      c: { x: (mx - f.left - v.x) / v.s, y: (my - f.top - v.y) / v.s },
    };
  };

  const onDown = (e: React.PointerEvent) => {
    // A video's own controls (play, seek, volume) get the pointer, not our gestures.
    if (closing.current || (e.target as HTMLElement).closest("button, a, video")) return;
    if (e.pointerType === "mouse" && e.button !== 0) return;
    ptrs.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    if (ptrs.current.size === 2) startPinch();
    else if (ptrs.current.size === 1) startOne({ x: e.clientX, y: e.clientY }, e.target === imgRef.current);
  };

  const onMove = (e: React.PointerEvent) => {
    if (!ptrs.current.has(e.pointerId)) return;
    ptrs.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const st = g.current;
    const f = fitRef.current;
    if (!f) return;
    if (st.mode === "pinch" && ptrs.current.size >= 2) {
      const [a, b] = [...ptrs.current.values()];
      const mx = (a.x + b.x) / 2;
      const my = (a.y + b.y) / 2;
      // 捏的过程中允许缩到比适应屏幕还小一点（有弹性），松手再弹回
      const s = Math.min(MAX * 1.2, Math.max(0.5, (st.v.s * Math.hypot(a.x - b.x, a.y - b.y)) / st.dist!));
      apply({ s, x: mx - f.left - st.c!.x * s, y: my - f.top - st.c!.y * s });
      return;
    }
    if (ptrs.current.size !== 1) return;
    const dx = e.clientX - st.sx;
    const dy = e.clientY - st.sy;
    if (!st.moved && Math.hypot(dx, dy) < 6) return;
    st.moved = true;
    if (st.mode === "none") st.mode = Math.abs(dx) > Math.abs(dy) ? (many ? "swipe" : "pan") : dy > 0 ? "dismiss" : "pan";
    if (st.mode === "pan") apply(view.current.s > 1.01 ? { ...st.v, x: st.v.x + dx, y: st.v.y + dy } : { ...st.v, y: st.v.y + dy * 0.3 });
    else if (st.mode === "swipe") apply({ ...st.v, x: st.v.x + dx });
    else if (st.mode === "dismiss") {
      const k = Math.max(0.6, 1 - Math.max(0, dy) / 1000);
      apply({ s: k, x: (f.w * (1 - k)) / 2 + dx, y: (f.h * (1 - k)) / 2 + dy });
      fade(Math.max(0.2, 1 - Math.max(0, dy) / 400));
    }
  };

  const onUp = (e: React.PointerEvent) => {
    if (!ptrs.current.delete(e.pointerId)) return;
    const st = g.current;
    const left = [...ptrs.current.values()];
    // 捏完抬起一个指头：剩下那个接着拖，不跳
    if (st.mode === "pinch" && left.length === 1) {
      startOne(left[0], false, true);
      return;
    }
    if (left.length) return;
    const dx = e.clientX - st.sx;
    const dy = e.clientY - st.sy;
    if (!st.moved && e.type !== "pointercancel") return tap(e, st.onImg);
    if (st.mode === "pinch" || st.mode === "pan") {
      apply(clamp(view.current), 220);
      setZoomed(view.current.s > 1.01);
      return;
    }
    if (st.mode === "swipe") {
      if (Math.abs(dx) > 60) go(dx < 0 ? 1 : -1);
      else apply({ s: 1, x: 0, y: 0 }, 200);
      return;
    }
    if (st.mode === "dismiss") {
      if (dy > 100) close();
      else (apply({ s: 1, x: 0, y: 0 }, 200), fade(1, 200));
      return;
    }
  };

  const tap = (e: React.PointerEvent, onImg: boolean) => {
    // 轻点：点空白处关；鼠标点图放大 / 还原；手指点图要等第二下（双击放大 / 还原）
    const now = Date.now();
    const dbl = lastTap.current && now - lastTap.current.t < 300 && Math.hypot(e.clientX - lastTap.current.x, e.clientY - lastTap.current.y) < 30;
    lastTap.current = dbl ? null : { t: now, x: e.clientX, y: e.clientY };
    const toggle = () =>
      view.current.s > 1.01
        ? (apply(clamp({ s: 1, x: 0, y: 0 }), 240), setZoomed(false))
        : zoomAt(e.pointerType === "mouse" ? 2 : 2.5, e.clientX, e.clientY, 240);
    if (e.pointerType === "mouse") onImg ? toggle() : close();
    else if (dbl) toggle();
    else if (!onImg && view.current.s <= 1.01) close();
  };

  const btn =
    "flex size-9 items-center justify-center rounded-full bg-background/70 text-foreground shadow-sm ring-1 ring-border backdrop-blur hover:bg-background";
  return (
    <div
      ref={rootRef}
      role="dialog"
      aria-label={t("lightbox.label")}
      className="fixed inset-0 z-[200] touch-none select-none"
      onPointerDown={onDown}
      onPointerMove={onMove}
      onPointerUp={onUp}
      onPointerCancel={onUp}
    >
      <div ref={bgRef} className="absolute inset-0 bg-background/90 backdrop-blur-sm" style={{ opacity: 0 }} />
      {it.video ? (
        <video
          key={`${index}:${it.url}`}
          ref={imgRef}
          src={it.url}
          controls
          autoPlay
          playsInline
          onLoadedMetadata={(e) => !nat && setNat({ w: e.currentTarget.videoWidth || 800, h: e.currentTarget.videoHeight || 450 })}
          className="absolute top-0 left-0 max-w-none rounded-md bg-black shadow-2xl will-change-transform"
          style={fit ? { left: fit.left, top: fit.top, width: fit.w, height: fit.h, transformOrigin: "0 0" } : { opacity: 0 }}
        />
      ) : (
        <img
          key={`${index}:${it.url}`}
          ref={imgRef}
          src={it.url}
          alt={it.alt}
          draggable={false}
          onLoad={(e) => !nat && setNat({ w: e.currentTarget.naturalWidth || 800, h: e.currentTarget.naturalHeight || 800 })}
          className={cn(
            "absolute top-0 left-0 max-w-none rounded-md shadow-2xl will-change-transform",
            zoomed ? "cursor-grab active:cursor-grabbing" : "cursor-zoom-in",
          )}
          style={fit ? { left: fit.left, top: fit.top, width: fit.w, height: fit.h, transformOrigin: "0 0" } : { opacity: 0 }}
        />
      )}
      <div ref={chromeRef} style={{ opacity: 0 }}>
        <div className="absolute top-3 right-3 flex gap-2">
          {it.path && chatHost().openPath && (
            <button
              className={btn}
              title={t("lightbox.openInViewer")}
              onClick={() => {
                emit(null);
                chatHost().openPath?.(it.path!, it.cwd);
              }}
            >
              <FileSearch className="size-4" />
            </button>
          )}
          <a className={btn} title={t("lightbox.download")} href={downloadURL(it.url)} download={downloadName(it)}>
            <Download className="size-4" />
          </a>
          <button className={btn} title={t("lightbox.close")} onClick={close}>
            <X className="size-4" />
          </button>
        </div>
        {many && (
          <>
            <button className={cn(btn, "absolute top-1/2 left-3 -translate-y-1/2 max-md:hidden")} title={t("lightbox.prev")} onClick={() => go(-1)}>
              <ChevronLeft className="size-5" />
            </button>
            <button className={cn(btn, "absolute top-1/2 right-3 -translate-y-1/2 max-md:hidden")} title={t("lightbox.next")} onClick={() => go(1)}>
              <ChevronRight className="size-5" />
            </button>
          </>
        )}
        {(many || it.alt) && !zoomed && (
          <div className={cn("pointer-events-none absolute inset-x-0 flex justify-center px-4", it.video ? "top-4" : "bottom-4")}>
            <span className="max-w-full truncate rounded-full bg-background/70 px-3 py-1 text-[12px] text-muted-foreground ring-1 ring-border backdrop-blur">
              {many && (
                <span className="tabular-nums">
                  {index + 1} / {items.length}
                </span>
              )}
              {many && it.alt && " · "}
              {it.alt}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
