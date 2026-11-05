import { Component, useEffect, useState, type ReactNode } from "react";
import { Check, CircleHelp, ClipboardList, Undo2 } from "lucide-react";
import { Markdown, type GPart, type ToolPart } from "@gerdr/chat";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { useCardType } from "@/lib/cards";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";

/**
 * A tool call whose result asks to be seen: a Work tool (cmd/gerdr/workmcp.go) returns `{ gerdrCard: … }` when what
 * it did is something the user should notice in the chat, e.g. a step put on their board. The card replaces the
 * usual folded "called a tool" row, and looks like the chat's own question card (AskCard) with a yellow edge: it's
 * something waiting on the user.
 *
 * `ref` names it, so a page can point the conversation at it (WorkPeek's jump). `choose` makes it answerable right
 * here: the pick runs `fn` of that Work project with `{ ...input, [field]: answer }`, the same call the page would
 * make. A pick waits UNDO_MS before it's sent, with an undo: once sent, the agent is already acting on it.
 * `multi`: options are ticked rather than sent on click, and a submit button sends the ticked ones (and anything
 * written under "other"), one per line.
 */
export type Card = {
  /** A card type a UI registered (lib/cards.ts): its component draws it; the fields below are the fallback. */
  type?: string;
  title: string;
  /** The small line above the title ("等你决定 · t23"). */
  kicker?: string;
  body?: string;
  tone?: "warn" | "info" | "ok";
  ref?: string;
  /** The panel whose tool made it (its project key), stamped by Gerdr: its type is looked up in that panel's cards. */
  panel?: string;
  choose?: { project: string; fn: string; input?: Record<string, unknown>; field: string; options: string[]; other?: boolean; multi?: boolean };
};

const UNDO_MS = 6000;

/** The card in a finished tool call's result, if it has one. */
export function cardOf(part: GPart | ToolPart): Card | undefined {
  if (part.type !== "dynamic-tool") return undefined;
  const p = part as ToolPart;
  if (p.state !== "output-available") return undefined;
  for (const c of p.toolMetadata?.content ?? []) {
    const text = c.type === "content" && c.content?.type === "text" ? c.content.text : undefined;
    if (!text || !text.includes("gerdrCard")) continue;
    try {
      const v = JSON.parse(text)?.gerdrCard;
      if (v && (typeof v.title === "string" || typeof v.type === "string")) return { title: "", ...v };
    } catch {
      // not JSON: an ordinary result
    }
  }
  return undefined;
}

// Picks by card, outside the component: the chat is a virtual list, so a card scrolled away unmounts, and its pending
// pick must still go out (or stay undoable) when it comes back
type Pick = { v: string; at: number; sent?: boolean; err?: string; timer?: ReturnType<typeof setTimeout> };
const picks = new Map<string, Pick>();

function send(key: string, card: Card, v: string) {
  const ch = card.choose!;
  const p: Pick = { v, at: Date.now() };
  p.timer = setTimeout(() => {
    api.workRun(ch.fn, { ...ch.input, [ch.field]: v }, ch.project).then(
      () => picks.set(key, { ...p, sent: true }),
      (e) => picks.set(key, { ...p, err: String(e?.message ?? e) }),
    );
  }, UNDO_MS);
  picks.set(key, p);
}

/** "6 秒" left to undo, re-rendered every second while a pick waits. */
function usePick(key: string) {
  const [, tick] = useState(0);
  const p = picks.get(key);
  const waiting = !!p && !p.sent && !p.err;
  useEffect(() => {
    if (!waiting) return;
    const id = setInterval(() => tick((n) => n + 1), 500);
    return () => clearInterval(id);
  }, [waiting]);
  return { p, rerender: () => tick((n) => n + 1) };
}

/** "label：what it costs" → the label bold, the rest muted, like the question card's options. */
function split(o: string): [string, string] {
  const i = o.search(/[：:]/);
  return i > 0 && i < 40 ? [o.slice(0, i), o.slice(i + 1).trim()] : [o, ""];
}

/** A registered card type draws the card; the built-in card when there's none or it throws. */
export function ToolCard({ card, session }: { card: Card; session: string }) {
  const Custom = useCardType(card.type, card.panel);
  const fallback = <BuiltinCard card={card} />;
  if (!Custom) return fallback;
  return (
    <CardBoundary key={card.type} fallback={fallback}>
      <Custom card={card as Card & Record<string, unknown>} session={session} />
    </CardBoundary>
  );
}

