import { type RefObject, useEffect, useLayoutEffect, useRef } from "react";

export const GAP = 8; // px between two pages
const SLOP = 10; // px before a touch counts as a drag and its direction is decided
const TURN = 0.35; // of the page width dragged: let go past it and the page turns
const FLICK = 0.3; // px/ms: a flick this fast towards a neighbour turns the page however short it was
const EDGE = 20; // px at the screen's sides left to the system's back gesture (Android) / history swipe (iOS)
const EASE = "cubic-bezier(0.25, 1, 0.5, 1)"; // fast start, long settle: carries on the finger's speed

/**
 * Pages side by side turned by a sideways swipe (the phone overview's tiles, the phone's Settings pages). `tile` is the
 * page shown and `index` its place in the strip: the caller puts page i at translateX(i × (100% + GAP)) inside `track`
 * (pageX), and the track sits at -index × that width at rest. `neighbour` is the page a step away, `go` switches to one;
 * `progress` gets the track's offset from the shown page every frame while it moves (null at rest).
 * The pager's gesture. The track of pages follows the finger 1:1 (rubber-banding where there is no neighbour); on
 * release it slides on to the next page or back, at the finger's speed.
 * Pages sit at fixed places by index, so a turn needs nothing re-centred: the slide heads for the next page's place,
 * and once the tile switches (go: a frame later, so the slide starts on the compositor before the render) that place
 * is simply the new tile's rest. It used to centre the pages on the shown tile and, on switching, move the track by a
 * page width read back from the running transition (getComputedStyle): with the main thread busy rendering, that
 * read lagged the compositor, so the page jumped back to where the finger let go and slid again (a screen recording:
 * forward 236px, still, back, forward). A finger can still land mid-slide and grab the track where it is: quick
 * swipes in a row run through the pages (asked for).
 * Native listeners, not React's: React bubbles events through portals, so a swipe in the image lightbox (portaled to
 * <body>, it pages its own images) would also have turned the page. Left alone: touches that start in something
 * that scrolls sideways (code blocks, tables), in a text box (moving the caret / selecting), in a terminal, or with
 * text selected; and two-finger touches (pinch).
 */
