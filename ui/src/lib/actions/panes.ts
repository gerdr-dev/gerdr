import { toast } from "sonner";
import { api } from "../api";
import {
  activeTab,
  activeWs,
  allLeaves,
  closePane,
  currentSpace,
  cycle,
  findLeaf,
  leaves,
  locateSession,
  neighbor,
  newPane,
  newTab,
  rid,
  spaceList,
  spaceWorkspaces,
  split,
  swap,
  updateTab,
  updateWs,
  type PaneLeaf,
  type PaneNode,
  type Tab,
  type Workspace,
} from "../layout";
import { get, set, setLayout } from "../store";
import { addActive, focusTile, MAX_TILES, overviewFocus, overviewIds, setOverview } from "../overview";
import { workProjectOf } from "../work";
import { rememberPane } from "./reopen";
import { createInPane, focusSession, openInWork } from "./sessions";
import { basename, focusedLeaf, inSpace, inheritDraft, killTerms, removeTab, selectTabIn, shutdownGone } from "./shared";
import { openTerminal } from "./terminal";
import { showCode } from "./views";
import { t } from "../i18n";

// ---- panes ----

/** tab 的默认名字：用户起过就用它，不然是它第一个会话所在目录的名字。 */
export function tabName(tab: Tab): string {
  if (tab.name) return tab.name;
  // Unnamed tab: follow its first pane, so it reads like the agent's title (most tabs are never named by hand).
  const { sessions } = get();
  for (const p of leaves(tab.root)) {
    const s = p.sessionId ? sessions[p.sessionId] : undefined;
    if (s) return s.title || basename(s.cwd);
    if (p.term?.cwd) return basename(p.term.cwd);
    if (p.file) return basename(p.file.path);
    if (p.draft?.cwd) return basename(p.draft.cwd);
  }
  return "new";
}

export function splitFocused(dir: "right" | "down") {
  setLayout((l) => {
    const t = activeTab(l);
    const fresh: PaneLeaf = { type: "pane", id: rid("p"), draft: inheritDraft(l) };
    return updateTab(l, t.id, (tab) => split(tab, tab.zoom ?? tab.focus, dir === "right" ? "row" : "col", fresh));
  });
}

/**
 * ⌘D：把焦点那个复制一份放旁边。会话 → 同 agent / 目录 / 模式的新会话直接开起来（只复制配置，不带对话）；
 * 终端 → 同目录终端；空面板 → 还是空面板（没东西可复制）。
 */
export function duplicateFocused(dir: "right" | "down") {
  const l = get().layout;
  const p = focusedLeaf(l);
  if (p?.term) return openTerminal(dir);
  const s = p?.sessionId ? get().sessions[p.sessionId] : undefined;
  if (!s) return splitFocused(dir);
  const fresh: PaneLeaf = { type: "pane", id: rid("p"), draft: { agent: s.agent, cwd: s.cwd } };
  set((st) => ({ starting: { ...st.starting, [fresh.id]: true } }));
  setLayout((l) => updateTab(l, activeTab(l).id, (tab) => split(tab, tab.zoom ?? tab.focus, dir === "right" ? "row" : "col", fresh)));
  // 开失败时 createInPane 已经 toast 过，pane 落回空面板，人可以自己再点开始
  createInPane(fresh.id, { agent: s.agent, cwd: s.cwd, mode: s.modes?.currentModeId ?? s.mode })
    .catch(() => {})
    .finally(() =>
      set((st) => {
        const { [fresh.id]: _, ...rest } = st.starting;
        return { starting: rest };
      }),
    );
}

/**
 * ⌘D / ⌘T with the overview up, and its "new agent" button: start an agent like the focused tile's (agent, directory,
 * mode) and put it in as the next tile. Its pane is a new tab right after the focused tile's, in that tile's workspace,
 * opened in the background: the tabs under the overview don't move, so closing the overview lands where it was.
 * Returns false when no tile is focused (nothing to copy); the caller falls back to the plain key.
 */
