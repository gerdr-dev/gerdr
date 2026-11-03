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

/** Tell every usePanels to fetch again (after adding, removing, restoring, a new icon). */
export const panelsChanged = () => window.dispatchEvent(new Event("gerdr:panels"));

const cache: Record<string, { panels: Panel[]; ownRemoved: boolean }> = {};

export function usePanels(space: string): { panels: Panel[]; ownRemoved: boolean; loaded: boolean } {
  const [state, setState] = useState(() => ({ ...(cache[space] ?? { panels: [], ownRemoved: false }), loaded: !!cache[space] }));
  useEffect(() => {
    let dead = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = () =>
      api.panels(space).then(
        (r) => {
          const panels = r.panels.map((p) => ({
            key: p.key,
            name: p.name,
            title: p.name ? (str(p.config.title) && p.config.title !== "Work" ? String(p.config.title) : p.name) : "Work",
            icon: str(p.config.icon),
            color: str(p.config.color),
            source: p.source,
          }));
          cache[space] = { panels, ownRemoved: r.ownRemoved };
          if (!dead) setState({ panels, ownRemoved: r.ownRemoved, loaded: true });
        },
        () => {},
      );
    setState({ ...(cache[space] ?? { panels: [], ownRemoved: false }), loaded: !!cache[space] });
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
  return state;
}
