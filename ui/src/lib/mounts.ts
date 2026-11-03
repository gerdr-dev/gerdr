import { useEffect, useState } from "react";

/**
 * The UIs compiled into this page (internal/uisite/mounts.go): each project's Work UI, under its project id. The
 * runtime puts them in window.__GERDR_UI__.mounts and fires "gerdr:ui" on every hot update (remount: the ones whose
 * change couldn't be hot-replaced). WorkView draws one as a second React root (MountRoot); its cards (src/cards.tsx)
 * are loaded at start, here, so a conversation in Code draws them too.
 */
export type MountInfo = {
  /** Its entry module: createApp({ path, onNavigate }). Missing while it has an error. */
  entry?: string;
  cards?: string;
  error?: string;
  /** Its ui.json. */
  config?: Record<string, unknown>;
};

type UIUpdate = { mounts: Record<string, MountInfo>; remount: string[] };

const page = () => (window as any).__GERDR_UI__ as { mounts?: Record<string, MountInfo> } | undefined;

/**
 * A Work's mount in the page: its key, with a panel's "/" as "~" (a mount name is one path segment; server mountName).
 * Every mount function here takes the key.
 */
export const mountName = (key: string) => key.replace("/", "~");

export function mountInfo(key: string): MountInfo | undefined {
  return page()?.mounts?.[mountName(key)];
}

/** One of a project's panels, for the rail. */
export type Panel = { key: string; name: string; title: string; icon?: string };

function panelsOf(space: string): Panel[] {
  const iconOf = (m?: MountInfo) => (typeof m?.config?.icon === "string" && m.config.icon ? m.config.icon : undefined);
  const out: Panel[] = [{ key: space, name: "", title: "Work", icon: iconOf(page()?.mounts?.[space]) }];
  for (const [n, m] of Object.entries(page()?.mounts ?? {})) {
    if (!n.startsWith(space + "~")) continue;
    const name = n.slice(space.length + 1);
    const title = typeof m.config?.title === "string" && m.config.title !== "Work" ? m.config.title : name;
    out.push({ key: `${space}/${name}`, name, title, icon: iconOf(m) });
  }
  return out.sort((a, b) => (a.name === "" ? -1 : b.name === "" ? 1 : a.name.localeCompare(b.name)));
}

/** A project's panels: its own Work first, then the ones installed or added (they appear as their mounts do). */
export function usePanels(space: string): Panel[] {
  const [list, setList] = useState(() => panelsOf(space));
  useEffect(() => {
    setList(panelsOf(space));
    const f = () => setList(panelsOf(space));
    window.addEventListener("gerdr:ui", f);
    return () => window.removeEventListener("gerdr:ui", f);
  }, [space]);
  return list;
}

/** A Work's mount info, and a number that goes up whenever its root has to be rendered again. */
export function useMount(key: string): { info: MountInfo | undefined; seq: number } {
  const name = mountName(key);
  const [state, setState] = useState(() => ({ info: mountInfo(key), seq: 0 }));
  useEffect(() => {
    setState((s) => ({ ...s, info: mountInfo(key) }));
    const f = (e: Event) => {
      const d = (e as CustomEvent<UIUpdate>).detail;
      const info = d.mounts?.[name];
      setState((s) => {
        const again = d.remount?.includes(name) || !!s.info?.entry !== !!info?.entry;
        return again || s.info?.error !== info?.error || s.info?.entry !== info?.entry || s.info?.cards !== info?.cards
          ? { info, seq: s.seq + (again ? 1 : 0) }
          : s;
      });
    };
    window.addEventListener("gerdr:ui", f);
    return () => window.removeEventListener("gerdr:ui", f);
  }, [key]);
  return state;
}

/** Its components registered with React Refresh, so editing them keeps the page's state (the runtime does the host's). */
export async function registerMount(key: string) {
  const name = mountName(key);
  const rt = (window as any).__gerdrUIRuntime as { register(p: string, url: string): Promise<unknown>; modules(): Record<string, { url: string }> } | undefined;
  if (!rt) return;
  const prefix = `@m/${name}/`;
  await Promise.all(
    Object.entries(rt.modules())
      .filter(([p]) => p.startsWith(prefix) && !p.endsWith("@entry.js"))
      .map(([p, m]) => rt.register(p, m.url).catch(() => {})),
  );
}

const loadedCards = new Set<string>();

function loadCards(mounts: Record<string, MountInfo> | undefined) {
  for (const [name, m] of Object.entries(mounts ?? {})) {
    if (!m.cards || loadedCards.has(m.cards)) continue;
    loadedCards.add(m.cards);
    import(/* @vite-ignore */ m.cards).catch((e) => console.error(`[gerdr] ${name}'s cards failed to load`, e));
  }
}

let watching = false;

/** Loads every mounted UI's cards now and as they appear / change. Called at start (sync.ts). */
export function watchMountCards() {
  if (watching) return;
  watching = true;
  loadCards(page()?.mounts);
  window.addEventListener("gerdr:ui", (e) => loadCards((e as CustomEvent<UIUpdate>).detail.mounts));
}
