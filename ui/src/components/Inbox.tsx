import { BatteryLow, Check, CircleAlert, HelpCircle, Inbox as InboxIcon, Settings, ShieldAlert, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { focusSession } from "@/lib/actions";
import { api } from "@/lib/api";
import { clearAll, dismiss, markSeen, sessionName, useInbox, type InboxItem } from "@/lib/inbox";
import { set, useStore } from "@/lib/store";
import type { AskQuestion, PermissionOption } from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * The inbox button and its panel: on a phone in the top bar's right corner, on wider screens in the sidebar's bottom row.
 * Red dot = something came in since it was last opened; items stay until their × is clicked.
 */
export function InboxButton({ phone }: { phone?: boolean }) {
  const fresh = useInbox((s) => s.fresh);
  const count = useInbox((s) => s.items.length);
  const [open, setOpen] = useState(false);
  const onOpen = (v: boolean) => {
    setOpen(v);
    if (v) markSeen();
  };
  // The phone's top bar is short on room: an empty inbox's button only leads to an empty list. Kept while open, so
  // clearing the last item doesn't pull the panel out from under the finger.
  if (phone && !count && !open) return null;
  const trigger = (
    <Button
      variant={open ? "secondary" : "ghost"}
      size="icon"
      className={cn("relative", phone ? "size-9" : "size-7 text-muted-foreground")}
      aria-label="收件箱"
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
          <TooltipContent>收件箱{count ? `（${count}）` : ""}</TooltipContent>
        </Tooltip>
      )}
      <PopoverContent
        side={phone ? "bottom" : "right"}
        align={phone ? "end" : "end"}
        collisionPadding={8}
        className="flex max-h-[min(70vh,calc(var(--vvh,100vh)-80px))] w-[min(380px,calc(100vw-16px))] flex-col gap-0 overflow-hidden p-0"
      >
        <InboxPanel close={() => setOpen(false)} />
      </PopoverContent>
    </Popover>
  );
}

function InboxPanel({ close }: { close: () => void }) {
  const items = useInbox((s) => s.items);
  return (
    <>
      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-3">
        <span className="text-[13px] font-medium">收件箱</span>
        <span className="flex-1" />
        {items.length > 1 && (
          <Button variant="ghost" size="sm" className="h-7 px-2 text-[12px] text-muted-foreground" onClick={clearAll}>
            全部清除
          </Button>
        )}
        {/* An icon the size of each item's ×, so the two line up on the right edge */}
        <Button
          variant="ghost"
          size="icon"
          className="size-7 text-muted-foreground"
          aria-label="收件箱设置"
          title="收件箱设置"
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
        <div className="px-3 py-8 text-center text-[12px] text-muted-foreground">没有新消息。出错、在等你回答的 agent 和电量低都会出现在这里。</div>
      )}
    </>
  );
}

function ago(at: number) {
  const s = Math.max(0, (Date.now() - at) / 1000);
  if (s < 60) return "刚刚";
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return new Date(at).toLocaleDateString();
}

const ICON = {
  permission: <ShieldAlert className="size-3.5 text-warn" />,
  ask: <HelpCircle className="size-3.5 text-brand" />,
  error: <CircleAlert className="size-3.5 text-bad" />,
  battery: <BatteryLow className="size-3.5 text-warn" />,
};
const LABEL = { permission: "等你批准", ask: "在问你", error: "出错了", battery: "电量低" };

function Item({ it, close }: { it: InboxItem; close: () => void }) {
  const live = useInbox((s) => (it.reqId && s.live ? !!s.live[it.reqId] : true));
  const go = it.sessionId
    ? () => {
        close();
        focusSession(it.sessionId!);
      }
    : undefined;
  const req = it.request;
  // Simple enough to settle right here: an approval that isn't a plan, a single question
  const inline =
    live && it.reqId && ((it.kind === "permission" && req?.toolCall?.kind !== "switch_mode") || (it.kind === "ask" && req?.questions?.length === 1));
  // The session's current name (it may have been renamed since), falling back to the one saved with the item
  const name = useStore((s) => (it.sessionId && s.sessions[it.sessionId] ? sessionName(s.sessions[it.sessionId]) : undefined));
  const head = it.sessionId ? (name ?? it.title) : LABEL[it.kind];
  // A single question shows itself in the inline answer; battery's level is its title
  const text = it.kind === "battery" ? it.title : inline && it.kind === "ask" ? undefined : it.body;
  return (
    <div className="group px-3 py-2.5">
      <div className="-my-1 flex items-center gap-1.5 text-[12px]">
        <span className="shrink-0" title={LABEL[it.kind]}>
          {ICON[it.kind]}
        </span>
        <button
          className={cn("min-w-0 truncate text-left font-medium", go ? "cursor-pointer hover:underline" : "cursor-default")}
          disabled={!go}
          onClick={go}
          title={go ? "跳到这个 agent" : undefined}
        >
          {head}
        </button>
        <span className="shrink-0 text-muted-foreground/70">· {ago(it.at)}</span>
        <span className="flex-1" />
        <Button variant="ghost" size="icon" className="size-7 shrink-0 text-muted-foreground" aria-label="移除" onClick={() => dismiss(it.id)}>
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
          <Check className="size-3 text-ok" /> 已处理
        </div>
      )}
      {inline && it.kind === "permission" && <Permission it={it} />}
      {inline && it.kind === "ask" && <Ask it={it} q={req.questions[0]} />}
      {live && it.reqId && !inline && (
        <Button size="sm" variant="outline" className="mt-2 h-7 text-[12px]" onClick={go}>
          {it.kind === "ask" ? "去回答" : "去处理"}
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
            placeholder={opts.length ? "其他…" : "你的回答…"}
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
          跳过
        </Button>
        {(q.multiSelect || q.allowOther || !opts.length) && (
          <Button size="sm" className="h-7 text-[12px]" disabled={busy || (!sel.length && !other.trim())} onClick={() => send(sel)}>
            提交
          </Button>
        )}
      </div>
    </div>
  );
}
