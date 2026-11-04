import { useContext, useEffect, useRef, useSyncExternalStore, type ReactNode } from "react";
import { PanelKeyContext } from "./visible";

/**
 * A panel's own part of the phone drawer. On a phone a panel's top-left button opens the same drawer as Code
 * (projects, Code / panel switch, settings); what a panel offers there differs by panel (a board's message log and
 * settings, another's pages), so each gives its own items and Gerdr draws them, in its own style, while that panel
 * is the one shown. A panel that gives none gets the drawer as Code has it.
 */
export type PhoneMenuItem = {
  /** Stable per item (defaults to the label). */
  id?: string;
  label: string;
  icon?: ReactNode;
  /** A count or short word at the row's end (things waiting, "3"). */
  badge?: string | number;
  /** The drawer closes, then this runs. */
  onSelect: () => void;
};

const menus = new Map<string, PhoneMenuItem[]>();
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((f) => f());
const subscribe = (f: () => void) => (listeners.add(f), () => void listeners.delete(f));

/** The items the panel `key` gives (WorkView's drawer reads it). */
export function usePanelPhoneMenu(key: string | null | undefined): PhoneMenuItem[] | undefined {
  return useSyncExternalStore(subscribe, () => (key ? menus.get(key) : undefined));
}

/**
 * Give this panel's items to the phone drawer: call it in the panel's page with the current list. Republished only
 * when ids / labels / badges change; onSelect always runs the latest one.
 */
export function usePhoneMenu(items: PhoneMenuItem[]) {
  const key = useContext(PanelKeyContext);
  const latest = useRef(items);
  latest.current = items;
  const sig = items.map((x) => `${x.id ?? x.label}\u0000${x.label}\u0000${x.badge ?? ""}`).join("\u0001");
  useEffect(() => {
    if (!key) return;
    menus.set(
      key,
      latest.current.map((x, i) => ({ ...x, onSelect: () => latest.current[i]?.onSelect() })),
    );
    emit();
  }, [key, sig]);
  useEffect(() => {
    if (!key) return;
    return () => {
      menus.delete(key);
      emit();
    };
  }, [key]);
}
