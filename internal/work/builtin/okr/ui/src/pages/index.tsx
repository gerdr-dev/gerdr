import { Check, Loader2, PenLine, RefreshCw, Settings } from "lucide-react";
import { useEffect, useState } from "react";
import clsx from "clsx";
import { Button } from "../components/Button";
import { ask, openSettings, run } from "../lib/gerdr";

// OKR 周报: data from the local function okr.summary (../../local/okr.ts, which reads the okr MCP);
// "让 AI 帮我写" hands the week's context to the Work agent on the right.

type KR = {
  id: number;
  name: string;
  desc: string;
  progress: number;
  confidence: string;
  reportedThisWeek: boolean;
  lastReport: { week: string; time: string; content: string; progress: number } | null;
};
type Objective = { id: number; name: string; desc: string; progress: number | null; confidence: string; aligned?: boolean; krs: KR[] };
type OKRSummary = {
  user: { id: number; name: string; title: string };
  quarter: { name: string; start: string; end: string };
  week: { key: string; start: string; end: string };
  submittedThisWeek: boolean;
  lastReport: { time: string; week: string; object: string } | null;
  objectives: Objective[];
};

function ago(iso: string) {
  const d = (Date.now() - new Date(iso).getTime()) / 1000;
  if (d < 3600) return `${Math.max(1, Math.round(d / 60))} 分钟前`;
  if (d < 86400) return `${Math.round(d / 3600)} 小时前`;
  return `${Math.floor(d / 86400)} 天前`;
}

const fmtTime = (iso: string) =>
  new Date(iso).toLocaleString("zh-CN", { month: "numeric", day: "numeric", weekday: "short", hour: "2-digit", minute: "2-digit" });
const md = (ymd: string) => `${Number(ymd.slice(5, 7))}/${Number(ymd.slice(8, 10))}`;
const CONF: Record<string, string> = { commit: "承诺", stretch: "挑战" };

function Bar({ value }: { value: number }) {
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
      <div className="h-full rounded-full bg-brand" style={{ width: `${Math.max(0, Math.min(100, value))}%` }} />
    </div>
  );
}

function writePrompt(d: OKRSummary) {
  const krs = d.objectives.flatMap((o) => o.krs.map((k) => ({ o, k })));
  const lines = krs.map(({ o, k }) => {
    const last = k.lastReport ? `上次（${k.lastReport.week}，${k.lastReport.progress}%）：${k.lastReport.content.replace(/\s+/g, " ")}` : "还没写过";
    return `- KR ${k.id}「${k.name}」（属于「${o.name}」，现在 ${k.progress}%${k.reportedThisWeek ? "，本周已写" : ""}）\n  ${last}`;
  });
  return [
    `帮我写本周（${d.week.key}，${md(d.week.start)}–${md(d.week.end)}）的 OKR 周报。`,
    "",
    `我 ${d.quarter.name} 的 KR 和上次写的进展：`,
    ...lines,
    "",
    "先看我这周实际做了什么（相关仓库的提交、最近的对话；拿不准就问我），给每个本周还没写的 KR 起草一段进展和建议的进度。",
    "先把草稿给我看，我确认后再用 okr MCP 的 create_progress_report 提交。",
  ].join("\n");
}

