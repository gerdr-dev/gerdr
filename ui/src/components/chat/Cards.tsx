import { Check, CheckCircle2, Circle, CircleDot, HelpCircle, ShieldAlert, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Markdown } from "./Markdown";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import type { GData, PlanEntry } from "@/lib/transcript";
import { cn } from "@/lib/utils";

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
      <div className="flex items-center gap-1.5 px-1.5 py-0.5 text-[12px] text-muted-foreground">
        {ok ? <Check className="size-3.5 text-ok" /> : <X className="size-3.5 text-bad" />}
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

export function AskCard({ sessionId, data }: { sessionId: string; data: GData["ask"] }) {
  const qs = data.request.questions;
  const [sel, setSel] = useState<Record<string, string[]>>({});
  const [other, setOther] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  if (data.answers || data.cancelled) {
    return (
      <div className="my-1 space-y-0.5 px-1.5 text-[12px] text-muted-foreground">
        {data.cancelled && !data.answers ? (
          <div className="flex items-center gap-1.5">
            <X className="size-3.5" /> 没回答
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
  const single = qs.length === 1 && !qs[0].multiSelect;
  return (
    <div className="my-1.5 space-y-3 rounded-lg border border-brand/50 bg-brand/6 p-3" data-attention>
      {qs.map((q) => (
        <div key={q.id}>
          <div className="mb-1.5 flex items-start gap-2 text-[13px]">
            <HelpCircle className="mt-0.5 size-4 shrink-0 text-brand" />
            <div>
              {q.header && <div className="text-[11px] uppercase tracking-wide text-muted-foreground">{q.header}</div>}
              <div className="font-medium">{q.question}</div>
            </div>
          </div>
          <div className="flex flex-col gap-1 pl-6">
            {(q.options ?? []).map((o) => {
              const on = (sel[q.id] ?? []).includes(o.label);
              return (
                <button
                  key={o.label}
                  disabled={busy}
                  className={cn(
                    "flex items-start gap-2 rounded-md border px-2.5 py-1.5 text-left text-[13px] hover:bg-accent",
                    on && "border-brand bg-brand/10",
                  )}
                  onClick={() => {
                    if (q.multiSelect) {
                      const cur = sel[q.id] ?? [];
                      setSel({ ...sel, [q.id]: on ? cur.filter((x) => x !== o.label) : [...cur, o.label] });
                    } else if (single) {
                      submit({ [q.id]: [o.label] });
                    } else {
                      setSel({ ...sel, [q.id]: [o.label] });
                    }
                  }}
                >
                  {q.multiSelect && <Checkbox checked={on} className="mt-0.5" />}
                  <span>
                    <span className="font-medium">{o.label}</span>
                    {o.description && <span className="block text-[12px] text-muted-foreground">{o.description}</span>}
                  </span>
                </button>
              );
            })}
            {q.allowOther && (
              <Input
                className="h-8 text-[13px]"
                type={q.secret ? "password" : "text"}
                placeholder={q.options?.length ? "其他（自己写）" : "你的回答"}
                value={other[q.id] ?? ""}
                onChange={(e) => setOther({ ...other, [q.id]: e.target.value })}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !e.nativeEvent.isComposing && e.keyCode !== 229) submit();
                }}
              />
            )}
          </div>
        </div>
      ))}
      {!(single && !qs[0].allowOther) && (
        <div className="flex gap-2 pl-6">
          <Button size="sm" disabled={busy} onClick={() => submit()}>
            提交
          </Button>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => api.answer(sessionId, data.reqId, { cancelled: true }).catch(() => {})}>
            不回答
          </Button>
        </div>
      )}
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