export function useSwipePager(
  body: RefObject<HTMLDivElement | null>,
  track: RefObject<HTMLDivElement | null>,
  tile: string | undefined,
  index: number,
  neighbour: (step: 1 | -1) => string | undefined,
  progress: (x: number | null) => void,
  go: (id: string) => void,
) {
  const pick = useRef(neighbour);
  pick.current = neighbour;
  const report = useRef(progress);
  report.current = progress;
  const goTo = useRef(go);
  goTo.current = go;
  const shown = useRef(tile);
  shown.current = tile;
  const idx = useRef(index);
  idx.current = index;
  const width = () => (body.current?.clientWidth ?? 0) + GAP;
  // Where the track rests for the shown page (none: the pager is off, Settings on a wider screen, the track untouched)
  const home = () => (shown.current === undefined ? 0 : -Math.max(0, idx.current) * width());
  // The track's position now (mid-transition too)
  const offset = () => {
    const m = track.current && getComputedStyle(track.current).transform;
    return m && m !== "none" ? new DOMMatrix(m).m41 : 0;
  };
  const place = (x: number, ms = 0) => {
    const el = track.current;
    if (!el) return;
    el.style.transition = ms ? `transform ${ms}ms ${EASE}` : "";
    el.style.transform = x ? `translateX(${x}px)` : "";
  };
  // The rest of a slide at the finger's speed (at least a brisk one), within reason
  const slideMs = (dist: number, v: number) => Math.round(Math.min(340, Math.max(160, Math.abs(dist) / Math.max(Math.abs(v), 1.2))));

  // From the first drag until the track comes to rest: its offset from the shown page every frame for the chip strip
  const raf = useRef(0);
  const follow = () => {
    if (raf.current) return;
    const tick = () => {
      report.current(offset() - home());
      raf.current = requestAnimationFrame(tick);
    };
    raf.current = requestAnimationFrame(tick);
  };
  const rested = useRef<(() => void) | null>(null);
  const rest = () => {
    clearTimeout(timer.current);
    if (raf.current) cancelAnimationFrame(raf.current);
    raf.current = 0;
    report.current(null);
    rested.current?.();
  };
  const timer = useRef(0);
  // Slide to `to` and come to rest there
  const settle = (to: number, ms: number) => {
    place(to, ms);
    clearTimeout(timer.current);
    timer.current = window.setTimeout(rest, ms + 20);
  };

  // A turn whose tile hasn't switched yet: where the slide is going and when it gets there
  const turning = useRef<{ to: number; end: number } | null>(null);
  useLayoutEffect(() => {
    const tn = turning.current;
    turning.current = null;
    if (!tn) {
      // Switched some other way (a chip, the list), or the pages moved under it: there, no slide
      place(home());
      return rest();
    }
    const left = Math.max(0, tn.end - performance.now());
    // The slide is already heading for this page's place: let it arrive. (Not there: the strip changed meanwhile.)
    if (Math.abs(tn.to - home()) > 0.5) return settle(home(), Math.max(160, left));
    clearTimeout(timer.current);
    timer.current = window.setTimeout(rest, left + 20);
  }, [tile, index]);

  // A resize at rest (rotation, the keyboard's width on a tablet): the shown page's place moves with the width
  useEffect(() => {
    const el = body.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => !raf.current && !turning.current && place(home()));
    ro.observe(el);
    return () => ro.disconnect();
  }, [body]);

  useEffect(() => {
    const el = body.current;
    if (!el) return;
    let g: { x: number; y: number; base: number; caught: boolean; mode: "wait" | "swipe" | "off"; off: number; trail: { x: number; t: number }[] } | null =
      null;
    // Where the track goes for a drag of `d` from `base`: 1:1 towards a neighbour, rubber-band past the last page
    const at = (base: number, d: number) => {
      const h = home();
      const off = Math.max(-width(), Math.min(width(), base + d - h));
      return h + (pick.current(off < 0 ? 1 : -1) ? off : off * 0.25);
    };
    // Let go with the track at `pos`, moving at `v`
    const release = (pos: number, v: number) => {
      const off = pos - home();
      const step = off < 0 ? 1 : -1;
      const id = pick.current(step);
      const w = width();
      const turn = !!id && off !== 0 && (Math.abs(off) > w * TURN || (Math.abs(v) > FLICK && Math.sign(v) === Math.sign(off)));
      if (!turn || !id) return settle(home(), slideMs(off, v));
      // Slide on to the next page's place; once the tile has switched (at once, unless it is held until loaded:
      // lib/catchup) that is its rest and the layout effect above only waits for the slide to end
      const to = home() - step * w;
      const ms = slideMs(to - pos, v);
      place(to, ms);
      const t = (turning.current = { to, end: performance.now() + ms });
      clearTimeout(timer.current);
      // The tile didn't switch after all (taken out meanwhile…): back to where it was
      timer.current = window.setTimeout(() => turning.current === t && ((turning.current = null), settle(home(), 200)), 900);
      // Switch after the slide has started, not in this touchend: the switch re-renders the overview, synchronously
      // inside a touch handler (~70ms on a phone, measured at CPU x4), and the slide only started once it was done, a
      // hitch on every release. Started first, the slide runs on the compositor while the main thread renders.
      requestAnimationFrame(() => setTimeout(() => turning.current === t && goTo.current(id)));
    };
    const start = (e: TouchEvent) => {
      // A second finger joining a swipe (or one grabbing a slide): finish that one first. Dropping it left the track
      // where it was, halfway between two pages (user report: a two-finger tap stuck it there)
      if (g && (g.mode === "swipe" || g.caught)) release(g.off, 0);
      g = null;
      // No page (nothing to turn, or the pager is off: Settings on a wider screen) or a turn waiting for its tile to
      // switch: nothing to grab
      if (!shown.current || turning.current || e.touches.length !== 1) return;
      const p = e.touches[0];
      if (p.clientX < EDGE || p.clientX > window.innerWidth - EDGE) return;
      if (!(e.target instanceof Element) || skip(e.target, el)) return;
      if (String(window.getSelection() ?? "")) return;
      // Mid-slide: catch the track where it is (the touch may turn out a tap or a scroll: then it slides on, below)
      const caught = !!raf.current;
      const base = caught ? offset() : home();
      if (caught) {
        clearTimeout(timer.current);
        place(base);
      }
      g = { x: p.clientX, y: p.clientY, base, caught, mode: "wait", off: base, trail: [{ x: p.clientX, t: e.timeStamp }] };
    };
    const drag = (e: TouchEvent) => {
      if (!g || g.mode === "off") return;
      if (e.touches.length !== 1) {
        if (g.mode === "swipe" || g.caught) release(g.off, 0);
        return void (g.mode = "off");
      }
      const p = e.touches[0];
      const dx = p.clientX - g.x;
      const dy = p.clientY - g.y;
      if (g.mode === "wait") {
        if (Math.hypot(dx, dy) < SLOP) return;
        // Clearly sideways, or it is a scroll and the rest of this touch is left alone (a caught slide goes on)
        if (Math.abs(dx) <= Math.abs(dy) * 1.5) return void ((g.mode = "off"), g.caught && release(g.base, 0));
        g.mode = "swipe";
        follow();
        // Measured from here, so the page doesn't jump by the slop when it starts to move
        g.x = p.clientX - Math.sign(dx);
      }
      // Keep the chat from scrolling up and down under a sideways swipe
      if (e.cancelable) e.preventDefault();
      g.off = at(g.base, p.clientX - g.x);
      g.trail.push({ x: p.clientX, t: e.timeStamp });
      while (g.trail.length > 2 && e.timeStamp - g.trail[0].t > 100) g.trail.shift();
      place(g.off);
    };
    const end = () => {
      if (!g) return;
      const { mode, off, base, caught, trail } = g;
      g = null;
      // A tap on a caught slide: let it go on from where it was
      if (mode === "wait" && caught) return release(base, 0);
      if (mode !== "swipe") return;
      const last = trail[trail.length - 1];
      const v = (last.x - trail[0].x) / Math.max(1, last.t - trail[0].t) || 0; // px/ms over the last 100ms
      release(off, v);
    };
    el.addEventListener("touchstart", start, { passive: true });
    el.addEventListener("touchmove", drag, { passive: false });
    el.addEventListener("touchend", end, { passive: true });
    el.addEventListener("touchcancel", end, { passive: true });
    return () => {
      el.removeEventListener("touchstart", start);
      el.removeEventListener("touchmove", drag);
      el.removeEventListener("touchend", end);
      el.removeEventListener("touchcancel", end);
      clearTimeout(timer.current);
      cancelAnimationFrame(raf.current);
      raf.current = 0;
    };
  }, [body]);

  return {
    /** Run `fn` whenever the track comes to rest (right away if it is at rest now); returns the unsubscribe. */
    onRest(fn: () => void) {
      rested.current = fn;
      if (!raf.current && !turning.current) fn();
      return () => {
        if (rested.current === fn) rested.current = null;
      };
    },
  };
}

/** Where page `i` of a pager sits inside its track (useSwipePager). */
export const pageX = (i: number) => (i ? `translateX(calc(${i * 100}% + ${i * GAP}px))` : undefined);

/** Whether a touch starting on `t` belongs to something under it rather than to the tile swipe. */
function skip(t: Element, root: Element) {
  if (t.closest("input, textarea, select, [contenteditable=''], [contenteditable='true'], .xterm, [data-no-swipe]")) return true;
  for (let n: Element | null = t; n && n !== root; n = n.parentElement) {
    if (n.scrollWidth <= n.clientWidth + 1) continue;
    const ox = getComputedStyle(n).overflowX;
    if (ox === "auto" || ox === "scroll") return true;
  }
  return false;
}
