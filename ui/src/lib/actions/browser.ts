import { create } from "zustand";
import { api, type BrowserTab } from "../api";
import {
  activeTab,
  activeWs,
  allLeaves,
  closePane,
  floatable,
  leaves,
  locateSession,
  rid,
  setPane,
  spaceOf,
  split,
  toggleFloat,
  updateTab,
  type FloatFit,
  type Layout,
  type PaneLeaf,
} from "../layout";
import { get, setLayout } from "../store";
import { isNative, nativeAskBrowserWindows, nativeBrowserWindow, nativeNewPathWindow } from "../native";

// ---- Browser panes: a page of the project's own browser beside the chat (components/browser/, docs/dev/BROWSER-PANE.md) ----

/** Pages some Browser pane already shows: a new pane takes another one, or none (a fresh page), since one page is
 * one window of the App's browser and can be in one place only. */
function shown(l: Layout): Set<number> {
  return new Set(leavesOf(l).flatMap((p) => (p.browser?.tab ? [p.browser.tab] : [])));
}

/** Which pane shows each page as the real window (the App): the first to show it; another pane on the same page shows
 * its pictures instead (BrowserScreen). */
export const useNativeOwners = create<Record<number, string>>(() => ({}));

/** The page is in another window of the App (a page is in one window: the last pane to place it has it, internal/desktop
 * views.go). This window's panes show its pictures until that pane lets go or its window closes. */
const ELSEWHERE = "~another-window";
if (typeof window !== "undefined") {
  window.addEventListener("gerdr:browsertaken", (e) => useNativeOwners.setState({ [(e as CustomEvent<number>).detail]: ELSEWHERE }));
  window.addEventListener("gerdr:browserfree", (e) => {
    const tab = (e as CustomEvent<number>).detail;
    if (useNativeOwners.getState()[tab] !== ELSEWHERE) return;
    const { [tab]: _, ...rest } = useNativeOwners.getState();
    useNativeOwners.setState(rest, true);
  });
}

export function claimNative(tab: number, paneId: string): boolean {
  const owners = useNativeOwners.getState();
  if (owners[tab] && owners[tab] !== paneId) return false;
  if (!owners[tab]) useNativeOwners.setState({ [tab]: paneId });
  return true;
}

export function releaseNative(tab: number, paneId: string) {
  if (useNativeOwners.getState()[tab] === paneId) {
    const { [tab]: _, ...rest } = useNativeOwners.getState();
    useNativeOwners.setState(rest, true);
  }
}

/**
 * The pages in a window of their own (the App's, core/SessionBrowser.tsx BrowserWindow): the core says so as
 * gerdr:browserwindows whenever it changes. known: the core answered at all (an older one doesn't, and then a page is
 * popped out the old way: a new window each time, nothing tracked).
 */
export const useBrowserWindows = create<{ tabs: number[]; known: boolean }>(() => ({ tabs: [], known: false }));
if (typeof window !== "undefined") {
  window.addEventListener("gerdr:browserwindows", (e) => {
    const d = (e as CustomEvent<number[]>).detail;
    useBrowserWindows.setState({ tabs: Array.isArray(d) ? d : [], known: true });
  });
  // after this module and lib/native have both loaded: they import each other through lib/actions, so at this
  // module's own evaluation isNative may not be there yet ("isNative is not a function", the whole UI down)
  setTimeout(() => isNative() && nativeAskBrowserWindows(), 0);
}

/**
 * Page tab in a window of its own: the desktop App's core brings forward the window it is in already, or opens one more
 * window of Gerdr on /<project>?browser=<tab> (internal/desktop browserwin.go). A core that doesn't track them (it never
 * answered) gets the plain new window; a plain browser opens that address in a new tab.
 */
export function openBrowserWindow(project: string, tab: number) {
  const path = `/${encodeURIComponent(project)}?browser=${tab}`;
  if (isNative() && useBrowserWindows.getState().known) nativeBrowserWindow(project, tab);
  else if (isNative()) nativeNewPathWindow(project, path);
  else window.open(path, "_blank");
}

/** The pages Gerdr has open (pushed by the server as "browser_tabs" whenever one opens or closes). */
export const useBrowserTabs = create<{ tabs: BrowserTab[]; loaded: boolean }>(() => ({ tabs: [], loaded: false }));

export function setBrowserTabs(tabs: BrowserTab[] | null | undefined) {
  const prev = useBrowserTabs.getState();
  const next = tabs ?? [];
  useBrowserTabs.setState({ tabs: next, loaded: true });
  if (prev.loaded) followNewTabs(prev.tabs, next);
}

