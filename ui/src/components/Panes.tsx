import {
  Bot,
  Columns2,
  Copy,
  CopyPlus,
  ExternalLink,
  FileText,
  FolderOpen,
  GitFork,
  GitBranch,
  Globe,
  ListPlus,
  ListX,
  Maximize2,
  Monitor,
  RefreshCw,
  Smartphone,
  Tablet,
  Check,
  Minimize2,
  Minus,
  MoreHorizontal,
  PanelTopOpen,
  Pencil,
  PictureInPicture2,
  Plus,
  Rows2,
  Server,
  SquareTerminal,
  ChevronDown,
  CircleX,
  X,
} from "lucide-react";
import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { copyText } from "@/lib/clipboard";
import { Button } from "@/components/ui/button";
import { api, type FileStat } from "@/lib/api";
import { FileActions, FileContent } from "./FileViewer";
import { Folder } from "./folder/Folder";
import { BrowserScreen } from "./browser/BrowserScreen";
import { BrowserOutCard, PopOutItem } from "./browser/BrowserOut";
import { NewsDot } from "./browser/NewsDot";
import { codeMenuItems } from "@/lib/codeMenu";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import {
  basename,
  closeFocusedPane,
  closeSession,
  duplicateFocused,
  focusPane,
  focusSession,
  openArtifacts,
  openBrowserPane,
  openTerminal,
  paneToTab,
  reopenTerminal,
  runAction,
  splitFocused,
  toggleFloatPane,
  minimizePane,
  toggleZoom,
  setBrowserFit,
  fitPaneWidth,
  useBrowserWindows,
  useBrowserTabs,
  useBrowserNews,
} from "@/lib/actions";
import {
  dockedRoot,
  FLOAT_FITS,
  fitOf,
  floatable,
  floating,
  leaves,
  phoneRoot,
  setFloatBox,
  setFloatFit,
  FLOAT_OPERABLE,
  type FloatEdge as Edge,
  type FloatFit,
  setRatio,
  updateTab,
  type FloatBox,
  type PaneLeaf,
  type PaneNode,
  type PaneSplit,
  type Tab,
} from "@/lib/layout";
import { applyPaneTitle, paneSnapshot, tabSnapshot } from "@/lib/addons";
import { AddonSlot } from "./AddonSlot";
import { useWindowInFront } from "@/lib/front";
import { get, markRead, set, setLayout, useStore } from "@/lib/store";
import { activeIds, addToActive, removeActive } from "@/lib/overview";
import { usePhone } from "@/lib/phone";
import { useCaughtUp } from "@/lib/catchup";
import { cn } from "@/lib/utils";
import { FloatEdges } from "@/components/FloatEdges";
import { FadeText } from "./FadeText";
import { ActionKeys, keyText } from "./KeyCaps";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { HostTag } from "./HostTag";
import { ChatView } from "./chat/ChatView";
import { NewSession } from "./NewSession";
import { PixelLoader } from "./chat/PixelLoader";
import { startPaneDrag, useDrag, type TabGhost } from "./TabDrag";
import { TerminalView } from "./Terminal";
import { BOX_TRANSITION, useZoomAnim } from "@/lib/zoomAnim";
import { t } from "@/lib/i18n";

/** 这个 tab 是不是正显示着的那个（藏着的 tab 里的 pane 不算有焦点，不抢光标、不标已读）。 */
export const TabActive = createContext(true);

type Box = { x: number; y: number; w: number; h: number };
const FULL: Box = { x: 0, y: 0, w: 1, h: 1 };
const pct = (v: number) => `${v * 100}%`;

/** 把分屏树摊平：每个 pane 占整个 tab 的哪一块（比例），每条缝在哪。 */
function place(n: PaneNode, b: Box, panes: { pane: PaneLeaf; box: Box }[], seams: { split: PaneSplit; box: Box }[]) {
  if (n.type === "pane") {
    panes.push({ pane: n, box: b });
    return;
  }
  seams.push({ split: n, box: b });
  const r = n.ratio;
  if (n.dir === "row") {
    place(n.a, { ...b, w: b.w * r }, panes, seams);
    place(n.b, { ...b, x: b.x + b.w * r, w: b.w * (1 - r) }, panes, seams);
  } else {
    place(n.a, { ...b, h: b.h * r }, panes, seams);
    place(n.b, { ...b, y: b.y + b.h * r, h: b.h * (1 - r) }, panes, seams);
  }
}

/**
 * 所有 pane 平铺在同一层、按 id 做 key，用绝对定位摆到各自的位置 —— 不按分屏树一层层嵌套着画。
 * 嵌套着画的话，分屏会把原来的 pane 包进一个新的 split 节点、关 pane 会把兄弟提上一层，React 认为它换了位置，
 * 整个卸载重挂：对话重新加载、滚动位置丢掉、终端重连（用户报的「新建 pane 之前的 pane 会重新加载」）。
 * 放大也一样：别的 pane 只是藏起来（invisible，大小不变），还原时不重新加载。
 */
