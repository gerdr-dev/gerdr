import { Check, CheckCircle2, Circle, CircleDot, CornerDownRight, HelpCircle, ShieldAlert, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Markdown } from "./Markdown";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { api } from "@/lib/api";
import type { GData, PlanEntry } from "@/lib/transcript";
import { cn } from "@/lib/utils";
import { useKept } from "@/lib/kept";

export function PlanCard({ entries }: { entries: PlanEntry[] }) {
  if (!entries.length) return null;
  const done = entries.filter((e) => e.status === "completed").length;
  return (
    <div className="my-1 rounded-lg border bg-card px-3 py-2">
      <div className="mb-1 flex items-center justify-between text-[11px] text-muted-foreground">
        <span>计划</span>
        <span>
          {done}/{entries.length}
        </span>
      </div>
      <ul className="space-y-0.5 text-[13px]">
        {entries.map((e, i) => (
          <li key={i} className="flex items-start gap-2">
            {e.status === "completed" ? (
              <CheckCircle2 className="mt-0.5 size-3.5 shrink-0 text-ok" />
            ) : e.status === "in_progress" ? (
              <CircleDot className="mt-0.5 size-3.5 shrink-0 text-brand" />
            ) : (
              <Circle className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
            )}
            <span className={cn(e.status === "completed" && "text-muted-foreground line-through", e.status === "in_progress" && "font-medium")}>
              {e.content}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function detail(raw: any): string | undefined {
  if (!raw || typeof raw !== "object") return;
  if (typeof raw.command === "string") return raw.command;
  if (typeof raw.file_path === "string") return raw.file_path;
  if (typeof raw.url === "string") return raw.url;
  if (typeof raw.reason === "string") return raw.reason;
  return;
}

export function PermissionCard({ sessionId, data }: { sessionId: string; data: GData["permission"] }) {
  const [busy, setBusy] = useState<string>();
  const { request, answer, cancelled } = data;
  const tc = request.toolCall;
  const plan: string | undefined = tc.kind === "switch_mode" ? tc.rawInput?.plan : undefined;
  const d = tc._meta?.gerdr?.detail ?? (plan ? undefined : detail(tc.rawInput));
  const settled = answer || cancelled;
  if (settled) {
    const opt = request.options.find((o) => o.optionId === answer);
    const ok = opt && opt.kind.startsWith("allow");
    return (
      // 和工具调用那一行对齐（ToolCall 的 trigger：-ml-1.5 px-1.5、图标 3.5、gap-2）
      <div className="-ml-1.5 flex items-center gap-2 px-1.5 py-[3px] text-[13px] text-muted-foreground">
        {ok ? <Check className="size-3.5 shrink-0 text-ok" /> : <X className="size-3.5 shrink-0 text-bad" />}
        {cancelled && !answer ? "审批已作废" : (opt?.name ?? answer)}
      </div>
    );
  }
  const choose = async (optionId: string) => {
    setBusy(optionId);
    try {
      await api.answer(sessionId, data.reqId, { optionId });
    } catch (e: any) {
      toast.error(e.message);
      setBusy(undefined);
    }
  };
  return (
    <div className="my-1.5 rounded-lg border border-warn/60 bg-warn/8 p-3" data-attention>
      <div className="flex items-center gap-2 text-[13px] font-medium">
        <ShieldAlert className="size-4 text-warn" />
        <span className="min-w-0 truncate">{tc.title ?? "要你批准"}</span>
      </div>
      {plan && (
        <div className="gerdr-md mt-2 max-h-[50vh] overflow-auto rounded-md border bg-card px-3 py-2 text-[13px]">
          <Markdown>{plan}</Markdown>
        </div>
      )}
      {d && d !== tc.title && (
        <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/60 px-2 py-1 font-mono text-[12px]">{d}</pre>
      )}
      <div className="mt-2.5 flex flex-wrap gap-2">
        {request.options.map((o, i) => (
          <Button
            key={o.optionId}
            size="sm"
            variant={o.kind.startsWith("allow") ? (i === 0 ? "default" : "secondary") : "outline"}
            disabled={!!busy}
            onClick={() => choose(o.optionId)}
          >
            {o.name}
          </Button>
        ))}
      </div>
    </div>
  );
}

/**
 * agent 问人的问题（AskUserQuestion）。版式照 beUI 的 Questionnaire：一次一题（多题时「问题 1 / 3」翻页）、
 * 选项是不带框的平铺行（粗体标题 + 灰色说明，右边编号，选中变 ✓）、右下角「跳过 / 继续 ↵」。
 * 选到一半的状态用 useKept：对话是虚拟列表，滚出屏幕会卸载，滚回来要还在。
 */
export function AskCard({ sessionId, data }: { sessionId: string; data: GData["ask"] }) {
  const qs = data.request.questions;
  const k = `${sessionId}:ask:${data.reqId}`;
  const [sel, setSel] = useKept<Record<string, string[]>>(`${k}:sel`, {});
  const [other, setOther] = useKept<Record<string, string>>(`${k}:other`, {});
  const [page, setPage] = useKept(`${k}:page`, 0);
  const [busy, setBusy] = useState(false);
  if (data.answers || data.cancelled) {
    return (
      <div className="my-1 space-y-0.5 px-1.5 text-[12px] text-muted-foreground">
        {data.cancelled && !data.answers ? (
          <div className="flex items-center gap-1.5">
            {data.reason === "replied" ? (
              <>
                <CornerDownRight className="size-3.5" /> 没选，在对话里直接回了
              </>
            ) : (
              <>
                <X className="size-3.5" /> {data.reason === "skipped" ? "跳过了（已告诉 agent 不用再问）" : "没回答（这一轮被打断了）"}
              </>
            )}
          </div>
        ) : (
          qs.map((q) => (
            <div key={q.id} className="flex items-start gap-1.5">
              <Check className="mt-0.5 size-3.5 shrink-0 text-ok" />
              <span>
                {q.question} → <span className="text-foreground">{(data.answers?.[q.id] ?? []).join(", ") || "（空）"}</span>
              </span>
            </div>
          ))
        )}
      </div>
    );
  }
  const submit = async (override?: Record<string, string[]>) => {
    const answers: Record<string, string[]> = {};
    for (const q of qs) {
      const v = [...(override?.[q.id] ?? sel[q.id] ?? [])];
      const o = other[q.id]?.trim();
      if (o) {
        if (q.multiSelect) v.push(o);
        else v.splice(0, v.length, o);
      }
      answers[q.id] = v;
    }
    setBusy(true);
    try {
      await api.answer(sessionId, data.reqId, { answers });
    } catch (e: any) {
      toast.error(e.message);
      setBusy(false);
    }
  };
  const i = Math.min(page, qs.length - 1);
  const q = qs[i];
  const last = i === qs.length - 1;
  const picked = sel[q.id] ?? [];
  // 单选、又不能自己填：点一下就是答案，直接进下一题（最后一题就提交），不用再点「继续」
  const instant = !q.multiSelect && !q.allowOther;
  const next = (s2 = sel) => (last ? submit(s2) : setPage(i + 1));
  const pick = (label: string) => {
    if (q.multiSelect) {
      setSel({ ...sel, [q.id]: picked.includes(label) ? picked.filter((x) => x !== label) : [...picked, label] });
      return;
    }
    const s2 = { ...sel, [q.id]: [label] };
    setSel(s2);
    if (instant) next(s2);
  };
  return (
    <div className="my-1.5 rounded-xl border bg-card p-4 shadow-sm" data-attention>
      <div className="mb-1 flex items-center gap-1.5 text-[12px] text-muted-foreground">
        <HelpCircle className="size-3.5 text-brand" />
        {qs.length > 1 ? `问题 ${i + 1} / ${qs.length}` : "需要你回答"}
        {q.header && <span>· {q.header}</span>}
      </div>
      <div className="mb-2 text-[15px] font-medium leading-snug">{q.question}</div>
      {/* -mx-2.5 抵掉行的 px-2.5：选项文字和问题左边对齐，选中 / 悬停的底色往外多出一点 */}
      <div className="-mx-2.5 flex flex-col gap-0.5">
        {(q.options ?? []).map((o, n) => {
          const on = picked.includes(o.label);
          return (
            <button
              key={o.label}
              disabled={busy}
              className={cn(
                "flex w-full items-center gap-3 rounded-lg px-2.5 py-2 text-left text-[13px] transition-colors hover:bg-muted/60",
                on && "bg-muted hover:bg-muted",
              )}
              onClick={() => pick(o.label)}
            >
              {q.multiSelect && <Checkbox checked={on} className="pointer-events-none" />}
              <span className="min-w-0 flex-1">
                <span className="font-medium">{o.label}</span>
                {o.description && <span className="ml-1.5 text-muted-foreground">{o.description}</span>}
              </span>
              {!q.multiSelect && (
                <span
                  className={cn(
                    "flex size-5 shrink-0 items-center justify-center rounded-md border text-[11px] text-muted-foreground tabular-nums",
                    on && "border-transparent bg-foreground/10 text-foreground",
                  )}
                >
                  {on ? <Check className="size-3" /> : n + 1}
                </span>
              )}
            </button>
          );
        })}
        {q.allowOther && (
          <input
            className="mt-0.5 w-full rounded-lg border border-dashed bg-transparent px-2.5 py-2 text-[13px] outline-none placeholder:text-muted-foreground/70 focus:border-solid focus:border-ring/60"
            type={q.secret ? "password" : "text"}
            placeholder={q.options?.length ? "其他，自己写…" : "你的回答…"}
            value={other[q.id] ?? ""}
            disabled={busy}
            onChange={(e) => setOther({ ...other, [q.id]: e.target.value })}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.nativeEvent.isComposing && e.keyCode !== 229) next();
            }}
          />
        )}
      </div>
      <div className="mt-3 flex items-center gap-2">
        {i > 0 && (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => setPage(i - 1)}>
            上一题
          </Button>
        )}
        <span className="min-w-0 flex-1 truncate text-[12px] text-muted-foreground max-md:hidden">也可以不选，直接在下面输入框里回复</span>
        <span className="flex-1 md:hidden" />
        <Button
          size="sm"
          variant="ghost"
          disabled={busy}
          title="告诉 agent 你不想选、别再问这个问题"
          onClick={() => api.answer(sessionId, data.reqId, { cancelled: true, reason: "skipped" }).catch(() => {})}
        >
          跳过
        </Button>
        {!instant && (
          <Button size="sm" className="rounded-full pr-2 pl-4" disabled={busy} onClick={() => next()}>
            {last ? "提交" : "继续"}
            <span className="rounded border border-current/30 px-1 text-[10px] opacity-70">↵</span>
          </Button>
        )}
      </div>
    </div>
  );
}

export function QuickReplies({ data, onPick, enabled }: { data: GData["quickReplies"]; onPick: (s: string) => void; enabled: boolean }) {
  return (
    <div className="my-1 flex flex-wrap gap-1.5">
      {data.questions.flatMap((q) =>
        q.options.map((o) => (
          <Button key={q.question + o} size="sm" variant="outline" className="h-7 rounded-full text-[12px]" disabled={!enabled} onClick={() => onPick(o)}>
            {o}
          </Button>
        )),
      )}
    </div>
  );
}
