import { ClipboardCheck, Menu } from "lucide-react";
import { type RefObject, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { focusTile, setOverview } from "@/lib/overview";
import { phoneOrder } from "@/lib/overviewState";
import { useCaughtUp } from "@/lib/catchup";
import { set, useStore } from "@/lib/store";
import { syncWatch } from "@/lib/sync";
import { TabActive } from "./Panes";
import { AddMenu, PickButton } from "./OverviewPick";
import { Tile, TileChip } from "./OverviewTile";
import { t } from "@/lib/i18n";

/**
 * The overview on a phone: no room for a grid, so one tile at a time. The top bar is the tile switcher, one chip per
 * tile tinted with its status (waiting / running / unseen readable at a glance, asked for); the list of agents is the
 * sidebar drawer (UnifiedSidebar), whose 进行中 rows pick the tile here. The tiles are pages side by side: a sideways
 * swipe drags the next one in under the finger (asked for: like turning a page, not a cut). Only the shown tile and
 * its two neighbours are mounted (a phone can't afford a dozen live chats; store.watchedSessions streams the same
 * three); the rest keep their transcripts cached.
 */
export function PhoneOverview({ ids, focus, shown }: { ids: string[]; focus?: string; shown: boolean }) {
  const chips = useRef<HTMLDivElement>(null);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const frozen = useStore((s) => s.overview.phone);
  // Chips in the Code sidebar's agent-list order (asked for: what needs you first), sorted once on opening and then
  // held (phoneOrder): a swipe has to land where the strip said it would
  const sorted = phoneOrder(ids, sessions, unread, frozen);
  // Take the order on opening, drop it on leaving (the next opening sorts afresh). A layout effect: the first frame
  // already sorts the same way, so nothing moves when the frozen order arrives.
  useLayoutEffect(() => {
    if (shown && !frozen) setOverview((o) => ({ ...o, phone: phoneOrder(ids, sessions, unread) }));
    else if (!shown && frozen) setOverview((o) => ({ ...o, phone: undefined }));
  }, [shown, frozen]);
  // The one switched to from a chip or the list may not be loaded or caught up: keep the current tile up until it is
  // (lib/catchup) instead of drawing "loading" / the old state and then flashing to the new.
  const tile = useCaughtUp(focus, focus ? [focus] : [], (id) => sorted.includes(id));
  const cur = tile ? sorted.indexOf(tile) : -1;
  const body = useRef<HTMLDivElement>(null);
  const track = useRef<HTMLDivElement>(null);

  // While a page is dragged or slides into place, the chip strip shows how far: the selection box moves from the
  // current chip towards the one coming in, the texts brighten / dim, and the strip scrolls along (asked for: not a
  // jump when the page has turned). Fed every frame from the track's real position, so the release animation and a
  // spring back are followed too. Plain DOM writes: a React render per frame would cost the drag its smoothness.
  // `s0` is the strip's scroll when the run of swipes began: every chip's in-view scroll is measured from it, so
  // the strip stays continuous when the current chip changes mid-slide.
  const box = useRef<HTMLDivElement>(null);
  const moving = useRef<{ s0: number; styled: Set<HTMLElement> } | null>(null);
  const progress = (x: number | null) => {
    const strip = chips.current;
    const ind = box.current;
    if (!strip || !ind) return;
    const m = moving.current;
    if (x === null) {
      for (const el of m?.styled ?? []) el.style.color = el.style.borderColor = "";
      moving.current = null;
      ind.style.display = "";
      return;
    }
    const kids = strip.children as HTMLCollectionOf<HTMLElement>;
    const a = kids[cur];
    if (!a) return;
    const mv = m ?? (moving.current = { s0: strip.scrollLeft, styled: new Set() });
    const p = Math.max(-1, Math.min(1, -x / ((body.current?.clientWidth ?? 1) + GAP)));
    const step = p > 0 ? 1 : -1;
    const b = sorted[cur + step] ? kids[cur + step] : undefined;
    const k = b ? Math.abs(p) : 0;
    const to = b ?? a;
    const mix = (u: number, v: number) => u + (v - u) * k;
    Object.assign(ind.style, {
      display: "block",
      left: `${mix(a.offsetLeft, to.offsetLeft)}px`,
      width: `${mix(a.offsetWidth, to.offsetWidth)}px`,
      top: `${a.offsetTop}px`,
      height: `${a.offsetHeight}px`,
    });
    strip.scrollLeft = mix(inView(strip, a, mv.s0), inView(strip, to, mv.s0));
    // The box stands in for the chips' own border meanwhile; the text brightens on the chip coming in as it dims on
    // the current one (selected = foreground, the rest muted)
    for (const el of mv.styled) if (el !== a && el !== b) ((el.style.color = el.style.borderColor = ""), mv.styled.delete(el));
    for (const [el, v] of [
      [a, 1 - k],
      [b, k],
    ] as const) {
      if (!el) continue;
      el.style.borderColor = "transparent";
      el.style.color = shade(v);
      mv.styled.add(el);
    }
  };
  const pager = useSwipePager(body, track, tile, (step) => sorted[cur + step], progress);

  // Keep the current chip in view (on opening, switching from the list, a removal, a reorder). Scrolls the strip itself after layout:
  // scrollIntoView also moves ancestors, and run before the chips were laid out it left the current one off screen.
  // Not while swipes run: progress scrolls it then.
  const idsKey = sorted.join(",");
  useLayoutEffect(() => {
    const strip = chips.current;
    const chip = strip?.querySelector<HTMLElement>("[data-current]");
    if (strip && chip && !moving.current) strip.scrollLeft = inView(strip, chip, strip.scrollLeft);
  }, [focus, idsKey, shown]);

  // The pages either side of the drawn tile, in the strip's order (finger left → the next chip)
  const pages = cur < 0 ? [] : ([-1, 0, 1] as const).map((at) => ({ at, id: sorted[cur + at] })).filter((p) => p.id);
  // A page turned to brings a new neighbour, and mounting a chat is the heaviest thing here: done in the middle of the
  // slide it stuttered (reported). So while swipes run, only pages already mounted are drawn, the new ones are blank
  // until the track comes to rest (pager `onRest`); a run of quick swipes flies over blanks rather than stuttering.
  const [awake, setAwake] = useState(tile);
  const mounted = useRef(new Set<string>());
  const live = (id: string) => awake === tile || mounted.current.has(id);
  useEffect(() => {
    mounted.current = new Set(pages.map((p) => p.id).filter(live));
  });
  useEffect(() => pager.onRest(() => setAwake(tile)), [tile]);
  // A neighbour can change without the layout changing (a status reorders the chips): stream the new one
  const pagesKey = pages.map((p) => p.id).join(",");
  useEffect(() => {
    if (shown) syncWatch();
  }, [pagesKey, shown]);

  const openDrawer = () => {
    // Drop the keyboard first: with it up, the first tap in the drawer would only close the keyboard (as PhoneBar)
    (document.activeElement as HTMLElement | null)?.blur?.();
    set({ drawer: true });
  };
  return (
    <TabActive.Provider value={shown}>
      <div className="flex h-full flex-col bg-background">
        {/* No text selection: a long-press on PhoneBar's overview button opens this with the finger still down on this
        bar, and Android then turned that long-press into selecting a chip's title */}
        <div className="flex h-11 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5 select-none [-webkit-touch-callout:none]">
          {/* ☰ like the main view's (asked for), same size and spot as PhoneBar's: the drawer is the way out too (its rows
          leave the overview) */}
          <Button variant="ghost" size="icon" className="size-9 shrink-0" aria-label={t("phoneBar.sidebar")} onClick={openDrawer}>
            <Menu className="size-[18px]" />
          </Button>
          {/* Both scrollbar hiders: Android's WebView ignores scrollbar-width and drew a bar under the chips */}
          {/* relative: the chips' offsetLeft is then measured in the strip's own scrolled coordinates (inView, the box) */}
          <div ref={chips} className="relative flex min-w-0 flex-1 gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            {sorted.map((id) => (
              <TileChip key={id} id={id} current={id === focus} className="h-8 max-w-36" onClick={() => focusTile(id)} />
            ))}
            {/* The selection box while a page is dragged (progress above); last, so the chips keep their indexes */}
            <div ref={box} aria-hidden className="pointer-events-none absolute hidden rounded-md border border-brand" />
          </div>
          <AddMenu focus={tile} />
        </div>
        {/* The gap between two pages while one is dragged shows this background */}
        <div ref={body} className="relative min-h-0 flex-1 overflow-hidden bg-sidebar">
          {tile ? (
            <div ref={track} className="absolute inset-0">
              {/* Keyed by session: after a swipe the page dragged in becomes the middle one without remounting */}
              {pages.map(({ at, id }) => (
                <div
                  key={id}
                  className="absolute inset-0 bg-background"
                  style={at ? { transform: `translateX(calc(${at * 100}% + ${at * GAP}px))` } : undefined}
                  inert={at !== 0}
                >
                  {live(id) && (
                    <Tile id={id} focused={shown && at === 0} main={false} zoomed={false} target={false} phone onFocus={() => {}} onZoom={() => {}} />
                  )}
                </div>
              ))}
            </div>
          ) : (
            <div className="flex h-full flex-col items-center justify-center gap-3 bg-background px-8 text-center text-[13px] text-muted-foreground">
              <ClipboardCheck className="size-6" />
              {t("nav.activeEmpty")}
              <PickButton />
            </div>
          )}
        </div>
      </div>
    </TabActive.Provider>
  );
}

