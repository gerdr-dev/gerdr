import { Component, useEffect, useState, type ReactNode } from "react";
import { Check, CircleHelp, ClipboardList, Info, Undo2 } from "lucide-react";
import { ChoiceCard, ChoiceKicker, ChoiceList, ChoiceOther, ChoiceRow, ChoiceTitle, Markdown, type GPart, type ToolPart } from "@gerdr/chat";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { useCardType } from "@/lib/cards";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";

/**
 * A tool call whose result asks to be seen: a Work tool (cmd/gerdr/workmcp.go) returns `{ gerdrCard: … }` when what
 * it did is something the user should notice in the chat, e.g. a step put on their board. The card replaces the
 * usual folded "called a tool" row, and looks like the chat's own question card (AskCard) with a yellow edge: it's
 * something waiting on the user. With tone "info" / "ok" (and no choices) it's a record instead: no yellow edge, no
 * default "要你做" kicker.
 *
 * `ref` names it, so a page can point the conversation at it (WorkPeek's jump). `choose` makes it answerable right
 * here: the pick runs `fn` of that Work project with `{ ...input, [field]: answer }`, the same call the page would
 * make. A pick waits UNDO_MS (or the card type's `undoMs`) before it's sent, with an undo: once sent, the agent is
 * already acting on it.
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

// 3s: long enough to catch a mis-tap, short enough that a deliberate pick doesn't sit waiting (6s felt slow). A panel
// can give its own (DefaultCard's undoMs, e.g. from its settings)
const UNDO_MS = 3000;

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
type Pick = { v: string; at: number; ms: number; sent?: boolean; err?: string; timer?: ReturnType<typeof setTimeout> };
const picks = new Map<string, Pick>();

function send(key: string, card: Card, v: string, ms: number) {
  const ch = card.choose!;
  const p: Pick = { v, at: Date.now(), ms };
  p.timer = setTimeout(() => {
    api.workRun(ch.fn, { ...ch.input, [ch.field]: v }, ch.project).then(
      () => picks.set(key, { ...p, sent: true }),
      (e) => picks.set(key, { ...p, err: String(e?.message ?? e) }),
    );
  }, ms);
  picks.set(key, p);
}

/** "3 秒" left to undo, re-rendered every second while a pick waits. */
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

/** "label：what it costs" → the label, then the rest muted, like the question card's options. */
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

/**
 * How a card's question was settled outside it (its page, an earlier answer), told by the card type that wraps the
 * built-in card: drawn as answered — `value` picked (several: one per line), green, the choices closed — or, with
 * `dropped`, closed and grey. `note` is the line under the choices; `action` sits at its end (e.g. a button to change
 * the answer). A card without choices only changes colour: a line added under it would change its height.
 */
export type CardDone = { value?: string; dropped?: boolean; note?: ReactNode; action?: ReactNode };

/**
 * The built-in card (title, body, answerable choices with undo). core exports it as DefaultCard, for a card type to
 * wrap. Its height doesn't change with its state: open, sending, sent and settled (`done`) all draw the same rows,
 * the last one (the "other" box while open) turning into the status line — a card that shrank when answered moved
 * the chat under the reader's eyes.
 */
