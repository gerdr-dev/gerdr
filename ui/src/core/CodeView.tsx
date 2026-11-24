import { type ReactNode, useContext, useEffect, useMemo } from "react";
import { createPortal } from "react-dom";
import { addTab, addTabForSession, focusPane } from "@/lib/actions";
import { activeTab, currentSpace, locateSession, sideRows } from "@/lib/layout";
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
