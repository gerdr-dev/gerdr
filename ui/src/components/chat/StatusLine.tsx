import { useEffect, useState } from "react";
import { useSession } from "./sessionContext";

// 在跑的这一轮的状态行，照 Claude Code：「✻ Pondering… (1m 5s · ↓ 2.2k tokens · esc 打断)」。
// 符号转圈、动词每一轮换一个（按开始时间挑，同一轮不变），时间每秒走，token 数是 agent 报的（流式时是估的，请求结束换成准数）。

const FRAMES = ["·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"];
const VERBS = [
  "Pondering", "Thinking", "Working", "Cooking", "Brewing", "Crafting", "Musing", "Noodling", "Percolating", "Ruminating",
  "Simmering", "Tinkering", "Conjuring", "Computing", "Deliberating", "Forging", "Hatching", "Mulling", "Puzzling", "Scheming",
  "Spinning", "Synthesizing", "Untangling", "Whirring", "Billowing", "Churning", "Contemplating", "Distilling", "Finagling", "Wrangling",
];

export function fmtElapsed(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

export function fmtTokens(n: number) {
  if (n < 1000) return String(n);
  if (n < 100_000) return `${(n / 1000).toFixed(1)}k`;
  return `${Math.round(n / 1000)}k`;
}

export function StatusLine() {
  const session = useSession();
  const [, tick] = useState(0);
  const [frame, setFrame] = useState(0);
  useEffect(() => {
    const a = setInterval(() => setFrame((f) => (f + 1) % FRAMES.length), 120);
    const b = setInterval(() => tick((x) => x + 1), 1000);
    return () => {
      clearInterval(a);
      clearInterval(b);
    };
  }, []);
  const turn = session?.turn;
  const started = turn?.startedAt ?? Date.now();
  const verb = VERBS[Math.abs(Math.floor(started / 1000)) % VERBS.length];
  const waiting = session?.status === "waiting";
  return (
    <div className="flex items-baseline gap-2 font-mono text-[12.5px]">
      <span className="w-3 text-brand">{FRAMES[frame]}</span>
      <span className="text-brand">{waiting ? "等你处理" : `${verb}…`}</span>
      <span className="text-muted-foreground">
        ({fmtElapsed(Date.now() - started)}
        {turn && turn.outputTokens > 0 ? ` · ↓ ${fmtTokens(turn.outputTokens)} tokens` : ""} · esc 打断)
      </span>
    </div>
  );
}
