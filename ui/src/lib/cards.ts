import { useSyncExternalStore, type ComponentType } from "react";
import type { Card } from "@/components/chat/ToolCard";

/**
 * Card types a UI adds (core registerCard): a tool result `{ gerdrCard: { type: "task-step", … } }` is drawn by the
 * component registered for its type, in any conversation, in Code as well as in the page. A project's Work UI registers
 * its cards in src/cards.tsx, which gerdr loads at start (lib/mounts.ts), so a worker's chat opened in Code draws them
 * before the Work page was ever shown. Without a component for its type (not loaded, or it throws) the card falls back
 * to the built-in one, drawn from its title / body / choose: write those too.
 */
export type CardProps = {
  card: Card & Record<string, unknown>;
  /** The conversation the card is in. */
  session: string;
};

// Registered per panel: every project's panels load their cards into this one page, and two of them may name a type
// alike (the same panel installed in two projects, or two panels that both say "task-step"). A card says which
// panel made it (`panel`, stamped by Gerdr on the tool result: work.stampCard), and only that panel's component draws
// it. A registration outside any panel's cards file (a page registering at runtime) is global, the fallback.
const scoped = new Map<string, ComponentType<CardProps>>(); // "<panel>\n<type>"
const global = new Map<string, ComponentType<CardProps>>();
/** For cards from before they were stamped: the latest panel registration of each type. */
const latest = new Map<string, ComponentType<CardProps>>();
const subs = new Set<() => void>();
let version = 0;
const bump = () => {
  version++;
  for (const f of subs) f();
};

// The panel whose cards file is being evaluated now (mounts.ts loads them one at a time, inside loadingPanel).
let loading: string | undefined;

/** Runs `load` (importing a panel's src/cards.tsx) with what it registers attributed to `panel` (its project key). */
export async function loadingPanel<T>(panel: string, load: () => Promise<T>): Promise<T> {
  loading = panel;
  try {
    return await load();
  } finally {
    loading = undefined;
  }
}

export function registerCard(type: string, component: ComponentType<CardProps>): () => void {
  const panel = loading;
  const key = panel ? `${panel}\n${type}` : type;
  const map = panel ? scoped : global;
  map.set(key, component);
  if (panel) latest.set(type, component);
  bump();
  return () => {
    if (map.get(key) !== component) return;
    map.delete(key);
    if (latest.get(type) === component) latest.delete(type);
    bump();
  };
}

const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));

/**
 * The component for a card, re-rendering when one is registered later (a panel's cards load after the chat): its own
 * panel's when the card says which (never another panel's of the same name), else a global one; an unstamped card
 * (older) takes the latest registration of its type.
 */
export function useCardType(type: string | undefined, panel?: string): ComponentType<CardProps> | undefined {
  useSyncExternalStore(subscribe, () => version);
  if (!type) return undefined;
  if (panel) return scoped.get(`${panel}\n${type}`) ?? global.get(type);
  return global.get(type) ?? latest.get(type);
}
