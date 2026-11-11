import { Bot, Brain, ChevronDown, CircleAlert, File as FileIcon, FoldVertical, Info, Loader2, CornerDownRight } from "lucide-react";
import { RewindButton } from "./RewindButton";
import { memo, useEffect, useState, type ReactNode } from "react";
import { Message, MessageContent } from "../ai/message";
import { Markdown } from "./Markdown";
import { Reasoning, ReasoningContent, ReasoningTrigger } from "../ai/reasoning";
import { Shimmer } from "../ai/shimmer";
import { LOCAL_FILE, type GMessage, type GPart, type ToolPart } from "../transcript";
import { cn } from "../utils";
import { useKept } from "../kept";
import { useChatHost, useChatSettings } from "../host";
import { t, type MessageKey } from "../i18n";
import { AskCard, PermissionCard, PlanCard, QuickReplies } from "./Cards";
import { ToolCall } from "./ToolCall";
import { UsageCard } from "./UsageCard";
import { imageSize, rememberImageSize } from "./imageSize";
import { openLightbox } from "./Lightbox";
import { PlayBadge } from "./PlayBadge";
import { VideoThumb } from "./VideoThumb";
import { TaskDoneCard, WakeRow } from "./TasksBar";
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
  onWithdraw?: (messageId: string, now: boolean) => Promise<void> | void;
  /** Get every queued interjection read now: moves the running command aside (ChatView.sendQueuedNow) */
  onSendQueuedNow?: () => Promise<void> | void;
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

/** A file the user attached by its local path (not uploaded): name and path; a click opens it with the system. */
function LocalFileChip({ path, name }: { path: string; name: string }) {
  const host = useChatHost();
  return (
    <div
      title={path}
      onClick={() => host.openNative?.(path, undefined, false)}
      className={cn("flex max-w-64 items-center gap-2 rounded-lg bg-white/15 px-2.5 py-1.5", host.openNative && "cursor-pointer hover:bg-white/25")}
    >
      <FileIcon className="size-4 shrink-0 opacity-80" />
      <div className="min-w-0 text-left leading-tight">
        <div className="truncate text-[13px]">{name}</div>
        <div className="truncate text-[11px] opacity-70">{path.replace(/^\/Users\/[^/]+/, "~")}</div>
      </div>
    </div>
  );
}

/** A queued interjection's status and its Withdraw / Send now buttons. */
function QueuedSteer({
  messageId,
  now,
  held,
  onWithdraw,
  onSendQueuedNow,
}: {
  messageId?: string;
  now: boolean;
  held: boolean;
  onWithdraw?: Props["onWithdraw"];
  onSendQueuedNow?: Props["onSendQueuedNow"];
}) {
  // One request at a time: the round trip can take a moment on a long session, the buttons stayed live, and a second
  // click found the message already taken back and reported "it has already read this" (user report).
  const [busy, setBusy] = useState(false);
  const act = async (f: () => Promise<void> | void) => {
    if (busy) return;
    setBusy(true);
    try {
      await f();
    } finally {
      setBusy(false);
    }
  };
  return (
    <span className={cn("flex items-center gap-1", busy && "opacity-50")}>
      <span className="mr-2 flex items-center gap-1" title={t(held ? "msg.heldTitle" : now ? "msg.cuttingInTitle" : "msg.queuedTitle")}>
        <Loader2 className="size-3 shrink-0 animate-spin" />
        {t(now ? "msg.cuttingIn" : "msg.queued")}
      </span>
      {messageId && onWithdraw && (
        <button
          className="rounded-sm px-1.5 hover:bg-muted hover:text-foreground disabled:pointer-events-none"
          disabled={busy}
          title={t("msg.withdrawTitle")}
          onClick={() => act(() => onWithdraw(messageId, false))}
        >
          {t("msg.withdraw")}
        </button>
      )}
      {/* Send now moves the running command aside (like Claude Code's own "now"), so every queued message is read at
          the gap that makes, in the order sent. It used to withdraw this one and resend it as urgent: that put it ahead
          of (claude) or behind (native) the others queued, and withdrawing a queued urgent steer trips a Claude Code bug.
          Not on an urgent steer (it is already cutting in) or a held slash command (it waits for the turn's end). */}
      {onSendQueuedNow && !now && !held && (
        <button
          className="rounded-sm px-1.5 hover:bg-muted hover:text-foreground disabled:pointer-events-none"
          disabled={busy}
          title={t("msg.sendNowTitle")}
          onClick={() => act(onSendQueuedNow)}
        >
          {t("msg.sendNow")}
        </button>
      )}
    </span>
  );
}

