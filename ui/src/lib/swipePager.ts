import { type RefObject, useEffect, useLayoutEffect, useRef } from "react";

export const GAP = 8; // px between two pages
const SLOP = 10; // px before a touch counts as a drag and its direction is decided
const TURN = 0.35; // of the page width dragged: let go past it and the page turns
const FLICK = 0.3; // px/ms: a flick this fast towards a neighbour turns the page however short it was
const EDGE = 20; // px at the screen's sides left to the system's back gesture (Android) / history swipe (iOS)
const EASE = "cubic-bezier(0.25, 1, 0.5, 1)"; // fast start, long settle: carries on the finger's speed
/** EASE as a function of time (0..1 → 0..1), for the slides WebKit runs frame by frame. */
function ease(t: number) {
  // cubic-bezier(0.25, 1, 0.5, 1): find the curve parameter whose x is t (bisection), return its y
  const [x1, y1, x2, y2] = [0.25, 1, 0.5, 1];
  const bz = (u: number, a: number, b: number) => 3 * a * u * (1 - u) ** 2 + 3 * b * u ** 2 * (1 - u) + u ** 3;
  let lo = 0,
    hi = 1;
  for (let i = 0; i < 20; i++) {
    const mid = (lo + hi) / 2;
    if (bz(mid, x1, x2) < t) lo = mid;
    else hi = mid;
  }
  return bz((lo + hi) / 2, y1, y2);
}

