import { ChevronDown, Wrench } from "lucide-react";
import { memo, type ReactNode } from "react";
import { StatusLine } from "./StatusLine";
import type { GMessage, GPart, ToolPart } from "@/lib/transcript";
import { cn } from "@/lib/utils";
import { childTools, MessageView, partVisible, renderPart } from "./MessageView";

// 对话是虚拟列表（react-virtuoso），只渲染屏幕附近的行。长对话卡的是两样：DOM 太多、每次更新都要重排一长串 markdown。
//
// 「一行」不是一条消息，是消息里的一块：用户的一句话一行；助手一轮回复里的每段文字、每个工具调用、每张卡片各一行。
// 一轮回复常常就有几百个工具调用 —— 按消息切的话，最长的那一轮照样整个渲染，虚拟化等于没做。
// 子 agent 的工具调用挂在父调用里面（父调用那一行负责画它们），不单独成行。

export type Row =
  // prev / next：前后紧挨着也是用户消息（连发几句、中途插话）—— 气泡叠成一摞，相邻的角收小
  | { key: string; kind: "user"; msg: GMessage; first: true; prev?: boolean; next?: boolean }
  // peek：收着的组底下露出来的最新那一块，值是组的 key（见 buildRows）
  | {
      key: string;
      kind: "part";
      part: GPart;
      i: number;
      msgId: string;
      kids?: ToolPart[];
      first: boolean;
      streaming: boolean;
      live: boolean;
      tight?: boolean;
      peek?: string;
    }
  | { key: string; kind: "working"; empty: boolean; first: boolean }
  | { key: string; kind: "group"; first: boolean; open: boolean; summary: string; failed: number; busy: boolean; tight?: boolean }
  | { key: string; kind: "cleared"; n: number; first: true };

const isClear = (p: GPart) => p.type === "data-notice" && (p as any).data?.level === "clear";

// 连续的工具调用 / 思考折成一组（照 Claude 桌面版）：一轮里常常是几十行 `cd … && grep …`，夹着几行「思考」，
// 真正要读的那几段话被挤得找不到。改文件的不折（默认展开看 diff 是它的主要用途），带子 agent 的不折（有「查看对话」）。
function foldable(p: GPart, kids?: ToolPart[]): boolean {
  if (p.type === "reasoning") return true;
  // 已经答完的审批（拒绝 / 作废；批准的根本不成行）也收进组里；还在等人点的不折，要让人看见
  if (p.type === "data-permission") return !!(p.data.answer || p.data.cancelled);
  // 后台任务跑完的那一行：和工具调用一个样子，也收进组里
  // 子 agent 的那一行有「查看对话」按钮，不折
  if (p.type === "data-task") return !(p.data.task.type === "local_agent" || p.data.task.subagent);
  if (p.type !== "dynamic-tool" || kids?.length) return false;
  const tp = p as ToolPart;
  // 有操作按钮的不折，折起来就点不到了：被 auto 分类器拦下的有「允许并重试」，子 agent（Task / Agent，kind=think）有「查看对话」。
  // （「转到后台」只在正在跑时出现，那时它是组里最底下那一块，收着也露在外面）
  if (tp.state === "output-error" && tp.toolMetadata?.autoDenied?.length) return false;
  if (tp.toolMetadata?.kind === "think") return false;
  const c = tp.toolMetadata?.content ?? [];
  return !c.some((x: any) => x.type === "diff" || x._meta?.gerdr?.unifiedDiff || x.content?._meta?.gerdr?.unifiedDiff);
}

const KIND_TEXT: Record<string, [string, string]> = {
  execute: ["跑了", "条命令"],
  read: ["读了", "个文件"],
  search: ["搜了", "次"],
  fetch: ["抓了", "个网页"],
};

