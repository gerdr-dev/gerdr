import { useEffect, useState } from "react";
import { api } from "@/lib/api";

/**
 * A project's panels (server work.Runner: its own Work, unless the user removed it, and the ones installed or added),
 * for the rail, Work mode and Settings → Panels. Fetched per project, again whenever the page's UIs change (a panel
 * added or removed is a mount added or removed: the "gerdr:ui" event) or something here changed them (panelsChanged).
 */
export type Panel = {
  key: string;
  /** "" for the project's own Work. */
  name: string;
  title: string;
  icon?: string;
  color?: string;
  source: { git: boolean; remote?: string; dirty?: boolean };
};

const str = (v: unknown) => (typeof v === "string" && v ? v : undefined);

/**
 * A panel's name as already fetched (by any usePanels of its project), for labels outside React: undefined until
 * then. key: "project" (its own panel) or "project/name".
 */
export function panelTitle(key: string): string | undefined {
  const space = key.split("/")[0];
  return cache[space]?.panels.find((p) => p.key === key)?.title;
}

/** Tell every usePanels to fetch again (after adding, removing, restoring, a new icon). */
export const panelsChanged = () => window.dispatchEvent(new Event("gerdr:panels"));

const cache: Record<string, { panels: Panel[]; ownRemoved: boolean }> = {};

const fromCache = (space: string) => ({ space, ...(cache[space] ?? { panels: [], ownRemoved: false }), loaded: !!cache[space] });

export function usePanels(space: string): { panels: Panel[]; ownRemoved: boolean; loaded: boolean } {
  const [state, setState] = useState(() => fromCache(space));
  useEffect(() => {
    let dead = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = () =>
      api.panels(space).then(
        (r) => {
          const panels = r.panels.map((p) => ({
            key: p.key,
            name: p.name,
            // Its own name (ui.json title); a panel without one goes by its directory's name
            title: str(p.config.title) ?? (p.name || space),
            icon: str(p.config.icon),
            color: str(p.config.color),
            source: p.source,
          }));
          cache[space] = { panels, ownRemoved: r.ownRemoved };
          if (!dead) setState({ space, panels, ownRemoved: r.ownRemoved, loaded: true });
        },
        () => {},
      );
    setState(fromCache(space));
    void load();
    const later = () => {
      clearTimeout(timer);
      timer = setTimeout(load, 300);
    };
    window.addEventListener("gerdr:ui", later);
    window.addEventListener("gerdr:panels", later);
    return () => {
      dead = true;
      clearTimeout(timer);
      window.removeEventListener("gerdr:ui", later);
      window.removeEventListener("gerdr:panels", later);
    };
  }, [space]);
  // Right after the project changes, the state still holds the previous project's panels until the effect above runs.
  // Handing those out told PanelButtons the new project's remembered panel was gone, and it overwrote it with the first
  // one: switching projects lost which panel each was left on.
  return state.space === space ? state : fromCache(space);
}
