import {
  activeTab,
  activeWs,
  allLeaves,
  attach,
  closePane,
  findLeaf,
  leaves,
  newTab,
  rid,
  sideRows,
  spaceOf,
  updateTab,
  updateWs,
  type Side,
  type Tab,
} from "../layout";
import { get, setLayout, useStore } from "../store";
import { onlyBlank, remember } from "./reopen";
import { createInPane } from "./sessions";
import { inheritDraft, killTerms, removeTab, selectTabIn, shutdownGone } from "./shared";

// ---- tab ----

export function addTab(opts?: { workspaceId?: string; cwd?: string; agent?: string; name?: string; sessionId?: string; after?: string; before?: string }) {
  setLayout((l) => {
    const ws = opts?.workspaceId ? (l.workspaces.find((w) => w.id === opts.workspaceId) ?? activeWs(l)) : activeWs(l);
    const draft = opts?.sessionId
      ? undefined
      : { ...(inheritDraft(l) ?? {}), ...(opts?.cwd ? { cwd: opts.cwd } : {}), ...(opts?.agent ? { agent: opts.agent } : {}) };
    const t = newTab(draft, opts?.sessionId, opts?.name);
    const tabs = [...ws.tabs];
    let at = tabs.length;
    if (opts?.after) at = tabs.findIndex((x) => x.id === opts.after) + 1;
    if (opts?.before)
      at = Math.max(
        0,
        tabs.findIndex((x) => x.id === opts.before),
      );
    tabs.splice(at, 0, t);
    return selectTabIn(
      updateWs(l, ws.id, (w) => ({ ...w, tabs })),
      ws.id,
      t.id,
    );
  });
}

export function selectTab(index: number) {
  setLayout((l) => {
    const ws = activeWs(l);
    const t = ws.tabs[index];
    return t ? selectTabIn(l, ws.id, t.id) : l;
  });
}

export function stepTab(delta: number) {
  setLayout((l) => {
    const ws = activeWs(l);
    const i = ws.tabs.findIndex((t) => t.id === ws.activeTab);
    const t = ws.tabs[(i + delta + ws.tabs.length) % ws.tabs.length];
    return selectTabIn(l, ws.id, t.id);
  });
}

// The tab focused before the current one, across workspaces (and sessions). Workspace.lastTab only knows its own
// workspace, so jumping between two tabs in different workspaces had no shortcut. Kept in memory per window: it's
// "where I just was", not layout. A tab that was closed doesn't become the previous one (it can't be returned to).
let curTab = activeTab(get().layout).id;
let prevTab = "";
useStore.subscribe((s, p) => {
  if (s.layout === p.layout) return;
  const id = activeTab(s.layout).id;
  if (id === curTab) return;
  if (curTab && s.layout.workspaces.some((w) => w.tabs.some((t) => t.id === curTab))) prevTab = curTab;
  curTab = id;
});

/** Back to the previously focused tab, in whatever workspace it is; focus lands on the pane it had when you left. */
export function lastTab() {
  setLayout((l) => {
    const ws = l.workspaces.find((w) => w.tabs.some((t) => t.id === prevTab));
    return ws ? selectTabIn(l, ws.id, prevTab) : l;
  });
}

export function closeTab(tabId?: string) {
  const l = get().layout;
  const ws = l.workspaces.find((w) => w.tabs.some((t) => t.id === (tabId ?? activeWs(l).activeTab))) ?? activeWs(l);
  const id = tabId ?? ws.activeTab;
  const tab = ws.tabs.find((t) => t.id === id);
  if (tab && !onlyBlank(ws, tab)) remember({ kind: "tab", wsId: ws.id, index: ws.tabs.indexOf(tab), tab });
  killTerms(tab ? leaves(tab.root) : []);
  setLayout((l) => {
    const w = l.workspaces.find((x) => x.id === ws.id);
    return w ? removeTab(l, w, id) : l;
  });
  shutdownGone(tab ? leaves(tab.root) : []);
}

export function renameTab(tabId: string, name: string) {
  setLayout((l) => updateTab(l, tabId, (t) => ({ ...t, name: name.trim() || undefined })));
}

