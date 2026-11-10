import { createContext, useContext, type ComponentType, type ReactNode, type RefObject } from "react";
import type { DraftImage, DraftStore } from "./drafts";
import { LightboxHost } from "./components/Lightbox";
import { TooltipProvider } from "./ui/tooltip";
import { setMessages, type Locale, type MessageKey } from "./i18n";
import type { GMessage, ToolPart } from "./transcript";
import type { ChatSession, ContentBlock } from "./types";

/**
 * Everything the chat needs from the app around it. The chat never talks to a server itself: data comes in through
 * useTranscript / the session prop, and every action goes out through these callbacks. Optional ones turn their
 * feature off when missing (no "stop task" button without stopTask, no bare-file-name links without fileExists…).
 *
 * gerdr's own implementation is ui/src/lib/chatHost.tsx; a minimal one fed from a chatlog.Session is in the
 * package README.
 */
export interface ChatHost {
  // ---- data ----

  /**
   * The conversation of a session, folded from its event log (buildTranscript / Transcript in transcript.ts do the
   * folding; useEventsTranscript wraps them for a plain HubEvent[]). sub: a sub-agent's read-only view (the
   * toolCallId of its card). Called as a React hook.
   */
  useTranscript(sessionId: string, sub?: string): ChatTranscript;
  /** Display name of an agent id (shown when the conversation is empty). */
  agentName?(agent: string): string;
  /** Another session's title (a message sent by another session shows where it came from). Called as a hook. */
  useSessionTitle?(sessionId: string): string | undefined;

  // ---- sending, stopping, rewinding ----

  /**
   * Send a user message: a new turn when idle, an interjection while one runs (now = cut the current generation
   * short). messageId makes a retry idempotent. onRetry: the host is retrying a flaky network (n = attempt).
   * Reject with an Error to put the text back into the composer and show the message.
   */
  send(sessionId: string, content: ContentBlock[], opts?: { messageId?: string; now?: boolean; onRetry?: (n: number) => void }): Promise<void>;
  /** Stop the running turn. content: what goes back into the composer (withdrawn interjections, or the whole message). */
  stop(sessionId: string): Promise<{ content: ContentBlock[] }>;
  /** Take back the last interjection the agent hasn't read (↑ in an empty composer). */
  withdrawLast?(sessionId: string): Promise<{ content: ContentBlock[] }>;
  /**
   * Take back one queued interjection (its "withdraw" button). The chat no longer passes now (resend it as an urgent
   * one): "Send now" moves the running command to the background instead (backgroundTool), which keeps the order.
   */
  withdraw?(sessionId: string, messageId: string, now?: boolean): Promise<ContentBlock[] | undefined>;
  /** Rewind to before this user message; its content goes back into the composer. No rewind buttons without it. */
  rewind?(sessionId: string, messageId: string): Promise<{ content: ContentBlock[] }>;
  /** Answer a permission request ({optionId} | {cancelled}) or a question ({answers} | {cancelled, reason}). */
  answer(sessionId: string, reqId: string, response: unknown): Promise<void>;
  /**
   * Background tasks: stop one, or move a running tool call to the background. Also what "Send now" on a queued
   * interjection (and Ctrl/⌘+↵ in an empty composer) does: without it those aren't offered.
   */
  stopTask?(sessionId: string, taskId: string): Promise<void>;
  backgroundTool?(sessionId: string, toolCallId: string): Promise<boolean>;
  /**
   * The host's own things going on for a session, shown with its background tasks: in the same chip under the
   * composer and the same list, each group under its own heading (gerdr: what the session waits for — `gerdr wait`).
   * A React hook (it reads the host's state), called while the chip renders. Not the agent's tasks: those are
   * session.tasks. A general slot, so the next kind of thing (a timer, a scheduled send) needs no package change.
   */
  useTaskGroups?(sessionId: string): HostTaskGroup[];
  /** Allow what an auto-mode classifier blocked (claude), then the chat asks the agent to retry. */
  allowRules?(sessionId: string, rules: string[]): Promise<void>;
  /** Mode / config option pickers under the composer. */
  setMode?(sessionId: string, mode: string): Promise<void>;
  setConfig?(sessionId: string, id: string, value: string): Promise<void>;
  /** `!command` runs shown in the conversation (gerdr's composer shell mode): run again / stop one. */
  shell?: { run(sessionId: string, command: string): Promise<unknown>; stop(sessionId: string, runId: string): Promise<unknown> };
  /** Start the agent early (its slash commands only arrive once it runs). */
  warm?(sessionId: string): Promise<void>;

