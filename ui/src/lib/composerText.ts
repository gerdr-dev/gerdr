// Where add-ons' words go in a composer: at the caret, not after everything typed (voice input landed at the end
// whatever the caret said, reported). Drafts live in the store per pane; the caret only in the pane's textarea, so each
// mounted composer registers its textarea here (chatHost's useExtras).
import { useEffect, type RefObject } from "react";
import { get, set } from "./store";

const boxes = new Map<string, HTMLTextAreaElement>();
const senders = new Map<string, () => void>();

/** Called by every composer: its pane's textarea and its own send (text and attachments), while mounted. */
export function useComposerBox(paneId: string, ta: RefObject<HTMLTextAreaElement | null>, send: () => void) {
  useEffect(() => {
    const el = ta.current;
    if (!el) return;
    boxes.set(paneId, el);
    return () => void (boxes.get(paneId) === el && boxes.delete(paneId));
  }, [paneId]);
  useEffect(() => {
    senders.set(paneId, send);
    return () => void (senders.get(paneId) === send && senders.delete(paneId));
  });
}

/**
 * Send the pane's composer as Enter does, attachments included. false: no composer drawn for the pane (the caller
 * sends the text some other way).
 */
export function sendComposer(paneId: string): boolean {
  const f = senders.get(paneId);
  f?.();
  return !!f;
}

const draft = (paneId: string) => get().drafts[paneId] ?? "";

/**
 * The caret / selection in the pane's draft. A textarea keeps its selection when it loses focus (a click on a button
 * in the composer's bar), so it's read either way. No composer on screen: the end.
 */
export function composerSelection(paneId: string) {
  const t = draft(paneId);
  const el = boxes.get(paneId);
  if (!el || el.value !== t) return { start: t.length, end: t.length };
  return { start: el.selectionStart, end: el.selectionEnd };
}

/**
 * Replace the pane's draft, the caret put at `caret` (default: the end). Setting a textarea's value sends its caret to
 * the end, so it's put back after React has written the new text. Not on an unfocused touch screen: moving the
 * selection of an unfocused field can pop the keyboard up on iOS (same rule as the shared draft, chatHost).
 */
export function setComposerText(paneId: string, text: string, caret?: number) {
  set((s) => ({ drafts: { ...s.drafts, [paneId]: text } }));
  const el = boxes.get(paneId);
  if (!el || caret === undefined) return;
  if (document.activeElement !== el && window.matchMedia("(pointer: coarse)").matches) return;
  const at = Math.max(0, Math.min(caret, text.length));
  requestAnimationFrame(() => el.value === text && el.setSelectionRange(at, at));
}

/** Put text in at the caret (over the selection, if any), the caret after it. */
export function insertComposerText(paneId: string, s: string) {
  const t = draft(paneId);
  const { start, end } = composerSelection(paneId);
  setComposerText(paneId, t.slice(0, start) + s + t.slice(end), start + s.length);
}
