import { BatteryLow, Check, CircleAlert, Hand, HelpCircle, Inbox as InboxIcon, Settings, ShieldAlert, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api } from "@/lib/api";
import { clearAll, dismiss, kindLabel, markSeen, openInboxItem, sessionName, useInbox, type InboxItem } from "@/lib/inbox";
import { set, useStore } from "@/lib/store";
import type { AskQuestion, PermissionOption } from "@/lib/types";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";

/**
 * The inbox button and its panel: on a phone in the top bar's right corner, on wider screens in the rail (Rail.tsx).
 * Red dot = something came in since it was last opened; items stay until their × is clicked.
 * `rail`: drawn like the rail's other buttons (size-9, 16px icon, tooltip to the right), not the sidebar header's small one.
 */
export function InboxButton({ phone, rail }: { phone?: boolean; rail?: boolean }) {
  const fresh = useInbox((s) => s.fresh);
  const count = useInbox((s) => s.items.length);
  const [open, setOpen] = useState(false);
  const close = useCallback(() => setOpen(false), []);
  const onOpen = (v: boolean) => {
    setOpen(v);
    if (v) markSeen();
  };
  // The phone's top bar is short on room: an empty inbox's button only leads to an empty list. Kept while open, so the
  // popover's anchor doesn't vanish under it (emptying the list closes the panel anyway).
  if (phone && !count && !open) return null;
  // The rail's buttons are plain ones (Rail.tsx railBtn): Button's ghost hover is fainter in dark mode and stood out there
  const trigger = rail ? (
    <button
      aria-label={t("inbox.title")}
      className={cn(
        "relative flex size-9 items-center justify-center rounded-md hover:bg-accent hover:text-foreground",
        open ? "bg-accent text-foreground" : "text-muted-foreground",
      )}
    >
      <InboxIcon className="size-4" />
      {fresh && <span className="absolute top-1.5 right-1.5 size-1.5 rounded-full bg-bad" />}
    </button>
  ) : (
    <Button
      variant={open ? "secondary" : "ghost"}
      size="icon"
      className={cn("relative", phone ? "size-9" : "size-7 text-muted-foreground")}
      aria-label={t("inbox.title")}
    >
      <InboxIcon className={phone ? "size-[18px]" : "size-3.5"} />
      {fresh && <span className={cn("absolute rounded-full bg-bad", phone ? "top-1.5 right-1.5 size-2" : "top-1 right-1 size-1.5")} />}
    </Button>
  );
  return (
    <Popover open={open} onOpenChange={onOpen}>
      {phone ? (
        <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      ) : (
        <Tooltip>
          <TooltipTrigger asChild>
            <PopoverTrigger asChild>{trigger}</PopoverTrigger>
          </TooltipTrigger>
          <TooltipContent side={rail ? "right" : undefined}>{count ? t("inbox.titleN", { n: count }) : t("inbox.title")}</TooltipContent>
        </Tooltip>
      )}
      <PopoverContent
        side={phone ? "bottom" : "right"}
        align={phone ? "end" : "end"}
        collisionPadding={8}
        className="flex max-h-[min(70vh,calc(var(--vvh,100vh)-80px))] w-[min(380px,calc(100vw-16px))] flex-col gap-0 overflow-hidden p-0"
      >
        <InboxPanel close={close} />
      </PopoverContent>
    </Popover>
  );
}

function InboxPanel({ close }: { close: () => void }) {
  const items = useInbox((s) => s.items);
  // Emptied while open (cleared, the last × clicked, the last request handled): close rather than show an empty list.
  // Opening an already-empty inbox still shows the empty note.
  const had = useRef(items.length);
  useEffect(() => {
    if (had.current && !items.length) close();
    had.current = items.length;
  }, [items.length, close]);
  return (
    <>
      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-3">
        <span className="text-[13px] font-medium">{t("inbox.title")}</span>
        <span className="flex-1" />
        {items.length > 1 && (
          <Button variant="ghost" size="sm" className="h-7 px-2 text-[12px] text-muted-foreground" onClick={clearAll}>
            {t("inbox.clearAll")}
          </Button>
        )}
        {/* An icon the size of each item's ×, so the two line up on the right edge */}
        <Button
          variant="ghost"
          size="icon"
          className="size-7 text-muted-foreground"
          aria-label={t("inbox.settings")}
          title={t("inbox.settings")}
          onClick={() => {
            close();
            set({ dialog: { kind: "settings", tab: "inbox" } });
          }}
        >
          <Settings className="size-3.5" />
        </Button>
      </div>
      {items.length ? (
        <div className="min-h-0 flex-1 divide-y overflow-y-auto overscroll-contain">
          {items.map((it) => (
            <Item key={it.id} it={it} close={close} />
          ))}
        </div>
      ) : (
        <div className="px-3 py-8 text-center text-[12px] text-muted-foreground">{t("inbox.empty")}</div>
      )}
    </>
  );
}

