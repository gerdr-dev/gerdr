// Where add-ons' words go in a composer: at the caret, not after everything typed (voice input landed at the end
// whatever the caret said, reported). Drafts live in the store per pane; the caret only in the pane's textarea, so each
// mounted composer registers its textarea here (chatHost's useExtras).
import { useEffect, useSyncExternalStore, type RefObject } from "react";
import type { ComposerMark } from "@gerdr/chat";
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
 * Replace the pane's draft, the caret put at `caret` (default: the end). pending: the stretch [start, end) of it that
 * isn't settled yet, drawn in grey (voice input: words the recognizer may still change, as an input method shows its
 * composing text); look "cancel" draws it struck through instead (voice input: the finger is on "cancel", these words
 * are about to go). It lasts only while the draft is this text, so any edit by hand drops it. Setting a textarea's value sends its caret to
 * the end, so it's put back after React has written the new text. Not on an unfocused touch screen: moving the
 * selection of an unfocused field can pop the keyboard up on iOS (same rule as the shared draft, chatHost).
 */
export function setComposerText(paneId: string, text: string, caret?: number, pending?: PendingRange) {
  const was = pendings.get(paneId);
  if (pending && pending.start < pending.end)
    pendings.set(paneId, { text, start: Math.max(0, pending.start), end: Math.min(pending.end, text.length), look: pending.look });
  else pendings.delete(paneId);
  if (was || pendings.has(paneId)) for (const f of pendingSubs) f();
  set((s) => ({ drafts: { ...s.drafts, [paneId]: text } }));
  const el = boxes.get(paneId);
  if (!el || caret === undefined) return;
  const at = Math.max(0, Math.min(caret, text.length));
  const place = document.activeElement === el || !window.matchMedia("(pointer: coarse)").matches;
  requestAnimationFrame(() => {
    if (el.value !== text) return;
    if (place) el.setSelectionRange(at, at);
    showCaret(el, at);
  });
}

/**
 * Scroll the box so the line holding `at` shows. A browser scrolls a textarea to its caret only for typing, not for a
 * value or selection set from script, so voice input past the box's height went on out of sight below (reported).
 * Done whether or not the box has focus: scrolling it doesn't bring a keyboard up.
 */
function showCaret(el: HTMLTextAreaElement, at: number) {
  if (el.scrollHeight <= el.clientHeight) return;
  if (at >= el.value.length) return void (el.scrollTop = el.scrollHeight);
  // Where the caret's line ends: the text up to it laid out in a copy of the box's text area
  const cs = getComputedStyle(el);
  const d = document.createElement("div");
  for (const k of [
    "font",
    "letterSpacing",
    "wordSpacing",
    "lineHeight",
    "paddingLeft",
    "paddingRight",
    "paddingTop",
    "boxSizing",
    "whiteSpace",
    "overflowWrap",
    "wordBreak",
    "tabSize",
  ] as const)
    (d.style as any)[k] = cs[k];
  Object.assign(d.style, { position: "fixed", left: "-9999px", top: "0", visibility: "hidden", width: el.clientWidth + "px", paddingBottom: "0", border: "0" });
  d.textContent = el.value.slice(0, at) + "\u200b";
  document.body.appendChild(d);
  const bottom = d.offsetHeight;
  d.remove();
  const line = parseFloat(cs.lineHeight) || 20;
  if (bottom > el.scrollTop + el.clientHeight) el.scrollTop = bottom - el.clientHeight + parseFloat(cs.paddingBottom);
  else if (bottom - line < el.scrollTop) el.scrollTop = bottom - line - parseFloat(cs.paddingTop);
}

/** A tap can open the soft keyboard after the caret has already been revealed, shrinking the box again. */
export function keepComposerCaretVisible(el: HTMLTextAreaElement, text: string, at: number) {
  const observer = new ResizeObserver(() => {
    if (document.activeElement === el && el.value === text && el.selectionStart === at && el.selectionEnd === at) showCaret(el, at);
  });
  const stop = () => {
    observer.disconnect();
    clearTimeout(timer);
    el.removeEventListener("pointerdown", stop);
    el.removeEventListener("blur", stop);
  };
  // Follow the keyboard's opening animation, then leave scrolling to the user.
  const timer = setTimeout(stop, 750);
  el.addEventListener("pointerdown", stop);
  el.addEventListener("blur", stop);
  observer.observe(el);
}

/** Put text in at the caret (over the selection, if any), the caret after it. */
export function insertComposerText(paneId: string, s: string) {
  const t = draft(paneId);
  const { start, end } = composerSelection(paneId);
  setComposerText(paneId, t.slice(0, start) + s + t.slice(end), start + s.length);
}

// Each pane's unsettled stretch (setComposerText's pending), with the text it belongs to
export type PendingRange = { start: number; end: number; look?: "pending" | "cancel" };
const pendings = new Map<string, PendingRange & { text: string }>();
const pendingSubs = new Set<() => void>();
const subPending = (f: () => void) => (pendingSubs.add(f), () => void pendingSubs.delete(f));

/** The composer's marks for the pane (ChatHost useExtras): its pending stretch in grey (struck: cancel), while the text is still it. */
export function useComposerMarks(paneId: string, text: string): ComposerMark[] | undefined {
  const p = useSyncExternalStore(subPending, () => pendings.get(paneId));
  if (!p || p.text !== text) return undefined;
  return [{ start: p.start, end: p.end, className: p.look === "cancel" ? "text-muted-foreground line-through" : "text-muted-foreground" }];
}
