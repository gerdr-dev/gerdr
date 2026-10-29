// 所有「动作」：快捷键、命令面板、侧栏菜单、插件都调这里，各处不再自己写一份。
import { toast } from "sonner";
import { api, ApiError } from "./api";
import type { KeyAction } from "./keys";
import { stripLineAnchor } from "./paths";
import {
  activeTab,
  activeWs,
  allLeaves,
  attach,
  closePane,
  cycle,
  insertBeside,
  parentSplit,
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
  type Side,
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
    if (p.term?.cwd) return basename(p.term.cwd);
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
  if (p?.term?.cwd) return { cwd: p.term.cwd };
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
        const build = (n: ImportNode): PaneNode =>
          "sessionId" in n ? newPane(undefined, n.sessionId) : { type: "split", id: rid("s"), dir: n.dir, ratio: n.ratio, a: build(n.a), b: build(n.b) };
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
  if (p) rememberPane(activeWs(l), activeTab(l), p);
  if (p?.sessionId) {
    const s = get().sessions[p.sessionId];
    // 关 pane 不删会话（它还在会话列表里，prefix+g 能找回来），但正在跑的那一轮停掉。
    if (s && (s.status === "running" || s.status === "waiting")) api.cancel(s.id).catch(() => {});
  }
  killTerms(p ? [p] : []);
  setLayout((l) => {
    const ws = activeWs(l);
    const t = activeTab(l);
    const next = closePane(t, t.focus);
    if (next) return updateTab(l, t.id, () => next);
    return removeTab(l, ws, t.id);
  });
}

/**
 * 关掉显示某个会话的 pane（`gerdr session close`，别的 agent 关它开的会话）：和人手动关一样，
 * 正在跑的那一轮停掉，会话记录留着（`gerdr session open` / prefix+g 能找回来）。
 */
