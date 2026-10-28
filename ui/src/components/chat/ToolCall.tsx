import {
  ChevronRight,
  CircleAlert,
  FilePen,
  FileSearch,
  Globe,
  Loader2,
  Search,
  Sparkles,
  SquareTerminal,
  Trash2,
  Wrench,
  ArrowRightLeft,
  Brain,
} from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import type { ToolContent, ToolMeta, ToolPart } from "@/lib/transcript";
import { cn } from "@/lib/utils";
import { DiffView } from "./DiffView";
import { useSession } from "./sessionContext";
import { useKept } from "@/lib/kept";
import { elapsed } from "./TasksBar";
import { api } from "@/lib/api";
import { openSubagent } from "@/lib/actions";
import { toast } from "sonner";

function useTick(on: boolean) {
  const [, set] = useState(0);
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => set((x) => x + 1), 1000);
    return () => clearInterval(t);
  }, [on]);
}

const ICONS: Record<string, typeof Wrench> = {
  execute: SquareTerminal,
  edit: FilePen,
  read: FileSearch,
  search: Search,
  fetch: Globe,
  delete: Trash2,
  move: ArrowRightLeft,
  think: Brain,
  switch_mode: Sparkles,
};

function textOf(c: ToolContent): string | undefined {
  return c.type === "content" && c.content?.type === "text" ? c.content.text : undefined;
}

function OutputText({ text, keep }: { text: string; keep?: string }) {
  const [all, setAll] = useKept(keep, false);
  const lines = text.replace(/\n+$/, "").split("\n");
  const max = 18;
  const shown = all || lines.length <= max ? lines : lines.slice(-max);
  return (
    <div className="overflow-hidden rounded-md border bg-muted/30">
      {lines.length > max && (
        <button className="w-full border-b py-0.5 text-[11px] text-muted-foreground hover:bg-muted/50" onClick={() => setAll(!all)}>
          {all ? "收起" : `前面还有 ${lines.length - max} 行`}
        </button>
      )}
      <pre className="max-h-[420px] overflow-auto whitespace-pre-wrap break-all px-2 py-1.5 font-mono text-[12px] leading-[1.5]">{shown.join("\n")}</pre>
    </div>
  );
}