  // ---- files ----

  /** A URL the browser can load for a local path (images, videos, PDFs in the conversation); relative paths are under cwd. */
  fileURL(path: string, cwd?: string): string;
  /** Upload a file the user attached that can't go inline (videos): its path on the agent's machine. */
  upload?(file: File): Promise<{ path: string }>;
  /**
   * Desktop apps where the agent runs on this machine: the path of a file the user picked / pasted / dropped
   * (Electron's webUtils.getPathForFile). With it any file can be attached (a PDF, a 100 MB zip, a folder): it goes to
   * the agent as a resource_link to that path, nothing is read or uploaded. undefined for a file that has no path
   * (a pasted screenshot): images still go inline, other files fall back to upload.
   */
  localPath?(file: File): string | undefined;
  /** Size of a local file, to tell a playable video from a missing one. */
  fileStat?(path: string, cwd?: string): Promise<{ size: number; mtime: number }>;
  /** Which bare file names (README.md) open under cwd; they become links. */
  fileExists?(names: string[], cwd: string): Promise<{ exists: Record<string, boolean> }>;
  /** @-mentions in the composer: files matching q in the session's directory. */
  searchFiles?(sessionId: string, q: string): Promise<string[]>;
  /**
   * Open a local path (a click on a path, a diff header, an image's "show file"). beside: in a pane next to the chat
   * instead of a dialog (⌥-click). The host's right-click menu finds path links by data-gerdr-file / data-gerdr-cwd.
   */
  openPath?(path: string, cwd?: string, opts?: { beside?: boolean }): void;
  /** Open with the system's default app / reveal in the file manager (desktop apps). */
  openNative?(path: string, cwd: string | undefined, reveal: boolean): void;
  /**
   * Per-session overrides, for a host whose sessions don't all live in one place (gerdr: sessions running on other
   * computers, whose paths mean that computer's files). Whatever it returns replaces those members for everything the
   * chat does for that session: the files above, uploads, anything else. Missing = one host for every session.
   * Keyed by session id and computed once per id: the answer must not change for a session.
   */
  forSession?(sessionId: string): Partial<Omit<ChatHost, "forSession">> | undefined;

  // ---- navigation ----

  /** Open a sub-agent's read-only panel. */
  openSubagent?(sessionId: string, toolCallId: string, title: string): void;
  /** Go to another session (the sender of a message). */
  focusSession?(sessionId: string): void;
  /** A link was clicked in the conversation. Return true when handled (the default opens it in a new tab). */
  onLink?(href: string, e: MouseEvent): boolean;
  /** The chat closed an overlay of its own (lightbox): give focus back where the host wants it. */
  refocus?(): void;

  // ---- extension points ----

  /** Replace how a tool call renders (request_user_input, db_query…). undefined = the default card. */
  renderTool?(part: ToolPart, ctx: { sessionId: string; running: boolean }): ReactNode | undefined;
  /** Render fenced code blocks of these languages yourself (```chart). */
  codeBlocks?: Record<string, (code: string) => ReactNode>;
  /** Extra actions on a shell code block in a reply (gerdr: run it in this session / a terminal). */
  codeActions?(code: string, lang: string, session: ChatSession): ReactNode;
  /** Slots around the composer: above it (chips), in its bottom bar (model menu), and an overlay (dialogs). */
  composerTop?(session: ChatSession): ReactNode;
  /**
   * Decorate a turn's metadata line ("18:36 · 用时 59s · 4.1k tokens"): a class and tooltip for the time, and a node
   * drawn right after it. latest: the newest finished turn of the session. A React hook, called while the line
   * renders: it may hold state and timers, so what it shows can change with time (a prompt cache running out).
   */
  useTurnStat?(info: TurnStatInfo): TurnStatDeco | undefined;
  /** ctx.paneId: the composer's key in ChatHost drafts, to write into this composer from the bar. */
  composerBar?(session: ChatSession, ctx: { paneId: string }): ReactNode;
  /** The composer's empty-box text, for a "session" whose box isn't talking to an agent; undefined keeps the default. */
  placeholder?(session: ChatSession, running: boolean): string | undefined;
  composerOverlay?(session: ChatSession): ReactNode;
  /** Composer hooks for host-only features (gerdr: shared drafts, /resume, "session open elsewhere"…). */
  composer?: ComposerHooks;
  /**
   * Draw the "working" indicator yourself: the mark in front of a running turn's status line ("Pondering…"), and
   * every other PixelLoader. Return props.fallback to keep the default. The whole indicator goes to the host, not a
   * colour or a frame, so a host can put any animation there (gerdr hands it to an add-on).
   */
  renderLoader?(props: LoaderProps): ReactNode;
  /**
   * The host's own buttons on a message: beside a user message's bubble (with rewind: on hover, always on a touch
   * screen) and at the end of a finished reply's metadata line. Called while the message renders; what it returns may
   * use hooks (draw a component). Handed over as a whole rather than as one feature, so the next thing a host wants to
   * do with a message (read it aloud, translate it, send it elsewhere) needs no package release. Gerdr: reading aloud.
   */
  messageActions?(msg: MessageInfo): ReactNode;

