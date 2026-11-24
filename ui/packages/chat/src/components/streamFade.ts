import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";

/**
 * Streamed text fades in as it arrives, batch by batch: whatever reached the page in one render (a word, a sentence, a
 * whole closing paragraph) fades in together, as the model sent it. Nothing is staggered or held back.
 *
 * It paints with the CSS Custom Highlight API (::highlight rules in style.css): the text of the last DURATION_MS's
 * batches is covered by Ranges in a few shared highlights, one per opacity step, moved along each frame. The DOM is
 * never touched. streamdown's animate plugin did this by wrapping every character of the reply in its own styled
 * span; on a 6000-character reply that was ~5000 spans, and with the CPU slowed 4x (a phone) the stream took 23.6s
 * instead of 10s, half the frames over 50ms, the worst 0.7s, nearly all of it the browser's style and layout. With
 * Ranges the cost depends on how much text is fading, not how long the reply is. Its character counting also drifted
 * whenever Markdown was re-parsed, so new characters showed at once between fading ones.
 *
 * Batches are kept by offsets among the visible characters. Whitespace doesn't count: the newlines between blocks
 * and list items come and go as half-written Markdown is re-parsed, and counting them shifted later text onto other
 * offsets. While fading, text takes the foreground colour (a highlight can't read the element's own colour: a link
 * or inline code shows in the body colour for those 0.6s). Browsers without the API (Chrome < 105, Safari < 17.2)
 * just show the text.
 */
const DURATION_MS = 600;
const LEVELS = 10; // opacity steps: gerdr-fade-0 … gerdr-fade-9 in style.css
// After streaming stops the last batch is still fading
const TAIL_MS = DURATION_MS + 100;

const supported = typeof CSS !== "undefined" && "highlights" in CSS && typeof Highlight !== "undefined";

let shared: Highlight[] | null = null;
function levels(): Highlight[] {
  if (!shared) {
    shared = Array.from({ length: LEVELS }, (_, i) => {
      const h = new Highlight();
      CSS.highlights.set(`gerdr-fade-${i}`, h);
      return h;
    });
  }
  return shared;
}

const isSpace = (c: string) => c <= " " || /\s/.test(c);

function countVisible(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) if (!isSpace(s[i])) n++;
  return n;
}

/** Where in `s` its k-th visible character starts (s.length when k is past the last one). */
function visibleIndex(s: string, k: number): number {
  for (let i = 0; i < s.length; i++) {
    if (isSpace(s[i])) continue;
    if (k-- === 0) return i;
  }
  return s.length;
}

type Batch = { from: number; to: number; start: number };
type State = { root: Element | null; total: number; batches: Batch[]; ranges: [Range, number][]; raf: number };

function clear(st: State) {
  const hs = levels();
  for (const [r, l] of st.ranges) hs[l].delete(r);
  st.ranges = [];
}

/** Cover each fading batch's text with a Range in the highlight for its current opacity. */
function paint(st: State) {
  st.raf = 0;
  clear(st);
  const now = performance.now();
  st.batches = st.batches.filter((b) => now - b.start < DURATION_MS);
  if (!st.root || !st.batches.length) return;
  const hs = levels();
  const first = st.batches[0].from;
  const walk = document.createTreeWalker(st.root, NodeFilter.SHOW_TEXT);
  let pos = 0;
  for (let n = walk.nextNode(); n; n = walk.nextNode()) {
    const text = n.nodeValue ?? "";
    const c = countVisible(text);
    const end = pos + c;
    if (c && end > first) {
      for (const b of st.batches) {
        if (b.to <= pos || b.from >= end) continue;
        const r = new Range();
        r.setStart(n, visibleIndex(text, Math.max(0, b.from - pos)));
        r.setEnd(n, visibleIndex(text, Math.min(c, b.to - pos)));
        const p = (now - b.start) / DURATION_MS;
        const level = Math.min(LEVELS - 1, Math.floor((1 - (1 - p) * (1 - p)) * LEVELS)); // ease-out
        hs[level].add(r);
        st.ranges.push([r, level]);
      }
    }
    pos = end;
  }
  st.raf = requestAnimationFrame(() => paint(st));
}

/**
 * True while `on`, and for `ms` after it turns false. Decided during render: a flag set from an effect is still false
 * for the one render where `on` drops, and that render ended the fade (seen: the batches still fading vanished the
 * moment the turn's last text arrived together with its end).
 */
export function useLinger(on: boolean, ms: number) {
  const was = useRef(on);
  const until = useRef(0);
  const [, rerender] = useState(0);
  if (was.current && !on) until.current = performance.now() + ms;
  was.current = on;
  const lingering = !on && performance.now() < until.current;
  useEffect(() => {
    if (!lingering) return;
    const timer = setTimeout(() => rerender((n) => n + 1), until.current - performance.now() + 1);
    return () => clearTimeout(timer);
  }, [lingering]);
  return on || lingering;
}

/**
 * Props for the element (a Streamdown: it passes data-* through to its own root) whose text fades in while
 * `streaming`. Used by the reply (Markdown.tsx) and the thinking (ai/reasoning.tsx).
 */
export function useStreamFade(streaming: boolean) {
  const active = useLinger(streaming, TAIL_MS);
  const id = useId();
  const st = useRef<State>({ root: null, total: -1, batches: [], ranges: [], raf: 0 });
  // Every render: text past what was there before is a new batch. Text already there on mount fades in too: a
  // closing paragraph often arrives in one batch together with the end of the turn (a chat reopened mid-reply fades
  // its text in once, which is fine). Painted before the browser paints, so new text never shows at full first.
  useLayoutEffect(() => {
    const s = st.current;
    if (!active || !supported || matchMedia("(prefers-reduced-motion: reduce)").matches) {
      if (s.raf) cancelAnimationFrame(s.raf);
      s.raf = 0;
      s.batches = [];
      if (s.ranges.length) clear(s);
      return;
    }
    s.root = document.querySelector(`[data-gerdr-fade="${id}"]`);
    if (!s.root) return;
    const total = countVisible(s.root.textContent ?? "");
    if (total > Math.max(s.total, 0)) s.batches.push({ from: Math.max(s.total, 0), to: total, start: performance.now() });
    s.total = total;
    if (s.raf) cancelAnimationFrame(s.raf);
    paint(s);
  });
  useEffect(
    () => () => {
      const s = st.current;
      if (s.raf) cancelAnimationFrame(s.raf);
      if (s.ranges.length) clear(s);
    },
    [],
  );
  return { "data-gerdr-fade": id };
}