export function ToolBody({ meta, input, keep }: { meta: ToolMeta; input: unknown; keep?: string }) {
  const blocks: ReactNode[] = [];
  const content = meta.content ?? [];
  content.forEach((c, i) => {
    if (c.type === "diff") {
      blocks.push(<DiffView key={i} path={c.path} oldText={c.oldText} newText={c.newText} />);
      return;
    }
    const ud = c.type === "content" ? (c._meta?.gerdr?.unifiedDiff ?? (c.content as any)?._meta?.gerdr?.unifiedDiff) : undefined;
    if (ud) {
      blocks.push(<DiffView key={i} path={ud.path} unified={ud.diff} />);
      return;
    }
    const t = textOf(c);
    if (t) blocks.push(<OutputText key={i} text={t} keep={keep && `${keep}:out${i}`} />);
  });
  const cmd = meta.kind === "execute" && input && typeof input === "object" ? (input as any).command : undefined;
  return (
    <div className="space-y-2">
      {cmd && <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-md bg-muted/50 px-2 py-1.5 font-mono text-[12px]">$ {cmd}</pre>}
      {blocks}
      {!cmd && blocks.length === 0 && input != null && (
        <pre className="max-h-64 overflow-auto rounded-md bg-muted/40 px-2 py-1.5 font-mono text-[11px] text-muted-foreground">
          {JSON.stringify(input, null, 2)}
        </pre>
      )}
    </div>
  );
}

export function ToolCall({ part, children, running }: { part: ToolPart; children?: ReactNode; running: boolean }) {
  const meta = (part.toolMetadata ?? { kind: "other", status: "pending" }) as ToolMeta;
  const Icon = ICONS[meta.kind] ?? Wrench;
  const busy = (part.state === "input-streaming" || part.state === "input-available") && running;
  const failed = part.state === "output-error";
  const hasDiff = (meta.content ?? []).some(
    (c) => c.type === "diff" || (c.type === "content" && (c._meta?.gerdr?.unifiedDiff || (c.content as any)?._meta?.gerdr?.unifiedDiff)),
  );
  // 改文件的默认展开（看 diff 是主要用途），其余默认收起，一行一个。
  const session = useSession();
  // 展开没展开记在组件外面：对话是虚拟列表，滚出屏幕会卸载，滚回来要还是原样
  const keep = session ? `${session.id}:tool:${part.toolCallId}` : undefined;
  const [open, setOpen] = useKept<boolean | undefined>(keep, undefined);
  const isOpen = open ?? (hasDiff || !!children);
  const title = part.title || part.toolName;
  const task = session?.tasks?.find((t) => t.toolCallId === part.toolCallId);
  useTick(!!task && task.status === "running");
  // 「转到后台」= claude 里按 ctrl+b：这条命令 / 这个子 agent 接着在后台跑，这一轮先往下走。
  // 子 agent 的工具调用（meta.parent）不给：那是它自己那一轮里的事，转后台得由它决定。
  const canBackground = busy && !meta.parent && (session?.base ?? session?.agent) === "claude" && !task?.background && (meta.kind === "execute" || !!task);
  // 「允许并重试」：auto 模式的分类器拦下的调用（claude 里是 /permissions 里批准）。规则写进项目的
  // .claude/settings.local.json，再发一句话让它重试。点过的记一下，免得重复发。
  const [allowed, setAllowed] = useKept<boolean>(keep && `${keep}:allowed`, false);
  const canAllow = failed && !!meta.autoDenied?.length && !!session;
  return (
    <Collapsible open={isOpen} onOpenChange={setOpen} className="group/tool">
      <CollapsibleTrigger className="flex w-full min-w-0 items-center gap-2 rounded-md px-1.5 py-1 text-left text-[13px] hover:bg-muted/60">
        <ChevronRight className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", isOpen && "rotate-90")} />
        <Icon className={cn("size-3.5 shrink-0", failed ? "text-bad" : "text-muted-foreground")} />
        <span className={cn("min-w-0 truncate", meta.kind === "execute" && "font-mono text-[12px]")}>{title}</span>
        {(busy || task?.status === "running") && <Loader2 className="size-3.5 shrink-0 animate-spin text-muted-foreground" />}
        {task && (
          <>
            {/* 进度文字可以截断，耗时不能（截成「3.」看不懂） */}
            {(task.background || (task.status === "running" ? task.progress : task.summary)) && (
              <span className="min-w-0 shrink truncate text-[11px] text-muted-foreground">
                {task.background && "后台 · "}
                {task.status === "running" ? task.progress || "" : task.summary || ""}
              </span>
            )}
            <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
              {elapsed(task.startedAt, task.endedAt)}
              {task.toolUses ? ` · ${task.toolUses} 次工具` : ""}
            </span>
          </>
        )}
        {task && (task.type === "local_agent" || task.subagent) && (
          <span
            role="button"
            className="ml-auto shrink-0 rounded px-1.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
            title="在旁边打开这个子 agent 的完整对话（只读）"
            onClick={(e) => {
              e.stopPropagation();
              openSubagent(session!.id, part.toolCallId, task.description);
            }}
          >
            查看对话
          </span>
        )}
        {canBackground && (
          <span
            role="button"
            className={cn(
              "shrink-0 rounded px-1.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground",
              !(task && (task.type === "local_agent" || task.subagent)) && "ml-auto",
            )}
            title="让它在后台接着跑，这一轮先往下走（同 Claude Code 里的 ctrl+b）"
            onClick={(e) => {
              e.stopPropagation();
              api.backgroundTool(session!.id, part.toolCallId).catch((err) => toast.error(err.message));
            }}
          >
            转到后台
          </span>
        )}
        {canAllow && (
          <span
            role="button"
            aria-disabled={allowed}
            className={cn(
              "shrink-0 rounded px-1.5 text-[11px]",
              allowed ? "text-muted-foreground" : "text-brand hover:bg-muted",
            )}
            title={`分类器拦下了这次调用。加上这些规则（写进项目的 .claude/settings.local.json），然后让它重试：\n${meta.autoDenied!.join("\n")}`}
            onClick={async (e) => {
              e.stopPropagation();
              if (allowed) return;
              try {
                await api.allowRules(session!.id, meta.autoDenied!);
                setAllowed(true);
                await api.send(session!.id, [{ type: "text", text: "我已经允许了刚才被拦下的操作，请重试。" }]);
              } catch (err: any) {
                toast.error(err.message);
              }
            }}
          >
            {allowed ? "已允许" : "允许并重试"}
          </span>
        )}
        {failed && (
          <span className="flex shrink-0 items-center gap-1 text-[11px] text-bad">
            <CircleAlert className="size-3" />
            {part.errorText ?? "失败"}
          </span>
        )}
      </CollapsibleTrigger>
      <CollapsibleContent className="pl-6 pr-1 pt-1 pb-2">
        <ToolBody meta={meta} input={part.input} keep={keep} />
        {children && <div className="mt-2 space-y-1 border-l pl-2">{children}</div>}
      </CollapsibleContent>
    </Collapsible>
  );
}
