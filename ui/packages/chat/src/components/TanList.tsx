import { forwardRef, useEffect, useImperativeHandle, useLayoutEffect, useReducer, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Loader2 } from "lucide-react";
import { t as tr } from "../i18n";
import { RowView, type Row, type RowEnv } from "./Rows";

/**
 * The chat list on TanStack Virtual (Labs → "new chat list"), in place of react-virtuoso. Why: Virtuoso's scrolling
 * logic kept fighting a chat (docs/dev/SCROLL.md lists the patches), last of all its "upward scrolling compensation"
 * shoving the screen while a reply grew below it. TanStack Virtual's chat mode does what those patches did: sizes by row
 * key (not index), the place on screen kept by key when older messages go in front, a row that spans the top of the
 * screen growing at its bottom left alone, and no writes to scrollTop while an iOS finger or fling is moving.
 *
 * Following the bottom is ours (`stuck`), not followOnAppend: that one only follows an append, and missed rows growing
 * as they're measured after opening (opened 200px short of the bottom), a reply growing in place (ended 450px short)
 * and the whole log being replaced (17000px short; tools/scrolltest stream.mjs / streamup.mjs). Stuck = the user was at
 * the very bottom (2px, not Virtuoso's 80px: with 80px a finger 40px up still had the text running under it); while
 * stuck every change goes back to the end. Leaving is decided by the input, the moment the user scrolls up (wheel up,
 * a finger dragging down, up keys): the scroll event lands a frame later, after a push to the end could have undone it.
 */
export type ListHandle = {
  scrollToIndex(o: { index: number | "LAST"; align?: "start" | "end"; behavior?: "smooth" | "auto" }): void;
};

const END = 2;

export const TanList = forwardRef<
  ListHandle,
  {
    rows: Row[];
    env: RowEnv;
    older?: "more" | "loading";
    /** data-item-index = firstIdx + position, as Virtuoso's firstItemIndex: the same row keeps it when older ones go in front. */
    firstIdx: number;
    /** Render every row (the virtual-list switch off). */
    all: boolean;
    /** Where to open: a remembered scrollTop, else the bottom. */
    startTop?: number;
    /** A click just expanded something: don't follow the bottom while it grows (the clicked row stays put). */
    holding: () => boolean;
    scrollerRef: (el: HTMLElement | null) => void;
    onScroll: (el: HTMLElement) => void;
    onAtBottom: (b: boolean) => void;
    /** Rows changed height or the list changed size (the pinned prompt follows). */
    onLayout: () => void;
  }
