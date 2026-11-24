import { type ReactNode, useContext, useEffect, useMemo, useRef } from "react";
import { ROW_DROP } from "@/components/TabDrag";
import { createPortal } from "react-dom";
import { addTab, addTabForSession, focusPane } from "@/lib/actions";
import { activeTab, currentSpace, locateSession, sideRows } from "@/lib/layout";
import { type CodeMenuFn, setCodeMenu } from "@/lib/codeMenu";
import { toggleOverview } from "@/lib/overview";
import { focusedSession, get, markRead, setCodeHost, useStore } from "@/lib/store";
import { PanelKeyContext } from "./visible";

/**
 * Code inside a panel: while the panel that renders this is the one shown, Gerdr shows Code's own layout (tabs, split
 * panes, terminals, the list beside them) in the panel's place. Code is not a special view: it is this, with Code's
 * default list. A panel makes its own Code from the same parts:
 *
 * - `sidebar` with `list="add"` (default): the panel's part above Code's own list (its workspaces);
 * - `sidebar` with `list="replace"`: the panel draws the whole list, grouping the same panes its own way (by a task's
 *   status, by person…) from useCodeRows, each row drawn with CodeRow so it looks and behaves as in Code.
 *
 * It is the same layout Code mode shows, not a second one: a second copy of the panes would mean two terminals on one
 * PTY and two composers per conversation. The rest of the panel's page isn't drawn while this is mounted.
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
 */
export function useCodeRows(): CodeRowInfo[] {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  return useMemo(() => {
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
  }, [layout, sessions]);
}

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
export function showInCodeView(id: string) {
  toggleOverview(false);
  const loc = locateSession(get().layout, id);
  if (loc) focusPane(loc.pane.id);
  else addTabForSession(id);
  markRead(id);
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