const GAP = 8; // px between two pages
const SLOP = 10; // px before a touch counts as a drag and its direction is decided
const TURN = 0.35; // of the page width dragged: let go past it and the page turns
const FLICK = 0.3; // px/ms: a flick this fast towards a neighbour turns the page however short it was
const EDGE = 20; // px at the screen's sides left to the system's back gesture (Android) / history swipe (iOS)
const EASE = "cubic-bezier(0.25, 1, 0.5, 1)"; // fast start, long settle: carries on the finger's speed

/**
 * The pager's gesture. The track of pages follows the finger 1:1 (rubber-banding where there is no neighbour); on
 * release it slides on to the next page or back, at the finger's speed.
 * Turning switches the tile at once (focusTile), not after the slide: the pages re-centre on the new tile and the
 * track's offset is moved by a page width in the same frame (the layout effect on `tile`), so the picture doesn't
 * move and the slide carries on from there. That keeps the track's offset always relative to the tile shown, so a
 * finger can land mid-slide and grab it where it is: quick swipes in a row run through the pages (asked for; it used
 * to ignore touches until each slide had finished, then switch).
 * Native listeners, not React's: React bubbles events through portals, so a swipe in the image lightbox (portaled to
 * <body>, it pages its own images) would also have turned the page. Left alone: touches that start in something
 * that scrolls sideways (code blocks, tables), in a text box (moving the caret / selecting), in a terminal, or with
 * text selected; and two-finger touches (pinch).
 */
