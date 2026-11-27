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

  /** A file sent with a FeedChat message, or shown in one of the user's entries. */
  export type FeedFile = { path: string; name?: string; mime?: string };
  /** One thing in a FeedChat, oldest first. user: drawn as the user's own message (text, images, files); else render draws it. */
  export type FeedEntry = { key: string; at: number; user?: { text: string; files?: FeedFile[] } };

  /**
   * A conversation that isn't one session — a page's own list of what happened (a task's feed across several agents) —
   * drawn by the chat itself: the same scrolling, ↓ to the newest, Enter in an empty box going to the bottom, images
   * opening in the lightbox, and its input box (images, files; @ offers `mentions`). Fills its parent's height.
   * The user's entries are the chat's user messages; every other entry is drawn by `render`, one full-width row each
   * (keep `entries` the same array until it changes: a new one redraws the list). onSend gets the text and files
   * (pasted images are saved first, so each is a path); throw to keep the text in the box. hint: a line under the text,
   * given what's typed (who it will go to). jump: scroll to the entry with that key and flash it; bump seq to jump
   * again. children: drawn over the list (a floating pill). id: the scroll position and draft are kept per id.
   * Each entry's row carries `data-feed="<key>"`. For a "working…" last row, render PixelLoader.
   */
  export function FeedChat<E extends FeedEntry>(props: {
    id: string;
    entries: E[];
    render: (entry: E) => ReactNode;
    onSend: (msg: { text: string; files: FeedFile[] }) => Promise<void>;
    mentions?: string[];
    hint?: (text: string) => ReactNode;
    /** The empty input box's text (who it writes to), instead of the chat's own. */
    placeholder?: string;
    jump?: { key: string; seq: number };
    className?: string;
    children?: ReactNode;
  }): ReactNode;

  /** An agent's icon as Gerdr draws it everywhere (`agent`: its id, a session's `agent`), for a page's own rows. */
  export function AgentIcon(props: { agent: string; className?: string }): ReactNode;

  /**
   * A conversation's status dot as Code's rows draw it (pulsing amber running, red waiting / error, green unread).
   * `status`: dotStatus(session), or a session's `status`.
   */
  export function StatusDot(props: { status?: string; unread?: boolean; className?: string }): ReactNode;
  /** The dot's status for a session: its `status`, or "background" while idle with a background task running. */
  export function dotStatus(s?: { status: string; tasks?: { status: string }[] }): string | undefined;

  /** The chat's running indicator (the dots by "working…"). */
  export function PixelLoader(props: { className?: string }): ReactNode;

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
   * Whether the conversation has a turn that ended unseen (the green dot in Code's tabs and list). It clears when the
   * conversation is shown: in Code, or in a SessionChat with `focused` while the window is in front. A board can drop
   * a worker's "its turn is over" dot once it was read, as Code does. Older gerdr versions don't have it: import the
   * module as a namespace and check it's there.
   */
  export function useUnread(id: string | undefined): boolean;

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
   * What a conversation waits on someone for right now (approvals, questions), oldest first: this computer's and the
   * other computers' ("<host>:<id>"), the same list as gerdr's inbox. `inline`: PendingActions can settle it in place
   * (an approval that isn't a plan, a single question); otherwise send the user to the conversation. Older gerdr
   * versions don't have it: on a page that may run there, import the module as a namespace and check it's there.
   */
  export function usePending(id: string | undefined): { reqId: string; kind: "permission" | "ask"; summary: string; at: number; inline: boolean }[];

  /**
   * The inbox's own buttons for a conversation's oldest waiting request (allow once / always / deny…, or a single
   * question's choices), so a page settles it where the user sees it. Draws nothing when nothing inline waits.
   */
  export function PendingActions(props: { id: string; className?: string }): ReactNode;

  /**
   * How many times this panel's files have changed through its local functions (ctx.fs write / mkdir / move / copy /
   * trash: its own buttons, agents calling its tools, triggers, schedules) since the page loaded; it also moves once
   * after a lost connection comes back. Put it in an effect's dependencies to reload what a function returned, instead
   * of polling: `const v = usePanelChange("user/data"); useEffect(() => void load(), [v])`. `path` is relative to the
   * panel's directory, a file or a folder; none = any. Files an agent edits directly (not through a function) aren't
   * seen: keep a slow fallback poll if that happens. Code outside this panel's page (src/cards.tsx) passes `panel`.
   */
  export function usePanelChange(path?: string, opts?: { panel?: string }): number;
  /** Changed paths in this panel. A null path means to reload the snapshot after a reconnect. */
  export function onPanelFiles(panel: string, fn: (path: string | null) => void): () => void;

  /**
   * Show conversations this panel owns your own way. A conversation a local function started (ctx.sessions.start) is
   * this panel's: brought up from anywhere in gerdr (a notification, the inbox, the running list), gerdr switches to
   * this panel and calls `fn` with its id (a board opens that task's drawer with SessionChat). Without a handler gerdr
   * shows it in a drawer of its own over the panel. `key` is this panel's: `project` from ./lib/gerdr. Returns an
   * unregister function (call it when the page unmounts).
   */
  export function onShowSession(key: string, fn: (session: string) => void): () => void;

  /**
   * Open this panel's own inbox items: one it put there without a `session` (ctx.inbox.set), clicked in the inbox,
   * its card or its system notification. gerdr switches to this panel (in its project) and calls `fn` with the id
   * the item was given, so the panel shows that thing (a board opens the to-do's card); without a handler the
   * panel is just shown. `key`: this panel's (`project` from ./lib/gerdr). Returns an unregister function.
   */
  export function onShowInboxItem(key: string, fn: (id: string) => void): () => void;

  /**
   * Open a file on this computer (an absolute or `~/` path) in gerdr's viewer, the way a path clicked in the chat opens:
   * images, videos, PDFs, Markdown, code. Use it for links to local files (what an agent produced): a `file://` link
   * opens nothing from gerdr's page, and nothing at all on a phone. `cwd` resolves a relative path.
   */
  export function openPath(path: string, cwd?: string): Promise<void>;
  /**
   * An artifacts folder (ctx.artifacts.folder's path): its records (links, paths, text, pinned files), then its files
   * as a tree with a preview; Markdown is edited in place, other files previewed. Drop files in to add them. It fills
   * its box (give it a height) and re-reads the folder every few seconds while shown, so a worker's new files appear.
   * A folder that doesn't exist yet shows as empty with its path.
   * The file open in its preview counts as what the user has on screen (useScreenContext; ui.json "screen": true).
   */
  export function ArtifactsFolder(props: { dir: string }): ReactNode;

  /**
   * Say what the user has open on this page (the item a detail page shows: its name, id, folder, which tab), so the
   * agents they talk to know what "this" means. Gerdr puts it in front of the next message the user sends to this
   * panel's conversations (the agent column and conversations this panel started), together with the file open in an
   * ArtifactsFolder (not the text selected on the page), only when it changed since the last message; the composer shows
   * a chip saying what goes along, with × to leave it out. `title` is short, for the person (the chip, a line in the
   * chat); `text` is for the agent, plain lines. The latest mounted caller wins (a detail page over its list); null or
   * unmounting takes it back.
   * Off unless the panel's ui.json says "screen": true (neither this nor the folder's file is sent without
   * it): turn it on for a page people read and talk about ("shorten this one"), not for a board or a dashboard.
   */
  export function useScreenContext(ctx: { title: string; text?: string } | null): void;

  /**
   * A conversation's browser: the page its agent opened (`gerdr browser open`) in the project's own browser, with an
   * address bar and its other pages, clicked and typed into by the user (a sign-in, a captcha) as Code's Browser pane.
   * Gerdr already shows the pages this panel's conversations open in a column at the panel's right; this is for a
   * page that wants one conversation's browser in its own layout too. It fills its box (give it a height).
   */
  export function SessionBrowser(props: { session: string; className?: string }): ReactNode;

  /**
   * The pages a conversation's agent opened, oldest first (none: nothing to show yet), and `opened`, the time it last
   * opened one while gerdr's window was up (changes at each open).
   */
  export function useSessionBrowser(session: string | undefined): { pages: { tab: number; url: string; project?: string }[]; opened?: number };

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
    choose?: { project: string; fn: string; input?: Record<string, unknown>; field: string; options: string[]; other?: boolean; multi?: boolean };
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
   * overlay portaled to document.body stays on screen over everything else. The panel's area is the containing block
   * of `position: fixed` (a transform on it), so a fixed sheet or dialog covers the panel and not the agent / browser
   * columns beside it; a fixed box placed at a pointer's clientX / clientY must subtract the area's own offset (place
   * a `fixed inset-0` layer and read its getBoundingClientRect()).
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

  /**
   * The panel's header in Gerdr's phone top bar: on a phone a panel sits under Gerdr's 44px bar (☰ at the left), and
   * a header of its own below it costs a second strip. Wrap the header (or its phone version) in this: on a phone its
   * children go into that bar right of ☰ (a portal, so they keep the panel's router and state); anywhere else they
   * render where they are. Keep it one row of ~40px: a back arrow, a truncating title, a few icon buttons.
   */
  export function PhoneBar(props: { children: ReactNode }): ReactNode;

  /** One row of this panel's part of the command palette (useSearchItems). */
  export interface SearchItem {
    /** Stable per item (defaults to the label). Searched too, so a short id like "t144" finds its row. */
    id?: string;
    label: string;
    /** A short word at the row's end, kept whole (a status): keep it short, the label gives way to it. Searched too. */
    detail?: string;
    /** detail's theme colour, as the panel colours the same status (default: muted). */
    tone?: "brand" | "ok" | "warn" | "bad";
    /** detail as a filled pill in its tone: a status that wants attention, like a board's 等你 badge. */
    pill?: boolean;
    /** More words to match that aren't shown (a summary, tags). */
    keywords?: string;
    /** E.g. a lucide icon element. */
    icon?: ReactNode;
    /**
     * The whole row drawn by the panel, in place of icon / label / detail: when ⌘K should show the same row as the
     * panel's own list (its icon, number, second line, status dot). label / detail / keywords are still what's
     * matched. Redrawn with every render of the page, so it stays live.
     */
    content?: ReactNode;
    /** The palette closes, then this runs (open the thing in the page). */
    onSelect: () => void;
  }

  /**
   * This panel's rows in Gerdr's command palette (⌘K). While this panel is shown, ⌘K lists them first, under the
   * panel's name (or heading), above conversations and commands, and filters them by what's typed. Anywhere else in
   * the project (Code, another panel) they follow the conversations, under "<panel> · <heading>"; a pick there shows
   * this panel first, then runs onSelect. Only once the panel has been opened since Gerdr started (it stays mounted). Give what someone
   * would look for in the panel (a board's tasks, a site's pages). Call it once in the page with the current list;
   * the list can be long (hundreds of rows), only the matching ones are drawn.
   */
  export function useSearchItems(items: SearchItem[], opts?: { heading?: string }): void;

  export function registerCard(type: string, component: ComponentType<CardProps>): () => void;

  /**
   * Code in this panel's place: while the panel is shown, Gerdr shows Code's panes (tabs, split panes, terminals, the
   * list beside them). Code isn't a special view, it is this with Code's default list; a panel makes its own Code
   * from the same parts:
   * - `list="add"` (default): `sidebar` is drawn above Code's own list; the panes are Code mode's;
   * - `list="replace"`: `sidebar` is the whole list, and the panel has panes of its own: its conversations (tagged
   *   `panel`) open there, Code's stay in Code mode, and each keeps what it had in front. Group them your own way (by
   *   a task's status, by person…) from useCodeRows and draw each with CodeRow, so rows look and behave as in Code.
   *   Code mode's panes (what the person has open there): useCodeRows("code").
   * The rest of the page isn't drawn while this is mounted. Experimental (2026-11).
   */
  export function CodeView(props: { sidebar?: ReactNode; list?: "add" | "replace" }): ReactNode;
  /** A row of Code's list: one pane of the current project. */
  export type CodeRowInfo = {
    /** The pane's id: what CodeRow takes. */
    pane: string;
    kind: "session" | "terminal" | "file" | "browser" | "subagent" | "new";
    /** Its conversation (kind "session"; a subagent's pane: the conversation it belongs to). */
    session?: string;
    cwd?: string;
    /** The workspace its tab is in (Code's own grouping). */
    workspace: { id: string; name: string; cwd?: string };
    /** The pane in front. */
    current: boolean;
  };
  /**
   * With CodeView: the current project's panes as Code lists them. Follow the conversations' state with useSessions.
   * A panel hosting Code has a layout of its own: by default its panes; "code" gives Code mode's (what the person has
   * open there, e.g. to offer importing it). A conversation the panel started (tags.panel) opens in the panel's.
   */
  export function useCodeRows(of?: "here" | "code"): CodeRowInfo[];
  /** With CodeView: Code's own row for a pane (icon, status, unread, its menu, dragging). `where`: shown at its end instead of the folder. */
  /** `below`: a line of yours under it, part of the row (hovered, clicked, right-clicked and dragged with it). */
  export function CodeRow(props: { pane: string; where?: ReactNode; below?: ReactNode }): ReactNode;
  /**
   * With CodeView: Code's default list ("active" and the project's workspaces), the very part Code mode itself puts
   * beside its panes. Under list="replace", `<><Mine /><CodeList /></>` is what list="add" does.
   */
  export function CodeList(): ReactNode;
  /**
   * With CodeView: a place in your list that takes rows dropped on it (a group: a status, a person). Put the returned
   * ref on its element; a CodeRow (or a pane's title bar) dragged over it lights it up as Code's groups do, and
   * dropped, `fn` gets the pane and its conversation. Nothing moves in Code's layout: what it means is yours.
   */
  export function useCodeRowDrop(fn: (row: { pane: string; session?: string }) => void): (el: HTMLElement | null) => void;
  /**
   * With CodeView: your own items for a pane's menus (its ⋯ menu and its row's right-click menu): a task's status
   * and the like. `fn` is asked each time a menu is drawn; return [] for none. On a phone the ⋯ menu is the only
   * place for it: give there whatever your list does by hover, drag or right click.
   */
  export type CodeMenuItem = {
    id: string;
    label: string;
    icon?: ReactNode;
    disabled?: boolean;
    onSelect?: () => void;
    /** A second level, unfolded under the item when it's picked: for a list of choices that would make the menu long. */
    children?: CodeMenuItem[];
  };
  export function useCodeRowMenu(fn: (row: { pane: string; session?: string }) => CodeMenuItem[]): void;
  /**
   * With CodeView: Gerdr's own menu items for one of your conversations that has no pane (its tab was closed): what
   * a CodeRow's menu has besides yours (mark read, open beside, rename). For your own row drawn in its place, so it
   * offers the same menu as a CodeRow; call it when the menu opens.
   */
  export function codeSessionMenu(session: string): CodeMenuItem[];
  /**
   * With CodeView: close the phone's drawer (Code's list over the page), after something in your part of the list
   * opened what shows behind it. Overlays you open over Code (a sheet, a dialog) need `visible pointer-events-auto`
   * on themselves: the panel's page is hidden under Code, and they are part of it.
   */
  export function closeCodeDrawer(): void;
  /**
   * With CodeView: the folders the new-conversation page offers as quick picks while this panel shows, in place of
   * Code's own (the project's workspaces): your own idea of where work goes (the repositories in your settings).
   */
  /** color: a CSS colour for its folder icon (e.g. the colour the person gave a repository). */
  export type CodeDir = { path: string; name?: string; color?: string };
  export function useCodeDirs(dirs: CodeDir[]): void;
  /**
   * With CodeView: what this panel calls a new one. The new-conversation page's title and an empty pane's tab say
   * `noun` instead of "新会话" / "New session" (e.g. "新任务" for a panel whose every conversation is a task). Older
   * Gerdr: absent.
   */
  export function useCodeNoun(noun: string): void;
  /**
   * With CodeView: every conversation started in Code while this panel shows goes through `fn` instead of becoming
   * Code's own: the new-conversation page (Cmd+T, newCodeTab), ⌘D (a copy of the focused one: same agent, folder,
   * mode), the overview's new agent, and a fork (`continueFrom`: the one forked, its chat carried along; `fork` / `forkText`: pass them on to ctx.sessions.start, so a running one can be forked and the same engine copies its session). Start one of
   * yours (e.g. a task's worker via a local function and ctx.sessions.start) in `cwd` with `agent` (and `mode`,
   * `continueFrom` when given), send nothing (the person types the first message), and resolve to its id; Code shows
   * it. Throw to fail (an error toast, nothing opens). Not routed: importing an agent's old conversation, /resume,
   * moving one to another folder or computer, and anything on another computer.
   */
  /**
   * With CodeView: the conversations your list shows, in its order (every row that has a conversation, opened in Code
   * or not; leave out folded-away ones). While you host Code the keys go by it instead of Code's tabs and workspaces:
   * ⌘1–9 / prefix+1–9 the Nth, ⇧⌘] / ⇧⌘[ the next / previous, ⌘E the one shown before, ⌘W lands on the neighbour;
   * the workspace keys do nothing. CodeRow numbers itself while ⌘ is held; for a row you draw yourself, useCodeNumber.
   */
  export function useCodeOrder(sessions: string[]): void;
  /**
   * With CodeView: the conversations a phone's overview pages through while you host Code (its strip of tabs, the
   * swipe between them), in this order. Without it the overview is Code's "active" list, which yours doesn't show.
   * Give the ones worth swiping between (at work, waiting on the person), not your whole list.
   */
  export function useCodeOverview(sessions: string[]): void;
  /**
   * With useCodeOrder: ⌘W / ⇧⌘W where Gerdr closes nothing (a conversation alone on screen; it closes only a split's
   * part and an untouched new conversation). `fn` gets the pane and its conversation, to answer the key your way (ask
   * where a task goes now). Without it the key does nothing there.
   */
  export function useCodeCloseFallback(fn: (row: { pane: string; session?: string }) => void): void;
  /** With useCodeOrder: the number (1–9) to show on this conversation's row while ⌘ is held, else undefined. */
  export function useCodeNumber(session?: string): number | undefined;
  export function useCodeStart(fn: (o: { agent: string; cwd: string; mode?: string; continueFrom?: string; fork?: boolean; forkText?: boolean }) => Promise<string>): void;
  /**
   * With CodeView: a tag after a pane's title, what you know about the conversation in it (a task's number and
   * status); with `items`, a menu on it (move it). Data, drawn by Gerdr. Asked again whenever your component renders.
   */
  export type CodePaneTag = { label: string; icon?: ReactNode; tone?: "warn" | "ok" | "brand"; title?: string; items?: CodeMenuItem[] };
  /** Several tags come out as several chips, each with its own menu (where a task stands; what in it waits on the person). */
  export function useCodePaneTag(fn: (row: { pane: string; session?: string }) => CodePaneTag | CodePaneTag[] | null | undefined): void;
  /** With CodeView: a new tab in Code (the "new conversation" page), in `cwd` when given. */
  export function newCodeTab(opts?: { cwd?: string }): void;
  /**
   * With CodeView: show a conversation as a pane of Code (its pane, else a new tab in the workspace for its directory).
   * Call it from onShowSession too, so this panel's conversations brought up from a notification open as panes.
   * Under list="add", this panel's conversations aren't listed in Code's workspaces while the panel shows: list them in `sidebar`.
   */
  export function showInCodeView(session: string, replace?: string): void;
  /** showInCodeView, then scroll its chat to the card with `ref` (a panel tool's gerdrCard.ref) and flash it. */
  export function jumpInCode(session: string, ref: string): void;
  /** With CodeView: the conversation in Code's focused pane, to mark its row in `sidebar`. */
  export function useCodeSession(): string | undefined;

  /**
   * How a card's question was settled outside the card (on your page, an answer given before), for DefaultCard: drawn
   * answered — `value` picked (several: one per line), a green edge, the choices closed — or, `dropped`, closed and
   * grey. `note`: the status line under the choices; `action`: at its end (e.g. a button to change the answer).
   */
  export type CardDone = { value?: string; dropped?: boolean; note?: ReactNode; action?: ReactNode };

  /**
   * The built-in card (title, body, choices answerable in place with undo), for a card type that only adds to it.
   * `done`: settled outside it. It keeps its height in every state (the "other" box becomes the status line), so give
   * `done` rather than swapping it for a smaller card once answered: a card that shrinks moves the chat being read.
   * `undoMs`: how long a pick waits, undoable, before it's sent (default 3000; a panel setting can give its own).
   * Gerdr before 2026-11-23 ignores it and waits 6s.
   */
  export function DefaultCard(props: { card: Card; done?: CardDone; undoMs?: number }): ReactNode;

  /** gerdr's toasts. */
  export const toast: {
    (message: ReactNode, opts?: Record<string, unknown>): string | number;
    success(message: ReactNode, opts?: Record<string, unknown>): string | number;
    error(message: ReactNode, opts?: Record<string, unknown>): string | number;
  };
}