export function BuiltinCard({ card, done, undoMs }: { card: Card; done?: CardDone; undoMs?: number }) {
  const ms = undoMs != null && Number.isFinite(undoMs) && undoMs >= 0 ? undoMs : UNDO_MS;
  const key = card.ref ?? card.title;
  const { p, rerender } = usePick(key);
  const [own, setOwn] = useState("");
  const ch = card.choose;
  const waiting = p && !p.sent && !p.err;
  const left = waiting ? Math.max(0, Math.ceil((p.at + p.ms - Date.now()) / 1000)) : 0;
  const [ticked, setTicked] = useState<string[]>([]);
  const multi = !!ch?.multi;
  // Settled outside: wins over a pick made here that went out already (the outside now says the same, or newer)
  const settled = !!done && !waiting && !p?.err;
  const choose = (v: string) => {
    if (settled || (p && !p.err)) return;
    send(key, card, v, ms);
    rerender();
  };
  // A multiple choice: a click ticks; the submit button sends the ticked ones (in the options' order) plus what's written
  const tick = (o: string) => setTicked((x) => (x.includes(o) ? x.filter((y) => y !== o) : [...x, o]));
  const picked = [...(ch?.options.filter((o) => ticked.includes(o)) ?? []), ...(own.trim() ? [own.trim()] : [])];
  const submit = () => picked.length && choose(picked.join("\n"));
  // The answer given: the pick made here, else the one settled outside
  const answer = settled ? (done!.value ?? "") : (p?.v ?? "");
  const given = answer ? (multi ? answer.split("\n") : [answer]) : [];
  // What was answered beyond the options (written under "other"), shown in the status line
  const extra = given.filter((x) => !ch?.options.includes(x)).join("\n");
  const undo = () => {
    if (p?.timer) clearTimeout(p.timer);
    picks.delete(key);
    rerender();
  };
  const open = !!ch && !settled && (!p || !!p.err);
  const ok = settled ? !done!.dropped : !!p?.sent;
  const grey = settled && !!done!.dropped;
  // tone "info" / "ok" without choices: a record of what was done (a memory saved, a ticket approved), nothing to do
  // for the user, so no yellow edge and no "要你做". "warn" and no tone stay the to-do card.
  if (!ch && !done && (card.tone === "info" || card.tone === "ok"))
    return (
      <ChoiceCard className={cn("border-l-2", card.tone === "ok" ? "border-l-ok" : "border-l-border")}>
        {card.kicker && (
          <ChoiceKicker icon={card.tone === "ok" ? <Check className="size-3.5 text-ok" /> : <Info className="size-3.5 text-muted-foreground" />}>
            <span>{card.kicker}</span>
          </ChoiceKicker>
        )}
        <ChoiceTitle>{card.title}</ChoiceTitle>
        {card.body && (
          <div className="mt-1 text-[1em] text-muted-foreground">
            <Markdown>{card.body}</Markdown>
          </div>
        )}
      </ChoiceCard>
    );
  // The last row: the "other" box while open, else where things stand. Same box, same height (ChoiceOther's padding)
  const row = "flex min-w-0 items-center gap-2 rounded-lg border border-transparent px-2.5 py-2 text-[1em] leading-normal";
  return (
    <ChoiceCard className={cn("border-l-2", ok ? "border-l-ok" : grey ? "border-l-muted-foreground/40" : "border-l-warn")}>
      <ChoiceKicker
        icon={
          ok ? (
            <Check className="size-3.5 text-ok" />
          ) : grey ? (
            <Undo2 className="size-3.5" />
          ) : ch ? (
            <CircleHelp className="size-3.5 text-warn" />
          ) : (
            <ClipboardList className="size-3.5 text-warn" />
          )
        }
      >
        <span>{card.kicker ?? (ch ? t("toolCard.decide") : t("toolCard.todo"))}</span>
      </ChoiceKicker>
      <ChoiceTitle className={cn(grey && "text-muted-foreground line-through")}>{card.title}</ChoiceTitle>
      {card.body && (
        <div className="mt-1 text-[1em] text-muted-foreground">
          <Markdown>{card.body}</Markdown>
        </div>
      )}
      {ch && (
        <ChoiceList className="mt-3">
          {ch.options.map((o, n) => {
            const [label, desc] = split(o);
            const on = given.length ? given.includes(o) : multi && ticked.includes(o);
            return (
              <ChoiceRow
                key={n}
                label={label}
                desc={desc}
                n={n}
                on={on}
                tone={ok ? "ok" : "warn"}
                dim={!open && !on}
                disabled={!open}
                onClick={() => (multi ? tick(o) : choose(o))}
              />
            );
          })}
          {open ? (
            ch.other !== false ? (
              <ChoiceOther
                value={own}
                onChange={(e) => setOwn(e.target.value)}
                onKeyDown={(e) => {
                  if (e.nativeEvent.isComposing || e.keyCode === 229) return;
                  if (e.key === "Enter" && own.trim()) multi ? submit() : choose(own.trim());
                }}
                placeholder={ch.options.length ? t("toolCard.other") : t("toolCard.answer")}
              />
            ) : (
              <div className={cn(row, "text-muted-foreground/70")}>{t("toolCard.pick")}</div>
            )
          ) : (
            <div className={cn(row, extra && (ok ? "bg-ok/10" : "bg-warn/10"))}>
              {extra && <span className="min-w-0 flex-1 whitespace-pre-wrap">{extra}</span>}
              <span className={cn("min-w-0 truncate text-[12px]", !extra && "flex-1", waiting || grey ? "text-muted-foreground" : "text-ok")}>
                {waiting ? t("toolCard.sending", { n: left }) : settled ? done!.note : t("toolCard.sent")}
              </span>
              {waiting && (
                <button className="shrink-0 rounded-md px-1.5 text-[12px] text-muted-foreground hover:bg-accent hover:text-foreground" onClick={undo}>
                  <Undo2 className="mr-1 inline size-3" />
                  {t("toolCard.undo")}
                </button>
              )}
              {settled && done!.action}
            </div>
          )}
          {multi && open && (
            <Button size="sm" className="self-end" disabled={!picked.length} onClick={submit}>
              <Check className="size-3.5" />
              {t("toolCard.submit", { n: picked.length })}
            </Button>
          )}
        </ChoiceList>
      )}
      {p?.err && <div className="mt-3 text-[12px] text-bad">{p.err}</div>}
    </ChoiceCard>
  );
}
