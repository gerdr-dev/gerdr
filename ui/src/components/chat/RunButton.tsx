import { ChevronDown, Play, SquareTerminal, Terminal } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { basename, runInTerminal, terminalNear } from "@/lib/actions";
import type { PaneLeaf } from "@/lib/layout";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import type { ChatSession } from "@gerdr/chat";
import { t } from "@/lib/i18n";

// gerdr's actions on a `! command` in a reply (ChatHost.codeActions): the chat package draws the command, gerdr adds the
// button that runs it, since only gerdr can (hub/shell.go, its terminals).

/**
 * 「运行」：主按钮在对话里跑 —— 等于在输入框里敲 `!命令`，gerdr 在会话目录里直接跑，输出进对话、跟着下一条消息带给 agent（hub/shell.go）。
 * 旁边的箭头还能放进终端跑（新开一个 / 会话旁边已经开着的那个）：终端能交互（选选项、输密码），输出不进对话，agent 要自己用 `gerdr pane read` 去读（ShellHint 告诉了它）。
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
      <button title={t("runButton.title", { cmd, cwd: session.cwd })} disabled={busy} className={cn(box, "gap-0.5 px-1")} onClick={inChat}>
        <Play className="size-2.5 fill-current" />
        {t("runButton.run")}
      </button>
      <DropdownMenu onOpenChange={(o) => o && setNear(terminalNear(session.id))}>
        <DropdownMenuTrigger asChild>
          <button title={t("runButton.elsewhere")} className={cn(box, "border-l border-brand/40 px-0.5")}>
            <ChevronDown className="size-3" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-72">
          <DropdownMenuItem onSelect={inChat} className="flex-col items-start gap-0">
            <span className="flex items-center gap-1.5">
              <Play className="size-3.5" />
              {t("runButton.inChat")}
            </span>
            <span className="pl-5 text-xs text-muted-foreground">{t("runButton.inChatHint")}</span>
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => runInTerminal(session.id, session.cwd, cmd, "new")} className="flex-col items-start gap-0">
            <span className="flex items-center gap-1.5">
              <SquareTerminal className="size-3.5" />
              {t("runButton.newTerminal")}
            </span>
            <span className="pl-5 text-xs text-muted-foreground">{t("runButton.newTerminalHint")}</span>
          </DropdownMenuItem>
          {near?.term && (
            <DropdownMenuItem onSelect={() => runInTerminal(session.id, session.cwd, cmd, "near")} className="flex-col items-start gap-0">
              <span className="flex items-center gap-1.5">
                <Terminal className="size-3.5" />
                {t("runButton.nearTerminal")}
              </span>
              <span className="max-w-full truncate pl-5 text-xs text-muted-foreground">
                {t("runButton.nearTerminalHint", { dir: basename(near.term.cwd) })}
              </span>
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </span>
  );
}
