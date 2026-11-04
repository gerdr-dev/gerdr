import { useEffect, useMemo, useState } from "react";
import { ArrowLeft, Bot } from "lucide-react";
import type { GPart } from "@gerdr/chat";
import { ChatView } from "@/components/chat/ChatView";
import { cardOf } from "@/components/chat/ToolCard";
import { PixelLoader } from "@/components/chat/PixelLoader";
import { TabActive } from "@/components/Panes";
import { TooltipProvider } from "@/components/ui/tooltip";
import { set, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { syncWatch } from "@/lib/sync";
import { holdSendNow } from "@/lib/chatHost";
import { t } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { takeSubagents } from "@/lib/subagents";
import { StatusDot } from "@/components/AgentIcon";

/** Streams a conversation while a mounted page shows it (store mountWatch), like a pane on screen. */
function useWatched(id: string) {
  useEffect(() => {
    set((s) => ({ mountWatch: { ...s.mountWatch, [id]: (s.mountWatch[id] ?? 0) + 1 } }));
    syncWatch();
    return () => {
      set((s) => {
        const n = (s.mountWatch[id] ?? 1) - 1;
        const next = { ...s.mountWatch };
        if (n > 0) next[id] = n;
        else delete next[id];
        return { mountWatch: next };
      });
      syncWatch();
    };
  }, [id]);
}

/**
 * One conversation, the whole chat with its input box (images, files, permissions, questions, cards), for a page to
 * put wherever it wants: the same component as a pane in Code, fed by the same connection. It fills its parent's
 * height. jump: scroll to the card with that ref (a tool result's gerdrCard.ref) and flash it; bump seq to jump again.
 * sendNow: every message sent from here goes in right away while the agent runs (the composer's "send now"), instead
 * of waiting for its current step to end.
 * sub: show one of its subagents' conversation instead (the toolCallId of the call that started it, as in
 * ctx.sessions' tasks), read-only, with a way back to the main one. "查看对话" on a subagent's card opens it here too.
 */
export function SessionChat({
  id,
  focused = false,
  jump,
  className,
  sendNow = false,
  sub,
}: {
  id: string;
  focused?: boolean;
  jump?: { ref: string; seq: number };
  className?: string;
  sendNow?: boolean;
  sub?: string;
}) {
  useWatched(id);
  // The subagent shown instead of the main conversation: the page's choice (sub), or "查看对话" on a card here
  const [shownSub, setShownSub] = useState<{ id: string; title?: string } | null>(sub ? { id: sub } : null);
  useEffect(() => setShownSub(sub ? { id: sub } : null), [sub]);
  useEffect(() => takeSubagents(id, (tc, title) => setShownSub({ id: tc, title })), [id]);
  useEffect(() => (sendNow ? holdSendNow(id) : undefined), [id, sendNow]);
  const session = useStore((s) => s.sessions[id]);
  const ready = useStore((s) => s.ready);
  const focus = useMemo(() => (jump ? { match: (p: GPart) => cardOf(p)?.ref === jump.ref, seq: jump.seq } : undefined), [jump?.ref, jump?.seq]);
  // A mounted page is a React root of its own: the contexts the chat reads in Code (tooltips, "is this tab showing")
  // aren't above it, so they're given here. Its text size is gerdr's setting (--gerdr-font, App.tsx), not whatever the
  // page's own stylesheet gives its area (a panel's `body { font-size: 14px }` lands on it): it's gerdr's chat.
  return (
    <TooltipProvider delayDuration={400}>
      <TabActive.Provider value>
        <div className={cn("h-full min-h-0", className)} style={{ fontSize: "var(--gerdr-font, 14px)" }}>
          {session && shownSub ? (
            <SubagentChat session={session} sub={shownSub} onBack={() => setShownSub(null)} />
          ) : session ? (
            <ChatView key={id} session={session} paneId={`mount:${id}`} focused={focused} focus={focus} />
          ) : (
            <div className="flex h-full items-center justify-center text-[12px] text-muted-foreground">
              {ready ? t("workView.peekGone") : <PixelLoader className="size-4" />}
            </div>
          )}
        </div>
      </TabActive.Provider>
    </TooltipProvider>
  );
}

/** A subagent's conversation in a mounted chat: read-only (as in Code's sub pane), a line on top to go back. */
function SubagentChat({ session, sub, onBack }: { session: SessionSummary; sub: { id: string; title?: string }; onBack: () => void }) {
  const task = session.tasks?.find((x) => x.toolCallId === sub.id);
  const title = task?.description || sub.title || t("panes.subAgent");
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-1.5 text-[12px]">
        <button
          className="flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
          onClick={onBack}
        >
          <ArrowLeft className="size-3.5" />
          {t("sessionChat.back")}
        </button>
        <Bot className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-medium">{title}</span>
        {task?.status === "running" && <StatusDot status="running" />}
      </div>
      <div className="min-h-0 flex-1">
        <ChatView key={`sub:${sub.id}`} session={session} paneId={`mount:${session.id}:${sub.id}`} focused={false} sub={sub.id} />
      </div>
    </div>
  );
}