export const MessageView = memo(function MessageView({
  msg,
  sessionId,
  live,
  running,
  onRewind,
  steerRewind,
  onWithdraw,
  onSendQueuedNow,
  onQuickReply,
  stackPrev,
  stackNext,
}: Props) {
  const host = useChatHost();
  if (msg.role === "user") {
    const text = msg.parts.map((p) => (p.type === "text" ? p.text : "")).join("");
    // The host's buttons (ChatHost.messageActions) sit with rewind, floating left of the bubble
    // (only for a message that says something: an image alone has nothing to act on as text)
    const actions = text.trim()
      ? host.messageActions?.({ sessionId, role: "user", id: msg.metadata?.messageId ?? msg.id, text: () => text, blocks: () => [text] })
      : null;
    const all = msg.parts.filter((p) => p.type === "file");
    // Images and videos as thumbnails (one lightbox pages through them); files sent by their local path as chips
    const files = all.filter((p: any) => p.mediaType !== LOCAL_FILE);
    const locals = all.filter((p: any) => p.mediaType === LOCAL_FILE);
    const meta = msg.metadata;
    const canRewind = !!onRewind && !!meta?.messageId && (!meta?.steer || steerRewind) && !meta?.sending && !running;
    // 元信息（时间、来源）在气泡下面一行小灰字（照 Astryx ChatMessageMetadata）；一摞连发的只在最后一条下面写时间
    const showMeta = !stackNext || !!meta?.from || !!meta?.steer;
    return (
      // Sending (the server hasn't logged it yet, see Transcript.sending): half transparent. The logged copy keeps the row's
      // key (messageId), so the same element fades in; slow enough to be seen (asked for: the default 150ms read as a jump)
      <Message from="user" className={cn("group/user gap-1 transition-opacity duration-500 ease-out", meta?.sending && "opacity-50")}>
        {/* 回退按钮悬停时浮在气泡左边，不占高度 */}
        <div className="relative ml-auto flex min-w-0 max-w-[max(80%,280px)] justify-end">
          {(canRewind || actions != null) && (
            // Shown while the rewind popover is open too (the mouse moving to it leaves the hover)
            <div className="absolute right-full bottom-1 mr-1 flex gap-0.5 opacity-0 transition-opacity group-hover/user:opacity-100 has-[[data-state=open]]:opacity-100 [@media(hover:none)]:opacity-100">
              {actions}
              {canRewind && <RewindButton onConfirm={() => onRewind?.(msg)} />}
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
                    // Fixed height on the thumbnail itself, same reason as the images below. The wrapper takes max-w-full
                    // instead of the thumbnail, so its width doesn't depend on itself.
                    <div key={i} onClick={open} className="relative max-w-full cursor-zoom-in overflow-hidden rounded-xl border bg-muted">
                      <VideoThumb url={f.url} />
                      <PlayBadge />
                    </div>
                  ) : (
                    <SizedImg key={i} src={f.url} onClick={open} heightRem={10} className="rounded-xl" />
                  );
                })}
              </div>
            )}
            {locals.length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {locals.map((f: any, i) => (
                  <LocalFileChip key={i} path={f.url} name={f.filename} />
                ))}
              </div>
            )}
            <span data-message={msg.metadata?.messageId ?? msg.id}>{text}</span>
          </MessageContent>
        </div>
        {showMeta && (
          <div className="flex min-w-0 items-center justify-end gap-1.5 px-2 text-[12px] whitespace-nowrap text-muted-foreground [&>*:not(button)]:shrink-0">
            {meta?.from && <FromLabel id={meta.from} />}
            {meta?.steer &&
              (meta.queued ? (
                // 排在 agent 的队列里、还没被读到（claude 要等手上这个工具调用结束）
                <QueuedSteer messageId={meta.messageId} now={!!meta.now} held={!!meta.held} onWithdraw={onWithdraw} onSendQueuedNow={onSendQueuedNow} />
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
  /** This part is the latest finished turn's metadata line (buildRows) */
  latest?: boolean;
  /** On a turn's metadata line: the turn's stretches of text, for ChatHost.messageActions (buildRows) */
  turnBlocks?: () => string[];
  /** The finished turn this part belongs to, as MessageInfo.id (buildRows) */
  turnId?: string;
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
  // A retry notice folded once the retry got through (transcript.clearRetry) draws nothing, but a row still takes the
  // row's spacing: it left an empty gap between two tool calls
  if (p.type === "data-notice" && (p.data as any).done) return false;
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

// The toolCallId prefix of a context compaction's card (acpx.CompactIDPrefix on the Go side).
const COMPACT_PREFIX = "compact-";

// A context compaction, drawn as a centered notice like the turn-end line ("已中断") rather than a gray tool row: from
// here on the model no longer sees the earlier conversation, only a summary of it, and that should not slip by
// unnoticed. When the agent sent the summary along (the native agent does), the line opens it.
function CompactNotice({ part, running, keep }: { part: ToolPart; running: boolean; keep: string }) {
  const [open, setOpen] = useKept(keep, false);
  const m = part.toolMetadata;
  const busy = running && (part.state === "input-streaming" || part.state === "input-available");
  const failed = part.state === "output-error";
  const summary = (m?.content ?? [])
    .map((c: any) => (c.type === "content" && c.content?.type === "text" ? c.content.text : ""))
    .join("\n")
    .trim();
  const took = m?.startedAt && m.endedAt && m.endedAt - m.startedAt >= 1000 ? fmtElapsed(m.endedAt - m.startedAt) : "";
  const stopped = failed && (part as any).errorText === t("tool.interrupted");
  const label = busy ? t("compact.running") : stopped ? t("compact.interrupted") : failed ? t("compact.failed") : t("compact.done");
  // How full the context was before it: a share of the window when the agent knew it, else the token count (Claude's
  // own transcript doesn't record the window). Exact figures on hover.
  const c = m?.compact;
  const pre = c?.preTokens;
  const before = pre ? t("compact.before", { size: c?.contextWindow ? `${Math.min(100, Math.round((pre / c.contextWindow) * 100))}%` : fmtTokens(pre) }) : "";
  const sizes = pre ? t("compact.sizes", { pre: pre.toLocaleString(), post: c?.postTokens ? c.postTokens.toLocaleString() : "?" }) : undefined;
  const Icon = busy ? Loader2 : failed ? CircleAlert : FoldVertical;
  const line = (
    <>
      <Icon className={cn("mt-0.5 size-3.5 shrink-0", busy && "animate-spin")} />
      <span className="min-w-0">
        {label}
        {before && !busy ? ` · ${before}` : ""}
        {took && !busy ? ` · ${took}` : ""}
      </span>
      {summary && <ChevronDown className={cn("mt-0.5 size-3.5 shrink-0 opacity-60 transition-transform", open && "rotate-180")} />}
    </>
  );
  return (
    <div className="flex flex-col items-center py-1">
      {summary ? (
        <button
          type="button"
          onClick={() => setOpen(!open)}
          title={sizes ? `${t("compact.showSummary")}\n${sizes}` : t("compact.showSummary")}
          className="flex items-start gap-1.5 rounded-md px-2 py-0.5 text-[12px] text-muted-foreground hover:bg-muted/60"
        >
          {line}
        </button>
      ) : (
        <div title={sizes} className={cn("flex items-start gap-1.5 px-2 py-0.5 text-[12px]", failed && !stopped ? "text-bad" : "text-muted-foreground")}>
          {line}
        </div>
      )}
      {summary && open && (
        <div className="gerdr-md mt-1 w-full rounded-md border bg-muted/30 px-4 py-2 text-[0.92em] text-muted-foreground">
          <Markdown>{summary}</Markdown>
        </div>
      )}
    </div>
  );
}

// A turn's metadata line: end time · took · output tokens (after Astryx ChatMessageMetadata's "9:46 AM · Claude Opus").
// h-5 like the running turn's StatusLine it replaces, so the swap doesn't jump. The host may restyle the time and add
// something after it (ChatHost.turnStat: gerdr hands that to add-ons, e.g. coloring by prompt-cache age).
function TurnStat({
  sessionId,
  id,
  data,
  latest,
  blocks,
}: {
  sessionId: string;
  id: string;
  data: { ms: number; tokens: number; ts?: number };
  latest: boolean;
  blocks?: () => string[];
}) {
  const host = useChatHost();
  const deco = host.useTurnStat?.({ sessionId, ts: data.ts, ms: data.ms, tokens: data.tokens, latest });
  // The host's buttons on the reply (ChatHost.messageActions), at the end of its line: a turn that said nothing (only ran
  // tools) gets none
  const actions = blocks && host.messageActions ? host.messageActions({ sessionId, role: "assistant", id, text: () => blocks().join("\n\n"), blocks }) : null;
  const rest = [t("msg.took", { time: fmtElapsed(data.ms) }), data.tokens > 0 && `${fmtTokens(data.tokens)} tokens`].filter(Boolean);
  return (
    <div className="flex h-5 items-center gap-1 text-[12px] text-muted-foreground tabular-nums">
      {data.ts ? (
        <span className={deco?.timeClassName} title={deco?.title}>
          {fmtTime(data.ts)}
        </span>
      ) : null}
      {deco?.after}
      <span>{(data.ts ? ["", ...rest] : rest).join(" · ")}</span>
      {actions != null && <span className="ml-1 flex items-center gap-0.5">{actions}</span>}
    </div>
  );
}

export function renderPart(p: GPart, i: number, ctx: PartCtx): ReactNode {
  switch (p.type) {
    case "text":
      if (!p.text.trim()) return null;
      // claude 的 /usage：终端排版的纯文本，当 Markdown 画会挤成一坨（UsageCard.tsx）
      if (USAGE.test(p.text)) return <UsageCard key={i} text={p.text} />;
      return (
        // data-message: the host finds a message's text on screen by it (MessageInfo)
        <div key={i} className="gerdr-md" data-message={ctx.turnId}>
          <Markdown>{p.text}</Markdown>
        </div>
      );
    case "reasoning":
      if (!p.text.trim()) return null;
      return <KeptReasoning key={i} keep={ctx.keep || `${ctx.sessionId}:r:${ctx.msgId}:${i}`} streaming={ctx.streaming} text={p.text} />;
    case "dynamic-tool": {
      const tp = p as ToolPart;
      if (tp.toolMetadata?.parent && ctx.children.size && [...ctx.children.values()].some((arr) => arr.includes(tp))) return null;
      if (tp.toolCallId.startsWith(COMPACT_PREFIX))
        return <CompactNotice key={tp.toolCallId} part={tp} running={ctx.running} keep={ctx.keep || `${ctx.sessionId}:compact:${tp.toolCallId}`} />;
      // A subagent's own tool calls aren't drawn under its card (they buried the main conversation); "查看对话" opens them.
      return (
        <ToolCall
          key={tp.toolCallId}
          part={tp}
          running={ctx.running}
          keep={ctx.keep || undefined}
          agent={ctx.children.has(tp.toolCallId) || !!tp.toolMetadata?.hasSub}
        />
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
      return <TurnStat key={i} sessionId={ctx.sessionId} id={`${ctx.msgId}:${i}`} data={p.data} latest={!!ctx.latest} blocks={ctx.turnBlocks} />;
    case "data-task":
      return <TaskDoneCard key={i} sessionId={ctx.sessionId} t={p.data.task} command={p.data.command} />;
    case "data-wake":
      return <WakeRow key={i} w={p.data.wake} keep={ctx.keep || `${ctx.sessionId}:wake:${ctx.msgId}:${i}`} />;
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
      if (p.data.level === "handoff")
        // Another agent took the conversation over here (hub/handoff.go): a divider like /clear's, above it is the old agent's
        return (
          <div key={i} className="flex items-center gap-3 py-2 text-[12px] text-muted-foreground">
            <div className="h-px flex-1 bg-border" />
            <span className="min-w-0 text-center">{p.data.text}</span>
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
        // Like a notice: the block is centered, the text left-aligned. Centered text put each wrapped line in the
        // middle on its own, and the icon floated far left of a short first line (a multi-line sign-in error).
        <div key={i} className={cn("flex items-start justify-center gap-1.5 px-6 py-1 text-[12px]", p.data.error ? "text-bad" : "text-muted-foreground")}>
          <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
          <span className="min-w-0 whitespace-pre-wrap break-words">
            {p.data.error || (STOP_TEXT[p.data.stopReason] ? t(STOP_TEXT[p.data.stopReason]) : p.data.stopReason)}
          </span>
        </div>
      );
    case "file":
      return (
        <SizedImg
          key={i}
          src={(p as any).url}
          onClick={(e) => openLightbox([{ url: (p as any).url }], 0, e.currentTarget)}
          heightRem={15}
          className="self-start rounded-md"
        />
      );
  }
  return null;
}

/**
 * An attached image whose box height is known before it loads: the chat is a virtual list, and a row that grows
 * from 0 to its image's height once decoded made scrolling correct itself, unmount / remount the row, and twitch.
 * Unseen images reserve a fixed `heightRem` box; once the ratio is known (imageSize.ts, kept for remounts) the box follows it,
 * so an image wider than the column shrinks to its real height instead of floating in an empty, bordered 160px box.
 * Width is an explicit length capped by max-w-full, never a percentage: a fixed-height wrapper with a max-w-full img
 * made the wrapper's width depend on the img, and the image came out 0 wide.
 */
function SizedImg({
  src,
  heightRem,
  className,
  onClick,
}: {
  src: string;
  heightRem: number;
  className?: string;
  onClick: (e: React.MouseEvent<HTMLElement>) => void;
}) {
  const [ratio, setRatio] = useState(() => {
    const s = imageSize(src);
    return s && s.w / s.h;
  });
  return (
    <img
      src={src}
      onClick={onClick}
      onLoad={(e) => {
        const s = rememberImageSize(src, e.currentTarget);
        if (s && (ratio === undefined || Math.abs(s.w / s.h - ratio) > 0.01)) setRatio(s.w / s.h);
      }}
      // content-box once the ratio is known: under the default border-box, aspect-ratio sizes the box including its
      // border, so a short image's content area came out a few % wider than the image and object-contain left
      // empty strips at both sides. max-width then has to leave room for the 1px border on each side.
      style={
        ratio
          ? { aspectRatio: ratio, width: `${(heightRem * ratio).toFixed(3)}rem`, boxSizing: "content-box", maxWidth: "calc(100% - 2px)" }
          : { height: `${heightRem}rem` }
      }
      className={cn("cursor-zoom-in border object-contain", ratio ? "h-auto" : "w-auto max-w-full", className)}
    />
  );
}

/**
 * The retry notice's "N 秒后自动重试" / "retrying in Ns", counted down live: the server writes the wait once, so it used to sit at "10 秒"
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
  // the server's text is in the computer's language (Go internal/i18n): "N 秒后自动重试" or "retrying in Ns"
  return (
    <>
      {text
        .replace(/\d+ 秒后自动重试/, left > 0 ? `${left} 秒后自动重试` : "正在自动重试")
        .replace(/retrying in \d+s/, left > 0 ? `retrying in ${left}s` : "retrying now")}
    </>
  );
}
