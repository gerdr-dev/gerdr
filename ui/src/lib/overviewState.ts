// The overview's stored state and the read side of it. Kept apart from overview.ts, which needs the store: the store
// loads this at creation, and a store <-> overview import cycle would hand it an undefined loadOverview.
import { allLeaves, currentSpace, locateSession, spaceList, spaceOf, type Layout } from "./layout";
import { compareSessions, sessionRank, sessionSince } from "./order";
import type { SessionSummary } from "./types";

export type Overview = {
  on: boolean;
  /** Tile order last shown, so tiles keep their place as others come and go. Read the tiles through overviewIds. */
  ids: string[];
  /** Put in "active" by hand: stays until taken out (what joins on its own comes and goes, activeIds). */
  added: string[];
  /** Taken out of "active" while it had joined on its own: id → its sessionSince then. Out until it starts or ends a turn. */
  dismissed: Record<string, number>;
  /** Projects whose "active" lists every project's agents; the others list their own only. Set from the section's menu. */
  allIn: string[];
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
};

export const OVERVIEW_KEY = "gerdr.overview";
/** More tiles than this and each chat is too small to read; also bounds how many transcripts stream at once. */
export const MAX_TILES = 12;

export function loadOverview(): Overview {
  const d: Overview = { on: false, ids: [], added: [], dismissed: {}, allIn: [], ratio: 0.5, sort: "status", sizes: {}, shapes: {} };
  try {
    const v = JSON.parse(localStorage.getItem(OVERVIEW_KEY) ?? "null");
    if (!v) return d;
    const ids = Array.isArray(v.ids) ? v.ids.filter((x: unknown) => typeof x === "string") : [];
    return {
      on: !!v.on,
      ids,
      // Before "active", the tiles were all put in by hand: they carry over as added
      added: Array.isArray(v.added) ? v.added.filter((x: unknown) => typeof x === "string") : ids,
      dismissed:
        v.dismissed && typeof v.dismissed === "object"
          ? Object.fromEntries(Object.entries(v.dismissed).filter((e): e is [string, number] => typeof e[1] === "number"))
          : {},
      allIn: Array.isArray(v.allIn) ? v.allIn.filter((x: unknown) => typeof x === "string") : [],
      main: typeof v.main === "string" ? v.main : undefined,
      ratio: typeof v.ratio === "number" ? Math.min(0.8, Math.max(0.2, v.ratio)) : 0.5,
      sort: v.sort === "project" ? "project" : "status",
      sizes: v.sizes && typeof v.sizes === "object" ? v.sizes : {},
      shapes: v.shapes && typeof v.shapes === "object" ? v.shapes : {},
      focus: typeof v.focus === "string" ? v.focus : undefined,
    };
  } catch {
    return d;
  }
}

/** The part of the config saying which conversation each project's Work view shows (store Config; not imported, see above). */
export type WorkConf = { workSessions?: Record<string, string>; workSession?: string };

/**
 * Each project's current Work agent conversation (project → session id), for the projects that still exist. They sit in
 * no pane (Work mode shows them on its own right side), but they're open agents all the same (asked for: in the overview too).
 */
export function workChats(c: WorkConf, l: Layout): Record<string, string> {
  const out: Record<string, string> = {};
  for (const sp of spaceList(l)) {
    const id = c.workSessions?.[sp.id] ?? (sp.id === "default" ? c.workSession : undefined);
    if (id) out[sp.id] = id;
  }
  return out;
}

/** Open = some pane shows it, or it's a project's current Work conversation. */
export function isOpen(l: Layout, c: WorkConf, id: string): boolean {
  return !!locateSession(l, id) || Object.values(workChats(c, l)).includes(id);
}

/** What "active" is computed from: the store's state (a subset, so callers can pass a layout about to be set). */
export type ActiveSrc = {
  overview: Overview;
  layout: Layout;
  sessions: Record<string, SessionSummary>;
  config: WorkConf;
  unread: Record<string, true>;
};

/** Open agents (some pane shows them, or a project's Work conversation) with their project, panes first, each once. */
export function openSessionsWithProject(l: Layout, sessions: Record<string, SessionSummary>, c: WorkConf): { s: SessionSummary; project: string }[] {
  const seen = new Set<string>();
  const out: { s: SessionSummary; project: string }[] = [];
  const add = (id: string | undefined, project: string) => {
    const s = id ? sessions[id] : undefined;
    if (s && !seen.has(s.id)) (seen.add(s.id), out.push({ s, project }));
  };
  for (const { ws, pane } of allLeaves(l)) add(pane.sessionId, spaceOf(ws));
  for (const [project, id] of Object.entries(workChats(c, l))) add(id, project);
  return out;
}

/**
 * "Active" (in progress): the agents worth keeping in view, in the agents list's order (lib/order.ts). Who's in:
 * - put in by hand (stays until taken out);
 * - needs you: waiting, errored, finished unseen (always: taking one out marks it read);
 * - running, unless taken out since its current turn started.
 * Nothing joins for being recent (asked for: status and your own picks only); a finished one you've seen leaves.
 * Both the desktop sidebar and the overview grid show this list: the current project's agents, or every
 * project's when that project is in overview.allIn (each project chooses on its own).
 */
export function activeIds(st: ActiveSrc): string[] {
  const o = st.overview;
  const space = currentSpace(st.layout);
  const all = o.allIn.includes(space);
  const added = new Set(o.added);
  const isActive = (s: SessionSummary) => {
    if (added.has(s.id)) return true;
    const rank = sessionRank(s, st.unread);
    if (rank < 3) return true;
    const d = o.dismissed[s.id];
    return rank === 3 && !(d !== undefined && sessionSince(s) <= d);
  };
  return openSessionsWithProject(st.layout, st.sessions, st.config)
    .filter((x) => (all || x.project === space) && isActive(x.s))
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

/** The focused tile: the stored one while it's still a tile, else the first. */
export function overviewFocus(st: ActiveSrc): string | undefined {
  const ids = overviewIds(st);
  const o = st.overview;
  return o.focus && ids.includes(o.focus) ? o.focus : ids[0];
}
