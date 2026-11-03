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

const types = new Map<string, ComponentType<CardProps>>();
const subs = new Set<() => void>();
let version = 0;

export function registerCard(type: string, component: ComponentType<CardProps>): () => void {
  types.set(type, component);
  version++;
  for (const f of subs) f();
  return () => {
    if (types.get(type) !== component) return;
    types.delete(type);
    version++;
    for (const f of subs) f();
  };
}

const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));

/** The component for a card type, re-rendering when one is registered later (a Work UI's cards load after the chat). */
export function useCardType(type: string | undefined): ComponentType<CardProps> | undefined {
  useSyncExternalStore(subscribe, () => version);
  return type ? types.get(type) : undefined;
}