export function newAgentInOverview(): boolean {
  const cur = overviewFocus(get());
  const s = cur ? get().sessions[cur] : undefined;
  if (!s) return false;
  if (overviewIds(get()).length >= MAX_TILES) {
    toast(t("actionsPanes.gridFull", { n: MAX_TILES }));
    return true;
  }
  const opening = toast.loading(t("actionsPanes.openingAgent"));
  api
    .createSession({ agent: s.agent, cwd: s.cwd, mode: s.modes?.currentModeId ?? s.mode })
    .then((n) => {
      set((st) => ({ sessions: { ...st.sessions, [n.id]: n } }));
      setLayout((l) => {
        const loc = locateSession(l, s.id);
        // A Work conversation's tile has no pane: its project's workspace, as openSessionNear does
        const project = loc ? undefined : workProjectOf(s.id);
        const home = project ? spaceWorkspaces(l, project) : [];
        const ws = loc?.ws ?? home.find((w) => w.id === spaceList(l).find((x) => x.id === project)?.active) ?? home[0] ?? activeWs(l);
        const t = newTab(undefined, n.id);
        const tabs = [...ws.tabs];
        const at = loc ? tabs.findIndex((x) => x.id === loc.tab.id) + 1 : tabs.length;
        tabs.splice(at, 0, t);
        return updateWs(l, ws.id, (w) => ({ ...w, tabs }));
      });
      // Started from the grid: it's in "active" by hand (a blank conversation wouldn't join on its own)
      addActive([n.id]);
      setOverview((x) => {
        const ids = x.ids.filter((id) => id !== n.id);
        ids.splice(ids.indexOf(s.id) + 1, 0, n.id);
        return { ...x, ids };
      });
      focusTile(n.id);
      toast.dismiss(opening);
    })
    .catch((e) => toast.error(t("actionsSessions.openFailed", { error: e.message ?? e }), { id: opening }));
  return true;
}

/**
 * 别的 agent 用 `gerdr session new` 开的会话：放在发起它的那个会话旁边（右边分一屏）。
 * 默认不抢焦点（人可能正在别处打字）；发起方是 Work 助手（不在任何 pane 里）就开在它那个项目的工作空间里，
 * 都找不到就在当前工作空间后台开个 tab。
 */
export function openSessionNear(sessionId: string, near?: string, focus = false) {
  if (workProjectOf(sessionId)) {
    // An agent opening a Work conversation (`gerdr session open`): it stays in its Work view; switch there only when asked to focus
    if (focus) openInWork(sessionId);
    return;
  }
  if (locateSession(get().layout, sessionId)) {
    if (focus) focusSession(sessionId); // 已经开着：要焦点就切过去
    return;
  }
  const loc = near ? locateSession(get().layout, near) : undefined;
  setLayout((l) => {
    if (loc) {
      const fresh = newPane(undefined, sessionId);
      return updateTab(l, loc.tab.id, (tab) => {
        const next = split(tab, loc.pane.id, "row", fresh);
        return focus ? next : { ...next, focus: tab.focus };
      });
    }
    // Asked by a Work agent (no pane of its own): into its project's workspace, not whichever one is on screen
    const project = near ? workProjectOf(near) : undefined;
    const home = project && project !== currentSpace(l) ? spaceWorkspaces(l, project) : [];
    const ws = home.find((w) => w.id === spaceList(l).find((s) => s.id === project)?.active) ?? home[0] ?? activeWs(l);
    const t = newTab(undefined, sessionId);
    return updateWs(l, ws.id, (w) => ({ ...w, tabs: [...w.tabs, t], activeTab: focus ? t.id : w.activeTab }));
  });
}

export type ImportNode = { sessionId: string; cwd: string } | { dir: "row" | "col"; ratio: number; a: ImportNode; b: ImportNode };

/**
 * 把从 herdr 导入的结构建成工作空间 / tab / 分屏（会话服务端已经建好）。tab 名只在和目录名不一样时才写死，
 * 不然留空让它跟着会话标题走（herdr 的 tab 名大多就是目录名）。
 */
export function addImportedWorkspaces(list: { label: string; tabs: { label: string; tree: ImportNode }[] }[]) {
  let first: string | undefined;
  setLayout((l) => {
    let next = l;
    for (const w of list) {
      const tabs: Tab[] = w.tabs.map((t) => {
        const build = (n: ImportNode): PaneNode =>
          "sessionId" in n ? newPane(undefined, n.sessionId) : { type: "split", id: rid("s"), dir: n.dir, ratio: n.ratio, a: build(n.a), b: build(n.b) };
        const root = build(t.tree);
        const firstLeaf = (n: ImportNode): ImportNode & { cwd: string } => ("sessionId" in n ? n : firstLeaf(n.a));
        const name = t.label && t.label !== basename(firstLeaf(t.tree).cwd) ? t.label : undefined;
        const focus = root.type === "pane" ? root.id : leaves(root)[0].id;
        return { id: rid("t"), root, focus, name };
      });
      const ws: Workspace = {
        id: rid("w"),
        name: w.label,
        cwd: "sessionId" in w.tabs[0].tree ? w.tabs[0].tree.cwd : undefined,
        tabs,
        activeTab: tabs[0].id,
        space: inSpace(currentSpace(l)),
      };
      first ??= ws.id;
      next = { ...next, workspaces: [...next.workspaces, ws] };
    }
    return first ? { ...next, active: first, lastActive: l.active } : next;
  });
}

