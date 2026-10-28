// 把服务端的会话日志（HubEvent[]）归并成 AI SDK 的 UIMessage[]，界面拿它用 AI Elements 画。
//
// 增量的：apply() 一条条吃事件，只换掉被改到的那条消息（新对象），没动的消息保持同一个引用
// —— 流式输出时只有最后一条消息在重渲染。reset 时整份重来。
import type { DynamicToolUIPart, UIMessage } from "ai";
import type { AgentTask, AskRequest, ContentBlock, HubEvent, PermissionRequest } from "./types";

export type PlanEntry = { content: string; status: "pending" | "in_progress" | "completed"; priority?: string };

export type GMeta = {
  messageId?: string;
  steer?: boolean;
  /** 插话还在 agent 的队列里排着、没被读进这一轮（claude 要等手上这个工具调用结束才读） */
  queued?: boolean;
  from?: string; // 别的会话发来的：发送方会话 id
  ts: number;
  content?: ContentBlock[];
};

export type GData = {
  plan: { entries: PlanEntry[] };
  permission: { reqId: string; request: PermissionRequest; answer?: string; cancelled?: boolean };
  ask: { reqId: string; request: AskRequest; answers?: Record<string, string[]>; cancelled?: boolean; reason?: "skipped" | "replied" };
  /** level：info / warn / error / clear / auto / retry（「N 秒后重试」，done = 重试成功后收起了） */
  notice: { level: string; text: string; done?: boolean };
  task: { task: AgentTask };
  turnStat: { ms: number; tokens: number; ts?: number };
  turnEnd: { stopReason: string; error?: string };
  quickReplies: { questions: { question: string; options: string[] }[] };
};

export type GMessage = UIMessage<GMeta, GData>;
export type GPart = GMessage["parts"][number];

export type ToolContent =
  | { type: "content"; content: { type: string; text?: string; data?: string; mimeType?: string }; _meta?: any }
  | { type: "diff"; path: string; oldText?: string | null; newText: string }
  | { type: "terminal"; terminalId: string };

export type ToolMeta = {
  kind: string;
  locations?: { path: string; line?: number }[];
  content?: ToolContent[];
  rawOutput?: unknown;
  parent?: string;
  status: string;
  /** 开始 / 结束的时间（unix ms，日志事件的 ts）：工具行尾巴上的耗时 */
  startedAt?: number;
  endedAt?: number;
  /** 被 auto 模式的分类器拦了：加上这些权限规则就能放行（claude 适配器给的，界面画「允许并重试」） */
  autoDenied?: string[];
};

export type ToolPart = DynamicToolUIPart & { toolMetadata?: ToolMeta & Record<string, any> };

type Loc = { msg: number; part: number };

/*
 * agent 注入的那几种「块」：以 user 角色记着，但不是人说的话，原样显示就是一大坨标签占一个气泡
 * （最常见的是后台任务跑完的 `<task-notification><task-id>…</task-id>…`，人要看的只有「哪个任务跑完了」）。
 * 规则照 herdr-web 的 internal/transcript/claude.go（machineBlock）：那边扫过全机的转录，这类就这几种。
 * 判据是标签名白名单，不能写成「以 < 开头就算」—— 人话里真的有 `<https://…>` 这种写法。
 */
const NOTE_WRAP = /^\s*<task-notification>([\s\S]*?)<\/task-notification>/;
const CMD_OUT = /^\s*<local-command-stdout>([\s\S]*?)<\/local-command-stdout>/;
const BASH_IN = /^\s*<bash-input>([\s\S]*?)<\/bash-input>/;
const BASH_OUT = /^\s*<bash-(?:stdout|stderr)>/;
const ANY_TAG = /<\/?[a-zA-Z][\w-]*(?:\s+[^>]*)?>/g;
// claude 写进去的打断记号（`[Request interrupted by user]` / `…for tool use]`），也不是人打的
const INTERRUPT = /^\s*\[Request interrupted by user[^\]]*\]\s*$/;

/** 多行压成一行、剥掉 ANSI（这些都只占一行小字，长了也读不了）；太长截断。 */
function squash(s: string, max = 300) {
  const t = s.replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, "").split(/\s+/).filter(Boolean).join(" ");
  return t.length > max ? t.slice(0, max) + "…" : t;
}

