import { api } from "../api";
import {
  currentSpace,
  DEFAULT_SPACE,
  projectNameError,
  leaves,
  newWorkspace,
  spaceList,
  spaceNameError,
  spaceOf,
  spaceWorkspaces,
  type Layout,
  type Workspace,
} from "../layout";
import { flushLayout, get, setConfig, setLayout } from "../store";
import { workProjectOf } from "../work";
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

/** Change the name a project shows. Its id (address, directory) stays: see projectNameError. Returns why it couldn't, "" when it could. */
export function renameSpace(id: string, name: string): string {
  const err = spaceNameError(get().layout, name, id);
  if (err) return err;
  const n = name.trim();
  setLayout((l) => ({ ...l, spaces: spaceList(l).map((s) => (s.id === id ? { ...s, name: n, renamed: n !== s.id || undefined } : s)) }));
  return "";
}

/** Put project `id` at position `to` of the list (counted without it). */
export function moveSpace(id: string, to: number) {
  setLayout((l) => {
    const list = spaceList(l);
    const from = list.findIndex((s) => s.id === id);
    if (from < 0) return l;
    const rest = list.filter((s) => s.id !== id);
    const at = Math.max(0, Math.min(to, rest.length));
    if (at === from) return l;
    rest.splice(at, 0, list[from]);
    return { ...l, spaces: rest };
  });
}

/**
 * Remember project `id` as it is now — its workspaces, its place in the list, its name, which workspace it was on, its Work
 * agent — and return what puts it back after it was closed or deleted. Throws when that can't be done: a project with that
 * id is open again (re-created since). The default project is never off the list, only emptied: then its workspaces just
 * go back into it.
 */
export function snapshotSpace(id: string): () => void {
  const l0 = get().layout;
  const index = spaceList(l0).findIndex((s) => s.id === id);
  const space = spaceList(l0)[index];
  const workspaces = spaceWorkspaces(l0, id);
  const wasHere = currentSpace(l0) === id;
  const active = wasHere ? l0.active : space?.active;
  const c = get().config;
  const kept = { session: c.workSessions?.[id], agent: c.workAgents?.[id] };
  return () => {
    const l1 = get().layout;
    const listed = spaceList(l1).find((s) => s.id.toLowerCase() === id.toLowerCase());
    if (!space || (listed && (listed.id !== id || spaceWorkspaces(l1, id).length > 0))) throw new Error(`又有叫「${id}」的项目了`);
    setLayout((l) => {
      const ws = workspaces.filter((w) => !l.workspaces.some((x) => x.id === w.id));
      if (!ws.length) ws.push({ ...newWorkspace("default"), space: inSpace(id) });
      let spaces = spaceList(l);
      if (!spaces.some((s) => s.id === id)) spaces.splice(Math.min(index, spaces.length), 0, { ...space, active: wasHere ? undefined : active });
      else spaces = spaces.map((s) => (s.id === id ? { ...space, active: wasHere ? undefined : active } : s));
      const next = { ...l, spaces, workspaces: [...l.workspaces, ...ws] };
      if (!wasHere) return next;
      return { ...rememberSpace(next), active: ws.find((w) => w.id === active)?.id ?? ws[0].id, lastActive: l.active };
    });
    const now = get().config;
    if (kept.session) setConfig({ workSessions: { ...now.workSessions, [id]: kept.session } });
    if (kept.agent) setConfig({ workAgents: { ...now.workAgents, [id]: kept.agent } });
  };
}

/** Close a project, with an undo (snapshotSpace) that opens it again as it was. */
export function closeSpaceUndoable(id: string): () => void {
  const restore = snapshotSpace(id);
  closeSpace(id);
  return restore;
}

/**
 * Delete a project: close it (its agents stop, their records stay), stop its Work agent, and move its directory (Work site,
 * local functions, saved layout) to the trash on the server — soft, so nothing is lost, but a project created later under the
 * same name starts blank. Returns where the directory went ("" = it had none) and an undo that puts it all back: the
 * directory, then the project as snapshotSpace has it. The default project can't be deleted.
 */
export async function deleteSpace(id: string): Promise<{ trash: string; undo: () => Promise<void> }> {
  if (id === DEFAULT_SPACE) throw new Error("默认项目不能删除");
  const restore = snapshotSpace(id);
  const c = get().config;
  closeSpace(id);
  for (const s of Object.keys(get().sessions)) if (workProjectOf(s) === id) api.shutdown(s).catch(() => {});
  const drop = (m?: Record<string, string>) => (m && id in m ? Object.fromEntries(Object.entries(m).filter(([k]) => k !== id)) : m);
  if (c.workSessions?.[id] || c.workAgents?.[id]) setConfig({ workSessions: drop(c.workSessions), workAgents: drop(c.workAgents) });
  // The server refuses while its saved layout still has the project open (its next save would write the layout copy back)
  await flushLayout();
  const { trash } = await api.deleteProject(id);

  const undo = async () => {
    if (spaceList(get().layout).some((s) => s.id.toLowerCase() === id.toLowerCase())) throw new Error(`又有叫「${id}」的项目了`);
    // The directory first: putting the workspaces back makes the server write the project's layout copy, which would
    // create the directory and leave the trash nowhere to go
    if (trash) await api.restoreProject(id, trash);
    restore();
  };
  return { trash, undo };
}
