import { Bot, ChevronDown, Loader2, MessagesSquare, Square, SquareTerminal, Workflow } from "lucide-react";
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
        <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
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
export function TaskDoneCard({ sessionId, t }: { sessionId: string; t: AgentTask }) {
  const Icon = taskIcon(t);
  const ok = t.status === "completed";
  const isAgent = t.type === "local_agent" || !!t.subagent;
  return (
    <div className="flex items-start gap-2 rounded-md border bg-card/50 px-2.5 py-1.5 text-[12px]">
      <Icon className={cn("mt-0.5 size-3.5 shrink-0", ok ? "text-muted-foreground" : "text-bad")} />
      <div className="min-w-0 flex-1">
        <div className="truncate">
          <span className="text-muted-foreground">{isAgent ? `子 agent${t.subagent ? `（${t.subagent}）` : ""}` : "后台命令"}</span>
          <span className="mx-1 text-muted-foreground">·</span>
          {t.description || t.id}
        </div>
        <div className="truncate text-[11px] text-muted-foreground">
          {STATUS[t.status] ?? t.status} · {elapsed(t.startedAt, t.endedAt)}
          {t.toolUses ? ` · ${t.toolUses} 次工具调用` : ""}
          {t.summary ? ` · ${t.summary}` : ""}
        </div>
      </div>
      {isAgent && t.toolCallId && (
        <Button size="sm" variant="ghost" className="h-6 shrink-0 px-2 text-[11px]" onClick={() => openSubagent(sessionId, t.toolCallId!, t.description)}>
          <MessagesSquare className="size-3" />
          查看对话
        </Button>
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