/** 「跑了 5 条命令、读了 2 个文件，思考 3 次」 */
function summarize(parts: GPart[]): { summary: string; failed: number } {
  const n: Record<string, number> = {};
  let think = 0,
    failed = 0,
    denied = 0,
    tasks = 0;
  for (const p of parts) {
    if (p.type === "reasoning") {
      think++;
      continue;
    }
    if (p.type === "data-permission") {
      denied++;
      continue;
    }
    if (p.type === "data-task") {
      tasks++;
      continue;
    }
    const tp = p as ToolPart;
    const k = KIND_TEXT[tp.toolMetadata?.kind ?? ""] ? tp.toolMetadata!.kind : "other";
    n[k] = (n[k] ?? 0) + 1;
    if (tp.state === "output-error") failed++;
  }
  const bits = Object.keys(KIND_TEXT)
    .filter((k) => n[k])
    .map((k) => `${KIND_TEXT[k][0]} ${n[k]} ${KIND_TEXT[k][1]}`);
  if (n.other) bits.push(`调了 ${n.other} 次工具`);
  let summary = bits.join("、");
  if (think) summary += `${summary ? "，" : ""}思考 ${think} 次`;
  if (tasks) summary += `${summary ? "，" : ""}${tasks} 个后台任务跑完`;
  if (denied) summary += `${summary ? "，" : ""}拒了 ${denied} 次`;
  return { summary, failed };
}

/**
 * 把整份对话拍平成行。live：最后一条（不算排在底下的插话）助手消息（还在跑的那一轮里的东西：流式、「工作中…」、快捷回复可点）。
 * openGroups：展开了的工具组（key 是组头那一行的 key）。展开时组里每一块还是单独一行 —— 一组几百个调用也照样虚拟化。
 */
export function buildRows(msgs: GMessage[], running: boolean, showCleared = false, openGroups: Record<string, boolean> = {}): Row[] {
  const rows: Row[] = [];
  const lastIdx = msgs.length - 1;
  // 排着队的插话挂在最底下（见 Transcript.queued），还在跑的那条是它们前面那条
  let liveIdx = lastIdx;
  while (liveIdx >= 0 && msgs[liveIdx].metadata?.queued) liveIdx--;
  // /clear 之前的对话默认收起（照 Claude Code：清空就是清屏），顶上留一行「展开」—— 记录还在，只是不挡眼
  let cut = -1,
    cutPart = -1;
  if (!showCleared) {
    for (let mi = lastIdx; mi >= 0 && cut < 0; mi--) {
      const ps = msgs[mi].parts;
      for (let pi = ps.length - 1; pi >= 0; pi--)
        if (isClear(ps[pi])) {
          [cut, cutPart] = [mi, pi];
          break;
        }
    }
    const n = cut > 0 ? msgs.slice(0, cut).filter((m) => m.role === "user").length : 0; // cut = -1 时 slice(0,-1) 是「除了最后一条」，别算进去
    if (n > 0) rows.push({ key: "cleared", kind: "cleared", n, first: true });
  }
  msgs.forEach((m, mi) => {
    if (mi < cut) return;
    if (m.role === "user") {
      const last = rows[rows.length - 1];
      const prev = last?.kind === "user";
      if (prev) rows[rows.length - 1] = { ...last, next: true };
      rows.push({ key: m.id, kind: "user", msg: m, first: true, prev });
      return;
    }
    const live = mi === liveIdx;
    const children = childTools(m);
    const lastPart = m.parts.length - 1;
    const parts: Extract<Row, { kind: "part" }>[] = [];
    m.parts.forEach((p, i) => {
      if (mi === cut && i < cutPart) return;
      if (!partVisible(p, children)) return;
      const tp = p.type === "dynamic-tool" ? (p as ToolPart) : undefined;
      parts.push({
        key: `${m.id}:${tp ? tp.toolCallId : i}`,
        kind: "part",
        part: p,
        i,
        msgId: m.id,
        kids: tp ? children.get(tp.toolCallId) : undefined,
        first: false,
        streaming: live && running && i === lastPart,
        live,
      });
    });
    let first = true;
    const push = (r: Extract<Row, { kind: "part" }>) => {
      rows.push({ ...r, first });
      first = false;
    };
    for (let a = 0; a < parts.length;) {
      let b = a;
      while (b < parts.length && foldable(parts[b].part, parts[b].kids)) b++;
      if (b - a < 2) {
        push(parts[a]);
        a = Math.max(b, a + 1);
        continue;
      }
      const run = parts.slice(a, b);
      const key = `${run[0].key}:group`;
      const open = !!openGroups[key];
      // 还在跑、而且这组就在最底下：收着也把最新那一块露出来，看得到它正在干什么
      const busy = live && running && b === parts.length;
      rows.push({ key, kind: "group", first, open, busy, ...summarize(run.map((r) => r.part)) });
      first = false;
      if (open) run.forEach(push);
      // 露出来的这一块展开没展开跟着组记、不跟这条命令记：人展开它是想看「最新在干什么」，换了下一条命令也该展开着；
      // 也不写进那条命令自己的状态 —— 点开组看详情时各行照旧（用户要的）。key 加 :peek，和详情里同一条分开挂载
      else if (busy) {
        const last = run[run.length - 1];
        push({ ...last, key: `${last.key}:peek`, peek: key });
      }
      a = b;
    }
    // 在跑的这一轮：最底下一行状态（照 Claude Code：转圈 + 动词 + 用时 + token 数）
    if (live && running && liveIdx === lastIdx) rows.push({ key: `${m.id}:working`, kind: "working", empty: first, first });
  });
  // 最后一条是用户消息（排队中的插话、刚发出去它还没回）但这一轮在跑：状态行照样放在最底下。
  // 原来状态行只挂在最后那条助手消息上，插话一排队就排到了它后面，转圈和用时那一行就没了（用户报的）。
  // key 跟着还在跑的那条走：插话排进来、落下去时状态行不重新挂载
  const tail = msgs[lastIdx];
  const owner = msgs[liveIdx] ?? tail;
  if (running && (tail?.role === "user" || liveIdx !== lastIdx) && lastIdx >= cut)
    rows.push({ key: `${owner.id}:working`, kind: "working", empty: true, first: true });
  // 行距（照 Codex）：工具调用 / 思考这类一行小字，连着的几条挨紧（tight）；它和正文之间留开，像分段一样 ——
  // 原来一律挨着，正文和一串命令挤成一团
  const toolish = (r: Row) => r.kind === "group" || (r.kind === "part" && ["dynamic-tool", "reasoning", "data-permission", "data-task"].includes(r.part.type));
  for (let i = 1; i < rows.length; i++) {
    const r = rows[i];
    if ((r.kind === "part" || r.kind === "group") && !r.first) rows[i] = { ...r, tight: toolish(r) && toolish(rows[i - 1]) };
  }
  return rows;
}