export function TabView({ tab }: { tab: Tab }) {
  const box = useRef<HTMLDivElement>(null);
  const phone = usePhone();
  const all = leaves(tab.root);
  // 手机上：上下分屏照画，左右分屏只画焦点那一边（见 phoneRoot）。不管布局里的 zoom ——
  // 布局是存服务端、桌面那边共用的，手机上看一眼不该把电脑上的分屏变成放大。看不见的 pane 走面板一览，焦点那个标题栏上标「第几个 / 共几个」。
  // Floating panes (picture-in-picture, layout.toggleFloat) are laid out apart, over the split; not on a phone, which
  // shows one pane at a time anyway
  const docked = phone ? null : dockedRoot(tab);
  const floats = docked ? floating(tab) : [];
  // minimized ones are chips in the tab's corner (MinChips), their box not drawn: the stream closes with it
  const open = floats.filter((p) => !tab.float![p.id].min);
  const mins = floats.filter((p) => tab.float![p.id].min);
  const root = phone ? phoneRoot(tab) : (docked ?? tab.root);
  const panes: { pane: PaneLeaf; box: Box }[] = [];
  const seams: { split: PaneSplit; box: Box }[] = [];
  place(root, FULL, panes, seams);
  const zoom = !phone && tab.zoom && panes.some((p) => p.pane.id === tab.zoom) ? tab.zoom : undefined;
  // 分屏时和 herdr 一样：每个 pane 一圈边框、中间留缝，焦点那个边框高亮。只有一个 pane（或放大着）时不画框（省地方）。
  const anim = useZoomAnim(zoom, phone);
  // Frames stay on while a zoom animates (the growing pane looks like a window sliding over its neighbours) and drop at the end.
  // Only the docked panes count: one docked pane under floating ones is drawn as a tab's only pane (asked for: framed,
  // it looked split while nothing was beside it). A floating box draws its own frame (FloatPane).
  const framed = panes.length > 1 && (!zoom || !!anim);
  // The animating pane's body (chat / terminal) is pinned to the full tab size (the size at the zoomed end) for the whole
  // transition, so only its clipping box moves: the chat list / xterm see one resize at the zoomed end instead of one per
  // frame (re-measuring every row or refitting the terminal each frame is what made a naive width/height transition stutter).
  // Only the body (PaneBody): the frame and title bar follow the moving box, or the right / bottom border and the title
  // bar's buttons would sit outside it, clipped, until the end. Minus the 3px frame padding on each side.
  const pin = anim && box.current ? { width: box.current.clientWidth - 6, height: box.current.clientHeight - 6 } : undefined;
  let count: (Count & { pane: string }) | undefined;
  if (phone && panes.length < all.length) {
    const p = panes.find((x) => x.pane.id === tab.focus)?.pane ?? panes[0].pane;
    const i = all.indexOf(p);
    count = { i: i + 1, n: all.length, next: all[(i + 1) % all.length].id, pane: p.id };
  }

  const startDrag = (e: React.PointerEvent, split: PaneSplit, b: Box) => {
    e.preventDefault();
    const el = box.current;
    if (!el) return;
    const c = el.getBoundingClientRect();
    const row = split.dir === "row";
    const r = { left: c.left + b.x * c.width, top: c.top + b.y * c.height, width: b.w * c.width, height: b.h * c.height };
    // At most one layout update a frame: each re-renders every pane of the tab, and a phone sends several moves a frame
    let ratio = NaN;
    let raf = 0;
    const apply = () => {
      raf = 0;
      setLayout((l) => updateTab(l, tab.id, (t) => setRatio(t, split.id, ratio)));
    };
    const move = (ev: PointerEvent) => {
      ratio = row ? (ev.clientX - r.left) / r.width : (ev.clientY - r.top) / r.height;
      raf ||= requestAnimationFrame(apply);
    };
    const up = () => {
      if (raf) (cancelAnimationFrame(raf), apply());
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
      document.body.style.cursor = "";
    };
    document.body.style.cursor = row ? "col-resize" : "row-resize";
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
  };

  // Outer 3px + each pane's own 3px: 6px to the edges and 6px between panes, the same inset as the sidebar's rows
  // and the tab bar's tabs, so the cards line up with both across the divider
  return (
    <div className={cn("h-full min-h-0", framed && "p-[3px]")}>
      <div ref={box} className="relative h-full w-full">
        {panes.map(({ pane, box: b }) => {
          const z = zoom === pane.id;
          const at = z ? FULL : b;
          const moving = anim === pane.id;
          return (
            <div
              key={pane.id}
              // The transition is an inline style, not a class: browser Tailwind generates a class's CSS only after it first shows up
              // in the DOM, which is one style change too late — the first zoom of a page load would jump instead of animate.
              style={{
                left: pct(at.x),
                top: pct(at.y),
                width: pct(at.w),
                height: pct(at.h),
                transition: moving ? BOX_TRANSITION : undefined,
              }}
              className={cn(
                "absolute min-h-0 min-w-0 overflow-hidden",
                framed && "p-[3px]",
                // Neighbours stay put under the moving pane and are only hidden once it covers them.
                zoom && !z && !anim && "invisible",
                moving ? "z-20" : zoom && z && "z-10",
              )}
            >
              {/* Opaque while moving: it slides over its neighbours, which panes don't otherwise need to cover */}
              <div className={cn("h-full w-full", moving && "rounded-md bg-background")}>
                <PanePin.Provider value={moving ? pin : undefined}>
                  <PaneView pane={pane} tab={tab} framed={framed} zoomed={z || undefined} count={count?.pane === pane.id ? count : undefined} />
                </PanePin.Provider>
              </div>
            </div>
          );
        })}
        {!zoom &&
          seams.map(({ split, box: b }) => {
            const row = split.dir === "row";
            const style = row
              ? { left: pct(b.x + b.w * split.ratio), top: pct(b.y), height: pct(b.h) }
              : { top: pct(b.y + b.h * split.ratio), left: pct(b.x), width: pct(b.w) };
            return (
              <div
                key={split.id}
                onPointerDown={(e) => startDrag(e, split, b)}
                style={style}
                className={cn(
                  // 缝本身透明（两边各有边框），悬停时亮一条提示能拖
                  "absolute z-10 bg-transparent bg-clip-content transition-colors hover:bg-brand/50",
                  row ? "w-1.5 -translate-x-1/2 cursor-col-resize px-[2px]" : "h-1.5 -translate-y-1/2 cursor-row-resize py-[2px]",
                  // touch-none: a fast touch drag otherwise turns into a page pan (pointercancel) and the seam stops following.
                  // Fingers can't hit 6px: wider for touch, and taller on a phone (only top / bottom splits are drawn there)
                  "touch-none",
                  row && "pointer-coarse:w-5 pointer-coarse:px-[9px]",
                  phone && "h-3 py-[5px]",
                )}
              />
            );
          })}
        {open.map((pane) => (
          <FloatPane key={pane.id} pane={pane} tab={tab} box={tab.float![pane.id]} area={box} />
        ))}
        {mins.length > 0 && <MinChips panes={mins} />}
      </div>
    </div>
  );
}

/**
 * Minimized Browser panes (layout.minimizeFloat): a chip each at the tab's top right, just under the panes' title bars
 * (at the bottom it covered the composer's send button); a click puts it back where it was (layout.restoreFloat).
 */
function MinChips({ panes }: { panes: PaneLeaf[] }) {
  return (
    <div className="absolute top-12 right-3 z-30 flex max-w-[60%] flex-wrap justify-end gap-1.5">
      {panes.map((p) => (
        <MinChip key={p.id} pane={p} />
      ))}
    </div>
  );
}

function MinChip({ pane }: { pane: PaneLeaf }) {
  const b = pane.browser!;
  const url = useBrowserTabs((s) => s.tabs.find((x) => x.tab === b.tab)?.url) ?? b.url;
  // its page opened or went elsewhere since the user last looked (lib/actions/browser.ts useBrowserNews)
  const news = useBrowserNews((s) => !!s[pane.id]);
  let name = t("browser.tag"); // no page yet: the short word, as the sidebar tag (asked for)
  try {
    if (url) name = new URL(url).host || url;
  } catch {
    name = url!;
  }
  return (
    <button
      type="button"
      title={t("panes.restoreMin", { name })}
      onClick={() => minimizePane(pane.id, false)}
      className="relative flex h-8 max-w-56 items-center gap-1.5 rounded-full border bg-background px-3 text-xs shadow-lg hover:bg-accent"
    >
      <Globe className="size-3.5 shrink-0 text-brand" />
      <span className="truncate">{name}</span>
      {news && <NewsDot />}
    </button>
  );
}

/** A floating Browser pane's picture reached its fit's shape (BrowserScreen onFitDrawn): FloatPane switches its box then. */
const FloatFitDrawn = createContext<((fit: FloatFit) => void) | undefined>(undefined);
/** A floating Browser pane shrunk below FLOAT_OPERABLE of its fit's largest size: its page is only looked at. */
const FloatViewOnly = createContext(false);

/** The title bar's drag of a floating pane: it moves the pane inside its tab instead of moving it elsewhere (usePaneDrag). */
const FloatMove = createContext<((e: React.PointerEvent<HTMLElement>) => void) | undefined>(undefined);

/**
 * A pane floating over the split of its tab (picture-in-picture): dragged by its title bar, resized from its edges,
 * kept inside the tab. Its box is stored as fractions of the tab (Tab.float), so it follows the window's size.
 */
