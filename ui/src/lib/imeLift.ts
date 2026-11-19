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
 * What moves: the nearest `[data-ime-lift]` (a terminal) or chat (`[data-gerdr-chat]`) around the focused element,
 * clipped by its parent so it slides under the bar above instead of over it (a chat scrolled up: only its composer,
 * see target()). Anything else (a dialog's input) has no target and keeps the one-step change.
 */
import { onAndroid } from "./android";

const TARGET = "[data-ime-lift], [data-gerdr-chat]";

let h = 0; // keyboard height now, CSS px
let pad = 0; // how much of it the layout holds (taken at a resize)
let pending: number | null = null; // a pad the shell set that the page hasn't been resized to yet
let fallback = 0;
let el: HTMLElement | null = null; // what's lifted right now

/**
 * What to lift. A chat scrolled up from its end gets only its composer lifted: the resize keeps such a list's top where
 * it is, so a list lifted with the composer dropped back by the keyboard's height when the resize landed (user
 * report). That is also what a native screen does there: the box comes up over the list, the list stays put. A chat
 * at its end (ChatList's END, 2px) keeps the end in view across the resize, so it goes up whole, its last message
 * riding on the composer.
 */
function target(): HTMLElement | null {
  const t = document.activeElement?.closest<HTMLElement>(TARGET);
  if (!t?.hasAttribute("data-gerdr-chat")) return t ?? null;
  const s = t.querySelector<HTMLElement>("[data-chat-scroller]");
  if (!s || s.scrollHeight - s.clientHeight - s.scrollTop <= 2) return t;
  // The composer's box: the chat's last child (packages/chat ChatView)
  const box = t.lastElementChild as HTMLElement | null;
  box?.setAttribute("data-ime-alone", "");
  return box;
}

function apply() {
  // The lift must end exactly where the resize puts the chat, and the page's height is Math.round(vv.height) (html's,
  // useViewportHeight in packages/chat device.ts). Rounding h - pad by itself came out 1px off that (keyboard 316.6 CSS px: lift 317, page
  // 778 → 462), so the chat settled 1px higher and dropped when the resize landed (user report, screen recording).
  // This is what that height would lose if the page shrank by h - pad now: the same rounding, so the two meet.
  const vh = window.visualViewport?.height ?? innerHeight;
  const lift = h - pad > 0 ? Math.round(vh) - Math.round(vh - (h - pad)) : 0;
  if (!lift) {
    if (el) {
      // will-change and the parent's clip stay: dropping the layer in the frame the WebView resizes left the composer
      // without its box and buttons for one frame (screen recording; same compositing cause as TAILWIND.md "one bad
      // frame when an animation ends"). Only the offset goes.
      el.style.transform = el.style.boxShadow = el.style.backgroundColor = "";
      el.removeAttribute("data-ime-alone");
      el = null;
    }
    return;
  }
  if (!el) {
    el = target();
    if (!el) return;
    el.style.willChange = "transform";
    // A composer lifted alone leaves the list showing between it and the keyboard (user report): a shadow the page's
    // colour, as tall as a screen, fills below it. Offset by its spread, so its top edge is the box's own; it goes out
    // to the sides too, off screen or over the chat's margins, which are the same colour.
    // The box itself is see-through too (the composer's wrapper; its margins around the rounded box showed the list's
    // text once it sat over the list, user report), so it gets the colour as well.
    if (el.hasAttribute("data-ime-alone")) {
      el.style.boxShadow = "0 100vh 0 100vh var(--background)";
      el.style.backgroundColor = "var(--background)";
    } else if (el.parentElement) el.parentElement.style.clipPath = "inset(0)";
  }
  el.style.transform = `translate3d(0, ${-lift}px, 0)`;
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
