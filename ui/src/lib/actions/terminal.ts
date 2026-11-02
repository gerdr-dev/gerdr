import { toast } from "sonner";
import { api } from "../api";
import { activeTab, activeWs, allLeaves, findLeaf, leaves, rid, setPane, split, updateTab, updateWs, type Layout, type PaneLeaf, type Tab } from "../layout";
import { get, setLayout } from "../store";
import { focusPane } from "./panes";
import { focusedLeaf, inheritDraft, selectTabIn } from "./shared";
import { t } from "../i18n";

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
    if (!info?.id) throw new Error(t("actionsTerminal.noBackend"));
  } catch (e: any) {
    toast.error(t("actionsTerminal.openFailed", { error: e.message ?? e }));
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
// The plain "run" runs in the chat (hub/shell.go) and hands the output to the agent, but has no stdin; interactive ones
// (gh auth login picking options, typing a password) go into a terminal. Their output stays out of the chat: the agent
// reads it with `gerdr pane read` (which finds the terminal next to it).

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
    if (!info?.id) throw new Error(t("actionsTerminal.noBackend"));
  } catch (e: any) {
    toast.error(t("actionsTerminal.openFailed", { error: e.message ?? e }));
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
