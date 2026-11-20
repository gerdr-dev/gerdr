import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { focusPane } from "@/lib/actions";
import { api, type DesktopSession } from "@/lib/api";
import { currentSpace, locateSession } from "@/lib/layout";
import { get, set } from "@/lib/store";
import { t } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";

const plural = (key: string, n: number, vars?: Record<string, string | number>) => t(`${key}.${n === 1 ? "one" : "other"}` as MessageKey, { n, ...vars });

// Import from Claude Desktop: the Desktop App's Code-tab conversations are plain Claude Code sessions, so each is taken
// over with a resume (server/importapi.go). The server puts them in the current project's workspaces (it owns the
// placement so `gerdr session import claude-desktop` places them the same way); this only picks which.
export function ImportClaudeDesktop({ open }: { open: boolean }) {
  const [list, setList] = useState<DesktopSession[] | null>(null);
  const [err, setErr] = useState("");
  const [pick, setPick] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setList(null);
    setErr("");
    api
      .desktopScan()
      .then((r) => {
        setList(r.sessions);
        // Archived ones are what the user put away in the Desktop App: offered, not picked
        setPick(new Set(r.sessions.filter((s) => !s.skip && !s.existing && !s.archived).map((s) => s.id)));
      })
      .catch((e) => setErr(e.message));
  }, [open]);

  const close = () => !busy && set({ dialog: null });
  const run = async () => {
    setBusy(true);
    try {
      const r = await api.desktopImport([...pick], currentSpace(get().layout));
      const ok = r.results.filter((x) => x.session && !x.reused);
      const bad = r.results.filter((x) => x.error);
      if (ok.length) toast.success(plural("importDesktop.done", ok.length));
      if (bad.length) toast.error(plural("importDesktop.errors", bad.length, { error: `${bad[0].title || bad[0].id}: ${bad[0].error}` }), { duration: 10000 });
      if (r.placeError) toast.error(t("importDesktop.placeError", { error: r.placeError }), { duration: 10000 });
      set({ dialog: null });
      // The new tabs come in with the server's layout broadcast, a moment after the reply: go to the newest one then
      const first = ok[0]?.session;
      if (first) {
        const end = Date.now() + 3000;
        const go = () => {
          const loc = locateSession(get().layout, first);
          if (loc) focusPane(loc.pane.id);
          else if (Date.now() < end) setTimeout(go, 100);
        };
        go();
      }
    } catch (e: any) {
      toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };

  const toggle = (id: string, on: boolean) => {
    const next = new Set(pick);
    on ? next.add(id) : next.delete(id);
    setPick(next);
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("importDesktop.title")}</DialogTitle>
          <DialogDescription>{t("importDesktop.desc")}</DialogDescription>
        </DialogHeader>
        {/* Fixed height, as in ImportHerdr: in WebKit (the mac App) a scrolling row of the dialog's grid collapses to a sliver */}
        <div className="min-w-0 shrink-0 space-y-1.5 overflow-auto" style={{ height: "min(50vh, 440px)" }}>
          {err && <div className="text-[13px] text-bad">{err}</div>}
          {!list && !err && (
            <div className="flex items-center gap-2 text-[13px] text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" /> {t("importDesktop.reading")}
            </div>
          )}
          {list?.length === 0 && <div className="text-[13px] text-muted-foreground">{t("importDesktop.none")}</div>}
          {list?.map((s) => {
            const can = !s.skip && !s.existing;
            return (
              <label key={s.id} className={`flex items-start gap-2.5 rounded-md border px-3 py-2 ${can ? "cursor-pointer hover:bg-accent/50" : "opacity-60"}`}>
                <Checkbox className="mt-0.5" disabled={!can} checked={pick.has(s.id)} onCheckedChange={(v) => toggle(s.id, !!v)} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5">
                    <span className="min-w-0 truncate text-[13px] font-medium">{s.title || s.id.slice(0, 8)}</span>
                    {s.existing && <Tag>{t("importDesktop.existing")}</Tag>}
                    {s.archived && <Tag>{t("importDesktop.archived")}</Tag>}
                    <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{ago(Date.parse(s.updatedAt))}</span>
                  </div>
                  <div className="truncate text-[12px] text-muted-foreground" title={s.cwd}>
                    {s.skip ?? (s.scratch ? t("importDesktop.scratch") : s.cwd)}
                  </div>
                </div>
              </label>
            );
          })}
        </div>
        <div className="rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">{t("importDesktop.where")}</div>
        <DialogFooter>
          <Button variant="ghost" onClick={close} disabled={busy}>
            {t("common.cancel")}
          </Button>
          <Button onClick={run} disabled={busy || pick.size === 0}>
            {busy && <Loader2 className="size-3.5 animate-spin" />}
            {plural("importDesktop.import", pick.size)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Tag({ children }: { children: React.ReactNode }) {
  return <span className="shrink-0 rounded-sm bg-muted px-1.5 text-[11px] text-muted-foreground">{children}</span>;
}

function ago(at: number) {
  if (!at) return "";
  const s = Math.max(0, (Date.now() - at) / 1000);
  if (s < 60) return t("newSession.ago.now");
  if (s < 3600) return t("newSession.ago.min", { n: Math.floor(s / 60) });
  if (s < 86400) return t("newSession.ago.hour", { n: Math.floor(s / 3600) });
  return t("newSession.ago.day", { n: Math.floor(s / 86400) });
}
