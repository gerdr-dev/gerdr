import { create } from "zustand";
import { api, type BrowserTab } from "../api";
import { activeTab, activeWs, leaves, locateSession, rid, setPane, spaceOf, split, updateTab, type Layout, type PaneLeaf } from "../layout";
import { get, setLayout } from "../store";

// ---- Browser panes: a page of the project's own browser beside the chat (components/browser/, docs/dev/BROWSER-PANE.md) ----

/** The pages Gerdr has open (pushed by the server as "browser_tabs" whenever one opens or closes). */
export const useBrowserTabs = create<{ tabs: BrowserTab[]; loaded: boolean }>(() => ({ tabs: [], loaded: false }));

export function setBrowserTabs(tabs: BrowserTab[] | null | undefined) {
  useBrowserTabs.setState({ tabs: tabs ?? [], loaded: true });
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
