// The overview: every open agent across all projects (spaces) and workspaces, on one screen.
//
// Deliberately NOT a workspace in the layout. The layout assumes a session sits in exactly one pane: locateSession /
// focusSession find "the" pane, and shutdownGone stops an agent once no pane shows it. A workspace holding the same
// sessions again would break both (focus lands in the wrong place; closing a tile here could stop the agent, or keep a
// closed one alive). So the overview is a view over the layout: a list of session ids, per device (localStorage, like
// the sidebar / mode), and closing a tile only takes it off this list. Ids whose session no longer has a pane anywhere
// drop out on their own (overviewIds), so the overview never keeps an agent alive that the layout let go of.
// Each project's current Work conversation counts as open too (overviewState.workChats): it has no pane, Work mode shows it.
import { allLeaves, type Layout } from "./layout";
import { isOpen, MAX_TILES, OVERVIEW_KEY, overviewFocus, overviewIds, workChats, type Overview, type WorkConf } from "./overviewState";
import { sessionRank } from "./order";
import { get, set, useStore } from "./store";
import { syncWatch } from "./sync";
import type { KeyAction } from "./keys";
import type { SessionSummary } from "./types";

export { MAX_TILES, overviewFocus, overviewIds, workChats, type Overview };

export function setOverview(fn: (o: Overview) => Overview) {
  const next = fn(get().overview);
  if (next === get().overview) return;
  set({ overview: next });
  try {
    localStorage.setItem(OVERVIEW_KEY, JSON.stringify(next));
  } catch {}
  syncWatch(); // which transcripts to stream changes with the tiles (store.watchedSessions)
}

/** Open agents = sessions some pane shows, in any project, then each project's Work conversation. */
export function openSessions(l: Layout, sessions: Record<string, SessionSummary>, c: WorkConf): SessionSummary[] {
  const seen = new Set<string>();
  const out: SessionSummary[] = [];
  const add = (id?: string) => {
    const s = id ? sessions[id] : undefined;
    if (s && !seen.has(s.id)) (seen.add(s.id), out.push(s));
  };
  for (const { pane } of allLeaves(l)) add(pane.sessionId);
  for (const id of Object.values(workChats(c, l))) add(id);
  return out;
}

export const useOverviewIds = () => useStore((s) => overviewIds(s.overview, s.layout, s.sessions, s.config).join(","));

export function toggleOverview(on = !get().overview.on) {
  setOverview((o) => ({ ...o, on }));
}

export function addTiles(ids: string[]) {
  setOverview((o) => {
    const live = overviewIds(o, get().layout, get().sessions, get().config);
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
  // Zoomed in, moving to another tile zooms that one instead (as panes do: actions.focusPane)
  setOverview((o) => (o.focus === id ? o : { ...o, focus: id, zoom: o.zoom ? id : undefined }));
}

/** Zoom the tile to the whole grid, or back (`id` defaults to the focused tile). */
export function toggleTileZoom(id?: string) {
  setOverview((o) => {
    const t = id ?? overviewFocus(o, get().layout, get().sessions, get().config);
    if (!t) return o;
    return { ...o, zoom: o.zoom === t ? undefined : t, focus: t };
  });
}

export function toggleMain(id: string) {
  setOverview((o) => ({ ...o, main: o.main === id ? undefined : id }));
}

/** The agents worth a look: running, waiting for you, errored, or finished unseen (sessionRank 0–3). */
export function attentionIds(): string[] {
  const { layout, sessions, unread, config } = get();
  return openSessions(layout, sessions, config)
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
      if (busy(cur) && !busy(old) && isOpen(s.layout, s.config, cur.id)) add.push(cur.id);
    }
    if (add.length) addTiles(add);
  });
}

// ---- auto arrangement ----

export type Box = { x: number; y: number; w: number; h: number };

/** Chat tiles read best a bit wider than tall; a narrower tile wraps every line. */
const TARGET_ASPECT = 1.3;
// Narrow enough that two tiles still sit side by side in a landscape phone's ~590px grid (asked for: not stacked as strips)
const MIN_TILE_W = 280;
/** Title bar + composer take ~110px; below this a tile shows hardly any of the conversation. */
const MIN_TILE_H = 200;

/**
 * How many tiles go in each row when n tiles share a w×h area (2 → [2], 4 → [2,2], 6 → [3,3], 9 → [3,3,3]; tall areas
 * go vertical). Tries every column count and scores how far a cell is from TARGET_ASPECT plus how much of the last row
 * would sit empty; the last row just holds fewer, wider tiles, so there's never a hole.
 */