function FloatPane({ pane, tab, box: fb, area }: { pane: PaneLeaf; tab: Tab; box: FloatBox; area: React.RefObject<HTMLDivElement | null> }) {
  const focused = tab.focus === pane.id;
  // The tab's size in pixels: the box keeps its page's shape (layout.FLOAT_FITS), so its height comes from its width.
  // Measured on a layer of its own over the tab, not on `area`: mounted with its tab (a tab switched back to after it
  // was unmounted), this effect runs before the parent's ref is set, area.current was null, nothing was measured and
  // the box never showed again (user report)
  const meter = useRef<HTMLDivElement>(null);
  // top: the docked panes' title bars along the tab's top, which the box stays below (it covered them, user report)
  const [size, setSize] = useState<{ w: number; h: number; top: number } | null>(null);
  useLayoutEffect(() => {
    const el = meter.current;
    if (!el) return;
    const measure = () => {
      const r = el.getBoundingClientRect();
      // the first pane in the tab's DOM is a docked one (floating ones come after the split)
      const head = el.parentElement?.querySelector("[data-pane-id]")?.firstElementChild?.getBoundingClientRect();
      const top = head && head.top - r.top < 8 && head.height < 80 ? Math.max(0, Math.round(head.bottom - r.top)) : 0;
      setSize({ w: el.clientWidth, h: el.clientHeight, top });
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  // Another size picked: the box keeps the old one's shape until the page's first picture at the new one is drawn
  // (or a moment has passed), then both change together; switched at once, the old picture showed in the new box
  // for a few frames (user report)
  const cur = fitOf(fb);
  const [drawn, setDrawn] = useState(cur);
  useEffect(() => {
    if (drawn === cur) return;
    const id = setTimeout(() => setDrawn(cur), 1500);
    return () => clearTimeout(id);
  }, [cur, drawn]);
  const onFitDrawn = (f: FloatFit) => f === cur && setDrawn(f);
  const fit = FLOAT_FITS[cur];
  const ratio = fit.w / fit.h;
  const [pwMin, pwDef, pwMax] = fit.pw;
  const span = { min: pwMin + FLOAT_CHROME_W, max: pwMax + FLOAT_CHROME_W };
  const fresh = size && floatGeom(fb.x * size.w, fb.y * size.h - size.top, (fb.pw ?? pwDef) + FLOAT_CHROME_W, ratio, { w: size.w, h: size.h - size.top }, span);
  if (fresh && size) fresh.top += size.top;
  const lastGeom = useRef(fresh);
  const geom = drawn === cur || !lastGeom.current ? fresh : lastGeom.current;
  if (drawn === cur) lastGeom.current = fresh;
  // One gesture: pointer moves turn into box changes, at most one layout update a frame (as the seams do)
  const gesture = (e: React.PointerEvent, cursor: string, next: (dx: number, dy: number) => FloatBox, end?: () => void) => {
    const el = area.current;
    if (!el || e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    const c = el.getBoundingClientRect();
    const x0 = e.clientX;
    const y0 = e.clientY;
    let to: FloatBox | null = null;
    let raf = 0;
    const apply = () => {
      raf = 0;
      if (to) setLayout((l) => updateTab(l, tab.id, (t) => setFloatBox(t, pane.id, to!)));
    };
    const move = (ev: PointerEvent) => {
      to = next((ev.clientX - x0) / c.width, (ev.clientY - y0) / c.height);
      raf ||= requestAnimationFrame(apply);
    };
    const up = () => {
      if (raf) (cancelAnimationFrame(raf), apply());
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
      end?.();
    };
    document.body.style.cursor = cursor;
    document.body.style.userSelect = "none";
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
  };
  const onMove = (e: React.PointerEvent<HTMLElement>) => {
    // The title bar's own buttons (⋯, close…) keep their click
    if ((e.target as Element).closest("button, a, input, [role=menuitem]")) return;
    if (!focused) focusPane(pane.id);
    // from where it is drawn (kept inside the tab), with its drawn height stored too (the backend's pane rect reads it)
    if (!size || !geom) return;
    const x = geom.left / size.w,
      y = geom.top / size.h;
    gesture(e, "grabbing", (dx, dy) => ({ ...fb, x: x + dx, y: y + dy, w: geom.w / size.w, h: geom.h / size.h, pw: geom.w - FLOAT_CHROME_W }));
  };
  // Any edge resizes the box at its shape: a side's move is the width's, a top / bottom's turns into the width that
  // gives that height. The edges across from the one held stay put.
  // Shrunk past its smallest size and still pulled in: a hint over the box, and letting go minimizes it (asked for)
  const [toMin, setToMin] = useState(false);
  const onResize = (e: React.PointerEvent, g: Edge, cursor: string) => {
    if (!size || !geom) return;
    let min = false;
    const end = () => {
      setToMin(false);
      if (min) minimizePane(pane.id);
    };
    gesture(
      e,
      cursor,
      (dx, dy) => {
        const px = dx * size.w,
          py = dy * size.h;
        const dw = g.l ? -px : g.r ? px : (g.t ? -py : py) * ratio;
        const right = geom.left + geom.w,
          bottom = geom.top + geom.h;
        // the room on the side that moves
        const roomW = g.l ? right : size.w - geom.left;
        const roomH = g.t ? bottom - size.top : size.h - geom.top;
        const next = floatGeom(0, 0, geom.w + dw, ratio, { w: roomW, h: roomH }, span);
        const over = geom.w + dw < span.min - SHRINK_TO_MIN;
        if (over !== min) setToMin((min = over));
        const left = g.l ? right - next.w : geom.left;
        const top = g.t ? bottom - next.h : geom.top;
        return { ...fb, x: left / size.w, y: top / size.h, w: next.w / size.w, h: next.h / size.h, pw: next.w - FLOAT_CHROME_W };
      },
      end,
    );
  };
  const meterEl = <div ref={meter} aria-hidden className="pointer-events-none invisible absolute inset-0" />;
  if (!geom) return meterEl;
  return (
    <>
      {meterEl}
      <div
        style={{ left: geom.left, top: geom.top, width: geom.w, height: geom.h }}
        className={cn("absolute min-h-0 min-w-0 p-[3px]", focused ? "z-40" : "z-30")}
      >
        <div className="h-full w-full rounded-md bg-background shadow-2xl">
          <FloatMove.Provider value={onMove}>
            <FloatFitDrawn.Provider value={onFitDrawn}>
              <FloatViewOnly.Provider value={(geom.w - FLOAT_CHROME_W) / pwMax < FLOAT_OPERABLE}>
                <PaneView pane={pane} tab={tab} framed />
              </FloatViewOnly.Provider>
            </FloatFitDrawn.Provider>
          </FloatMove.Provider>
        </div>
        {toMin && (
          <div className="pointer-events-none absolute inset-[3px] z-10 flex flex-col items-center justify-center gap-1.5 rounded-md bg-background/95 text-sm font-medium">
            <Minus className="size-5 text-brand" />
            {t("panes.releaseToMin")}
          </div>
        )}
        <FloatEdges onDown={onResize} />
      </div>
    </>
  );
}

// How far (px) past its smallest width a floating box has to be pulled before letting go minimizes it: a little
// overshoot at the smallest size is just the drag ending there
const SHRINK_TO_MIN = 40;

// Around a floating pane's page: its wrapper's 3px padding and the pane's 1px border each side, and the title bar
// (h-8) above. The page's own box is what has the shape.
const FLOAT_CHROME_W = 8;
const FLOAT_CHROME_H = 40;

/** A floating box at width w and the page's shape (ratio = width / height), fitted into room and placed inside it. */
function floatGeom(x: number, y: number, w: number, ratio: number, room: { w: number; h: number }, span: { min: number; max: number }) {
  const hOf = (w: number) => (w - FLOAT_CHROME_W) / ratio + FLOAT_CHROME_H;
  const wOf = (h: number) => (h - FLOAT_CHROME_H) * ratio + FLOAT_CHROME_W;
  // the fit's range first, then the room there is (a small window wins over the smallest size)
  w = Math.min(Math.max(span.min, Math.min(w, span.max)), room.w, wOf(room.h));
  const h = Math.min(hOf(w), room.h);
  return { left: Math.min(Math.max(0, x), room.w - w), top: Math.min(Math.max(0, y), room.h - h), w, h };
}

const FIT_ICONS: Record<FloatFit, typeof Monitor> = { pc: Monitor, pad: Tablet, mobile: Smartphone };

/**
 * The pane floats as it is drawn here. The layout's float entry is the desktop's: a phone shows one pane at a time and
 * draws a floating one like any other (TabView), so there it gets a docked pane's menu and page size. Read from the
 * layout alone, a floated Browser pane on the phone offered only the floating sizes, which a phone's own layout wins
 * over (chromeext phone.go): picking one never showed, the page stayed at the phone's size (user report).
 */
function useFloats(tab: Tab, paneId: string) {
  return !usePhone() && !!tab.float?.[paneId];
}

/** A floating Browser pane's page size: a desktop, a tablet or a phone (layout.FLOAT_FITS). */
function FitItems({ pane, tab }: { pane: PaneLeaf; tab: Tab }) {
  const b = tab.float?.[pane.id];
  if (!useFloats(tab, pane.id) || !b || !pane.browser) return null;
  const cur = fitOf(b);
  return (
    <>
      <DropdownMenuSeparator />
      {(Object.keys(FLOAT_FITS) as FloatFit[]).map((f) => {
        const Icon = FIT_ICONS[f];
        return (
          <DropdownMenuItem key={f} onClick={() => setLayout((l) => updateTab(l, tab.id, (x) => setFloatFit(x, pane.id, f)))}>
            <Icon /> {t(`panes.fit.${f}`)}
            <span className="ml-auto text-[11px] text-muted-foreground">
              {FLOAT_FITS[f].w}×{FLOAT_FITS[f].h}
            </span>
            {f === cur ? <Check /> : <span className="size-4" />}
          </DropdownMenuItem>
        );
      })}
      <DropdownMenuSeparator />
    </>
  );
}

/** `next`: the pane a tap on the counter switches to (the following one, wrapping round to the first). */
type Count = { i: number; n: number; next: string };

/**
 * 标题栏按住拖：把这个 pane 挪到别处（见 TabDrag.tsx 的 startPaneDrag）。手机上一次只看一个 pane，不给拖。
 * 被拖着的那个 pane 淡下去，和 tab 栏里被拖的 tab 一样。
 */
/**
 * A lone pane's title bar sits right beside the desktop sidebar's header (no tab bar above it then), so it takes that
 * header's height and the two bottom borders line up. Split panes are framed and keep the short bar.
 */
function usePaneHeadHeight(framed: boolean) {
  const phone = usePhone();
  const besideSidebar = useStore((s) => s.sidebar);
  return !framed && besideSidebar && !phone ? "h-10" : "h-8";
}

function usePaneDrag(paneId: string, ghost: TabGhost) {
  const phone = usePhone();
  const dragging = useDrag()?.paneId === paneId;
  const floatMove = useContext(FloatMove);
  if (floatMove) return { dragging: false, onPointerDown: floatMove };
  return { dragging, onPointerDown: phone ? undefined : (e: React.PointerEvent<HTMLElement>) => startPaneDrag(e, paneId, ghost) };
}

function PaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  if (pane.sub) return <SubPaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
  if (pane.term) return <TermPaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
  if (pane.file?.folder) return <FolderPaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
  if (pane.file) return <FilePaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
  if (pane.browser) return <BrowserPaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
  return <HeldSessionPane pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
}

/** Size of the whole pane at the end of a zoom animation in progress (TabView), for PaneBody. */
const PanePin = createContext<{ width: number; height: number } | undefined>(undefined);

/**
 * A pane's content under its title bar. While the pane zooms it's laid out at its final size (clipped by the moving box),
 * so the chat / terminal re-lays out once instead of every frame: minus the 1px frame on each side and the 32px + 1px title bar.
 */
function PaneBody({ children }: { children: React.ReactNode }) {
  const pin = useContext(PanePin);
  return (
    <div className="min-h-0 flex-1 overflow-clip">
      <div className="h-full w-full" style={pin && { width: pin.width - 2, height: pin.height - 35 }}>
        {children}
      </div>
    </div>
  );
}

/** On a phone, how many panes this tab has and which one you're on: the others are off screen, something has to say
 * they're there. A tap cycles to the next one (asked for: one tap beats opening the drawer and picking). It sits
 * right before the ⋯ (or the close button where there is no ⋯) in every kind of pane, so repeated taps cycling through mixed
 * panes stay on one spot. */
function PaneCount({ count }: { count?: Count }) {
  if (!count) return null;
  return (
    <button
      className="shrink-0 rounded-sm border px-1.5 py-px font-mono text-[10px] text-muted-foreground"
      title={t("panes.count")}
      onClick={() => focusPane(count.next)}
    >
      {count.i}/{count.n}
    </button>
  );
}

/** The phone's single-pane title bar: the agent's directory, cut from the front when it doesn't fit (its last segments
 * are the ones that say where), and its branch, which gives way only past half the bar. */
function PhonePath({ path, branch, home, agent }: { path: string; branch?: string; home: string; agent: string }) {
  const shown = home && (path === home || path.startsWith(home + "/")) ? "~" + path.slice(home.length) : path;
  return (
    <>
      {/* The agent icon stays: under ☰, so the path lines up with the top bar's title as the pane title used to.
          Plain 12px text in the body text colour, not the desktop's small grey mono: alone in the bar that read like a
          footnote (asked for) */}
      <AgentIcon agent={agent} className="size-4" />
      <FadeText start className="min-w-0 shrink text-foreground" title={path}>
        {shown}
      </FadeText>
      {branch && (
        <span className="flex max-w-[45%] min-w-0 shrink-0 items-center gap-1 text-foreground">
          <GitBranch className="size-3.5 shrink-0" />
          <span className="truncate">{branch}</span>
        </span>
      )}
    </>
  );
}

/** 子 agent 的只读面板：标题栏 + 只读的对话。 */
function SubPaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const sub = pane.sub!;
  const session = useStore((s) => s.sessions[sub.sessionId]);
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;
  const headH = usePaneHeadHeight(framed);
  const task = session?.tasks?.find((t) => t.toolCallId === sub.toolCallId);
  const title = task?.description || sub.title || t("panes.subAgent");
  const drag = usePaneDrag(pane.id, { label: title });
  return (
    <div
      data-pane-id={pane.id}
      className={cn(
        "flex h-full min-h-0 flex-col",
        framed && "overflow-hidden rounded-md border",
        framed && (focused ? "border-brand" : "border-border"),
        drag.dragging && "opacity-40",
      )}
      onMouseDown={() => !focused && focusPane(pane.id)}
    >
      {/* 手机上和顶栏两头对齐：左 16px，16px 图标的中线落在 24px（= ☰：px-1.5 + 36px 按钮）；右边照旧 12px，24px 的 ⋯ 按钮中线也落在 24px（= 右上角 +）。三种 pane 的标题栏同一个边距 */}
      <div
        onPointerDown={drag.onPointerDown}
        className={cn("flex shrink-0 items-center gap-2 border-b px-3 text-[12px] max-md:pl-4", headH, !focused && many && "opacity-70")}
      >
        <Bot className="size-4 shrink-0 text-muted-foreground" />
        <FadeText className="min-w-0 shrink-[2] text-muted-foreground">{task?.subagent || t("panes.subAgent")}</FadeText>
        <FadeText className="min-w-0 flex-1 font-medium">{title}</FadeText>
        {task?.status === "running" && <StatusDot status="running" />}
        {/* Shrinks too (truncated), so in a narrow pane the close button still fits */}
        {session && (
          <button className="min-w-0 max-w-[40%] truncate text-[11px] text-muted-foreground hover:text-foreground" onClick={() => focusSession(session.id)}>
            ← {session.title || basename(session.cwd)}
          </button>
        )}
        <PaneCount count={count} />
        <Button variant="ghost" size="icon" className="size-6 shrink-0" title={t("common.close")} onClick={() => (focusPane(pane.id), closeFocusedPane())}>
          <X className="size-3.5" />
        </Button>
      </div>
      <PaneBody>
        {session ? (
          <ChatView key={pane.id} session={session} paneId={pane.id} focused={focused} sub={sub.toolCallId} />
        ) : (
          <div className="py-16 text-center text-[13px] text-muted-foreground">{t("panes.sessionGone")}</div>
        )}
      </PaneBody>
    </div>
  );
}

/**
 * A file opened beside the chat (⌥-click on a path): title bar + the same view as the file viewer dialog. Stats the
 * path when it shows and on ↻, so a file the agent rewrote since is read again.
 */
function FilePaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const path = pane.file!.path;
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;
  const headH = usePaneHeadHeight(framed);
  const [stat, setStat] = useState<FileStat | null>(null);
  const [err, setErr] = useState("");
  const [rev, setRev] = useState(0);
  useEffect(() => {
    let live = true;
    setErr("");
    api
      .fileStat(path, undefined, pane.file!.host)
      .then((s) => live && setStat(s))
      .catch((e) => live && (setStat(null), setErr(e.message ?? String(e))));
    return () => {
      live = false;
    };
  }, [path, pane.file!.host, rev]);
  const drag = usePaneDrag(pane.id, { label: basename(path) });
  return (
    <div
      data-pane-id={pane.id}
      className={cn(
        "flex h-full min-h-0 flex-col",
        framed && "overflow-hidden rounded-md border",
        framed && (focused ? "border-brand" : "border-border"),
        drag.dragging && "opacity-40",
      )}
      onMouseDown={() => !focused && focusPane(pane.id)}
    >
      <div
        onPointerDown={drag.onPointerDown}
        className={cn("flex shrink-0 items-center gap-2 border-b px-3 text-[12px] max-md:pl-4", headH, !focused && many && "opacity-70")}
      >
        <FileText className="size-4 shrink-0 text-muted-foreground" />
        <FadeText className="min-w-[3em] max-w-[60%] shrink font-medium max-md:max-w-none">{basename(path)}</FadeText>
        <span className="hidden min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground md:block" title={path}>
          {path}
        </span>
        <div className="flex-1 md:hidden" />
        <Button variant="ghost" size="icon" className="size-6 shrink-0" title={t("panes.reread")} onClick={() => setRev((r) => r + 1)}>
          <RefreshCw className="size-3.5" />
        </Button>
        {stat && <FileActions stat={stat} small />}
        <PaneCount count={count} />
        <Button variant="ghost" size="icon" className="size-6 shrink-0" title={t("common.close")} onClick={() => (focusPane(pane.id), closeFocusedPane())}>
          <X className="size-3.5" />
        </Button>
      </div>
      <PaneBody>
        <div className="h-full overflow-auto overscroll-contain">
          {stat ? (
            <FileContent key={`${stat.mtime}-${stat.size}`} stat={stat} fill />
          ) : (
            <div className="py-16 text-center text-[13px] text-muted-foreground">{err || t("panes.reading")}</div>
          )}
        </div>
      </PaneBody>
    </div>
  );
}

/** A page of the project's own browser beside the chat (components/browser/, docs/dev/BROWSER-PANE.md). */
function BrowserPaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const b = pane.browser!;
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;
  const headH = usePaneHeadHeight(framed);
  const fitDrawn = useContext(FloatFitDrawn);
  const viewOnly = useContext(FloatViewOnly);
  const drag = usePaneDrag(pane.id, { label: t("browser.empty") });
  // the page moved to a window of its own (BrowserOut): this pane holds its card meanwhile, and stays, since a Browser
  // pane closed closes its page (server/browserpanes.go)
  const out = useBrowserWindows((w) => !!b.tab && w.tabs.includes(b.tab));
  const canPopOut = useBrowserWindows((w) => w.known);
  const url = useBrowserTabs((s) => s.tabs.find((x) => x.tab === b.tab)?.url) ?? b.url;
  const floats = useFloats(tab, pane.id);
  const zoomBtn = zoomed && (
    <Button
      variant="ghost"
      size="icon"
      className="size-6 shrink-0"
      onClick={toggleZoom}
      title={keyText("zoom") ? t("panes.restoreKey", { key: keyText("zoom") }) : t("panes.restore")}
    >
      <Minimize2 className="size-3.5" />
    </Button>
  );
  return (
    <div
      data-pane-id={pane.id}
      className={cn(
        "flex h-full min-h-0 flex-col",
        framed && "overflow-hidden rounded-md border",
        framed && (focused ? "border-brand" : "border-border"),
        // floating too small to use: the page only shows (FloatViewOnly), and the border says so
        viewOnly && "border-dashed",
        drag.dragging && "opacity-40",
      )}
      onMouseDown={() => !focused && focusPane(pane.id)}
    >
      {out && b.tab ? (
        <BrowserOutCard
          project={b.project}
          tab={b.tab}
          url={url}
          headClassName={cn(headH, !focused && many && "opacity-70")}
          onHeadPointerDown={drag.onPointerDown}
          Body={PaneBody}
          end={
            <>
              {zoomBtn}
              <PaneCount count={count} />
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="ghost" size="icon" className="size-6 shrink-0">
                    <MoreHorizontal className="size-3.5" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <PaneMoveItems pane={pane} tab={tab} zoomed={zoomed} count={count} />
                </DropdownMenuContent>
              </DropdownMenu>
            </>
          }
        />
      ) : (
        <BrowserScreen
          project={b.project}
          tab={b.tab}
          lastURL={b.url}
          paneId={pane.id}
          focused={focused}
          fit={floats ? fitOf(tab.float![pane.id]) : undefined}
          size={b.fit}
          onSize={(f) => {
            setBrowserFit(pane.id, f);
            fitPaneWidth(pane.id, f);
          }}
          onFitDrawn={fitDrawn}
          viewOnly={viewOnly}
          headClassName={cn(headH, !focused && many && "opacity-70")}
          onHeadPointerDown={drag.onPointerDown}
          extra={
            <>
              <FloatButton pane={pane} tab={tab} />
              {zoomBtn}
              <PaneCount count={count} />
            </>
          }
          paneItems={
            <>
              {/* to a window of its own (the App only: it tracks them; not while floating, already out of the way).
                  In the menu with the pane moves, not on the bar: the bar's ↗ read as the same thing as "open in your
                  browser" beside it (user report) */}
              {b.tab && canPopOut && !floats && <PopOutItem project={b.project} tab={b.tab} />}
              <PaneMoveItems pane={pane} tab={tab} zoomed={zoomed} count={count} />
            </>
          }
          Body={PaneBody}
        />
      )}
    </div>
  );
}

