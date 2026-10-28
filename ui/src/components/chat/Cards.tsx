import { Check, CheckCircle2, Circle, CircleDot, CornerDownRight, HelpCircle, ShieldAlert, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Markdown } from "./Markdown";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { api } from "@/lib/api";
import type { GData, PlanEntry } from "@/lib/transcript";
import { cn } from "@/lib/utils";
import { useKept } from "@/lib/kept";
import { registerAsk } from "@/lib/askkeys";

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
        <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-sm bg-muted/60 px-2 py-1 font-mono text-[12px]">{d}</pre>
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
 * 键盘照 Claude Code 终端：↑↓ 挪高亮、数字直选、回车选高亮那个、空格勾多选、←→ 翻题；挪到「其他」那一行就进输入框写。
 * 这些键从输入框转过来（输入框空着时，见 lib/askkeys），点过卡片、焦点在卡片上时也认。
 */
export function AskCard({ sessionId, data }: { sessionId: string; data: GData["ask"] }) {
  const qs = data.request.questions;
  const k = `${sessionId}:ask:${data.reqId}`;
  const [sel, setSel] = useKept<Record<string, string[]>>(`${k}:sel`, {});
  const [other, setOther] = useKept<Record<string, string>>(`${k}:other`, {});
  const [page, setPage] = useKept(`${k}:page`, 0);
  const [cur, setCur] = useKept<Record<string, number>>(`${k}:cur`, {});
  const [busy, setBusy] = useState(false);
  const otherRef = useRef<HTMLInputElement>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const back = useRef<HTMLElement | null>(null); // 进「其他」输入框之前焦点在哪（一般是输入框），↑ / Esc 回那里
  const keyRef = useRef<(e: KeyboardEvent) => boolean>(() => false);
  const pending = !data.answers && !data.cancelled;
  useEffect(() => (pending ? registerAsk(sessionId, (e) => keyRef.current(e)) : undefined), [sessionId, pending]);
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
  const opts = q.options ?? [];
  // 单选点一下（或按数字 / 回车）就是答案，直接进下一题（最后一题就提交）—— 和 Claude Code 一样。
  // 只有多选、或要提交「其他」里自己写的字时才用得着「继续 / 提交」按钮。
  const instant = !q.multiSelect && !q.allowOther;
  const next = (s2 = sel) => (last ? submit(s2) : setPage(i + 1));
  const pick = (label: string) => {
    if (q.multiSelect) {
      setSel({ ...sel, [q.id]: picked.includes(label) ? picked.filter((x) => x !== label) : [...picked, label] });
      return;
    }
    const s2 = { ...sel, [q.id]: [label] };
    setSel(s2);
    // 选了现成的选项就不算「其他」：submit 里「其他」有字时会盖掉选项
    if (other[q.id]) setOther({ ...other, [q.id]: "" });
    next(s2);
  };
  // 高亮在哪一行：0..opts.length-1 是选项，opts.length 是「其他」那一行
  const rows = opts.length + (q.allowOther ? 1 : 0);
  const initial = Math.max(0, opts.findIndex((o) => picked.includes(o.label)));
  const c = Math.min(cur[q.id] ?? initial, Math.max(0, rows - 1));
  const moveTo = (n: number) => {
    setCur({ ...cur, [q.id]: n });
    if (q.allowOther && n === opts.length) {
      if (document.activeElement !== otherRef.current) back.current = document.activeElement as HTMLElement | null;
      otherRef.current?.focus();
    }
  };
  const leaveOther = (n: number) => {
    setCur({ ...cur, [q.id]: n });
    const b = back.current;
    (b?.isConnected && b !== otherRef.current ? b : cardRef.current)?.focus();
  };
  keyRef.current = (e) => {
    if (busy || e.metaKey || e.ctrlKey || e.altKey || e.isComposing || e.keyCode === 229) return false;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      if (!rows) return false;
      moveTo(Math.max(0, Math.min(rows - 1, c + (e.key === "ArrowDown" ? 1 : -1))));
      return true;
    }
    if ((e.key === "ArrowLeft" || e.key === "ArrowRight") && qs.length > 1) {
      const p = i + (e.key === "ArrowRight" ? 1 : -1);
      if (p >= 0 && p < qs.length) setPage(p);
      return true;
    }
    // 认 code 不认 key：shift 按住时 key 是符号
    const d = /^(?:Digit|Numpad)([1-9])$/.exec(e.code);
    if (d && !e.shiftKey) {
      const n = Number(d[1]) - 1;
      if (n < opts.length) {
        setCur({ ...cur, [q.id]: n });
        pick(opts[n].label);
        return true;
      }
      if (q.allowOther && n === opts.length) {
        moveTo(n);
        return true;
      }
      return false;
    }
    if (e.key === " " && !e.shiftKey) {
      if (c >= opts.length) return false;
      pick(opts[c].label);
      return true;
    }
    if (e.key === "Enter" && !e.shiftKey) {
      if (c >= opts.length) {
        if (!rows) return false;
        moveTo(c);
        return true;
      }
      // 多选：一个都没勾时回车就勾上高亮那个再继续；勾过了回车就是「继续 / 提交」
      if (q.multiSelect) next(picked.length ? sel : { ...sel, [q.id]: [opts[c].label] });
      else pick(opts[c].label);
      return true;
    }
    return false;
  };
  return (
    <div
      className="my-1.5 rounded-xl border bg-card p-4 shadow-sm outline-none"
      data-attention
      ref={cardRef}
      tabIndex={-1}
      onKeyDown={(e) => {
        if (e.target === otherRef.current) return; // 「其他」输入框自己管
        if (keyRef.current(e.nativeEvent)) e.preventDefault();
      }}
    >
      <div className="mb-1 flex items-center gap-1.5 text-[12px] text-muted-foreground">
        <HelpCircle className="size-3.5 text-brand" />
        {qs.length > 1 ? `问题 ${i + 1} / ${qs.length}` : "需要你回答"}
        {q.header && <span>· {q.header}</span>}
      </div>
      <div className="mb-2 text-[15px] font-medium leading-snug">{q.question}</div>
      {/* -mx-2.5 抵掉行的 px-2.5：选项文字和问题左边对齐，选中 / 悬停的底色往外多出一点 */}
      <div className="-mx-2.5 flex flex-col gap-0.5">
        {opts.map((o, n) => {
          const on = picked.includes(o.label);
          return (
            <button
              key={o.label}
              disabled={busy}
              className={cn(
                "flex w-full items-center gap-3 rounded-lg px-2.5 py-2 text-left text-[13px] outline-none transition-colors hover:bg-muted/60",
                n === c && "bg-muted/60",
                on && "bg-muted hover:bg-muted",
              )}
              onMouseEnter={() => c !== n && setCur({ ...cur, [q.id]: n })}
              onClick={() => {
                setCur({ ...cur, [q.id]: n });
                pick(o.label);
              }}
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
            ref={otherRef}
            className="mt-0.5 w-full rounded-lg border border-dashed bg-transparent px-2.5 py-2 text-[13px] outline-none placeholder:text-muted-foreground/70 focus:border-solid focus:border-ring/60"
            type={q.secret ? "password" : "text"}
            placeholder={q.options?.length ? "其他，自己写…" : "你的回答…"}
            value={other[q.id] ?? ""}
            disabled={busy}
            onChange={(e) => setOther({ ...other, [q.id]: e.target.value })}
            onFocus={() => c !== opts.length && setCur({ ...cur, [q.id]: opts.length })}
            onKeyDown={(e) => {
              if (e.nativeEvent.isComposing || e.keyCode === 229) return;
              if (e.key === "Enter") {
                e.preventDefault();
                // 空着回车不算答案：多选照样继续（勾过的选项就是答案），单选没东西可交
                if (other[q.id]?.trim() || (q.multiSelect && picked.length) || !opts.length) next();
              } else if (e.key === "ArrowUp" && opts.length) {
                e.preventDefault();
                leaveOther(opts.length - 1);
              } else if (e.key === "Escape") {
                e.preventDefault();
                e.stopPropagation();
                leaveOther(c);
              }
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
        <span className="min-w-0 flex-1 truncate text-[12px] text-muted-foreground max-md:hidden">
          ↑↓ 选 · 数字直选 · ↵ 确定{qs.length > 1 && " · ←→ 翻题"} · 或直接在下面输入框里回复
        </span>
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
            <span className="rounded-sm border border-current/30 px-1 text-[10px] opacity-70">↵</span>
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
