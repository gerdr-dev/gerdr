import { useEffect, useLayoutEffect, useMemo, useRef, type ReactNode } from "react";
import { ChatView, type ChatSession, type GMessage, type GPart, type ToolPart } from "@gerdr/chat";
import { TooltipProvider } from "@/components/ui/tooltip";
import { FEED_PREFIX, setFeed, type FeedFile } from "@/lib/feedChat";
import { cn } from "@/lib/utils";

const LOCAL_FILE = "application/x-local-file"; // the chat's chip for a file sent by its path (transcript.ts)
const IMAGE = /\.(png|jpe?g|gif|webp|avif|bmp|svg)$/i;
const VIDEO = /\.(mp4|mov|webm|m4v)$/i;
const rawURL = (p: string) => `/api/fs/raw?path=${encodeURIComponent(p)}&cwd=`;

/** One thing in a FeedChat, oldest first. user: drawn as the user's own message; else the page draws it (render). */
export type FeedEntry = { key: string; at: number; user?: { text: string; files?: FeedFile[] } };

/**
 * A conversation that isn't one session: a page's own list of what happened (a requirement's feed across several
 * agents), drawn by the chat itself — the same scrolling, ↓ to the newest, Enter in an empty box going to the bottom,
 * images opening in the lightbox, and its input box (images, files, @ picking from `mentions`).
 *
 * The user's entries are the chat's user messages; every other entry is drawn by render (one row each, full width).
 * Sending hands the page the text and files (pasted images are saved first, so each is a path); reject to keep the
 * text in the box. jump scrolls to an entry and flashes it (a new seq jumps again). children are drawn over the
 * list (a floating pill). id names it: the scroll position and the draft are kept per id.
 */
export function FeedChat<E extends FeedEntry>({
  id,
  entries,
  render,
  onSend,
  mentions = [],
  hint,
  placeholder,
  jump,
  className,
  children,
}: {
  id: string;
  entries: E[];
  render: (entry: E) => ReactNode;
  onSend: (msg: { text: string; files: FeedFile[] }) => Promise<void>;
  mentions?: string[];
  hint?: (text: string) => ReactNode;
  placeholder?: string;
  jump?: { key: string; seq: number };
  className?: string;
  children?: ReactNode;
}) {
  // No ":" (a session on another computer) and no "/" past the prefix: the id is the chat's, not a path
  const sid = FEED_PREFIX + `${(window as any).__GERDR_PROJECT__ ?? ""}.${id}`.replace(/[:/]/g, "_");
  const messages = useMemo(() => entries.map(toMessage), [entries]);
  const byKey = useMemo(() => new Map(entries.map((e) => [e.key, e])), [entries]);
  const latest = useRef({ byKey, render, onSend, hint });
  latest.current = { byKey, render, onSend, hint };
  useLayoutEffect(() => {
    setFeed(sid, {
      messages,
      mentions,
      render: (p: ToolPart) => {
        const e = latest.current.byKey.get(p.toolCallId);
        return e ? <div data-feed={e.key}>{latest.current.render(e)}</div> : undefined;
      },
      send: (m) => latest.current.onSend(m),
      hint: (text) => latest.current.hint?.(text),
      placeholder,
    });
  }, [sid, messages, mentions.join("\n"), placeholder]);
  useEffect(() => () => setFeed(sid, undefined), [sid]);
  const session = useMemo<ChatSession>(() => ({ id: sid, agent: "", cwd: "", status: "idle", commands: [] }) as ChatSession, [sid]);
  const focus = useMemo(
    () => (jump ? { match: (p: GPart) => p.type === "dynamic-tool" && (p as ToolPart).toolCallId === jump.key, seq: jump.seq } : undefined),
    [jump?.key, jump?.seq],
  );
  return (
    <TooltipProvider delayDuration={400}>
      <div className={cn("h-full min-h-0", className)} style={{ fontSize: "var(--gerdr-font, 14px)" }}>
        <ChatView key={sid} session={session} paneId={`feed:${sid}`} focused={false} focus={focus}>
          {children}
        </ChatView>
      </div>
    </TooltipProvider>
  );
}

function toMessage(e: FeedEntry): GMessage {
  if (e.user) {
    const files = (e.user.files ?? []).map((f) => {
      const kind = f.mime?.startsWith("image/") || IMAGE.test(f.path) ? "image/*" : f.mime?.startsWith("video/") || VIDEO.test(f.path) ? "video/*" : "";
      return kind
        ? { type: "file" as const, mediaType: kind, url: rawURL(f.path) }
        : { type: "file" as const, mediaType: LOCAL_FILE, url: f.path, filename: f.name ?? f.path.split("/").pop() };
    });
    return {
      id: e.key,
      role: "user",
      parts: [...files, ...(e.user.text ? [{ type: "text" as const, text: e.user.text }] : [])],
      metadata: { ts: e.at, messageId: e.key },
    };
  }
  // The page's own row: a tool part the host draws (renderTool), kept out of the chat's folded groups
  const part = { type: "dynamic-tool", toolName: "feed", toolCallId: e.key, state: "output-available", input: {}, output: undefined } as unknown as ToolPart;
  return { id: e.key, role: "assistant", parts: [part], metadata: { ts: e.at } };
}
