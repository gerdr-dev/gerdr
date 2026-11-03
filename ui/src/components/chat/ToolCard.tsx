import { useState } from "react";
import { Check, CircleHelp, ClipboardList, Loader2, Send } from "lucide-react";
import { Markdown, type GPart, type ToolPart } from "@gerdr/chat";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";

/**
 * A tool call whose result asks to be seen: a Work tool (cmd/gerdr/workmcp.go) returns `{ gerdrCard: … }` when what
 * it did is something the user should notice in the chat, e.g. a step put on their board. The card replaces the
 * usual folded "called a tool" row.
 *
 * `ref` names it, so a page can point the conversation at it (WorkPeek's jump). `choose` makes it answerable right
 * here: the user picks an option (or writes their own) and the card runs `fn` of that Work project with
 * `{ ...input, [field]: answer }`, the same call the page would make, so answering in the chat or on the board is one
 * thing. The chat doesn't wait on it: the agent's turn ended when it asked.
 */
export type Card = {
  title: string;
  body?: string;
  tone?: "warn" | "info" | "ok";
  ref?: string;
  choose?: { project: string; fn: string; input?: Record<string, unknown>; field: string; options: string[]; other?: boolean };
};

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
      if (v && typeof v.title === "string") return v;
    } catch {
      // not JSON: an ordinary result
    }
  }
  return undefined;
}

// Answers given in this page, by card: a remounted card (the virtual list) still shows what was picked
const answered = new Map<string, string>();

export function ToolCard({ card }: { card: Card }) {
  const key = card.ref ?? card.title;
  const [picked, setPicked] = useState(answered.get(key));
  const [own, setOwn] = useState("");
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const ch = card.choose;
  const pick = async (v: string) => {
    if (!ch) return;
    setBusy(v);
    setErr("");
    try {
      await api.workRun(ch.fn, { ...ch.input, [ch.field]: v }, ch.project);
      answered.set(key, v);
      setPicked(v);
    } catch (e: any) {
      setErr(String(e?.message ?? e));
    } finally {
      setBusy("");
    }
  };
  return (
    <div
      className={cn(
        "my-1 space-y-1.5 rounded-lg border p-3",
        picked ? "bg-card" : card.tone === "warn" ? "border-warn/50 bg-warn/5" : card.tone === "ok" ? "border-ok/40 bg-ok/5" : "bg-card",
      )}
    >
      <div className={cn("flex items-center gap-1.5 text-[13px] font-medium", card.tone === "warn" && !picked && "text-warn")}>
        {ch ? <CircleHelp className="size-3.5 shrink-0" /> : <ClipboardList className="size-3.5 shrink-0" />}
        {card.title}
      </div>
      {card.body && (
        <div className="text-[13px]">
          <Markdown>{card.body}</Markdown>
        </div>
      )}
      {ch &&
        (picked ? (
          <div className="flex items-center gap-1.5 text-[13px] text-ok">
            <Check className="size-3.5 shrink-0" />
            {t("toolCard.picked", { v: picked })}
          </div>
        ) : (
          <div className="space-y-1.5">
            {ch.options.map((o, i) => (
              <button
                key={i}
                disabled={!!busy}
                onClick={() => pick(o)}
                className="flex w-full gap-2 rounded-md border bg-background p-2 text-left text-[13px] hover:border-ring hover:bg-accent disabled:opacity-50"
              >
                <span className="shrink-0 font-mono text-muted-foreground">{i + 1}</span>
                <span className="min-w-0 flex-1 whitespace-pre-wrap">{o}</span>
                {busy === o && <Loader2 className="size-3.5 shrink-0 animate-spin" />}
              </button>
            ))}
            {ch.other !== false && (
              <div className="flex gap-1.5">
                <input
                  value={own}
                  onChange={(e) => setOwn(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && own.trim() && !busy && pick(own.trim())}
                  placeholder={t("toolCard.other")}
                  className="h-8 min-w-0 flex-1 rounded-md border bg-background px-2 text-[13px] outline-none focus:ring-2 focus:ring-ring"
                />
                <Button size="sm" disabled={!own.trim() || !!busy} onClick={() => pick(own.trim())}>
                  <Send className="size-3.5" />
                </Button>
              </div>
            )}
          </div>
        ))}
      {err && <p className="text-[12px] text-bad">{err}</p>}
    </div>
  );
}
