import { cn } from "@/lib/utils";
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandShortcut } from "@/components/ui/command";
import { basename, focusSession } from "@/lib/actions";
import { allCommands, runCommand } from "@/lib/commands";
import { formatCombo } from "@/lib/keys";
import { currentSpace, locateSession, spaceOf } from "@/lib/layout";
import { panelTitle } from "@/lib/panels";
import { panelOfKey, spaceOfKey, useWorkProject } from "@/lib/work";
import { usePanelSearches, type SearchItem } from "@/core/search";
import { closeDialog, setMode, setWorkPanel, useStore, useStoreWhile } from "@/lib/store";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { t } from "@/lib/i18n";

// A panel row's status colour (SearchItem.tone / pill): theme tokens, so it matches how the panel draws that status
const TEXT = { brand: "text-brand", ok: "text-ok", warn: "text-warn", bad: "text-bad" } as const;
const PILL = { brand: "bg-brand", ok: "bg-ok", warn: "bg-warn", bad: "bg-bad" } as const;

// The command palette (prefix+: / ⌘K): the shown panel's own rows (useSearchItems in @gerdr/core), the conversations
// open in a pane, the project's other panels' rows, then commands. Conversations before commands: jumping to one is what ⌘K is mostly for (the person,
// 2026-11-26).
export function Palette({ open }: { open: boolean }) {
  useStore((s) => s.addonVersion);
  // Mounted all the time (Dialogs.tsx): only follow sessions / layout while open
  const sessions = useStoreWhile(open, (s) => s.sessions);
  const layout = useStoreWhile(open, (s) => s.layout);
  const cmds = open ? allCommands() : [];
  const panelKey = useWorkProject();
  const inPanel = useStoreWhile(open, (s) => s.mode === "work");
  // Every panel of the project on screen gives its rows (useSearchItems), wherever ⌘K is pressed: the shown panel's
  // first, the others after the conversations, under "<panel> · <heading>". ⌘K in Code found the Superagent's
  // conversations but not its tasks, while ⌘K over it found Code's (the person, 2026-11-26). Another project's
  // panels stay out, like its conversations
  const space = currentSpace(layout);
  const groups = usePanelSearches(open).filter(([k, e]) => spaceOfKey(k) === space && e.items.length > 0);
  const here = inPanel ? groups.find(([k]) => k === panelKey) : undefined;
  const others = groups.filter((g) => g !== here);
  // A row of a panel not on screen: show that panel first, then let it open the row as it would its own
  const pick = (key: string, x: SearchItem, shown: boolean) => {
    closeDialog();
    if (!shown) (setWorkPanel(spaceOfKey(key), panelOfKey(key)), setMode("work"));
    setTimeout(() => x.onSelect(), 0);
  };
  const panelGroup = ([key, e]: [string, { heading?: string; items: SearchItem[] }], shown: boolean) => {
    const title = panelTitle(key);
    const heading = shown ? e.heading || title || t("palette.panel") : [title, e.heading].filter(Boolean).join(" · ") || t("palette.panel");
    return (
      <CommandGroup key={key} heading={heading}>
        {e.items.map((x, i) => (
          <CommandItem
            key={`${x.id ?? x.label}\u0000${i}`}
            value={`${x.label} ${x.detail ?? ""} ${x.keywords ?? ""} ${x.id ?? i}`}
            onSelect={() => pick(key, x, shown)}
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
    );
  };
  return (
    <CommandDialog
      open={open}
      onOpenChange={(o) => !o && closeDialog()}
      title={t("palette.title")}
      description={t("palette.description")}
      // Wider and taller than the stock 512 × 300px: a panel's rows are two lines (a board's task with its line under it),
      // and at that size four of them filled the list (the person, 2026-11-26)
      className="sm:max-w-2xl"
    >
      <CommandInput placeholder={t("palette.placeholder")} />
      <CommandList className="max-h-[min(65vh,600px)]">
        <CommandEmpty>{t("palette.empty")}</CommandEmpty>
        {here && panelGroup(here, true)}
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
        {others.map((x) => panelGroup(x, false))}
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
