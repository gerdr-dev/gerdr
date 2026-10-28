import { Activity, Bot, ChevronDown, CircleX, MessagesSquare, Square, SquareTerminal, Workflow } from "lucide-react";
import { openSubagent } from "@/lib/actions";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import type { AgentTask, SessionSummary } from "@/lib/types";
import { cn } from "@/lib/utils";

// 后台任务栏：输入框上面一条。有在跑的就显示「n 个后台任务」，点开是列表（停止 / 看结果）。
// 和 Claude Code 自己的任务列表是同一份东西（CLI 报的 task_*）。

export function taskIcon(t: AgentTask) {
  if (t.type === "local_bash") return SquareTerminal;
  if (t.type === "local_agent" || t.subagent) return Bot;
  return Workflow;
}

export function elapsed(from: number, to?: number) {
  const s = Math.max(0, Math.round(((to ?? Date.now()) - from) / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m${s % 60 ? `${s % 60}s` : ""}`;
  return `${Math.floor(m / 60)}h${m % 60}m`;
}

const STATUS: Record<string, string> = { completed: "完成", failed: "失败", killed: "已停止", stopped: "已停止", cancelled: "已停止" };

function useTick(on: boolean) {
  const [, set] = useState(0);
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => set((x) => x + 1), 1000);
    return () => clearInterval(t);
  }, [on]);
}

// 只放正在跑的。跑完的进对话流（hub 记一条 task 事件，MessageView 画成 TaskDoneCard）。
export function TasksBar({ session }: { session: SessionSummary }) {
  const all = session.tasks ?? [];
  const running = all.filter((t) => t.status === "running");
  const [open, setOpen] = useState(true);
  useTick(running.length > 0);
  if (running.length === 0) return null;
  const top = running.filter((t) => !t.parent || !running.some((x) => x.toolCallId === t.parent));
  return (
    <div className="mx-3 mb-1.5 rounded-lg border bg-card text-[12px]">
      <button className="flex w-full items-center gap-2 px-3 py-1.5" onClick={() => setOpen(!open)}>
        {/* 不转圈：对话里那条工具调用和底下的状态行已经在转了 */}
        <Activity className="size-3.5 text-muted-foreground" />
        <span>{running.length} 个后台任务在跑</span>
        {!open && top[0] && <span className="min-w-0 flex-1 truncate text-left text-muted-foreground">{top[0].progress || top[0].description}</span>}
        <ChevronDown className={cn("ml-auto size-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-180")} />
      </button>
      {open && (
        <div className="max-h-56 divide-y overflow-auto border-t">
          {/* 子 agent 起的任务挂在它下面（缩进），别看着像主 agent 自己在跑 */}
          {top.map((t) => (
            <div key={t.id}>
              <TaskRow sessionId={session.id} t={t} />
              {running
                .filter((c) => c.parent && c.parent === t.toolCallId)
                .map((c) => (
                  <div key={c.id} className="ml-5 border-l-2">
                    <TaskRow sessionId={session.id} t={c} />
                  </div>
                ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** 对话流里的「这个后台任务结束了」。 */
/** CLI 自动写的那种摘要（`Background command "xxx" completed (exit code 0)`）就是把描述和状态又说了一遍，不显示。 */
function boilerplate(t: AgentTask) {
  return !t.summary || t.summary === t.description || /^Background command ".*" (completed|failed|was stopped|killed)/.test(t.summary);
}

/**
 * 后台任务跑完了：时间线上标一下它是这时候结束的。画成和工具调用（ToolCall）一样的一行 ——
 * 状态图标打头、行尾耗时，对齐也一样（-ml-1.5 px-1.5），不再是一张带框的卡片夹在一串工具行中间。
 */
export function TaskDoneCard({ sessionId, t }: { sessionId: string; t: AgentTask }) {
  const Icon = taskIcon(t);
  const ok = t.status === "completed";
  const isAgent = t.type === "local_agent" || !!t.subagent;
  return (
    <div className="-ml-1.5 flex min-w-0 items-center gap-2 px-1.5 py-[3px] text-[13px] text-muted-foreground">
      {/* 和工具调用那一行一样只有一个图标：成功就是任务图标，失败换成红叉 */}
      {ok ? <Icon className="size-3.5 shrink-0" /> : <CircleX className="size-3.5 shrink-0 text-bad" />}
      <span className="min-w-0 truncate">
        {isAgent && t.subagent ? `${t.subagent} · ` : ""}
        {t.description || t.id}
      </span>
      <span className="shrink-0 rounded bg-muted px-1 text-[11px]">{isAgent ? "子 agent" : "后台"}</span>
      {!ok && <span className="shrink-0 text-[11px] text-bad">{STATUS[t.status] ?? t.status}</span>}
      {!boilerplate(t) && <span className="min-w-0 shrink truncate text-[11px]">{t.summary}</span>}
      <span className="shrink-0 text-[11px] tabular-nums opacity-70">
        {elapsed(t.startedAt, t.endedAt)}
        {t.toolUses ? ` · ${t.toolUses} 次工具` : ""}
      </span>
      <span className="flex-1" />
      {isAgent && t.toolCallId && (
        <button
          className="shrink-0 rounded px-1.5 text-[11px] hover:bg-muted hover:text-foreground"
          onClick={() => openSubagent(sessionId, t.toolCallId!, t.description)}
        >
          查看对话
        </button>
      )}
    </div>
  );
}

function TaskRow({ sessionId, t }: { sessionId: string; t: AgentTask }) {
  const Icon = taskIcon(t);
  const live = t.status === "running";
  const [stopping, setStopping] = useState(false);
  return (
    <div className="flex items-start gap-2 px-3 py-1.5">
      <Icon className={cn("mt-0.5 size-3.5 shrink-0", live ? "text-foreground" : "text-muted-foreground")} />
      <div className="min-w-0 flex-1">
        <div className="truncate">
          {t.subagent && <span className="mr-1 text-muted-foreground">{t.subagent} ·</span>}
          {t.description || t.id}
        </div>
        <div className="truncate text-[11px] text-muted-foreground">
          {live ? (t.progress ? `${t.progress} · ` : "") : `${STATUS[t.status] ?? t.status} · `}
          {elapsed(t.startedAt, t.endedAt)}
          {t.toolUses ? ` · ${t.toolUses} 次工具调用` : ""}
          {!live && t.summary ? ` · ${t.summary}` : ""}
        </div>
      </div>
      {t.toolCallId && (t.type === "local_agent" || t.subagent) && (
        <Button size="sm" variant="ghost" className="h-6 shrink-0 px-2 text-[11px]" onClick={() => openSubagent(sessionId, t.toolCallId!, t.description)}>
          <MessagesSquare className="size-3" />
          查看对话
        </Button>
      )}
      {live && (
        <Button
          size="sm"
          variant="ghost"
          className="h-6 shrink-0 px-2 text-[11px]"
          disabled={stopping}
          onClick={async () => {
            setStopping(true);
            try {
              await api.stopTask(sessionId, t.id);
            } catch (e: any) {
              toast.error(e.message);
            } finally {
              setStopping(false);
            }
          }}
        >
          <Square className="size-3" />
          停止
        </Button>
      )}
    </div>
  );
}
