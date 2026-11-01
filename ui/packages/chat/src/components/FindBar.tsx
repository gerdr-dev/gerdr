import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { ChevronDown, ChevronUp, X } from "lucide-react";
import { t as tr } from "../i18n";
import type { Row } from "./Rows";
import type { ListHandle } from "./ChatList";

/*
 * ⌘F / Ctrl+F inside a conversation. The browser's own find can't do it: the list is virtual (rows off screen aren't in
 * the DOM) and the desktop App has no find at all. So matches are found in the data (what each row says), the list
 * scrolls to the row, and the words are marked with the CSS Custom Highlight API: ranges painted over the DOM without
 * touching it, so React's nodes and the list's measured sizes stay as they are.
 *
 * Only what people write and read is searched: user messages, the agent's text and visible thinking. Tool calls and
 * their output are not (a find in a long session would be all `grep` lines). A row's count comes from its source text,
 * which can differ a little from what's drawn (Markdown syntax, a link's URL); the n-th match of a row is clamped to
 * what the DOM has.
 */

type Match = { row: number; key: string; n: number };

const lowerOf = new WeakMap<object, string>();
function rowText(r: Row): string {
  if (r.kind === "user") {
    let s = lowerOf.get(r.msg);
    if (s === undefined) {
      s = r.msg.parts
        .map((p) => (p.type === "text" ? p.text : ""))
        .join("\n")
        .toLowerCase();
      lowerOf.set(r.msg, s);
    }
    return s;
  }
  if (r.kind === "part" && (r.part.type === "text" || r.part.type === "reasoning")) {
    let s = lowerOf.get(r.part);
    if (s === undefined) lowerOf.set(r.part, (s = r.part.text.toLowerCase()));
    return s;
  }
  return "";
}

function count(hay: string, q: string): number {
  let n = 0;
  for (let i = hay.indexOf(q); i >= 0; i = hay.indexOf(q, i + q.length)) n++;
  return n;
}

/** The ranges of q (lowercase) in el's text, across text nodes (a match can span **bold** and plain text). */
function domMatches(el: Element, q: string): Range[] {
  const nodes: Text[] = [];
  const starts: number[] = [];
  let text = "";
  const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  for (let n = w.nextNode() as Text | null; n; n = w.nextNode() as Text | null) {
    nodes.push(n);
    starts.push(text.length);
    text += n.data.toLowerCase();
  }
  const out: Range[] = [];
  let j = 0;
  const at = (pos: number, end: boolean): [Text, number] => {
    // the node holding pos; an end position at a node boundary stays in the earlier node
    while (j < nodes.length - 1 && (end ? starts[j + 1] < pos : starts[j + 1] <= pos)) j++;
    return [nodes[j], pos - starts[j]];
  };
  for (let i = text.indexOf(q); i >= 0; i = text.indexOf(q, i + q.length)) {
    const [a, ao] = at(i, false);
    const [b, bo] = at(i + q.length, true);
    const r = document.createRange();
    r.setStart(a, Math.min(ao, a.length));
    r.setEnd(b, Math.min(bo, b.length));
    out.push(r);
  }
  return out;
}

const HL = "gerdr-find";
const HL_CUR = "gerdr-find-current";
const registry = (): any => (typeof CSS !== "undefined" ? (CSS as any).highlights : undefined);

