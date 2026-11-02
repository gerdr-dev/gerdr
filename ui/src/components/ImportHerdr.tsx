import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { addImportedWorkspaces } from "@/lib/actions";
import { api, type HerdrWorkspace } from "@/lib/api";
import { set } from "@/lib/store";
import { t } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";

/** key.one / key.other by n (English needs both; zh has the same text in each). */
const plural = (key: string, n: number, vars?: Record<string, string | number>) => t(`${key}.${n === 1 ? "one" : "other"}` as MessageKey, { n, ...vars });

// 从 herdr 导入：herdr 的工作空间 / tab / 分屏搬过来，每个 agent pane 接上它的会话（历史全在，接着聊走 resume）。
export function ImportHerdr({ open }: { open: boolean }) {
  const [list, setList] = useState<HerdrWorkspace[] | null>(null);
  const [err, setErr] = useState("");
  const [pick, setPick] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setList(null);
    setErr("");
    api
      .herdrScan()
      .then((r) => {
        setList(r.workspaces);
        setPick(new Set(r.workspaces.filter((w) => count(w).ok > 0).map((w) => w.id)));
      })
      .catch((e) => setErr(e.message));
  }, [open]);

  const close = () => !busy && set({ dialog: null });
  const run = async () => {
    setBusy(true);
    try {
      const r = await api.herdrImport([...pick]);
      addImportedWorkspaces(r.workspaces);
      const n = r.imported + r.reused;
      toast.success(
        t("importHerdr.done", {
          workspaces: plural("importHerdr.workspaces", r.workspaces.length),
          sessions: plural("importHerdr.sessions", n),
          reused: r.reused ? t("importHerdr.doneReused", { n: r.reused }) : "",
        }),
      );
      if (r.errors?.length) toast.error(plural("importHerdr.errors", r.errors.length, { error: r.errors[0] }), { duration: 10000 });
      set({ dialog: null });
    } catch (e: any) {
      toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };

  const total = list?.filter((w) => pick.has(w.id)).reduce((s, w) => s + count(w).ok, 0) ?? 0;
  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("importHerdr.title")}</DialogTitle>
          <DialogDescription>{t("importHerdr.desc")}</DialogDescription>
        </DialogHeader>
        {/* 高度要写死：弹窗是网格 + 限高，这一行可滚动，WebKit（mac App）里它的自动最小高度按 0 算，整个列表被压成一条缝
            （Chrome 里正常，所以光在浏览器里验看不出来；用户报了两次「导入的 UI 烂了」）。 */}
        <div className="min-w-0 shrink-0 space-y-2 overflow-auto" style={{ height: "min(50vh, 440px)" }}>
          {err && <div className="text-[13px] text-bad">{err}</div>}
          {!list && !err && (
            <div className="flex items-center gap-2 text-[13px] text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" /> {t("importHerdr.reading")}
            </div>
          )}
          {list?.map((w) => {
            const c = count(w);
            return (
              <label key={w.id} className="flex cursor-pointer items-start gap-2.5 rounded-md border px-3 py-2 hover:bg-accent/50">
                <Checkbox
                  className="mt-0.5"
                  disabled={c.ok === 0}
                  checked={pick.has(w.id)}
                  onCheckedChange={(v) => {
                    const next = new Set(pick);
                    v ? next.add(w.id) : next.delete(w.id);
                    setPick(next);
                  }}
                />
                <div className="min-w-0 flex-1">
                  <div className="text-[13px] font-medium">{w.label}</div>
                  <div className="text-[12px] text-muted-foreground">
                    {plural("importHerdr.sessions", c.ok)}
                    {c.existing ? t("importHerdr.existing", { n: c.existing }) : ""} · {plural("importHerdr.tabs", w.tabs.length)}
                    {c.skipped ? t("importHerdr.skipped", { n: c.skipped, why: c.why }) : ""}
                  </div>
                </div>
              </label>
            );
          })}
        </div>
        <div className="rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">{t("importHerdr.warning")}</div>
        <DialogFooter>
          <Button variant="ghost" onClick={close} disabled={busy}>
            {t("common.cancel")}
          </Button>
          <Button onClick={run} disabled={busy || total === 0}>
            {busy && <Loader2 className="size-3.5 animate-spin" />}
            {plural("importHerdr.import", total)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function count(w: HerdrWorkspace) {
  let ok = 0,
    existing = 0,
    skipped = 0;
  const why = new Set<string>();
  for (const tab of w.tabs)
    for (const p of tab.panes) {
      if (p.skip) {
        skipped++;
        why.add(p.agent ? t("importHerdr.noSessionId") : t("importHerdr.plainTerminal"));
      } else {
        ok++;
        if (p.existing) existing++;
      }
    }
  return { ok, existing, skipped, why: [...why].join(t("importHerdr.whySep")) };
}
