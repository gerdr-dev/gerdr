// ctx.highlight(text) of a message action (lib/messageActions.ts): mark a stretch of the message on screen — the
// paragraph being read aloud, the sentence being translated. One mark at a time, the whole app over: a new one replaces
// it, and the run ending clears it.
//
// Where: @gerdr/chat marks the elements drawing a message's text with data-message (MessageInfo.id). How: the CSS
// Custom Highlight API (index.css ::highlight(gerdr-reading)), a Range over the text nodes, so nothing is added to the
// DOM React owns. Finding the text: what an add-on has is plain words (msg.text: Markdown marks gone, code blocks as
// "(code)"), not what is on screen (list markers, table cells, rendered links), so both sides are reduced to letters and
// digits and matched on that. Text not on screen (a code block read as "(code)") finds nothing and marks nothing.
// The list is virtual: rows scrolled away unmount and come back as new nodes, so the mark is redrawn every so often
// while it is on.
//
// ctx.onPick(stretches, pick): while the run lasts, a click on one of those stretches of the message (the paragraphs
// being read) calls pick(index), so reading can jump there. Not a click on a link / button, nor the end of a drag that
// selected text. The stretches are found the same way as a mark; a click between two goes to the one after it.

import { t } from "./i18n";

const NAME = "gerdr-reading";
const PENDING = "gerdr-reading-pending";
const WAIT = "data-reading-wait";
const isWord = /[\p{L}\p{N}]/u;

/** from: where this mark's search starts; next: where the owner's next mark in this message searches from (past this one). */
/**
 * pending: put by a click (onPick) before the add-on marks it itself, i.e. before that paragraph sounds: a jump waits
 * a second or two for synthesis, and with no mark until then the click looks missed. Drawn breathing
 * (::highlight(gerdr-reading-pending)) until the add-on's own highlight() of it.
 */
type Mark = { id: string; text: string; from: number; next: number; owner: object; pending?: boolean };
let mark: Mark | null = null;
let timer: ReturnType<typeof setInterval> | undefined;

const supported = () => typeof CSS !== "undefined" && "highlights" in CSS && typeof Highlight !== "undefined";

/**
 * Mark `text` in message `id` for `owner` (a run; null: clear). The search starts after the owner's last mark in the
 * same message.
 */
export function highlightMessage(id: string, text: string | null, owner: object) {
  if (!supported()) return;
  if (!text || !norm(text)) return unmark(owner);
  // The paragraph a click marked ahead, now sounding: same place, full color
  if (mark?.pending && mark.id === id && mark.owner === owner && norm(mark.text) === norm(text)) {
    mark.pending = false;
    return draw();
  }
  const from = mark?.id === id && mark.owner === owner ? mark.next : 0;
  mark = { id, text, from, next: from, owner };
  draw();
  timer ??= setInterval(draw, 500);
}

/** Clear the mark, if it is `owner`'s: a run stopped by a newer one must not wipe the newer one's mark. */
function unmark(owner: object) {
  if (mark?.owner !== owner) return;
  mark = null;
  if (supported()) {
    CSS.highlights.delete(NAME);
    CSS.highlights.delete(PENDING);
  }
  for (const el of document.querySelectorAll(`[${WAIT}]`)) el.removeAttribute(WAIT);
  idle();
}

/** A run ended: its mark goes, and its stretches stop taking clicks (only its own, as above). */
export function endHighlight(owner: object) {
  if (picker?.owner === owner) setPicker(null);
  unmark(owner);
  idle();
}

/** Nothing on: stop redrawing. */
function idle() {
  if (mark || picker || !timer) return;
  clearInterval(timer);
  timer = undefined;
}

type Picker = { id: string; raw: string[]; stretches: string[][]; pick: (i: number) => void; owner: object };
let picker: Picker | null = null;
const PICK = "data-reading-pick"; // on the message's elements while they take clicks (index.css: the pointer cursor)

/** Make `stretches` of message `id` clickable for `owner` until its run ends (or null: no longer). */
export function pickableMessage(id: string, stretches: string[] | null, pick: (i: number) => void, owner: object) {
  if (!stretches?.length) return picker?.owner === owner ? setPicker(null) : undefined;
  setPicker({ id, raw: stretches, stretches: stretches.map(lines), pick, owner });
  timer ??= setInterval(draw, 500);
  draw();
}

function setPicker(p: Picker | null) {
  for (const el of document.querySelectorAll(`[${PICK}]`)) el.removeAttribute(PICK);
  picker = p;
  if (p) document.addEventListener("click", onClick, true);
  else document.removeEventListener("click", onClick, true);
}

