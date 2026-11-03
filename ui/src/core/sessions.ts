import { focusSession } from "@/lib/actions";
import { toggleOverview } from "@/lib/overview";
import { setMode, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";

/** A conversation's summary (title, status, agent, cwd…), kept current; undefined while unknown. */
export function useSession(id: string | undefined): SessionSummary | undefined {
  return useStore((s) => (id ? s.sessions[id] : undefined));
}

/** Go to the conversation in Code (its pane, or a new tab for it). */
export function openInCode(id: string) {
  toggleOverview(false);
  setMode("agents");
  focusSession(id);
}
