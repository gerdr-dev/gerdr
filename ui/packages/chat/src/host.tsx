import { createContext, useContext, type ReactNode, type RefObject } from "react";
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
  /** Take back one queued interjection (its "withdraw" button); now = resend it as an urgent one instead. */
  withdraw?(sessionId: string, messageId: string, now?: boolean): Promise<ContentBlock[] | undefined>;
  /** Rewind to before this user message; its content goes back into the composer. No rewind buttons without it. */
  rewind?(sessionId: string, messageId: string): Promise<{ content: ContentBlock[] }>;
  /** Answer a permission request ({optionId} | {cancelled}) or a question ({answers} | {cancelled, reason}). */
  answer(sessionId: string, reqId: string, response: unknown): Promise<void>;
  /** Background tasks: stop one, or move a running tool call to the background. */
  stopTask?(sessionId: string, taskId: string): Promise<void>;
  backgroundTool?(sessionId: string, toolCallId: string): Promise<boolean>;
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
  /** Size of a local file, to tell a playable video from a missing one. */
  fileStat?(path: string, cwd?: string): Promise<{ size: number; mtime: number }>;
  /** Which bare file names (README.md) open under cwd; they become links. */
  fileExists?(names: string[], cwd: string): Promise<{ exists: Record<string, boolean> }>;
  /** @-mentions in the composer: files matching q in the session's directory. */
  searchFiles?(sessionId: string, q: string): Promise<string[]>;
  /** Open a local path (a click on a path, a diff header, an image's "show file"). */
  openPath?(path: string, cwd?: string): void;
  /** Open with the system's default app / reveal in the file manager (desktop apps). */
  openNative?(path: string, cwd: string | undefined, reveal: boolean): void;

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
  composerOverlay?(session: ChatSession): ReactNode;
  /** Composer hooks for host-only features (gerdr: shared drafts, /resume, "session open elsewhere"…). */
  composer?: ComposerHooks;

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

export type TurnStatInfo = { sessionId: string; ts?: number; ms: number; tokens: number; latest: boolean };
export type TurnStatDeco = { timeClassName?: string; title?: string; after?: ReactNode };

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

export const useChatSettings = (): ChatSettings => useChatHost().settings ?? {};