/** Float this pane over the others in the tab (picture-in-picture), or dock it back. Only with another pane to stay docked. */
function FloatItem({ pane, tab }: { pane: PaneLeaf; tab: Tab; count?: Count }) {
  if (usePhone()) return null; // a phone shows one pane at a time: nothing to float over
  const on = !!tab.float?.[pane.id];
  if (!on && !floatable(tab, pane.id)) return null;
  return (
    <DropdownMenuItem onClick={() => toggleFloatPane(pane.id)}>
      <PictureInPicture2 /> {t(on ? "panes.dock" : "panes.float")}
      <ActionKeys action="float" />
    </DropdownMenuItem>
  );
}

/** Fold this Browser pane to a chip in the tab's corner (floated first when docked); same rules as floating. */
function MinimizeItem({ pane, tab }: { pane: PaneLeaf; tab: Tab }) {
  if (usePhone()) return null;
  if (!tab.float?.[pane.id] && !floatable(tab, pane.id)) return null;
  return (
    <DropdownMenuItem onClick={() => minimizePane(pane.id)}>
      <Minus /> {t("panes.minimize")}
    </DropdownMenuItem>
  );
}

/** The same as a button on the Browser pane's bar (asked for there, and later in its ⋯ too, beside Minimize). */
function FloatButton({ pane, tab }: { pane: PaneLeaf; tab: Tab }) {
  if (usePhone()) return null;
  const on = !!tab.float?.[pane.id];
  if (!on && !floatable(tab, pane.id)) return null;
  const label = t(on ? "panes.dock" : "panes.float");
  const key = keyText("float");
  return (
    <Button
      variant="ghost"
      size="icon"
      className={cn("size-6 shrink-0", on && "text-brand")}
      title={key ? `${label} (${key})` : label}
      onClick={() => toggleFloatPane(pane.id)}
    >
      <PictureInPicture2 className="size-3.5" />
    </Button>
  );
}

