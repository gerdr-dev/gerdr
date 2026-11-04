import { createContext, useContext, useSyncExternalStore } from "react";

/**
 * Whether a panel is on screen. Gerdr keeps a panel mounted (hidden) while another panel or Code is shown, and a
 * panel's page lives inside gerdr's own page, so `document.visibilityState` says "visible" the whole time the app is
 * open: pages that polled "while visible" polled all day (two installs of one panel were ~1 function run a second).
 * WorkView sets each panel's state; PanelKeyContext tells the hook which panel it is in.
 */
export const PanelKeyContext = createContext<string | null>(null);

const shown = new Map<string, boolean>();
const listeners = new Set<() => void>();

export function setPanelShown(key: string, on: boolean) {
  if (shown.get(key) === on) return;
  shown.set(key, on);
  for (const f of listeners) f();
}

function subscribe(f: () => void) {
  listeners.add(f);
  document.addEventListener("visibilitychange", f);
  return () => {
    listeners.delete(f);
    document.removeEventListener("visibilitychange", f);
  };
}

/** True while this panel is the one on screen and the window isn't hidden. Outside a panel: the window alone. */
export function usePanelVisible(): boolean {
  const key = useContext(PanelKeyContext);
  return useSyncExternalStore(subscribe, () => document.visibilityState === "visible" && (key == null || shown.get(key) !== false));
}