/**
 * 认出注入的块：notice = 画成一行系统小字（level 决定颜色），text = 还是人的气泡、只换个写法。
 * 不是这几种就返回 undefined，照常当人话。
 */
export function machineBlock(text: string): { notice?: { level: string; text: string }; text?: string } | undefined {
  let m = NOTE_WRAP.exec(text);
  if (m) {
    const body = m[1];
    // 形状变了也别退回显示标签：没有 summary 就把标签抹掉当散文读
    const gist = /<summary>([\s\S]*?)<\/summary>/.exec(body)?.[1].trim() || body.replace(ANY_TAG, " ");
    const status = /<status>([^<]*)<\/status>/.exec(body)?.[1].trim();
    const ok = !status || status === "completed";
    return { notice: { level: ok ? "info" : "warn", text: `后台任务${ok ? "跑完了" : status}：${squash(gist)}` } };
  }
  if ((m = CMD_OUT.exec(text))) return { notice: { level: "info", text: squash(m[1]) } };
  // 人在 claude 里敲的 `!命令`：这是人的动作，照旧是气泡，写成 `! 命令`（和 TUI 里一样）
  if ((m = BASH_IN.exec(text))) return { text: "! " + squash(m[1]) };
  if (BASH_OUT.test(text)) return { notice: { level: "info", text: squash(text.replace(ANY_TAG, " ")) } };
  if (INTERRUPT.test(text)) return { notice: { level: "info", text: "被打断了" } };
  return undefined;
}

export class Transcript {
  /**
   * sub：这是某个子 agent 的只读视图（值 = 主会话里那张子 agent 卡片的 toolCallId）。
   * 同一份日志两种读法：主视图跳过带 _meta.gerdr.subagent 的正文（子 agent 说的话），它的工具调用
   * 照样挂在卡片下面；子 agent 视图只要它自己的那些，工具调用提成顶层。
   */
  readonly sub?: string;
  constructor(sub?: string) {
    this.sub = sub;
  }

  messages: GMessage[] = [];
  running = false;
  /** 正在等人回答的请求（审批 / 提问）。 */
  pending = new Map<string, "permission" | "ask">();
  lastSeq = 0;

  private tools = new Map<string, Loc>();
  private chunks = new Map<string, Loc>(); // "text:<msgId>" / "reasoning:<msgId>" → part
  private lastChunkKey = "";
  private requests = new Map<string, Loc>();
  private planLoc: Loc | null = null;
  private nextId = 0;

  reset(events: HubEvent[]) {
    this.messages = [];
    this.running = false;
    this.pending.clear();
    this.tools.clear();
    this.chunks.clear();
    this.requests.clear();
    this.planLoc = null;
    this.retryLoc = null;
    this.lastChunkKey = "";
    this.lastSeq = 0;
    // id 从头编：同一份日志重放出来的消息 id 一样，React 才不会把组件全部重建（展开状态、滚动位置都在组件里）。
    this.nextId = 0;
    for (const e of events) this.apply(e);
  }

  private uid() {
    return `m${this.nextId++}`;
  }

  /** 改第 i 条消息：换成一个新对象（React 靠引用判断要不要重画）。 */
  private touch(i: number): GMessage {
    const m = this.messages[i];
    const copy = { ...m, parts: [...m.parts] };
    this.messages[i] = copy;
    return copy;
  }

  private assistant(ts: number): number {
    const last = this.messages.length - 1;
    if (last >= 0 && this.messages[last].role === "assistant") return last;
    this.messages.push({ id: this.uid(), role: "assistant", parts: [], metadata: { ts } });
    this.chunks.clear();
    this.lastChunkKey = "";
    this.planLoc = null;
    return this.messages.length - 1;
  }

  /**
   * 「N 秒后重试」那行（level retry）：重试期间原地更新同一行，agent 一有新输出（= 重试成功了）或这一轮结束就收起
   * （标 done，渲染时不画 —— 不真删，删了会让 tools / requests 里记的位置错位）。不收的话成功之后它一直挂在那儿（用户报的）。
   */
  private retryLoc: Loc | null = null;

  private clearRetry() {
    const loc = this.retryLoc;
    if (!loc) return;
    this.retryLoc = null;
    const p = this.messages[loc.msg]?.parts[loc.part] as any;
    if (p?.type === "data-notice") this.setPart(loc, { ...p, data: { ...p.data, done: true } });
  }