export function moveTab(wsId: string, from: number, to: number) {
  setLayout((l) =>
    updateWs(l, wsId, (w) => {
      const tabs = [...w.tabs];
      const [t] = tabs.splice(from, 1);
      tabs.splice(to, 0, t);
      return { ...w, tabs };
    }),
  );
}

/**
 * 把 fromId 这个 tab 整个并进 toId（拖 tab 到别的 tab 上 / 别的 pane 的某一边）：它的分屏原样搬过去，
 * 贴在 paneId 那个 pane（不给 = 整个 tab）的 side 那边，焦点跟过去。会话、终端都不动，只是换个地方显示。
 * 反过来拆开用 paneToTab。
 */
export function mergeTab(fromId: string, toId: string, paneId: string | undefined, side: Side) {
  if (fromId === toId) return;
  setLayout((l) => {
    const fw = l.workspaces.find((w) => w.tabs.some((t) => t.id === fromId));
    const from = fw?.tabs.find((t) => t.id === fromId);
    const tw = l.workspaces.find((w) => w.tabs.some((t) => t.id === toId));
    if (!fw || !from || !tw) return l;
    let out = updateTab(l, toId, (t) => ({ ...attach(t, paneId, side, from.root), focus: from.focus }));
    out = removeTab(
      out,
      out.workspaces.find((w) => w.id === fw.id)!,
      fromId,
    );
    // 「上一个 tab」别指向刚被并掉的那个
    const gone = (id?: string) => !id || id === fromId || id === toId;
    const last = !gone(tw.activeTab) ? tw.activeTab : !gone(tw.lastTab) ? tw.lastTab : undefined;
    out = updateWs(out, tw.id, (w) => ({ ...w, activeTab: toId, lastTab: last }));
    return tw.id === l.active ? out : { ...out, active: tw.id, lastActive: l.active };
  });
}

/** 把一个 pane 拆出来单独成一个 tab，放在原 tab 后面：合并 tab 的反操作。tab 里只有它一个时不动。 */
export function paneToTab(paneId: string) {
  setLayout((l) => {
    for (const w of l.workspaces)
      for (const t of w.tabs) {
        const p = findLeaf(t.root, paneId);
        if (!p) continue;
        const rest = closePane(t, paneId);
        if (!rest) return l;
        const nt: Tab = { id: rid("t"), root: p, focus: p.id };
        return updateWs(l, w.id, (w) => ({ ...w, tabs: w.tabs.flatMap((x) => (x.id === t.id ? [rest, nt] : [x])), activeTab: nt.id, lastTab: t.id }));
      }
    return l;
  });
}

/**
 * 拖 pane 标题栏把一个 pane 挪走：贴到 tabId 那个 tab 里 paneId 那个 pane（不给 = 整个 tab）的 side 那边（同一个 tab 里换位置也走这里），
 * 或者单独成一个新 tab，插在 wsId 那个工作空间的第 index 个（不给 = 最后）。焦点跟过去。
 * tab 里只有它一个时不动 —— 那种情况拖的是整个 tab（mergeTab / moveTab / moveTabToWorkspace）。
 */
export function movePane(paneId: string, to: { tabId: string; paneId?: string; side: Side } | { wsId: string; index?: number }) {
  setLayout((l) => {
    const src = allLeaves(l).find((x) => x.pane.id === paneId);
    const rest = src && closePane(src.tab, paneId);
    if (!src || !rest) return l;
    let out = updateTab(l, src.tab.id, () => rest);
    let wsId: string;
    if ("tabId" in to) {
      const tw = out.workspaces.find((w) => w.tabs.some((t) => t.id === to.tabId));
      if (!tw || (to.paneId && to.paneId === paneId)) return l;
      wsId = tw.id;
      out = updateTab(out, to.tabId, (t) => ({ ...attach(t, to.paneId, to.side, src.pane), focus: paneId }));
      out = updateWs(out, wsId, (w) => (w.activeTab === to.tabId ? w : { ...w, activeTab: to.tabId, lastTab: w.activeTab }));
    } else {
      if (!out.workspaces.some((w) => w.id === to.wsId)) return l;
      wsId = to.wsId;
      const nt: Tab = { id: rid("t"), root: src.pane, focus: paneId };
      out = updateWs(out, wsId, (w) => {
        const tabs = [...w.tabs];
        tabs.splice(to.index ?? tabs.length, 0, nt);
        return { ...w, tabs, activeTab: nt.id, lastTab: w.activeTab };
      });
    }
    return wsId === l.active ? out : { ...out, active: wsId, lastActive: l.active };
  });
}

