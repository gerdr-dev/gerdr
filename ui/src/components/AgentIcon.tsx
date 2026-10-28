import { Bot } from "lucide-react";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

// 不用厂商 logo（商标），用一个字母徽记区分：C = Claude Code，X = Codex。变体（设置里加的「Claude 2」）用底子的徽记。
export function AgentIcon({ agent: id, className }: { agent: string; className?: string }) {
  const agent = useStore((s) => s.agents.find((a) => a.id === id)?.base) || id;
  if (agent === "claude")
    return (
      <span
        className={cn("inline-flex shrink-0 items-center justify-center rounded-[5px] bg-[oklch(0.68_0.13_45)] font-semibold text-white", className)}
        style={{ fontSize: "0.62em" }}
      >
        C
      </span>
    );
  if (agent === "codex")
    return (
      <span
        className={cn("inline-flex shrink-0 items-center justify-center rounded-[5px] bg-foreground font-semibold text-background", className)}
        style={{ fontSize: "0.62em" }}
      >
        X
      </span>
    );
  return <Bot className={cn("shrink-0 text-muted-foreground", className)} />;
}

export function StatusDot({ status, unread, className }: { status?: string; unread?: boolean; className?: string }) {
  const c =
    status === "waiting"
      ? "bg-bad animate-pulse"
      : status === "running"
        ? "bg-warn animate-pulse"
        : status === "error"
          ? "bg-bad"
          : unread
            ? "bg-ok"
            : status === "starting"
              ? "bg-muted-foreground/50"
              : "bg-transparent";
  return <span className={cn("inline-block size-2 shrink-0 rounded-full", c, className)} />;
}
