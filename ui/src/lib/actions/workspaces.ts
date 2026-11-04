import { currentSpace, DEFAULT_SPACE, leaves, newWorkspace, spaceOf, spaceWorkspaces, updateWs, type Layout } from "../layout";
import { get, setLayout } from "../store";
import { inSpace, killTerms, shutdownGone } from "./shared";
import { landIn } from "./spaces";

// ---- 工作空间 ----

export function addWorkspace(name: string, cwd?: string) {
  setLayout((l) => {
    const space = currentSpace(l);
    const w = { ...newWorkspace(name || `workspace ${spaceWorkspaces(l, space).length + 1}`, cwd), space: inSpace(space) };
    return { ...l, workspaces: [...l.workspaces, w], active: w.id, lastActive: l.active };
  });
}

export function selectWorkspace(id: string) {
  setLayout((l) => (l.active === id ? l : { ...l, active: id, lastActive: l.active }));
}

/** The n-th workspace of the current session (0-based), as numbered in the sidebar (⌃⌘1–9, Alt+1–9 off the mac). */
export function selectWorkspaceN(n: number) {
  const w = spaceWorkspaces(get().layout)[n];
  if (w) selectWorkspace(w.id);
}

/** Next / previous workspace, cycling within the current session only. */
export function stepWorkspace(delta: number) {
  setLayout((l) => {
    const list = spaceWorkspaces(l);
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
 * Where to go once workspace `gone` disappears from `l`: the last one you were on if it's in the same session, else the first one left
 * in that session. Returns undefined when the session has none left.
 */
function fallbackIn(l: Layout, space: string, gone: string): string | undefined {
  const left = l.workspaces.filter((w) => w.id !== gone && spaceOf(w) === space);
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
    const same = fallbackIn(l, space, wid);
    if (same) return { ...l, workspaces, active: same };
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
