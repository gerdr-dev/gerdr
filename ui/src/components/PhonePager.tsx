import { type ReactNode, useEffect, useLayoutEffect, useRef, useState } from "react";
import { locateSession, type Tab } from "@/lib/layout";
import { setOverview } from "@/lib/overview";
import { overviewIds, panelTiles, phoneOrder } from "@/lib/overviewState";
import { get, set, useStore } from "@/lib/store";
import { syncWatch } from "@/lib/sync";
import { GAP, pageX, useSwipePager } from "@/lib/swipePager";
import { followSwipe, setSwipePages } from "@/lib/phoneSwipe";
import { show } from "@/lib/actions/hosted";
import { focusPane } from "@/lib/actions/panes";
import { cn } from "@/lib/utils";
import { Tile } from "./OverviewTile";

/**
 * The phone's main view: the tab in front, and a sideways swipe drags the next conversation in under the finger, like
 * turning a page. It replaced the phone overview (a separate screen of one tile at a time with a strip of chips, opened
 * from a button in PhoneBar): the person wanted the main view itself to swipe (2026-11-26), so there is one screen,
 * with the pane's own title bar and everything it offers, instead of two that showed the same conversations.
 *
 * The pages are the conversations the overview paged through: a hosting panel's (core useCodeOverview, e.g. a board's
 * tasks at work or waiting), else Code's "active" (in progress), in the sidebar's "look at first" order. That order is
 * taken once and held (overview.phone, as the overview did): re-sorted live, a status change moved the pages under the
 * finger and a swipe landed somewhere unexpected. It is taken afresh each time the drawer opens, where the list shows
 * the current order. A tab in front that isn't one of them (a terminal, a file, the new-conversation page, a
 * conversation not in progress) is the page before the first, so a swipe still leads into the list.
 *
 * Each page is the conversation's tab as Code draws it (TabView), the same mounted element the tab has when it is in
 * front, so a turn neither remounts the chat nor loses its scroll. One in the list with no pane yet (a panel's task
 * not opened on this layout) is drawn as a phone tile until the turn lands on it and gives it a tab. Kept tabs are
 * drawn in a fixed order and only their transform changes: moving DOM nodes resets their scroll (App's kept tabs).
 * A neighbour not already mounted mounts once the track is at rest: mounting a chat mid-slide stuttered (measured on
 * the overview). A turn moves only the pager's own idea of the page in front; the layout follows when the phone is idle.
 */
