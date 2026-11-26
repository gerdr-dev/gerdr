import { api, clientId } from "../api";
import { currentSpace, DEFAULT_SPACE, dockFloatsUnderBrowsers, newWorkspace, validLayout, viewOf, withView, type Layout, type View } from "../layout";
import { enterSpaceMode, get, set } from "../store";

// ---- 布局：本地先改，300ms 后写回服务端（别的窗口 / 设备跟着变）----
//
// Code mode's panes are one layout (layout.json); a panel with a Code list of its own (core CodeView list="replace")
// has another, its own data like its tasks, kept in its directory (server/panellayout.go). They are separate all the
// way: separate files, separate objects here. `layout` in the store is the one on screen (`layoutOwner`: "" = Code's,
// else the panel's key); the others wait in `stashed` until their view is shown (lib/hostView.ts swaps them). What
// is about all projects (the project list, switching projects, every project's dot) is Code's: codeLayout().
// Nothing ever decides on its own that a pane belongs to the other side. docs/dev/HOSTED-CODE.md

/** Code's layout, whichever one is on screen: the project list and everything about all projects. */
export function codeLayout(s: { layout: Layout; layoutOwner: string; stashed: Record<string, Layout> } = get()): Layout {
  return s.layoutOwner === "" ? s.layout : (s.stashed[""] ?? s.layout);
}

/** The layout of `owner` ("" = Code's, a panel key = that panel's own), on screen or not; undefined if it has none yet. */
export function layoutOf(owner: string, s: { layout: Layout; layoutOwner: string; stashed: Record<string, Layout> } = get()): Layout | undefined {
  return s.layoutOwner === owner ? s.layout : s.stashed[owner];
}

/**
 * Whether the backend keeps panels' panes apart (hello has panelLayouts). The UI is synced before the backend restarts
 * onto a new binary: until then a panel shows Code's layout, as before, and nothing is saved to an endpoint that isn't
 * there yet.
 */
let apart = false;
export const panelsApart = () => apart;

/** Whether panel `key` has panes of its own (a stored layout, or one made here). */
export const hasPanelLayout = (key: string) => apart && !!layoutOf(key);

// Each device's own "where I'm looking" (layout.ts View), per layout: kept in this browser.
const viewKey = (owner: string) => (owner ? `gerdr.view:${owner}` : "gerdr.view");
function saveView(owner: string, l: Layout) {
  try {
    localStorage.setItem(viewKey(owner), JSON.stringify(viewOf(l)));
  } catch {}
}
export function loadView(owner = ""): View | null {
  try {
    const v = JSON.parse(localStorage.getItem(viewKey(owner)) ?? "null");
    return v && typeof v.active === "string" && v.ws && v.tabs ? v : null;
  } catch {
    return null;
  }
}

const timers = new Map<string, ReturnType<typeof setTimeout>>();
function save(owner: string) {
  const l = layoutOf(owner);
  if (!l) return Promise.resolve();
  return (owner ? api.putPanelLayout(owner, l, clientId) : api.putLayout(l, clientId)).catch(() => {});
}
function queueSave(owner: string) {
  clearTimeout(timers.get(owner));
  timers.set(
    owner,
    setTimeout(() => (timers.delete(owner), void save(owner)), 300),
  );
}

/** Change the layout on screen. */
export function setLayout(fn: (l: Layout) => Layout) {
  setLayoutOf(get().layoutOwner, fn);
}

