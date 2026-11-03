import { toast } from "sonner";
import { api } from "../api";
import { askConfirm } from "../confirm";
import { allLeaves, currentSpace, locateSession, setPane, spaceList, type PaneLeaf } from "../layout";
import { get, markRead, overviewShown, set, setLayout, setMode } from "../store";
import { focusTile, overviewIds, replaceTile, toggleOverview } from "../overview";
import { isOpen } from "../overviewState";
import { openWorkChat, workProjectOf } from "../work";
import { currentView } from "./views";
import type { ContentBlock } from "../types";
import { focusPane } from "./panes";
import { focusedLeaf, shutdownGone } from "./shared";
import { switchSpace } from "./spaces";
import { addTab } from "./tabs";
import { t } from "../i18n";

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
    toast.error(t("actionsSessions.openFailed", { error: e.message ?? e }));
    throw e;
  }
}

function setPane0(paneId: string, patch: Partial<PaneLeaf>) {
  setLayout((l) => setPane(l, paneId, patch));
}

/** /resume：这个 pane 换成另一个会话。那个会话已经在别的 pane 里开着就直接切过去（一个会话不在两处同时显示）。 */
export function resumeInPane(paneId: string, sessionId: string) {
  if (paneId.startsWith("ov-")) return resumeInTile(paneId.slice(3), sessionId);
  const loc = locateSession(get().layout, sessionId);
  if (loc) return focusPane(loc.pane.id);
  const was = allLeaves(get().layout).find((x) => x.pane.id === paneId)?.pane;
  setPane0(paneId, { sessionId, draft: undefined });
  shutdownGone(was ? [was] : []); // 换下来的那个会话也不显示了
  focusPane(paneId);
}

/**
 * /resume in an overview tile (OverviewTile.tsx gives its chat the pane id `ov-<session>`, which is no pane in the layout:
 * the code above found nothing to change, or jumped to a pane hidden under the grid, so nothing visible happened).
 * The tile switches to the picked conversation. Already open somewhere: it only takes this tile's place in the grid,
 * the panes stay as they are (the grid is a view over them). Not open: the pane underneath switches to it, as /resume
 * there would.
 */
function resumeInTile(cur: string, sessionId: string) {
  const before = overviewIds(get());
  const st = get();
  if (!isOpen(st.layout, st.config, sessionId)) {
    const here = locateSession(st.layout, cur);
    if (!here) return void toast(t("actionsSessions.notInPane")); // a Work conversation: no pane to switch
    setPane0(here.pane.id, { sessionId, draft: undefined });
    shutdownGone([here.pane]);
  }
  replaceTile(before, cur, sessionId);
}

/**
 * Picking an earlier conversation while another agent is chosen (the new-session page, /resume): carry it on with that
 * agent in this pane. Same agent = just resume it. A different one gets a new session that copies the conversation and
 * hands it to the new agent with the next message (hub/handoff.go); the original stays as it was.
 */
export async function continueInPane(paneId: string, sessionId: string, agent: string) {
  const from = get().sessions[sessionId];
  if (!from || from.agent === agent) return resumeInPane(paneId, sessionId);
  try {
    const s = await api.createSession({ agent, cwd: from.cwd, continueFrom: sessionId });
    set((st) => ({ sessions: { ...st.sessions, [s.id]: s } }));
    resumeInPane(paneId, s.id);
    return s;
  } catch (e: any) {
    toast.error(t("actionsSessions.switchAgentFailed", { error: e.message ?? e }));
    throw e;
  }
}

/** 把一个会话放到眼前：已经在某个 pane 里就切过去，不在就开一个新 tab 放它。 */
/**
 * A Work agent conversation lives in its project's Work view (right-hand chat), never in a Code pane. Every way of bringing a
 * session up (a notification, next-needs-you, the palette, the session lists) goes through here first; without it they all
 * added a tab to whatever workspace was on screen, and the Work chat showed up in Code. Returns whether it handled the session.
 */
export function openInWork(sessionId: string): boolean {
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
  // Already a tile in the overview on screen: go to that tile, closing the overview would only lose the user's place
  const st = get();
  if (overviewShown() && overviewIds(st).includes(sessionId)) {
    focusTile(sessionId);
    markRead(sessionId);
    return;
  }
  // Otherwise going to a session (notification, next-needs-you, the lists) means its pane: the overview would cover it
  if (st.overview.on) toggleOverview(false);
  // Already the one in front (in Work: the page's conversation or the Work agent itself): stay
  if (currentView().session() === sessionId) return markRead(sessionId);
  if (openInWork(sessionId)) return;
  const loc = locateSession(get().layout, sessionId);
  if (loc) focusPane(loc.pane.id);
  else addTab({ sessionId });
  markRead(sessionId);
}

