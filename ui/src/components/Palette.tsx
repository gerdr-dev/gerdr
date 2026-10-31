import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandShortcut } from "@/components/ui/command";
import { basename, focusSession } from "@/lib/actions";
import { allCommands, runCommand } from "@/lib/commands";
import { formatCombo } from "@/lib/keys";
import { locateSession } from "@/lib/layout";
import { closeDialog, useStore } from "@/lib/store";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";

// The command palette (prefix+: / ⌘K): commands, then the conversations open in a pane.
export function Palette({ open }: { open: boolean }) {
  useStore((s) => s.addonVersion);
  const sessions = useStore((s) => s.sessions);
  const layout = useStore((s) => s.layout);
  const cmds = open ? allCommands() : [];
  return (
    <CommandDialog open={open} onOpenChange={(o) => !o && closeDialog()} title="命令面板" description="搜索命令或会话">
      <CommandInput placeholder="命令、会话…" />
      <CommandList>
        <CommandEmpty>没有匹配的</CommandEmpty>
        <CommandGroup heading="命令">
          {cmds.map((c) => (
            <CommandItem
              key={c.id}
              value={`${c.title} ${c.id}`}
              onSelect={() => {
                closeDialog();
                setTimeout(() => runCommand(c.id), 0);
              }}
            >
              {c.title}
              {c.source !== "builtin" && <span className="text-[11px] text-muted-foreground">· {c.source}</span>}
              {c.keys[0] && <CommandShortcut>{formatCombo(c.keys[0])}</CommandShortcut>}
            </CommandItem>
          ))}
        </CommandGroup>
        <CommandGroup heading="会话">
          {/* 只列开在 pane 里的：关掉的 pane 连 agent 一起关了（shutdownGone），这里再搜出来像没关掉。全部会话在 prefix+g */}
          {Object.values(sessions)
            .filter((s) => locateSession(layout, s.id))
            .sort((a, b) => b.updatedAt - a.updatedAt)
            .map((s) => (
              <CommandItem
                key={s.id}
                value={`${s.title ?? ""} ${s.cwd} ${s.id}`}
                onSelect={() => {
                  closeDialog();
                  focusSession(s.id);
                }}
              >
                <AgentIcon agent={s.agent} className="size-4" />
                <span className="truncate">{s.title || basename(s.cwd)}</span>
                <span className="truncate font-mono text-[11px] text-muted-foreground">{basename(s.cwd)}</span>
                <StatusDot status={dotStatus(s)} />
              </CommandItem>
            ))}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