  // ---- settings ----

  settings?: ChatSettings;
  setPinnedCommands?(names: string[]): void;
}

export type ChatTranscript = {
  /** The conversation (what to draw). */
  all: GMessage[];
  /** A turn is running. */
  running: boolean;
  /** The log has arrived (before that the chat shows "loading"). */
  loaded: boolean;
  /** More history before what's loaded: "more" (scroll up to load) or "loading". */
  older?: "more" | "loading";
  loadOlder?(): void;
};

export type ChatSettings = {
  /** enter = Enter sends (Shift+Enter breaks a line); mod+enter = ⌘/Ctrl+Enter sends. */
  sendKey?: "enter" | "mod+enter";
  showThinking?: boolean;
  /** Virtualize the message list (only render rows near the viewport). */
  virtualList?: boolean;
  /** While following the bottom, stop at the start of a reply text that grows taller than the screen (read it from its start). */
  readFromTop?: boolean;
  /** Expand edit diffs by default. */
  expandEdits?: boolean;
  /** Ask before opening links. */
  confirmLinks?: boolean;
  /** Slash commands pinned above the composer. */
  pinnedCommands?: string[];
};

export type ComposerHooks = {
  /** Where drafts live (default: in memory, see drafts.ts). */
  drafts?: DraftStore;
  /**
   * Called inside every composer (a hook): host features that need the composer's state (gerdr: drafts shared across
   * devices). onSelect is called when the caret moves.
   */
  useExtras?(ctx: ComposerContext): { onSelect?(): void } | void;
  /** Slash commands the host runs itself instead of sending them (gerdr: /resume, /usage). Return true from run to clear the composer. */
  localCommands?(session: ChatSession): { name: string; description: string; run(paneId: string): void }[];
  /** A send failed; return true if the host deals with it (gerdr: "open elsewhere" → its dialog, then retry). */
  onSendError?(session: ChatSession, error: unknown, retry: () => void): boolean;
  /** Ctrl+C twice on an empty, idle composer (gerdr: close this agent). Without it Ctrl+C only clears and stops. */
  onExit?(paneId: string): void;
  /** Defaults for new sessions ("make this the default" in the option menus). Called as a hook. */
  useDefaults?(agent: string): { values: Record<string, string>; set(id: string, value: string): void; setAll(values: Record<string, string>): void };
};

/**
 * A message for ChatHost.messageActions. id: stable for the message (a reply's is per turn: one assistant message can
 * hold several turns). text(): what it says as written (Markdown for a reply; tool calls and thinking left out), worked
 * out when called, since joining a long reply on every render isn't free. blocks(): the same text as the stretches
 * between tool calls and thinking, in order, so the host can take just the answer (the last one) and skip what was
 * said along the way; a user's message is one block. On screen, the elements drawing a message's text carry
 * data-message="<id>" (a reply's: every text part of that turn), so the host can find and mark it (read aloud: the
 * paragraph being read).
 */
/** A group of the host's things in the tasks chip (ChatHost.useTaskGroups). */
export type HostTaskGroup = {
  id: string;
  /** Its heading and its part of the chip's text, for n items ("2 waiting") */
  label(n: number): string;
  icon: ComponentType<{ className?: string }>;
  items: HostTask[];
};

