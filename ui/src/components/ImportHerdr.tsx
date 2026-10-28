import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { addImportedWorkspaces } from "@/lib/actions";
import { api, type HerdrWorkspace } from "@/lib/api";
import { set } from "@/lib/store";

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
      toast.success(`导入了 ${r.workspaces.length} 个工作空间、${n} 个会话${r.reused ? `（${r.reused} 个本来就在 gerdr 里）` : ""}`);
      if (r.errors?.length) toast.error(`${r.errors.length} 个会话没接上：${r.errors[0]}`, { duration: 10000 });
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
          <DialogTitle>从 herdr 导入</DialogTitle>
          <DialogDescription>
            按 herdr 的工作空间、tab、分屏建出来，每个 agent 接上它的会话（历史全在，接着聊）。普通终端 pane 跳过。
          </DialogDescription>
        </DialogHeader>
        {/* 高度要写死：弹窗是网格 + 限高，这一行可滚动，WebKit（mac App）里它的自动最小高度按 0 算，整个列表被压成一条缝
            （Chrome 里正常，所以光在浏览器里验看不出来；用户报了两次「导入的 UI 烂了」）。 */}
        <div className="min-w-0 shrink-0 space-y-2 overflow-auto" style={{ height: "min(50vh, 440px)" }}>
          {err && <div className="text-[13px] text-bad">{err}</div>}
          {!list && !err && (
            <div className="flex items-center gap-2 text-[13px] text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" /> 读 herdr…
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
                    {c.ok} 个会话{c.existing ? `（${c.existing} 个已经在 gerdr 里）` : ""} · {w.tabs.length} 个 tab
                    {c.skipped ? ` · 跳过 ${c.skipped} 个（${c.why}）` : ""}
                  </div>
                </div>
              </label>
            );
          })}
        </div>
        <div className="rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">
          这些会话在 herdr 里多半还开着。接着在 gerdr 里聊之前，先在 herdr 里退出对应的 agent —— 两边同时往一个会话里写，对话会分叉。
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={close} disabled={busy}>
            取消
          </Button>
          <Button onClick={run} disabled={busy || total === 0}>
            {busy && <Loader2 className="size-3.5 animate-spin" />}
            导入 {total} 个会话
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
  for (const t of w.tabs)
    for (const p of t.panes) {
      if (p.skip) {
        skipped++;
        why.add(p.agent ? "没有会话 id" : "普通终端");
      } else {
        ok++;
        if (p.existing) existing++;
      }
    }
  return { ok, existing, skipped, why: [...why].join("、") };
}