function onClick(e: MouseEvent) {
  if (!picker || e.button !== 0) return;
  const target = e.target as Element | null;
  const el = target?.closest(`[data-message="${CSS.escape(picker.id)}"]`);
  if (!el || target?.closest("a, button, input, textarea, summary")) return;
  if (!(window.getSelection()?.isCollapsed ?? true)) return; // the end of a drag that selected text
  const at = caretAt(e.clientX, e.clientY);
  if (!at) return;
  const pane = el.closest("[data-chat-scroller]");
  const els = [...document.querySelectorAll(`[data-message="${CSS.escape(picker.id)}"]`)].filter((x) => x.closest("[data-chat-scroller]") === pane);
  const text = words(els);
  // The clicked spot as a position among the word characters: the first one at or after it
  let pos = text.at.findIndex((w) => w.node === at.node && w.off >= at.off);
  if (pos < 0) {
    const n = text.at.findIndex((w) => !!(at.node.compareDocumentPosition(w.node) & Node.DOCUMENT_POSITION_FOLLOWING));
    pos = n < 0 ? text.s.length : n;
  }
  // Each stretch's place, found in order like the marks; the click goes to the one it falls in, or the next one
  let from = 0;
  for (let i = 0; i < picker.stretches.length; i++) {
    let start = -1;
    let end = from;
    for (const want of picker.stretches[i]) {
      const r = find(text, want, end, false);
      if (!r) continue;
      if (start < 0) start = r.end - want.length;
      end = r.end;
    }
    if (start < 0) continue;
    if (pos < end) {
      e.preventDefault();
      e.stopPropagation();
      // The mark this jump leads to is searched from here, not past the paragraph that was playing
      // Marked at once, lighter until it sounds (see Mark); the add-on's mark of it then finds it at this place
      mark = { id: picker.id, text: picker.raw[i], from: start, next: start, owner: picker.owner, pending: true };
      draw();
      picker.pick(i);
      return;
    }
    from = end;
  }
}

/** Where a click landed in the text: the text node and offset under the pointer. */
function caretAt(x: number, y: number): { node: Node; off: number } | null {
  const d = document as Document & { caretPositionFromPoint?(x: number, y: number): { offsetNode: Node; offset: number } | null };
  const p = d.caretPositionFromPoint?.(x, y);
  if (p) return { node: p.offsetNode, off: p.offset };
  const r = document.caretRangeFromPoint?.(x, y);
  return r ? { node: r.startContainer, off: r.startOffset } : null;
}

/** A stretch's lines, normalized, without the "(code)" plainText puts for a code block (never on screen, and its one
 * word would match prose elsewhere). */
function lines(text: string): string[] {
  const code = norm(t("messageActions.code"));
  return text
    .split("\n")
    .map(norm)
    .filter((l) => l && l !== code);
}

const norm = (s: string) =>
  [...s]
    .filter((c) => isWord.test(c))
    .join("")
    .toLowerCase();

function draw() {
  // Elements remount as the list scrolls: the clickable ones are marked again each time
  if (picker) for (const el of document.querySelectorAll(`[data-message="${CSS.escape(picker.id)}"]`)) el.setAttribute(PICK, "");
  if (!mark) return;
  const ranges: Range[] = [];
  let end = mark.from;
  // The same session may be open in several panes: each pane's copy is searched on its own
  const panes = new Map<Element | null, Element[]>();
  for (const el of document.querySelectorAll(`[data-message="${CSS.escape(mark.id)}"]`)) {
    const pane = el.closest("[data-chat-scroller]");
    panes.set(pane, [...(panes.get(pane) ?? []), el]);
  }
  // Line by line, each searched past the one before: a stretch may join lines of which some aren't on screen as words
  // (a code block read as "(code)" put before the paragraph after it), and those are left out rather than failing all
  const wanted = lines(mark.text);
  for (const els of panes.values()) {
    const text = words(els);
    let pos = mark.from;
    for (const want of wanted) {
      const r = find(text, want, pos);
      if (!r) continue;
      ranges.push(r.range);
      pos = r.end;
      end = Math.max(end, r.end);
    }
  }
  // The breathing of a waiting mark runs on its message's elements (index.css [data-reading-wait])
  for (const els of panes.values()) for (const el of els) el.toggleAttribute(WAIT, !!mark.pending);
  CSS.highlights.delete(mark.pending ? NAME : PENDING);
  if (!ranges.length) return void CSS.highlights.delete(mark.pending ? PENDING : NAME);
  CSS.highlights.set(mark.pending ? PENDING : NAME, new Highlight(...ranges));
  // The next paragraph is searched past this one, so a paragraph said twice is found in order
  if (!mark.pending) mark.next = Math.max(mark.next, end);
}

type Words = { s: string; at: { node: Text; off: number }[] };

/** Every word character of the elements' text, normalized, with the text node and offset it came from. */
function words(els: Element[]): Words {
  const at: { node: Text; off: number }[] = [];
  let s = "";
  for (const el of els) {
    const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    for (let n = walk.nextNode() as Text | null; n; n = walk.nextNode() as Text | null) {
      const v = n.data;
      for (let i = 0; i < v.length; i++) {
        const c = v[i];
        if (!isWord.test(c)) continue;
        s += c.toLowerCase();
        at.push({ node: n, off: i });
      }
    }
  }
  return { s, at };
}

/** The first match of `want` (normalized) at or after position `from`, as a Range; `wrap`: else from the start. */
function find({ s, at }: Words, want: string, from: number, wrap = true): { range: Range; end: number } | null {
  let i = s.indexOf(want, from);
  // Searched past it (a mark redrawn after scrolling, the message re-rendered): from the start again
  if (i < 0 && from > 0 && wrap) i = s.indexOf(want);
  if (i < 0) return null;
  const a = at[i];
  const b = at[i + want.length - 1];
  const range = document.createRange();
  range.setStart(a.node, a.off);
  range.setEnd(b.node, b.off + 1);
  return { range, end: i + want.length };
}
