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
  /** 刚点了发送、服务端还没记进日志（见 Transcript.sending）：半透明画着 */
  sending?: boolean;
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
  /** woke：这个任务一结束 agent 就自己开了一轮（turn_start auto），卡片上标「→ agent 接着处理」 */
  /** command: the command of the tool call that started it, so the card can tell whether its description is that command (monospace) or prose */
  task: { task: AgentTask; woke?: boolean; command?: string };
  turnStat: { ms: number; tokens: number; ts?: number };
  turnEnd: { stopReason: string; error?: string };
  quickReplies: { questions: { question: string; options: string[] }[] };
  /** 输入框里的 `!命令`（gerdr 自己跑的，hub/shell.go）。end：stopped = 人点了停止，lost = gerdr 重启时还在跑；replayed = 从 agent 记录重放出来的 */
  /** path：完整输出的文件（日志只记开头 256KB，太长时 agent 拿到的也是它，见 hub/shell.go） */
  shell: { id: string; command: string; output: string; running: boolean; exit?: number; end?: string; error?: string; replayed?: boolean; path?: string };
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
// 点「允许并重试」时 gerdr 替人发的那句（allowRetryText）：画成系统小字，不当人话 —— 写死一句中文在气泡里，换个界面语言就不对了
const ALLOWED = /^\s*<gerdr-allowed>([\s\S]*?)<\/gerdr-allowed>/;
// 以前发的是这句中文，旧对话里留着的也一样画
const ALLOWED_OLD = /^\s*我已经允许了刚才被拦下的操作，请重试。\s*$/;
// claude 写进去的打断记号（`[Request interrupted by user]` / `…for tool use]`），也不是人打的
const INTERRUPT = /^\s*\[Request interrupted by user[^\]]*\]\s*$/;

/** 多行压成一行、剥掉 ANSI（这些都只占一行小字，长了也读不了）；太长截断。 */
function squash(s: string, max = 300) {
  const t = s
    .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, "")
    .split(/\s+/)
    .filter(Boolean)
    .join(" ");
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
  if (ALLOWED_OLD.test(text)) return { notice: { level: "info", text: "已允许被拦下的操作，让它重试" } };
  if ((m = ALLOWED.exec(text))) {
    const rules = m[1].split("\n").flatMap((l) => (l.startsWith("- ") ? [l.slice(2).trim()] : []));
    return { notice: { level: "info", text: `已允许被拦下的操作，让它重试${rules.length ? "：" + rules.join("、") : ""}` } };
  }
  return undefined;
}

/**
 * 「允许并重试」发给 agent 的话：给 agent 看的用英文（它照样按对话的语言回），包一层标签让界面认出来画成系统提示（machineBlock）。
 * 规则一行一条「- 」开头，界面从这里取出来显示。
 */
export function allowRetryText(rules: string[]) {
  return [
    "<gerdr-allowed>",
    "The user has allowed the operation that was just blocked, by adding these permission rules:",
    ...rules.map((r) => "- " + r),
    "Please retry it.",
    "</gerdr-allowed>",
  ].join("\n");
}

/*
 * `!命令` 跟着下一条消息带给 agent 的那段（hub/shell.go 的 shellContext）：一条命令一块文字，排在人说的话前面。
 * 日志里有 shell 事件画卡片，这几块不再画成人说的话；从 agent 记录重放时没有 shell 事件，就靠解析它画回卡片。
 * 和 Claude Code 自己 `!` 留下的记录（输入、输出各一条消息，machineBlock 管）不同：这里输入输出在同一块里。
 */
const SHELL_CTX = /^<bash-input>([\s\S]*?)<\/bash-input>\n<bash-stdout>([\s\S]*?)<\/bash-stdout><bash-stderr>([\s\S]*?)<\/bash-stderr>\s*$/;

export const isShellContext = (text: string) => SHELL_CTX.test(text);

