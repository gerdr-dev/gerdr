// What `import … from "@gerdr/core"` gives this panel's page: gerdr's own components and state. This page isn't a frame:
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
  /**
   * sub: show one of its subagents' conversation instead (the toolCallId of ctx.sessions' tasks), read-only, with a way
   * back; "查看对话" on a subagent's card in this chat opens it here too. onSub: the page decides instead — 查看对话 and the
   * way back call it (null = the main conversation), and `sub` is what shows.
   */
  export function SessionChat(props: {
    id: string;
    focused?: boolean;
    jump?: { ref: string; seq: number };
    className?: string;
    sendNow?: boolean;
    sub?: string;
    onSub?: (toolCallId: string | null, title?: string) => void;
  }): ReactNode;

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
   * Every conversation `match` picks (all without one), kept current like useSession: this computer's and the other
   * computers' ("<host>:<id>"). The same array until one it picked changes or the set does, so a page re-renders only
   * for the sessions it cares about. Watch the sessions a view depends on with this instead of polling a function
   * that lists them: e.g. reload a board when one of its workers' `status` / `waiting` changes.
   */
  export function useSessions(
    match?: (s: { id: string; status: string; tags?: Record<string, string>; [k: string]: unknown }) => boolean,
  ): { id: string; title?: string; agent: string; cwd: string; status: string; tags?: Record<string, string>; [k: string]: unknown }[];

  /**
   * How many times this panel's files have changed through its local functions (ctx.fs write / mkdir / move / copy /
   * trash: its own buttons, agents calling its tools, triggers, schedules) since the page loaded; it also moves once
   * after a lost connection comes back. Put it in an effect's dependencies to reload what a function returned, instead
   * of polling: `const v = usePanelChange("user/data"); useEffect(() => void load(), [v])`. `path` is relative to the
   * panel's directory, a file or a folder; none = any. Files an agent edits directly (not through a function) aren't
   * seen: keep a slow fallback poll if that happens. Code outside this panel's page (src/cards.tsx) passes `panel`.
   */
  export function usePanelChange(path?: string, opts?: { panel?: string }): number;

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
    /** The panel whose tool made it (its project key, "lb" / "lb/board"): set by gerdr; your code runs for your panel's cards only. */
    panel?: string;
    choose?: { project: string; fn: string; input?: Record<string, unknown>; field: string; options: string[]; other?: boolean };
    [k: string]: unknown;
  };
  export type CardProps = { card: Card; session: string };

  /**
   * Draw cards of a type yourself, in every conversation (Code too). Call it in src/cards.tsx: gerdr loads that file at
   * start, before this page is ever shown. A card whose component isn't there or throws falls back to the built-in one.
   * The type is this panel's: cards your tools make are drawn by it, another panel's cards of the same type aren't.
   */
  /**
   * Markdown drawn the way the chat draws it: `![](/abs/path.mp4)` plays a local video, `![](/abs/shot.png)` shows an
   * image, a local path opens in Gerdr's viewer, code blocks are highlighted. For text agents wrote (a step, a
   * question, a summary): showing it as plain text loses what the chat shows.
   */
  export function Markdown(props: { children: string }): ReactNode;

  /**
   * The element your overlays (sheets, dialogs, popovers, menus) should be portaled into, e.g. shadcn's
   * `<SheetPrimitive.Portal container={usePortalContainer()}>`. It's inside this panel's own area: gerdr keeps a panel
   * mounted but hidden while another panel or Code is shown, and only what's inside the panel is hidden with it — an
   * overlay portaled to document.body stays on screen over everything else.
   */
  export function usePortalContainer(): HTMLElement | null;

  /**
   * Whether this panel is on screen: it's the panel shown and the window isn't hidden. Gerdr keeps a panel mounted
   * while another panel or Code is shown, and this page lives inside gerdr's own, so `document.visibilityState` stays
   * "visible" all the while the app is open. Poll only while this is true, and refresh when it turns true again.
   */
  export function usePanelVisible(): boolean;

  /** One row of this panel's part of the phone drawer (usePhoneMenu). */
  export interface PhoneMenuItem {
    /** Stable per item (defaults to the label). */
    id?: string;
    label: string;
    /** E.g. a lucide icon element. */
    icon?: ReactNode;
    /** A count or short word at the row's end. */
    badge?: string | number;
    /** The drawer closes, then this runs. */
    onSelect: () => void;
  }

  /**
   * This panel's items in the phone drawer. On a phone the panel's top-left button opens Gerdr's drawer (projects, the
   * Code / panel switch, settings); while this panel is shown, these rows take the place of Code's tab list there. Put
   * what the panel's header can't fit on a phone: its secondary pages, logs, settings. Call it once in the page with
   * the current list; with none (or an empty list) the drawer is as in Code.
   */
  export function usePhoneMenu(items: PhoneMenuItem[]): void;

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
