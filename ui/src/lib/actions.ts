// 所有「动作」：快捷键、命令面板、侧栏菜单、插件都调这里，各处不再自己写一份。
import { toast } from "sonner";
import { api } from "./api";
import type { KeyAction } from "./keys";
import { stripLineAnchor } from "./paths";
import {
  activeTab,
  activeWs,
  allLeaves,
  closePane,
  cycle,
  findLeaf,
  leaves,
  locateSession,
  neighbor,
  newPane,
  newTab,
  newWorkspace,
  rid,
  setPane,
  split,
  swap,
  updateTab,
  updateWs,
  type Layout,
  type PaneLeaf,
  type PaneNode,
  type Tab,
  type Workspace,
} from "./layout";
import { get, markRead, set, setLayout, toggleSidebar } from "./store";
import type { ContentBlock } from "./types";

export const basename = (p: string) => p.replace(/\/+$/, "").split("/").pop() || p;

/** tab 的默认名字：用户起过就用它，不然是它第一个会话所在目录的名字。 */
export function tabName(tab: Tab): string {
  if (tab.name) return tab.name;
  const { sessions } = get();
  for (const p of leaves(tab.root)) {
    const s = p.sessionId ? sessions[p.sessionId] : undefined;
    if (s) return basename(s.cwd);
    if (p.draft?.cwd) return basename(p.draft.cwd);
  }
  return "new";
}

function focusedLeaf(l: Layout): PaneLeaf | undefined {
  const t = activeTab(l);
  return findLeaf(t.root, t.focus);
}

/** 新 pane 默认继承：焦点那个会话的目录和 agent。 */
function inheritDraft(l: Layout): PaneLeaf["draft"] {
  const p = focusedLeaf(l);
  const s = p?.sessionId ? get().sessions[p.sessionId] : undefined;
  if (s) return { agent: s.agent, cwd: s.cwd };
  if (p?.draft) return { ...p.draft };
  const ws = activeWs(l);
  return ws.cwd ? { cwd: ws.cwd } : undefined;
}

export function splitFocused(dir: "right" | "down") {
  setLayout((l) => {
    const t = activeTab(l);
    const fresh: PaneLeaf = { type: "pane", id: rid("p"), draft: inheritDraft(l) };
    return updateTab(l, t.id, (tab) => split(tab, tab.zoom ?? tab.focus, dir === "right" ? "row" : "col", fresh));
  });
}

/**
 * 别的 agent 用 `gerdr session new` 开的会话：放在发起它的那个会话旁边（右边分一屏）。
 * 默认不抢焦点（人可能正在别处打字）；找不到发起方就在当前工作空间后台开个 tab。
 */
export function openSessionNear(sessionId: string, near?: string, focus = false) {
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
    const ws = activeWs(l);
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
        const build = (n: ImportNode): PaneNode => ("sessionId" in n ? newPane(undefined, n.sessionId) : { type: "split", id: rid("s"), dir: n.dir, ratio: n.ratio, a: build(n.a), b: build(n.b) });
        const root = build(t.tree);
        const firstLeaf = (n: ImportNode): ImportNode & { cwd: string } => ("sessionId" in n ? n : firstLeaf(n.a));
        const name = t.label && t.label !== basename(firstLeaf(t.tree).cwd) ? t.label : undefined;
        const focus = root.type === "pane" ? root.id : leaves(root)[0].id;
        return { id: rid("t"), root, focus, name };
      });
      const ws: Workspace = { id: rid("w"), name: w.label, cwd: "sessionId" in w.tabs[0].tree ? w.tabs[0].tree.cwd : undefined, tabs, activeTab: tabs[0].id };
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
  if (p?.sessionId) {
    const s = get().sessions[p.sessionId];
    // 关 pane 不删会话（它还在会话列表里，prefix+g 能找回来），但正在跑的那一轮停掉。
    if (s && (s.status === "running" || s.status === "waiting")) api.cancel(s.id).catch(() => {});
  }
  setLayout((l) => {
    const ws = activeWs(l);
    const t = activeTab(l);
    const next = closePane(t, t.focus);
    if (next) return updateTab(l, t.id, () => next);
    return removeTab(l, ws, t.id);
  });
}

function removeTab(l: Layout, ws: Workspace, tabId: string): Layout {
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
}

function selectTabIn(l: Layout, wsId: string, tabId: string): Layout {
  let next = l;
  if (l.active !== wsId) next = { ...next, active: wsId, lastActive: l.active };
  return updateWs(next, wsId, (w) => (w.activeTab === tabId ? w : { ...w, activeTab: tabId, lastTab: w.activeTab }));
}

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

export function lastTab() {
  setLayout((l) => {
    const ws = activeWs(l);
    return ws.lastTab && ws.tabs.some((t) => t.id === ws.lastTab) ? selectTabIn(l, ws.id, ws.lastTab) : l;
  });
}

