// The overview: every open agent across all projects (spaces) and workspaces, on one screen.
//
// Deliberately NOT a workspace in the layout. The layout assumes a session sits in exactly one pane: locateSession /
// focusSession find "the" pane, and shutdownGone stops an agent once no pane shows it. A workspace holding the same
// sessions again would break both (focus lands in the wrong place; closing a tile here could stop the agent, or keep a
// closed one alive). So the overview is a view over the layout: a list of session ids, and closing a tile only takes it
// off this list. Who is on the list is shared by every device (config.active); how the grid shows it is per device
// (localStorage, like the sidebar / mode). The tiles are "active" (in progress,
// overviewState.activeIds): agents join on their status (need you, run) and then stay until taken out, others are put in by hand; only
// open sessions count, so the overview never keeps an agent alive that the layout let go of.
import { toast } from "sonner";
import { allLeaves, type Layout } from "./layout";
import {
  activeConf,
  activeIds,
  loadLegacyActive,
  MAX_TILES,
  OVERVIEW_KEY,
  openSessionsWithProject,
  overviewFocus,
  overviewIds,
  toPin,
  type ActiveConf,
  type Overview,
} from "./overviewState";
import { sessionSince } from "./order";
import { currentSpace } from "./layout";
import { get, markRead, set, setConfig, useStore } from "./store";
import { syncWatch } from "./sync";
import type { SessionSummary } from "./types";
import { t } from "./i18n";

export { activeIds, MAX_TILES, overviewFocus, overviewIds, type Overview };

export function setOverview(fn: (o: Overview) => Overview) {
  const next = fn(get().overview);
  if (next === get().overview) return;
  set({ overview: next });
  try {
    localStorage.setItem(OVERVIEW_KEY, JSON.stringify(next));
  } catch {}
  syncWatch(); // which transcripts to stream changes with the tiles (store.watchedSessions)
}

/**
 * Change who is in "active" (config.active, shared by every device). Not before the server's config is in: setConfig
 * writes the whole config, and the defaults would overwrite everything else in it.
 */
function setActive(fn: (a: ActiveConf) => ActiveConf) {
  const st = get();
  if (!st.ready || st.authError) return;
  const cur = activeConf(st.config);
  const next = fn(cur);
  if (next === cur) return;
  setConfig({ active: next });
  syncWatch();
}

/** Open agents = sessions some pane shows, in any project. */
export function openSessions(l: Layout, sessions: Record<string, SessionSummary>): SessionSummary[] {
  const seen = new Set<string>();
  const out: SessionSummary[] = [];
  const add = (id?: string) => {
    const s = id ? sessions[id] : undefined;
    if (s && !seen.has(s.id)) (seen.add(s.id), out.push(s));
  };
  for (const { pane } of allLeaves(l)) add(pane.sessionId);
  return out;
}

export const useOverviewIds = () => useStore((s) => overviewIds(s).join(","));
/** "Active" (overviewState.activeIds), joined: the grid shows its first MAX_TILES. */
export const useActiveIds = () => useStore((s) => activeIds(s).join(","));

export function toggleOverview(on = !get().overview.on) {
  setOverview((o) => ({ ...o, on }));
}

// An agent that joined "active" on its status (started running, asks you something…) stays once that passes: finishing
// doesn't take it out, only you do (removeActive, "移出已完成的").
// Also seeds config.active once, from this device's old localStorage copy, when the config doesn't have it yet.
useStore.subscribe((s, p) => {
  if (!s.ready || s.authError) return;
  if (s.sessions === p.sessions && s.unread === p.unread && s.layout === p.layout && s.config === p.config && p.ready) return;
  if (!s.config.active) return setActive(() => loadLegacyActive() ?? { added: [], dismissed: {}, allIn: [] });
  const ids = toPin(s);
  if (ids.length) setActive((a) => ({ ...a, added: [...a.added, ...ids.filter((id) => !a.added.includes(id))] }));
});

/** Put agents in "active" by hand: they stay until taken out. */
export function addActive(ids: string[]) {
  setActive((a) => {
    const add = ids.filter((id) => !a.added.includes(id));
    const dismissed = { ...a.dismissed };
    for (const id of ids) delete dismissed[id];
    return { ...a, added: [...a.added, ...add], dismissed };
  });
}

/**
 * Put tiles just added right after `after` (the tile they were added from), in both the grid's kept order and the
 * phone's frozen strip; otherwise they'd go on the end, far from where the user was (asked for).
 */
