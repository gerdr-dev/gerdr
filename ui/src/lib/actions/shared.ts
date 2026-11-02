// Helpers the action files share. lib/actions.ts does not re-export them (apart from basename).
import { api } from "../api";
import { activeTab, activeWs, DEFAULT_SPACE, findLeaf, locateSession, newTab, updateWs, type Layout, type PaneLeaf, type Workspace } from "../layout";
import { get } from "../store";

export const basename = (p: string) => p.replace(/\/+$/, "").split("/").pop() || p;

export function focusedLeaf(l: Layout): PaneLeaf | undefined {
  const t = activeTab(l);
  return findLeaf(t.root, t.focus);
}

/** 新 pane 默认继承：焦点那个会话的目录和 agent。 */
export function inheritDraft(l: Layout): PaneLeaf["draft"] {
  const p = focusedLeaf(l);
  const s = p?.sessionId ? get().sessions[p.sessionId] : undefined;
  if (s) return { agent: s.agent, cwd: s.cwd };
  if (p?.term?.cwd) return { cwd: p.term.cwd };
  if (p?.draft) return { ...p.draft };
  const ws = activeWs(l);
  return ws.cwd ? { cwd: ws.cwd } : undefined;
}

/** 关掉 pane 里的终端：和 tmux 一样，关 pane 就是结束里面的 shell（不然它在服务端没人看得见地一直跑着）。 */
export function killTerms(panes: PaneLeaf[]) {
  for (const p of panes) if (p.term) api.termKill(p.term.id).catch(() => {});
}

/**
 * 关掉的 pane 里的会话：布局里已经哪儿都不显示了，就把 agent 那边也关掉（claude 进程当场收掉，正在跑的那一轮算取消），
 * 和 herdr 关 pane 就结束里面的 agent 一个意思 —— 不然它没人看得见地挂着，claude 进程要白占几百 MB 到闲置超时。
 * 记录留着：⌘⇧T 能原样放回来，下次发话再 --resume 接上。要在 setLayout 之后调（看的是关完之后的布局）。
 */
export function shutdownGone(panes: PaneLeaf[]) {
  const l = get().layout;
  for (const p of panes) if (p.sessionId && get().sessions[p.sessionId] && !locateSession(l, p.sessionId)) api.shutdown(p.sessionId).catch(() => {});
}

export function removeTab(l: Layout, ws: Workspace, tabId: string): Layout {
  const idx = ws.tabs.findIndex((t) => t.id === tabId);
  const tabs = ws.tabs.filter((t) => t.id !== tabId);
  if (tabs.length === 0) {
    // 最后一个 tab 关了：留一个空的（工作空间不跟着消失，要关工作空间用 prefix+shift+d）。
    const t = newTab(ws.cwd ? { cwd: ws.cwd } : undefined);
    return updateWs(l, ws.id, (w) => ({ ...w, tabs: [t], activeTab: t.id }));
  }
  const next = tabs[Math.min(idx, tabs.length - 1)];
  return updateWs(l, ws.id, (w) => ({ ...w, tabs, activeTab: w.activeTab === tabId ? next.id : w.activeTab }));
}

export function selectTabIn(l: Layout, wsId: string, tabId: string): Layout {
  let next = l;
  if (l.active !== wsId) next = { ...next, active: wsId, lastActive: l.active };
  return updateWs(next, wsId, (w) => (w.activeTab === tabId ? w : { ...w, activeTab: tabId, lastTab: w.activeTab }));
}

// The default space is stored as "no space" so layouts written before spaces existed need no migration.
export const inSpace = (space: string) => (space === DEFAULT_SPACE ? undefined : space);
