// The overview: every open agent across all projects (spaces) and workspaces, on one screen.
//
// Deliberately NOT a workspace in the layout. The layout assumes a session sits in exactly one pane: locateSession /
// focusSession find "the" pane, and shutdownGone stops an agent once no pane shows it. A workspace holding the same
// sessions again would break both (focus lands in the wrong place; closing a tile here could stop the agent, or keep a
// closed one alive). So the overview is a view over the layout: a list of session ids, per device (localStorage, like
// the sidebar / mode), and closing a tile only takes it off this list. Ids whose session no longer has a pane anywhere
// drop out on their own (overviewIds), so the overview never keeps an agent alive that the layout let go of.
import { allLeaves, locateSession, type Layout } from "./layout";
import { MAX_TILES, OVERVIEW_KEY, overviewFocus, overviewIds, type Overview } from "./overviewState";
import { sessionRank } from "./order";
import { get, set, useStore } from "./store";
import { syncWatch } from "./sync";
import type { SessionSummary } from "./types";

export { MAX_TILES, overviewFocus, overviewIds, type Overview };

export function setOverview(fn: (o: Overview) => Overview) {
  const next = fn(get().overview);
  if (next === get().overview) return;
  set({ overview: next });
  try {
    localStorage.setItem(OVERVIEW_KEY, JSON.stringify(next));
  } catch {}
  syncWatch(); // which transcripts to stream changes with the tiles (store.watchedSessions)
}

/** Open agents = sessions some pane shows, in any project. */
export function openSessions(l: Layout, sessions: Record<string, SessionSummary>): SessionSummary[] {
  const seen = new Set<string>();
  const out: SessionSummary[] = [];
  for (const { pane } of allLeaves(l)) {
    const s = pane.sessionId ? sessions[pane.sessionId] : undefined;
    if (s && !seen.has(s.id)) (seen.add(s.id), out.push(s));
  }
  return out;
}

export const useOverviewIds = () => useStore((s) => overviewIds(s.overview, s.layout, s.sessions).join(","));

export function toggleOverview(on = !get().overview.on) {
  setOverview((o) => ({ ...o, on }));
}

export function addTiles(ids: string[]) {
  setOverview((o) => {
    const live = overviewIds(o, get().layout, get().sessions);
    const add = ids.filter((id) => !live.includes(id));
    if (!add.length) return o;
    return { ...o, ids: [...live, ...add].slice(0, MAX_TILES) };
  });
}

export function removeTile(id: string) {
  setOverview((o) => ({ ...o, ids: o.ids.filter((x) => x !== id), main: o.main === id ? undefined : o.main }));
}

export function clearTiles() {
  setOverview((o) => ({ ...o, ids: [], main: undefined }));
}

export function focusTile(id: string) {
  setOverview((o) => (o.focus === id ? o : { ...o, focus: id }));
}

export function toggleMain(id: string) {
  setOverview((o) => ({ ...o, main: o.main === id ? undefined : id }));
}

/** The agents worth a look: running, waiting for you, errored, or finished unseen (sessionRank 0–3). */
export function attentionIds(): string[] {
  const { layout, sessions, unread } = get();
  return openSessions(layout, sessions)
    .filter((s) => sessionRank(s, unread) <= 3)
    .sort((a, b) => sessionRank(a, unread) - sessionRank(b, unread))
    .map((s) => s.id);
}

/** Follow mode: an agent that starts running or starts waiting comes in by itself. Started once from App. */
export function watchFollow() {
  let prev = get().sessions;
  return useStore.subscribe((s) => {
    if (s.sessions === prev) return;
    const before = prev;
    prev = s.sessions;
    if (!s.overview.follow) return;
    const add: string[] = [];
    for (const cur of Object.values(s.sessions)) {
      const old = before[cur.id];
      const busy = (x?: SessionSummary) => x?.status === "running" || x?.status === "waiting";
      if (busy(cur) && !busy(old) && locateSession(s.layout, cur.id)) add.push(cur.id);
    }
    if (add.length) addTiles(add);
  });
}

// ---- auto arrangement ----

export type Box = { x: number; y: number; w: number; h: number };

/** Chat tiles read best a bit wider than tall; a narrower tile wraps every line. */
const TARGET_ASPECT = 1.3;
const MIN_TILE_W = 300;

/**
 * How many tiles go in each row when n tiles share a w×h area (2 → [2], 4 → [2,2], 6 → [3,3], 9 → [3,3,3]; tall areas
 * go vertical). Tries every column count and scores how far a cell is from TARGET_ASPECT plus how much of the last row
 * would sit empty; the last row just holds fewer, wider tiles, so there's never a hole.
 */
export function gridRows(n: number, w: number, h: number): number[] {
  if (n <= 0) return [];
  let best = { cols: 1, score: Infinity };
  for (let cols = 1; cols <= n; cols++) {
    const rows = Math.ceil(n / cols);
    const cw = w / cols;
    const ch = h / rows;
    const empty = cols * rows - n;
    let score = Math.abs(Math.log(cw / ch / TARGET_ASPECT)) + (0.6 * empty) / cols;
    if (cw < MIN_TILE_W && cols > 1) score += 10;
    if (score < best.score) best = { cols, score };
  }
  const rows = Math.ceil(n / best.cols);
  return Array.from({ length: rows }, (_, r) => (r === rows - 1 ? n - best.cols * (rows - 1) : best.cols));
}

