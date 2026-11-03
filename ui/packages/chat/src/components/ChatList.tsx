import { forwardRef, useEffect, useImperativeHandle, useLayoutEffect, useReducer, useRef } from "react";
import { flushSync } from "react-dom";
import { Loader2 } from "lucide-react";
import { t as tr } from "../i18n";
import { RowView, type Row, type RowEnv } from "./Rows";

/**
 * Gerdr's own virtual list for the chat, in place of react-virtuoso (removed; in git history). Why our own: Virtuoso and
 * then TanStack Virtual each kept several parties writing scrollTop (the library's compensation, its following of the
 * end, our patches on top), each from a different frame, and the chat jumped wherever they disagreed
 * (docs/dev/SCROLL.md). Here one function, `sync`, owns scrollTop and the rows' positions:
 *
 * - It runs before paint: after every React commit (layout effect) and from a ResizeObserver on the rows. It measures
 *   rows, lays every row out from the sizes kept by row key, writes the positions straight to the DOM and puts
 *   scrollTop where it belongs, all in the frame the change happened in. Nothing is drawn at a stale size.
 * - Where scrollTop belongs: stuck to the bottom, the end. Otherwise the row at the top of the screen keeps its distance
 *   from the top of the screen, found by key in the layout from before the change. Rows above it growing, shrinking or
 *   being put in front (older messages) move nothing on screen; the top row growing at its bottom (a reply being
 *   written) moves nothing either, and the user's own scrolling isn't a layout change, so it's never undone.
 * - Stuck = the user was at the very bottom (2px). Leaving is decided by the input, the moment the user scrolls up
 *   (wheel up, a finger dragging down, up keys, dragging the scrollbar up): the scroll event lands a frame later.
 * - Every row is rendered and measured once when the chat first opens at a width (and when older messages come
 *   in), then only the rows near the screen stay mounted: a row scrolled back into view comes in at its real height.
 * - Smooth scrolls (jump to bottom, to a prompt) are animated here, frame by frame towards a target recomputed each
 *   frame, so a layout change during the animation neither cuts it short nor sends it to a stale place.
 */
export type ListHandle = {
  scrollToIndex(o: { index: number | "LAST"; align?: "start" | "end"; behavior?: "smooth" | "auto" }): void;
  /** Put scrollTop at top, leaving the bottom (find in the chat: a match inside a row taller than the screen). */
  scrollTo(top: number): void;
};

const END = 2; // px from the end that still count as the bottom
const SETTLE = 150; // ms without a scroll after the finger lifts that end a touch (its fling has stopped)
const EST = 60; // a row never measured
const OVERSCAN = 800; // px mounted above and below the screen
const HEAD = 32; // the "load older" line
// A chat has been shown in this page: Tailwind has written the CSS its rows use (see the open effect below)
let stylesWarm = false;
// Row sizes measured by earlier mounts of a list (memKey), at the scroller width w. A tab not kept mounted remounts its
// chat on every switch; without these the whole chat was mounted and measured again each time (~700ms on a 15MB
// session). Rows whose cached size went stale (a group expanded before, collapsed now) are measured as they mount and
// the top row's anchor absorbs the difference, as after a width change.
const sizeMem = new Map<string, { w: number; sizes: Map<string, number> }>();
const SIZE_MEM_MAX = 40;

type Layout = { keys: string[]; tops: number[]; sizes: number[]; total: number; missing: number };

