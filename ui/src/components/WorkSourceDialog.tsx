import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { switchSpace } from "@/lib/actions";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { panelsChanged } from "@/lib/panels";
import { set, setMode, setWorkPanel, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

/**
 * Adding a panel to a project (internal/work/source.go): installing one someone published (a git repository: its
 * pages, local functions, skills, schedules), or a blank one to build. Adding never replaces anything: the panel gets a
 * button of its own on the rail. Installing runs someone else's code on this computer (functions run commands,
 * schedules start by themselves), so the dialog says so before anything happens.
 */
/** Panels published on GitHub carry this topic (README of gerdr-panel-superagent says so too). */
const PANEL_TOPIC = "https://github.com/topics/gerdr-panel";

/** Panels we made and stand behind, offered above the topic link so a first install is one tap, not a search. */
const RECOMMENDED = [{ name: "Superagent", url: "https://github.com/gerdr-dev/gerdr-panel-superagent", desc: "workSource.recSuperagent" }] as const;

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
      panelsChanged();
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
      {/* One column that may shrink: the recommended panel's address (one line, fading) otherwise widened the grid past
          a phone's screen, and everything sat off centre with no right margin (reported) */}
      <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-lg">
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
          {install && (
            <Input
              autoFocus
              className="text-[13px]"
              placeholder="https://github.com/owner/repo"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              disabled={busy}
            />
          )}
          <Input
            autoFocus={!install}
            className="text-[13px]"
            placeholder={install ? t("workSource.nameOptional") : t("workSource.namePlaceholder")}
            value={name}
            onChange={(e) => setName(e.target.value)}
            disabled={busy}
          />
        </form>
        {/* Picking one only fills the form: installing still goes through the button, under the trust note */}
        {install && (
          <div className="space-y-1.5">
            <div className="text-[12px] text-muted-foreground">{t("workSource.recommended")}</div>
            {RECOMMENDED.map((r) => (
              <button
                key={r.url}
                type="button"
                disabled={busy}
                onClick={() => (setUrl(r.url), setName(""), setErr(""))}
                className={cn(
                  "w-full rounded-md border px-3 py-2 text-left transition-colors hover:bg-muted/60",
                  url.trim() === r.url ? "border-brand bg-brand/5" : "border-border",
                )}
              >
                <div className="text-[13px] font-medium">{r.name}</div>
                <div className="mt-0.5 text-[12px] text-muted-foreground">{t(r.desc)}</div>
                <div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground/70">{r.url.replace("https://", "")}</div>
              </button>
            ))}
          </div>
        )}
        {/* Where to find one: published panels carry the GitHub topic gerdr-panel */}
        {install && (
          <p className="text-[12px] text-muted-foreground">
            {t("workSource.findBefore")}
            <a href={PANEL_TOPIC} target="_blank" rel="noreferrer" className="text-brand underline-offset-2 hover:underline">
              github.com/topics/gerdr-panel
            </a>
            {t("workSource.findAfter")}
          </p>
        )}
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