  private setPart(loc: Loc, part: GPart) {
    const m = this.touch(loc.msg);
    m.parts[loc.part] = part;
  }

  private pushPart(i: number, part: GPart): Loc {
    const m = this.touch(i);
    m.parts.push(part);
    return { msg: i, part: m.parts.length - 1 };
  }

  apply(e: HubEvent) {
    if (e.seq > this.lastSeq) this.lastSeq = e.seq;
    if (this.sub) {
      if (e.kind === "update") {
        this.messages = [...this.messages];
        this.update(e.update, e.ts);
      }
      return;
    }
    switch (e.kind) {
      case "user": {
        const only = e.content?.length === 1 && e.content[0].type === "text" ? e.content[0].text : undefined;
        const mb = only !== undefined ? machineBlock(only) : undefined;
        if (mb?.notice) {
          this.messages = [...this.messages];
          this.pushPart(this.assistant(e.ts), { type: "data-notice", data: mb.notice });
          break;
        }
        const parts: GPart[] = [];
        for (const c of e.content ?? []) {
          if (c.type === "text") parts.push({ type: "text", text: mb?.text ?? c.text });
          else if (c.type === "image") parts.push({ type: "file", mediaType: c.mimeType, url: `data:${c.mimeType};base64,${c.data}` });
          else if (c.type === "resource_link") parts.push({ type: "text", text: `@${c.uri.replace(/^file:\/\//, "")}` });
        }
        this.messages = [...this.messages];
        this.messages.push({
          id: e.messageId ?? this.uid(),
          role: "user",
          parts,
          metadata: { messageId: e.messageId, steer: e.steer, from: e.from, ts: e.ts, content: e.content },
        });
        this.chunks.clear();
        this.lastChunkKey = "";
        break;
      }
      case "task": {
        // 后台任务 / 子 agent 结束了：在对话里留一张小卡片（任务栏只放在跑的）。
        if (!e.task) break;
        this.messages = [...this.messages];
        const i = this.assistant(e.ts);
        this.pushPart(i, { type: "data-task", data: { task: e.task } });
        this.lastChunkKey = "";
        break;
      }
      case "turn_start": {
        this.running = true;
        this.messages = [...this.messages];
        const i = this.assistant(e.ts);
        // agent 自己开的一轮（后台任务跑完了）：前面没有用户消息，标一下这段话是怎么来的。
        if (e.auto) this.pushPart(i, { type: "data-notice", data: { level: "auto", text: "后台任务有结果了，agent 接着处理" } });
        break;
      }
      case "steer_state": {
        const i = this.messages.findIndex((m) => m.role === "user" && m.metadata?.messageId === e.messageId);
        if (i >= 0) {
          const m = this.messages[i];
          this.messages = [...this.messages];
          this.messages[i] = { ...m, metadata: { ...m.metadata!, queued: e.text === "queued" } };
        }
        break;
      }
      case "turn_end": {
        this.messages = [...this.messages];
        this.clearRetry();
        // 这一轮完了：还标着「排队中」的插话不再转圈（claude 会把它们带进下一轮，到时候自然被读到；别让圈一直转着）
        if (this.messages.some((m) => m.metadata?.queued)) {
          this.messages = this.messages.map((m) => (m.metadata?.queued ? { ...m, metadata: { ...m.metadata, queued: false } } : m));
        }
        this.running = false;
        this.messages = [...this.messages];
        if (e.error || (e.stopReason && e.stopReason !== "end_turn")) {
          const i = this.assistant(e.ts);
          this.pushPart(i, { type: "data-turnEnd", data: { stopReason: e.stopReason ?? "", error: e.error } });
        } else if (e.durationMs && e.durationMs > 1000) {
          // 照 Claude Code：一轮结束留一行「用时 1m 5s · ↓ 2.2k tokens」
          const i = this.assistant(e.ts);
          this.pushPart(i, { type: "data-turnStat", data: { ms: e.durationMs, tokens: e.outputTokens ?? 0, ts: e.ts } });
        }
        // 这一轮里还在「进行中」的工具调用，到这里就不会再有结果了。
        for (const [, loc] of this.tools) {
          const p = this.messages[loc.msg]?.parts[loc.part] as ToolPart | undefined;
          if (p && (p.state === "input-available" || p.state === "input-streaming")) {
            this.setPart(loc, { ...p, state: "output-error", errorText: e.stopReason === "cancelled" ? "已中断" : "未完成", input: p.input } as any);
          }
        }
        break;
      }
      case "update":
        this.messages = [...this.messages];
        this.update(e.update, e.ts);
        break;
      case "permission": {
        this.messages = [...this.messages];
        const req = e.request as PermissionRequest;
        this.pending.set(e.reqId!, "permission");
        const i = this.assistant(e.ts);
        // 审批画在对应的工具调用下面；工具调用还没出现过（有的 agent 先问后报）就先补一个。
        const tc = req.toolCall;
        if (tc?.toolCallId && !this.tools.has(tc.toolCallId)) {
          this.toolStart({ toolCallId: tc.toolCallId, title: tc.title, kind: tc.kind, rawInput: tc.rawInput, status: "pending" }, e.ts);
        }
        this.requests.set(e.reqId!, this.pushPart(i, { type: "data-permission", data: { reqId: e.reqId!, request: req } }));
        break;
      }
      case "ask": {
        this.messages = [...this.messages];
        this.pending.set(e.reqId!, "ask");
        const i = this.assistant(e.ts);
        this.requests.set(e.reqId!, this.pushPart(i, { type: "data-ask", data: { reqId: e.reqId!, request: e.request as AskRequest } }));
        break;
      }
      case "permission_done":
      case "ask_done": {
        this.messages = [...this.messages];
        this.pending.delete(e.reqId!);
        const loc = this.requests.get(e.reqId!);
        if (!loc) break;
        const p = this.messages[loc.msg]?.parts[loc.part] as any;
        if (!p) break;
        const r = e.response ?? {};
        if (p.type === "data-permission") {
          this.setPart(loc, { ...p, data: { ...p.data, answer: r.optionId, cancelled: !!r.cancelled || !r.optionId } });
        } else {
          this.setPart(loc, { ...p, data: { ...p.data, answers: r.answers, cancelled: !!r.cancelled, reason: r.reason } });
        }
        break;
      }
      case "notice": {
        this.messages = [...this.messages];
        const data = { level: e.level ?? "info", text: e.text ?? "" };
        if (data.level === "retry" && this.retryLoc) {
          this.setPart(this.retryLoc, { type: "data-notice", data });
          break;
        }
        const loc = this.pushPart(this.assistant(e.ts), { type: "data-notice", data });
        if (data.level === "retry") this.retryLoc = loc;
        break;
      }
    }
  }

