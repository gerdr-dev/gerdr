import {
  ChevronDown,
  CircleAlert,
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
  Bot,
} from "lucide-react";
import { diffLines } from "diff";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "../ui/collapsible";
import { allowRetryText, isCommandText, type ToolContent, type ToolMeta, type ToolPart } from "../transcript";
import { cn } from "../utils";
import { DiffView } from "./DiffView";
import { useSession } from "./sessionContext";
import { useKept } from "../kept";
import { elapsed } from "./TasksBar";
import { chatHost, useChatHost, useChatSettings } from "../host";
import { t } from "../i18n";
import { toast } from "sonner";

function useTick(on: boolean) {
  const [, set] = useState(0);
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => set((x) => x + 1), 1000);
    return () => clearInterval(t);
  }, [on]);
}

/**
 * 从 since（unix ms）起满 ms 毫秒才变 true，on 变回 false 时归零。since 用日志里的时间：重新打开会话时早就跑了很久的那条马上就是 true，
 * 不用再等一遍。since 来自服务端，和本机钟对不上时最多等 ms；没有 since 从挂载算。
 */
function useAfter(on: boolean, since: number | undefined, ms: number) {
  const [, set] = useState(0);
  const [mounted] = useState(Date.now); // 没有开始时间：从画出来那一刻算
  const left = Math.min(ms, ms - (Date.now() - (since ?? mounted)));
  useEffect(() => {
    if (!on || left <= 0) return;
    const t = setTimeout(() => set((x) => x + 1), left);
    return () => clearTimeout(t);
  }, [on, since]);
  return on && left <= 0;
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
          {all ? t("common.collapse") : t("tool.moreBefore", { n: lines.length - max })}
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

/** A tool call row; the host may render some tools its own way (ChatHost.renderTool). */
export function ToolCall(props: { part: ToolPart; agent?: boolean; running: boolean; keep?: string }) {
  const host = useChatHost();
  const session = useSession();
  const custom = host.renderTool?.(props.part, { sessionId: session?.id ?? "", running: props.running });
  return custom !== undefined ? <>{custom}</> : <DefaultToolCall {...props} />;
}

function DefaultToolCall({ part, agent, running, keep: keepOpen }: { part: ToolPart; agent?: boolean; running: boolean; keep?: string }) {
  const meta = (part.toolMetadata ?? { kind: "other", status: "pending" }) as ToolMeta;
  const busy = (part.state === "input-streaming" || part.state === "input-available") && running;
  const failed = part.state === "output-error";
  const hasDiff = (meta.content ?? []).some(
    (c) => c.type === "diff" || (c.type === "content" && (c._meta?.gerdr?.unifiedDiff || (c.content as any)?._meta?.gerdr?.unifiedDiff)),
  );
  // 改文件的默认展开（看 diff 是主要用途；设置里能关），其余默认收起，一行一个。
  const expandEdits = useChatSettings().expandEdits !== false;
  const session = useSession();
  // 展开没展开记在组件外面：对话是虚拟列表，滚出屏幕会卸载，滚回来要还是原样
  const keep = session ? `${session.id}:tool:${part.toolCallId}` : undefined;
  const [open, setOpen] = useKept<boolean | undefined>(keepOpen ?? keep, undefined);
  const isOpen = open ?? (hasDiff && expandEdits);
  const title = part.title || part.toolName;
  const inp = part.input && typeof part.input === "object" ? (part.input as Record<string, unknown>) : undefined;
  const cmd = meta.kind === "execute" && typeof inp?.command === "string" ? inp.command : undefined;
  const desc = meta.kind === "execute" && typeof inp?.description === "string" && inp.description.trim() ? inp.description.trim() : undefined;
  const stat = useMemo(() => diffStat(meta.content ?? []), [meta.content]);
  const took = meta.startedAt && meta.endedAt && meta.endedAt - meta.startedAt >= 1000 ? elapsed(meta.startedAt, meta.endedAt) : undefined;
  const task = session?.tasks?.find((t) => t.toolCallId === part.toolCallId);
  useTick(!!task && task.status === "running");
  // Claude reports a subagent (Task / Agent tool) with kind "think", which drew a brain and made it look like a thinking step.
  // agent: it has tool calls of its own, which marks a subagent even before its task is reported.
  const isAgent = !!task && (task.type === "local_agent" || !!task.subagent);
  const Icon = isAgent || agent ? Bot : (ICONS[meta.kind] ?? Wrench);
  // 任务的进度 / 摘要：和行里已经写着的（说明、标题、任务描述）一样就不再写一遍
  const note = task && (task.status === "running" ? task.progress : task.summary);
  const taskNote =
    note && ![desc, title, task?.description].includes(note) && !/^Background command ".*" (completed|failed|was stopped|killed)/.test(note) ? note : undefined;
  // 「转到后台」= claude 里按 ctrl+b：这条命令 / 这个子 agent 接着在后台跑，这一轮先往下走。
  // 子 agent 的工具调用（meta.parent）不给：那是它自己那一轮里的事，转后台得由它决定。
  // 跑满 3 秒才露出来：大部分命令一两秒就完，按钮一闪而过，还会把行尾的耗时挤来挤去
  const slow = useAfter(busy, meta.startedAt, 3000);
  const canBackground =
    slow &&
    busy &&
    !!chatHost().backgroundTool &&
    !meta.parent &&
    ["claude", "native"].includes(session?.base ?? session?.agent ?? "") &&
    !task?.background &&
    (meta.kind === "execute" || !!task);
  // 「允许并重试」：auto 模式的分类器拦下的调用（claude 里是 /permissions 里批准）。规则写进项目的
  // .claude/settings.local.json，再发一句话让它重试。点过的记一下，免得重复发。
  const [allowed, setAllowed] = useKept<boolean>(keep && `${keep}:allowed`, false);
  const canAllow = failed && !!meta.autoDenied?.length && !!session && !!chatHost().allowRules;
  return (
    <Collapsible open={isOpen} onOpenChange={setOpen} className="group/tool">
      {/* 照 Astryx 的 ChatToolCalls：状态图标打头（转圈 / ✓ / ✗），然后是干了什么，尾巴上 diff 行数、耗时、展开箭头。
          -ml-1.5 抵掉 px-1.5：图标和正文左边对齐，hover 底色往外多出一点 */}
      <CollapsibleTrigger className="group/row -ml-1.5 flex w-[calc(100%+0.375rem)] min-w-0 items-center gap-2 rounded-md px-1.5 py-[3px] text-left text-[13px] text-muted-foreground hover:bg-muted/60 hover:text-foreground">
        {/* 一行一个图标（照 Codex）：平时是工具种类，在跑换成转圈，失败换成红叉。成功不打勾 —— 一列下来全是绿勾，只是噪音 */}
        {busy || task?.status === "running" ? (
          <Loader2 className="size-3.5 shrink-0 animate-spin" />
        ) : failed ? (
          <CircleX className="size-3.5 shrink-0 text-bad" />
        ) : (
          <Icon className="size-3.5 shrink-0" />
        )}
        {desc ? (
          // 命令带了说明（claude 的 Bash 工具有 description 字段）：只写说明，命令点开看（照 Codex，一整行命令太抢眼）
          <span className="min-w-0 truncate" title={cmd}>
            {desc}
          </span>
        ) : (
          // Monospace only when the title is the command itself (isCommandText); execute rows can also carry prose titles
          // ("Stop background command", "Run command") that looked out of place in monospace.
          // 12px monospace at line-height 1.5 is 18px, 1.5px shorter than 13px body text: when the description arrives after the
          // command has streamed in, the row height changes and the virtual list jumps. Pin it to the body's 19.5px
          <span className={cn("min-w-0 truncate", isCommandText(title, cmd) && "font-mono text-[12px] leading-[19.5px]")}>{title}</span>
        )}
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
            {/* Claude only sets is_backgrounded for ctrl+b; a Bash call with run_in_background is a background command too */}
            {(task.background || inp?.run_in_background === true) && (
              <span className="shrink-0 rounded-sm bg-muted px-1 text-[11px]">{t("task.background")}</span>
            )}
            {/* 进度文字可以截断，耗时不能（截成「3.」看不懂）。跑完后 CLI 自动写的「Background command "…" completed」
                不显示：就是把标题和状态又说一遍 */}
            {taskNote && <span className="min-w-0 shrink truncate text-[11px] text-muted-foreground">{taskNote}</span>}
            <span className="shrink-0 text-[11px] tabular-nums opacity-70">
              {elapsed(task.startedAt, task.endedAt)}
              {task.toolUses ? t("task.tools", { n: task.toolUses }) : ""}
            </span>
          </>
        )}
        {/* 后面的按钮和展开箭头都靠右 */}
        <span className="flex-1" />
        {task && isAgent && chatHost().openSubagent && (
          <span
            role="button"
            className="shrink-0 rounded-sm px-1.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
            title={t("tool.openSubagent")}
            onClick={(e) => {
              e.stopPropagation();
              chatHost().openSubagent?.(session!.id, part.toolCallId, task.description);
            }}
          >
            {t("task.viewChat")}
          </span>
        )}
        {canBackground && (
          <span
            role="button"
            className="shrink-0 rounded-sm px-1.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
            title={t("tool.toBackgroundTitle")}
            onClick={(e) => {
              e.stopPropagation();
              chatHost()
                .backgroundTool?.(session!.id, part.toolCallId)
                .catch((err) => toast.error(err.message));
            }}
          >
            {t("tool.toBackground")}
          </span>
        )}
        {canAllow && (
          <span
            role="button"
            aria-disabled={allowed}
            className={cn("shrink-0 rounded-sm px-1.5 text-[11px]", allowed ? "text-muted-foreground" : "text-brand hover:bg-muted")}
            title={t("tool.allowTitle", { rules: meta.autoDenied!.join("\n") })}
            onClick={async (e) => {
              e.stopPropagation();
              if (allowed) return;
              try {
                await chatHost().allowRules?.(session!.id, meta.autoDenied!);
                setAllowed(true);
                await chatHost().send(session!.id, [{ type: "text", text: allowRetryText(meta.autoDenied!) }]);
              } catch (err: any) {
                toast.error(err.message);
              }
            }}
          >
            {allowed ? t("tool.allowed") : t("tool.allowRetry")}
          </span>
        )}
        {failed && part.errorText && part.errorText !== t("tool.failed") && (
          <span className="flex min-w-0 shrink items-center gap-1 truncate text-[11px] text-bad">
            <CircleAlert className="size-3 shrink-0" />
            {part.errorText}
          </span>
        )}
        <ChevronDown className={cn("size-3.5 shrink-0 opacity-0 transition group-hover/row:opacity-60", isOpen && "rotate-180 opacity-60")} />
      </CollapsibleTrigger>
      <CollapsibleContent className="pl-[22px] pr-1 pt-1 pb-2">
        <ToolBody meta={meta} input={part.input} keep={keep} />
      </CollapsibleContent>
    </Collapsible>
  );
}
