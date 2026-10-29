import { CircleX, Loader2, RotateCw, Square } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { GData } from "@/lib/transcript";
import { cn } from "@/lib/utils";

// 去掉 ANSI 颜色 / 光标控制（输出是给终端看的，这里只画字）；\r 覆盖的进度条只留最后一版
const plain = (s: string) =>
  s
    .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, "")
    .replace(/\x1b\][^\x07]*(\x07|\x1b\\)/g, "")
    .split("\n")
    .map((l) => l.slice(l.lastIndexOf("\r", l.length - 2) + 1).replace(/\r$/, ""))
    .join("\n");

/**
 * 输入框里 `!命令` 跑出来的那张卡片（gerdr 自己跑的，不经过 agent，见 hub/shell.go）：
 * 头一行是命令（和 Claude Code 里一样写成 `! 命令`），下面是输出。在跑时输出跟着滚到底、能停；跑完能再跑一次。
 * 输出会跟着人的下一条消息带给 agent，卡片上不用再说 —— 输入框里打 ! 时提示过了。
 */
export function ShellCard({ sessionId, data }: { sessionId: string; data: GData["shell"] }) {
  const out = plain(data.output).replace(/\n+$/, "");
  const pre = useRef<HTMLPreElement>(null);
  const [busy, setBusy] = useState(false);
  // 在跑时跟着输出滚到底（人往上翻过就不跟了：离底部超过一行算翻过）
  useLayoutEffect(() => {
    const el = pre.current;
    if (el && data.running && el.scrollHeight - el.scrollTop - el.clientHeight < 40) el.scrollTop = el.scrollHeight;
  }, [out, data.running]);
  const failed = !data.running && (data.end === "stopped" || data.end === "lost" || !!data.error || (data.exit ?? 0) !== 0);
  const status = data.running
    ? null
    : data.end === "stopped"
      ? "已停止"
      : data.end === "lost"
        ? "gerdr 重启，没跑完"
        : data.error
          ? data.error
          : (data.exit ?? 0) !== 0
            ? `退出码 ${data.exit}`
            : null;
  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await fn();
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="group/shell -ml-1.5 flex min-w-0 flex-col gap-1 px-1.5 py-[3px]">
      <div className="flex min-w-0 items-center gap-2 text-[13px]">
        {data.running ? (
          <Loader2 className="size-3.5 shrink-0 animate-spin text-muted-foreground" />
        ) : failed ? (
          <CircleX className="size-3.5 shrink-0 text-bad" />
        ) : (
          <span className="w-3.5 shrink-0 text-center font-mono font-semibold text-brand">!</span>
        )}
        <span className="min-w-0 truncate font-mono text-[12px]" title={data.command}>
          {data.command}
        </span>
        {status && <span className={cn("shrink-0 text-[11px]", failed ? "text-bad" : "text-muted-foreground")}>{status}</span>}
        <span className="flex-1" />
        {data.running ? (
          <button
            className="flex shrink-0 items-center gap-1 rounded-sm px-1.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
            disabled={busy}
            onClick={() => act(() => api.stopShell(sessionId, data.id))}
          >
            <Square className="size-3" />
            停止
          </button>
        ) : (
          <button
            title="在会话目录里再跑一次"
            className="flex shrink-0 items-center gap-1 rounded-sm px-1.5 text-[11px] text-muted-foreground opacity-0 group-hover/shell:opacity-100 hover:bg-muted hover:text-foreground disabled:opacity-50 [@media(hover:none)]:opacity-100"
            disabled={busy}
            onClick={() => act(() => api.shell(sessionId, data.command))}
          >
            <RotateCw className="size-3" />
            再跑一次
          </button>
        )}
      </div>
      {out && (
        <pre
          ref={pre}
          className="ml-5.5 max-h-72 overflow-auto rounded-md bg-muted/60 px-2.5 py-1.5 font-mono text-[12px] leading-relaxed whitespace-pre-wrap break-all text-muted-foreground"
        >
          {out}
        </pre>
      )}
    </div>
  );
}
