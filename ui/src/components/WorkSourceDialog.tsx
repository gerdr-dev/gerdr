import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { switchSpace } from "@/lib/actions";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { set, setMode, setWorkPanel, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

/**
 * Adding a panel to a project (internal/work/source.go): installing one someone published (a git repository: its
 * pages, local functions, skills, schedules), or a blank one to build. Adding never replaces anything: the panel gets a
 * button of its own on the rail. Installing runs someone else's code on this computer (functions run commands,
 * schedules start by themselves), so the dialog says so before anything happens.
 */
export function WorkSourceDialog() {
  const d = useStore((s) => s.dialog);
  const ask = d?.kind === "workSource" ? d : undefined;
  // The two ways to add one, switchable in the dialog (the rail's + opens it on "install")
  const [action, setAction] = useState<"install" | "new">("install");
  const install = action === "install";
  const [url, setUrl] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    setUrl("");
    setName("");
    setErr("");
    setBusy(false);
    if (ask) setAction(ask.action);
  }, [ask?.project, ask?.action]);
  const ready = install ? !!url.trim() : !!name.trim();
  const close = () => !busy && set({ dialog: null });
  const go = async () => {
    if (!ask || busy || !ready) return;
    setBusy(true);
    setErr("");
    try {
      const r = install ? await api.installPanel(ask.project, url.trim(), name.trim() || undefined) : await api.newPanel(ask.project, name.trim());
      set({ dialog: null });
      switchSpace(ask.project);
      setWorkPanel(ask.project, r.key.split("/")[1] ?? "");
      setMode("work");
      toast.success(install ? t("workSource.installed") : t("workSource.created"));
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
          <DialogTitle>{t("workSource.add")}</DialogTitle>
          <DialogDescription>{install ? t("workSource.installDesc") : t("workSource.newDesc")}</DialogDescription>
        </DialogHeader>
        <div className="flex gap-0.5 self-start rounded-lg bg-muted p-0.5">
          {(["install", "new"] as const).map((a) => (
            <button
              key={a}
              disabled={busy}
              className={cn("h-7 rounded-md px-3 text-[12px]", action === a ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground")}
              onClick={() => (setAction(a), setErr(""))}
            >
              {a === "install" ? t("workSource.installTitle") : t("workSource.newTitle")}
            </button>
          ))}
        </div>
        <form
          className="space-y-2"
          onSubmit={(e) => {
            e.preventDefault();
            void go();
          }}
        >
          {install && <Input autoFocus placeholder="https://github.com/owner/repo" value={url} onChange={(e) => setUrl(e.target.value)} disabled={busy} />}
          <Input
            autoFocus={!install}
            placeholder={install ? t("workSource.nameOptional") : t("workSource.namePlaceholder")}
            value={name}
            onChange={(e) => setName(e.target.value)}
            disabled={busy}
          />
        </form>
        {install && <div className="rounded-md border-l-2 border-l-warn bg-warn/5 px-3 py-2 text-[12px] text-muted-foreground">{t("workSource.trust")}</div>}
        {err && <div className="text-[12px] whitespace-pre-wrap text-bad">{err}</div>}
        <DialogFooter>
          <Button variant="outline" onClick={close} disabled={busy}>
            {t("common.cancel")}
          </Button>
          <Button onClick={() => void go()} disabled={busy || !ready}>
            {busy ? t("workSource.working") : install ? t("workSource.install") : t("workSource.create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
