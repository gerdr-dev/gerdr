import { ArrowLeft, ArrowUpRight, Eraser, GripVertical, ClipboardCheck, Maximize2, Minimize2, PanelLeft, Pin, PinOff, Plus, X } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { basename, focusSession, tabName } from "@/lib/actions";
import { locateSession, spaceList, spaceOf, type Layout } from "@/lib/layout";
import { compareSessions } from "@/lib/order";
import {
  addTiles,
  arrange,
  attentionIds,
  clearTiles,
  fitTiles,
  focusTile,
  toggleTileZoom,
  MAX_TILES,
  openSessions,
  overviewFocus,
  overviewIds,
  removeTile,
  moveSeam,
  setGridSizes,
  setOverview,
  swapTiles,
  toggleMain,
  toggleOverview,
  useOverviewIds,
  workChats,
  type Box,
  type Seam,
} from "@/lib/overview";
import type { WorkConf } from "@/lib/overviewState";
import { usePhone } from "@/lib/phone";
import { markRead, toggleSidebar, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { cn } from "@/lib/utils";
import { BOX_TRANSITION, useLayoutAnim, useZoomAnim } from "@/lib/zoomAnim";
import { AgentIcon, StatusDot } from "./AgentIcon";
import { ChatView } from "./chat/ChatView";
import { Elapsed, SidebarEdge, SidebarFooter, SidebarHeader } from "./Chrome";
import { TabActive } from "./Panes";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";

/** The project a session belongs to: its pane's, or the one whose Work view shows it. */
function projectOf(l: Layout, c: WorkConf, id: string): string | undefined {
  const loc = locateSession(l, id);
  if (loc) return spaceOf(loc.ws);
  return Object.entries(workChats(c, l)).find(([, x]) => x === id)?.[0];
}

/** "project · workspace · tab" of a session's pane, or "project · Work" (the project only when there's more than one). */
function whereOf(l: Layout, c: WorkConf, id: string): string {
  const loc = locateSession(l, id);
  const spaces = spaceList(l);
  if (!loc) {
    const p = projectOf(l, c, id);
    if (!p) return "";
    return spaces.length > 1 ? `${spaces.find((s) => s.id === p)?.name ?? p} · Work` : "Work";
  }
  const ti = loc.ws.tabs.indexOf(loc.tab);
  const parts = [loc.ws.name, `${ti + 1}.${tabName(loc.tab)}`];
  if (spaces.length > 1) parts.unshift(spaces.find((s) => s.id === spaceOf(loc.ws))?.name ?? spaceOf(loc.ws));
  return parts.join(" · ");
}

/**
 * The all-agents overview (lib/overview.ts): every open agent of every project in the list on the left, the ones put in
 * auto-arranged on the right. Covers the sidebar and the tabs; they stay mounted underneath (TabActive off) so closing
 * this lands back exactly where things were. `shown` false = kept mounted but hidden, for the same reason.
 */
export function OverviewView({ shown }: { shown: boolean }) {
  const phone = usePhone();
  const ids = useOverviewIds().split(",").filter(Boolean);
  const zoom = useStore((s) => s.overview.zoom);
  const sidebar = useStore((s) => s.sidebar);
  const cur = useStore((s) => overviewFocus(s.overview, s.layout, s.sessions, s.config));
  // A tile taken out while zoomed: back to the grid
  const zoomed = zoom && ids.includes(zoom) ? zoom : undefined;
  const pick = (id: string) => {
    if (!ids.includes(id)) addTiles([id]);
    focusTile(id);
  };
  if (phone) return <PhoneOverview ids={ids} focus={cur} shown={shown} onPick={pick} />;
  return (
    <TabActive.Provider value={shown}>
      <div className="flex h-full bg-background">
        {/* Hidden with the Code sidebar (one setting, asked for: as in Code mode); a thin rail keeps the way back where the
            hide button was, at the bottom (the top-left corner is the window's traffic lights in the desktop App) */}
        {sidebar ? (
          <OverviewList current={cur} onPick={pick} />
        ) : (
          <div className="flex w-10 shrink-0 flex-col justify-end border-r bg-sidebar">
            <div className="flex h-10 shrink-0 items-center justify-center border-t">
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" onClick={toggleSidebar}>
                    <PanelLeft className="size-3.5" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent side="right">显示侧栏</TooltipContent>
              </Tooltip>
            </div>
          </div>
        )}
        <Grid ids={ids} focus={cur} zoom={zoomed} shown={shown} onFocus={focusTile} onZoom={toggleTileZoom} />
      </div>
    </TabActive.Provider>
  );
}

/**
 * The overview on a phone: no room for a grid, so one tile at a time. The top bar is the tile switcher, one chip per
 * tile tinted with its status (waiting / running / unseen readable at a glance, asked for); the list of all agents is a
 * drawer. Only the shown tile is mounted (a phone can't afford a dozen live chats); its transcript stays cached.
 */
function PhoneOverview({ ids, focus, shown, onPick }: { ids: string[]; focus?: string; shown: boolean; onPick: (id: string) => void }) {
  const [list, setList] = useState(false);
  const chips = useRef<HTMLDivElement>(null);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  // Chips in the Code sidebar's agent-list order (asked for: what needs you first), not the order they were put in;
  // ties keep that order (sort is stable).
  const sorted = ids.filter((id) => sessions[id]).sort((a, b) => compareSessions(sessions[a], sessions[b], unread));
  // Keep the current chip in view (on opening, switching from the list, a removal, a reorder). Scrolls the strip itself after layout:
  // scrollIntoView also moves ancestors, and run before the chips were laid out it left the current one off screen.
  const idsKey = sorted.join(",");
  useLayoutEffect(() => {
    const strip = chips.current;
    const chip = strip?.querySelector<HTMLElement>("[data-current]");
    if (!strip || !chip) return;
    const left = chip.offsetLeft - strip.offsetLeft;
    if (left < strip.scrollLeft) strip.scrollLeft = left;
    else if (left + chip.offsetWidth > strip.scrollLeft + strip.clientWidth) strip.scrollLeft = left + chip.offsetWidth - strip.clientWidth;
  }, [focus, idsKey, shown]);
  return (
    <TabActive.Provider value={shown}>
      <div className="flex h-full flex-col bg-background">
        <div className="flex h-11 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5">
          <Button variant="ghost" size="icon" className="size-8 shrink-0" aria-label="返回" onClick={() => toggleOverview(false)}>
            <ArrowLeft className="size-[18px]" />
          </Button>
          {/* Both scrollbar hiders: Android's WebView ignores scrollbar-width and drew a bar under the chips */}
          <div ref={chips} className="flex min-w-0 flex-1 gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            {sorted.map((id) => (
              <TileChip key={id} id={id} current={id === focus} className="h-8 max-w-36" onClick={() => focusTile(id)} />
            ))}
          </div>
          <Button variant="ghost" size="icon" className="size-9 shrink-0" aria-label="全部 agent" onClick={() => setList(true)}>
            <Plus className="size-[18px]" />
          </Button>
        </div>
        <div className="min-h-0 flex-1">
          {focus ? (
            <Tile key={focus} id={focus} focused={shown} main={false} zoomed={false} target={false} phone onFocus={() => {}} onZoom={() => {}} />
          ) : (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-8 text-center text-[13px] text-muted-foreground">
              <ClipboardCheck className="size-6" />
              待办是空的
              <Button size="sm" variant="outline" onClick={() => setList(true)}>
                <Plus className="size-3.5" /> 挑几个
              </Button>
            </div>
          )}
        </div>
      </div>
      {list && (
        <div className="fixed inset-0 z-40 flex">
          <OverviewList current={focus} onPick={(id) => (onPick(id), setList(false))} phone onClose={() => setList(false)} />
          <div className="flex-1 bg-background/50" onClick={() => setList(false)} />
        </div>
      )}
    </TabActive.Provider>
  );
}