function useSwipePager(
  body: RefObject<HTMLDivElement | null>,
  track: RefObject<HTMLDivElement | null>,
  tile: string | undefined,
  neighbour: (step: 1 | -1) => string | undefined,
  progress: (x: number | null) => void,
) {
  const pick = useRef(neighbour);
  pick.current = neighbour;
  const report = useRef(progress);
  report.current = progress;
  const width = () => (body.current?.clientWidth ?? 0) + GAP;
  const offset = () => {
    const m = track.current && getComputedStyle(track.current).transform;
    return m && m !== "none" ? new DOMMatrix(m).m41 : 0;
  };
  const place = (dx: number, ms = 0) => {
    const el = track.current;
    if (!el) return;
    el.style.transition = ms ? `transform ${ms}ms ${EASE}` : "";
    el.style.transform = dx ? `translateX(${dx}px)` : "";
  };
  // The rest of a slide at the finger's speed (at least a brisk one), within reason
  const slideMs = (dist: number, v: number) => Math.round(Math.min(340, Math.max(160, Math.abs(dist) / Math.max(Math.abs(v), 1.2))));

  // From the first drag until the track comes to rest: its offset every frame for the chip strip (null at rest)
  const raf = useRef(0);
  const follow = () => {
    if (raf.current) return;
    const tick = () => {
      report.current(offset());
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

  // A turn whose tile hasn't switched yet: the step it went and the finger's speed to carry on at
  const turning = useRef<{ step: 1 | -1; v: number } | null>(null);
  useLayoutEffect(() => {
    const tn = turning.current;
    turning.current = null;
    if (!tn) {
      // Switched some other way (a chip, the list): no slide
      place(0);
      return rest();
    }
    // The pages are centred on the new tile now: the same picture is a page width over. Then on to 0.
    const x = offset() + tn.step * width();
    place(x);
    void track.current?.offsetWidth; // let the browser take that as the start before the transition
    settle(0, slideMs(x, tn.v));
  }, [tile]);

  useEffect(() => {
    const el = body.current;
    if (!el) return;
    let g: { x: number; y: number; base: number; caught: boolean; mode: "wait" | "swipe" | "off"; off: number; trail: { x: number; t: number }[] } | null =
      null;
    // Where the track goes for a drag of `d` from `base`: 1:1 towards a neighbour, rubber-band past the last page
    const at = (base: number, d: number) => {
      const off = Math.max(-width(), Math.min(width(), base + d));
      return pick.current(off < 0 ? 1 : -1) ? off : off * 0.25;
    };
    const release = (off: number, v: number) => {
      const step = off < 0 ? 1 : -1;
      const id = pick.current(step);
      const w = width();
      const turn = !!id && off !== 0 && (Math.abs(off) > w * TURN || (Math.abs(v) > FLICK && Math.sign(v) === Math.sign(off)));
      if (!turn || !id) return settle(0, slideMs(off, v));
      // Slide on meanwhile; the layout effect above takes over once the tile has switched (at once, unless it is
      // held until loaded: lib/catchup)
      place(-step * w, slideMs(-step * w - off, v));
      turning.current = { step, v };
      clearTimeout(timer.current);
      // The tile didn't switch after all (taken out meanwhile…): back to where it was
      timer.current = window.setTimeout(() => turning.current && ((turning.current = null), settle(0, 200)), 900);
      focusTile(id);
    };
    const start = (e: TouchEvent) => {
      g = null;
      // A turn waiting for its tile to load: the track's offset isn't the shown tile's yet, nothing to grab
      if (turning.current || e.touches.length !== 1) return;
      const p = e.touches[0];
      if (p.clientX < EDGE || p.clientX > window.innerWidth - EDGE) return;
      if (!(e.target instanceof Element) || skip(e.target, el)) return;
      if (String(window.getSelection() ?? "")) return;
      // Mid-slide: catch the track where it is (the touch may turn out a tap or a scroll: then it slides on, below)
      const caught = !!raf.current;
      const base = caught ? offset() : 0;
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

/** Where the strip `strip` has to scroll, from `from`, to show `chip` whole: the least move (none if it already shows). */
function inView(strip: HTMLElement, chip: HTMLElement, from: number) {
  const left = chip.offsetLeft;
  if (left < from) return left;
  if (left + chip.offsetWidth > from + strip.clientWidth) return left + chip.offsetWidth - strip.clientWidth;
  return from;
}

/** A chip's text colour `k` of the way from unselected (0, muted) to selected (1, foreground). */
function shade(k: number) {
  return `color-mix(in oklab, var(--foreground) ${Math.round(k * 100)}%, var(--muted-foreground))`;
}