/** One of them: a line, a second line, since when (unix ms), and a way to stop it (none: no button). */
export type HostTask = {
  id: string;
  title: string;
  detail?: string;
  /** The second line is a problem (drawn in the "bad" colour): it can't go on as it is */
  problem?: boolean;
  startedAt: number;
  stop?(): Promise<void>;
  /** The stop button's text (default: the tasks' "stop") */
  stopLabel?: string;
};

export type MessageInfo = { sessionId: string; role: "user" | "assistant"; id: string; text(): string; blocks(): string[] };
export type TurnStatInfo = { sessionId: string; ts?: number; ms: number; tokens: number; latest: boolean };
export type TurnStatDeco = { timeClassName?: string; title?: string; after?: ReactNode };
/**
 * paused: the turn waits on the user (a permission, a question), not working. className: size and colour from where
 * it sits (text-brand while running, text-warn while waiting). fallback: the default PixelLoader.
 */
export type LoaderProps = { paused: boolean; className?: string; fallback: ReactNode };

export type ComposerContext = {
  session: ChatSession;
  paneId: string;
  textarea: RefObject<HTMLTextAreaElement | null>;
  text: string;
  images: DraftImage[];
  setText(text: string): void;
  setImages(f: DraftImage[] | ((cur: DraftImage[]) => DraftImage[])): void;
  /** The latest draft (not the one this render saw). */
  get(): { text: string; images: DraftImage[] };
};

const Ctx = createContext<ChatHost | null>(null);
let current: ChatHost | null = null;

/**
 * Puts a host (and the locale) in place for the chat components below. locale defaults to zh; messages
 * overrides single texts (keys in messages.ts). It also provides the tooltips' context and mounts the image viewer
 * (one per page: render a single ChatProvider near the root, around every ChatView).
 */
export function ChatProvider({
  host,
  locale = "zh",
  messages,
  children,
}: {
  host: ChatHost;
  locale?: Locale;
  messages?: Partial<Record<MessageKey, string>>;
  children: ReactNode;
}) {
  current = host;
  setMessages(locale, messages);
  return (
    <Ctx.Provider value={host}>
      <TooltipProvider delayDuration={400}>
        {children}
        <LightboxHost />
      </TooltipProvider>
    </Ctx.Provider>
  );
}

export function useChatHost(): ChatHost {
  const h = useContext(Ctx) ?? current;
  if (!h) throw new Error("@gerdr/chat: render the chat inside <ChatProvider host={…}>");
  return h;
}

/** The host outside React (the transcript, path checks). The last ChatProvider rendered wins. */
export function chatHost(): ChatHost {
  if (!current) throw new Error("@gerdr/chat: no ChatProvider rendered yet");
  return current;
}

// hostFor's answers, per base host (a new ChatProvider host starts over)
const scoped = new WeakMap<ChatHost, Map<string, ChatHost>>();

/**
 * The host as it applies to one session (ChatHost.forSession merged in). Outside React; inside a ChatView,
 * useChatHost() already gives this one. No id, or no forSession = chatHost().
 */
export function hostFor(sessionId?: string | null, base: ChatHost = chatHost()): ChatHost {
  if (!sessionId || !base.forSession) return base;
  let m = scoped.get(base);
  if (!m) scoped.set(base, (m = new Map()));
  let h = m.get(sessionId);
  if (!h) {
    const o = base.forSession(sessionId);
    h = o ? { ...base, ...o } : base;
    m.set(sessionId, h);
  }
  return h;
}

/** Puts hostFor(sessionId) in place for the components below (ChatView does this for its session). */
export function SessionHost({ sessionId, children }: { sessionId?: string; children: ReactNode }) {
  const base = useContext(Ctx) ?? current;
  if (!base) throw new Error("@gerdr/chat: render the chat inside <ChatProvider host={…}>");
  return <Ctx.Provider value={hostFor(sessionId, base)}>{children}</Ctx.Provider>;
}

/** chatHost() without the throw: for pieces a host may also draw outside the chat, before any ChatProvider (PixelLoader). */
export function peekChatHost(): ChatHost | null {
  return current;
}

export const useChatSettings = (): ChatSettings => useChatHost().settings ?? {};
