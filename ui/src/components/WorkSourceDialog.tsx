import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { switchSpace } from "@/lib/actions";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { set, setMode, useStore } from "@/lib/store";

/**
 * Replacing a project's Work (internal/work/source.go): installing a Work someone published (a git repository: its
 * pages, local functions, skills, schedules), or going back to the blank one. Both move the current Work to the trash.
 * Installing runs someone else's code on this computer (functions run commands, schedules start by themselves), so
 * the dialog says so before anything happens.
 */
export function WorkSourceDialog() {
  const d = useStore((s) => s.dialog);
  const ask = d?.kind === "workSource" ? d : undefined;
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    setUrl("");
    setErr("");
    setBusy(false);
  }, [ask?.project, ask?.action]);
  const close = () => !busy && set({ dialog: null });
  const go = async () => {
    if (!ask || busy || (ask.action === "install" && !url.trim())) return;
    setBusy(true);
    setErr("");
    try {
      const r = ask.action === "install" ? await api.installWork(ask.project, url.trim()) : await api.resetWork(ask.project);
      set({ dialog: null });
      switchSpace(ask.project);
      setMode("work");
      toast.success(ask.action === "install" ? t("workSource.installed") : t("workSource.wasReset"), {
        description: r.trash ? t("workSource.oldIn", { path: r.trash }) : undefined,
      });
    } catch (e: any) {
      setErr(e.message ?? String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={!!ask} onOpenChange={(o) => !o && close()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{ask?.action === "install" ? t("workSource.installTitle") : t("workSource.resetTitle")}</DialogTitle>
          <DialogDescription>{ask?.action === "install" ? t("workSource.installDesc") : t("workSource.resetDesc")}</DialogDescription>
        </DialogHeader>
        {ask?.action === "install" && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void go();
            }}
          >
            <Input autoFocus placeholder="https://github.com/owner/repo" value={url} onChange={(e) => setUrl(e.target.value)} disabled={busy} />
          </form>
        )}
        <div className="rounded-md border-l-2 border-l-warn bg-warn/5 px-3 py-2 text-[12px] text-muted-foreground">
          {ask?.action === "install" && <p className="mb-1">{t("workSource.trust")}</p>}
          <p>{t("workSource.moved")}</p>
        </div>
        {err && <div className="text-[12px] whitespace-pre-wrap text-bad">{err}</div>}
        <DialogFooter>
          <Button variant="outline" onClick={close} disabled={busy}>
            {t("common.cancel")}
          </Button>
          <Button onClick={() => void go()} disabled={busy || (ask?.action === "install" && !url.trim())}>
            {busy ? t("workSource.working") : ask?.action === "install" ? t("workSource.install") : t("workSource.reset")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