/** 消息内容里去掉 `!命令` 带的那几块（撤回、回退放回输入框时用：命令还在对话里，下一条照样会带上） */
export const dropShellContext = (blocks: ContentBlock[]) => blocks.filter((c) => !(c.type === "text" && isShellContext(c.text)));

/**
 * Whether a row's text is the command itself rather than a description of it. Only command text is drawn
 * monospace: the same label switching fonts between rows (description in the tool row, monospace in the
 * task-done row; English prose like "Stop background command" in monospace) is what kept looking off.
 * Titles may be the command squeezed onto one line and cut with "…" (native's oneLine), so compare loosely.
 */
export function isCommandText(text: string | undefined, command: string | undefined): boolean {
  if (!text || !command) return false;
  const flat = (s: string) => s.split(/\s+/).join(" ").trim();
  const t = flat(text),
    c = flat(command);
  return t === c || (t.endsWith("…") && t.length > 1 && c.startsWith(t.slice(0, -1)));
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
  /**
   * 排着队的插话（steer_state queued：claude 要等手上这个工具调用结束才读）。不放进 messages：放进去的话 agent 接着说的话、
   * 跑的工具全排在它后面，看着像这句话早就插进去了。先挂在最底下，被读到（started）或这一轮结束时才落进 messages ——
   * 落在它真正插进去的那个位置。重放日志走的是同一套，位置一样。
   */
  queued: GMessage[] = [];
  /**
   * 刚点了发送、还没回到日志里的消息：服务端要等 agent 进程连上（冷会话要起进程，几百毫秒到几秒）才记下这条，
   * 这段时间输入框已经清空、对话里却什么都没有，像是没发出去。先在最底下画一条半透明的，日志里那条（同一个 messageId）到了就换掉；
   * 发失败就拿掉（字放回输入框）。不是日志重放出来的，reset 不清 —— 重放里有它的那条会顺手把它换掉。
   */
  sending: GMessage[] = [];
  private allCache: { m: GMessage[]; q: GMessage[]; s: GMessage[]; all: GMessage[] } | null = null;
  /** 界面要画的全部：正文 + 排着队的插话 + 发送中的。都没换就返回同一个数组（下游靠引用判断要不要重算）。 */
  get all(): GMessage[] {
    const c = this.allCache;
    if (c && c.m === this.messages && c.q === this.queued && c.s === this.sending) return c.all;
    const all = this.queued.length || this.sending.length ? [...this.messages, ...this.queued, ...this.sending] : this.messages;
    this.allCache = { m: this.messages, q: this.queued, s: this.sending, all };
    return all;
  }

  /** 先画一条发送中的（id 用 messageId：日志里那条的 id 也是它，换过去时 React 不重建这一行） */
  addSending(messageId: string, content: ContentBlock[]) {
    const parts: GPart[] = [];
    for (const c of content) {
      if (c.type === "text") parts.push({ type: "text", text: c.text });
      else if (c.type === "image") parts.push({ type: "file", mediaType: c.mimeType, url: imageURL(c) });
    }
    this.sending = [...this.sending, { id: messageId, role: "user", parts, metadata: { messageId, sending: true, ts: Date.now(), content } }];
  }

  dropSending(messageId: string) {
    if (this.sending.some((m) => m.id === messageId)) this.sending = this.sending.filter((m) => m.id !== messageId);
  }
  running = false;
  /** 正在等人回答的请求（审批 / 提问）。 */
  pending = new Map<string, "permission" | "ask">();
  lastSeq = 0;

  private tools = new Map<string, Loc>();
  private chunks = new Map<string, Loc>(); // "text:<msgId>" / "reasoning:<msgId>" → part
  private lastChunkKey = "";
  private requests = new Map<string, Loc>();
  private shells = new Map<string, Loc>(); // `!命令` 的 id → 卡片
  private planLoc: Loc | null = null;
  private curSeq = 0;
  private idBase = "";
  private idDup = 0;

  reset(events: HubEvent[]) {
    this.messages = [];
    this.queued = [];
    this.running = false;
    this.pending.clear();
    this.tools.clear();
    this.chunks.clear();
    this.requests.clear();
    this.shells.clear();
    this.planLoc = null;
    this.retryLoc = null;
    this.lastChunkKey = "";
    this.lastSeq = 0;
    // 同一份日志重放出来的消息 id 一样（见 uid），React 才不会把组件全部重建（展开状态、滚动位置都在组件里）。
    this.idBase = "";
    this.idDup = 0;
    for (const e of events) this.apply(e);
  }

  // 按生成它的那条日志的序号起名：往前补了更早的日志（loadOlder）整份重放，已有消息的 id 不变 ——
  // React 不重建、虚拟列表的行 key 对得上（从头数的话前面一多，后面全错位）。同一条日志生成两条消息时加后缀。
  private uid() {
    const base = `m${this.curSeq}`;
    this.idDup = base === this.idBase ? this.idDup + 1 : 0;
    this.idBase = base;
    return this.idDup ? `${base}_${this.idDup}` : base;
  }

  /** 排着队的插话落进正文末尾（不再标排队中）。之后 agent 的输出另起一条消息接在它后面。 */
  private landQueued(ms: GMessage[]) {
    this.messages = [...this.messages, ...ms.map((m) => ({ ...m, metadata: { ...m.metadata!, queued: false } }))];
    this.chunks.clear();
    this.lastChunkKey = "";
    this.planLoc = null;
  }

  /** 从正文里拿掉第 i 条（转去排队的插话）：后面那些消息里记着的位置都往前挪一格。 */
  private removeMessage(i: number) {
    this.messages.splice(i, 1);
    const fix = (l: Loc | null) => (l && l.msg > i ? { msg: l.msg - 1, part: l.part } : l);
    for (const m of [this.tools, this.chunks, this.requests, this.shells]) for (const [k, l] of m) m.set(k, fix(l)!);
    this.planLoc = fix(this.planLoc);
    this.retryLoc = fix(this.retryLoc);
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

  /** The `command` input of the tool call with this id (the one that started a background task). */
  private commandOf(toolCallId?: string): string | undefined {
    if (!toolCallId) return undefined;
    for (let mi = this.messages.length - 1; mi >= 0; mi--)
      for (const p of this.messages[mi].parts)
        if (p.type === "dynamic-tool" && (p as ToolPart).toolCallId === toolCallId) {
          const c = ((p as ToolPart).input as any)?.command;
          return typeof c === "string" ? c : undefined;
        }
    return undefined;
  }

  private pushPart(i: number, part: GPart): Loc {
    const m = this.touch(i);
    m.parts.push(part);
    return { msg: i, part: m.parts.length - 1 };
  }

  apply(e: HubEvent) {
    if (e.seq > this.lastSeq) this.lastSeq = e.seq;
    this.curSeq = e.seq;
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
          if (c.type === "text" && isShellContext(c.text)) continue; // `!命令` 带过去的输出：上面已经有那张命令卡片了
          if (c.type === "text") parts.push({ type: "text", text: mb?.text ?? c.text });
          else if (c.type === "image") parts.push({ type: "file", mediaType: c.mimeType, url: imageURL(c) });
          else if (c.type === "resource_link") parts.push({ type: "text", text: `@${c.uri.replace(/^file:\/\//, "")}` });
        }
        if (e.messageId) this.dropSending(e.messageId);
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
        this.pushPart(i, { type: "data-task", data: { task: e.task, command: this.commandOf(e.task.toolCallId) } });
        this.lastChunkKey = "";
        break;
      }
      case "turn_start": {
        this.running = true;
        this.messages = [...this.messages];
        const i = this.assistant(e.ts);
        // agent 自己开的一轮（后台任务跑完了）：前面没有用户消息，标一下这段话是怎么来的。
        // 紧挨着的就是那张「任务结束」卡片时标在卡片上（「哪个任务跑完、agent 因此接着干」一行说清）；
        // 另起一行居中提示会和卡片重复说一遍，还看着像警告（用户报的）。没有卡片（别的原因唤醒）才单独提示。
        if (e.auto) {
          const parts = this.messages[i].parts;
          const last = parts[parts.length - 1];
          if (last?.type === "data-task") this.setPart({ msg: i, part: parts.length - 1 }, { ...last, data: { ...last.data, woke: true } });
          else this.pushPart(i, { type: "data-notice", data: { level: "auto", text: "后台任务跑完了，agent 自己接着干" } });
        }
        break;
      }
      case "steer_state": {
        const queued = e.text === "queued";
        const q = this.queued.findIndex((m) => m.metadata?.messageId === e.messageId);
        if (q >= 0) {
          // 被读到了：从底下挪进正文，接在 agent 此刻说到的地方后面
          if (!queued) {
            const m = this.queued[q];
            this.queued = this.queued.filter((_, j) => j !== q);
            this.landQueued([m]);
          }
          break;
        }
        const i = this.messages.findIndex((m) => m.role === "user" && m.metadata?.messageId === e.messageId);
        if (i < 0) break;
        const m = { ...this.messages[i], metadata: { ...this.messages[i].metadata!, queued } };
        this.messages = [...this.messages];
        if (queued) {
          this.removeMessage(i);
          this.queued = [...this.queued, m];
        } else {
          this.messages[i] = m;
        }
        break;
      }
      case "turn_end": {
        this.messages = [...this.messages];
        this.clearRetry();
        this.running = false;
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
        // 这一轮完了还没被读到的插话：claude 会把它们带进下一轮，就落在这一轮的末尾（别让圈一直转着）
        if (this.queued.length) {
          const q = this.queued;
          this.queued = [];
          this.landQueued(q);
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
      case "shell": {
        this.messages = [...this.messages];
        const id = e.messageId!;
        this.shells.set(id, this.pushPart(this.assistant(e.ts), { type: "data-shell", data: { id, command: e.text ?? "", output: "", running: true } }));
        this.lastChunkKey = "";
        break;
      }
      case "shell_out":
      case "shell_done": {
        const loc = this.shells.get(e.messageId!);
        const p = loc && (this.messages[loc.msg]?.parts[loc.part] as any);
        if (!p || p.type !== "data-shell") break;
        this.messages = [...this.messages];
        const data =
          e.kind === "shell_out"
            ? { ...p.data, output: p.data.output + (e.text ?? "") }
            : { ...p.data, running: false, exit: e.exit, end: e.level, error: e.error, path: e.text || undefined };
        this.setPart(loc!, { ...p, data });
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
        const sh = u.content?.type === "text" ? SHELL_CTX.exec(u.content.text ?? "") : null;
        if (sh) {
          // `!命令` 带过去的那段（重新载入后日志里没有 shell 事件了）：画回命令卡片
          const i = this.assistant(ts);
          const id = `r${this.curSeq}-${this.messages[i].parts.length}`; // 一条消息里可能带了好几条命令
          this.pushPart(i, { type: "data-shell", data: { id, command: sh[1], output: sh[2], running: false, error: sh[3] || undefined, replayed: true } });
          break;
        }
        if (u.content?.type === "text" && u.content.text) {
          const mb = machineBlock(u.content.text);
          if (mb?.notice) {
            this.pushPart(this.assistant(ts), { type: "data-notice", data: mb.notice });
            break;
          }
          part = { type: "text", text: mb?.text ?? u.content.text };
        } else if (u.content?.type === "image" && (u.content.data || u.content.blob)) {
          part = { type: "file", mediaType: u.content.mimeType, url: imageURL(u.content) };
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

/** 图片：推过来时多半已换成地址（blob，服务端存着），老的 / 接口原样回的还是 base64。 */
function imageURL(c: { data?: string; mimeType?: string; blob?: string }) {
  return c.blob ?? `data:${c.mimeType};base64,${c.data}`;
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
