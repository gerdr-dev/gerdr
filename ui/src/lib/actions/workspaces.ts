import { currentHost, currentSpace, DEFAULT_SPACE, leaves, newWorkspace, spaceOf, updateWs, viewWorkspaces, type Layout, type Workspace } from "../layout";
import { get, setLayout } from "../store";
import { inSpace, killTerms, shutdownGone } from "./shared";
import { landIn } from "./spaces";

// ---- 工作空间 ----

export function addWorkspace(name: string, cwd?: string) {
  setLayout((l) => {
    const space = currentSpace(l);
    const host = currentHost(l);
    const w = { ...newWorkspace(name || `workspace ${viewWorkspaces(l).length + 1}`, cwd), space: inSpace(space), ...(host ? { host } : {}) };
    return { ...l, workspaces: [...l.workspaces, w], active: w.id, lastActive: l.active };
  });
}

export function selectWorkspace(id: string) {
  setLayout((l) => (l.active === id ? l : { ...l, active: id, lastActive: l.active }));
}

/** The n-th workspace of the current session (0-based), as numbered in the sidebar (⌃⌘1–9, Alt+1–9 off the mac). */
export function selectWorkspaceN(n: number) {
  const w = viewWorkspaces(get().layout)[n];
  if (w) selectWorkspace(w.id);
}

/** Next / previous workspace, cycling within the current session only. */
export function stepWorkspace(delta: number) {
  setLayout((l) => {
    const list = viewWorkspaces(l);
    const i = list.findIndex((w) => w.id === l.active);
    const w = list[(i + delta + list.length) % list.length];
    return w.id === l.active ? l : { ...l, active: w.id, lastActive: l.active };
  });
}

export function renameWorkspace(id: string, name: string) {
  setLayout((l) => updateWs(l, id, (w) => ({ ...w, name: name.trim() || w.name })));
}

/** Do not disturb on / off for a group (Workspace.muted). */
export function muteWorkspace(id: string, on: boolean) {
  setLayout((l) => updateWs(l, id, (w) => (!!w.muted === on ? w : { ...w, muted: on ? true : undefined })));
}

/** Change a workspace's directory (and name). The directory is only the default for tabs opened later; open ones keep theirs. */
export function editWorkspace(id: string, name: string, cwd?: string) {
  setLayout((l) => updateWs(l, id, (w) => ({ ...w, name: name.trim() || w.name, cwd })));
}

/**
 * Where to go once workspace `gone` disappears from `l`: the last one you were on if it's in the same view (project, and
 * Code's or the same panel's: Workspace.host), else the first one left there. Returns undefined when it has none left.
 */
function fallbackIn(l: Layout, gone: Workspace): string | undefined {
  const left = viewWorkspaces(l, gone).filter((w) => w.id !== gone.id);
  return left.find((w) => w.id === l.lastActive)?.id ?? left[0]?.id;
}

export function closeWorkspace(id?: string) {
  const w0 = get().layout.workspaces.find((w) => w.id === (id ?? get().layout.active));
  const panes = w0 ? w0.tabs.flatMap((t) => leaves(t.root)) : [];
  killTerms(panes);
  setLayout((l) => {
    const wid = id ?? l.active;
    const gone = l.workspaces.find((w) => w.id === wid);
    if (!gone) return l;
    const space = spaceOf(gone);
    const workspaces = l.workspaces.filter((w) => w.id !== wid);
    if (workspaces.length === 0) {
      const w = { ...newWorkspace("default"), space: gone.space };
      return { ...l, workspaces: [w], active: w.id };
    }
    if (l.active !== wid) return { ...l, workspaces };
    const same = fallbackIn(l, gone);
    if (same) return { ...l, workspaces, active: same };
    // The view's last one while the project has others (Code's while a panel has its own, or the other way round): the
    // project stays, and this view starts over with an empty workspace, as a project with nothing open does
    if (workspaces.some((w) => spaceOf(w) === space)) {
      const w = { ...newWorkspace("default"), space: gone.space, ...(gone.host ? { host: gone.host } : {}) };
      return { ...l, workspaces: [...workspaces, w], active: w.id };
    }
    // That was the session's last workspace: the session goes with it (the default one just stays, empty), and we land in another session.
    const spaces = (l.spaces ?? []).filter((s) => s.id !== space || space === DEFAULT_SPACE);
    const next = { ...l, workspaces, spaces };
    return { ...next, active: landIn(next, spaceOf(workspaces[0]))! };
  });
  shutdownGone(panes);
}

/** Move workspace `id` to position `to` among its session's workspaces (indexes as the sidebar shows them). */
export function moveWorkspace(id: string, to: number) {
  setLayout((l) => {
    const w = l.workspaces.find((x) => x.id === id);
    if (!w) return l;
    const rest = l.workspaces.filter((x) => x.id !== id);
    const peers = rest.filter((x) => spaceOf(x) === spaceOf(w));
    // Insert before the peer that ends up after it; past the end = right after the last peer (other sessions' workspaces keep their places)
    const at = to < peers.length ? rest.indexOf(peers[to]) : peers.length ? rest.indexOf(peers[peers.length - 1]) + 1 : rest.length;
    if (l.workspaces.indexOf(w) === at) return l;
    rest.splice(at, 0, w);
    return { ...l, workspaces: rest };
  });
}
