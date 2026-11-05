import { Ask, Permission } from "@/components/Inbox";
import { useInbox } from "@/lib/inbox";

/**
 * What a conversation is waiting on someone for right now (approvals, questions), oldest first: this computer's and
 * the other computers' ("<host>:<id>"), from the same list as the inbox. `inline`: simple enough to settle with
 * PendingActions (an approval that isn't a plan, a single question); the rest need the conversation.
 */
export function usePending(id: string | undefined) {
  const reqs = useInbox((s) => s.reqs);
  if (!id) return [];
  return reqs
    .filter((r) => r.sessionId === id)
    .map((r) => ({
      reqId: r.reqId,
      kind: r.kind,
      summary: r.summary,
      at: r.at,
      inline: (r.kind === "permission" && r.request?.toolCall?.kind !== "switch_mode") || (r.kind === "ask" && r.request?.questions?.length === 1),
    }));
}

/**
 * The buttons that settle a conversation's oldest waiting request in place, the inbox's own: an approval's options
 * (allow once / always / deny…), or a single question's choices. Nothing when it isn't waiting, or waits on something
 * that needs its conversation (a plan, several questions): check usePending(id)[0]?.inline to offer that instead.
 */
export function PendingActions({ id, className }: { id: string; className?: string }) {
  const r = useInbox((s) => s.reqs.find((x) => x.sessionId === id));
  if (!r) return null;
  const it = { id: `req:${r.reqId}`, kind: r.kind, at: r.at, sessionId: r.sessionId, title: "", reqId: r.reqId, request: r.request };
  if (r.kind === "permission" && r.request?.toolCall?.kind !== "switch_mode")
    return (
      <div className={className}>
        <Permission key={r.reqId} it={it} />
      </div>
    );
  if (r.kind === "ask" && r.request?.questions?.length === 1)
    return (
      <div className={className}>
        <Ask key={r.reqId} it={it} q={r.request.questions[0]} />
      </div>
    );
  return null;
}
