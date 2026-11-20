/**
 * The Android App's soft keyboard, followed as it slides (MainActivity.onCreate, Bridge.ime).
 *
 * The shell resizes the WebView only once per slide: at its end when the keyboard comes up, at its start when it goes
 * down. Resizing on every frame relays the whole page out and dropped frames; one resize at the start (what Android
 * does by itself) made the composer jump up while the keyboard was still on its way. In between, the shell sends the
 * keyboard's height on every frame (`h`) with how much of it the WebView's padding holds (`pad`), and this lifts the
 * chat under the focused box by the difference with a transform: the compositor moves it, nothing is laid out.
 *
 * The switch between the two must land in one frame, or the chat jumps by the keyboard's height for a frame: a new
 * `pad` arrives before the resize that applies it, so it is only taken in the visual viewport's resize event, where
 * the page's height (html's, useViewportHeight) changes too (with a fallback in case that resize never comes).
 *
 * What moves: the chat (`[data-gerdr-chat]`) around the focused element, clipped by its parent so it slides under the
 * bar above instead of over it; its composer may go up further on its own (see pick()). Nothing else: a terminal or a
 * dialog's input keeps the one-step change. The terminal was lifted too once, and a short one (a single `ls`) flashed
 * up under the bar and back down on every keyboard move, the same as a short chat. Where its content lands after
 * xterm's resize depends on the cursor row and whole cell heights, so it was taken out rather than modelled
 * (user's call: keep the effect to the chat).
 */
import { onAndroid } from "./android";

const TARGET = "[data-gerdr-chat]";

let h = 0; // keyboard height now, CSS px
let pad = 0; // how much of it the layout holds (taken at a resize)
let pending: number | null = null; // a pad the shell set that the page hasn't been resized to yet
let fallback = 0;
let el: HTMLElement | null = null; // the chat lifted right now
let box: HTMLElement | null = null; // a chat's composer, lifted on its own by what el's lift leaves
let slack = 0; // how much of the lift el sits out: Infinity = all of it (only box moves)

/**
 * What to lift, and how. The lift has to end where the resize puts things, and the resize moves a chat's messages only
 * as far as its list was already full: it shrinks the list by the keyboard's height and the list keeps its end in view
 * (ChatList's END, 2px), so its content goes up by however much of the shrink the empty space under the messages
 * doesn't take. So the chat goes up by max(0, lift - slack), slack being that empty space, and its composer the rest:
 * - a full list at its end (slack 0) goes up whole, its last message riding on the composer;
 * - a list shorter than the screen (slack > 0) stays put until the keyboard has eaten its empty space. Lifting it whole
 *   pushed the messages up under the bar and the resize dropped them back down, every time the keyboard came up or
 *   went down (user report, screen recording: the whole chat flashed up and down);
 * - a list scrolled up from its end keeps its top where it is across the resize (slack Infinity): only the composer.
 *   A list lifted with the composer dropped back by the keyboard's height when the resize landed (user report). That
 *   is also what a native screen does there: the box comes up over the list, the list stays put.
 */
function pick(): HTMLElement | null {
  const t = document.activeElement?.closest<HTMLElement>(TARGET);
  slack = 0;
  if (!t) return null;
  const s = t.querySelector<HTMLElement>("[data-chat-scroller]");
  // The empty space is the scroller's height less its content's (the rows' box, ChatList sets its height to their
  // total), not less scrollHeight: that is never under clientHeight, so a short chat measured as full and went up
  // whole (the first fix for this read it there and changed nothing on the phone: scrollHeight 602 = clientHeight 602)
  const list = s?.querySelector<HTMLElement>("[data-chat-list]");
  if (s) slack = s.scrollHeight - s.clientHeight - s.scrollTop > 2 ? Infinity : Math.max(0, s.clientHeight - (list?.offsetHeight ?? s.scrollHeight));
  if (!slack) return t;
  // The composer's box: the chat's last child (packages/chat ChatView)
  box = t.lastElementChild as HTMLElement | null;
  if (!box) {
    slack = 0;
    return t;
  }
  box.setAttribute("data-ime-alone", "");
  box.style.willChange = "transform";
  // A composer lifted alone leaves the list showing between it and the keyboard (user report): a shadow the page's
  // colour, as tall as a screen, fills below it. Offset by its spread, so its top edge is the box's own; it goes out
  // to the sides too, off screen or over the chat's margins, which are the same colour.
  // The box itself is see-through too (the composer's wrapper; its margins around the rounded box showed the list's
  // text once it sat over the list, user report), so it gets the colour as well.
  box.style.boxShadow = "0 100vh 0 100vh var(--background)";
  box.style.backgroundColor = "var(--background)";
  return t;
}

function apply() {
  // The lift must end exactly where the resize puts the chat, and the page's height is Math.round(vv.height) (html's,
  // useViewportHeight in packages/chat device.ts). Rounding h - pad by itself came out 1px off that (keyboard 316.6 CSS px: lift 317, page
  // 778 → 462), so the chat settled 1px higher and dropped when the resize landed (user report, screen recording).
  // This is what that height would lose if the page shrank by h - pad now: the same rounding, so the two meet.
  const vh = window.visualViewport?.height ?? innerHeight;
  const lift = h - pad > 0 ? Math.round(vh) - Math.round(vh - (h - pad)) : 0;
  if (!lift) {
    // will-change and the parent's clip stay: dropping the layer in the frame the WebView resizes left the composer
    // without its box and buttons for one frame (screen recording; same compositing cause as TAILWIND.md "one bad
    // frame when an animation ends"). Only the offset goes.
    if (el) el.style.transform = "";
    if (box) {
      box.style.transform = box.style.boxShadow = box.style.backgroundColor = "";
      box.removeAttribute("data-ime-alone");
    }
    el = box = null;
    return;
  }
  if (!el) {
    el = pick();
    if (!el) return;
    if (slack !== Infinity) {
      el.style.willChange = "transform";
      // Clipped by its parent, so it slides under the bar above instead of over it
      if (el.parentElement) el.parentElement.style.clipPath = "inset(0)";
    }
  }
  const up = Math.max(0, lift - slack);
  if (slack !== Infinity) el.style.transform = up ? `translate3d(0, ${-up}px, 0)` : "";
  if (box) box.style.transform = lift - up ? `translate3d(0, ${up - lift}px, 0)` : "";
}

function takePad() {
  if (pending === null) return;
  pad = pending;
  pending = null;
  clearTimeout(fallback);
  apply();
}

/** Start following the shell's keyboard messages; returns the undo (for initAndroid's hot-reload teardown). */
export function watchImeLift(): () => void {
  const off = onAndroid(
    "ime",
    (m) => {
      h = Number(m.h) || 0;
      const p = Number(m.pad) || 0;
      if (p !== (pending ?? pad)) {
        pending = p;
        clearTimeout(fallback);
        fallback = window.setTimeout(takePad, 300);
      }
      apply();
    },
    false,
  );
  // The page's height (html's) is written on the visual viewport's resize (packages/chat device.ts), not the
  // window's: the two came a frame apart, and taking the pad on the window's left the chat lifted by the keyboard's
  // height for one frame when it came up (user report: the composer flashed up then back). Same event, same frame.
  const vv: EventTarget = window.visualViewport ?? window;
  vv.addEventListener("resize", takePad);
  return () => {
    off();
    vv.removeEventListener("resize", takePad);
    clearTimeout(fallback);
    h = pad = 0;
    pending = null;
    apply();
  };
}