function ago(at: number) {
  const s = Math.max(0, (Date.now() - at) / 1000);
  if (s < 60) return t("nav.justNow");
  if (s < 3600) return t("nav.minutesAgo", { n: Math.floor(s / 60) });
  if (s < 86400) return t("nav.hoursAgo", { n: Math.floor(s / 3600) });
  return new Date(at).toLocaleDateString();
}

const ICON = {
  permission: <ShieldAlert className="size-3.5 text-warn" />,
  ask: <HelpCircle className="size-3.5 text-brand" />,
  error: <CircleAlert className="size-3.5 text-bad" />,
  battery: <BatteryLow className="size-3.5 text-warn" />,
  panel: <Hand className="size-3.5 text-warn" />,
};

function Item({ it, close }: { it: InboxItem; close: () => void }) {
  const live = useInbox((s) => (it.reqId && s.live ? !!s.live[it.reqId] : true));
  const go =
    it.sessionId || it.panel
      ? () => {
          close();
          openInboxItem(it);
        }
      : undefined;
  const req = it.request;
  // Simple enough to settle right here: an approval that isn't a plan, a single question
  const inline =
    live && it.reqId && ((it.kind === "permission" && req?.toolCall?.kind !== "switch_mode") || (it.kind === "ask" && req?.questions?.length === 1));
  // The session's current name (it may have been renamed since), falling back to the one saved with the item
  const name = useStore((s) => (it.sessionId && s.sessions[it.sessionId] ? sessionName(s.sessions[it.sessionId]) : undefined));
  // A panel's item says what it is itself ("t3 问你：…"); the conversation's name would only repeat the task
  const head = it.kind === "panel" ? it.title : it.sessionId ? (name ?? it.title) : kindLabel(it.kind);
  // A single question shows itself in the inline answer; battery's level is its title
  const text = it.kind === "battery" ? it.title : inline && it.kind === "ask" ? undefined : it.body;
  return (
    <div
      className={cn("group px-3 py-2.5", go && "cursor-pointer hover:bg-accent/50")}
      onClick={(e) => {
        // Buttons and inline answer controls handle their own clicks without opening the session.
        if ((e.target as HTMLElement).closest("button, input, textarea, select, a, [role='checkbox']")) return;
        go?.();
      }}
    >
      <div className="-my-1 flex items-center gap-1.5 text-[12px]">
        <span className="shrink-0" title={kindLabel(it.kind)}>
          {ICON[it.kind]}
        </span>
        <button
          className={cn("min-w-0 truncate text-left font-medium", go ? "cursor-pointer hover:underline" : "cursor-default")}
          disabled={!go}
          onClick={go}
          title={go ? t("inbox.jumpToAgent") : undefined}
        >
          {head}
        </button>
        <span className="shrink-0 text-muted-foreground/70">· {ago(it.at)}</span>
        <span className="flex-1" />
        <Button variant="ghost" size="icon" className="size-7 shrink-0 text-muted-foreground" aria-label={t("common.remove")} onClick={() => dismiss(it.id)}>
          <X className="size-3.5" />
        </Button>
      </div>
      {text && (
        <div className={cn("mt-1 line-clamp-3 text-[13px] break-all whitespace-pre-wrap", it.kind === "error" && "text-[12px] text-muted-foreground")}>
          {text}
        </div>
      )}
      {it.kind === "battery" && it.body && <div className="mt-0.5 text-[12px] text-muted-foreground">{it.body}</div>}
      {it.reqId && !live && (
        <div className="mt-1 flex items-center gap-1 text-[12px] text-muted-foreground">
          <Check className="size-3 text-ok" /> {t("inbox.handled")}
        </div>
      )}
      {inline && it.kind === "permission" && <Permission it={it} />}
      {inline && it.kind === "ask" && <Ask it={it} q={req.questions[0]} />}
      {live && it.reqId && !inline && (
        <Button size="sm" variant="outline" className="mt-2 h-7 text-[12px]" onClick={go}>
          {it.kind === "ask" ? t("inbox.goAnswer") : t("inbox.goHandle")}
        </Button>
      )}
    </div>
  );
}

