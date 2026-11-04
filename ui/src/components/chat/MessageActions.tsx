import { Ellipsis, Loader2, Puzzle, Square } from "lucide-react";
import { isValidElement, useMemo, useState, type ReactNode } from "react";
import type { MessageInfo } from "@gerdr/chat";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import type { MessageAction } from "@/lib/addons";
import { t } from "@/lib/i18n";
import { actionsFor, lazyMsg, runKey, toggleRun, useAnyRun, useRunPhase, type Msg } from "@/lib/messageActions";
import { lucideIcon } from "@/lib/rail";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

/**
 * Add-ons' buttons on a message (g.messageAction), through ChatHost.messageActions: beside a user's bubble, at the end
 * of a reply's metadata line. One action shows as its own icon; several sit behind a "⋯" so a message doesn't grow a
 * row of icons per add-on installed, with whatever is running pulled out next to it (its stop must stay one tap away).
 */
export function MessageActions({ info }: { info: MessageInfo }) {
  useStore((s) => s.addonVersion); // an action registering or going away
  // info is a new object on each render; the message it describes isn't (a user's message, a finished turn)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const msg = useMemo(() => lazyMsg(info), [info.sessionId, info.id]);
  const prefix = `${info.sessionId}:${info.id}:`;
  useAnyRun(prefix); // re-render when one of ours starts / ends, to pull it out of the ⋯
  const list = actionsFor(msg);
  if (!list.length) return null;
  if (list.length === 1) return <ActionButton a={list[0]} msg={msg} />;
  return (
    <>
      {list.map((a) => (
        <RunningButton key={`${a.addon}:${a.id}`} a={a} msg={msg} />
      ))}
      <More list={list} msg={msg} />
    </>
  );
}

function iconOf(a: MessageAction): ReactNode {
  if (isValidElement(a.icon)) return a.icon;
  const I = lucideIcon(typeof a.icon === "string" ? a.icon : undefined) ?? Puzzle;
  return <I className="size-3.5" />;
}

const btn = "flex size-5 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground [&_svg]:size-3.5";

function ActionButton({ a, msg }: { a: MessageAction; msg: Msg }) {
  const phase = useRunPhase(runKey(msg, a));
  const label = phase ? t("messageActions.stop", { action: a.title }) : a.title;
  return (
    <button type="button" title={label} aria-label={label} className={cn(btn, phase && "text-brand hover:text-brand")} onClick={() => toggleRun(a, msg)}>
      {phase === "busy" ? <Loader2 className="animate-spin" /> : phase === "playing" ? <Square className="!size-3 fill-current" /> : iconOf(a)}
    </button>
  );
}

// Behind the ⋯ an action can't be stopped: while it runs it shows here, outside
function RunningButton({ a, msg }: { a: MessageAction; msg: Msg }) {
  return useRunPhase(runKey(msg, a)) ? <ActionButton a={a} msg={msg} /> : null;
}

function More({ list, msg }: { list: MessageAction[]; msg: Msg }) {
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" title={t("messageActions.more")} aria-label={t("messageActions.more")} className={btn}>
          <Ellipsis />
        </button>
      </PopoverTrigger>
      <PopoverContent side="top" align="start" className="w-auto min-w-36 p-1">
        {list.map((a) => (
          <button
            key={`${a.addon}:${a.id}`}
            type="button"
            className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent hover:text-accent-foreground [&_svg]:size-3.5 [&_svg]:text-muted-foreground"
            onClick={() => {
              setOpen(false);
              toggleRun(a, msg);
            }}
          >
            {iconOf(a)}
            {a.title}
          </button>
        ))}
      </PopoverContent>
    </Popover>
  );
}
