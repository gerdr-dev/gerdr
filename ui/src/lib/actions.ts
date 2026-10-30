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
  currentSpace,
  cycle,
  DEFAULT_SPACE,
  insertBeside,
  parentSplit,
  findLeaf,
  projectNameError,
  leaves,
  locateSession,
  neighbor,
  newPane,
  newTab,
  newWorkspace,
  rid,
  setPane,
  spaceList,
  spaceOf,
  spaceWorkspaces,
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
import { get, markRead, overviewShown, set, setLayout, setMode, toggleSidebar, useStore } from "./store";
import { overviewFocus, overviewKey, toggleOverview } from "./overview";
import { openWorkChat, workProjectOf } from "./work";
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

function confirmCloseFocusedPane() {
  const p = focusedLeaf(get().layout);
  const status = p?.sessionId ? get().sessions[p.sessionId]?.status : undefined;
  if (p && (status === "running" || status === "waiting")) {
    const armed = closeArmed?.paneId === p.id && Date.now() - closeArmed.at < 2000;
    if (!armed) {
      closeArmed = { paneId: p.id, at: Date.now() };
      toast("agent 还在跑，再按一次关闭", { id: "closepane", duration: 2000 });
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

/** 关掉 pane 里的终端：和 tmux 一样，关 pane 就是结束里面的 shell（不然它在服务端没人看得见地一直跑着）。 */
function killTerms(panes: PaneLeaf[]) {
  for (const p of panes) if (p.term) api.termKill(p.term.id).catch(() => {});
}

/**
 * 关掉的 pane 里的会话：布局里已经哪儿都不显示了，就把 agent 那边也关掉（claude 进程当场收掉，正在跑的那一轮算取消），
 * 和 herdr 关 pane 就结束里面的 agent 一个意思 —— 不然它没人看得见地挂着，claude 进程要白占几百 MB 到闲置超时。
 * 记录留着：⌘⇧T 能原样放回来，下次发话再 --resume 接上。要在 setLayout 之后调（看的是关完之后的布局）。
 */
function shutdownGone(panes: PaneLeaf[]) {
  const l = get().layout;
  for (const p of panes) if (p.sessionId && get().sessions[p.sessionId] && !locateSession(l, p.sessionId)) api.shutdown(p.sessionId).catch(() => {});
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

// ---- 撤销关闭（⌘⇧T）----
//
// 只记人手关的（关 pane、关 tab、两次 Ctrl+C 关掉 agent）；shell 自己退出、别的 agent 用 `gerdr session close` 关的不记。
// 只记在这个页面的内存里：刷新就没了，和浏览器的「重新打开关闭的标签页」一样够用。
// 会话关 pane 时 agent 那边关了、gerdr 的记录还在（shutdownGone），所以能原样接回来（下次发话再 --resume）；终端关的时候 shell 已经结束，回来的是同目录新开的一个。

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
  remember({ kind: "agent", paneId, sessionId: p.sessionId });
  setLayout((l) => setPane(l, paneId, { sessionId: undefined, draft: s ? { agent: s.agent, cwd: s.cwd } : undefined }));
  shutdownGone([p]);
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

// The default space is stored as "no space" so layouts written before spaces existed need no migration.
const inSpace = (space: string) => (space === DEFAULT_SPACE ? undefined : space);

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

// ---- sessions (herdr's: isolated groups of workspaces; Space in layout.ts) ----

/** The workspace to show when entering `space`: the one you left it on, else its first. Undefined if it has none. */
function landIn(l: Layout, space: string): string | undefined {
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

// ---- 对话里的「运行」放到终端里跑 ----
// 默认的「运行」在对话里跑（hub/shell.go），输出带给 agent，但没有 stdin；要交互的（gh auth login 选选项、输密码）放进终端跑，
// 代价是 agent 看不到输出。

/** 这个会话所在的 pane 和它的 tab（同一个会话开在几处时优先当前 tab） */
function sessionPane(l: Layout, sessionId: string) {
  const all = allLeaves(l).filter((x) => x.pane.sessionId === sessionId);
  const cur = activeTab(l).id;
  return all.find((x) => x.tab.id === cur) ?? all[0];
}

/** 会话旁边已经开着的终端：同一个 tab 里，焦点那个优先，不然是最后一个。没有返回 undefined */
export function terminalNear(sessionId: string): PaneLeaf | undefined {
  const at = sessionPane(get().layout, sessionId);
  if (!at) return undefined;
  const terms = leaves(at.tab.root).filter((p) => p.term);
  return terms.find((p) => p.id === at.tab.focus) ?? terms[terms.length - 1];
}

/**
 * 把命令（带回车）敲进终端。near：会话旁边已经开着的那个（terminalNear）；没有、或者它的 shell 已经没了（gerdr 重启过）
 * 就和 new 一样，在会话右边新开一个（目录 = 会话目录），焦点给终端：要交互的话人接着在里面操作。
 */
export async function runInTerminal(sessionId: string, cwd: string, command: string, where: "new" | "near") {
  const line = command + "\r";
  const near = where === "near" ? terminalNear(sessionId) : undefined;
  if (near?.term) {
    try {
      await api.termInput(near.term.id, line);
      focusPane(near.id);
      return;
    } catch {
      // shell 没了：落到下面新开一个
    }
  }
  let info;
  try {
    info = await api.termCreate({ cwd, input: line });
    if (!info?.id) throw new Error("后端没有终端接口，gerdr 要升级或重启");
  } catch (e: any) {
    toast.error(`开终端失败：${e.message ?? e}`);
    return;
  }
  const fresh: PaneLeaf = { type: "pane", id: rid("p"), term: { id: info.id, cwd: info.cwd } };
  setLayout((l) => {
    const at = sessionPane(l, sessionId);
    if (at) return updateTab(l, at.tab.id, (tab) => split(tab, at.pane.id, "row", fresh));
    const t = activeTab(l);
    return updateTab(l, t.id, (tab) => split(tab, t.zoom ?? t.focus, "row", fresh));
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
    // 没给模式时后端按设置里的默认模式 / 模型开（hub.Create），这里不用管
    const s = await api.createSession(o);
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
  const was = allLeaves(get().layout).find((x) => x.pane.id === paneId)?.pane;
  setPane0(paneId, { sessionId, draft: undefined });
  shutdownGone(was ? [was] : []); // 换下来的那个会话也不显示了
  focusPane(paneId);
}

/** 把一个会话放到眼前：已经在某个 pane 里就切过去，不在就开一个新 tab 放它。 */
/**
 * A Work agent conversation lives in its project's Work view (right-hand chat), never in a Code pane. Every way of bringing a
 * session up (a notification, next-needs-you, the palette, the session lists) goes through here first; without it they all
 * added a tab to whatever workspace was on screen, and the Work chat showed up in Code. Returns whether it handled the session.
 */
function openInWork(sessionId: string): boolean {
  if (locateSession(get().layout, sessionId)) return false; // already in a pane (from before this check): leave it there
  const project = workProjectOf(sessionId);
  if (!project) return false;
  if (currentSpace(get().layout) !== project && spaceList(get().layout).some((s) => s.id === project)) switchSpace(project);
  setMode("work");
  openWorkChat(sessionId, project);
  markRead(sessionId);
  return true;
}

export function focusSession(sessionId: string) {
  // Going to a session (notification, next-needs-you, the lists) means its pane: the overview would cover it
  if (get().overview.on) toggleOverview(false);
  if (openInWork(sessionId)) return;
  const loc = locateSession(get().layout, sessionId);
  if (loc) focusPane(loc.pane.id);
  else addTab({ sessionId });
  markRead(sessionId);
}

/** 在焦点 pane 里换成另一个会话。 */
export function openSessionHere(sessionId: string) {
  if (openInWork(sessionId)) return;
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

/**
 * ⌘S: put the composer's message (text and images) aside, press again on an empty composer to take it back — in any pane,
 * so a half-written prompt can move to another session or agent. One slot: stashing again replaces what was there.
 */
export function stashDraft() {
  const el = document.activeElement as HTMLElement | null;
  const leaf = focusedLeaf(get().layout);
  // The composer being typed in (also the Work view's, which isn't a pane), else the focused pane's
  const ovFocus = overviewShown() ? overviewFocus(get().overview, get().layout, get().sessions) : undefined;
  const paneId = el?.dataset.composer ?? (ovFocus ? `ov-${ovFocus}` : leaf?.sessionId ? leaf.id : undefined);
  const box = paneId ? document.querySelector<HTMLTextAreaElement>(`textarea[data-composer="${CSS.escape(paneId)}"]`) : null;
  if (!paneId || !box) return toast("没有可以暂存的输入框");
  const { drafts, draftImgs, stash } = get();
  const text = drafts[paneId] ?? "";
  const imgs = draftImgs[paneId] ?? [];
  if (text.trim() !== "" || imgs.length > 0) {
    // A video still uploading gets its path written back into this pane's draft when it finishes; stashed, it'd stay pathless
    if (imgs.some((im) => im.mimeType.startsWith("video/") && !im.path)) return toast("视频还在上传，传完再暂存");
    set((s) => ({ drafts: { ...s.drafts, [paneId]: "" }, draftImgs: { ...s.draftImgs, [paneId]: [] }, stash: { text, imgs } }));
  } else {
    if (!stash) return toast("没有暂存的消息");
    set((s) => ({ drafts: { ...s.drafts, [paneId]: stash.text }, draftImgs: { ...s.draftImgs, [paneId]: stash.imgs }, stash: null }));
  }
  requestAnimationFrame(() => {
    box.focus();
    box.setSelectionRange(box.value.length, box.value.length);
  });
}

export function focusedSessionId(): string | undefined {
  // With the overview up, the focused tile is the one you're on (not the pane hidden under it)
  if (overviewShown()) return overviewFocus(get().overview, get().layout, get().sessions);
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
  if (overviewShown() && overviewKey(a)) return;
  const l = get().layout;
  switch (a) {
    case "splitRight":
      return splitFocused("right");
    case "splitDown":
      return splitFocused("down");
    case "duplicateRight":
      return duplicateFocused("right");
    case "duplicateDown":
      return duplicateFocused("down");
    case "splitTerminal":
      return openTerminal("right");
    case "splitTerminalDown":
      return openTerminal("down");
    case "terminalTab":
      return openTerminal("tab");
    case "closePane":
      return confirmCloseFocusedPane();
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
    case "ws1":
    case "ws2":
    case "ws3":
    case "ws4":
    case "ws5":
    case "ws6":
    case "ws7":
    case "ws8":
    case "ws9":
      return selectWorkspaceN(Number(a.slice(2)) - 1);
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
    case "newSpace":
      return set({ dialog: { kind: "rename", target: "newSpace", id: "", value: "" } });
    case "nextSpace":
      return stepSpace(1);
    case "prevSpace":
      return stepSpace(-1);
    case "sessions":
      return set({ dialog: { kind: "sessions" } });
    case "toggleSidebar":
      return toggleSidebar();
    case "overview":
      if (get().mode === "work") setMode("agents");
      return toggleOverview();
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
    case "stash":
      return stashDraft();
  }
}

/**
 * After an overlay closes (store.dialog, the Lightbox), hand focus back to the focused pane: its composer / terminal
 * listens for gerdr:refocus. Radix restores focus to the dialog's trigger (often a sidebar button) when it unmounts
 * after its exit animation, so wait until no dialog is left in the DOM. Gives up if another one opens meanwhile.
 */
export function refocusAfterDialog() {
  const start = performance.now();
  const tick = () => {
    if (get().dialog) return;
    if (document.querySelector("[role=dialog]") && performance.now() - start < 1000) return void requestAnimationFrame(tick);
    window.dispatchEvent(new Event("gerdr:refocus"));
  };
  requestAnimationFrame(tick);
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
