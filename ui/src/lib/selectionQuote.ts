import { create } from "zustand";
import { isFeed } from "./feedChat";

// Copy suggestions are local to this page. Confirmed quotes become ordinary text in the session's draft.
export const useQuoteSuggestion = create<Record<string, string | undefined>>(() => ({}));

const scrollerOf = (node: Node | null) => (node instanceof Element ? node : node?.parentElement)?.closest("[data-chat-scroller]");

export function quoteSelection(selection = window.getSelection()) {
  if (!selection || selection.isCollapsed) return;
  const scroller = scrollerOf(selection.anchorNode);
  if (!scroller || scroller !== scrollerOf(selection.focusNode)) return;
  const chat = scroller.closest("[data-gerdr-chat]");
  const id = chat?.querySelector<HTMLTextAreaElement>("textarea[data-composer]")?.dataset.session;
  const text = selection.toString();
  if (id && !isFeed(id) && text.trim()) return { id, text };
}

export function offerQuote(quote: ReturnType<typeof quoteSelection>) {
  if (quote) useQuoteSuggestion.setState({ [quote.id]: quote.text });
}

export function dismissQuote(id: string) {
  useQuoteSuggestion.setState(({ [id]: _, ...rest }) => rest, true);
}
