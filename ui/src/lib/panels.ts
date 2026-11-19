import { useEffect, useState } from "react";
import { toast } from "sonner";
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

type Cached = { panels: Panel[]; codeAt: number; ownRemoved: boolean };
const cache: Record<string, Cached> = {};

/**
 * Code's key in the rail's order (work.CodeName): Code is dragged and moved like any panel, its place kept with theirs.
 * "." can't be a panel's name, so it never clashes.
 */
export const codeKey = (space: string) => `${space}/.`;

/**
 * The project's rail group in order: Code (`null`) at its place among the panels. What the rail, the phone's switch and
 * the drawer's list all draw, so Code is never treated apart from the panels.
 */
export function railItems(panels: Panel[], codeAt: number): (Panel | null)[] {
  const out: (Panel | null)[] = [...panels];
  out.splice(Math.max(0, Math.min(codeAt, panels.length)), 0, null);
  return out;
}

/**
 * Put a panel (or Code: codeKey) at place `to` (0-based) among its project's rail group, Code counted: dragging its
 * button on the rail, or its menu's move up / down. Shown at once (every usePanels of the project reads the cache
 * again), then saved (work/order.go) and fetched.
 */
export function movePanel(space: string, key: string, to: number) {
  const c = cache[space];
  if (c) {
    const code = codeKey(space);
    const keys = railItems(c.panels, c.codeAt).map((p) => p?.key ?? code);
    if (!keys.includes(key)) return;
    const rest = keys.filter((k) => k !== key);
    rest.splice(Math.max(0, Math.min(to, rest.length)), 0, key);
    const byKey = new Map(c.panels.map((p) => [p.key, p]));
    cache[space] = { ...c, panels: rest.flatMap((k) => byKey.get(k) ?? []), codeAt: rest.indexOf(code) };
    window.dispatchEvent(new CustomEvent("gerdr:panels-cache", { detail: space }));
  }
  api.movePanel(key, to).then(panelsChanged, (e) => (panelsChanged(), toast.error(e.message)));
}

const fromCache = (space: string) => ({ space, ...(cache[space] ?? { panels: [], codeAt: 0, ownRemoved: false }), loaded: !!cache[space] });

export function usePanels(space: string): { panels: Panel[]; codeAt: number; ownRemoved: boolean; loaded: boolean } {
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
            // Its name (ui.json title, or the user's rename in user/ui.json); a panel without one goes by its directory's name
            title: str(p.config.title) ?? (p.name || space),
            icon: str(p.config.icon),
            color: str(p.config.color),
            source: p.source,
          }));
          const codeAt = r.codeAt ?? 0;
          cache[space] = { panels, codeAt, ownRemoved: r.ownRemoved };
          if (!dead) setState({ space, panels, codeAt, ownRemoved: r.ownRemoved, loaded: true });
        },
        () => {},
      );
    setState(fromCache(space));
    void load();
    const later = () => {
      clearTimeout(timer);
      timer = setTimeout(load, 300);
    };
    const local = (e: Event) => (e as CustomEvent).detail === space && !dead && setState(fromCache(space));
    window.addEventListener("gerdr:ui", later);
    window.addEventListener("gerdr:panels", later);
    window.addEventListener("gerdr:panels-cache", local);
    return () => {
      dead = true;
      clearTimeout(timer);
      window.removeEventListener("gerdr:ui", later);
      window.removeEventListener("gerdr:panels", later);
      window.removeEventListener("gerdr:panels-cache", local);
    };
  }, [space]);
  // Right after the project changes, the state still holds the previous project's panels until the effect above runs.
  // Handing those out told PanelButtons the new project's remembered panel was gone, and it overwrote it with the first
  // one: switching projects lost which panel each was left on.
  return state.space === space ? state : fromCache(space);
}
