import { toast } from "sonner";
import {
  activeWs,
  allLeaves,
  attach,
  insertBeside,
  parentSplit,
  leaves,
  locateSession,
  rid,
  setPane,
  updateTab,
  updateWs,
  type Layout,
  type PaneLeaf,
  type PaneNode,
  type Tab,
  type Workspace,
} from "../layout";
import { get, setLayout } from "../store";
import { focusPane } from "./panes";
import { focusSession } from "./sessions";
import { selectTabIn, shutdownGone } from "./shared";
import { addTab } from "./tabs";
import { reopenTerminal } from "./terminal";

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

export function remember(c: Closed) {
  closed.push(c);
  if (closed.length > 30) closed.shift();
}

/** 里面什么都没有的 pane / tab（还没开会话的空 pane）。 */
const blankLeaf = (p: PaneLeaf) => !p.sessionId && !p.term && !p.sub && !p.file;
const blankTab = (t: Tab) => !t.name && t.root.type === "pane" && blankLeaf(t.root);
/**
 * 空 tab 也要记：⌘T 开个新 tab 随手 ⌘W 关掉，再按 ⌘⇧T 却说「没有刚关掉的」，和浏览器对不上（用户报的）。
 * 只有工作区里唯一的那个空 tab 不记：关掉它会立刻补一个一模一样的空 tab，记了只会让 ⌘⇧T 多按一次才回到真正关掉的东西。
 */
export const onlyBlank = (ws: Workspace, t: Tab) => ws.tabs.length === 1 && blankTab(t);

/** 关 pane 之前记下它在哪：有兄弟就记兄弟（摆回它旁边），tab 里只剩它就当关了整个 tab。 */
export function rememberPane(ws: Workspace, tab: Tab, p: PaneLeaf) {
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
