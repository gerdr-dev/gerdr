import { api } from "../api";
import {
  currentSpace,
  DEFAULT_SPACE,
  projectNameError,
  leaves,
  newWorkspace,
  spaceList,
  spaceOf,
  spaceWorkspaces,
  type Layout,
  type Workspace,
} from "../layout";
import { get, setLayout } from "../store";
import { inSpace, killTerms, shutdownGone } from "./shared";

// ---- sessions (herdr's: isolated groups of workspaces; Space in layout.ts) ----

/** The workspace to show when entering `space`: the one you left it on, else its first. Undefined if it has none. */
export function landIn(l: Layout, space: string): string | undefined {
  const list = spaceWorkspaces(l, space);
  const last = spaceList(l).find((s) => s.id === space)?.active;
  return list.find((w) => w.id === last)?.id ?? list[0]?.id;
}

/** Remember which workspace the current session was on, so switching back lands there. */
function rememberSpace(l: Layout): Layout {
  const cur = currentSpace(l);
  return { ...l, spaces: spaceList(l).map((s) => (s.id === cur ? { ...s, active: l.active } : s)) };
}

export function switchSpace(id: string) {
  setLayout((l) => {
    if (currentSpace(l) === id || !spaceList(l).some((s) => s.id === id)) return l;
    let next = rememberSpace(l);
    let wid = landIn(next, id);
    if (!wid) {
      // An empty session (the default one after its last workspace closed): give it a fresh workspace
      const w = { ...newWorkspace("default"), space: inSpace(id) };
      next = { ...next, workspaces: [...next.workspaces, w] };
      wid = w.id;
    }
    return { ...next, active: wid, lastActive: l.active };
  });
}

export function stepSpace(delta: number) {
  const l = get().layout;
  const list = spaceList(l);
  const i = list.findIndex((s) => s.id === currentSpace(l));
  if (list.length > 1) switchSpace(list[(i + delta + list.length) % list.length].id);
}

/**
 * New project named `name` (its id is the name, see projectNameError). A project that existed before under that name gets its
 * workspaces back from its directory (server work/layout.go keeps each project's copy current), so closing and re-creating it
 * restores its Code — tabs, splits, the conversations in them — as well as its Work. Returns why it couldn't, "" when it could.
 */
export async function addSpace(name: string): Promise<string> {
  const err = projectNameError(get().layout, name);
  if (err) return err;
  const id = name.trim();
  const saved = await api.projectLayout(id).catch(() => ({}) as { workspaces?: Workspace[]; active?: string });
  setLayout((l) => {
    const prev = rememberSpace(l);
    const sp = { id, name: id };
    const back = (saved.workspaces ?? [])
      .filter((w) => w?.id && Array.isArray(w.tabs) && !l.workspaces.some((x) => x.id === w.id))
      .map((w) => ({ ...w, space: id }));
    const ws = back.length ? back : [{ ...newWorkspace("default"), space: id }];
    const active = ws.find((w) => w.id === saved.active)?.id ?? ws[0].id;
    return { ...prev, spaces: [...prev.spaces!, sp], workspaces: [...l.workspaces, ...ws], active, lastActive: l.active };
  });
  return "";
}

/** Close a whole session: all its workspaces (their agents stop, records stay — same as closing the workspaces one by one). */
export function closeSpace(id: string) {
  const l0 = get().layout;
  const gone = spaceWorkspaces(l0, id);
  const panes = gone.flatMap((w) => w.tabs.flatMap((t) => leaves(t.root)));
  killTerms(panes);
  setLayout((l) => {
    const workspaces = l.workspaces.filter((w) => spaceOf(w) !== id);
    const spaces = spaceList(l).filter((s) => s.id !== id || id === DEFAULT_SPACE);
    if (workspaces.length === 0) {
      const w = newWorkspace("default");
      return { ...l, workspaces: [w], spaces: spaces.filter((s) => s.id === DEFAULT_SPACE), active: w.id };
    }
    const next = { ...l, workspaces, spaces };
    if (currentSpace(l) !== id) return next;
    return { ...next, active: landIn(next, spaceOf(workspaces[0]))!, lastActive: undefined };
  });
  shutdownGone(panes);
}

/**
 * Move a workspace into another session. Its agents keep running, but from now on they're in the other session:
 * they can only reach agents there (and the ones they were talking to can't reach them).
 */
export function moveWorkspaceToSpace(wsId: string, space: string) {
  setLayout((l) => {
    const w = l.workspaces.find((x) => x.id === wsId);
    if (!w || spaceOf(w) === space) return l;
    const from = spaceOf(w);
    const moved = { ...w, space: inSpace(space) };
    const rest = l.workspaces.filter((x) => x.id !== wsId);
    let next: Layout = { ...l, workspaces: [...rest, moved] };
    if (!rest.some((x) => spaceOf(x) === from) && from !== DEFAULT_SPACE) next = { ...next, spaces: spaceList(next).filter((s) => s.id !== from) };
    // Moving the workspace you're on takes you along (otherwise the view would jump to a session you didn't pick)
    return next;
  });
}
