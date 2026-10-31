import {
  Bot,
  Columns2,
  Copy,
  ExternalLink,
  GitBranch,
  ListPlus,
  ListX,
  Maximize2,
  RefreshCw,
  Minimize2,
  MoreHorizontal,
  PanelTopOpen,
  Pencil,
  Rows2,
  SquareTerminal,
  Trash2,
  X,
} from "lucide-react";
import { createContext, useContext, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import {
  basename,
  closeFocusedPane,
  deleteSession,
  focusPane,
  focusSession,
  openTerminal,
  paneToTab,
  reopenTerminal,
  splitFocused,
  toggleZoom,
} from "@/lib/actions";
import { leaves, phoneRoot, setRatio, updateTab, type PaneLeaf, type PaneNode, type PaneSplit, type Tab } from "@/lib/layout";
import { applyPaneTitle } from "@/lib/addons";
import { get, markRead, set, setLayout, useStore } from "@/lib/store";
import { addTiles, MAX_TILES, overviewIds, removeTile } from "@/lib/overview";
import { usePhone } from "@/lib/phone";
import { cn } from "@/lib/utils";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { ChatView } from "./chat/ChatView";
import { NewSession } from "./NewSession";
import { PixelLoader } from "./chat/PixelLoader";
import { startPaneDrag, useDrag, type TabGhost } from "./TabDrag";
import { TerminalView } from "./Terminal";
import { BOX_TRANSITION, useZoomAnim } from "@/lib/zoomAnim";

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
  const root = phone ? phoneRoot(tab) : tab.root;
  const panes: { pane: PaneLeaf; box: Box }[] = [];
  const seams: { split: PaneSplit; box: Box }[] = [];
  place(root, FULL, panes, seams);
  const zoom = !phone && tab.zoom && panes.some((p) => p.pane.id === tab.zoom) ? tab.zoom : undefined;
  // 分屏时和 herdr 一样：每个 pane 一圈边框、中间留缝，焦点那个边框高亮。只有一个 pane（或放大着）时不画框（省地方）。
  const anim = useZoomAnim(zoom, phone);
  // Frames stay on while a zoom animates (the growing pane looks like a window sliding over its neighbours) and drop at the end.
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
    count = { i: all.indexOf(p) + 1, n: all.length, pane: p.id };
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
      </div>
    </div>
  );
}

type Count = { i: number; n: number };

/**
 * 标题栏按住拖：把这个 pane 挪到别处（见 TabDrag.tsx 的 startPaneDrag）。手机上一次只看一个 pane，不给拖。
 * 被拖着的那个 pane 淡下去，和 tab 栏里被拖的 tab 一样。
 */
function usePaneDrag(paneId: string, ghost: TabGhost) {
  const phone = usePhone();
  const dragging = useDrag()?.paneId === paneId;
  return { dragging, onPointerDown: phone ? undefined : (e: React.PointerEvent<HTMLElement>) => startPaneDrag(e, paneId, ghost) };
}

function PaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  if (pane.sub) return <SubPaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
  if (pane.term) return <TermPaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
  return <SessionPaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
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

/** 手机上「这个 tab 里有几个 pane、你在第几个」：别的 pane 在屏幕上看不见，得有个地方说它们还在。点开面板一览去挑。 */
function PaneCount({ count }: { count?: Count }) {
  if (!count) return null;
  return (
    <button
      className="shrink-0 rounded-sm border px-1.5 py-px font-mono text-[10px] text-muted-foreground"
      title="这个 tab 里的 pane（点开面板一览）"
      onClick={() => set({ switcher: true, drawer: false })}
    >
      {count.i}/{count.n}
    </button>
  );
}