export type RowEnv = {
  sessionId: string;
  running: boolean;
  onRewind?: (m: GMessage) => void;
  onWithdraw?: (messageId: string, now: boolean) => void;
  onQuickReply: (s: string) => void;
  onShowCleared?: () => void;
  onToggleGroup?: (key: string) => void;
};

/** 一组折起来的工具调用 / 思考：一行汇总，点开逐行列出来（照 Astryx ChatToolCalls 的组头：扳手 + 数量 + 箭头）。 */
function GroupHeader({ row, onToggle }: { row: Extract<Row, { kind: "group" }>; onToggle?: (key: string) => void }) {
  return (
    <button
      className="-ml-1.5 flex w-[calc(100%+0.375rem)] min-w-0 items-center gap-2 rounded-md px-1.5 py-[3px] text-left text-[13px] text-muted-foreground hover:bg-muted/60 hover:text-foreground"
      onClick={() => onToggle?.(row.key)}
    >
      {/* 在跑时底下露着最新那一块，它自己会转圈；组头再转就是一屏好几个 loading */}
      <Wrench className="size-3.5 shrink-0" />
      <span className="min-w-0 truncate">{row.summary}</span>
      {row.failed > 0 && <span className="shrink-0 text-[11px] text-bad">{row.failed} 个失败</span>}
      <ChevronDown className={cn("size-3.5 shrink-0 opacity-60 transition-transform", row.open && "rotate-180")} />
    </button>
  );
}