export function placeTilesAfter(ids: string[], after: string) {
  const put = (order: string[]) => {
    const rest = order.filter((id) => !ids.includes(id));
    const i = rest.indexOf(after);
    if (i < 0) return order;
    rest.splice(i + 1, 0, ...ids);
    return rest;
  };
  setOverview((o) => ({ ...o, ids: put(o.ids), phone: o.phone && put(o.phone) }));
}

/**
 * Take an agent out of "active" (it keeps running): unpinned, and out until it starts or ends a turn. Unseen output counts as seen, else "finished unseen" would put it straight back.
 */
export function removeActive(id: string) {
  const s = get().sessions[id];
  if (s && get().unread[id]) markRead(id);
  setActive((a) => {
    // Forget sessions gone since, so the map doesn't grow forever
    const dismissed = Object.fromEntries(Object.entries(a.dismissed).filter(([k]) => get().sessions[k]));
    if (s) dismissed[id] = sessionSince(s);
    return { ...a, added: a.added.filter((x) => x !== id), dismissed };
  });
  setOverview((o) => (o.main === id ? { ...o, main: undefined } : o));
}

/** The finished ones (seen or not): what's still running, waiting on you or failed stays. */
export function doneActiveIds(st = get()): string[] {
  return activeIds(st).filter((id) => st.sessions[id] && !working(st.sessions[id]));
}

/** Take the finished agents out of "active". */
export function clearActive() {
  for (const id of doneActiveIds()) removeActive(id);
}

const working = (s: SessionSummary) => s.status === "running" || s.status === "waiting" || s.status === "error";

/** Agents still at work (running, waiting on you, failed) that were taken out of "active", in this list's scope. */
export function workingOutIds(st = get()): string[] {
  const all = activeAllProjects(st);
  const space = currentSpace(st.layout);
  const shown = new Set(activeIds(st));
  return openSessionsWithProject(st.layout, st.sessions)
    .filter((x) => (all || x.project === space) && working(x.s) && !shown.has(x.s.id))
    .map((x) => x.s.id);
}

/** Put them back: forget the dismissal; being at work, they rejoin (and get pinned again). */
export function restoreWorking() {
  const ids = workingOutIds();
  setActive((a) => ({ ...a, dismissed: Object.fromEntries(Object.entries(a.dismissed).filter(([k]) => !ids.includes(k))) }));
}

/** Whether this project's "active" lists every project's agents (each project has its own setting). */
export function activeAllProjects(st: { config: { active?: ActiveConf }; layout: Layout } = get()): boolean {
  return activeConf(st.config).allIn.includes(currentSpace(st.layout));
}

export function setActiveAllProjects(on: boolean) {
  const sp = currentSpace(get().layout);
  setActive((a) => ({ ...a, allIn: on ? [...a.allIn.filter((x) => x !== sp), sp] : a.allIn.filter((x) => x !== sp) }));
}

/** Open the grid on this session: put it in "active" and focus its tile. False (grid left alone) when it can't be a tile. */
export function showInOverview(id: string): boolean {
  if (!overviewIds(get()).includes(id)) {
    addActive([id]);
    if (!overviewIds(get()).includes(id)) return false;
  }
  focusTile(id);
  toggleOverview(true);
  return true;
}

/** "Add to active" from a menu: stay where you are, but the toast offers a way to jump to the tile. */
export function addToActive(id: string) {
  addActive([id]);
  if (!activeIds(get()).includes(id)) return void toast(t("overview.addedElsewhere"));
  toast(t("overview.added"), { action: { label: t("overview.view"), onClick: () => showInOverview(id) } });
}

export function focusTile(id: string) {
  // Zoomed in, moving to another tile zooms that one instead (as panes do: actions.focusPane)
  setOverview((o) => (o.focus === id ? o : { ...o, focus: id, zoom: o.zoom ? id : undefined }));
}

/** Zoom the tile to the whole grid, or back (`id` defaults to the focused tile). */
export function toggleTileZoom(id?: string) {
  setOverview((o) => {
    const t = id ?? overviewFocus({ ...get(), overview: o });
    if (!t) return o;
    return { ...o, zoom: o.zoom === t ? undefined : t, focus: t };
  });
}

/**
 * Minimize a tile (asked for): it leaves the grid for the "more" row under it, and the others take its room. The focus
 * moves to a tile still in the grid; picking it from "more" (or focusing it any other way) brings it back (OverviewGrid).
 */
export function minimizeTile(id: string) {
  setOverview((o) => {
    const ids = overviewIds({ ...get(), overview: o });
    const mini = [...(o.mini ?? []).filter((x) => x !== id && ids.includes(x)), id];
    const rest = ids.filter((x) => !mini.includes(x));
    const i = ids.indexOf(id);
    const next = o.focus === id || !o.focus ? (rest.find((x) => ids.indexOf(x) > i) ?? rest[rest.length - 1]) : o.focus;
    return { ...o, mini, focus: next, zoom: o.zoom === id ? undefined : o.zoom };
  });
}