export function closeTab(tabId?: string) {
  const l = get().layout;
  const ws = l.workspaces.find((w) => w.tabs.some((t) => t.id === (tabId ?? activeWs(l).activeTab))) ?? activeWs(l);
  const id = tabId ?? ws.activeTab;
  const tab = ws.tabs.find((t) => t.id === id);
  for (const p of tab ? leaves(tab.root) : []) {
    const s = p.sessionId ? get().sessions[p.sessionId] : undefined;
    if (s && (s.status === "running" || s.status === "waiting")) api.cancel(s.id).catch(() => {});
  }
  setLayout((l) => {
    const w = l.workspaces.find((x) => x.id === ws.id);
    return w ? removeTab(l, w, id) : l;
  });
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
      return { type: "pane", id: rid("p"), draft: s ? { agent: s.agent, cwd: s.cwd } : n.draft };
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

// ---- 工作空间 ----

export function addWorkspace(name: string, cwd?: string) {
  setLayout((l) => {
    const w = newWorkspace(name || `workspace ${l.workspaces.length + 1}`, cwd);
    return { ...l, workspaces: [...l.workspaces, w], active: w.id, lastActive: l.active };
  });
}

export function selectWorkspace(id: string) {
  setLayout((l) => (l.active === id ? l : { ...l, active: id, lastActive: l.active }));
}

export function stepWorkspace(delta: number) {
  setLayout((l) => {
    const i = l.workspaces.findIndex((w) => w.id === l.active);
    const w = l.workspaces[(i + delta + l.workspaces.length) % l.workspaces.length];
    return { ...l, active: w.id, lastActive: l.active };
  });
}

export function renameWorkspace(id: string, name: string) {
  setLayout((l) => updateWs(l, id, (w) => ({ ...w, name: name.trim() || w.name })));
}

export function closeWorkspace(id?: string) {
  setLayout((l) => {
    const wid = id ?? l.active;
    const workspaces = l.workspaces.filter((w) => w.id !== wid);
    if (workspaces.length === 0) {
      const w = newWorkspace("default");
      return { ...l, workspaces: [w], active: w.id };
    }
    const active = l.active === wid ? (l.lastActive && workspaces.some((w) => w.id === l.lastActive) ? l.lastActive : workspaces[0].id) : l.active;
    return { ...l, workspaces, active };
  });
}

export function moveWorkspace(from: number, to: number) {
  setLayout((l) => {
    const ws = [...l.workspaces];
    const [w] = ws.splice(from, 1);
    ws.splice(to, 0, w);
    return { ...l, workspaces: ws };
  });
}

// ---- 会话 ----

export async function createInPane(paneId: string, o: { agent: string; cwd: string; mode?: string; import?: string; title?: string }) {
  setPane0(paneId, { draft: { agent: o.agent, cwd: o.cwd } });
  try {
    const mode = o.mode ?? get().config.defaultModes[o.agent];
    const s = await api.createSession({ ...o, mode: mode || undefined });
    set((st) => ({ sessions: { ...st.sessions, [s.id]: s } }));
    setLayout((l) => setPane(l, paneId, { sessionId: s.id, draft: undefined }));
    return s;
  } catch (e: any) {
    toast.error(`开会话失败：${e.message ?? e}`);
    throw e;
  }
}

function setPane0(paneId: string, patch: Partial<PaneLeaf>) {
  setLayout((l) => setPane(l, paneId, patch));
}

/** /resume：这个 pane 换成另一个会话。那个会话已经在别的 pane 里开着就直接切过去（一个会话不在两处同时显示）。 */
export function resumeInPane(paneId: string, sessionId: string) {
  const loc = locateSession(get().layout, sessionId);
  if (loc) return focusPane(loc.pane.id);
  setPane0(paneId, { sessionId, draft: undefined });
  focusPane(paneId);
}

/** 把一个会话放到眼前：已经在某个 pane 里就切过去，不在就开一个新 tab 放它。 */
export function focusSession(sessionId: string) {
  const loc = locateSession(get().layout, sessionId);
  if (loc) focusPane(loc.pane.id);
  else addTab({ sessionId });
  markRead(sessionId);
}

/** 在焦点 pane 里换成另一个会话。 */
export function openSessionHere(sessionId: string) {
  const loc = locateSession(get().layout, sessionId);
  if (loc) return focusPane(loc.pane.id);
  const p = focusedLeaf(get().layout);
  if (p && !p.sessionId) setLayout((l) => setPane(l, p.id, { sessionId, draft: undefined }));
  else addTab({ sessionId });
}

export async function deleteSession(id: string) {
  await api.deleteSession(id);
  setLayout((l) => {
    const loc = locateSession(l, id);
    return loc ? setPane(l, loc.pane.id, { sessionId: undefined, draft: { agent: get().sessions[id]?.agent, cwd: get().sessions[id]?.cwd } }) : l;
  });
}

export async function send(sessionId: string, content: ContentBlock[] | string) {
  const blocks: ContentBlock[] = typeof content === "string" ? [{ type: "text", text: content }] : content;
  return api.send(sessionId, blocks);
}

export function stopFocused() {
  const id = focusedSessionId();
  if (id) api.cancel(id).catch(() => {});
}

export function focusedSessionId(): string | undefined {
  return focusedLeaf(get().layout)?.sessionId;
}

/** 要你处理的会话：在等你的最优先，然后是出错的、跑完了没看的。 */
export function attentionList(): string[] {
  const { sessions, unread } = get();
  const all = Object.values(sessions);
  const rank = (s: (typeof all)[number]) => (s.status === "waiting" ? 0 : s.status === "error" ? 1 : unread[s.id] ? 2 : 9);
  return all
    .filter((s) => rank(s) < 9)
    .sort((a, b) => rank(a) - rank(b) || b.updatedAt - a.updatedAt)
    .map((s) => s.id);
}

export function stepAttention(delta: number) {
  const list = attentionList();
  if (!list.length) {
    toast("没有要你处理的会话");
    return;
  }
  const cur = focusedSessionId();
  const i = cur ? list.indexOf(cur) : -1;
  const next = list[(i + delta + list.length) % list.length];
  focusSession(next);
}

// ---- 快捷键动作 ----

export function runAction(a: KeyAction) {
  const l = get().layout;
  switch (a) {
    case "splitRight":
      return splitFocused("right");
    case "splitDown":
      return splitFocused("down");
    case "closePane":
      return closeFocusedPane();
    case "zoom":
      return toggleZoom();
    case "focusLeft":
      return focusDir("left");
    case "focusDown":
      return focusDir("down");
    case "focusUp":
      return focusDir("up");
    case "focusRight":
      return focusDir("right");
    case "swapLeft":
      return swapDir("left");
    case "swapDown":
      return swapDir("down");
    case "swapUp":
      return swapDir("up");
    case "swapRight":
      return swapDir("right");
    case "nextPane":
      return cyclePane(1);
    case "prevPane":
      return cyclePane(-1);
    case "newTab":
      return addTab();
    case "nextTab":
      return stepTab(1);
    case "prevTab":
      return stepTab(-1);
    case "lastTab":
      return lastTab();
    case "closeTab":
      return closeTab();
    case "renameTab": {
      const t = activeTab(l);
      return set({ dialog: { kind: "rename", target: "tab", id: t.id, value: t.name ?? tabName(t) } });
    }
    case "tab1":
    case "tab2":
    case "tab3":
    case "tab4":
    case "tab5":
    case "tab6":
    case "tab7":
    case "tab8":
    case "tab9":
      return selectTab(Number(a.slice(3)) - 1);
    case "newWorkspace":
      return set({ dialog: { kind: "newWorkspace" } });
    case "renameWorkspace": {
      const w = activeWs(l);
      return set({ dialog: { kind: "rename", target: "workspace", id: w.id, value: w.name } });
    }
    case "closeWorkspace":
      return closeWorkspace();
    case "workspaces":
      return set({ dialog: { kind: "workspaces" } });
    case "nextWorkspace":
      return stepWorkspace(1);
    case "prevWorkspace":
      return stepWorkspace(-1);
    case "sessions":
      return set({ dialog: { kind: "sessions" } });
    case "toggleSidebar":
      return toggleSidebar();
    case "help":
      return set({ dialog: { kind: "help" } });
    case "settings":
      return set({ dialog: { kind: "settings" } });
    case "importHerdr":
      return set({ dialog: { kind: "importHerdr" } });
    case "palette":
      return set({ dialog: { kind: "palette" } });
    case "nextAttention":
      return stepAttention(1);
    case "prevAttention":
      return stepAttention(-1);
    case "focusAttention": {
      const first = attentionList()[0];
      if (first) focusSession(first);
      else toast("没有在等你的会话");
      return;
    }
    case "renamePane": {
      const id = focusedSessionId();
      if (!id) return;
      return set({ dialog: { kind: "rename", target: "session", id, value: get().sessions[id]?.title ?? "" } });
    }
    case "stop":
      return stopFocused();
  }
}

// ---- 文件 ----

let openSeq = 0;

/**
 * 原地打开一条路径（对话里点的）。先按原样问一次，找不到再把行号锚点剥掉重试：
 * agent 爱写 `a.md#L21` / `main.go:42`，那几位不是文件名的一部分；但 `notes#1.md` 是合法文件名，所以不能先剥。
 * 慢的时候（路径在网络盘上）点了别的就丢掉迟到的结果，免得过一会儿突然弹出一个查看器。
 */
export async function openPath(raw: string, cwd?: string) {
  const seq = ++openSeq;
  (document.activeElement as HTMLElement | null)?.blur?.(); // 手机上收键盘，不然查看器只露半截
  const tries = [raw];
  const bare = stripLineAnchor(raw);
  if (bare && bare !== raw) tries.push(bare);
  let err: unknown;
  for (const p of tries) {
    try {
      const stat = await api.fileStat(p, cwd);
      if (seq !== openSeq) return;
      if (stat.dir) {
        toast(`这是个目录：${stat.path}`);
        return;
      }
      set({ dialog: { kind: "file", stat } });
      return;
    } catch (e) {
      err = e;
    }
  }
  if (seq === openSeq) toast.error((err as Error)?.message ?? "打不开这个路径");
}
