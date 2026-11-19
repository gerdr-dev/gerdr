import { useRef } from "react";
import { addTabForSession, focusSession } from "@/lib/actions";
import { locateSession } from "@/lib/layout";
import { toggleOverview } from "@/lib/overview";
import { get, setMode, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";

/** A conversation's summary (title, status, agent, cwd…), kept current; undefined while unknown. */
export function useSession(id: string | undefined): SessionSummary | undefined {
  return useStore((s) => (id ? s.sessions[id] : undefined));
}

/**
 * Whether the conversation has a turn that ended unseen (gerdr's unread: the green dot in Code's tabs and list). It
 * clears when the conversation is shown — in Code, or in a panel's SessionChat while that has `focused`.
 */
export function useUnread(id: string | undefined): boolean {
  return useStore((s) => !!(id && s.unread[id]));
}

/**
 * Every conversation `match` picks (all without one: this computer's and the other computers', "<host>:<id>"), kept
 * current from the same push as the rest of gerdr. The same array until a picked one changes or the set does, so a
 * page re-renders only for the sessions it cares about.
 */
export function useSessions(match?: (s: SessionSummary) => boolean): SessionSummary[] {
  const last = useRef<SessionSummary[]>([]);
  const pick = useRef(match);
  pick.current = match;
  return useStore((s) => {
    const next: SessionSummary[] = [];
    for (const x of Object.values(s.sessions)) if (!pick.current || pick.current(x)) next.push(x);
    // Not taken for granted to be an array: a page hot-replaced with its hooks reordered can hand this ref another
    // hook's old value for one render (seen: the board crashed on the update that added this call)
    const prev = last.current;
    if (Array.isArray(prev) && prev.length === next.length && prev.every((x, i) => x === next[i])) return prev;
    last.current = next;
    return next;
  });
}

/**
 * Go to the conversation in Code (its pane, else a tab in the workspace for its directory), whoever owns it: gerdr's
 * own drawer and the old bridge's openSession ask for exactly that. Not for panels (core doesn't export it).
 */
export function openInCode(id: string) {
  toggleOverview(false);
  setMode("agents");
  if (locateSession(get().layout, id)) focusSession(id);
  else addTabForSession(id);
}
