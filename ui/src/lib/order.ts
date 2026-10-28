import type { SessionSummary } from "./types";

/*
 * 会话按「该不该先看」排的顺序。侧栏的 agents 列表和手机上的面板一览（PaneSwitcher）共用这一份：
 * 两边各写一份时只修了一边（在跑的会话一直来回换位置，侧栏修了、面板一览漏了，用户报的）。
 */

/** 分档：等你 0 > 出错 1 > 跑完没看 2 > 在跑 3 > 闲着 4 */
export function sessionRank(s: SessionSummary, unread: Record<string, true>): number {
  if (s.status === "waiting") return 0;
  if (s.status === "error") return 1;
  if (unread[s.id]) return 2;
  if (s.status === "running") return 3;
  return 4;
}

/**
 * 同一档里谁算「新」：看进入这个状态的时刻。在跑的按这一轮开始的时间（跑的过程中不变），别的按 updatedAt
 * （不在跑时它不动，刚跑完的就排最前）。不能一律按 updatedAt：在跑的 agent 每出一点字就更新一次，
 * 几个一起跑时谁刚出字谁排前，列表一直在跳；只按界面位置排又会让刚跑完的沉到底。
 */
export function sessionSince(s: SessionSummary): number {
  return s.status === "running" && s.turn ? s.turn.startedAt : s.updatedAt;
}

/** 先比分档，同档新的在前；分不出来返回 0（调用方再按自己的位置排，保证稳定） */
export function compareSessions(a: SessionSummary, b: SessionSummary, unread: Record<string, true>): number {
  return sessionRank(a, unread) - sessionRank(b, unread) || sessionSince(b) - sessionSince(a);
}
