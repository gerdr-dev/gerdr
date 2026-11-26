import { type ReactNode, useContext, useEffect, useMemo, useRef } from "react";
import { ROW_DROP } from "@/components/TabDrag";
import { createPortal } from "react-dom";
import { addTab, addTabForSession, focusPane, replaceSessionInPane } from "@/lib/actions";
import { activeTab, currentSpace, locateSession, sideRows, type Layout } from "@/lib/layout";
import { ownerHost, showOwner } from "@/lib/hostView";
import { type CodeCloseFn, type CodeMenuFn, type CodePaneTagFn, setCodeCloseFallback, setCodeJump, setCodeMenu, setCodePaneTag } from "@/lib/codeMenu";
import { toggleOverview } from "@/lib/overview";
import { syncWatch } from "@/lib/sync";
import {
  type CodeDir,
  type CodeStartFn,
  codeLayout,
  focusedSession,
  get,
  layoutOf,
  panelsApart,
  markRead,
  set,
  setCodeDirs,
  setCodeHost,
  setCodeOrder,
  setCodeOverview,
  setCodeStart,
  hostOrder,
  useStore,
} from "@/lib/store";
import { PanelKeyContext } from "./visible";
import { frozenOrder, useModFrozen, useModShown } from "@/lib/modHeld";

/**
 * Code inside a panel: while the panel that renders this is the one shown, Gerdr shows Code's own layout (tabs, split
 * panes, terminals, the list beside them) in the panel's place. Code is not a special view: it is this, with Code's
 * default list. A panel makes its own Code from the same parts:
 *
 * - `sidebar` with `list="add"` (default): the panel's part above Code's own list (its workspaces);
 * - `sidebar` with `list="replace"`: the panel draws the whole list, grouping the same panes its own way (by a task's
 *   status, by person…) from useCodeRows, each row drawn with CodeRow so it looks and behaves as in Code.
 *
 * The panes are Code's own kind (Panes.tsx: the same tabs / splits / terminals), but with list="replace" they are the
 * panel's own, stored and held apart from Code mode's (store/layoutSync.ts, lib/hostView.ts): its conversations open
 * there, Code's stay in Code mode. A conversation is never in both: a panel imports one by starting a new one that
 * carries it on. list="add" shows Code's own layout.
 * docs/dev/HOSTED-CODE.md. The rest of the panel's page isn't drawn while this is mounted.
 */
export function CodeView({ sidebar, list = "add" }: { sidebar?: ReactNode; list?: "add" | "replace" }) {
  const key = useContext(PanelKeyContext);
  useEffect(() => {
    if (!key) return;
    setCodeHost(key, { list });
    return () => setCodeHost(key, null);
  }, [key, list]);
  // The slot is only there while this panel is the one shown (UnifiedSidebar): another hosting panel kept mounted,
  // hidden, must not draw into it
  const slot = useStore((s) => {
    if (!key || s.mode !== "work") return null;
    const space = currentSpace(s.layout);
    const panel = s.workPanels[space];
    return (panel ? `${space}/${panel}` : space) === key ? s.codeSlot : null;
  });
  return slot && sidebar ? createPortal(sidebar, slot) : null;
}

/** A row of Code's list: one pane of the current project (a conversation, a terminal, a file, a browser page…). */
export type CodeRowInfo = {
  /** The pane's id: what CodeRow and focusCodeRow take. */
  pane: string;
  kind: "session" | "terminal" | "file" | "browser" | "subagent" | "new";
  /** Its conversation (kind "session"; a subagent's pane: the conversation it belongs to). */
  session?: string;
  /** The directory it works in, when it has one. */
  cwd?: string;
  /** The workspace its tab is in (Code's own grouping). */
  workspace: { id: string; name: string; cwd?: string };
  /** The pane in front. */
  current: boolean;
};

/**
 * The panes of the current project, as Code lists them (each workspace's rows, pinned first, then latest activity):
 * what a panel groups its own way under CodeView list="replace". Follow the conversations' state with useSessions.
 * A hosting panel has a layout of its own (lib/hostView.ts): by default these are its panes; `"code"` gives Code
 * mode's instead (what the person has open there: what a panel offers to import).
 */