/**
 * The ⋯ menu's part every pane has, in the session pane's order and words: split, new tab, zoom, to its own tab, close.
 * A pane's own items go above it (the Browser pane's pages).
 */
function PaneMoveItems({ pane, tab, zoomed, count }: { pane: PaneLeaf; tab: Tab; zoomed?: boolean; count?: Count }) {
  // A floating pane isn't in the split: splitting it or opening a tab from it means nothing there (asked to drop them)
  // the layout's own: a floated pane isn't in the split on any device, a phone included
  const floating = !!tab.float?.[pane.id];
  return (
    <>
      {!floating && (
        <>
          <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("right"))}>
            <Columns2 /> {t("panes.splitRight")}
            <ActionKeys action="splitRight" />
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("down"))}>
            <Rows2 /> {t("panes.splitDown")}
            <ActionKeys action="splitDown" />
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => runAction("newTab")}>
            <Plus /> {t("panes.newTab")}
            <ActionKeys action="newTab" />
          </DropdownMenuItem>
        </>
      )}
      <FloatItem pane={pane} tab={tab} count={count} />
      {pane.browser && <MinimizeItem pane={pane} tab={tab} />}
      <FitItems pane={pane} tab={tab} />
      {!zoomed && !count && !tab.float?.[pane.id] && leaves(dockedRoot(tab) ?? tab.root).length > 1 && (
        <DropdownMenuItem onClick={() => (focusPane(pane.id), toggleZoom())}>
          <Maximize2 /> {t("panes.zoom")}
          <ActionKeys action="zoom" />
        </DropdownMenuItem>
      )}
      {leaves(tab.root).length > 1 && (
        <DropdownMenuItem onClick={() => paneToTab(pane.id)}>
          <ExternalLink /> {t("panes.toNewTab")}
        </DropdownMenuItem>
      )}
      <DropdownMenuSeparator />
      <DropdownMenuItem onClick={() => (focusPane(pane.id), closeFocusedPane())}>
        <X /> {t("panes.closePane")}
        <ActionKeys action="closePane" />
      </DropdownMenuItem>
    </>
  );
}

