import type { ReactNode } from "react";
import type { ToolPart } from "@gerdr/chat";

/**
 * The page side of FeedChat (core/FeedChat.tsx): a conversation that isn't a session. Its id starts with FEED_PREFIX;
 * chatHost.tsx's forSession hands the chat this page's transcript, sending and rows instead of a session's. Kept in a
 * registry and read at call time: hostFor computes a session's overrides once per id, the page's props change after.
 */
export const FEED_PREFIX = "feed/";

export type FeedFile = { path: string; name?: string; mime?: string };

export type FeedConfig = {
  /** What to draw, already folded into the chat's messages (FeedChat does that). */
  messages: import("@gerdr/chat").GMessage[];
  /** A row the page draws itself (an entry that isn't the user's), by the part FeedChat made for it. */
  render(part: ToolPart): ReactNode | undefined;
  send(msg: { text: string; files: FeedFile[] }): Promise<void>;
  /** Names the composer's @ offers. */
  mentions: string[];
  placeholder?: string;
  /** Under the composer's text, next to the attach button: given what's typed. */
  hint?(text: string): ReactNode;
};

const feeds = new Map<string, FeedConfig>();
const subs = new Map<string, Set<() => void>>();

export const isFeed = (id: string) => id.startsWith(FEED_PREFIX);
export const feedOf = (id: string) => feeds.get(id);

export function setFeed(id: string, c: FeedConfig | undefined) {
  if (c) feeds.set(id, c);
  else feeds.delete(id);
  subs.get(id)?.forEach((f) => f());
}

export function subscribeFeed(id: string, f: () => void) {
  let s = subs.get(id);
  if (!s) subs.set(id, (s = new Set()));
  s.add(f);
  return () => void s!.delete(f);
}
