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
  | { key: string; kind: "user"; msg: GMessage; first: true }
  | { key: string; kind: "part"; part: GPart; i: number; msgId: string; kids?: ToolPart[]; first: boolean; streaming: boolean; live: boolean }
  | { key: string; kind: "working"; empty: boolean; first: boolean };

/** 把整份对话拍平成行。live：最后一条助手消息（还在跑的那一轮里的东西：流式、「工作中…」、快捷回复可点）。 */
export function buildRows(msgs: GMessage[], running: boolean): Row[] {
  const rows: Row[] = [];
  const lastIdx = msgs.length - 1;
  msgs.forEach((m, mi) => {
    if (m.role === "user") {
      rows.push({ key: m.id, kind: "user", msg: m, first: true });
      return;
    }
    const live = mi === lastIdx;
    const children = childTools(m);
    const lastPart = m.parts.length - 1;
    let first = true;
    m.parts.forEach((p, i) => {
      if (!partVisible(p, children)) return;
      const tp = p.type === "dynamic-tool" ? (p as ToolPart) : undefined;
      rows.push({
        key: `${m.id}:${tp ? tp.toolCallId : i}`,
        kind: "part",
        part: p,
        i,
        msgId: m.id,
        kids: tp ? children.get(tp.toolCallId) : undefined,
        first,
        streaming: live && running && i === lastPart,
        live,
      });
      first = false;
    });
    // 在跑的这一轮：最底下一行状态（照 Claude Code：转圈 + 动词 + 用时 + token 数）
    if (live && running) rows.push({ key: `${m.id}:working`, kind: "working", empty: first, first });
  });
  return rows;
}

export type RowEnv = {
  sessionId: string;
  running: boolean;
  onRewind?: (m: GMessage) => void;
  onEdit?: (m: GMessage) => void;
  onQuickReply: (s: string) => void;
};

function Frame({ first, children }: { first: boolean; children: ReactNode }) {
  // 消息之间的间距在一条消息的第一行上（pt-5），同一条消息里各块之间 pt-2：和原来一整条消息时的排版一样
  return <div className={cn("mx-auto w-full max-w-[860px] px-4", first ? "pt-5" : "pt-2")}>{children}</div>;
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
        <Frame first>
          <MessageView msg={row.msg} sessionId={env.sessionId} live={false} running={env.running} onRewind={env.onRewind} onEdit={env.onEdit} onQuickReply={env.onQuickReply} />
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
      <Frame first={row.first}>
        <div className="flex w-full min-w-0 max-w-full flex-col text-[1em] text-foreground">
          {renderPart(row.part, row.i, {
            sessionId: env.sessionId,
            msgId: row.msgId,
            streaming: row.streaming,
            running: row.live && env.running,
            children,
            onQuickReply: env.onQuickReply,
            quickEnabled: row.live,
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
    if (x.kind === "user" && y.kind === "user") return x.msg === y.msg;
    if (x.kind === "working" && y.kind === "working") return x.empty === y.empty;
    if (x.kind === "part" && y.kind === "part") return x.part === y.part && x.streaming === y.streaming && x.live === y.live && sameKids(x.kids, y.kids);
    return false;
  },
);
