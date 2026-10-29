import { Bot, Brain, ChevronDown, CircleAlert, Info, Loader2, CornerDownRight } from "lucide-react";
import { RewindButton } from "./RewindButton";
import { memo, type ReactNode } from "react";
import { Message, MessageContent } from "@/components/ai-elements/message";
import { Markdown } from "./Markdown";
import { Reasoning, ReasoningContent, ReasoningTrigger } from "@/components/ai-elements/reasoning";
import { Shimmer } from "@/components/ai-elements/shimmer";
import type { GMessage, GPart, ToolPart } from "@/lib/transcript";
import { cn } from "@/lib/utils";
import { useKept } from "@/lib/kept";
import { focusSession } from "@/lib/actions";
import { useStore } from "@/lib/store";
import { AskCard, PermissionCard, PlanCard, QuickReplies } from "./Cards";
import { ToolCall } from "./ToolCall";
import { UsageCard } from "./UsageCard";
import { openLightbox } from "../Lightbox";
import { TaskDoneCard } from "./TasksBar";
import { ShellCard } from "./ShellCard";
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
  steerRewind?: boolean;
  onWithdraw?: (messageId: string, now: boolean) => void;
  onQuickReply: (s: string) => void;
  stackPrev?: boolean; // 用户消息：上 / 下紧挨着也是用户消息（叠成一摞）
  stackNext?: boolean;
};

/** 这条消息是别的会话里的 agent 发来的（gerdr session send）：标出来源，点一下跳过去。 */
function FromLabel({ id }: { id: string }) {
  const s = useStore((st) => st.sessions[id]);
  const name = s ? s.title || s.cwd.split("/").pop() || s.agent : id.slice(0, 8);
  return (
    <button className="flex items-center gap-1 hover:text-foreground" onClick={() => focusSession(id)}>
      <Bot className="size-3" />
      来自会话「{name}」
    </button>
  );
}

/** 消息时间：今天的只写时分，别的日子带上月日。 */
function fmtTime(ts?: number) {
  if (!ts) return "";
  const d = new Date(ts);
  const hm = `${d.getHours()}:${String(d.getMinutes()).padStart(2, "0")}`;
  return d.toDateString() === new Date().toDateString() ? hm : `${d.getMonth() + 1}/${d.getDate()} ${hm}`;
}