// Safari and everything on iOS (all of it WebKit: CriOS / FxiOS say so, not "Chrome")
const WEBKIT = typeof navigator !== "undefined" && /AppleWebKit/.test(navigator.userAgent) && !/Chrome|Chromium|Android/.test(navigator.userAgent);

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
  neighbour: (step: number) => string | undefined,
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
  const homeAt = (i: number) => (shown.current === undefined ? 0 : -Math.max(0, i) * width());
  // A turn on its way: the page the track is heading for, and when the slide gets there. The tile switches to it a frame
  // later (WebKit: once the slide has ended), so until then the track can be ahead of the tile shown; a finger landing
  // meanwhile carries on from that page (quick swipes in a row, asked for).
  const goal = useRef<{ i: number; end: number } | null>(null);
  const home = () => homeAt(goal.current?.i ?? idx.current);
  // The page `step` away from where the track is heading
  const next = (step: number) => pick.current((goal.current?.i ?? idx.current) - idx.current + step);
  // The track's position now (mid-transition too)
  const offset = () => {
    const m = track.current && getComputedStyle(track.current).transform;
    return m && m !== "none" ? new DOMMatrix(m).m41 : 0;
  };
  // A slide WebKit runs as a CSS transition (place below): the frame loop moving the track, if one runs
  const anim = useRef(0);
  const set = (el: HTMLElement, x: number) => void (el.style.transform = x ? `translateX(${x}px)` : "");
  const place = (x: number, ms = 0) => {
    const el = track.current;
    if (!el) return;
    if (anim.current) cancelAnimationFrame(anim.current);
    anim.current = 0;
    if (!WEBKIT || !ms) {
      el.style.transition = ms ? `transform ${ms}ms ${EASE}` : "";
      return set(el, x);
    }
    // WebKit (iOS): moved frame by frame like the drag, on the same curve. Its CSS transition made two long frames
    // (~50 and ~100ms on an iPhone X) as it started, every release (it prepares the layer along the whole path), while a
    // drag moving the track each frame holds 60fps there.
    el.style.transition = "";
    const from = offset();
    const t0 = performance.now();
    const step = (now: number) => {
      const k = Math.min(1, (now - t0) / ms);
      set(el, from + (x - from) * ease(k));
      anim.current = k < 1 ? requestAnimationFrame(step) : 0;
    };
    anim.current = requestAnimationFrame(step);
  };
  useEffect(() => () => cancelAnimationFrame(anim.current), []);
  // The rest of a slide at the finger's speed (at least a brisk one), within reason
  const slideMs = (dist: number, v: number) => Math.round(Math.min(340, Math.max(160, Math.abs(dist) / Math.max(Math.abs(v), 1.2))));

  // From the first drag until the track comes to rest: its offset from the shown page every frame for the chip strip
  const raf = useRef(0);
  const follow = () => {
    if (raf.current) return;
    const tick = () => {
      report.current(offset() - homeAt(idx.current));
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

  // Switching the tile to the page a turn heads for: a frame after the release, WebKit once the slide has ended (its
  // ~250ms of render would otherwise hold back the slide: it ran as a CSS transition, handed to the compositor late,
  // and the page jumped there with no slide; user report). A finger landing first puts it off to its own release.
  const switchT = useRef(0);
  const scheduleSwitch = (ms: number) => {
    const gl = goal.current;
    if (!gl) return;
    const id = next(0);
    if (!id) return;
    const run = () => goal.current === gl && gl.i !== idx.current && goTo.current(id);
    clearTimeout(switchT.current);
    if (WEBKIT) switchT.current = window.setTimeout(run, ms + 30);
    else requestAnimationFrame(() => setTimeout(run));
  };
  useEffect(() => () => clearTimeout(switchT.current), []);
  useLayoutEffect(() => {
    const gl = goal.current;
    if (!gl) {
      // Switched some other way (a chip, the list), or the pages moved under it: there, no slide
      place(home());
      return rest();
    }
    // A switch on the way to a page further on (quick swipes in a row): the slide is heading there, let it go on
    if (gl.i !== idx.current) return;
    // The tile is the page the slide heads for: nothing to move, rest once it arrives
    goal.current = null;
    clearTimeout(timer.current);
    timer.current = window.setTimeout(rest, Math.max(0, gl.end - performance.now()) + 20);
  }, [tile, index]);

  // A resize at rest (rotation, the keyboard's width on a tablet): the shown page's place moves with the width
  useEffect(() => {
    const el = body.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => !raf.current && !goal.current && place(home()));
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
      return h + (next(off < 0 ? 1 : -1) ? off : off * 0.25);
    };
    // Let go with the track at `pos`, moving at `v`
    const release = (pos: number, v: number) => {
      const off = pos - home();
      const step = off < 0 ? 1 : -1;
      const id = next(step);
      const w = width();
      const turn = !!id && off !== 0 && (Math.abs(off) > w * TURN || (Math.abs(v) > FLICK && Math.sign(v) === Math.sign(off)));
      if (!turn || !id) {
        const ms = slideMs(off, v);
        settle(home(), ms);
        // Caught mid-turn and let go short of another: the turn still ends where it was heading
        return scheduleSwitch(ms);
      }
      // Slide on to the next page's place; once the tile has switched (lib/catchup may hold it until loaded) that is its
      // rest and the layout effect above only waits for the slide to end
      const i = (goal.current?.i ?? idx.current) + step;
      const to = homeAt(i);
      const ms = slideMs(to - pos, v);
      place(to, ms);
      const t = (goal.current = { i, end: performance.now() + ms });
      clearTimeout(timer.current);
      // The tile didn't switch after all (taken out meanwhile…): back to the one shown
      timer.current = window.setTimeout(() => goal.current === t && ((goal.current = null), settle(home(), 200)), ms + 1200);
      scheduleSwitch(ms);
    };
    const start = (e: TouchEvent) => {
      // A second finger joining a swipe (or one grabbing a slide): finish that one first. Dropping it left the track
      // where it was, halfway between two pages (user report: a two-finger tap stuck it there)
      if (g && (g.mode === "swipe" || g.caught)) release(g.off, 0);
      g = null;
      // No page (nothing to turn, or the pager is off: Settings on a wider screen): nothing to grab. A turn whose tile
      // hasn't switched yet can be grabbed: the drag goes on from the page it was heading for (goal)
      if (!shown.current || e.touches.length !== 1) return;
      const p = e.touches[0];
      if (p.clientX < EDGE || p.clientX > window.innerWidth - EDGE) return;
      if (!(e.target instanceof Element) || skip(e.target, el)) return;
      if (String(window.getSelection() ?? "")) return;
      // Mid-slide: catch the track where it is (the touch may turn out a tap or a scroll: then it slides on, below)
      const caught = !!raf.current;
      const base = caught ? offset() : home();
      if (caught) {
        clearTimeout(timer.current);
        clearTimeout(switchT.current);
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
      if (!raf.current && !goal.current) fn();
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