class CardBoundary extends Component<{ fallback: ReactNode; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  componentDidCatch(e: unknown) {
    console.error("[gerdr] card failed, drawing the built-in one", e);
  }
  render() {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}

/** The built-in card (title, body, answerable choices with undo). core exports it as DefaultCard, for a card type to wrap. */
export function BuiltinCard({ card }: { card: Card }) {
  const key = card.ref ?? card.title;
  const { p, rerender } = usePick(key);
  const [own, setOwn] = useState("");
  const ch = card.choose;
  const waiting = p && !p.sent && !p.err;
  const left = waiting ? Math.max(0, Math.ceil((p.at + UNDO_MS - Date.now()) / 1000)) : 0;
  const [ticked, setTicked] = useState<string[]>([]);
  const multi = !!ch?.multi;
  const choose = (v: string) => {
    if (p && !p.err) return;
    send(key, card, v);
    rerender();
  };
  // A multiple choice: a click ticks; the submit button sends the ticked ones (in the options' order) plus what's written
  const tick = (o: string) => setTicked((x) => (x.includes(o) ? x.filter((y) => y !== o) : [...x, o]));
  const picked = [...(ch?.options.filter((o) => ticked.includes(o)) ?? []), ...(own.trim() ? [own.trim()] : [])];
  const submit = () => picked.length && choose(picked.join("\n"));
  // What was sent beyond the options (written under "other"), shown under them
  const extra =
    p &&
    (multi
      ? p.v
          .split("\n")
          .filter((x) => !ch?.options.includes(x))
          .join("\n")
      : ch?.options.includes(p.v)
        ? ""
        : p.v);
  const undo = () => {
    if (p?.timer) clearTimeout(p.timer);
    picks.delete(key);
    rerender();
  };
  const open = !!ch && (!p || !!p.err);
  return (
    <div className={cn("my-1.5 rounded-xl border border-l-2 bg-card p-4 shadow-sm", p?.sent ? "border-l-ok" : "border-l-warn")}>
      <div className="mb-1.5 flex items-center gap-1.5 text-[12px] text-muted-foreground">
        {ch ? <CircleHelp className="size-3.5 text-warn" /> : <ClipboardList className="size-3.5 text-warn" />}
        <span>{card.kicker ?? (ch ? t("toolCard.decide") : t("toolCard.todo"))}</span>
      </div>
      <div className="text-[1em] leading-relaxed font-medium">{card.title}</div>
      {card.body && (
        <div className="mt-1 text-[1em] text-muted-foreground">
          <Markdown>{card.body}</Markdown>
        </div>
      )}
      {ch && (
        <div className="-mx-2.5 mt-3 flex flex-col gap-1.5">
          {ch.options.map((o, n) => {
            const [label, desc] = split(o);
            const on = multi ? (p ? p.v.split("\n").includes(o) : ticked.includes(o)) : p?.v === o;
            return (
              <button
                key={n}
                disabled={!open}
                onClick={() => (multi ? tick(o) : choose(o))}
                className={cn(
                  "flex w-full items-center gap-3 rounded-lg px-2.5 py-2.5 text-left text-[1em] transition-colors",
                  open && "hover:bg-muted/60",
                  on && "bg-warn/10",
                  !open && !on && "opacity-50",
                )}
              >
                <span className="min-w-0 flex-1">
                  <span className="font-medium">{label}</span>
                  {desc && <span className="ml-1.5 text-muted-foreground">{desc}</span>}
                </span>
                <span
                  className={cn(
                    "flex size-5 shrink-0 items-center justify-center rounded-md border text-[11px] text-muted-foreground tabular-nums",
                    on && "border-transparent bg-warn/20 text-warn",
                  )}
                >
                  {on ? <Check className="size-3" /> : n + 1}
                </span>
              </button>
            );
          })}
          {ch.other !== false && open && (
            <input
              value={own}
              onChange={(e) => setOwn(e.target.value)}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing || e.keyCode === 229) return;
                if (e.key === "Enter" && own.trim()) multi ? submit() : choose(own.trim());
              }}
              placeholder={ch.options.length ? t("toolCard.other") : t("toolCard.answer")}
              className="w-full rounded-lg border border-dashed bg-transparent px-2.5 py-2 text-[1em] outline-none placeholder:text-muted-foreground/70 focus:border-solid focus:border-ring/60"
            />
          )}
          {multi && open && (
            <Button size="sm" className="self-end" disabled={!picked.length} onClick={submit}>
              <Check className="size-3.5" />
              {t("toolCard.submit", { n: picked.length })}
            </Button>
          )}
          {extra && (
            <div className="flex items-center gap-3 rounded-lg bg-warn/10 px-2.5 py-2.5 text-[1em]">
              <span className="min-w-0 flex-1">{extra}</span>
              <Check className="size-3.5 text-warn" />
            </div>
          )}
        </div>
      )}
      {waiting && (
        <div className="mt-3 flex items-center gap-2 text-[12px] text-muted-foreground">
          <span className="flex-1">{t("toolCard.sending", { n: left })}</span>
          <Button size="sm" variant="outline" className="h-7 text-[12px]" onClick={undo}>
            <Undo2 className="size-3.5" />
            {t("toolCard.undo")}
          </Button>
        </div>
      )}
      {p?.sent && <div className="mt-3 text-[12px] text-ok">{t("toolCard.sent")}</div>}
      {p?.err && <div className="mt-3 text-[12px] text-bad">{p.err}</div>}
    </div>
  );
}
