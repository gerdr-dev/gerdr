import { useContext, useSyncExternalStore } from "react";
import { PanelKeyContext } from "./visible";

/**
 * Changes a panel's functions made to its files (server: work.Runner.Changed → "panel_data" on /ws). A page that
 * showed what a function returns refreshes when this moves, instead of polling: its data changes from many places
 * (its own buttons, agents calling its tools, triggers, schedules), all of them functions.
 */
const counts = new Map<string, { path: string; n: number }[]>();
const listeners = new Set<() => void>();
// epoch moves every count at once: after a reconnect, when changes made meanwhile weren't heard
let epoch = 0;

export function panelDataChanged(panel: string, path: string) {
  const list = counts.get(panel) ?? [];
  const hit = list.find((x) => x.path === path);
  if (hit) hit.n++;
  else list.push({ path, n: 1 });
  counts.set(panel, list);
  for (const f of listeners) f();
}

/** The connection came back: whatever changed while it was down wasn't heard, so every page refreshes once. */
export function panelDataResync() {
  epoch++;
  for (const f of listeners) f();
}

function subscribe(f: () => void) {
  listeners.add(f);
  return () => void listeners.delete(f);
}

function countOf(panel: string | null, path?: string) {
  if (panel == null) return epoch;
  let n = epoch;
  for (const x of counts.get(panel) ?? []) if (!path || x.path === path || x.path.startsWith(path + "/")) n += x.n;
  return n;
}

/**
 * How many times this panel's files changed since the page loaded: put it in an effect's dependencies to reload
 * then. `path` (relative to the panel's directory, a file or a folder) narrows it; `panel` is for code outside the
 * panel's page (cards.tsx), which isn't inside it.
 */
export function usePanelChange(path?: string, opts?: { panel?: string }): number {
  const inPanel = useContext(PanelKeyContext);
  const panel = opts?.panel ?? inPanel;
  return useSyncExternalStore(subscribe, () => countOf(panel, path));
}