>(function TanList({ rows, env, older, firstIdx, all, startTop, holding, scrollerRef, onScroll, onAtBottom, onLayout }, ref) {
  const el = useRef<HTMLDivElement | null>(null);
  // Every row rendered once and measured before the list shrinks to the rows near the screen, as on the Virtuoso path
  // (ChatView's `measured`): sizes are kept by row key, so a row scrolled back into view comes in at its real height.
  // Without it each row met while scrolling up came in at the 60px estimate and the screen jumped when it measured
  // (tools/scrolltest/fling.mjs: 6-7 jumps). Again after older messages went in front, and when the width changed.
  const [measuring, setMeasuring] = useState(true);
  const settled = useRef(0);
  const measureAgain = () => {
    setMeasuring(true);
    clearTimeout(settled.current);
    settled.current = window.setTimeout(() => setMeasuring(false), 2000);
  };
  const stuck = useRef(startTop === undefined);
  const setStuck = (b: boolean) => {
    if (stuck.current === b) return;
    stuck.current = b;
    onAtBottom(b);
  };
  // A smooth scroll the list started (the jump-to-bottom button): following the end meanwhile jumped it there in one
  // go, cutting the animation short (user report: "scrolling to the bottom isn't smooth")
  const smoothUntil = useRef(0);
  const lastInput = useRef(0); // the user's last wheel / touch / key on the list
  const remeasure = useRef(0);
  const toEnd = () => {
    const s = el.current;
    if (!s || !stuck.current || holding() || performance.now() < smoothUntil.current) return;
    if (s.scrollHeight - s.clientHeight - s.scrollTop > 0.5) v.scrollToEnd();
  };
  const phone = typeof window !== "undefined" && window.matchMedia("(max-width: 767px)").matches;
  const vRef = useRef<ReturnType<typeof useVirtualizer<HTMLDivElement, Element>> | null>(null);
  const v = useVirtualizer({
    count: rows.length,
    getScrollElement: () => el.current,
    getItemKey: (i) => rows[i].key,
    estimateSize: () => 60,
    overscan: all || measuring ? rows.length : 8,
    // Header (the "load older" line, 32px) and footer (the gap above the composer: 48px, 20 on a phone)
    paddingStart: 32,
    paddingEnd: phone ? 20 : 48,
    // Older messages put in front keep the screen where it was (by row key)
    anchorTo: "end",
    // -1: nothing counts as "at the end" for the virtualizer's own following; `stuck` does that
    scrollEndThreshold: -1,
    onChange: () => {
      if (!el.current) return;
      toEnd();
      onLayout();
      // Measured = the total height stayed put for 300ms (2s at most, measureAgain)
      if (measuring) {
        clearTimeout(settled.current);
        settled.current = window.setTimeout(() => setMeasuring(false), 300);
      }
    },
  });
  vRef.current = v;
  useImperativeHandle(ref, () => ({
    scrollToIndex: ({ index, align, behavior }) => {
      if (behavior === "smooth") smoothUntil.current = performance.now() + 1000;
      if (index === "LAST") {
        setStuck(true);
        v.scrollToEnd({ behavior: behavior ?? "auto" });
      } else v.scrollToIndex(index, { align: align ?? "start", behavior: behavior ?? "auto" });
    },
  }));
  // Open where it was, else at the bottom. Hidden until then, as Virtuoso does: the first render has no scroller to
  // measure yet, so it mounts the first few rows and the top of the chat flashed before the jump to the end
  // (tools/scrolltest/open.mjs: one frame of rows 0-2, then the bottom).
  const [shown, setShown] = useState(false);
  useLayoutEffect(() => {
    if (startTop !== undefined && el.current) el.current.scrollTop = startTop;
    else v.scrollToEnd();
    let r2 = 0;
    const r1 = requestAnimationFrame(() => {
      r2 = requestAnimationFrame(() => {
        if (startTop === undefined) toEnd();
        setShown(true);
      });
    });
    return () => {
      cancelAnimationFrame(r1);
      cancelAnimationFrame(r2);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  // New or replaced rows: back to the end while stuck (measured growth comes through onChange)
  useLayoutEffect(toEnd, [rows]);
  useEffect(() => {
    measureAgain();
    return () => clearTimeout(settled.current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows[0]?.key]);
  useEffect(() => {
    const s = el.current;
    if (!s) return;
    let w = s.clientWidth;
    const ro = new ResizeObserver(() => {
      // The chat itself got shorter or taller (the keyboard, or its IME candidate bar coming and going with each key: the
      // user saw the chat jump a line on every space typed; a seam dragged): still at the bottom, before paint
      if (stuck.current && !holding() && performance.now() >= smoothUntil.current) {
        const end = s.scrollHeight - s.clientHeight;
        if (Math.abs(end - s.scrollTop) > 0.5) s.scrollTop = end;
      }
      if (Math.abs(s.clientWidth - w) <= 1) return;
      w = s.clientWidth;
      measureAgain();
    });
    ro.observe(s);
    return () => ro.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  // Rows on screen and below measured by our own ResizeObserver, before paint: the virtualizer's own observer hands sizes over a frame or
  // more later, and meanwhile a row that grew (a reply being written) was drawn over the next one, which still sat at
  // the old offset (phone screenshot; tools/scrolltest/touchscroll.mjs --cpu=4: up to 24px over several frames), and
  // the list was drawn short of the bottom (panes.mjs --cpu=4: 24px). Here the new size goes in, the rows are moved
  // and the bottom followed in the same frame. Rows stay absolutely placed: laid out one after another instead, a row
  // growing above the screen moved what's on screen a frame before the virtualizer made up for it (fling.mjs and
  // open.mjs jumped).
  const [, bump] = useReducer((x: number) => x + 1, 0);
  const rowRO = useRef<ResizeObserver | null>(null);
  if (!rowRO.current && typeof ResizeObserver !== "undefined")
    rowRO.current = new ResizeObserver((entries) => {
      let changed = false;
      for (const e of entries) {
        const n = e.target as HTMLElement;
        if (!n.isConnected) continue;
        const i = Number(n.dataset.index);
        const h = e.borderBoxSize?.[0]?.blockSize ?? n.getBoundingClientRect().height;
        const m = vRef.current?.measurementsCache[i];
        if (!m || Math.abs(m.size - h) < 0.5) continue;
        // A row wholly above the screen is the virtualizer's: it keeps the screen in place for those (older messages put
        // in front), and sizing them ahead of it broke that (fling.mjs --to-top: 2-4 jumps). Ours are the rows on screen
        // and below, where a late size draws rows over each other or leaves the list short of the bottom.
        if (m.end <= (el.current?.scrollTop ?? 0)) continue;
        vRef.current!.resizeItem(i, h);
        changed = true;
      }
      if (!changed) return;
      flushSync(bump);
      const s = el.current;
      if (s && stuck.current && !holding() && performance.now() >= smoothUntil.current) {
        const end = s.scrollHeight - s.clientHeight;
        if (end - s.scrollTop > 0.5) s.scrollTop = end;
      }
    });
  useEffect(() => () => rowRO.current?.disconnect(), []);
  const observe = (n: HTMLDivElement | null) => {
    v.measureElement(n);
    if (n) rowRO.current?.observe(n, { box: "border-box" });
  };
  // Which way the user scrolls, from the input
  useEffect(() => {
    const s = el.current;
    if (!s) return;
    const up = () => s.scrollTop > 0 && setStuck(false);
    const touched = () => (lastInput.current = performance.now());
    const wheel = (e: WheelEvent) => (touched(), e.deltaY < 0 && up());
    let y: number | undefined;
    const tstart = (e: TouchEvent) => (touched(), (y = e.touches[0]?.clientY));
    const tmove = (e: TouchEvent) => {
      touched();
      const ny = e.touches[0]?.clientY;
      if (ny !== undefined && y !== undefined && ny > y) up();
      y = ny;
    };
    const key = (e: KeyboardEvent) => (touched(), ["ArrowUp", "PageUp", "Home"].includes(e.key) && up());
    s.addEventListener("wheel", wheel, { passive: true });
    s.addEventListener("touchstart", tstart, { passive: true });
    s.addEventListener("touchmove", tmove, { passive: true });
    s.addEventListener("keydown", key);
    // Dragging the scrollbar: the user's scroll too (no wheel or touch to tell)
    const press = (e: PointerEvent) => (e.type === "pointerdown" || e.buttons) && touched();
    s.addEventListener("pointerdown", press, { passive: true });
    s.addEventListener("pointermove", press, { passive: true });
    return () => {
      s.removeEventListener("pointerdown", press);
      s.removeEventListener("pointermove", press);
      s.removeEventListener("wheel", wheel);
      s.removeEventListener("touchstart", tstart);
      s.removeEventListener("touchmove", tmove);
      s.removeEventListener("keydown", key);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => () => scrollerRef(null), []); // eslint-disable-line react-hooks/exhaustive-deps
  const items = v.getVirtualItems();
  return (
    <div
      ref={(n) => {
        el.current = n;
        if (n) scrollerRef(n);
      }}
      data-chat-scroller=""
      className="h-full overflow-y-auto [overflow-anchor:none]"
      onScroll={(e) => {
        const s = e.currentTarget;
        // Once the scrolling stops, measure the mounted rows again: during a smooth scroll the virtualizer only measures
        // rows near its target, so a row that grew meanwhile (a reply being written) kept its old height and the next
        // row was drawn over it (user screenshot), for good once it stopped growing.
        clearTimeout(remeasure.current);
        remeasure.current = window.setTimeout(() => {
          for (const n of s.querySelectorAll<HTMLElement>("[data-chat-list] > [data-index]")) v.measureElement(n);
          toEnd();
        }, 200);
        const dist = s.scrollHeight - s.clientHeight - s.scrollTop;
        // Back at the very bottom (the user scrolled down to it): follow again
        if (!stuck.current && dist <= END) setStuck(true);
        // Moved off the bottom while following it. By the user (dragging the scrollbar: no wheel or touch to read a
        // direction from): stop following. Not by the user: the browser clamped the scroll position in a layout where the
        // list was briefly taller (the composer measuring itself did that); back to the end.
        else if (stuck.current && dist > END && !holding() && performance.now() >= smoothUntil.current) {
          if (performance.now() - lastInput.current < 500) setStuck(false);
          else s.scrollTop = s.scrollHeight - s.clientHeight;
        }
        onScroll(s);
      }}
    >
      <div data-chat-list="" className="relative w-full" style={{ height: v.getTotalSize(), visibility: shown ? undefined : "hidden" }}>
        <div className="absolute inset-x-0 top-0 flex h-8 items-center justify-center text-[12px] text-muted-foreground">
          {older === "loading" && <Loader2 className="size-3.5 animate-spin" />}
          {older === "more" && tr("chat.scrollForOlder")}
        </div>
        {items.map((it) => (
          <div
            key={it.key}
            ref={observe}
            data-index={it.index}
            data-item-index={firstIdx + it.index}
            className="absolute inset-x-0 top-0"
            style={{ transform: `translateY(${it.start}px)` }}
          >
            <RowView row={rows[it.index]} env={env} />
          </div>
        ))}
      </div>
    </div>
  );
});
