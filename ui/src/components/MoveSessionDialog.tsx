import { Laptop, Loader2, Server } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { resumeInPane } from "@/lib/actions";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { closeDialog, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { DirPicker, ensureDir } from "./NewSession";

/**
 * Carry a conversation on on another computer (docs/dev/MULTIHOST.md, "Moving a conversation"): pick the computer and
 * a folder there; the backend copies the log and its images into a new session there (server/hostmove.go), which
 * takes this pane's place. The original stays where it was, like switching agents.
 */
export function MoveSessionDialog() {
  const d = useStore((s) => s.dialog);
  const mv = d?.kind === "moveSession" ? d : null;
  const session = useStore((s) => (mv ? s.sessions[mv.sessionId] : undefined));
  const hosts = useStore((s) => s.hosts);
  const localHome = useStore((s) => s.home);
  const from = session?.host?.id ?? "";
  // Every other computer: this one when the session is on a host, the hosts it isn't on
  const choices = [...(from ? [{ id: "", name: t("newSession.thisComputer"), online: true }] : []), ...hosts.filter((h) => h.id !== from)];
  const [to, setTo] = useState<string | null>(null);
  const [homes, setHomes] = useState<Record<string, string>>({});
  const [cwd, setCwd] = useState("~");
  const [busy, setBusy] = useState(false);
  const target = to ?? choices[0]?.id ?? null;
  const home = target === "" ? localHome : target ? (homes[target] ?? "") : "";

  useEffect(() => {
    if (!mv) return setTo(null);
    // Start from the same place under the home there: projects tend to sit at the same spot on each machine
    const srcHome = from ? "" : localHome;
    const c = session?.cwd ?? "";
    setCwd(srcHome && c.startsWith(srcHome) ? "~" + c.slice(srcHome.length) : "~");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mv?.sessionId]);
  useEffect(() => {
    if (!target || homes[target] !== undefined) return;
    api.dirs("", target).then(
      (r) => setHomes((m) => ({ ...m, [target]: r.home })),
      (e) => toast.error(t("newSession.hostUnreachable", { error: e.message ?? String(e) })),
    );
  }, [target, homes]);

  const go = async (dir = cwd) => {
    if (!mv || !session || target === null || busy || !home) return;
    setBusy(true);
    try {
      const abs = dir.startsWith("~") ? home + dir.slice(1) : dir;
      await ensureDir(abs, target || undefined);
      const s = await api.createSession({ agent: session.agent, cwd: abs, continueFrom: session.id, host: target || undefined });
      useStore.setState((st) => ({ sessions: { ...st.sessions, [s.id]: s } }));
      closeDialog();
      resumeInPane(mv.paneId, s.id);
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={!!mv} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent className="max-w-md gap-3">
        <DialogTitle className="text-[14px]">{t("moveSession.title")}</DialogTitle>
        <DialogDescription className="text-[12px]">{t("moveSession.desc", { title: session?.title || session?.cwd || "" })}</DialogDescription>
        {choices.length === 0 ? (
          <div className="text-[12px] text-muted-foreground">{t("moveSession.noHosts")}</div>
        ) : (
          <>
            <div className="flex flex-wrap gap-1.5">
              {choices.map((h) => (
                <button
                  key={h.id || "local"}
                  onClick={() => setTo(h.id)}
                  className={cn(
                    "flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-[12px] transition-colors",
                    target === h.id ? "border-brand bg-brand/8" : "hover:bg-accent",
                  )}
                >
                  {h.id ? <Server className="size-3.5" /> : <Laptop className="size-3.5" />}
                  {h.name}
                  {h.id && <span className={cn("size-1.5 rounded-full", h.online ? "bg-ok" : "bg-muted-foreground/40")} />}
                </button>
              ))}
            </div>
            <DirPicker value={cwd} onChange={setCwd} onEnter={(v) => go(v)} host={target || undefined} hostHome={home} />
            <Button disabled={busy || !home} onClick={() => go()}>
              {busy && <Loader2 className="size-4 animate-spin" />}
              {t("moveSession.go")}
            </Button>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