  private update(u: any, ts: number) {
    if (!u) return;
    const tag: string | undefined = u._meta?.gerdr?.subagent;
    if (this.sub) {
      switch (u.sessionUpdate) {
        case "agent_message_chunk":
        case "agent_thought_chunk":
        case "user_message_chunk":
          if (tag !== this.sub) return;
          break;
        case "tool_call":
          if (u._meta?.gerdr?.parentToolCallId !== this.sub) return;
          u = { ...u, _meta: undefined };
          break;
        case "tool_call_update":
          if (!this.tools.has(u.toolCallId)) return;
          break;
        default:
          return;
      }
    } else if (tag) {
      return;
    }
    // agent 有新输出了：重试成功，收起「N 秒后重试」
    if (this.retryLoc && ["agent_message_chunk", "agent_thought_chunk", "tool_call", "tool_call_update"].includes(u.sessionUpdate)) this.clearRetry();
    switch (u.sessionUpdate) {
      case "agent_message_chunk":
      case "agent_thought_chunk": {
        const reasoning = u.sessionUpdate === "agent_thought_chunk";
        const text: string = u.content?.type === "text" ? u.content.text : "";
        const qr = u._meta?.gerdr?.quickReplies;
        const key = `${reasoning ? "r" : "t"}:${u.messageId ?? ""}`;
        const i = this.assistant(ts);
        let loc = this.chunks.get(key);
        // 没有 messageId 的片段：只和紧挨着的上一段同类片段拼（中间夹了工具调用就另起一段）。
        if (!u.messageId && this.lastChunkKey !== key) loc = undefined;
        if (loc && loc.msg === i) {
          const p = this.messages[i].parts[loc.part] as any;
          this.setPart(loc, { ...p, text: p.text + text });
        } else if (text) {
          loc = this.pushPart(i, reasoning ? { type: "reasoning", text, state: "streaming" } : { type: "text", text, state: "streaming" });
          this.chunks.set(key, loc);
        }
        this.lastChunkKey = key;
        if (qr && qr.length) this.pushPart(i, { type: "data-quickReplies", data: { questions: qr } });
        break;
      }
      case "user_message_chunk": {
        // 只有导入的历史会出现（agent 重放）。一条消息可能分几块来（文字 + 贴的图），MessageId 相同，拼回一条
        let part: GPart | undefined;
        if (u.content?.type === "text" && u.content.text) {
          const mb = machineBlock(u.content.text);
          if (mb?.notice) {
            this.pushPart(this.assistant(ts), { type: "data-notice", data: mb.notice });
            break;
          }
          part = { type: "text", text: mb?.text ?? u.content.text };
        } else if (u.content?.type === "image" && u.content.data) {
          part = { type: "file", mediaType: u.content.mimeType, url: `data:${u.content.mimeType};base64,${u.content.data}` };
        }
        if (!part) break;
        const last = this.messages.length - 1;
        if (u.messageId && last >= 0 && this.messages[last].role === "user" && this.messages[last].metadata?.messageId === u.messageId) {
          this.touch(last).parts.push(part);
        } else {
          this.messages.push({ id: u.messageId ?? this.uid(), role: "user", parts: [part], metadata: { messageId: u.messageId, ts } });
        }
        this.chunks.clear();
        this.lastChunkKey = "";
        break;
      }
      case "tool_call":
        this.toolStart(u, ts);
        this.lastChunkKey = "";
        break;
      case "tool_call_update":
        this.toolUpdate(u, ts);
        break;
      case "plan": {
        const i = this.assistant(ts);
        const part: GPart = { type: "data-plan", data: { entries: u.entries ?? [] } };
        if (this.planLoc && this.planLoc.msg === i) this.setPart(this.planLoc, part);
        else this.planLoc = this.pushPart(i, part);
        break;
      }
    }
  }