/** 把 tab 挪到另一个工作空间（拖到侧栏的工作空间上）：放到最后、切过去看着它。 */
export function moveTabToWorkspace(tabId: string, wsId: string, index?: number) {
  setLayout((l) => {
    const fw = l.workspaces.find((w) => w.tabs.some((t) => t.id === tabId));
    const tab = fw?.tabs.find((t) => t.id === tabId);
    if (!fw || !tab || fw.id === wsId || !l.workspaces.some((w) => w.id === wsId)) return l;
    // Its panes' sidebar places go too: they follow the tab to the new workspace's group
    const ids = new Set(leaves(tab.root).map((p) => p.id));
    l = { ...l, workspaces: l.workspaces.map((w) => (w.rows?.some((id) => ids.has(id)) ? { ...w, rows: w.rows.filter((id) => !ids.has(id)) } : w)) };
    const out = updateWs(removeTab(l, fw, tabId), wsId, (w) => {
      const tabs = [...w.tabs];
      tabs.splice(index ?? tabs.length, 0, tab);
      return { ...w, tabs, activeTab: tab.id, lastTab: w.activeTab };
    });
    return { ...out, active: wsId, lastActive: l.active };
  });
}

/**
 * The sidebar only: list pane `paneId` in group `wsId` at `index` (counted without it). Tabs and splits stay as they are:
 * the sidebar's groups are just how the list is arranged (layout.sideRows).
 */
export function placeRow(paneId: string, wsId: string, index: number) {
  setLayout((l) => {
    const owner = allLeaves(l).find((x) => x.pane.id === paneId);
    const groups = owner && sideRows(l, spaceOf(owner.ws));
    const rows = groups?.get(wsId);
    if (!rows) return l;
    const ids = rows.map((r) => r.pane.id).filter((id) => id !== paneId);
    ids.splice(Math.min(index, ids.length), 0, paneId);
    return {
      ...l,
      workspaces: l.workspaces.map((w) =>
        w.id === wsId ? { ...w, rows: ids } : w.rows?.includes(paneId) ? { ...w, rows: w.rows.filter((x) => x !== paneId) } : w,
      ),
    };
  });
}

/** 复制一个 tab：同样的分屏结构，每个 pane 用同样的 agent / 目录开新会话（对话本身复制不了）。 */
export async function duplicateTab(tabId: string, where: "after" | "before") {
  const l = get().layout;
  const ws = l.workspaces.find((w) => w.tabs.some((t) => t.id === tabId));
  const tab = ws?.tabs.find((t) => t.id === tabId);
  if (!ws || !tab) return;
  const { sessions } = get();
  const clone = (n: any): any => {
    if (n.type === "pane") {
      const s = n.sessionId ? sessions[n.sessionId] : undefined;
      // 终端复制不了里面的东西：留一个同目录的空 pane，在那儿点「终端」再开
      return { type: "pane", id: rid("p"), draft: s ? { agent: s.agent, cwd: s.cwd } : n.term ? { cwd: n.term.cwd } : n.draft };
    }
    return { ...n, id: rid("s"), a: clone(n.a), b: clone(n.b) };
  };
  const root = clone(tab.root);
  const t: Tab = { id: rid("t"), name: tab.name, root, focus: leaves(root)[0].id };
  setLayout((l) =>
    selectTabIn(
      updateWs(l, ws.id, (w) => {
        const tabs = [...w.tabs];
        const i = tabs.findIndex((x) => x.id === tabId);
        tabs.splice(where === "after" ? i + 1 : i, 0, t);
        return { ...w, tabs };
      }),
      ws.id,
      t.id,
    ),
  );
  // 一个 pane 的直接开起来；多个的留着让人逐个确认（一下子起好几个 agent 进程太重）。
  const ls = leaves(root);
  if (ls.length === 1 && ls[0].draft?.agent && ls[0].draft?.cwd) {
    await createInPane(ls[0].id, { agent: ls[0].draft.agent, cwd: ls[0].draft.cwd });
  }
}