/** A tile as a small chip tinted with its status: the phone's tile switcher, and the tiles that didn't fit in the grid. */
function TileChip({ id, current, className, onClick }: { id: string; current: boolean; className?: string; onClick: () => void }) {
  const s = useStore((st) => st.sessions[id]);
  const unread = useStore((st) => !!st.unread[id]);
  if (!s) return null;
  return (
    <button
      data-current={current || undefined}
      className={cn(
        "flex shrink-0 items-center gap-1.5 rounded-md border px-2 text-[12px]",
        statusTint(s.status, unread),
        current ? "border-brand font-medium" : "border-transparent text-muted-foreground",
        className,
      )}
      onClick={onClick}
    >
      {/* An idle one has no dot: StatusDot is transparent then but would still leave a gap */}
      {(s.status !== "idle" || unread) && <StatusDot status={s.status} unread={unread} />}
      <span className="truncate">{s.title || basename(s.cwd)}</span>
    </button>
  );
}

function OverviewList({
  current,
  onPick,
  phone,
  onClose,
}: {
  current?: string;
  onPick: (id: string) => void;
  /** In the phone drawer: wider, and the header closes the drawer instead of the overview. */
  phone?: boolean;
  onClose?: () => void;
}) {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const o = useStore((s) => s.overview);
  const sidebarW = useStore((s) => s.sidebarW);
  const config = useStore((s) => s.config);
  const tiles = new Set(overviewIds(o, layout, sessions, config));
  const all = openSessions(layout, sessions, config);
  const attention = all.filter((s) => s.status === "running" || s.status === "waiting" || s.status === "error" || unread[s.id]);
  const missing = attention.filter((s) => !tiles.has(s.id)).length;
  const full = tiles.size >= MAX_TILES;

  // By status: one list in sessionRank order. By project: grouped under each project, layout order inside.
  const groups: { name?: string; rows: SessionSummary[] }[] = [];
  if (o.sort === "status") groups.push({ rows: [...all].sort((a, b) => compareSessions(a, b, unread)) });
  else
    for (const sp of spaceList(layout)) {
      const rows = all.filter((s) => projectOf(layout, config, s.id) === sp.id);
      if (rows.length) groups.push({ name: sp.name, rows });
    }

  return (
    <div
      className={cn("relative flex h-full shrink-0 flex-col border-r bg-sidebar", phone && "w-[min(18rem,85vw)] shadow-xl")}
      style={phone ? undefined : { width: sidebarW }}
    >
      {!phone && <SidebarEdge />}
      {phone ? (
        // The phone drawer: a way to close it, the overview's own top bar is behind it
        <div className="flex h-10 shrink-0 items-center gap-1 border-b px-2">
          <Button variant="ghost" size="icon" className="size-7" onClick={onClose} aria-label="返回">
            <ArrowLeft className="size-4" />
          </Button>
          <span className="flex-1 truncate text-[13px] font-medium">全部 agent</span>
        </div>
      ) : (
        // The same top row as the Code sidebar and Work mode (asked for): project, then the Code / overview / Work switch
        <SidebarHeader />
      )}
      <div className="shrink-0 space-y-2 border-b p-1.5">
        <Button
          variant="outline"
          size="sm"
          className="h-7 w-full justify-start gap-2 text-[12px]"
          disabled={!missing || full}
          onClick={() => addTiles(attentionIds())}
        >
          <Plus className="size-3.5" />
          放入运行中和未读
          <span className="ml-auto tabular-nums text-muted-foreground">{missing || ""}</span>
        </Button>
        <label className="flex items-center gap-2 px-1 text-[12px] text-muted-foreground" title="有 agent 开始跑或在等你时，自动放进来">
          <Switch size="sm" checked={o.follow} onCheckedChange={(v) => setOverview((x) => ({ ...x, follow: v }))} />
          自动放入开始运行的
        </label>
      </div>
      <div className="flex shrink-0 items-center gap-1 px-3.5 pt-2 pb-1 text-[11px] text-muted-foreground">
        <button
          className={cn("hover:text-foreground", o.sort === "status" && "font-medium text-foreground")}
          onClick={() => setOverview((x) => ({ ...x, sort: "status" }))}
        >
          按状态
        </button>
        <span>/</span>
        <button
          className={cn("hover:text-foreground", o.sort === "project" && "font-medium text-foreground")}
          onClick={() => setOverview((x) => ({ ...x, sort: "project" }))}
        >
          按项目
        </button>
        <span className="flex-1" />
        <span className="tabular-nums">
          {tiles.size}/{all.length}
        </span>
        {tiles.size > 0 && (
          <button className="ml-1 hover:text-foreground" title="全部移出" onClick={clearTiles}>
            <Eraser className="size-3" />
          </button>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
        {all.length === 0 && <div className="px-2 py-1 text-[12px] text-muted-foreground">没有开着的会话</div>}
        {groups.map((g, gi) => (
          <div key={g.name ?? gi}>
            {g.name && <div className="truncate px-2 pt-2 pb-0.5 text-[11px] font-medium text-muted-foreground">{g.name}</div>}
            {g.rows.map((s) => {
              const on = tiles.has(s.id);
              return (
                <div
                  key={s.id}
                  role="button"
                  // Three looks that tell apart at a glance (a slightly different grey didn't, in the dark theme): the tile you're on
                  // = brand tint + bar, in the grid = bar, not in = dimmed title
                  className={cn(
                    "group relative flex w-full cursor-default items-start gap-2 rounded-md px-2 py-1 text-left hover:bg-accent/60",
                    s.id === current && on && "bg-brand/15 hover:bg-brand/20",
                    !on && full && "opacity-50",
                  )}
                  title={on ? "在右边（点一下聚焦）" : full ? `最多放 ${MAX_TILES} 个` : "放到右边"}
                  onClick={() => (on || !full) && onPick(s.id)}
                >
                  {on && <span className="absolute top-1.5 bottom-1.5 left-0 w-[3px] rounded-full bg-brand" />}
                  <AgentIcon agent={s.agent} className="mt-0.5 size-3.5 shrink-0" />
                  <div className="min-w-0 flex-1">
                    <div className={cn("truncate text-[12px] leading-5", !on && "text-muted-foreground", s.id === current && on && "font-semibold")}>
                      {s.title || basename(s.cwd)}
                    </div>
                    <div className="flex text-[11px] leading-4 text-muted-foreground">
                      <span className="min-w-0 truncate">{whereOf(layout, config, s.id)}</span>
                      {s.status === "running" && s.turn && <Elapsed since={s.turn.startedAt} />}
                    </div>
                  </div>
                  {on ? (
                    <button
                      className="mt-0.5 hidden size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground group-hover:flex hover:text-foreground"
                      title="从右边移出"
                      onClick={(e) => (e.stopPropagation(), removeTile(s.id))}
                    >
                      <X className="size-3" />
                    </button>
                  ) : null}
                  <StatusDot status={s.status} unread={!!unread[s.id]} className={cn("mt-1.5", on && "group-hover:hidden")} />
                </div>
              );
            })}
          </div>
        ))}
      </div>
      {!phone && <SidebarFooter sidebarToggle />}
    </div>
  );
}

const pct = (v: number) => `${v * 100}%`;
const NO_SIZES = {};
/** Height of the "more" row under the grid, holding the tiles that didn't fit. */
const MORE_H = 30;

/**
 * Follow the pointer until it's released: the shared part of every drag here (seams, tiles). The pointer is captured by
 * the handle: otherwise moving across other tiles with the button down reaches their chats as "the user is dragging this
 * scroller" (ChatView lastInputOf), and when the drop then resizes them they take the moved scroll position as the
 * user's and stop sticking to the bottom (measured: 400-1600px off the bottom after a swap).
 * Every handle needs touch-none (DRAG_TOUCH): without it the browser takes a touch drag over as a pan after a few px and
 * sends pointercancel, so on a phone or tablet nothing here moved more than a nudge. A cancel still ends the drag.
 */
function track(e: React.PointerEvent, cursor: string, move: (ev: PointerEvent) => void, up?: (ev: PointerEvent) => void) {
  e.preventDefault();
  const handle = e.currentTarget as HTMLElement;
  try {
    handle.setPointerCapture(e.pointerId);
  } catch {}
  const done = (ev: PointerEvent) => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", done);
    window.removeEventListener("pointercancel", done);
    document.body.style.cursor = "";
    up?.(ev);
  };
  document.body.style.cursor = cursor;
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", done);
  window.addEventListener("pointercancel", done);
}

/** Drag handles here on touch (track): no browser pan; seams wider for a finger (8px is hard to hit). */
const DRAG_TOUCH = "touch-none";
const SEAM_COL_COARSE = "pointer-coarse:w-6";
const SEAM_ROW_COARSE = "pointer-coarse:h-6";

type TileDrag = { id: string; x: number; y: number; over?: string; label: string };

function Grid({
  ids,
  focus,
  zoom,
  shown,
  onFocus,
  onZoom,
}: {
  ids: string[];
  focus?: string;
  zoom?: string;
  shown: boolean;
  onFocus: (id: string) => void;
  onZoom: (id: string) => void;
}) {
  const wrap = useRef<HTMLDivElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const [area, setArea] = useState({ w: 0, h: 0 });
  const main = useStore((s) => s.overview.main);
  const ratio = useStore((s) => s.overview.ratio);
  const saved = useStore((s) => s.overview.sizes) ?? NO_SIZES;
  const [drag, setDrag] = useState<TileDrag>();
  useLayoutEffect(() => {
    const el = wrap.current!;
    // Minus the 3px inset on each side (the grid box below)
    const ro = new ResizeObserver(() => setArea({ w: Math.max(0, el.clientWidth - 6), h: Math.max(0, el.clientHeight - 6) }));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const all = ids.join(",");
  // Only as many tiles as fit at a readable size (lib/overview fitTiles); the rest wait in the "more" row, which itself
  // takes MORE_H off the grid, so fit again with that height once some don't fit
  const shownIds = useMemo(() => {
    const v = fitTiles(ids, main, ratio, area.w, area.h);
    return v.length < ids.length ? fitTiles(ids, main, ratio, area.w, area.h - MORE_H) : v;
  }, [all, main, ratio, area.w, area.h]);
  const more = ids.filter((id) => !shownIds.includes(id));
  const size = { w: area.w, h: more.length ? area.h - MORE_H : area.h };
  const key = shownIds.join(",");
  const lay = useMemo(() => arrange(shownIds, main, ratio, size.w, size.h, saved), [key, main, ratio, size.w, size.h, saved]);
  // Focusing a tile that's in "more" (from there, the list, the keys) brings it into the grid in place of the last one
  useEffect(() => {
    if (!focus || !ids.includes(focus) || shownIds.includes(focus)) return;
    const out = [...shownIds].reverse().find((id) => id !== main) ?? shownIds[shownIds.length - 1];
    if (out) swapTiles(focus, out);
  }, [focus, key, all]);
  const hasMain = !!main && shownIds.includes(main) && shownIds.length > 1 && !zoom;
  // Same zoom animation as panes in a tab (lib/zoomAnim): the tile slides to full size over the others and back. Only its
  // chat is pinned to the full size while moving (Tile `pin`), so the chat re-lays out once, not every frame; the frame and
  // title bar follow the box, keeping the buttons in the corner and all four borders in view.
  const anim = useZoomAnim(zoom, false);
  const pin = anim && box.current ? { width: box.current.clientWidth - 8, height: box.current.clientHeight - 8 } : undefined;
  // Re-arranging (pin, swap, add / remove a tile) slides every tile to its new box the same way; seam drags and window
  // resizes follow the pointer / window and don't animate. Each chat is laid out at its final size meanwhile (tilePin).
  const moved = useLayoutAnim(`${key}|${main ?? ""}`);
  // Not before the grid has been measured: a chat laid out in a 0-wide box wraps everything (the composer stayed huge)
  const tilePin = (b: Box) => (size.w ? { width: b.w * size.w - 8, height: b.h * size.h - 8 } : undefined);

  const dragMain = (e: React.PointerEvent) => {
    const rect = box.current!.getBoundingClientRect();
    track(e, "col-resize", (ev) => {
      const r = Math.min(0.8, Math.max(0.2, (ev.clientX - rect.left) / rect.width));
      setOverview((o) => ({ ...o, ratio: r }));
    });
  };

  const dragSeam = (e: React.PointerEvent, seam: Seam) => {
    const rect = box.current!.getBoundingClientRect();
    const { key, sizes, region } = lay;
    track(e, seam.dir === "row" ? "row-resize" : "col-resize", (ev) => {
      // Where the pointer is along the seam's drag axis, as a fraction of the grid region
      const at = seam.dir === "row" ? (ev.clientY - rect.top) / rect.height : ((ev.clientX - rect.left) / rect.width - region.x) / region.w;
      if (seam.outer) setGridSizes(key, { ...sizes, rows: moveSeam(sizes.rows, seam.index, at) });
      else setGridSizes(key, { ...sizes, cols: sizes.cols.map((c, r) => (r === seam.row ? moveSeam(c, seam.index, at) : c)) });
    });
  };

  // Drag a tile by its title bar onto another one: the two trade places. Starts after a few px, so a click still focuses.
  const dragTile = (e: React.PointerEvent, id: string, label: string) => {
    if (e.button !== 0 || (e.target as HTMLElement).closest("button")) return;
    const sx = e.clientX;
    const sy = e.clientY;
    let on = false;
    const overAt = (ev: PointerEvent) => {
      const el = document.elementFromPoint(ev.clientX, ev.clientY)?.closest<HTMLElement>("[data-ov-tile]");
      const over = el?.dataset.ovTile;
      return over && over !== id ? over : undefined;
    };
    track(
      e,
      "grabbing",
      (ev) => {
        if (!on && Math.hypot(ev.clientX - sx, ev.clientY - sy) < 5) return;
        on = true;
        setDrag({ id, label, x: ev.clientX, y: ev.clientY, over: overAt(ev) });
      },
      (ev) => {
        const over = on ? overAt(ev) : undefined;
        setDrag(undefined);
        if (over) swapTiles(id, over);
      },
    );
    onFocus(id);
  };

  // 3px around + 3px per tile: 6px to the edges and between tiles, the same inset as the sidebar header and split panes
  return (
    <div ref={wrap} className="relative min-w-0 flex-1">
      <div ref={box} className="absolute inset-[3px]" style={more.length ? { bottom: 3 + MORE_H } : undefined}>
        {ids.length === 0 && (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-[13px] text-muted-foreground">
            <ClipboardCheck className="size-6" />
            待办是空的：在左边点 agent 放进来，或者点「放入运行中和未读」
          </div>
        )}
        {/* Flat and keyed by session, positioned absolutely (like Panes.TabView): re-arranging only moves boxes, the chats don't remount */}
        {shownIds.map((id) => {
          const b = zoom === id ? { x: 0, y: 0, w: 1, h: 1 } : lay.boxes.get(id);
          if (!b) return null;
          // Neighbours stay put under the moving tile and are only hidden once it covers them
          const hidden = !!zoom && zoom !== id && !anim;
          const moving = anim === id;
          const sliding = moved && !anim;
          return (
            <div
              key={id}
              data-ov-tile={id}
              className={cn(
                "absolute overflow-clip p-[3px]",
                hidden && "pointer-events-none invisible",
                drag?.id === id && "opacity-40",
                moving ? "z-20" : zoom === id && "z-10",
              )}
              // Inline transition, not a class: see Panes.TabView (browser Tailwind would add the class's CSS a change too late)
              style={{
                left: pct(b.x),
                top: pct(b.y),
                width: pct(b.w),
                height: pct(b.h),
                transition: moving || sliding ? BOX_TRANSITION : undefined,
              }}
            >
              <Tile
                id={id}
                focused={shown && focus === id}
                main={main === id}
                zoomed={zoom === id}
                target={drag?.over === id}
                onFocus={() => onFocus(id)}
                onZoom={() => onZoom(id)}
                onDragStart={zoom ? undefined : (e, label) => dragTile(e, id, label)}
                pin={moving ? pin : sliding ? tilePin(b) : undefined}
              />
            </div>
          );
        })}
        {!zoom &&
          !anim &&
          !moved &&
          lay.seams.map((sm) => {
            const { region } = lay;
            const row = sm.dir === "row";
            return (
              <div
                key={`${sm.outer ? "o" : "i"}${sm.row}.${sm.index}`}
                className={cn(
                  "group/seam absolute z-10 flex items-center justify-center hover:bg-brand/30",
                  DRAG_TOUCH,
                  row ? `h-2 -translate-y-1/2 cursor-row-resize ${SEAM_ROW_COARSE}` : `w-2 -translate-x-1/2 cursor-col-resize ${SEAM_COL_COARSE}`,
                )}
                style={
                  row
                    ? { left: pct(region.x + sm.x * region.w), width: pct(sm.len * region.w), top: pct(sm.y) }
                    : { left: pct(region.x + sm.x * region.w), top: pct(sm.y), height: pct(sm.len) }
                }
                onPointerDown={(e) => dragSeam(e, sm)}
                onDoubleClick={() => setGridSizes(lay.key, undefined)}
                title="拖动调大小，双击恢复等分"
              >
                <SeamGrip row={row} />
              </div>
            );
          })}
        {hasMain && !anim && !moved && (
          <div
            className={cn(
              "group/seam absolute top-0 bottom-0 z-10 flex w-2 -translate-x-1/2 cursor-col-resize items-center justify-center hover:bg-brand/30",
              DRAG_TOUCH,
              SEAM_COL_COARSE,
            )}
            style={{ left: pct(ratio) }}
            onPointerDown={dragMain}
            onDoubleClick={() => setOverview((o) => ({ ...o, ratio: 0.5 }))}
            title="拖动调宽窄，双击还原"
          >
            <SeamGrip row={false} />
          </div>
        )}
      </div>
      {more.length > 0 && (
        <div
          className="absolute inset-x-[6px] bottom-[3px] flex items-center gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
          style={{ height: MORE_H }}
        >
          <span className="shrink-0 px-1 text-[11px] text-muted-foreground" title="放不下了：点一个换进来，顶替最后一格">
            更多 {more.length}
          </span>
          {more.map((id) => (
            <TileChip key={id} id={id} current={false} className="h-6 max-w-40" onClick={() => onFocus(id)} />
          ))}
        </div>
      )}
      {drag && (
        <div
          className="pointer-events-none fixed z-50 max-w-60 truncate rounded-md border bg-popover px-2 py-1 text-[12px] shadow-md"
          style={{ left: drag.x + 12, top: drag.y + 12 }}
        >
          {drag.label}
        </div>
      )}
    </div>
  );
}

/** Always-visible handle in the middle of a seam: the gap alone doesn't say it can be dragged. */
function SeamGrip({ row }: { row: boolean }) {
  return <span className={cn("rounded-full bg-muted-foreground/40 group-hover/seam:bg-brand", row ? "h-1 w-10" : "h-10 w-1")} />;
}

function Tile({
  id,
  focused,
  main,
  zoomed,
  target,
  onFocus,
  onZoom,
  onDragStart,
  pin,
  phone,
}: {
  id: string;
  focused: boolean;
  main: boolean;
  zoomed: boolean;
  /** Another tile is being dragged over this one: dropping swaps them. */
  target: boolean;
  onFocus: () => void;
  onZoom: () => void;
  onDragStart?: (e: React.PointerEvent, label: string) => void;
  /** Tile size at the end of a zoom animation in progress: the chat is laid out at that size meanwhile (clipped). */
  pin?: { width: number; height: number };
  /** Shown alone on a phone: no pinning / zooming there. */
  phone?: boolean;
}) {
  const session = useStore((s) => s.sessions[id]);
  const unread = useStore((s) => !!s.unread[id]);
  const where = useStore((s) => whereOf(s.layout, s.config, id));
  const inWork = useStore((s) => !locateSession(s.layout, id)); // a Work conversation (no pane)
  useEffect(() => {
    if (focused && unread) markRead(id);
  }, [focused, unread, id]);
  if (!session) return null;
  const title = session.title || basename(session.cwd);
  // A Work conversation goes to Work mode (actions.openInWork), any other to its pane
  const jump = () => {
    toggleOverview(false);
    focusSession(id);
  };
  return (
    <div
      className={cn(
        "flex h-full min-h-0 flex-col overflow-clip bg-background",
        !phone && "rounded-md border",
        focused ? "border-brand" : "border-border",
        target && "ring-2 ring-brand",
      )}
      onMouseDown={() => !focused && onFocus()}
    >
      <div
        className={cn(
          "flex h-8 shrink-0 items-center gap-2 border-b px-3 text-[12px]",
          // The status is the whole title bar's tint instead of a dot (asked for): readable at a glance across a dozen tiles.
          // Dimming an unfocused tile goes on the contents only, so the tint keeps its strength.
          statusTint(session.status, unread),
          !focused && "[&>*]:opacity-70",
          // touch-none: the bar is a drag handle (track), and it has nothing to scroll
          onDragStart && `cursor-grab ${DRAG_TOUCH}`,
        )}
        onDoubleClick={onZoom}
        onPointerDown={onDragStart && ((e) => onDragStart(e, title))}
        // Middle click closes it, like a browser tab (mousedown is cancelled so the browser doesn't start auto-scroll)
        onMouseDown={(e) => e.button === 1 && e.preventDefault()}
        onAuxClick={(e) => e.button === 1 && removeTile(id)}
        title="拖到别的格子上交换位置，双击放大，中键关闭"
      >
        {onDragStart && <GripVertical className="-mx-1.5 size-3.5 shrink-0 text-muted-foreground/60" />}
        <AgentIcon agent={session.agent} className="size-4" />
        <span className="min-w-[3em] max-w-[50%] shrink truncate font-medium">{title}</span>
        <StatusDot status={session.status} unread={unread} />
        {session.pending ? <span className="shrink-0 whitespace-nowrap rounded-sm bg-bad/15 px-1.5 text-[11px] text-bad">等你</span> : null}
        <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">{where}</span>
        {!phone && (
          <>
            <TileButton title={main ? "取消固定" : "固定在左边"} onClick={() => toggleMain(id)}>
              {main ? <PinOff className="size-3.5" /> : <Pin className="size-3.5" />}
            </TileButton>
            <TileButton title={zoomed ? "还原" : "放大（双击标题栏也行）"} onClick={onZoom}>
              {zoomed ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}
            </TileButton>
          </>
        )}
        <TileButton title={inWork ? "去 Work 模式" : "回到它所在的 tab"} onClick={jump}>
          <ArrowUpRight className="size-3.5" />
        </TileButton>
        <TileButton title="完成，移出待办（agent 不受影响）" onClick={() => removeTile(id)}>
          <X className="size-3.5" />
        </TileButton>
      </div>
      <div className="min-h-0 flex-1 overflow-clip">
        {/* Own pane id: the composer's draft here is separate from the one in the session's real pane */}
        {/* Minus the 1px frame on each side and the 32px + 1px title bar */}
        <div className="h-full w-full" style={pin && { width: pin.width - 2, height: pin.height - 35 }}>
          <ChatView key={id} session={session} paneId={`ov-${id}`} focused={focused} />
        </div>
      </div>
    </div>
  );
}

/** Title bar tint for a tile's status, same colours as StatusDot: waiting / error red, running amber, finished unseen green. */
function statusTint(status: string, unread: boolean) {
  if (status === "waiting" || status === "error") return "bg-bad/15";
  if (status === "running") return "bg-warn/15";
  if (unread) return "bg-ok/15";
  return undefined;
}

function TileButton({ title, onClick, children }: { title: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <Button variant="ghost" size="icon" className="size-6 shrink-0" title={title} onClick={onClick} onDoubleClick={(e) => e.stopPropagation()}>
      {children}
    </Button>
  );
}