async function answer(it: InboxItem, resp: unknown) {
  try {
    await api.answer(it.sessionId!, it.reqId!, resp);
  } catch (e: any) {
    toast.error(e.message);
    throw e;
  }
}

function Permission({ it }: { it: InboxItem }) {
  const [busy, setBusy] = useState(false);
  const opts: PermissionOption[] = it.request.options ?? [];
  return (
    <div className="mt-2 flex flex-wrap gap-1.5">
      {opts.map((o, i) => (
        <Button
          key={o.optionId}
          size="sm"
          className="h-7 px-2.5 text-[12px]"
          variant={o.kind.startsWith("allow") ? (i === 0 ? "default" : "secondary") : "outline"}
          disabled={busy}
          onClick={() => {
            setBusy(true);
            answer(it, { optionId: o.optionId }).catch(() => setBusy(false));
          }}
        >
          {o.name}
        </Button>
      ))}
    </div>
  );
}

/** One question answered in place: a single choice sends on click; multiple choice / a typed answer has a send button. */
function Ask({ it, q }: { it: InboxItem; q: AskQuestion }) {
  const [sel, setSel] = useState<string[]>([]);
  const [other, setOther] = useState("");
  const [busy, setBusy] = useState(false);
  const opts = q.options ?? [];
  const send = (picked: string[]) => {
    const o = other.trim();
    const v = o ? (q.multiSelect ? [...picked, o] : [o]) : picked;
    if (!v.length) return;
    setBusy(true);
    answer(it, { answers: { [q.id]: v } }).catch(() => setBusy(false));
  };
  return (
    <div className="mt-1.5">
      <div className="text-[12px] leading-relaxed">{q.question}</div>
      <div className="mt-1.5 flex flex-col gap-1">
        {opts.map((o) => {
          const on = sel.includes(o.label);
          return (
            <button
              key={o.label}
              disabled={busy}
              className={cn("flex items-center gap-2 rounded-md border px-2 py-1.5 text-left text-[12px] hover:bg-accent", on && "border-brand/60 bg-brand/10")}
              onClick={() => {
                if (!q.multiSelect) return send([o.label]);
                setSel(on ? sel.filter((x) => x !== o.label) : [...sel, o.label]);
              }}
            >
              {q.multiSelect && <Checkbox checked={on} className="pointer-events-none" />}
              <span className="min-w-0 flex-1">
                <span className="font-medium">{o.label}</span>
                {o.description && <span className="ml-1.5 text-muted-foreground">{o.description}</span>}
              </span>
            </button>
          );
        })}
        {q.allowOther && (
          <input
            className="w-full rounded-md border border-dashed bg-transparent px-2 py-1.5 text-[12px] outline-none placeholder:text-muted-foreground/70 focus:border-solid focus:border-ring/60"
            type={q.secret ? "password" : "text"}
            placeholder={opts.length ? t("inbox.otherPlaceholder") : t("inbox.answerPlaceholder")}
            value={other}
            disabled={busy}
            onChange={(e) => setOther(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.nativeEvent.isComposing) send(sel);
            }}
          />
        )}
      </div>
      <div className="mt-1.5 flex justify-end gap-1.5">
        <Button
          size="sm"
          variant="ghost"
          className="h-7 text-[12px]"
          disabled={busy}
          onClick={() => answer(it, { cancelled: true, reason: "skipped" }).catch(() => {})}
        >
          {t("inbox.skip")}
        </Button>
        {(q.multiSelect || q.allowOther || !opts.length) && (
          <Button size="sm" className="h-7 text-[12px]" disabled={busy || (!sel.length && !other.trim())} onClick={() => send(sel)}>
            {t("inbox.submit")}
          </Button>
        )}
      </div>
    </div>
  );
}
