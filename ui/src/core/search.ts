import { useContext, useEffect, useRef, useSyncExternalStore, type ReactNode } from "react";
import { PanelKeyContext } from "./visible";

/**
 * A panel's own rows in the command palette (⌘K). What there is to find differs by panel (a board's tasks, another's
 * pages), so each gives its list and the palette draws it, above commands and conversations, while that panel is the
 * one shown: ⌘K over a board finds its tasks the way it finds conversations in Code. Same shape as usePhoneMenu.
 */
export type SearchItem = {
  /** Stable per item (defaults to the label). */
  id?: string;
  label: string;
  /** A short word at the row's end, kept whole (a status). Searched too. */
  detail?: string;
  /** detail's theme colour, as the panel colours the same status (default: muted). */
  tone?: "brand" | "ok" | "warn" | "bad";
  /** detail as a filled pill in its tone: a status that wants attention, like a board's 等你 badge. */
  pill?: boolean;
  /** More words to match that aren't shown (a summary, tags). */
  keywords?: string;
  icon?: ReactNode;
  /** The palette closes, then this runs. */
  onSelect: () => void;
};

type Entry = { heading?: string; items: SearchItem[] };

const lists = new Map<string, Entry>();
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((f) => f());
const subscribe = (f: () => void) => (listeners.add(f), () => void listeners.delete(f));

/** The rows the panel `key` gives (Palette reads it). */
export function usePanelSearch(key: string | null | undefined): Entry | undefined {
  return useSyncExternalStore(subscribe, () => (key ? lists.get(key) : undefined));
}

/**
 * Give this panel's rows to the command palette: call it in the panel's page with the current list. Republished only
 * when ids / labels / details / keywords change; onSelect always runs the latest one. heading: the group's name
 * (defaults to the panel's).
 */
export function useSearchItems(items: SearchItem[], opts?: { heading?: string }) {
  const key = useContext(PanelKeyContext);
  const latest = useRef(items);
  latest.current = items;
  const heading = opts?.heading;
  const sig = items
    .map((x) => `${x.id ?? x.label}\u0000${x.label}\u0000${x.detail ?? ""}\u0000${x.tone ?? ""}\u0000${x.pill ? 1 : 0}\u0000${x.keywords ?? ""}`)
    .join("\u0001");
  useEffect(() => {
    if (!key) return;
    lists.set(key, {
      heading,
      items: latest.current.map((x, i) => ({ ...x, onSelect: () => latest.current[i]?.onSelect() })),
    });
    emit();
  }, [key, sig, heading]);
  useEffect(() => {
    if (!key) return;
    return () => {
      lists.delete(key);
      emit();
    };
  }, [key]);
}
