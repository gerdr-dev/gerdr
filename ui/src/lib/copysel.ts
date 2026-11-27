// 实验室：选中即复制（config.copyOnSelect）。照 herdr 的 ui.copy_on_select 做，用过 herdr 的人手感一样：
//   - 拖着选，松开鼠标就复制，选区马上清掉（清掉 = 告诉你「已经拿走了」，也免得下一次点击被当成在选区上拖）；
//   - 双击选词 / 三击选行，复制后高亮留 500ms 再清，让人看清复制的是哪一段；
//   - a small "Copied" bubble next to where the mouse let go, for ~1s. Not a toast: toasts sit in a window corner, and
//     the bottom-right one covered the composer of the rightmost pane every time (user report). The bubble is where the
//     person is already looking and covers nothing they need;
//   - 复制完光标回到这个 pane 的输入框（终端本来就在终端里）。
// 对话等普通页面看 DOM 选区（document 上的 mouseup）；终端是 xterm 自己画的选区，不进 DOM，Terminal.tsx 里单独接。
// 输入框 / 可编辑区域里的选区不管：在那里选字是为了改、为了删，复制走了还清掉选区就没法改了。
import { dialogOver, isCoarse } from "./phone";
import { get, prefOf } from "./store";
import { t } from "./i18n";
import { insertSelectionQuote, offerComposerQuote, QUOTE_SUGGESTION_MS, quoteSelection, type CopyPoint, type SelectionQuote } from "./selectionQuote";

const HIGHLIGHT_MS = 500; // herdr 的 PANE_COPY_HIGHLIGHT_DURATION
const BUBBLE_MS = 1000;

export const copyOnSelectOn = () => !!prefOf(get(), "copyOnSelect");

// 还没到点的「清选区」：三击时第二下（双击）复制完会定一个，第三下又定一个。新的一次复制要先把旧的撤掉 —— 不然旧的提前
// 跑，整行的高亮刚出来就被清掉、焦点进了输入框；接着第三下的再清一次，清掉的就是输入框里的光标了（还聚焦着却没光标，
// 打不了字、输入法也起不来；用户报的）
let pendingClear: ReturnType<typeof setTimeout> | undefined;

let removeBubble: (() => void) | undefined;
let copyingInPlace = false;

/** The caller supplies the quote and position after this succeeds, so the native copy event can be skipped. */
export function copyInPlace() {
  copyingInPlace = true;
  try {
    return document.execCommand("copy");
  } finally {
    copyingInPlace = false;
  }
}

const visible = (el: HTMLElement) =>
  el.isConnected && !el.closest('[aria-hidden="true"]') && el.getClientRects().length > 0 && getComputedStyle(el).visibility === "visible";

/** Plain DOM and CSS so the popup is styled and positioned before its first frame. */
function showBubble({ x, y }: CopyPoint, quote?: SelectionQuote) {
  removeBubble?.();
  const el = document.createElement("div");
  el.className = "copy-bubble";
  const label = document.createElement("span");
  label.setAttribute("role", "status");
  label.textContent = t("common.copied");
  el.appendChild(label);
  let detach: () => void;
  const close = () => {
    detach();
    el.remove();
    if (removeBubble === close) removeBubble = undefined;
  };

  if (quote) {
    el.classList.add("copy-bubble-action");
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = t("quoteBar.insert");
    button.onclick = () => {
      close();
      if (visible(quote.box)) insertSelectionQuote(quote.box, quote.id, quote.text);
    };
    el.appendChild(button);

    const outside = (e: PointerEvent) => {
      if (e.target instanceof Node && !el.contains(e.target)) close();
    };
    const key = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented || e.isComposing) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      close();
    };
    const observer = new MutationObserver(() => {
      if (!visible(quote.box) || quote.box.dataset.session !== quote.id) close();
    });
    // Kept tabs switch visibility in place. Observe the source's ancestors so a keyboard tab switch also closes it.
    for (let parent: HTMLElement | null = quote.box; parent; parent = parent.parentElement) {
      observer.observe(parent, { childList: true, attributes: true, attributeFilter: ["class", "style", "hidden", "aria-hidden", "data-session"] });
    }
    const timeout = setTimeout(close, QUOTE_SUGGESTION_MS);
    detach = () => {
      clearTimeout(timeout);
      observer.disconnect();
      window.removeEventListener("pointerdown", outside, true);
      window.removeEventListener("keydown", key, true);
    };
    window.addEventListener("pointerdown", outside, true);
    window.addEventListener("keydown", key, true);
  } else {
    const fade = setTimeout(() => {
      el.classList.add("copy-bubble-out");
      setTimeout(close, 200);
    }, BUBBLE_MS);
    detach = () => clearTimeout(fade);
  }

  removeBubble = close;
  document.body.appendChild(el);
  const { width, height } = el.getBoundingClientRect();
  el.style.left = `${Math.max(4, Math.min(x + 8, window.innerWidth - width - 4))}px`;
  const above = y - height - 8;
  const top = quote && above < 4 ? Math.min(y + 8, window.innerHeight - height - 4) : above;
  el.style.top = `${Math.max(4, top)}px`;
}