export const MessageView = memo(function MessageView({
  msg,
  sessionId,
  live,
  running,
  onRewind,
  steerRewind,
  onWithdraw,
  onQuickReply,
  stackPrev,
  stackNext,
}: Props) {
  if (msg.role === "user") {
    const text = msg.parts.map((p) => (p.type === "text" ? p.text : "")).join("");
    const files = msg.parts.filter((p) => p.type === "file");
    const meta = msg.metadata;
    const canRewind = !!onRewind && !!meta?.messageId && (!meta?.steer || steerRewind) && !meta?.sending && !running;
    // 元信息（时间、来源）在气泡下面一行小灰字（照 Astryx ChatMessageMetadata）；一摞连发的只在最后一条下面写时间
    const showMeta = !stackNext || !!meta?.from || !!meta?.steer;
    return (
      // 发送中（服务端还没记下，见 Transcript.sending）：整条半透明，记下了换成正常的那条
      <Message from="user" className={cn("group/user gap-1 transition-opacity", meta?.sending && "opacity-50")}>
        {/* 回退按钮悬停时浮在气泡左边，不占高度 */}
        <div className="relative ml-auto flex min-w-0 max-w-[max(80%,280px)] justify-end">
          {canRewind && (
            // 气泡开着时按钮一直显示（鼠标挪去点气泡，hover 没了也别消失）
            <div className="absolute right-full bottom-1 mr-1 flex gap-0.5 opacity-0 transition-opacity group-hover/user:opacity-100 has-[[data-state=open]]:opacity-100 [@media(hover:none)]:opacity-100">
              <RewindButton onConfirm={() => onRewind?.(msg)} />
            </div>
          )}
          <MessageContent
            className={cn(
              "whitespace-pre-wrap break-words text-[1em] leading-[1.5]",
              stackPrev && "group-[.is-user]:rounded-tr-md",
              stackNext && "group-[.is-user]:rounded-br-md",
            )}
          >
            {files.length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {files.map((f: any, i) => (
                  // 高度定死在图片自己身上（宽度按比例自动算）：对话是虚拟列表，行高要在图片加载完之前就定下来。
                  // 只给 max-h 的话行先按 0 高量、解码完再长 160px，滚动跟着修正又把行卸载 / 重挂，来回抽搐。
                  // 别套一层定高的 div 再给 img max-w-full：外层宽度又取决于 img，循环依赖，图片宽度会被算成 0（踩过，图没了）
                  <img
                    key={i}
                    src={f.url}
                    onClick={(e) =>
                      openLightbox(
                        files.map((x: any) => ({ url: x.url })),
                        i,
                        e.currentTarget,
                      )
                    }
                    className="h-40 w-auto max-w-full cursor-zoom-in rounded-xl border object-contain"
                  />
                ))}
              </div>
            )}
            {text}
          </MessageContent>
        </div>
        {showMeta && (
          <div className="flex items-center justify-end gap-1.5 px-2 text-[12px] text-muted-foreground">
            {meta?.from && <FromLabel id={meta.from} />}
            {meta?.steer &&
              (meta.queued ? (
                // 排在 agent 的队列里、还没被读到（claude 要等手上这个工具调用结束）
                <span className="flex items-center gap-1">
                  <span className="flex items-center gap-1" title="agent 手上的这一步做完就会读到它；输入框里按 ↑ 也能收回来改">
                    <Loader2 className="size-3 animate-spin" />
                    排队中，等它读到…
                  </span>
                  {meta.messageId && onWithdraw && (
                    <>
                      <button
                        className="rounded-sm px-1.5 hover:bg-muted hover:text-foreground"
                        title="收回来放进输入框改（↑）"
                        onClick={() => onWithdraw(meta.messageId!, false)}
                      >
                        撤回
                      </button>
                      <button
                        className="rounded-sm px-1.5 hover:bg-muted hover:text-foreground"
                        title="不等了，打断它手上这一步马上读（Ctrl+↵ 发的就是这种）"
                        onClick={() => onWithdraw(meta.messageId!, true)}
                      >
                        立即发
                      </button>
                    </>
                  )}
                </span>
              ) : (
                <span className="flex items-center gap-1">
                  <CornerDownRight className="size-3" />
                  中途插话
                </span>
              ))}
            {(meta?.from || meta?.steer) && !stackNext && meta?.ts ? <span>·</span> : null}
            {!stackNext && (meta?.sending ? <span>发送中…</span> : <span className="tabular-nums">{fmtTime(meta?.ts)}</span>)}
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
  /** 展开状态记在这个 key 下（不按块自己记），见 buildRows 的 peek */
  keep?: string;
};

/** 思考过程：展开没展开记在组件外面（虚拟列表滚出去会卸载）。 */
function KeptReasoning({ keep, streaming, text }: { keep: string; streaming: boolean; text: string }) {
  const [kept, setOpen] = useKept<boolean | undefined>(keep, undefined);
  // 开着「显示思考」默认展开。不用 Reasoning 的 defaultOpen：它为 true 时流完 1 秒会自己收起来
  const show = useStore((s) => s.config.showThinking !== false);
  const open = kept ?? show;
  // 和工具调用那一行一个样子（ToolCall）：一行小灰字，不抢正文的眼
  return (
    <Reasoning isStreaming={streaming} defaultOpen={false} open={open} onOpenChange={setOpen} className="mb-0">
      <ReasoningTrigger className="-ml-1.5 w-auto gap-2 rounded-md px-1.5 py-[3px] text-[13px] hover:bg-muted/60">
        <Brain className="size-3.5 shrink-0" />
        {streaming ? <Shimmer duration={1}>思考中…</Shimmer> : <span>思考</span>}
        <ChevronDown className={cn("size-3.5 shrink-0 opacity-60 transition-transform", open && "rotate-180")} />
      </ReasoningTrigger>
      {/* 开着「显示思考」时字色和正文一样（用户要的）：思考默认摊开给人读，灰字读着费劲，和正文靠左边那条竖线、小一号的字分开。
          关着时照旧是灰字 */}
      <ReasoningContent className={cn("gerdr-md mt-1 mb-1 ml-[7px] border-l pl-4 text-[0.92em]", show && "text-foreground")}>{text}</ReasoningContent>
    </Reasoning>
  );
}

/** 这一块在不在主列表里单独成一行（子 agent 的工具调用挂在父调用里面画，不单独成行；空文字不画）。 */
export function partVisible(p: GPart, children: Map<string, ToolPart[]>): boolean {
  if ((p.type === "text" || p.type === "reasoning") && !p.text.trim()) return false;
  // 批准了的审批不单独成行：工具那一行的 ✓ 已经说明放行了，再多一行「✓ 允许」只会把一串工具调用切碎。
  // 拒绝 / 作废的留着（那是这一轮为什么拐弯的原因）。
  if (p.type === "data-permission" && permAllowed(p.data)) return false;
  return true;
  if (p.type === "dynamic-tool") {
    const tp = p as ToolPart;
    if (tp.toolMetadata?.parent && [...children.values()].some((arr) => arr.includes(tp))) return false;
  }
  return true;
}

/** 审批已经答了、而且选的是放行的那种。 */
export function permAllowed(d: { answer?: string; request: { options: { optionId: string; kind: string }[] } }): boolean {
  if (!d.answer) return false;
  return !!d.request.options.find((o) => o.optionId === d.answer)?.kind.startsWith("allow");
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

const USAGE = /^Current [^\n:]+:\s*\d+% used/m;

export function renderPart(p: GPart, i: number, ctx: PartCtx): ReactNode {
  switch (p.type) {
    case "text":
      if (!p.text.trim()) return null;
      // claude 的 /usage：终端排版的纯文本，当 Markdown 画会挤成一坨（UsageCard.tsx）
      if (USAGE.test(p.text)) return <UsageCard key={i} text={p.text} />;
      return (
        <div key={i} className="gerdr-md">
          <Markdown>{p.text}</Markdown>
        </div>
      );
    case "reasoning":
      if (!p.text.trim()) return null;
      return <KeptReasoning key={i} keep={ctx.keep || `${ctx.sessionId}:r:${ctx.msgId}:${i}`} streaming={ctx.streaming} text={p.text} />;
    case "dynamic-tool": {
      const tp = p as ToolPart;
      if (tp.toolMetadata?.parent && ctx.children.size && [...ctx.children.values()].some((arr) => arr.includes(tp))) return null;
      const kids = ctx.children.get(tp.toolCallId);
      return (
        <ToolCall key={tp.toolCallId} part={tp} running={ctx.running} keep={ctx.keep || undefined}>
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
        // 一轮的元信息：结束时间 · 用时 · 输出 token（照 Astryx ChatMessageMetadata 的「9:46 AM · Claude Opus」）
        <div key={i} className="text-[12px] text-muted-foreground tabular-nums">
          {[fmtTime(p.data.ts), `用时 ${fmtElapsed(p.data.ms)}`, p.data.tokens > 0 && `${fmtTokens(p.data.tokens)} tokens`].filter(Boolean).join(" · ")}
        </div>
      );
    case "data-task":
      return <TaskDoneCard key={i} sessionId={ctx.sessionId} t={p.data.task} woke={p.data.woke} command={p.data.command} />;
    case "data-shell":
      return <ShellCard key={p.data.id} sessionId={ctx.sessionId} data={p.data} />;
    case "data-notice":
      if (p.data.done) return null; // 重试成功后收起的「N 秒后重试」（transcript.clearRetry）
      if (p.data.level === "clear")
        return (
          <div key={i} className="flex items-center gap-3 py-2 text-[12px] text-muted-foreground">
            <div className="h-px flex-1 bg-border" />
            {p.data.text}（/clear）
            <div className="h-px flex-1 bg-border" />
          </div>
        );
      if (p.data.level === "auto")
        // agent 自己开的一轮：只是交代这段话怎么来的，左对齐的淡字，和 turnStat 一个分量，不用居中 + 图标（那是错误 / 警告的样子）
        return (
          <div key={i} className="text-[12px] text-muted-foreground">
            {p.data.text}
          </div>
        );
      return (
        // System notice: a small centered line that reads as nobody's words (after Astryx ChatSystemMessage).
        // Only the block is centered; the text itself stays left-aligned, otherwise a wrapped notice centers
        // each line and the icon ends up floating beside an indented first line.
        <div
          key={i}
          className={cn(
            "flex items-start justify-center gap-1.5 px-6 py-1 text-[12px]",
            p.data.level === "error" ? "text-bad" : p.data.level === "warn" || p.data.level === "retry" ? "text-warn" : "text-muted-foreground",
          )}
        >
          <Info className="mt-0.5 size-3.5 shrink-0" />
          <span className="min-w-0">{p.data.text}</span>
        </div>
      );
    case "data-turnEnd":
      return (
        <div
          key={i}
          className={cn("flex items-start justify-center gap-1.5 px-6 py-1 text-center text-[12px]", p.data.error ? "text-bad" : "text-muted-foreground")}
        >
          <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
          <span className="min-w-0 whitespace-pre-wrap break-words">{p.data.error || STOP_TEXT[p.data.stopReason] || p.data.stopReason}</span>
        </div>
      );
    case "file":
      // 同用户消息里的图：高度定死在 img 上，行高不随图片加载变（虚拟列表会抽搐）
      return (
        <img
          key={i}
          src={(p as any).url}
          onClick={(e) => openLightbox([{ url: (p as any).url }], 0, e.currentTarget)}
          className="h-60 w-auto max-w-full cursor-zoom-in self-start rounded-md border object-contain"
        />
      );
  }
  return null;
}
