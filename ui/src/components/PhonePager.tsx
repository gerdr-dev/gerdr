import { type ReactNode, useEffect, useLayoutEffect, useRef, useState } from "react";
import { locateSession, type Tab } from "@/lib/layout";
import { setOverview } from "@/lib/overview";
import { overviewIds, panelTiles, phoneOrder } from "@/lib/overviewState";
import { get, set, useStore } from "@/lib/store";
import { syncWatch } from "@/lib/sync";
import { pageX, useSwipePager } from "@/lib/swipePager";
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
 * the overview).
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
  if (!keys.includes(shown)) keys.unshift(shown);
  const cur = keys.indexOf(shown);

  // Take the order once; drop it while the drawer is open (its list sorts live), so closing it takes the order it showed
  useLayoutEffect(() => {
    if (drawer) {
      if (frozen) setOverview((o) => ({ ...o, phone: undefined }));
    } else if (!frozen) setOverview((o) => ({ ...o, phone: swipeOrder({ ...get(), overview: { ...o, phone: undefined } }) }));
  }, [drawer, frozen]);

  const go = (key: string) => {
    const id = sid.get(key);
    if (id) return show(id);
    const tab = all.get(key);
    if (tab) focusPane(tab.focus);
  };
  const pager = useSwipePager(body, track, shown, cur, (step) => keys[cur + step], noProgress, go);

  // Neighbours not already mounted wait until the track rests on the page in front
  const [awake, setAwake] = useState(shown);
  useEffect(() => pager.onRest(() => setAwake(shown)), [shown]);
  const mounted = useRef(new Set<string>());
  const side = [-1, 1].map((at) => ({ at, key: keys[cur + at] })).filter((p): p is { at: number; key: string } => !!p.key);
  const live = side.filter((p) => awake === shown || mounted.current.has(p.key));
  useEffect(() => {
    mounted.current = new Set(live.map((p) => p.key));
  });
  const tabs = [...new Set([...kept, ...live.map((p) => p.key).filter((k) => all.has(k))])].sort();
  const tiles = live.filter((p) => p.key.startsWith("s:"));

  return (
    // data-phone-overview: found by measuring tools (the perf probe tells swipes here from scrolls in a chat)
    <div ref={body} data-phone-overview className="absolute inset-0 overflow-hidden bg-sidebar">
      {/* Its own compositor layer: without one every frame of a drag repainted and re-rastered the pages it moves
          (measured on the overview: paint 35 → 12ms, raster 30 → 13ms per frame) */}
      <div ref={track} className="absolute inset-0 will-change-transform">
        {tabs.map((id) => {
          const tab = all.get(id)!;
          const active = id === shown;
          const at = keys.indexOf(id);
          const page = at >= 0 && Math.abs(at - cur) <= 1;
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
        {tiles.map(({ key }) => (
          <div key={key} className="pointer-events-none absolute inset-0 bg-background" style={{ transform: pageX(keys.indexOf(key)) }} aria-hidden inert>
            <Watch id={sid.get(key)!} />
            <Tile id={sid.get(key)!} focused={false} main={false} zoomed={false} target={false} phone onFocus={() => {}} onZoom={() => {}} />
          </div>
        ))}
      </div>
    </div>
  );
}

const noProgress = () => {};

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
