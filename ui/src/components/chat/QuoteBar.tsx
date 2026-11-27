import { Check, Quote, X } from "lucide-react";
import { useState, type MouseEvent } from "react";
import { composerSelection, setComposerText } from "@/lib/composerText";
import { t } from "@/lib/i18n";
import { isCoarse } from "@/lib/phone";
import { dismissQuote, useQuoteSuggestion } from "@/lib/selectionQuote";
import { get, useStore } from "@/lib/store";

export function QuoteBar({ sessionId }: { sessionId: string }) {
  const suggestion = useQuoteSuggestion((s) => s[sessionId]);
  const [inserted, setInserted] = useState<{ paneId: string; text: string; block: string }>();
  const confirmed = useStore((s) => !!inserted && !!s.drafts[inserted.paneId]?.includes(inserted.block));
  const preview = suggestion ?? (confirmed ? inserted?.text : undefined);
  if (!preview) return null;

  const insert = (e: MouseEvent<HTMLButtonElement>) => {
    if (!suggestion) return;
    const box = e.currentTarget.closest("[data-gerdr-chat]")?.querySelector<HTMLTextAreaElement>("textarea[data-composer]");
    const paneId = box?.dataset.composer;
    if (!box || !paneId || box.dataset.session !== sessionId) return;
    const draft = get().drafts[paneId] ?? "";
    const { end } = composerSelection(paneId);
    // Inserting a quote into a shell draft makes it chat text. The Markdown escape keeps the bang visible.
    const before = draft.slice(0, end).replace(/^!/, "\\!");
    const after = draft.slice(end);
    const gap = !before || before.endsWith("\n\n") ? "" : before.endsWith("\n") ? "\n" : "\n\n";
    const block = suggestion
      .trimEnd()
      .split(/\r?\n/)
      .map((line) => `> ${line}`)
      .join("\n");
    const head = `${before}${gap}${block}\n\n`;
    if (!isCoarse()) box.focus({ preventScroll: true });
    setComposerText(paneId, head + after, head.length);
    setInserted({ paneId, text: suggestion, block });
    dismissQuote(sessionId);
  };

  return (
    <div className="mb-2 flex min-w-0 max-w-full items-center gap-1 rounded-md bg-muted px-1 text-xs text-muted-foreground">
      {suggestion ? (
        <>
          <button
            type="button"
            className="flex min-w-0 flex-1 items-center gap-2 rounded-sm px-2 py-1.5 text-left hover:bg-accent hover:text-foreground"
            onClick={insert}
            title={preview}
          >
            <Quote className="size-3.5 shrink-0" />
            <span className="min-w-0 flex-1 truncate">{preview}</span>
            <span className="shrink-0 text-brand">{t("quoteBar.insert")}</span>
          </button>
          <button
            type="button"
            className="shrink-0 rounded-sm p-1.5 hover:bg-accent hover:text-foreground"
            aria-label={t("quoteBar.dismiss")}
            title={t("quoteBar.dismiss")}
            onClick={() => dismissQuote(sessionId)}
          >
            <X className="size-3.5" />
          </button>
        </>
      ) : (
        <div role="status" className="flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5" title={preview}>
          <Check className="size-3.5 shrink-0 text-brand" />
          <span className="min-w-0 flex-1 truncate">{preview}</span>
          <span className="shrink-0">{t("quoteBar.inserted")}</span>
        </div>
      )}
    </div>
  );
}