function Frame({ first, tight, stack, children }: { first: boolean; tight?: boolean; stack?: boolean; children: ReactNode }) {
  // 消息之间的间距在一条消息的第一行上（first），同一条消息里各块之间 pt-4；
  // 工具调用 / 思考接着工具调用 / 思考时挨紧（tight，见 buildRows 最后那段）。
  // 宽 800 去掉两边 px-4 = 正文 768（照 Codex：一行字少一点读着不累）。轮与轮之间（first）留 32、轮里面各块挨着：层次靠间距拉开
  return <div className={cn("mx-auto w-full max-w-[800px] px-4", stack ? "pt-1" : first ? "pt-8" : tight ? "pt-0.5" : "pt-4")}>{children}</div>;
}

const sameKids = (a?: ToolPart[], b?: ToolPart[]) => a === b || (!!a && !!b && a.length === b.length && a.every((x, i) => x === b[i]));

/**
 * 一行。行对象每次都重新生成，比较时只看里面的东西：没变的块（同一个 part 对象）不重渲染 ——
 * 流式输出时只有最后那一行在动。
 */
export const RowView = memo(
  function RowView({ row, env }: { row: Row; env: RowEnv }) {
    if (row.kind === "user") {
      return (
        <Frame first stack={row.prev}>
          <MessageView
            msg={row.msg}
            sessionId={env.sessionId}
            live={false}
            running={env.running}
            onRewind={env.onRewind}
            onWithdraw={env.onWithdraw}
            onQuickReply={env.onQuickReply}
            stackPrev={row.prev}
            stackNext={row.next}
          />
        </Frame>
      );
    }
    if (row.kind === "cleared") {
      return (
        <Frame first>
          <button
            className="w-full rounded-md py-1 text-center text-[12px] text-muted-foreground hover:bg-accent/50 hover:text-foreground"
            onClick={env.onShowCleared}
          >
            展开 /clear 之前的对话（{row.n} 条）
          </button>
        </Frame>
      );
    }
    if (row.kind === "group") {
      return (
        <Frame first={row.first} tight={row.tight}>
          <GroupHeader row={row} onToggle={env.onToggleGroup} />
        </Frame>
      );
    }
    if (row.kind === "working") {
      return (
        <Frame first={row.first}>
          <StatusLine />
        </Frame>
      );
    }
    const children = new Map<string, ToolPart[]>();
    if (row.kids && row.part.type === "dynamic-tool") children.set((row.part as ToolPart).toolCallId, row.kids);
    return (
      <Frame first={row.first} tight={row.tight}>
        <div className="flex w-full min-w-0 max-w-full flex-col text-[1em] text-foreground">
          {renderPart(row.part, row.i, {
            sessionId: env.sessionId,
            msgId: row.msgId,
            streaming: row.streaming,
            running: row.live && env.running,
            children,
            onQuickReply: env.onQuickReply,
            quickEnabled: row.live,
            keep: row.peek && `${env.sessionId}:peek:${row.peek}`,
          })}
        </div>
      </Frame>
    );
  },
  (a, b) => {
    if (a.env !== b.env) {
      // env 只有 running 会变；它只影响用户消息（能不能回退）和还在跑的那一轮
      if (a.env.running !== b.env.running && (a.row.kind === "user" || (a.row.kind === "part" && a.row.live))) return false;
      if (a.env.sessionId !== b.env.sessionId) return false;
    }
    const x = a.row,
      y = b.row;
    if (x.kind !== y.kind || x.first !== y.first) return false;
    if (x.kind === "user" && y.kind === "user") return x.msg === y.msg && x.prev === y.prev && x.next === y.next;
    if (x.kind === "working" && y.kind === "working") return x.empty === y.empty;
    if (x.kind === "group" && y.kind === "group")
      return (
        x.open === y.open &&
        x.summary === y.summary &&
        x.failed === y.failed &&
        x.busy === y.busy &&
        x.tight === y.tight &&
        a.env.onToggleGroup === b.env.onToggleGroup
      );
    if (x.kind === "cleared" && y.kind === "cleared") return x.n === y.n && a.env.onShowCleared === b.env.onShowCleared;
    if (x.kind === "part" && y.kind === "part")
      return x.part === y.part && x.streaming === y.streaming && x.live === y.live && x.tight === y.tight && x.peek === y.peek && sameKids(x.kids, y.kids);
    return false;
  },
);