/** Take a tile out of the minimized ones (it comes back into the grid). */
export function unminimizeTile(id: string) {
  setOverview((o) => (o.mini?.includes(id) ? { ...o, mini: o.mini.filter((x) => x !== id) } : o));
}

export function toggleMain(id: string) {
  setOverview((o) => ({ ...o, main: o.main === id ? undefined : id }));
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

/** Which saved shape (Overview.shapes) applies: one per tile count, with and without a main tile. */
const countKey = (n: number, hasMain: boolean) => (hasMain ? "m" : "") + n;

/** The shape n tiles take: the one the user picked by dropping (placeTile) while it still fits n, else gridShape. */
function shapeOf(n: number, hasMain: boolean, w: number, h: number, picked: Record<string, Shape> | undefined): Shape {
  const p = picked?.[countKey(n, hasMain)];
  const ok = p && Array.isArray(p.counts) && p.counts.every((c) => c > 0) && p.counts.reduce((a, b) => a + b, 0) === n;
  return ok ? { counts: p.counts, cols: !!p.cols } : gridShape(n, w, h);
}

/**
 * Where every tile goes: with a main tile it takes the left `ratio` and the others share the right side's grid, else
 * the grid fills the area. Seams and the region are in area fractions; seam positions are within the region.
 */
export function arrange(
  ids: string[],
  main: string | undefined,
  ratio: number,
  w: number,
  h: number,
  saved: Record<string, GridSizes>,
  picked?: Record<string, Shape>,
): Arranged {
  const hasMain = !!main && ids.includes(main) && ids.length > 1;
  const rest = hasMain ? ids.filter((id) => id !== main) : ids;
  const region: Box = hasMain ? { x: ratio, y: 0, w: 1 - ratio, h: 1 } : { x: 0, y: 0, w: 1, h: 1 };
  const shape = shapeOf(rest.length, hasMain, w * region.w, h, picked);
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
 * rest wait behind "more" (OverviewGrid.tsx). On a desktop all 12 fit; a landscape phone takes two (asked for: four
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

/**
 * /resume in a tile: `to` takes the tile `from` had and gets the focus. When `to` was a tile too they trade places;
 * when `from` stays open (it still has a pane) it moves to the end rather than leaving the grid. `before` = the tiles
 * before the switch: once `from`'s pane shows `to`, `from` is no longer a tile and its place is only known from then.
 */
export function replaceTile(before: string[], from: string, to: string) {
  addActive([to]);
  setOverview((o) => {
    const a = before.indexOf(from);
    const b = before.indexOf(to);
    let ids = [...before];
    if (a < 0) ids.push(to);
    else if (b >= 0) [ids[a], ids[b]] = [to, from];
    else ids = [...before.slice(0, a), to, ...before.slice(a + 1), from];
    const trade = (x?: string) => (x === from ? to : x === to ? from : x);
    return { ...o, ids, main: trade(o.main), focus: to, zoom: o.zoom ? to : undefined };
  });
}

/** Drop tile `from` on tile `to`: they trade places (and the main slot, if either had it). */
export function swapTiles(from: string, to: string) {
  if (from === to) return;
  setOverview((o) => {
    const ids = overviewIds({ ...get(), overview: o });
    const a = ids.indexOf(from);
    const b = ids.indexOf(to);
    if (a < 0 || b < 0) return o;
    [ids[a], ids[b]] = [ids[b], ids[a]];
    const main = o.main === to ? from : o.main === from ? to : o.main;
    return { ...o, ids, main };
  });
}

export type DropSide = "left" | "right" | "up" | "down";

/** Every way to cut n tiles into lines (compositions of n), in both directions. 2^(n-1) × 2: 4096 at the 12-tile cap. */
function allShapes(n: number): Shape[] {
  const out: Shape[] = [];
  const walk = (left: number, acc: number[]) => {
    if (!left) return out.push({ counts: acc }, { counts: acc, cols: true });
    for (let c = 1; c <= left; c++) walk(left - c, [...acc, c]);
  };
  walk(n, []);
  return out;
}

/**
 * Drop tile `from` on an edge of tile `to` (asked for: arrange the overview by dragging, like panes in a tab): `from`
 * moves next to `to` in the order, and the grid takes a shape that puts it on that side. Of all the shapes that do, the
 * one that moves the other tiles least: trying only the current shape turned / reversed (as before) never reached three
 * in a row from "1 | 1/1" (asked for), and judging by how far the rest move picks it as soon as it's what the drop says.
 * Shapes whose tiles get too small to read only count when none fits. The pick is remembered per tile count. No shape
 * does it: they just trade places. `shown` = the tiles in the grid now, w×h its size.
 */
export function placeTile(from: string, to: string, side: DropSide, shown: string[], w: number, h: number) {
  if (from === to) return;
  const o = get().overview;
  const hasMain = !!o.main && shown.includes(o.main) && shown.length > 1;
  if (from === o.main || to === o.main) return swapTiles(from, to);
  const after = side === "right" || side === "down";
  const move = (list: string[]) => {
    if (!list.includes(to)) return list;
    const out = list.filter((x) => x !== from);
    out.splice(out.indexOf(to) + (after ? 1 : 0), 0, from);
    return out;
  };
  const vis = move(shown);
  const n = hasMain ? vis.length - 1 : vis.length;
  const regionW = hasMain ? w * (1 - o.ratio) : w;
  const cur = shapeOf(n, hasMain, regionW, h, o.shapes);
  const auto = gridShape(n, regionW, h);
  const same = (a: Shape, b: Shape) => !!a.cols === !!b.cols && a.counts.join() === b.counts.join();
  const onSide = (a: Box, b: Box) => {
    const e = 1e-6;
    const vOverlap = a.y < b.y + b.h - e && a.y + a.h > b.y + e;
    const hOverlap = a.x < b.x + b.w - e && a.x + a.w > b.x + e;
    if (side === "left") return vOverlap && a.x + a.w <= b.x + e;
    if (side === "right") return vOverlap && a.x >= b.x + b.w - e;
    if (side === "up") return hOverlap && a.y + a.h <= b.y + e;
    return hOverlap && a.y >= b.y + b.h - e;
  };
  const before = arrange(shown, o.main, o.ratio, w, h, {}, o.shapes).boxes;
  let best: { shape: Shape; score: number } | undefined;
  for (const shape of allShapes(n)) {
    const { boxes } = arrange(vis, o.main, o.ratio, w, h, {}, { [countKey(n, hasMain)]: shape });
    if (!onSide(boxes.get(from)!, boxes.get(to)!)) continue;
    let score = 0;
    for (const [id, b] of boxes) {
      if (id === from) continue;
      const a = before.get(id)!;
      score += Math.abs(a.x - b.x) + Math.abs(a.y - b.y) + Math.abs(a.w - b.w) + Math.abs(a.h - b.h);
    }
    const small = [...boxes.values()].some((b) => b.w * w < MIN_TILE_W || b.h * h < MIN_TILE_H);
    // Too small loses to any that fits; on a tie the current shape, then the auto one (it needs no pin)
    score += (small ? 100 : 0) + (same(shape, cur) ? 0 : 1e-4) + (same(shape, auto) ? 0 : 1e-5);
    if (!best || score < best.score) best = { shape, score };
  }
  if (!best) return swapTiles(from, to);
  const hit = best.shape;
  setOverview((x) => {
    const shapes = { ...x.shapes };
    // The auto shape needs no pin: leaving it unset lets it follow the window again
    if (same(hit, auto)) delete shapes[countKey(n, hasMain)];
    else shapes[countKey(n, hasMain)] = { counts: hit.counts, ...(hit.cols ? { cols: true } : {}) };
    return { ...x, ids: move(overviewIds({ ...get(), overview: x })), shapes };
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

export type Dir = "left" | "right" | "up" | "down";

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

// What the overview's keys do to the tiles (its view in actions/views.ts binds them)

/** Close the focused tile: it leaves the grid (the agent keeps running), focus moves to its neighbour. */
export function closeTile() {
  const cur = overviewFocus(get());
  if (!cur) return;
  const ids = overviewIds(get());
  const i = ids.indexOf(cur);
  const next = ids[i + 1] ?? ids[i - 1];
  removeActive(cur);
  if (next) focusTile(next);
}

export function stepTile(delta: 1 | -1) {
  const cur = overviewFocus(get());
  const ids = overviewIds(get());
  if (cur && ids.length > 1) focusTile(ids[(ids.indexOf(cur) + delta + ids.length) % ids.length]);
}

export function focusTileDir(dir: Dir) {
  const cur = overviewFocus(get());
  const n = cur && neighbour(cur, dir);
  if (n) focusTile(n);
}

export function swapTileDir(dir: Dir) {
  const cur = overviewFocus(get());
  const n = cur && !get().overview.zoom ? neighbour(cur, dir) : undefined;
  if (n) swapTiles(cur!, n);
}
