import { type Workspace } from "@/lib/layout";
import type { SessionSummary } from "@/lib/types";
import { StatusDot, dotRank } from "./AgentIcon";
import { tabStatus } from "./TabBar";

/** idle：没状态时也占着位置（透明的点；侧栏里靠它和别的行对齐）。传 hideIdle 就不画（面板一览的标签里，占位只是一块空白） */
export function WsDot({
  w,
  sessions,
  unread,
  hideIdle,
}: {
  w: Workspace;
  sessions: Record<string, SessionSummary>;
  unread: Record<string, true>;
  hideIdle?: boolean;
}) {
  let best: { status?: string; unread: boolean } = { unread: false };
  let rank = 9;
  for (const t of w.tabs) {
    const st = tabStatus(t, sessions, unread);
    const r = dotRank(st.status, st.unread);
    if (r < rank) {
      rank = r;
      best = st;
    }
  }
  if (hideIdle && rank === 9) return null;
  return <StatusDot status={best.status} unread={best.unread} />;
}