/** An artifacts folder beside the chat (components/folder/, docs/dev/ARTIFACTS.md): title bar + the folder view. */
function FolderPaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const f = pane.file!;
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;
  const headH = usePaneHeadHeight(framed);
  const drag = usePaneDrag(pane.id, { label: basename(f.path) });
  return (
    <div
      data-pane-id={pane.id}
      className={cn(
        "flex h-full min-h-0 flex-col",
        framed && "overflow-hidden rounded-md border",
        framed && (focused ? "border-brand" : "border-border"),
        drag.dragging && "opacity-40",
      )}
      onMouseDown={() => !focused && focusPane(pane.id)}
    >
      <div
        onPointerDown={drag.onPointerDown}
        className={cn("flex shrink-0 items-center gap-2 border-b px-3 text-[12px] max-md:pl-4", headH, !focused && many && "opacity-70")}
      >
        <FolderOpen className="size-4 shrink-0 text-brand" />
        <FadeText className="min-w-[3em] max-w-[60%] shrink font-medium max-md:max-w-none">{f.session ? t("folder.open") : basename(f.path)}</FadeText>
        <div className="flex-1" />
        <PaneCount count={count} />
        <Button variant="ghost" size="icon" className="size-6 shrink-0" title={t("common.close")} onClick={() => (focusPane(pane.id), closeFocusedPane())}>
          <X className="size-3.5" />
        </Button>
      </div>
      <PaneBody>
        <Folder by={f.session ? { session: f.session } : { dir: f.path }} host={f.host} />
      </PaneBody>
    </div>
  );
}

/** 终端：标题栏 + xterm。标题是 shell 设的窗口标题（OSC 0/2，多数 zsh 主题会设成「用户@机器: 目录」或正在跑的命令），没设就是目录名。 */
function TermPaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const term = pane.term!;
  const home = useStore((s) => s.home);
  // 坏数据兜底（旧后端配新界面时写进布局的空终端）：没 id 就显示「重开」
  const cwd = term.cwd || home;
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;
  const headH = usePaneHeadHeight(framed);
  const [osc, setOsc] = useState("");
  useStore((s) => s.addonVersion);
  const title = applyPaneTitle(pane, tab, osc || basename(cwd));
  const drag = usePaneDrag(pane.id, { label: title, term: true });
  return (
    <div
      data-pane-id={pane.id}
      className={cn(
        "flex h-full min-h-0 flex-col",
        framed && "overflow-hidden rounded-md border",
        framed && (focused ? "border-brand" : "border-border"),
        drag.dragging && "opacity-40",
      )}
      onMouseDown={() => !focused && focusPane(pane.id)}
    >
      <div
        onPointerDown={drag.onPointerDown}
        className={cn("flex shrink-0 items-center gap-2 border-b px-3 text-[12px] max-md:pl-4", headH, !focused && many && "opacity-70")}
      >
        <SquareTerminal className="size-4 text-muted-foreground" />
        {/* The 60% cap leaves room for the path, which a phone hides: there it only left a gap before the ⋯ */}
        <FadeText className="min-w-[3em] max-w-[60%] shrink font-medium max-md:max-w-none">{title}</FadeText>
        <span className="hidden min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground md:block" title={cwd}>
          {cwd}
        </span>
        <div className="flex-1 md:hidden" />
        <AddonSlot name="paneTitle" props={{ pane: paneSnapshot(pane), tab: tabSnapshot(tab) }} />
        {zoomed && (
          <Button
            variant="ghost"
            size="icon"
            className="size-6"
            onClick={toggleZoom}
            title={keyText("zoom") ? t("panes.restoreKey", { key: keyText("zoom") }) : t("panes.restore")}
          >
            <Minimize2 className="size-3.5" />
          </Button>
        )}
        <PaneCount count={count} />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-6">
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" onCloseAutoFocus={(e) => e.preventDefault()}>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), openTerminal("right"))}>
              <SquareTerminal /> {t("panes.termRight")}
              {/* ⌘D on a terminal is exactly this (duplicateFocused opens a terminal in the same directory) */}
              <ActionKeys action="duplicateRight" />
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), openTerminal("down"))}>
              <SquareTerminal /> {t("panes.termDown")}
              <ActionKeys action="duplicateDown" />
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), openBrowserPane("right"))}>
              <Globe /> {t("keys.splitBrowser")}
              <ActionKeys action="splitBrowser" />
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("right"))}>
              <Columns2 /> {t("panes.sessionRight")}
              <ActionKeys action="splitRight" />
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("down"))}>
              <Rows2 /> {t("panes.sessionDown")}
              <ActionKeys action="splitDown" />
            </DropdownMenuItem>
            {/* Through runAction like ⌘T itself: with the overview up that opens a new agent there instead of a tab */}
            <DropdownMenuItem onClick={() => runAction("newTab")}>
              <Plus /> {t("panes.newTab")}
              <ActionKeys action="newTab" />
            </DropdownMenuItem>
            {!zoomed && !count && !tab.float?.[pane.id] && leaves(dockedRoot(tab) ?? tab.root).length > 1 && (
              <DropdownMenuItem onClick={() => (focusPane(pane.id), toggleZoom())}>
                <Maximize2 /> {t("panes.zoom")}
                <ActionKeys action="zoom" />
              </DropdownMenuItem>
            )}
            {leaves(tab.root).length > 1 && (
              // 合并 tab（把 tab 拖进别的 tab）的反操作
              <DropdownMenuItem onClick={() => paneToTab(pane.id)}>
                <ExternalLink /> {t("panes.toNewTab")}
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => (focusPane(pane.id), closeFocusedPane())}>
              <X /> {t("panes.closeTerminal")}
              <ActionKeys action="closePane" />
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <PaneBody>
        {term.id ? (
          <TerminalView key={term.id} paneId={pane.id} termId={term.id} cwd={cwd} focused={focused} onTitle={setOsc} />
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-[13px] text-muted-foreground">
            {t("panes.termFailed")}
            <Button size="sm" variant="outline" onClick={() => reopenTerminal(pane.id, cwd, term.id)}>
              <SquareTerminal className="size-3.5" />
              {t("panes.reopenIn", { dir: basename(cwd) })}
            </Button>
          </div>
        )}
      </PaneBody>
    </div>
  );
}