/** Equal-split tile boxes (fractions of the area), row by row. */
export function gridBoxes(n: number, w: number, h: number): Box[] {
  return layoutTiles(gridRows(n, w, h), undefined).boxes;
}

/**
 * Sizes the user dragged the seams to, for one shape of grid (see shapeKey). Shares of the area: rows = each row's
 * height, cols[r] = each tile's width in row r. Each sums to 1.
 */
export type GridSizes = { rows: number[]; cols: number[][] };

/** One grid shape = the tiles per row, plus whether a main tile takes the left. Sizes are remembered per shape. */
export const shapeKey = (counts: number[], hasMain: boolean) => (hasMain ? "m" : "") + counts.join("-");

/** A seam between two rows (dir "row": drags up/down) or two tiles in a row (dir "col": drags left/right). In area fractions. */
export type Seam = { dir: "row" | "col"; row: number; index: number; x: number; y: number; len: number };

const equal = (n: number) => Array.from({ length: n }, () => 1 / n);

function fit(saved: number[] | undefined, n: number): number[] {
  return saved && saved.length === n && saved.every((v) => v > 0) ? saved : equal(n);
}

/** Boxes and seams of a grid with `counts` tiles per row, sized by `sizes` (equal where they don't fit the shape). */
export function layoutTiles(counts: number[], sizes: GridSizes | undefined): { boxes: Box[]; seams: Seam[]; sizes: GridSizes } {
  const rows = fit(sizes?.rows, counts.length);
  const cols = counts.map((c, r) => fit(sizes?.cols?.[r], c));
  const boxes: Box[] = [];
  const seams: Seam[] = [];
  let y = 0;
  counts.forEach((_, r) => {
    let x = 0;
    cols[r].forEach((cw, c) => {
      boxes.push({ x, y, w: cw, h: rows[r] });
      x += cw;
      if (c < cols[r].length - 1) seams.push({ dir: "col", row: r, index: c, x, y, len: rows[r] });
    });
    y += rows[r];
    if (r < counts.length - 1) seams.push({ dir: "row", row: r, index: r, x: 0, y, len: 1 });
  });
  return { boxes, seams, sizes: { rows, cols } };
}

/** Move the seam after item `index` of `arr` to `at` (a fraction of the whole), keeping each side at least `min`. */
export function moveSeam(arr: number[], index: number, at: number, min = 0.08): number[] {
  const start = arr.slice(0, index).reduce((a, b) => a + b, 0);
  const pair = arr[index] + arr[index + 1];
  const a = Math.min(pair - min, Math.max(min, at - start));
  const out = [...arr];
  out[index] = a;
  out[index + 1] = pair - a;
  return out;
}

export type Arranged = { boxes: Map<string, Box>; seams: Seam[]; key: string; sizes: GridSizes; region: Box };

/**
 * Where every tile goes: with a main tile it takes the left `ratio` and the others share the right side's grid, else
 * the grid fills the area. Seams and the region are in area fractions; seam positions are within the region.
 */
export function arrange(ids: string[], main: string | undefined, ratio: number, w: number, h: number, saved: Record<string, GridSizes>): Arranged {
  const hasMain = !!main && ids.includes(main) && ids.length > 1;
  const rest = hasMain ? ids.filter((id) => id !== main) : ids;
  const region: Box = hasMain ? { x: ratio, y: 0, w: 1 - ratio, h: 1 } : { x: 0, y: 0, w: 1, h: 1 };
  const counts = gridRows(rest.length, w * region.w, h);
  const key = shapeKey(counts, hasMain);
  const { boxes, seams, sizes } = layoutTiles(counts, saved[key]);
  const out = new Map<string, Box>();
  if (hasMain) out.set(main!, { x: 0, y: 0, w: ratio, h: 1 });
  rest.forEach((id, i) => {
    const b = boxes[i];
    out.set(id, { x: region.x + b.x * region.w, y: b.y, w: b.w * region.w, h: b.h });
  });
  return { boxes: out, seams, key, sizes, region };
}

/** Drop tile `from` on tile `to`: they trade places (and the main slot, if either had it). */
export function swapTiles(from: string, to: string) {
  if (from === to) return;
  setOverview((o) => {
    const ids = [...o.ids];
    const a = ids.indexOf(from);
    const b = ids.indexOf(to);
    if (a < 0 || b < 0) return o;
    [ids[a], ids[b]] = [ids[b], ids[a]];
    const main = o.main === to ? from : o.main === from ? to : o.main;
    return { ...o, ids, main };
  });
}

export function setGridSizes(key: string, sizes: GridSizes | undefined) {
  setOverview((o) => {
    const all = { ...o.sizes };
    if (sizes) all[key] = sizes;
    else delete all[key];
    return { ...o, sizes: all };
  });
}
