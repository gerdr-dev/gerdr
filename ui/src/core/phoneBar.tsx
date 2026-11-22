import { useContext, useEffect, useSyncExternalStore, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { PanelKeyContext } from "./visible";

/**
 * A panel's own content in Gerdr's phone top bar. On a phone a panel sits under a 44px bar holding only ☰ (and the
 * page / assistant switch when there is one), and a panel's own header went below it: two strips on top before any
 * of the page, half a phone screen on the content panel (user report). `<PhoneBar>` puts its children in that bar,
 * right of ☰, through a portal (so they keep the panel's own contexts: its router, its state); anywhere without
 * that bar (a wider window, outside a panel) they render where they are, so a panel writes its header once.
 */

const slots = new Map<string, HTMLElement>(); // panel key → the bar's slot element, while WorkView's phone bar is up
const filled = new Map<string, number>(); // panel key → how many <PhoneBar>s are in its slot
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((f) => f());
const subscribe = (f: () => void) => (listeners.add(f), () => void listeners.delete(f));

/** WorkView's phone bar: the element for `key`'s content (null on unmount). */
export function setPhoneBarSlot(key: string, el: HTMLElement | null) {
  if (el) slots.set(key, el);
  else slots.delete(key);
  emit();
}

/** Whether panel `key` has put something in its bar (WorkView moves its own switch aside then). */
export function usePhoneBarFilled(key: string | null | undefined): boolean {
  return useSyncExternalStore(subscribe, () => !!key && (filled.get(key) ?? 0) > 0);
}

/** Children in Gerdr's phone top bar when the panel is shown under one, else rendered in place. */
export function PhoneBar({ children }: { children: ReactNode }) {
  const key = useContext(PanelKeyContext);
  const slot = useSyncExternalStore(subscribe, () => (key ? slots.get(key) : undefined));
  useEffect(() => {
    if (!key || !slot) return;
    filled.set(key, (filled.get(key) ?? 0) + 1);
    emit();
    return () => {
      filled.set(key, (filled.get(key) ?? 1) - 1);
      emit();
    };
  }, [key, slot]);
  return slot ? createPortal(children, slot) : <>{children}</>;
}
