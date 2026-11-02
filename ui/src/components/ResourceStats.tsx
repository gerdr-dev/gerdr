import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { focusSession } from "@/lib/actions";
import { nativeWebPid } from "@/lib/native";
import { prefOf, set, useStore } from "@/lib/store";
import { t } from "@/lib/i18n";

// 设置 →「资源」：内存都花在哪了。每 2 秒刷新一次（只在这一页开着时）。
// 后端和 agent 进程用 ps 量（和活动监视器对得上）；界面这边：mac App 里量 WebView 进程，浏览器里只有 JS 堆（Chromium 才有）和 DOM 节点数。

type Proc = { pid: number; label: string; session?: string; rss: number; total: number; procs: number };
type Stats = { server?: Proc; web?: Proc; agents?: Proc[]; goHeap: number; goroutines: number };

const mb = (kb: number) => (kb >= 1024 * 1024 ? `${(kb / 1024 / 1024).toFixed(2)} GB` : `${Math.round(kb / 1024)} MB`);

export function ResourceStats({ open }: { open: boolean }) {
  const [s, setS] = useState<Stats | null>(null);
  const [err, setErr] = useState("");
  const [page, setPage] = useState({ heap: 0, nodes: 0 });
  const kept = useStore((st) => st.keptTabs.length);
  const keep = useStore((st) => prefOf(st, "keepTabs") ?? 6);
  useEffect(() => {
    if (!open) return;
    let stop = false;
    const tick = async () => {
      try {
        const pid = await nativeWebPid();
        const r = await api.stats(pid);
        if (!stop) setS(r);
        setErr("");
      } catch (e: any) {
        setErr(e.message);
      }
      const mem = (performance as any).memory;
      setPage({ heap: mem ? mem.usedJSHeapSize / 1024 : 0, nodes: document.getElementsByTagName("*").length });
    };
    tick();
    const t = setInterval(tick, 2000);
    return () => {
      stop = true;
      clearInterval(t);
    };
  }, [open]);

  const agentsTotal = (s?.agents ?? []).reduce((a, p) => a + p.total, 0);
  const total = (s?.server?.rss ?? 0) + agentsTotal + (s?.web?.rss ?? 0);
  const Row = ({ label, value, sub, onClick }: { label: string; value: string; sub?: string; onClick?: () => void }) => (
    <div className="flex items-baseline gap-3 px-3 py-1.5">
      <div className="min-w-0 flex-1">
        {onClick ? (
          <button className="max-w-full truncate text-left text-[13px] hover:underline" onClick={onClick}>
            {label}
          </button>
        ) : (
          <div className="truncate text-[13px]">{label}</div>
        )}
        {sub && <div className="truncate text-[11px] text-muted-foreground">{sub}</div>}
      </div>
      <div className="shrink-0 font-mono text-[12px] tabular-nums">{value}</div>
    </div>
  );
  return (
    <div className="min-w-0 space-y-4">
      {err && <div className="text-[13px] text-bad">{err}</div>}
      <div className="text-[13px]">
        {t("resourceStats.total")} <span className="font-mono tabular-nums">{s ? mb(total) : "…"}</span>
        <span className="ml-2 text-[12px] text-muted-foreground">{t("resourceStats.refreshNote")}</span>
      </div>

      <div className="space-y-1.5">
        <div className="text-[13px] font-medium">{t("resourceStats.ui")}</div>
        <div className="divide-y rounded-md border">
          {s?.web ? (
            <Row label={t("resourceStats.webview")} value={mb(s.web.rss)} sub={`pid ${s.web.pid}`} />
          ) : (
            <Row label={t("resourceStats.webview")} value="—" sub={t("resourceStats.webviewOnlyMac")} />
          )}
          {page.heap > 0 && <Row label={t("resourceStats.jsHeap")} value={mb(page.heap)} />}
          <Row label={t("resourceStats.domNodes")} value={page.nodes.toLocaleString()} />
          <Row label={t("resourceStats.keptTabs")} value={`${kept} / ${keep}`} sub={t("resourceStats.keptTabsHint")} />
        </div>
      </div>

      <div className="space-y-1.5">
        <div className="text-[13px] font-medium">{t("resourceStats.backend")}</div>
        <div className="divide-y rounded-md border">
          {s?.server && (
            <Row label="gerdr" value={mb(s.server.rss)} sub={t("resourceStats.serverSub", { pid: s.server.pid, heap: mb(s.goHeap), n: s.goroutines })} />
          )}
        </div>
      </div>

      <div className="space-y-1.5">
        <div className="text-[13px] font-medium">
          {t("resourceStats.agents")}{" "}
          {s?.agents?.length ? <span className="font-normal text-muted-foreground">{t("resourceStats.agentsTotal", { size: mb(agentsTotal) })}</span> : null}
        </div>
        <div className="divide-y rounded-md border">
          {!s?.agents?.length && <Row label={t("resourceStats.noAgents")} value="" sub={t("resourceStats.noAgentsHint")} />}
          {s?.agents?.map((p) => (
            <Row
              key={p.pid}
              label={p.label}
              value={mb(p.total)}
              sub={`pid ${p.pid}${p.procs > 1 ? t(p.procs === 2 ? "resourceStats.children.one" : "resourceStats.children.other", { n: p.procs - 1, size: mb(p.rss) }) : ""}`}
              onClick={p.session ? () => (set({ dialog: null }), focusSession(p.session!)) : undefined}
            />
          ))}
        </div>
      </div>
    </div>
  );
}