export function FindBar({
  rows,
  list,
  scroller,
  seq,
  older,
  loadOlder,
  onClose,
}: {
  rows: Row[];
  list: RefObject<ListHandle | null>;
  scroller: RefObject<HTMLElement | null>;
  /** Goes up on every ⌘F: focus and select the input again. */
  seq: number;
  older?: "more" | "loading";
  loadOlder?: () => void;
  onClose: () => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [text, setText] = useState("");
  // Searched 250ms after typing stops: every keystroke would otherwise scan a long session and jump the list. Not while an
  // IME is composing: the box holds pinyin letters then ("wo men" on the way to 我们), and the list jumped to those.
  const [q, setQ] = useState("");
  const [composing, setComposing] = useState(false);
  useEffect(() => {
    if (composing) return;
    const v = text.trim().toLowerCase();
    const id = setTimeout(() => setQ(v), 250);
    return () => clearTimeout(id);
  }, [text, composing]);
  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, [seq]);

  const matches = useMemo(() => {
    const out: Match[] = [];
    if (!q) return out;
    rows.forEach((r, row) => {
      const n = count(rowText(r), q);
      for (let i = 0; i < n; i++) out.push({ row, key: r.key, n: i });
    });
    return out;
  }, [rows, q]);
  const [cur, setCur] = useState(-1);
  // The current match by row key, so new output coming in (rows added below, older ones put in front) keeps it
  const curRef = useRef<Match | null>(null);
  const live = useRef({ matches, q });
  live.current = { matches, q };

  const paint = () => {
    const H = registry();
    const s = scroller.current;
    if (!H || !s) return;
    const { matches, q } = live.current;
    if (!q || !matches.length) {
      H.delete(HL);
      H.delete(HL_CUR);
      return;
    }
    const keys = new Set(matches.map((m) => m.key));
    const all: Range[] = [];
    let current: Range | undefined;
    const c = curRef.current;
    for (const el of s.querySelectorAll<HTMLElement>("[data-chat-key]")) {
      const k = el.dataset.chatKey!;
      if (!keys.has(k)) continue;
      const rs = domMatches(el, q);
      if (c && c.key === k && rs.length) current = rs[Math.min(c.n, rs.length - 1)];
      for (const r of rs) if (r !== current) all.push(r);
    }
    H.set(HL, new (window as any).Highlight(...all));
    if (current) H.set(HL_CUR, new (window as any).Highlight(current));
    else H.delete(HL_CUR);
  };

  // Bring a match on screen: its row first if it isn't mounted (virtual list), then the words a third of the way down
  const reveal = (m: Match, tries = 0) => {
    const s = scroller.current;
    if (!s) return;
    const el = s.querySelector<HTMLElement>(`[data-chat-key="${CSS.escape(m.key)}"]`);
    if (!el) {
      if (tries === 0) list.current?.scrollToIndex({ index: m.row, align: "start" });
      if (tries < 4) requestAnimationFrame(() => reveal(m, tries + 1));
      return;
    }
    paint();
    const rs = domMatches(el, live.current.q);
    const target = rs.length ? rs[Math.min(m.n, rs.length - 1)] : el;
    const rect = target.getBoundingClientRect();
    const box = s.getBoundingClientRect();
    // 80: the pinned prompt and this bar over the top of the list
    if (rect.top < box.top + 80 || rect.bottom > box.bottom - 24) list.current?.scrollTo(s.scrollTop + rect.top - box.top - s.clientHeight / 3);
  };

  const go = (i: number) => {
    const m = matches[i];
    if (!m) return;
    curRef.current = m;
    setCur(i);
    reveal(m);
  };

  // A new query: start from the first match at or below the top of the screen (as a browser's find), else the last one
  useEffect(() => {
    curRef.current = null;
    if (!matches.length) {
      setCur(-1);
      paint();
      return;
    }
    const s = scroller.current;
    let top = 0;
    if (s) {
      const y = s.getBoundingClientRect().top;
      for (const it of s.querySelectorAll<HTMLElement>("[data-index]"))
        if (it.getBoundingClientRect().bottom > y + 1) {
          top = Number(it.dataset.index);
          break;
        }
    }
    const i = matches.findIndex((m) => m.row >= top);
    go(i >= 0 ? i : matches.length - 1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q]);

  // Rows changed under the same query: find the current match again by key, repaint once the DOM has them
  useEffect(() => {
    const c = curRef.current;
    if (c) {
      let i = matches.findIndex((m) => m.key === c.key && m.n === c.n);
      // Gone (a rewind cut it off): the nearest one left
      if (i < 0) {
        i = Math.min(cur, matches.length - 1);
        curRef.current = matches[i] ?? null;
      }
      if (i !== cur) setCur(i);
    }
    const id = requestAnimationFrame(paint);
    return () => cancelAnimationFrame(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [matches]);

  // Rows mount and unmount as the list scrolls (virtual list): repaint
  useEffect(() => {
    const s = scroller.current;
    if (!s) return;
    let raf = 0;
    const on = () => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(paint);
    };
    s.addEventListener("scroll", on, { passive: true });
    return () => {
      cancelAnimationFrame(raf);
      s.removeEventListener("scroll", on);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scroller.current]);

  useEffect(
    () => () => {
      registry()?.delete(HL);
      registry()?.delete(HL_CUR);
    },
    [],
  );

  const step = (d: 1 | -1) => {
    if (!matches.length) return;
    const i = cur < 0 ? 0 : (cur + d + matches.length) % matches.length;
    go(i);
  };
  // ⌘G / ⇧⌘G (F3 off the mac) while the bar is open, wherever the focus is
  const stepRef = useRef(step);
  stepRef.current = step;
  useEffect(() => {
    const mac = /Mac/.test(navigator.platform);
    const on = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      const g = e.code === "KeyG" && (mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey) && !e.altKey;
      if (!g && e.key !== "F3") return;
      e.preventDefault();
      stepRef.current(e.shiftKey ? -1 : 1);
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, []);

  const busy = text.trim().toLowerCase() !== q;
  return (
    // Below the pinned prompt (ChatView's PinnedPrompt, ~30px at the top), not over it: in a narrow pane the two overlapped
    <div
      data-keep-focus=""
      className="absolute top-10 right-3 z-20 flex items-center gap-0.5 rounded-lg border bg-background/95 py-1.5 pr-1.5 pl-3 text-[13px] shadow-md backdrop-blur"
    >
      <input
        ref={input}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onCompositionStart={() => setComposing(true)}
        onCompositionEnd={() => setComposing(false)}
        placeholder={tr("find.placeholder")}
        className="h-7 w-44 bg-transparent outline-none placeholder:text-muted-foreground max-md:w-32"
        onKeyDown={(e) => {
          if (e.nativeEvent.isComposing || e.keyCode === 229) return;
          if (e.key === "Enter") {
            e.preventDefault();
            // Enter before the debounce fired: search now
            if (busy) setQ(text.trim().toLowerCase());
            else step(e.shiftKey ? -1 : 1);
          } else if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            onClose();
          }
        }}
      />
      <span className="min-w-12 shrink-0 px-1 text-right text-[12px] text-muted-foreground tabular-nums">
        {q && !busy ? (matches.length ? `${cur + 1}/${matches.length}` : tr("find.none")) : ""}
      </span>
      {older === "more" && loadOlder && (
        <button
          className="shrink-0 rounded-md px-1.5 py-1 text-[12px] text-muted-foreground hover:bg-muted hover:text-foreground"
          onClick={loadOlder}
          title={tr("find.olderHint")}
        >
          {tr("find.older")}
        </button>
      )}
      <button
        className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-40"
        disabled={!matches.length}
        onClick={() => step(-1)}
        title={tr("find.prev")}
      >
        <ChevronUp className="size-4" />
      </button>
      <button
        className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-40"
        disabled={!matches.length}
        onClick={() => step(1)}
        title={tr("find.next")}
      >
        <ChevronDown className="size-4" />
      </button>
      <button className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground" onClick={onClose} title={tr("find.close")}>
        <X className="size-4" />
      </button>
    </div>
  );
}
