import { create } from "zustand";
import { composerSelection, setComposerText } from "./composerText";
import { isFeed } from "./feedChat";
import { isCoarse } from "./phone";
import { get } from "./store";

export type CopyPoint = { x: number; y: number };
export type SelectionQuote = { box: HTMLTextAreaElement; id: string; text: string; anchor: CopyPoint };

// Copy suggestions are local to this page. Confirmed quotes become ordinary text in the session's draft.
export const useQuoteSuggestion = create<Record<string, string | undefined>>(() => ({}));

const scrollerOf = (node: Node | null) => (node instanceof Element ? node : node?.parentElement)?.closest("[data-chat-scroller]");

export function quoteSelection(selection = window.getSelection()): SelectionQuote | undefined {
  if (!selection || selection.isCollapsed || !selection.rangeCount) return;
  const scroller = scrollerOf(selection.anchorNode);
  if (!scroller || scroller !== scrollerOf(selection.focusNode)) return;
  const chat = scroller.closest("[data-gerdr-chat]");
  const box = chat?.querySelector<HTMLTextAreaElement>("textarea[data-composer]");
  const id = box?.dataset.session;
  const text = selection.toString();
  if (!box || !id || isFeed(id) || !text.trim()) return;
  const range = selection.getRangeAt(0);
  const end = Array.from(range.getClientRects()).at(-1) ?? range.getBoundingClientRect();
  return { box, id, text, anchor: { x: end.right, y: end.top } };
}

export function offerComposerQuote({ id, text }: SelectionQuote) {
  useQuoteSuggestion.setState({ [id]: text });
}

export function dismissQuote(id: string) {
  useQuoteSuggestion.setState(({ [id]: _, ...rest }) => rest, true);
}

export function insertSelectionQuote(box: HTMLTextAreaElement | null | undefined, id: string, text: string) {
  const paneId = box?.dataset.composer;
  if (!box?.isConnected || !paneId || box.dataset.session !== id) return;
  const draft = get().drafts[paneId] ?? "";
  const { end } = composerSelection(paneId);
  // Inserting a quote into a shell draft makes it chat text. The Markdown escape keeps the bang visible.
  const before = draft.slice(0, end).replace(/^!/, "\\!");
  const after = draft.slice(end);
  const gap = !before || before.endsWith("\n\n") ? "" : before.endsWith("\n") ? "\n" : "\n\n";
  const block = text
    .trimEnd()
    .split(/\r?\n/)
    .map((line) => `> ${line}`)
    .join("\n");
  const head = `${before}${gap}${block}\n\n`;
  if (!isCoarse()) box.focus({ preventScroll: true });
  setComposerText(paneId, head + after, head.length);
  dismissQuote(id);
}
