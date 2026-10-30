import { useRef } from "react";
import type { ChatTranscript } from "./host";
import { Transcript } from "./transcript";
import type { HubEvent } from "./types";

/**
 * A ChatTranscript from a plain event log (what chatlog.Session's OnEvents delivers), for ChatHost.useTranscript.
 * Keep `events` as an array that only grows while events arrive (append new ones; replace the whole array when the
 * server sends a rewrite): appended events are folded in incrementally, anything else rebuilds the conversation.
 * sub: a sub-agent's read-only view (the toolCallId of its card).
 */
export function useEventsTranscript(events: HubEvent[] | undefined, sub?: string): ChatTranscript {
  const st = useRef<{ t: Transcript; seen: HubEvent[]; n: number; sub?: string } | null>(null);
  let s = st.current;
  if (!s || s.sub !== sub) s = st.current = { t: new Transcript(sub), seen: [], n: 0, sub };
  const evs = events ?? [];
  // Only grew: the events folded so far are still the start of the list (same first and last seq)
  const appended = s.n === 0 || (evs.length >= s.n && evs[0]?.seq === s.seen[0]?.seq && evs[s.n - 1]?.seq === s.seen[s.n - 1]?.seq);
  if (evs !== s.seen) {
    if (appended) for (let i = s.n; i < evs.length; i++) s.t.apply(evs[i]);
    else s.t.reset(evs);
    s.seen = evs;
    s.n = evs.length;
  }
  return { all: s.t.all, running: s.t.running, loaded: events !== undefined };
}