/** 子 agent 的只读面板：标题栏 + 只读的对话。 */
function SubPaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const sub = pane.sub!;
  const session = useStore((s) => s.sessions[sub.sessionId]);
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;
  const task = session?.tasks?.find((t) => t.toolCallId === sub.toolCallId);
  const title = task?.description || sub.title || "子 agent";
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
        className={cn("flex h-8 shrink-0 items-center gap-2 border-b px-3 text-[12px] max-md:pl-4", !focused && many && "opacity-70")}
      >
        <Bot className="size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 shrink-[2] truncate text-muted-foreground">{task?.subagent || "子 agent"}</span>
        <span className="min-w-0 flex-1 truncate font-medium">{title}</span>
        {task?.status === "running" && <StatusDot status="running" />}
        <PaneCount count={count} />
        {/* Shrinks too (truncated), so in a narrow pane the close button still fits */}
        {session && (
          <button className="min-w-0 max-w-[40%] truncate text-[11px] text-muted-foreground hover:text-foreground" onClick={() => focusSession(session.id)}>
            ← {session.title || basename(session.cwd)}
          </button>
        )}
        <Button variant="ghost" size="icon" className="size-6 shrink-0" title="关闭" onClick={() => (focusPane(pane.id), closeFocusedPane())}>
          <X className="size-3.5" />
        </Button>
      </div>
      <PaneBody>
        {session ? (
          <ChatView key={pane.id} session={session} paneId={pane.id} focused={focused} sub={sub.toolCallId} />
        ) : (
          <div className="py-16 text-center text-[13px] text-muted-foreground">这个会话已经不在了</div>
        )}
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
        className={cn("flex h-8 shrink-0 items-center gap-2 border-b px-3 text-[12px] max-md:pl-4", !focused && many && "opacity-70")}
      >
        <SquareTerminal className="size-4 text-muted-foreground" />
        <span className="min-w-[3em] max-w-[60%] shrink truncate font-medium">{title}</span>
        <PaneCount count={count} />
        <span className="hidden min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground md:block" title={cwd}>
          {cwd}
        </span>
        <div className="flex-1 md:hidden" />
        {zoomed && (
          <Button variant="ghost" size="icon" className="size-6" onClick={toggleZoom} title="还原（prefix+z）">
            <Minimize2 className="size-3.5" />
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-6">
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" onCloseAutoFocus={(e) => e.preventDefault()}>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), openTerminal("right"))}>
              <SquareTerminal /> 右边再开一个终端
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), openTerminal("down"))}>
              <SquareTerminal /> 下面再开一个终端
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("right"))}>
              <Columns2 /> 右边开会话
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("down"))}>
              <Rows2 /> 下面开会话
            </DropdownMenuItem>
            {!zoomed && !count && leaves(tab.root).length > 1 && (
              <DropdownMenuItem onClick={() => (focusPane(pane.id), toggleZoom())}>
                <Maximize2 /> 放大
              </DropdownMenuItem>
            )}
            {leaves(tab.root).length > 1 && (
              // 合并 tab（把 tab 拖进别的 tab）的反操作
              <DropdownMenuItem onClick={() => paneToTab(pane.id)}>
                <ExternalLink /> 移到新 tab
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => (focusPane(pane.id), closeFocusedPane())}>
              <X /> 关闭终端
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <PaneBody>
        {term.id ? (
          <TerminalView key={term.id} paneId={pane.id} termId={term.id} cwd={cwd} focused={focused} onTitle={setOsc} />
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-[13px] text-muted-foreground">
            这个终端没开起来
            <Button size="sm" variant="outline" onClick={() => reopenTerminal(pane.id, cwd)}>
              <SquareTerminal className="size-3.5" />在 {basename(cwd)} 重开
            </Button>
          </div>
        )}
      </PaneBody>
    </div>
  );
}

function SessionPaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const session = useStore((s) => (pane.sessionId ? s.sessions[pane.sessionId] : undefined));
  const unread = useStore((s) => (pane.sessionId ? !!s.unread[pane.sessionId] : false));
  const starting = useStore((s) => !!s.starting[pane.id]);
  const inTodo = useStore((s) => !!pane.sessionId && overviewIds(s.overview, s.layout, s.sessions, s.config).includes(pane.sessionId));
  const agents = useStore((s) => s.agents);
  useStore((s) => s.addonVersion);
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;

  useEffect(() => {
    if (focused && pane.sessionId && unread) markRead(pane.sessionId);
  }, [focused, pane.sessionId, unread]);

  const base = session ? session.title || basename(session.cwd) : starting && pane.draft?.cwd ? basename(pane.draft.cwd) : "新会话";
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
        className={cn("flex h-8 shrink-0 items-center gap-2 border-b px-3 text-[12px] max-md:pl-4", !focused && many && "opacity-70")}
      >
        {session || (starting && pane.draft?.agent) ? (
          <AgentIcon agent={session?.agent ?? pane.draft!.agent!} className="size-4" />
        ) : (
          <PanelTopOpen className="size-3.5 text-muted-foreground" />
        )}
        <span className="min-w-[3em] max-w-[60%] shrink truncate font-medium">{title}</span>
        <StatusDot status={dotStatus(session)} unread={unread} />
        {session?.pending ? <span className="shrink-0 whitespace-nowrap rounded-sm bg-bad/15 px-1.5 text-[11px] text-bad">等你</span> : null}
        <PaneCount count={count} />
        {session ? (
          <span className="hidden min-w-0 flex-1 items-center gap-2 overflow-hidden font-mono text-[11px] text-muted-foreground md:flex">
            <span className="min-w-0 truncate" title={session.cwd}>
              {session.cwd}
            </span>
            {/* 分支不许被截：窄的时候宁可截路径（路径的末段在标题里多半已经看得出来） */}
            {session.branch && (
              <span className="flex shrink-0 items-center gap-1" title={`git 分支：${session.branch}`}>
                <GitBranch className="size-3" />
                <span className="max-w-[16em] truncate">{session.branch}</span>
              </span>
            )}
          </span>
        ) : null}
        {/* 路径那段手机上藏了，没它撑开的话 ⋯ 会紧跟在标题后面；和终端 pane 一样补一个手机上的占位 */}
        <div className={cn("flex-1", session && "md:hidden")} />
        {zoomed && (
          <Button variant="ghost" size="icon" className="size-6" onClick={toggleZoom} title="还原（prefix+z）">
            <Minimize2 className="size-3.5" />
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-6">
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" onCloseAutoFocus={(e) => e.preventDefault()}>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("right"))}>
              <Columns2 /> 左右分屏
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("down"))}>
              <Rows2 /> 上下分屏
            </DropdownMenuItem>
            {!zoomed && !count && leaves(tab.root).length > 1 && (
              <DropdownMenuItem onClick={() => (focusPane(pane.id), toggleZoom())}>
                <Maximize2 /> 放大
              </DropdownMenuItem>
            )}
            {leaves(tab.root).length > 1 && (
              // 合并 tab（把 tab 拖进别的 tab）的反操作
              <DropdownMenuItem onClick={() => paneToTab(pane.id)}>
                <ExternalLink /> 移到新 tab
              </DropdownMenuItem>
            )}
            {session && (
              <>
                <DropdownMenuSeparator />
                {inTodo ? (
                  <DropdownMenuItem onClick={() => removeTile(session.id)}>
                    <ListX /> 移出待办
                  </DropdownMenuItem>
                ) : (
                  <DropdownMenuItem
                    onClick={() => {
                      addTiles([session.id]);
                      toast(get().overview.ids.includes(session.id) ? "已加入待办" : `待办最多放 ${MAX_TILES} 个，先移出几个`);
                    }}
                  >
                    <ListPlus /> 加入待办
                  </DropdownMenuItem>
                )}
                <DropdownMenuItem onClick={() => set({ dialog: { kind: "rename", target: "session", id: session.id, value: session.title ?? "" } })}>
                  <Pencil /> 重命名会话
                </DropdownMenuItem>
                <DropdownMenuItem
                  onClick={() => {
                    navigator.clipboard.writeText(session.agentSessionId);
                    toast("已复制 agent 会话 id");
                  }}
                >
                  <Copy /> 复制 agent 会话 id
                </DropdownMenuItem>
                {["claude", "codex"].includes(session.base ?? session.agent) && (
                  // 在别处（herdr / 终端）接着聊过的会话，打开时会自动追上；这里是手动的那一下
                  <DropdownMenuItem
                    onClick={() =>
                      api
                        .reload(session.id)
                        .then(() => toast.success("已从 agent 自己的记录重新载入"))
                        .catch((e) => toast.error(e.message))
                    }
                  >
                    <RefreshCw /> 从 agent 记录重新载入
                  </DropdownMenuItem>
                )}
                <DropdownMenuItem variant="destructive" onClick={() => deleteSession(session.id).catch((e) => toast.error(e.message))}>
                  <Trash2 /> 删除会话
                </DropdownMenuItem>
              </>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => (focusPane(pane.id), closeFocusedPane())}>
              <X /> 关闭 pane
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
            正在开 {agents.find((a) => a.id === pane.draft?.agent)?.name ?? pane.draft?.agent}…
          </div>
        ) : (
          <NewSession key={pane.id + (pane.draft?.cwd ?? "")} pane={pane} focused={focused} />
        )}
      </PaneBody>
    </div>
  );
}
