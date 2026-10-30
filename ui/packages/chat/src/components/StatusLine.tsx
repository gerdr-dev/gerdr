import { useEffect, useState } from "react";
import { useSession } from "./sessionContext";
import { PixelLoader } from "./PixelLoader";
import { t } from "../i18n";

// 在跑的这一轮的状态行，照 Claude Code：「✻ Pondering… (1m 5s · ↓ 2.2k tokens · esc 打断)」，打头的符号换成了像素 loading（PixelLoader）。
// 动词每一轮换一个（按开始时间挑，同一轮不变），时间每秒走，token 数是 agent 报的（流式时是估的，请求结束换成准数）。

const VERBS = [
  "Pondering",
  "Thinking",
  "Working",
  "Cooking",
  "Brewing",
  "Crafting",
  "Musing",
  "Noodling",
  "Percolating",
  "Ruminating",
  "Simmering",
  "Tinkering",
  "Conjuring",
  "Computing",
  "Deliberating",
  "Forging",
  "Hatching",
  "Mulling",
  "Puzzling",
  "Scheming",
  "Spinning",
  "Synthesizing",
  "Untangling",
  "Whirring",
  "Billowing",
  "Churning",
  "Contemplating",
  "Distilling",
  "Finagling",
  "Wrangling",
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
  useEffect(() => {
    const b = setInterval(() => tick((x) => x + 1), 1000);
    return () => clearInterval(b);
  }, []);
  const turn = session?.turn;
  const started = turn?.startedAt ?? Date.now();
  const verb = VERBS[Math.abs(Math.floor(started / 1000)) % VERBS.length];
  const waiting = session?.status === "waiting";
  return (
    // 像素转圈打头（PixelLoader，照 beUI 的 Pixel orbit），后面动词 + 用时 / token；等人处理时它停下来呼吸、换成 warn 色
    // Fixed h-5, same as the turn's metadata line that replaces it when the turn ends (MessageView, data-turnStat): heights
    // that differed made the chat jump at that swap
    <div className="flex h-5 items-center gap-2 text-[13px]">
      <PixelLoader paused={waiting} className={waiting ? "text-warn" : "text-brand"} />
      <span className={waiting ? "text-warn" : "text-brand"}>{waiting ? t("status.waiting") : `${verb}…`}</span>
      <span className="text-[12px] text-muted-foreground tabular-nums">
        {fmtElapsed(Date.now() - started)}
        {turn && turn.outputTokens > 0 ? ` · ↓ ${fmtTokens(turn.outputTokens)} tokens` : ""} · {t("status.escToStop")}
      </span>
    </div>
  );
}
