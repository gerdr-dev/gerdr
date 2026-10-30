import { Bot, Brain, ChevronDown, CircleAlert, Info, Loader2, CornerDownRight } from "lucide-react";
import { RewindButton } from "./RewindButton";
import { memo, useEffect, useState, type ReactNode } from "react";
import { Message, MessageContent } from "../ai/message";
import { Markdown } from "./Markdown";
import { Reasoning, ReasoningContent, ReasoningTrigger } from "../ai/reasoning";
import { Shimmer } from "../ai/shimmer";
import type { GMessage, GPart, ToolPart } from "../transcript";
import { cn } from "../utils";
import { useKept } from "../kept";
import { useChatHost, useChatSettings } from "../host";
import { t, type MessageKey } from "../i18n";
import { AskCard, PermissionCard, PlanCard, QuickReplies } from "./Cards";
import { ToolCall } from "./ToolCall";
import { UsageCard } from "./UsageCard";
import { openLightbox } from "./Lightbox";
import { PlayBadge } from "./PlayBadge";
import { TaskDoneCard } from "./TasksBar";
import { ShellCard } from "./ShellCard";
import { fmtElapsed, fmtTokens } from "./StatusLine";

const STOP_TEXT: Record<string, MessageKey> = {
  cancelled: "tool.interrupted",
  max_tokens: "stop.maxTokens",
  max_turn_requests: "stop.maxTurnRequests",
  refusal: "stop.refusal",
  error: "stop.error",
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
  const host = useChatHost();
  const name = host.useSessionTitle?.(id) ?? id.slice(0, 8);
  return (
    // A long session title truncates instead of wrapping the whole metadata row (and squeezing the icon).
    <button className="flex min-w-0 items-center gap-1 hover:text-foreground" title={name} onClick={() => host.focusSession?.(id)}>
      <Bot className="size-3 shrink-0" />
      <span className="truncate">{t("msg.fromSession", { name })}</span>
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
                {files.map((f: any, i) => {
                  // Images and videos page together in one lightbox; a video only plays once opened there.
                  const open = (e: React.MouseEvent<HTMLElement>) =>
                    openLightbox(
                      files.map((x: any) => ({ url: x.url, video: x.mediaType?.startsWith("video/") })),
                      i,
                      e.currentTarget,
                    );
                  return f.mediaType?.startsWith("video/") ? (
                    // Fixed height on the <video> itself, same reason as the images below. The wrapper takes max-w-full
                    // instead of the video, so its width doesn't depend on itself.
                    <div key={i} onClick={open} className="relative max-w-full cursor-zoom-in overflow-hidden rounded-xl border bg-muted">
                      <video src={`${f.url}#t=0.1`} muted playsInline preload="metadata" className="pointer-events-none h-40 w-auto" />
                      <PlayBadge />
                    </div>
                  ) : (
                    // 高度定死在图片自己身上（宽度按比例自动算）：对话是虚拟列表，行高要在图片加载完之前就定下来。
                    // 只给 max-h 的话行先按 0 高量、解码完再长 160px，滚动跟着修正又把行卸载 / 重挂，来回抽搐。
                    // 别套一层定高的 div 再给 img max-w-full：外层宽度又取决于 img，循环依赖，图片宽度会被算成 0（踩过，图没了）
                    <img key={i} src={f.url} onClick={open} className="h-40 w-auto max-w-full cursor-zoom-in rounded-xl border object-contain" />
                  );
                })}
              </div>
            )}
            {text}
          </MessageContent>
        </div>
        {showMeta && (
          <div className="flex min-w-0 items-center justify-end gap-1.5 px-2 text-[12px] whitespace-nowrap text-muted-foreground [&>*:not(button)]:shrink-0">
            {meta?.from && <FromLabel id={meta.from} />}
            {meta?.steer &&
              (meta.queued ? (
                // 排在 agent 的队列里、还没被读到（claude 要等手上这个工具调用结束）
                <span className="flex items-center gap-1">
                  <span className="flex items-center gap-1" title={t("msg.queuedTitle")}>
                    <Loader2 className="size-3 shrink-0 animate-spin" />
                    {t("msg.queued")}
                  </span>
                  {meta.messageId && onWithdraw && (
                    <>
                      <button
                        className="rounded-sm px-1.5 hover:bg-muted hover:text-foreground"
                        title={t("msg.withdrawTitle")}
                        onClick={() => onWithdraw(meta.messageId!, false)}
                      >
                        {t("msg.withdraw")}
                      </button>
                      <button
                        className="rounded-sm px-1.5 hover:bg-muted hover:text-foreground"
                        title={t("msg.sendNowTitle")}
                        onClick={() => onWithdraw(meta.messageId!, true)}
                      >
                        {t("msg.sendNow")}
                      </button>
                    </>
                  )}
                </span>
              ) : (
                <span className="flex items-center gap-1">
                  <CornerDownRight className="size-3 shrink-0" />
                  {t("msg.steered")}
                </span>
              ))}
            {(meta?.from || meta?.steer) && !stackNext && meta?.ts ? <span>·</span> : null}
            {!stackNext && (meta?.sending ? <span>{t("msg.sending")}</span> : <span className="tabular-nums">{fmtTime(meta?.ts)}</span>)}
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
          <Shimmer className="text-[13px]">{empty ? t("msg.thinking") : t("msg.working")}</Shimmer>
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
  const show = useChatSettings().showThinking !== false;
  const open = kept ?? show;
  // 和工具调用那一行一个样子（ToolCall）：一行小灰字，不抢正文的眼
  return (
    <Reasoning isStreaming={streaming} defaultOpen={false} open={open} onOpenChange={setOpen} className="mb-0">
      <ReasoningTrigger className="-ml-1.5 w-auto gap-2 rounded-md px-1.5 py-[3px] text-[13px] hover:bg-muted/60">
        <Brain className="size-3.5 shrink-0" />
        {streaming ? <Shimmer duration={1}>{t("msg.thinking")}</Shimmer> : <span>{t("msg.thought")}</span>}
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
      // A subagent's own tool calls aren't drawn under its card (they buried the main conversation); "查看对话" opens them.
      return <ToolCall key={tp.toolCallId} part={tp} running={ctx.running} keep={ctx.keep || undefined} agent={ctx.children.has(tp.toolCallId)} />;
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
        // h-5 like the running turn's StatusLine it replaces, so the swap doesn't jump
        <div key={i} className="flex h-5 items-center text-[12px] text-muted-foreground tabular-nums">
          {[fmtTime(p.data.ts), t("msg.took", { time: fmtElapsed(p.data.ms) }), p.data.tokens > 0 && `${fmtTokens(p.data.tokens)} tokens`]
            .filter(Boolean)
            .join(" · ")}
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
          <span className="min-w-0">{p.data.level === "retry" && p.data.until ? <RetryText text={p.data.text} until={p.data.until} /> : p.data.text}</span>
        </div>
      );
    case "data-turnEnd":
      return (
        <div
          key={i}
          className={cn("flex items-start justify-center gap-1.5 px-6 py-1 text-center text-[12px]", p.data.error ? "text-bad" : "text-muted-foreground")}
        >
          <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
          <span className="min-w-0 whitespace-pre-wrap break-words">
            {p.data.error || (STOP_TEXT[p.data.stopReason] ? t(STOP_TEXT[p.data.stopReason]) : p.data.stopReason)}
          </span>
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

/**
 * The retry notice's "N 秒后自动重试", counted down live: the server writes the wait once, so it used to sit at "10 秒"
 * while the seconds went by. Past the deadline the retry is under way (the notice is folded away once it succeeds or the turn ends).
 */
function RetryText({ text, until }: { text: string; until: number }) {
  const [now, setNow] = useState(Date.now);
  const left = Math.ceil((until - now) / 1000);
  useEffect(() => {
    if (left <= 0) return;
    // wake right as the shown second changes (9.3s left shows 10; 300ms later it is 9)
    const t = setTimeout(() => setNow(Date.now()), ((until - Date.now()) % 1000 || 1000) + 5);
    return () => clearTimeout(t);
  }, [now, until, left]);
  return <>{text.replace(/\d+ 秒后自动重试/, left > 0 ? `${left} 秒后自动重试` : "正在自动重试" /* the server's (Chinese) text, counted down in place */)}</>;
}
