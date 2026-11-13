// The overview's stored state and the read side of it. Kept apart from overview.ts, which needs the store: the store
// loads this at creation, and a store <-> overview import cycle would hand it an undefined loadOverview.
import { allLeaves, currentSpace, spaceOf, type Layout } from "./layout";
import { compareSessions, sessionRank, sessionSince } from "./order";
import type { SessionSummary } from "./types";

/**
 * Who is in "active": one list for every device (asked for: the same everywhere), so it lives in the server-side
 * config (Config.active), not in localStorage. How the grid shows it (Overview below) stays per device: tile sizes and
 * shapes depend on the screen.
 */
export type ActiveConf = {
  /** Pinned in "active": put in by hand, or joined on its status once (activeIds). Stays until taken out. */
  added: string[];
  /** Taken out of "active" while it had joined on its own: id → its sessionSince then. Out until it starts or ends a turn. */
  dismissed: Record<string, number>;
  /** Projects whose "active" lists every project's agents; the others list their own only. Set from the section's menu. */
  allIn: string[];
};

const strs = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);

/** Config.active read defensively (hand-edited config.json, older UIs writing it). */
export function activeConf(c: { active?: unknown }): ActiveConf {
  const v = (c.active && typeof c.active === "object" ? c.active : {}) as Record<string, unknown>;
  const d = v.dismissed && typeof v.dismissed === "object" ? (v.dismissed as Record<string, unknown>) : {};
  return {
    added: strs(v.added),
    dismissed: Object.fromEntries(Object.entries(d).filter((e): e is [string, number] => typeof e[1] === "number")),
    allIn: strs(v.allIn),
  };
}

/** The grid as this device shows it (localStorage). */
export type Overview = {
  on: boolean;
  /** Tile order last shown, so tiles keep their place as others come and go. Read the tiles through overviewIds. */
  ids: string[];
  /** The tile pinned to the left, the rest auto-arranged on the right. */
  main?: string;
  /** Width share of the main tile. */
  ratio: number;
  /** Left list order. */
  sort: "status" | "project";
  /** The tile shown full size (zoomed), if any. Not restored on reload. */
  zoom?: string;
  /** The tile you're on (gets the keyboard; the only one counted as seen, see overviewFocus). */
  focus?: string;
  /** Seam positions the user dragged to, per grid shape (overview.ts shapeKey / GridSizes). */
  sizes: Record<string, { rows: number[]; cols: number[][] }>;
  /** Grid shape the user picked by dropping a tile on another's edge, per tile count (overview.ts shapeOf / placeTile). */
  shapes: Record<string, { counts: number[]; cols?: boolean }>;
  /** Tiles minimized by hand: kept off the grid, waiting in its "more" row with the ones that didn't fit (OverviewGrid). */
  mini?: string[];
  /** The phone's page order, taken when it opened (OverviewPhone.tsx, phoneOrder). Not stored: each opening sorts afresh. */
  phone?: string[];
};

export const OVERVIEW_KEY = "gerdr.overview";
/** More tiles than this and each chat is too small to read; also bounds how many transcripts stream at once. */
export const MAX_TILES = 12;

export function loadOverview(): Overview {
  const d: Overview = { on: false, ids: [], ratio: 0.5, sort: "status", sizes: {}, shapes: {} };
  try {
    const v = JSON.parse(localStorage.getItem(OVERVIEW_KEY) ?? "null");
    if (!v) return d;
    return {
      on: !!v.on,
      ids: strs(v.ids),
      main: typeof v.main === "string" ? v.main : undefined,
      ratio: typeof v.ratio === "number" ? Math.min(0.8, Math.max(0.2, v.ratio)) : 0.5,
      sort: v.sort === "project" ? "project" : "status",
      sizes: v.sizes && typeof v.sizes === "object" ? v.sizes : {},
      shapes: v.shapes && typeof v.shapes === "object" ? v.shapes : {},
      focus: typeof v.focus === "string" ? v.focus : undefined,
      mini: strs(v.mini),
    };
  } catch {
    return d;
  }
}

/**
 * "Active" as this device kept it in localStorage before it moved to the config, or null if there's nothing there.
 * The first device to load a UI with Config.active seeds it from this (lib/overview.ts).
 */
export function loadLegacyActive(): ActiveConf | null {
  try {
    const v = JSON.parse(localStorage.getItem(OVERVIEW_KEY) ?? "null");
    if (!v || typeof v !== "object") return null;
    // Before "active", the tiles were all put in by hand: they carry over as added
    return activeConf({ active: { ...v, added: Array.isArray(v.added) ? v.added : v.ids } });
  } catch {
    return null;
  }
}

/** What "active" is computed from: the store's state (a subset, so callers can pass a layout about to be set). */
export type ActiveSrc = {
  overview: Overview;
  layout: Layout;
  sessions: Record<string, SessionSummary>;
  config: { active?: ActiveConf };
  unread: Record<string, true>;
};

/**
 * Open agents (some pane shows them) with their project, each once. A panel's agent conversation is no pane's, so it's
 * never here: Code and panels are apart, the panel's agent has its own place (the panel's right side, its rail dot).
 */
