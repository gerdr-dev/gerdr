import { create } from "zustand";
import { api, type BrowserTab } from "../api";
import { activeTab, activeWs, leaves, locateSession, rid, setPane, spaceOf, split, updateTab, type Layout, type PaneLeaf } from "../layout";
import { get, setLayout } from "../store";

// ---- Browser panes: a page of the project's own browser beside the chat (components/browser/, docs/dev/BROWSER-PANE.md) ----

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
 * new tab; when that tab closes, the pane goes back to the page that opened it.
 */
function followNewTabs(prev: BrowserTab[], next: BrowserTab[]) {
  const had = new Set(prev.map((x) => x.tab));
  const alive = new Set(next.filter((x) => !x.gone).map((x) => x.tab));
  const moves = new Map<number, number>(); // pane's tab → where it goes
  for (const x of next) if (x.opener && !had.has(x.tab) && !x.gone) moves.set(x.opener, x.tab);
  for (const x of prev) if (!alive.has(x.tab) && x.opener && alive.has(x.opener)) moves.set(x.tab, x.opener);
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
      .tabs.filter((x) => x.project === project && !x.gone)
      .sort((a, b) => b.opened - a.opened)[0];
    const fresh: PaneLeaf = { type: "pane", id: rid("p"), browser: { project, tab: newest?.tab } };
    return updateTab(l, t.id, (tab) => ({ ...split(tab, tab.zoom ?? tab.focus, dir === "right" ? "row" : "col", fresh), focus: fresh.id }));
  });
}

/** Point a Browser pane at another page (or none: the address bar). */
export function showBrowserTab(paneId: string, project: string, tab?: number) {
  setLayout((l) => setPane(l, paneId, { browser: { project, tab } }));
}

/** Remember where a Browser pane's page is, to open it again after a restart (PaneLeaf.browser.url). */
export function noteBrowserURL(paneId: string, url: string) {
  setLayout((l) => {
    const p = leavesOf(l).find((x) => x.id === paneId);
    if (!p?.browser || p.browser.url === url) return l;
    return setPane(l, paneId, { browser: { ...p.browser, url } });
  });
}

function leavesOf(l: Layout) {
  return l.workspaces.flatMap((w) => w.tabs.flatMap((t) => leaves(t.root)));
}

/**
 * An agent opened a page (`gerdr browser open`): show it beside that agent's pane, without taking the focus from
 * where the user is. A tab that already has a Browser pane switches it to the new page. An agent no pane shows (a Work
 * agent, a closed one) gets nothing on screen: its project's pages are in any Browser pane's page list.
 */
export function browserOpened(m: { tab: number; project: string; session?: string }) {
  if (!m.session) return;
  const loc = locateSession(get().layout, m.session);
  if (!loc) return;
  setLayout((l) => {
    const old = leaves(loc.tab.root).find((p) => p.browser);
    if (old) return setPane(l, old.id, { browser: { project: m.project, tab: m.tab } });
    const fresh: PaneLeaf = { type: "pane", id: rid("p"), browser: { project: m.project, tab: m.tab } };
    return updateTab(l, loc.tab.id, (tab) => ({ ...split(tab, loc.pane.id, "row", fresh), focus: tab.focus }));
  });
}