export const ChatList = forwardRef<
  ListHandle,
  {
    rows: Row[];
    env: RowEnv;
    older?: "more" | "loading";
    /** data-item-index = firstIdx + position: the same row keeps it when older ones go in front. */
    firstIdx: number;
    /** Render every row (the virtual-list switch off). */
    all: boolean;
    /** Keeps measured row sizes across remounts under this key (one list at a time per key). */
    memKey?: string;
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
>(function ChatList({ rows, env, older, firstIdx, all, memKey, startTop, holding, scrollerRef, onScroll, onAtBottom, onLayout }, ref) {
  const el = useRef<HTMLDivElement | null>(null);
  const box = useRef<HTMLDivElement | null>(null);
  const phone = typeof window !== "undefined" && window.matchMedia("(max-width: 767px)").matches;
  const foot = phone ? 20 : 48; // the gap above the composer
  // Sizes from an earlier mount: used if the width is still the same (checked on opening)
  const mem = useRef(memKey ? sizeMem.get(memKey) : undefined);
  const sizes = useRef(mem.current?.sizes ?? new Map<string, number>());
  if (memKey && !mem.current) {
    mem.current = { w: 0, sizes: sizes.current };
    sizeMem.set(memKey, mem.current);
    if (sizeMem.size > SIZE_MEM_MAX) sizeMem.delete(sizeMem.keys().next().value!);
  }
  const warm = useRef(!!mem.current?.w);
  const rowsRef = useRef(rows);
  rowsRef.current = rows;
  const cb = useRef({ holding, onScroll, onAtBottom, onLayout });
  cb.current = { holding, onScroll, onAtBottom, onLayout };

  const stuck = useRef(startTop === undefined);
  const setStuck = (b: boolean) => {
    if (stuck.current === b) return;
    stuck.current = b;
    cb.current.onAtBottom(b);
  };

  // Measure everything: on open, then for a while after the width changed. Ends once no row changed size for 300ms
  // (2s at most). Rows never measured (older messages put in front) are mounted along with the range for one commit,
  // measured and dropped (`missing`): mounting the whole chat again for them was a phone's worst frame
  // (touchscroll.mjs --cpu=4 counts it as a remount).
  const measuring = useRef(!warm.current);
  const settle = useRef(0);
  const settleMax = useRef(0);
  const [, render] = useReducer((x: number) => x + 1, 0);
  const endMeasuring = () => {
    clearTimeout(settle.current);
    clearTimeout(settleMax.current);
    if (!measuring.current) return;
    measuring.current = false;
    flushSync(render);
  };
  const measureAll = (inEffect: boolean) => {
    measuring.current = true;
    clearTimeout(settle.current);
    clearTimeout(settleMax.current);
    settleMax.current = window.setTimeout(endMeasuring, 2000);
    settle.current = window.setTimeout(endMeasuring, 300);
    if (inEffect) render();
    else flushSync(render);
  };
  const changed = () => {
    if (!measuring.current) return;
    clearTimeout(settle.current);
    settle.current = window.setTimeout(endMeasuring, 300);
  };

  const layout = (rs: Row[]): Layout => {
    const keys: string[] = [];
    const tops: number[] = [];
    const sz: number[] = [];
    let y = HEAD;
    let missing = 0;
    for (const r of rs) {
      const known = sizes.current.get(r.key);
      if (known === undefined) missing++;
      const h = known ?? EST;
      keys.push(r.key);
      tops.push(y);
      sz.push(h);
      y += h;
    }
    return { keys, tops, sizes: sz, total: y + foot, missing };
  };
  // What's in the DOM now (positions written by the last sync)
  const shown = useRef<Layout>({ keys: [], tops: [], sizes: [], total: 0, missing: 0 });
  const range = useRef<[number, number]>([0, 0]); // rows mounted by the last render, [from, to)
  const extra = useRef(0); // rows never measured mounted by the last render besides the range
  // scrollTop the range was computed for. Opening with known sizes, the first render mounts only the rows near where it
  // opens: guess the screen's height until there is one.
  const lastTop = useRef(0);
  const lastEnd = useRef(0); // endOf(scroller) when sync last placed it
  if (warm.current && !el.current && lastTop.current === 0)
    lastTop.current = startTop ?? Math.max(0, layout(rows).total - (typeof window !== "undefined" ? window.innerHeight : 800));

  const want = (lay: Layout, top: number, height: number): [number, number] => {
    const n = lay.keys.length;
    if (all || measuring.current) return [0, n];
    const lo = top - OVERSCAN;
    const hi = top + height + OVERSCAN;
    // first row ending below lo
    let a = 0;
    let b = n;
    while (a < b) {
      const m = (a + b) >> 1;
      if (lay.tops[m] + lay.sizes[m] <= lo) a = m + 1;
      else b = m;
    }
    let to = a;
    while (to < n && lay.tops[to] < hi) to++;
    return [a, Math.max(to, Math.min(a + 1, n))];
  };

  const dragging = useRef(false); // the scrollbar
  // A finger is down. The touchend can be lost when the row under the finger is replaced (a tool call's "peek" row
  // turning into its group: events go to the detached node and never bubble up); the next touchstart resets it.
  const finger = useRef(false);
  // A touch, from touchstart until its fling has settled (SETTLE after the last scroll once the finger is up). "At the
  // bottom" is a position, the same for touches as for everything else: within END of the end. Never the finger's
  // direction (at the bottom a finger pushing up, jittering or slipping back reads as "scrolling up" though nothing
  // scrolls: the button showed at the very end, four phone recordings), nor a distance threshold (a deliberate pull of
  // 12px snapped back to the end on lifting, read off a trace from the phone).
  // - While it runs: the chat scrolled up by the user and now off the end → not stuck. Getting back to the end doesn't
  //   stick again yet: a fling may still carry it on.
  // - When it ends: at the end → stuck, however it started.
  // Taps in quick succession are one touch (a touchstart before the settle carries it on). Touchmoves aren't read at
  // all, so a replaced row swallowing them (above) doesn't matter.
  const touch = useRef(false);
  const settleTouch = useRef(0);
  // Last wheel / key input upwards. A Mac trackpad starting a slow scroll moves half a pixel per event on a Retina
  // screen: not "up" by the scroll event's 0.5px test, still within END of the end, so each step was stuck again and
  // put back, and a small scroll up couldn't leave the bottom (user report). Shortly after an up input, landing within
  // END doesn't stick again; a wheel down there does (see the wheel listener).
  const upAt = useRef(-Infinity);
  const UP_HOLD = 300; // ms
  // A smooth scroll we're animating: towards target(), recomputed each frame
  const anim = useRef<{ raf: number; target: () => number | undefined } | null>(null);
  const stopAnim = () => {
    if (!anim.current) return;
    cancelAnimationFrame(anim.current.raf);
    anim.current = null;
  };
  const endOf = (s: HTMLElement) => s.scrollHeight - s.clientHeight;

  /**
   * Measure, lay out, place rows, place scrollTop. `measure`: rows whose size may have changed (all mounted ones if
   * omitted). `inEffect`: called from a layout effect, where a re-render is a plain setState.
   */
  const sync = (measure?: Iterable<Element>, inEffect = false) => {
    const s = el.current;
    const b = box.current;
    // Not laid out (a pane hidden with display:none): every row would measure 0. Its ResizeObservers fire again on showing.
    if (!s || !b || !s.clientHeight) return;
    const prev = shown.current;
    const top = s.scrollTop;
    // Scrolled up by a finger or the scrollbar since we last placed it, its scroll event maybe not in yet: theirs, not
    // ours to undo
    // A touch by how far it got from the end we last placed, not by each scroll event's step: a slow finger on a dpr-3
    // phone moves a third of a pixel per event, never "up by more than 0.5" from the last one.
    if (stuck.current && touch.current && top < lastTop.current && lastEnd.current - top > END) setStuck(false);
    if (stuck.current && dragging.current && top < lastTop.current - 0.5 && endOf(s) - top > END) setStuck(false);
    // The row at the top of the screen in the layout on screen until now (where it starts relative to the screen's top
    // is prev.tops[a] - top)
    let a = -1;
    for (let i = 0; i < prev.keys.length; i++)
      if (prev.tops[i] + prev.sizes[i] > top) {
        a = i;
        break;
      }
    let grew = false;
    for (const n of measure ?? b.querySelectorAll("[data-chat-key]")) {
      const k = (n as HTMLElement).dataset.chatKey!;
      if (!n.isConnected) continue;
      const h = n.getBoundingClientRect().height;
      const old = sizes.current.get(k);
      if (old !== undefined && Math.abs(old - h) < 0.5) continue;
      sizes.current.set(k, h);
      grew = true;
    }
    if (grew) changed();
    const lay = layout(rowsRef.current);
    const moved = grew || lay.total !== prev.total || lay.keys.length !== prev.keys.length || lay.keys[0] !== prev.keys[0];
    shown.current = lay;
    b.style.height = lay.total + "px";
    const at = new Map<string, number>();
    lay.keys.forEach((k, i) => at.set(k, i));
    for (const n of b.querySelectorAll<HTMLElement>("[data-chat-key]")) {
      const i = at.get(n.dataset.chatKey!);
      if (i === undefined) continue;
      const t = `translateY(${lay.tops[i]}px)`;
      if (n.style.transform !== t) n.style.transform = t;
    }
    // Where scrollTop belongs
    if (stuck.current && !cb.current.holding()) {
      // A touch pulling up within END keeps its offset from the end instead of being put back: a slow drag moves ~1px
      // per scroll event, and snapping each one back meant it never got past END and couldn't leave the bottom.
      // Getting stuck again is up to the touch ending (endTouch).
      const off = touch.current ? Math.min(END, Math.max(0, lastEnd.current - top)) : 0;
      const to = endOf(s) - off;
      if (!anim.current && Math.abs(to - s.scrollTop) > 0.5) s.scrollTop = to;
    } else if (a >= 0) {
      // By key. If that row is gone, the next one still there: the rows of a message cut off at the top get new keys
      // once the rest of it comes in with older messages (fling.mjs --to-top: the whole screen swapped on the first load)
      for (let j = a; j < prev.keys.length; j++) {
        const i = at.get(prev.keys[j]);
        if (i === undefined) continue;
        const t = lay.tops[i] - (prev.tops[j] - top);
        if (Math.abs(t - s.scrollTop) > 0.5) s.scrollTop = t;
        break;
      }
    }
    lastTop.current = s.scrollTop;
    lastEnd.current = endOf(s);
    const [f, t] = want(lay, s.scrollTop, s.clientHeight);
    if (f !== range.current[0] || t !== range.current[1] || lay.missing !== extra.current) {
      if (inEffect) render();
      else flushSync(render);
    }
    if (moved) cb.current.onLayout();
  };

  // Rows' sizes, before paint
  const ro = useRef<ResizeObserver | null>(null);
  if (!ro.current && typeof ResizeObserver !== "undefined")
    ro.current = new ResizeObserver((entries) => sync(entries.map((e) => e.target).filter((n) => n.isConnected)));
  const observed = useRef(new Set<Element>());
  useLayoutEffect(() => {
    const b = box.current;
    if (!b) return;
    const now = new Set<Element>(b.querySelectorAll("[data-chat-key]"));
    const fresh: Element[] = [];
    for (const n of now)
      if (!observed.current.has(n)) {
        ro.current?.observe(n);
        fresh.push(n);
      }
    for (const n of observed.current) if (!now.has(n)) ro.current?.unobserve(n);
    observed.current = now;
    // New rows (or rows in a new order) get measured and placed now, before paint
    sync(fresh, true);
  });
  useEffect(
    () => () => {
      ro.current?.disconnect();
      observed.current = new Set();
    },
    [],
  );

  // Open where it was, else at the bottom. The first chat opened in this page stays hidden for two frames: the UI runs
  // Tailwind in the browser, which writes the CSS for classes it hasn't seen yet a frame or so after they mount, so
  // every row resizes once (open.mjs --reload: 29 rows, the chat 320px shorter, everything on screen moved). Later
  // opens find those styles already there and show from the first frame: hiding them too was a blank chat for two
  // frames on every session switch (a phone screen recording).
  const ready = useRef(stylesWarm);
  useLayoutEffect(() => {
    const s = el.current;
    if (!s) return;
    if (mem.current?.w && s.clientWidth && Math.abs(mem.current.w - s.clientWidth) > 1) {
      // Another width since: those sizes are someone else's
      sizes.current.clear();
      warm.current = false;
    }
    if (mem.current && s.clientWidth) mem.current.w = s.clientWidth;
    if (startTop !== undefined) s.scrollTop = startTop;
    else s.scrollTop = endOf(s);
    if (warm.current) sync(undefined, true);
    else measureAll(true);
    let r2 = 0;
    const r1 = requestAnimationFrame(() => {
      r2 = requestAnimationFrame(() => {
        stylesWarm = true;
        if (!ready.current) {
          ready.current = true;
          flushSync(render);
        }
        cb.current.onLayout();
      });
    });
    return () => {
      cancelAnimationFrame(r1);
      cancelAnimationFrame(r2);
      clearTimeout(settle.current);
      clearTimeout(settleMax.current);
      stopAnim();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // The scroller's own size: the keyboard, the IME candidate bar, a seam dragged, a pane zoomed. Width changes reflow
  // every row, but only the mounted ones are measured again: the rest keep their size from the old width as an estimate
  // and are measured when they mount, which the top row's anchor in sync absorbs. Mounting the whole chat to measure it
  // here (as on opening) cost ~700ms per pane zoom on a long session, more with every page of older messages loaded.
  useEffect(() => {
    const s = el.current;
    if (!s) return;
    let w = s.clientWidth;
    const r = new ResizeObserver(() => {
      // Hidden (display:none) reads as width 0: not a width change, it comes back at the old one
      if (!s.clientWidth) return;
      const reflow = Math.abs(s.clientWidth - w) > 1;
      w = s.clientWidth;
      if (mem.current) mem.current.w = w;
      sync(reflow ? undefined : []);
    });
    r.observe(s);
    return () => r.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const animate = (target: () => number | undefined) => {
    stopAnim();
    const s = el.current;
    if (!s) return;
    // From far away, start a screen and a half short: no need to fly over (and mount) everything in between
    const t0 = target();
    if (t0 !== undefined && Math.abs(t0 - s.scrollTop) > s.clientHeight * 1.5) s.scrollTop = t0 - Math.sign(t0 - s.scrollTop) * s.clientHeight * 1.5;
    const a = { raf: 0, target };
    const step = () => {
      const t = Math.max(0, Math.min(endOf(s), a.target() ?? s.scrollTop));
      const d = t - s.scrollTop;
      if (Math.abs(d) < 1) {
        s.scrollTop = t;
        anim.current = null;
        sync([]);
        return;
      }
      s.scrollTop += Math.abs(d) * 0.2 < 2 ? Math.sign(d) * Math.min(2, Math.abs(d)) : d * 0.2;
      a.raf = requestAnimationFrame(step);
    };
    anim.current = a;
    a.raf = requestAnimationFrame(step);
  };
  useImperativeHandle(ref, () => ({
    scrollToIndex: ({ index, align, behavior }) => {
      const s = el.current;
      if (!s) return;
      clearTimeout(settleTouch.current);
      touch.current = false;
      if (index === "LAST") {
        setStuck(true);
        if (behavior === "smooth") animate(() => endOf(s));
        else {
          stopAnim();
          s.scrollTop = endOf(s);
        }
        return;
      }
      const key = rowsRef.current[index]?.key;
      if (!key) return;
      setStuck(false);
      const target = () => {
        const lay = shown.current;
        const i = lay.keys.indexOf(key);
        if (i < 0) return undefined;
        return align === "end" ? lay.tops[i] + lay.sizes[i] - s.clientHeight : lay.tops[i];
      };
      if (behavior === "smooth") animate(target);
      else {
        stopAnim();
        const t = target();
        if (t !== undefined) s.scrollTop = t;
      }
    },
    scrollTo: (top) => {
      const s = el.current;
      if (!s) return;
      clearTimeout(settleTouch.current);
      touch.current = false;
      setStuck(false);
      stopAnim();
      s.scrollTop = Math.max(0, top);
    },
  }));

  const endTouch = () => {
    clearTimeout(settleTouch.current);
    const was = touch.current;
    touch.current = false;
    const s = el.current;
    if (!was || !s) return;
    if (!stuck.current && endOf(s) - s.scrollTop <= END) setStuck(true);
    sync([]); // stuck: back to the end
  };
  const waitSettle = () => {
    clearTimeout(settleTouch.current);
    settleTouch.current = window.setTimeout(endTouch, SETTLE);
  };

  // Which way the user scrolls, from the input. Wheel and keys say it outright; touches see the touch ref above.
  useEffect(() => {
    const s = el.current;
    if (!s) return;
    const up = () => {
      stopAnim();
      upAt.current = performance.now();
      if (s.scrollTop > 0) setStuck(false);
    };
    const down = () => {
      stopAnim();
      upAt.current = -Infinity;
    };
    const wheel = (e: WheelEvent) => {
      if (e.deltaY < 0) return up();
      if (e.deltaY <= 0) return;
      down();
      // Back down to the end (the scroll is usually applied before this event): follow again. Also covers a wheel down
      // at the end, which fires no scroll event.
      if (!stuck.current && !touch.current && endOf(s) - s.scrollTop <= END) {
        setStuck(true);
        sync([]);
      }
    };
    const tstart = () => {
      stopAnim();
      clearTimeout(settleTouch.current);
      touch.current = true;
      finger.current = true;
    };
    const tend = (e: TouchEvent) => {
      finger.current = e.touches.length > 0;
      if (!finger.current && touch.current) waitSettle();
    };
    const key = (e: KeyboardEvent) => {
      if (["ArrowUp", "PageUp", "Home"].includes(e.key)) up();
      else if (["ArrowDown", "PageDown", "End", " "].includes(e.key)) down();
    };
    // The scrollbar: a press on the scroller itself, not on a row
    const press = (e: PointerEvent) => {
      if (e.target !== s) return;
      stopAnim();
      dragging.current = true;
    };
    const release = () => (dragging.current = false);
    s.addEventListener("wheel", wheel, { passive: true });
    s.addEventListener("touchstart", tstart, { passive: true });
    window.addEventListener("touchend", tend, { passive: true });
    window.addEventListener("touchcancel", tend, { passive: true });
    s.addEventListener("keydown", key);
    s.addEventListener("pointerdown", press, { passive: true });
    window.addEventListener("pointerup", release);
    window.addEventListener("pointercancel", release);
    return () => {
      s.removeEventListener("wheel", wheel);
      s.removeEventListener("touchstart", tstart);
      clearTimeout(settleTouch.current);
      window.removeEventListener("touchend", tend);
      window.removeEventListener("touchcancel", tend);
      s.removeEventListener("keydown", key);
      s.removeEventListener("pointerdown", press);
      window.removeEventListener("pointerup", release);
      window.removeEventListener("pointercancel", release);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => () => scrollerRef(null), []); // eslint-disable-line react-hooks/exhaustive-deps

  // Render: the rows the range covers, from the layout for these rows and the last known scrollTop. sync corrects it
  // before paint if that was off (new sizes, a new scrollTop).
  const lay = layout(rows);
  const s0 = el.current;
  const [from, to] = want(lay, s0 ? s0.scrollTop : lastTop.current, s0 ? s0.clientHeight : 2000);
  range.current = [from, to];
  const idx: number[] = [];
  for (let i = 0; i < from; i++) if (!sizes.current.has(rows[i].key)) idx.push(i);
  for (let i = from; i < to; i++) idx.push(i);
  for (let i = to; i < rows.length; i++) if (!sizes.current.has(rows[i].key)) idx.push(i);
  extra.current = lay.missing;
  const items = [];
  for (const i of idx) {
    const r = rows[i];
    items.push(
      <div key={r.key} data-chat-key={r.key} data-index={i} data-item-index={firstIdx + i} className="absolute inset-x-0 top-0">
        <RowView row={r} env={env} />
      </div>,
    );
  }
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
        const dist = endOf(s) - s.scrollTop;
        const wentUp = s.scrollTop < lastTop.current - 0.5;
        if (dragging.current && wentUp && dist > END) setStuck(false);
        // A touch decides when it ends (see touch); its fling keeps it going
        if (touch.current) {
          if (!finger.current) waitSettle();
        }
        // Back at the very bottom (scrolled or dragged down to it): follow again. Not right after a wheel / key up: those
        // can move less than 0.5px a step (upAt)
        else if (!stuck.current && dist <= END && !wentUp && performance.now() - upAt.current > UP_HOLD) setStuck(true);
        // Off the bottom while following it, not by the user: the browser clamped the scroll position in a layout where
        // the list was briefly shorter (the composer measuring itself did that). Back to the end.
        else if (stuck.current && dist > END && !anim.current && !cb.current.holding()) s.scrollTop = endOf(s);
        sync([]);
        cb.current.onScroll(s);
      }}
    >
      <div ref={box} data-chat-list="" className="relative w-full" style={{ visibility: ready.current ? undefined : "hidden" }}>
        <div className="absolute inset-x-0 top-0 flex h-8 items-center justify-center text-[12px] text-muted-foreground">
          {older === "loading" && <Loader2 className="size-3.5 animate-spin" />}
          {older === "more" && tr("chat.scrollForOlder")}
        </div>
        {items}
      </div>
    </div>
  );
});