export function openSessionsWithProject(
  l: Layout,
  sessions: Record<string, SessionSummary>,
): { s: SessionSummary; project: string; /** mutedSession(l, s.id), found on the same walk */ muted: boolean }[] {
  const seen = new Set<string>();
  const out: { s: SessionSummary; project: string; muted: boolean }[] = [];
  for (const { ws, pane } of allLeaves(l)) {
    const s = pane.sessionId ? sessions[pane.sessionId] : undefined;
    if (s && !seen.has(s.id)) (seen.add(s.id), out.push({ s, project: spaceOf(ws), muted: !!ws.muted }));
  }
  return out;
}

/**
 * Joins "active" by its status: waiting on you or errored, always; finished unseen or running, unless taken out since
 * (it rejoins when it starts or ends a turn). Unseen has to honour the dismissal too: unread is per device while
 * "active" is shared, so another device that hasn't seen it would otherwise pin back what you just took out.
 */
export function joinsOnItsOwn(s: SessionSummary, st: Pick<ActiveSrc, "config" | "unread">, conf = activeConf(st.config)): boolean {
  const rank = sessionRank(s, st.unread);
  if (rank < 2) return true;
  const d = conf.dismissed[s.id];
  return rank <= 3 && !(d !== undefined && sessionSince(s) <= d);
}

/**
 * Joins on its own (joinsOnItsOwn), unless it sits in a muted group (Workspace.muted): those only come in by hand.
 * Takes the parsed conf and the walk's muted flag rather than looking them up per session: activeIds runs on every
 * pushed summary, in several components, and the per-session activeConf + mutedSession (a walk of every pane) made it
 * quadratic, the biggest JS cost of a push on a phone.
 */
const joins = (x: { s: SessionSummary; muted: boolean }, st: ActiveSrc, conf: ActiveConf) => !x.muted && joinsOnItsOwn(x.s, st, conf);

/** Open agents (any project) that joined on their own but aren't pinned yet: lib/overview.ts pins them, so they stay. */
export function toPin(st: ActiveSrc): string[] {
  const conf = activeConf(st.config);
  const added = new Set(conf.added);
  return openSessionsWithProject(st.layout, st.sessions)
    .filter((x) => !added.has(x.s.id) && joins(x, st, conf))
    .map((x) => x.s.id);
}

/**
 * "Active" (in progress): the agents worth keeping in view, in the agents list's order (lib/order.ts). Who's in:
 * - put in by hand (stays until taken out);
 * - needs you: waiting, errored, finished unseen (always: taking one out marks it read);
 * - running, unless taken out since its current turn started.
 * Whatever joins on its status is then pinned (lib/overview.ts, toPin), so finishing doesn't take it out: it stays until
 * you take it out. Nothing joins for being recent (asked for: status and your own picks only).
 * Both the desktop sidebar and the overview grid show this list: the current project's agents, or every
 * project's when that project is in config.active.allIn (each project chooses on its own).
 */
export function activeIds(st: ActiveSrc): string[] {
  const o = activeConf(st.config);
  const space = currentSpace(st.layout);
  const all = o.allIn.includes(space);
  const added = new Set(o.added);
  return openSessionsWithProject(st.layout, st.sessions)
    .filter((x) => (all || x.project === space) && (added.has(x.s.id) || joins(x, st, o)))
    .map((x) => x.s)
    .sort((a, b) => compareSessions(a, b, st.unread))
    .map((s) => s.id);
}

/**
 * The tiles: the first MAX_TILES of "active", the ones already shown keeping their order (overview.ids) so the grid
 * doesn't reshuffle every time a status changes; newcomers after them.
 */
export function overviewIds(st: ActiveSrc): string[] {
  const top = activeIds(st).slice(0, MAX_TILES);
  const kept = st.overview.ids.filter((id) => top.includes(id));
  return [...kept, ...top.filter((id) => !kept.includes(id))];
}

/**
 * The phone's tiles in its chip strip's order (OverviewPhone.tsx): the sidebar's "look at first" order, not the order
 * they were put in; ties keep that order (sort is stable). Sorted once, when the overview opens (`frozen`): re-sorted
 * live, reading a tile reordered the strip under the finger and a swipe landed somewhere unexpected (asked for). While
 * it stays open the frozen order holds, those taken out just drop out and newcomers go on the end.
 */
export function phoneOrder(ids: string[], sessions: Record<string, SessionSummary>, unread: Record<string, true>, frozen?: string[]): string[] {
  const live = ids.filter((id) => sessions[id]);
  const sorted = (xs: string[]) => xs.sort((a, b) => compareSessions(sessions[a], sessions[b], unread));
  if (!frozen) return sorted(live);
  const kept = frozen.filter((id) => live.includes(id));
  return [...kept, ...sorted(live.filter((id) => !kept.includes(id)))];
}

/**
 * The phone's pages: the focused tile and the one either side of it. The neighbours are mounted off screen (and so
 * watched) so a sideways swipe drags the real next page in, not a blank that fills in after the switch.
 */
export function phonePages(st: ActiveSrc): string[] {
  const order = phoneOrder(overviewIds(st), st.sessions, st.unread, st.overview.phone);
  const i = order.indexOf(overviewFocus(st) ?? "");
  return i < 0 ? [] : order.slice(Math.max(0, i - 1), i + 2);
}

/** The focused tile: the stored one while it's still a tile, else the first. */
export function overviewFocus(st: ActiveSrc): string | undefined {
  const ids = overviewIds(st);
  const o = st.overview;
  if (o.focus && ids.includes(o.focus)) return o.focus;
  return ids.find((id) => !o.mini?.includes(id)) ?? ids[0];
}
