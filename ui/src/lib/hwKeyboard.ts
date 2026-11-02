// Is a hardware keyboard there to press shortcuts on? Decides whether menus show their keys (components/KeyCaps.tsx):
// on a phone without one they're dead weight, with one (a tablet on a keyboard case) they're the guide.
//
// A page can't ask the system, so:
// - Not touch-first (a desktop, a laptop): yes, always.
// - Android App that says it can (hello's `keyboard`): the shell's answer, from Configuration.hardKeyboardHidden, sent
//   on hello and whenever a keyboard is plugged in / out (MainActivity.onConfigurationChanged → Bridge.keyboard).
// - Any other touch device (a browser, an iPad, an older App): no, until a key arrives that only a real keyboard sends
//   (a physical key code; soft keyboards send none, or 229 while composing), and no again once a soft keyboard shows
//   (the viewport shrinks): the system doesn't raise one while a hardware keyboard is attached.

import { useSyncExternalStore } from "react";
import { androidCan, onAndroid } from "./android";
import { isCoarse, keyboardUp } from "./phone";

let guessed = false;
let native: boolean | undefined;
const subs = new Set<() => void>();
const emit = () => subs.forEach((f) => f());

export function hasHwKeyboard(): boolean {
  if (typeof window === "undefined" || !isCoarse()) return true;
  if (native !== undefined) return native;
  return guessed;
}

const setGuess = (v: boolean) => {
  if (v === guessed) return;
  guessed = v;
  emit();
};

/** A key only a real keyboard sends: soft keyboards leave `code` empty (or say 229 while composing). */
const physical = (e: KeyboardEvent) =>
  !e.isComposing && e.keyCode !== 229 && !!e.code && e.code !== "Unidentified" && !/^(Audio|Volume|Media|Browser)/.test(e.code);

let started = false;
function start() {
  if (started || typeof window === "undefined") return;
  started = true;
  window.addEventListener("keydown", (e) => physical(e) && setGuess(true), true);
  window.visualViewport?.addEventListener("resize", () => keyboardUp() && setGuess(false));
  onAndroid("keyboard", (m) => {
    if (!androidCan("keyboard")) return;
    native = m.hard === true;
    emit();
  });
  // Turning a tablet changes coarse / fine on some devices, and the answer with it
  window.matchMedia("(pointer: coarse)").addEventListener("change", emit);
}

export function useHwKeyboard(): boolean {
  start();
  return useSyncExternalStore((f) => (subs.add(f), () => void subs.delete(f)), hasHwKeyboard);
}
