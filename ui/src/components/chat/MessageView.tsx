import { Bot, CircleAlert, Info, Pencil, RotateCcw, CornerDownRight } from "lucide-react";
import { memo, type ReactNode } from "react";
import { Message, MessageContent } from "@/components/ai-elements/message";
import { Markdown } from "./Markdown";
import { Reasoning, ReasoningContent, ReasoningTrigger } from "@/components/ai-elements/reasoning";
import { Shimmer } from "@/components/ai-elements/shimmer";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { GMessage, GPart, ToolPart } from "@/lib/transcript";
import { cn } from "@/lib/utils";
import { useKept } from "@/lib/kept";
import { focusSession } from "@/lib/actions";
import { useStore } from "@/lib/store";
import { AskCard, PermissionCard, PlanCard, QuickReplies } from "./Cards";
import { ToolCall } from "./ToolCall";
import { TaskDoneCard } from "./TasksBar";
import { fmtElapsed, fmtTokens } from "./StatusLine";

const STOP_TEXT: Record<string, string> = {
  cancelled: "已中断",
  max_tokens: "输出太长被截断了",
  max_turn_requests: "到了单轮的步数上限",
  refusal: "模型拒绝了这个请求",
  error: "出错了",
};

type Props = {
  msg: GMessage;
  sessionId: string;
  live: boolean; // 这一条是正在流式输出的那条
  running: boolean;
  onRewind?: (m: GMessage) => void;
  onEdit?: (m: GMessage) => void;
  onQuickReply: (s: string) => void;
};

/** 这条消息是别的会话里的 agent 发来的（gerdr session send）：标出来源，点一下跳过去。 */
function FromLabel({ id }: { id: string }) {
  const s = useStore((st) => st.sessions[id]);
  const name = s ? s.title || s.cwd.split("/").pop() || s.agent : id.slice(0, 8);
  return (
    <button className="ml-auto flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground" onClick={() => focusSession(id)}>
      <Bot className="size-3" />
      来自会话「{name}」
    </button>
  );
}

export const MessageView = memo(function MessageView({ msg, sessionId, live, running, onRewind, onEdit, onQuickReply }: Props) {
  if (msg.role === "user") {
    const text = msg.parts.map((p) => (p.type === "text" ? p.text : "")).join("");
    const files = msg.parts.filter((p) => p.type === "file");
    const canRewind = !!onRewind && !!msg.metadata?.messageId && !msg.metadata?.steer && !running;
    return (
      <Message from="user" className="group/user">
        {msg.metadata?.from && <FromLabel id={msg.metadata.from} />}
        {msg.metadata?.steer && (
          <div className="ml-auto flex items-center gap-1 text-[11px] text-muted-foreground">
            <CornerDownRight className="size-3" />
            中途插话
          </div>
        )}
        <MessageContent className="whitespace-pre-wrap break-words text-[1em]">
          {files.length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {files.map((f: any, i) => (
                <img key={i} src={f.url} className="max-h-40 rounded-md border" />
              ))}
            </div>
          )}
          {text}
        </MessageContent>
        {canRewind && (
          <div className="ml-auto flex gap-0.5 opacity-0 transition-opacity group-hover/user:opacity-100 [@media(hover:none)]:opacity-100">
            <Tooltip>
              <TooltipTrigger asChild>
                <button className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => onEdit?.(msg)}>
                  <Pencil className="size-3.5" />
                </button>
              </TooltipTrigger>
              <TooltipContent>改这句重发（回到它之前）</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <button className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => onRewind?.(msg)}>
                  <RotateCcw className="size-3.5" />
                </button>
              </TooltipTrigger>
              <TooltipContent>回到这条消息之前</TooltipContent>
            </Tooltip>
          </div>
        )}
      </Message>
    );
  }

  // 子 agent 的工具调用挂在它的父调用下面。
  const children = new Map<string, ToolPart[]>();
  const toolIds = new Set(msg.parts.filter((p): p is ToolPart => p.type === "dynamic-tool").map((p) => p.toolCallId));
  for (const p of msg.parts) {
    if (p.type !== "dynamic-tool") continue;
    const parent = (p as ToolPart).toolMetadata?.parent;
    if (parent && toolIds.has(parent)) {
      const arr = children.get(parent) ?? [];
      arr.push(p as ToolPart);
      children.set(parent, arr);
    }
  }
  const lastIdx = msg.parts.length - 1;
  const nodes: ReactNode[] = [];
  msg.parts.forEach((p, i) => {
    const node = renderPart(p, i, {
      sessionId,
      streaming: live && running && i === lastIdx,
      running: live && running,
      children,
      onQuickReply,
      quickEnabled: live,
    });
    if (node) nodes.push(node);
  });
  const empty = nodes.length === 0;
  return (
    <Message from="assistant">
      <MessageContent className="w-full max-w-full text-[1em]">
        {nodes}
        {live && running && (empty || msg.parts[lastIdx]?.type === "dynamic-tool" || msg.parts[lastIdx]?.type?.startsWith("data-")) && (
          <Shimmer className="text-[13px]">{empty ? "思考中…" : "工作中…"}</Shimmer>
        )}
      </MessageContent>
    </Message>
  );
});

