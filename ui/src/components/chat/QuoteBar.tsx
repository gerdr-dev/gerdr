import { Quote, X } from "lucide-react";
import { t } from "@/lib/i18n";
import { dismissQuote, insertSelectionQuote, useQuoteSuggestion } from "@/lib/selectionQuote";

export function QuoteBar({ sessionId }: { sessionId: string }) {
  const suggestion = useQuoteSuggestion((s) => s[sessionId]);
  if (!suggestion) return null;

  return (
    <div data-quote-bar="" className="relative">
      <div className="absolute inset-x-0 bottom-2 z-20 flex min-w-0 items-center gap-1 rounded-md border bg-popover px-1 text-xs text-popover-foreground shadow-md">
        <button
          type="button"
          className="flex min-w-0 flex-1 items-center gap-2 rounded-sm px-2 py-2 text-left hover:bg-accent"
          onClick={(e) => {
            const box = e.currentTarget.closest("[data-gerdr-chat]")?.querySelector<HTMLTextAreaElement>("textarea[data-composer]");
            insertSelectionQuote(box, sessionId, suggestion);
          }}
          title={suggestion}
        >
          <Quote className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate">{suggestion}</span>
          <span className="shrink-0 text-brand">{t("quoteBar.insert")}</span>
        </button>
        <button
          type="button"
          className="shrink-0 rounded-sm p-2 text-muted-foreground hover:bg-accent hover:text-foreground"
          aria-label={t("quoteBar.dismiss")}
          title={t("quoteBar.dismiss")}
          onClick={() => dismissQuote(sessionId)}
        >
          <X className="size-3.5" />
        </button>
      </div>
    </div>
  );
}