/**
 * Panes follow their page the way a browser's tab strip does. A page that opened a new tab (a target=_blank link, a
 * sign-in popup) sits behind it in the same window, hidden and no longer painting, so a pane showing it moves to the
 * new tab; when that tab closes, the pane goes back to the page that opened it. A page moved into the desktop App's browser
 * (replaces) takes its old tab's panes.
 */
function followNewTabs(prev: BrowserTab[], next: BrowserTab[]) {
  const moves = tabMoves(prev, next);
  if (!moves.size) return;
  setLayout((l) => {
    let out = l;
    for (const p of leavesOf(l)) {
      const to = p.browser?.tab && moves.get(p.browser.tab);
      if (to) out = setPane(out, p.id, { browser: { ...p.browser!, tab: to } });
    }
    return out;
  });
}

/** Where a view of each page goes after the page list changed from prev to next (followNewTabs): page → page. */
export function tabMoves(prev: BrowserTab[], next: BrowserTab[]): Map<number, number> {
  const had = new Set(prev.map((x) => x.tab));
  const alive = new Set(next.filter((x) => !x.gone).map((x) => x.tab));
  const moves = new Map<number, number>();
  for (const x of next) if (x.opener && !had.has(x.tab) && !x.gone) moves.set(x.opener, x.tab);
  // the same page moved into the desktop App's browser when it connected (chromeext Tab.Replaces)
  for (const x of next) if (x.replaces && !had.has(x.tab) && !x.gone) moves.set(x.replaces, x.tab);
  for (const x of prev) if (!alive.has(x.tab) && x.opener && alive.has(x.opener)) moves.set(x.tab, x.opener);
  return moves;
}

export async function loadBrowserTabs() {
  try {
    setBrowserTabs((await api.browserTabs()).tabs);
  } catch {
    // the bridge is off on this server: no pages
    setBrowserTabs([]);
  }
}

/**
 * A Browser pane beside the focused one, in the active project. Shows the project's newest page when it has one;
 * otherwise an address bar to open one. A tab that already has a Browser pane focuses it instead of adding another.
 */
export async function openBrowserPane(dir: "right" | "down" = "right") {
  if (!useBrowserTabs.getState().loaded) await loadBrowserTabs();
  setLayout((l) => {
    const project = spaceOf(activeWs(l));
    const t = activeTab(l);
    const old = leaves(t.root).find((p) => p.browser);
    if (old) return updateTab(l, t.id, (tab) => ({ ...tab, zoom: undefined, focus: old.id }));
    const newest = useBrowserTabs
      .getState()
      .tabs.filter((x) => x.project === project && !x.gone && !shown(l).has(x.tab))
      .sort((a, b) => b.opened - a.opened)[0];
    const fresh: PaneLeaf = { type: "pane", id: rid("p"), browser: { project, tab: newest?.tab } };
    return updateTab(l, t.id, (tab) => ({ ...split(tab, tab.zoom ?? tab.focus, dir === "right" ? "row" : "col", fresh), focus: fresh.id }));
  });
}

/**
 * Turn a pane (an empty one, the new-session screen's) into a Browser pane of its workspace's project, on the project's
 * newest page when there is one, else the address bar.
 */
export async function browserHere(paneId: string) {
  if (!useBrowserTabs.getState().loaded) await loadBrowserTabs();
  setLayout((l) => {
    const loc = allLeaves(l).find((x) => x.pane.id === paneId);
    if (!loc) return l;
    const project = spaceOf(loc.ws);
    const newest = useBrowserTabs
      .getState()
      .tabs.filter((x) => x.project === project && !x.gone && !shown(l).has(x.tab))
      .sort((a, b) => b.opened - a.opened)[0];
    return setPane(l, paneId, { draft: undefined, browser: { project, tab: newest?.tab } });
  });
}

/**
 * Point a Browser pane at another page (or none: the address bar). A page is in one pane: when another pane shows the
 * page picked, the two swap. Two panes on one page looked like two pages (the second is the first one's pictures):
 * reloading or going somewhere in either changed "all of them" (user report).
 */
export function showBrowserTab(paneId: string, project: string, tab?: number) {
  setLayout((l) => {
    const mine = leavesOf(l).find((x) => x.id === paneId)?.browser;
    let out = l;
    if (tab)
      for (const p of leavesOf(l)) if (p.id !== paneId && p.browser?.tab === tab) out = setPane(out, p.id, { browser: mine ?? { project: p.browser.project } });
    return setPane(out, paneId, { browser: { project, tab } });
  });
}