/** 在旁边开一个子 agent 的只读面板；已经开着就切过去。 */
export function openSubagent(sessionId: string, toolCallId: string, title?: string) {
  const hit = allLeaves(get().layout).find((x) => x.pane.sub?.sessionId === sessionId && x.pane.sub.toolCallId === toolCallId);
  if (hit) return focusPane(hit.pane.id);
  setLayout((l) => {
    const t = activeTab(l);
    const fresh: PaneLeaf = { type: "pane", id: rid("p"), sub: { sessionId, toolCallId, title } };
    return updateTab(l, t.id, (tab) => split(tab, tab.zoom ?? tab.focus, "row", fresh));
  });
}

export function closeFocusedPane() {
  const l = get().layout;
  const p = focusedLeaf(l);
  if (p) rememberPane(activeWs(l), activeTab(l), p);
  killTerms(p ? [p] : []);
  setLayout((l) => {
    const ws = activeWs(l);
    const t = activeTab(l);
    const next = closePane(t, t.focus);
    if (next) return updateTab(l, t.id, () => next);
    return removeTab(l, ws, t.id);
  });
  shutdownGone(p ? [p] : []);
}

// Closing a pane ends its agent, cancelling a turn in flight, so ⌘W / prefix+x on an agent that is mid-turn (running, or
// waiting on a permission prompt) needs a second press within 2s, the same as double Ctrl+C. Idle agents, empty panes
// and terminals close on the first press: nothing is lost there, the session can be reopened with ⌘⇧T.
let closeArmed: { paneId: string; at: number } | undefined;

export function confirmCloseFocusedPane() {
  const p = focusedLeaf(get().layout);
  const status = p?.sessionId ? get().sessions[p.sessionId]?.status : undefined;
  if (p && (status === "running" || status === "waiting")) {
    const armed = closeArmed?.paneId === p.id && Date.now() - closeArmed.at < 2000;
    if (!armed) {
      closeArmed = { paneId: p.id, at: Date.now() };
      toast(t("actionsPanes.closeArmed"), { id: "closepane", duration: 2000 });
      return;
    }
    toast.dismiss("closepane");
  }
  closeArmed = undefined;
  closeFocusedPane();
}

/**
 * 关掉显示某个会话的 pane（`gerdr session close`，别的 agent 关它开的会话）：和人手动关一样。
 * agent 那边服务端已经关了，这里只收 pane；会话记录留着（`gerdr session open` / prefix+g 能找回来）。
 */
export function closeSessionPanes(sessionId: string) {
  if (!sessionId || !locateSession(get().layout, sessionId)) return;
  setLayout((l) => {
    for (let loc = locateSession(l, sessionId); loc; loc = locateSession(l, sessionId)) {
      const tabId = loc.tab.id;
      const ws = l.workspaces.find((w) => w.tabs.some((t) => t.id === tabId));
      if (!ws) break;
      const next = closePane(loc.tab, loc.pane.id);
      l = next ? updateTab(l, tabId, () => next) : removeTab(l, ws, tabId);
    }
    return l;
  });
}

/** 按 id 关一个 pane（不一定是焦点那个）：终端里的 shell 退出了就这样收掉。 */
export function closePaneById(paneId: string) {
  setLayout((l) => {
    for (const ws of l.workspaces)
      for (const tab of ws.tabs)
        if (findLeaf(tab.root, paneId)) {
          const next = closePane(tab, paneId);
          return next ? updateTab(l, tab.id, () => next) : removeTab(l, ws, tab.id);
        }
    return l;
  });
}

export function toggleZoom() {
  setLayout((l) => {
    const t = activeTab(l);
    if (leaves(t.root).length < 2 && !t.zoom) return l;
    return updateTab(l, t.id, (tab) => ({ ...tab, zoom: tab.zoom ? undefined : tab.focus }));
  });
}

export function focusDir(dir: "left" | "right" | "up" | "down") {
  setLayout((l) => {
    const t = activeTab(l);
    if (t.zoom) return l;
    const n = neighbor(t, dir);
    return n ? updateTab(l, t.id, (tab) => ({ ...tab, focus: n })) : l;
  });
}

export function swapDir(dir: "left" | "right" | "up" | "down") {
  setLayout((l) => updateTab(l, activeTab(l).id, (tab) => swap(tab, dir)));
}

export function cyclePane(delta: number) {
  setLayout((l) => updateTab(l, activeTab(l).id, (tab) => cycle(tab, delta)));
}

/** Focus a pane, switching to its tab / workspace / project, and show it: from Work this goes to Code (views.showCode). */
export function focusPane(paneId: string) {
  setLayout((l) => {
    for (const ws of l.workspaces)
      for (const tab of ws.tabs)
        if (findLeaf(tab.root, paneId)) {
          let next = updateTab(l, tab.id, (t) => ({ ...t, focus: paneId, zoom: t.zoom ? paneId : undefined }));
          next = selectTabIn(next, ws.id, tab.id);
          return next;
        }
    return l;
  });
  showCode();
}
