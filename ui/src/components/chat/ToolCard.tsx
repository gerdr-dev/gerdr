import { ClipboardList } from "lucide-react";
import { Markdown, type ToolPart } from "@gerdr/chat";
import { cn } from "@/lib/utils";

/**
 * A tool call whose result asks to be seen: a Work tool (cmd/gerdr/workmcp.go) returns `{ gerdrCard: { title, body,
 * tone } }` when what it did is something the user should notice in the chat, e.g. a step put on their board. The
 * card replaces the usual folded "called a tool" row.
 */
export type Card = { title: string; body?: string; tone?: "warn" | "info" | "ok" };

/** The card in a finished tool call's result, if it has one. */
export function cardOf(part: ToolPart): Card | undefined {
  if (part.state !== "output-available") return undefined;
  for (const c of part.toolMetadata?.content ?? []) {
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

export function ToolCard({ card }: { card: Card }) {
  return (
    <div
      className={cn(
        "my-1 space-y-1.5 rounded-lg border p-3",
        card.tone === "warn" ? "border-warn/50 bg-warn/5" : card.tone === "ok" ? "border-ok/40 bg-ok/5" : "bg-card",
      )}
    >
      <div className={cn("flex items-center gap-1.5 text-[13px] font-medium", card.tone === "warn" && "text-warn")}>
        <ClipboardList className="size-3.5 shrink-0" />
        {card.title}
      </div>
      {card.body && (
        <div className="text-[13px]">
          <Markdown>{card.body}</Markdown>
        </div>
      )}
    </div>
  );
}