/** Remember where a Browser pane's page is, to open it again after a restart (PaneLeaf.browser.url). */
export function noteBrowserURL(paneId: string, url: string) {
  setLayout((l) => {
    const p = leavesOf(l).find((x) => x.id === paneId);
    if (!p?.browser || p.browser.url === url) return l;
    return setPane(l, paneId, { browser: { ...p.browser, url } });
  });
}

/** A Browser pane's page size on every device (its ⋯ menu); undefined = auto, the pane's own size. */
export function setBrowserFit(paneId: string, fit: FloatFit | undefined) {
  setLayout((l) => {
    const p = leavesOf(l).find((x) => x.id === paneId);
    if (!p?.browser || p.browser.fit === fit) return l;
    const { fit: _, ...rest } = p.browser;
    return setPane(l, paneId, { browser: fit ? { ...rest, fit } : rest });
  });
}

function leavesOf(l: Layout) {
  return l.workspaces.flatMap((w) => w.tabs.flatMap((t) => leaves(t.root)));
}

/** The page each agent session opened last, as it opened (browser_open): a panel turns to its browser then
 * (core/SessionBrowser.tsx useSessionBrowser). Only what happened since this UI loaded: an open seen again on reload
 * would pull the panel there again. */
export const useBrowserOpens = create<Record<string, { tab: number; at: number; show?: boolean }>>(() => ({}));

/**
 * An agent opened a page (`gerdr browser open`) or wants the user in it (`gerdr browser show`, show): put it beside that
 * agent's pane, without taking the focus from where the user is. A tab that already has a Browser pane switches it to
 * the new page. A new pane floats in a corner (the page's pictures, laid out at a desktop's size): an agent testing
 * its own work only needs to be watched (asked for: a pane taking half the screen each time). show docks it, as the
 * App's real page, which the user can sign in and type into. An agent no pane shows (a panel's, a closed one) gets no
 * pane: a panel shows its conversation's pages itself (core/SessionBrowser.tsx), and the project's pages are in any
 * Browser pane's page list.
 */
export function browserOpened(m: { tab: number; project: string; session?: string; show?: boolean }) {
  if (!m.session) return;
  useBrowserOpens.setState({ [m.session]: { tab: m.tab, at: Date.now(), show: !!m.show } });
  const loc = locateSession(get().layout, m.session);
  if (!loc) return;
  setLayout((l) => {
    const old = leaves(loc.tab.root).find((p) => p.browser);
    if (old) {
      const out = setPane(l, old.id, { browser: { project: m.project, tab: m.tab } });
      // docked for the user to act in; a pane they placed themselves stays where it is otherwise
      return m.show && loc.tab.float?.[old.id] ? updateTab(out, loc.tab.id, (t) => ({ ...toggleFloat(t, old.id), focus: t.focus })) : out;
    }
    const fresh: PaneLeaf = { type: "pane", id: rid("p"), browser: { project: m.project, tab: m.tab } };
    return updateTab(l, loc.tab.id, (tab) => {
      const docked = { ...split(tab, loc.pane.id, "row", fresh), focus: tab.focus };
      return m.show || !floatable(docked, fresh.id) ? docked : { ...toggleFloat(docked, fresh.id), focus: tab.focus };
    });
  });
}

/**
 * An agent closed a page it was done with (`gerdr browser close`, not --keep): the panes showing it go too (asked for:
 * each left an empty "this page is closed" pane behind). Not a pane alone in its tab, which would take the tab with it.
 * The user's own "close page" in the pane's menu keeps the pane: it moves to another page (BrowserScreen).
 */
export function browserClosed(m: { tab: number }) {
  setLayout((l) => {
    let out = l;
    for (const { tab, pane } of allLeaves(l)) {
      if (pane.browser?.tab !== m.tab) continue;
      const cur = out.workspaces.flatMap((w) => w.tabs).find((t) => t.id === tab.id);
      const next = cur && closePane(cur, pane.id);
      if (next) out = updateTab(out, tab.id, () => next);
    }
    return out;
  });
}

/**
 * A Browser pane put back (⌘⇧T): its page closed with the pane (server/browserpanes.go), so it is opened again at the
 * address the pane remembers. One still open (an agent's, kept) is shown as it is.
 */
export async function reopenBrowserPage(paneId: string, b: { project: string; tab?: number; url?: string }) {
  await loadBrowserTabs();
  if (b.tab && useBrowserTabs.getState().tabs.some((x) => x.tab === b.tab && !x.gone)) return;
  if (!b.url) return showBrowserTab(paneId, b.project, undefined);
  try {
    const t = await api.browserOpen(b.url, b.project);
    showBrowserTab(paneId, b.project, t.tab);
    void loadBrowserTabs();
  } catch {
    showBrowserTab(paneId, b.project, undefined); // the address bar, to type it again
  }
}
