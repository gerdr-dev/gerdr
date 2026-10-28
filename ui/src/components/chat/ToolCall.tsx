import {
  ChevronDown,
  CircleAlert,
  CircleCheck,
  CircleDashed,
  CircleX,
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
import { diffLines } from "diff";
import { useEffect, useMemo, useState, type ReactNode } from "react";
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

/** 改文件的调用：总共加了几行、删了几行（行尾的 +12 -3）。 */
function diffStat(content: ToolContent[]): { add: number; del: number } | undefined {
  let add = 0,
    del = 0,
    any = false;
  for (const c of content) {
    if (c.type === "diff") {
      any = true;
      for (const p of diffLines(c.oldText ?? "", c.newText)) {
        const n = p.count ?? p.value.replace(/\n$/, "").split("\n").length;
        if (p.added) add += n;
        else if (p.removed) del += n;
      }
      continue;
    }
    const ud = c.type === "content" ? (c._meta?.gerdr?.unifiedDiff ?? (c.content as any)?._meta?.gerdr?.unifiedDiff) : undefined;
    if (!ud) continue;
    any = true;
    for (const l of String(ud.diff).split("\n")) {
      if (l.startsWith("+") && !l.startsWith("+++")) add++;
      else if (l.startsWith("-") && !l.startsWith("---")) del++;
    }
  }
  return any ? { add, del } : undefined;
}

function textOf(c: ToolContent): string | undefined {
  return c.type === "content" && c.content?.type === "text" ? c.content.text : undefined;
}

function OutputText({ text, keep, bare }: { text: string; keep?: string; bare?: boolean }) {
  const [all, setAll] = useKept(keep, false);
  const lines = text.replace(/\n+$/, "").split("\n");
  const max = 18;
  const shown = all || lines.length <= max ? lines : lines.slice(-max);
  return (
    <div className={cn("overflow-hidden", bare ? "border-t" : "rounded-lg border bg-muted/30")}>
      {lines.length > max && (
        <button className="w-full border-b py-0.5 text-[11px] text-muted-foreground hover:bg-muted/50" onClick={() => setAll(!all)}>
          {all ? "收起" : `前面还有 ${lines.length - max} 行`}
        </button>
      )}
      <pre className="max-h-[420px] overflow-auto whitespace-pre-wrap break-all px-2.5 py-1.5 font-mono text-[12px] leading-[1.5]">{shown.join("\n")}</pre>
    </div>
  );
}

export function ToolBody({ meta, input, keep }: { meta: ToolMeta; input: unknown; keep?: string }) {
  const cmd: string | undefined = meta.kind === "execute" && input && typeof input === "object" ? (input as any).command : undefined;
  const isCmd = !!cmd;
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
    if (t) blocks.push(<OutputText key={i} text={t} keep={keep && `${keep}:out${i}`} bare={isCmd} />);
  });
  // 命令：命令和输出合在一张卡片里（上面 $ 命令，下面输出），照 Astryx ChatToolCalls 展开后的代码块
  if (cmd)
    return (
      <div className="overflow-hidden rounded-lg border bg-muted/30">
        <pre className="overflow-x-auto whitespace-pre-wrap break-all px-2.5 py-1.5 font-mono text-[12px]">
          <span className="select-none text-muted-foreground">$ </span>
          {cmd}
        </pre>
        {blocks}
      </div>
    );
  return (
    <div className="space-y-2">
      {blocks}
      {blocks.length === 0 && input != null && (
        <pre className="max-h-64 overflow-auto rounded-lg bg-muted/40 px-2 py-1.5 font-mono text-[11px] text-muted-foreground">
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
  const stat = useMemo(() => diffStat(meta.content ?? []), [meta.content]);
  const took = meta.startedAt && meta.endedAt && meta.endedAt - meta.startedAt >= 1000 ? elapsed(meta.startedAt, meta.endedAt) : undefined;
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
      {/* 照 Astryx 的 ChatToolCalls：状态图标打头（转圈 / ✓ / ✗），然后是干了什么，尾巴上 diff 行数、耗时、展开箭头。
          -ml-1.5 抵掉 px-1.5：图标和正文左边对齐，hover 底色往外多出一点 */}
      <CollapsibleTrigger className="group/row -ml-1.5 flex w-[calc(100%+0.375rem)] min-w-0 items-center gap-2 rounded-md px-1.5 py-[3px] text-left text-[13px] text-muted-foreground hover:bg-muted/60 hover:text-foreground">
        {busy || task?.status === "running" ? (
          <Loader2 className="size-3.5 shrink-0 animate-spin" />
        ) : failed ? (
          <CircleX className="size-3.5 shrink-0 text-bad" />
        ) : part.state === "output-available" ? (
          <CircleCheck className="size-3.5 shrink-0 text-ok/80" />
        ) : (
          <CircleDashed className="size-3.5 shrink-0" />
        )}
        <Icon className="size-3.5 shrink-0 opacity-70" />
        <span className={cn("min-w-0 truncate", meta.kind === "execute" && "font-mono text-[12px]")}>{title}</span>
        {stat && (stat.add > 0 || stat.del > 0) && (
          <span className="shrink-0 font-mono text-[11px] tabular-nums">
            {stat.add > 0 && <span className="text-ok">+{stat.add}</span>}
            {stat.add > 0 && stat.del > 0 && " "}
            {stat.del > 0 && <span className="text-bad">-{stat.del}</span>}
          </span>
        )}
        {!task && took && <span className="shrink-0 text-[11px] tabular-nums opacity-70">{took}</span>}
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
        {/* 后面的按钮和展开箭头都靠右 */}
        <span className="flex-1" />
        {task && (task.type === "local_agent" || task.subagent) && (
          <span
            role="button"
            className="shrink-0 rounded px-1.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
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
            className="shrink-0 rounded px-1.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
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
        {failed && part.errorText && part.errorText !== "失败" && (
          <span className="flex min-w-0 shrink items-center gap-1 truncate text-[11px] text-bad">
            <CircleAlert className="size-3 shrink-0" />
            {part.errorText}
          </span>
        )}
        <ChevronDown className={cn("size-3.5 shrink-0 opacity-0 transition group-hover/row:opacity-60", isOpen && "rotate-180 opacity-60")} />
      </CollapsibleTrigger>
      <CollapsibleContent className="pl-[22px] pr-1 pt-1 pb-2">
        <ToolBody meta={meta} input={part.input} keep={keep} />
        {children && <div className="mt-2 space-y-1 border-l pl-2">{children}</div>}
      </CollapsibleContent>
    </Collapsible>
  );
}
