import { Loader2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { openSessionNear } from "@/lib/actions";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { closeDialog, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { AgentIcon } from "./AgentIcon";

/**
 * Fork a conversation: a new session carrying it so far, with the same agent or another, named "Fork: <title>"
 * (hub CreateOptions.Fork, the same copy-and-hand-over as switching agents in hub/handoff.go). It opens beside the
 * original, which stays as it was. One click on an agent forks: there's nothing else to choose.
 */
export function ForkSessionDialog() {
  const d = useStore((s) => s.dialog);
  const fk = d?.kind === "forkSession" ? d : null;
  const session = useStore((s) => (fk ? s.sessions[fk.sessionId] : undefined));
  const agents = useStore((s) => s.agents);
  const [busy, setBusy] = useState("");
  if (!session) return null;
  // The conversation's own agent first: forking to try another direction with it is the usual case
  const list = [...agents.filter((a) => a.id === session.agent), ...agents.filter((a) => a.id !== session.agent)];

  const fork = async (agent: string) => {
    if (busy) return;
    setBusy(agent);
    try {
      const s = await api.createSession({ agent, cwd: session.cwd, continueFrom: session.id, fork: true, host: session.host?.id });
      useStore.setState((st) => ({ sessions: { ...st.sessions, [s.id]: s } }));
      closeDialog();
      openSessionNear(s.id, session.id, true);
    } catch (e: any) {
      toast.error(t("forkSession.failed", { error: e.message ?? String(e) }));
    } finally {
      setBusy("");
    }
  };

  return (
    <Dialog open={!!fk} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent className="max-w-md gap-3">
        <DialogTitle className="text-[14px]">{t("forkSession.title")}</DialogTitle>
        <DialogDescription className="text-[12px]">{t("forkSession.desc", { title: session.title || session.cwd })}</DialogDescription>
        <div className="grid grid-cols-2 gap-2">
          {list.map((a) => (
            <button
              key={a.id}
              disabled={!a.available || !!busy}
              title={a.error}
              onClick={() => fork(a.id)}
              className={cn(
                "flex items-center gap-2.5 rounded-lg border px-3 py-2.5 text-left transition-colors",
                a.id === session.agent ? "border-brand bg-brand/8" : "hover:bg-accent",
                (!a.available || (busy && busy !== a.id)) && "cursor-not-allowed opacity-50",
              )}
            >
              {busy === a.id ? <Loader2 className="size-5 animate-spin" /> : <AgentIcon agent={a.id} className="size-5" />}
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{a.name}</div>
                <div className="truncate text-[11px] text-muted-foreground">
                  {!a.available ? t("newSession.notInstalled") : a.id === session.agent ? t("forkSession.same") : t("forkSession.other")}
                </div>
              </div>
            </button>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
