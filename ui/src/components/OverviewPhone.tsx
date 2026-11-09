import { ClipboardCheck } from "lucide-react";
import { type ReactNode, useEffect, useLayoutEffect, useRef, useState } from "react";
import { focusTile, setOverview } from "@/lib/overview";
import { phoneOrder } from "@/lib/overviewState";
import { useCaughtUp } from "@/lib/catchup";
import { useStore } from "@/lib/store";
import { syncWatch } from "@/lib/sync";
import { GAP, useSwipePager } from "@/lib/swipePager";
import { TabActive } from "./Panes";
import { AddMenu, PickButton } from "./OverviewPick";
import { DrawerButton } from "./DrawerButton";
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
  const pager = useSwipePager(body, track, tile, (step) => sorted[cur + step], progress, focusTile);

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
  // until the track comes to rest (pager `onRest`). A page drawn before shows the picture it had when it went (Snapshot)
  // instead of a blank: a run of quick swipes used to flash the empty background (reported).
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

  return (
    <TabActive.Provider value={shown}>
      <div className="flex h-full flex-col bg-background">
        {/* No text selection: a long-press on PhoneBar's overview button opens this with the finger still down on this
        bar, and Android then turned that long-press into selecting a chip's title */}
        <div className="flex h-11 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5 select-none [-webkit-touch-callout:none]">
          {/* ☰ like the main view's (asked for), same size and spot as PhoneBar's: the drawer is the way out too (its rows
          leave the overview) */}
          <DrawerButton />
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
                  {live(id) ? (
                    <Snap id={id}>
                      <Tile id={id} focused={shown && at === 0} main={false} zoomed={false} target={false} phone onFocus={() => {}} onZoom={() => {}} />
                    </Snap>
                  ) : (
                    <Snapshot id={id} />
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

// The last picture of each page taken down (Snap), most recent last: shown in its place until the page is mounted again
const snaps = new Map<string, { el: HTMLElement; scroll: number[] }>();
const SNAPS = 12;
// Attributes the rest of the UI finds live chats by (document-wide queries: the composer, messages, the scroller) or
// that would make a copy act (autoplay, autofocus): a snapshot is only a picture
const LIVE_ATTRS = ["id", "role", "autofocus", "autoplay", "data-composer", "data-message", "data-chat-scroller", "data-chat-list", "data-ov-tile"];

/**
 * Wraps a mounted page and copies its DOM as it is taken down. Cloning is plain DOM work (a few ms for the rows a
 * virtual list keeps), far cheaper than mounting the chat again, which is why pages are left blank mid-swipe at all.
 * The copy keeps the chat scrolled where it was (scrollTop isn't part of a clone).
 */
function Snap({ id, children }: { id: string; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    // Runs before React detaches the DOM (deletion runs a component's layout cleanups before removing its nodes)
    return () => {
      if (!el?.isConnected) return;
      const scroll = [...el.querySelectorAll<HTMLElement>("[data-chat-scroller]")].map((s) => s.scrollTop);
      const copy = el.cloneNode(true) as HTMLElement;
      copy.querySelectorAll("iframe, canvas").forEach((n) => n.remove());
      copy.querySelectorAll("[data-chat-scroller]").forEach((n) => n.setAttribute("data-snap-scroller", ""));
      for (const a of LIVE_ATTRS) copy.querySelectorAll(`[${a}]`).forEach((n) => n.removeAttribute(a));
      snaps.delete(id);
      snaps.set(id, { el: copy, scroll });
      if (snaps.size > SNAPS) snaps.delete(snaps.keys().next().value!);
    };
  }, [id]);
  return (
    <div ref={ref} className="h-full">
      {children}
    </div>
  );
}

/** A page's last picture (Snap), inert, until the page is mounted again; empty if it was never drawn. */
function Snapshot({ id }: { id: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const box = ref.current;
    const snap = snaps.get(id);
    if (!box || !snap) return;
    // A copy can be shown once at a time (a node has one parent); the same page is never on two pages of the track
    const copy = snap.el;
    box.replaceChildren(copy);
    copy.querySelectorAll<HTMLElement>("[data-snap-scroller]").forEach((s, i) => {
      s.scrollTop = snap.scroll[i] ?? 0;
    });
    return () => copy.remove();
  }, [id]);
  return <div ref={ref} aria-hidden inert className="pointer-events-none h-full" />;
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