export function PhonePager({
  kept,
  all,
  shown,
  render,
}: {
  /** The tabs App keeps mounted (its keptTabs), the one in front among them. */
  kept: string[];
  all: Map<string, Tab>;
  /** The tab drawn in front (App's, held by lib/catchup until a switched-to tab is caught up). */
  shown: string;
  render: (tab: Tab, active: boolean) => ReactNode;
}) {
  const body = useRef<HTMLDivElement>(null);
  const track = useRef<HTMLDivElement>(null);
  const drawer = useStore((s) => s.drawer);
  const frozen = useStore((s) => !!s.overview.phone);
  // The held order, as page keys: a session's tab id, or "s:<id>" for one with no pane. Followed as a string: the
  // sessions map changes with every streamed event, and the pages only care when the order or the tabs move.
  const pairs = useStore((s) => {
    const out: string[] = [];
    const seen = new Set<string>();
    for (const id of swipeOrder(s)) {
      const loc = locateSession(s.layout, id);
      const key = loc ? loc.tab.id : `s:${id}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push(`${key}|${id}`);
    }
    return out.join(",");
  });
  const pages = pairs ? pairs.split(",").map((p) => p.split("|") as [string, string]) : [];
  const keys = pages.map(([k]) => k);
  const sid = new Map(pages);
  // The page a swipe turned to, until the layout follows (commit below). Turning a page used to switch the layout's tab
  // at once: a layout change re-renders everything that follows it and saves the layout, ~50ms on the phone (perf probe,
  // every turn), landing as the slide settled or the next swipe began. The overview never paid it, a turn there only
  // moved its own focus; this is the same: the pager draws `front`, the layout catches up once the phone is idle.
  const [pick, setPick] = useState<string | undefined>(undefined);
  // The tab in front by the layout stays a page while a pick is on its way, so the page sliding out doesn't vanish
  if (!keys.includes(shown)) keys.unshift(shown);
  const front = pick && keys.includes(pick) ? pick : shown;
  const cur = keys.indexOf(front);

  // Take the order once; drop it while the drawer is open (its list sorts live), so closing it takes the order it showed
  useLayoutEffect(() => {
    if (drawer) {
      if (frozen) setOverview((o) => ({ ...o, phone: undefined }));
    } else if (!frozen) setOverview((o) => ({ ...o, phone: swipeOrder({ ...get(), overview: { ...o, phone: undefined } }) }));
  }, [drawer, frozen]);

  // The layout moved to another tab: the commit of the pick landed, or something else (the drawer, a notification)
  // switched, which wins over a pick not committed yet
  const lastShown = useRef(shown);
  useLayoutEffect(() => {
    if (lastShown.current === shown) return;
    lastShown.current = shown;
    setPick(undefined);
  }, [shown]);

  // Give the layout the page turned to: its tab focused (or one opened for a conversation with no pane)
  const commit = (key: string) => {
    const id = sid.get(key);
    if (id) return show(id);
    const tab = all.get(key);
    if (tab) focusPane(tab.focus);
  };
  const commitRef = useRef(commit);
  commitRef.current = commit;
  const go = (key: string) => setPick(key === shown ? undefined : key);

  // The top bar's title follows the drag (PhoneBar, followSwipe): fed every frame from the track's real position, so the
  // release slide and a spring back are followed too
  const progress = (x: number | null) => followSwipe(x === null ? null : -x / ((body.current?.clientWidth ?? 1) + GAP));
  const pager = useSwipePager(body, track, front, cur, (step) => keys[cur + step], progress, go);
  const prevKey = keys[cur - 1];
  const nextKey = keys[cur + 1];
  useLayoutEffect(() => setSwipePages({ shown: front, prev: prevKey, next: nextKey }), [front, prevKey, nextKey]);
  useEffect(() => () => setSwipePages({}), []);

  // Neighbours not already mounted wait until the track rests on the page in front; then, once no finger has been down
  // for a moment and the browser is idle, the layout takes the pick and the pages two steps away mount ahead, so a turn
  // brings a neighbour that is already there and nothing mounts as the slide comes to rest, when the finger lands for
  // the next swipe (the overview's measurements: a mount is ~115ms at CPU x4)
  const [awake, setAwake] = useState(front);
  useEffect(() => pager.onRest(() => setAwake(front)), [front]);
  const [far, setFar] = useState<string | undefined>(undefined);
  useEffect(() => {
    if ((far === front && !pick) || awake !== front) return;
    let idle = 0;
    let timer = 0;
    let lastTouch = 0;
    const touch = () => ((lastTouch = performance.now()), schedule());
    const schedule = () => {
      clearTimeout(timer);
      if (idle) cancelIdle(idle);
      idle = 0;
      timer = window.setTimeout(() => {
        // The pick on a plain timer: waiting for idle too held the layout back for seconds while agents stream
        if (pick) return commitRef.current(pick);
        idle = onIdle(() => (performance.now() - lastTouch < QUIET ? schedule() : setFar(front)));
      }, QUIET);
    };
    window.addEventListener("touchstart", touch, { capture: true, passive: true });
    schedule();
    return () => {
      window.removeEventListener("touchstart", touch, true);
      clearTimeout(timer);
      if (idle) cancelIdle(idle);
    };
  }, [front, awake, far, pick]);
  const mounted = useRef(new Set<string>());
  const side = [-2, -1, 1, 2].map((at) => ({ at, key: keys[cur + at] })).filter((p): p is { at: number; key: string } => !!p.key);
  const live = side.filter((p) => (Math.abs(p.at) === 1 ? awake === front : far === front) || mounted.current.has(p.key));
  useEffect(() => {
    mounted.current = new Set([front, ...live.map((p) => p.key)]);
  });
  const tabs = [...new Set([...kept, front, ...live.map((p) => p.key)].filter((k) => all.has(k)))].sort();
  const tiles = [...new Set([front, ...live.map((p) => p.key)])].filter((k) => k.startsWith("s:"));

  return (
    // data-phone-overview: found by measuring tools (the perf probe tells swipes here from scrolls in a chat)
    <div ref={body} data-phone-overview className="absolute inset-0 overflow-hidden bg-sidebar">
      {/* Its own compositor layer: without one every frame of a drag repainted and re-rastered the pages it moves
          (measured on the overview: paint 35 → 12ms, raster 30 → 13ms per frame) */}
      <div ref={track} className="absolute inset-0 will-change-transform">
        {tabs.map((id) => {
          const tab = all.get(id)!;
          const active = id === front;
          const at = keys.indexOf(id);
          const page = at >= 0 && Math.abs(at - cur) <= 2;
          return (
            <div
              key={id}
              className={cn("absolute inset-0 bg-background", !active && "pointer-events-none", !active && !page && "invisible")}
              style={{ transform: pageX(page ? at : cur) }}
              aria-hidden={!active}
              inert={!active}
            >
              {page && !active && sid.get(id) && <Watch id={sid.get(id)!} />}
              {render(tab, active)}
            </div>
          );
        })}
        {tiles.map((key) => (
          <div
            key={key}
            className={cn("absolute inset-0 bg-background", key !== front && "pointer-events-none")}
            style={{ transform: pageX(keys.indexOf(key)) }}
            aria-hidden={key !== front}
            inert={key !== front}
          >
            <Watch id={sid.get(key)!} />
            <Tile id={sid.get(key)!} focused={key === front} main={false} zoomed={false} target={false} phone onFocus={() => {}} onZoom={() => {}} />
          </div>
        ))}
      </div>
    </div>
  );
}

const QUIET = 400; // ms with no finger down before the layout takes a pick and the far pages mount
const onIdle = (f: () => void) => (typeof requestIdleCallback === "function" ? requestIdleCallback(f, { timeout: 2000 }) : window.setTimeout(f, 50));
const cancelIdle = (h: number) => (typeof cancelIdleCallback === "function" ? cancelIdleCallback(h) : clearTimeout(h));

/** The conversations the main view swipes through, in the order held (overview.phone) or the live one. */
function swipeOrder(s: ReturnType<typeof get>): string[] {
  return phoneOrder(overviewIds(s), s.sessions, s.unread, s.overview.phone, !!panelTiles(s));
}

/** Streams a neighbour page's conversation (store mountWatch), so the page dragged in is current, not stale. */
function Watch({ id }: { id: string }) {
  useEffect(() => {
    set((s) => ({ mountWatch: { ...s.mountWatch, [id]: (s.mountWatch[id] ?? 0) + 1 } }));
    syncWatch();
    return () => {
      set((s) => {
        const n = (s.mountWatch[id] ?? 1) - 1;
        const next = { ...s.mountWatch };
        if (n > 0) next[id] = n;
        else delete next[id];
        return { mountWatch: next };
      });
      syncWatch();
    };
  }, [id]);
  return null;
}