export function closeSessionPanes(sessionId: string) {
  if (!sessionId || !locateSession(get().layout, sessionId)) return;
  const s = get().sessions[sessionId];
  if (s && (s.status === "running" || s.status === "waiting")) api.cancel(s.id).catch(() => {});
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

/** 关掉 pane 里的终端：和 tmux 一样，关 pane 就是结束里面的 shell（不然它在服务端没人看得见地一直跑着）。 */
function killTerms(panes: PaneLeaf[]) {
  for (const p of panes) if (p.term) api.termKill(p.term.id).catch(() => {});
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
  if (tab && !onlyBlank(ws, tab)) remember({ kind: "tab", wsId: ws.id, index: ws.tabs.indexOf(tab), tab });
  for (const p of tab ? leaves(tab.root) : []) {
    const s = p.sessionId ? get().sessions[p.sessionId] : undefined;
    if (s && (s.status === "running" || s.status === "waiting")) api.cancel(s.id).catch(() => {});
  }
  killTerms(tab ? leaves(tab.root) : []);
  setLayout((l) => {
    const w = l.workspaces.find((x) => x.id === ws.id);
    return w ? removeTab(l, w, id) : l;
  });
}

// ---- 撤销关闭（⌘⇧T）----
//
// 只记人手关的（关 pane、关 tab、两次 Ctrl+C 关掉 agent）；shell 自己退出、别的 agent 用 `gerdr session close` 关的不记。
// 只记在这个页面的内存里：刷新就没了，和浏览器的「重新打开关闭的标签页」一样够用。
// 会话关 pane 时只是停掉这一轮、记录还在，所以能原样接回来；终端关的时候 shell 已经结束，回来的是同目录新开的一个。

type Closed =
  | { kind: "tab"; wsId: string; index: number; tab: Tab }
  | { kind: "pane"; wsId: string; tabId: string; pane: PaneLeaf; beside: string; dir: "row" | "col"; first: boolean; ratio: number }
  | { kind: "agent"; paneId: string; sessionId: string };

const closed: Closed[] = [];

function remember(c: Closed) {
  closed.push(c);
  if (closed.length > 30) closed.shift();
}

/** 里面什么都没有的 pane / tab（还没开会话的空 pane）。 */
const blankLeaf = (p: PaneLeaf) => !p.sessionId && !p.term && !p.sub;
const blankTab = (t: Tab) => !t.name && t.root.type === "pane" && blankLeaf(t.root);
/**
 * 空 tab 也要记：⌘T 开个新 tab 随手 ⌘W 关掉，再按 ⌘⇧T 却说「没有刚关掉的」，和浏览器对不上（用户报的）。
 * 只有工作区里唯一的那个空 tab 不记：关掉它会立刻补一个一模一样的空 tab，记了只会让 ⌘⇧T 多按一次才回到真正关掉的东西。
 */
const onlyBlank = (ws: Workspace, t: Tab) => ws.tabs.length === 1 && blankTab(t);

/** 关 pane 之前记下它在哪：有兄弟就记兄弟（摆回它旁边），tab 里只剩它就当关了整个 tab。 */
function rememberPane(ws: Workspace, tab: Tab, p: PaneLeaf) {
  if (blankLeaf(p)) {
    // 两次 Ctrl+C 退出 agent、接着 Ctrl+D 关掉这个空 pane：撤销时把会话连 pane 一起放回原位，而不是另开一个 tab
    const last = closed.at(-1);
    if (last?.kind === "agent" && last.paneId === p.id) {
      closed.pop();
      p = { type: "pane", id: p.id, sessionId: last.sessionId };
    } else if (onlyBlank(ws, tab)) return;
  }
  const par = parentSplit(tab.root, p.id);
  if (!par) return remember({ kind: "tab", wsId: ws.id, index: ws.tabs.indexOf(tab), tab });
  const first = par.a.id === p.id;
  remember({ kind: "pane", wsId: ws.id, tabId: tab.id, pane: p, beside: (first ? par.b : par.a).id, dir: par.dir, first, ratio: par.ratio });
}

/** 撤销回来的 pane：会话没了 / 已经在别处开着就留个同 agent 同目录的空 pane；终端换成待重开的。 */
function revive(p: PaneLeaf, l: Layout): PaneLeaf {
  if (p.term) return { ...p, term: { id: "", cwd: p.term.cwd } };
  if (p.sessionId) {
    const s = get().sessions[p.sessionId];
    if (!s || locateSession(l, p.sessionId)) return { type: "pane", id: p.id, draft: s ? { agent: s.agent, cwd: s.cwd } : p.draft };
  }
  return p;
}

function reviveTree(n: PaneNode, l: Layout): PaneNode {
  return n.type === "pane" ? revive(n, l) : { ...n, a: reviveTree(n.a, l), b: reviveTree(n.b, l) };
}

export function reopenClosed() {
  const c = closed.pop();
  if (!c) {
    toast("没有刚关掉的 tab / pane");
    return;
  }
  const l = get().layout;
  if (c.kind === "agent") {
    const loc = locateSession(l, c.sessionId);
    if (loc) return focusPane(loc.pane.id);
    const at = allLeaves(l).find((x) => x.pane.id === c.paneId && blankLeaf(x.pane));
    if (at) {
      setLayout((l) => setPane(l, c.paneId, { sessionId: c.sessionId, draft: undefined }));
      return focusPane(c.paneId);
    }
    return addTab({ sessionId: c.sessionId });
  }
  if (c.kind === "pane") {
    // 单个会话 pane 已经在别处开着（prefix+g 找回来过）：切过去就行
    if (c.pane.sessionId && locateSession(l, c.pane.sessionId)) return focusSession(c.pane.sessionId);
    const pane = revive(c.pane, l);
    setLayout((l) => {
      const ws = l.workspaces.find((w) => w.tabs.some((t) => t.id === c.tabId));
      const tab = ws?.tabs.find((t) => t.id === c.tabId);
      if (ws && tab) {
        // 原来的兄弟被关了 / 挪走了：贴在整个 tab 右边
        const next = insertBeside(tab, c.beside, pane, c.dir, c.first, c.ratio) ?? attach(tab, undefined, "right", pane);
        return selectTabIn(
          updateTab(l, tab.id, () => ({ ...next, focus: pane.id })),
          ws.id,
          tab.id,
        );
      }
      // tab 也没了：单独开一个
      const w = l.workspaces.find((x) => x.id === c.wsId) ?? activeWs(l);
      const t: Tab = { id: rid("t"), root: pane, focus: pane.id };
      return selectTabIn(
        updateWs(l, w.id, (w) => ({ ...w, tabs: [...w.tabs, t] })),
        w.id,
        t.id,
      );
    });
    reopenTerms([pane]);
    return;
  }
  const root = reviveTree(c.tab.root, l);
  const tab: Tab = { ...c.tab, root };
  setLayout((l) => {
    const w = l.workspaces.find((x) => x.id === c.wsId) ?? activeWs(l);
    // 关的是最后一个 tab 时补上的那个空 tab：顶掉它
    let tabs = w.tabs.length === 1 && blankTab(w.tabs[0]) ? [] : [...w.tabs];
    tabs = tabs.filter((t) => t.id !== tab.id);
    tabs.splice(Math.min(c.index, tabs.length), 0, tab);
    return selectTabIn(
      updateWs(l, w.id, (w) => ({ ...w, tabs })),
      w.id,
      tab.id,
    );
  });
  reopenTerms(leaves(root));
}

function reopenTerms(panes: PaneLeaf[]) {
  for (const p of panes) if (p.term && !p.term.id) reopenTerminal(p.id, p.term.cwd);
}

/**
 * 两次 Ctrl+C（照 Claude Code 退出）：关掉这个 pane 里的 agent，pane 留着、回到「新会话」那一屏（同 agent 同目录），
 * 要关 pane 再按 Ctrl+D。会话记录还在，⌘⇧T / prefix+g 能接回来。
 */
export function closeAgentInPane(paneId: string) {
  const l = get().layout;
  const p = allLeaves(l).find((x) => x.pane.id === paneId)?.pane;
  if (!p?.sessionId) return;
  const s = get().sessions[p.sessionId];
  if (s && (s.status === "running" || s.status === "waiting")) api.cancel(s.id).catch(() => {});
  remember({ kind: "agent", paneId, sessionId: p.sessionId });
  setLayout((l) => setPane(l, paneId, { sessionId: undefined, draft: s ? { agent: s.agent, cwd: s.cwd } : undefined }));
  focusPane(paneId);
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

/** 把 tab 挪到另一个工作空间（拖到侧栏的工作空间上）：放到最后、切过去看着它。 */
export function moveTabToWorkspace(tabId: string, wsId: string) {
  setLayout((l) => {
    const fw = l.workspaces.find((w) => w.tabs.some((t) => t.id === tabId));
    const tab = fw?.tabs.find((t) => t.id === tabId);
    if (!fw || !tab || fw.id === wsId || !l.workspaces.some((w) => w.id === wsId)) return l;
    const out = updateWs(removeTab(l, fw, tabId), wsId, (w) => ({ ...w, tabs: [...w.tabs, tab], activeTab: tab.id, lastTab: w.activeTab }));
    return { ...out, active: wsId, lastActive: l.active };
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
  const w0 = get().layout.workspaces.find((w) => w.id === (id ?? get().layout.active));
  killTerms(w0 ? w0.tabs.flatMap((t) => leaves(t.root)) : []);
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

// ---- 终端 ----

/**
 * 开一个终端。where：在焦点 pane 右边 / 下面分屏、新 tab，或者把某个空 pane（还没开会话的）换成终端。
 * 先在服务端建好再放进布局：布局是几个客户端共用的，「pane 里有终端但还没建」这种中间态让别的客户端看见，
 * 它们会各自去建一个。
 */
export async function openTerminal(where: "right" | "down" | "tab" | { pane: string; cwd?: string }) {
  const l = get().layout;
  const p = focusedLeaf(l);
  const here = typeof where === "object" ? where : undefined;
  const cwd = here?.cwd ?? inheritDraft(l)?.cwd ?? get().home;
  let info;
  try {
    // 从终端旁边开的：跟着它 shell 现在的目录（cd 过之后分屏，和 tmux 一样）
    info = await api.termCreate({ cwd, from: !here && p?.term ? p.term.id : undefined });
    // 后端太旧（没有终端接口）时可能回一页 HTML 而不是报错：没 id 就当失败，别把空终端写进布局
    if (!info?.id) throw new Error("后端没有终端接口，gerdr 要升级或重启");
  } catch (e: any) {
    toast.error(`开终端失败：${e.message ?? e}`);
    return;
  }
  const term = { id: info.id, cwd: info.cwd };
  if (here) {
    setLayout((l) => setPane(l, here.pane, { term, draft: undefined, sessionId: undefined }));
    return;
  }
  if (where === "tab") {
    setLayout((l) => {
      const ws = activeWs(l);
      const pane: PaneLeaf = { type: "pane", id: rid("p"), term };
      const t: Tab = { id: rid("t"), root: pane, focus: pane.id };
      return selectTabIn(
        updateWs(l, ws.id, (w) => ({ ...w, tabs: [...w.tabs, t] })),
        ws.id,
        t.id,
      );
    });
    return;
  }
  setLayout((l) => {
    const t = activeTab(l);
    const fresh: PaneLeaf = { type: "pane", id: rid("p"), term };
    // 建终端那一会儿焦点可能换过 tab：还在的话贴着发起的那个 pane 分，不在了就贴当前焦点
    const at = p && findLeaf(t.root, p.id) ? p.id : (t.zoom ?? t.focus);
    return updateTab(l, t.id, (tab) => split(tab, at, where === "right" ? "row" : "col", fresh));
  });
}

/** 终端没了（gerdr 重启过 / shell 退了）：在同一个 pane、同一个目录重开一个。 */
export async function reopenTerminal(paneId: string, cwd: string) {
  return openTerminal({ pane: paneId, cwd });
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
    case "splitTerminal":
      return openTerminal("right");
    case "splitTerminalDown":
      return openTerminal("down");
    case "terminalTab":
      return openTerminal("tab");
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
    case "reopenClosed":
      return reopenClosed();
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
      // 写短了的路径对上好几个文件（服务端按后缀找的）：让人挑一个
      if (e instanceof ApiError && e.candidates?.length) {
        if (seq === openSeq) set({ dialog: { kind: "pickFile", path: p, candidates: e.candidates } });
        return;
      }
      err = e;
    }
  }
  if (seq === openSeq) toast.error((err as Error)?.message ?? "打不开这个路径");
}

/**
 * 对话里「排队中」那条插话的两个按钮：撤回（收回来，回调方放进输入框改）、立即发（收回后按 now 重发，打断它手上这一步）。
 * 已经被 agent 读到的收不回（按下去的那一刻它刚好读到），提示一下。撤回时返回收回的内容（字和图）。
 */
export async function withdrawSteer(sessionId: string, messageId: string, now: boolean): Promise<ContentBlock[] | undefined> {
  try {
    const r = await api.withdraw(sessionId, messageId);
    if (!r.withdrawn) {
      toast("它已经读到这条了");
      return;
    }
    const content = r.content ?? [];
    if (now) {
      await api.send(sessionId, content, undefined, true);
      return;
    }
    return content;
  } catch (e: any) {
    toast.error(e.message ?? String(e));
  }
}
