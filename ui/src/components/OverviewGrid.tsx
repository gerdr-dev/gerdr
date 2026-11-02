import { ClipboardCheck } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { arrange, fitTiles, moveSeam, setGridSizes, setOverview, placeTile, swapTiles, type Box, type DropSide, type Seam } from "@/lib/overview";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { BOX_TRANSITION, useLayoutAnim, useZoomAnim } from "@/lib/zoomAnim";
import { PickButton } from "./OverviewPick";
import { Tile, TileChip } from "./OverviewTile";
import { DRAG_TOUCH, SEAM_COL_COARSE, SEAM_ROW_COARSE } from "./tileInfo";
import { t } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";

const pct = (v: number) => `${v * 100}%`;
const NO_SIZES = {};
const NO_SHAPES = {};
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

/** Where a dragged tile would land on `over`: an edge puts it on that side (placeTile), the middle swaps the two. */
type TileDrag = { id: string; x: number; y: number; over?: string; side?: DropSide; label: string };

/** How far in from a tile's edge (share of its size) a drop still means "beside it"; deeper in, it's a swap. */
const EDGE = 0.3;
const SIDE_TEXT = {
  left: "overviewGrid.dropLeft",
  right: "overviewGrid.dropRight",
  up: "overviewGrid.dropUp",
  down: "overviewGrid.dropDown",
} as const satisfies Record<DropSide, MessageKey>;

export function Grid({
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
  const picked = useStore((s) => s.overview.shapes) ?? NO_SHAPES;
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
  const lay = useMemo(() => arrange(shownIds, main, ratio, size.w, size.h, saved, picked), [key, main, ratio, size.w, size.h, saved, picked]);
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
  const moved = useLayoutAnim(`${key}|${main ?? ""}|${lay.key}`);
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

  // Drag a tile by its title bar onto another one: near one of its edges it goes on that side, in the middle the two trade
  // places. Starts after a few px, so a click still focuses. The main tile only swaps (it has its own column).
  const dragTile = (e: React.PointerEvent, id: string, label: string) => {
    if (e.button !== 0 || (e.target as HTMLElement).closest("button")) return;
    const sx = e.clientX;
    const sy = e.clientY;
    let on = false;
    const overAt = (ev: PointerEvent): Pick<TileDrag, "over" | "side"> => {
      const el = document.elementFromPoint(ev.clientX, ev.clientY)?.closest<HTMLElement>("[data-ov-tile]");
      const over = el?.dataset.ovTile;
      if (!el || !over || over === id) return {};
      if (main && (over === main || id === main) && shownIds.length > 1) return { over };
      const r = el.getBoundingClientRect();
      const fx = (ev.clientX - r.left) / r.width;
      const fy = (ev.clientY - r.top) / r.height;
      const [side, d] = (
        [
          ["left", fx],
          ["right", 1 - fx],
          ["up", fy],
          ["down", 1 - fy],
        ] as [DropSide, number][]
      ).sort((a, b) => a[1] - b[1])[0];
      return d < EDGE ? { over, side } : { over };
    };
    track(
      e,
      "grabbing",
      (ev) => {
        if (!on && Math.hypot(ev.clientX - sx, ev.clientY - sy) < 5) return;
        on = true;
        setDrag({ id, label, x: ev.clientX, y: ev.clientY, ...overAt(ev) });
      },
      (ev) => {
        const { over, side } = on ? overAt(ev) : {};
        setDrag(undefined);
        if (over && side) placeTile(id, over, side, shownIds, size.w, size.h);
        else if (over) swapTiles(id, over);
      },
    );
    onFocus(id);
  };

  // 3px around + 3px per tile: 6px to the edges and between tiles, the same inset as the sidebar header and split panes
  return (
    <div ref={wrap} className="relative min-w-0 flex-1">
      <div ref={box} className="absolute inset-[3px]" style={more.length ? { bottom: 3 + MORE_H } : undefined}>
        {ids.length === 0 && (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-[13px] text-muted-foreground">
            <ClipboardCheck className="size-6" />
            {t("overviewGrid.empty")}
            <PickButton />
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
                target={drag?.over === id && !drag.side}
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
                title={t("overviewGrid.seamHint")}
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
            title={t("nav.dragResize")}
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
          <span className="shrink-0 px-1 text-[11px] text-muted-foreground" title={t("overviewGrid.moreHint")}>
            {t("overviewGrid.moreN", { n: more.length })}
          </span>
          {more.map((id) => (
            <TileChip key={id} id={id} current={false} className="h-6 max-w-40" onClick={() => onFocus(id)} />
          ))}
        </div>
      )}
      {drag?.over && drag.side && <DropPreview over={drag.over} side={drag.side} />}
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

/** The half of tile `over` a dragged tile would take (same look as dropping a pane beside another, TabDrag). */
function DropPreview({ over, side }: { over: string; side: DropSide }) {
  const el = document.querySelector<HTMLElement>(`[data-ov-tile="${CSS.escape(over)}"]`);
  if (!el) return null;
  const r = el.getBoundingClientRect();
  const half = { left: r.left + 3, top: r.top + 3, width: r.width - 6, height: r.height - 6 };
  if (side === "left" || side === "right") half.width /= 2;
  else half.height /= 2;
  if (side === "right") half.left += half.width;
  if (side === "down") half.top += half.height;
  return (
    <div
      className="pointer-events-none fixed z-40 flex items-center justify-center rounded-md border-2 border-brand bg-brand/15 transition-all duration-150 ease-out"
      style={half}
    >
      <span className="rounded-md bg-background/90 px-2 py-1 text-[12px] text-foreground shadow-sm">{t(SIDE_TEXT[side])}</span>
    </div>
  );
}

/** Always-visible handle in the middle of a seam: the gap alone doesn't say it can be dragged. */
function SeamGrip({ row }: { row: boolean }) {
  return <span className={cn("rounded-full bg-muted-foreground/40 group-hover/seam:bg-brand", row ? "h-1 w-10" : "h-10 w-1")} />;
}
