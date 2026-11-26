import { cn } from "@/lib/utils";
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandShortcut } from "@/components/ui/command";
import { basename, focusSession } from "@/lib/actions";
import { allCommands, runCommand } from "@/lib/commands";
import { formatCombo } from "@/lib/keys";
import { currentSpace, locateSession, spaceOf } from "@/lib/layout";
import { panelTitle } from "@/lib/panels";
import { useWorkProject } from "@/lib/work";
import { usePanelSearch } from "@/core/search";
import { closeDialog, useStore, useStoreWhile } from "@/lib/store";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { t } from "@/lib/i18n";

// A panel row's status colour (SearchItem.tone / pill): theme tokens, so it matches how the panel draws that status
const TEXT = { brand: "text-brand", ok: "text-ok", warn: "text-warn", bad: "text-bad" } as const;
const PILL = { brand: "bg-brand", ok: "bg-ok", warn: "bg-warn", bad: "bg-bad" } as const;

// The command palette (prefix+: / ⌘K): the shown panel's own rows (useSearchItems in @gerdr/core), the conversations
// open in a pane, then commands. Conversations before commands: jumping to one is what ⌘K is mostly for (the person,
// 2026-11-26).
export function Palette({ open }: { open: boolean }) {
  useStore((s) => s.addonVersion);
  // Mounted all the time (Dialogs.tsx): only follow sessions / layout while open
  const sessions = useStoreWhile(open, (s) => s.sessions);
  const layout = useStoreWhile(open, (s) => s.layout);
  const cmds = open ? allCommands() : [];
  const panelKey = useWorkProject();
  const inPanel = useStoreWhile(open, (s) => s.mode === "work");
  const panel = usePanelSearch(open && inPanel ? panelKey : null);
  return (
    <CommandDialog open={open} onOpenChange={(o) => !o && closeDialog()} title={t("palette.title")} description={t("palette.description")}>
      <CommandInput placeholder={t("palette.placeholder")} />
      <CommandList>
        <CommandEmpty>{t("palette.empty")}</CommandEmpty>
        {panel && panel.items.length > 0 && (
          <CommandGroup heading={panel.heading || panelTitle(panelKey) || t("palette.panel")}>
            {panel.items.map((x, i) => (
              <CommandItem
                key={`${x.id ?? x.label}\u0000${i}`}
                value={`${x.label} ${x.detail ?? ""} ${x.keywords ?? ""} ${x.id ?? i}`}
                onSelect={() => {
                  closeDialog();
                  setTimeout(() => x.onSelect(), 0);
                }}
              >
                {x.content ?? (
                  <>
                    {x.icon}
                    <span className="min-w-0 truncate">{x.label}</span>
                  </>
                )}
                {!x.content && x.detail && (
                  <span
                    className={cn(
                      "ml-auto shrink-0 text-[11px]",
                      x.pill
                        ? cn("rounded-full px-1.5 font-medium text-background", PILL[x.tone ?? "warn"])
                        : cn("pl-3", x.tone ? TEXT[x.tone] : "text-muted-foreground"),
                    )}
                  >
                    {x.detail}
                  </span>
                )}
              </CommandItem>
            ))}
          </CommandGroup>
        )}
        <CommandGroup heading={t("palette.sessions")}>
          {/* Only those open in a pane of the project on screen: a closed pane shut its agent down with it (shutdownGone),
              so finding it here would look like it never closed; another project's read as noise over a panel's own rows
              (the person, 2026-11-26). Every conversation is in prefix+g */}
          {Object.values(sessions)
            .filter((s) => {
              const at = locateSession(layout, s.id);
              return !!at && spaceOf(at.ws) === currentSpace(layout);
            })
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
        <CommandGroup heading={t("palette.commands")}>
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
      </CommandList>
    </CommandDialog>
  );
}
