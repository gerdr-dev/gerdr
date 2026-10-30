// The overview's stored state and the read side of it. Kept apart from overview.ts, which needs the store: the store
// loads this at creation, and a store <-> overview import cycle would hand it an undefined loadOverview.
import { locateSession, spaceList, type Layout } from "./layout";
import type { SessionSummary } from "./types";

export type Overview = {
  on: boolean;
  /** Tiles, in order. May hold stale ids (session closed since); read them through overviewIds. */
  ids: string[];
  /** The tile pinned to the left, the rest auto-arranged on the right. */
  main?: string;
  /** Width share of the main tile. */
  ratio: number;
  /** Put agents in by themselves when they start running or need you. */
  follow: boolean;
  /** Left list order. */
  sort: "status" | "project";
  /** The tile shown full size (zoomed), if any. Not restored on reload. */
  zoom?: string;
  /** The tile you're on (gets the keyboard; the only one counted as seen, see overviewFocus). */
  focus?: string;
  /** Seam positions the user dragged to, per grid shape (overview.ts shapeKey / GridSizes). */
  sizes: Record<string, { rows: number[]; cols: number[][] }>;
};

export const OVERVIEW_KEY = "gerdr.overview";
/** More tiles than this and each chat is too small to read; also bounds how many transcripts stream at once. */
export const MAX_TILES = 12;

export function loadOverview(): Overview {
  const d: Overview = { on: false, ids: [], ratio: 0.5, follow: false, sort: "status", sizes: {} };
  try {
    const v = JSON.parse(localStorage.getItem(OVERVIEW_KEY) ?? "null");
    if (!v) return d;
    return {
      on: !!v.on,
      ids: Array.isArray(v.ids) ? v.ids.filter((x: unknown) => typeof x === "string") : [],
      main: typeof v.main === "string" ? v.main : undefined,
      ratio: typeof v.ratio === "number" ? Math.min(0.8, Math.max(0.2, v.ratio)) : 0.5,
      follow: !!v.follow,
      sort: v.sort === "project" ? "project" : "status",
      sizes: v.sizes && typeof v.sizes === "object" ? v.sizes : {},
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

/** The tiles that can actually be shown: still open somewhere. */
export function overviewIds(o: Overview, l: Layout, sessions: Record<string, SessionSummary>, c: WorkConf): string[] {
  return o.ids.filter((id) => sessions[id] && isOpen(l, c, id)).slice(0, MAX_TILES);
}

/** The focused tile: the stored one while it's still a tile, else the first. */
export function overviewFocus(o: Overview, l: Layout, sessions: Record<string, SessionSummary>, c: WorkConf): string | undefined {
  const ids = overviewIds(o, l, sessions, c);
  return o.focus && ids.includes(o.focus) ? o.focus : ids[0];
}
