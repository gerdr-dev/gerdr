import { ChevronDown, Play, SquareTerminal, Terminal } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { basename, runInTerminal, terminalNear } from "@/lib/actions";
import type { PaneLeaf } from "@/lib/layout";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import type { ChatSession } from "@gerdr/chat";

// gerdr's actions on a `! command` in a reply (ChatHost.codeActions): the chat package draws the command, gerdr adds the
// button that runs it, since only gerdr can (hub/shell.go, its terminals).

/**
 * 「运行」：主按钮在对话里跑 —— 等于在输入框里敲 `!命令`，gerdr 在会话目录里直接跑，输出进对话、跟着下一条消息带给 agent（hub/shell.go）。
 * 旁边的箭头还能放进终端跑（新开一个 / 会话旁边已经开着的那个）：终端能交互（选选项、输密码），代价是 agent 看不到输出。
 */
export function RunButton({ cmd, session, className }: { cmd: string; session: ChatSession; className?: string }) {
  const [busy, setBusy] = useState(false);
  // 旁边开着的终端：菜单打开时才找（布局一变就让对话里每个按钮重画不值得）
  const [near, setNear] = useState<PaneLeaf | undefined>();
  if (!session) return null;
  const inChat = async () => {
    setBusy(true);
    try {
      await api.shell(session.id, cmd);
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    } finally {
      setBusy(false);
    }
  };
  const box = "inline-flex items-center text-[11px] leading-4 text-brand hover:bg-brand/10 disabled:opacity-50";
  return (
    <span className={cn("inline-flex items-stretch rounded-sm border border-brand/40", className)}>
      <button
        title={`在对话里运行：${cmd}\n目录：${session.cwd}\n输出会跟着你的下一条消息带给 agent`}
        disabled={busy}
        className={cn(box, "gap-0.5 px-1")}
        onClick={inChat}
      >
        <Play className="size-2.5 fill-current" />
        运行
      </button>
      <DropdownMenu onOpenChange={(o) => o && setNear(terminalNear(session.id))}>
        <DropdownMenuTrigger asChild>
          <button title="换个地方运行" className={cn(box, "border-l border-brand/40 px-0.5")}>
            <ChevronDown className="size-3" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-72">
          <DropdownMenuItem onSelect={inChat} className="flex-col items-start gap-0">
            <span className="flex items-center gap-1.5">
              <Play className="size-3.5" />
              在对话里运行
            </span>
            <span className="pl-5 text-[11px] text-muted-foreground">输出带给 agent；没有输入，要交互的命令会卡住</span>
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => runInTerminal(session.id, session.cwd, cmd, "new")} className="flex-col items-start gap-0">
            <span className="flex items-center gap-1.5">
              <SquareTerminal className="size-3.5" />
              在新终端里运行
            </span>
            <span className="pl-5 text-[11px] text-muted-foreground">在右边开一个终端，能交互；agent 看不到输出</span>
          </DropdownMenuItem>
          {near?.term && (
            <DropdownMenuItem onSelect={() => runInTerminal(session.id, session.cwd, cmd, "near")} className="flex-col items-start gap-0">
              <span className="flex items-center gap-1.5">
                <Terminal className="size-3.5" />
                在当前终端里运行
              </span>
              <span className="max-w-full truncate pl-5 text-[11px] text-muted-foreground">敲进旁边开着的终端（{basename(near.term.cwd)}）</span>
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </span>
  );
}
