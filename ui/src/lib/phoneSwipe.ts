// The phone main view's swipe (components/PhonePager.tsx), as the top bar (components/PhoneBar.tsx) follows it: which page
// is drawn and which pages are either side, so the bar's title is the page's on screen and the neighbours' titles are
// there to fade in under the finger. The per-frame part (how far the track is) is plain DOM writes from the pager
// (followSwipe): a React render per frame would cost the drag its smoothness.
import { useSyncExternalStore } from "react";

export type SwipePages = { shown?: string; prev?: string; next?: string };

let pages: SwipePages = {};
const subs = new Set<() => void>();

/** The pager says which page is drawn and which are either side (page keys: a tab id, or "s:<session id>"). */
export function setSwipePages(p: SwipePages) {
  if (p.shown === pages.shown && p.prev === pages.prev && p.next === pages.next) return;
  pages = p;
  for (const f of subs) f();
}

export const useSwipePages = () =>
  useSyncExternalStore(
    (f) => (subs.add(f), () => void subs.delete(f)),
    () => pages,
  );

/**
 * Where the drag is, from the pager every frame: `k` pages towards the next (negative: the previous), null at rest.
 * The bar's title slides and fades out as the neighbour's slides and fades in.
 */
export function followSwipe(k: number | null) {
  const cur = document.querySelector<HTMLElement>("[data-swipe-title='0']");
  const prev = document.querySelector<HTMLElement>("[data-swipe-title='-1']");
  const next = document.querySelector<HTMLElement>("[data-swipe-title='1']");
  if (!cur) return;
  if (k === null || k === 0) {
    for (const el of [cur, prev, next]) if (el) el.style.opacity = el.style.transform = "";
    return;
  }
  const a = Math.min(1, Math.abs(k));
  const dir = Math.sign(k);
  const inc = dir > 0 ? next : prev;
  const out = dir > 0 ? prev : next;
  cur.style.opacity = String(1 - a);
  cur.style.transform = `translateX(${-dir * a * SHIFT}px)`;
  if (inc) {
    inc.style.opacity = String(a);
    inc.style.transform = `translateX(${dir * (1 - a) * SHIFT}px)`;
  }
  if (out) out.style.opacity = out.style.transform = "";
}

const SHIFT = 24; // px a title slides as it fades: a hint of the direction, not the whole page's travel