  private toolStart(u: any, ts: number): void {
    const id: string = u.toolCallId;
    if (this.tools.has(id)) return this.toolUpdate(u, ts);
    const i = this.assistant(ts);
    const meta: ToolMeta = {
      kind: u.kind ?? "other",
      locations: u.locations,
      content: u.content,
      status: u.status ?? "pending",
      parent: u._meta?.gerdr?.parentToolCallId,
      rawOutput: u.rawOutput,
      startedAt: ts,
    };
    if (meta.status === "completed" || meta.status === "failed") meta.endedAt = ts;
    const part: ToolPart = {
      type: "dynamic-tool",
      toolName: u.kind ?? "tool",
      toolCallId: id,
      title: u.title,
      state: toolState(meta.status),
      input: u.rawInput,
      toolMetadata: meta as any,
    } as ToolPart;
    this.tools.set(id, this.pushPart(i, part as GPart));
  }

  private toolUpdate(u: any, ts: number): void {
    const id: string = u.toolCallId;
    const loc = this.tools.get(id);
    if (!loc) return this.toolStart({ ...u, title: u.title ?? "工具调用" }, ts);
    const p = this.messages[loc.msg].parts[loc.part] as ToolPart;
    const meta: ToolMeta = { ...(p.toolMetadata as ToolMeta) };
    if (u.kind) meta.kind = u.kind;
    if (u.locations) meta.locations = u.locations;
    if (u.content) meta.content = u.content;
    if (u.status) meta.status = u.status;
    if ((meta.status === "completed" || meta.status === "failed") && !meta.endedAt) meta.endedAt = ts;
    if (u.rawOutput !== undefined) meta.rawOutput = u.rawOutput;
    if (u._meta?.gerdr?.autoDenied) meta.autoDenied = u._meta.gerdr.autoDenied;
    const next: any = {
      ...p,
      title: u.title ?? p.title,
      toolName: meta.kind,
      input: u.rawInput ?? p.input,
      toolMetadata: meta,
      state: toolState(meta.status),
    };
    if (next.state === "output-error") next.errorText = "失败";
    if (next.state === "output-available") next.output = meta.content ?? null;
    this.setPart(loc, next);
  }
}

function toolState(status: string): ToolPart["state"] {
  switch (status) {
    case "completed":
      return "output-available";
    case "failed":
      return "output-error";
    case "in_progress":
      return "input-available";
  }
  return "input-streaming";
}
