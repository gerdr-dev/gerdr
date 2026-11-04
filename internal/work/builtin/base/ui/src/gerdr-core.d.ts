// What `import … from "@gerdr/core"` gives this Work UI: gerdr's own components and state. This page isn't a frame:
// gerdr compiles it into its own page and draws it as a React root of its own, so these are the very components gerdr
// draws, on the same connection. Gerdr upgrades this file; don't edit it.

declare module "@gerdr/core" {
  import type { ComponentType, ReactNode } from "react";

  /**
   * One conversation: the whole chat with its input box (images, files, permissions, questions, cards), the same as a
   * pane in Code. It fills its parent's height, so give the parent one (`h-full`, `flex-1 min-h-0`…).
   * jump: scroll to the card whose `ref` it is (a tool result's gerdrCard.ref) and flash it; bump seq to jump again.
   */
  /**
   * sendNow: every message sent from it goes in right away while the agent runs (the composer's "send now": a running
   * command moves to the background and the agent reads the message at once), instead of waiting for its current step.
   */
  export function SessionChat(props: { id: string; focused?: boolean; jump?: { ref: string; seq: number }; className?: string; sendNow?: boolean }): ReactNode;

  /** A conversation's summary, kept current; undefined while unknown. */
  export function useSession(id: string | undefined):
    | {
        id: string;
        title?: string;
        agent: string;
        cwd: string;
        /** idle / running / waiting (a permission or question) / error… */
        status: string;
        tags?: Record<string, string>;
        [k: string]: unknown;
      }
    | undefined;

  /**
   * Show conversations this panel owns your own way. A conversation a local function started (ctx.sessions.start) is
   * this panel's: brought up from anywhere in gerdr (a notification, the inbox, the running list), gerdr switches to
   * this panel and calls `fn` with its id (a board opens that task's drawer with SessionChat). Without a handler gerdr
   * shows it in a drawer of its own over the panel. `key` is this panel's: `project` from ./lib/gerdr. Returns an
   * unregister function (call it when the page unmounts).
   */
  export function onShowSession(key: string, fn: (session: string) => void): () => void;

  /** Whether gerdr is in dark mode (the page shares gerdr's <html>, so its `dark` class is right already). */
  export function useDark(): boolean;

  /**
   * Ask the user for a key (a password, a token) in gerdr's own dialog; it's saved for local functions'
   * ctx.secrets.get(name) and never passes through this page. Resolves false when they cancel.
   */
  export function askSecret(name: string, opts?: { label?: string; note?: string }): Promise<boolean>;

  /** A card in a conversation: a tool's result `{ gerdrCard: Card }` (FUNCTIONS.md, "As tools for agents"). */
  export type Card = {
    /** A type registered with registerCard draws it; the fields below are what's drawn without one (fallback). */
    type?: string;
    title: string;
    kicker?: string;
    body?: string;
    tone?: "warn" | "info" | "ok";
    ref?: string;
    choose?: { project: string; fn: string; input?: Record<string, unknown>; field: string; options: string[]; other?: boolean };
    [k: string]: unknown;
  };
  export type CardProps = { card: Card; session: string };

  /**
   * Draw cards of a type yourself, in every conversation (Code too). Call it in src/cards.tsx: gerdr loads that file at
   * start, before this page is ever shown. A card whose component isn't there or throws falls back to the built-in one.
   */
  export function registerCard(type: string, component: ComponentType<CardProps>): () => void;

  /** The built-in card (title, body, choices answerable in place with undo), for a card type that only adds to it. */
  export function DefaultCard(props: { card: Card }): ReactNode;

  /** gerdr's toasts. */
  export const toast: {
    (message: ReactNode, opts?: Record<string, unknown>): string | number;
    success(message: ReactNode, opts?: Record<string, unknown>): string | number;
    error(message: ReactNode, opts?: Record<string, unknown>): string | number;
  };
}