export function offerQuote(quote: SelectionQuote | undefined, at?: CopyPoint) {
  if (!quote || !visible(quote.box)) return false;
  if (isCoarse()) {
    offerComposerQuote(quote);
    return false;
  }
  showBubble(at ?? quote.anchor, quote);
  return true;
}

/** 复制并提示；clear 是清掉选区的办法，多击时晚一点再清。at: where the mouse let go (the bubble goes there). */
export function copySelected(text: string, multiClick: boolean, clear: () => void, at: CopyPoint, showQuote?: () => boolean) {
  // Empty or whitespace-only: don't touch the clipboard or toast. Double-clicking blank space selects an invisible
  // newline / run of spaces (in the DOM and in xterm alike), which would otherwise report "copied" for nothing.
  if (!text.trim()) return;
  clearTimeout(pendingClear);
  const done = () => {
    if (!showQuote?.()) showBubble(at);
    clearTimeout(pendingClear);
    if (multiClick) pendingClear = setTimeout(clear, HIGHLIGHT_MS);
    else clear();
  };
  // clipboard API 要页面聚焦、安全上下文（https / localhost）；不行就退回 execCommand（它复制的是当前 DOM 选区，
  // 所以退回时不能先清选区）
  const fallback = () => {
    if (copyInPlace()) done();
  };
  if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, fallback);
  else fallback();
}

function editable(n: Node | null): boolean {
  const el = n instanceof Element ? n : n?.parentElement;
  return !!el?.closest("input, textarea, select, [contenteditable]:not([contenteditable=false])");
}

/** Tracks manual copies from the chat, and copies DOM selections on mouseup when Copy on select is enabled. */
export function watchSelectionCopy(): () => void {
  let seq = 0;
  let pointer: CopyPoint | undefined;
  const onMove = (e: MouseEvent) => {
    pointer = { x: e.clientX, y: e.clientY };
  };
  const onCopy = (e: ClipboardEvent) => {
    if (copyingInPlace || e.defaultPrevented || editable(e.target as Node | null) || editable(document.activeElement)) return;
    const quote = quoteSelection();
    if (!quote) return;
    const chat = quote.box.closest("[data-gerdr-chat]");
    const at = pointer && chat?.contains(document.elementFromPoint(pointer.x, pointer.y)) ? pointer : undefined;
    offerQuote(quote, at);
  };
  const onUp = (e: MouseEvent) => {
    onMove(e);
    if (e.button !== 0 || !copyOnSelectOn()) return;
    if ((e.target as Element | null)?.closest?.(".xterm, .copy-bubble-action, [data-quote-bar]")) return;
    // 等浏览器把这次点击的选区定下来（mouseup 当下双击的选区有时还没落）。只认最后一次松手：双击点得快时，第一下的检查
    // 还没跑第二下就把词选上了，按第一下（单击）算会当成拖选马上清掉
    const my = ++seq;
    setTimeout(() => {
      if (my !== seq) return;
      const sel = window.getSelection();
      if (!sel || sel.isCollapsed || !sel.rangeCount) return;
      // 输入框里的选区不进 getSelection 的节点，看焦点（点对话里的字时焦点会离开输入框）
      if (editable(sel.anchorNode) || editable(sel.focusNode) || editable(document.activeElement)) return;
      // 复制完把光标放回这个 pane 的输入框，接着就能粘贴 / 打字（用户要的）。清选区那一刻再聚焦：聚焦输入框会顶掉页面选区，
      // 早了双击的高亮就看不到了。不在 pane 里（设置弹窗之类）、没有输入框的 pane（子 agent）不动焦点；触屏上不聚焦（会弹键盘）
      // A grid tile (Overview) is a pane too, but has no data-pane-id: that id names a real pane elsewhere (context menu).
      // data-composer-scope marks any other chat with a composer that isn't a pane: a panel's agent column, its peek drawer.
      const pane = (sel.anchorNode instanceof Element ? sel.anchorNode : sel.anchorNode?.parentElement)?.closest(
        "[data-pane-id], [data-ov-tile], [data-composer-scope]",
      );
      const box = pane?.querySelector<HTMLTextAreaElement>("textarea[data-composer]");
      // 三击选段时浏览器把段尾的换行也选上了，粘进输入框会多出空行
      const text = e.detail >= 3 ? sel.toString().replace(/\s+$/, "") : sel.toString();
      const quote = quoteSelection(sel);
      const at = { x: e.clientX, y: e.clientY };
      copySelected(
        text,
        e.detail >= 2,
        () => {
          // 焦点已经在输入框里（用户等不及自己点进去了）时别清：这时的页面选区就是输入框的光标
          if (editable(document.activeElement)) return;
          sel.removeAllRanges();
          // A dialog on screen over the chat keeps the focus (not one the chat sits in: a panel's drawer)
          if (box?.isConnected && !isCoarse() && !dialogOver(box)) box.focus();
        },
        at,
        () => offerQuote(quote && { ...quote, text }, at),
      );
    });
  };
  document.addEventListener("mouseup", onUp);
  document.addEventListener("mousemove", onMove);
  document.addEventListener("copy", onCopy);
  return () => {
    document.removeEventListener("mouseup", onUp);
    document.removeEventListener("mousemove", onMove);
    document.removeEventListener("copy", onCopy);
    removeBubble?.();
  };
}
