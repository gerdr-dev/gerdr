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
 * Go to the conversation in Code (its pane, else a tab in the workspace for its directory), whoever owns it: gerdr's
 * own drawer and the old bridge's openSession ask for exactly that. Not for panels (core doesn't export it).
 */
export function openInCode(id: string) {
  toggleOverview(false);
  setMode("agents");
  if (locateSession(get().layout, id)) focusSession(id);
  else addTabForSession(id);
}