/** 在焦点 pane 里换成另一个会话。 */
export function openSessionHere(sessionId: string) {
  // "Here" is the focused pane, and in Work that's hidden: open it the way going to a session does
  if (currentView().name === "work") return focusSession(sessionId);
  if (openInWork(sessionId)) return;
  const loc = locateSession(get().layout, sessionId);
  if (loc) return focusPane(loc.pane.id);
  const p = focusedLeaf(get().layout);
  if (p && !p.sessionId) setLayout((l) => setPane(l, p.id, { sessionId, draft: undefined }));
  else addTab({ sessionId });
}

/**
 * "Close session": end the conversation but keep the pane, turned back into a new-session page for the same agent and
 * directory — what you want when you're done with a chat and about to start the next one. Nothing is lost: the session
 * stays in the sessions list and can be reopened. Unlike closing the pane this doesn't disturb the split / tab layout.
 * Only asks when the agent is mid-turn, since closing cancels that turn.
 */
export async function closeSession(id: string) {
  const s = get().sessions[id];
  if (!s) return;
  if (s.status === "running" || s.status === "waiting") {
    const ok = await askConfirm({
      title: t("actionsSessions.closeRunningTitle"),
      description: t("actionsSessions.closeRunningBody"),
      action: t("common.close"),
      destructive: true,
    });
    if (!ok) return;
  }
  setLayout((l) => {
    for (let loc = locateSession(l, id); loc; loc = locateSession(l, id))
      l = setPane(l, loc.pane.id, { sessionId: undefined, draft: { agent: s.agent, cwd: s.cwd } });
    return l;
  });
  api.shutdown(id).catch(() => {});
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
  // The composer being typed in, else the one on screen in this view
  const paneId = el?.dataset.composer ?? currentView().composer();
  const box = paneId ? document.querySelector<HTMLTextAreaElement>(`textarea[data-composer="${CSS.escape(paneId)}"]`) : null;
  if (!paneId || !box) return toast(t("actionsSessions.noComposer"));
  const { drafts, draftImgs, stash } = get();
  const text = drafts[paneId] ?? "";
  const imgs = draftImgs[paneId] ?? [];
  if (text.trim() !== "" || imgs.length > 0) {
    // A video still uploading gets its path written back into this pane's draft when it finishes; stashed, it'd stay pathless
    if (imgs.some((im) => im.mimeType.startsWith("video/") && !im.path)) return toast(t("actionsSessions.videoUploading"));
    set((s) => ({ drafts: { ...s.drafts, [paneId]: "" }, draftImgs: { ...s.draftImgs, [paneId]: [] }, stash: { text, imgs } }));
  } else {
    if (!stash) return toast(t("actionsSessions.noStash"));
    set((s) => ({ drafts: { ...s.drafts, [paneId]: stash.text }, draftImgs: { ...s.draftImgs, [paneId]: stash.imgs }, stash: null }));
  }
  requestAnimationFrame(() => {
    box.focus();
    box.setSelectionRange(box.value.length, box.value.length);
  });
}

/** The conversation in front of the user: the focused pane's, the focused tile's, the Work chat (views.ts). */
export const focusedSessionId = (): string | undefined => currentView().session();

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
    toast(t("actionsSessions.noAttention"));
    return;
  }
  const cur = focusedSessionId();
  const i = cur ? list.indexOf(cur) : -1;
  const next = list[(i + delta + list.length) % list.length];
  focusSession(next);
}

/**
 * 对话里「排队中」那条插话的两个按钮：撤回（收回来，回调方放进输入框改）、立即发（收回后按 now 重发，打断它手上这一步）。
 * 已经被 agent 读到的收不回（按下去的那一刻它刚好读到），提示一下。撤回时返回收回的内容（字和图）。
 */
export async function withdrawSteer(sessionId: string, messageId: string, now: boolean): Promise<ContentBlock[] | undefined> {
  try {
    const r = await api.withdraw(sessionId, messageId);
    if (!r.withdrawn) {
      toast(t("actionsSessions.alreadyRead"));
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