export function useCodeRows(of: "here" | "code" = "here"): CodeRowInfo[] {
  // This panel's own panes (on screen or put aside: its list is kept up while Code mode shows), or Code's
  const key = useContext(PanelKeyContext);
  const layout = useStore((s) => (of === "code" ? codeLayout(s) : (key && layoutOf(key, s)) || (panelsApart() ? EMPTY : codeLayout(s))));
  const sessions = useStore((s) => s.sessions);
  return useMemo(() => {
    if (layout === EMPTY) return [];
    const cur = activeTab(layout);
    const out: CodeRowInfo[] = [];
    for (const rows of sideRows(layout, undefined, sessions).values())
      for (const { ws, tab, pane } of rows) {
        const s = pane.sessionId ? sessions[pane.sessionId] : undefined;
        out.push({
          pane: pane.id,
          kind: pane.sessionId ? "session" : pane.term ? "terminal" : pane.file ? "file" : pane.browser ? "browser" : pane.sub ? "subagent" : "new",
          session: pane.sessionId ?? pane.sub?.sessionId,
          cwd: s?.cwd ?? pane.term?.cwd ?? pane.draft?.cwd,
          workspace: { id: ws.id, name: ws.name, cwd: ws.cwd },
          current: tab.id === cur.id && tab.focus === pane.id,
        });
      }
    return out;
  }, [layout, sessions, of]);
}

const EMPTY: Layout = { version: 1, workspaces: [], active: "" };

/** Code's own row for a pane (icon, status, unread, its menu, dragging it onto a pane): `where` replaces the folder shown at its end. */
export { PaneRow as CodeRow } from "@/components/UnifiedSidebar";

/**
 * Code's default list, the very part Code mode puts beside its panes: "active" and the project's workspaces. For a
 * panel that draws the list itself (list="replace") and wants it somewhere in its own: `<><Mine /><CodeList /></>`
 * is what list="add" does.
 */
export { CodeList } from "@/components/UnifiedSidebar";

/** A new tab in Code (the "new conversation" page, where the agent and folder are picked), in `cwd` when given. */
export function newCodeTab(opts?: { cwd?: string }) {
  toggleOverview(false);
  addTab(opts?.cwd ? { cwd: opts.cwd } : undefined);
}

/**
 * Show a conversation as a pane of the hosted Code: its pane when it has one, else a new tab in the workspace for its
 * directory. For the hosting panel's own conversations, which Gerdr otherwise hands back to the panel to show
 * (onShowSession): here "showing it" is a pane like any other.
 */
export function showInCodeView(id: string, replace?: string) {
  // In its own side's panes: this panel's own for its conversations (lib/hostView.ts), on screen first
  showOwner(ownerHost(id));
  if (replace && replace !== id) return replaceSessionInPane(replace, id);
  toggleOverview(false);
  const loc = locateSession(get().layout, id);
  if (loc) focusPane(loc.pane.id);
  else addTabForSession(id);
  markRead(id);
}

/**
 * Show a conversation in the hosted Code and scroll it to the card with `ref` (a panel tool's gerdrCard.ref), flashing
 * it: from a list of what waits on the person straight to the question. Older parts of a long chat are loaded until it
 * is found.
 */
export function jumpInCode(session: string, ref: string) {
  showInCodeView(session);
  setCodeJump(session, ref);
}

/** The conversation in the focused pane of Code's layout, for the panel's part to mark its row. */
export function useCodeSession(): string | undefined {
  return useStore((s) => focusedSession(s.layout));
}

/**
 * A place in the panel's list that takes rows dropped on it (a group: a status, a person): put the returned ref on
 * its element. While a row of Code (CodeRow, a pane's title bar) is dragged over it, it lights up as Code's own
 * groups do; dropped, `fn` gets the pane and its conversation. Nothing moves in Code's layout: what landing there
 * means is the panel's (a task changes status).
 */
export function useCodeRowDrop(fn: (row: { pane: string; session?: string }) => void): (el: HTMLElement | null) => void {
  const f = useRef(fn);
  f.current = fn;
  const at = useRef<HTMLElement | null>(null);
  const on = useRef((e: Event) => f.current((e as CustomEvent).detail)).current;
  return useRef((el: HTMLElement | null) => {
    at.current?.removeEventListener(ROW_DROP, on);
    at.current = el;
    if (!el) return;
    el.dataset.rowDrop = "";
    el.addEventListener(ROW_DROP, on);
  }).current;
}

/**
 * The panel's own items for a pane's menus: its ⋯ menu and its row's right-click menu (a task's status: 移到待验收).
 * `fn` is asked each time a menu is drawn, with the pane and its conversation; return [] for none. On a phone the ⋯
 * menu is the only place for such a thing: give there whatever your list does by hover, drag or right click.
 */
export function useCodeRowMenu(fn: CodeMenuFn) {
  const key = useContext(PanelKeyContext);
  const f = useRef(fn);
  f.current = fn;
  useEffect(() => {
    if (!key) return;
    setCodeMenu(key, (row) => f.current(row));
    return () => setCodeMenu(key, null);
  }, [key]);
}

export type { CodeMenuItem } from "@/lib/codeMenu";

/**
 * ⌘W / ⇧⌘W where Gerdr has nothing to close (with useCodeOrder, a conversation alone on screen: only splits and
 * untouched new conversations close): `fn` gets the pane and its conversation, to answer the key the panel's way (ask
 * where a task goes now). Without it the key does nothing there.
 */