export default function OKRPage() {
  const [d, setD] = useState<OKRSummary | null>(null);
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(false);
  const [asking, setAsking] = useState(false);
  const load = () => {
    setLoading(true);
    setErr("");
    run<OKRSummary>("okr.summary")
      .then(setD, (e) => setErr(e.message ?? String(e)))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);
  const askAI = async () => {
    if (!d) return;
    setAsking(true);
    try {
      ask(writePrompt(d));
    } finally {
      setAsking(false);
    }
  };

  if (!d) {
    return (
      <div className="flex h-full items-center justify-center p-6 text-center text-[13px] text-muted-foreground">
        {err ? (
          <div className="max-w-md space-y-3">
            <div className="text-bad">读 OKR 失败：{err}</div>
            {/401|sign|登录|authoriz/i.test(err) && <div>okr 这个 MCP 还没登录：去 设置 → MCP 授权一下。</div>}
            <div className="flex justify-center gap-2">
              <Button size="sm" variant="outline" onClick={load}>
                <RefreshCw className="size-3.5" /> 重试
              </Button>
              <Button size="sm" variant="ghost" onClick={() => openSettings("mcp")}>
                <Settings className="size-3.5" /> MCP 设置
              </Button>
            </div>
          </div>
        ) : (
          <span className="flex items-center gap-2">
            <Loader2 className="size-4 animate-spin" /> 正在读 OKR…
          </span>
        )}
      </div>
    );
  }

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-3xl space-y-5 p-5 max-md:p-3">
        <div className="flex items-start gap-3">
          <div className="min-w-0 flex-1">
            <div className="text-[18px] font-semibold">OKR · {d.quarter.name}</div>
            <div className="text-[12px] text-muted-foreground">
              {d.user.name}
              {d.user.title ? ` · ${d.user.title}` : ""} · {md(d.quarter.start)}–{md(d.quarter.end)}
            </div>
          </div>
          <Button size="icon" variant="ghost" className="size-8" title="刷新" disabled={loading} onClick={load}>
            <RefreshCw className={clsx("size-4", loading && "animate-spin")} />
          </Button>
        </div>
        {err && <div className="text-[12px] text-bad">刷新失败：{err}</div>}

        <div className={clsx("flex items-center gap-4 rounded-xl border p-4", d.submittedThisWeek ? "border-ok/40 bg-ok/5" : "border-warn/50 bg-warn/5")}>
          <div className="min-w-0 flex-1 space-y-1">
            <div className="flex items-center gap-2 text-[15px] font-medium">
              {d.submittedThisWeek ? <Check className="size-4 text-ok" /> : <PenLine className="size-4 text-warn" />}
              本周（{md(d.week.start)}–{md(d.week.end)}）{d.submittedThisWeek ? "周报已提交" : "周报还没写"}
            </div>
            <div className="text-[12px] text-muted-foreground">
              {d.lastReport ? (
                <>
                  上次提交：<span className="text-foreground">{ago(d.lastReport.time)}</span>（{fmtTime(d.lastReport.time)}，{d.lastReport.week}）
                </>
              ) : (
                "还没提交过周报"
              )}
            </div>
          </div>
          <Button size="sm" variant={d.submittedThisWeek ? "outline" : "default"} disabled={asking} onClick={askAI}>
            {asking ? <Loader2 className="size-3.5 animate-spin" /> : <PenLine className="size-3.5" />}
            {d.submittedThisWeek ? "让 AI 补写" : "让 AI 帮我写"}
          </Button>
        </div>

        {d.objectives.map((o) => (
          <div key={o.id} className="rounded-xl border bg-card">
            <div className="space-y-2 border-b p-4">
              <div className="flex items-start gap-2">
                <span className="mt-0.5 shrink-0 rounded-sm bg-brand/10 px-1.5 text-[11px] font-medium text-brand">{o.aligned ? "对齐" : "O"}</span>
                <div className="min-w-0 flex-1 text-[14px] font-medium">{o.name}</div>
                {o.confidence && <span className="shrink-0 text-[11px] text-muted-foreground">{CONF[o.confidence] ?? o.confidence}</span>}
                {o.progress != null && <span className="w-10 shrink-0 text-right text-[12px] tabular-nums">{o.progress}%</span>}
              </div>
              {o.progress != null && <Bar value={o.progress} />}
            </div>
            <div className="divide-y">
              {o.krs.length === 0 && <div className="p-4 text-[12px] text-muted-foreground">没有 KR</div>}
              {o.krs.map((k) => (
                <div key={k.id} className="space-y-1.5 p-4">
                  <div className="flex items-start gap-2">
                    <span className="mt-0.5 shrink-0 rounded-sm bg-muted px-1.5 text-[11px] text-muted-foreground">KR</span>
                    <div className="min-w-0 flex-1 text-[13px]">{k.name}</div>
                    {k.reportedThisWeek ? (
                      <span className="shrink-0 text-[11px] text-ok">本周已写</span>
                    ) : (
                      <span className="shrink-0 text-[11px] text-warn">本周未写</span>
                    )}
                    <span className="w-10 shrink-0 text-right text-[12px] tabular-nums">{k.progress}%</span>
                  </div>
                  <Bar value={k.progress} />
                  {k.lastReport && (
                    <div className="text-[12px] text-muted-foreground">
                      <span className="tabular-nums">{k.lastReport.week}</span>：{k.lastReport.content}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
