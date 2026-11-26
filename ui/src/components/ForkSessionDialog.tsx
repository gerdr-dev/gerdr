import { Loader2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { focusSession, openSessionNear } from "@/lib/actions";
import { ownerOf } from "@/lib/panelRoute";
import { workProjectOf } from "@/lib/work";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { closeDialog, hostStart, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { AgentIcon } from "./AgentIcon";

/**
 * Fork a conversation: a new session carrying it so far, with the same agent or another, named "Fork: <title>"
 * (hub CreateOptions.Fork). The same engine copies its own session (hub/fork.go), unless "hand over as text" is
 * ticked; another engine always gets the history as text (hub/handoff.go). It opens beside the original, which stays
 * as it was. One click on an agent forks. Forked from a panel, it opens in Code (openSessionNear) and the view goes there.
 */
export function ForkSessionDialog() {
  const d = useStore((s) => s.dialog);
  const fk = d?.kind === "forkSession" ? d : null;
  const session = useStore((s) => (fk ? s.sessions[fk.sessionId] : undefined));
  const agents = useStore((s) => s.agents);
  const [busy, setBusy] = useState("");
  const [text, setText] = useState(false);
  if (!session) return null;
  const engine = (id: string) => agents.find((a) => a.id === id)?.base || id;
  const same = (id: string) => engine(id) === (session.base || engine(session.agent));
  // The conversation's own agent first: forking to try another direction with it is the usual case
  const list = [...agents.filter((a) => a.id === session.agent), ...agents.filter((a) => a.id !== session.agent)];

  const fork = async (agent: string) => {
    if (busy) return;
    setBusy(agent);
    try {
      // A panel hosting Code makes the fork its own (core useCodeStart: carried on from this one), as any start there
      const viaPanel = session.host ? undefined : hostStart();
      if (viaPanel) {
        const id = await viaPanel({ agent, cwd: session.cwd, continueFrom: session.id, fork: true, forkText: text && same(agent) });
        closeDialog();
        openSessionNear(id, session.id, true);
        return;
      }
      const s = await api.createSession({
        agent,
        cwd: session.cwd,
        continueFrom: session.id,
        fork: true,
        forkText: text && same(agent),
        host: session.host?.id,
      });
      useStore.setState((st) => ({ sessions: { ...st.sessions, [s.id]: s } }));
      closeDialog();
      openSessionNear(s.id, session.id, true);
      // Forked from a panel's conversation: the fork is Code's (no panel tag), so go there to show it, or it opens out of sight
      if (workProjectOf(session.id) || ownerOf(session.id)) focusSession(s.id);
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
        <DialogDescription className="text-[12px]">{t("forkSession.desc")}</DialogDescription>
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
                  {!a.available ? t("newSession.notInstalled") : same(a.id) && !text ? t("forkSession.copy") : t("forkSession.asText")}
                </div>
              </div>
            </button>
          ))}
        </div>
        {/* Only the same engine has a choice: another one can only get the conversation as text */}
        <label className="flex items-start gap-2 text-[12px] text-muted-foreground">
          <Checkbox className="mt-0.5" checked={text} onCheckedChange={(v) => setText(v === true)} />
          <span>{t("forkSession.text")}</span>
        </label>
      </DialogContent>
    </Dialog>
  );
}