export function gridRows(n: number, w: number, h: number): number[] {
  if (n <= 0) return [];
  // (3 in a wide area doesn't get here: gridShape lays it out as columns)
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

/**
 * A grid's shape: how many tiles in each row, or with `cols`, in each column (counts run left to right then).
 * 3 tiles in a wide area go one full-height on the left and two stacked on the right (asked for: "1 | 1/1"), rather
 * than two above one; everything else is gridRows.
 */
export type Shape = { counts: number[]; cols?: boolean };

export function gridShape(n: number, w: number, h: number): Shape {
  // Down to a somewhat tall area (asked for: columns where possible); a clearly tall one stacks the three instead
  if (n === 3 && w >= h * 0.6) return { counts: [1, 2], cols: true };
  return { counts: gridRows(n, w, h) };
}

/** Equal-split tile boxes (fractions of the area). */
export function gridBoxes(n: number, w: number, h: number): Box[] {
  return layoutTiles(gridShape(n, w, h), undefined).boxes;
}

/**
 * Sizes the user dragged the seams to, for one shape of grid (see shapeKey). Shares of the area: rows = each row's
 * height, cols[r] = each tile's width in row r; for a column shape, rows = each column's width and cols[c] = each
 * tile's height in column c. Each sums to 1.
 */
export type GridSizes = { rows: number[]; cols: number[][] };

/** One grid shape = the tiles per row / column, plus whether a main tile takes the left. Sizes are remembered per shape. */
export const shapeKey = (shape: Shape, hasMain: boolean) => (hasMain ? "m" : "") + (shape.cols ? "c" : "") + shape.counts.join("-");

/**
 * A seam, in area fractions. dir "row" = a horizontal line (drags up / down), starting at x for len of the width; "col"
 * = a vertical one (drags left / right), starting at y for len of the height. outer: between two rows (or columns),
 * sized by GridSizes.rows; else between two tiles of row (column) `row`, sized by GridSizes.cols[row].
 */
export type Seam = { dir: "row" | "col"; outer: boolean; row: number; index: number; x: number; y: number; len: number };

const equal = (n: number) => Array.from({ length: n }, () => 1 / n);

function fit(saved: number[] | undefined, n: number): number[] {
  return saved && saved.length === n && saved.every((v) => v > 0) ? saved : equal(n);
}

/**
 * Boxes and seams of a grid shape, sized by `sizes` (equal where they don't fit the shape). Laid out along the major
 * axis (down for rows, across for columns), each line split along the other.
 */
export function layoutTiles(shape: Shape, sizes: GridSizes | undefined): { boxes: Box[]; seams: Seam[]; sizes: GridSizes } {
  const { counts, cols: T } = shape;
  const outer = fit(sizes?.rows, counts.length);
  const inner = counts.map((c, r) => fit(sizes?.cols?.[r], c));
  const boxes: Box[] = [];
  const seams: Seam[] = [];
  let major = 0;
  counts.forEach((_, r) => {
    let minor = 0;
    inner[r].forEach((size, c) => {
      boxes.push(T ? { x: major, y: minor, w: outer[r], h: size } : { x: minor, y: major, w: size, h: outer[r] });
      minor += size;
      if (c < inner[r].length - 1)
        seams.push(
          T
            ? { dir: "row", outer: false, row: r, index: c, x: major, y: minor, len: outer[r] }
            : { dir: "col", outer: false, row: r, index: c, x: minor, y: major, len: outer[r] },
        );
    });
    major += outer[r];
    if (r < counts.length - 1)
      seams.push(
        T ? { dir: "col", outer: true, row: r, index: r, x: major, y: 0, len: 1 } : { dir: "row", outer: true, row: r, index: r, x: 0, y: major, len: 1 },
      );
  });
  return { boxes, seams, sizes: { rows: outer, cols: inner } };
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
  const shape = gridShape(rest.length, w * region.w, h);
  const key = shapeKey(shape, hasMain);
  const { boxes, seams, sizes } = layoutTiles(shape, saved[key]);
  const out = new Map<string, Box>();
  if (hasMain) out.set(main!, { x: 0, y: 0, w: ratio, h: 1 });
  rest.forEach((id, i) => {
    const b = boxes[i];
    out.set(id, { x: region.x + b.x * region.w, y: b.y, w: b.w * region.w, h: b.h });
  });
  return { boxes: out, seams, key, sizes, region };
}

/**
 * The tiles that fit in a w×h grid without any going under MIN_TILE_W × MIN_TILE_H (equal split), in `ids` order; the
 * rest wait behind "more" (Overview.tsx). On a desktop all 12 fit; a landscape phone takes two (asked for: four
 * stacked 80px strips were unusable). The main tile is always among them. Unmeasured (0×0): all of them.
 */
export function fitTiles(ids: string[], main: string | undefined, ratio: number, w: number, h: number): string[] {
  if (!w || !h) return ids;
  const hasMain = !!main && ids.includes(main);
  for (let k = ids.length; k > 1; k--) {
    const rest = ids.filter((id) => id !== main).slice(0, hasMain ? k - 1 : k);
    const vis = ids.filter((id) => id === main || rest.includes(id));
    const { boxes } = arrange(vis, main, ratio, w, h, {});
    if ([...boxes.values()].every((b) => b.w * w >= MIN_TILE_W && b.h * h >= MIN_TILE_H)) return vis;
  }
  return ids.length ? [hasMain ? main! : ids[0]] : [];
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

// ---- keys ----

type Dir = "left" | "right" | "up" | "down";

/** The tile next to `from` in a direction, by where the tiles are on screen (zoomed-out positions: hidden tiles keep theirs). */
function neighbour(from: string, dir: Dir): string | undefined {
  const rect = (el: Element) => el.getBoundingClientRect();
  const els = [...document.querySelectorAll<HTMLElement>("[data-ov-tile]")];
  const cur = els.find((e) => e.dataset.ovTile === from);
  if (!cur) return;
  const a = rect(cur);
  let best: { id: string; d: number } | undefined;
  for (const el of els) {
    const id = el.dataset.ovTile!;
    if (id === from) continue;
    const b = rect(el);
    // Strictly on that side, overlapping on the other axis (so "right" from a tall main tile reaches the column beside it)
    const side =
      dir === "left" ? b.right <= a.left + 1 : dir === "right" ? b.left >= a.right - 1 : dir === "up" ? b.bottom <= a.top + 1 : b.top >= a.bottom - 1;
    const overlap = dir === "left" || dir === "right" ? b.top < a.bottom - 1 && b.bottom > a.top + 1 : b.left < a.right - 1 && b.right > a.left + 1;
    if (!side || !overlap) continue;
    const d = dir === "left" ? a.left - b.right : dir === "right" ? b.left - a.right : dir === "up" ? a.top - b.bottom : b.top - a.bottom;
    // Nearest edge first; among equals, the one closest to the current tile's top / left
    const tie = dir === "left" || dir === "right" ? Math.abs(b.top - a.top) : Math.abs(b.left - a.left);
    const score = d * 10000 + tie;
    if (!best || score < best.d) best = { id, d: score };
  }
  return best?.id;
}

/** Keys that only open something or go somewhere by themselves: they work the same with the overview up. */
const NEUTRAL = new Set<KeyAction>([
  "overview",
  "help",
  "settings",
  "importHerdr",
  "palette",
  "sessions",
  "nextAttention",
  "prevAttention",
  "focusAttention",
  "toggleSidebar",
]);

/**
 * A shortcut pressed while the overview is up (actions.runAction asks first). Pane keys act on the tiles: zoom, close
 * (takes the tile out, the agent keeps running), focus / swap by direction, next / previous; stop, rename and stash go
 * to the focused tile's session (actions.focusedSessionId). Anything that would change the tabs underneath (split, new
 * tab, switch workspace…) leaves the overview first, so it doesn't happen out of sight. Returns true when handled here.
 */
export function overviewKey(a: KeyAction): boolean {
  if (NEUTRAL.has(a) || a === "stop" || a === "renamePane" || a === "stash") return false;
  const { overview: o, layout, sessions, config } = get();
  const ids = overviewIds(o, layout, sessions, config);
  const cur = overviewFocus(o, layout, sessions, config);
  const dirOf: Partial<Record<KeyAction, Dir>> = {
    focusLeft: "left",
    focusRight: "right",
    focusUp: "up",
    focusDown: "down",
    swapLeft: "left",
    swapRight: "right",
    swapUp: "up",
    swapDown: "down",
  };
  switch (a) {
    case "zoom":
      if (cur) toggleTileZoom(cur);
      return true;
    case "closePane":
      if (cur) {
        const i = ids.indexOf(cur);
        const next = ids[i + 1] ?? ids[i - 1];
        removeTile(cur);
        if (next) focusTile(next);
      }
      return true;
    case "nextPane":
    case "prevPane":
      if (cur && ids.length > 1) focusTile(ids[(ids.indexOf(cur) + (a === "nextPane" ? 1 : -1) + ids.length) % ids.length]);
      return true;
    case "focusLeft":
    case "focusRight":
    case "focusUp":
    case "focusDown": {
      const n = cur && neighbour(cur, dirOf[a]!);
      if (n) focusTile(n);
      return true;
    }
    case "swapLeft":
    case "swapRight":
    case "swapUp":
    case "swapDown": {
      const n = cur && !o.zoom ? neighbour(cur, dirOf[a]!) : undefined;
      if (n) swapTiles(cur!, n);
      return true;
    }
  }
  toggleOverview(false);
  return false;
}
