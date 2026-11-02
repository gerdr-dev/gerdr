import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { basename, focusSession, openSessionHere, selectWorkspace, tabName } from "@/lib/actions";
import { currentSpace, locateSession, spaceList, spaceOf, spaceWorkspaces } from "@/lib/layout";
import { closeDialog, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { ago } from "./NewSession";
import { t } from "@/lib/i18n";

// Pick-one-and-go lists built on CommandDialog: every conversation (prefix+g) and the current session's workspaces (prefix+w).

// 会话列表（prefix+g）：和命令面板一个样子。「开着的」= 界面上有 pane 显示它；关掉 pane 就是关掉会话（和 herdr 一样），
// 在「关掉的」里，点一下在当前位置接着聊（历史都在）。
export function SessionsDialog({ open }: { open: boolean }) {
  const sessions = useStore((s) => s.sessions);
  const layout = useStore((s) => s.layout);
  const unread = useStore((s) => s.unread);
  const all = Object.values(sessions).sort((a, b) => b.updatedAt - a.updatedAt);
  const isOpen = (id: string) => !!locateSession(layout, id);
  // Open in another (herdr-style) session: listed apart with that session's name; picking one switches over to it
  const here = currentSpace(layout);
  const spaceName = (id: string) => {
    const loc = locateSession(layout, id);
    const sp = loc && spaceOf(loc.ws);
    return sp && sp !== here ? (spaceList(layout).find((x) => x.id === sp)?.name ?? sp) : undefined;
  };
  const opened = all.filter((s) => isOpen(s.id) && !spaceName(s.id));
  const elsewhere = all.filter((s) => isOpen(s.id) && spaceName(s.id));
  const closed = all.filter((s) => !isOpen(s.id));
  const row = (s: SessionSummary, shown: boolean, space?: string) => (
    <CommandItem
      key={s.id}
      value={`${s.id} ${s.title ?? ""} ${s.cwd} ${s.agent}`}
      className="group gap-2.5"
      onSelect={() => {
        closeDialog();
        if (shown) focusSession(s.id);
        else openSessionHere(s.id);
      }}
    >
      <AgentIcon agent={s.agent} className="size-4 shrink-0" />
      <span className="min-w-0 truncate">{s.title || basename(s.cwd)}</span>
      <span className="min-w-0 shrink truncate font-mono text-[11px] text-muted-foreground">{basename(s.cwd)}</span>
      <span className="ml-auto flex shrink-0 items-center gap-2">
        {space && <span className="max-w-24 truncate rounded-sm bg-muted px-1 text-[11px] text-muted-foreground">{space}</span>}
        <StatusDot status={dotStatus(s)} unread={!!unread[s.id]} />
        <span className="w-16 text-right text-[11px] text-muted-foreground">{ago(s.updatedAt)}</span>
      </span>
    </CommandItem>
  );
  return (
    <CommandDialog
      open={open}
      onOpenChange={(o) => !o && closeDialog()}
      title={t("pickers.sessions")}
      description={t("pickers.allSessions")}
      className="max-w-2xl"
    >
      <CommandInput placeholder={t("pickers.searchSessions")} />
      <CommandList className="max-h-[60vh]">
        <CommandEmpty>{t("pickers.none")}</CommandEmpty>
        {opened.length > 0 && <CommandGroup heading={t("pickers.opened", { n: opened.length })}>{opened.map((s) => row(s, true))}</CommandGroup>}
        {elsewhere.length > 0 && (
          <CommandGroup heading={t("pickers.elsewhere", { n: elsewhere.length })}>{elsewhere.map((s) => row(s, true, spaceName(s.id)))}</CommandGroup>
        )}
        {closed.length > 0 && <CommandGroup heading={t("pickers.closed", { n: closed.length })}>{closed.map((s) => row(s, false))}</CommandGroup>}
      </CommandList>
    </CommandDialog>
  );
}

export function WorkspacesDialog({ open }: { open: boolean }) {
  const layout = useStore((s) => s.layout);
  const list = spaceWorkspaces(layout); // only the current session's, like the sidebar
  return (
    <CommandDialog open={open} onOpenChange={(o) => !o && closeDialog()} title={t("pickers.workspaces")} description={t("pickers.switchWorkspace")}>
      <CommandInput placeholder={t("pickers.workspacePlaceholder")} />
      <CommandList>
        <CommandEmpty>{t("pickers.none")}</CommandEmpty>
        <CommandGroup>
          {list.map((w, i) => (
            <CommandItem
              key={w.id}
              value={`${i + 1} ${w.name}`}
              onSelect={() => {
                closeDialog();
                selectWorkspace(w.id);
              }}
            >
              <span className="w-4 font-mono text-[11px] text-muted-foreground">{i + 1}</span>
              {w.name}
              <span className="text-[11px] text-muted-foreground">{w.tabs.map(tabName).join(" · ")}</span>
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