/** Change `owner`'s layout, on screen or not (a conversation placed in the other view). */
export function setLayoutOf(owner: string, fn: (l: Layout) => Layout) {
  const st = get();
  const shown = st.layoutOwner === owner;
  const prev = shown ? st.layout : st.stashed[owner];
  if (!prev) return;
  // no floating Browser pane under a docked one's page, whatever made the change (layout.dockFloatsUnderBrowsers)
  const next = dockFloatsUnderBrowsers(fn(prev));
  if (next === prev) return;
  if (shown) {
    // Same set() as the layout: listeners (nav history) never see the new project in the old project's mode
    const space = currentSpace(next);
    set(owner !== "" || space === currentSpace(prev) ? { layout: next } : { layout: next, ...enterSpaceMode(space) });
    afterLayoutChange(prev, next);
  } else {
    // Code's put aside under a panel: switching projects (the rail) still changes the project, and its mode with it
    const space = currentSpace(next);
    set({ stashed: { ...st.stashed, [owner]: next }, ...(owner === "" && space !== currentSpace(prev) ? enterSpaceMode(space) : {}) });
  }
  saveView(owner, next);
  queueSave(owner);
}

/** Save the layout now instead of in 300ms (setLayout), for a step the server must see it for (deleting a closed project). */
export function flushLayout(): Promise<unknown> {
  const owners = [...timers.keys()];
  for (const o of owners) (clearTimeout(timers.get(o)), timers.delete(o));
  if (!owners.includes("")) owners.push("");
  return Promise.all(owners.map(save));
}

/**
 * Put `owner`'s layout on screen and the one there aside (lib/hostView.ts, when the view switches). A panel showing its
 * panes for the first time gets a layout of its own: one workspace in its project, saved at once (that file is what
 * tells every device the panel has panes of its own).
 */
export function showLayout(owner: string) {
  const st = get();
  if (st.layoutOwner === owner) return;
  let next = st.stashed[owner];
  if (!next) {
    if (!owner) return;
    const space = owner.split("/")[0] || DEFAULT_SPACE;
    const w = newWorkspace(owner.split("/")[1] || "default");
    if (space !== DEFAULT_SPACE) w.space = space;
    next = { version: 1, workspaces: [w], active: w.id };
    queueSave(owner);
  }
  const { [owner]: _, ...rest } = st.stashed;
  const prev = st.layout;
  set({ layout: next, layoutOwner: owner, stashed: { ...rest, [st.layoutOwner]: prev } });
  afterLayoutChange(prev, next);
}

/** The panels' own layouts from the server (hello). Returns Code's with this device's view of it. */
export function loadedLayouts(code: Layout, panels: Record<string, unknown> | undefined): Layout {
  apart = !!panels;
  const stashed: Record<string, Layout> = {};
  for (const [k, v] of Object.entries(panels ?? {})) if (isPanelLayout(v)) stashed[k] = withView(v, loadView(k));
  set({ stashed, layoutOwner: "" });
  return withView(code, loadView());
}

const isPanelLayout = (v: unknown): v is Layout => !!v && Array.isArray((v as Layout).workspaces) && (v as Layout).workspaces.length > 0;

/** 别的端存的布局（或重连时服务端那份）：结构照收，「看的位置」留自己的 —— 手机上切 tab，主机不跟着动。 */
export function applyRemoteLayout(l: unknown) {
  if (validLayout(l)) applyRemote("", l);
}

/** A panel's own layout saved elsewhere (another device, an agent's `gerdr pane …`): the same. */
export function applyRemotePanelLayout(key: string, l: unknown) {
  if (key && isPanelLayout(l)) applyRemote(key, l);
}

function applyRemote(owner: string, l: Layout) {
  const st = get();
  if (st.layoutOwner === owner) {
    const prev = st.layout;
    const next = withView(l, viewOf(prev));
    set({ layout: next });
    afterLayoutChange(prev, next);
  } else {
    const had = st.stashed[owner];
    set({ stashed: { ...st.stashed, [owner]: withView(l, had ? viewOf(had) : loadView(owner)) } });
  }
}

const layoutListeners = new Set<(prev: Layout, next: Layout) => void>();
export function onLayoutChange(fn: (prev: Layout, next: Layout) => void) {
  layoutListeners.add(fn);
  return () => layoutListeners.delete(fn);
}
function afterLayoutChange(prev: Layout, next: Layout) {
  for (const fn of layoutListeners) fn(prev, next);
}