export function useCodeCloseFallback(fn: CodeCloseFn) {
  const key = useContext(PanelKeyContext);
  const f = useRef(fn);
  f.current = fn;
  useEffect(() => {
    if (!key) return;
    setCodeCloseFallback(key, (row) => f.current(row));
    return () => setCodeCloseFallback(key, null);
  }, [key]);
}

/**
 * Close the phone's drawer (Code's list over the page): after something in the panel's part of the list opened what
 * shows behind it (its settings). Code's own rows do it when picked; nothing to do on a wide screen.
 */
export function closeCodeDrawer() {
  if (get().drawer) set({ drawer: false });
}

/**
 * The folders the new-conversation page offers as quick picks while this panel hosts Code, in place of Code's own (the
 * project's workspaces): for a panel with its own idea of where work goes (the repositories in its settings). [] or
 * unmounting gives Code's back.
 */
export function useCodeDirs(dirs: CodeDir[]) {
  const key = useContext(PanelKeyContext);
  const sig = JSON.stringify(dirs);
  useEffect(() => {
    if (!key) return;
    setCodeDirs(key, JSON.parse(sig));
    return () => setCodeDirs(key, null);
  }, [key, sig]);
}

export type { CodeDir } from "@/lib/store";

/**
 * The conversations this panel's list shows, in its order: while it hosts Code, the keys go by it, not by Code's tabs
 * and workspaces (which it doesn't show). ⌘1–9 / prefix+1–9 the Nth (numbered on its rows while ⌘ is held: CodeRow
 * by itself, a row of the panel's own with useCodeNumber), ⇧⌘] / ⇧⌘[ the next / previous, ⌘E the one before, ⌘W lands
 * on the neighbour; the workspace keys do nothing. Call it where the list is ordered, with every row that has a
 * conversation, opened in Code or not.
 */
export function useCodeOrder(sessions: string[]) {
  const key = useContext(PanelKeyContext);
  const sig = sessions.join(",");
  useEffect(() => {
    if (!key) return;
    setCodeOrder(key, sig ? sig.split(",") : []);
    return () => setCodeOrder(key, null);
  }, [key, sig]);
}

/**
 * The conversations the phone overview pages through while this panel hosts Code, in this order: its strip of tabs
 * and the swipe between them. Without it the overview is Code's "active" (every agent that ran in the project), which
 * the panel's list doesn't show. Give the ones worth swiping between (at work, waiting on the person), not the whole
 * list; [] is an empty overview, unmounting gives Code's back.
 */
export function useCodeOverview(sessions: string[]) {
  const key = useContext(PanelKeyContext);
  const sig = sessions.join(",");
  useEffect(() => {
    if (!key) return;
    setCodeOverview(key, sig ? sig.split(",") : []);
    syncWatch(); // the pages around the shown tile are streamed (overviewState.phonePages): they move with the list
    return () => (setCodeOverview(key, null), syncWatch());
  }, [key, sig]);
}

/** The number ⌘N puts on this conversation's row while ⌘ is held (1–9), for a row the panel draws itself; else undefined. */
export function useCodeNumber(session?: string): number | undefined {
  const shown = useModShown();
  useModFrozen();
  if (!shown || !session) return undefined;
  const i = frozenOrder(hostOrder() ?? []).indexOf(session);
  return i >= 0 && i < 9 ? i + 1 : undefined;
}

/**
 * While this panel hosts Code, conversations started in Code go through `fn` instead of becoming Code's (createInPane:
 * the new-conversation page, ⌘D, a new tab that starts at once; the overview's new agent; a fork): the panel starts
 * one of its own (a task's worker, with its tools and rules), sends nothing, and answers with its id. Gerdr types in
 * gerdr-core.d.ts list what isn't routed.
 */
export function useCodeStart(fn: CodeStartFn) {
  const key = useContext(PanelKeyContext);
  const f = useRef(fn);
  f.current = fn;
  useEffect(() => {
    if (!key) return;
    setCodeStart(key, (o) => f.current(o));
    return () => setCodeStart(key, null);
  }, [key]);
}

/**
 * A tag after a pane's title, from the panel: what it knows about the conversation in that pane (a task's number and
 * status), and with `items` a menu on it (move it). `fn` is asked whenever this component renders, so call the hook
 * where the data it reads lives. Return null for no tag, a list for several (each its own chip and menu).
 */
export function useCodePaneTag(fn: CodePaneTagFn) {
  const key = useContext(PanelKeyContext);
  const f = useRef(fn);
  f.current = fn;
  // Every render: what fn answers follows the panel's own state, which only its renders tell of
  useEffect(() => {
    if (key) setCodePaneTag(key, (row) => f.current(row));
  });
  useEffect(() => () => void (key && setCodePaneTag(key, null)), [key]);
}

export type { CodePaneTag } from "@/lib/codeMenu";