export type PartCtx = {
  sessionId: string;
  msgId?: string;
  streaming: boolean;
  running: boolean;
  children: Map<string, ToolPart[]>;
  onQuickReply: (s: string) => void;
  quickEnabled: boolean;
};

/** 思考过程：展开没展开记在组件外面（虚拟列表滚出去会卸载）。 */
function KeptReasoning({ keep, streaming, text }: { keep: string; streaming: boolean; text: string }) {
  const [open, setOpen] = useKept<boolean | undefined>(keep, undefined);
  return (
    <Reasoning isStreaming={streaming} defaultOpen={false} open={open} onOpenChange={setOpen} className="mb-0">
      <ReasoningTrigger />
      <ReasoningContent className="gerdr-md text-[0.92em]">{text}</ReasoningContent>
    </Reasoning>
  );
}

/** 这一块在不在主列表里单独成一行（子 agent 的工具调用挂在父调用里面画，不单独成行；空文字不画）。 */
export function partVisible(p: GPart, children: Map<string, ToolPart[]>): boolean {
  if ((p.type === "text" || p.type === "reasoning") && !p.text.trim()) return false;
  if (p.type === "dynamic-tool") {
    const tp = p as ToolPart;
    if (tp.toolMetadata?.parent && [...children.values()].some((arr) => arr.includes(tp))) return false;
  }
  return true;
}

/** 子 agent 的工具调用挂在它的父调用下面：父 toolCallId → 子调用。 */
export function childTools(msg: GMessage): Map<string, ToolPart[]> {
  const children = new Map<string, ToolPart[]>();
  const toolIds = new Set(msg.parts.filter((p): p is ToolPart => p.type === "dynamic-tool").map((p) => p.toolCallId));
  for (const p of msg.parts) {
    if (p.type !== "dynamic-tool") continue;
    const parent = (p as ToolPart).toolMetadata?.parent;
    if (parent && toolIds.has(parent)) {
      const arr = children.get(parent) ?? [];
      arr.push(p as ToolPart);
      children.set(parent, arr);
    }
  }
  return children;
}

export function renderPart(p: GPart, i: number, ctx: PartCtx): ReactNode {
  switch (p.type) {
    case "text":
      if (!p.text.trim()) return null;
      return (
        <div key={i} className="gerdr-md">
          <Markdown>{p.text}</Markdown>
        </div>
      );
    case "reasoning":
      if (!p.text.trim()) return null;
      return <KeptReasoning key={i} keep={`${ctx.sessionId}:r:${ctx.msgId}:${i}`} streaming={ctx.streaming} text={p.text} />;
    case "dynamic-tool": {
      const tp = p as ToolPart;
      if (tp.toolMetadata?.parent && ctx.children.size && [...ctx.children.values()].some((arr) => arr.includes(tp))) return null;
      const kids = ctx.children.get(tp.toolCallId);
      return (
        <ToolCall key={tp.toolCallId} part={tp} running={ctx.running}>
          {kids?.map((k) => (
            <ToolCall key={k.toolCallId} part={k} running={ctx.running} />
          ))}
        </ToolCall>
      );
    }
    case "data-plan":
      return <PlanCard key={i} entries={p.data.entries} />;
    case "data-permission":
      return <PermissionCard key={p.data.reqId} sessionId={ctx.sessionId} data={p.data} />;
    case "data-ask":
      return <AskCard key={p.data.reqId} sessionId={ctx.sessionId} data={p.data} />;
    case "data-quickReplies":
      return <QuickReplies key={i} data={p.data} onPick={ctx.onQuickReply} enabled={ctx.quickEnabled} />;
    case "data-turnStat":
      return (
        <div key={i} className="font-mono text-[12px] text-muted-foreground">
          ✻ 用时 {fmtElapsed(p.data.ms)}
          {p.data.tokens > 0 ? ` · ↓ ${fmtTokens(p.data.tokens)} tokens` : ""}
        </div>
      );
    case "data-task":
      return <TaskDoneCard key={i} sessionId={ctx.sessionId} t={p.data.task} />;
    case "data-notice":
      return (
        <div
          key={i}
          className={cn(
            "flex items-start gap-1.5 rounded-md px-2 py-1 text-[12px]",
            p.data.level === "error" ? "bg-bad/10 text-bad" : p.data.level === "warn" ? "bg-warn/10 text-warn" : "bg-muted/60 text-muted-foreground",
          )}
        >
          <Info className="mt-0.5 size-3.5 shrink-0" />
          {p.data.text}
        </div>
      );
    case "data-turnEnd":
      return (
        <div key={i} className={cn("flex items-start gap-1.5 rounded-md px-2 py-1 text-[12px]", p.data.error ? "bg-bad/10 text-bad" : "text-muted-foreground")}>
          <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
          <span className="whitespace-pre-wrap break-words">{p.data.error || STOP_TEXT[p.data.stopReason] || p.data.stopReason}</span>
        </div>
      );
    case "file":
      return <img key={i} src={(p as any).url} className="max-h-60 rounded-md border" />;
  }
  return null;
}