/**
 * The pane switched to another session (picked from the sidebar) that hasn't loaded yet, or whose cache is behind: keep
 * drawing the session it showed until the new one's first frame is in (lib/catchup). Drawing it right away showed a
 * frame or two of the empty "loading" state, or the stale cache, before the content (reported on a phone).
 */
function HeldSessionPane(props: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const sid = props.pane.sessionId;
  const shown = useCaughtUp(sid, sid ? [sid] : [], (id) => !!get().sessions[id]);
  return <SessionPaneView {...props} pane={shown === sid ? props.pane : { ...props.pane, sessionId: shown }} />;
}

function SessionPaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const hasHosts = useStore((s) => s.hosts.length > 0);
  const session = useStore((s) => (pane.sessionId ? s.sessions[pane.sessionId] : undefined));
  const unread = useStore((s) => (pane.sessionId ? !!s.unread[pane.sessionId] : false));
  const starting = useStore((s) => !!s.starting[pane.id]);
  const inTodo = useStore((s) => !!pane.sessionId && activeIds(s).includes(pane.sessionId));
  // Asked on every draw of the title bar: the panel's answer changes with its own data, which nothing here follows
  const panelItems = codeMenuItems(pane.id, pane.sessionId);
  const agents = useStore((s) => s.agents);
  const home = useStore((s) => s.home);
  // The tab's only pane on a phone: the top bar (PhoneBar) already says its title and status, so this bar says where
  // the agent works instead (asked for: one title per screen, the path the desktop bar has had all along)
  const alone = usePhone() && leaves(tab.root).length === 1;
  const [more, setMore] = useState(false);
  useStore((s) => s.addonVersion);
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;
  const headH = usePaneHeadHeight(framed);
  const front = useWindowInFront();

  // Only while the window is in front: unread is shared, and a pane left focused behind other windows would clear it
  // for every device (lib/front.ts)
  useEffect(() => {
    if (front && focused && pane.sessionId && unread) markRead(pane.sessionId);
  }, [front, focused, pane.sessionId, unread]);

  const base = session ? session.title || basename(session.cwd) : starting && pane.draft?.cwd ? basename(pane.draft.cwd) : t("panes.newSession");
  const title = applyPaneTitle(pane, tab, base);
  const drag = usePaneDrag(pane.id, { label: title, agent: session?.agent });

  return (
    <div
      data-pane-id={pane.id}
      className={cn(
        "flex h-full min-h-0 flex-col",
        framed && "overflow-hidden rounded-md border",
        framed && (focused ? "border-brand" : "border-border"),
        drag.dragging && "opacity-40",
      )}
      onMouseDown={() => !focused && focusPane(pane.id)}
    >
      <div
        onPointerDown={drag.onPointerDown}
        className={cn("flex shrink-0 items-center gap-2 border-b px-3 text-[12px] max-md:pl-4", headH, !focused && many && "opacity-70")}
      >
        {alone && session ? (
          <PhonePath path={session.workdir ?? session.cwd} branch={session.branch} home={home} agent={session.agent} />
        ) : (
          <>
            {session || (starting && pane.draft?.agent) ? (
              <AgentIcon agent={session?.agent ?? pane.draft!.agent!} className="size-4" />
            ) : (
              <PanelTopOpen className="size-3.5 text-muted-foreground" />
            )}
            {/* The 60% cap leaves room for the path, which a phone hides: there it only left a gap before the ⋯ */}
            <FadeText className="min-w-[3em] max-w-[60%] shrink font-medium max-md:max-w-none">{title}</FadeText>
            <StatusDot status={dotStatus(session)} unread={unread} />
          </>
        )}
        <HostTag session={session} />
        {session?.pending ? <span className="shrink-0 whitespace-nowrap rounded-sm bg-bad/15 px-1.5 text-[11px] text-bad">{t("panes.waiting")}</span> : null}
        {session ? (
          <span className="hidden min-w-0 flex-1 items-center gap-2 overflow-hidden font-mono text-[11px] text-muted-foreground md:flex">
            {/* Where the agent works now (it can move itself, e.g. Claude's EnterWorktree); the branch below is that place's */}
            <span className="min-w-0 truncate" title={session.workdir ? t("panes.workdir", { cwd: session.cwd }) : session.cwd}>
              {session.workdir ?? session.cwd}
            </span>
            {/* 分支不许被截：窄的时候宁可截路径（路径的末段在标题里多半已经看得出来） */}
            {session.branch && (
              <span className="flex shrink-0 items-center gap-1" title={t("panes.branch", { branch: session.branch })}>
                <GitBranch className="size-3" />
                <span className="max-w-[16em] truncate">{session.branch}</span>
              </span>
            )}
          </span>
        ) : null}
        {/* 路径那段手机上藏了，没它撑开的话 ⋯ 会紧跟在标题后面；和终端 pane 一样补一个手机上的占位 */}
        <div className={cn("flex-1", session && "md:hidden")} />
        <AddonSlot name="paneTitle" props={{ pane: paneSnapshot(pane), tab: tabSnapshot(tab) }} />
        {/* Only a panel's task has an artifacts folder; Code's own sessions have none (docs/dev/ARTIFACTS.md) */}
        {session?.artifactsDir && (
          <Button variant="ghost" size="icon" className="size-6 shrink-0" title={t("folder.openHint")} onClick={() => openArtifacts(session.id)}>
            <FolderOpen className="size-3.5" />
          </Button>
        )}
        {zoomed && (
          <Button
            variant="ghost"
            size="icon"
            className="size-6"
            onClick={toggleZoom}
            title={keyText("zoom") ? t("panes.restoreKey", { key: keyText("zoom") }) : t("panes.restore")}
          >
            <Minimize2 className="size-3.5" />
          </Button>
        )}
        <PaneCount count={count} />
        <DropdownMenu onOpenChange={(o) => !o && setMore(false)}>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-6">
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" onCloseAutoFocus={(e) => e.preventDefault()}>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("right"))}>
              <Columns2 /> {t("panes.splitRight")}
              <ActionKeys action="splitRight" />
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("down"))}>
              <Rows2 /> {t("panes.splitDown")}
              <ActionKeys action="splitDown" />
            </DropdownMenuItem>
            {session && (
              // ⌘D / ⌘⇧D: a new session like this one (same agent, directory, mode) beside it, the way iTerm duplicates
              <>
                <DropdownMenuItem onClick={() => (focusPane(pane.id), duplicateFocused("right"))}>
                  <CopyPlus /> {t("panes.duplicateRight")}
                  <ActionKeys action="duplicateRight" />
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => (focusPane(pane.id), duplicateFocused("down"))}>
                  <CopyPlus /> {t("panes.duplicateDown")}
                  <ActionKeys action="duplicateDown" />
                </DropdownMenuItem>
              </>
            )}
            {/* The project's browser beside this pane (components/browser/) */}
            <DropdownMenuItem onClick={() => (focusPane(pane.id), openBrowserPane("right"))}>
              <Globe /> {t("keys.splitBrowser")}
              <ActionKeys action="splitBrowser" />
            </DropdownMenuItem>
            {/* Through runAction like ⌘T itself: with the overview up that opens a new agent there instead of a tab */}
            <DropdownMenuItem onClick={() => runAction("newTab")}>
              <Plus /> {t("panes.newTab")}
              <ActionKeys action="newTab" />
            </DropdownMenuItem>
            {!zoomed && !count && !tab.float?.[pane.id] && leaves(dockedRoot(tab) ?? tab.root).length > 1 && (
              <DropdownMenuItem onClick={() => (focusPane(pane.id), toggleZoom())}>
                <Maximize2 /> {t("panes.zoom")}
                <ActionKeys action="zoom" />
              </DropdownMenuItem>
            )}
            {leaves(tab.root).length > 1 && (
              // 合并 tab（把 tab 拖进别的 tab）的反操作
              <DropdownMenuItem onClick={() => paneToTab(pane.id)}>
                <ExternalLink /> {t("panes.toNewTab")}
              </DropdownMenuItem>
            )}
            {/* The hosting panel's own items for this pane (lib/codeMenu.ts): a task's status and the like */}
            {panelItems.length > 0 && (
              <>
                <DropdownMenuSeparator />
                {panelItems.map((it) => (
                  <DropdownMenuItem key={it.id} disabled={it.disabled} onClick={it.onSelect}>
                    {it.icon} {it.label}
                  </DropdownMenuItem>
                ))}
              </>
            )}
            {session && (
              <>
                <DropdownMenuSeparator />
                {inTodo ? (
                  <DropdownMenuItem onClick={() => removeActive(session.id)}>
                    <ListX /> {t("panes.removeActive")}
                  </DropdownMenuItem>
                ) : (
                  <DropdownMenuItem onClick={() => addToActive(session.id)}>
                    <ListPlus /> {t("panes.addActive")}
                  </DropdownMenuItem>
                )}
                <DropdownMenuItem onClick={() => set({ dialog: { kind: "rename", target: "session", id: session.id, value: session.title ?? "" } })}>
                  <Pencil /> {t("panes.renameSession")}
                  <ActionKeys action="renamePane" />
                </DropdownMenuItem>
                {/* A new conversation carrying this one so far, the same agent or another; this one stays (ForkSessionDialog) */}
                <DropdownMenuItem onClick={() => set({ dialog: { kind: "forkSession", sessionId: session.id } })}>
                  <GitFork /> {t("panes.forkSession")}
                </DropdownMenuItem>
                {/* Gerdr's own id is the one name for a session, for people and for `gerdr session`. The agent's own
                    session id changes on /clear and is only good for carrying on in a terminal, so it is only handed out
                    inside that command. */}
                <DropdownMenuItem
                  onClick={() => {
                    copyText(session.id).then(
                      () => toast(t("panes.idCopied")),
                      () => toast.error(t("common.copyFailed")),
                    );
                  }}
                >
                  <Copy /> {t("panes.copyId")}
                </DropdownMenuItem>
                {/* Rarely needed: folded under 更多 so the menu stays short (asked for), not dropped — this is their only way in.
                    Unfolded in place rather than a submenu: on a phone a submenu has no room on either side and was cut off */}
                {(["claude", "codex"].includes(session.base ?? session.agent) || hasHosts) && (
                  <>
                    <DropdownMenuItem onSelect={(e) => (e.preventDefault(), setMore((v) => !v))}>
                      <MoreHorizontal /> {t("common.more")}
                      <ChevronDown className={cn("ml-auto transition-transform", more && "rotate-180")} />
                    </DropdownMenuItem>
                    {more && (
                      <div className="ml-3 border-l pl-1">
                        {["claude", "codex"].includes(session.base ?? session.agent) && session.agentSessionId && (
                          <DropdownMenuItem
                            onClick={() => {
                              copyText(resumeCommand(session.base ?? session.agent, session.cwd, session.agentSessionId)).then(
                                () => toast(t("panes.resumeCopied")),
                                () => toast.error(t("common.copyFailed")),
                              );
                            }}
                          >
                            <SquareTerminal /> {t("panes.copyResume")}
                          </DropdownMenuItem>
                        )}
                        {["claude", "codex"].includes(session.base ?? session.agent) && (
                          // 在别处（herdr / 终端）接着聊过的会话，打开时会自动追上；这里是手动的那一下
                          <DropdownMenuItem
                            onClick={() =>
                              api
                                .reload(session.id)
                                .then(() => toast.success(t("panes.reloaded")))
                                .catch((e) => toast.error(e.message))
                            }
                          >
                            <RefreshCw /> {t("panes.reload")}
                          </DropdownMenuItem>
                        )}
                        {hasHosts && (
                          // Another computer this one drives (docs/dev/MULTIHOST.md): the conversation carries on over there
                          <DropdownMenuItem onClick={() => set({ dialog: { kind: "moveSession", sessionId: session.id, paneId: pane.id } })}>
                            <Server /> {t("panes.moveSession")}
                          </DropdownMenuItem>
                        )}
                      </div>
                    )}
                  </>
                )}
                <DropdownMenuItem onClick={() => closeSession(session.id)}>
                  <CircleX /> {t("panes.closeSession")}
                </DropdownMenuItem>
              </>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => (focusPane(pane.id), closeFocusedPane())}>
              <X /> {t("panes.closePane")}
              <ActionKeys action="closePane" />
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <PaneBody>
        {session ? (
          <ChatView key={session.id} session={session} paneId={pane.id} focused={focused} />
        ) : starting ? (
          <div className="flex h-full items-center justify-center gap-2 text-[12px] text-muted-foreground">
            <PixelLoader className="size-4" />
            {t("panes.starting", { agent: agents.find((a) => a.id === pane.draft?.agent)?.name ?? pane.draft?.agent ?? "" })}
          </div>
        ) : (
          <NewSession key={pane.id + (pane.draft?.cwd ?? "")} pane={pane} focused={focused} />
        )}
      </PaneBody>
    </div>
  );
}

/** The shell command that carries this conversation on in a terminal, in its own directory (Gerdr catches up on it
 * when the session is opened again). */
function resumeCommand(agent: string, cwd: string, agentSessionId: string): string {
  const q = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`;
  const resume = agent === "codex" ? `codex resume ${agentSessionId}` : `claude --resume ${agentSessionId}`;
  return cwd ? `cd ${q(cwd)} && ${resume}` : resume;
}
